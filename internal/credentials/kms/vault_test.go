package kms_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/rantipay/trading-mcp/internal/credentials/kms"
)

func TestVaultTransitProvider_GenerateDataKey_ServerError(t *testing.T) {
	// Vault endpoint that always returns HTTP 500.
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"errors":["internal server error"]}`))
	}))
	defer ts.Close()

	cfg := kms.VaultConfig{
		Address:        ts.URL,
		RoleID:         "test-role-id",
		SecretID:       "test-secret-id",
		TransitKeyName: "test-key",
	}

	provider, err := kms.NewVaultProvider(cfg)
	require.NoError(t, err)

	_, _, _, err = provider.GenerateDataKey(context.Background())
	require.Error(t, err, "GenerateDataKey should return an error when Vault returns 500")
	assert.Contains(t, err.Error(), "500", "error should mention HTTP 500 status")
}

func TestVaultTransitProvider_Health_ServerError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer ts.Close()

	cfg := kms.VaultConfig{
		Address:        ts.URL,
		RoleID:         "test-role-id",
		SecretID:       "test-secret-id",
		TransitKeyName: "test-key",
	}

	provider, err := kms.NewVaultProvider(cfg)
	require.NoError(t, err)

	err = provider.Health(context.Background())
	assert.Error(t, err, "Health should return an error when Vault health endpoint returns 500")
}

func TestNewVaultProvider_EmptyAddress(t *testing.T) {
	_, err := kms.NewVaultProvider(kms.VaultConfig{
		RoleID:         "r",
		SecretID:       "s",
		TransitKeyName: "k",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "Address")
}

func TestNewVaultProvider_EmptyRoleID(t *testing.T) {
	_, err := kms.NewVaultProvider(kms.VaultConfig{
		Address:        "https://vault.example.com",
		SecretID:       "s",
		TransitKeyName: "k",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "RoleID")
}

func TestNewVaultProvider_EmptySecretID(t *testing.T) {
	_, err := kms.NewVaultProvider(kms.VaultConfig{
		Address:        "https://vault.example.com",
		RoleID:         "r",
		TransitKeyName: "k",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "SecretID")
}

func TestNewVaultProvider_EmptyTransitKeyName(t *testing.T) {
	_, err := kms.NewVaultProvider(kms.VaultConfig{
		Address:  "https://vault.example.com",
		RoleID:   "r",
		SecretID: "s",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "TransitKeyName")
}

func TestVaultTransitProvider_GenerateDataKey_ConnectionRefused(t *testing.T) {
	// Use a port that is almost certainly not listening.
	cfg := kms.VaultConfig{
		Address:        "http://127.0.0.1:19999",
		RoleID:         "r",
		SecretID:       "s",
		TransitKeyName: "k",
	}

	provider, err := kms.NewVaultProvider(cfg)
	require.NoError(t, err)

	ctx := context.Background()
	_, _, _, err = provider.GenerateDataKey(ctx)
	require.Error(t, err, "should return error when Vault is not reachable")
}
