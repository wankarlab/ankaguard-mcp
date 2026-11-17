package risk

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/rantipay/trading-mcp/internal/config/dynamic"
	configdomain "github.com/rantipay/trading-mcp/internal/config/dynamic/domain"
	"github.com/rantipay/trading-mcp/internal/config/dynamic/manifest"
	"github.com/rantipay/trading-mcp/internal/edition"
)

// RiskMode defines the risk level for trading operations
type RiskMode int

const (
	// Conservative mode: Max 1 position, leverage 1x
	Conservative RiskMode = iota
	// Normal mode: Max 2 positions, leverage 2x
	Normal
	// Aggressive mode: Max 3 positions, leverage 3x
	Aggressive
	// ControlledCrazy mode: Max 5 positions, leverage 5x
	ControlledCrazy
)

// String returns the string representation of RiskMode
func (rm RiskMode) String() string {
	switch rm {
	case Conservative:
		return "Conservative"
	case Normal:
		return "Normal"
	case Aggressive:
		return "Aggressive"
	case ControlledCrazy:
		return "ControlledCrazy"
	default:
		return "Unknown"
	}
}

// RiskState represents the current risk state of the system
type RiskState struct {
	// Drawdown tracking
	CurrentDrawdownDaily   float64 `json:"current_drawdown_daily"`
	CurrentDrawdownWeekly  float64 `json:"current_drawdown_weekly"`
	CurrentDrawdownMonthly float64 `json:"current_drawdown_monthly"`

	// Exposure tracking
	TotalExposure    float64            `json:"total_exposure"`
	ExposureBySymbol map[string]float64 `json:"exposure_by_symbol"`

	// Loss tracking
	ConsecutiveLosses int `json:"consecutive_losses"`

	// Risk mode and kill switch
	RiskMode         RiskMode `json:"risk_mode"`
	KillSwitchActive bool     `json:"kill_switch_active"`
	KillSwitchReason string   `json:"kill_switch_reason,omitempty"`

	// Timestamps
	LastUpdate            time.Time `json:"last_update"`
	LastDrawdownReset     time.Time `json:"last_drawdown_reset"`
	KillSwitchActivatedAt time.Time `json:"kill_switch_activated_at,omitempty"`
}

// RiskDecision represents the decision made by the Risk Sentinel
type RiskDecision struct {
	Allowed bool   `json:"allowed"`
	Reason  string `json:"reason,omitempty"`
	// Code is a stable machine-readable rejection code (ORDER_CAP_EXCEEDED,
	// ORDER_NOTIONAL_UNKNOWN); empty for other rejections.
	Code        string   `json:"code,omitempty"`
	MaxSize     float64  `json:"max_size"`
	MaxLeverage float64  `json:"max_leverage"`
	Warnings    []string `json:"warnings,omitempty"`
}

// TradingAction represents an action that needs risk validation
type TradingAction struct {
	Type     ActionType `json:"type"`
	Symbol   string     `json:"symbol"`
	Side     string     `json:"side"` // "long" or "short"
	Size     float64    `json:"size"`
	Leverage float64    `json:"leverage"`
	Price    float64    `json:"price,omitempty"`

	// NotionalUSD is the real USD notional of the order, computed by whoever
	// knows the units (the GCT client: spot = base units x price; futures =
	// contracts x contract size x price). It replaces Size*Price for the
	// per-order cap. 0 with NotionalUnknown=false means "not provided": the
	// sentinel falls back to Size*Price for callers that predate this field.
	NotionalUSD float64 `json:"notional_usd,omitempty"`
	// NotionalUnknown is true when the notional could not be determined (no
	// recent reference price or no contract specification). NotionalReason
	// says why.
	NotionalUnknown bool   `json:"notional_unknown,omitempty"`
	NotionalReason  string `json:"notional_reason,omitempty"`
	// OwnPrice is true when the order carries its own limit price. Orders
	// without it (market orders) are only evaluated against the cap when
	// risk.enforce_cap_on_market_orders is on.
	OwnPrice bool `json:"own_price,omitempty"`

	// SpecID and TenantID are optional; when set EvaluateAction also
	// checks Ring1 (per-spec) and Ring2 (per-tenant) kill switches.
	SpecID   string `json:"spec_id,omitempty"`
	TenantID string `json:"tenant_id,omitempty"`
}

// ActionType defines the type of trading action
type ActionType int

const (
	ActionOpenPosition ActionType = iota
	ActionClosePosition
	ActionModifyPosition
)

