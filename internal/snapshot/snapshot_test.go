package snapshot

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/dsnet/compress/bzip2"
	"github.com/klauspost/compress/zstd"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/ulikunitz/xz"
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

// Gzip-compressed payload is often uploaded with a .tar filename; magic-byte sniff must still extract.
func TestSyncIfNeeded_TarExtensionDetectsGzip(t *testing.T) {
	d := testDownloader(t)
	dir := filepath.Join(t.TempDir(), "chaindata")

	server := createTarGzServer(t, map[string]string{"db/metadata": "gzipped"})
	defer server.Close()

	result, err := d.SyncIfNeeded(context.Background(), server.URL+"/data.tar", dir)
	require.NoError(t, err)
	assert.True(t, result.Downloaded)
	assert.Equal(t, "tar", result.Method)

	data, err := os.ReadFile(filepath.Join(dir, "db/metadata"))
	require.NoError(t, err)
	assert.Equal(t, "gzipped", string(data))
}

func buildTarBytes(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
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
	require.NoError(t, tw.Close())
	return buf.Bytes()
}

func TestSyncIfNeeded_TarExtensionDetectsCompressedMagic(t *testing.T) {
	tarBytes := buildTarBytes(t, map[string]string{"probe.txt": "magic"})

	tests := []struct {
		name     string
		compress func(t *testing.T, in []byte) []byte
	}{
		{
			name: "zstd",
			compress: func(t *testing.T, in []byte) []byte {
				var buf bytes.Buffer
				enc, err := zstd.NewWriter(&buf)
				require.NoError(t, err)
				_, err = enc.Write(in)
				require.NoError(t, err)
				require.NoError(t, enc.Close())
				return buf.Bytes()
			},
		},
		{
			name: "xz",
			compress: func(t *testing.T, in []byte) []byte {
				var buf bytes.Buffer
				w, err := xz.NewWriter(&buf)
				require.NoError(t, err)
				_, err = w.Write(in)
				require.NoError(t, err)
				require.NoError(t, w.Close())
				return buf.Bytes()
			},
		},
		{
			name: "bzip2",
			compress: func(t *testing.T, in []byte) []byte {
				var buf bytes.Buffer
				w, err := bzip2.NewWriter(&buf, &bzip2.WriterConfig{Level: bzip2.BestSpeed})
				require.NoError(t, err)
				_, err = w.Write(in)
				require.NoError(t, err)
				require.NoError(t, w.Close())
				return buf.Bytes()
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			body := tc.compress(t, tarBytes)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = w.Write(body)
			}))
			t.Cleanup(server.Close)

			d := testDownloader(t)
			dir := filepath.Join(t.TempDir(), "chaindata")
			result, err := d.SyncIfNeeded(context.Background(), server.URL+"/snapshot.tar", dir)
			require.NoError(t, err)
			assert.True(t, result.Downloaded)
			assert.Equal(t, "tar", result.Method)
			data, err := os.ReadFile(filepath.Join(dir, "probe.txt"))
			require.NoError(t, err)
			assert.Equal(t, "magic", string(data))
		})
	}
}

