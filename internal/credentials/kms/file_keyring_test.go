package kms_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/rantipay/trading-mcp/internal/credentials/kms"
)

func setupFileKeyring(t *testing.T) (provider *kms.FileKeyringProvider, cleanup func()) {
	t.Helper()

	dir := t.TempDir()
	t.Setenv("AGE_PASSPHRASE", "test-passphrase-for-unit-tests")

	p, err := kms.NewFileKeyringProvider(kms.FileKeyringConfig{DataDir: dir})
	require.NoError(t, err)

	return p, func() {
		os.Unsetenv("AGE_PASSPHRASE") //nolint:errcheck
	}
}

func TestFileKeyringProvider_RoundTrip(t *testing.T) {
	provider, cleanup := setupFileKeyring(t)
	defer cleanup()

	ctx := context.Background()

	plaintext, wrapped, keyID, err := provider.GenerateDataKey(ctx)
	require.NoError(t, err)
	assert.NotNil(t, plaintext)
	assert.NotNil(t, wrapped)
	assert.NotEmpty(t, keyID)

	recovered, err := provider.UnwrapDataKey(ctx, wrapped, keyID)
	require.NoError(t, err)
	assert.Equal(t, plaintext, recovered, "unwrapped DEK must match the original plaintext")
}

func TestFileKeyringProvider_UnwrapFromDisk(t *testing.T) {
	// Verify that UnwrapDataKey can recover the DEK even when wrapped is nil
	// (i.e. read from disk using the keyID).
	provider, cleanup := setupFileKeyring(t)
	defer cleanup()

	ctx := context.Background()

	plaintext, _, keyID, err := provider.GenerateDataKey(ctx)
	require.NoError(t, err)

	// Pass nil wrapped — provider should read from disk.
	recovered, err := provider.UnwrapDataKey(ctx, nil, keyID)
	require.NoError(t, err)
	assert.Equal(t, plaintext, recovered, "UnwrapDataKey(nil, keyID) must read from disk and recover DEK")
}

func TestFileKeyringProvider_Health(t *testing.T) {
	provider, cleanup := setupFileKeyring(t)
	defer cleanup()

	err := provider.Health(context.Background())
	assert.NoError(t, err, "Health must return nil for a writable keyring dir")
}

func TestFileKeyringProvider_UnwritableDir(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("running as root — permission checks don't apply")
	}

	// Create a temp dir and make it read-only.
	base := t.TempDir()
	roDir := filepath.Join(base, "readonly")
	require.NoError(t, os.MkdirAll(roDir, 0o500)) // r-x only, no write

	t.Setenv("AGE_PASSPHRASE", "test-passphrase")
	defer os.Unsetenv("AGE_PASSPHRASE") //nolint:errcheck

	_, err := kms.NewFileKeyringProvider(kms.FileKeyringConfig{DataDir: roDir})
	assert.Error(t, err, "NewFileKeyringProvider must fail when DataDir is not writable")
}

func TestFileKeyringProvider_MissingPassphrase(t *testing.T) {
	dir := t.TempDir()
	// Ensure env var is not set.
	os.Unsetenv("AGE_PASSPHRASE") //nolint:errcheck

	_, err := kms.NewFileKeyringProvider(kms.FileKeyringConfig{DataDir: dir})
	// Constructor succeeds (passphrase is only needed at DEK generation time).
	require.NoError(t, err)

	p, _ := kms.NewFileKeyringProvider(kms.FileKeyringConfig{DataDir: dir})
	_, _, _, err = p.GenerateDataKey(context.Background())
	assert.Error(t, err, "GenerateDataKey must fail when no passphrase is configured")
}
