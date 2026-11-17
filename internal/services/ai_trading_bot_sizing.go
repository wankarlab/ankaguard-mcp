package services

import (
	"context"
	"fmt"
	"math"
	"strings"
	"sync"
	"time"

	"github.com/sirupsen/logrus"

	aivalidation "github.com/rantipay/trading-mcp/internal/ai/validation"
	dynamic "github.com/rantipay/trading-mcp/internal/config/dynamic"
	"github.com/rantipay/trading-mcp/internal/gct"
)

// Capital-relative sizing (OpenSpec change capital-relative-sizing).
//
// Every money value the bot trades with — trade notional, confidence-tier
// sizes, the daily loss cap, the per-pair session pause and the drawdown base —
// is resolved here from the dynamic config snapshot and the real account
// capital, so the bot trades any account size without code or YAML changes.
// In bot.sizing.mode=absolute (or when no config store is available at all)
// the legacy USD values from the YAML config are used instead.

const (
	SizingModeCapitalPct = "capital_pct"
	SizingModeAbsolute   = "absolute"

	defaultSizingBasePct       = 10.0
	defaultSizingMinNotional   = 2.0
	defaultSizingMaxNotional   = 25.0
	defaultDailyLossPct        = 3.0
	defaultPairPauseNetLossPct = 0.5
	// legacyPairNetLossUSD is the pre-change fixed per-pair session pause,
	// kept for absolute mode and for when no capital reading exists.
	legacyPairNetLossUSD = 0.15
)

// sizingState is the mutable part of the resolver. It has its own mutex so it
// never has to be taken together with bot.mu while the capital is fetched.
type sizingState struct {
	mu sync.Mutex
	// dayStartCapital freezes the capital at the first resolution of the
	// trading day: the relative loss caps use it so a losing streak does not
	// shrink the cap as the account goes down. Reset with the daily counters.
	dayStartCapital float64
	dayStartAt      time.Time
	// drawdownFactor scales new entries after the intraday drawdown control
	// fired (1 = no reduction). Reset with the daily counters.
	drawdownFactor float64
	// tierPct caches the size_usd → size_pct conversion per tier so a tier that
	// only carries USD becomes relative from its first use onwards.
	tierPct map[string]float64
	// fallbackLogged avoids repeating the YAML-fallback notice every cycle.
	fallbackLogged bool
	// lastLogged is the fingerprint of the sizing params last logged.
	lastLogged string
	// lastValidation is the fingerprint of the validation.* limits last applied.
	lastValidation string
}

// sizingParams is the dynamic configuration the resolver works with.
type sizingParams struct {
	Mode           string
	BasePct        float64
	MinNotionalUSD float64
	MaxNotionalUSD float64
	DailyLossPct   float64
	DailyLossUSD   float64 // optional absolute cap; 0 = none
	PairPausePct   float64
	UseAccountCap  bool
	// Fallback is true when no dynamic config snapshot exists (config DB down
	// at boot): the legacy YAML values are used in absolute mode.
	Fallback bool
}

// SizingResolution is what the bot trades with in this cycle. It is exposed in
// the status endpoint so the operator sees the effective numbers.
type SizingResolution struct {
	Mode                  string    `json:"mode"`
	Fallback              bool      `json:"fallback"`
	CapitalUSD            float64   `json:"capital_usd"`
	CapitalAt             time.Time `json:"capital_at"`
	DayStartCapitalUSD    float64   `json:"day_start_capital_usd"`
	BaseNotionalUSD       float64   `json:"base_notional_usd"`
	MinNotionalUSD        float64   `json:"min_notional_usd"`
	MaxNotionalUSD        float64   `json:"max_notional_usd"`
	DailyLossLimitUSD     float64   `json:"daily_loss_limit_usd"`
	DailyLossUSD          float64   `json:"daily_loss_usd"`
	PairPauseThresholdUSD float64   `json:"pair_pause_threshold_usd"`
	DrawdownSizeFactor    float64   `json:"drawdown_size_factor"`

	// RiskSentinel per-order cap (order-notional-cap spec). OrderCapUSD is 0
	// when no cap is configured or the sentinel does not expose it.
	OrderCapUSD       float64 `json:"order_cap_usd"`
	BotMaxNotionalUSD float64 `json:"bot_max_notional_usd"`
	// CapAppliesToMarketOrders mirrors risk.enforce_cap_on_market_orders.
	CapAppliesToMarketOrders bool `json:"cap_applies_to_market_orders"`
	// CapIncoherent is true when the cap is below what the bot can produce.
	CapIncoherent        bool   `json:"cap_incoherent"`
	CapWarning           string `json:"cap_warning,omitempty"`
	CapNotAppliedWarning string `json:"cap_not_applied_warning,omitempty"`
}

