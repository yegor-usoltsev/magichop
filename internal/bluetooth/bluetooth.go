package bluetooth

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

const (
	attachRetryDelay      = time.Second
	connectAttemptTimeout = 5 * time.Second
	connectVerifyTimeout  = time.Second
	connectRetryDelay     = 300 * time.Millisecond
	pairAttemptTimeout    = 8 * time.Second
	pairSettleDelay       = 500 * time.Millisecond
	releaseAttemptTimeout = 3 * time.Second
	stateCheckTimeout     = 750 * time.Millisecond
)

type Backend interface {
	Connect(ctx context.Context, address string, timeout time.Duration) Result
	Disconnect(ctx context.Context, address string, timeout time.Duration) Result
	IsConnected(ctx context.Context, address string, timeout time.Duration) (bool, Result)
}

type Result struct {
	OK         bool
	ReturnCode int
	Stdout     string
	Stderr     string
	Error      string
}

func (r Result) Message() string {
	for _, value := range []string{r.Error, r.Stderr, r.Stdout} {
		if value != "" {
			return value
		}
	}
	return fmt.Sprintf("exit %d", r.ReturnCode)
}

type Blueutil struct {
	Path string
}

func NewBlueutil() (*Blueutil, error) {
	path, err := exec.LookPath("blueutil")
	if err != nil {
		return nil, fmt.Errorf("find blueutil: %w", err)
	}
	return &Blueutil{Path: path}, nil
}

func (b Blueutil) Connect(ctx context.Context, address string, timeout time.Duration) Result {
	deadline := time.Now().Add(timeout)
	connected, result := b.IsConnected(ctx, address, minDuration(2*time.Second, time.Until(deadline)))
	if connected {
		return result
	}
	if ctx.Err() != nil {
		return Result{Error: ctx.Err().Error()}
	}
	return b.attach(ctx, address, deadline)
}

func (b Blueutil) Disconnect(ctx context.Context, address string, timeout time.Duration) Result {
	deadline := time.Now().Add(timeout)
	unpair := b.run(ctx, Args("unpair", address), minDuration(releaseAttemptTimeout, time.Until(deadline)))
	result, ok := b.waitForConnectionState(ctx, address, false, deadline)
	if ok {
		return result
	}
	if unpair.OK {
		return result
	}
	return unpair
}

func (b Blueutil) IsConnected(ctx context.Context, address string, timeout time.Duration) (bool, Result) {
	result := b.run(ctx, Args("is-connected", address), timeout)
	return result.OK && strings.TrimSpace(result.Stdout) == "1", result
}

func (b Blueutil) run(ctx context.Context, args []string, timeout time.Duration) Result {
	if timeout <= 0 {
		return Result{Error: context.DeadlineExceeded.Error()}
	}
	cmdCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(cmdCtx, b.Path, args...) //nolint:gosec // fixed executable with explicit blueutil arguments
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	result := Result{
		OK:     err == nil,
		Stdout: strings.TrimSpace(stdout.String()),
		Stderr: strings.TrimSpace(stderr.String()),
	}
	if cmd.ProcessState != nil {
		result.ReturnCode = cmd.ProcessState.ExitCode()
	}
	if err != nil {
		result.Error = err.Error()
	}
	if cmdCtx.Err() != nil {
		result.Error = cmdCtx.Err().Error()
	}
	return result
}

func Args(action, address string) []string {
	switch action {
	case "connect":
		return []string{"--connect", address}
	case "disconnect":
		return []string{"--disconnect", address}
	case "is-connected":
		return []string{"--is-connected", address}
	case "pair":
		return []string{"--pair", address}
	case "unpair":
		return []string{"--unpair", address}
	default:
		return []string{"--" + action, address}
	}
}

func (b Blueutil) attach(ctx context.Context, address string, deadline time.Time) Result {
	var last Result
	haveLast := false
	for {
		if time.Until(deadline) <= 0 {
			if haveLast {
				return last
			}
			return Result{Error: context.DeadlineExceeded.Error()}
		}
		_ = b.run(ctx, Args("unpair", address), minDuration(releaseAttemptTimeout, time.Until(deadline)))
		if err := sleepUntil(ctx, pairSettleDelay, deadline); err != nil {
			if haveLast {
				return last
			}
			return Result{Error: err.Error()}
		}
		last = b.run(ctx, Args("pair", address), minDuration(pairAttemptTimeout, time.Until(deadline)))
		haveLast = true
		if last.OK {
			if err := sleepUntil(ctx, pairSettleDelay, deadline); err != nil {
				return Result{Error: err.Error()}
			}
			last = b.connectPaired(ctx, address, deadline)
			if last.OK {
				return last
			}
		}
		if ctx.Err() != nil {
			last.Error = ctx.Err().Error()
			return last
		}
		if err := sleepUntil(ctx, attachRetryDelay, deadline); err != nil {
			return last
		}
	}
}

func (b Blueutil) connectPaired(ctx context.Context, address string, deadline time.Time) Result {
	var last Result
	haveLast := false
	for {
		attemptTimeout := minDuration(connectAttemptTimeout, time.Until(deadline))
		if attemptTimeout <= 0 {
			if haveLast {
				return last
			}
			return Result{Error: context.DeadlineExceeded.Error()}
		}
		last = b.run(ctx, Args("connect", address), attemptTimeout)
		haveLast = true
		if last.OK {
			verifyDeadline := time.Now().Add(minDuration(connectVerifyTimeout, time.Until(deadline)))
			result, ok := b.waitForConnectionState(ctx, address, true, verifyDeadline)
			if ok {
				return result
			}
			last = result
		}
		if ctx.Err() != nil {
			last.Error = ctx.Err().Error()
			return last
		}
		if err := sleepUntil(ctx, connectRetryDelay, deadline); err != nil {
			return last
		}
	}
}

func (b Blueutil) waitForConnectionState(ctx context.Context, address string, connectedState bool, deadline time.Time) (Result, bool) {
	var last Result
	haveLast := false
	for {
		checkTimeout := minDuration(stateCheckTimeout, time.Until(deadline))
		if checkTimeout <= 0 {
			if haveLast {
				return stateMismatchResult(last, connectedState), false
			}
			return Result{Error: context.DeadlineExceeded.Error()}, false
		}
		connected, result := b.IsConnected(ctx, address, checkTimeout)
		last = result
		haveLast = true
		if result.OK && connected == connectedState {
			return result, true
		}
		if ctx.Err() != nil {
			result.Error = ctx.Err().Error()
			return result, false
		}
		if err := sleepUntil(ctx, connectRetryDelay, deadline); err != nil {
			return stateMismatchResult(last, connectedState), false
		}
	}
}

func stateMismatchResult(result Result, connectedState bool) Result {
	if !result.OK {
		return result
	}
	result.OK = false
	if connectedState {
		result.Error = "device did not report connected"
	} else {
		result.Error = "device did not report disconnected"
	}
	return result
}

func sleepUntil(ctx context.Context, delay time.Duration, deadline time.Time) error {
	sleep := minDuration(delay, time.Until(deadline))
	if sleep <= 0 {
		return context.DeadlineExceeded
	}
	timer := time.NewTimer(sleep)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return fmt.Errorf("context done: %w", ctx.Err())
	case <-timer.C:
		return nil
	}
}

func minDuration(a, b time.Duration) time.Duration {
	if a < b {
		return a
	}
	return b
}
