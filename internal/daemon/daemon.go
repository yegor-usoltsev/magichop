package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"github.com/nats-io/nats.go"
	"gopkg.in/natefinch/lumberjack.v2"

	"github.com/yegor-usoltsev/magichop/internal/bluetooth"
	"github.com/yegor-usoltsev/magichop/internal/config"
	"github.com/yegor-usoltsev/magichop/internal/coordinator"
	"github.com/yegor-usoltsev/magichop/internal/protocol"
	"github.com/yegor-usoltsev/magichop/internal/state"
)

type Options struct {
	ConfigPath string
}

const LostReplyRetryInterval = 500 * time.Millisecond
const coordinatorRetryInterval = 2 * time.Second

type Service struct {
	cfg    config.Config
	store  eventStore
	runner bluetooth.Runner
	logger *slog.Logger

	coordMu sync.RWMutex
	coord   coordinatorClient

	mu     sync.Mutex
	claims map[string]claimReplay
	locks  map[string]chan struct{}
	peers  map[string]peerReleaseReplay
	guards map[string]time.Time
	recent []protocol.FinalResult
}

type eventStore interface {
	Append(state.Event) error
}

type claimReplay struct {
	accepted protocol.Accepted
	final    *protocol.FinalResult
}

type peerReleaseReplay struct {
	requester string
	device    string
	reply     protocol.ReleaseReply
	expires   time.Time
}

type coordinatorClient interface {
	Claim(context.Context, protocol.Claim) (protocol.ClaimResult, error)
	Close()
}

func SocketPath() string {
	base := os.Getenv("TMPDIR")
	if base == "" {
		base = os.TempDir()
	}
	return filepath.Join(base, "magichop-"+strconv.Itoa(os.Getuid()), "daemon.sock")
}

func Run(ctx context.Context, opts Options) error {
	fmt.Fprintf(os.Stderr, "magichop daemon starting config=%s socket=%s\n", opts.ConfigPath, SocketPath())
	cfg, _, err := config.Load(config.LoadOptions{Path: opts.ConfigPath})
	if err != nil {
		return err
	}
	if err := state.TerminalizeOpen(state.DefaultPath(), protocol.ErrDaemonRestarted); err != nil {
		return err
	}
	store, err := state.Open(state.DefaultPath())
	if err != nil {
		return err
	}
	defer store.Close()
	logger, err := openLogger()
	if err != nil {
		return err
	}
	logger.Info("daemon starting", "node", cfg.NodeName, "coordinator", cfg.CoordinatorURL, "socket", SocketPath())
	svc := NewService(cfg, store, bluetooth.ExecRunner{Logger: logger})
	svc.logger = logger
	svc.seedRecent(state.DefaultPath())
	dir := filepath.Dir(SocketPath())
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	lockFile, err := acquireDaemonLock(dir)
	if err != nil {
		return err
	}
	defer releaseDaemonLock(lockFile)
	_ = os.Remove(SocketPath())
	ln, err := net.Listen("unix", SocketPath())
	if err != nil {
		return err
	}
	defer ln.Close()
	logger.Info("daemon ready", "node", cfg.NodeName, "socket", SocketPath())
	fmt.Fprintf(os.Stderr, "magichop daemon ready node=%s socket=%s\n", cfg.NodeName, SocketPath())
	go svc.connectCoordinatorLoop(ctx)
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

func (s *Service) connectCoordinatorLoop(ctx context.Context) {
	for {
		coord, err := connectCoordinator(ctx, s.cfg, s.handlePeerRelease)
		if err == nil {
			s.setCoordinator(coord)
			s.log("coordinator connected", "url", s.cfg.CoordinatorURL)
			fmt.Fprintf(os.Stderr, "magichop daemon connected coordinator=%s\n", s.cfg.CoordinatorURL)
			<-ctx.Done()
			coord.Close()
			return
		}
		s.log("coordinator connection failed", "url", s.cfg.CoordinatorURL, "err", err)
		fmt.Fprintf(os.Stderr, "magichop daemon coordinator connect failed url=%s err=%v\n", s.cfg.CoordinatorURL, err)
		select {
		case <-ctx.Done():
			return
		case <-time.After(coordinatorRetryInterval):
		}
	}
}

func (s *Service) setCoordinator(coord coordinatorClient) {
	s.coordMu.Lock()
	defer s.coordMu.Unlock()
	s.coord = coord
}

func (s *Service) getCoordinator() coordinatorClient {
	s.coordMu.RLock()
	defer s.coordMu.RUnlock()
	return s.coord
}

func acquireDaemonLock(dir string) (*os.File, error) {
	path := filepath.Join(dir, "daemon.lock")
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := lockFile(file); err != nil {
		_ = file.Close()
		return nil, err
	}
	return file, nil
}

func releaseDaemonLock(file *os.File) {
	if file == nil {
		return
	}
	_ = unlockFile(file)
	_ = file.Close()
}

func LogPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join("Library", "Logs", "MagicHop", "daemon.jsonl")
	}
	return filepath.Join(home, "Library", "Logs", "MagicHop", "daemon.jsonl")
}