// readSizingParams reads the sizing keys from the snapshot carried by ctx.
func (bot *AITradingBot) readSizingParams(ctx context.Context) sizingParams {
	snap := dynamic.SnapshotFromContext(ctx)
	if snap == nil {
		bot.sizing.mu.Lock()
		logIt := !bot.sizing.fallbackLogged
		bot.sizing.fallbackLogged = true
		bot.sizing.mu.Unlock()
		if logIt {
			bot.log.Warn("📏 sizing: YAML fallback mode — no dynamic config snapshot, using absolute USD values from config.yaml")
		}
		return sizingParams{Mode: SizingModeAbsolute, Fallback: true}
	}
	p := sizingParams{
		Mode:           strings.ToLower(strings.TrimSpace(snap.String("bot.sizing.mode"))),
		BasePct:        snap.Float("bot.sizing.base_pct"),
		MinNotionalUSD: snap.Float("bot.sizing.min_notional_usd"),
		MaxNotionalUSD: snap.Float("bot.sizing.max_notional_usd"),
		DailyLossPct:   snap.Float("bot.max_daily_loss_percent"),
		DailyLossUSD:   snap.Float("bot.max_daily_loss_usd"),
		PairPausePct:   snap.Float("bot.pair_pause.net_loss_pct"),
		UseAccountCap:  true,
	}
	if v, ok := snap.BoolOk("bot.drawdown.use_account_capital"); ok {
		p.UseAccountCap = v
	}
	if p.Mode != SizingModeAbsolute {
		p.Mode = SizingModeCapitalPct
	}
	if p.BasePct <= 0 {
		p.BasePct = defaultSizingBasePct
	}
	if p.MinNotionalUSD <= 0 {
		p.MinNotionalUSD = defaultSizingMinNotional
	}
	if p.MaxNotionalUSD <= 0 {
		// There is no "no ceiling": the ceiling is the safety net against a
		// wrong percentage or a bad capital reading.
		bot.log.WithField("default", defaultSizingMaxNotional).
			Warn("📏 sizing: bot.sizing.max_notional_usd unset or 0 — using the default ceiling")
		p.MaxNotionalUSD = defaultSizingMaxNotional
	}
	if p.MaxNotionalUSD < p.MinNotionalUSD {
		p.MaxNotionalUSD = p.MinNotionalUSD
	}
	if p.DailyLossPct <= 0 {
		p.DailyLossPct = defaultDailyLossPct
	}
	if p.PairPausePct <= 0 {
		p.PairPausePct = defaultPairPauseNetLossPct
	}
	return p
}

// dayStartCapital returns the frozen capital of the current trading day,
// initialising it from the live capital on the first call of the day.
func (bot *AITradingBot) dayStartCapital(ctx context.Context) (float64, bool) {
	bot.sizing.mu.Lock()
	v := bot.sizing.dayStartCapital
	bot.sizing.mu.Unlock()
	if v > 0 {
		return v, true
	}
	total, _, err := bot.accountCapital(ctx)
	if err != nil || total <= 0 {
		return 0, false
	}
	bot.sizing.mu.Lock()
	if bot.sizing.dayStartCapital <= 0 {
		bot.sizing.dayStartCapital = total
		bot.sizing.dayStartAt = time.Now()
		bot.log.WithField("capital_usd", fmt.Sprintf("%.2f", total)).Info("📏 sizing: day-start capital frozen for relative loss caps")
	}
	v = bot.sizing.dayStartCapital
	bot.sizing.mu.Unlock()
	return v, true
}

// resetSizingDay clears the per-day sizing state (called with the daily counters).
func (bot *AITradingBot) resetSizingDay() {
	bot.sizing.mu.Lock()
	bot.sizing.dayStartCapital = 0
	bot.sizing.dayStartAt = time.Time{}
	bot.sizing.drawdownFactor = 0
	bot.sizing.mu.Unlock()
}

