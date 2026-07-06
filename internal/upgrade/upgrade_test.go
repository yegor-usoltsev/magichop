package upgrade

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestNewerVersion(t *testing.T) {
	t.Parallel()

	tests := []struct {
		current string
		latest  string
		want    bool
	}{
		{current: "0.0.1", latest: "v0.0.2", want: true},
		{current: "0.0.2", latest: "v0.0.2", want: false},
		{current: "0.1.0", latest: "v0.0.9", want: false},
	}
	for _, tt := range tests {
		got, err := newerVersion(tt.current, tt.latest)
		if err != nil {
			t.Fatalf("newerVersion(%q, %q): %v", tt.current, tt.latest, err)
		}
		if got != tt.want {
			t.Fatalf("newerVersion(%q, %q) = %t, want %t", tt.current, tt.latest, got, tt.want)
		}
	}
}

func TestMatchingAsset(t *testing.T) {
	t.Parallel()

	assets := []asset{
		{Name: "checksums.sha256.txt"},
		{Name: "magichop_0.0.2_darwin_arm64.tar.gz"},
	}
	got, err := matchingAsset(assets, "0.0.2", "darwin", "arm64")
	if err != nil {
		t.Fatalf("matchingAsset: %v", err)
	}
	if got.Name != "magichop_0.0.2_darwin_arm64.tar.gz" {
		t.Fatalf("unexpected asset: %#v", got)
	}
}

func TestChecksumFor(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), checksumAssetName)
	if err := os.WriteFile(path, []byte("abc  other\n123  magichop_0.0.2_darwin_arm64.tar.gz\n"), 0o600); err != nil {
		t.Fatalf("write checksum: %v", err)
	}
	got, err := checksumFor(path, "magichop_0.0.2_darwin_arm64.tar.gz")
	if err != nil {
		t.Fatalf("checksumFor: %v", err)
	}
	if got != "123" {
		t.Fatalf("checksum = %q", got)
	}
}

func TestRunUpgradesFromHTTPRelease(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	executable := filepath.Join(dir, binaryName)
	if err := os.WriteFile(executable, []byte("old"), 0o755); err != nil {
		t.Fatalf("write executable: %v", err)
	}
	assetName := "magichop_0.0.2_" + mustAssetSuffix(t) + ".tar.gz"
	archive := makeTarGz(t, "new")
	sum := sha256.Sum256(archive)
	checksums := []byte(hex.EncodeToString(sum[:]) + "  " + assetName + "\n")

	var serverURL string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/latest":
			_ = json.NewEncoder(w).Encode(release{
				TagName: "v0.0.2",
				Assets: []asset{
					{Name: assetName, DownloadURL: serverURL + "/asset"},
					{Name: checksumAssetName, DownloadURL: serverURL + "/checksums"},
				},
			})
		case "/asset":
			_, _ = w.Write(archive)
		case "/checksums":
			_, _ = w.Write(checksums)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	serverURL = server.URL

	result, err := Run(context.Background(), Options{
		CurrentVersion: "0.0.1",
		ExecutablePath: executable,
		LatestURL:      server.URL + "/latest",
		Client:         server.Client(),
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !result.Updated || result.LatestVersion != "0.0.2" || result.AssetName != assetName {
		t.Fatalf("unexpected result: %#v", result)
	}
	raw, err := os.ReadFile(executable)
	if err != nil {
		t.Fatalf("read executable: %v", err)
	}
	if string(raw) != "new" {
		t.Fatalf("executable = %q", raw)
	}
}

func mustAssetSuffix(t *testing.T) string {
	t.Helper()

	suffix, err := assetSuffix(runtime.GOOS, runtime.GOARCH)
	if err != nil {
		t.Fatalf("assetSuffix: %v", err)
	}
	return suffix
}

func makeTarGz(t *testing.T, content string) []byte {
	t.Helper()

	var raw bytes.Buffer
	gz := gzip.NewWriter(&raw)
	tw := tar.NewWriter(gz)
	data := []byte(content)
	if err := tw.WriteHeader(&tar.Header{Name: binaryName, Mode: 0o755, Size: int64(len(data))}); err != nil {
		t.Fatalf("write header: %v", err)
	}
	if _, err := tw.Write(data); err != nil {
		t.Fatalf("write tar: %v", err)
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("close tar: %v", err)
	}
	if err := gz.Close(); err != nil {
		t.Fatalf("close gzip: %v", err)
	}
	return raw.Bytes()
}
