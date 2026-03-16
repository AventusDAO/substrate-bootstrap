package bootstrap

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/nicce/substrate-bootstrap/internal/config"
	"github.com/nicce/substrate-bootstrap/internal/snapshot"
)

type mockExecutor struct {
	commands []string
	failAt   int // 1-based: fail when this many commands have been received
}

func (m *mockExecutor) Execute(_ context.Context, command string) error {
	m.commands = append(m.commands, command)
	if m.failAt > 0 && len(m.commands) >= m.failAt {
		return errors.New("mock command failure")
	}
	return nil
}

func newTestBootstrapper(t *testing.T, cfg config.BootstrapConfig, stateFile string, executor CommandExecutor) *Bootstrapper {
	t.Helper()
	logger, err := zap.NewDevelopment()
	require.NoError(t, err)
	return NewBootstrapper(logger, cfg, stateFile, executor)
}

func TestBootstrapper_FirstBootstrap(t *testing.T) {
	dir := t.TempDir()
	stateFile := filepath.Join(dir, "bootstrap_state.json")
	cfg := config.BootstrapConfig{
		Commands: []string{"echo hello", "echo world"},
	}

	exec := &mockExecutor{}
	b := newTestBootstrapper(t, cfg, stateFile, exec)

	err := b.Run(context.Background())
	require.NoError(t, err)

	assert.Equal(t, []string{"echo hello", "echo world"}, exec.commands)

	data, err := os.ReadFile(stateFile)
	require.NoError(t, err)

	var state BootstrapState
	require.NoError(t, json.Unmarshal(data, &state))
	assert.Equal(t, hashCommands(cfg.Commands), state.CommandsHash)
	assert.Equal(t, cfg.Commands, state.Commands)
	assert.Equal(t, PhaseCompleted, state.Phase)
	assert.Equal(t, 1, state.CompletedStep)
	assert.NotEmpty(t, state.StartedAt)
	assert.NotEmpty(t, state.CompletedAt)
}

func TestBootstrapper_AlreadyBootstrapped(t *testing.T) {
	dir := t.TempDir()
	stateFile := filepath.Join(dir, "bootstrap_state.json")
	cfg := config.BootstrapConfig{
		Commands: []string{"echo done"},
	}

	state := BootstrapState{
		Phase:         PhaseCompleted,
		CommandsHash:  hashCommands(cfg.Commands),
		Commands:      cfg.Commands,
		CompletedStep: 0,
		StartedAt:     "2024-01-01T00:00:00Z",
		CompletedAt:   "2024-01-01T00:00:00Z",
	}
	stateData, err := json.MarshalIndent(state, "", "  ")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(stateFile, stateData, 0o644))

	exec := &mockExecutor{}
	b := newTestBootstrapper(t, cfg, stateFile, exec)

	err = b.Run(context.Background())
	require.NoError(t, err)

	assert.Empty(t, exec.commands)
}

func TestBootstrapper_ConfigChangeDetection(t *testing.T) {
	dir := t.TempDir()
	stateFile := filepath.Join(dir, "bootstrap_state.json")
	originalCommands := []string{"echo original"}
	state := BootstrapState{
		Phase:         PhaseCompleted,
		CommandsHash:  hashCommands(originalCommands),
		Commands:      originalCommands,
		CompletedStep: 0,
		StartedAt:     "2024-01-01T00:00:00Z",
		CompletedAt:   "2024-01-01T00:00:00Z",
	}
	stateData, err := json.MarshalIndent(state, "", "  ")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(stateFile, stateData, 0o644))

	cfg := config.BootstrapConfig{
		Commands: []string{"echo changed"},
	}

	exec := &mockExecutor{}
	b := newTestBootstrapper(t, cfg, stateFile, exec)

	err = b.Run(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "bootstrap config changed")
	assert.Contains(t, err.Error(), "wipe")
	assert.Contains(t, err.Error(), dir, "error should include effective data dir path")
	assert.Empty(t, exec.commands)
}

