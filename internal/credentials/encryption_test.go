package credentials

import (
	"bytes"
	"testing"
)

func TestNewEncryptionService(t *testing.T) {
	// Test with valid 32-byte key
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i)
	}

	svc, err := NewEncryptionService(key)
	if err != nil {
		t.Fatalf("NewEncryptionService failed: %v", err)
	}

	if svc == nil {
		t.Fatal("Expected non-nil service")
	}

	if svc.GetKeyCount() != 1 {
		t.Errorf("Expected 1 key, got %d", svc.GetKeyCount())
	}

	keyID := svc.GetActiveKeyID()
	if keyID == "" {
		t.Error("Expected non-empty key ID")
	}
}

func TestNewEncryptionServiceInvalidKey(t *testing.T) {
	tests := []struct {
		name    string
		keySize int
	}{
		{"too short", 16},
		{"too long", 64},
		{"empty", 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			key := make([]byte, tt.keySize)
			_, err := NewEncryptionService(key)
			if err != ErrInvalidKeySize {
				t.Errorf("Expected ErrInvalidKeySize, got %v", err)
			}
		})
	}
}

func TestEncryptDecrypt(t *testing.T) {
	key, err := GenerateRandomKey()
	if err != nil {
		t.Fatalf("Failed to generate key: %v", err)
	}

	svc, err := NewEncryptionService(key)
	if err != nil {
		t.Fatalf("NewEncryptionService failed: %v", err)
	}

	tests := []struct {
		name      string
		plaintext []byte
	}{
		{"short text", []byte("hello")},
		{"API key", []byte("sk-1234567890abcdef")},
		{"long secret", []byte("this-is-a-very-long-secret-key-with-special-characters-!@#$%^&*()")},
		{"unicode", []byte("password-with-unicode: \xe4\xb8\xad\xe6\x96\x87")},
		{"binary data", []byte{0x00, 0x01, 0xff, 0xfe, 0x10, 0x20}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Encrypt
			ciphertext, keyID, err := svc.Encrypt(tt.plaintext)
			if err != nil {
				t.Fatalf("Encrypt failed: %v", err)
			}

			if len(ciphertext) == 0 {
				t.Error("Expected non-empty ciphertext")
			}

			if keyID == "" {
				t.Error("Expected non-empty key ID")
			}

			// Verify ciphertext is different from plaintext
			if bytes.Equal(ciphertext, tt.plaintext) {
				t.Error("Ciphertext should not equal plaintext")
			}

			// Decrypt
			decrypted, err := svc.Decrypt(ciphertext, keyID)
			if err != nil {
				t.Fatalf("Decrypt failed: %v", err)
			}

			if !bytes.Equal(decrypted, tt.plaintext) {
				t.Errorf("Decrypted text does not match original: got %q, want %q", decrypted, tt.plaintext)
			}
		})
	}
}

func TestEncryptDecryptString(t *testing.T) {
	key, _ := GenerateRandomKey()
	svc, _ := NewEncryptionService(key)

	plaintext := "my-secret-api-key-12345"

	// Encrypt as string
	ciphertextBase64, keyID, err := svc.EncryptString(plaintext)
	if err != nil {
		t.Fatalf("EncryptString failed: %v", err)
	}

	if ciphertextBase64 == "" {
		t.Error("Expected non-empty ciphertext")
	}

	// Decrypt as string
	decrypted, err := svc.DecryptString(ciphertextBase64, keyID)
	if err != nil {
		t.Fatalf("DecryptString failed: %v", err)
	}

	if decrypted != plaintext {
		t.Errorf("Decrypted text does not match: got %q, want %q", decrypted, plaintext)
	}
}

func TestDecryptWithWrongKey(t *testing.T) {
	// Create two services with different keys
	key1, _ := GenerateRandomKey()
	key2, _ := GenerateRandomKey()

	svc1, _ := NewEncryptionService(key1)
	svc2, _ := NewEncryptionService(key2)

	plaintext := []byte("secret data")

	// Encrypt with service 1
	ciphertext, keyID1, err := svc1.Encrypt(plaintext)
	if err != nil {
		t.Fatalf("Encrypt failed: %v", err)
	}

	// Try to decrypt with service 2 using wrong key ID
	_, err = svc2.Decrypt(ciphertext, keyID1)
	if err != ErrKeyNotFound {
		t.Errorf("Expected ErrKeyNotFound, got %v", err)
	}
}

func TestDecryptInvalidCiphertext(t *testing.T) {
	key, _ := GenerateRandomKey()
	svc, _ := NewEncryptionService(key)
	keyID := svc.GetActiveKeyID()

	tests := []struct {
		name       string
		ciphertext []byte
		wantErr    error
	}{
		{"too short", []byte{1, 2, 3}, ErrInvalidCiphertext},
		{"corrupted", make([]byte, 50), ErrDecryptionFailed},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := svc.Decrypt(tt.ciphertext, keyID)
			if err != tt.wantErr {
				t.Errorf("Expected %v, got %v", tt.wantErr, err)
			}
		})
	}
}

func TestAddKey(t *testing.T) {
	key1, _ := GenerateRandomKey()
	key2, _ := GenerateRandomKey()

	svc, _ := NewEncryptionService(key1)
	originalKeyID := svc.GetActiveKeyID()

	// Add a second key
	newKeyID, err := svc.AddKey(key2)
	if err != nil {
		t.Fatalf("AddKey failed: %v", err)
	}

	if newKeyID == "" {
		t.Error("Expected non-empty key ID")
	}

	if newKeyID == originalKeyID {
		t.Error("New key ID should be different from original")
	}

	if svc.GetActiveKeyID() != newKeyID {
		t.Error("Active key should be the newly added key")
	}

	if svc.GetKeyCount() != 2 {
		t.Errorf("Expected 2 keys, got %d", svc.GetKeyCount())
	}
}

