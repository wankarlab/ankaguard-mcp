package config

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/sirupsen/logrus"
)

// ConfigSubscriber is notified when configuration changes
type ConfigSubscriber interface {
	OnConfigUpdate(config *DynamicTradingConfig) error
}

// ConfigManager manages trading configuration with hot-reload support
type ConfigManager struct {
	db          *sql.DB
	validator   *ConfigValidator
	subscribers []ConfigSubscriber
	mu          sync.RWMutex
	logger      *logrus.Logger
}

// NewConfigManager creates a new configuration manager
func NewConfigManager(db *sql.DB, validator *ConfigValidator, logger *logrus.Logger) *ConfigManager {
	if logger == nil {
		logger = logrus.New()
	}

	return &ConfigManager{
		db:          db,
		validator:   validator,
		subscribers: make([]ConfigSubscriber, 0),
		logger:      logger,
	}
}

// LoadConfig loads configuration from database or returns default
func (cm *ConfigManager) LoadConfig(ctx context.Context, profileName string) (*DynamicTradingConfig, error) {
	cm.mu.RLock()
	defer cm.mu.RUnlock()

	// If no profile name, return default
	if profileName == "" {
		cm.logger.Info("No profile specified, using default configuration")
		return GetDefault(), nil
	}

	// Load profile from database
	profile, err := cm.getProfileByName(ctx, profileName)
	if err != nil {
		return nil, fmt.Errorf("failed to load profile '%s': %w", profileName, err)
	}

	cm.logger.WithFields(logrus.Fields{
		"profile": profileName,
		"version": profile.Config.Version,
	}).Info("Configuration loaded from profile")

	return &profile.Config, nil
}

// SaveProfile saves a trading profile to database
func (cm *ConfigManager) SaveProfile(ctx context.Context, profile *TradingProfile) error {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	// Validate configuration
	if errs := cm.validator.Validate(ctx, &profile.Config); len(errs) > 0 {
		return fmt.Errorf("invalid configuration: %v", errs)
	}

	// Generate ID if not provided
	if profile.ID == "" {
		profile.ID = uuid.New().String()
	}

	// Set timestamps
	now := time.Now()
	if profile.CreatedAt.IsZero() {
		profile.CreatedAt = now
	}
	profile.UpdatedAt = now

	// Serialize config to JSON
	configJSON, err := json.Marshal(profile.Config)
	if err != nil {
		return fmt.Errorf("failed to serialize config: %w", err)
	}

	// Serialize tags to JSON
	tagsJSON, err := json.Marshal(profile.Tags)
	if err != nil {
		return fmt.Errorf("failed to serialize tags: %w", err)
	}

	// Insert or update profile
	query := `
		INSERT INTO trading_profiles (id, name, description, config, created_at, updated_at, is_default, tags)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		ON CONFLICT (name) DO UPDATE SET
			description = EXCLUDED.description,
			config = EXCLUDED.config,
			updated_at = EXCLUDED.updated_at,
			is_default = EXCLUDED.is_default,
			tags = EXCLUDED.tags
	`

	_, err = cm.db.ExecContext(ctx, query,
		profile.ID,
		profile.Name,
		profile.Description,
		configJSON,
		profile.CreatedAt,
		profile.UpdatedAt,
		profile.IsDefault,
		tagsJSON,
	)

	if err != nil {
		return fmt.Errorf("failed to save profile: %w", err)
	}

	cm.logger.WithFields(logrus.Fields{
		"profile_id":   profile.ID,
		"profile_name": profile.Name,
	}).Info("Profile saved successfully")

	return nil
}

