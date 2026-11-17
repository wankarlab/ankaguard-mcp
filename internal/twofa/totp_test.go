package twofa

import (
	"strings"
	"testing"
	"time"
)

// TestTOTPEnrollAndVerify enrolls a user, generates a valid TOTP code for the
// current period, and verifies it returns true.
func TestTOTPEnrollAndVerify(t *testing.T) {
	svc := NewTOTPService("TestIssuer")

	result, err := svc.Enroll("user-001")
	if err != nil {
		t.Fatalf("Enroll returned unexpected error: %v", err)
	}

	if result.Secret == "" {
		t.Error("Secret must not be empty")
	}
	if !strings.HasPrefix(result.QRCodeURL, "otpauth://totp/") {
		t.Errorf("QRCodeURL has unexpected prefix: %s", result.QRCodeURL)
	}
	if len(result.RecoveryCodes) != recoveryCount {
		t.Errorf("expected %d recovery codes, got %d", recoveryCount, len(result.RecoveryCodes))
	}
	for _, c := range result.RecoveryCodes {
		if len(c) != recoveryLen {
			t.Errorf("recovery code length: want %d, got %d (code=%q)", recoveryLen, len(c), c)
		}
	}

	// Generate expected OTP for the current counter.
	counter := time.Now().Unix() / totpPeriod
	code, err := generateTOTP(result.Secret, counter)
	if err != nil {
		t.Fatalf("generateTOTP error: %v", err)
	}

	if !svc.Verify(result.Secret, code) {
		t.Error("Verify should return true for a freshly generated code")
	}
}

// TestTOTPVerify_InvalidCode ensures that a wrong code is rejected.
func TestTOTPVerify_InvalidCode(t *testing.T) {
	svc := NewTOTPService("TestIssuer")

	result, err := svc.Enroll("user-002")
	if err != nil {
		t.Fatalf("Enroll error: %v", err)
	}

	cases := []string{
		"000000",
		"999999",
		"123456",
		"abcdef",
		"12345",  // too short
		"1234567", // too long
	}

	for _, code := range cases {
		if svc.Verify(result.Secret, code) {
			// A collision with the actual OTP is astronomically unlikely but
			// possible — only fail if the code length is correct.
			if len(code) == totpDigits {
				// Re-generate and compare to rule out a fluke.
				counter := time.Now().Unix() / totpPeriod
				for delta := int64(-totpWindow); delta <= int64(totpWindow); delta++ {
					real, _ := generateTOTP(result.Secret, counter+delta)
					if real == code {
						// Genuine match — skip this test case.
						goto nextCase
					}
				}
				t.Errorf("Verify(%q) = true, want false", code)
			} else {
				t.Errorf("Verify(%q) = true for wrong-length code, want false", code)
			}
		}
	nextCase:
	}
}

// TestTOTPVerify_Window checks that codes from the adjacent period are also accepted.
func TestTOTPVerify_Window(t *testing.T) {
	svc := NewTOTPService("TestIssuer")

	result, err := svc.Enroll("user-003")
	if err != nil {
		t.Fatalf("Enroll error: %v", err)
	}

	counter := time.Now().Unix() / totpPeriod

	// Previous period.
	codePrev, err := generateTOTP(result.Secret, counter-1)
	if err != nil {
		t.Fatalf("generateTOTP (-1) error: %v", err)
	}
	if !svc.Verify(result.Secret, codePrev) {
		t.Error("Verify should accept code from previous period (window=1)")
	}

	// Next period.
	codeNext, err := generateTOTP(result.Secret, counter+1)
	if err != nil {
		t.Fatalf("generateTOTP (+1) error: %v", err)
	}
	if !svc.Verify(result.Secret, codeNext) {
		t.Error("Verify should accept code from next period (window=1)")
	}
}

// TestRecoveryCodes exercises HashRecoveryCodes and VerifyRecoveryCode.
func TestRecoveryCodes(t *testing.T) {
	svc := NewTOTPService("TestIssuer")

	plain := []string{"abc12345", "xyz67890", "qwerty12"}
	hashed := HashRecoveryCodes(plain)

	if len(hashed) != len(plain) {
		t.Fatalf("HashRecoveryCodes length mismatch: want %d, got %d", len(plain), len(hashed))
	}

	// Each hash must be a 64-char hex string (SHA-256).
	for i, h := range hashed {
		if len(h) != 64 {
			t.Errorf("hash[%d] length: want 64, got %d", i, len(h))
		}
		if h == plain[i] {
			t.Errorf("hash[%d] must differ from plain code", i)
		}
	}

	// Verify a valid code — it should be consumed.
	valid, remaining := svc.VerifyRecoveryCode(hashed, plain[1])
	if !valid {
		t.Error("VerifyRecoveryCode should return true for matching code")
	}
	if len(remaining) != len(hashed)-1 {
		t.Errorf("remaining count: want %d, got %d", len(hashed)-1, len(remaining))
	}
	// Ensure the consumed code is absent.
	consumed := HashRecoveryCodes([]string{plain[1]})[0]
	for _, r := range remaining {
		if r == consumed {
			t.Error("consumed code must not appear in remaining list")
		}
	}

	// Verify an invalid code — nothing consumed.
	valid2, remaining2 := svc.VerifyRecoveryCode(hashed, "notavalidcode")
	if valid2 {
		t.Error("VerifyRecoveryCode should return false for invalid code")
	}
	if len(remaining2) != len(hashed) {
		t.Errorf("remaining count after invalid verify: want %d, got %d", len(hashed), len(remaining2))
	}

	// Re-using a consumed code must fail.
	valid3, _ := svc.VerifyRecoveryCode(remaining, plain[1])
	if valid3 {
		t.Error("consumed code must not be usable a second time")
	}
}
