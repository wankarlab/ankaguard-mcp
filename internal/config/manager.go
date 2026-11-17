package config

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/spf13/viper"
)

// Manager manages configuration with advanced features
type Manager struct {
	config          *Config
	logger          *logrus.Logger
	mu              sync.RWMutex
	watchers        []ConfigWatcher
	secretsProvider SecretsProvider
	encryptionKey   []byte
	ctx             context.Context
	cancel          context.CancelFunc
}

// ConfigWatcher represents a configuration change watcher
type ConfigWatcher interface {
	OnConfigChange(config *Config) error
}

// SecretsProvider represents a secrets management provider
type SecretsProvider interface {
	GetSecret(ctx context.Context, key string) (string, error)
	SetSecret(ctx context.Context, key, value string) error
	DeleteSecret(ctx context.Context, key string) error
	ListSecrets(ctx context.Context, prefix string) ([]string, error)
	RefreshSecrets(ctx context.Context) error
}

// ExchangeCredentials represents exchange API credentials
type ExchangeCredentials struct {
	APIKey     string            `json:"api_key"`
	APISecret  string            `json:"api_secret"`
	Passphrase string            `json:"passphrase,omitempty"`
	SubAccount string            `json:"sub_account,omitempty"`
	Metadata   map[string]string `json:"metadata,omitempty"`
	CreatedAt  time.Time         `json:"created_at"`
	UpdatedAt  time.Time         `json:"updated_at"`
	ExpiresAt  *time.Time        `json:"expires_at,omitempty"`
}

// NewManager creates a new configuration manager
func NewManager(logger *logrus.Logger) (*Manager, error) {
	config, err := Load()
	if err != nil {
		return nil, fmt.Errorf("failed to load config: %w", err)
	}

	ctx, cancel := context.WithCancel(context.Background())

	manager := &Manager{
		config:   config,
		logger:   logger,
		watchers: make([]ConfigWatcher, 0),
		ctx:      ctx,
		cancel:   cancel,
	}

	// Initialize encryption key
	if err := manager.initEncryption(); err != nil {
		logger.WithError(err).Warn("Failed to initialize encryption, credentials will be stored in plain text")
	}

	// Initialize secrets provider
	if err := manager.initSecretsProvider(); err != nil {
		logger.WithError(err).Warn("Failed to initialize secrets provider, using environment variables")
	}

	return manager, nil
}

// GetConfig returns the current configuration
func (m *Manager) GetConfig() *Config {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.config
}

// UpdateConfig updates the configuration
func (m *Manager) UpdateConfig(newConfig *Config) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.config = newConfig

	// Notify watchers
	for _, watcher := range m.watchers {
		if err := watcher.OnConfigChange(newConfig); err != nil {
			m.logger.WithError(err).Error("Config watcher failed")
		}
	}

	return nil
}

// AddWatcher adds a configuration change watcher
func (m *Manager) AddWatcher(watcher ConfigWatcher) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.watchers = append(m.watchers, watcher)
}

// GetExchangeCredentials retrieves exchange credentials
func (m *Manager) GetExchangeCredentials(exchange string) (*ExchangeCredentials, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	exchangeConfig, exists := m.config.Exchanges[exchange]
	if !exists {
		return nil, fmt.Errorf("exchange %s not configured", exchange)
	}

	// Try to get credentials from secrets provider first
	if m.secretsProvider != nil {
		// Create a default credential config for the provider
		credConfig := &CredentialConfig{
			Source: "provider",
		}
		return m.getCredentialsFromProvider(exchange, credConfig)
	}

	// Fall back to configuration
	return &ExchangeCredentials{
		APIKey:    exchangeConfig.APIKey,
		APISecret: exchangeConfig.APISecret,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}, nil
}

// SetExchangeCredentials stores exchange credentials securely
func (m *Manager) SetExchangeCredentials(exchange string, credentials *ExchangeCredentials) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	credentials.UpdatedAt = time.Now()
	if credentials.CreatedAt.IsZero() {
		credentials.CreatedAt = time.Now()
	}

	// Store in secrets provider if available
	if m.secretsProvider != nil {
		return m.storeCredentialsInProvider(exchange, credentials)
	}

	// Fall back to encrypted file storage
	return m.storeCredentialsInFile(exchange, credentials)
}