func (bot *AITradingBot) drawdownSizeFactor() float64 {
	bot.sizing.mu.Lock()
	defer bot.sizing.mu.Unlock()
	if bot.sizing.drawdownFactor <= 0 || bot.sizing.drawdownFactor > 1 {
		return 1
	}
	return bot.sizing.drawdownFactor
}

// setDrawdownSizeFactor records the reduction factor for the rest of the day.
// It never compounds: the smallest factor seen today wins.
func (bot *AITradingBot) setDrawdownSizeFactor(f float64) {
	if f <= 0 || f > 1 {
		return
	}
	bot.sizing.mu.Lock()
	if bot.sizing.drawdownFactor <= 0 || f < bot.sizing.drawdownFactor {
		bot.sizing.drawdownFactor = f
	}
	bot.sizing.mu.Unlock()
}

// tierPercent returns the tier size as % of capital, converting a USD-only
// tier once against the capital at its first use.
func (bot *AITradingBot) tierPercent(tier *SizingTier, capital float64) float64 {
	if tier.SizePct > 0 {
		return tier.SizePct
	}
	if tier.SizeUSD <= 0 || capital <= 0 {
		return 0
	}
	key := fmt.Sprintf("%s|%.6f", tier.Name, tier.SizeUSD)
	bot.sizing.mu.Lock()
	defer bot.sizing.mu.Unlock()
	if bot.sizing.tierPct == nil {
		bot.sizing.tierPct = make(map[string]float64)
	}
	if pct, ok := bot.sizing.tierPct[key]; ok {
		return pct
	}
	pct := tier.SizeUSD / capital * 100
	bot.sizing.tierPct[key] = pct
	bot.log.WithFields(logrus.Fields{
		"tier":        tier.Name,
		"size_usd":    tier.SizeUSD,
		"capital_usd": fmt.Sprintf("%.2f", capital),
		"size_pct":    fmt.Sprintf("%.2f", pct),
	}).Info("📏 sizing: tier without size_pct converted to % of capital")
	return pct
}

// resolveNotional returns the trade notional in USD for an automatic entry
// (tier may be nil → base size). In capital_pct mode an unavailable capital is
// an error: the caller must not trade (fail-closed).
func (bot *AITradingBot) resolveNotional(ctx context.Context, tier *SizingTier) (float64, error) {
	p := bot.readSizingParams(ctx)
	factor := bot.drawdownSizeFactor()

	if p.Mode == SizingModeAbsolute {
		bot.mu.RLock()
		size := bot.config.TradeSize
		bot.mu.RUnlock()
		if tier != nil && tier.SizeUSD > 0 {
			size = tier.SizeUSD
		}
		return size * factor, nil
	}

	capital, _, err := bot.accountCapital(ctx)
	if err != nil || capital <= 0 {
		return 0, fmt.Errorf("account capital unavailable for capital-relative sizing: %v", err)
	}
	pct := p.BasePct
	source := "base"
	if tier != nil {
		if tp := bot.tierPercent(tier, capital); tp > 0 {
			pct = tp
			source = "tier:" + tier.Name
		}
	}
	raw := capital * pct / 100 * factor
	notional := math.Min(math.Max(raw, p.MinNotionalUSD), p.MaxNotionalUSD)
	fields := logrus.Fields{
		"capital_usd":  fmt.Sprintf("%.2f", capital),
		"pct":          fmt.Sprintf("%.2f", pct),
		"source":       source,
		"raw_usd":      fmt.Sprintf("%.2f", raw),
		"notional_usd": fmt.Sprintf("%.2f", notional),
	}
	if factor < 1 {
		fields["drawdown_factor"] = factor
	}
	switch {
	case raw > p.MaxNotionalUSD:
		bot.log.WithFields(fields).WithField("max_notional_usd", p.MaxNotionalUSD).Warn("📏 sizing: notional capped by bot.sizing.max_notional_usd")
	case raw < p.MinNotionalUSD:
		bot.log.WithFields(fields).WithField("min_notional_usd", p.MinNotionalUSD).Info("📏 sizing: notional raised to bot.sizing.min_notional_usd")
	default:
		bot.log.WithFields(fields).Info("📏 sizing resolved")
	}
	return notional, nil
}

