package cmd

import "testing"

func TestKongParsesClaimArgs(t *testing.T) {
	t.Parallel()

	var app cli
	parser, err := newParser(&app, func(int) {})
	if err != nil {
		t.Fatalf("new parser: %v", err)
	}
	ctx, err := parser.Parse([]string{"claim", "--config", "/tmp/cfg.json", "device"})
	if err != nil {
		t.Fatalf("parse args: %v", err)
	}
	if ctx.Command() != "claim <device>" {
		t.Fatalf("unexpected command: %s", ctx.Command())
	}
	if app.Claim.Config != "/tmp/cfg.json" || app.Claim.Device != "device" {
		t.Fatalf("unexpected args: %#v", app.Claim)
	}
}

func TestKongRejectsMissingConfigValue(t *testing.T) {
	t.Parallel()

	var app cli
	parser, err := newParser(&app, func(int) {})
	if err != nil {
		t.Fatalf("new parser: %v", err)
	}
	if _, err := parser.Parse([]string{"claim", "--config"}); err == nil {
		t.Fatal("expected missing config value error")
	}
}

func TestKongRejectsExtraStatusArgs(t *testing.T) {
	t.Parallel()

	var app cli
	parser, err := newParser(&app, func(int) {})
	if err != nil {
		t.Fatalf("new parser: %v", err)
	}
	if _, err := parser.Parse([]string{"status", "device"}); err == nil {
		t.Fatal("expected extra device error")
	}
}

func TestKongParsesConfigInitDevices(t *testing.T) {
	t.Parallel()

	var app cli
	parser, err := newParser(&app, func(int) {})
	if err != nil {
		t.Fatalf("new parser: %v", err)
	}
	if _, err := parser.Parse([]string{"config", "init", "--force", "--device", "trackpad=AA:BB:CC:DD:EE:FF"}); err != nil {
		t.Fatalf("parse config init: %v", err)
	}
	devices, err := parseDeviceFlags(app.Config.Init.Devices)
	if err != nil {
		t.Fatalf("parse devices: %v", err)
	}
	if !app.Config.Init.Force || devices["trackpad"] != "aa-bb-cc-dd-ee-ff" {
		t.Fatalf("unexpected config args: %#v %#v", app.Config.Init, devices)
	}
}

func TestKongParsesUpgradeCommand(t *testing.T) {
	t.Parallel()

	var app cli
	parser, err := newParser(&app, func(int) {})
	if err != nil {
		t.Fatalf("new parser: %v", err)
	}
	ctx, err := parser.Parse([]string{"upgrade"})
	if err != nil {
		t.Fatalf("parse upgrade: %v", err)
	}
	if ctx.Command() != "upgrade" {
		t.Fatalf("unexpected command: %s", ctx.Command())
	}
}
