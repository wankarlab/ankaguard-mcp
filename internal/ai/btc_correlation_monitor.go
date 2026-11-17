package ai

import (
	"context"
	"fmt"
	"math"
	"sync"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/rantipay/trading-mcp/internal/gct"
)

// BTCCorrelationMonitor monitors BTC price movements and their correlation with altcoins
type BTCCorrelationMonitor struct {
	gctClient gct.GCTClient
	logger    *logrus.Logger
	config    *BTCCorrelationConfig

	// Price tracking
	btcPrices     []PricePoint
	altcoinPrices map[string][]PricePoint
	correlations  map[string]float64

	// State
	running  bool
	stopChan chan struct{}
	mu       sync.RWMutex

	// Callbacks
	onBTCDrop           func(ctx context.Context, dropPercent float64, affectedSymbols []string) error
	onCorrelationUpdate func(symbol string, correlation float64)
}

// BTCCorrelationConfig holds configuration for BTC correlation monitoring
type BTCCorrelationConfig struct {
	// Monitoring settings
	CheckInterval    time.Duration `yaml:"check_interval"`
	PriceHistorySize int           `yaml:"price_history_size"`

	// Correlation settings
	CorrelationWindow        int     `yaml:"correlation_window"`         // Number of price points for correlation calculation
	HighCorrelationThreshold float64 `yaml:"high_correlation_threshold"` // e.g., 0.7

	// BTC drop detection
	BTCDropThreshold  float64       `yaml:"btc_drop_threshold"`   // e.g., 3.0 (3%)
	BTCDropTimeWindow time.Duration `yaml:"btc_drop_time_window"` // e.g., 15 minutes

	// Stop loss adjustment
	SLAdjustmentFactor float64 `yaml:"sl_adjustment_factor"` // e.g., 1.5 (widen SL by 50%)
	MinSLAdjustment    float64 `yaml:"min_sl_adjustment"`    // Minimum adjustment in %
	MaxSLAdjustment    float64 `yaml:"max_sl_adjustment"`    // Maximum adjustment in %

	// Symbols to monitor
	AltcoinSymbols []string `yaml:"altcoin_symbols"`
	BTCSymbol      string   `yaml:"btc_symbol"`
	Exchange       string   `yaml:"exchange"`
}

// PricePoint represents a price at a specific time
type PricePoint struct {
	Price     float64   `json:"price"`
	Timestamp time.Time `json:"timestamp"`
}

