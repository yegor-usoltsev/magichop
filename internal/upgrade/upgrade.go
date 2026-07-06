package upgrade

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

const (
	DefaultLatestReleaseURL = "https://api.github.com/repos/yegor-usoltsev/MagicHop/releases/latest"
	binaryName              = "magichop"
	checksumAssetName       = "checksums.sha256.txt"
)

type Options struct {
	CurrentVersion string
	ExecutablePath string
	LatestURL      string
	Client         *http.Client
}

type Result struct {
	CurrentVersion string
	LatestVersion  string
	AssetName      string
	Updated        bool
}

type release struct {
	TagName string  `json:"tag_name"`
	Assets  []asset `json:"assets"`
}

type asset struct {
	Name        string `json:"name"`
	DownloadURL string `json:"browser_download_url"`
}

func Run(ctx context.Context, opts Options) (Result, error) {
	if opts.CurrentVersion == "" || opts.CurrentVersion == "dev" {
		return Result{}, errors.New("self-upgrade requires a release build")
	}
	if opts.ExecutablePath == "" {
		path, err := os.Executable()
		if err != nil {
			return Result{}, fmt.Errorf("find executable: %w", err)
		}
		opts.ExecutablePath, err = filepath.EvalSymlinks(path)
		if err != nil {
			return Result{}, fmt.Errorf("resolve executable: %w", err)
		}
	}
	if opts.LatestURL == "" {
		opts.LatestURL = DefaultLatestReleaseURL
	}
	if opts.Client == nil {
		opts.Client = &http.Client{Timeout: 30 * time.Second}
	}

	rel, err := fetchRelease(ctx, opts.Client, opts.LatestURL)
	if err != nil {
		return Result{}, err
	}
	latest := strings.TrimPrefix(rel.TagName, "v")
	result := Result{CurrentVersion: opts.CurrentVersion, LatestVersion: latest}
	newer, err := newerVersion(opts.CurrentVersion, latest)
	if err != nil {
		return Result{}, err
	}
	if !newer {
		return result, nil
	}

	binaryAsset, err := matchingAsset(rel.Assets, latest, runtime.GOOS, runtime.GOARCH)
	if err != nil {
		return Result{}, err
	}
	checksumAsset, err := namedAsset(rel.Assets, checksumAssetName)
	if err != nil {
		return Result{}, err
	}
	result.AssetName = binaryAsset.Name

	dir, err := os.MkdirTemp(filepath.Dir(opts.ExecutablePath), ".magichop-upgrade-*")
	if err != nil {
		return Result{}, fmt.Errorf("create temp dir: %w", err)
	}
	defer os.RemoveAll(dir)

	archivePath := filepath.Join(dir, binaryAsset.Name)
	if err := downloadFile(ctx, opts.Client, binaryAsset.DownloadURL, archivePath); err != nil {
		return Result{}, err
	}
	checksumPath := filepath.Join(dir, checksumAssetName)
	if err := downloadFile(ctx, opts.Client, checksumAsset.DownloadURL, checksumPath); err != nil {
		return Result{}, err
	}
	if err := verifyChecksum(archivePath, checksumPath, binaryAsset.Name); err != nil {
		return Result{}, err
	}

	nextPath := filepath.Join(dir, binaryName)
	if err := extractBinary(archivePath, nextPath); err != nil {
		return Result{}, err
	}
	info, err := os.Stat(opts.ExecutablePath)
	if err != nil {
		return Result{}, fmt.Errorf("stat executable: %w", err)
	}
	if err := os.Chmod(nextPath, info.Mode().Perm()|0o700); err != nil {
		return Result{}, fmt.Errorf("chmod new executable: %w", err)
	}
	if err := os.Rename(nextPath, opts.ExecutablePath); err != nil {
		return Result{}, fmt.Errorf("replace executable: %w", err)
	}
	result.Updated = true
	return result, nil
}

func fetchRelease(ctx context.Context, client *http.Client, url string) (release, error) {
	var rel release
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, http.NoBody)
	if err != nil {
		return rel, fmt.Errorf("create release request: %w", err)
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "magichop")
	resp, err := client.Do(req)
	if err != nil {
		return rel, fmt.Errorf("fetch latest release: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return rel, fmt.Errorf("fetch latest release: %s", resp.Status)
	}
	if err := json.NewDecoder(resp.Body).Decode(&rel); err != nil {
		return rel, fmt.Errorf("decode latest release: %w", err)
	}
	return rel, nil
}

func matchingAsset(assets []asset, version, goos, goarch string) (asset, error) {
	suffix, err := assetSuffix(goos, goarch)
	if err != nil {
		return asset{}, err
	}
	for _, item := range assets {
		if strings.Contains(item.Name, "_"+version+"_"+suffix+".") {
			return item, nil
		}
	}
	return asset{}, fmt.Errorf("release asset not found for %s", suffix)
}

