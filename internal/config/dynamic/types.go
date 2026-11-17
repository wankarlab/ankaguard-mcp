package dynamic

import (
	"encoding/json"
	"strconv"
	"time"

	"github.com/sirupsen/logrus"
)

// pkgLog is a package-level logger used by accessors when no store logger is available.
var pkgLog = logrus.New()

// Int returns the integer value for key resolved through the scope chain.
// On parse failure it logs a WARN and returns the registry default (or 0).
func (s *ScopedSnapshot) Int(key string) int {
	raw := s.resolve(key)
	if v, err := strconv.Atoi(raw); err == nil {
		return v
	}
	// Empty string means "not configured" — use default silently without spamming logs.
	if raw != "" {
		pkgLog.WithFields(logrus.Fields{"key": key, "raw": raw}).Warn("config: cannot parse int, using default")
	}
	return s.intDefault(key)
}

// Float returns the float64 value for key resolved through the scope chain.
func (s *ScopedSnapshot) Float(key string) float64 {
	raw := s.resolve(key)
	if v, err := strconv.ParseFloat(raw, 64); err == nil {
		return v
	}
	// Empty string means "not configured" — use default silently without spamming logs.
	if raw != "" {
		pkgLog.WithFields(logrus.Fields{"key": key, "raw": raw}).Warn("config: cannot parse float, using default")
	}
	return s.floatDefault(key)
}

// Bool returns the boolean value for key resolved through the scope chain.
func (s *ScopedSnapshot) Bool(key string) bool {
	raw := s.resolve(key)
	if v, err := strconv.ParseBool(raw); err == nil {
		return v
	}
	// Empty string means "not configured" — use default silently without spamming logs.
	if raw != "" {
		pkgLog.WithFields(logrus.Fields{"key": key, "raw": raw}).Warn("config: cannot parse bool, using default")
	}
	return s.boolDefault(key)
}

// String returns the string value for key resolved through the scope chain.
func (s *ScopedSnapshot) String(key string) string {
	return s.resolve(key)
}

// Duration returns the time.Duration value for key resolved through the scope chain.
func (s *ScopedSnapshot) Duration(key string) time.Duration {
	raw := s.resolve(key)
	if v, err := time.ParseDuration(raw); err == nil {
		return v
	}
	// Empty string means "not configured" — use default silently without spamming logs.
	if raw != "" {
		pkgLog.WithFields(logrus.Fields{"key": key, "raw": raw}).Warn("config: cannot parse duration, using default")
	}
	return s.durationDefault(key)
}

// JSON unmarshals the raw string value for key into out.
// Returns an error if unmarshalling fails; does not fall back silently.
func (s *ScopedSnapshot) JSON(key string, out interface{}) error {
	raw := s.resolve(key)
	return json.Unmarshal([]byte(raw), out)
}

// ---- presence-aware variants — return ok=true when the key is configured ----
//
// These report whether a key resolves to a usable value, where "usable" means it has an
// explicit scope override OR a registry/manifest default AND that value parses. Unlike a
// caller-side `if v := cfg.Float(key); v != 0` guard, these treat an explicitly configured
// 0 / 0.0 / false / 0s as a real value, so a factor can be disabled by setting it to zero.
// Unknown keys (not in the registry and with no override) report ok=false.

// FloatOk returns the float64 value and ok=true when key is configured and parses.
func (s *ScopedSnapshot) FloatOk(key string) (float64, bool) {
	raw, found := s.resolveOk(key)
	if !found || raw == "" {
		return 0, false
	}
	v, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		pkgLog.WithFields(logrus.Fields{"key": key, "raw": raw}).Warn("config: cannot parse float, treating as unset")
		return 0, false
	}
	return v, true
}

// IntOk returns the int value and ok=true when key is configured and parses.
func (s *ScopedSnapshot) IntOk(key string) (int, bool) {
	raw, found := s.resolveOk(key)
	if !found || raw == "" {
		return 0, false
	}
	v, err := strconv.Atoi(raw)
	if err != nil {
		pkgLog.WithFields(logrus.Fields{"key": key, "raw": raw}).Warn("config: cannot parse int, treating as unset")
		return 0, false
	}
	return v, true
}

// BoolOk returns the bool value and ok=true when key is configured and parses.
func (s *ScopedSnapshot) BoolOk(key string) (bool, bool) {
	raw, found := s.resolveOk(key)
	if !found || raw == "" {
		return false, false
	}
	v, err := strconv.ParseBool(raw)
	if err != nil {
		pkgLog.WithFields(logrus.Fields{"key": key, "raw": raw}).Warn("config: cannot parse bool, treating as unset")
		return false, false
	}
	return v, true
}

// DurationOk returns the time.Duration value and ok=true when key is configured and parses.
func (s *ScopedSnapshot) DurationOk(key string) (time.Duration, bool) {
	raw, found := s.resolveOk(key)
	if !found || raw == "" {
		return 0, false
	}
	v, err := time.ParseDuration(raw)
	if err != nil {
		pkgLog.WithFields(logrus.Fields{"key": key, "raw": raw}).Warn("config: cannot parse duration, treating as unset")
		return 0, false
	}
	return v, true
}

// ---- Strict variants — return (value, ok=false) on parse failure ----

// IntStrict returns the int value and false if parsing fails.
func (s *ScopedSnapshot) IntStrict(key string) (int, bool) {
	raw := s.resolve(key)
	v, err := strconv.Atoi(raw)
	return v, err == nil
}

// FloatStrict returns the float64 value and false if parsing fails.
func (s *ScopedSnapshot) FloatStrict(key string) (float64, bool) {
	raw := s.resolve(key)
	v, err := strconv.ParseFloat(raw, 64)
	return v, err == nil
}

// BoolStrict returns the bool value and false if parsing fails.
func (s *ScopedSnapshot) BoolStrict(key string) (bool, bool) {
	raw := s.resolve(key)
	v, err := strconv.ParseBool(raw)
	return v, err == nil
}

// DurationStrict returns the time.Duration value and false if parsing fails.
func (s *ScopedSnapshot) DurationStrict(key string) (time.Duration, bool) {
	raw := s.resolve(key)
	v, err := time.ParseDuration(raw)
	return v, err == nil
}

// ---- default helpers — read registry default and parse ----

func (s *ScopedSnapshot) intDefault(key string) int {
	if reg, ok := s.registry[key]; ok {
		if v, err := strconv.Atoi(reg.DefaultValue); err == nil {
			return v
		}
	}
	return 0
}

func (s *ScopedSnapshot) floatDefault(key string) float64 {
	if reg, ok := s.registry[key]; ok {
		if v, err := strconv.ParseFloat(reg.DefaultValue, 64); err == nil {
			return v
		}
	}
	return 0
}

func (s *ScopedSnapshot) boolDefault(key string) bool {
	if reg, ok := s.registry[key]; ok {
		if v, err := strconv.ParseBool(reg.DefaultValue); err == nil {
			return v
		}
	}
	return false
}

func (s *ScopedSnapshot) stringDefault(key string) string {
	if reg, ok := s.registry[key]; ok {
		return reg.DefaultValue
	}
	return ""
}

func (s *ScopedSnapshot) durationDefault(key string) time.Duration {
	if reg, ok := s.registry[key]; ok {
		if v, err := time.ParseDuration(reg.DefaultValue); err == nil {
			return v
		}
	}
	return 0
}

// ensure stringDefault is used (suppress unused warning if String calls resolve directly)
var _ = (*ScopedSnapshot).stringDefault
