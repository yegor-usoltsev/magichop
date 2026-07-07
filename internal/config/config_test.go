package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestNormalizeAddress(t *testing.T) {
	tests := map[string]string{
		"AA:BB:CC:DD:EE:FF": "aa:bb:cc:dd:ee:ff",
		"aa-bb-cc-dd-ee-ff": "aa:bb:cc:dd:ee:ff",
	}
	for input, want := range tests {
		got, err := NormalizeAddress(input)
		if err != nil {
			t.Fatalf("NormalizeAddress(%q) returned error: %v", input, err)
		}
		if got != want {
			t.Fatalf("NormalizeAddress(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestNormalizeAddressRejectsMixedSeparators(t *testing.T) {
	if _, err := NormalizeAddress("aa:bb-cc:dd:ee:ff"); err == nil {
		t.Fatal("expected mixed separators to fail")
	}
}

func TestLoadPrecedence(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, []byte(`{
		"node_name": "file-node",
		"coordinator_url": "nats://file:4222",
		"auth_token": "secret",
		"default_device": "trackpad",
		"devices": {"trackpad": "AA-BB-CC-DD-EE-FF"}
	}`), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MAGICHOP_NODE_NAME", "env-node")
	cfg, _, err := Load(LoadOptions{Path: path, FlagData: map[string]any{"node_name": "flag-node"}})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.NodeName != "flag-node" {
		t.Fatalf("NodeName = %q, want flag-node", cfg.NodeName)
	}
	if cfg.Devices["trackpad"] != "aa:bb:cc:dd:ee:ff" {
		t.Fatalf("device not normalized: %q", cfg.Devices["trackpad"])
	}
}

func TestCheckSecretFileMode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(`{}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := CheckSecretFileMode(path); err == nil {
		t.Fatal("expected unsafe mode to fail")
	}
}