// effectiveDailyLossLimit returns the daily loss cap in USD (positive number).
// capital_pct: min(day-start capital × pct, absolute cap when > 0). absolute or
// no capital reading: the legacy YAML cap (tightened by the absolute key when set).
func (bot *AITradingBot) effectiveDailyLossLimit(ctx context.Context) float64 {
	p := bot.readSizingParams(ctx)
	bot.mu.RLock()
	legacy := bot.config.MaxDailyLoss
	bot.mu.RUnlock()

	limit := legacy
	if p.Mode == SizingModeCapitalPct {
		if dsc, ok := bot.dayStartCapital(ctx); ok {
			limit = dsc * p.DailyLossPct / 100
		}
	}
	if p.DailyLossUSD > 0 && (limit <= 0 || p.DailyLossUSD < limit) {
		limit = p.DailyLossUSD
	}
	return limit
}

// dailyLossLimitReached reports whether the realized daily loss hit the cap.
func (bot *AITradingBot) dailyLossLimitReached(ctx context.Context) (bool, float64) {
	limit := bot.effectiveDailyLossLimit(ctx)
	bot.mu.RLock()
	loss := bot.dailyLoss
	bot.mu.RUnlock()
	return limit > 0 && loss <= -limit, limit
}

// pairPauseThreshold returns the (negative) net session PnL at which a pair is
// auto-paused: a % of the day-start capital, or the legacy -0.15 USD.
func (bot *AITradingBot) pairPauseThreshold(ctx context.Context) float64 {
	p := bot.readSizingParams(ctx)
	if p.Mode == SizingModeCapitalPct {
		if dsc, ok := bot.dayStartCapital(ctx); ok {
			return -(dsc * p.PairPausePct / 100)
		}
	}
	return -legacyPairNetLossUSD
}

// drawdownBaseCapital is the capital the intraday drawdown % is measured on.
func (bot *AITradingBot) drawdownBaseCapital(ctx context.Context) float64 {
	p := bot.readSizingParams(ctx)
	if p.UseAccountCap && !p.Fallback {
		if dsc, ok := bot.dayStartCapital(ctx); ok {
			return dsc
		}
	}
	proxy := 1000.0
	if snap := dynamic.SnapshotFromContext(ctx); snap != nil {
		if v := snap.Float("bot.drawdown.initial_capital_proxy"); v > 0 {
			proxy = v
		}
	}
	return proxy
}

// SizingStatus resolves the effective sizing numbers without touching the
// exchange beyond the cached capital (used by the status endpoint).
func (bot *AITradingBot) SizingStatus(ctx context.Context) SizingResolution {
	p := bot.readSizingParams(ctx)
	bot.capitalMu.Lock()
	capital, capitalAt := bot.capitalTotal, bot.capitalAt
	bot.capitalMu.Unlock()
	bot.sizing.mu.Lock()
	dsc := bot.sizing.dayStartCapital
	bot.sizing.mu.Unlock()
	bot.mu.RLock()
	legacySize, legacyLoss, dailyLoss := bot.config.TradeSize, bot.config.MaxDailyLoss, bot.dailyLoss
	bot.mu.RUnlock()

	r := SizingResolution{
		Mode:               p.Mode,
		Fallback:           p.Fallback,
		CapitalUSD:         capital,
		CapitalAt:          capitalAt,
		DayStartCapitalUSD: dsc,
		MinNotionalUSD:     p.MinNotionalUSD,
		MaxNotionalUSD:     p.MaxNotionalUSD,
		DailyLossUSD:       -dailyLoss,
		DrawdownSizeFactor: bot.drawdownSizeFactor(),
	}
	if r.DailyLossUSD < 0 {
		r.DailyLossUSD = 0
	}
	if p.Mode == SizingModeAbsolute {
		r.BaseNotionalUSD = legacySize * r.DrawdownSizeFactor
		r.DailyLossLimitUSD = legacyLoss
		r.PairPauseThresholdUSD = legacyPairNetLossUSD
	} else {
		if capital > 0 {
			raw := capital * p.BasePct / 100 * r.DrawdownSizeFactor
			r.BaseNotionalUSD = math.Min(math.Max(raw, p.MinNotionalUSD), p.MaxNotionalUSD)
		}
		base := dsc
		if base <= 0 {
			base = capital
		}
		if base > 0 {
			r.DailyLossLimitUSD = base * p.DailyLossPct / 100
			r.PairPauseThresholdUSD = base * p.PairPausePct / 100
		} else {
			r.DailyLossLimitUSD = legacyLoss
			r.PairPauseThresholdUSD = legacyPairNetLossUSD
		}
	}
	if p.DailyLossUSD > 0 && (r.DailyLossLimitUSD <= 0 || p.DailyLossUSD < r.DailyLossLimitUSD) {
		r.DailyLossLimitUSD = p.DailyLossUSD
	}
	bot.fillOrderCapStatus(ctx, &r)
	return r
}