func openLogger() (*slog.Logger, error) {
	path := LogPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	writer := &lumberjack.Logger{Filename: path, MaxSize: 10, MaxBackups: 3, MaxAge: 30, Compress: true}
	return slog.New(slog.NewTextHandler(writer, &slog.HandlerOptions{})), nil
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
		peers:  map[string]peerReleaseReplay{},
		guards: map[string]time.Time{},
	}
}

type natsCoordinatorClient struct {
	nc             *nats.Conn
	cfg            config.Config
	stop           chan struct{}
	done           chan struct{}
	closed         sync.Once
	releaseHandler func(context.Context, protocol.Release) protocol.ReleaseReply
}

func connectCoordinator(ctx context.Context, cfg config.Config, releaseHandler func(context.Context, protocol.Release) protocol.ReleaseReply) (*natsCoordinatorClient, error) {
	nc, err := nats.Connect(cfg.CoordinatorURL)
	if err != nil {
		return nil, err
	}
	client := &natsCoordinatorClient{nc: nc, cfg: cfg, stop: make(chan struct{}), done: make(chan struct{}), releaseHandler: releaseHandler}
	if err := client.register(ctx); err != nil {
		nc.Close()
		return nil, err
	}
	if err := client.subscribeRelease(); err != nil {
		nc.Close()
		return nil, err
	}
	go client.heartbeatLoop()
	return client, nil
}

func (c *natsCoordinatorClient) register(ctx context.Context) error {
	req := protocol.Register{Protocol: protocol.Version, Type: protocol.TypeRegister, Node: c.cfg.NodeName, AuthToken: c.cfg.AuthToken, Devices: c.cfg.Devices}
	data, err := json.Marshal(req)
	if err != nil {
		return err
	}
	msg, err := c.nc.RequestWithContext(ctx, coordinator.SubjectRegister, data)
	if err != nil {
		return err
	}
	var res protocol.RegisterResult
	if err := json.Unmarshal(msg.Data, &res); err != nil {
		return err
	}
	if res.Status != "ok" {
		return fmt.Errorf("%s: %s", res.Status, res.Reason)
	}
	return nil
}

func (c *natsCoordinatorClient) subscribeRelease() error {
	_, err := c.nc.Subscribe(coordinator.SubjectRelease(c.cfg.NodeName), func(msg *nats.Msg) {
		var req protocol.Release
		if err := json.Unmarshal(msg.Data, &req); err != nil {
			respondNATSJSON(msg, protocol.ReleaseReply{Protocol: protocol.Version, Type: protocol.TypeReleaseReply, Status: "fail", Reason: protocol.ErrInvalidRequest})
			return
		}
		if c.releaseHandler == nil {
			respondNATSJSON(msg, protocol.ReleaseReply{Protocol: protocol.Version, Type: protocol.TypeReleaseReply, RequestID: req.RequestID, Status: "fail", Reason: protocol.ErrReleaseFailed})
			return
		}
		respondNATSJSON(msg, c.releaseHandler(context.Background(), req))
	})
	if err != nil {
		return err
	}
	c.nc.Flush()
	return c.nc.LastError()
}

func respondNATSJSON(msg *nats.Msg, v any) {
	data, err := json.Marshal(v)
	if err != nil {
		return
	}
	_ = msg.Respond(data)
}

