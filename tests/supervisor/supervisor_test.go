//go:build integration

package supervisor

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var (
	bootstrapBinary string
	mockNodeBinary  string
)

func TestMain(m *testing.M) {
	tmpDir, err := os.MkdirTemp("", "supervisor-test-*")
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to create temp dir: %v\n", err)
		os.Exit(1)
	}

	bootstrapBinary = filepath.Join(tmpDir, "substrate-bootstrap")
	cmd := exec.Command("go", "build", "-o", bootstrapBinary, "../../cmd/bootstrap")
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "failed to build substrate-bootstrap: %v\n", err)
		os.RemoveAll(tmpDir)
		os.Exit(1)
	}

	mockNodeBinary = filepath.Join(tmpDir, "mock-node")
	cmd = exec.Command("go", "build", "-o", mockNodeBinary, "../e2e/mock_node")
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "failed to build mock-node: %v\n", err)
		os.RemoveAll(tmpDir)
		os.Exit(1)
	}

	code := m.Run()
	os.RemoveAll(tmpDir)
	os.Exit(code)
}

type testEnv struct {
	t         *testing.T
	dir       string
	basePath  string
	stateFile string
	configDir string
}

func newTestEnv(t *testing.T) *testEnv {
	t.Helper()
	dir := t.TempDir()
	basePath := filepath.Join(dir, "data")
	require.NoError(t, os.MkdirAll(basePath, 0o755))
	configDir := filepath.Join(dir, "configs")
	require.NoError(t, os.MkdirAll(configDir, 0o755))

	return &testEnv{
		t:         t,
		dir:       dir,
		basePath:  basePath,
		stateFile: filepath.Join(basePath, "bootstrap_state.json"),
		configDir: configDir,
	}
}

func (e *testEnv) writeConfig(role string, bootstrapCmds []string) string {
	e.t.Helper()

	cmdsYAML := "  commands: []\n"
	if len(bootstrapCmds) > 0 {
		lines := make([]string, len(bootstrapCmds))
		for i, c := range bootstrapCmds {
			lines[i] = fmt.Sprintf("    - %q", c)
		}
		cmdsYAML = fmt.Sprintf("  commands:\n%s\n", strings.Join(lines, "\n"))
	}

	keystoreSection := ""
	enableKeystore := "false"
	if role == "listener" {
		keystoreSection = `keystore:
  cleanup_on_stop: true
`
		enableKeystore = "true"
	}

	extraArgs := ""
	if role == "rpc" {
		extraArgs = `  extra_args:
    - "--rpc-port=9944"
    - "--rpc-external"
    - "--rpc-cors=all"
    - "--db-cache=1024"
`
	}

	cfg := fmt.Sprintf(`node:
  binary: %s
  name: test-node
  enable_keystore: %s

chain:
  chain_spec: /opt/chain.json
  port: 40333
  blocks_pruning: archive-canonical
  state_pruning: "256"
  bootnodes:
    - /dns/boot.example.io/tcp/40333/p2p/12D3KooW
%s
relay_chain:
  chain_spec: /opt/relay.json
  port: 30333
  execution: wasm
  bootnodes:
    - /dns/relay.parity.io/tcp/30333/p2p/12D3KooW

prometheus:
  enabled: true
  port: 9615
  external: true

telemetry:
  urls:
    - "wss://telemetry.example.io/submit 0"

%s
bootstrap:
%s
logging:
  level: info
  format: json
`, mockNodeBinary, enableKeystore, extraArgs, keystoreSection, cmdsYAML)

	path := filepath.Join(e.configDir, role+".yaml")
	require.NoError(e.t, os.WriteFile(path, []byte(cfg), 0o644))
	return path
}

