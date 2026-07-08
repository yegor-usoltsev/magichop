package install

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestEnsureConfigOmitsTimeouts(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := EnsureConfig(path); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, []byte(`"timeouts"`)) {
		t.Fatalf("default config should not include timeouts: %s", raw)
	}
	if !bytes.Contains(raw, []byte(`"node_name"`)) {
		t.Fatalf("default config missing node name: %s", raw)
	}
	if !bytes.Contains(raw, []byte(`"default_device": "trackpad"`)) {
		t.Fatalf("default config missing trackpad default: %s", raw)
	}
}

func TestLaunchAgentUsesAbsoluteBinary(t *testing.T) {
	raw := launchAgent("/Users/me/.local/bin/magichop", "/Users/me/.config/magichop/config.json")
	if !bytes.Contains([]byte(raw), []byte("<string>/Users/me/.local/bin/magichop</string>")) {
		t.Fatalf("launch agent missing absolute binary: %s", raw)
	}
	if !bytes.Contains([]byte(raw), []byte("<key>StandardErrorPath</key>")) {
		t.Fatalf("launch agent missing stderr log path: %s", raw)
	}
	if !bytes.Contains([]byte(raw), []byte("/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin")) {
		t.Fatalf("launch agent missing Homebrew PATH: %s", raw)
	}
}