// capEnforcementProvider is implemented by the RiskSentinel: whether the
// per-order cap is applied to orders without a price of their own (market).
type capEnforcementProvider interface {
	EnforceCapOnMarketOrders() bool
}

// fillOrderCapStatus adds the RiskSentinel per-order cap, whether it is applied
// to market orders, and the cap-vs-bot coherence to the status (spec
// order-notional-cap). Fields stay zero when the sentinel does not expose them.
func (bot *AITradingBot) fillOrderCapStatus(ctx context.Context, r *SizingResolution) {
	bot.mu.RLock()
	sentinel := bot.riskSentinel
	bot.mu.RUnlock()
	capper, ok := sentinel.(orderCapProvider)
	if !ok {
		return
	}
	r.OrderCapUSD = capper.EffectiveMaxOrderSizeUSD(ctx)
	if enf, ok := sentinel.(capEnforcementProvider); ok {
		r.CapAppliesToMarketOrders = enf.EnforceCapOnMarketOrders()
	}
	r.BotMaxNotionalUSD = bot.maxProducibleNotional(ctx)
	if r.OrderCapUSD > 0 && r.BotMaxNotionalUSD > r.OrderCapUSD {
		r.CapIncoherent = true
		r.CapWarning = fmt.Sprintf("per-order cap %.2f USD is below the %.2f USD the bot can produce", r.OrderCapUSD, r.BotMaxNotionalUSD)
	}
	if r.OrderCapUSD > 0 && !r.CapAppliesToMarketOrders {
		r.CapNotAppliedWarning = "the per-order cap is NOT applied to market orders (risk.enforce_cap_on_market_orders=false)"
	}
}

// logSizingIfChanged logs the resolved sizing at the first analysis cycle and
// after any change of the sizing keys. It warms the capital cache so the
// logged numbers are the ones the cycle will trade with.
func (bot *AITradingBot) logSizingIfChanged(ctx context.Context) {
	p := bot.readSizingParams(ctx)
	fp := fmt.Sprintf("%s|%v|%g|%g|%g|%g|%g|%g|%v", p.Mode, p.Fallback, p.BasePct, p.MinNotionalUSD,
		p.MaxNotionalUSD, p.DailyLossPct, p.DailyLossUSD, p.PairPausePct, p.UseAccountCap)
	if snap := dynamic.SnapshotFromContext(ctx); snap != nil {
		// Sentinel caps and tiers take part so the coherence check re-runs on change.
		fp += "|" + snap.String("risk.max_order_size_usd") + "|" + snap.String("risk.max_order_size_pct") + "|" + snap.String("risk.enforce_cap_on_market_orders") +
			"|" + snap.String("dynamic_sizing.tiers")
	}
	bot.sizing.mu.Lock()
	changed := fp != bot.sizing.lastLogged
	if changed {
		bot.sizing.lastLogged = fp
	}
	bot.sizing.mu.Unlock()
	if !changed {
		return
	}
	if p.Mode == SizingModeCapitalPct {
		if _, _, err := bot.accountCapital(ctx); err != nil {
			bot.log.WithError(err).Warn("📏 sizing: capital unavailable — automatic entries are skipped until it is readable")
		}
		bot.dayStartCapital(ctx)
	}
	r := bot.SizingStatus(ctx)
	bot.log.WithFields(logrus.Fields{
		"mode":                     r.Mode,
		"fallback_yaml":            r.Fallback,
		"capital_usd":              fmt.Sprintf("%.2f", r.CapitalUSD),
		"day_start_capital_usd":    fmt.Sprintf("%.2f", r.DayStartCapitalUSD),
		"base_pct":                 p.BasePct,
		"base_notional_usd":        fmt.Sprintf("%.2f", r.BaseNotionalUSD),
		"min_notional_usd":         r.MinNotionalUSD,
		"max_notional_usd":         r.MaxNotionalUSD,
		"daily_loss_limit_usd":     fmt.Sprintf("%.2f", r.DailyLossLimitUSD),
		"pair_pause_threshold_usd": fmt.Sprintf("%.2f", r.PairPauseThresholdUSD),
		"drawdown_size_factor":     r.DrawdownSizeFactor,
	}).Info("📏 sizing: effective values (startup or config change)")
	bot.checkConsistency(ctx)
}