func runBootstrap(t *testing.T, configPath string, dataDir string, env []string, timeout time.Duration) (string, int) {
	t.Helper()
	cmd := exec.Command(bootstrapBinary, "--config", configPath)
	fullEnv := append(os.Environ(), env...)
	if dataDir != "" {
		fullEnv = append(fullEnv, "SUBSTRATE_BOOTSTRAP_DATA_DIR="+dataDir)
	}
	cmd.Env = fullEnv

	var out strings.Builder
	cmd.Stdout = &out
	cmd.Stderr = &out

	if err := cmd.Start(); err != nil {
		t.Fatalf("failed to start bootstrap: %v", err)
	}

	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()

	select {
	case err := <-done:
		exitCode := 0
		if err != nil {
			if exitErr, ok := err.(*exec.ExitError); ok {
				exitCode = exitErr.ExitCode()
			} else {
				exitCode = -1
			}
		}
		return out.String(), exitCode
	case <-time.After(timeout):
		_ = cmd.Process.Kill()
		t.Fatalf("bootstrap timed out after %v", timeout)
		return "", -1
	}
}

func runBootstrapWithSignal(t *testing.T, configPath string, dataDir string, env []string, signalDelay time.Duration, sig syscall.Signal) (string, int) {
	t.Helper()
	cmd := exec.Command(bootstrapBinary, "--config", configPath)
	fullEnv := append(os.Environ(), env...)
	if dataDir != "" {
		fullEnv = append(fullEnv, "SUBSTRATE_BOOTSTRAP_DATA_DIR="+dataDir)
	}
	cmd.Env = fullEnv
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	var out strings.Builder
	cmd.Stdout = &out
	cmd.Stderr = &out

	require.NoError(t, cmd.Start())

	time.Sleep(signalDelay)
	_ = cmd.Process.Signal(sig)

	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()

	select {
	case err := <-done:
		exitCode := 0
		if err != nil {
			if exitErr, ok := err.(*exec.ExitError); ok {
				exitCode = exitErr.ExitCode()
			} else {
				exitCode = -1
			}
		}
		return out.String(), exitCode
	case <-time.After(10 * time.Second):
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		t.Fatalf("bootstrap did not exit after signal")
		return "", -1
	}
}

// --- Scenario A: Successful startup flow (bootstrap → node → clean exit) ---

func TestSupervisor_RPCCleanStartup(t *testing.T) {
	t.Parallel()
	env := newTestEnv(t)
	configPath := env.writeConfig("rpc", nil)

	output, exitCode := runBootstrap(t, configPath, env.basePath, []string{"MOCK_NODE_MODE=exit0"}, 10*time.Second)

	assert.Equal(t, 0, exitCode, "expected clean exit, output: %s", output)
	assert.Contains(t, output, "MOCK_NODE_ARGS=")
	assert.Contains(t, output, "--rpc-port")
	assert.Contains(t, output, "--rpc-cors=all")
	assert.Contains(t, output, "--db-cache")
}

func TestSupervisor_ListenerCleanStartup(t *testing.T) {
	t.Parallel()
	env := newTestEnv(t)
	configPath := env.writeConfig("listener", nil)

	output, exitCode := runBootstrap(t, configPath, env.basePath, []string{"MOCK_NODE_MODE=exit0"}, 10*time.Second)

	assert.Equal(t, 0, exitCode, "expected clean exit, output: %s", output)
	assert.Contains(t, output, "MOCK_NODE_ARGS=")
	assert.Contains(t, output, "--keystore-path")
	assert.NotContains(t, output, "--rpc-port")

	// Keystore directory should be cleaned up
	keystorePath := filepath.Join(env.basePath, "keystore")
	_, err := os.Stat(keystorePath)
	assert.True(t, os.IsNotExist(err), "keystore should be cleaned up after exit")
}

// --- Scenario B: Bootstrap command failure → exit code 1 ---

func TestSupervisor_BootstrapCommandFailure(t *testing.T) {
	t.Parallel()
	env := newTestEnv(t)
	configPath := env.writeConfig("rpc", []string{"exit 1"})

	output, exitCode := runBootstrap(t, configPath, env.basePath, []string{"MOCK_NODE_MODE=exit0"}, 10*time.Second)

	assert.Equal(t, 1, exitCode, "expected exit code 1, output: %s", output)
	assert.NotContains(t, output, "MOCK_NODE_ARGS=", "node should not have started")
}

// --- Scenario C: Bootstrap succeeds, then node reused (idempotent) ---

