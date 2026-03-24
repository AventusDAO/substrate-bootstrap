package snapshot

import (
	"archive/tar"
	"compress/bzip2"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/klauspost/compress/zstd"
	"github.com/pierrec/lz4/v4"
	"github.com/ulikunitz/xz"
	"go.uber.org/zap"
)

// versionInURL matches Polkadot snapshot version suffix (e.g. 20260316-011637).
var versionInURL = regexp.MustCompile(`/\d{8}-\d{6}$`)

func parallelTransfers() string {
	n := runtime.NumCPU() * 5
	if n > 50 {
		n = 50
	}
	if n < 1 {
		n = 1
	}
	return strconv.Itoa(n)
}

type Downloader struct {
	logger     *zap.Logger
	httpClient *http.Client
}

const defaultDownloadTimeout = 2 * time.Hour

func NewDownloader(logger *zap.Logger) *Downloader {
	return &Downloader{
		logger: logger.With(zap.String("component", "snapshot")),
		httpClient: &http.Client{
			Timeout: defaultDownloadTimeout,
		},
	}
}

type SyncResult struct {
	Downloaded bool   `json:"downloaded"`
	Skipped    bool   `json:"skipped"`
	Method     string `json:"method,omitempty"`
	URL        string `json:"url"`
	DataPath   string `json:"data_path"`
}

// SyncIfNeeded downloads a snapshot if the data directory is empty.
// Auto-detects the download method based on the URL:
//   - URLs ending with a tar extension (.tar.gz, .tar.lz4, etc.) use streaming tar extraction.
//   - All other URLs (e.g. Polkadot snapshots.polkadot.io) use rclone with a files.txt manifest.
//
// For Polkadot-style base URLs (no version suffix), fetches latest_version.meta.txt to resolve latest snapshot.
func (d *Downloader) SyncIfNeeded(ctx context.Context, snapshotURL, dataPath string) (*SyncResult, error) {
	if snapshotURL == "" {
		return nil, nil
	}

	resolvedURL, err := d.resolveSnapshotURL(ctx, snapshotURL)
	if err != nil {
		return nil, fmt.Errorf("resolving snapshot URL: %w", err)
	}

	result := &SyncResult{URL: resolvedURL, DataPath: dataPath}

	hasData, err := dirHasData(dataPath)
	if err != nil {
		return nil, fmt.Errorf("checking data directory %s: %w", dataPath, err)
	}
	if hasData {
		d.logger.Info("data directory already contains files, skipping snapshot download",
			zap.String("path", dataPath))
		result.Skipped = true
		return result, nil
	}

	d.logger.Info("data directory is empty, downloading snapshot",
		zap.String("url", snapshotURL),
		zap.String("path", dataPath))

	if err := os.MkdirAll(dataPath, 0o750); err != nil {
		return nil, fmt.Errorf("creating data directory %s: %w", dataPath, err)
	}

	if isTarURL(resolvedURL) {
		result.Method = "tar"
		err = d.downloadAndExtractTar(ctx, resolvedURL, dataPath)
	} else {
		result.Method = "rclone"
		err = d.downloadWithRclone(ctx, resolvedURL, dataPath)
	}
	if err != nil {
		return nil, fmt.Errorf("downloading snapshot: %w", err)
	}

	result.Downloaded = true
	d.logger.Info("snapshot downloaded successfully", zap.String("path", dataPath))
	return result, nil
}

const (
	chainspecDownloadTimeout = 5 * time.Minute
	// maxChainspecSizeBytes prevents a misconfigured or malicious URL from filling the data volume.
	maxChainspecSizeBytes = 100 * 1024 * 1024 // 100 MiB
)

