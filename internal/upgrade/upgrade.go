package upgrade

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/yegor-usoltsev/magichop/internal/daemon"
)

const (
	githubAPI             = "https://api.github.com/repos/yegor-usoltsev/magichop/releases"
	launchAgentLabel      = "dev.magichop.daemon"
	checksumAssetName     = "checksums.sha256.txt"
	restartSocketTimeout  = 5 * time.Second
	restartSocketInterval = 200 * time.Millisecond
)

type Options struct {
	Check   bool
	Version string
	Yes     bool
}

type githubRelease struct {
	TagName string        `json:"tag_name"`
	Name    string        `json:"name"`
	Assets  []githubAsset `json:"assets"`
}

type githubAsset struct {
	Name        string `json:"name"`
	DownloadURL string `json:"browser_download_url"`
}

type deps struct {
	client     *http.Client
	apiBase    string
	goos       string
	goarch     string
	stdout     io.Writer
	executable func() (string, error)
	restart    func(context.Context, string) error
}

func Run(ctx context.Context, opts Options) error {
	d := deps{
		client:     http.DefaultClient,
		apiBase:    githubAPI,
		goos:       runtime.GOOS,
		goarch:     runtime.GOARCH,
		stdout:     os.Stdout,
		executable: currentExecutable,
		restart:    restartLaunchAgentIfInstalled,
	}
	return run(ctx, opts, d)
}

func run(ctx context.Context, opts Options, d deps) error {
	if d.client == nil {
		d.client = http.DefaultClient
	}
	if d.apiBase == "" {
		d.apiBase = githubAPI
	}
	if d.stdout == nil {
		d.stdout = io.Discard
	}
	if d.goos == "" {
		d.goos = runtime.GOOS
	}
	if d.goarch == "" {
		d.goarch = runtime.GOARCH
	}
	if d.executable == nil {
		d.executable = currentExecutable
	}
	if d.restart == nil {
		d.restart = restartLaunchAgentIfInstalled
	}

	rel, err := fetchRelease(ctx, d.client, d.apiBase, opts.Version)
	if err != nil {
		return err
	}
	if opts.Check {
		_, err := fmt.Fprintln(d.stdout, rel.TagName)
		return err
	}

	archiveAsset, err := selectArchiveAsset(rel.Assets, d.goos, d.goarch)
	if err != nil {
		return err
	}
	checksumsAsset, err := selectChecksumAsset(rel.Assets)
	if err != nil {
		return err
	}

	archiveData, err := download(ctx, d.client, archiveAsset.DownloadURL)
	if err != nil {
		return fmt.Errorf("download %s: %w", archiveAsset.Name, err)
	}
	checksumsData, err := download(ctx, d.client, checksumsAsset.DownloadURL)
	if err != nil {
		return fmt.Errorf("download %s: %w", checksumsAsset.Name, err)
	}
	if err := verifyChecksum(archiveAsset.Name, archiveData, string(checksumsData)); err != nil {
		return err
	}
	binary, err := extractBinary(archiveAsset.Name, archiveData)
	if err != nil {
		return err
	}

	exe, err := d.executable()
	if err != nil {
		return err
	}
	oldPath, err := replaceBinary(exe, binary)
	if err != nil {
		return err
	}

	if err := d.restart(ctx, oldPath); err != nil {
		if restoreErr := restoreOldBinary(exe, oldPath); restoreErr != nil {
			return fmt.Errorf("restart failed after upgrade: %w; restore failed: %v", err, restoreErr)
		}
		if restartErr := d.restart(ctx, oldPath); restartErr != nil {
			return fmt.Errorf("restart failed after upgrade: %w; restored old binary but restart failed: %v", err, restartErr)
		}
		return fmt.Errorf("restart failed after upgrade; restored old binary: %w", err)
	}

	_, err = fmt.Fprintf(d.stdout, "upgraded to %s\n", rel.TagName)
	return err
}

func fetchRelease(ctx context.Context, client *http.Client, apiBase, version string) (githubRelease, error) {
	path := "/latest"
	if version != "" {
		path = "/tags/" + normalizeVersion(version)
	}
	var rel githubRelease
	if err := getJSON(ctx, client, strings.TrimRight(apiBase, "/")+path, &rel); err != nil {
		return githubRelease{}, err
	}
	if rel.TagName == "" {
		return githubRelease{}, errors.New("GitHub release response missing tag_name")
	}
	return rel, nil
}

func getJSON(ctx context.Context, client *http.Client, url string, out any) error {
	data, err := download(ctx, client, url)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, out)
}

func normalizeVersion(version string) string {
	version = strings.TrimSpace(version)
	if version == "" || strings.HasPrefix(version, "v") {
		return version
	}
	return "v" + version
}

func selectArchiveAsset(assets []githubAsset, goos, goarch string) (githubAsset, error) {
	archNames := []string{strings.ToLower(goarch)}
	switch goarch {
	case "amd64":
		archNames = append(archNames, "x86_64")
	case "arm64":
		archNames = append(archNames, "aarch64")
	}
	goos = strings.ToLower(goos)
	for _, asset := range assets {
		name := strings.ToLower(asset.Name)
		if !(strings.HasSuffix(name, ".tar.gz") || strings.HasSuffix(name, ".zip")) {
			continue
		}
		if !strings.Contains(name, goos) {
			continue
		}
		for _, arch := range archNames {
			if strings.Contains(name, arch) {
				return asset, nil
			}
		}
	}
	return githubAsset{}, fmt.Errorf("no release archive for %s/%s", goos, goarch)
}

func selectChecksumAsset(assets []githubAsset) (githubAsset, error) {
	for _, asset := range assets {
		if strings.EqualFold(asset.Name, checksumAssetName) {
			return asset, nil
		}
	}
	return githubAsset{}, errors.New("release is missing checksums.sha256.txt")
}

