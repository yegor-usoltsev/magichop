package bluetooth

import (
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"
	"time"
)

func TestArgs(t *testing.T) {
	t.Parallel()

	tests := map[string][]string{
		"connect":      {"--connect", "aa"},
		"disconnect":   {"--disconnect", "aa"},
		"is-connected": {"--is-connected", "aa"},
		"pair":         {"--pair", "aa"},
		"unpair":       {"--unpair", "aa"},
	}
	for action, want := range tests {
		if got := Args(action, "aa"); !reflect.DeepEqual(got, want) {
			t.Fatalf("Args(%s) = %#v, want %#v", action, got, want)
		}
	}
}

func TestResultMessage(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		result Result
		want   string
	}{
		{name: "error", result: Result{Error: "boom", Stderr: "stderr", Stdout: "stdout"}, want: "boom"},
		{name: "stderr", result: Result{Stderr: "stderr", Stdout: "stdout"}, want: "stderr"},
		{name: "stdout", result: Result{Stdout: "stdout"}, want: "stdout"},
		{name: "exit", result: Result{ReturnCode: 7}, want: "exit 7"},
	}
	for _, tt := range tests {
		if got := tt.result.Message(); got != tt.want {
			t.Fatalf("%s: got %q, want %q", tt.name, got, tt.want)
		}
	}
}

func TestBlueutilConnectRetries(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell script test")
	}

	dir := t.TempDir()
	logPath := filepath.Join(dir, "log")
	markerPath := filepath.Join(dir, "marker")
	scriptPath := filepath.Join(dir, "blueutil")
	script := `#!/bin/sh
echo "$1 $2" >> "$MAGICHOP_TEST_LOG"
case "$1" in
  --is-connected)
    if [ -f "$MAGICHOP_TEST_MARKER" ]; then
      echo 1
    else
      echo 0
    fi
    exit 0
    ;;
  --unpair|--pair)
    exit 0
    ;;
  --connect)
    if [ ! -f "$MAGICHOP_TEST_MARKER" ]; then
      touch "$MAGICHOP_TEST_MARKER"
      exit 1
    fi
    exit 0
    ;;
esac
exit 64
`
	if err := os.WriteFile(scriptPath, []byte(script), 0o700); err != nil {
		t.Fatalf("write script: %v", err)
	}
	t.Setenv("MAGICHOP_TEST_LOG", logPath)
	t.Setenv("MAGICHOP_TEST_MARKER", markerPath)

	result := Blueutil{Path: scriptPath}.Connect(t.Context(), "aa-bb-cc-dd-ee-ff", 5*time.Second)
	if !result.OK {
		t.Fatalf("Connect failed: %#v", result)
	}
	raw, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	want := "--is-connected aa-bb-cc-dd-ee-ff\n--unpair aa-bb-cc-dd-ee-ff\n--pair aa-bb-cc-dd-ee-ff\n--connect aa-bb-cc-dd-ee-ff\n--connect aa-bb-cc-dd-ee-ff\n--is-connected aa-bb-cc-dd-ee-ff\n"
	if string(raw) != want {
		t.Fatalf("operations = %q, want %q", raw, want)
	}
}

