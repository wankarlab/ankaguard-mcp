package llm

import (
	"context"
	"fmt"
	"os"
	"sync"
	"time"

	dynamic "github.com/rantipay/trading-mcp/internal/config/dynamic"
	"github.com/sirupsen/logrus"
)

// ProviderType represents the type of LLM provider
type ProviderType string

const (
	ProviderTypeGemini     ProviderType = "google"
	ProviderTypeOpenAI     ProviderType = "openai"
	ProviderTypeAnthropic  ProviderType = "anthropic"
	ProviderTypeOpenRouter ProviderType = "openrouter"
	ProviderTypeGroq       ProviderType = "groq"
	ProviderTypeNVIDIA     ProviderType = "nvidia"
)

// ProviderPriority defines the fallback order for providers
// Groq is placed second — it's the fastest inference and free tier is generous
var DefaultProviderPriority = []ProviderType{
	ProviderTypeGroq,       // First: fastest inference, generous free tier
	ProviderTypeGemini,     // Second: good free quota
	ProviderTypeNVIDIA,     // Third: NVIDIA NIM, OpenAI-compatible, free credit tier
	ProviderTypeOpenRouter, // Fourth: aggregator with many models
	ProviderTypeOpenAI,
	ProviderTypeAnthropic,
}

// MultiProviderConfig holds configuration for multiple LLM providers
type MultiProviderConfig struct {
	// Provider configurations
	Providers map[ProviderType]*ProviderConfig

	// Fallback order (first available is used)
	FallbackOrder []ProviderType

	// Rate limiting per provider (requests per minute)
	RateLimits map[ProviderType]int

	// Health check interval
	HealthCheckInterval time.Duration

	// Enable automatic failover
	AutoFailover bool

	// Max consecutive failures before marking a provider as unhealthy
	MaxRetriesBeforeFailover int

	// After this duration, an unhealthy provider gets a recovery attempt
	RecoveryInterval time.Duration
}

// ProviderManager manages multiple LLM providers with automatic fallback
type ProviderManager struct {
	config       *MultiProviderConfig
	providers    map[ProviderType]Provider
	rateLimiters map[ProviderType]*RateLimiter
	healthStatus map[ProviderType]*ProviderHealthStatus
	mu           sync.RWMutex
	log          *logrus.Logger
}

// ProviderHealthStatus tracks the health of a provider
type ProviderHealthStatus struct {
	IsHealthy        bool
	LastCheck        time.Time
	LastSuccess      time.Time
	LastError        error
	ConsecutiveFails int
	TotalCalls       int64
	SuccessfulCalls  int64
	AvgLatency       time.Duration
}

// RateLimiter implements a simple token bucket rate limiter
type RateLimiter struct {
	tokens     int
	maxTokens  int
	refillRate time.Duration
	lastRefill time.Time
	mu         sync.Mutex
}

// NewRateLimiter creates a new rate limiter
func NewRateLimiter(requestsPerMinute int) *RateLimiter {
	return &RateLimiter{
		tokens:     requestsPerMinute,
		maxTokens:  requestsPerMinute,
		refillRate: time.Minute / time.Duration(requestsPerMinute),
		lastRefill: time.Now(),
	}
}

// Allow checks if a request is allowed
func (rl *RateLimiter) Allow() bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	// Refill tokens based on elapsed time
	now := time.Now()
	elapsed := now.Sub(rl.lastRefill)
	tokensToAdd := int(elapsed / rl.refillRate)
	if tokensToAdd > 0 {
		rl.tokens = min(rl.tokens+tokensToAdd, rl.maxTokens)
		rl.lastRefill = now
	}

	if rl.tokens > 0 {
		rl.tokens--
		return true
	}
	return false
}

