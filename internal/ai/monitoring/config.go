package monitoring

import (
	"time"
)

// Config holds configuration for the monitoring system
type Config struct {
	// Enabled enables performance monitoring
	Enabled bool

	// StoragePath is the path to store decision records
	StoragePath string

	// LogDecisions enables logging of all decisions
	LogDecisions bool

	// TrackPerformance enables performance tracking
	TrackPerformance bool

	// ReportInterval is the interval for generating reports
	ReportInterval time.Duration

	// RetentionDays is the number of days to retain decision records
	RetentionDays int
}

// DefaultConfig returns default monitoring configuration
func DefaultConfig() *Config {
	return &Config{
		Enabled:          true,
		StoragePath:      "data/monitoring",
		LogDecisions:     true,
		TrackPerformance: true,
		ReportInterval:   time.Hour,
		RetentionDays:    90,
	}
}