func TestSupervisor_BootstrapIdempotent(t *testing.T) {
	t.Parallel()
	env := newTestEnv(t)
	configPath := env.writeConfig("rpc", []string{"echo bootstrap-ran"})

	// First run: bootstrap executes commands
	output1, exitCode1 := runBootstrap(t, configPath, env.basePath, []string{"MOCK_NODE_MODE=exit0"}, 10*time.Second)
	assert.Equal(t, 0, exitCode1)
	assert.Contains(t, output1, "bootstrap-ran")

	// State file should exist
	_, err := os.Stat(env.stateFile)
	require.NoError(t, err, "state file should exist after bootstrap")

	// Second run: bootstrap should skip (already done)
	output2, exitCode2 := runBootstrap(t, configPath, env.basePath, []string{"MOCK_NODE_MODE=exit0"}, 10*time.Second)
	assert.Equal(t, 0, exitCode2)
	// "bootstrap-ran" should NOT appear in second run's output because commands were skipped
	lines := strings.Split(output2, "\n")
	bootstrapRanCount := 0
	for _, l := range lines {
		if strings.Contains(l, "bootstrap-ran") {
			bootstrapRanCount++
		}
	}
	assert.Equal(t, 0, bootstrapRanCount, "bootstrap commands should not re-run on second invocation")
}

// --- Scenario D: Config change detection → exit code 2 ---

func TestSupervisor_ConfigChangeDetected(t *testing.T) {
	t.Parallel()
	env := newTestEnv(t)

	// First run with one set of commands
	configPath := env.writeConfig("rpc", []string{"echo first-run"})
	_, exitCode1 := runBootstrap(t, configPath, env.basePath, []string{"MOCK_NODE_MODE=exit0"}, 10*time.Second)
	assert.Equal(t, 0, exitCode1)

	// Second run with different commands (config changed)
	configPath2 := env.writeConfig("rpc", []string{"echo different-commands"})
	output2, exitCode2 := runBootstrap(t, configPath2, env.basePath, []string{"MOCK_NODE_MODE=exit0"}, 10*time.Second)

	assert.Equal(t, 2, exitCode2, "expected exit code 2 for config change, output: %s", output2)
}

// --- Scenario E: Permission error → exit code 3 ---

func TestSupervisor_PermissionError(t *testing.T) {
	t.Parallel()
	if os.Getuid() == 0 {
		t.Skip("test requires non-root user")
	}

	env := newTestEnv(t)
	require.NoError(t, os.Chmod(env.basePath, 0o444))
	t.Cleanup(func() { _ = os.Chmod(env.basePath, 0o755) })

	configPath := env.writeConfig("rpc", nil)

	output, exitCode := runBootstrap(t, configPath, env.basePath, []string{"MOCK_NODE_MODE=exit0"}, 10*time.Second)

	assert.Equal(t, 3, exitCode, "expected exit code 3 for permission error, output: %s", output)
}

// --- Scenario F: Node crash → retry with backoff → eventual failure ---

func TestSupervisor_NodeCrashExhaustsRetries(t *testing.T) {
	t.Parallel()
	env := newTestEnv(t)
	configPath := env.writeConfig("rpc", nil)

	// Node always exits with code 1 -- will exhaust retries
	output, exitCode := runBootstrap(t, configPath, env.basePath, []string{"MOCK_NODE_MODE=exit1"}, 60*time.Second)

	assert.NotEqual(t, 0, exitCode, "should fail after exhausting retries, output: %s", output)
}

// --- Scenario G: Node crash → recovers on retry ---

func TestSupervisor_NodeCrashThenRecover(t *testing.T) {
	t.Parallel()
	env := newTestEnv(t)
	configPath := env.writeConfig("rpc", nil)

	marker := filepath.Join(env.dir, "crash_marker")

	output, exitCode := runBootstrap(t, configPath, env.basePath, []string{
		"MOCK_NODE_MODE=crash_then_ok",
		"MOCK_NODE_MARKER=" + marker,
	}, 30*time.Second)

	assert.Equal(t, 0, exitCode, "should recover after crash, output: %s", output)
	assert.Contains(t, output, "MOCK_NODE_CRASH")
	assert.Contains(t, output, "MOCK_NODE_RECOVERED")
}

// --- Scenario H: Signal forwarding (SIGTERM) → clean shutdown ---

