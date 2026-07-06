package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"time"

	"github.com/kelseyhightower/envconfig"

	"github.com/yegor-usoltsev/MagicHop/internal/protocol"
)

const (
	envPrefix        = "MAGICHOP"
	defaultConfigRel = ".config/magichop/config.json"
)

type ClientConfig struct {
	NodeName          string            `json:"node_name"`
	CoordinatorURL    string            `json:"coordinator_url"`
	AuthToken         string            `json:"auth_token"`
	Devices           map[string]string `json:"devices"`
	DefaultDevice     string            `json:"default_device"`
	ClaimTimeout      Duration          `json:"claim_timeout"`
	ConnectTimeout    Duration          `json:"connect_timeout"`
	DisconnectTimeout Duration          `json:"disconnect_timeout"`
}

type ServerConfig struct {
	ServerHost string `split_words:"true" required:"true" default:"0.0.0.0"`
	ServerPort uint16 `split_words:"true" required:"true" default:"4222"`
	AuthToken  string `split_words:"true" required:"true"`
}

type Duration struct {
	time.Duration
}

func (d Duration) MarshalJSON() ([]byte, error) {
	raw, err := json.Marshal(d.String())
	if err != nil {
		return nil, fmt.Errorf("marshal duration: %w", err)
	}
	return raw, nil
}

func (d *Duration) UnmarshalJSON(raw []byte) error {
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return fmt.Errorf("unmarshal duration: %w", err)
	}
	parsed, err := time.ParseDuration(value)
	if err != nil {
		return fmt.Errorf("parse duration: %w", err)
	}
	d.Duration = parsed
	return nil
}

func DefaultPath() (string, error) {
	if path := os.Getenv("MAGICHOP_CONFIG"); path != "" {
		return path, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("find home directory: %w", err)
	}
	return filepath.Join(home, defaultConfigRel), nil
}

func DefaultClientConfig() ClientConfig {
	return ClientConfig{
		NodeName:          "",
		CoordinatorURL:    "nats://coordinator.local:4222",
		AuthToken:         "",
		Devices:           map[string]string{},
		DefaultDevice:     "",
		ClaimTimeout:      Duration{Duration: 6 * time.Second},
		ConnectTimeout:    Duration{Duration: 60 * time.Second},
		DisconnectTimeout: Duration{Duration: 6 * time.Second},
	}
}

func InitClientConfig() ClientConfig {
	cfg := DefaultClientConfig()
	cfg.AuthToken = "CHANGE_ME"
	cfg.Devices = map[string]string{
		"device": "aa-bb-cc-dd-ee-ff",
	}
	cfg.DefaultDevice = "device"
	return cfg
}

func LoadClient(path string) (ClientConfig, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return ClientConfig{}, fmt.Errorf("read config: %w", err)
	}
	cfg := DefaultClientConfig()
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return ClientConfig{}, fmt.Errorf("decode config: %w", err)
	}
	if cfg.NodeName == "" {
		host, err := os.Hostname()
		if err != nil {
			return ClientConfig{}, fmt.Errorf("find hostname: %w", err)
		}
		cfg.NodeName = host
	}
	if err := cfg.Validate(); err != nil {
		return ClientConfig{}, err
	}
	return cfg, nil
}

func WriteClient(path string, cfg ClientConfig, overwrite bool) error {
	if !overwrite {
		if _, err := os.Stat(path); err == nil {
			return fmt.Errorf("config exists: %s", path)
		} else if !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("stat config: %w", err)
		}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create config dir: %w", err)
	}
	raw, err := json.MarshalIndent(cfg, "", "  ") //nolint:gosec // config file intentionally stores the local NATS token
	if err != nil {
		return fmt.Errorf("encode config: %w", err)
	}
	raw = append(raw, '\n')
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		return fmt.Errorf("write config: %w", err)
	}
	return nil
}

func (cfg ClientConfig) Validate() error {
	if cfg.CoordinatorURL == "" {
		return errors.New("coordinator_url is required")
	}
	if cfg.AuthToken == "" || cfg.AuthToken == "CHANGE_ME" {
		return errors.New("auth_token must be set")
	}
	if len(cfg.Devices) == 0 {
		return errors.New("at least one device is required")
	}
	if cfg.DefaultDevice != "" {
		if _, ok := cfg.Devices[cfg.DefaultDevice]; !ok && !protocol.IsBluetoothAddress(cfg.DefaultDevice) {
			return fmt.Errorf("default_device is not configured: %s", cfg.DefaultDevice)
		}
	}
	for alias, address := range cfg.Devices {
		if alias == "" {
			return errors.New("device alias cannot be empty")
		}
		if !protocol.IsBluetoothAddress(address) {
			return fmt.Errorf("device %s has invalid bluetooth address: %s", alias, address)
		}
	}
	if cfg.ClaimTimeout.Duration <= 0 {
		return errors.New("claim_timeout must be positive")
	}
	if cfg.ConnectTimeout.Duration <= 0 {
		return errors.New("connect_timeout must be positive")
	}
	if cfg.DisconnectTimeout.Duration <= 0 {
		return errors.New("disconnect_timeout must be positive")
	}
	return nil
}

func (cfg ClientConfig) ResolveDevice(ref string) (string, error) {
	if ref == "" {
		ref = cfg.DefaultDevice
	}
	if ref == "" {
		return "", errors.New("device is required")
	}
	if address, ok := cfg.Devices[ref]; ok {
		return protocol.NormalizeBluetoothAddress(address), nil
	}
	if protocol.IsBluetoothAddress(ref) {
		return protocol.NormalizeBluetoothAddress(ref), nil
	}
	return "", fmt.Errorf("unknown device alias or address: %s", ref)
}

func NewServerConfigFromEnv() (ServerConfig, error) {
	var cfg ServerConfig
	if err := envconfig.Process(envPrefix, &cfg); err != nil {
		_ = envconfig.Usage(envPrefix, &cfg)
		return cfg, fmt.Errorf("process env config: %w", err)
	}
	return cfg, nil
}

func LocalhostNATSURL(port uint16) string {
	return fmt.Sprintf("nats://127.0.0.1:%d", port)
}

func CoordinatorAddress(host string, port uint16) string {
	return net.JoinHostPort(host, fmt.Sprintf("%d", port))
}