// RiskConfig holds the configuration for the Risk Sentinel
type RiskConfig struct {
	// Drawdown limits (as percentage of capital)
	MaxDrawdownDaily   float64 `yaml:"max_drawdown_daily_pct"`
	MaxDrawdownWeekly  float64 `yaml:"max_drawdown_weekly_pct"`
	MaxDrawdownMonthly float64 `yaml:"max_drawdown_monthly_pct"`

	// Position limits
	MaxConsecutiveLosses int `yaml:"max_consecutive_losses"`

	// Exposure limits (as percentage of capital)
	MaxExposurePerSymbol float64 `yaml:"max_exposure_per_symbol_pct"`
	MaxExposureTotal     float64 `yaml:"max_exposure_total_pct"`

	// Circuit breaker configuration
	CircuitBreakers []CircuitBreakerConfig `yaml:"circuit_breakers"`

	// Kill switch configuration
	KillSwitch KillSwitchConfig `yaml:"kill_switch"`
}

// CircuitBreakerConfig defines a circuit breaker rule
type CircuitBreakerConfig struct {
	Trigger   string  `yaml:"trigger"`
	Threshold float64 `yaml:"threshold"`
	Action    string  `yaml:"action"`
}

// KillSwitchConfig defines kill switch configuration
type KillSwitchConfig struct {
	Manual       bool     `yaml:"manual"`
	AutoTriggers []string `yaml:"auto_triggers"`
}

// KillRing identifies the scope of a kill-switch activation.
// Ring0 = global (all tenants), Ring1 = per-Spec, Ring2 = per-tenant.
// This ordering is intentional: Ring0 always wins; Ring1 freezes a single
// certified strategy; Ring2 isolates one user without affecting others.
// AI code (Bucle B) is never permitted to deactivate any ring — only
// operator/admin (level≥3) or the automatic risk path may do so.
type KillRing int

const (
	KillRingGlobal    KillRing = 0
	KillRingPerSpec   KillRing = 1
	KillRingPerTenant KillRing = 2
)

// RingEntry records a single ring activation.
type RingEntry struct {
	Reason      string    `json:"reason"`
	ActivatedAt time.Time `json:"activated_at"`
	Ring        KillRing  `json:"ring"`
}

// RiskSentinel is the interface for risk management
type RiskSentinel interface {
	// EvaluateAction evaluates if a trading action is allowed
	EvaluateAction(ctx context.Context, action *TradingAction) (*RiskDecision, error)

	// UpdateState updates the risk state after a trade
	UpdateState(ctx context.Context, trade *Trade) error

	// GetRiskState returns the current risk state
	GetRiskState(ctx context.Context) (*RiskState, error)

	// ActivateKillSwitch activates the Ring0 global kill switch.
	// Only operator/admin or the automatic risk path may call this.
	// AI code is never permitted to call this method.
	ActivateKillSwitch(ctx context.Context, reason string) error

	// DeactivateKillSwitch deactivates the Ring0 global kill switch.
	// Authorization enforced at the handler layer (level>=3, not AI).
	DeactivateKillSwitch(ctx context.Context) error

	// ActivateRing1 freezes a single certified Spec (Ring1).
	// The Spec's bots stop entering new positions; open positions follow
	// existing exit logic. Other Specs continue unaffected.
	// Only operator/admin or automatic risk triggers may activate.
	ActivateRing1(ctx context.Context, specID, reason string) error

	// DeactivateRing1 resumes a frozen Spec.
	// Authorization enforced at the handler layer (level>=3, not AI).
	DeactivateRing1(ctx context.Context, specID string) error

	// IsSpecFrozen returns true when Ring1 is active for the given specID
	// or when the global Ring0 kill switch is active.
	IsSpecFrozen(specID string) bool

	// ActivateRing2 isolates a single tenant (Ring2): all bots for this
	// tenant_id are halted; other tenants continue unaffected.
	// Only operator/admin or automatic risk triggers may activate.
	ActivateRing2(ctx context.Context, tenantID, reason string) error

	// DeactivateRing2 resumes an isolated tenant.
	// Authorization enforced at the handler layer (level>=3, not AI).
	DeactivateRing2(ctx context.Context, tenantID string) error

	// IsTenantIsolated returns true when Ring2 is active for the given tenantID
	// or when the global Ring0 kill switch is active.
	IsTenantIsolated(tenantID string) bool

	// FrozenSpecs returns a snapshot of all currently frozen Spec entries (Ring1).
	FrozenSpecs() map[string]RingEntry

	// IsolatedTenants returns a snapshot of all currently isolated tenant entries (Ring2).
	IsolatedTenants() map[string]RingEntry

	// ResetDrawdown resets drawdown counters
	ResetDrawdown(ctx context.Context, period string) error
}

// Trade represents a completed trade for risk tracking
type Trade struct {
	Symbol     string
	Side       string
	Size       float64
	EntryPrice float64
	ExitPrice  float64
	PnL        float64
	Timestamp  time.Time
}