// UpdateConfig updates configuration with hot-reload support
func (cm *ConfigManager) UpdateConfig(ctx context.Context, updates map[string]interface{}, hotReload bool) (*DynamicTradingConfig, error) {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	// Get current config (would typically be stored in memory)
	// For now, we'll create a new config and apply updates
	config := GetDefault()

	// Apply updates
	if err := cm.applyUpdates(config, updates); err != nil {
		return nil, fmt.Errorf("failed to apply updates: %w", err)
	}

	// Validate updated configuration
	if errs := cm.validator.Validate(ctx, config); len(errs) > 0 {
		return nil, fmt.Errorf("invalid configuration after updates: %v", errs)
	}

	// Update version and timestamp
	config.Version++
	config.UpdatedAt = time.Now()

	// Notify subscribers if hot-reload is enabled
	if hotReload {
		cm.logger.Info("Hot-reloading configuration...")
		if err := cm.notifySubscribers(config); err != nil {
			return nil, fmt.Errorf("failed to notify subscribers: %w", err)
		}
		cm.logger.Info("Configuration hot-reloaded successfully")
	}

	return config, nil
}

// ListProfiles returns all available profiles
func (cm *ConfigManager) ListProfiles(ctx context.Context) ([]*TradingProfile, error) {
	cm.mu.RLock()
	defer cm.mu.RUnlock()

	query := `
		SELECT id, name, description, config, created_at, updated_at, is_default, tags
		FROM trading_profiles
		ORDER BY is_default DESC, created_at DESC
	`

	rows, err := cm.db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("failed to query profiles: %w", err)
	}
	defer rows.Close()

	profiles := make([]*TradingProfile, 0)

	for rows.Next() {
		var profile TradingProfile
		var configJSON, tagsJSON []byte

		err := rows.Scan(
			&profile.ID,
			&profile.Name,
			&profile.Description,
			&configJSON,
			&profile.CreatedAt,
			&profile.UpdatedAt,
			&profile.IsDefault,
			&tagsJSON,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan profile: %w", err)
		}

		// Deserialize config
		if err := json.Unmarshal(configJSON, &profile.Config); err != nil {
			cm.logger.WithError(err).WithField("profile", profile.Name).Warn("Failed to deserialize config")
			continue
		}

		// Deserialize tags
		if err := json.Unmarshal(tagsJSON, &profile.Tags); err != nil {
			cm.logger.WithError(err).WithField("profile", profile.Name).Warn("Failed to deserialize tags")
			profile.Tags = []string{}
		}

		profiles = append(profiles, &profile)
	}

	return profiles, nil
}

// DeleteProfile deletes a profile by name
func (cm *ConfigManager) DeleteProfile(ctx context.Context, name string) error {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	// Check if profile is default
	var isDefault bool
	err := cm.db.QueryRowContext(ctx, "SELECT is_default FROM trading_profiles WHERE name = $1", name).Scan(&isDefault)
	if err == sql.ErrNoRows {
		return fmt.Errorf("profile not found: %s", name)
	}
	if err != nil {
		return fmt.Errorf("failed to check profile: %w", err)
	}

	if isDefault {
		return fmt.Errorf("cannot delete default profile")
	}

	// Delete profile
	result, err := cm.db.ExecContext(ctx, "DELETE FROM trading_profiles WHERE name = $1", name)
	if err != nil {
		return fmt.Errorf("failed to delete profile: %w", err)
	}

	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		return fmt.Errorf("profile not found: %s", name)
	}

	cm.logger.WithField("profile", name).Info("Profile deleted successfully")

	return nil
}

// ApplyOverrides applies overrides to a configuration
func (cm *ConfigManager) ApplyOverrides(config *DynamicTradingConfig, overrides map[string]interface{}) *DynamicTradingConfig {
	// Create a copy
	result := *config

	// Apply overrides
	if err := cm.applyUpdates(&result, overrides); err != nil {
		cm.logger.WithError(err).Warn("Failed to apply overrides")
		return config
	}

	return &result
}

// Subscribe adds a configuration change subscriber
func (cm *ConfigManager) Subscribe(subscriber ConfigSubscriber) {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	cm.subscribers = append(cm.subscribers, subscriber)
	cm.logger.Info("Configuration subscriber added")
}

// Validator returns the configuration validator
func (cm *ConfigManager) Validator() *ConfigValidator {
	return cm.validator
}

// Helper methods