func TestBlueutilConnectVerifiesSuccessfulCommand(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell script test")
	}

	dir := t.TempDir()
	logPath := filepath.Join(dir, "log")
	connectedPath := filepath.Join(dir, "connected")
	seenPath := filepath.Join(dir, "seen")
	scriptPath := filepath.Join(dir, "blueutil")
	script := `#!/bin/sh
echo "$1 $2" >> "$MAGICHOP_TEST_LOG"
case "$1" in
  --is-connected)
    if [ ! -f "$MAGICHOP_TEST_CONNECTED" ]; then
      echo 0
    elif [ ! -f "$MAGICHOP_TEST_SEEN" ]; then
      touch "$MAGICHOP_TEST_SEEN"
      echo 0
    else
      echo 1
    fi
    exit 0
    ;;
  --unpair|--pair)
    exit 0
    ;;
  --connect)
    touch "$MAGICHOP_TEST_CONNECTED"
    exit 0
    ;;
esac
exit 64
`
	if err := os.WriteFile(scriptPath, []byte(script), 0o700); err != nil {
		t.Fatalf("write script: %v", err)
	}
	t.Setenv("MAGICHOP_TEST_LOG", logPath)
	t.Setenv("MAGICHOP_TEST_CONNECTED", connectedPath)
	t.Setenv("MAGICHOP_TEST_SEEN", seenPath)

	result := Blueutil{Path: scriptPath}.Connect(t.Context(), "aa-bb-cc-dd-ee-ff", 5*time.Second)
	if !result.OK {
		t.Fatalf("Connect failed: %#v", result)
	}
	raw, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	want := "--is-connected aa-bb-cc-dd-ee-ff\n--unpair aa-bb-cc-dd-ee-ff\n--pair aa-bb-cc-dd-ee-ff\n--connect aa-bb-cc-dd-ee-ff\n--is-connected aa-bb-cc-dd-ee-ff\n--is-connected aa-bb-cc-dd-ee-ff\n"
	if string(raw) != want {
		t.Fatalf("operations = %q, want %q", raw, want)
	}
}

func TestBlueutilDisconnectUnpairs(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell script test")
	}

	dir := t.TempDir()
	logPath := filepath.Join(dir, "log")
	scriptPath := filepath.Join(dir, "blueutil")
	script := `#!/bin/sh
echo "$1 $2" >> "$MAGICHOP_TEST_LOG"
case "$1" in
  --disconnect|--unpair)
    exit 0
    ;;
  --is-connected)
    echo 0
    exit 0
    ;;
esac
exit 64
`
	if err := os.WriteFile(scriptPath, []byte(script), 0o700); err != nil {
		t.Fatalf("write script: %v", err)
	}
	t.Setenv("MAGICHOP_TEST_LOG", logPath)

	result := Blueutil{Path: scriptPath}.Disconnect(t.Context(), "aa-bb-cc-dd-ee-ff", 5*time.Second)
	if !result.OK {
		t.Fatalf("Disconnect failed: %#v", result)
	}
	raw, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	want := "--disconnect aa-bb-cc-dd-ee-ff\n--unpair aa-bb-cc-dd-ee-ff\n--is-connected aa-bb-cc-dd-ee-ff\n"
	if string(raw) != want {
		t.Fatalf("operations = %q, want %q", raw, want)
	}
}

func TestBlueutilDisconnectWaitsUntilDisconnected(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell script test")
	}

	dir := t.TempDir()
	logPath := filepath.Join(dir, "log")
	markerPath := filepath.Join(dir, "marker")
	scriptPath := filepath.Join(dir, "blueutil")
	script := `#!/bin/sh
echo "$1 $2" >> "$MAGICHOP_TEST_LOG"
case "$1" in
  --disconnect|--unpair)
    exit 0
    ;;
  --is-connected)
    if [ ! -f "$MAGICHOP_TEST_MARKER" ]; then
      touch "$MAGICHOP_TEST_MARKER"
      echo 1
    else
      echo 0
    fi
    exit 0
    ;;
esac
exit 64
`
	if err := os.WriteFile(scriptPath, []byte(script), 0o700); err != nil {
		t.Fatalf("write script: %v", err)
	}
	t.Setenv("MAGICHOP_TEST_LOG", logPath)
	t.Setenv("MAGICHOP_TEST_MARKER", markerPath)

	result := Blueutil{Path: scriptPath}.Disconnect(t.Context(), "aa-bb-cc-dd-ee-ff", 5*time.Second)
	if !result.OK {
		t.Fatalf("Disconnect failed: %#v", result)
	}
	raw, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	want := "--disconnect aa-bb-cc-dd-ee-ff\n--unpair aa-bb-cc-dd-ee-ff\n--is-connected aa-bb-cc-dd-ee-ff\n--is-connected aa-bb-cc-dd-ee-ff\n"
	if string(raw) != want {
		t.Fatalf("operations = %q, want %q", raw, want)
	}
}
