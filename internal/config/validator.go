package config

import (
	"context"
	"fmt"
	"net"
	"time"
)

// GCTValidationResult represents the result of a GCT configuration validation
type GCTValidationResult struct {
	Valid    bool                 `json:"valid"`
	Errors   []GCTValidationError `json:"errors,omitempty"`
	Warnings []string             `json:"warnings,omitempty"`
}

// GCTValidationError represents a GCT configuration validation error
type GCTValidationError struct {
	Field   string `json:"field"`
	Message string `json:"message"`
	Code    string `json:"code"`
}

// Validator validates configuration
type Validator struct {
	config *GCTConfig
}

// NewValidator creates a new configuration validator
func NewValidator(config *GCTConfig) *Validator {
	return &Validator{
		config: config,
	}
}

// ValidateAll performs all validation checks
func (v *Validator) ValidateAll(ctx context.Context) *GCTValidationResult {
	result := &GCTValidationResult{
		Valid:    true,
		Errors:   []GCTValidationError{},
		Warnings: []string{},
	}

	// Validate configuration structure
	v.validateStructure(result)

	// Validate network connectivity
	v.validateNetworkConnectivity(ctx, result)

	// Validate GoCryptoTrader connection (if structure is valid)
	if result.Valid {
		v.validateGCTConnection(ctx, result)
	}

	// Validate exchange configuration
	v.validateExchanges(result)

	// Validate security settings
	v.validateSecurity(result)

	// Validate feature flags
	v.validateFeatures(result)

	return result
}

// validateStructure validates the configuration structure
func (v *Validator) validateStructure(result *GCTValidationResult) {
	// Validate gRPC settings
	if v.config.GoCryptoTrader.GRPC.Address == "" {
		v.addError(result, "gocryptotrader.grpc.address", "gRPC address is required", "REQUIRED_FIELD")
	}

	if v.config.GoCryptoTrader.GRPC.TimeoutSeconds <= 0 {
		v.addError(result, "gocryptotrader.grpc.timeout_seconds", "timeout must be positive", "INVALID_VALUE")
	}

	if v.config.GoCryptoTrader.GRPC.MaxRetries < 0 {
		v.addError(result, "gocryptotrader.grpc.max_retries", "max retries cannot be negative", "INVALID_VALUE")
	}

	// Validate auth settings
	if v.config.GoCryptoTrader.Auth.Username == "" {
		v.addError(result, "gocryptotrader.auth.username", "username is required", "REQUIRED_FIELD")
	}

	if v.config.GoCryptoTrader.Auth.Password == "" {
		v.addError(result, "gocryptotrader.auth.password", "password is required", "REQUIRED_FIELD")
	}

	// Validate server settings
	if v.config.TradingMCP.Server.Port <= 0 || v.config.TradingMCP.Server.Port > 65535 {
		v.addError(result, "trading_mcp.server.port", fmt.Sprintf("invalid port: %d", v.config.TradingMCP.Server.Port), "INVALID_VALUE")
	}

	if v.config.TradingMCP.Server.HealthCheckPort <= 0 || v.config.TradingMCP.Server.HealthCheckPort > 65535 {
		v.addError(result, "trading_mcp.server.health_check_port", fmt.Sprintf("invalid health check port: %d", v.config.TradingMCP.Server.HealthCheckPort), "INVALID_VALUE")
	}

	// Validate logging settings
	validLevels := map[string]bool{"debug": true, "info": true, "warn": true, "error": true}
	if !validLevels[v.config.TradingMCP.Logging.Level] {
		v.addError(result, "trading_mcp.logging.level", fmt.Sprintf("invalid log level: %s", v.config.TradingMCP.Logging.Level), "INVALID_VALUE")
	}

	validFormats := map[string]bool{"json": true, "text": true}
	if !validFormats[v.config.TradingMCP.Logging.Format] {
		v.addError(result, "trading_mcp.logging.format", fmt.Sprintf("invalid log format: %s", v.config.TradingMCP.Logging.Format), "INVALID_VALUE")
	}

	// Validate cache settings
	if v.config.TradingMCP.Cache.Enabled {
		if v.config.TradingMCP.Cache.TTLSeconds <= 0 {
			v.addError(result, "trading_mcp.cache.ttl_seconds", "TTL must be positive when cache is enabled", "INVALID_VALUE")
		}
		if v.config.TradingMCP.Cache.MaxEntries <= 0 {
			v.addError(result, "trading_mcp.cache.max_entries", "max entries must be positive when cache is enabled", "INVALID_VALUE")
		}
	}

	// Validate rate limiting settings
	if v.config.TradingMCP.RateLimiting.Enabled {
		if v.config.TradingMCP.RateLimiting.RequestsPerSecond <= 0 {
			v.addError(result, "trading_mcp.rate_limiting.requests_per_second", "requests per second must be positive", "INVALID_VALUE")
		}
		if v.config.TradingMCP.RateLimiting.Burst <= 0 {
			v.addError(result, "trading_mcp.rate_limiting.burst", "burst must be positive", "INVALID_VALUE")
		}
	}
}

