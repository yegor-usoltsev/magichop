package bluetooth

import (
	"context"
	"reflect"
	"testing"
	"time"
)

func TestParseConnected(t *testing.T) {
	for stdout, want := range map[string]bool{"0": false, "1\n": true} {
		got, err := ParseConnected(CommandResult{ExitCode: 0, Stdout: stdout})
		if err != nil {
			t.Fatalf("ParseConnected(%q) error: %v", stdout, err)
		}
		if got != want {
			t.Fatalf("ParseConnected(%q) = %t, want %t", stdout, got, want)
		}
	}
}

func TestParseConnectedRejectsMalformedOutput(t *testing.T) {
	for _, stdout := range []string{"", "yes", "0\n1"} {
		if _, err := ParseConnected(CommandResult{ExitCode: 0, Stdout: stdout}); err == nil {
			t.Fatalf("expected %q to fail", stdout)
		}
	}
}

func TestParseConnectedRejectsNonZeroExit(t *testing.T) {
	if _, err := ParseConnected(CommandResult{ExitCode: 1, Stdout: "1"}); err == nil {
		t.Fatal("expected non-zero exit to fail")
	}
}

func TestScanPairedDoesNotChangeState(t *testing.T) {
	runner := &FakeRunner{Results: []CommandResult{{ExitCode: 0, Stdout: "aa:bb:cc:dd:ee:ff Trackpad\n"}}}
	devices, err := ScanPaired(context.Background(), runner, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if devices["aa:bb:cc:dd:ee:ff Trackpad"] == "" {
		t.Fatalf("unexpected devices: %#v", devices)
	}
	if len(runner.Calls) != 1 || runner.Calls[0][0] != "--paired" {
		t.Fatalf("unexpected calls: %#v", runner.Calls)
	}
}

func TestReleaseUnpairsAndVerifiesDisconnected(t *testing.T) {
	clock := newFakeClock()
	runner := &FakeRunner{Results: []CommandResult{
		{ExitCode: 1},
		{ExitCode: 0, Stdout: "1"},
		{ExitCode: 0, Stdout: "0"},
	}}

	err := release(context.Background(), runner, "aa:bb:cc:dd:ee:ff", 2*time.Second, clock)
	if err != nil {
		t.Fatalf("release error: %v", err)
	}

	wantCalls := [][]string{
		{"--unpair", "aa:bb:cc:dd:ee:ff"},
		{"--is-connected", "aa:bb:cc:dd:ee:ff"},
		{"--is-connected", "aa:bb:cc:dd:ee:ff"},
	}
	if !reflect.DeepEqual(runner.Calls, wantCalls) {
		t.Fatalf("calls = %#v, want %#v", runner.Calls, wantCalls)
	}
}

func TestReleaseReturnsVerifyFailedForMalformedConnectedOutput(t *testing.T) {
	clock := newFakeClock()
	runner := &FakeRunner{Results: []CommandResult{
		{ExitCode: 0},
		{ExitCode: 0, Stdout: "maybe"},
		{ExitCode: 0, Stdout: ""},
		{ExitCode: 0, Stdout: "1"},
	}}

	err := release(context.Background(), runner, "aa:bb:cc:dd:ee:ff", 2*time.Second, clock)
	if ErrorCode(err) != ErrVerifyFailed {
		t.Fatalf("release error = %v, want %s", err, ErrVerifyFailed)
	}
}

func TestAcquireRetriesConnectAfterVerifyFailureWithinBudget(t *testing.T) {
	clock := newFakeClock()
	runner := &FakeRunner{Results: []CommandResult{
		{ExitCode: 0},
		{ExitCode: 0},
		{ExitCode: 0},
		{ExitCode: 0, Stdout: "0"},
		{ExitCode: 0, Stdout: "0"},
		{ExitCode: 0, Stdout: "0"},
		{ExitCode: 0},
		{ExitCode: 0, Stdout: "1"},
	}}

	err := acquire(context.Background(), runner, "aa:bb:cc:dd:ee:ff", 5*time.Second, clock)
	if err != nil {
		t.Fatalf("acquire error: %v", err)
	}

	wantCalls := [][]string{
		{"--unpair", "aa:bb:cc:dd:ee:ff"},
		{"--pair", "aa:bb:cc:dd:ee:ff"},
		{"--connect", "aa:bb:cc:dd:ee:ff"},
		{"--is-connected", "aa:bb:cc:dd:ee:ff"},
		{"--is-connected", "aa:bb:cc:dd:ee:ff"},
		{"--is-connected", "aa:bb:cc:dd:ee:ff"},
		{"--connect", "aa:bb:cc:dd:ee:ff"},
		{"--is-connected", "aa:bb:cc:dd:ee:ff"},
	}
	if !reflect.DeepEqual(runner.Calls, wantCalls) {
		t.Fatalf("calls = %#v, want %#v", runner.Calls, wantCalls)
	}

	if got, want := runner.Timeouts[2], 2*time.Second; got != want {
		t.Fatalf("first connect timeout = %s, want %s", got, want)
	}
	if got, want := runner.Timeouts[6], 2*time.Second; got != want {
		t.Fatalf("retry connect timeout = %s, want %s", got, want)
	}
}

func TestAcquireReturnsVerifyFailedWhenRetryBudgetIsInsufficient(t *testing.T) {
	clock := newFakeClock()
	runner := &FakeRunner{Results: []CommandResult{
		{ExitCode: 0},
		{ExitCode: 0},
		{ExitCode: 0},
		{ExitCode: 0, Stdout: "0"},
		{ExitCode: 0, Stdout: "0"},
		{ExitCode: 0, Stdout: "0"},
	}}

	err := acquire(context.Background(), runner, "aa:bb:cc:dd:ee:ff", 1500*time.Millisecond, clock)
	if ErrorCode(err) != ErrVerifyFailed {
		t.Fatalf("acquire error = %v, want %s", err, ErrVerifyFailed)
	}
}

func TestAcquireReturnsPairFailed(t *testing.T) {
	clock := newFakeClock()
	runner := &FakeRunner{Results: []CommandResult{
		{ExitCode: 0},
		{ExitCode: 1},
	}}

	err := acquire(context.Background(), runner, "aa:bb:cc:dd:ee:ff", 11*time.Second, clock)
	if ErrorCode(err) != ErrPairFailed {
		t.Fatalf("acquire error = %v, want %s", err, ErrPairFailed)
	}
}

func TestAcquireReturnsConnectFailed(t *testing.T) {
	clock := newFakeClock()
	runner := &FakeRunner{Results: []CommandResult{
		{ExitCode: 0},
		{ExitCode: 0},
		{ExitCode: 1},
	}}

	err := acquire(context.Background(), runner, "aa:bb:cc:dd:ee:ff", 11*time.Second, clock)
	if ErrorCode(err) != ErrConnectFailed {
		t.Fatalf("acquire error = %v, want %s", err, ErrConnectFailed)
	}
}

func TestAcquireReturnsClaimTimeoutWhenBudgetExpires(t *testing.T) {
	clock := newFakeClock()
	runner := &FakeRunner{Results: []CommandResult{
		{ExitCode: 0},
	}}

	err := acquire(context.Background(), runner, "aa:bb:cc:dd:ee:ff", 300*time.Millisecond, clock)
	if ErrorCode(err) != ErrClaimTimeout {
		t.Fatalf("acquire error = %v, want %s", err, ErrClaimTimeout)
	}
}

type fakeClock struct {
	now time.Time
}

func newFakeClock() *fakeClock {
	return &fakeClock{now: time.Unix(0, 0)}
}

func (c *fakeClock) Now() time.Time {
	return c.now
}

func (c *fakeClock) Sleep(ctx context.Context, d time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	c.now = c.now.Add(d)
	return nil
}
