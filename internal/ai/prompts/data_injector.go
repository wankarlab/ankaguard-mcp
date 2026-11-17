package prompts

import (
	"fmt"
	"sync"
	"time"
)

// MarketDataInjector injects market data into prompts
type MarketDataInjector struct {
	cache      map[string]*cachedData
	cacheMutex sync.RWMutex
	cacheTTL   time.Duration
}

type cachedData struct {
	data      map[string]interface{}
	timestamp time.Time
}

// NewMarketDataInjector creates a new market data injector
func NewMarketDataInjector(cacheTTL time.Duration) *MarketDataInjector {
	if cacheTTL == 0 {
		cacheTTL = 30 * time.Second // Default 30s cache
	}
	return &MarketDataInjector{
		cache:    make(map[string]*cachedData),
		cacheTTL: cacheTTL,
	}
}

// InjectData converts market data to variables for prompt substitution
func (m *MarketDataInjector) InjectData(data *MarketData) (map[string]interface{}, error) {
	if data == nil {
		return nil, fmt.Errorf("market data is nil")
	}

	// Check cache first
	cacheKey := fmt.Sprintf("%s_%d", data.Pair, data.Timestamp.Unix())
	if cached := m.getFromCache(cacheKey); cached != nil {
		return cached, nil
	}

	variables := make(map[string]interface{})

	// Basic market data
	variables["Pair"] = data.Pair
	variables["Price"] = fmt.Sprintf("%.2f", data.Price)
	variables["Timestamp"] = data.Timestamp.Format("2006-01-02 15:04:05")

	// Order Flow
	variables["CVD1m"] = fmt.Sprintf("%.2f", data.CVD1m)
	variables["CVD5m"] = fmt.Sprintf("%.2f", data.CVD5m)
	variables["DeltaImbalance"] = fmt.Sprintf("%.2f%%", data.DeltaImbalance*100)
	variables["DeltaBias"] = getDeltaBias(data.DeltaImbalance)

	// Order Book
	variables["BidVolume10"] = fmt.Sprintf("%.2f", data.BidVolume10)
	variables["AskVolume10"] = fmt.Sprintf("%.2f", data.AskVolume10)
	variables["BookImbalance"] = fmt.Sprintf("%.2f%%", data.BookImbalance*100)
	variables["BookBias"] = getBookBias(data.BookImbalance)

	// Derivatives
	variables["FundingRate"] = fmt.Sprintf("%.4f%%", data.FundingRate*100)
	variables["FundingBias"] = getFundingBias(data.FundingRate)
	variables["OpenInterest"] = fmt.Sprintf("%.2f", data.OpenInterest)
	variables["OIChange1h"] = fmt.Sprintf("%.2f%%", data.OIChange1h*100)
	variables["OITrend"] = getOITrend(data.OIChange1h)
	variables["Liquidations1h"] = fmt.Sprintf("%.2f", data.Liquidations1h)

	// Technical Indicators
	variables["RSI5m"] = fmt.Sprintf("%.2f", data.RSI5m)
	variables["RSICondition"] = getRSICondition(data.RSI5m)
	variables["VWAP"] = fmt.Sprintf("%.2f", data.VWAP)
	variables["VWAPDeviation"] = fmt.Sprintf("%.2f%%", data.VWAPDeviation*100)
	variables["VWAPPosition"] = getVWAPPosition(data.VWAPDeviation)
	variables["ATR"] = fmt.Sprintf("%.2f", data.ATR)
	variables["Volatility"] = getVolatilityLevel(data.ATR, data.Price)

	// Market Context
	variables["MarketContext"] = buildMarketContext(data)
	variables["OrderFlowSummary"] = buildOrderFlowSummary(data)
	variables["DerivativesSummary"] = buildDerivativesSummary(data)

	// Additional data
	for k, v := range data.Additional {
		variables[k] = v
	}

	// Cache the result
	m.putInCache(cacheKey, variables)

	return variables, nil
}

// getFromCache retrieves data from cache if not expired
func (m *MarketDataInjector) getFromCache(key string) map[string]interface{} {
	m.cacheMutex.RLock()
	defer m.cacheMutex.RUnlock()

	cached, exists := m.cache[key]
	if !exists {
		return nil
	}

	if time.Since(cached.timestamp) > m.cacheTTL {
		return nil
	}

	return cached.data
}

