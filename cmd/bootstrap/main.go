package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"go.uber.org/zap"

	"github.com/AventusDAO/substrate-bootstrap/internal/bootstrap"
	"github.com/AventusDAO/substrate-bootstrap/internal/config"
	"github.com/AventusDAO/substrate-bootstrap/internal/keystore"
	"github.com/AventusDAO/substrate-bootstrap/internal/logging"
	"github.com/AventusDAO/substrate-bootstrap/internal/node"
	"github.com/AventusDAO/substrate-bootstrap/internal/publicip"
	"github.com/AventusDAO/substrate-bootstrap/internal/snapshot"
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

	logger, err := logging.NewLogger(cfg.Logging, cfg.Node.Name)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error initializing logger: %v\n", err)
		return ExitGeneralError
	}
	defer func() { _ = logger.Sync() }()

	logBootnodeWarnings(cfg, logger)

	logger.Info("substrate-bootstrap starting",
		zap.String("version", Version),
		zap.String("build_time", BuildTime),
		zap.String("config", *configPath))

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
		chainDest := config.ChainDBDataPath(
			strings.TrimSpace(cfg.Chain.ChainData.ChainID),
			cfg.Chain.ChainData.Database,
		)
		result, err := snapDl.SyncIfNeeded(ctx, cfg.Chain.SnapshotURL, chainDest, strings.TrimSpace(cfg.Chain.ChainData.ChainID))
		if err != nil {
			return fmt.Errorf("chain snapshot: %w", err)
		}
		chainSnap = result
	}
	if !cfg.IsSolochain() && cfg.RelayChain.SnapshotURL != "" && !cfg.Chain.RelayChainLightClient {
		relayDest := config.RelayChainDBDataPath(
			strings.TrimSpace(cfg.RelayChain.ChainData.ChainID),
			cfg.RelayChain.ChainData.Database,
		)
		result, err := snapDl.SyncIfNeeded(ctx, cfg.RelayChain.SnapshotURL, relayDest, strings.TrimSpace(cfg.RelayChain.ChainData.ChainID))
		if err != nil {
			return fmt.Errorf("relay chain snapshot: %w", err)
		}
		relaySnap = result
	}

	if cfg.Chain.ChainspecURL != "" {
		dest := config.ChainspecPath()
		if err := snapDl.DownloadChainspec(ctx, cfg.Chain.ChainspecURL, dest, cfg.Chain.ForceDownloadChainspec); err != nil {
			return fmt.Errorf("chain chainspec: %w", err)
		}
		cfg.Chain.ChainSpec = dest
	}
	if !cfg.IsSolochain() && cfg.RelayChain.ChainspecURL != "" {
		dest := config.RelayChainspecPath()
		if err := snapDl.DownloadChainspec(ctx, cfg.RelayChain.ChainspecURL, dest, cfg.RelayChain.ForceDownloadChainspec); err != nil {
			return fmt.Errorf("relay chain chainspec: %w", err)
		}
		cfg.RelayChain.ChainSpec = dest
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

	publicIP := resolvePublicIP(ctx, logger, cfg)
	runner := node.NewRunner(cfg, logger, publicIP)
	err := runner.Run(ctx)

	if keystoreMgr != nil {
		if cleanupErr := keystoreMgr.Cleanup(); cleanupErr != nil {
			logger.Error("keystore cleanup failed", zap.Error(cleanupErr))
		}
	}

	return err
}

func resolvePublicIP(ctx context.Context, logger *zap.Logger, cfg *config.Config) string {
	if v := os.Getenv("SUBSTRATE_BOOTSTRAP_DISABLE_PUBLIC_IP"); strings.EqualFold(v, "1") || strings.EqualFold(v, "true") || strings.EqualFold(v, "yes") {
		logger.Info("Public IP lookup disabled via SUBSTRATE_BOOTSTRAP_DISABLE_PUBLIC_IP")
		return ""
	}
	if cfg.Chain.Port == 40333 {
		return ""
	}
	client := &http.Client{Timeout: 5 * time.Second}
	ip, err := publicip.Fetch(ctx, client)
	if err != nil {
		logger.Warn("Failed to get public IP (continuing without --public-addr)", zap.Error(err))
		return ""
	}
	logger.Info("Detected public IP", zap.String("public_ip", ip))
	return ip
}

func logBootnodeWarnings(cfg *config.Config, logger *zap.Logger) {
	chainBootnodes := cfg.Chain.Bootnodes
	if len(cfg.Chain.OverrideBootnodes) > 0 {
		chainBootnodes = cfg.Chain.OverrideBootnodes
	}
	if len(chainBootnodes) == 0 {
		logger.Warn("no chain bootnodes configured in config; if the chainspec also lacks bootnodes, the node may be unable to peer")
	}
	if !cfg.IsSolochain() && len(cfg.RelayChain.Bootnodes) == 0 {
		logger.Warn("no relay_chain bootnodes configured in config; if the relay chainspec also lacks bootnodes, the node may be unable to peer")
	}
}

func checkDataDirectory(basePath string) error {
	info, err := os.Stat(basePath)
	if os.IsNotExist(err) {
		if mkErr := os.MkdirAll(basePath, 0o750); mkErr != nil {
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

	testFile := filepath.Join(basePath, ".write_test")
	if err := os.WriteFile(testFile, []byte("ok"), 0o600); err != nil {
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