// resolveFallbackOrder builds the provider fallback order, honoring AI_PROVIDER
// (primary) and AI_FALLBACK_PROVIDER (secondary) env vars. Any remaining
// providers from DefaultProviderPriority are appended as deeper fallbacks so a
// total outage of the preferred providers still degrades gracefully.
func resolveFallbackOrder() []ProviderType {
	valid := map[string]ProviderType{
		"google":     ProviderTypeGemini,
		"openai":     ProviderTypeOpenAI,
		"anthropic":  ProviderTypeAnthropic,
		"openrouter": ProviderTypeOpenRouter,
		"groq":       ProviderTypeGroq,
		"nvidia":     ProviderTypeNVIDIA,
	}

	order := make([]ProviderType, 0, len(DefaultProviderPriority))
	seen := make(map[ProviderType]bool)
	add := func(pt ProviderType) {
		if pt != "" && !seen[pt] {
			order = append(order, pt)
			seen[pt] = true
		}
	}

	if pt, ok := valid[os.Getenv("AI_PROVIDER")]; ok {
		add(pt)
	}
	if pt, ok := valid[os.Getenv("AI_FALLBACK_PROVIDER")]; ok {
		add(pt)
	}
	for _, pt := range DefaultProviderPriority {
		add(pt)
	}

	if len(order) == 0 {
		return DefaultProviderPriority
	}
	return order
}