// sentinel is the concrete implementation of RiskSentinel
type sentinel struct {
	config *RiskConfig
	state  *RiskState
	logger *logrus.Logger
	mu     sync.RWMutex

	// Capital tracking
	initialCapital float64
	currentCapital float64

	// Ring1: per-Spec frozen entries. Access protected by mu.
	frozenSpecs map[string]RingEntry

	// Ring2: per-tenant isolated entries. Access protected by mu.
	isolatedTenants map[string]RingEntry

	// configStore is the optional dynamic config store. When wired, the sentinel
	// reads per-RiskMode leverage caps (risk.leverage.*) and the per-order USD size
	// cap (risk.max_order_size_usd) live from the manifest/DB instead of hardcoded
	// values. nil → hardcoded defaults (unchanged behaviour). Audit P1.
	configStore *dynamic.ConfigStore

	// capitalProvider returns the account capital in USD for the relative order
	// cap (risk.max_order_size_pct). The same source the AI bot sizes with.
	// nil → the relative cap is not evaluated. Called outside mu.
	capitalProvider CapitalProvider
}

// CapitalProvider returns the current account capital in USD.
type CapitalProvider func(ctx context.Context) (float64, error)

// SetCapitalProvider wires the capital source used by risk.max_order_size_pct.
func (s *sentinel) SetCapitalProvider(p CapitalProvider) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.capitalProvider = p
}

// EffectiveMaxOrderSizeUSD is the per-order cap in force: the smaller of
// risk.max_order_size_usd (when > 0) and risk.max_order_size_pct of the account
// capital (when > 0 and the capital is readable). 0 = no cap.
func (s *sentinel) EffectiveMaxOrderSizeUSD(ctx context.Context) float64 {
	s.mu.RLock()
	snap := s.globalSnap()
	provider := s.capitalProvider
	s.mu.RUnlock()
	if snap == nil {
		return 0
	}
	capital, capErr := 0.0, error(nil)
	if pct, ok := snap.FloatOk("risk.max_order_size_pct"); ok && pct > 0 && provider != nil {
		capital, capErr = provider(ctx)
		if capErr != nil || capital <= 0 {
			s.logger.WithError(capErr).Warn("risk sentinel: capital unavailable for risk.max_order_size_pct — using the absolute cap only")
		}
	}
	return effectiveOrderCap(snap, capital)
}

// effectiveOrderCap combines the absolute and the relative per-order caps.
// capital <= 0 means "unknown": only the absolute cap applies.
func effectiveOrderCap(snap *dynamic.ScopedSnapshot, capital float64) float64 {
	limit := 0.0
	if v, ok := snap.FloatOk("risk.max_order_size_usd"); ok && v > 0 {
		limit = v
	}
	if pct, ok := snap.FloatOk("risk.max_order_size_pct"); ok && pct > 0 && capital > 0 {
		if rel := capital * pct / 100; limit <= 0 || rel < limit {
			limit = rel
		}
	}
	return limit
}

// Rejection codes of the per-order cap.
const (
	CodeOrderCapExceeded     = "ORDER_CAP_EXCEEDED"
	CodeOrderNotionalUnknown = "ORDER_NOTIONAL_UNKNOWN"
)

// DefaultReferencePriceMaxAge is the fallback of risk.reference_price_max_age.
const DefaultReferencePriceMaxAge = 10 * time.Second

type capRejection struct{ code, reason string }

// checkOrderCap applies the per-order cap to the action's USD notional.
// Rules (spec order-notional-cap):
//   - no cap (maxUSD <= 0): nothing to check.
//   - orders that only reduce or close exposure are never blocked, neither by
//     the cap nor by an unknown notional.
//   - orders without a price of their own (market) are only evaluated when
//     risk.enforce_cap_on_market_orders is true; otherwise behaviour is the
//     one before the parameter existed (not evaluated).
//   - unknown notional: rejected (fail closed) when enforcement is on; an
//     order carrying its own price whose notional is unknown while enforcement
//     is off is let through, as before.
//
// enforce is risk.enforce_cap_on_market_orders.
func checkOrderCap(action *TradingAction, maxUSD float64, enforce bool) *capRejection {
	if maxUSD <= 0 || action.Type == ActionClosePosition {
		return nil
	}
	legacy := action.NotionalUSD == 0 && !action.NotionalUnknown // caller predates NotionalUSD
	hasPrice := action.OwnPrice || (legacy && action.Price > 0)
	if !hasPrice && !enforce {
		return nil
	}
	notional := action.NotionalUSD
	unknown := action.NotionalUnknown
	if legacy {
		if action.Price <= 0 {
			unknown = true
		} else {
			notional = action.Size * action.Price
		}
	}
	if unknown {
		if !enforce {
			return nil
		}
		reason := action.NotionalReason
		if reason == "" {
			reason = "no reference price or contract specification"
		}
		return &capRejection{CodeOrderNotionalUnknown, fmt.Sprintf(
			"%s: cannot determine the USD notional of the order (%s) while the per-order cap $%.2f is active",
			CodeOrderNotionalUnknown, reason, maxUSD)}
	}
	if notional > maxUSD {
		return &capRejection{CodeOrderCapExceeded, fmt.Sprintf(
			"%s: order notional $%.2f exceeds the per-order cap $%.2f (max_order_size_usd/pct)",
			CodeOrderCapExceeded, notional, maxUSD)}
	}
	return nil
}

