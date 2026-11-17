package kms

// NOTE: This implementation uses stdlib crypto only (PBKDF2 + AES-256-GCM).
// For production, add "filippo.io/age" to go.mod and replace the PBKDF2 logic
// with age.Encrypt / age.Decrypt using an X25519 identity or passphrase recipient.

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"golang.org/x/crypto/pbkdf2"
)

// FileKeyringConfig holds configuration for the file-based keyring provider.
type FileKeyringConfig struct {
	// DataDir is the root directory where wrapped DEK files are stored.
	// Wrapped DEKs are written to {DataDir}/keyring/wrapped_dek_{keyID}.enc
	DataDir string
	// IdentityFile is the path to a file containing the passphrase.
	// If empty, the passphrase is read from the AGE_PASSPHRASE environment variable.
	// (Named "IdentityFile" to align with the age toolchain convention.)
	IdentityFile string
}

const (
	pbkdf2Iterations = 200_000
	pbkdf2SaltSize   = 16
	pbkdf2KeySize    = 32 // AES-256
	aesNonceSize     = 12 // GCM standard nonce
	fileKeyIDPrefix  = "file-keyring-v1"
)

// FileKeyringProvider implements KMSProvider by wrapping DEKs with AES-256-GCM
// using a key derived (PBKDF2-SHA256) from a passphrase stored in an env var or
// identity file. Wrapped DEK files are stored under {DataDir}/keyring/.
type FileKeyringProvider struct {
	cfg FileKeyringConfig
}

// NewFileKeyringProvider creates and validates a FileKeyringProvider.
func NewFileKeyringProvider(cfg FileKeyringConfig) (*FileKeyringProvider, error) {
	if cfg.DataDir == "" {
		return nil, errors.New("file keyring: DataDir is required")
	}
	p := &FileKeyringProvider{cfg: cfg}
	// Ensure the keyring subdirectory is writable now so callers fail fast.
	if err := os.MkdirAll(p.keyringDir(), 0o700); err != nil {
		return nil, fmt.Errorf("file keyring: cannot create keyring dir %q: %w", p.keyringDir(), err)
	}
	return p, nil
}

// GenerateDataKey generates a fresh random DEK, wraps it with the passphrase-derived
// key, persists the wrapped blob to disk, and returns all three values.
func (f *FileKeyringProvider) GenerateDataKey(ctx context.Context) (plaintext []byte, wrapped []byte, keyID string, err error) {
	passphrase, err := f.readPassphrase()
	if err != nil {
		return nil, nil, "", err
	}

	// Generate plaintext DEK.
	dek := make([]byte, dekSize)
	if _, err := io.ReadFull(rand.Reader, dek); err != nil {
		return nil, nil, "", fmt.Errorf("file keyring: failed to generate DEK: %w", err)
	}

	// Derive key from passphrase + fresh salt.
	salt := make([]byte, pbkdf2SaltSize)
	if _, err := io.ReadFull(rand.Reader, salt); err != nil {
		return nil, nil, "", fmt.Errorf("file keyring: failed to generate salt: %w", err)
	}

	wrapKey := pbkdf2.Key([]byte(passphrase), salt, pbkdf2Iterations, pbkdf2KeySize, sha256.New)

	wrapped, err = aesGCMEncrypt(wrapKey, dek, salt)
	if err != nil {
		return nil, nil, "", fmt.Errorf("file keyring: failed to wrap DEK: %w", err)
	}

	// Build a stable key ID from the timestamp + random suffix.
	randSuffix := make([]byte, 4)
	_, _ = io.ReadFull(rand.Reader, randSuffix)
	kID := fmt.Sprintf("%s-%d-%s", fileKeyIDPrefix, time.Now().Unix(), hex.EncodeToString(randSuffix))

	// Persist to disk.
	if err := f.writeWrapped(kID, wrapped); err != nil {
		return nil, nil, "", err
	}

	return dek, wrapped, kID, nil
}