func createRawTarServer(t *testing.T, files map[string]string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tw := tar.NewWriter(w)
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

func TestSyncIfNeeded_TarExtensionUncompressedTar(t *testing.T) {
	d := testDownloader(t)
	dir := filepath.Join(t.TempDir(), "chaindata")

	server := createRawTarServer(t, map[string]string{"plain.txt": "raw"})
	defer server.Close()

	result, err := d.SyncIfNeeded(context.Background(), server.URL+"/snapshot.tar", dir)
	require.NoError(t, err)
	assert.True(t, result.Downloaded)
	assert.Equal(t, "tar", result.Method)

	data, err := os.ReadFile(filepath.Join(dir, "plain.txt"))
	require.NoError(t, err)
	assert.Equal(t, "raw", string(data))
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

func TestDownloadChainspec_EmptyURL(t *testing.T) {
	d := testDownloader(t)
	dest := filepath.Join(t.TempDir(), "chainspec.json")
	err := d.DownloadChainspec(context.Background(), "", dest, false)
	require.NoError(t, err)
	_, err = os.Stat(dest)
	assert.True(t, os.IsNotExist(err))
}

func TestDownloadChainspec_ExistsAndNotForce_Skips(t *testing.T) {
	d := testDownloader(t)
	dir := t.TempDir()
	dest := filepath.Join(dir, "chainspec.json")
	require.NoError(t, os.MkdirAll(dir, 0o755))
	require.NoError(t, os.WriteFile(dest, []byte(`{"existing": true}`), 0o644))

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"new": true}`))
	}))
	defer server.Close()

	err := d.DownloadChainspec(context.Background(), server.URL+"/chainspec.json", dest, false)
	require.NoError(t, err)

	data, err := os.ReadFile(dest)
	require.NoError(t, err)
	assert.Equal(t, `{"existing": true}`, string(data))
}

func TestDownloadChainspec_Downloads(t *testing.T) {
	d := testDownloader(t)
	dir := filepath.Join(t.TempDir(), "chain-data")
	dest := filepath.Join(dir, "chainspec.json")

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"name": "test-chain", "id": "test"}`))
	}))
	defer server.Close()

	err := d.DownloadChainspec(context.Background(), server.URL+"/chainspec.json", dest, false)
	require.NoError(t, err)

	data, err := os.ReadFile(dest)
	require.NoError(t, err)
	assert.Equal(t, `{"name": "test-chain", "id": "test"}`, string(data))
}