// ReferencePriceMaxAge returns risk.reference_price_max_age clamped to
// [1s, 5m]; DefaultReferencePriceMaxAge when unset or without config store.
func (s *sentinel) ReferencePriceMaxAge() time.Duration {
	s.mu.RLock()
	snap := s.globalSnap()
	s.mu.RUnlock()
	if snap == nil {
		return DefaultReferencePriceMaxAge
	}
	d, ok := snap.DurationOk("risk.reference_price_max_age")
	if !ok || d <= 0 {
		return DefaultReferencePriceMaxAge
	}
	return min(max(d, time.Second), 5*time.Minute)
}

// EnforceCapOnMarketOrders reports risk.enforce_cap_on_market_orders.
func (s *sentinel) EnforceCapOnMarketOrders() bool {
	s.mu.RLock()
	snap := s.globalSnap()
	s.mu.RUnlock()
	if snap == nil {
		return false
	}
	v, _ := snap.BoolOk("risk.enforce_cap_on_market_orders")
	return v
}

// NewRiskSentinel creates a new Risk Sentinel instance
func NewRiskSentinel(config *RiskConfig, initialCapital float64, logger *logrus.Logger) RiskSentinel {
	if logger == nil {
		logger = logrus.New()
	}

	return &sentinel{
		config: config,
		state: &RiskState{
			RiskMode:          Normal,
			ExposureBySymbol:  make(map[string]float64),
			LastUpdate:        time.Now(),
			LastDrawdownReset: time.Now(),
		},
		logger:          logger,
		initialCapital:  initialCapital,
		currentCapital:  initialCapital,
		frozenSpecs:     make(map[string]RingEntry),
		isolatedTenants: make(map[string]RingEntry),
	}
}

// SetConfigStore wires the dynamic config store so the sentinel reads per-RiskMode
// leverage caps and the per-order USD size cap live. nil keeps hardcoded defaults
// (unchanged behaviour). Audit P1.
func (s *sentinel) SetConfigStore(store *dynamic.ConfigStore) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.configStore = store
}

// globalSnap returns a global-scoped dynamic snapshot, or nil when no config store
// is wired. Caller must hold at least s.mu.RLock (reads s.configStore).
func (s *sentinel) globalSnap() *dynamic.ScopedSnapshot {
	if s.configStore == nil {
		// Product edition without the config store (Postgres down or absent):
		// fall back to the registry defaults so the per-order cap and its
		// enforcement on market orders still apply instead of silently
		// disappearing. The standard build keeps its historical behaviour.
		if storeFallbackEnabled() {
			return productDefaultSnapshot()
		}
		return nil
	}
	return s.configStore.SnapshotForScope([]dynamic.ScopeRef{{Type: configdomain.ScopeGlobal, ID: "*"}})
}

