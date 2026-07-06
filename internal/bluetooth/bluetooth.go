package bluetooth

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"
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
	return b.run(ctx, Args("connect", address), timeout)
}

func (b Blueutil) Disconnect(ctx context.Context, address string, timeout time.Duration) Result {
	return b.run(ctx, Args("disconnect", address), timeout)
}

func (b Blueutil) IsConnected(ctx context.Context, address string, timeout time.Duration) (bool, Result) {
	result := b.run(ctx, Args("is-connected", address), timeout)
	return result.OK && strings.TrimSpace(result.Stdout) == "1", result
}

func (b Blueutil) run(ctx context.Context, args []string, timeout time.Duration) Result {
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
	default:
		return []string{"--" + action, address}
	}
}