func TestBootstrapper_CommandFailure(t *testing.T) {
	dir := t.TempDir()
	stateFile := filepath.Join(dir, "bootstrap_state.json")
	cfg := config.BootstrapConfig{
		Commands: []string{"echo ok", "echo fail", "echo never"},
	}

	exec := &mockExecutor{failAt: 2}
	b := newTestBootstrapper(t, cfg, stateFile, exec)

	err := b.Run(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "bootstrap command 1 failed")
	assert.Contains(t, err.Error(), "mock command failure")

	assert.Equal(t, []string{"echo ok", "echo fail"}, exec.commands)

	data, err := os.ReadFile(stateFile)
	require.NoError(t, err)
	var state BootstrapState
	require.NoError(t, json.Unmarshal(data, &state))
	assert.Equal(t, PhaseCommands, state.Phase)
	assert.Equal(t, 0, state.CompletedStep, "only the first command succeeded")
}

func TestBootstrapper_ResumeAfterFailure(t *testing.T) {
	dir := t.TempDir()
	stateFile := filepath.Join(dir, "bootstrap_state.json")
	commands := []string{"echo step0", "echo step1", "echo step2"}

	state := BootstrapState{
		Phase:         PhaseCommands,
		StartedAt:     "2024-01-01T00:00:00Z",
		CommandsHash:  hashCommands(commands),
		Commands:      commands,
		CompletedStep: 0,
	}
	stateData, err := json.MarshalIndent(state, "", "  ")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(stateFile, stateData, 0o644))

	cfg := config.BootstrapConfig{
		Commands: commands,
	}

	exec := &mockExecutor{}
	b := newTestBootstrapper(t, cfg, stateFile, exec)

	err = b.Run(context.Background())
	require.NoError(t, err)

	assert.Equal(t, []string{"echo step1", "echo step2"}, exec.commands, "should skip step 0")

	data, err := os.ReadFile(stateFile)
	require.NoError(t, err)
	var finalState BootstrapState
	require.NoError(t, json.Unmarshal(data, &finalState))
	assert.Equal(t, PhaseCompleted, finalState.Phase)
	assert.Equal(t, 2, finalState.CompletedStep)
	assert.NotEmpty(t, finalState.CompletedAt)
}

func TestBootstrapper_ResumeAllStepsAlreadyDone(t *testing.T) {
	dir := t.TempDir()
	stateFile := filepath.Join(dir, "bootstrap_state.json")
	commands := []string{"echo only"}

	state := BootstrapState{
		Phase:         PhaseCommands,
		StartedAt:     "2024-01-01T00:00:00Z",
		CommandsHash:  hashCommands(commands),
		Commands:      commands,
		CompletedStep: 0,
	}
	stateData, err := json.MarshalIndent(state, "", "  ")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(stateFile, stateData, 0o644))

	cfg := config.BootstrapConfig{Commands: commands}
	exec := &mockExecutor{}
	b := newTestBootstrapper(t, cfg, stateFile, exec)

	err = b.Run(context.Background())
	require.NoError(t, err)
	assert.Empty(t, exec.commands, "no commands should re-run")

	data, err := os.ReadFile(stateFile)
	require.NoError(t, err)
	var finalState BootstrapState
	require.NoError(t, json.Unmarshal(data, &finalState))
	assert.Equal(t, PhaseCompleted, finalState.Phase)
}

func TestBootstrapper_EmptyCommands(t *testing.T) {
	dir := t.TempDir()
	stateFile := filepath.Join(dir, "bootstrap_state.json")
	cfg := config.BootstrapConfig{
		Commands: []string{},
	}

	exec := &mockExecutor{}
	b := newTestBootstrapper(t, cfg, stateFile, exec)

	err := b.Run(context.Background())
	require.NoError(t, err)

	assert.Empty(t, exec.commands)
	_, err = os.Stat(stateFile)
	assert.True(t, os.IsNotExist(err))
}

func TestBootstrapper_CorruptStateFile(t *testing.T) {
	dir := t.TempDir()
	stateFile := filepath.Join(dir, "bootstrap_state.json")
	require.NoError(t, os.WriteFile(stateFile, []byte("not valid json"), 0o644))

	cfg := config.BootstrapConfig{
		Commands: []string{"echo test"},
	}

	exec := &mockExecutor{}
	b := newTestBootstrapper(t, cfg, stateFile, exec)

	err := b.Run(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "corrupt bootstrap state file")
	assert.Empty(t, exec.commands)
}

func TestBootstrapper_ContextCancellation(t *testing.T) {
	dir := t.TempDir()
	stateFile := filepath.Join(dir, "bootstrap_state.json")
	cfg := config.BootstrapConfig{
		Commands: []string{"echo test"},
	}

	exec := &mockExecutor{}
	b := newTestBootstrapper(t, cfg, stateFile, exec)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := b.Run(ctx)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "bootstrap cancelled")
}

