package credentials

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/rantipay/trading-mcp/internal/credentials/kms"
)

const (
	// NonceSize is the size of the GCM nonce in bytes (12 bytes = 96 bits, recommended for GCM)
	NonceSize = 12

	// KeySize is the size of the AES-256 key in bytes (32 bytes = 256 bits)
	KeySize = 32

	// KeyIDLength is the length of the key ID prefix
	KeyIDLength = 8
)

var (
	// ErrInvalidKeySize is returned when the master key is not 32 bytes
	ErrInvalidKeySize = errors.New("master key must be 32 bytes (256 bits)")

	// ErrDecryptionFailed is returned when decryption fails
	ErrDecryptionFailed = errors.New("decryption failed: ciphertext may be corrupted or key is incorrect")

	// ErrInvalidCiphertext is returned when the ciphertext is too short
	ErrInvalidCiphertext = errors.New("ciphertext is too short")

	// ErrKeyNotFound is returned when the encryption key ID is not found
	ErrKeyNotFound = errors.New("encryption key not found")

	// ErrKeyExpired is returned when the encryption key has expired
	ErrKeyExpired = errors.New("encryption key has expired")
)

// EncryptionKey represents a versioned encryption key for key rotation support.
type EncryptionKey struct {
	ID        string
	Key       []byte
	CreatedAt time.Time
	ExpiresAt *time.Time
	IsActive  bool

	// WrappedDEK holds the KMS-wrapped (encrypted) version of Key.
	// Non-nil only when this key was generated via a KMSProvider (envelope encryption).
	WrappedDEK []byte
	// KMSKeyID is the identifier of the master key used to wrap WrappedDEK.
	// Empty for legacy keys that use a static master key.
	KMSKeyID string
}

// EncryptionService handles AES-256-GCM encryption and decryption of credentials.
// It supports key rotation by maintaining a map of key IDs to keys.
//
// Dual-mode operation:
//   - Legacy mode (kmsProvider == nil): uses a static master key supplied at construction.
//   - KMS mode (kmsProvider != nil): uses envelope encryption — the EncryptionService asks
//     the KMSProvider for a DEK, encrypts data with it, and stores the wrapped DEK alongside
//     the ciphertext so it can recover the DEK after a restart via UnwrapDataKey.
type EncryptionService struct {
	mu          sync.RWMutex
	keys        map[string]*EncryptionKey
	activeKeyID string

	// kmsProvider is optional. When nil the service operates in legacy mode.
	kmsProvider kms.KMSProvider
}

// NewEncryptionService creates a new EncryptionService with the provided master key.
// The master key must be exactly 32 bytes (256 bits) for AES-256.
func NewEncryptionService(masterKey []byte) (*EncryptionService, error) {
	if len(masterKey) != KeySize {
		return nil, ErrInvalidKeySize
	}

	// Generate a key ID from the first 8 bytes of the key's SHA-256 hash
	keyID := generateKeyID(masterKey)

	encKey := &EncryptionKey{
		ID:        keyID,
		Key:       masterKey,
		CreatedAt: time.Now(),
		IsActive:  true,
	}

	return &EncryptionService{
		keys:        map[string]*EncryptionKey{keyID: encKey},
		activeKeyID: keyID,
	}, nil
}

// NewEncryptionServiceFromBase64 creates a new EncryptionService from a base64-encoded master key.
func NewEncryptionServiceFromBase64(masterKeyBase64 string) (*EncryptionService, error) {
	masterKey, err := base64.StdEncoding.DecodeString(masterKeyBase64)
	if err != nil {
		return nil, fmt.Errorf("failed to decode master key: %w", err)
	}
	return NewEncryptionService(masterKey)
}

// SetKMSProvider attaches a KMSProvider to an existing EncryptionService, switching it
// from legacy mode to envelope encryption mode for all future Encrypt calls.
// Existing keys (legacy) remain usable for Decrypt.
func (e *EncryptionService) SetKMSProvider(provider kms.KMSProvider) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.kmsProvider = provider
}