// EvaluateAction evaluates if a trading action is allowed
func (s *sentinel) EvaluateAction(ctx context.Context, action *TradingAction) (*RiskDecision, error) {
	// Resolved before taking mu: the relative cap may read the account capital.
	maxUSD := s.EffectiveMaxOrderSizeUSD(ctx)

	s.mu.RLock()
	defer s.mu.RUnlock()

	decision := &RiskDecision{
		Allowed:     true,
		MaxSize:     action.Size,
		MaxLeverage: s.getMaxLeverageByRiskMode(),
		Warnings:    []string{},
	}

	// Per-order USD size cap: min(risk.max_order_size_usd, risk.max_order_size_pct
	// of capital). Only enforced when a config store is wired and the cap is > 0;
	// 0 or no store → no check (unchanged behaviour). Previously this safety cap was
	// YAML-only and not enforced in the sentinel (audit P1).
	enforceMarket := false
	if snap := s.globalSnap(); snap != nil {
		enforceMarket, _ = snap.BoolOk("risk.enforce_cap_on_market_orders")
	}
	if reject := checkOrderCap(action, maxUSD, enforceMarket); reject != nil {
		decision.Allowed = false
		decision.Code = reject.code
		decision.Reason = reject.reason
		return decision, nil
	}

	// Ring0: global kill switch
	if s.state.KillSwitchActive {
		decision.Allowed = false
		decision.Reason = fmt.Sprintf("Ring0 kill switch active: %s", s.state.KillSwitchReason)
		return decision, nil
	}

	// Ring1: per-Spec freeze
	if action.SpecID != "" {
		if entry, frozen := s.frozenSpecs[action.SpecID]; frozen {
			decision.Allowed = false
			decision.Reason = fmt.Sprintf("Ring1 Spec %s frozen: %s (since %s)",
				action.SpecID, entry.Reason, entry.ActivatedAt.Format(time.RFC3339))
			return decision, nil
		}
	}

	// Ring2: per-tenant isolation
	if action.TenantID != "" {
		if entry, isolated := s.isolatedTenants[action.TenantID]; isolated {
			decision.Allowed = false
			decision.Reason = fmt.Sprintf("Ring2 tenant %s isolated: %s (since %s)",
				action.TenantID, entry.Reason, entry.ActivatedAt.Format(time.RFC3339))
			return decision, nil
		}
	}

	// Check drawdown limits
	if s.state.CurrentDrawdownDaily > s.config.MaxDrawdownDaily {
		decision.Allowed = false
		decision.Reason = fmt.Sprintf("Daily drawdown limit exceeded: %.2f%% > %.2f%%",
			s.state.CurrentDrawdownDaily, s.config.MaxDrawdownDaily)
		return decision, nil
	}

	if s.state.CurrentDrawdownWeekly > s.config.MaxDrawdownWeekly {
		decision.Allowed = false
		decision.Reason = fmt.Sprintf("Weekly drawdown limit exceeded: %.2f%% > %.2f%%",
			s.state.CurrentDrawdownWeekly, s.config.MaxDrawdownWeekly)
		return decision, nil
	}

	if s.state.CurrentDrawdownMonthly > s.config.MaxDrawdownMonthly {
		decision.Allowed = false
		decision.Reason = fmt.Sprintf("Monthly drawdown limit exceeded: %.2f%% > %.2f%%",
			s.state.CurrentDrawdownMonthly, s.config.MaxDrawdownMonthly)
		return decision, nil
	}

	// Check consecutive losses
	if s.state.ConsecutiveLosses >= s.config.MaxConsecutiveLosses {
		decision.Allowed = false
		decision.Reason = fmt.Sprintf("Max consecutive losses reached: %d",
			s.state.ConsecutiveLosses)
		return decision, nil
	}

	// Check exposure limits
	newTotalExposure := s.state.TotalExposure + (action.Size * action.Price)
	maxExposure := s.currentCapital * (s.config.MaxExposureTotal / 100.0)

	if newTotalExposure > maxExposure {
		decision.Allowed = false
		decision.Reason = fmt.Sprintf("Total exposure limit exceeded: %.2f > %.2f",
			newTotalExposure, maxExposure)
		return decision, nil
	}

	// Check per-symbol exposure
	symbolExposure := s.state.ExposureBySymbol[action.Symbol] + (action.Size * action.Price)
	maxSymbolExposure := s.currentCapital * (s.config.MaxExposurePerSymbol / 100.0)

	if symbolExposure > maxSymbolExposure {
		decision.Allowed = false
		decision.Reason = fmt.Sprintf("Symbol exposure limit exceeded for %s: %.2f > %.2f",
			action.Symbol, symbolExposure, maxSymbolExposure)
		return decision, nil
	}

	// Check leverage
	if action.Leverage > decision.MaxLeverage {
		decision.Warnings = append(decision.Warnings,
			fmt.Sprintf("Leverage %.1fx exceeds max %.1fx for risk mode %s",
				action.Leverage, decision.MaxLeverage, s.state.RiskMode))
		decision.MaxLeverage = s.getMaxLeverageByRiskMode()
	}

	// Add warnings if approaching limits
	if s.state.CurrentDrawdownDaily > s.config.MaxDrawdownDaily*0.8 {
		decision.Warnings = append(decision.Warnings,
			fmt.Sprintf("Approaching daily drawdown limit: %.2f%%", s.state.CurrentDrawdownDaily))
	}

	if newTotalExposure > maxExposure*0.8 {
		decision.Warnings = append(decision.Warnings,
			fmt.Sprintf("Approaching total exposure limit: %.2f", newTotalExposure))
	}

	return decision, nil
}

// getMaxLeverageByRiskMode returns the maximum leverage for the current risk mode.
// Caps are read from the dynamic config (risk.leverage.*) when a config store is
// wired; otherwise the hardcoded defaults (1/2/3/5) are used so behaviour is
// unchanged. Audit P1: previously these were hardcoded with no override path.
// Caller must hold at least s.mu.RLock.
func (s *sentinel) getMaxLeverageByRiskMode() float64 {
	snap := s.globalSnap()

	// dynLev resolves a leverage key from the dynamic config, falling back to def
	// when no store is wired or the key is not configured.
	dynLev := func(key string, def float64) float64 {
		if snap == nil {
			return def
		}
		if v, ok := snap.FloatOk(key); ok && v > 0 {
			return v
		}
		return def
	}

	switch s.state.RiskMode {
	case Conservative:
		return dynLev("risk.leverage.conservative", 1.0)
	case Normal:
		return dynLev("risk.leverage.normal", 2.0)
	case Aggressive:
		return dynLev("risk.leverage.aggressive", 3.0)
	case ControlledCrazy:
		return dynLev("risk.leverage.controlled_crazy", 5.0)
	default:
		return dynLev("risk.leverage.conservative", 1.0)
	}
}

