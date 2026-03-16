package keystore

import (
	"fmt"
	"os"

	"go.uber.org/zap"
)

type Manager struct {
	path          string
	cleanupOnStop bool
	logger        *zap.Logger
}

func NewManager(path string, cleanupOnStop bool, logger *zap.Logger) *Manager {
	return &Manager{
		path:          path,
		cleanupOnStop: cleanupOnStop,
		logger:        logger.With(zap.String("component", "keystore")),
	}
}

func (m *Manager) EnsureDirectory() error {
	m.logger.Info("ensuring keystore directory exists", zap.String("path", m.path))
	if err := os.MkdirAll(m.path, 0o700); err != nil {
		return fmt.Errorf("creating keystore directory %s: %w", m.path, err)
	}
	return nil
}

func (m *Manager) Cleanup() error {
	if !m.cleanupOnStop {
		m.logger.Info("keystore cleanup disabled, skipping")
		return nil
	}

	m.logger.Info("cleaning up keystore directory", zap.String("path", m.path))
	if err := os.RemoveAll(m.path); err != nil {
		return fmt.Errorf("removing keystore directory %s: %w", m.path, err)
	}
	m.logger.Info("keystore directory removed")
	return nil
}