// DownloadChainspec fetches a chainspec JSON from url and writes to destPath.
// Skips if destPath exists and force is false.
// Enforces maxChainspecSizeBytes to prevent unbounded disk usage.
func (d *Downloader) DownloadChainspec(ctx context.Context, url, destPath string, force bool) error {
	if url == "" {
		return nil
	}

	fi, err := os.Stat(destPath)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("stat chainspec destination %q: %w", destPath, err)
	}
	if err == nil {
		if !fi.Mode().IsRegular() {
			return fmt.Errorf("chainspec destination %q is not a regular file", destPath)
		}
		if !force {
			d.logger.Info("chainspec already exists, skipping download",
				zap.String("path", destPath))
			return nil
		}
	}

	if err := os.MkdirAll(filepath.Dir(destPath), 0o750); err != nil {
		return fmt.Errorf("creating chainspec directory: %w", err)
	}

	reqCtx, cancel := context.WithTimeout(ctx, chainspecDownloadTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("creating request: %w", err)
	}

	resp, err := d.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("HTTP GET %s: %w", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP GET %s returned status %d", url, resp.StatusCode)
	}

	if cl := resp.Header.Get("Content-Length"); cl != "" {
		if contentLength, err := strconv.ParseInt(cl, 10, 64); err == nil && contentLength > maxChainspecSizeBytes {
			return fmt.Errorf("chainspec too large: content-length %d exceeds limit %d", contentLength, maxChainspecSizeBytes)
		}
	}

	dir := filepath.Dir(destPath)
	tmpFile, err := os.CreateTemp(dir, ".chainspec-*.json.tmp")
	if err != nil {
		return fmt.Errorf("creating temp file: %w", err)
	}
	tmpPath := tmpFile.Name()
	defer func() {
		if tmpFile != nil {
			_ = tmpFile.Close()
		}
		_ = os.Remove(tmpPath)
	}()

	limitedReader := io.LimitReader(resp.Body, maxChainspecSizeBytes+1)
	written, err := io.Copy(tmpFile, limitedReader)
	if err != nil {
		return fmt.Errorf("writing chainspec: %w", err)
	}
	if written > maxChainspecSizeBytes {
		return fmt.Errorf("chainspec too large: wrote %d bytes, limit is %d", written, maxChainspecSizeBytes)
	}
	if err := tmpFile.Sync(); err != nil {
		return fmt.Errorf("syncing chainspec: %w", err)
	}
	if err := tmpFile.Close(); err != nil {
		return fmt.Errorf("closing temp file: %w", err)
	}
	tmpFile = nil

	if force {
		if err := os.Rename(tmpPath, destPath); err != nil {
			return fmt.Errorf("renaming chainspec: %w", err)
		}
	} else {
		if err := os.Link(tmpPath, destPath); err != nil {
			if os.IsExist(err) {
				d.logger.Info("chainspec already exists, keeping existing file",
					zap.String("path", destPath))
				return nil
			}
			return fmt.Errorf("linking chainspec: %w", err)
		}
	}

	d.logger.Info("chainspec downloaded successfully",
		zap.String("url", url),
		zap.String("path", destPath))
	return nil
}

// resolveSnapshotURL fetches latest_version.meta.txt when URL is a base URL (no version suffix).
// Returns the URL unchanged if it already has a version or is a tar URL.
func (d *Downloader) resolveSnapshotURL(ctx context.Context, snapshotURL string) (string, error) {
	snapshotURL = strings.TrimRight(snapshotURL, "/")
	if isTarURL(snapshotURL) || versionInURL.MatchString(snapshotURL) {
		return snapshotURL, nil
	}

	metaURL := snapshotURL + "/latest_version.meta.txt"
	d.logger.Info("resolving latest snapshot version", zap.String("url", metaURL))

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, metaURL, nil)
	if err != nil {
		return "", fmt.Errorf("creating request for %s: %w", metaURL, err)
	}
	resp, err := d.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("fetching %s: %w", metaURL, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("%s returned status %d", metaURL, resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("reading %s: %w", metaURL, err)
	}
	version := strings.TrimSpace(string(body))
	if version == "" {
		d.logger.Warn("latest_version.meta.txt is empty, using base URL as-is", zap.String("url", snapshotURL))
		return snapshotURL, nil
	}
	resolved := snapshotURL + "/" + version
	d.logger.Info("resolved snapshot URL", zap.String("version", version), zap.String("url", resolved))
	return resolved, nil
}

