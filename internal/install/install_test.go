package install

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGenerateLaunchAgentPlist(t *testing.T) {
	t.Parallel()

	raw, err := GenerateLaunchAgentPlist("/Users/me/.local/bin/magichop", "/cfg.json")
	if err != nil {
		t.Fatalf("generate plist: %v", err)
	}
	for _, want := range []string{
		"<string>com.magichop.agent</string>",
		"<string>/Users/me/.local/bin/magichop</string>",
		"<string>daemon</string>",
		"<string>/cfg.json</string>",
	} {
		if !bytes.Contains(raw, []byte(want)) {
			t.Fatalf("plist missing %q: %s", want, raw)
		}
	}
	for _, unwanted := range []string{"StandardOutPath", "StandardErrorPath"} {
		if bytes.Contains(raw, []byte(unwanted)) {
			t.Fatalf("plist contains %q: %s", unwanted, raw)
		}
	}
}

func TestGenerateRaycastScript(t *testing.T) {
	t.Parallel()

	raw, err := GenerateRaycastScript("/Users/me/bin/magic hop")
	if err != nil {
		t.Fatalf("generate raycast script: %v", err)
	}
	text := string(raw)
	for _, want := range []string{
		"#!/bin/bash",
		"# Required parameters:",
		"# @raycast.schemaVersion 1",
		"# @raycast.title Claim Magic Peripheral",
		"# @raycast.mode compact",
		"# @raycast.packageName MagicHop",
		"# Optional parameters:",
		"# @raycast.needsConfirmation false",
		"# Documentation:",
		"# @raycast.description Claim the default configured Magic peripheral through MagicHop.",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing %q: %s", want, text)
		}
	}
	if !strings.Contains(text, "exec '/Users/me/bin/magic hop' claim") {
		t.Fatalf("missing binary call: %s", text)
	}
}

func TestGenerateRaycastScriptEscapesSingleQuote(t *testing.T) {
	t.Parallel()

	raw, err := GenerateRaycastScript("/Users/me/bin/magic'hop")
	if err != nil {
		t.Fatalf("generate raycast script: %v", err)
	}
	if !strings.Contains(string(raw), `exec '/Users/me/bin/magic'\''hop' claim`) {
		t.Fatalf("missing escaped binary call: %s", raw)
	}
}

func TestWriteRaycastScript(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path, err := WriteRaycastScript(dir, "/Users/me/.local/bin/magichop")
	if err != nil {
		t.Fatalf("write raycast script: %v", err)
	}
	if path != filepath.Join(dir, "magichop-claim.sh") {
		t.Fatalf("unexpected script path: %s", path)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat script: %v", err)
	}
	if info.Mode().Perm() != 0o755 {
		t.Fatalf("unexpected mode: %v", info.Mode().Perm())
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read script: %v", err)
	}
	if !strings.Contains(string(raw), "exec '/Users/me/.local/bin/magichop' claim") {
		t.Fatalf("missing binary call: %s", raw)
	}
}