func TestSupervisor_SignalForwarding_SIGTERM(t *testing.T) {
	t.Parallel()
	env := newTestEnv(t)
	configPath := env.writeConfig("rpc", nil)

	output, exitCode := runBootstrapWithSignal(
		t, configPath, env.basePath,
		[]string{"MOCK_NODE_MODE=signal"},
		1*time.Second,
		syscall.SIGTERM,
	)

	assert.Equal(t, 0, exitCode, "should exit cleanly on SIGTERM, output: %s", output)
	assert.Contains(t, output, "MOCK_NODE_SIGNAL_RECEIVED")
}

// --- Scenario I: Signal forwarding (SIGINT) → clean shutdown ---

func TestSupervisor_SignalForwarding_SIGINT(t *testing.T) {
	t.Parallel()
	env := newTestEnv(t)
	configPath := env.writeConfig("rpc", nil)

	output, exitCode := runBootstrapWithSignal(
		t, configPath, env.basePath,
		[]string{"MOCK_NODE_MODE=signal"},
		1*time.Second,
		syscall.SIGINT,
	)

	assert.Equal(t, 0, exitCode, "should exit cleanly on SIGINT, output: %s", output)
	assert.Contains(t, output, "MOCK_NODE_SIGNAL_RECEIVED")
}

// --- Scenario J: Listener cleanup after signal ---

func TestSupervisor_ListenerKeystoreCleanupAfterSignal(t *testing.T) {
	t.Parallel()
	env := newTestEnv(t)
	configPath := env.writeConfig("listener", nil)

	keystorePath := filepath.Join(env.basePath, "keystore")

	output, exitCode := runBootstrapWithSignal(
		t, configPath, env.basePath,
		[]string{"MOCK_NODE_MODE=signal"},
		1*time.Second,
		syscall.SIGTERM,
	)

	assert.Equal(t, 0, exitCode, "output: %s", output)

	_, err := os.Stat(keystorePath)
	assert.True(t, os.IsNotExist(err), "keystore should be cleaned up after SIGTERM")
}

// --- Scenario K: Bootstrap with real commands ---

func TestSupervisor_BootstrapRunsRealCommands(t *testing.T) {
	t.Parallel()
	env := newTestEnv(t)
	markerFile := filepath.Join(env.dir, "bootstrap_marker")
	configPath := env.writeConfig("rpc", []string{
		fmt.Sprintf("touch %s", markerFile),
	})

	output, exitCode := runBootstrap(t, configPath, env.basePath, []string{"MOCK_NODE_MODE=exit0"}, 10*time.Second)

	assert.Equal(t, 0, exitCode, "output: %s", output)

	_, err := os.Stat(markerFile)
	assert.NoError(t, err, "bootstrap command should have created marker file")
}

// --- Scenario L: State file persists correctly ---

func TestSupervisor_StateFilePersistence(t *testing.T) {
	t.Parallel()
	env := newTestEnv(t)
	configPath := env.writeConfig("rpc", []string{"echo hello"})

	_, exitCode := runBootstrap(t, configPath, env.basePath, []string{"MOCK_NODE_MODE=exit0"}, 10*time.Second)
	assert.Equal(t, 0, exitCode)

	data, err := os.ReadFile(env.stateFile)
	require.NoError(t, err)

	var state map[string]interface{}
	require.NoError(t, json.Unmarshal(data, &state))

	assert.NotEmpty(t, state["commands_hash"])
	assert.NotEmpty(t, state["completed_at"])
	assert.NotEmpty(t, state["commands"])
}

// --- Scenario M: Missing config flag ---

func TestSupervisor_MissingConfigFlag(t *testing.T) {
	t.Parallel()
	cmd := exec.Command(bootstrapBinary)
	var out strings.Builder
	cmd.Stdout = &out
	cmd.Stderr = &out

	err := cmd.Run()
	require.Error(t, err)

	exitErr, ok := err.(*exec.ExitError)
	require.True(t, ok)
	assert.Equal(t, 1, exitErr.ExitCode())
	assert.Contains(t, out.String(), "--config")
}

// --- Scenario N: Invalid config file ---

