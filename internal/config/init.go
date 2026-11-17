package config

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/rantipay/trading-mcp/internal/gct"
	"github.com/sirupsen/logrus"
)

// InitializeConfigSystem initializes the configuration system with database and seeding
func InitializeConfigSystem(ctx context.Context, db *sql.DB, gctClient gct.GCTClient, logger *logrus.Logger) (*ConfigManager, error) {
	if logger == nil {
		logger = logrus.New()
	}

	logger.Info("🔧 Initializing configuration system...")

	// Create validator
	validator := NewConfigValidator(gctClient)

	// Create config manager
	configManager := NewConfigManager(db, validator, logger)

	// Seed default profiles
	logger.Info("🌱 Seeding default trading profiles...")

	seedCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	if err := SeedDefaultProfiles(seedCtx, configManager, logger); err != nil {
		logger.WithError(err).Warn("Failed to seed default profiles - continuing anyway")
	} else {
		logger.Info("✅ Default profiles seeded successfully")
	}

	// Verify profiles exist
	profiles, err := configManager.ListProfiles(ctx)
	if err != nil {
		logger.WithError(err).Warn("Failed to list profiles")
	} else {
		logger.WithField("count", len(profiles)).Info("📋 Trading profiles available")
		for _, profile := range profiles {
			logger.WithFields(logrus.Fields{
				"name":        profile.Name,
				"description": profile.Description,
				"is_default":  profile.IsDefault,
			}).Debug("Profile loaded")
		}
	}

	logger.Info("✅ Configuration system initialized")

	return configManager, nil
}

// QuickInitConfigManager is a convenience function for quick initialization
// with automatic database setup
func QuickInitConfigManager(dbPath string, gctClient gct.GCTClient, logger *logrus.Logger) (*ConfigManager, *sql.DB, error) {
	if logger == nil {
		logger = logrus.New()
	}

	// Open database
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to open database: %w", err)
	}

	// Test connection
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, nil, fmt.Errorf("failed to ping database: %w", err)
	}

	// Initialize config system
	ctx := context.Background()
	configManager, err := InitializeConfigSystem(ctx, db, gctClient, logger)
	if err != nil {
		db.Close()
		return nil, nil, fmt.Errorf("failed to initialize config system: %w", err)
	}

	return configManager, db, nil
}
