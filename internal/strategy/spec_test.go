package strategy_test

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/rantipay/trading-mcp/internal/config"
	"github.com/rantipay/trading-mcp/internal/strategy"
	"gopkg.in/yaml.v3"
)

// ─── helpers ─────────────────────────────────────────────────────────────────

func loadFixture(t *testing.T) *strategy.StrategySpec {
	t.Helper()
	data, err := os.ReadFile("testdata/sei-usdt-trendfollow-5m.yaml")
	if err != nil {
		t.Fatalf("load fixture: %v", err)
	}
	var s strategy.StrategySpec
	if err := yaml.Unmarshal(data, &s); err != nil {
		t.Fatalf("unmarshal fixture: %v", err)
	}
	return &s
}

func defaultLimits() strategy.GlobalLimits {
	return strategy.GlobalLimits{
		MaxOrderSizeUSD: 2.0,
		MaxLeverage:     20,
		MaxDailyLossUSD: 5.0,
	}
}

// ─── YAML round-trip ─────────────────────────────────────────────────────────

func TestSpec_YAMLRoundTrip(t *testing.T) {
	s := loadFixture(t)

	b, err := yaml.Marshal(s)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var s2 strategy.StrategySpec
	if err := yaml.Unmarshal(b, &s2); err != nil {
		t.Fatalf("unmarshal 2nd: %v", err)
	}

	// ContentHash must be identical after round-trip
	if s.ContentHash() != s2.ContentHash() {
		t.Errorf("ContentHash changed after YAML round-trip:\n  before=%s\n  after=%s",
			s.ContentHash(), s2.ContentHash())
	}
}

// ─── ContentHash determinism ──────────────────────────────────────────────────

func TestSpec_ContentHash_Deterministic(t *testing.T) {
	s := loadFixture(t)
	h1 := s.ContentHash()
	h2 := s.ContentHash()
	if h1 != h2 {
		t.Errorf("ContentHash is not deterministic: %q vs %q", h1, h2)
	}
	if len(h1) != 64 {
		t.Errorf("ContentHash must be 64 hex chars, got %d: %q", len(h1), h1)
	}
}

func TestSpec_ContentHash_SensitiveToFieldChange(t *testing.T) {
	s := loadFixture(t)
	original := s.ContentHash()

	s.Entry.ConfidenceThreshold = 0.99
	changed := s.ContentHash()

	if original == changed {
		t.Error("ContentHash must change when entry.confidence_threshold changes")
	}
}

func TestSpec_ContentHash_ImmuneTo_SpecID(t *testing.T) {
	s := loadFixture(t)
	h1 := s.ContentHash()

	s.Metadata.SpecID = "aaaa" + h1[4:] // modify spec_id
	h2 := s.ContentHash()

	if h1 != h2 {
		t.Error("ContentHash must NOT change when only metadata.spec_id changes")
	}
}

func TestSpec_ContentHash_ImmuneTo_ValidationBlock(t *testing.T) {
	s := loadFixture(t)
	h1 := s.ContentHash()

	s.Validation.Verdict = "certified"
	s.Validation.AggPF = 1.45
	h2 := s.ContentHash()

	if h1 != h2 {
		t.Error("ContentHash must NOT change when only validation block changes")
	}
}

func TestSpec_ContentHash_SensitiveTo_TPOrder(t *testing.T) {
	s := loadFixture(t)
	h1 := s.ContentHash()

	// Reverse TP levels order
	levels := s.Exit.TPLevels
	for i, j := 0, len(levels)-1; i < j; i, j = i+1, j-1 {
		levels[i], levels[j] = levels[j], levels[i]
	}
	s.Exit.TPLevels = levels
	h2 := s.ContentHash()

	if h1 == h2 {
		t.Error("ContentHash must change when TP levels order is reversed")
	}
}

// ─── Validate ────────────────────────────────────────────────────────────────

func TestSpec_Validate_ValidFixture(t *testing.T) {
	s := loadFixture(t)
	if err := s.Validate(defaultLimits()); err != nil {
		t.Errorf("valid fixture failed Validate: %v", err)
	}
}

func TestSpec_Validate_NonInitialValidationBlock(t *testing.T) {
	s := loadFixture(t)
	s.Validation.Verdict = "certified"
	if err := s.Validate(defaultLimits()); err == nil {
		t.Error("expected error for non-initial validation block, got nil")
	}
}

