package backtest

import (
	"context"
	"fmt"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/rantipay/trading-mcp/internal/ai"
	"github.com/rantipay/trading-mcp/internal/backtest/domain"
	"github.com/rantipay/trading-mcp/internal/gct"
	"github.com/rantipay/trading-mcp/internal/killswitch"
	"github.com/rantipay/trading-mcp/internal/storage"
)

// RangeBackfiller pulls historical candles for a window (implemented by
// gct.KuCoinRESTClient.BackfillFuturesCandles).
type RangeBackfiller interface {
	BackfillFuturesCandles(ctx context.Context, symbol, interval string, from, to time.Time) ([]*gct.Candle, error)
}

// ValidationService keeps strategy verdicts fresh: it backfills recent candles
// for the pairs the bot cares about, runs walk-forward validation, and persists
// the verdict that the live gate consults. This is the engine of the
// "only trade validated edges" discipline.
type ValidationService struct {
	validator    *Validator
	archive      *storage.CandleArchiveRepository
	store        *storage.ValidationRepository
	backfiller   RangeBackfiller
	exchange     string
	cfg          ValidationConfig
	ttl          time.Duration
	backfillDays int
	// cfgFn, when set, supplies the live ValidationConfig and backfillDays from
	// dynamic config on every cycle (hot-reload). Nil → use the static cfg/backfillDays
	// passed to the constructor (legacy behavior preserved).
	cfgFn func() (ValidationConfig, int)
	log   *logrus.Logger
}

// SetConfigProvider wires a dynamic provider so validation thresholds
// (PF, consistency, windows, trades, drawdown) and backfillDays are read live
// each cycle instead of frozen at construction. Additive: with no provider set,
// the service behaves exactly as before. The provider's Costs field, if zero,
// is filled from the static cfg so cost modeling is never accidentally dropped.
func (s *ValidationService) SetConfigProvider(fn func() (ValidationConfig, int)) {
	s.cfgFn = fn
}

// effectiveConfig returns the config in force right now: the dynamic provider's
// values if present, else the static fallback captured at construction.
func (s *ValidationService) effectiveConfig() (ValidationConfig, int) {
	if s.cfgFn == nil {
		return s.cfg, s.backfillDays
	}
	cfg, days := s.cfgFn()
	if (cfg.Costs == domain.CostModel{}) {
		cfg.Costs = s.cfg.Costs
	}
	if days <= 0 {
		days = s.backfillDays
	}
	return cfg, days
}

// NewValidationService constructs a ValidationService.
func NewValidationService(
	validator *Validator,
	archive *storage.CandleArchiveRepository,
	store *storage.ValidationRepository,
	backfiller RangeBackfiller,
	exchange string,
	cfg ValidationConfig,
	ttl time.Duration,
	backfillDays int,
	log *logrus.Logger,
) *ValidationService {
	if backfillDays <= 0 {
		backfillDays = 21
	}
	return &ValidationService{
		validator: validator, archive: archive, store: store, backfiller: backfiller,
		exchange: exchange, cfg: cfg, ttl: ttl, backfillDays: backfillDays, log: log,
	}
}

// ValidatePair backfills fresh candles for the pair, runs validation, and stores
// the verdict. Returns the verdict for inspection. A panic in the backtest path
// (e.g. incomplete market context) is contained here so it can NEVER crash the
// live trading server — validation is best-effort background work.
func (s *ValidationService) ValidatePair(ctx context.Context, pair string) (verdict *ValidationVerdict, err error) {
	defer func() {
		if r := recover(); r != nil {
			s.log.WithField("pair", pair).WithField("panic", r).Error("🛡️ validation panic recovered — server protected")
			verdict, err = nil, fmt.Errorf("validation panicked for %s: %v", pair, r)
		}
	}()
	if kerr := killswitch.Check(); kerr != nil {
		return nil, kerr
	}
	s.refreshCandles(ctx, pair)

	cfg, _ := s.effectiveConfig()
	verdict, err = s.validator.Validate(ctx, pair, s.exchange, "", nil, cfg)
	if err != nil {
		return nil, err
	}

	if serr := s.store.Save(storage.ValidationRecord{
		Pair: pair, Exchange: s.exchange, StrategyHash: "live",
		Pass: verdict.Pass, Reason: verdict.Reason,
		ProfitFactor: verdict.AggProfitFactor, Consistency: verdict.Consistency,
		NTrades: verdict.NTradesTotal, WorstDrawdown: verdict.WorstDrawdown,
	}, s.ttl); serr != nil {
		s.log.WithError(serr).WithField("pair", pair).Warn("validation: failed to persist verdict")
	}
	return verdict, nil
}