// GetOrCreateActiveKey returns the active EncryptionKey, creating a new DEK via the
// KMSProvider if one is configured (KMS mode). In legacy mode it simply returns the
// current active key.
//
// In KMS mode the returned key has WrappedDEK and KMSKeyID populated. The key is
// registered in the keys map so subsequent Encrypt calls find it immediately.
func (e *EncryptionService) GetOrCreateActiveKey(ctx context.Context) (*EncryptionKey, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	// Legacy mode — return current active key unchanged.
	if e.kmsProvider == nil {
		k, ok := e.keys[e.activeKeyID]
		if !ok {
			return nil, errors.New("encryption: no active key")
		}
		return k, nil
	}

	// KMS mode — generate a fresh DEK (provider caches it internally).
	plaintext, wrapped, kmsKeyID, err := e.kmsProvider.GenerateDataKey(ctx)
	if err != nil {
		return nil, fmt.Errorf("encryption: KMS GenerateDataKey failed: %w", err)
	}

	keyID := generateKeyID(plaintext)

	// Reuse the key if it was already registered (provider returned cached DEK).
	if existing, ok := e.keys[keyID]; ok {
		e.activeKeyID = keyID
		return existing, nil
	}

	encKey := &EncryptionKey{
		ID:         keyID,
		Key:        plaintext,
		CreatedAt:  time.Now(),
		IsActive:   true,
		WrappedDEK: wrapped,
		KMSKeyID:   kmsKeyID,
	}
	e.keys[keyID] = encKey
	e.activeKeyID = keyID
	return encKey, nil
}

// AddKey adds a new encryption key for key rotation.
// The new key becomes the active key for encryption.
func (e *EncryptionService) AddKey(key []byte) (string, error) {
	if len(key) != KeySize {
		return "", ErrInvalidKeySize
	}

	e.mu.Lock()
	defer e.mu.Unlock()

	keyID := generateKeyID(key)

	// Check if key already exists
	if _, exists := e.keys[keyID]; exists {
		return keyID, nil
	}

	encKey := &EncryptionKey{
		ID:        keyID,
		Key:       key,
		CreatedAt: time.Now(),
		IsActive:  true,
	}

	e.keys[keyID] = encKey
	e.activeKeyID = keyID

	return keyID, nil
}

// DeactivateKey marks a key as inactive but keeps it for decryption of existing data.
func (e *EncryptionService) DeactivateKey(keyID string) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	key, exists := e.keys[keyID]
	if !exists {
		return ErrKeyNotFound
	}

	key.IsActive = false

	// If this was the active key, we need a new active key
	if e.activeKeyID == keyID {
		for id, k := range e.keys {
			if k.IsActive {
				e.activeKeyID = id
				return nil
			}
		}
		return errors.New("no active encryption keys remaining")
	}

	return nil
}

// Encrypt encrypts plaintext using AES-256-GCM with the active key.
// Returns the ciphertext (nonce prepended) and the key ID used.
func (e *EncryptionService) Encrypt(plaintext []byte) (ciphertext []byte, keyID string, err error) {
	if len(plaintext) == 0 {
		return nil, "", errors.New("plaintext cannot be empty")
	}

	e.mu.RLock()
	activeKey := e.keys[e.activeKeyID]
	e.mu.RUnlock()

	if activeKey == nil {
		return nil, "", errors.New("no active encryption key")
	}

	if !activeKey.IsActive {
		return nil, "", errors.New("active key has been deactivated")
	}

	if activeKey.ExpiresAt != nil && activeKey.ExpiresAt.Before(time.Now()) {
		return nil, "", ErrKeyExpired
	}

	// Create AES cipher block
	block, err := aes.NewCipher(activeKey.Key)
	if err != nil {
		return nil, "", fmt.Errorf("failed to create cipher: %w", err)
	}

	// Create GCM cipher mode
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, "", fmt.Errorf("failed to create GCM: %w", err)
	}

	// Generate random nonce
	nonce := make([]byte, NonceSize)
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, "", fmt.Errorf("failed to generate nonce: %w", err)
	}

	// Encrypt with GCM (includes authentication tag)
	// The ciphertext includes the nonce prepended
	ciphertext = gcm.Seal(nonce, nonce, plaintext, nil)

	return ciphertext, activeKey.ID, nil
}

// EncryptString encrypts a string and returns base64-encoded ciphertext.
func (e *EncryptionService) EncryptString(plaintext string) (ciphertextBase64 string, keyID string, err error) {
	ciphertext, keyID, err := e.Encrypt([]byte(plaintext))
	if err != nil {
		return "", "", err
	}
	return base64.StdEncoding.EncodeToString(ciphertext), keyID, nil
}

