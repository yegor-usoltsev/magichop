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

func TestClaimDisconnectsPeerThenConnectsLocal(t *testing.T) {
	t.Parallel()

	ctx := t.Context()

	token := "secret"
	serverCfg := config.ServerConfig{ServerHost: "127.0.0.1", ServerPort: freePort(t), AuthToken: token}
	go func() {
		if err := coordinator.New(serverCfg).Run(ctx); err != nil && ctx.Err() == nil {
			t.Errorf("coordinator failed: %v", err)
		}
	}()
	time.Sleep(200 * time.Millisecond)

	aBT := &bluetooth.Fake{}
	bBT := &bluetooth.Fake{}
	a := Client{Config: testConfig("mac-a", serverCfg.ServerPort), Bluetooth: aBT}
	b := Client{Config: testConfig("mac-b", serverCfg.ServerPort), Bluetooth: bBT}
	go func() {
		if err := b.Run(ctx); err != nil && ctx.Err() == nil {
			t.Errorf("daemon failed: %v", err)
		}
	}()
	time.Sleep(200 * time.Millisecond)

	result, err := a.Claim(ctx, "device")
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	if len(result.Acks) != 1 || !result.Acks[0].OK || result.Acks[0].Node != "mac-b" {
		t.Fatalf("unexpected acks: %#v", result.Acks)
	}
	if len(bBT.Disconnected) != 1 || bBT.Disconnected[0] != "aa-bb-cc-dd-ee-ff" {
		t.Fatalf("peer disconnects: %#v", bBT.Disconnected)
	}
	if len(aBT.Connected) != 1 || aBT.Connected[0] != "aa-bb-cc-dd-ee-ff" {
		t.Fatalf("local connects: %#v", aBT.Connected)
	}
}

func TestClaimDoesNotWaitForStalePeerStatus(t *testing.T) {
	t.Parallel()

	ctx := t.Context()

	token := "secret"
	serverCfg := config.ServerConfig{ServerHost: "127.0.0.1", ServerPort: freePort(t), AuthToken: token}
	go func() {
		if err := coordinator.New(serverCfg).Run(ctx); err != nil && ctx.Err() == nil {
			t.Errorf("coordinator failed: %v", err)
		}
	}()
	time.Sleep(200 * time.Millisecond)

	aBT := &bluetooth.Fake{}
	bBT := &bluetooth.Fake{}
	a := Client{Config: testConfig("mac-a", serverCfg.ServerPort), Bluetooth: aBT}
	b := Client{Config: testConfig("mac-b", serverCfg.ServerPort), Bluetooth: bBT}

	bCtx, stopB := context.WithCancel(ctx)
	bDone := make(chan struct{})
	go func() {
		defer close(bDone)
		if err := b.Run(bCtx); err != nil && bCtx.Err() == nil {
			t.Errorf("daemon failed: %v", err)
		}
	}()
	time.Sleep(200 * time.Millisecond)
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