// UpdateState updates the risk state after a trade
func (s *sentinel) UpdateState(ctx context.Context, trade *Trade) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Update capital
	s.currentCapital += trade.PnL

	// Update consecutive losses
	if trade.PnL < 0 {
		s.state.ConsecutiveLosses++
		s.logger.WithFields(logrus.Fields{
			"consecutive_losses": s.state.ConsecutiveLosses,
			"pnl":                trade.PnL,
			"symbol":             trade.Symbol,
		}).Warn("Trade resulted in loss")
	} else {
		// Reset consecutive losses on profit
		if s.state.ConsecutiveLosses > 0 {
			s.logger.WithFields(logrus.Fields{
				"previous_losses": s.state.ConsecutiveLosses,
				"pnl":             trade.PnL,
			}).Info("Consecutive losses reset after profit")
		}
		s.state.ConsecutiveLosses = 0
	}

	// Update drawdown
	s.updateDrawdown()

	// Update exposure (remove closed position)
	positionValue := trade.Size * trade.ExitPrice
	s.state.TotalExposure -= positionValue
	if s.state.TotalExposure < 0 {
		s.state.TotalExposure = 0
	}

	s.state.ExposureBySymbol[trade.Symbol] -= positionValue
	if s.state.ExposureBySymbol[trade.Symbol] < 0 {
		s.state.ExposureBySymbol[trade.Symbol] = 0
	}

	s.state.LastUpdate = time.Now()

	// Check circuit breakers
	s.checkCircuitBreakers()

	s.logger.WithFields(logrus.Fields{
		"current_capital":    s.currentCapital,
		"drawdown_daily":     s.state.CurrentDrawdownDaily,
		"consecutive_losses": s.state.ConsecutiveLosses,
		"total_exposure":     s.state.TotalExposure,
		"risk_mode":          s.state.RiskMode,
	}).Debug("Risk state updated")

	return nil
}

// updateDrawdown calculates and updates drawdown percentages
func (s *sentinel) updateDrawdown() {
	if s.initialCapital == 0 {
		return
	}

	capitalLoss := s.initialCapital - s.currentCapital
	if capitalLoss < 0 {
		capitalLoss = 0
	}

	drawdownPct := (capitalLoss / s.initialCapital) * 100.0

	// Update all drawdown periods
	s.state.CurrentDrawdownDaily = drawdownPct
	s.state.CurrentDrawdownWeekly = drawdownPct
	s.state.CurrentDrawdownMonthly = drawdownPct

	// Check if we need to reset drawdown counters
	now := time.Now()

	// Reset daily at midnight
	if now.Day() != s.state.LastDrawdownReset.Day() {
		s.state.CurrentDrawdownDaily = 0
		s.state.LastDrawdownReset = now
		s.logger.Info("Daily drawdown reset")
	}

	// Reset weekly on Monday
	if now.Weekday() == time.Monday && s.state.LastDrawdownReset.Weekday() != time.Monday {
		s.state.CurrentDrawdownWeekly = 0
		s.logger.Info("Weekly drawdown reset")
	}

	// Reset monthly on 1st
	if now.Day() == 1 && s.state.LastDrawdownReset.Day() != 1 {
		s.state.CurrentDrawdownMonthly = 0
		s.logger.Info("Monthly drawdown reset")
	}
}

// checkCircuitBreakers checks and triggers circuit breakers
func (s *sentinel) checkCircuitBreakers() {
	for _, cb := range s.config.CircuitBreakers {
		triggered := false

		switch cb.Trigger {
		case "consecutive_losses":
			if float64(s.state.ConsecutiveLosses) >= cb.Threshold {
				triggered = true
			}
		case "daily_drawdown":
			if s.state.CurrentDrawdownDaily >= cb.Threshold {
				triggered = true
			}
		case "weekly_drawdown":
			if s.state.CurrentDrawdownWeekly >= cb.Threshold {
				triggered = true
			}
		case "monthly_drawdown":
			if s.state.CurrentDrawdownMonthly >= cb.Threshold {
				triggered = true
			}
		}

		if triggered {
			s.executeCircuitBreakerAction(cb)
		}
	}
}

// executeCircuitBreakerAction executes the action for a triggered circuit breaker
func (s *sentinel) executeCircuitBreakerAction(cb CircuitBreakerConfig) {
	s.logger.WithFields(logrus.Fields{
		"trigger":   cb.Trigger,
		"threshold": cb.Threshold,
		"action":    cb.Action,
	}).Warn("Circuit breaker triggered")

	switch cb.Action {
	case "reduce_risk_mode":
		if s.state.RiskMode > Conservative {
			oldMode := s.state.RiskMode
			s.state.RiskMode--
			s.logger.WithFields(logrus.Fields{
				"old_mode": oldMode,
				"new_mode": s.state.RiskMode,
			}).Warn("Risk mode reduced by circuit breaker")
		}
	case "pause_trading":
		reason := fmt.Sprintf("Circuit breaker: %s >= %.2f", cb.Trigger, cb.Threshold)
		s.activateKillSwitchInternal(reason)
	}
}

