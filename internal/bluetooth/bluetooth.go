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
	Results []CommandResult
	Calls   [][]string
}

func (f *FakeRunner) Run(_ context.Context, _ time.Duration, args ...string) (CommandResult, error) {
	f.Calls = append(f.Calls, append([]string(nil), args...))
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