// ValidateSpec runs OOS validation for a specific StrategySpec and writes the
// STATUS-ONLY verdict keyed by spec.ContentHash() (EV-5/EV-6 integration).
// The StrategySpec must have passed Validate() before calling this.
// This is the new Spec-aware validation path; ValidatePair uses the legacy 'live' key.
func (s *ValidationService) ValidateSpec(ctx context.Context, spec specIdentifier, pair string) (verdict *ValidationVerdict, err error) {
	defer func() {
		if r := recover(); r != nil {
			verdict, err = nil, fmt.Errorf("validation panicked for spec %s pair %s: %v", spec.SpecID(), pair, r)
		}
	}()
	if kerr := killswitch.Check(); kerr != nil {
		return nil, kerr
	}
	s.refreshCandles(ctx, pair)

	// Apply spec-level overrides to the validation config (e.g. oos_window_days).
	cfg, _ := s.effectiveConfig()
	if w := spec.OOSWindowDays(); w > 0 {
		cfg.Windows = w / 7 // approximate: 7 days per window
		if cfg.Windows < 2 {
			cfg.Windows = 2
		}
	}

	verdict, err = s.validator.Validate(ctx, pair, s.exchange, "", spec.ToOverrides(), cfg)
	if err != nil {
		return nil, err
	}

	specID := spec.SpecID()
	stratHash := specID // EV-5: use spec_id as strategy_hash, not 'live'
	if stratHash == "" {
		stratHash = "live"
	}

	if serr := s.store.Save(storage.ValidationRecord{
		Pair: pair, Exchange: s.exchange, StrategyHash: stratHash,
		Pass: verdict.Pass, Reason: verdict.Reason,
		ProfitFactor: verdict.AggProfitFactor, Consistency: verdict.Consistency,
		NTrades: verdict.NTradesTotal, WorstDrawdown: verdict.WorstDrawdown,
		SpecID: specID,
	}, s.ttl); serr != nil {
		s.log.WithError(serr).WithField("spec_id", specID).Warn("validation: failed to persist spec verdict")
	}
	return verdict, nil
}

// specIdentifier is the minimal interface a StrategySpec provides for validation.
type specIdentifier interface {
	SpecID() string
	OOSWindowDays() int
	ToOverrides() map[string]string
}

// refreshCandles backfills the last N days of 1m/5m/15m candles into the archive.
func (s *ValidationService) refreshCandles(ctx context.Context, pair string) {
	_, backfillDays := s.effectiveConfig()
	to := time.Now()
	from := to.Add(-time.Duration(backfillDays) * 24 * time.Hour)
	for _, tf := range []string{"5m", "15m", "1m"} {
		raw, err := s.backfiller.BackfillFuturesCandles(ctx, pair, tf, from, to)
		if err != nil && len(raw) == 0 {
			s.log.WithError(err).WithField("pair", pair).WithField("tf", tf).Debug("validation: backfill failed")
			continue
		}
		candles := make([]ai.Candle, 0, len(raw))
		for _, c := range raw {
			candles = append(candles, ai.Candle{
				Timestamp: c.Timestamp, Open: c.Open, High: c.High, Low: c.Low, Close: c.Close, Volume: c.Volume,
			})
		}
		if _, serr := s.archive.Save(pair, tf, candles); serr != nil {
			s.log.WithError(serr).Debug("validation: archive save failed")
		}
	}
}

// Loop periodically re-validates the pairs returned by pairsFn. Verdicts expire
// (s.ttl), so a pair whose edge decays is automatically de-certified until it
// validates again. Runs until ctx is cancelled.
func (s *ValidationService) Loop(ctx context.Context, interval time.Duration, pairsFn func() []string) {
	defer func() {
		if r := recover(); r != nil {
			s.log.WithField("panic", r).Error("🛡️ validation loop panic recovered — restarting loop")
			// Re-arm the loop so a one-off panic doesn't permanently stop validation.
			go s.Loop(ctx, interval, pairsFn)
		}
	}()
	if interval <= 0 {
		interval = 30 * time.Minute
	}
	// Initial run shortly after start so the gate has data quickly.
	timer := time.NewTimer(2 * time.Minute)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
			if killswitch.Ring0Active() {
				s.log.Info("validation cycle skipped: ring 0 (global kill switch) active")
				timer.Reset(interval)
				continue
			}
			pairs := pairsFn()
			s.log.WithField("pairs", len(pairs)).Info("🔬 validation cycle: re-certifying strategies")
			for _, p := range pairs {
				select {
				case <-ctx.Done():
					return
				default:
				}
				if _, err := s.ValidatePair(ctx, p); err != nil {
					s.log.WithError(err).WithField("pair", p).Debug("validation: pair failed")
				}
			}
			timer.Reset(interval)
		}
	}
}
