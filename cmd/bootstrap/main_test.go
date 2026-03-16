package main

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/nicce/substrate-bootstrap/internal/config"
	"github.com/nicce/substrate-bootstrap/internal/logging"
)

func buildMockNode(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	binary := filepath.Join(dir, "mock-node")
	cmd := exec.Command("go", "build", "-o", binary, "../../tests/e2e/mock_node")
	cmd.Stderr = os.Stderr
	require.NoError(t, cmd.Run())
	return binary
}

func testLogger(t *testing.T) *zap.Logger {
	t.Helper()
	logger, err := zap.NewDevelopment()
	require.NoError(t, err)
	return logger
}

func requireDataDir(t *testing.T) {
	t.Helper()
	if err := checkDataDirectory(config.DataDir()); err != nil {
		t.Skipf("skipping: fixed data directory %s is not available: %v", config.DataDir(), err)
	}
}

func TestMain_ShowVersion(t *testing.T) {
	modRoot := findModuleRoot(t)
	require.NotEmpty(t, modRoot, "could not find module root")

	dir := t.TempDir()
	binary := filepath.Join(dir, "substrate-bootstrap")
	build := exec.Command("go", "build", "-o", binary, ".")
	build.Dir = filepath.Join(modRoot, "cmd", "bootstrap")
	require.NoError(t, build.Run())

	cmd := exec.Command(binary, "-v")
	out, err := cmd.Output()
	require.NoError(t, err)
	assert.Contains(t, string(out), "substrate-bootstrap version")
	assert.Contains(t, string(out), "build:")
}

func findModuleRoot(t *testing.T) string {
	t.Helper()
	cmd := exec.Command("go", "list", "-m", "-f", "{{.Dir}}")
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func TestRun_RPCCleanExit(t *testing.T) {
	requireDataDir(t)
	t.Setenv("MOCK_NODE_MODE", "exit0")

	binary := buildMockNode(t)

	cfg := &config.Config{
		Node: config.NodeConfig{
			Binary: binary,
			Name:   "test-rpc",
		},
		Chain: config.ChainConfig{
			ChainSpec:     "/opt/chain.json",
			Port:          40333,
			BlocksPruning: "archive-canonical",
			StatePruning:  "256",
			Bootnodes:     []string{"/dns/a/tcp/1/p2p/x"},
		},
		RelayChain: config.RelayChainConfig{
			ChainSpec: "/opt/relay.json",
			Port:      30333,
			Execution: "wasm",
			Bootnodes: []string{"/dns/b/tcp/1/p2p/y"},
		},
		Logging: logging.Config{Level: "info", Format: "json"},
	}

	err := run(context.Background(), cfg, testLogger(t))
	assert.NoError(t, err)
}

func TestRun_ListenerCleanExit(t *testing.T) {
	requireDataDir(t)
	t.Setenv("MOCK_NODE_MODE", "exit0")

	binary := buildMockNode(t)

	cfg := &config.Config{
		Node: config.NodeConfig{
			Binary:         binary,
			Name:           "test-listener",
			EnableKeystore: true,
		},
		Chain: config.ChainConfig{
			ChainSpec:     "/opt/chain.json",
			Port:          40333,
			BlocksPruning: "archive-canonical",
			StatePruning:  "256",
			Bootnodes:     []string{"/dns/a/tcp/1/p2p/x"},
		},
		RelayChain: config.RelayChainConfig{
			ChainSpec: "/opt/relay.json",
			Port:      30333,
			Execution: "wasm",
			Bootnodes: []string{"/dns/b/tcp/1/p2p/y"},
		},
		Keystore: config.KeystoreConfig{
			CleanupOnStop: true,
		},
		Logging: logging.Config{Level: "info", Format: "json"},
	}

	err := run(context.Background(), cfg, testLogger(t))
	assert.NoError(t, err)

	_, statErr := os.Stat(config.KeystorePath())
	assert.True(t, os.IsNotExist(statErr))
}

func TestRun_BootstrapFailure(t *testing.T) {
	requireDataDir(t)

	cfg := &config.Config{
		Node: config.NodeConfig{
			Binary: "/bin/echo",
			Name:   "test",
		},
		Chain: config.ChainConfig{
			ChainSpec: "/opt/chain.json",
			Port:      40333,
			Bootnodes: []string{"a"},
		},
		RelayChain: config.RelayChainConfig{
			ChainSpec: "/opt/relay.json",
			Port:      30333,
			Bootnodes: []string{"b"},
		},
		Bootstrap: config.BootstrapConfig{
			Commands: []string{"exit 1"},
		},
		Logging: logging.Config{Level: "info", Format: "json"},
	}

	err := run(context.Background(), cfg, testLogger(t))
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "bootstrap")
}

func TestCheckDataDirectory_CreatesMissing(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "newdir", "nested")
	err := checkDataDirectory(dir)
	require.NoError(t, err)

	info, err := os.Stat(dir)
	require.NoError(t, err)
	assert.True(t, info.IsDir())
}

func TestCheckDataDirectory_ExistingDir(t *testing.T) {
	dir := t.TempDir()
	err := checkDataDirectory(dir)
	require.NoError(t, err)
}

func TestCheckDataDirectory_NotADirectory(t *testing.T) {
	f := filepath.Join(t.TempDir(), "file")
	require.NoError(t, os.WriteFile(f, []byte("data"), 0o644))

	err := checkDataDirectory(f)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not a directory")
}

func TestCheckDataDirectory_NotWritable(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("test requires non-root user")
	}
	dir := t.TempDir()
	require.NoError(t, os.Chmod(dir, 0o444))
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })

	err := checkDataDirectory(dir)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not writable")
}

func TestClassifyError_PermissionError(t *testing.T) {
	err := &exitError{code: ExitPermissionError, err: errors.New("not writable")}
	assert.Equal(t, ExitPermissionError, classifyError(err))
}

func TestClassifyError_ConfigChanged(t *testing.T) {
	err := errors.New("bootstrap config changed; wipe /data and restart")
	assert.Equal(t, ExitConfigChanged, classifyError(err))
}

func TestClassifyError_GeneralError(t *testing.T) {
	err := errors.New("something went wrong")
	assert.Equal(t, ExitGeneralError, classifyError(err))
}

func TestExitError_Unwrap(t *testing.T) {
	inner := errors.New("inner error")
	ee := &exitError{code: 3, err: inner}
	assert.Equal(t, "inner error", ee.Error())
	assert.Equal(t, inner, errors.Unwrap(ee))
}

func TestRun_PermissionError(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("test requires non-root user")
	}

	t.Skip("requires /data to be read-only; not practical in unit tests with fixed paths")
}