func download(ctx context.Context, client *http.Client, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json, application/octet-stream")
	req.Header.Set("User-Agent", "magichop-upgrade")
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, fmt.Errorf("HTTP %s", resp.Status)
	}
	return io.ReadAll(resp.Body)
}

func verifyChecksum(filename string, data []byte, checksums string) error {
	want, ok := checksumFor(filename, checksums)
	if !ok {
		return fmt.Errorf("checksum for %s not found", filename)
	}
	sum := sha256.Sum256(data)
	got := hex.EncodeToString(sum[:])
	if !strings.EqualFold(got, want) {
		return fmt.Errorf("checksum mismatch for %s", filename)
	}
	return nil
}

func checksumFor(filename, checksums string) (string, bool) {
	base := filepath.Base(filename)
	for _, line := range strings.Split(checksums, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "SHA256 (") {
			if got, ok := parseBSDChecksum(line); ok && got.name == base {
				return got.sum, true
			}
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		name := strings.TrimPrefix(fields[len(fields)-1], "*")
		if filepath.Base(name) == base && isSHA256(fields[0]) {
			return fields[0], true
		}
	}
	return "", false
}

type checksumLine struct {
	name string
	sum  string
}

func parseBSDChecksum(line string) (checksumLine, bool) {
	const prefix = "SHA256 ("
	idx := strings.Index(line, ") = ")
	if idx < len(prefix) {
		return checksumLine{}, false
	}
	name := line[len(prefix):idx]
	sum := strings.TrimSpace(line[idx+4:])
	if !isSHA256(sum) {
		return checksumLine{}, false
	}
	return checksumLine{name: filepath.Base(name), sum: sum}, true
}

func isSHA256(value string) bool {
	if len(value) != sha256.Size*2 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func extractBinary(assetName string, archive []byte) ([]byte, error) {
	switch {
	case strings.HasSuffix(strings.ToLower(assetName), ".zip"):
		return extractBinaryFromZip(archive)
	case strings.HasSuffix(strings.ToLower(assetName), ".tar.gz"):
		return extractBinaryFromTarGz(archive)
	default:
		return nil, fmt.Errorf("unsupported archive format %s", assetName)
	}
}

func extractBinaryFromTarGz(archive []byte) ([]byte, error) {
	gz, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		return nil, err
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		header, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, err
		}
		if header.FileInfo().IsDir() || !isBinaryName(header.Name) {
			continue
		}
		return io.ReadAll(tr)
	}
	return nil, errors.New("archive does not contain magichop binary")
}

func extractBinaryFromZip(archive []byte) ([]byte, error) {
	zr, err := zip.NewReader(bytes.NewReader(archive), int64(len(archive)))
	if err != nil {
		return nil, err
	}
	for _, file := range zr.File {
		if file.FileInfo().IsDir() || !isBinaryName(file.Name) {
			continue
		}
		rc, err := file.Open()
		if err != nil {
			return nil, err
		}
		data, readErr := io.ReadAll(rc)
		closeErr := rc.Close()
		if readErr != nil {
			return nil, readErr
		}
		if closeErr != nil {
			return nil, closeErr
		}
		return data, nil
	}
	return nil, errors.New("archive does not contain magichop binary")
}

func isBinaryName(name string) bool {
	base := filepath.Base(name)
	return base == "magichop" || base == "magichop.exe"
}

func currentExecutable() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		return resolved, nil
	}
	return exe, nil
}

func replaceBinary(exe string, binary []byte) (string, error) {
	candidate := exe + ".new"
	old := exe + ".old"
	if err := os.Remove(candidate); err != nil && !os.IsNotExist(err) {
		return "", err
	}
	if err := os.Remove(old); err != nil && !os.IsNotExist(err) {
		return "", err
	}
	if err := os.WriteFile(candidate, binary, 0o755); err != nil {
		return "", err
	}
	if err := os.Chmod(candidate, 0o755); err != nil {
		_ = os.Remove(candidate)
		return "", err
	}
	if err := os.Rename(exe, old); err != nil {
		_ = os.Remove(candidate)
		return "", err
	}
	if err := os.Rename(candidate, exe); err != nil {
		_ = os.Rename(old, exe)
		_ = os.Remove(candidate)
		return "", err
	}
	return old, nil
}

func restoreOldBinary(exe, old string) error {
	if _, err := os.Stat(old); err != nil {
		return err
	}
	if err := os.Remove(exe); err != nil && !os.IsNotExist(err) {
		return err
	}
	return os.Rename(old, exe)
}

func restartLaunchAgentIfInstalled(ctx context.Context, _ string) error {
	plist, err := launchAgentPlist()
	if err != nil {
		return err
	}
	if _, err := os.Stat(plist); os.IsNotExist(err) {
		return nil
	} else if err != nil {
		return err
	}
	if runtime.GOOS != "darwin" {
		return nil
	}
	_ = exec.CommandContext(ctx, "launchctl", "unload", plist).Run()
	if err := exec.CommandContext(ctx, "launchctl", "load", plist).Run(); err != nil {
		return err
	}
	return waitForDaemonSocket(ctx, daemon.SocketPath(), restartSocketTimeout)
}

func launchAgentPlist() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "Library", "LaunchAgents", launchAgentLabel+".plist"), nil
}

func waitForDaemonSocket(ctx context.Context, socket string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	var last error
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("unix", socket, restartSocketInterval)
		if err == nil {
			return conn.Close()
		}
		last = err
		timer := time.NewTimer(restartSocketInterval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
	if last == nil {
		last = os.ErrDeadlineExceeded
	}
	return fmt.Errorf("daemon socket unavailable after restart: %w", last)
}