// CorrelationResult represents correlation analysis result
type CorrelationResult struct {
	Symbol      string    `json:"symbol"`
	Correlation float64   `json:"correlation"`
	IsHigh      bool      `json:"is_high"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// BTCDropEvent represents a significant BTC drop event
type BTCDropEvent struct {
	DropPercent     float64       `json:"drop_percent"`
	TimeWindow      time.Duration `json:"time_window"`
	StartPrice      float64       `json:"start_price"`
	CurrentPrice    float64       `json:"current_price"`
	AffectedSymbols []string      `json:"affected_symbols"`
	DetectedAt      time.Time     `json:"detected_at"`
}

// NewBTCCorrelationMonitor creates a new BTC correlation monitor
func NewBTCCorrelationMonitor(gctClient gct.GCTClient, config *BTCCorrelationConfig, logger *logrus.Logger) *BTCCorrelationMonitor {
	if logger == nil {
		logger = logrus.New()
	}

	if config == nil {
		config = GetDefaultBTCCorrelationConfig()
	}

	return &BTCCorrelationMonitor{
		gctClient:     gctClient,
		logger:        logger,
		config:        config,
		btcPrices:     make([]PricePoint, 0, config.PriceHistorySize),
		altcoinPrices: make(map[string][]PricePoint),
		correlations:  make(map[string]float64),
		stopChan:      make(chan struct{}),
	}
}

// Start starts the BTC correlation monitoring
func (m *BTCCorrelationMonitor) Start(ctx context.Context) error {
	m.mu.Lock()
	if m.running {
		m.mu.Unlock()
		return fmt.Errorf("BTC correlation monitor already running")
	}
	m.running = true
	m.mu.Unlock()

	m.logger.Info("Starting BTC correlation monitor")

	// Initialize altcoin price arrays
	for _, symbol := range m.config.AltcoinSymbols {
		m.altcoinPrices[symbol] = make([]PricePoint, 0, m.config.PriceHistorySize)
	}

	// Start monitoring goroutine
	go m.monitoringLoop(ctx)

	return nil
}

// Stop stops the BTC correlation monitoring
func (m *BTCCorrelationMonitor) Stop() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.running {
		return fmt.Errorf("BTC correlation monitor not running")
	}

	m.logger.Info("Stopping BTC correlation monitor")
	close(m.stopChan)
	m.running = false

	return nil
}

// SetBTCDropCallback sets the callback for BTC drop events
func (m *BTCCorrelationMonitor) SetBTCDropCallback(callback func(ctx context.Context, dropPercent float64, affectedSymbols []string) error) {
	m.onBTCDrop = callback
}

// SetCorrelationUpdateCallback sets the callback for correlation updates
func (m *BTCCorrelationMonitor) SetCorrelationUpdateCallback(callback func(symbol string, correlation float64)) {
	m.onCorrelationUpdate = callback
}

// GetCorrelations returns current correlations for all monitored symbols
func (m *BTCCorrelationMonitor) GetCorrelations() map[string]CorrelationResult {
	m.mu.RLock()
	defer m.mu.RUnlock()

	results := make(map[string]CorrelationResult)
	for symbol, correlation := range m.correlations {
		results[symbol] = CorrelationResult{
			Symbol:      symbol,
			Correlation: correlation,
			IsHigh:      correlation >= m.config.HighCorrelationThreshold,
			UpdatedAt:   time.Now(),
		}
	}

	return results
}

// GetBTCPriceHistory returns recent BTC price history
func (m *BTCCorrelationMonitor) GetBTCPriceHistory() []PricePoint {
	m.mu.RLock()
	defer m.mu.RUnlock()

	// Return a copy
	history := make([]PricePoint, len(m.btcPrices))
	copy(history, m.btcPrices)
	return history
}

// monitoringLoop runs the main monitoring loop
func (m *BTCCorrelationMonitor) monitoringLoop(ctx context.Context) {
	ticker := time.NewTicker(m.config.CheckInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-m.stopChan:
			return
		case <-ticker.C:
			if err := m.updatePrices(ctx); err != nil {
				m.logger.WithError(err).Error("Failed to update prices")
				continue
			}

			m.updateCorrelations()
			m.checkBTCDrop(ctx)
		}
	}
}

// updatePrices fetches and stores current prices for BTC and altcoins
func (m *BTCCorrelationMonitor) updatePrices(ctx context.Context) error {
	now := time.Now()

	// Get BTC price
	btcTicker, err := m.gctClient.GetTicker(ctx, m.config.Exchange, m.config.BTCSymbol)
	if err != nil {
		return fmt.Errorf("failed to get BTC ticker: %w", err)
	}

	// Store BTC price
	m.mu.Lock()
	m.btcPrices = append(m.btcPrices, PricePoint{
		Price:     btcTicker.Last,
		Timestamp: now,
	})

	// Trim BTC price history
	if len(m.btcPrices) > m.config.PriceHistorySize {
		m.btcPrices = m.btcPrices[len(m.btcPrices)-m.config.PriceHistorySize:]
	}
	m.mu.Unlock()

	// Get altcoin prices
	for _, symbol := range m.config.AltcoinSymbols {
		ticker, err := m.gctClient.GetTicker(ctx, m.config.Exchange, symbol)
		if err != nil {
			m.logger.WithError(err).WithField("symbol", symbol).Warn("Failed to get altcoin ticker")
			continue
		}

		m.mu.Lock()
		m.altcoinPrices[symbol] = append(m.altcoinPrices[symbol], PricePoint{
			Price:     ticker.Last,
			Timestamp: now,
		})

		// Trim altcoin price history
		if len(m.altcoinPrices[symbol]) > m.config.PriceHistorySize {
			m.altcoinPrices[symbol] = m.altcoinPrices[symbol][len(m.altcoinPrices[symbol])-m.config.PriceHistorySize:]
		}
		m.mu.Unlock()
	}

	return nil
}

// updateCorrelations calculates correlations between BTC and altcoins
func (m *BTCCorrelationMonitor) updateCorrelations() {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Need at least correlation window size of data points
	if len(m.btcPrices) < m.config.CorrelationWindow {
		return
	}

	// Get recent BTC price changes
	btcChanges := m.calculatePriceChanges(m.btcPrices, m.config.CorrelationWindow)

	// Calculate correlation for each altcoin
	for symbol, prices := range m.altcoinPrices {
		if len(prices) < m.config.CorrelationWindow {
			continue
		}

		altChanges := m.calculatePriceChanges(prices, m.config.CorrelationWindow)
		correlation := m.calculateCorrelation(btcChanges, altChanges)

		oldCorrelation := m.correlations[symbol]
		m.correlations[symbol] = correlation

		// Call callback if correlation changed significantly
		if m.onCorrelationUpdate != nil && math.Abs(correlation-oldCorrelation) > 0.1 {
			go m.onCorrelationUpdate(symbol, correlation)
		}

		m.logger.WithFields(logrus.Fields{
			"symbol":      symbol,
			"correlation": correlation,
			"is_high":     correlation >= m.config.HighCorrelationThreshold,
		}).Debug("Updated correlation")
	}
}

// calculatePriceChanges calculates percentage price changes for the last N points
func (m *BTCCorrelationMonitor) calculatePriceChanges(prices []PricePoint, window int) []float64 {
	if len(prices) < window {
		return nil
	}

	// Get the last 'window' prices
	recentPrices := prices[len(prices)-window:]
	changes := make([]float64, len(recentPrices)-1)

	for i := 1; i < len(recentPrices); i++ {
		prevPrice := recentPrices[i-1].Price
		currentPrice := recentPrices[i].Price
		change := (currentPrice - prevPrice) / prevPrice * 100.0
		changes[i-1] = change
	}

	return changes
}

// calculateCorrelation calculates Pearson correlation coefficient
func (m *BTCCorrelationMonitor) calculateCorrelation(x, y []float64) float64 {
	if len(x) != len(y) || len(x) == 0 {
		return 0.0
	}

	n := float64(len(x))

	// Calculate means
	var sumX, sumY float64
	for i := 0; i < len(x); i++ {
		sumX += x[i]
		sumY += y[i]
	}
	meanX := sumX / n
	meanY := sumY / n

	// Calculate correlation
	var numerator, sumXX, sumYY float64
	for i := 0; i < len(x); i++ {
		dx := x[i] - meanX
		dy := y[i] - meanY
		numerator += dx * dy
		sumXX += dx * dx
		sumYY += dy * dy
	}

	denominator := math.Sqrt(sumXX * sumYY)
	if denominator == 0 {
		return 0.0
	}

	return numerator / denominator
}

// checkBTCDrop checks for significant BTC drops and triggers callbacks
func (m *BTCCorrelationMonitor) checkBTCDrop(ctx context.Context) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if len(m.btcPrices) < 2 {
		return
	}

	// Check for drop within the time window
	now := time.Now()
	windowStart := now.Add(-m.config.BTCDropTimeWindow)

	var startPrice, currentPrice float64
	var startFound bool

	// Find the price at the start of the window
	for i := len(m.btcPrices) - 1; i >= 0; i-- {
		if m.btcPrices[i].Timestamp.Before(windowStart) {
			if i < len(m.btcPrices)-1 {
				startPrice = m.btcPrices[i+1].Price
				startFound = true
			}
			break
		}
	}

	if !startFound && len(m.btcPrices) > 0 {
		startPrice = m.btcPrices[0].Price
		startFound = true
	}

	if !startFound {
		return
	}

	currentPrice = m.btcPrices[len(m.btcPrices)-1].Price
	dropPercent := (startPrice - currentPrice) / startPrice * 100.0

	// Check if drop exceeds threshold
	if dropPercent >= m.config.BTCDropThreshold {
		// Find affected symbols (high correlation altcoins)
		affectedSymbols := make([]string, 0)
		for symbol, correlation := range m.correlations {
			if correlation >= m.config.HighCorrelationThreshold {
				affectedSymbols = append(affectedSymbols, symbol)
			}
		}

		if len(affectedSymbols) > 0 {
			m.logger.WithFields(logrus.Fields{
				"drop_percent":     dropPercent,
				"start_price":      startPrice,
				"current_price":    currentPrice,
				"affected_symbols": affectedSymbols,
				"time_window":      m.config.BTCDropTimeWindow,
			}).Warn("Significant BTC drop detected")

			// Trigger callback
			if m.onBTCDrop != nil {
				go func() {
					if err := m.onBTCDrop(ctx, dropPercent, affectedSymbols); err != nil {
						m.logger.WithError(err).Error("BTC drop callback failed")
					}
				}()
			}
		}
	}
}

// GetDefaultBTCCorrelationConfig returns default configuration
func GetDefaultBTCCorrelationConfig() *BTCCorrelationConfig {
	return &BTCCorrelationConfig{
		CheckInterval:            30 * time.Second,
		PriceHistorySize:         200, // ~1.5 hours at 30s intervals
		CorrelationWindow:        20,  // Last 20 price points for correlation
		HighCorrelationThreshold: 0.7, // 70% correlation threshold
		BTCDropThreshold:         3.0, // 3% drop threshold
		BTCDropTimeWindow:        15 * time.Minute,
		SLAdjustmentFactor:       1.5,  // Widen SL by 50%
		MinSLAdjustment:          5.0,  // Minimum 5% adjustment
		MaxSLAdjustment:          25.0, // Maximum 25% adjustment
		AltcoinSymbols: []string{
			"ETHUSDT", "ADAUSDT", "DOTUSDT", "LINKUSDT", "LTCUSDT",
			"XRPUSDT", "BCHUSDT", "EOSUSDT", "XLMUSDT", "TRXUSDT",
		},
		BTCSymbol: "BTCUSDT",
		Exchange:  "kucoin",
	}
}
