package bluetooth

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os/exec"
	"strings"
	"time"
)

const (
	CommandName      = "blueutil"
	ErrPairFailed    = "pair_failed"
	ErrConnectFailed = "connect_failed"
	ErrVerifyFailed  = "verify_failed"
	ErrClaimTimeout  = "claim_timeout"

	unpairTimeout         = time.Second
	releaseVerifyTimeout  = 300 * time.Millisecond
	releasePollInterval   = 200 * time.Millisecond
	releaseWindow         = 2 * time.Second
	acquireSettleSleep    = 300 * time.Millisecond
	pairTimeout           = 5 * time.Second
	connectAttemptTimeout = 2 * time.Second
	acquireVerifyTimeout  = 300 * time.Millisecond
	acquirePollInterval   = 200 * time.Millisecond
	acquireVerifyWindow   = 500 * time.Millisecond
)

type CommandResult struct {
	Name     string
	Args     []string
	Stdout   string
	Stderr   string
	ExitCode int
	Duration time.Duration
	TimedOut bool
}

type Runner interface {
	Run(ctx context.Context, timeout time.Duration, args ...string) (CommandResult, error)
}

type FlowError struct {
	Code string
}

func (e FlowError) Error() string {
	return e.Code
}

func ErrorCode(err error) string {
	if err == nil {
		return ""
	}
	var flowErr FlowError
	if errors.As(err, &flowErr) {
		return flowErr.Code
	}
	return err.Error()
}

func Release(ctx context.Context, runner Runner, address string, budget time.Duration) error {
	return release(ctx, runner, address, budget, realClock{})
}

func Acquire(ctx context.Context, runner Runner, address string, budget time.Duration) error {
	return acquire(ctx, runner, address, budget, realClock{})
}

type ExecRunner struct {
	Path   string
	Logger *slog.Logger
}