func TestSupervisor_InvalidConfigFile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	badConfig := filepath.Join(dir, "bad.yaml")
	require.NoError(t, os.WriteFile(badConfig, []byte("{{invalid yaml}}"), 0o644))

	cmd := exec.Command(bootstrapBinary, "--config", badConfig)
	var out strings.Builder
	cmd.Stdout = &out
	cmd.Stderr = &out

	err := cmd.Run()
	require.Error(t, err)

	exitErr, ok := err.(*exec.ExitError)
	require.True(t, ok)
	assert.Equal(t, 1, exitErr.ExitCode())
}

// --- Scenario O: Nonexistent config file ---

func TestSupervisor_NonexistentConfigFile(t *testing.T) {
	t.Parallel()
	cmd := exec.Command(bootstrapBinary, "--config", "/nonexistent/config.yaml")
	var out strings.Builder
	cmd.Stdout = &out
	cmd.Stderr = &out

	err := cmd.Run()
	require.Error(t, err)

	exitErr, ok := err.(*exec.ExitError)
	require.True(t, ok)
	assert.Equal(t, 1, exitErr.ExitCode())
}

// --- Scenario P: Correct args passed to node by role ---

func TestSupervisor_RPCArgsCorrect(t *testing.T) {
	t.Parallel()
	env := newTestEnv(t)
	configPath := env.writeConfig("rpc", nil)

	output, exitCode := runBootstrap(t, configPath, env.basePath, []string{"MOCK_NODE_MODE=exit0"}, 10*time.Second)
	require.Equal(t, 0, exitCode)

	argsLine := extractMockNodeArgs(output)
	assert.Contains(t, argsLine, "--name test-node")
	assert.Contains(t, argsLine, "--rpc-port=9944")
	assert.Contains(t, argsLine, "--rpc-external")
	assert.Contains(t, argsLine, "--rpc-cors=all")
	assert.Contains(t, argsLine, "--db-cache=1024")
	assert.Contains(t, argsLine, "--prometheus-port 9615")
	assert.Contains(t, argsLine, "--prometheus-external")
	assert.Contains(t, argsLine, "-- --name test-node")
	assert.NotContains(t, argsLine, "--keystore-path")
}

func TestSupervisor_ListenerArgsCorrect(t *testing.T) {
	t.Parallel()
	env := newTestEnv(t)
	configPath := env.writeConfig("listener", nil)

	output, exitCode := runBootstrap(t, configPath, env.basePath, []string{"MOCK_NODE_MODE=exit0"}, 10*time.Second)
	require.Equal(t, 0, exitCode)

	argsLine := extractMockNodeArgs(output)
	assert.Contains(t, argsLine, "--name test-node")
	assert.Contains(t, argsLine, "--keystore-path")
	assert.NotContains(t, argsLine, "--rpc-port=")
	assert.NotContains(t, argsLine, "--rpc-external")
	assert.NotContains(t, argsLine, "--db-cache=")
	assert.Contains(t, argsLine, "-- --name test-node")
}

func extractMockNodeArgs(output string) string {
	for _, line := range strings.Split(output, "\n") {
		if strings.HasPrefix(line, "MOCK_NODE_ARGS=") {
			return strings.TrimPrefix(line, "MOCK_NODE_ARGS=")
		}
	}
	return ""
}

// --- Real supervisor lifecycle tests (non-mocked process behavior) ---

func TestSupervisor_FullLifecycle(t *testing.T) {
	t.Parallel()
	env := newTestEnv(t)
	markerFile := filepath.Join(env.dir, "bootstrap_artifact")
	dataFile := filepath.Join(env.dir, "bootstrap_data")

	configPath := env.writeConfig("rpc", []string{
		fmt.Sprintf("touch %s", markerFile),
		fmt.Sprintf("echo 'chain_data_ready' > %s", dataFile),
	})

	output, exitCode := runBootstrap(t, configPath, env.basePath, []string{
		"MOCK_NODE_MODE=run_for",
		"MOCK_NODE_DURATION=2s",
		"MOCK_NODE_INTERVAL=200ms",
	}, 15*time.Second)

	assert.Equal(t, 0, exitCode, "full lifecycle should exit 0, output: %s", output)

	_, err := os.Stat(markerFile)
	assert.NoError(t, err, "bootstrap command should have created marker file")

	data, err := os.ReadFile(dataFile)
	assert.NoError(t, err)
	assert.Contains(t, string(data), "chain_data_ready")

	assert.Contains(t, output, "MOCK_NODE_BLOCK=1")
	assert.Contains(t, output, "MOCK_NODE_CLEAN_EXIT")
	assert.Contains(t, output, "MOCK_NODE_BLOCKS_PRODUCED=")

	blocksProduced := extractValue(output, "MOCK_NODE_BLOCKS_PRODUCED=")
	var blocks int
	n, err := fmt.Sscanf(blocksProduced, "%d", &blocks)
	require.NoError(t, err)
	assert.Equal(t, 1, n, "should parse one integer")
	assert.GreaterOrEqual(t, blocks, 1, "should have produced at least one block")
}

