// Package kms provides abstractions and implementations for key management
// operations used in envelope encryption of credential data keys (DEKs).
package kms

import (
	"context"
	"crypto/rand"
	"fmt"
	"io"
)

// KMSProvider abstracts key management operations for envelope encryption.
// Implementations: VaultTransitProvider (SaaS), FileKeyringProvider (self-hosted).
type KMSProvider interface {
	// GenerateDataKey creates a new random DEK and returns it plaintext + wrapped.
	GenerateDataKey(ctx context.Context) (plaintext []byte, wrapped []byte, keyID string, err error)
	// UnwrapDataKey decrypts a wrapped DEK using the master key identified by keyID.
	UnwrapDataKey(ctx context.Context, wrapped []byte, keyID string) (plaintext []byte, err error)
	// Health returns nil if the provider is reachable and operational.
	Health(ctx context.Context) error
}

const (
	// mockKeyID is the static key ID used by MockKMSProvider.
	mockKeyID = "mock-kms-key-v1"

	// dekSize is the size of generated data encryption keys in bytes (AES-256).
	dekSize = 32

	// xorKeyByte is the byte used for trivial XOR wrapping in MockKMSProvider.
	// This is intentionally insecure and only suitable for testing.
	xorKeyByte = byte(0xAB)
)

// MockKMSProvider is a no-op provider for testing. It generates real random DEKs
// and wraps them with a trivial single-byte XOR — never use in production.
type MockKMSProvider struct{}

// GenerateDataKey creates a cryptographically random 32-byte DEK, returns it
// plaintext and XOR-wrapped. Both slices are freshly allocated.
func (m *MockKMSProvider) GenerateDataKey(_ context.Context) (plaintext []byte, wrapped []byte, keyID string, err error) {
	plaintext = make([]byte, dekSize)
	if _, err = io.ReadFull(rand.Reader, plaintext); err != nil {
		return nil, nil, "", fmt.Errorf("mock kms: failed to generate DEK: %w", err)
	}

	wrapped = xorWrap(plaintext)
	return plaintext, wrapped, mockKeyID, nil
}

// UnwrapDataKey reverses the trivial XOR wrapping to recover the plaintext DEK.
func (m *MockKMSProvider) UnwrapDataKey(_ context.Context, wrapped []byte, _ string) (plaintext []byte, err error) {
	if len(wrapped) == 0 {
		return nil, fmt.Errorf("mock kms: wrapped DEK is empty")
	}
	// XOR is its own inverse.
	return xorWrap(wrapped), nil
}

// Health always returns nil for the mock provider.
func (m *MockKMSProvider) Health(_ context.Context) error {
	return nil
}

// xorWrap applies a single-byte XOR mask to every byte of src.
// Used only by MockKMSProvider — not cryptographically secure.
func xorWrap(src []byte) []byte {
	dst := make([]byte, len(src))
	for i, b := range src {
		dst[i] = b ^ xorKeyByte
	}
	return dst
}
