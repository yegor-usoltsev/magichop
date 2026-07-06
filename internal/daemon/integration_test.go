package daemon

import (
	"context"
	"net"
	"strconv"
	"testing"
	"time"

	"github.com/yegor-usoltsev/MagicHop/internal/bluetooth"
	"github.com/yegor-usoltsev/MagicHop/internal/config"
	"github.com/yegor-usoltsev/MagicHop/internal/coordinator"
)

func TestClaimReleasesPeerThenConnectsLocal(t *testing.T) {
	t.Parallel()

	ctx := t.Context()

	serverCfg := startCoordinator(t, ctx)

	aBT := &bluetooth.Fake{}
	bBT := &bluetooth.Fake{}
	a := Client{Config: testConfig("mac-a", serverCfg.ServerPort), Bluetooth: aBT}
	b := Client{Config: testConfig("mac-b", serverCfg.ServerPort), Bluetooth: bBT}
	startDaemon(t, ctx, b)

	result, err := a.Claim(ctx, "device")
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	if len(result.Acks) != 1 || !result.Acks[0].OK || result.Acks[0].Node != "mac-b" {
		t.Fatalf("unexpected acks: %#v", result.Acks)
	}
	if len(bBT.Released) != 1 || bBT.Released[0] != "aa-bb-cc-dd-ee-ff" {
		t.Fatalf("peer releases: %#v", bBT.Released)
	}
	if len(aBT.Connected) != 1 || aBT.Connected[0] != "aa-bb-cc-dd-ee-ff" {
		t.Fatalf("local connects: %#v", aBT.Connected)
	}
}

func TestClaimDoesNotWaitForStalePeerStatus(t *testing.T) {
	t.Parallel()

	ctx := t.Context()

	serverCfg := startCoordinator(t, ctx)

	aBT := &bluetooth.Fake{}
	bBT := &bluetooth.Fake{}
	a := Client{Config: testConfig("mac-a", serverCfg.ServerPort), Bluetooth: aBT}
	b := Client{Config: testConfig("mac-b", serverCfg.ServerPort), Bluetooth: bBT}

	bCtx, stopB := context.WithCancel(ctx)
	bDone := startDaemon(t, bCtx, b)
	stopB()
	<-bDone

	start := time.Now()
	result, err := a.Claim(ctx, "device")
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("claim waited for stale peer: %s", elapsed)
	}
	if len(result.Acks) != 0 {
		t.Fatalf("unexpected stale peer acks: %#v", result.Acks)
	}
	if len(aBT.Connected) != 1 || aBT.Connected[0] != "aa-bb-cc-dd-ee-ff" {
		t.Fatalf("local connects: %#v", aBT.Connected)
	}
}

func TestClaimUsesSinglePeerReleaseAndRemainingConnectBudget(t *testing.T) {
	t.Parallel()

	ctx := t.Context()

	serverCfg := startCoordinator(t, ctx)

	aBT := &bluetooth.Fake{FailConnects: 1}
	bBT := &bluetooth.Fake{}
	aCfg := testConfig("mac-a", serverCfg.ServerPort)
	aCfg.ConnectTimeout.Duration = 1500 * time.Millisecond
	aCfg.ClaimTimeout.Duration = time.Second
	a := Client{Config: aCfg, Bluetooth: aBT}
	b := Client{Config: testConfig("mac-b", serverCfg.ServerPort), Bluetooth: bBT}
	startDaemon(t, ctx, b)

	result, err := a.Claim(ctx, "device")
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	if result.Connect.OK {
		t.Fatalf("connect unexpectedly succeeded: %#v", result.Connect)
	}
	if len(bBT.Released) != 1 {
		t.Fatalf("peer release count = %d, want 1: %#v", len(bBT.Released), bBT.Released)
	}
	if len(aBT.Connected) != 1 {
		t.Fatalf("local connect count = %d, want 1: %#v", len(aBT.Connected), aBT.Connected)
	}
	if len(aBT.ConnectTimeouts) != 1 {
		t.Fatalf("connect timeouts: %#v", aBT.ConnectTimeouts)
	}
	if timeout := aBT.ConnectTimeouts[0]; timeout <= time.Second || timeout > aCfg.ConnectTimeout.Duration {
		t.Fatalf("connect timeout = %s, want remaining budget near %s", timeout, aCfg.ConnectTimeout.Duration)
	}
}

func testConfig(node string, port uint16) config.ClientConfig {
	cfg := config.DefaultClientConfig()
	cfg.NodeName = node
	cfg.AuthToken = "secret"
	cfg.CoordinatorURL = config.LocalhostNATSURL(port)
	cfg.Devices = map[string]string{"device": "aa-bb-cc-dd-ee-ff"}
	cfg.DefaultDevice = "device"
	cfg.ClaimTimeout.Duration = 2 * time.Second
	return cfg
}

func startCoordinator(t *testing.T, ctx context.Context) config.ServerConfig {
	t.Helper()

	serverCfg := config.ServerConfig{ServerHost: "127.0.0.1", ServerPort: freePort(t), AuthToken: "secret"}
	go func() {
		if err := coordinator.New(serverCfg).Run(ctx); err != nil && ctx.Err() == nil {
			t.Errorf("coordinator failed: %v", err)
		}
	}()
	time.Sleep(200 * time.Millisecond)
	return serverCfg
}

func startDaemon(t *testing.T, ctx context.Context, client Client) <-chan struct{} {
	t.Helper()

	done := make(chan struct{})
	go func() {
		defer close(done)
		if err := client.Run(ctx); err != nil && ctx.Err() == nil {
			t.Errorf("daemon failed: %v", err)
		}
	}()
	time.Sleep(200 * time.Millisecond)
	return done
}

func freePort(t *testing.T) uint16 {
	t.Helper()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen on random port: %v", err)
	}
	defer ln.Close()
	_, portText, err := net.SplitHostPort(ln.Addr().String())
	if err != nil {
		t.Fatalf("split host port: %v", err)
	}
	port, err := strconv.ParseUint(portText, 10, 16)
	if err != nil {
		t.Fatalf("parse port: %v", err)
	}
	return uint16(port)
}