func TestKeyRotation(t *testing.T) {
	key1, _ := GenerateRandomKey()
	key2, _ := GenerateRandomKey()

	svc, _ := NewEncryptionService(key1)
	keyID1 := svc.GetActiveKeyID()

	// Encrypt with key 1
	plaintext := []byte("sensitive data")
	ciphertext1, _, err := svc.Encrypt(plaintext)
	if err != nil {
		t.Fatalf("Encrypt failed: %v", err)
	}

	// Add key 2 (becomes active)
	keyID2, _ := svc.AddKey(key2)

	// Re-encrypt with new key
	ciphertext2, newKeyID, err := svc.ReEncrypt(ciphertext1, keyID1)
	if err != nil {
		t.Fatalf("ReEncrypt failed: %v", err)
	}

	if newKeyID != keyID2 {
		t.Errorf("Expected new key ID %s, got %s", keyID2, newKeyID)
	}

	// Verify decryption with new key works
	decrypted, err := svc.Decrypt(ciphertext2, keyID2)
	if err != nil {
		t.Fatalf("Decrypt with new key failed: %v", err)
	}

	if !bytes.Equal(decrypted, plaintext) {
		t.Error("Decrypted data does not match original")
	}

	// Verify decryption with old key still works
	decrypted, err = svc.Decrypt(ciphertext1, keyID1)
	if err != nil {
		t.Fatalf("Decrypt with old key failed: %v", err)
	}

	if !bytes.Equal(decrypted, plaintext) {
		t.Error("Decrypted data does not match original")
	}
}

func TestDeactivateKey(t *testing.T) {
	key1, _ := GenerateRandomKey()
	key2, _ := GenerateRandomKey()

	svc, _ := NewEncryptionService(key1)
	keyID1 := svc.GetActiveKeyID()

	// Add second key
	keyID2, _ := svc.AddKey(key2)

	// Deactivate first key
	err := svc.DeactivateKey(keyID1)
	if err != nil {
		t.Fatalf("DeactivateKey failed: %v", err)
	}

	// Active key should still be key2
	if svc.GetActiveKeyID() != keyID2 {
		t.Error("Active key should still be key2")
	}

	// First key should still work for decryption
	plaintext := []byte("test")
	ciphertext, _, _ := svc.Encrypt(plaintext)

	// This should use key2 since key1 is deactivated
	decrypted, err := svc.Decrypt(ciphertext, keyID2)
	if err != nil {
		t.Fatalf("Decrypt failed: %v", err)
	}

	if !bytes.Equal(decrypted, plaintext) {
		t.Error("Decrypted data does not match")
	}
}

func TestHasKey(t *testing.T) {
	key, _ := GenerateRandomKey()
	svc, _ := NewEncryptionService(key)
	keyID := svc.GetActiveKeyID()

	if !svc.HasKey(keyID) {
		t.Error("Expected HasKey to return true for existing key")
	}

	if svc.HasKey("non-existent-key") {
		t.Error("Expected HasKey to return false for non-existent key")
	}
}

func TestGenerateRandomKey(t *testing.T) {
	key1, err := GenerateRandomKey()
	if err != nil {
		t.Fatalf("GenerateRandomKey failed: %v", err)
	}

	if len(key1) != KeySize {
		t.Errorf("Expected key size %d, got %d", KeySize, len(key1))
	}

	// Generate another key and verify it's different
	key2, _ := GenerateRandomKey()
	if bytes.Equal(key1, key2) {
		t.Error("Generated keys should be different")
	}
}

func TestGenerateRandomKeyBase64(t *testing.T) {
	keyBase64, err := GenerateRandomKeyBase64()
	if err != nil {
		t.Fatalf("GenerateRandomKeyBase64 failed: %v", err)
	}

	if keyBase64 == "" {
		t.Error("Expected non-empty base64 key")
	}

	// Verify it can be used to create a service
	svc, err := NewEncryptionServiceFromBase64(keyBase64)
	if err != nil {
		t.Fatalf("NewEncryptionServiceFromBase64 failed: %v", err)
	}

	if svc == nil {
		t.Error("Expected non-nil service")
	}
}

func TestDeriveKey(t *testing.T) {
	password := []byte("my-secure-password")
	salt := []byte("random-salt-12345")

	key := DeriveKey(password, salt)

	if len(key) != KeySize {
		t.Errorf("Expected key size %d, got %d", KeySize, len(key))
	}

	// Same password and salt should produce same key
	key2 := DeriveKey(password, salt)
	if !bytes.Equal(key, key2) {
		t.Error("Same password and salt should produce same key")
	}

	// Different salt should produce different key
	key3 := DeriveKey(password, []byte("different-salt"))
	if bytes.Equal(key, key3) {
		t.Error("Different salt should produce different key")
	}
}

func TestEncryptEmptyPlaintext(t *testing.T) {
	key, _ := GenerateRandomKey()
	svc, _ := NewEncryptionService(key)

	_, _, err := svc.Encrypt([]byte{})
	if err == nil {
		t.Error("Expected error for empty plaintext")
	}
}

func BenchmarkEncrypt(b *testing.B) {
	key, _ := GenerateRandomKey()
	svc, _ := NewEncryptionService(key)
	plaintext := []byte("api-key-12345678901234567890")

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _, _ = svc.Encrypt(plaintext)
	}
}

func BenchmarkDecrypt(b *testing.B) {
	key, _ := GenerateRandomKey()
	svc, _ := NewEncryptionService(key)
	plaintext := []byte("api-key-12345678901234567890")
	ciphertext, keyID, _ := svc.Encrypt(plaintext)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = svc.Decrypt(ciphertext, keyID)
	}
}
