package bootstrap

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"go.uber.org/zap"

	"github.com/AventusDAO/substrate-bootstrap/internal/config"
	"github.com/AventusDAO/substrate-bootstrap/internal/snapshot"
)

const (
	PhaseSnapshots = "snapshots"
	PhaseCommands  = "commands"
	PhaseCompleted = "completed"
)

type CommandExecutor interface {
	Execute(ctx context.Context, command string) error
}

// ShellExecutor executes commands using sh -c.
// When running in distroless or static containers, an sh-compatible shell
// (e.g. /bin/sh or BusyBox-provided sh) must be available for bootstrap commands to succeed.
type ShellExecutor struct{}

func (s *ShellExecutor) Execute(ctx context.Context, command string) error {
	// #nosec G204 -- bootstrap runs operator-configured shell commands by design
	cmd := exec.CommandContext(ctx, "sh", "-c", command)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

type BootstrapState struct {
	Phase         string               `json:"phase"`
	StartedAt     string               `json:"started_at"`
	CompletedAt   string               `json:"completed_at,omitempty"`
	CommandsHash  string               `json:"commands_hash"`
	Commands      []string             `json:"commands"`
	CompletedStep int                  `json:"completed_step"`
	ChainSnapshot *snapshot.SyncResult `json:"chain_snapshot,omitempty"`
	RelaySnapshot *snapshot.SyncResult `json:"relay_snapshot,omitempty"`
}

type Bootstrapper struct {
	cfg           config.BootstrapConfig
	stateFile     string
	executor      CommandExecutor
	logger        *zap.Logger
	chainSnapshot *snapshot.SyncResult
	relaySnapshot *snapshot.SyncResult
}

func NewBootstrapper(logger *zap.Logger, cfg config.BootstrapConfig, stateFile string, executor CommandExecutor) *Bootstrapper {
	return &Bootstrapper{
		cfg:       cfg,
		stateFile: stateFile,
		executor:  executor,
		logger:    logger.With(zap.String("component", "bootstrap")),
	}
}

func (b *Bootstrapper) SetSnapshotResults(chain, relay *snapshot.SyncResult) {
	b.chainSnapshot = chain
	b.relaySnapshot = relay
}

func (b *Bootstrapper) Run(ctx context.Context) error {
	if len(b.cfg.Commands) == 0 {
		b.logger.Info("no bootstrap commands configured, skipping")
		b.saveSnapshotOnlyState()
		return nil
	}

	currentHash := hashCommands(b.cfg.Commands)

	existing, err := b.loadState()
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("reading bootstrap state: %w", err)
	}

	if existing != nil {
		if existing.CommandsHash == currentHash && existing.Phase == PhaseCompleted {
			b.logger.Info("bootstrap already completed, skipping",
				zap.String("completed_at", existing.CompletedAt))
			return nil
		}
		if existing.CommandsHash != currentHash {
			dataDir := filepath.Dir(b.stateFile)
			return fmt.Errorf("bootstrap config changed (hash %s -> %s); wipe %s and restart",
				existing.CommandsHash, currentHash, dataDir)
		}
		if existing.Phase == PhaseCommands {
			return b.resumeCommands(ctx, existing)
		}
	}

	state := &BootstrapState{
		Phase:         PhaseCommands,
		StartedAt:     time.Now().UTC().Format(time.RFC3339),
		CommandsHash:  currentHash,
		Commands:      b.cfg.Commands,
		CompletedStep: -1,
		ChainSnapshot: b.chainSnapshot,
		RelaySnapshot: b.relaySnapshot,
	}

	b.logger.Info("running bootstrap commands", zap.Int("count", len(b.cfg.Commands)))
	return b.executeFrom(ctx, state, 0)
}

func (b *Bootstrapper) resumeCommands(ctx context.Context, state *BootstrapState) error {
	resumeFrom := state.CompletedStep + 1
	if resumeFrom >= len(state.Commands) {
		b.logger.Info("all commands already completed, finalizing state")
		return b.finalize(state)
	}

	b.logger.Info("resuming bootstrap from interrupted step",
		zap.Int("resume_from", resumeFrom),
		zap.Int("total_commands", len(state.Commands)))

	state.ChainSnapshot = b.chainSnapshot
	state.RelaySnapshot = b.relaySnapshot
	return b.executeFrom(ctx, state, resumeFrom)
}

func (b *Bootstrapper) executeFrom(ctx context.Context, state *BootstrapState, startIdx int) error {
	for i := startIdx; i < len(b.cfg.Commands); i++ {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("bootstrap cancelled: %w", err)
		}

		cmd := b.cfg.Commands[i]
		b.logger.Info("executing bootstrap command",
			zap.Int("step", i),
			zap.Int("total", len(b.cfg.Commands)),
			zap.String("command", cmd))

		if err := b.executor.Execute(ctx, cmd); err != nil {
			return fmt.Errorf("bootstrap command %d failed: %w", i, err)
		}

		state.CompletedStep = i
		if err := b.saveState(state); err != nil {
			b.logger.Warn("failed to save intermediate state", zap.Error(err))
		}
	}

	return b.finalize(state)
}

func (b *Bootstrapper) finalize(state *BootstrapState) error {
	state.Phase = PhaseCompleted
	state.CompletedAt = time.Now().UTC().Format(time.RFC3339)
	if err := b.saveState(state); err != nil {
		return fmt.Errorf("saving bootstrap state: %w", err)
	}
	b.logger.Info("bootstrap completed successfully")
	return nil
}

func (b *Bootstrapper) saveSnapshotOnlyState() {
	if b.chainSnapshot == nil && b.relaySnapshot == nil {
		return
	}
	state := &BootstrapState{
		Phase:         PhaseCompleted,
		StartedAt:     time.Now().UTC().Format(time.RFC3339),
		CompletedAt:   time.Now().UTC().Format(time.RFC3339),
		CompletedStep: -1,
		ChainSnapshot: b.chainSnapshot,
		RelaySnapshot: b.relaySnapshot,
	}
	if err := b.saveState(state); err != nil {
		b.logger.Warn("failed to save snapshot-only state", zap.Error(err))
	}
}

func (b *Bootstrapper) loadState() (*BootstrapState, error) {
	data, err := os.ReadFile(b.stateFile)
	if err != nil {
		return nil, err
	}
	var state BootstrapState
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, fmt.Errorf("corrupt bootstrap state file: %w", err)
	}
	return &state, nil
}

func (b *Bootstrapper) saveState(state *BootstrapState) error {
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(b.stateFile, data, 0o600)
}

func hashCommands(commands []string) string {
	h := sha256.New()
	h.Write([]byte(strings.Join(commands, "\n")))
	return fmt.Sprintf("%x", h.Sum(nil))
}
