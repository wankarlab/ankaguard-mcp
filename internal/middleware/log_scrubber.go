package middleware

import (
	"regexp"
	"sync"

	"github.com/sirupsen/logrus"
)

// sensitiveKeys are log field names whose values must be redacted.
var sensitiveKeys = map[string]bool{
	"Authorization": true,
	"KC-API-SIGN":   true,
	"api_secret":    true,
	"passphrase":    true,
	"apiKey":        true,
	"secret":        true,
	"password":      true,
	"token":         true,
}

// bearerPattern redacts Bearer tokens in log messages.
var bearerPattern = regexp.MustCompile(`(?i)(bearer\s+)[A-Za-z0-9._\-]+`)

// base64Pattern redacts base64-encoded strings (40+ chars) in log messages.
var base64Pattern = regexp.MustCompile(`[A-Za-z0-9+/]{40,}=*`)

// queryParamPattern redacts values in query strings like api_secret=XXX or passphrase=XXX.
var queryParamPattern = regexp.MustCompile(`(?i)(api_secret|passphrase)=([A-Za-z0-9._\-]+)`)

// LogScrubberHook is a logrus hook that redacts sensitive data from log entries.
type LogScrubberHook struct {
	mu sync.RWMutex
}

// NewLogScrubberHook creates a new LogScrubberHook.
func NewLogScrubberHook() *LogScrubberHook {
	return &LogScrubberHook{}
}

// Levels returns all log levels for which this hook should be called.
func (h *LogScrubberHook) Levels() []logrus.Level {
	return logrus.AllLevels
}

// Fire is called when a log entry is fired. It redacts sensitive fields and message content.
func (h *LogScrubberHook) Fire(entry *logrus.Entry) error {
	h.mu.Lock()
	defer h.mu.Unlock()

	// Redact sensitive fields in entry.Data
	for key := range entry.Data {
		if sensitiveKeys[key] {
			entry.Data[key] = "[REDACTED]"
		}
	}

	// Redact sensitive patterns in the message
	entry.Message = redactMessage(entry.Message)

	return nil
}

// redactMessage redacts sensitive patterns from a log message.
func redactMessage(msg string) string {
	// Redact Bearer tokens
	msg = bearerPattern.ReplaceAllString(msg, "${1}[REDACTED]")

	// Redact query parameters
	msg = queryParamPattern.ReplaceAllString(msg, "${1}=[REDACTED]")

	return msg
}