// GetRiskState returns the current risk state
func (s *sentinel) GetRiskState(ctx context.Context) (*RiskState, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	// Create a copy to avoid race conditions
	stateCopy := &RiskState{
		CurrentDrawdownDaily:   s.state.CurrentDrawdownDaily,
		CurrentDrawdownWeekly:  s.state.CurrentDrawdownWeekly,
		CurrentDrawdownMonthly: s.state.CurrentDrawdownMonthly,
		TotalExposure:          s.state.TotalExposure,
		ExposureBySymbol:       make(map[string]float64),
		ConsecutiveLosses:      s.state.ConsecutiveLosses,
		RiskMode:               s.state.RiskMode,
		KillSwitchActive:       s.state.KillSwitchActive,
		KillSwitchReason:       s.state.KillSwitchReason,
		LastUpdate:             s.state.LastUpdate,
		LastDrawdownReset:      s.state.LastDrawdownReset,
		KillSwitchActivatedAt:  s.state.KillSwitchActivatedAt,
	}

	// Copy exposure map
	for k, v := range s.state.ExposureBySymbol {
		stateCopy.ExposureBySymbol[k] = v
	}

	return stateCopy, nil
}

// ActivateKillSwitch activates the kill switch
func (s *sentinel) ActivateKillSwitch(ctx context.Context, reason string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.activateKillSwitchInternal(reason)
}

// activateKillSwitchInternal activates the kill switch (internal, no lock)
func (s *sentinel) activateKillSwitchInternal(reason string) error {
	if s.state.KillSwitchActive {
		s.logger.Warn("Kill switch already active")
		return fmt.Errorf("kill switch already active")
	}

	s.state.KillSwitchActive = true
	s.state.KillSwitchReason = reason
	s.state.KillSwitchActivatedAt = time.Now()

	s.logger.WithFields(logrus.Fields{
		"reason":    reason,
		"timestamp": s.state.KillSwitchActivatedAt,
	}).Error("🚨 KILL SWITCH ACTIVATED 🚨")

	return nil
}

// DeactivateKillSwitch deactivates the kill switch
func (s *sentinel) DeactivateKillSwitch(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.state.KillSwitchActive {
		s.logger.Warn("Kill switch already inactive")
		return fmt.Errorf("kill switch already inactive")
	}

	s.state.KillSwitchActive = false
	previousReason := s.state.KillSwitchReason
	s.state.KillSwitchReason = ""

	s.logger.WithFields(logrus.Fields{
		"previous_reason": previousReason,
		"timestamp":       time.Now(),
	}).Info("Kill switch deactivated")

	return nil
}

// ResetDrawdown resets drawdown counters for a specific period
func (s *sentinel) ResetDrawdown(ctx context.Context, period string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	switch period {
	case "daily":
		s.state.CurrentDrawdownDaily = 0
		s.logger.Info("Daily drawdown manually reset")
	case "weekly":
		s.state.CurrentDrawdownWeekly = 0
		s.logger.Info("Weekly drawdown manually reset")
	case "monthly":
		s.state.CurrentDrawdownMonthly = 0
		s.logger.Info("Monthly drawdown manually reset")
	case "all":
		s.state.CurrentDrawdownDaily = 0
		s.state.CurrentDrawdownWeekly = 0
		s.state.CurrentDrawdownMonthly = 0
		s.logger.Info("All drawdown counters manually reset")
	default:
		return fmt.Errorf("invalid period: %s (must be daily, weekly, monthly, or all)", period)
	}

	s.state.LastDrawdownReset = time.Now()
	return nil
}

// UpdateExposure updates the exposure for a symbol (called when position is opened)
func (s *sentinel) UpdateExposure(symbol string, size float64, price float64) {
	s.mu.Lock()
	defer s.mu.Unlock()

	positionValue := size * price
	s.state.TotalExposure += positionValue
	s.state.ExposureBySymbol[symbol] += positionValue

	s.logger.WithFields(logrus.Fields{
		"symbol":          symbol,
		"position_value":  positionValue,
		"total_exposure":  s.state.TotalExposure,
		"symbol_exposure": s.state.ExposureBySymbol[symbol],
	}).Debug("Exposure updated")
}

// GetCurrentCapital returns the current capital
func (s *sentinel) GetCurrentCapital() float64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.currentCapital
}