func (r ExecRunner) Run(ctx context.Context, timeout time.Duration, args ...string) (CommandResult, error) {
	path := r.Path
	if path == "" {
		path = CommandName
	}
	start := time.Now()
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(cctx, path, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	res := CommandResult{
		Name:     path,
		Args:     args,
		Stdout:   strings.TrimSpace(stdout.String()),
		Stderr:   stderr.String(),
		ExitCode: exitCode(err),
		Duration: time.Since(start),
		TimedOut: errors.Is(cctx.Err(), context.DeadlineExceeded),
	}
	if r.Logger != nil {
		r.Logger.Info("bluetooth_command", "command", path, "args", args, "timeout", timeout.String(), "exit_code", res.ExitCode, "stdout", res.Stdout, "stderr", res.Stderr, "duration", res.Duration.String(), "timed_out", res.TimedOut)
	}
	return res, err
}

func exitCode(err error) int {
	if err == nil {
		return 0
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode()
	}
	return -1
}

func ParseConnected(res CommandResult) (bool, error) {
	if res.ExitCode != 0 {
		return false, fmt.Errorf("is-connected exit %d", res.ExitCode)
	}
	switch strings.TrimSpace(res.Stdout) {
	case "0":
		return false, nil
	case "1":
		return true, nil
	default:
		return false, fmt.Errorf("malformed is-connected output")
	}
}

type clock interface {
	Now() time.Time
	Sleep(context.Context, time.Duration) error
}

type realClock struct{}

func (realClock) Now() time.Time {
	return time.Now()
}

func (realClock) Sleep(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

type flowDeadline struct {
	clock clock
	at    time.Time
}

func newFlowDeadline(c clock, budget time.Duration) flowDeadline {
	return flowDeadline{clock: c, at: c.Now().Add(budget)}
}

func (d flowDeadline) remaining() time.Duration {
	return d.at.Sub(d.clock.Now())
}

func (d flowDeadline) expired() bool {
	return !d.clock.Now().Before(d.at)
}

func (d flowDeadline) clip(timeout time.Duration) (time.Duration, error) {
	remaining := d.at.Sub(d.clock.Now())
	if remaining <= 0 {
		return 0, FlowError{Code: ErrClaimTimeout}
	}
	if timeout <= 0 || timeout > remaining {
		return remaining, nil
	}
	return timeout, nil
}

func (d flowDeadline) sleep(ctx context.Context, requested time.Duration) error {
	sleepFor, err := d.clip(requested)
	if err != nil {
		return err
	}
	if sleepFor <= 0 {
		return FlowError{Code: ErrClaimTimeout}
	}
	if err := d.clock.Sleep(ctx, sleepFor); err != nil {
		if ctx.Err() != nil {
			return FlowError{Code: ErrClaimTimeout}
		}
		return err
	}
	return nil
}

func runBounded(ctx context.Context, runner Runner, deadline flowDeadline, timeout time.Duration, args ...string) (CommandResult, error) {
	clipped, err := deadline.clip(timeout)
	if err != nil {
		return CommandResult{Name: CommandName, Args: args, ExitCode: -1}, err
	}
	res, runErr := runner.Run(ctx, clipped, args...)
	if ctx.Err() != nil {
		return res, FlowError{Code: ErrClaimTimeout}
	}
	if deadline.expired() {
		return res, FlowError{Code: ErrClaimTimeout}
	}
	return res, runErr
}

func release(ctx context.Context, runner Runner, address string, budget time.Duration, c clock) error {
	deadline := newFlowDeadline(c, budget)
	windowEnd := c.Now().Add(releaseWindow)
	_, err := runBounded(ctx, runner, deadline, unpairTimeout, "--unpair", address)
	if errors.As(err, &FlowError{}) {
		return err
	}

	ok, err := waitConnectedState(ctx, runner, deadline, windowEnd, address, false, releaseVerifyTimeout, releasePollInterval)
	if err != nil {
		return err
	}
	if ok {
		return nil
	}
	return FlowError{Code: ErrVerifyFailed}
}

func acquire(ctx context.Context, runner Runner, address string, budget time.Duration, c clock) error {
	deadline := newFlowDeadline(c, budget)
	if _, err := runBounded(ctx, runner, deadline, unpairTimeout, "--unpair", address); err != nil {
		if ErrorCode(err) == ErrClaimTimeout {
			return err
		}
	}
	if err := deadline.sleep(ctx, acquireSettleSleep); err != nil {
		return err
	}

	pairRes, err := runBounded(ctx, runner, deadline, pairTimeout, "--pair", address)
	if ErrorCode(err) == ErrClaimTimeout {
		return err
	}
	if err != nil || pairRes.ExitCode != 0 {
		return FlowError{Code: ErrPairFailed}
	}
	if err := deadline.sleep(ctx, acquireSettleSleep); err != nil {
		return err
	}

	for {
		connectRes, err := runBounded(ctx, runner, deadline, connectAttemptTimeout, "--connect", address)
		if ErrorCode(err) == ErrClaimTimeout {
			return err
		}
		if err != nil || connectRes.ExitCode != 0 {
			return FlowError{Code: ErrConnectFailed}
		}

		ok, err := waitConnectedState(ctx, runner, deadline, c.Now().Add(acquireVerifyWindow), address, true, acquireVerifyTimeout, acquirePollInterval)
		if err != nil {
			return err
		}
		if ok {
			return nil
		}
		if deadline.expired() {
			return FlowError{Code: ErrClaimTimeout}
		}
		if deadline.remaining() < connectAttemptTimeout+acquireVerifyTimeout {
			return FlowError{Code: ErrVerifyFailed}
		}
	}
}

func waitConnectedState(ctx context.Context, runner Runner, deadline flowDeadline, windowEnd time.Time, address string, want bool, checkTimeout, pollInterval time.Duration) (bool, error) {
	for deadline.clock.Now().Before(windowEnd) {
		timeout := checkTimeout
		if untilWindowEnd := windowEnd.Sub(deadline.clock.Now()); untilWindowEnd < timeout {
			timeout = untilWindowEnd
		}
		res, err := runBounded(ctx, runner, deadline, timeout, "--is-connected", address)
		if ErrorCode(err) == ErrClaimTimeout {
			return false, err
		}
		if err == nil {
			connected, parseErr := ParseConnected(res)
			if parseErr == nil && connected == want {
				return true, nil
			}
		}

		if !deadline.clock.Now().Before(windowEnd) {
			break
		}
		sleepFor := pollInterval
		if untilWindowEnd := windowEnd.Sub(deadline.clock.Now()); untilWindowEnd < sleepFor {
			sleepFor = untilWindowEnd
		}
		if err := deadline.sleep(ctx, sleepFor); err != nil {
			return false, err
		}
	}
	return false, nil
}

func Preflight(path string) error {
	if path == "" {
		path = CommandName
	}
	found, err := exec.LookPath(path)
	if err != nil {
		return err
	}
	if found == "" {
		return fmt.Errorf("blueutil not found")
	}
	return nil
}

type FakeRunner struct {
	Results  []CommandResult
	Calls    [][]string
	Timeouts []time.Duration
}

func (f *FakeRunner) Run(_ context.Context, timeout time.Duration, args ...string) (CommandResult, error) {
	f.Calls = append(f.Calls, append([]string(nil), args...))
	f.Timeouts = append(f.Timeouts, timeout)
	if len(f.Results) == 0 {
		return CommandResult{Name: CommandName, Args: args, ExitCode: 0}, nil
	}
	res := f.Results[0]
	f.Results = f.Results[1:]
	res.Args = args
	if res.Name == "" {
		res.Name = CommandName
	}
	if res.ExitCode != 0 {
		return res, fmt.Errorf("fake command failed")
	}
	return res, nil
}
