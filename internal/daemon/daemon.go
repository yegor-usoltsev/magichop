package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"github.com/yegor-usoltsev/magichop/internal/bluetooth"
	"github.com/yegor-usoltsev/magichop/internal/config"
	"github.com/yegor-usoltsev/magichop/internal/protocol"
	"github.com/yegor-usoltsev/magichop/internal/state"
)

type Options struct {
	ConfigPath string
}

type Service struct {
	cfg    config.Config
	store  eventStore
	runner bluetooth.Runner

	mu     sync.Mutex
	claims map[string]claimReplay
	locks  map[string]chan struct{}
}

type eventStore interface {
	Append(state.Event) error
}

type claimReplay struct {
	accepted protocol.Accepted
	final    *protocol.FinalResult
}

func SocketPath() string {
	base := os.Getenv("TMPDIR")
	if base == "" {
		base = os.TempDir()
	}
	return filepath.Join(base, "magichop-"+strconv.Itoa(os.Getuid()), "daemon.sock")
}

func Run(ctx context.Context, opts Options) error {
	cfg, _, err := config.Load(config.LoadOptions{Path: opts.ConfigPath})
	if err != nil {
		return err
	}
	store, err := state.Open(state.DefaultPath())
	if err != nil {
		return err
	}
	defer store.Close()
	svc := NewService(cfg, store, bluetooth.ExecRunner{})
	dir := filepath.Dir(SocketPath())
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	_ = os.Remove(SocketPath())
	ln, err := net.Listen("unix", SocketPath())
	if err != nil {
		return err
	}
	defer ln.Close()
	go func() {
		<-ctx.Done()
		_ = ln.Close()
	}()
	for {
		conn, err := ln.Accept()
		if err != nil {
			if errors.Is(ctx.Err(), context.Canceled) {
				return nil
			}
			return err
		}
		go svc.handleConn(conn)
	}
}

func NewService(cfg config.Config, store eventStore, runner bluetooth.Runner) *Service {
	if runner == nil {
		runner = bluetooth.ExecRunner{}
	}
	return &Service{
		cfg:    cfg,
		store:  store,
		runner: runner,
		claims: map[string]claimReplay{},
		locks:  map[string]chan struct{}{},
	}
}

func (s *Service) handleConn(conn net.Conn) {
	defer conn.Close()
	var req protocol.LocalRequest
	if err := json.NewDecoder(conn).Decode(&req); err != nil {
		return
	}
	enc := json.NewEncoder(conn)
	for _, reply := range s.Handle(context.Background(), req) {
		_ = enc.Encode(reply)
	}
}

func (s *Service) Handle(ctx context.Context, req protocol.LocalRequest) []any {
	switch req.Type {
	case protocol.LocalDevices:
		return []any{protocol.DevicesResult{Type: protocol.LocalDevicesResult, OK: true, Devices: s.cfg.Devices}}
	case protocol.LocalStatus:
		device, address, _ := config.ResolveDevice(s.cfg, req.Device)
		return []any{protocol.StatusResult{Type: protocol.LocalStatusResult, OK: true, Daemon: "running", Coordinator: "disconnected", Device: displayDevice(device, address), Recent: s.recentFinals()}}
	case protocol.LocalDoctor:
		return []any{protocol.DoctorResult{Type: protocol.LocalDoctorResult, OK: true, Checks: []protocol.DoctorCheck{{Name: "config", OK: true}}}}
	case protocol.LocalRelease:
		return []any{s.handleRelease(ctx, req)}
	case protocol.LocalClaim:
		return s.handleClaim(ctx, req)
	default:
		return []any{map[string]any{"type": "error", "ok": false, "error": fmt.Sprintf("%s: unknown request", protocol.ErrInvalidRequest)}}
	}
}