// validateNetworkConnectivity validates network connectivity
func (v *Validator) validateNetworkConnectivity(ctx context.Context, result *GCTValidationResult) {
	// Check if gRPC address is reachable
	host, port, err := net.SplitHostPort(v.config.GoCryptoTrader.GRPC.Address)
	if err != nil {
		v.addError(result, "gocryptotrader.grpc.address", fmt.Sprintf("invalid address format: %v", err), "INVALID_FORMAT")
		return
	}

	// Try to resolve the host
	_, err = net.LookupHost(host)
	if err != nil {
		v.addWarning(result, fmt.Sprintf("Cannot resolve host %s: %v (GoCryptoTrader may not be running)", host, err))
	}

	// Try to connect to the port (with timeout)
	timeout := 2 * time.Second
	conn, err := net.DialTimeout("tcp", net.JoinHostPort(host, port), timeout)
	if err != nil {
		v.addWarning(result, fmt.Sprintf("Cannot connect to %s:%s: %v (GoCryptoTrader may not be running)", host, port, err))
	} else {
		conn.Close()
	}
}

// validateGCTConnection validates the connection to GoCryptoTrader
func (v *Validator) validateGCTConnection(ctx context.Context, result *GCTValidationResult) {
	// Create a context with timeout
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	// Try to validate the connection
	err := v.config.ValidateGCTConnection(ctx)
	if err != nil {
		v.addWarning(result, fmt.Sprintf("GoCryptoTrader connection validation failed: %v", err))
	}
}

// validateExchanges validates exchange configuration
func (v *Validator) validateExchanges(result *GCTValidationResult) {
	// Validate KuCoin settings
	if v.config.Exchanges.KuCoin.Enabled {
		if len(v.config.Exchanges.KuCoin.TradingPairs) == 0 {
			v.addWarning(result, "No trading pairs configured for KuCoin")
		}

		validAssetTypes := map[string]bool{"spot": true, "margin": true, "futures": true}
		if !validAssetTypes[v.config.Exchanges.KuCoin.DefaultAssetType] {
			v.addError(result, "exchanges.kucoin.default_asset_type", fmt.Sprintf("invalid asset type: %s", v.config.Exchanges.KuCoin.DefaultAssetType), "INVALID_VALUE")
		}

		// Validate trading pair format
		for i, pair := range v.config.Exchanges.KuCoin.TradingPairs {
			if pair == "" {
				v.addError(result, fmt.Sprintf("exchanges.kucoin.trading_pairs[%d]", i), "trading pair cannot be empty", "INVALID_VALUE")
			}
		}
	}
}

