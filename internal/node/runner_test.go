package node

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/nicce/substrate-bootstrap/internal/config"
)

func TestBackoffDuration(t *testing.T) {
	r := &Runner{InitialBackoff: 2 * time.Second, MaxBackoff: 30 * time.Second}
	tests := []struct {
		attempt  int
		expected time.Duration
	}{
		{1, 2 * time.Second},
		{2, 4 * time.Second},
		{3, 8 * time.Second},
		{4, 16 * time.Second},
		{5, 30 * time.Second},
		{10, 30 * time.Second},
	}

	for _, tt := range tests {
		d := r.backoffDuration(tt.attempt)
		assert.Equal(t, tt.expected, d, "attempt %d", tt.attempt)
	}
}

func buildMockNode(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	binary := filepath.Join(dir, "mock-node")
	cmd := exec.Command("go", "build", "-o", binary, "../../tests/e2e/mock_node")
	cmd.Stderr = os.Stderr
	require.NoError(t, cmd.Run(), "failed to build mock node binary")
	return binary
}

func runnerConfig(binary string) *config.Config {
	return &config.Config{
		Node: config.NodeConfig{
			Binary: binary,
			Name:   "test-node",
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
			Bootnodes: []string{"/dns/b/tcp/1/p2p/y"},
		},
	}
}

func fastRunner(cfg *config.Config, logger *zap.Logger) *Runner {
	r := NewRunner(cfg, logger, "")
	r.MaxRetries = 1
	r.InitialBackoff = 10 * time.Millisecond
	r.MaxBackoff = 50 * time.Millisecond
	return r
}

func TestRunner_CleanExit(t *testing.T) {
	binary := buildMockNode(t)
	cfg := runnerConfig(binary)

	logger, _ := zap.NewDevelopment()
	runner := NewRunner(cfg, logger, "")

	t.Setenv("MOCK_NODE_MODE", "exit0")
	err := runner.Run(context.Background())
	require.NoError(t, err)
}

func TestRunner_FailedStart(t *testing.T) {
	cfg := runnerConfig("/nonexistent/binary")

	logger, _ := zap.NewDevelopment()
	runner := fastRunner(cfg, logger)

	err := runner.Run(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "node failed after")
}

func TestRunner_NonZeroExit(t *testing.T) {
	binary := buildMockNode(t)
	cfg := runnerConfig(binary)

	logger, _ := zap.NewDevelopment()
	runner := fastRunner(cfg, logger)

	t.Setenv("MOCK_NODE_MODE", "exit1")
	err := runner.Run(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "node failed after")
}

func TestRunner_SignalForwarding(t *testing.T) {
	binary := buildMockNode(t)
	cfg := runnerConfig(binary)

	logger, _ := zap.NewDevelopment()
	runner := NewRunner(cfg, logger, "")

	t.Setenv("MOCK_NODE_MODE", "signal")

	errCh := make(chan error, 1)
	go func() {
		errCh <- runner.Run(context.Background())
	}()

	time.Sleep(500 * time.Millisecond)

	p, err := os.FindProcess(os.Getpid())
	require.NoError(t, err)
	require.NoError(t, p.Signal(os.Interrupt))

	select {
	case err := <-errCh:
		assert.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("runner did not exit within timeout")
	}
}

func TestRunner_ContextCancellation(t *testing.T) {
	binary := buildMockNode(t)
	cfg := runnerConfig(binary)

	logger, _ := zap.NewDevelopment()
	runner := NewRunner(cfg, logger, "")

	t.Setenv("MOCK_NODE_MODE", "signal")

	ctx, cancel := context.WithCancel(context.Background())

	errCh := make(chan error, 1)
	go func() {
		errCh <- runner.Run(ctx)
	}()

	time.Sleep(500 * time.Millisecond)
	cancel()

	select {
	case err := <-errCh:
		assert.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("runner did not exit within timeout")
	}
}

func TestNewRunner(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	cfg := runnerConfig("/bin/echo")
	runner := NewRunner(cfg, logger, "")
	assert.NotNil(t, runner)
	assert.Equal(t, 3, runner.MaxRetries)
	assert.Equal(t, 2*time.Second, runner.InitialBackoff)
	assert.Equal(t, 30*time.Second, runner.MaxBackoff)
}