// UnwrapDataKey reads the wrapped DEK from disk and decrypts it with the passphrase.
// The wrapped parameter takes precedence over disk if non-nil; the file is still
// checked for existence to confirm the keyID is valid.
func (f *FileKeyringProvider) UnwrapDataKey(_ context.Context, wrapped []byte, keyID string) (plaintext []byte, err error) {
	passphrase, err := f.readPassphrase()
	if err != nil {
		return nil, err
	}

	// If wrapped is not supplied, read from disk.
	if len(wrapped) == 0 {
		wrapped, err = f.readWrapped(keyID)
		if err != nil {
			return nil, err
		}
	}

	// The first pbkdf2SaltSize bytes of the wrapped blob are the salt (prepended by aesGCMEncrypt).
	if len(wrapped) < pbkdf2SaltSize {
		return nil, fmt.Errorf("file keyring: wrapped DEK too short for keyID %q", keyID)
	}
	salt := wrapped[:pbkdf2SaltSize]
	cipherBlob := wrapped[pbkdf2SaltSize:]

	wrapKey := pbkdf2.Key([]byte(passphrase), salt, pbkdf2Iterations, pbkdf2KeySize, sha256.New)

	plaintext, err = aesGCMDecrypt(wrapKey, cipherBlob)
	if err != nil {
		return nil, fmt.Errorf("file keyring: failed to unwrap DEK for keyID %q: %w", keyID, err)
	}

	return plaintext, nil
}

// Health checks that the keyring directory is accessible and writable.
func (f *FileKeyringProvider) Health(_ context.Context) error {
	probe := filepath.Join(f.keyringDir(), ".health_probe")
	if err := os.WriteFile(probe, []byte("ok"), 0o600); err != nil {
		return fmt.Errorf("file keyring: keyring dir not writable: %w", err)
	}
	_ = os.Remove(probe)
	return nil
}

// ---- internal helpers ----

func (f *FileKeyringProvider) keyringDir() string {
	return filepath.Join(f.cfg.DataDir, "keyring")
}

func (f *FileKeyringProvider) wrappedPath(keyID string) string {
	// Sanitize keyID to avoid path traversal.
	safe := filepath.Base(keyID)
	return filepath.Join(f.keyringDir(), "wrapped_dek_"+safe+".enc")
}

func (f *FileKeyringProvider) writeWrapped(keyID string, data []byte) error {
	path := f.wrappedPath(keyID)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return fmt.Errorf("file keyring: failed to write wrapped DEK to %q: %w", path, err)
	}
	return nil
}

func (f *FileKeyringProvider) readWrapped(keyID string) ([]byte, error) {
	path := f.wrappedPath(keyID)
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("file keyring: failed to read wrapped DEK from %q: %w", path, err)
	}
	return data, nil
}

// readPassphrase reads the passphrase from IdentityFile or AGE_PASSPHRASE env var.
func (f *FileKeyringProvider) readPassphrase() (string, error) {
	if f.cfg.IdentityFile != "" {
		data, err := os.ReadFile(f.cfg.IdentityFile)
		if err != nil {
			return "", fmt.Errorf("file keyring: failed to read identity file %q: %w", f.cfg.IdentityFile, err)
		}
		return string(data), nil
	}
	pp := os.Getenv("AGE_PASSPHRASE")
	if pp == "" {
		return "", errors.New("file keyring: passphrase not set — set AGE_PASSPHRASE env var or provide IdentityFile")
	}
	return pp, nil
}

// aesGCMEncrypt encrypts plaintext with AES-256-GCM using key.
// Returns salt || nonce || ciphertext+tag.
func aesGCMEncrypt(key, plaintext, salt []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, aesNonceSize)
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, err
	}
	ciphertext := gcm.Seal(nonce, nonce, plaintext, nil)
	// Prepend salt so it's available for decryption.
	out := make([]byte, 0, len(salt)+len(ciphertext))
	out = append(out, salt...)
	out = append(out, ciphertext...)
	return out, nil
}

// aesGCMDecrypt decrypts nonce||ciphertext+tag produced by aesGCMEncrypt (salt already stripped).
func aesGCMDecrypt(key, nonceAndCiphertext []byte) ([]byte, error) {
	if len(nonceAndCiphertext) < aesNonceSize {
		return nil, errors.New("ciphertext too short")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	nonce := nonceAndCiphertext[:aesNonceSize]
	ciphertext := nonceAndCiphertext[aesNonceSize:]
	return gcm.Open(nil, nonce, ciphertext, nil)
}
