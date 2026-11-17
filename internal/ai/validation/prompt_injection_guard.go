// Package validation — see prompt_injection_guard.go for AI-03.
//
// AI-03 (2026-09-10 audit): as of this commit, SanitizeExternalData /
// MustBeInternalData have ZERO callers anywhere in this repo. That is not a
// bug today — no live code path currently interpolates external free text
// (news feed content, exchange-supplied descriptions, user notes, websocket
// payloads) into an LLM prompt, so there is nothing to exploit yet.
//
// This IS a landmine for tomorrow: the moment any new feature adds external
// text to a prompt (e.g. a news-sentiment feature, a "why did this pair
// move" summarizer, a support-ticket triage bot), that new code MUST call
// SanitizeExternalData(data) on every externally-sourced string before it is
// concatenated into a prompt sent to internal/ai/llm. Do not assume "we'll
// add sanitization later" — prompt injection via unsanitized external text
// is a real, demonstrated LLM attack class, and this repo already has the
// guard implemented and tested; there is no excuse to skip it once a call
// site exists. If you are adding such a call site, wire it here and remove
// this comment's "zero callers" claim.
package validation

import (
	"regexp"
	"strings"
)

// injectionPatterns are patterns that indicate prompt injection attempts embedded
// in external data (e.g. news feeds, ticker descriptions, websocket payloads).
// External data must never be interpreted as an instruction by an LLM.
var injectionPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)ignore\s+(previous|prior|all|above)\s+(instructions?|prompts?|context|rules?)`),
	regexp.MustCompile(`(?i)(system|user|assistant)\s*:\s*`),                         // role injection
	regexp.MustCompile(`(?i)\bact\s+as\b.*\b(admin|root|superuser|unrestricted)\b`),  // persona hijack
	regexp.MustCompile(`(?i)you\s+(are|must|will|should)\s+(now|henceforth|always)`), // direct instruction
	regexp.MustCompile(`(?i)(forget|disregard|override)\s+(your|all)\s+(training|instructions?|rules?)`),
	regexp.MustCompile(`(?i)<\s*(system|instructions?)\s*>`), // XML-style role tags
	regexp.MustCompile(`\x00|\x01|\x02|\x03|\x04|\x05|\x06`), // control characters
}

// SanitizeExternalData ensures that a string sourced from an external system
// (news feed, exchange description, websocket event, user-supplied note) cannot
// be used as a prompt-injection vector.
//
// Behaviour:
//   - If an injection pattern is detected, returns ("", true) so callers can
//     log and discard the content rather than forwarding it to the LLM.
//   - If clean, returns (trimmed input, false).
//
// This function is intentionally conservative: false negatives (missed attacks)
// are more dangerous than false positives (clean content rejected).
func SanitizeExternalData(data string) (clean string, injectionDetected bool) {
	trimmed := strings.TrimSpace(data)
	for _, pat := range injectionPatterns {
		if pat.MatchString(trimmed) {
			return "", true
		}
	}
	return trimmed, false
}

// MustBeInternalData panics if data looks like it came from an external source
// with injection patterns. Use this for data that MUST be constructed internally.
// Prefer SanitizeExternalData for runtime filtering.
func MustBeInternalData(data string) string {
	if _, injected := SanitizeExternalData(data); injected {
		panic("prompt injection detected in data claimed to be internal: " + data[:min(50, len(data))])
	}
	return data
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