// putInCache stores data in cache
func (m *MarketDataInjector) putInCache(key string, data map[string]interface{}) {
	m.cacheMutex.Lock()
	defer m.cacheMutex.Unlock()

	m.cache[key] = &cachedData{
		data:      data,
		timestamp: time.Now(),
	}

	// Clean old cache entries
	m.cleanCache()
}

// cleanCache removes expired entries
func (m *MarketDataInjector) cleanCache() {
	now := time.Now()
	for key, cached := range m.cache {
		if now.Sub(cached.timestamp) > m.cacheTTL*2 {
			delete(m.cache, key)
		}
	}
}

// Helper functions for market interpretation

func getDeltaBias(imbalance float64) string {
	if imbalance > 0.3 {
		return "STRONG BULLISH"
	} else if imbalance > 0.1 {
		return "BULLISH"
	} else if imbalance < -0.3 {
		return "STRONG BEARISH"
	} else if imbalance < -0.1 {
		return "BEARISH"
	}
	return "NEUTRAL"
}

func getBookBias(imbalance float64) string {
	if imbalance > 0.2 {
		return "BID HEAVY"
	} else if imbalance < -0.2 {
		return "ASK HEAVY"
	}
	return "BALANCED"
}

func getFundingBias(rate float64) string {
	if rate > 0.03 {
		return "EXTREMELY LONG HEAVY (squeeze risk)"
	} else if rate > 0.01 {
		return "LONG HEAVY"
	} else if rate < -0.02 {
		return "SHORT HEAVY (squeeze risk)"
	} else if rate < -0.005 {
		return "SHORT BIASED"
	}
	return "NEUTRAL"
}

func getOITrend(change float64) string {
	if change > 5 {
		return "RAPIDLY INCREASING"
	} else if change > 2 {
		return "INCREASING"
	} else if change < -5 {
		return "RAPIDLY DECREASING"
	} else if change < -2 {
		return "DECREASING"
	}
	return "STABLE"
}

func getRSICondition(rsi float64) string {
	if rsi > 70 {
		return "OVERBOUGHT"
	} else if rsi > 60 {
		return "BULLISH"
	} else if rsi < 30 {
		return "OVERSOLD"
	} else if rsi < 40 {
		return "BEARISH"
	}
	return "NEUTRAL"
}

func getVWAPPosition(deviation float64) string {
	if deviation > 1 {
		return "FAR ABOVE VWAP"
	} else if deviation > 0.3 {
		return "ABOVE VWAP"
	} else if deviation < -1 {
		return "FAR BELOW VWAP"
	} else if deviation < -0.3 {
		return "BELOW VWAP"
	}
	return "AT VWAP"
}

func getVolatilityLevel(atr, price float64) string {
	atrPercent := (atr / price) * 100
	if atrPercent > 3 {
		return "EXTREME"
	} else if atrPercent > 2 {
		return "HIGH"
	} else if atrPercent > 1 {
		return "ELEVATED"
	} else if atrPercent < 0.5 {
		return "LOW"
	}
	return "NORMAL"
}

func buildMarketContext(data *MarketData) string {
	return fmt.Sprintf(
		"Price: $%.2f | RSI: %.1f (%s) | VWAP: $%.2f (%s) | Volatility: %s",
		data.Price,
		data.RSI5m,
		getRSICondition(data.RSI5m),
		data.VWAP,
		getVWAPPosition(data.VWAPDeviation),
		getVolatilityLevel(data.ATR, data.Price),
	)
}

func buildOrderFlowSummary(data *MarketData) string {
	return fmt.Sprintf(
		"CVD 1m: %.2f | CVD 5m: %.2f | Delta: %s | Book: %s",
		data.CVD1m,
		data.CVD5m,
		getDeltaBias(data.DeltaImbalance),
		getBookBias(data.BookImbalance),
	)
}

func buildDerivativesSummary(data *MarketData) string {
	return fmt.Sprintf(
		"Funding: %.4f%% (%s) | OI: %.2f (%s) | Liq 1h: $%.2f",
		data.FundingRate*100,
		getFundingBias(data.FundingRate),
		data.OpenInterest,
		getOITrend(data.OIChange1h),
		data.Liquidations1h,
	)
}

// ClearCache clears all cached data
func (m *MarketDataInjector) ClearCache() {
	m.cacheMutex.Lock()
	defer m.cacheMutex.Unlock()
	m.cache = make(map[string]*cachedData)
}