func (cm *ConfigManager) getProfileByName(ctx context.Context, name string) (*TradingProfile, error) {
	query := `
		SELECT id, name, description, config, created_at, updated_at, is_default, tags
		FROM trading_profiles
		WHERE name = $1
	`

	var profile TradingProfile
	var configJSON, tagsJSON []byte

	err := cm.db.QueryRowContext(ctx, query, name).Scan(
		&profile.ID,
		&profile.Name,
		&profile.Description,
		&configJSON,
		&profile.CreatedAt,
		&profile.UpdatedAt,
		&profile.IsDefault,
		&tagsJSON,
	)

	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("profile not found: %s", name)
	}
	if err != nil {
		return nil, err
	}

	// Deserialize config
	if err := json.Unmarshal(configJSON, &profile.Config); err != nil {
		return nil, fmt.Errorf("failed to deserialize config: %w", err)
	}

	// Deserialize tags
	if err := json.Unmarshal(tagsJSON, &profile.Tags); err != nil {
		profile.Tags = []string{}
	}

	return &profile, nil
}

func (cm *ConfigManager) applyUpdates(config *DynamicTradingConfig, updates map[string]interface{}) error {
	// Apply each update
	for key, value := range updates {
		switch key {
		// Trading parameters
		case "pair":
			if v, ok := value.(string); ok {
				config.Pair = v
			}
		case "exchange":
			if v, ok := value.(string); ok {
				config.Exchange = v
			}
		case "timeframes":
			if v, ok := value.([]interface{}); ok {
				timeframes := make([]string, len(v))
				for i, tf := range v {
					if tfStr, ok := tf.(string); ok {
						timeframes[i] = tfStr
					}
				}
				config.Timeframes = timeframes
			}
		case "confidence_threshold":
			if v, ok := value.(float64); ok {
				config.ConfidenceThreshold = v
			}
		case "trade_size_usd":
			if v, ok := value.(float64); ok {
				config.TradeSize = v
			}
		case "leverage":
			if v, ok := value.(float64); ok {
				config.Leverage = int(v)
			}

		// Timing
		case "analysis_interval_seconds":
			if v, ok := value.(float64); ok {
				config.AnalysisIntervalSeconds = int(v)
			}
		case "monitor_interval_seconds":
			if v, ok := value.(float64); ok {
				config.MonitorIntervalSeconds = int(v)
			}

		// Risk management
		case "risk_per_trade_pct":
			if v, ok := value.(float64); ok {
				config.RiskPerTradePct = v
			}
		case "max_daily_loss_usd":
			if v, ok := value.(float64); ok {
				config.MaxDailyLoss = v
			}
		case "max_daily_trades":
			if v, ok := value.(float64); ok {
				config.MaxDailyTrades = int(v)
			}
		case "consecutive_loss_limit":
			if v, ok := value.(float64); ok {
				config.ConsecutiveLossLimit = int(v)
			}
		case "max_open_positions":
			if v, ok := value.(float64); ok {
				config.MaxOpenPositions = int(v)
			}

		// TP/SL
		case "take_profit_pct":
			if v, ok := value.(float64); ok {
				config.TakeProfitPct = v
			}
		case "stop_loss_pct":
			if v, ok := value.(float64); ok {
				config.StopLossPct = v
			}

		// Mode
		case "auto_execute":
			if v, ok := value.(bool); ok {
				config.AutoExecute = v
			}
		case "dry_run":
			if v, ok := value.(bool); ok {
				config.DryRun = v
			}
		case "autonomous_mode":
			if v, ok := value.(bool); ok {
				config.AutonomousMode = v
			}
		case "hot_reload":
			if v, ok := value.(bool); ok {
				config.HotReload = v
			}

		// Add more fields as needed
		default:
			cm.logger.WithField("key", key).Debug("Unknown configuration key")
		}
	}

	return nil
}

func (cm *ConfigManager) notifySubscribers(config *DynamicTradingConfig) error {
	for _, subscriber := range cm.subscribers {
		if err := subscriber.OnConfigUpdate(config); err != nil {
			return fmt.Errorf("subscriber notification failed: %w", err)
		}
	}
	return nil
}