func TestSpec_Validate_BadAPIVersion(t *testing.T) {
	s := loadFixture(t)
	s.APIVersion = "v2"
	if err := s.Validate(defaultLimits()); err == nil {
		t.Error("expected error for bad apiVersion")
	}
}

func TestSpec_Validate_BadKind(t *testing.T) {
	s := loadFixture(t)
	s.Kind = "NotASpec"
	if err := s.Validate(defaultLimits()); err == nil {
		t.Error("expected error for bad kind")
	}
}

func TestSpec_Validate_SpecIDMismatch(t *testing.T) {
	s := loadFixture(t)
	s.Metadata.SpecID = "0000000000000000000000000000000000000000000000000000000000000000"
	if err := s.Validate(defaultLimits()); err == nil {
		t.Error("expected error when spec_id does not match ContentHash")
	}
}

func TestSpec_Validate_TPLevelsNotSumming100(t *testing.T) {
	s := loadFixture(t)
	s.Exit.TPLevels = []config.TPLevel{
		{PricePct: 0.5, SizePct: 40},
		{PricePct: 1.2, SizePct: 40},
	}
	if err := s.Validate(defaultLimits()); err == nil {
		t.Error("expected error when tp_levels size_pct does not sum to 100")
	}
}

func TestSpec_Validate_TPLevelsDecreasingPrice(t *testing.T) {
	s := loadFixture(t)
	s.Exit.TPLevels = []config.TPLevel{
		{PricePct: 1.2, SizePct: 50},
		{PricePct: 0.6, SizePct: 50},
	}
	if err := s.Validate(defaultLimits()); err == nil {
		t.Error("expected error when tp_levels price_pct is not strictly increasing")
	}
}

func TestSpec_Validate_LeverageExceedsCap(t *testing.T) {
	s := loadFixture(t)
	s.RiskLimits.Leverage = 25
	if err := s.Validate(defaultLimits()); err == nil {
		t.Error("expected error when leverage exceeds global cap")
	}
}

func TestSpec_Validate_MLGateWithoutModelID(t *testing.T) {
	s := loadFixture(t)
	s.ML.GBMGateEnabled = true
	s.ML.ModelID = ""
	if err := s.Validate(defaultLimits()); err == nil {
		t.Error("expected error when gbm_gate_enabled=true but model_id is empty")
	}
}

func TestSpec_Validate_MLGateMinProbaTooLow(t *testing.T) {
	s := loadFixture(t)
	s.ML.GBMGateEnabled = true
	s.ML.ModelID = "some-model"
	s.ML.MinProba = 0.3 // below 0.5
	if err := s.Validate(defaultLimits()); err == nil {
		t.Error("expected error when min_proba is below 0.5")
	}
}

func TestSpec_Validate_FundingRateLimits_OnSpot(t *testing.T) {
	s := loadFixture(t)
	s.Scope.MarketType = "spot"
	s.RiskLimits.FundingRateLimits = &config.FundingRateLimits{MaxPositive: 0.01, MaxNegative: -0.01}
	if err := s.Validate(defaultLimits()); err == nil {
		t.Error("expected error for funding_rate_limits on non-futures market")
	}
}

func TestSpec_Validate_DuplicateTimeframes(t *testing.T) {
	s := loadFixture(t)
	s.Scope.Timeframes = []string{"5m", "5m", "15m"}
	if err := s.Validate(defaultLimits()); err == nil {
		t.Error("expected error for duplicate timeframes")
	}
}

func TestSpec_Validate_MACDFastGeqSlow(t *testing.T) {
	s := loadFixture(t)
	s.Indicators.MACDFast = 30
	s.Indicators.MACDSlow = 26
	if err := s.Validate(defaultLimits()); err == nil {
		t.Error("expected error when macd_fast >= macd_slow")
	}
}

// ─── ToConfigOverride ────────────────────────────────────────────────────────