// ActivateRing1 freezes a single Spec (Ring1).
// Only operator/admin or automatic risk triggers may call this.
func (s *sentinel) ActivateRing1(ctx context.Context, specID, reason string) error {
	if specID == "" {
		return fmt.Errorf("specID is required for Ring1 activation")
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.frozenSpecs[specID]; exists {
		return fmt.Errorf("Ring1 already active for spec %s", specID)
	}
	s.frozenSpecs[specID] = RingEntry{
		Reason:      reason,
		ActivatedAt: time.Now(),
		Ring:        KillRingPerSpec,
	}
	s.logger.WithFields(logrus.Fields{
		"spec_id": specID,
		"reason":  reason,
	}).Error("🔴 RING1 ACTIVATED — Spec frozen")
	return nil
}

// DeactivateRing1 resumes a frozen Spec.
// Authorization is enforced at the handler layer (level>=3, not AI).
func (s *sentinel) DeactivateRing1(ctx context.Context, specID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.frozenSpecs[specID]; !exists {
		return fmt.Errorf("Ring1 not active for spec %s", specID)
	}
	delete(s.frozenSpecs, specID)
	s.logger.WithField("spec_id", specID).Info("Ring1 deactivated — Spec resumed")
	return nil
}

// IsSpecFrozen returns true when the Spec is frozen (Ring1) or Ring0 is active.
func (s *sentinel) IsSpecFrozen(specID string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.state.KillSwitchActive {
		return true
	}
	_, frozen := s.frozenSpecs[specID]
	return frozen
}

// ActivateRing2 isolates a single tenant (Ring2).
// Only operator/admin or automatic risk triggers may call this.
func (s *sentinel) ActivateRing2(ctx context.Context, tenantID, reason string) error {
	if tenantID == "" {
		return fmt.Errorf("tenantID is required for Ring2 activation")
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.isolatedTenants[tenantID]; exists {
		return fmt.Errorf("Ring2 already active for tenant %s", tenantID)
	}
	s.isolatedTenants[tenantID] = RingEntry{
		Reason:      reason,
		ActivatedAt: time.Now(),
		Ring:        KillRingPerTenant,
	}
	s.logger.WithFields(logrus.Fields{
		"tenant_id": tenantID,
		"reason":    reason,
	}).Error("🔴 RING2 ACTIVATED — Tenant isolated")
	return nil
}

// DeactivateRing2 resumes an isolated tenant.
// Authorization is enforced at the handler layer (level>=3, not AI).
func (s *sentinel) DeactivateRing2(ctx context.Context, tenantID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.isolatedTenants[tenantID]; !exists {
		return fmt.Errorf("Ring2 not active for tenant %s", tenantID)
	}
	delete(s.isolatedTenants, tenantID)
	s.logger.WithField("tenant_id", tenantID).Info("Ring2 deactivated — Tenant resumed")
	return nil
}

// IsTenantIsolated returns true when the tenant is isolated (Ring2) or Ring0 is active.
func (s *sentinel) IsTenantIsolated(tenantID string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.state.KillSwitchActive {
		return true
	}
	_, isolated := s.isolatedTenants[tenantID]
	return isolated
}

// FrozenSpecs returns a snapshot of all Ring1 entries.
func (s *sentinel) FrozenSpecs() map[string]RingEntry {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make(map[string]RingEntry, len(s.frozenSpecs))
	for k, v := range s.frozenSpecs {
		out[k] = v
	}
	return out
}

// IsolatedTenants returns a snapshot of all Ring2 entries.
func (s *sentinel) IsolatedTenants() map[string]RingEntry {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make(map[string]RingEntry, len(s.isolatedTenants))
	for k, v := range s.isolatedTenants {
		out[k] = v
	}
	return out
}

// SetRiskMode manually sets the risk mode
func (s *sentinel) SetRiskMode(mode RiskMode) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if mode < Conservative || mode > ControlledCrazy {
		return fmt.Errorf("invalid risk mode: %d", mode)
	}

	oldMode := s.state.RiskMode
	s.state.RiskMode = mode

	s.logger.WithFields(logrus.Fields{
		"old_mode": oldMode,
		"new_mode": mode,
	}).Info("Risk mode manually changed")

	return nil
}

// storeFallbackEnabled reports whether the registry-default fallback applies;
// a variable so tests of the store-less logic can switch it off.
var storeFallbackEnabled = edition.IsProduct

var (
	productDefaultsOnce sync.Once
	productDefaults     *dynamic.ScopedSnapshot
)

// productDefaultSnapshot is a global snapshot of every manifest default,
// built once.
func productDefaultSnapshot() *dynamic.ScopedSnapshot {
	productDefaultsOnce.Do(func() {
		values := map[string]string{}
		for _, e := range manifest.All() {
			if e.Default != "" {
				values[e.Key] = e.Default
			}
		}
		productDefaults = dynamic.NewStaticSnapshot(values)
	})
	return productDefaults
}
