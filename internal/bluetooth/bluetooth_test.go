package bluetooth

import (
	"reflect"
	"testing"
)

func TestArgs(t *testing.T) {
	t.Parallel()

	tests := map[string][]string{
		"connect":      {"--connect", "aa"},
		"disconnect":   {"--disconnect", "aa"},
		"is-connected": {"--is-connected", "aa"},
	}
	for action, want := range tests {
		if got := Args(action, "aa"); !reflect.DeepEqual(got, want) {
			t.Fatalf("Args(%s) = %#v, want %#v", action, got, want)
		}
	}
}

func TestResultMessage(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		result Result
		want   string
	}{
		{name: "error", result: Result{Error: "boom", Stderr: "stderr", Stdout: "stdout"}, want: "boom"},
		{name: "stderr", result: Result{Stderr: "stderr", Stdout: "stdout"}, want: "stderr"},
		{name: "stdout", result: Result{Stdout: "stdout"}, want: "stdout"},
		{name: "exit", result: Result{ReturnCode: 7}, want: "exit 7"},
	}
	for _, tt := range tests {
		if got := tt.result.Message(); got != tt.want {
			t.Fatalf("%s: got %q, want %q", tt.name, got, tt.want)
		}
	}
}
