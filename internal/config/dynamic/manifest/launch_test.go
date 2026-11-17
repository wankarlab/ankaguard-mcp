package manifest

import (
	"encoding/json"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/rantipay/trading-mcp/internal/config/dynamic/domain"
)

func TestLaunchEntries_RegisteredUniqueAndValid(t *testing.T) {
	seen := map[string]bool{}
	for _, e := range All() {
		if seen[e.Key] {
			t.Fatalf("duplicate manifest key %q", e.Key)
		}
		seen[e.Key] = true
	}

	launch := launchEntries()
	if len(launch) == 0 {
		t.Fatal("no launch.* entries registered")
	}
	for _, e := range launch {
		if !strings.HasPrefix(e.Key, "launch.") || e.Group != "launch" {
			t.Errorf("%s: key must start with launch. and group must be launch", e.Key)
		}
		if !seen[e.Key] {
			t.Errorf("%s: not returned by All()", e.Key)
		}
		if len(e.Enum) > 0 && !slices.Contains(e.Enum, e.Default) {
			t.Errorf("%s: default %q not in enum %v", e.Key, e.Default, e.Enum)
		}
		switch e.Type {
		case domain.ParamTypeInt, domain.ParamTypeFloat:
			v := mustFloat(t, e.Key, e.Default)
			if e.Min != "" && v < mustFloat(t, e.Key, e.Min) {
				t.Errorf("%s: default %s below min %s", e.Key, e.Default, e.Min)
			}
			if e.Max != "" && v > mustFloat(t, e.Key, e.Max) {
				t.Errorf("%s: default %s above max %s", e.Key, e.Default, e.Max)
			}
		case domain.ParamTypeBool:
			if _, err := strconv.ParseBool(e.Default); err != nil {
				t.Errorf("%s: invalid bool default %q", e.Key, e.Default)
			}
		case domain.ParamTypeJSON:
			if !json.Valid([]byte(e.Default)) {
				t.Errorf("%s: invalid JSON default %q", e.Key, e.Default)
			}
		}
	}
}

// Safety defaults: a fresh install must never trade real funds or enable
// optional risk-increasing features without an explicit operator change.
func TestLaunchEntries_SafeDefaults(t *testing.T) {
	want := map[string]string{
		"launch.real_mode.enabled":            "false",
		"launch.tx.jito_enabled":              "false",
		"launch.gate.require_manual_approval": "true",
		"launch.gate.max_fraud_buys":          "0",
		"launch.tenancy.mode":                 "single",
	}
	got := map[string]string{}
	for _, e := range launchEntries() {
		got[e.Key] = e.Default
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s default = %q, want %q", k, got[k], v)
		}
	}
}

func mustFloat(t *testing.T, key, s string) float64 {
	t.Helper()
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		t.Fatalf("%s: %q is not numeric", key, s)
	}
	return v
}