// downloadWithRclone uses rclone to sync a Polkadot-style snapshot.
// The process follows the official instructions from https://snapshots.polkadot.io/:
//  1. Fetch files.txt manifest from the snapshot URL
//  2. Use rclone copy with parallel transfers to download all listed files
func (d *Downloader) downloadWithRclone(ctx context.Context, snapshotURL, destPath string) error {
	snapshotURL = strings.TrimRight(snapshotURL, "/")

	filesListPath := filepath.Join(destPath, "files.txt")

	d.logger.Info("downloading files.txt manifest",
		zap.String("url", snapshotURL+"/files.txt"))

	// #nosec G204 -- rclone fixed argv; URL and dest come from operator snapshot config
	copyURLCmd := exec.CommandContext(ctx, "rclone", "copyurl",
		snapshotURL+"/files.txt", filesListPath)
	copyURLCmd.Stdout = os.Stdout
	copyURLCmd.Stderr = os.Stderr
	if err := copyURLCmd.Run(); err != nil {
		return fmt.Errorf("rclone copyurl files.txt: %w", err)
	}
	defer func() { _ = os.Remove(filesListPath) }()

	transfers := parallelTransfers()
	d.logger.Info("starting rclone copy with parallel transfers",
		zap.String("dest", destPath),
		zap.String("transfers", transfers))

	start := time.Now()
	// #nosec G204 -- rclone fixed argv; snapshot URL and dest from operator config
	rcloneCmd := exec.CommandContext(ctx, "rclone", "copy",
		"--progress",
		"--transfers", transfers,
		"--error-on-no-transfer",
		"--http-url", snapshotURL,
		"--no-traverse",
		"--http-no-head",
		"--disable-http2",
		"--no-gzip-encoding",
		"--size-only",
		"--retries", "6",
		"--retries-sleep", "10s",
		"--files-from", filesListPath,
		":http:", destPath,
	)
	rcloneCmd.Stdout = os.Stdout
	rcloneCmd.Stderr = os.Stderr

	if err := rcloneCmd.Run(); err != nil {
		return fmt.Errorf("rclone copy: %w", err)
	}

	d.logger.Info("rclone download completed", zap.Duration("elapsed", time.Since(start)))
	return nil
}

func (d *Downloader) downloadAndExtractTar(ctx context.Context, url, destPath string) error {
	d.logger.Info("streaming snapshot via tar",
		zap.String("dest", destPath))

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("creating request: %w", err)
	}

	start := time.Now()
	resp, err := d.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("HTTP GET %s: %w", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP GET %s returned status %d", url, resp.StatusCode)
	}

	if resp.ContentLength > 0 {
		d.logger.Info("snapshot download started",
			zap.Int64("size_bytes", resp.ContentLength),
			zap.String("size_human", humanSize(resp.ContentLength)))
	}

	decomp, err := newDecompressor(url, resp.Body)
	if err != nil {
		return fmt.Errorf("decompressor: %w", err)
	}
	defer decomp.Close()

	if err := extractTarSecure(destPath, decomp); err != nil {
		return fmt.Errorf("extracting snapshot: %w", err)
	}

	d.logger.Info("tar extraction completed", zap.Duration("elapsed", time.Since(start)))
	return nil
}

func newDecompressor(url string, r io.Reader) (io.ReadCloser, error) {
	lower := strings.ToLower(url)
	switch {
	case strings.HasSuffix(lower, ".tar.gz") || strings.HasSuffix(lower, ".tgz"):
		return gzip.NewReader(r)
	case strings.HasSuffix(lower, ".tar.lz4"):
		return io.NopCloser(lz4.NewReader(r)), nil
	case strings.HasSuffix(lower, ".tar.zst") || strings.HasSuffix(lower, ".tar.zstd"):
		zr, err := zstd.NewReader(r)
		if err != nil {
			return nil, err
		}
		return zr.IOReadCloser(), nil
	case strings.HasSuffix(lower, ".tar.bz2"):
		return io.NopCloser(bzip2.NewReader(r)), nil
	case strings.HasSuffix(lower, ".tar.xz"):
		xr, err := xz.NewReader(r)
		if err != nil {
			return nil, err
		}
		return io.NopCloser(xr), nil
	case strings.HasSuffix(lower, ".tar"):
		return io.NopCloser(r), nil
	default:
		return gzip.NewReader(r)
	}
}

