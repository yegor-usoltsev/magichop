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

	"github.com/yegor-usoltsev/magichop/internal/config"
	"github.com/yegor-usoltsev/magichop/internal/protocol"
)

type Options struct {
	ConfigPath string
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
		go handleConn(conn, cfg)
	}
}

func handleConn(conn net.Conn, cfg config.Config) {
	defer conn.Close()
	var req protocol.LocalRequest
	if err := json.NewDecoder(conn).Decode(&req); err != nil {
		return
	}
	enc := json.NewEncoder(conn)
	switch req.Type {
	case protocol.LocalDevices:
		_ = enc.Encode(protocol.DevicesResult{Type: protocol.LocalDevicesResult, OK: true, Devices: cfg.Devices})
	case protocol.LocalStatus:
		device, address, _ := config.ResolveDevice(cfg, req.Device)
		_ = enc.Encode(protocol.StatusResult{Type: protocol.LocalStatusResult, OK: true, Daemon: "running", Coordinator: "disconnected", Device: displayDevice(device, address)})
	case protocol.LocalDoctor:
		_ = enc.Encode(protocol.DoctorResult{Type: protocol.LocalDoctorResult, OK: true, Checks: []protocol.DoctorCheck{{Name: "config", OK: true}}})
	case protocol.LocalRelease:
		device, address, err := config.ResolveDevice(cfg, req.Device)
		if err != nil {
			_ = enc.Encode(protocol.ReleaseResult{Type: protocol.LocalReleaseResult, OK: false, Error: protocol.ErrInvalidRequest})
			return
		}
		_ = enc.Encode(protocol.ReleaseResult{Type: protocol.LocalReleaseResult, OK: true, Device: displayDevice(device, address), Address: address})
	case protocol.LocalClaim:
		device, address, err := config.ResolveDevice(cfg, req.Device)
		if err != nil {
			_ = enc.Encode(protocol.FinalResult{Type: protocol.LocalFinalResult, OK: false, Error: protocol.ErrInvalidRequest})
			return
		}
		requestID, _ := protocol.NewRequestID()
		_ = enc.Encode(protocol.Accepted{Type: protocol.LocalAccepted, ClientRequestID: req.ClientRequestID, RequestID: requestID})
		_ = enc.Encode(protocol.FinalResult{Type: protocol.LocalFinalResult, RequestID: requestID, OK: false, Error: protocol.ErrCoordinatorUnavailable, Device: displayDevice(device, address), Address: address})
	default:
		_ = enc.Encode(map[string]any{"type": "error", "ok": false, "error": fmt.Sprintf("%s: unknown request", protocol.ErrInvalidRequest)})
	}
}

func displayDevice(alias, address string) string {
	if alias != "" {
		return alias
	}
	return address
}
