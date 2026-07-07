package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/knadh/koanf/parsers/json"
	"github.com/knadh/koanf/providers/env"
	"github.com/knadh/koanf/providers/file"
	"github.com/knadh/koanf/v2"
)

const EnvConfig = "MAGICHOP_CONFIG"

type Timeouts struct {
	WholeClaim       time.Duration `koanf:"whole_claim" json:"whole_claim"`
	ReleaseStartWait time.Duration `koanf:"release_start_wait" json:"release_start_wait"`
	ReleaseWindow    time.Duration `koanf:"release_window" json:"release_window"`
	Pair             time.Duration `koanf:"pair" json:"pair"`
	ConnectAttempt   time.Duration `koanf:"connect_attempt" json:"connect_attempt"`
}

type Config struct {
	NodeName       string            `koanf:"node_name" json:"node_name"`
	CoordinatorURL string            `koanf:"coordinator_url" json:"coordinator_url"`
	AuthToken      string            `koanf:"auth_token" json:"auth_token"`
	DefaultDevice  string            `koanf:"default_device" json:"default_device"`
	Devices        map[string]string `koanf:"devices" json:"devices"`
	Timeouts       Timeouts          `koanf:"timeouts" json:"timeouts"`
}

type LoadOptions struct {
	Path     string
	FlagData map[string]any
}

func DefaultPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(".config", "magichop", "config.json")
	}
	return filepath.Join(home, ".config", "magichop", "config.json")
}

func Defaults() Config {
	return Config{
		CoordinatorURL: "nats://127.0.0.1:4222",
		Devices:        map[string]string{},
		Timeouts: Timeouts{
			WholeClaim:       11 * time.Second,
			ReleaseStartWait: 300 * time.Millisecond,
			ReleaseWindow:    2 * time.Second,
			Pair:             5 * time.Second,
			ConnectAttempt:   2 * time.Second,
		},
	}
}

func Load(opts LoadOptions) (Config, string, error) {
	k := koanf.New(".")
	if err := loadDefaults(k); err != nil {
		return Config{}, "", err
	}

	path := opts.Path
	if path == "" {
		path = os.Getenv(EnvConfig)
	}
	if path == "" {
		path = DefaultPath()
	}

	if _, err := os.Stat(path); err == nil {
		if err := CheckSecretFileMode(path); err != nil {
			return Config{}, path, err
		}
		if err := k.Load(file.Provider(path), json.Parser()); err != nil {
			return Config{}, path, err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return Config{}, path, err
	}

	if err := k.Load(env.Provider("MAGICHOP_", ".", func(s string) string {
		return strings.ToLower(strings.TrimPrefix(s, "MAGICHOP_"))
	}), nil); err != nil {
		return Config{}, path, err
	}

	for key, val := range opts.FlagData {
		if val != nil {
			_ = k.Set(key, val)
		}
	}

	var cfg Config
	if err := k.Unmarshal("", &cfg); err != nil {
		return Config{}, path, err
	}
	if cfg.Devices == nil {
		cfg.Devices = map[string]string{}
	}
	for alias, addr := range cfg.Devices {
		norm, err := NormalizeAddress(addr)
		if err != nil {
			return Config{}, path, fmt.Errorf("%s: %w", alias, err)
		}
		cfg.Devices[alias] = norm
	}
	return cfg, path, nil
}

func loadDefaults(k *koanf.Koanf) error {
	cfg := Defaults()
	values := map[string]any{
		"coordinator_url":             cfg.CoordinatorURL,
		"devices":                     cfg.Devices,
		"timeouts.whole_claim":        cfg.Timeouts.WholeClaim,
		"timeouts.release_start_wait": cfg.Timeouts.ReleaseStartWait,
		"timeouts.release_window":     cfg.Timeouts.ReleaseWindow,
		"timeouts.pair":               cfg.Timeouts.Pair,
		"timeouts.connect_attempt":    cfg.Timeouts.ConnectAttempt,
	}
	for key, val := range values {
		if err := k.Set(key, val); err != nil {
			return err
		}
	}
	return nil
}

func CheckSecretFileMode(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if info.Mode().Perm()&0o077 != 0 {
		return fmt.Errorf("config file %s has unsafe permissions: %w", path, os.ErrPermission)
	}
	return nil
}

var bluetoothAddressPattern = regexp.MustCompile(`(?i)^[0-9a-f]{2}([:-])[0-9a-f]{2}([:-])[0-9a-f]{2}([:-])[0-9a-f]{2}([:-])[0-9a-f]{2}([:-])[0-9a-f]{2}$`)

func NormalizeAddress(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	matches := bluetoothAddressPattern.FindStringSubmatch(raw)
	if matches == nil {
		return "", fmt.Errorf("invalid bluetooth address")
	}
	for i := 2; i < len(matches); i++ {
		if matches[i] != matches[1] {
			return "", fmt.Errorf("invalid bluetooth address")
		}
	}
	parts := strings.FieldsFunc(raw, func(r rune) bool { return r == ':' || r == '-' })
	return strings.ToLower(strings.Join(parts, ":")), nil
}

func ResolveDevice(cfg Config, input string) (alias string, address string, err error) {
	if input == "" {
		input = cfg.DefaultDevice
	}
	if input == "" {
		return "", "", fmt.Errorf("device required")
	}
	if addr, ok := cfg.Devices[input]; ok {
		return input, addr, nil
	}
	addr, err := NormalizeAddress(input)
	if err != nil {
		return "", "", err
	}
	return "", addr, nil
}