func (c *natsCoordinatorClient) heartbeatLoop() {
	defer close(c.done)
	ticker := time.NewTicker(coordinator.HeartbeatInterval)
	defer ticker.Stop()
	c.publishHeartbeat()
	for {
		select {
		case <-c.stop:
			return
		case <-ticker.C:
			c.publishHeartbeat()
		}
	}
}

func (c *natsCoordinatorClient) publishHeartbeat() {
	req := protocol.Heartbeat{Protocol: protocol.Version, Type: protocol.TypeHeartbeat, Node: c.cfg.NodeName, AuthToken: c.cfg.AuthToken}
	data, err := json.Marshal(req)
	if err == nil {
		_ = c.nc.Publish(coordinator.SubjectHeartbeat(c.cfg.NodeName), data)
	}
}

func (c *natsCoordinatorClient) Claim(ctx context.Context, req protocol.Claim) (protocol.ClaimResult, error) {
	data, err := json.Marshal(req)
	if err != nil {
		return protocol.ClaimResult{}, err
	}
	msg, err := c.nc.RequestWithContext(ctx, coordinator.SubjectClaim(req.Device), data)
	if err != nil {
		return protocol.ClaimResult{}, err
	}
	var res protocol.ClaimResult
	if err := json.Unmarshal(msg.Data, &res); err != nil {
		return protocol.ClaimResult{}, err
	}
	return res, nil
}

