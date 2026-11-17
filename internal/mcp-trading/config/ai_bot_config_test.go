package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func loadForTest(t *testing.T, path string) *Config {
	t.Helper()
	t.Setenv("GCT_USERNAME", "test")
	t.Setenv("GCT_PASSWORD", "test")
	cfg, err := Load(path)
	require.NoError(t, err)
	return cfg
}

func TestAIBotAutoStart_ProductionConfigIsDisabled(t *testing.T) {
	cfg := loadForTest(t, filepath.Join("..", "..", "..", "configs", "mcp-trading", "config.yaml"))
	assert.False(t, cfg.AIBot.AutoStart)
}

func TestAIBotAutoStart_DefaultsToFalseWhenKeyMissing(t *testing.T) {
	src, err := os.ReadFile(filepath.Join("..", "..", "..", "configs", "mcp-trading", "config.yaml"))
	require.NoError(t, err)
	// Same config with the whole ai_bot section removed.
	var kept []byte
	skipping := false
	for _, line := range splitLines(src) {
		if string(line) == "ai_bot:" {
			skipping = true
			continue
		}
		if skipping && (len(line) == 0 || line[0] == ' ' || line[0] == '#') {
			continue
		}
		skipping = false
		kept = append(append(kept, line...), '\n')
	}
	path := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(path, kept, 0o600))

	cfg := loadForTest(t, path)
	assert.False(t, cfg.AIBot.AutoStart)
}

func splitLines(b []byte) [][]byte {
	var out [][]byte
	start := 0
	for i, c := range b {
		if c == '\n' {
			out = append(out, b[start:i])
			start = i + 1
		}
	}
	return append(out, b[start:])
}
