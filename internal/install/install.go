package install

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/yegor-usoltsev/magichop/internal/config"
	"github.com/yegor-usoltsev/magichop/internal/daemon"
	"github.com/yegor-usoltsev/magichop/internal/state"
)

func Mac(configPath, binary string) error {
	if err := os.MkdirAll(filepath.Dir(configPath), 0o700); err != nil {
		return err
	}
	if err := EnsureConfig(configPath); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(state.DefaultPath()), 0o700); err != nil {
		return err
	}
	logPath := filepath.Join(mustHome(), "Library", "Logs", "MagicHop")
	if err := os.MkdirAll(logPath, 0o700); err != nil {
		return err
	}
	plist := filepath.Join(mustHome(), "Library", "LaunchAgents", "dev.magichop.daemon.plist")
	if err := os.MkdirAll(filepath.Dir(plist), 0o700); err != nil {
		return err
	}
	if err := os.WriteFile(plist, []byte(launchAgent(binary, configPath)), 0o644); err != nil {
		return err
	}
	if err := exec.Command("launchctl", "load", plist).Run(); err != nil {
		return err
	}
	conn, err := net.DialTimeout("unix", daemon.SocketPath(), 5*time.Second)
	if err != nil {
		return err
	}
	return conn.Close()
}

func Raycast(dir string, cfg config.Config, binary string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	for alias := range cfg.Devices {
		path := filepath.Join(dir, "magichop-claim-"+alias+".sh")
		content := fmt.Sprintf("#!/bin/bash\n# @raycast.schemaVersion 1\n# @raycast.title Claim %s\n# @raycast.mode compact\n# @raycast.packageName MagicHop\n\n%s claim %s\n", alias, binary, alias)
		if err := os.WriteFile(path, []byte(content), 0o700); err != nil {
			return err
		}
	}
	return nil
}

func UninstallMac() error {
	plist := filepath.Join(mustHome(), "Library", "LaunchAgents", "dev.magichop.daemon.plist")
	_ = exec.Command("launchctl", "unload", plist).Run()
	if err := os.Remove(plist); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func EditConfig(path string) error {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		if err := EnsureConfig(path); err != nil {
			return err
		}
	}
	editor := os.Getenv("EDITOR")
	if editor == "" {
		return fmt.Errorf("$EDITOR is unavailable")
	}
	cmd := exec.Command(editor, path)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func EnsureConfig(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	if _, err := os.Stat(path); err == nil {
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}
	cfg := config.Defaults()
	cfg.AuthToken = "CHANGE_ME"
	cfg.DefaultDevice = "trackpad"
	cfg.Devices = map[string]string{"trackpad": "aa:bb:cc:dd:ee:ff"}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}

func launchAgent(binary, configPath string) string {
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
<key>Label</key><string>dev.magichop.daemon</string>
<key>ProgramArguments</key><array><string>%s</string><string>daemon</string><string>--config</string><string>%s</string></array>
<key>RunAtLoad</key><true/>
<key>KeepAlive</key><true/>
</dict></plist>
`, binary, configPath)
}

func mustHome() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "."
	}
	return home
}