func TestSupervisor_StdoutStderrForwarding(t *testing.T) {
	t.Parallel()
	env := newTestEnv(t)
	configPath := env.writeConfig("rpc", nil)

	output, exitCode := runBootstrap(t, configPath, env.basePath, []string{
		"MOCK_NODE_MODE=run_for",
		"MOCK_NODE_DURATION=1s",
		"MOCK_NODE_INTERVAL=100ms",
	}, 10*time.Second)

	assert.Equal(t, 0, exitCode, "output: %s", output)
	assert.Contains(t, output, "MOCK_NODE_BLOCK=")
	assert.Contains(t, output, "MOCK_NODE_LOG block=")
	assert.Contains(t, output, "MOCK_NODE_CLEAN_EXIT")
}

func TestSupervisor_GracefulShutdownRunningProcess(t *testing.T) {
	t.Parallel()
	env := newTestEnv(t)
	configPath := env.writeConfig("rpc", nil)

	output, exitCode := runBootstrapWithSignal(
		t, configPath, env.basePath,
		[]string{
			"MOCK_NODE_MODE=run_for",
			"MOCK_NODE_DURATION=30s",
			"MOCK_NODE_INTERVAL=100ms",
		},
		2*time.Second,
		syscall.SIGTERM,
	)

	assert.Equal(t, 0, exitCode, "should shut down gracefully, output: %s", output)
	assert.Contains(t, output, "MOCK_NODE_BLOCK=")
	assert.Contains(t, output, "MOCK_NODE_SIGNAL_RECEIVED=")
	assert.Contains(t, output, "MOCK_NODE_BLOCKS_PRODUCED=")
}

func TestSupervisor_SIGINTDuringActiveProcess(t *testing.T) {
	t.Parallel()
	env := newTestEnv(t)
	configPath := env.writeConfig("listener", nil)

	keystorePath := filepath.Join(env.basePath, "keystore")

	output, exitCode := runBootstrapWithSignal(
		t, configPath, env.basePath,
		[]string{
			"MOCK_NODE_MODE=run_for",
			"MOCK_NODE_DURATION=30s",
			"MOCK_NODE_INTERVAL=200ms",
		},
		1500*time.Millisecond,
		syscall.SIGINT,
	)

	assert.Equal(t, 0, exitCode, "output: %s", output)
	assert.Contains(t, output, "MOCK_NODE_BLOCK=")
	assert.Contains(t, output, "MOCK_NODE_SIGNAL_RECEIVED=")

	_, err := os.Stat(keystorePath)
	assert.True(t, os.IsNotExist(err), "keystore should be cleaned up after SIGINT during active process")
}

func TestSupervisor_CrashDuringRun(t *testing.T) {
	t.Parallel()
	env := newTestEnv(t)
	configPath := env.writeConfig("rpc", nil)

	marker := filepath.Join(env.dir, "crash_marker")
	output, exitCode := runBootstrap(t, configPath, env.basePath, []string{
		"MOCK_NODE_MODE=crash_after_then_ok",
		"MOCK_NODE_DURATION=500ms",
		"MOCK_NODE_RUN_DURATION=1s",
		"MOCK_NODE_MARKER=" + marker,
	}, 30*time.Second)

	assert.Equal(t, 0, exitCode, "should recover after crash during run, output: %s", output)
	assert.Contains(t, output, "MOCK_NODE_STARTED_WILL_CRASH")
	assert.Contains(t, output, "MOCK_NODE_CRASH_AFTER_RUN")
	assert.Contains(t, output, "MOCK_NODE_RECOVERED_RUNNING")
	assert.Contains(t, output, "MOCK_NODE_CLEAN_EXIT")
}

