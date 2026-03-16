package snapshot

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"go.uber.org/zap"
)

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
func (d *Downloader) SyncIfNeeded(ctx context.Context, snapshotURL, dataPath string) (*SyncResult, error) {
	if snapshotURL == "" {
		return nil, nil
	}

	result := &SyncResult{URL: snapshotURL, DataPath: dataPath}

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

	if err := os.MkdirAll(dataPath, 0o755); err != nil {
		return nil, fmt.Errorf("creating data directory %s: %w", dataPath, err)
	}

	if isTarURL(snapshotURL) {
		result.Method = "tar"
		err = d.downloadAndExtractTar(ctx, snapshotURL, dataPath)
	} else {
		result.Method = "rclone"
		err = d.downloadWithRclone(ctx, snapshotURL, dataPath)
	}
	if err != nil {
		return nil, fmt.Errorf("downloading snapshot: %w", err)
	}

	result.Downloaded = true
	d.logger.Info("snapshot downloaded successfully", zap.String("path", dataPath))
	return result, nil
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

	copyURLCmd := exec.CommandContext(ctx, "rclone", "copyurl",
		snapshotURL+"/files.txt", filesListPath)
	copyURLCmd.Stdout = os.Stdout
	copyURLCmd.Stderr = os.Stderr
	if err := copyURLCmd.Run(); err != nil {
		return fmt.Errorf("rclone copyurl files.txt: %w", err)
	}
	defer os.Remove(filesListPath)

	d.logger.Info("starting rclone copy with parallel transfers",
		zap.String("dest", destPath))

	start := time.Now()
	rcloneCmd := exec.CommandContext(ctx, "rclone", "copy",
		"--progress",
		"--transfers", "20",
		"--http-url", snapshotURL,
		"--no-traverse",
		"--http-no-head",
		"--disable-http2",
		"--inplace",
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
	tarFlags := detectTarFlags(url)

	d.logger.Info("streaming snapshot via tar",
		zap.String("tar_flags", tarFlags),
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

	args := append(strings.Fields(tarFlags), "-C", destPath)
	cmd := exec.CommandContext(ctx, "tar", args...)
	cmd.Stdin = resp.Body
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("extracting snapshot with tar: %w", err)
	}

	d.logger.Info("tar extraction completed", zap.Duration("elapsed", time.Since(start)))
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

func detectTarFlags(url string) string {
	lower := strings.ToLower(url)

	switch {
	case strings.HasSuffix(lower, ".tar.gz") || strings.HasSuffix(lower, ".tgz"):
		return "xzf -"
	case strings.HasSuffix(lower, ".tar.lz4"):
		return "--use-compress-program=lz4 -xf -"
	case strings.HasSuffix(lower, ".tar.zst") || strings.HasSuffix(lower, ".tar.zstd"):
		return "--use-compress-program=zstd -xf -"
	case strings.HasSuffix(lower, ".tar.bz2"):
		return "xjf -"
	case strings.HasSuffix(lower, ".tar.xz"):
		return "xJf -"
	case strings.HasSuffix(lower, ".tar"):
		return "xf -"
	default:
		return "xzf -"
	}
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
