package daemon

import (
	"context"
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

func testConfig() config.Config {
	cfg := config.Defaults()
	cfg.DefaultDevice = "trackpad"
	cfg.Devices = map[string]string{"trackpad": "aa:bb:cc:dd:ee:ff"}
	cfg.Timeouts.ReleaseWindow = 2 * time.Second
	return cfg
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
