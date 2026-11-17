package middleware

import (
	"testing"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
)

// TestLogScrubberHook_RedactsFields tests that sensitive fields are redacted.
func TestLogScrubberHook_RedactsFields(t *testing.T) {
	hook := NewLogScrubberHook()

	entry := &logrus.Entry{
		Data: logrus.Fields{
			"Authorization": "Bearer eyABCDEFGHIJKLMNOPQRSTUVWXYZ123456",
			"passphrase":    "secret123",
			"user_id":       "123",
		},
		Message: "test message",
	}

	err := hook.Fire(entry)
	assert.NoError(t, err)

	// Check that sensitive fields are redacted
	assert.Equal(t, "[REDACTED]", entry.Data["Authorization"])
	assert.Equal(t, "[REDACTED]", entry.Data["passphrase"])

	// Check that non-sensitive fields are not redacted
	assert.Equal(t, "123", entry.Data["user_id"])
}

// TestLogScrubberHook_RedactsBearerInMessage tests that Bearer tokens in messages are redacted.
func TestLogScrubberHook_RedactsBearerInMessage(t *testing.T) {
	hook := NewLogScrubberHook()

	originalToken := "eyABCDEFGHIJKLMNOPQRSTUVWXYZ123456"
	entry := &logrus.Entry{
		Data:    logrus.Fields{},
		Message: "Authorization: Bearer " + originalToken,
	}

	err := hook.Fire(entry)
	assert.NoError(t, err)

	// Check that the original token is not in the message
	assert.NotContains(t, entry.Message, originalToken)

	// Check that [REDACTED] is in the message
	assert.Contains(t, entry.Message, "[REDACTED]")
}

// TestLogScrubberHook_LevelsCoversAll tests that Levels() returns all logrus levels.
func TestLogScrubberHook_LevelsCoversAll(t *testing.T) {
	hook := NewLogScrubberHook()

	levels := hook.Levels()

	// Check that all logrus levels are present
	expectedLevels := logrus.AllLevels
	assert.Equal(t, len(expectedLevels), len(levels))

	// Create a map for easier comparison
	levelMap := make(map[logrus.Level]bool)
	for _, level := range levels {
		levelMap[level] = true
	}

	for _, expectedLevel := range expectedLevels {
		assert.True(t, levelMap[expectedLevel], "expected level %v to be present", expectedLevel)
	}
}

// TestLogScrubberHook_SafeFields tests that non-sensitive fields are not redacted.
func TestLogScrubberHook_SafeFields(t *testing.T) {
	hook := NewLogScrubberHook()

	entry := &logrus.Entry{
		Data: logrus.Fields{
			"user_id":     "123",
			"action":      "login",
			"ip_address":  "192.168.1.1",
			"request_id":  "abc-def-ghi",
		},
		Message: "User action",
	}

	err := hook.Fire(entry)
	assert.NoError(t, err)

	// Check that safe fields are not redacted
	assert.Equal(t, "123", entry.Data["user_id"])
	assert.Equal(t, "login", entry.Data["action"])
	assert.Equal(t, "192.168.1.1", entry.Data["ip_address"])
	assert.Equal(t, "abc-def-ghi", entry.Data["request_id"])
}

// TestLogScrubberHook_RedactsMultipleSensitiveFields tests redacting multiple sensitive field types.
func TestLogScrubberHook_RedactsMultipleSensitiveFields(t *testing.T) {
	hook := NewLogScrubberHook()

	entry := &logrus.Entry{
		Data: logrus.Fields{
			"Authorization": "Bearer token123",
			"KC-API-SIGN":   "sign456",
			"api_secret":    "secret789",
			"apiKey":        "key101112",
			"secret":        "mysecret",
			"password":      "mypassword",
			"token":         "mytoken",
		},
		Message: "test",
	}

	err := hook.Fire(entry)
	assert.NoError(t, err)

	// Check that all sensitive fields are redacted
	assert.Equal(t, "[REDACTED]", entry.Data["Authorization"])
	assert.Equal(t, "[REDACTED]", entry.Data["KC-API-SIGN"])
	assert.Equal(t, "[REDACTED]", entry.Data["api_secret"])
	assert.Equal(t, "[REDACTED]", entry.Data["apiKey"])
	assert.Equal(t, "[REDACTED]", entry.Data["secret"])
	assert.Equal(t, "[REDACTED]", entry.Data["password"])
	assert.Equal(t, "[REDACTED]", entry.Data["token"])
}

// TestLogScrubberHook_RedactsQueryParams tests that query parameters in messages are redacted.
func TestLogScrubberHook_RedactsQueryParams(t *testing.T) {
	hook := NewLogScrubberHook()

	entry := &logrus.Entry{
		Data:    logrus.Fields{},
		Message: "Request: https://example.com?api_secret=mySecretValue123&passphrase=myPassphrase456",
	}

	err := hook.Fire(entry)
	assert.NoError(t, err)

	// Check that the original values are not in the message
	assert.NotContains(t, entry.Message, "mySecretValue123")
	assert.NotContains(t, entry.Message, "myPassphrase456")

	// Check that [REDACTED] is in the message
	assert.Contains(t, entry.Message, "[REDACTED]")
}