// Decrypt decrypts ciphertext using AES-256-GCM with the specified key.
// The ciphertext must have the nonce prepended.
//
// KMS restart recovery: if the EncryptionKey for keyID has a WrappedDEK but an empty
// Key (plaintext DEK wiped after a restart), and a KMSProvider is configured, this
// method automatically calls UnwrapDataKey to recover the DEK before decrypting.
func (e *EncryptionService) Decrypt(ciphertext []byte, keyID string) (plaintext []byte, err error) {
	if len(ciphertext) < NonceSize {
		return nil, ErrInvalidCiphertext
	}

	e.mu.RLock()
	encKey, exists := e.keys[keyID]
	provider := e.kmsProvider
	e.mu.RUnlock()

	if !exists {
		return nil, ErrKeyNotFound
	}

	// KMS restart recovery: key record exists but plaintext DEK was not loaded.
	if len(encKey.Key) == 0 && len(encKey.WrappedDEK) != 0 && provider != nil {
		recovered, err := provider.UnwrapDataKey(context.Background(), encKey.WrappedDEK, encKey.KMSKeyID)
		if err != nil {
			return nil, fmt.Errorf("encryption: failed to unwrap DEK for key %q: %w", keyID, err)
		}
		e.mu.Lock()
		encKey.Key = recovered
		e.mu.Unlock()
	}

	if len(encKey.Key) == 0 {
		return nil, fmt.Errorf("encryption: plaintext DEK for key %q is unavailable", keyID)
	}

	// Create AES cipher block
	block, err := aes.NewCipher(encKey.Key)
	if err != nil {
		return nil, fmt.Errorf("failed to create cipher: %w", err)
	}

	// Create GCM cipher mode
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("failed to create GCM: %w", err)
	}

	// Extract nonce from the beginning of ciphertext
	nonce := ciphertext[:NonceSize]
	ciphertextData := ciphertext[NonceSize:]

	// Decrypt and verify authentication tag
	plaintext, err = gcm.Open(nil, nonce, ciphertextData, nil)
	if err != nil {
		return nil, ErrDecryptionFailed
	}

	return plaintext, nil
}

// DecryptString decrypts base64-encoded ciphertext and returns the plaintext string.
func (e *EncryptionService) DecryptString(ciphertextBase64 string, keyID string) (string, error) {
	ciphertext, err := base64.StdEncoding.DecodeString(ciphertextBase64)
	if err != nil {
		return "", fmt.Errorf("failed to decode ciphertext: %w", err)
	}

	plaintext, err := e.Decrypt(ciphertext, keyID)
	if err != nil {
		return "", err
	}

	return string(plaintext), nil
}

// GetActiveKeyID returns the ID of the currently active encryption key.
func (e *EncryptionService) GetActiveKeyID() string {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.activeKeyID
}

// GetKeyCount returns the number of keys in the service.
func (e *EncryptionService) GetKeyCount() int {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return len(e.keys)
}

// HasKey checks if a key with the given ID exists.
func (e *EncryptionService) HasKey(keyID string) bool {
	e.mu.RLock()
	defer e.mu.RUnlock()
	_, exists := e.keys[keyID]
	return exists
}

// ReEncrypt decrypts data with the old key and re-encrypts it with the active key.
// This is used during key rotation to migrate existing credentials.
func (e *EncryptionService) ReEncrypt(ciphertext []byte, oldKeyID string) (newCiphertext []byte, newKeyID string, err error) {
	// Decrypt with old key
	plaintext, err := e.Decrypt(ciphertext, oldKeyID)
	if err != nil {
		return nil, "", fmt.Errorf("failed to decrypt with old key: %w", err)
	}

	// Encrypt with new (active) key
	newCiphertext, newKeyID, err = e.Encrypt(plaintext)
	if err != nil {
		return nil, "", fmt.Errorf("failed to encrypt with new key: %w", err)
	}

	return newCiphertext, newKeyID, nil
}

// generateKeyID generates a unique key ID from the key bytes.
func generateKeyID(key []byte) string {
	hash := sha256.Sum256(key)
	return base64.URLEncoding.EncodeToString(hash[:KeyIDLength])
}

// GenerateRandomKey generates a cryptographically secure random 32-byte key.
func GenerateRandomKey() ([]byte, error) {
	key := make([]byte, KeySize)
	if _, err := io.ReadFull(rand.Reader, key); err != nil {
		return nil, fmt.Errorf("failed to generate random key: %w", err)
	}
	return key, nil
}

// GenerateRandomKeyBase64 generates a cryptographically secure random key and returns it as base64.
func GenerateRandomKeyBase64() (string, error) {
	key, err := GenerateRandomKey()
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(key), nil
}

// DeriveKey derives a 32-byte key from a password and salt using SHA-256.
// Note: For production use, consider using Argon2id or PBKDF2 for password-based key derivation.
func DeriveKey(password, salt []byte) []byte {
	combined := append(password, salt...)
	hash := sha256.Sum256(combined)
	return hash[:]
}

// DeriveKeyFromString derives a 32-byte key from a password string and salt string.
func DeriveKeyFromString(password, salt string) []byte {
	return DeriveKey([]byte(password), []byte(salt))
}