func extractTarSecure(destPath string, r io.Reader) error {
	absDest, err := filepath.Abs(destPath)
	if err != nil {
		return fmt.Errorf("resolving dest path: %w", err)
	}

	tr := tar.NewReader(r)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("reading tar: %w", err)
		}

		if err := validateTarPath(absDest, hdr); err != nil {
			return err
		}

		target := filepath.Join(absDest, filepath.Clean(hdr.Name))
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o750); err != nil {
				return fmt.Errorf("creating directory %s: %w", hdr.Name, err)
			}
		case tar.TypeReg, tar.TypeRegA: //nolint:staticcheck // TypeRegA for compatibility with legacy archives
			if err := os.MkdirAll(filepath.Dir(target), 0o750); err != nil {
				return fmt.Errorf("creating parent for %s: %w", hdr.Name, err)
			}
			if err := extractTarRegularFile(target, tr, hdr); err != nil {
				return err
			}
		case tar.TypeSymlink:
			if err := validateSymlinkTarget(absDest, hdr.Name, hdr.Linkname); err != nil {
				return err
			}
			if err := os.MkdirAll(filepath.Dir(target), 0o750); err != nil {
				return fmt.Errorf("creating parent for symlink %s: %w", hdr.Name, err)
			}
			if err := os.Symlink(hdr.Linkname, target); err != nil {
				return fmt.Errorf("creating symlink %s: %w", hdr.Name, err)
			}
		}
	}
	return nil
}

func tarEntryPerm(hdr *tar.Header) os.FileMode {
	const mask int64 = 0o777
	if hdr.Mode < 0 {
		return 0o644
	}
	return os.FileMode(uint32(hdr.Mode & mask))
}

func extractTarRegularFile(target string, tr *tar.Reader, hdr *tar.Header) error {
	perm := tarEntryPerm(hdr)
	// #nosec G304 -- path confined under destination root by validateTarPath before extraction
	f, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, perm)
	if err != nil {
		return fmt.Errorf("creating file %s: %w", hdr.Name, err)
	}
	defer func() { _ = f.Close() }()

	src := io.LimitReader(tr, hdr.Size)
	if _, err := io.Copy(f, src); err != nil {
		return fmt.Errorf("writing file %s: %w", hdr.Name, err)
	}
	return nil
}

func validateTarPath(destPath string, hdr *tar.Header) error {
	if filepath.IsAbs(hdr.Name) {
		return fmt.Errorf("rejecting absolute path in archive: %s", hdr.Name)
	}
	clean := filepath.Clean(hdr.Name)
	target := filepath.Join(destPath, clean)
	rel, err := filepath.Rel(destPath, target)
	if err != nil || strings.HasPrefix(rel, "..") || rel == ".." {
		return fmt.Errorf("rejecting path outside destination: %s", hdr.Name)
	}
	return nil
}

func validateSymlinkTarget(destPath, linkPath, linkname string) error {
	if filepath.IsAbs(linkname) {
		return fmt.Errorf("rejecting absolute symlink target: %s -> %s", linkPath, linkname)
	}
	linkDir := filepath.Dir(filepath.Join(destPath, filepath.Clean(linkPath)))
	resolved := filepath.Clean(filepath.Join(linkDir, linkname))
	rel, err := filepath.Rel(destPath, resolved)
	if err != nil || strings.HasPrefix(rel, "..") || rel == ".." {
		return fmt.Errorf("rejecting symlink escaping destination: %s -> %s", linkPath, linkname)
	}
	return nil
}

var tarExtensions = []string{
	".tar.gz", ".tgz",
	".tar.lz4",
	".tar.zst", ".tar.zstd",
	".tar.bz2",
	".tar.xz",
	".tar",
}

func isTarURL(url string) bool {
	lower := strings.ToLower(url)
	for _, ext := range tarExtensions {
		if strings.HasSuffix(lower, ext) {
			return true
		}
	}
	return false
}

func dirHasData(path string) (bool, error) {
	entries, err := os.ReadDir(path)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return len(entries) > 0, nil
}

func humanSize(bytes int64) string {
	const (
		mb = 1024 * 1024
		gb = 1024 * mb
	)
	switch {
	case bytes >= gb:
		return fmt.Sprintf("%.1f GB", float64(bytes)/float64(gb))
	case bytes >= mb:
		return fmt.Sprintf("%.1f MB", float64(bytes)/float64(mb))
	default:
		return fmt.Sprintf("%d bytes", bytes)
	}
}