// DeleteExchangeCredentials removes exchange credentials
func (m *Manager) DeleteExchangeCredentials(exchange string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.secretsProvider != nil {
		return m.secretsProvider.DeleteSecret(m.ctx, fmt.Sprintf("exchange_%s", exchange))
	}

	// Remove from file storage
	credentialsPath := m.getCredentialsFilePath(exchange)
	if err := os.Remove(credentialsPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to remove credentials file: %w", err)
	}

	return nil
}

// ListExchanges returns a list of configured exchanges
func (m *Manager) ListExchanges() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()

	exchanges := make([]string, 0, len(m.config.Exchanges))
	for exchange := range m.config.Exchanges {
		exchanges = append(exchanges, exchange)
	}

	return exchanges
}

// ValidateExchangeConnection validates exchange API credentials
func (m *Manager) ValidateExchangeConnection(exchange string) error {
	credentials, err := m.GetExchangeCredentials(exchange)
	if err != nil {
		return fmt.Errorf("failed to get credentials: %w", err)
	}

	if credentials.APIKey == "" || credentials.APISecret == "" {
		return fmt.Errorf("missing API credentials for exchange %s", exchange)
	}

	// TODO: Implement actual exchange connection validation
	// This would involve making a test API call to the exchange

	return nil
}

// GetGoCryptoTraderConfig returns GoCryptoTrader configuration
func (m *Manager) GetGoCryptoTraderConfig() *GoCryptoTraderConfig {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return &m.config.GoCryptoTrader
}

// UpdateGoCryptoTraderConfig updates GoCryptoTrader configuration
func (m *Manager) UpdateGoCryptoTraderConfig(config *GoCryptoTraderConfig) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.config.GoCryptoTrader = *config
	return nil
}

// GetNotificationConfig returns notification configuration
func (m *Manager) GetNotificationConfig() *NotificationConfig {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return &m.config.Notifications
}

// UpdateNotificationConfig updates notification configuration
func (m *Manager) UpdateNotificationConfig(config *NotificationConfig) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.config.Notifications = *config
	return nil
}

// GetEnvironmentConfig returns environment-specific configuration
func (m *Manager) GetEnvironmentConfig() *EnvironmentConfig {
	env := os.Getenv("ENVIRONMENT")
	if env == "" {
		env = "development"
	}

	return &EnvironmentConfig{
		Name:     env,
		Debug:    env == "development",
		LogLevel: m.getLogLevelForEnvironment(env),
		Features: m.getFeaturesForEnvironment(env),
	}
}

// RefreshConfiguration reloads configuration from sources
func (m *Manager) RefreshConfiguration() error {
	m.logger.Info("Refreshing configuration")

	newConfig, err := Load()
	if err != nil {
		return fmt.Errorf("failed to reload config: %w", err)
	}

	// Refresh secrets if provider is available
	if m.secretsProvider != nil {
		if err := m.secretsProvider.RefreshSecrets(m.ctx); err != nil {
			m.logger.WithError(err).Warn("Failed to refresh secrets")
		}
	}

	return m.UpdateConfig(newConfig)
}

// Start starts the configuration manager
func (m *Manager) Start() error {
	m.logger.Info("Starting configuration manager")

	// Start configuration refresh routine
	go m.refreshRoutine()

	return nil
}

// Stop stops the configuration manager
func (m *Manager) Stop() error {
	m.logger.Info("Stopping configuration manager")
	m.cancel()
	return nil
}

// Private methods

func (m *Manager) initEncryption() error {
	keyStr := os.Getenv("CONFIG_ENCRYPTION_KEY")
	if keyStr == "" {
		// Generate a random key for development
		key := make([]byte, 32)
		if _, err := rand.Read(key); err != nil {
			return fmt.Errorf("failed to generate encryption key: %w", err)
		}
		m.encryptionKey = key
		m.logger.Warn("Using generated encryption key - set CONFIG_ENCRYPTION_KEY for production")
		return nil
	}

	key, err := base64.StdEncoding.DecodeString(keyStr)
	if err != nil {
		return fmt.Errorf("failed to decode encryption key: %w", err)
	}

	if len(key) != 32 {
		return fmt.Errorf("encryption key must be 32 bytes")
	}

	m.encryptionKey = key
	return nil
}

