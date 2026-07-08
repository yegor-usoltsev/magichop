package cmd

import (
	"bytes"
	"errors"
	"io"
	"os"
	"strings"
	"testing"
)

func TestRunWithoutArgsPrintsHelp(t *testing.T) {
	stdout := os.Stdout
	readEnd, writeEnd, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = writeEnd
	var out bytes.Buffer
	done := make(chan struct{})
	go func() {
		_, _ = io.Copy(&out, readEnd)
		close(done)
	}()
	code := Run(nil)
	_ = writeEnd.Close()
	os.Stdout = stdout
	<-done
	_ = readEnd.Close()
	if code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	if !strings.Contains(out.String(), "Usage: magichop <command>") {
		t.Fatalf("help output missing usage: %s", out.String())
	}
}

func TestExitCodeMapping(t *testing.T) {
	stderr := os.Stderr
	readEnd, writeEnd, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stderr = writeEnd
	defer func() {
		os.Stderr = stderr
		_ = readEnd.Close()
	}()
	defer writeEnd.Close()
	go func() {
		_, _ = io.Copy(io.Discard, readEnd)
	}()

	tests := []struct {
		err  error
		want int
	}{
		{nil, 0},
		{runtimeErr("failed"), 1},
		{usageErr(errors.New("bad usage")), 2},
		{daemonUnavailableErr(errors.New("missing socket")), 3},
		{permissionErr(os.ErrPermission), 4},
		{os.ErrPermission, 4},
	}
	for _, tt := range tests {
		got := 0
		if tt.err != nil {
			got = exitCode(tt.err)
		}
		if got != tt.want {
			t.Fatalf("exitCode(%v) = %d, want %d", tt.err, got, tt.want)
		}
	}
}
