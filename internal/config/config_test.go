package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestResolveDeviceAliasAndAddress(t *testing.T) {
	t.Parallel()

	cfg := testClientConfig()

	got, err := cfg.ResolveDevice("")
	if err != nil {
		t.Fatalf("resolve default: %v", err)
	}
	if got != "aa-bb-cc-dd-ee-ff" {
		t.Fatalf("unexpected default address: %s", got)
	}

	got, err = cfg.ResolveDevice("11:22:33:44:55:66")
	if err != nil {
		t.Fatalf("resolve raw: %v", err)
	}
	if got != "11-22-33-44-55-66" {
		t.Fatalf("unexpected raw address: %s", got)
	}
}

func TestValidateRejectsPlaceholderToken(t *testing.T) {
	t.Parallel()

	cfg := InitClientConfig()
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected validation error")
	}
}

func TestLoadClientDoesNotInheritInitDevice(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(`{"auth_token":"secret"}`+"\n"), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	if _, err := LoadClient(path); err == nil {
		t.Fatal("expected missing device error")
	}
}

func TestWriteAndLoadClient(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "config.json")
	cfg := testClientConfig()
	cfg.NodeName = "test-node"
	cfg.ClaimTimeout.Duration = 2 * time.Second
	if err := WriteClient(path, cfg, false); err != nil {
		t.Fatalf("write config: %v", err)
	}
	loaded, err := LoadClient(path)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if loaded.NodeName != "test-node" || loaded.ClaimTimeout.Duration != 2*time.Second {
		t.Fatalf("unexpected loaded config: %#v", loaded)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	if !strings.Contains(string(raw), `"release_timeout"`) || strings.Contains(string(raw), `"disconnect_timeout"`) {
		t.Fatalf("unexpected timeout keys in config: %s", raw)
	}
	if info, err := os.Stat(path); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("unexpected file mode: %v %v", info, err)
	}
}

func TestLoadClientAcceptsLegacyDisconnectTimeout(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "config.json")
	raw := `{
  "auth_token": "secret",
  "devices": {"trackpad": "aa-bb-cc-dd-ee-ff"},
  "default_device": "trackpad",
  "disconnect_timeout": "9s"
}
`
	if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	loaded, err := LoadClient(path)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if loaded.ReleaseTimeout.Duration != 9*time.Second {
		t.Fatalf("release timeout = %s, want 9s", loaded.ReleaseTimeout.Duration)
	}
}

func testClientConfig() ClientConfig {
	cfg := DefaultClientConfig()
	cfg.AuthToken = "secret"
	cfg.Devices = map[string]string{"headphones": "AA:BB:CC:DD:EE:FF"}
	cfg.DefaultDevice = "headphones"
	return cfg
}

func TestServerConfigFromEnv(t *testing.T) {
	t.Setenv("MAGICHOP_SERVER_HOST", "127.0.0.1")
	t.Setenv("MAGICHOP_SERVER_PORT", "4223")
	t.Setenv("MAGICHOP_AUTH_TOKEN", "secret")

	cfg, err := NewServerConfigFromEnv()
	if err != nil {
		t.Fatalf("load server config: %v", err)
	}
	if cfg.ServerHost != "127.0.0.1" || cfg.ServerPort != 4223 || cfg.AuthToken != "secret" {
		t.Fatalf("unexpected server config: %#v", cfg)
	}
}