func (m *Manager) initSecretsProvider() error {
	// TODO: Implement different secrets providers based on configuration
	// For now, we'll use a simple environment variable provider
	return nil
}

func (m *Manager) getCredentialsFromProvider(exchange string, credConfig *CredentialConfig) (*ExchangeCredentials, error) {
	key := fmt.Sprintf("exchange_%s", exchange)

	credentialsJSON, err := m.secretsProvider.GetSecret(m.ctx, key)
	if err != nil {
		return nil, fmt.Errorf("failed to get credentials from provider: %w", err)
	}

	var credentials ExchangeCredentials
	if err := json.Unmarshal([]byte(credentialsJSON), &credentials); err != nil {
		return nil, fmt.Errorf("failed to unmarshal credentials: %w", err)
	}

	return &credentials, nil
}

func (m *Manager) storeCredentialsInProvider(exchange string, credentials *ExchangeCredentials) error {
	key := fmt.Sprintf("exchange_%s", exchange)

	credentialsJSON, err := json.Marshal(credentials)
	if err != nil {
		return fmt.Errorf("failed to marshal credentials: %w", err)
	}

	return m.secretsProvider.SetSecret(m.ctx, key, string(credentialsJSON))
}

func (m *Manager) storeCredentialsInFile(exchange string, credentials *ExchangeCredentials) error {
	credentialsJSON, err := json.Marshal(credentials)
	if err != nil {
		return fmt.Errorf("failed to marshal credentials: %w", err)
	}

	// Encrypt credentials if encryption is available
	var data []byte
	if m.encryptionKey != nil {
		data, err = m.encrypt(credentialsJSON)
		if err != nil {
			return fmt.Errorf("failed to encrypt credentials: %w", err)
		}
	} else {
		data = credentialsJSON
	}

	credentialsPath := m.getCredentialsFilePath(exchange)
	if err := os.MkdirAll(filepath.Dir(credentialsPath), 0700); err != nil {
		return fmt.Errorf("failed to create credentials directory: %w", err)
	}

	if err := os.WriteFile(credentialsPath, data, 0600); err != nil {
		return fmt.Errorf("failed to write credentials file: %w", err)
	}

	return nil
}

func (m *Manager) getCredentialsFilePath(exchange string) string {
	return filepath.Join(os.TempDir(), "trading-mcp", "credentials", fmt.Sprintf("%s.json", exchange))
}

func (m *Manager) encrypt(data []byte) ([]byte, error) {
	block, err := aes.NewCipher(m.encryptionKey)
	if err != nil {
		return nil, err
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}

	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, err
	}

	ciphertext := gcm.Seal(nonce, nonce, data, nil)
	return ciphertext, nil
}

func (m *Manager) decrypt(data []byte) ([]byte, error) {
	block, err := aes.NewCipher(m.encryptionKey)
	if err != nil {
		return nil, err
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}

	nonceSize := gcm.NonceSize()
	if len(data) < nonceSize {
		return nil, fmt.Errorf("ciphertext too short")
	}

	nonce, ciphertext := data[:nonceSize], data[nonceSize:]
	plaintext, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return nil, err
	}

	return plaintext, nil
}

func (m *Manager) getLogLevelForEnvironment(env string) string {
	switch env {
	case "development":
		return "debug"
	case "staging":
		return "info"
	case "production":
		return "warn"
	default:
		return "info"
	}
}

func (m *Manager) getFeaturesForEnvironment(env string) []string {
	switch env {
	case "development":
		return []string{"debug", "metrics", "websocket", "notifications"}
	case "staging":
		return []string{"metrics", "websocket", "notifications"}
	case "production":
		return []string{"websocket", "notifications"}
	default:
		return []string{"websocket"}
	}
}

