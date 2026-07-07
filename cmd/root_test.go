package cmd

import (
	"errors"
	"os"
	"testing"
)

func TestExitCodeMapping(t *testing.T) {
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
