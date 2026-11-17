// Package volatility implements dynamic per-pair volatility classification.
//
// Pairs are auto-classified into tiers based on their live ATR/price ratio.
// A background goroutine refreshes classifications every 15 minutes so pairs
// migrate between tiers as their market behavior changes — no hardcoded lists.
//
// Tiers:
//   - low:      ATR < 1.5%  → no protections needed (BTC, ETH usually)
//   - normal:   1.5% – 2.5% → mild protection if exceeds upper bound
//   - volatile: 2.5% – 4.5% → reduce size 30-50%, widen SL
//   - extreme:  > 4.5%      → block entries (likely news/manipulation)
//
// Consumers (e.g. AITradingBot) call Classify(pair) to get the current tier
// and adjust trade size / SL accordingly.
package volatility

import (
	"context"
	"database/sql"
	"fmt"
	"sync"
	"time"

	"github.com/sirupsen/logrus"
)

// Tier represents a volatility classification level.
type Tier string

const (
	TierLow      Tier = "low"
	TierNormal   Tier = "normal"
	TierVolatile Tier = "volatile"
	TierExtreme  Tier = "extreme"
)

// IsValid returns true if t is a known tier.
func (t Tier) IsValid() bool {
	switch t {
	case TierLow, TierNormal, TierVolatile, TierExtreme:
		return true
	}
	return false
}

// Classification holds the current volatility state for a pair.
type Classification struct {
	Pair             string
	Tier             Tier
	CurrentATRPct    float64   // latest observation
	AvgATRPct24h     float64   // smoothed 24h rolling average
	MaxATRPct24h     float64   // worst spike in last 24h
	LastSpikeATRPct  float64   // value of most recent spike
	LastSpikeAt      time.Time // when last spike was recorded
	SamplesCount     int
	TierChangedAt    time.Time
	UpdatedAt        time.Time
	ClassifiedBy     string // "auto" or "manual_override"
}

// Thresholds defines the ATR/price boundaries between tiers.
// All values are percentages (e.g. 1.5 means 1.5%).
type Thresholds struct {
	LowMax       float64 // upper bound of "low" tier
	NormalMax    float64 // upper bound of "normal" tier
	VolatileMax  float64 // upper bound of "volatile" tier (above = extreme)
}

// DefaultThresholds returns sensible defaults for a typical crypto futures market.
func DefaultThresholds() Thresholds {
	return Thresholds{
		LowMax:      1.5, // BTC, ETH typical
		NormalMax:   2.5, // most altcoins
		VolatileMax: 4.5, // memecoins, low-cap
	}
}

// TierFromATRPct returns the tier corresponding to a given ATR/price percentage.
func (t Thresholds) TierFromATRPct(atrPct float64) Tier {
	switch {
	case atrPct < t.LowMax:
		return TierLow
	case atrPct < t.NormalMax:
		return TierNormal
	case atrPct < t.VolatileMax:
		return TierVolatile
	default:
		return TierExtreme
	}
}

// ATRSampler is implemented by anything that can fetch the current ATR% for a pair.
// The AITradingBot satisfies this via its cached indicator snapshots.
type ATRSampler interface {
	SampleATRPct(pair string) (atrPct float64, ok bool)
}

// Classifier maintains the dynamic volatility classification table.
// Thread-safe; consumers can call Classify concurrently with the background loop.
type Classifier struct {
	db         *sql.DB
	log        *logrus.Logger
	sampler    ATRSampler
	thresholds Thresholds

	mu    sync.RWMutex
	cache map[string]*Classification // pair → classification (hot read cache)

	refreshInterval time.Duration
	stopCh          chan struct{}
}

// New creates a Classifier wired to the given DB and ATR sampler.
// sampler may be nil; in that case the loop only persists manually-pushed updates.
func New(db *sql.DB, log *logrus.Logger, sampler ATRSampler) *Classifier {
	if log == nil {
		log = logrus.New()
	}
	return &Classifier{
		db:              db,
		log:             log,
		sampler:         sampler,
		thresholds:      DefaultThresholds(),
		cache:           make(map[string]*Classification),
		refreshInterval: 15 * time.Minute,
		stopCh:          make(chan struct{}),
	}
}

// SetThresholds overrides the default tier boundaries (e.g. from dynamic config).
func (c *Classifier) SetThresholds(t Thresholds) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.thresholds = t
}