func namedAsset(assets []asset, name string) (asset, error) {
	for _, item := range assets {
		if item.Name == name {
			return item, nil
		}
	}
	return asset{}, fmt.Errorf("release asset not found: %s", name)
}

func assetSuffix(goos, goarch string) (string, error) {
	switch goarch {
	case "arm":
		return goos + "_armv6", nil
	case "386", "amd64", "arm64":
		return goos + "_" + goarch, nil
	default:
		return "", fmt.Errorf("unsupported architecture: %s/%s", goos, goarch)
	}
}

func downloadFile(ctx context.Context, client *http.Client, url, path string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, http.NoBody)
	if err != nil {
		return fmt.Errorf("create download request: %w", err)
	}
	req.Header.Set("User-Agent", "magichop")
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("download %s: %w", filepath.Base(path), err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download %s: %s", filepath.Base(path), resp.Status)
	}
	out, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("create %s: %w", path, err)
	}
	defer out.Close()
	if _, err := io.Copy(out, resp.Body); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}

func verifyChecksum(path, checksumPath, name string) error {
	want, err := checksumFor(checksumPath, name)
	if err != nil {
		return err
	}
	got, err := fileSHA256(path)
	if err != nil {
		return err
	}
	if got != want {
		return fmt.Errorf("checksum mismatch for %s", name)
	}
	return nil
}

func checksumFor(path, name string) (string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read checksums: %w", err)
	}
	for line := range strings.SplitSeq(string(raw), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && fields[1] == name {
			return fields[0], nil
		}
	}
	return "", fmt.Errorf("checksum not found for %s", name)
}

func fileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("open %s: %w", path, err)
	}
	defer f.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, f); err != nil {
		return "", fmt.Errorf("hash %s: %w", path, err)
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func extractBinary(archivePath, outPath string) error {
	switch {
	case strings.HasSuffix(archivePath, ".tar.gz"):
		return extractTarGz(archivePath, outPath)
	case strings.HasSuffix(archivePath, ".zip"):
		return extractZip(archivePath, outPath)
	default:
		return fmt.Errorf("unsupported archive: %s", filepath.Base(archivePath))
	}
}

func extractTarGz(archivePath, outPath string) error {
	f, err := os.Open(archivePath)
	if err != nil {
		return fmt.Errorf("open archive: %w", err)
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return fmt.Errorf("open gzip: %w", err)
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		header, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return fmt.Errorf("read tar: %w", err)
		}
		if filepath.Base(header.Name) != binaryName {
			continue
		}
		return writeExtracted(outPath, tr)
	}
	return errors.New("magichop binary not found in archive")
}

func extractZip(archivePath, outPath string) error {
	zr, err := zip.OpenReader(archivePath)
	if err != nil {
		return fmt.Errorf("open zip: %w", err)
	}
	defer zr.Close()
	for _, file := range zr.File {
		if filepath.Base(file.Name) != binaryName+".exe" {
			continue
		}
		in, err := file.Open()
		if err != nil {
			return fmt.Errorf("open zip entry: %w", err)
		}
		err = writeExtracted(outPath, in)
		if closeErr := in.Close(); closeErr != nil && err == nil {
			return fmt.Errorf("close zip entry: %w", closeErr)
		}
		return err
	}
	return errors.New("magichop binary not found in archive")
}

func writeExtracted(path string, in io.Reader) error {
	out, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("create extracted binary: %w", err)
	}
	defer out.Close()
	if _, err := io.Copy(out, in); err != nil {
		return fmt.Errorf("write extracted binary: %w", err)
	}
	return nil
}

func newerVersion(current, latest string) (bool, error) {
	currentParts, err := versionParts(current)
	if err != nil {
		return false, fmt.Errorf("parse current version: %w", err)
	}
	latestParts, err := versionParts(latest)
	if err != nil {
		return false, fmt.Errorf("parse latest version: %w", err)
	}
	for i := range latestParts {
		if latestParts[i] > currentParts[i] {
			return true, nil
		}
		if latestParts[i] < currentParts[i] {
			return false, nil
		}
	}
	return false, nil
}

func versionParts(value string) ([3]int, error) {
	var parts [3]int
	value = strings.TrimPrefix(value, "v")
	value, _, _ = strings.Cut(value, "-")
	values := strings.Split(value, ".")
	if len(values) != 3 {
		return parts, fmt.Errorf("expected major.minor.patch: %s", value)
	}
	for i, raw := range values {
		part, err := strconv.Atoi(raw)
		if err != nil {
			return parts, fmt.Errorf("parse %s: %w", raw, err)
		}
		parts[i] = part
	}
	return parts, nil
}