// validateSecurity validates security settings
func (v *Validator) validateSecurity(result *GCTValidationResult) {
	// Check for default credentials
	if v.config.GoCryptoTrader.Auth.Username == "admin" && v.config.GoCryptoTrader.Auth.Password == "Password" {
		v.addWarning(result, "Using default GoCryptoTrader credentials - change these in production!")
	}

	// Check TLS settings
	if !v.config.GoCryptoTrader.GRPC.TLS.Enabled {
		v.addWarning(result, "TLS is disabled for gRPC connection - this is insecure for production")
	}

	if v.config.GoCryptoTrader.GRPC.TLS.Enabled && v.config.GoCryptoTrader.GRPC.TLS.InsecureSkipVerify {
		v.addWarning(result, "TLS certificate verification is disabled - this is insecure for production")
	}
}

// validateFeatures validates feature flags
func (v *Validator) validateFeatures(result *GCTValidationResult) {
	// Validate hot testing settings
	if v.config.Features.HotTesting.Enabled {
		v.addWarning(result, "Hot testing is ENABLED - real money trading is active!")

		if v.config.Features.HotTesting.MaxPositionSizeUSD <= 0 {
			v.addError(result, "features.hot_testing.max_position_size_usd", "max position size must be positive when hot testing is enabled", "INVALID_VALUE")
		}

		if v.config.Features.HotTesting.MaxDailyLossUSD <= 0 {
			v.addError(result, "features.hot_testing.max_daily_loss_usd", "max daily loss must be positive when hot testing is enabled", "INVALID_VALUE")
		}

		// Warn about small limits
		if v.config.Features.HotTesting.MaxPositionSizeUSD < 10 {
			v.addWarning(result, fmt.Sprintf("Max position size is very small: $%.2f", v.config.Features.HotTesting.MaxPositionSizeUSD))
		}
	}

	// Validate backtesting settings
	if v.config.Features.Backtesting.Enabled {
		if v.config.Features.Backtesting.DataDirectory == "" {
			v.addError(result, "features.backtesting.data_directory", "data directory is required when backtesting is enabled", "REQUIRED_FIELD")
		}
	}

	// Validate notifications settings
	if v.config.Features.Notifications.Enabled {
		if len(v.config.Features.Notifications.Channels) == 0 {
			v.addWarning(result, "Notifications are enabled but no channels are configured")
		}

		validChannels := map[string]bool{"slack": true, "telegram": true, "email": true, "sms": true}
		for i, channel := range v.config.Features.Notifications.Channels {
			if !validChannels[channel] {
				v.addError(result, fmt.Sprintf("features.notifications.channels[%d]", i), fmt.Sprintf("invalid notification channel: %s", channel), "INVALID_VALUE")
			}
		}
	}
}

// addError adds a validation error
func (v *Validator) addError(result *GCTValidationResult, field, message, code string) {
	result.Valid = false
	result.Errors = append(result.Errors, GCTValidationError{
		Field:   field,
		Message: message,
		Code:    code,
	})
}

// addWarning adds a validation warning
func (v *Validator) addWarning(result *GCTValidationResult, message string) {
	result.Warnings = append(result.Warnings, message)
}

// ValidateAndPrint validates configuration and prints results
func ValidateAndPrint(ctx context.Context, config *GCTConfig) error {
	validator := NewValidator(config)
	result := validator.ValidateAll(ctx)

	fmt.Println("Configuration Validation Results:")
	fmt.Println("=================================")

	if result.Valid {
		fmt.Println("✅ Configuration is VALID")
	} else {
		fmt.Println("❌ Configuration is INVALID")
	}

	if len(result.Errors) > 0 {
		fmt.Println("\nErrors:")
		for _, err := range result.Errors {
			fmt.Printf("  ❌ [%s] %s: %s\n", err.Code, err.Field, err.Message)
		}
	}

	if len(result.Warnings) > 0 {
		fmt.Println("\nWarnings:")
		for _, warning := range result.Warnings {
			fmt.Printf("  ⚠️  %s\n", warning)
		}
	}

	if !result.Valid {
		return fmt.Errorf("configuration validation failed with %d error(s)", len(result.Errors))
	}

	return nil
}