// orderCapProvider is implemented by the RiskSentinel (per-order cap in force).
type orderCapProvider interface {
	EffectiveMaxOrderSizeUSD(ctx context.Context) float64
}

// AccountCapitalUSD exposes the bot's cached account capital (the same source
// the sizing resolver uses) so the RiskSentinel can evaluate its relative cap.
func (bot *AITradingBot) AccountCapitalUSD(ctx context.Context) (float64, error) {
	total, _, err := bot.accountCapital(ctx)
	return total, err
}

// sizingTiers returns the tier ladder executeTrade would use: the dynamic
// dynamic_sizing.tiers when set, else the enabled YAML tiers.
func (bot *AITradingBot) sizingTiers(ctx context.Context) []SizingTier {
	if snap := dynamic.SnapshotFromContext(ctx); snap != nil {
		var dyn []SizingTier
		if err := snap.JSON("dynamic_sizing.tiers", &dyn); err == nil && len(dyn) > 0 {
			return dyn
		}
	}
	bot.mu.RLock()
	defer bot.mu.RUnlock()
	if !bot.config.DynamicSizing.Enabled {
		return nil
	}
	return append([]SizingTier(nil), bot.config.DynamicSizing.Tiers...)
}

// maxProducibleNotional is the largest notional the resolver can produce right
// now (base size or highest tier, clamped by the floor/ceiling), from cached
// values only. 0 when unknown (capital_pct without a capital reading).
func (bot *AITradingBot) maxProducibleNotional(ctx context.Context) float64 {
	p := bot.readSizingParams(ctx)
	tiers := bot.sizingTiers(ctx)
	if p.Mode == SizingModeAbsolute {
		bot.mu.RLock()
		max := bot.config.TradeSize
		bot.mu.RUnlock()
		for i := range tiers {
			if tiers[i].SizeUSD > max {
				max = tiers[i].SizeUSD
			}
		}
		return max
	}
	bot.capitalMu.Lock()
	capital := bot.capitalTotal
	bot.capitalMu.Unlock()
	if capital <= 0 {
		return 0
	}
	pct := p.BasePct
	for i := range tiers {
		if tp := bot.tierPercent(&tiers[i], capital); tp > pct {
			pct = tp
		}
	}
	return math.Min(math.Max(capital*pct/100, p.MinNotionalUSD), p.MaxNotionalUSD)
}

// checkConsistency warns when the RiskSentinel's per-order cap is below the
// largest notional the bot can produce. It never corrects either value: the
// operator decides which one is wrong.
func (bot *AITradingBot) checkConsistency(ctx context.Context) {
	capper, ok := bot.riskSentinel.(orderCapProvider)
	if !ok {
		return
	}
	sentinelCap := capper.EffectiveMaxOrderSizeUSD(ctx)
	botMax := bot.maxProducibleNotional(ctx)
	if sentinelCap <= 0 || botMax <= 0 {
		return
	}
	if enf, ok := bot.riskSentinel.(capEnforcementProvider); ok && sentinelCap > 0 && !enf.EnforceCapOnMarketOrders() {
		bot.log.WithField("sentinel_max_order_usd", fmt.Sprintf("%.2f", sentinelCap)).Warn(
			"📏 sizing: the RiskSentinel per-order cap is NOT applied to market orders (risk.enforce_cap_on_market_orders=false)")
	}
	fields := logrus.Fields{
		"sentinel_max_order_usd": fmt.Sprintf("%.2f", sentinelCap),
		"bot_max_notional_usd":   fmt.Sprintf("%.2f", botMax),
	}
	if sentinelCap < botMax {
		bot.log.WithFields(fields).Warn(fmt.Sprintf(
			"📏 sizing: INCOHERENT caps — the RiskSentinel allows %.2f USD per order but the bot can produce %.2f USD (ceiling or highest tier); raise risk.max_order_size_usd / set risk.max_order_size_pct, or lower bot.sizing.max_notional_usd",
			sentinelCap, botMax))
		return
	}
	bot.log.WithFields(fields).Info("📏 sizing: RiskSentinel cap is coherent with the bot maximum")
}

