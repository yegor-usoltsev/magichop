package upgrade

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCheckPrintsLatestVersionOnly(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/latest" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		fmt.Fprintf(w, `{"tag_name":"v1.2.3","assets":[]}`)
	}))
	defer server.Close()

	var out bytes.Buffer
	called := false
	err := run(context.Background(), Options{Check: true}, deps{
		client:  server.Client(),
		apiBase: server.URL,
		stdout:  &out,
		executable: func() (string, error) {
			called = true
			return "", nil
		},
		restart: func(context.Context, string) error {
			called = true
			return nil
		},
	})
	if err != nil {
		t.Fatalf("run check: %v", err)
	}
	if got := strings.TrimSpace(out.String()); got != "v1.2.3" {
		t.Fatalf("output = %q, want v1.2.3", got)
	}
	if called {
		t.Fatal("check should not touch executable or restart")
	}
}

func TestRunDownloadsVerifiesAndReplacesBinary(t *testing.T) {
	archive := tarGz(t, "magichop", []byte("new binary"))
	sum := sha256.Sum256(archive)
	checksums := hex.EncodeToString(sum[:]) + "  magichop_1.2.3_darwin_arm64.tar.gz\n"

	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/tags/v1.2.3":
			fmt.Fprintf(w, `{"tag_name":"v1.2.3","assets":[{"name":"magichop_1.2.3_darwin_arm64.tar.gz","browser_download_url":"%s/archive"},{"name":"checksums.txt","browser_download_url":"%s/checksums"}]}`, server.URL, server.URL)
		case "/archive":
			_, _ = w.Write(archive)
		case "/checksums":
			_, _ = w.Write([]byte(checksums))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	exe := filepath.Join(t.TempDir(), "magichop")
	if err := os.WriteFile(exe, []byte("old binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	restarted := false
	var out bytes.Buffer
	err := run(context.Background(), Options{Version: "1.2.3"}, deps{
		client:  server.Client(),
		apiBase: server.URL,
		goos:    "darwin",
		goarch:  "arm64",
		stdout:  &out,
		executable: func() (string, error) {
			return exe, nil
		},
		restart: func(context.Context, string) error {
			restarted = true
			return nil
		},
	})
	if err != nil {
		t.Fatalf("run upgrade: %v", err)
	}
	if got := readFile(t, exe); got != "new binary" {
		t.Fatalf("binary = %q", got)
	}
	if got := readFile(t, exe+".old"); got != "old binary" {
		t.Fatalf("old binary = %q", got)
	}
	if !restarted {
		t.Fatal("expected restart")
	}
	if got := strings.TrimSpace(out.String()); got != "upgraded to v1.2.3" {
		t.Fatalf("output = %q", got)
	}
}

func TestChecksumMismatchRefusesReplacement(t *testing.T) {
	archive := tarGz(t, "magichop", []byte("new binary"))
	checksums := strings.Repeat("0", 64) + "  magichop_1.2.3_darwin_arm64.tar.gz\n"

	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/latest":
			fmt.Fprintf(w, `{"tag_name":"v1.2.3","assets":[{"name":"magichop_1.2.3_darwin_arm64.tar.gz","browser_download_url":"%s/archive"},{"name":"checksums.txt","browser_download_url":"%s/checksums"}]}`, server.URL, server.URL)
		case "/archive":
			_, _ = w.Write(archive)
		case "/checksums":
			_, _ = w.Write([]byte(checksums))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	exe := filepath.Join(t.TempDir(), "magichop")
	if err := os.WriteFile(exe, []byte("old binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	err := run(context.Background(), Options{}, deps{
		client:  server.Client(),
		apiBase: server.URL,
		goos:    "darwin",
		goarch:  "arm64",
		stdout:  &bytes.Buffer{},
		executable: func() (string, error) {
			return exe, nil
		},
		restart: func(context.Context, string) error {
			t.Fatal("restart should not run after checksum mismatch")
			return nil
		},
	})
	if err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("error = %v, want checksum mismatch", err)
	}
	if got := readFile(t, exe); got != "old binary" {
		t.Fatalf("binary changed to %q", got)
	}
	if _, err := os.Stat(exe + ".old"); !os.IsNotExist(err) {
		t.Fatalf("old backup exists after failed verification: %v", err)
	}
}

func TestChecksumForSupportsCommonFormats(t *testing.T) {
	sum := strings.Repeat("a", 64)
	cases := []string{
		sum + "  magichop_1.2.3_darwin_arm64.tar.gz",
		sum + " *magichop_1.2.3_darwin_arm64.tar.gz",
		"SHA256 (magichop_1.2.3_darwin_arm64.tar.gz) = " + sum,
	}
	for _, input := range cases {
		got, ok := checksumFor("magichop_1.2.3_darwin_arm64.tar.gz", input)
		if !ok || got != sum {
			t.Fatalf("checksumFor(%q) = %q, %t", input, got, ok)
		}
	}
}

func TestSelectArchiveAssetMatchesPlatform(t *testing.T) {
	asset, err := selectArchiveAsset([]githubAsset{
		{Name: "checksums.txt"},
		{Name: "magichop_1.2.3_linux_amd64.tar.gz"},
		{Name: "magichop_1.2.3_darwin_arm64.tar.gz"},
	}, "darwin", "arm64")
	if err != nil {
		t.Fatal(err)
	}
	if asset.Name != "magichop_1.2.3_darwin_arm64.tar.gz" {
		t.Fatalf("asset = %s", asset.Name)
	}
}

func tarGz(t *testing.T, name string, content []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o755, Size: int64(len(content))}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(content); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}
