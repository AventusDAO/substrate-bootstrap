package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"

	"go.uber.org/zap"

	"github.com/nicce/substrate-bootstrap/internal/bootstrap"
	"github.com/nicce/substrate-bootstrap/internal/config"
	"github.com/nicce/substrate-bootstrap/internal/keystore"
	"github.com/nicce/substrate-bootstrap/internal/logging"
	"github.com/nicce/substrate-bootstrap/internal/node"
	"github.com/nicce/substrate-bootstrap/internal/snapshot"
)

var (
	Version   = "dev"
	BuildTime = "unknown"
)

const (
	ExitSuccess         = 0
	ExitGeneralError    = 1
	ExitConfigChanged   = 2
	ExitPermissionError = 3
)

func deriveRole(cfg *config.Config) string {
	if cfg.Node.EnableKeystore {
		return "listener"
	}
	return "node"
}

func main() {
	os.Exit(mainE())
}

func mainE() int {
	configPath := flag.String("config", "", "path to YAML config file")
	showVersion := flag.Bool("v", false, "print version and exit")
	flag.Parse()

	if *showVersion {
		fmt.Printf("substrate-bootstrap version %s (build: %s)\n", Version, BuildTime)
		return ExitSuccess
	}

	if *configPath == "" {
		fmt.Fprintln(os.Stderr, "error: --config flag is required")
		flag.Usage()
		return ExitGeneralError
	}

	cfg, err := config.Load(*configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error loading config: %v\n", err)
		return ExitGeneralError
	}

	role := deriveRole(cfg)
	logger, err := logging.NewLogger(cfg.Logging, role, cfg.Node.Name)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error initializing logger: %v\n", err)
		return ExitGeneralError
	}
	defer func() { _ = logger.Sync() }()

	logger.Info("substrate-bootstrap starting",
		zap.String("version", Version),
		zap.String("build_time", BuildTime),
		zap.String("config", *configPath),
		zap.String("role", role))

	ctx := context.Background()

	if err := run(ctx, cfg, logger); err != nil {
		code := classifyError(err)
		logger.Error("fatal error", zap.Error(err), zap.Int("exit_code", code))
		return code
	}

	return ExitSuccess
}

func run(ctx context.Context, cfg *config.Config, logger *zap.Logger) error {
	if err := checkDataDirectory(config.DataDir()); err != nil {
		return &exitError{code: ExitPermissionError, err: err}
	}

	snapDl := snapshot.NewDownloader(logger)

	var chainSnap, relaySnap *snapshot.SyncResult

	if cfg.Chain.SnapshotURL != "" {
		result, err := snapDl.SyncIfNeeded(ctx, cfg.Chain.SnapshotURL, config.ChainDataPath())
		if err != nil {
			return fmt.Errorf("chain snapshot: %w", err)
		}
		chainSnap = result
	}
	if !cfg.IsSolochain() && cfg.RelayChain.SnapshotURL != "" {
		result, err := snapDl.SyncIfNeeded(ctx, cfg.RelayChain.SnapshotURL, config.RelayChainDataPath())
		if err != nil {
			return fmt.Errorf("relay chain snapshot: %w", err)
		}
		relaySnap = result
	}

	var keystoreMgr *keystore.Manager
	if cfg.Node.EnableKeystore {
		keystoreMgr = keystore.NewManager(config.KeystorePath(), cfg.Keystore.CleanupOnStop, logger)
		if err := keystoreMgr.EnsureDirectory(); err != nil {
			return fmt.Errorf("keystore setup: %w", err)
		}
	}

	bootstrapper := bootstrap.NewBootstrapper(logger, cfg.Bootstrap, config.BootstrapStatePath(), &bootstrap.ShellExecutor{})
	bootstrapper.SetSnapshotResults(chainSnap, relaySnap)
	if err := bootstrapper.Run(ctx); err != nil {
		return fmt.Errorf("bootstrap: %w", err)
	}

	runner := node.NewRunner(cfg, logger)
	err := runner.Run(ctx)

	if keystoreMgr != nil {
		if cleanupErr := keystoreMgr.Cleanup(); cleanupErr != nil {
			logger.Error("keystore cleanup failed", zap.Error(cleanupErr))
		}
	}

	return err
}

func checkDataDirectory(basePath string) error {
	info, err := os.Stat(basePath)
	if os.IsNotExist(err) {
		if mkErr := os.MkdirAll(basePath, 0o755); mkErr != nil {
			return fmt.Errorf("cannot create data directory %s: %w", basePath, mkErr)
		}
		return nil
	}
	if err != nil {
		return fmt.Errorf("cannot stat data directory %s: %w", basePath, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("data path %s exists but is not a directory", basePath)
	}

	testFile := basePath + "/.write_test"
	if err := os.WriteFile(testFile, []byte("ok"), 0o644); err != nil {
		return fmt.Errorf("data directory %s is not writable: %w", basePath, err)
	}
	_ = os.Remove(testFile)
	return nil
}

type exitError struct {
	code int
	err  error
}

func (e *exitError) Error() string { return e.err.Error() }
func (e *exitError) Unwrap() error { return e.err }

func classifyError(err error) int {
	var ee *exitError
	if errors.As(err, &ee) {
		return ee.code
	}

	msg := err.Error()
	if strings.Contains(msg, "bootstrap config changed") || strings.Contains(msg, "wipe /data") {
		return ExitConfigChanged
	}

	return ExitGeneralError
}
