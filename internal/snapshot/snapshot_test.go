package snapshot

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func testDownloader(t *testing.T) *Downloader {
	t.Helper()
	logger, err := zap.NewDevelopment()
	require.NoError(t, err)
	return NewDownloader(logger)
}

func createTarGzServer(t *testing.T, files map[string]string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/gzip")
		gw := gzip.NewWriter(w)
		defer gw.Close()
		tw := tar.NewWriter(gw)
		defer tw.Close()

		for name, content := range files {
			hdr := &tar.Header{
				Name: name,
				Mode: 0o644,
				Size: int64(len(content)),
			}
			require.NoError(t, tw.WriteHeader(hdr))
			_, err := tw.Write([]byte(content))
			require.NoError(t, err)
		}
	}))
}

func TestSyncIfNeeded_EmptyURL(t *testing.T) {
	d := testDownloader(t)
	result, err := d.SyncIfNeeded(context.Background(), "", "/any/path")
	require.NoError(t, err)
	assert.Nil(t, result)
}

func TestSyncIfNeeded_AlreadyHasData(t *testing.T) {
	d := testDownloader(t)
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "existing"), []byte("data"), 0o644))

	server := createTarGzServer(t, map[string]string{"test.txt": "should not download"})
	defer server.Close()

	result, err := d.SyncIfNeeded(context.Background(), server.URL+"/snap.tar.gz", dir)
	require.NoError(t, err)
	assert.True(t, result.Skipped)
	assert.False(t, result.Downloaded)

	_, err = os.Stat(filepath.Join(dir, "test.txt"))
	assert.True(t, os.IsNotExist(err))
}

func TestSyncIfNeeded_DownloadsAndExtracts(t *testing.T) {
	d := testDownloader(t)
	dir := filepath.Join(t.TempDir(), "chaindata")

	server := createTarGzServer(t, map[string]string{
		"db/metadata":   "version=1",
		"db/000001.log": "logdata",
		"db/MANIFEST":   "manifest",
	})
	defer server.Close()

	result, err := d.SyncIfNeeded(context.Background(), server.URL+"/snap.tar.gz", dir)
	require.NoError(t, err)
	assert.True(t, result.Downloaded)
	assert.False(t, result.Skipped)
	assert.Equal(t, "tar", result.Method)

	data, err := os.ReadFile(filepath.Join(dir, "db/metadata"))
	require.NoError(t, err)
	assert.Equal(t, "version=1", string(data))

	data, err = os.ReadFile(filepath.Join(dir, "db/000001.log"))
	require.NoError(t, err)
	assert.Equal(t, "logdata", string(data))
}

func TestSyncIfNeeded_NonexistentDir(t *testing.T) {
	d := testDownloader(t)
	dir := filepath.Join(t.TempDir(), "deep", "nested", "chaindata")

	server := createTarGzServer(t, map[string]string{"data.txt": "hello"})
	defer server.Close()

	result, err := d.SyncIfNeeded(context.Background(), server.URL+"/snap.tar.gz", dir)
	require.NoError(t, err)
	assert.True(t, result.Downloaded)

	data, err := os.ReadFile(filepath.Join(dir, "data.txt"))
	require.NoError(t, err)
	assert.Equal(t, "hello", string(data))
}

func TestSyncIfNeeded_HTTPError(t *testing.T) {
	d := testDownloader(t)
	dir := filepath.Join(t.TempDir(), "chaindata")

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	_, err := d.SyncIfNeeded(context.Background(), server.URL+"/missing.tar.gz", dir)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "status 404")
}

func TestSyncIfNeeded_InvalidURL(t *testing.T) {
	d := testDownloader(t)
	dir := filepath.Join(t.TempDir(), "chaindata")

	_, err := d.SyncIfNeeded(context.Background(), "http://localhost:1/definitely-not-running.tar.gz", dir)
	require.Error(t, err)
}

func TestSyncIfNeeded_ContextCancelled(t *testing.T) {
	d := testDownloader(t)
	dir := filepath.Join(t.TempDir(), "chaindata")

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer server.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := d.SyncIfNeeded(ctx, server.URL+"/snap.tar.gz", dir)
	require.Error(t, err)
}

func TestIsTarURL(t *testing.T) {
	tarURLs := []string{
		"https://example.io/snap.tar.gz",
		"https://example.io/snap.tgz",
		"https://example.io/snap.tar.lz4",
		"https://example.io/snap.tar.zst",
		"https://example.io/snap.tar.zstd",
		"https://example.io/snap.tar.bz2",
		"https://example.io/snap.tar.xz",
		"https://example.io/snap.tar",
	}
	for _, u := range tarURLs {
		assert.True(t, isTarURL(u), "expected tar URL: %s", u)
	}

	nonTarURLs := []string{
		"https://snapshots.polkadot.io/polkadot-paritydb-prune/20260304-062636",
		"https://snapshots.polkadot.io/kusama-rocksdb-archive/20260304-025510",
		"https://example.io/snapshot",
		"https://example.io/snap/latest",
	}
	for _, u := range nonTarURLs {
		assert.False(t, isTarURL(u), "expected non-tar URL: %s", u)
	}
}

func TestDetectTarFlags(t *testing.T) {
	tests := []struct {
		url      string
		expected string
	}{
		{"https://example.io/snap.tar.gz", "xzf -"},
		{"https://example.io/snap.tgz", "xzf -"},
		{"https://example.io/snap.tar.lz4", "--use-compress-program=lz4 -xf -"},
		{"https://example.io/snap.tar.zst", "--use-compress-program=zstd -xf -"},
		{"https://example.io/snap.tar.zstd", "--use-compress-program=zstd -xf -"},
		{"https://example.io/snap.tar.bz2", "xjf -"},
		{"https://example.io/snap.tar.xz", "xJf -"},
		{"https://example.io/snap.tar", "xf -"},
		{"https://example.io/snap", "xzf -"},
	}
	for _, tt := range tests {
		assert.Equal(t, tt.expected, detectTarFlags(tt.url), "url: %s", tt.url)
	}
}

func TestDirHasData(t *testing.T) {
	t.Run("nonexistent", func(t *testing.T) {
		has, err := dirHasData("/nonexistent/path")
		require.NoError(t, err)
		assert.False(t, has)
	})
	t.Run("empty", func(t *testing.T) {
		has, err := dirHasData(t.TempDir())
		require.NoError(t, err)
		assert.False(t, has)
	})
	t.Run("with_files", func(t *testing.T) {
		dir := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(dir, "f"), []byte("x"), 0o644))
		has, err := dirHasData(dir)
		require.NoError(t, err)
		assert.True(t, has)
	})
}

func TestHumanSize(t *testing.T) {
	assert.Equal(t, "500 bytes", humanSize(500))
	assert.Equal(t, "1.5 MB", humanSize(1572864))
	assert.Equal(t, "2.0 GB", humanSize(2147483648))
}
