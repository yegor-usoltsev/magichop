package cmd

import "testing"

func TestParseClientArgs(t *testing.T) {
	t.Parallel()

	opts, err := parseClientArgs("claim", []string{"--config", "/tmp/cfg.json", "device"}, 1)
	if err != nil {
		t.Fatalf("parse args: %v", err)
	}
	if opts.configPath != "/tmp/cfg.json" || opts.device != "device" {
		t.Fatalf("unexpected args: %#v", opts)
	}
}

func TestParseClientArgsRejectsMissingConfigValue(t *testing.T) {
	t.Parallel()

	if _, err := parseClientArgs("claim", []string{"--config"}, 1); err == nil {
		t.Fatal("expected missing config value error")
	}
}

func TestParseClientArgsRejectsExtraDevices(t *testing.T) {
	t.Parallel()

	if _, err := parseClientArgs("status", []string{"device"}, 0); err == nil {
		t.Fatal("expected extra device error")
	}
}