// NewMultiProviderConfig creates a configuration from environment variables
func NewMultiProviderConfig() *MultiProviderConfig {
	config := &MultiProviderConfig{
		Providers:                make(map[ProviderType]*ProviderConfig),
		FallbackOrder:            resolveFallbackOrder(),
		RateLimits:               make(map[ProviderType]int),
		HealthCheckInterval:      5 * time.Minute,
		AutoFailover:             true,
		MaxRetriesBeforeFailover: 3,                // Open circuit after 3 consecutive fails
		RecoveryInterval:         90 * time.Second, // Auto-recover after 90s (fast enough for trading)
	}

	// TODO(TC-6.1): missing manifest keys llm.gemini.model, llm.gemini.timeout, llm.gemini.max_retries,
	// llm.gemini.retry_delay, llm.gemini.temperature, llm.gemini.max_tokens,
	// llm.gemini.cost_per_input_k, llm.gemini.cost_per_output_k, llm.gemini.rate_limit_rpm
	// Load Google Gemini config
	if apiKey := os.Getenv("GOOGLE_API_KEY"); apiKey != "" && apiKey != "your_google_api_key_here" {
		config.Providers[ProviderTypeGemini] = &ProviderConfig{
			Name:           string(ProviderTypeGemini),
			APIKey:         apiKey,
			Model:          getEnvOrDefault("GEMINI_MODEL", "gemini-2.5-flash"),
			Endpoint:       "https://generativelanguage.googleapis.com/v1beta/models",
			TimeoutSeconds: 30,
			MaxRetries:     3,
			RetryDelay:     time.Second,
			Temperature:    0.7,
			MaxTokens:      1000,
			CostPerInputK:  0.000075, // Gemini 2.0 Flash pricing
			CostPerOutputK: 0.0003,
		}
		config.RateLimits[ProviderTypeGemini] = 60 // 60 RPM for Gemini
	}

	// TODO(TC-6.1): missing manifest keys llm.openai.model, llm.openai.timeout, llm.openai.max_retries,
	// llm.openai.retry_delay, llm.openai.temperature, llm.openai.max_tokens,
	// llm.openai.cost_per_input_k, llm.openai.cost_per_output_k, llm.openai.rate_limit_rpm
	// Load OpenAI config
	if apiKey := os.Getenv("OPENAI_API_KEY"); apiKey != "" && apiKey != "your_openai_api_key_here" {
		config.Providers[ProviderTypeOpenAI] = &ProviderConfig{
			Name:           string(ProviderTypeOpenAI),
			APIKey:         apiKey,
			Model:          getEnvOrDefault("OPENAI_MODEL", "gpt-4o-mini"),
			Endpoint:       "https://api.openai.com/v1/chat/completions",
			TimeoutSeconds: 30,
			MaxRetries:     3,
			RetryDelay:     time.Second,
			Temperature:    0.7,
			MaxTokens:      1000,
			CostPerInputK:  0.00015, // GPT-4o-mini pricing
			CostPerOutputK: 0.0006,
		}
		config.RateLimits[ProviderTypeOpenAI] = 60
	}

	// TODO(TC-6.1): missing manifest keys llm.anthropic.model, llm.anthropic.timeout, llm.anthropic.max_retries,
	// llm.anthropic.retry_delay, llm.anthropic.temperature, llm.anthropic.max_tokens,
	// llm.anthropic.cost_per_input_k, llm.anthropic.cost_per_output_k, llm.anthropic.rate_limit_rpm
	// Load Anthropic config
	if apiKey := os.Getenv("ANTHROPIC_API_KEY"); apiKey != "" && apiKey != "your_anthropic_api_key_here" {
		config.Providers[ProviderTypeAnthropic] = &ProviderConfig{
			Name:           string(ProviderTypeAnthropic),
			APIKey:         apiKey,
			Model:          getEnvOrDefault("ANTHROPIC_MODEL", "claude-3-haiku-20240307"),
			Endpoint:       "https://api.anthropic.com/v1/messages",
			TimeoutSeconds: 30,
			MaxRetries:     3,
			RetryDelay:     time.Second,
			Temperature:    0.7,
			MaxTokens:      1000,
			CostPerInputK:  0.00025, // Claude 3 Haiku pricing
			CostPerOutputK: 0.00125,
		}
		config.RateLimits[ProviderTypeAnthropic] = 60
	}

	// TODO(TC-6.1): missing manifest keys llm.openrouter.model, llm.openrouter.timeout, llm.openrouter.max_retries,
	// llm.openrouter.retry_delay, llm.openrouter.temperature, llm.openrouter.max_tokens,
	// llm.openrouter.cost_per_input_k, llm.openrouter.cost_per_output_k, llm.openrouter.rate_limit_rpm
	// Load OpenRouter config
	if apiKey := os.Getenv("OPENROUTER_API_KEY"); apiKey != "" && apiKey != "your_openrouter_api_key_here" {
		config.Providers[ProviderTypeOpenRouter] = &ProviderConfig{
			Name:   string(ProviderTypeOpenRouter),
			APIKey: apiKey,
			// google/gemini-2.0-flash-001 returns 404 (no endpoints) with current keys;
			// meta-llama/llama-3.3-70b-instruct is verified 200 OK and OpenAI-compatible.
			Model:          getEnvOrDefault("OPENROUTER_MODEL", "meta-llama/llama-3.3-70b-instruct"),
			Endpoint:       "https://openrouter.ai/api/v1/chat/completions",
			TimeoutSeconds: 30,
			MaxRetries:     3,
			RetryDelay:     time.Second,
			Temperature:    0.7,
			MaxTokens:      1000,
			CostPerInputK:  0.000075, // Varies by model
			CostPerOutputK: 0.0003,
		}
		config.RateLimits[ProviderTypeOpenRouter] = 60
	}

	// TODO(TC-6.1): missing manifest keys llm.groq.model, llm.groq.timeout, llm.groq.max_retries,
	// llm.groq.retry_delay, llm.groq.temperature, llm.groq.max_tokens,
	// llm.groq.cost_per_input_k, llm.groq.cost_per_output_k, llm.groq.rate_limit_rpm
	// Load Groq config — ultra-fast inference, OpenAI-compatible
	if apiKey := os.Getenv("GROQ_API_KEY"); apiKey != "" {
		config.Providers[ProviderTypeGroq] = &ProviderConfig{
			Name:           string(ProviderTypeGroq),
			APIKey:         apiKey,
			Model:          getEnvOrDefault("GROQ_MODEL", "llama-3.3-70b-versatile"),
			Endpoint:       "https://api.groq.com/openai/v1/chat/completions",
			TimeoutSeconds: 20, // Groq is very fast, lower timeout
			MaxRetries:     3,
			RetryDelay:     500 * time.Millisecond,
			Temperature:    0.7,
			MaxTokens:      1000,
			CostPerInputK:  0.00059, // $0.59 per 1M tokens
			CostPerOutputK: 0.00079, // $0.79 per 1M tokens
		}
		config.RateLimits[ProviderTypeGroq] = 30 // Groq free tier: 30 RPM
	}

	// Load NVIDIA NIM config — OpenAI-compatible, hosted on build.nvidia.com.
	// Keys are prefixed "nvapi-". Free within the credit tier, so costs default
	// to zero unless NVIDIA_COST_* are wired later.
	if apiKey := os.Getenv("NVIDIA_API_KEY"); apiKey != "" && apiKey != "your_nvidia_api_key_here" {
		config.Providers[ProviderTypeNVIDIA] = &ProviderConfig{
			Name:           string(ProviderTypeNVIDIA),
			APIKey:         apiKey,
			Model:          getEnvOrDefault("NVIDIA_MODEL", "meta/llama-3.3-70b-instruct"),
			Endpoint:       "https://integrate.api.nvidia.com/v1/chat/completions",
			TimeoutSeconds: 30,
			MaxRetries:     3,
			RetryDelay:     time.Second,
			Temperature:    0.7,
			MaxTokens:      1000,
			CostPerInputK:  0.0, // free credit tier
			CostPerOutputK: 0.0,
		}
		config.RateLimits[ProviderTypeNVIDIA] = 60
	}

	return config
}

