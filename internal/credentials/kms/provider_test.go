package kms_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/rantipay/trading-mcp/internal/credentials/kms"
)

func TestMockKMSProvider_GenerateDataKey_NonNil(t *testing.T) {
	provider := &kms.MockKMSProvider{}
	ctx := context.Background()

	plaintext, wrapped, keyID, err := provider.GenerateDataKey(ctx)

	require.NoError(t, err)
	assert.NotNil(t, plaintext, "plaintext DEK must not be nil")
	assert.NotNil(t, wrapped, "wrapped DEK must not be nil")
	assert.NotEmpty(t, keyID, "key ID must not be empty")
	assert.Equal(t, 32, len(plaintext), "plaintext DEK must be 32 bytes (AES-256)")
	assert.Equal(t, 32, len(wrapped), "wrapped DEK must be 32 bytes")
}

func TestMockKMSProvider_RoundTrip(t *testing.T) {
	provider := &kms.MockKMSProvider{}
	ctx := context.Background()

	plaintext, wrapped, keyID, err := provider.GenerateDataKey(ctx)
	require.NoError(t, err)

	recovered, err := provider.UnwrapDataKey(ctx, wrapped, keyID)
	require.NoError(t, err)

	assert.Equal(t, plaintext, recovered, "UnwrapDataKey must recover the original plaintext DEK")
}

func TestMockKMSProvider_GenerateDataKey_ProducesRandomKeys(t *testing.T) {
	provider := &kms.MockKMSProvider{}
	ctx := context.Background()

	p1, _, _, err := provider.GenerateDataKey(ctx)
	require.NoError(t, err)

	p2, _, _, err := provider.GenerateDataKey(ctx)
	require.NoError(t, err)

	assert.NotEqual(t, p1, p2, "successive GenerateDataKey calls must return different DEKs")
}

func TestMockKMSProvider_Health(t *testing.T) {
	provider := &kms.MockKMSProvider{}
	err := provider.Health(context.Background())
	assert.NoError(t, err, "mock Health must always return nil")
}

func TestMockKMSProvider_UnwrapDataKey_EmptyInput(t *testing.T) {
	provider := &kms.MockKMSProvider{}
	_, err := provider.UnwrapDataKey(context.Background(), nil, "any-key")
	assert.Error(t, err, "UnwrapDataKey with empty wrapped DEK must return an error")
}
