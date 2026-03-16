package keystore

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func testManager(t *testing.T, path string, cleanup bool) *Manager {
	t.Helper()
	logger, err := zap.NewDevelopment()
	require.NoError(t, err)
	return NewManager(path, cleanup, logger)
}

func TestEnsureDirectory_Creates(t *testing.T) {
	dir := t.TempDir()
	keystorePath := filepath.Join(dir, "keystore", "nested")

	m := testManager(t, keystorePath, false)
	err := m.EnsureDirectory()
	require.NoError(t, err)

	info, err := os.Stat(keystorePath)
	require.NoError(t, err)
	assert.True(t, info.IsDir())
}

func TestEnsureDirectory_AlreadyExists(t *testing.T) {
	dir := t.TempDir()
	keystorePath := filepath.Join(dir, "keystore")
	require.NoError(t, os.MkdirAll(keystorePath, 0o700))

	m := testManager(t, keystorePath, false)
	err := m.EnsureDirectory()
	require.NoError(t, err)
}

func TestCleanup_RemovesDirectory(t *testing.T) {
	dir := t.TempDir()
	keystorePath := filepath.Join(dir, "keystore")
	require.NoError(t, os.MkdirAll(keystorePath, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(keystorePath, "key1"), []byte("data"), 0o600))

	m := testManager(t, keystorePath, true)
	err := m.Cleanup()
	require.NoError(t, err)

	_, err = os.Stat(keystorePath)
	assert.True(t, os.IsNotExist(err))
}

func TestCleanup_DisabledSkips(t *testing.T) {
	dir := t.TempDir()
	keystorePath := filepath.Join(dir, "keystore")
	require.NoError(t, os.MkdirAll(keystorePath, 0o700))

	m := testManager(t, keystorePath, false)
	err := m.Cleanup()
	require.NoError(t, err)

	_, err = os.Stat(keystorePath)
	require.NoError(t, err)
}

func TestCleanup_NonexistentDirectory(t *testing.T) {
	dir := t.TempDir()
	keystorePath := filepath.Join(dir, "does-not-exist")

	m := testManager(t, keystorePath, true)
	err := m.Cleanup()
	require.NoError(t, err)
}
