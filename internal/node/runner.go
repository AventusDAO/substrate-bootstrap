package node

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"syscall"
	"time"

	"go.uber.org/zap"

	"github.com/AventusDAO/substrate-bootstrap/internal/config"
)

type Runner struct {
	cfg                 *config.Config
	logger              *zap.Logger
	publicIP            string
	MaxRetries          int
	InitialBackoff      time.Duration
	MaxBackoff          time.Duration
	ShutdownGracePeriod time.Duration
}

func NewRunner(cfg *config.Config, logger *zap.Logger, publicIP string) *Runner {
	return &Runner{
		cfg:                 cfg,
		logger:              logger.With(zap.String("component", "node")),
		publicIP:            publicIP,
		MaxRetries:          3,
		InitialBackoff:      2 * time.Second,
		MaxBackoff:          30 * time.Second,
		ShutdownGracePeriod: 30 * time.Second,
	}
}

func (r *Runner) Run(ctx context.Context) error {
	args := BuildArgs(r.cfg, r.publicIP)

	r.logger.Info("starting node process",
		zap.String("binary", r.cfg.Node.Binary),
		zap.Strings("args", args))

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)
	defer signal.Stop(sigCh)

	var lastErr error
	for attempt := 0; attempt <= r.MaxRetries; attempt++ {
		if attempt > 0 {
			backoff := r.backoffDuration(attempt)
			r.logger.Warn("restarting node after unexpected exit",
				zap.Int("attempt", attempt),
				zap.Duration("backoff", backoff),
				zap.Error(lastErr))

			select {
			case <-time.After(backoff):
			case sig := <-sigCh:
				r.logger.Info("received signal during backoff, exiting", zap.String("signal", sig.String()))
				return nil
			case <-ctx.Done():
				r.logger.Info("context cancelled during backoff, exiting")
				return nil
			}
		}

		exitCode, terminated, err := r.runProcess(ctx, sigCh, args)
		if terminated {
			if err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
				return err
			}
			r.logger.Info("node stopped by signal")
			return nil
		}
		if exitCode == 0 {
			r.logger.Info("node exited cleanly")
			return nil
		}

		if err != nil {
			lastErr = fmt.Errorf("node exited with code %d: %w", exitCode, err)
		} else {
			lastErr = fmt.Errorf("node exited with code %d", exitCode)
		}
	}

	return fmt.Errorf("node failed after %d retries: %w", r.MaxRetries, lastErr)
}

func (r *Runner) runProcess(ctx context.Context, sigCh <-chan os.Signal, args []string) (exitCode int, terminated bool, err error) {
	// #nosec G204 -- node binary path comes from operator config; required to spawn the chain process
	cmd := exec.CommandContext(ctx, r.cfg.Node.Binary, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Start(); err != nil {
		return -1, false, fmt.Errorf("starting node process: %w", err)
	}

	r.logger.Info("node process started", zap.Int("pid", cmd.Process.Pid))

	doneCh := make(chan error, 1)
	go func() {
		doneCh <- cmd.Wait()
	}()

	select {
	case sig := <-sigCh:
		r.logger.Info("received signal, forwarding to node", zap.String("signal", sig.String()))
		if err := cmd.Process.Signal(sig); err != nil {
			r.logger.Warn("failed to forward signal, killing process", zap.Error(err))
			_ = cmd.Process.Kill()
		}
		gracePeriod := r.ShutdownGracePeriod
		if gracePeriod <= 0 {
			gracePeriod = 30 * time.Second
		}
		select {
		case <-doneCh:
			return 0, true, nil
		case <-time.After(gracePeriod):
			r.logger.Warn("node did not exit within grace period, force-killing",
				zap.Duration("grace_period", gracePeriod))
			_ = cmd.Process.Kill()
			<-doneCh
			return -1, true, fmt.Errorf("node did not exit within %s, force-killed", gracePeriod)
		}

	case err := <-doneCh:
		if err == nil {
			return 0, false, nil
		}
		if exitErr, ok := err.(*exec.ExitError); ok {
			return exitErr.ExitCode(), false, err
		}
		return -1, false, err

	case <-ctx.Done():
		_ = cmd.Process.Kill()
		<-doneCh
		return -1, true, ctx.Err()
	}
}

func (r *Runner) backoffDuration(attempt int) time.Duration {
	d := r.InitialBackoff
	for i := 1; i < attempt; i++ {
		d *= 2
		if d > r.MaxBackoff {
			return r.MaxBackoff
		}
	}
	return d
}
