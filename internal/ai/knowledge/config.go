package knowledge

import (
	"time"
)

// Config holds configuration for the knowledge base system
type Config struct {
	// SteeringDir is the directory containing steering files
	SteeringDir string

	// TrainingDocsDir is the directory containing training documents
	TrainingDocsDir string

	// CacheEnabled enables context caching
	CacheEnabled bool

	// CacheTTL is the time-to-live for cached contexts
	CacheTTL time.Duration

	// ReloadOnChange watches for file changes and reloads
	ReloadOnChange bool
}

// DefaultConfig returns default configuration
func DefaultConfig() *Config {
	return &Config{
		SteeringDir:     ".kiro/steering",
		TrainingDocsDir: "docs/training",
		CacheEnabled:    true,
		CacheTTL:        time.Hour,
		ReloadOnChange:  true,
	}
}
