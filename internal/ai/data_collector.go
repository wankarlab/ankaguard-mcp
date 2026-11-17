package ai

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/sirupsen/logrus"
)

// GCTClient interface for data collection
type GCTClient interface {
	GetCandles(ctx context.Context, exchange, pair, interval string, limit int) ([]interface{}, error)
	GetTicker(ctx context.Context, exchange, pair string) (interface{}, error)
}

// DataCollector collects multi-timeframe data
type DataCollector struct {
	client   GCTClient
	cache    *TimeframeCache
	exchange string
	log      *logrus.Logger
}

// NewDataCollector creates a new data collector
func NewDataCollector(client GCTClient, exchange string, log *logrus.Logger) *DataCollector {
	return &DataCollector{
		client:   client,
		cache:    NewTimeframeCache(),
		exchange: exchange,
		log:      log,
	}
}

// CollectData collects data from multiple timeframes
// REFACTORED: Now accepts optional style parameter for dynamic timeframe collection
// If style is nil, uses default scalping style for backward compatibility
func (dc *DataCollector) CollectData(ctx context.Context, pair string, style ...*Strategy) (*MultiTimeframeData, error) {
	var wg sync.WaitGroup
	var mu sync.Mutex
	errors := make(map[string]error)

	mtfData := &MultiTimeframeData{
		Pair:       pair,
		Timeframes: make(map[string]*TimeframeData),
		Timestamp:  time.Now(),
	}

	// Use provided style or default
	if len(style) > 0 && style[0] != nil {
		mtfData.Style = style[0]
	} else {
		mtfData.EnsureStyleSet() // Use default style for backward compatibility
	}

	// Get all timeframes to collect from style
	allTimeframes := mtfData.Style.GetTimeframeIntervals()
	requiredTimeframes := mtfData.Style.RequiredTimeframes

	dc.log.Infof("Collecting %d timeframes for %s: %v", len(allTimeframes), pair, allTimeframes)

	// Collect all timeframes in parallel
	for _, tf := range allTimeframes {
		wg.Add(1)
		go func(timeframe string) {
			defer wg.Done()

			// Get candle limit for this timeframe
			limit := mtfData.Style.GetCandleLimit(timeframe)

			data, err := dc.fetchTimeframeData(ctx, pair, timeframe, limit)
			if err != nil {
				mu.Lock()
				errors[timeframe] = err
				mu.Unlock()

				// Check if this is a required timeframe
				isRequired := mtfData.Style.IsTimeframeRequired(timeframe)
				if !isRequired {
					dc.log.Warnf("Failed to fetch %s data (optional): %v", timeframe, err)
				} else {
					dc.log.Errorf("Failed to fetch %s data (required): %v", timeframe, err)
				}
				return
			}

			mu.Lock()
			mtfData.Timeframes[timeframe] = data
			mu.Unlock()

			dc.log.Debugf("Collected %s data: %d candles", timeframe, len(data.Candles))
		}(tf)
	}

	wg.Wait()

	// Check if required timeframes are available
	for _, tf := range requiredTimeframes {
		if _, ok := mtfData.Timeframes[tf]; !ok {
			if err, hasErr := errors[tf]; hasErr {
				return nil, fmt.Errorf("required timeframe %s failed: %w", tf, err)
			}
			return nil, fmt.Errorf("required timeframe %s not collected", tf)
		}
	}

	dc.log.Infof("Successfully collected %d/%d timeframes for %s", len(mtfData.Timeframes), len(allTimeframes), pair)

	return mtfData, nil
}

// fetchTimeframeData fetches data for a specific timeframe
func (dc *DataCollector) fetchTimeframeData(ctx context.Context, pair, timeframe string, limit int) (*TimeframeData, error) {
	// Check cache first
	cacheKey := fmt.Sprintf("%s_%s_%s", dc.exchange, pair, timeframe)
	if cached, ok := dc.cache.Get(cacheKey); ok {
		dc.log.Debugf("Using cached data for %s", cacheKey)
		return cached, nil
	}

	// Fetch from GCT
	dc.log.Infof("Fetching %s data for %s (limit: %d)", timeframe, pair, limit)
	rawCandles, err := dc.client.GetCandles(ctx, dc.exchange, pair, timeframe, limit)
	if err != nil {
		return nil, fmt.Errorf("failed to get candles: %w", err)
	}

	dc.log.Infof("Received %d candles for %s %s", len(rawCandles), timeframe, pair)

	// Convert to Candle structs
	candles := make([]Candle, 0, len(rawCandles))
	for _, raw := range rawCandles {
		// Type assertion - handle both map and struct types
		var candle Candle

		switch v := raw.(type) {
		case map[string]interface{}:
			candle = Candle{
				Timestamp: time.Now(), // TODO: parse from response
				Open:      getFloat(v, "open"),
				High:      getFloat(v, "high"),
				Low:       getFloat(v, "low"),
				Close:     getFloat(v, "close"),
				Volume:    getFloat(v, "volume"),
			}
		default:
			// Try to extract fields using reflection
			candle = extractCandleFromStruct(raw)
		}

		candles = append(candles, candle)
	}

	dc.log.Infof("Converted %d candles for %s %s", len(candles), timeframe, pair)

	// Defensive guard: ensure ascending (oldest→newest). GetCandles now guarantees
	// this contract at the source, so this is a cheap no-op insurance for the
	// indicator/decision path (which reads Candles[len-1] as the CURRENT candle).
	// A newest-first slice once made the engine analyse a ~lookback-hour STALE
	// candle as "now" on REST-only pairs (HYPE, 2026-06-01). No-op when already
	// ascending, so it can never double-reverse a correctly-ordered source.
	if len(candles) >= 2 && candles[0].Timestamp.After(candles[len(candles)-1].Timestamp) {
		for i, j := 0, len(candles)-1; i < j; i, j = i+1, j-1 {
			candles[i], candles[j] = candles[j], candles[i]
		}
	}

	data := &TimeframeData{
		Timeframe: timeframe,
		Candles:   candles,
		UpdatedAt: time.Now(),
	}

	// Cache for appropriate duration
	ttl := dc.getTTL(timeframe)
	dc.cache.Set(cacheKey, data, ttl)

	return data, nil
}

// getTTL returns appropriate TTL for timeframe
// REFACTORED: Now supports all timeframes dynamically
func (dc *DataCollector) getTTL(timeframe string) time.Duration {
	switch timeframe {
	case "1m":
		return 30 * time.Second
	case "5m":
		return 2 * time.Minute
	case "15m":
		return 5 * time.Minute
	case "30m":
		return 8 * time.Minute
	case "1h":
		return 15 * time.Minute
	case "2h":
		return 30 * time.Minute
	case "4h":
		return 1 * time.Hour
	case "8h":
		return 2 * time.Hour
	case "12h":
		return 3 * time.Hour
	case "1d":
		return 4 * time.Hour
	case "1w":
		return 12 * time.Hour
	case "1M":
		return 24 * time.Hour
	default:
		// Default: 1 minute for unknown timeframes
		return 1 * time.Minute
	}
}

// getFloat safely extracts float from map
func getFloat(m map[string]interface{}, key string) float64 {
	if val, ok := m[key]; ok {
		switch v := val.(type) {
		case float64:
			return v
		case float32:
			return float64(v)
		case int:
			return float64(v)
		case int64:
			return float64(v)
		}
	}
	return 0
}

// ClearCache clears the data cache
func (dc *DataCollector) ClearCache() {
	dc.cache.Clear()
}