func TestDownloadChainspec_ForceOverwrites(t *testing.T) {
	d := testDownloader(t)
	dir := t.TempDir()
	dest := filepath.Join(dir, "chainspec.json")
	require.NoError(t, os.MkdirAll(dir, 0o755))
	require.NoError(t, os.WriteFile(dest, []byte(`{"old": true}`), 0o644))

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"new": true}`))
	}))
	defer server.Close()

	err := d.DownloadChainspec(context.Background(), server.URL+"/chainspec.json", dest, true)
	require.NoError(t, err)

	data, err := os.ReadFile(dest)
	require.NoError(t, err)
	assert.Equal(t, `{"new": true}`, string(data))
}

func TestDownloadChainspec_HTTPError(t *testing.T) {
	d := testDownloader(t)
	dest := filepath.Join(t.TempDir(), "chainspec.json")

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	err := d.DownloadChainspec(context.Background(), server.URL+"/chainspec.json", dest, false)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "status 404")
}

func TestDownloadChainspec_DestIsDirectory_ReturnsError(t *testing.T) {
	d := testDownloader(t)
	dir := t.TempDir()
	dest := filepath.Join(dir, "chainspec.json")
	require.NoError(t, os.MkdirAll(dest, 0o755))

	err := d.DownloadChainspec(context.Background(), "http://example.com/chainspec.json", dest, false)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not a regular file")
}

func TestDownloadChainspec_TooLarge_ContentLength(t *testing.T) {
	d := testDownloader(t)
	dest := filepath.Join(t.TempDir(), "chainspec.json")

	// Exceed maxChainspecSizeBytes
	oversized := maxChainspecSizeBytes + 1
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", strconv.FormatInt(int64(oversized), 10))
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	err := d.DownloadChainspec(context.Background(), server.URL+"/chainspec.json", dest, false)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "chainspec too large")
	assert.Contains(t, err.Error(), "content-length")
}

func TestDownloadChainspec_TooLarge_Body(t *testing.T) {
	d := testDownloader(t)
	dest := filepath.Join(t.TempDir(), "chainspec.json")

	// Exceed maxChainspecSizeBytes
	oversized := make([]byte, maxChainspecSizeBytes+1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(oversized)
	}))
	defer server.Close()

	err := d.DownloadChainspec(context.Background(), server.URL+"/chainspec.json", dest, false)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "chainspec too large")
	assert.Contains(t, err.Error(), "wrote")
}

func TestResolveSnapshotURL_WithVersion_ReturnsUnchanged(t *testing.T) {
	d := testDownloader(t)
	url := "https://snapshots.polkadot.io/paseo-muse-paritydb-archive/20260316-011637"
	resolved, err := d.resolveSnapshotURL(context.Background(), url)
	require.NoError(t, err)
	assert.Equal(t, url, resolved)
}

func TestResolveSnapshotURL_BaseURL_FetchesLatest(t *testing.T) {
	d := testDownloader(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/paseo-muse-paritydb-archive/latest_version.meta.txt" {
			_, _ = w.Write([]byte("20260316-011637\n"))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	baseURL := server.URL + "/paseo-muse-paritydb-archive"
	resolved, err := d.resolveSnapshotURL(context.Background(), baseURL)
	require.NoError(t, err)
	assert.Equal(t, baseURL+"/20260316-011637", resolved)
}

func TestResolveSnapshotURL_TarURL_ReturnsUnchanged(t *testing.T) {
	d := testDownloader(t)
	url := "https://example.com/snapshot.tar.gz"
	resolved, err := d.resolveSnapshotURL(context.Background(), url)
	require.NoError(t, err)
	assert.Equal(t, url, resolved)
}

func TestResolveSnapshotURL_EmptyMeta_FallsBackToBase(t *testing.T) {
	d := testDownloader(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(""))
	}))
	defer server.Close()

	baseURL := server.URL + "/snapshots"
	resolved, err := d.resolveSnapshotURL(context.Background(), baseURL)
	require.NoError(t, err)
	assert.Equal(t, baseURL, resolved)
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

func TestExtractTarSecure_RejectsPathTraversal(t *testing.T) {
	dir := t.TempDir()

	malicious := createTarGzServer(t, map[string]string{
		"../../../etc/passwd": "malicious",
	})
	defer malicious.Close()

	d := testDownloader(t)
	_, err := d.SyncIfNeeded(context.Background(), malicious.URL+"/evil.tar.gz", dir)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "rejecting path outside destination")
}

func TestExtractTarSecure_AcceptsSymlinkWithDoubleDotsInName(t *testing.T) {
	dir := t.TempDir()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gw := gzip.NewWriter(w)
		tw := tar.NewWriter(gw)
		// "foo..bar" is a valid filename, not path traversal
		require.NoError(t, tw.WriteHeader(&tar.Header{Name: "link", Mode: 0o755, Typeflag: tar.TypeSymlink, Linkname: "foo..bar"}))
		_ = tw.Close()
		_ = gw.Close()
	}))
	defer server.Close()

	d := testDownloader(t)
	_, err := d.SyncIfNeeded(context.Background(), server.URL+"/snap.tar.gz", dir)
	require.NoError(t, err)

	// Symlink should exist (target "foo..bar" resolves within dir, so it's valid)
	linkPath := filepath.Join(dir, "link")
	info, err := os.Lstat(linkPath)
	require.NoError(t, err)
	assert.True(t, info.Mode()&os.ModeSymlink != 0)
}

func TestExtractTarSecure_RejectsAbsolutePath(t *testing.T) {
	dir := t.TempDir()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gw := gzip.NewWriter(w)
		tw := tar.NewWriter(gw)
		hdr := &tar.Header{Name: "/etc/passwd", Mode: 0o644, Size: 9}
		_ = tw.WriteHeader(hdr)
		_, _ = tw.Write([]byte("malicious"))
		_ = tw.Close()
		_ = gw.Close()
	}))
	defer server.Close()

	d := testDownloader(t)
	_, err := d.SyncIfNeeded(context.Background(), server.URL+"/evil.tar.gz", dir)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "absolute path")
}

func TestTarEntryPerm(t *testing.T) {
	tests := []struct {
		name string
		mode int64
		want os.FileMode
	}{
		{"zero", 0, 0},
		{"perm_only_644", 0o644, 0o644},
		{"perm_only_755", 0o755, 0o755},
		{"type_bits_masked", 0o100644, 0o644},
		{"extra_high_bits_masked", (1 << 33) | 0o750, 0o750},
		{"negative_defaults_644", -1, 0o644},
		{"negative_non_max_defaults_644", -100, 0o644},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tarEntryPerm(&tar.Header{Mode: tt.mode})
			assert.Equal(t, tt.want, got)
		})
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

func TestParallelTransfers(t *testing.T) {
	s := parallelTransfers()
	n, err := strconv.Atoi(s)
	require.NoError(t, err)
	assert.GreaterOrEqual(t, n, 1)
	assert.LessOrEqual(t, n, 50)
}
