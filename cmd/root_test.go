package cmd

import (
	"errors"
	"io"
	"os"
	"testing"
)

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