// getEnvOrDefault gets an environment variable or returns a default value
func getEnvOrDefault(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

// NewProviderManager creates a new provider manager
func NewProviderManager(config *MultiProviderConfig, log *logrus.Logger) (*ProviderManager, error) {
	if log == nil {
		log = logrus.New()
	}

	pm := &ProviderManager{
		config:       config,
		providers:    make(map[ProviderType]Provider),
		rateLimiters: make(map[ProviderType]*RateLimiter),
		healthStatus: make(map[ProviderType]*ProviderHealthStatus),
		log:          log,
	}

	// Initialize providers
	for providerType, providerConfig := range config.Providers {
		provider, err := pm.createProvider(providerType, providerConfig)
		if err != nil {
			log.WithError(err).WithField("provider", providerType).Warn("Failed to create provider")
			continue
		}
		pm.providers[providerType] = provider
		pm.healthStatus[providerType] = &ProviderHealthStatus{
			IsHealthy: true, // Assume healthy until proven otherwise
		}

		// Initialize rate limiter — llm.provider.default_rate_limit_rpm (default 60) is hot-reloadable
		rateLimit := config.RateLimits[providerType]
		if rateLimit == 0 {
			rateLimit = 60 // Default 60 RPM
			if snap := dynamic.SnapshotFromContext(context.Background()); snap != nil {
				if v := snap.Int("llm.provider.default_rate_limit_rpm"); v > 0 {
					rateLimit = v
				}
			}
		}
		pm.rateLimiters[providerType] = NewRateLimiter(rateLimit)

		log.WithFields(logrus.Fields{
			"provider":   providerType,
			"model":      providerConfig.Model,
			"rate_limit": rateLimit,
		}).Info("LLM provider initialized")
	}

	if len(pm.providers) == 0 {
		return nil, fmt.Errorf("no LLM providers configured - please set at least one API key (GOOGLE_API_KEY, OPENAI_API_KEY, ANTHROPIC_API_KEY, OPENROUTER_API_KEY, GROQ_API_KEY, or NVIDIA_API_KEY)")
	}

	log.WithField("provider_count", len(pm.providers)).Info("Provider manager initialized")
	return pm, nil
}

// createProvider creates a provider instance based on type
func (pm *ProviderManager) createProvider(providerType ProviderType, config *ProviderConfig) (Provider, error) {
	switch providerType {
	case ProviderTypeGemini:
		return NewGeminiProvider(config), nil
	case ProviderTypeOpenAI:
		return NewOpenAIProvider(config), nil
	case ProviderTypeAnthropic:
		return NewAnthropicProvider(config), nil
	case ProviderTypeOpenRouter:
		return NewOpenRouterProvider(config), nil
	case ProviderTypeGroq:
		return NewGroqProvider(config), nil
	case ProviderTypeNVIDIA:
		return NewNVIDIAProvider(config), nil
	default:
		return nil, fmt.Errorf("unknown provider type: %s", providerType)
	}
}

// Call makes a call to the best available provider with automatic fallback
func (pm *ProviderManager) Call(ctx context.Context, request *LLMRequest) (*LLMResponse, error) {
	pm.mu.RLock()
	defer pm.mu.RUnlock()

	var lastErr error

	// Try providers in fallback order
	for _, providerType := range pm.config.FallbackOrder {
		provider, exists := pm.providers[providerType]
		if !exists {
			continue
		}

		// Check rate limit
		rateLimiter, hasLimiter := pm.rateLimiters[providerType]
		if hasLimiter && !rateLimiter.Allow() {
			pm.log.WithField("provider", providerType).Warn("Provider rate limit exceeded, trying next")
			lastErr = fmt.Errorf("provider %s: rate limit exceeded", providerType)
			continue
		}

		// Check health status — but allow recovery after cooldown period
		// llm.provider.max_retries_before_failover (default 3) and llm.provider.recovery_interval (default 90s)
		// are hot-reloadable via dynamic config.
		maxRetries := pm.config.MaxRetriesBeforeFailover
		recoveryInterval := pm.config.RecoveryInterval
		autoFailover := pm.config.AutoFailover
		if snap := dynamic.SnapshotFromContext(ctx); snap != nil {
			if v := snap.Int("llm.provider.max_retries_before_failover"); v > 0 {
				maxRetries = v
			}
			if v := snap.Duration("llm.provider.recovery_interval"); v > 0 {
				recoveryInterval = v
			}
			autoFailover = snap.Bool("llm.provider.auto_failover")
		}
		health := pm.healthStatus[providerType]
		if health != nil && !health.IsHealthy && health.ConsecutiveFails >= maxRetries {
			// Check if enough time has passed for a recovery attempt
			if recoveryInterval == 0 {
				recoveryInterval = 90 * time.Second
			}
			if time.Since(health.LastCheck) < recoveryInterval {
				remaining := recoveryInterval - time.Since(health.LastCheck)
				pm.log.WithFields(logrus.Fields{
					"provider":           providerType,
					"consecutive_fails":  health.ConsecutiveFails,
					"cooldown_remaining": remaining.Round(time.Second).String(),
				}).Warn("Provider in cooldown, trying next")
				lastErr = fmt.Errorf("provider %s: in cooldown for %s (consecutive_fails=%d)",
					providerType, remaining.Round(time.Second), health.ConsecutiveFails)
				continue
			}
			// Recovery window — reset fails to give this provider another chance
			pm.log.WithField("provider", providerType).Info("Provider recovery attempt after cooldown")
			health.ConsecutiveFails = 0
			health.IsHealthy = true
		}

		// Try the provider
		startTime := time.Now()
		response, err := provider.Call(ctx, request)
		latency := time.Since(startTime)

		if err == nil {
			// Success - update health status
			pm.updateHealthStatus(providerType, true, latency, nil)
			response.Latency = latency
			return response, nil
		}

		// Failure - update health status
		pm.updateHealthStatus(providerType, false, latency, err)
		lastErr = err

		pm.log.WithError(err).WithFields(logrus.Fields{
			"provider": providerType,
			"latency":  latency,
		}).Warn("Provider call failed, trying next")

		// Continue to next provider if auto-failover is enabled
		if !autoFailover {
			break
		}
	}

	if lastErr == nil {
		lastErr = fmt.Errorf("all providers are unavailable (none configured or all in cooldown)")
	}
	return nil, fmt.Errorf("all providers failed: %w", lastErr)
}

// updateHealthStatus updates the health status for a provider
func (pm *ProviderManager) updateHealthStatus(providerType ProviderType, success bool, latency time.Duration, err error) {
	health := pm.healthStatus[providerType]
	if health == nil {
		return
	}

	health.TotalCalls++
	if success {
		health.SuccessfulCalls++
		health.ConsecutiveFails = 0
		health.LastSuccess = time.Now()
		health.IsHealthy = true
		health.LastError = nil
	} else {
		health.ConsecutiveFails++
		health.LastError = err
		// llm.provider.max_retries_before_failover (default 3) — hot-reloadable
		maxRetries := pm.config.MaxRetriesBeforeFailover
		if snap := dynamic.SnapshotFromContext(context.Background()); snap != nil {
			if v := snap.Int("llm.provider.max_retries_before_failover"); v > 0 {
				maxRetries = v
			}
		}
		if health.ConsecutiveFails >= maxRetries {
			health.IsHealthy = false
		}
	}
	health.LastCheck = time.Now()

	// Update average latency
	if health.TotalCalls > 1 {
		health.AvgLatency = (health.AvgLatency*time.Duration(health.TotalCalls-1) + latency) / time.Duration(health.TotalCalls)
	} else {
		health.AvgLatency = latency
	}
}

// GetHealthStatus returns the health status of all providers
func (pm *ProviderManager) GetHealthStatus() map[ProviderType]*ProviderHealthStatus {
	pm.mu.RLock()
	defer pm.mu.RUnlock()

	status := make(map[ProviderType]*ProviderHealthStatus)
	for pt, hs := range pm.healthStatus {
		// Create a copy to avoid race conditions
		status[pt] = &ProviderHealthStatus{
			IsHealthy:        hs.IsHealthy,
			LastCheck:        hs.LastCheck,
			LastSuccess:      hs.LastSuccess,
			LastError:        hs.LastError,
			ConsecutiveFails: hs.ConsecutiveFails,
			TotalCalls:       hs.TotalCalls,
			SuccessfulCalls:  hs.SuccessfulCalls,
			AvgLatency:       hs.AvgLatency,
		}
	}
	return status
}

// GetAvailableProviders returns a list of configured providers
func (pm *ProviderManager) GetAvailableProviders() []ProviderType {
	pm.mu.RLock()
	defer pm.mu.RUnlock()

	providers := make([]ProviderType, 0, len(pm.providers))
	for pt := range pm.providers {
		providers = append(providers, pt)
	}
	return providers
}

// GetPrimaryProvider returns the first available healthy provider
func (pm *ProviderManager) GetPrimaryProvider() (Provider, ProviderType) {
	pm.mu.RLock()
	defer pm.mu.RUnlock()

	for _, pt := range pm.config.FallbackOrder {
		if provider, exists := pm.providers[pt]; exists {
			health := pm.healthStatus[pt]
			if health == nil || health.IsHealthy {
				return provider, pt
			}
		}
	}

	// Return first available if none are healthy
	for pt, provider := range pm.providers {
		return provider, pt
	}

	return nil, ""
}

// HealthCheck performs a health check on all providers
func (pm *ProviderManager) HealthCheck(ctx context.Context) map[ProviderType]bool {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	results := make(map[ProviderType]bool)

	for pt, provider := range pm.providers {
		// Simple health check - try to check if provider is available
		isAvailable := provider.IsAvailable(ctx)
		results[pt] = isAvailable

		if health := pm.healthStatus[pt]; health != nil {
			health.LastCheck = time.Now()
			// llm.provider.max_retries_before_failover (default 3) — hot-reloadable
			maxRetries := pm.config.MaxRetriesBeforeFailover
			if snap := dynamic.SnapshotFromContext(ctx); snap != nil {
				if v := snap.Int("llm.provider.max_retries_before_failover"); v > 0 {
					maxRetries = v
				}
			}
			if isAvailable && health.ConsecutiveFails < maxRetries {
				health.IsHealthy = true
			}
		}

		pm.log.WithFields(logrus.Fields{
			"provider":  pt,
			"available": isAvailable,
		}).Debug("Health check completed")
	}

	return results
}

// GetProviderStats returns statistics for all providers
func (pm *ProviderManager) GetProviderStats() map[string]*ProviderStats {
	pm.mu.RLock()
	defer pm.mu.RUnlock()

	stats := make(map[string]*ProviderStats)
	for pt, provider := range pm.providers {
		stats[string(pt)] = provider.GetStats()
	}
	return stats
}

// min returns the minimum of two integers
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