func (m *Manager) refreshRoutine() {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-m.ctx.Done():
			return
		case <-ticker.C:
			if err := m.RefreshConfiguration(); err != nil {
				m.logger.WithError(err).Error("Failed to refresh configuration")
			}
		}
	}
}

// Utility functions

// LoadFromFile loads configuration from a specific file
func LoadFromFile(filePath string) (*Config, error) {
	viper.SetConfigFile(filePath)

	if err := viper.ReadInConfig(); err != nil {
		return nil, fmt.Errorf("failed to read config file %s: %w", filePath, err)
	}

	var config Config
	if err := viper.Unmarshal(&config); err != nil {
		return nil, fmt.Errorf("failed to unmarshal config: %w", err)
	}

	// Expand environment variables in API keys
	expandEnvVars(&config)

	return &config, nil
}

// expandEnvVars expands environment variables in configuration
func expandEnvVars(config *Config) {
	// Expand LLM API key
	if config.AI.LLM.APIKey != "" {
		config.AI.LLM.APIKey = os.ExpandEnv(config.AI.LLM.APIKey)
	}

	// Expand sentiment API keys
	for name, source := range config.AI.Sentiment.Sources {
		if source.APIKey != "" {
			source.APIKey = os.ExpandEnv(source.APIKey)
			config.AI.Sentiment.Sources[name] = source
		}
		if source.ClientID != "" {
			source.ClientID = os.ExpandEnv(source.ClientID)
			config.AI.Sentiment.Sources[name] = source
		}
		if source.ClientSecret != "" {
			source.ClientSecret = os.ExpandEnv(source.ClientSecret)
			config.AI.Sentiment.Sources[name] = source
		}
	}

	// Expand exchange API keys
	for name, exchange := range config.Exchanges {
		if exchange.APIKey != "" {
			exchange.APIKey = os.ExpandEnv(exchange.APIKey)
			config.Exchanges[name] = exchange
		}
		if exchange.APISecret != "" {
			exchange.APISecret = os.ExpandEnv(exchange.APISecret)
			config.Exchanges[name] = exchange
		}
		if exchange.Passphrase != "" {
			exchange.Passphrase = os.ExpandEnv(exchange.Passphrase)
			config.Exchanges[name] = exchange
		}
	}
}

// SaveToFile saves configuration to a file
func SaveToFile(config *Config, filePath string) error {
	// Create directory if it doesn't exist
	if err := os.MkdirAll(filepath.Dir(filePath), 0755); err != nil {
		return fmt.Errorf("failed to create config directory: %w", err)
	}

	// Marshal config to YAML
	viper.Set("config", config)
	if err := viper.WriteConfigAs(filePath); err != nil {
		return fmt.Errorf("failed to write config file: %w", err)
	}

	return nil
}

// ValidateConfig validates configuration
func ValidateConfig(config *Config) error {
	if config.Port <= 0 || config.Port > 65535 {
		return fmt.Errorf("invalid port: %d", config.Port)
	}

	if config.Host == "" {
		return fmt.Errorf("host cannot be empty")
	}

	// Validate GoCryptoTrader configuration
	if config.GoCryptoTrader.HTTPAddress == "" && config.GoCryptoTrader.GRPCAddress == "" {
		return fmt.Errorf("at least one GoCryptoTrader address must be configured")
	}

	// Validate exchange configurations
	for name, exchange := range config.Exchanges {
		if exchange.Name == "" {
			return fmt.Errorf("exchange %s: name cannot be empty", name)
		}
	}

	return nil
}

// MergeConfigs merges two configurations, with the second taking precedence
func MergeConfigs(base, override *Config) *Config {
	// This is a simplified merge - in practice you'd want a more sophisticated merge
	merged := *base

	if override.Port != 0 {
		merged.Port = override.Port
	}

	if override.Host != "" {
		merged.Host = override.Host
	}

	// Merge exchanges
	if override.Exchanges != nil {
		if merged.Exchanges == nil {
			merged.Exchanges = make(map[string]ExchangeConfig)
		}
		for name, config := range override.Exchanges {
			merged.Exchanges[name] = config
		}
	}

	return &merged
}