func TestBootstrapper_SnapshotStatusRecorded(t *testing.T) {
	dir := t.TempDir()
	stateFile := filepath.Join(dir, "bootstrap_state.json")
	cfg := config.BootstrapConfig{
		Commands: []string{"echo setup"},
	}

	exec := &mockExecutor{}
	b := newTestBootstrapper(t, cfg, stateFile, exec)
	b.SetSnapshotResults(
		&snapshot.SyncResult{Downloaded: true, Method: "tar", URL: "https://example.io/chain.tar.gz", DataPath: "/data/chain"},
		&snapshot.SyncResult{Skipped: true, URL: "https://example.io/relay", DataPath: "/data/relay"},
	)

	err := b.Run(context.Background())
	require.NoError(t, err)

	data, err := os.ReadFile(stateFile)
	require.NoError(t, err)

	var state BootstrapState
	require.NoError(t, json.Unmarshal(data, &state))

	require.NotNil(t, state.ChainSnapshot)
	assert.True(t, state.ChainSnapshot.Downloaded)
	assert.Equal(t, "tar", state.ChainSnapshot.Method)
	assert.Equal(t, "https://example.io/chain.tar.gz", state.ChainSnapshot.URL)

	require.NotNil(t, state.RelaySnapshot)
	assert.True(t, state.RelaySnapshot.Skipped)
	assert.Equal(t, "https://example.io/relay", state.RelaySnapshot.URL)
}

func TestBootstrapper_SnapshotOnlyNoCommands(t *testing.T) {
	dir := t.TempDir()
	stateFile := filepath.Join(dir, "bootstrap_state.json")
	cfg := config.BootstrapConfig{
		Commands: []string{},
	}

	exec := &mockExecutor{}
	b := newTestBootstrapper(t, cfg, stateFile, exec)
	b.SetSnapshotResults(
		&snapshot.SyncResult{Downloaded: true, Method: "rclone", URL: "https://snapshots.polkadot.io/snap", DataPath: "/data/chain"},
		nil,
	)

	err := b.Run(context.Background())
	require.NoError(t, err)

	data, err := os.ReadFile(stateFile)
	require.NoError(t, err)

	var state BootstrapState
	require.NoError(t, json.Unmarshal(data, &state))
	assert.Equal(t, PhaseCompleted, state.Phase)
	require.NotNil(t, state.ChainSnapshot)
	assert.True(t, state.ChainSnapshot.Downloaded)
	assert.Nil(t, state.RelaySnapshot)
}

func TestBootstrapper_StepByStepStatePersistence(t *testing.T) {
	dir := t.TempDir()
	stateFile := filepath.Join(dir, "bootstrap_state.json")
	cfg := config.BootstrapConfig{
		Commands: []string{"echo a", "echo b", "echo c"},
	}

	exec := &mockExecutor{failAt: 3}
	b := newTestBootstrapper(t, cfg, stateFile, exec)

	err := b.Run(context.Background())
	require.Error(t, err)

	data, err := os.ReadFile(stateFile)
	require.NoError(t, err)

	var state BootstrapState
	require.NoError(t, json.Unmarshal(data, &state))
	assert.Equal(t, PhaseCommands, state.Phase)
	assert.Equal(t, 1, state.CompletedStep, "steps 0 and 1 ran; step 2 (failAt=3) failed")
	assert.Empty(t, state.CompletedAt, "should not be completed")
}

func TestShellExecutor_Execute(t *testing.T) {
	exec := &ShellExecutor{}
	err := exec.Execute(context.Background(), "echo -n ok")
	require.NoError(t, err)
}

func TestShellExecutor_FailingCommand(t *testing.T) {
	exec := &ShellExecutor{}
	err := exec.Execute(context.Background(), "exit 1")
	require.Error(t, err)
}

func TestHashCommands(t *testing.T) {
	h1 := hashCommands([]string{"a", "b"})
	h2 := hashCommands([]string{"a", "b"})
	h3 := hashCommands([]string{"b", "a"})

	assert.Equal(t, h1, h2)
	assert.NotEqual(t, h1, h3)
	assert.Len(t, h1, 64)
}