func (s *Service) handleClaim(ctx context.Context, req protocol.LocalRequest) []any {
	if req.ClientRequestID == "" {
		return []any{protocol.FinalResult{Type: protocol.LocalFinalResult, OK: false, Error: protocol.ErrInvalidRequest}}
	}
	if replay, ok := s.replay(req.ClientRequestID); ok {
		if replay.final != nil {
			return []any{replay.accepted, *replay.final}
		}
		return []any{replay.accepted}
	}
	device, address, err := config.ResolveDevice(s.cfg, req.Device)
	if err != nil {
		return []any{protocol.FinalResult{Type: protocol.LocalFinalResult, OK: false, Error: protocol.ErrInvalidRequest}}
	}
	if !s.tryLock(address) {
		return []any{protocol.FinalResult{Type: protocol.LocalFinalResult, OK: false, Error: protocol.ErrBusy, Device: displayDevice(device, address), Address: address}}
	}
	defer s.unlock(address)

	requestID, err := protocol.NewRequestID()
	if err != nil {
		return []any{protocol.FinalResult{Type: protocol.LocalFinalResult, OK: false, Error: protocol.ErrInvalidRequest}}
	}
	accepted := protocol.Accepted{Type: protocol.LocalAccepted, ClientRequestID: req.ClientRequestID, RequestID: requestID}
	if err := s.append(state.Event{Event: state.EventAccepted, RequestID: requestID, ClientRequestID: req.ClientRequestID, Device: displayDevice(device, address), Address: address}); err != nil {
		return []any{protocol.FinalResult{Type: protocol.LocalFinalResult, RequestID: requestID, OK: false, Error: protocol.ErrLogUnavailable, Device: displayDevice(device, address), Address: address}}
	}
	s.rememberAccepted(req.ClientRequestID, accepted)

	start := time.Now()
	final := protocol.FinalResult{Type: protocol.LocalFinalResult, RequestID: requestID, OK: false, Error: protocol.ErrCoordinatorUnavailable, DurationMS: time.Since(start).Milliseconds(), Device: displayDevice(device, address), Address: address}
	if err := s.append(state.Event{Event: state.EventFinalResult, RequestID: requestID, ClientRequestID: req.ClientRequestID, OK: final.OK, Error: final.Error, DurationMS: final.DurationMS}); err != nil {
		final.Error = protocol.ErrLogUnavailable
	}
	s.rememberFinal(req.ClientRequestID, final)
	return []any{accepted, final}
}

func (s *Service) handleRelease(ctx context.Context, req protocol.LocalRequest) protocol.ReleaseResult {
	start := time.Now()
	device, address, err := config.ResolveDevice(s.cfg, req.Device)
	if err != nil {
		return protocol.ReleaseResult{Type: protocol.LocalReleaseResult, OK: false, Error: protocol.ErrInvalidRequest}
	}
	if !s.tryLock(address) {
		return protocol.ReleaseResult{Type: protocol.LocalReleaseResult, OK: false, Error: protocol.ErrBusy, Device: displayDevice(device, address), Address: address}
	}
	defer s.unlock(address)

	err = bluetooth.Release(ctx, s.runner, address, s.cfg.Timeouts.ReleaseWindow)
	result := protocol.ReleaseResult{Type: protocol.LocalReleaseResult, OK: err == nil, Device: displayDevice(device, address), Address: address, DurationMS: time.Since(start).Milliseconds()}
	if err != nil {
		result.Error = bluetooth.ErrorCode(err)
	}
	if appendErr := s.append(state.Event{Event: state.EventLocalRelease, Device: result.Device, Address: address, OK: result.OK, Error: result.Error, DurationMS: result.DurationMS}); appendErr != nil {
		result.OK = false
		result.Error = protocol.ErrLogUnavailable
	}
	return result
}

func (s *Service) replay(clientRequestID string) (claimReplay, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	replay, ok := s.claims[clientRequestID]
	return replay, ok
}

func (s *Service) rememberAccepted(clientRequestID string, accepted protocol.Accepted) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.claims[clientRequestID] = claimReplay{accepted: accepted}
}

func (s *Service) rememberFinal(clientRequestID string, final protocol.FinalResult) {
	s.mu.Lock()
	defer s.mu.Unlock()
	replay := s.claims[clientRequestID]
	replay.final = &final
	s.claims[clientRequestID] = replay
}

func (s *Service) recentFinals() []protocol.FinalResult {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []protocol.FinalResult
	for _, replay := range s.claims {
		if replay.final != nil {
			out = append(out, *replay.final)
		}
	}
	return out
}

func (s *Service) append(event state.Event) error {
	if s.store == nil {
		return fmt.Errorf(protocol.ErrLogUnavailable)
	}
	return s.store.Append(event)
}

func (s *Service) tryLock(address string) bool {
	s.mu.Lock()
	lock, ok := s.locks[address]
	if !ok {
		lock = make(chan struct{}, 1)
		lock <- struct{}{}
		s.locks[address] = lock
	}
	s.mu.Unlock()
	select {
	case <-lock:
		return true
	default:
		return false
	}
}

func (s *Service) unlock(address string) {
	s.mu.Lock()
	lock := s.locks[address]
	s.mu.Unlock()
	if lock != nil {
		lock <- struct{}{}
	}
}

func displayDevice(alias, address string) string {
	if alias != "" {
		return alias
	}
	return address
}