// SetRefreshInterval changes how often the background loop refreshes classifications.
func (c *Classifier) SetRefreshInterval(d time.Duration) {
	if d < time.Minute {
		d = time.Minute
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.refreshInterval = d
}

// LoadCache reads all classifications from DB into memory.
// Call once at startup so Classify() can return tier without hitting DB on hot path.
func (c *Classifier) LoadCache(ctx context.Context) error {
	if c.db == nil {
		return nil
	}
	rows, err := c.db.QueryContext(ctx, `
		SELECT pair, tier, current_atr_pct, avg_atr_pct_24h, max_atr_pct_24h,
		       last_spike_atr_pct, last_spike_at, samples_count,
		       tier_changed_at, updated_at, classified_by
		FROM pair_volatility_classification
	`)
	if err != nil {
		return fmt.Errorf("load volatility cache: %w", err)
	}
	defer rows.Close()

	c.mu.Lock()
	defer c.mu.Unlock()
	c.cache = make(map[string]*Classification)
	for rows.Next() {
		var cl Classification
		var lastSpikeAt sql.NullTime
		if err := rows.Scan(
			&cl.Pair, &cl.Tier, &cl.CurrentATRPct, &cl.AvgATRPct24h, &cl.MaxATRPct24h,
			&cl.LastSpikeATRPct, &lastSpikeAt, &cl.SamplesCount,
			&cl.TierChangedAt, &cl.UpdatedAt, &cl.ClassifiedBy,
		); err != nil {
			c.log.WithError(err).Warn("volatility: skip row scan error")
			continue
		}
		if lastSpikeAt.Valid {
			cl.LastSpikeAt = lastSpikeAt.Time
		}
		c.cache[cl.Pair] = &cl
	}
	c.log.WithField("pairs", len(c.cache)).Info("✅ Volatility classifier cache loaded")
	return rows.Err()
}

// Classify returns the current tier for a pair.
// Returns TierNormal as a safe default if the pair has not been classified yet
// (e.g. brand new pair the classifier hasn't observed). Callers should treat the
// "not in cache" case as "unknown — use base sizing" because the classifier will
// pick it up on the next refresh cycle.
func (c *Classifier) Classify(pair string) Tier {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if cl, ok := c.cache[pair]; ok && cl.Tier.IsValid() {
		return cl.Tier
	}
	return TierNormal
}

// Get returns the full classification record (or false if not present).
func (c *Classifier) Get(pair string) (*Classification, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if cl, ok := c.cache[pair]; ok {
		copy := *cl
		return &copy, true
	}
	return nil, false
}

// GetAll returns a snapshot of all classifications.
func (c *Classifier) GetAll() []*Classification {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := make([]*Classification, 0, len(c.cache))
	for _, cl := range c.cache {
		copy := *cl
		out = append(out, &copy)
	}
	return out
}

// RecordATR persists a single ATR observation for a pair and may transition
// the tier if the observation breaches a threshold. Safe to call frequently;
// the in-memory cache is updated synchronously, DB write is best-effort.
func (c *Classifier) RecordATR(ctx context.Context, pair string, atrPct float64) error {
	if pair == "" || atrPct < 0 {
		return nil
	}

	c.mu.Lock()
	cl, ok := c.cache[pair]
	if !ok {
		cl = &Classification{
			Pair:          pair,
			Tier:          TierNormal,
			ClassifiedBy:  "auto",
			TierChangedAt: time.Now(),
		}
		c.cache[pair] = cl
	}

	prevTier := cl.Tier
	newTier := c.thresholds.TierFromATRPct(atrPct)

	cl.CurrentATRPct = atrPct
	cl.SamplesCount++

	// EWMA smoothing for 24h average (alpha = 1/24 hourly samples ≈ 0.042 per 15min).
	alpha := 0.05
	if cl.AvgATRPct24h == 0 {
		cl.AvgATRPct24h = atrPct
	} else {
		cl.AvgATRPct24h = alpha*atrPct + (1-alpha)*cl.AvgATRPct24h
	}
	if atrPct > cl.MaxATRPct24h || time.Since(cl.UpdatedAt) > 24*time.Hour {
		cl.MaxATRPct24h = atrPct
	}

	// Track spikes (any value > volatile threshold)
	if atrPct >= c.thresholds.VolatileMax {
		cl.LastSpikeATRPct = atrPct
		cl.LastSpikeAt = time.Now()
	}

	if newTier != prevTier {
		cl.Tier = newTier
		cl.TierChangedAt = time.Now()
		c.log.WithFields(logrus.Fields{
			"pair":     pair,
			"old_tier": string(prevTier),
			"new_tier": string(newTier),
			"atr_pct":  fmt.Sprintf("%.2f%%", atrPct),
		}).Info("📊 Volatility tier transition")
	}
	cl.UpdatedAt = time.Now()
	c.mu.Unlock()

	if c.db == nil {
		return nil
	}

	_, err := c.db.ExecContext(ctx, `
		INSERT INTO pair_volatility_classification
		    (pair, tier, current_atr_pct, avg_atr_pct_24h, max_atr_pct_24h,
		     last_spike_atr_pct, last_spike_at, samples_count,
		     tier_changed_at, updated_at, classified_by)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(pair) DO UPDATE SET
		    tier               = excluded.tier,
		    current_atr_pct    = excluded.current_atr_pct,
		    avg_atr_pct_24h    = excluded.avg_atr_pct_24h,
		    max_atr_pct_24h    = MAX(max_atr_pct_24h, excluded.max_atr_pct_24h),
		    last_spike_atr_pct = excluded.last_spike_atr_pct,
		    last_spike_at      = excluded.last_spike_at,
		    samples_count      = excluded.samples_count,
		    tier_changed_at    = excluded.tier_changed_at,
		    updated_at         = excluded.updated_at
	`, pair, string(newTier), atrPct, cl.AvgATRPct24h, cl.MaxATRPct24h,
		cl.LastSpikeATRPct, nullTime(cl.LastSpikeAt), cl.SamplesCount,
		cl.TierChangedAt, cl.UpdatedAt, cl.ClassifiedBy)
	if err != nil {
		c.log.WithError(err).WithField("pair", pair).Warn("volatility: DB upsert failed")
		return err
	}
	return nil
}

// SetManualOverride pins a pair to a specific tier regardless of observed ATR.
// Useful for blacklisting known-bad pairs or whitelisting trusted ones.
func (c *Classifier) SetManualOverride(ctx context.Context, pair string, tier Tier) error {
	if !tier.IsValid() {
		return fmt.Errorf("invalid tier: %s", tier)
	}
	c.mu.Lock()
	cl, ok := c.cache[pair]
	if !ok {
		cl = &Classification{Pair: pair, TierChangedAt: time.Now()}
		c.cache[pair] = cl
	}
	cl.Tier = tier
	cl.ClassifiedBy = "manual_override"
	cl.UpdatedAt = time.Now()
	c.mu.Unlock()

	if c.db == nil {
		return nil
	}
	_, err := c.db.ExecContext(ctx, `
		INSERT INTO pair_volatility_classification (pair, tier, classified_by, updated_at)
		VALUES (?, ?, 'manual_override', CURRENT_TIMESTAMP)
		ON CONFLICT(pair) DO UPDATE SET
		    tier = excluded.tier, classified_by = 'manual_override', updated_at = CURRENT_TIMESTAMP
	`, pair, string(tier))
	return err
}

// Start launches the background refresh loop. It does nothing if no ATR sampler
// was wired; in that case the classifier still serves RecordATR calls.
func (c *Classifier) Start(ctx context.Context) {
	if c.sampler == nil {
		c.log.Warn("volatility: classifier started without ATR sampler — manual updates only")
		return
	}
	go c.loop(ctx)
}

// Stop signals the background loop to exit.
func (c *Classifier) Stop() {
	select {
	case <-c.stopCh:
	default:
		close(c.stopCh)
	}
}

func (c *Classifier) loop(ctx context.Context) {
	c.mu.RLock()
	interval := c.refreshInterval
	c.mu.RUnlock()

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	c.log.WithField("interval", interval).Info("✅ Volatility classifier loop started")

	// Initial pass on startup so the cache is populated quickly.
	c.refresh(ctx)

	for {
		select {
		case <-ctx.Done():
			return
		case <-c.stopCh:
			return
		case <-ticker.C:
			c.refresh(ctx)
		}
	}
}

func (c *Classifier) refresh(ctx context.Context) {
	if c.sampler == nil {
		return
	}

	// Snapshot all pairs currently in the cache; they're the active universe.
	c.mu.RLock()
	pairs := make([]string, 0, len(c.cache))
	for p := range c.cache {
		pairs = append(pairs, p)
	}
	c.mu.RUnlock()

	for _, pair := range pairs {
		atrPct, ok := c.sampler.SampleATRPct(pair)
		if !ok || atrPct <= 0 {
			continue
		}
		if err := c.RecordATR(ctx, pair, atrPct); err != nil {
			c.log.WithError(err).WithField("pair", pair).Debug("volatility: record failed")
		}
	}
}

// TrackPair ensures a pair has an entry in the classifier (creates one with
// TierNormal if missing). Bots should call this when they start trading a new
// pair so the refresh loop picks it up.
func (c *Classifier) TrackPair(ctx context.Context, pair string) {
	c.mu.Lock()
	if _, exists := c.cache[pair]; !exists {
		c.cache[pair] = &Classification{
			Pair:          pair,
			Tier:          TierNormal,
			ClassifiedBy:  "auto",
			TierChangedAt: time.Now(),
			UpdatedAt:     time.Now(),
		}
	}
	c.mu.Unlock()
}

func nullTime(t time.Time) interface{} {
	if t.IsZero() {
		return nil
	}
	return t
}