// thresholdUpdater is the hot-reload hook of the AI-02 validation service.
type thresholdUpdater interface {
	UpdateThresholds(aivalidation.Thresholds)
}

// applyValidationThresholds pushes the validation.* dynamic keys into the AI-02
// validator when they changed since the last call. Unset keys keep the
// validator's current value.
func (bot *AITradingBot) applyValidationThresholds(ctx context.Context) {
	upd, ok := bot.validationSvc.(thresholdUpdater)
	if !ok {
		return
	}
	snap := dynamic.SnapshotFromContext(ctx)
	if snap == nil {
		return
	}
	t := aivalidation.Thresholds{
		MaxRiskPerTrade: snap.Float("validation.max_risk_per_trade_pct"),
		MaxDailyLoss:    snap.Float("validation.max_daily_loss_pct"),
		MaxDrawdown:     snap.Float("validation.max_drawdown_pct"),
		MaxStopLossPct:  snap.Float("validation.max_stop_loss_pct"),
		MinRiskReward:   snap.Float("validation.min_risk_reward"),
		MinConfluence:   snap.Int("validation.min_confluence"),
	}
	fp := fmt.Sprintf("%g|%g|%g|%g|%g|%d", t.MaxRiskPerTrade, t.MaxDailyLoss, t.MaxDrawdown, t.MaxStopLossPct, t.MinRiskReward, t.MinConfluence)
	bot.sizing.mu.Lock()
	changed := fp != bot.sizing.lastValidation
	if changed {
		bot.sizing.lastValidation = fp
	}
	bot.sizing.mu.Unlock()
	if !changed {
		return
	}
	upd.UpdateThresholds(t)
	bot.log.WithFields(logrus.Fields{
		"max_risk_per_trade_pct": t.MaxRiskPerTrade,
		"max_daily_loss_pct":     t.MaxDailyLoss,
		"max_drawdown_pct":       t.MaxDrawdown,
		"max_stop_loss_pct":      t.MaxStopLossPct,
		"min_risk_reward":        t.MinRiskReward,
		"min_confluence":         t.MinConfluence,
	}).Info("🛡️ AI-02: validation thresholds applied from dynamic config")
}

// minContractReason is the blacklist reason for a pair whose minimum contract
// exceeds the resolved notional.
const minContractReason = "min contract exceeds target size"

// handleMinContractTooLarge blacklists the pair for pairBlacklistTTL (not
// persisted: a price move or a larger account can make it tradeable again) so
// the pair selection rotates to another opportunity.
func (bot *AITradingBot) handleMinContractTooLarge(pair string, e *gct.ErrMinContractTooLarge) {
	bot.log.WithFields(logrus.Fields{
		"pair":                  pair,
		"min_contract_usd":      fmt.Sprintf("%.2f", e.MinContractNotional),
		"target_usd":            fmt.Sprintf("%.2f", e.TargetUSD),
		"max_notional_factor":   e.Factor,
		"min_size_usd":          fmt.Sprintf("%.2f", e.MinSizeUSD()),
		"blacklist_ttl_minutes": pairBlacklistTTL.Minutes(),
	}).Warn("📏 Entry skipped — minimum contract exceeds the target size; rotating pair")
	bot.blacklistPairFor(pair, minContractReason, pairBlacklistTTL, false)
}

// defaultMaxStopLossPct mirrors the STOP_LOSS_MAX_DISTANCE rule default.
const defaultMaxStopLossPct = 5.0

// maxStopLossPct is the largest stop distance (% of price) the AI-02 validator
// accepts (validation.max_stop_loss_pct). Safe to call with bot.mu held.
func maxStopLossPct(ctx context.Context) float64 {
	if snap := dynamic.SnapshotFromContext(ctx); snap != nil {
		if v := snap.Float("validation.max_stop_loss_pct"); v > 0 {
			return v
		}
	}
	return defaultMaxStopLossPct
}

// roundPct removes float noise from a computed percentage so a value that is
// exactly on a limit (5 % stop = 5.0000000000000036) compares as the limit.
func roundPct(v float64) float64 {
	return math.Round(v*1e4) / 1e4
}