func TestSpec_ToConfigOverride_TypedKeysDisjoint(t *testing.T) {
	s := loadFixture(t)
	floats, bools, ints, err := s.ToConfigOverride()
	if err != nil {
		t.Fatalf("ToConfigOverride error: %v", err)
	}

	// Keys must appear in exactly one map
	allKeys := make(map[string]int)
	for k := range floats {
		allKeys[k]++
	}
	for k := range bools {
		allKeys[k]++
	}
	for k := range ints {
		allKeys[k]++
	}
	for k, count := range allKeys {
		if count > 1 {
			t.Errorf("key %q appears in %d maps — must be in exactly one", k, count)
		}
	}

	// Core float keys must be present
	mustFloat := []string{"entry.confidence_threshold", "exit.take_profit_pct", "exit.stop_loss_pct"}
	for _, k := range mustFloat {
		if _, ok := floats[k]; !ok {
			t.Errorf("expected float key %q", k)
		}
	}

	// Core bool keys must be present
	mustBool := []string{"decision.macro_filter_enabled", "decision.counter_trend_filter_enabled", "decision.require_alignment"}
	for _, k := range mustBool {
		if _, ok := bools[k]; !ok {
			t.Errorf("expected bool key %q", k)
		}
	}

	// Core int keys must be present
	mustInt := []string{"indicators.lookback_candles"}
	for _, k := range mustInt {
		if _, ok := ints[k]; !ok {
			t.Errorf("expected int key %q", k)
		}
	}
}

func TestSpec_ToConfigOverride_PullbackMaps_TrendFollow(t *testing.T) {
	s := loadFixture(t)
	s.Entry.Type = "pullback"
	floats, _, _, err := s.ToConfigOverride()
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if v, ok := floats["bot.directional.mode_trend_follow"]; !ok || v <= 0 {
		t.Error("pullback entry type must emit bot.directional.mode_trend_follow > 0 in floats")
	}
}

func TestSpec_ToConfigOverride_MeanReversionMaps_AllowShort(t *testing.T) {
	s := loadFixture(t)
	s.Entry.Type = "mean_reversion"
	_, bools, _, err := s.ToConfigOverride()
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if !bools["decision.allow_short_selling"] {
		t.Error("mean_reversion entry type must set decision.allow_short_selling=true")
	}
}

func TestSpec_ToConfigOverride_MLKeys(t *testing.T) {
	s := loadFixture(t)
	floats, bools, _, err := s.ToConfigOverride()
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if !bools["ml.gbm_gate_enabled"] {
		t.Error("ml.gbm_gate_enabled must be in bools when GBMGateEnabled=true")
	}
	if _, ok := floats["ml.min_proba"]; !ok {
		t.Error("ml.min_proba must be in floats when GBMGateEnabled=true")
	}
}

// ─── JSON marshal / unmarshal ────────────────────────────────────────────────

func TestSpec_JSONRoundTrip(t *testing.T) {
	s := loadFixture(t)
	b, err := json.Marshal(s)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var s2 strategy.StrategySpec
	if err := json.Unmarshal(b, &s2); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if s.ContentHash() != s2.ContentHash() {
		t.Error("ContentHash changed after JSON round-trip")
	}
}

// ─── DefaultSpec + ApplyDefaults ─────────────────────────────────────────────

func TestSpec_DefaultSpec_Validates(t *testing.T) {
	d := strategy.DefaultSpec()
	if err := d.Validate(defaultLimits()); err != nil {
		t.Errorf("DefaultSpec() fails Validate: %v", err)
	}
}

func TestSpec_ApplyDefaults_InheritsExchange(t *testing.T) {
	d := strategy.DefaultSpec()
	s := &strategy.StrategySpec{
		APIVersion: strategy.APIVersion,
		Kind:       strategy.Kind,
		Metadata:   strategy.SpecMetadata{Name: "test"},
	}
	s.ApplyDefaults(d)
	if s.Scope.Exchange != "kucoin" {
		t.Errorf("expected exchange=kucoin inherited from default, got %q", s.Scope.Exchange)
	}
	if s.RiskLimits.MaxPositionSizeUSD == 0 {
		t.Error("risk_limits.max_position_size_usd must be inherited from default")
	}
}

func TestSpec_ApplyDefaults_DoesNotOverwrite(t *testing.T) {
	d := strategy.DefaultSpec()
	s := &strategy.StrategySpec{
		APIVersion: strategy.APIVersion,
		Kind:       strategy.Kind,
		Metadata:   strategy.SpecMetadata{Name: "test"},
		Scope:      strategy.SpecScope{Exchange: "binance"},
	}
	s.ApplyDefaults(d)
	if s.Scope.Exchange != "binance" {
		t.Errorf("ApplyDefaults must not overwrite existing exchange=binance, got %q", s.Scope.Exchange)
	}
}
