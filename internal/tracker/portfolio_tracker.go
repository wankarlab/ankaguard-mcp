package tracker

import (
	"context"
	"math"
	"strings"
	"time"

	"github.com/rantipay/trading-mcp/internal/gct"
	"github.com/rantipay/trading-mcp/internal/storage"
	"github.com/sirupsen/logrus"
)

// PortfolioTracker saves hourly portfolio snapshots to SQLite for real performance history
type PortfolioTracker struct {
	gctClient gct.GCTClient
	repo      *storage.PortfolioSnapshotRepository
	log       *logrus.Logger
	interval  time.Duration
	stopCh    chan struct{}
	// priceUSDT returns the last USDT price of a currency (overridable in tests).
	priceUSDT func(ctx context.Context, currency string) (float64, error)
}

// NewPortfolioTracker creates a new portfolio tracker
func NewPortfolioTracker(gctClient gct.GCTClient, repo *storage.PortfolioSnapshotRepository, log *logrus.Logger) *PortfolioTracker {
	t := &PortfolioTracker{
		gctClient: gctClient,
		repo:      repo,
		log:       log,
		interval:  1 * time.Hour,
		stopCh:    make(chan struct{}),
	}
	t.priceUSDT = t.kucoinPriceUSDT
	return t
}

func (t *PortfolioTracker) kucoinPriceUSDT(ctx context.Context, currency string) (float64, error) {
	tk, err := t.gctClient.GetTicker(ctx, "kucoin", currency+"-USDT")
	if err != nil {
		return 0, err
	}
	return tk.Last, nil
}

// equityUSDT values every holding in USDT for the equity history. USDT
// balances (spot and futures) count at face value; any other currency is
// priced with its <CUR>-USDT ticker. The portfolio's own TotalValue is not
// used: the REST path leaves non-USDT spot assets out and the gRPC path sums
// raw quantities. A currency without a valid price is reported and skipped,
// never valued at its quantity.
func (t *PortfolioTracker) equityUSDT(ctx context.Context, holdings map[string]*gct.Holding) (total float64, unpriced []string) {
	for key, h := range holdings {
		if h == nil || h.Total <= 0 {
			continue
		}
		cur := strings.ToUpper(h.Currency)
		if cur == "" {
			cur = strings.ToUpper(key)
		}
		if cur == "USDT" || strings.HasPrefix(strings.ToUpper(key), "USDT") {
			total += h.Total
			continue
		}
		px, err := t.priceUSDT(ctx, cur)
		if err != nil || !(px > 0) || math.IsInf(px, 0) {
			unpriced = append(unpriced, cur)
			continue
		}
		total += h.Total * px
	}
	return total, unpriced
}

// Start begins periodic snapshot collection in the background
func (t *PortfolioTracker) Start(ctx context.Context) {
	t.saveSnapshot(ctx)

	go func() {
		ticker := time.NewTicker(t.interval)
		defer ticker.Stop()

		for {
			select {
			case <-ticker.C:
				t.saveSnapshot(ctx)
			case <-t.stopCh:
				return
			case <-ctx.Done():
				return
			}
		}
	}()

	t.log.Info("Portfolio tracker started (hourly snapshots)")
}

// Stop halts the tracker
func (t *PortfolioTracker) Stop() {
	select {
	case t.stopCh <- struct{}{}:
	default:
	}
}

func (t *PortfolioTracker) saveSnapshot(ctx context.Context) {
	snapCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	portfolio, err := t.gctClient.GetPortfolio(snapCtx)
	if err != nil {
		t.log.WithError(err).Debug("Portfolio tracker: could not fetch portfolio, skipping snapshot")
		return
	}

	assets := make(map[string]float64)
	availableBalance := 0.0

	for currency, holding := range portfolio.Holdings {
		if holding.Total > 0 {
			assets[currency] = holding.Total
		}
		if currency == "USDT" {
			availableBalance = holding.Available
		}
	}

	// Truncate to current hour for idempotent inserts
	now := time.Now().UTC()
	snapshotTime := time.Date(now.Year(), now.Month(), now.Day(), now.Hour(), 0, 0, 0, time.UTC)

	equity, unpriced := t.equityUSDT(snapCtx, portfolio.Holdings)
	if len(unpriced) > 0 {
		t.log.WithField("unpriced", unpriced).Warn("Portfolio tracker: holdings without a USDT price left out of equity")
	}

	snap := &storage.PortfolioSnapshot{
		SnapshotTime:     snapshotTime,
		TotalValueUSDT:   equity,
		AvailableBalance: availableBalance,
		UnrealizedPnL:    portfolio.TotalPnL,
		PositionsCount:   len(portfolio.Holdings),
		Assets:           assets,
	}

	if err := t.repo.Save(snap); err != nil {
		t.log.WithError(err).Warn("Portfolio tracker: failed to save snapshot")
		return
	}

	t.log.WithFields(logrus.Fields{
		"total_value": equity,
		"pnl":         portfolio.TotalPnL,
		"assets":      len(assets),
	}).Debug("Portfolio snapshot saved")
}
