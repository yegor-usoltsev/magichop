package bluetooth

import "testing"

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