func (c *natsCoordinatorClient) Close() {
	c.closed.Do(func() {
		close(c.stop)
		<-c.done
		c.nc.Close()
	})
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
		if req.Scan {
			devices, err := bluetooth.ScanPaired(ctx, s.runner, 5*time.Second)
			if err != nil {
				return []any{protocol.DevicesResult{Type: protocol.LocalDevicesResult, OK: false, Error: bluetooth.ErrorCode(err)}}
			}
			return []any{protocol.DevicesResult{Type: protocol.LocalDevicesResult, OK: true, Devices: devices}}
		}
		return []any{protocol.DevicesResult{Type: protocol.LocalDevicesResult, OK: true, Devices: s.cfg.Devices}}
	case protocol.LocalStatus:
		device, address, _ := config.ResolveDevice(s.cfg, req.Device)
		coordinatorStatus := "disconnected"
		if s.getCoordinator() != nil {
			coordinatorStatus = "connected"
		}
		return []any{protocol.StatusResult{Type: protocol.LocalStatusResult, OK: true, Daemon: "running", Coordinator: coordinatorStatus, Device: displayDevice(device, address), Recent: s.recentFinals()}}
	case protocol.LocalDoctor:
		return []any{s.handleDoctor()}
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
	if err := s.preflightBluetooth(); err != nil {
		return []any{protocol.FinalResult{Type: protocol.LocalFinalResult, OK: false, Error: protocol.ErrBlueutilMissing, Device: displayDevice(device, address), Address: address}}
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
	s.log("claim accepted", "request_id", requestID, "device", displayDevice(device, address), "address", address)
	s.rememberAccepted(req.ClientRequestID, accepted)

	final := s.runClaim(ctx, requestID, displayDevice(device, address), address, req.TimeoutMS)
	if err := s.append(state.Event{Event: state.EventFinalResult, RequestID: requestID, ClientRequestID: req.ClientRequestID, OK: final.OK, Error: final.Error, DurationMS: final.DurationMS}); err != nil {
		final.OK = false
		final.Error = protocol.ErrLogUnavailable
		final.AcquireSeen = final.AcquireSeen || final.Connected
	}
	s.log("claim finished", "request_id", requestID, "device", final.Device, "address", final.Address, "ok", final.OK, "error", final.Error, "duration_ms", final.DurationMS)
	s.rememberFinal(req.ClientRequestID, final)
	return []any{accepted, final}
}

func (s *Service) runClaim(ctx context.Context, requestID, device, address string, timeoutMS int64) protocol.FinalResult {
	start := time.Now()
	if timeoutMS <= 0 {
		timeoutMS = s.cfg.Timeouts.WholeClaim.Milliseconds()
	}
	deadline := start.Add(time.Duration(timeoutMS) * time.Millisecond)
	result := protocol.FinalResult{Type: protocol.LocalFinalResult, RequestID: requestID, Device: device, Address: address}
	connected, err := s.localConnected(ctx, address)
	if err != nil {
		result.Error = bluetooth.ErrVerifyFailed
		result.DurationMS = time.Since(start).Milliseconds()
		return result
	}
	if connected {
		result.DurationMS = time.Since(start).Milliseconds()
		result.OK = true
		result.Connected = true
		result.AcquireSeen = true
		if err := s.persistGuard(address, deadline); err != nil {
			result.OK = false
			result.Error = protocol.ErrLogUnavailable
		}
		return result
	}
	coord := s.getCoordinator()
	if coord == nil {
		result.Error = protocol.ErrCoordinatorUnavailable
		result.DurationMS = time.Since(start).Milliseconds()
		return result
	}
	claimCtx, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()
	claimResult, err := s.claimWithRetry(claimCtx, coord, requestID, address, deadline)
	if err != nil {
		result.Error = protocol.ErrCoordinatorUnavailable
		result.DurationMS = time.Since(start).Milliseconds()
		return result
	}
	if claimResult.Status != "proceed" {
		result.Error = claimResult.Status
		result.DurationMS = time.Since(start).Milliseconds()
		return result
	}
	if claimResult.Reason == coordinator.ReasonNoPeer {
		if connected, err := s.localConnected(ctx, address); err == nil && connected {
			result.DurationMS = time.Since(start).Milliseconds()
			result.OK = true
			result.Connected = true
			result.AcquireSeen = true
			return result
		}
	}
	if err := sleepClipped(claimCtx, time.Duration(claimResult.ReleaseWaitMS)*time.Millisecond); err != nil {
		result.Error = protocol.ErrClaimTimeout
		result.DurationMS = time.Since(start).Milliseconds()
		return result
	}
	err = bluetooth.Acquire(claimCtx, s.runner, address, time.Until(deadline))
	result.DurationMS = time.Since(start).Milliseconds()
	if err != nil {
		result.Error = bluetooth.ErrorCode(err)
		return result
	}
	if err := s.persistGuard(address, deadline); err != nil {
		result.Error = protocol.ErrLogUnavailable
		result.AcquireSeen = true
		return result
	}
	result.OK = true
	result.Connected = true
	result.AcquireSeen = true
	return result
}

func (s *Service) localConnected(ctx context.Context, address string) (bool, error) {
	res, err := s.runner.Run(ctx, 300*time.Millisecond, "--is-connected", address)
	if err != nil {
		return false, err
	}
	return bluetooth.ParseConnected(res)
}

func (s *Service) claimWithRetry(ctx context.Context, coord coordinatorClient, requestID, address string, deadline time.Time) (protocol.ClaimResult, error) {
	for {
		if !time.Now().Before(deadline) {
			return protocol.ClaimResult{}, context.DeadlineExceeded
		}
		attemptTimeout := LostReplyRetryInterval
		if remaining := time.Until(deadline); remaining < attemptTimeout {
			attemptTimeout = remaining
		}
		attemptCtx, cancel := context.WithTimeout(ctx, attemptTimeout)
		claim := protocol.Claim{Protocol: protocol.Version, Type: protocol.TypeClaim, RequestID: requestID, Requester: s.cfg.NodeName, Device: address, RemainingMS: time.Until(deadline).Milliseconds()}
		res, err := coord.Claim(attemptCtx, claim)
		cancel()
		if err == nil {
			return res, nil
		}
		if ctx.Err() != nil {
			return protocol.ClaimResult{}, ctx.Err()
		}
	}
}

func sleepClipped(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return nil
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func (s *Service) persistGuard(address string, until time.Time) error {
	if err := s.append(state.Event{Event: state.EventGuard, Address: address, Until: until}); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.guards[address] = until
	return nil
}

func (s *Service) guardActive(address string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	until, ok := s.guards[address]
	if !ok {
		return false
	}
	if !time.Now().Before(until) {
		delete(s.guards, address)
		return false
	}
	return true
}

func (s *Service) preflightBluetooth() error {
	if runner, ok := s.runner.(bluetooth.ExecRunner); ok {
		return bluetooth.Preflight(runner.Path)
	}
	return nil
}

func (s *Service) handleRelease(ctx context.Context, req protocol.LocalRequest) protocol.ReleaseResult {
	start := time.Now()
	device, address, err := config.ResolveDevice(s.cfg, req.Device)
	if err != nil {
		return protocol.ReleaseResult{Type: protocol.LocalReleaseResult, OK: false, Error: protocol.ErrInvalidRequest}
	}
	if s.store == nil {
		return protocol.ReleaseResult{Type: protocol.LocalReleaseResult, OK: false, Error: protocol.ErrLogUnavailable, Device: displayDevice(device, address), Address: address}
	}
	if err := s.preflightBluetooth(); err != nil {
		return protocol.ReleaseResult{Type: protocol.LocalReleaseResult, OK: false, Error: protocol.ErrBlueutilMissing, Device: displayDevice(device, address), Address: address}
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

func (s *Service) handleDoctor() protocol.DoctorResult {
	checks := []protocol.DoctorCheck{
		{Name: "config", OK: s.cfg.CoordinatorURL != "" && s.cfg.AuthToken != ""},
		{Name: "blueutil", OK: s.preflightBluetooth() == nil},
		{Name: "daemon_socket", OK: true},
		{Name: "coordinator", OK: s.getCoordinator() != nil},
		{Name: "state", OK: s.store != nil},
		{Name: "log", OK: pathWritable(LogPath())},
	}
	ok := true
	for i := range checks {
		if !checks[i].OK {
			ok = false
			if checks[i].Err == "" {
				checks[i].Err = "failed"
			}
		}
	}
	return protocol.DoctorResult{Type: protocol.LocalDoctorResult, OK: ok, Checks: checks}
}

func pathWritable(path string) bool {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return false
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return false
	}
	return f.Close() == nil
}

func (s *Service) handlePeerRelease(ctx context.Context, req protocol.Release) protocol.ReleaseReply {
	if req.Protocol != protocol.Version || req.Type != protocol.TypeRelease || !protocol.ValidRequestID(req.RequestID) || req.Requester == "" || req.Device == "" {
		return protocol.ReleaseReply{Protocol: protocol.Version, Type: protocol.TypeReleaseReply, RequestID: req.RequestID, Status: "fail", Reason: protocol.ErrInvalidRequest}
	}
	if replay, ok := s.peerReplay(req); ok {
		return replay
	}
	if !s.hasDevice(req.Device) || req.Requester == s.cfg.NodeName {
		return protocol.ReleaseReply{Protocol: protocol.Version, Type: protocol.TypeReleaseReply, RequestID: req.RequestID, Status: "fail", Reason: protocol.ErrStaleRelease}
	}
	if s.guardActive(req.Device) {
		return protocol.ReleaseReply{Protocol: protocol.Version, Type: protocol.TypeReleaseReply, RequestID: req.RequestID, Status: "busy", Reason: protocol.ErrPeerBusy}
	}
	if !s.tryLock(req.Device) {
		reply := protocol.ReleaseReply{Protocol: protocol.Version, Type: protocol.TypeReleaseReply, RequestID: req.RequestID, Status: "busy", Reason: protocol.ErrPeerBusy}
		s.rememberPeer(req, reply)
		return reply
	}

	checkCtx, cancel := context.WithTimeout(ctx, 300*time.Millisecond)
	connectedRes, err := s.runner.Run(checkCtx, 300*time.Millisecond, "--is-connected", req.Device)
	checkTimedOut := checkCtx.Err() != nil
	cancel()
	if err != nil || checkTimedOut {
		s.unlock(req.Device)
		reply := protocol.ReleaseReply{Protocol: protocol.Version, Type: protocol.TypeReleaseReply, RequestID: req.RequestID, Status: "fail", Reason: protocol.ErrReleaseFailed}
		s.rememberPeer(req, reply)
		return reply
	}
	connected, err := bluetooth.ParseConnected(connectedRes)
	if err != nil {
		s.unlock(req.Device)
		reply := protocol.ReleaseReply{Protocol: protocol.Version, Type: protocol.TypeReleaseReply, RequestID: req.RequestID, Status: "fail", Reason: protocol.ErrReleaseFailed}
		s.rememberPeer(req, reply)
		return reply
	}

	reply := protocol.ReleaseReply{Protocol: protocol.Version, Type: protocol.TypeReleaseReply, RequestID: req.RequestID, Status: "started"}
	if !connected {
		s.unlock(req.Device)
		s.rememberPeer(req, reply)
		return reply
	}

	unpairCtx, cancel := context.WithTimeout(ctx, time.Second)
	_, err = s.runner.Run(unpairCtx, time.Second, "--unpair", req.Device)
	unpairTimedOut := unpairCtx.Err() != nil
	cancel()
	if err != nil && unpairTimedOut {
		s.unlock(req.Device)
		reply := protocol.ReleaseReply{Protocol: protocol.Version, Type: protocol.TypeReleaseReply, RequestID: req.RequestID, Status: "fail", Reason: protocol.ErrReleaseFailed}
		s.rememberPeer(req, reply)
		return reply
	}

	s.rememberPeer(req, reply)
	go s.finishPeerRelease(req)
	return reply
}

func (s *Service) finishPeerRelease(req protocol.Release) {
	defer s.unlock(req.Device)
	deadline := time.Now().Add(time.Duration(req.ReleaseTTLMS) * time.Millisecond)
	if req.ReleaseTTLMS <= 0 {
		deadline = time.Now().Add(s.cfg.Timeouts.ReleaseWindow)
	}
	for time.Now().Before(deadline) {
		timeout := 300 * time.Millisecond
		if remaining := time.Until(deadline); remaining < timeout {
			timeout = remaining
		}
		if timeout <= 0 {
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		res, err := s.runner.Run(ctx, timeout, "--is-connected", req.Device)
		cancel()
		if err == nil {
			connected, parseErr := bluetooth.ParseConnected(res)
			if parseErr == nil && !connected {
				return
			}
		}
		sleep := 200 * time.Millisecond
		if remaining := time.Until(deadline); remaining < sleep {
			sleep = remaining
		}
		if sleep <= 0 {
			return
		}
		time.Sleep(sleep)
	}
}

func (s *Service) hasDevice(address string) bool {
	for _, device := range s.cfg.Devices {
		if device == address {
			return true
		}
	}
	return false
}

func (s *Service) peerReplay(req protocol.Release) (protocol.ReleaseReply, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	for key, replay := range s.peers {
		if !now.Before(replay.expires) {
			delete(s.peers, key)
		}
	}
	replay, ok := s.peers[req.RequestID]
	if !ok || replay.requester != req.Requester || replay.device != req.Device {
		return protocol.ReleaseReply{}, false
	}
	return replay.reply, true
}

func (s *Service) rememberPeer(req protocol.Release, reply protocol.ReleaseReply) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.peers[req.RequestID] = peerReleaseReplay{requester: req.Requester, device: req.Device, reply: reply, expires: time.Now().Add(s.cfg.Timeouts.WholeClaim)}
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
	s.recent = append(s.recent, final)
	if len(s.recent) > 100 {
		s.recent = s.recent[len(s.recent)-100:]
	}
}

func (s *Service) recentFinals() []protocol.FinalResult {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := append([]protocol.FinalResult(nil), s.recent...)
	for _, replay := range s.claims {
		if replay.final != nil {
			out = append(out, *replay.final)
		}
	}
	return out
}

func (s *Service) seedRecent(path string) {
	events, err := state.RecentFinalResults(path, 100)
	if err != nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, event := range events {
		s.recent = append(s.recent, protocol.FinalResult{Type: protocol.LocalFinalResult, RequestID: event.RequestID, OK: event.OK, Error: event.Error, DurationMS: event.DurationMS})
	}
}

func (s *Service) append(event state.Event) error {
	if s.store == nil {
		return fmt.Errorf(protocol.ErrLogUnavailable)
	}
	return s.store.Append(event)
}

func (s *Service) log(msg string, args ...any) {
	if s.logger == nil {
		return
	}
	base := []any{"node", s.cfg.NodeName}
	base = append(base, args...)
	s.logger.Info(msg, base...)
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