func TestSupervisor_CrashDuringRunExhaustsRetries(t *testing.T) {
	t.Parallel()
	env := newTestEnv(t)
	configPath := env.writeConfig("rpc", nil)

	output, exitCode := runBootstrap(t, configPath, env.basePath, []string{
		"MOCK_NODE_MODE=crash_after",
		"MOCK_NODE_DURATION=500ms",
	}, 60*time.Second)

	assert.NotEqual(t, 0, exitCode, "should fail after retries exhausted, output: %s", output)

	crashCount := strings.Count(output, "MOCK_NODE_CRASH_AFTER_RUN")
	assert.GreaterOrEqual(t, crashCount, 2, "should have attempted multiple runs")
}

func TestSupervisor_BootstrapThenLongRunningNode(t *testing.T) {
	t.Parallel()
	env := newTestEnv(t)

	scriptFile := filepath.Join(env.dir, "setup.sh")
	artifactDir := filepath.Join(env.dir, "artifacts")
	require.NoError(t, os.WriteFile(scriptFile, []byte(fmt.Sprintf(
		"#!/bin/sh\nmkdir -p %s && echo 'keys_injected' > %s/keys.json && echo 'setup_complete'",
		artifactDir, artifactDir,
	)), 0o755))

	configPath := env.writeConfig("rpc", []string{
		fmt.Sprintf("sh %s", scriptFile),
	})

	output, exitCode := runBootstrap(t, configPath, env.basePath, []string{
		"MOCK_NODE_MODE=run_for",
		"MOCK_NODE_DURATION=3s",
		"MOCK_NODE_INTERVAL=500ms",
	}, 15*time.Second)

	assert.Equal(t, 0, exitCode, "output: %s", output)
	assert.Contains(t, output, "setup_complete")
	assert.Contains(t, output, "MOCK_NODE_BLOCK=1")
	assert.Contains(t, output, "MOCK_NODE_CLEAN_EXIT")

	keysData, err := os.ReadFile(filepath.Join(artifactDir, "keys.json"))
	require.NoError(t, err)
	assert.Equal(t, "keys_injected\n", string(keysData))

	_, err = os.Stat(env.stateFile)
	assert.NoError(t, err, "state file should persist after full lifecycle")

	var state map[string]interface{}
	stateData, _ := os.ReadFile(env.stateFile)
	require.NoError(t, json.Unmarshal(stateData, &state))
	assert.NotEmpty(t, state["commands_hash"])
	assert.NotEmpty(t, state["completed_at"])
}

func TestSupervisor_IdempotentBootstrapWithLongRunningNode(t *testing.T) {
	t.Parallel()
	env := newTestEnv(t)
	marker := filepath.Join(env.dir, "bootstrap_ran_marker")

	configPath := env.writeConfig("rpc", []string{
		fmt.Sprintf("touch %s", marker),
	})

	// First run: bootstrap + node runs for a while
	output1, exit1 := runBootstrap(t, configPath, env.basePath, []string{
		"MOCK_NODE_MODE=run_for",
		"MOCK_NODE_DURATION=1s",
	}, 10*time.Second)
	assert.Equal(t, 0, exit1, "first run, output: %s", output1)

	_, err := os.Stat(marker)
	assert.NoError(t, err, "marker should exist after first run")

	require.NoError(t, os.Remove(marker))

	// Second run: bootstrap skipped, node still runs
	output2, exit2 := runBootstrap(t, configPath, env.basePath, []string{
		"MOCK_NODE_MODE=run_for",
		"MOCK_NODE_DURATION=1s",
	}, 10*time.Second)
	assert.Equal(t, 0, exit2, "second run, output: %s", output2)

	_, err = os.Stat(marker)
	assert.True(t, os.IsNotExist(err), "bootstrap should not re-run on second invocation (marker should not be recreated)")

	assert.Contains(t, output2, "MOCK_NODE_CLEAN_EXIT", "node should still run on second invocation")
}

func extractValue(output, prefix string) string {
	for _, line := range strings.Split(output, "\n") {
		if strings.HasPrefix(line, prefix) {
			return strings.TrimPrefix(line, prefix)
		}
	}
	return ""
}
