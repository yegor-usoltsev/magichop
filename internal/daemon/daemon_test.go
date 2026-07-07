package daemon

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/yegor-usoltsev/magichop/internal/bluetooth"
	"github.com/yegor-usoltsev/magichop/internal/config"
	"github.com/yegor-usoltsev/magichop/internal/protocol"
	"github.com/yegor-usoltsev/magichop/internal/state"
)

type memoryStore struct {
	events []state.Event
	err    error
}

func (s *memoryStore) Append(event state.Event) error {
	if s.err != nil {
		return s.err
	}
	s.events = append(s.events, event)
	return nil
}

func TestClaimDuplicateClientRequestIDReplaysFinal(t *testing.T) {
	store := &memoryStore{}
	svc := NewService(testConfig(), store, &bluetooth.FakeRunner{})
	req := protocol.LocalRequest{Type: protocol.LocalClaim, ClientRequestID: "client-1", Device: "trackpad", TimeoutMS: 11000}

	first := svc.Handle(context.Background(), req)
	second := svc.Handle(context.Background(), req)

	if len(first) != 2 || len(second) != 2 {
		t.Fatalf("reply lengths = %d and %d, want 2 and 2", len(first), len(second))
	}
	firstAccepted := first[0].(protocol.Accepted)
	secondAccepted := second[0].(protocol.Accepted)
	if firstAccepted.RequestID != secondAccepted.RequestID {
		t.Fatalf("request id changed on replay: %q != %q", firstAccepted.RequestID, secondAccepted.RequestID)
	}
	firstFinal := first[1].(protocol.FinalResult)
	secondFinal := second[1].(protocol.FinalResult)
	if firstFinal.RequestID != secondFinal.RequestID || secondFinal.Error != protocol.ErrCoordinatorUnavailable {
		t.Fatalf("unexpected replay final: %+v", secondFinal)
	}
	if got := countEvents(store.events, state.EventAccepted); got != 1 {
		t.Fatalf("accepted events = %d, want 1", got)
	}
	if got := countEvents(store.events, state.EventFinalResult); got != 1 {
		t.Fatalf("final events = %d, want 1", got)
	}
}

func TestLocalReleaseRunsBluetoothAndAppendsState(t *testing.T) {
	store := &memoryStore{}
	runner := &bluetooth.FakeRunner{Results: []bluetooth.CommandResult{
		{ExitCode: 0},
		{ExitCode: 0, Stdout: "0"},
	}}
	svc := NewService(testConfig(), store, runner)

	reply := svc.Handle(context.Background(), protocol.LocalRequest{Type: protocol.LocalRelease, Device: "trackpad"})[0].(protocol.ReleaseResult)
	if !reply.OK || reply.Address != "aa:bb:cc:dd:ee:ff" {
		t.Fatalf("unexpected release reply: %+v", reply)
	}
	if got := countEvents(store.events, state.EventLocalRelease); got != 1 {
		t.Fatalf("local release events = %d, want 1", got)
	}
	if len(runner.Calls) < 2 || runner.Calls[0][0] != "--unpair" || runner.Calls[1][0] != "--is-connected" {
		t.Fatalf("unexpected bluetooth calls: %#v", runner.Calls)
	}
}

func TestLocalOperationLockReturnsBusy(t *testing.T) {
	svc := NewService(testConfig(), &memoryStore{}, &bluetooth.FakeRunner{})
	if !svc.tryLock("aa:bb:cc:dd:ee:ff") {
		t.Fatal("failed to pre-lock device")
	}
	defer svc.unlock("aa:bb:cc:dd:ee:ff")

	reply := svc.Handle(context.Background(), protocol.LocalRequest{Type: protocol.LocalRelease, Device: "trackpad"})[0].(protocol.ReleaseResult)
	if reply.Error != protocol.ErrBusy {
		t.Fatalf("error = %q, want busy", reply.Error)
	}
}

func TestDaemonLockAllowsOnlyOneActiveDaemon(t *testing.T) {
	dir := t.TempDir()
	first, err := acquireDaemonLock(dir)
	if err != nil {
		t.Fatal(err)
	}
	if second, err := acquireDaemonLock(dir); err == nil {
		releaseDaemonLock(second)
		t.Fatal("expected second daemon lock to fail")
	}
	releaseDaemonLock(first)
	if third, err := acquireDaemonLock(dir); err != nil {
		t.Fatalf("expected lock after release: %v", err)
	} else {
		releaseDaemonLock(third)
	}
}

func TestPeerDuplicateReleaseReplaysStartedWithoutSecondUnpair(t *testing.T) {
	runner := &recordingRunner{}
	svc := NewService(testConfig(), &memoryStore{}, runner)
	req := protocol.Release{Protocol: protocol.Version, Type: protocol.TypeRelease, RequestID: mustID(t), Requester: "peer", Device: "aa:bb:cc:dd:ee:ff", ReleaseTTLMS: 50, MaxStartDelayMS: 300}

	first := svc.handlePeerRelease(context.Background(), req)
	second := svc.handlePeerRelease(context.Background(), req)

	if first.Status != "started" || second.Status != "started" {
		t.Fatalf("statuses = %q, %q; want started, started", first.Status, second.Status)
	}
	if got := runner.count("--unpair"); got != 1 {
		t.Fatalf("unpair calls = %d, want 1", got)
	}
}

func TestPeerReleaseReturnsBusyWhenLocalLockHeld(t *testing.T) {
	svc := NewService(testConfig(), &memoryStore{}, &recordingRunner{})
	if !svc.tryLock("aa:bb:cc:dd:ee:ff") {
		t.Fatal("failed to pre-lock device")
	}
	defer svc.unlock("aa:bb:cc:dd:ee:ff")

	reply := svc.handlePeerRelease(context.Background(), protocol.Release{Protocol: protocol.Version, Type: protocol.TypeRelease, RequestID: mustID(t), Requester: "peer", Device: "aa:bb:cc:dd:ee:ff", ReleaseTTLMS: 50, MaxStartDelayMS: 300})
	if reply.Status != "busy" || reply.Reason != protocol.ErrPeerBusy {
		t.Fatalf("unexpected reply: %+v", reply)
	}
}

type recordingRunner struct {
	mu    sync.Mutex
	calls [][]string
}

func (r *recordingRunner) Run(_ context.Context, _ time.Duration, args ...string) (bluetooth.CommandResult, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, append([]string(nil), args...))
	if len(args) > 0 && args[0] == "--is-connected" {
		return bluetooth.CommandResult{ExitCode: 0, Stdout: "0"}, nil
	}
	return bluetooth.CommandResult{ExitCode: 0}, nil
}

func (r *recordingRunner) count(command string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	var count int
	for _, call := range r.calls {
		if len(call) > 0 && call[0] == command {
			count++
		}
	}
	return count
}

func testConfig() config.Config {
	cfg := config.Defaults()
	cfg.DefaultDevice = "trackpad"
	cfg.Devices = map[string]string{"trackpad": "aa:bb:cc:dd:ee:ff"}
	cfg.Timeouts.ReleaseWindow = 2 * time.Second
	return cfg
}

func mustID(t *testing.T) string {
	t.Helper()
	id, err := protocol.NewRequestID()
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func countEvents(events []state.Event, typ string) int {
	var count int
	for _, event := range events {
		if event.Event == typ {
			count++
		}
	}
	return count
}
