package health

import (
	"context"
	"fmt"
	"time"

	"github.com/sirupsen/logrus"
)

// GCTTickerClient is the minimal interface required for ticker-level health checks.
// gctclient.Client satisfies this interface; use NewGCTTickerClientWrapper to adapt it.
type GCTTickerClient interface {
	IsConnected() bool
	// GetTickerLast returns the last-traded price for the given exchange/pair.
	// Returns an error when the exchange is unavailable or the price is stale.
	GetTickerLast(ctx context.Context, exchange, pair string) (float64, error)
}

// GCTTickerChecker verifies GoCryptoTrader ticker availability.
// It retries on transient failures, supports periodic background checks,
// and can validate a specific exchange/pair combination.
type GCTTickerChecker struct {
	gctClient GCTTickerClient
	exchange  string
	pair      string
	logger    *logrus.Logger
	timeout   time.Duration
}

// NewGCTTickerChecker creates a new ticker-level health checker.
// exchange and pair identify the market used for health probes (e.g. "kucoin", "BTC-USDT").
func NewGCTTickerChecker(client GCTTickerClient, exchange string, logger *logrus.Logger) *GCTTickerChecker {
	return &GCTTickerChecker{
		gctClient: client,
		exchange:  exchange,
		pair:      "BTC-USDT",
		logger:    logger,
		timeout:   60 * time.Second,
	}
}

// CheckGCTTickers verifies that GCT has live tickers using the configured exchange/pair.
func (c *GCTTickerChecker) CheckGCTTickers(ctx context.Context) error {
	return c.checkTickers(ctx, c.exchange, c.pair)
}

// CheckGCTTickersForPair verifies tickers for an arbitrary exchange/pair.
func (c *GCTTickerChecker) CheckGCTTickersForPair(exchange, pair string) error {
	ctx, cancel := context.WithTimeout(context.Background(), c.timeout)
	defer cancel()
	return c.checkTickers(ctx, exchange, pair)
}

// checkTickers is the shared retry loop used by both public methods.
func (c *GCTTickerChecker) checkTickers(ctx context.Context, exchange, pair string) error {
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, c.timeout)
		defer cancel()
	}

	if !c.gctClient.IsConnected() {
		return fmt.Errorf("GCT client is not connected")
	}

	const maxRetries = 3
	retryDelay := 5 * time.Second

	for attempt := 1; attempt <= maxRetries; attempt++ {
		last, err := c.gctClient.GetTickerLast(ctx, exchange, pair)
		if err == nil && last > 0 {
			c.logger.WithFields(logrus.Fields{
				"exchange": exchange,
				"pair":     pair,
				"price":    last,
				"attempt":  attempt,
			}).Info("GCT health check passed - tickers available")
			return nil
		}

		if attempt < maxRetries {
			c.logger.WithFields(logrus.Fields{
				"exchange": exchange,
				"pair":     pair,
				"attempt":  attempt,
				"error":    err,
			}).Warn("GCT health check failed, retrying...")

			select {
			case <-ctx.Done():
				return fmt.Errorf("health check timeout after %d attempts", attempt)
			case <-time.After(retryDelay):
				// continue to next attempt
			}
		}
	}

	return fmt.Errorf("GCT does not have tickers for %s:%s after %d attempts", exchange, pair, maxRetries)
}

// StartPeriodicCheck runs a background ticker-health loop until ctx is cancelled.
// Call this in a goroutine; it logs failures but never returns an error.
func (c *GCTTickerChecker) StartPeriodicCheck(ctx context.Context) {
	const interval = 30 * time.Second
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	c.logger.WithFields(logrus.Fields{
		"exchange": c.exchange,
		"pair":     c.pair,
		"interval": interval,
	}).Info("Starting periodic GCT health check")

	for {
		select {
		case <-ctx.Done():
			c.logger.Info("Stopping periodic GCT health check")
			return
		case <-ticker.C:
			checkCtx, cancel := context.WithTimeout(context.Background(), c.timeout)
			if err := c.CheckGCTTickers(checkCtx); err != nil {
				c.logger.WithFields(logrus.Fields{
					"exchange": c.exchange,
					"pair":     c.pair,
					"error":    err,
				}).Warn("Periodic GCT health check failed")
			}
			cancel()
		}
	}
}

// TickerHealthStatus reports the current ticker-level health state.
type TickerHealthStatus struct {
	Healthy   bool      `json:"healthy"`
	Exchange  string    `json:"exchange"`
	Pair      string    `json:"pair"`
	LastCheck time.Time `json:"last_check"`
	Error     string    `json:"error,omitempty"`
}

// GetHealthStatus returns a point-in-time ticker health snapshot.
func (c *GCTTickerChecker) GetHealthStatus() *TickerHealthStatus {
	status := &TickerHealthStatus{
		Exchange:  c.exchange,
		Pair:      c.pair,
		LastCheck: time.Now(),
	}

	ctx, cancel := context.WithTimeout(context.Background(), c.timeout)
	defer cancel()

	if err := c.CheckGCTTickers(ctx); err != nil {
		status.Healthy = false
		status.Error = err.Error()
	} else {
		status.Healthy = true
	}

	return status
}
