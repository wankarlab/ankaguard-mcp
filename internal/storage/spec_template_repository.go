package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/rantipay/trading-mcp/internal/strategy"
	"github.com/sirupsen/logrus"
)

// SpecTemplateRow is one seeded StrategySpec template served by
// GET /api/v1/specs/templates. spec_json holds a full StrategySpec document so
// the Spec Studio editor opens from real backend data (never a client constant).
type SpecTemplateRow struct {
	TemplateID  string `json:"template_id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Regime      string `json:"regime"`
	Kind        string `json:"kind"`
	SpecJSON    string `json:"spec_json"`
	SortOrder   int    `json:"sort_order"`
}

// SpecTemplateRepository reads/seeds the spec_templates table (migration 028).
type SpecTemplateRepository struct {
	db  *sql.DB
	log *logrus.Logger
}

// NewSpecTemplateRepository constructs a SpecTemplateRepository.
func NewSpecTemplateRepository(db *sql.DB, log *logrus.Logger) *SpecTemplateRepository {
	if log == nil {
		log = logrus.New()
	}
	return &SpecTemplateRepository{db: db, log: log}
}

// List returns all templates ordered by sort_order, name.
func (r *SpecTemplateRepository) List(ctx context.Context) ([]*SpecTemplateRow, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT template_id, name, description, regime, kind, spec_json, sort_order
		  FROM spec_templates
		 ORDER BY sort_order, name
	`)
	if err != nil {
		return nil, fmt.Errorf("list spec_templates: %w", err)
	}
	defer rows.Close()

	var out []*SpecTemplateRow
	for rows.Next() {
		t := &SpecTemplateRow{}
		if err := rows.Scan(&t.TemplateID, &t.Name, &t.Description, &t.Regime, &t.Kind, &t.SpecJSON, &t.SortOrder); err != nil {
			return nil, fmt.Errorf("scan spec_template: %w", err)
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// SeedIfEmpty inserts the canonical starter templates when the table is empty.
// Idempotent: skips when any row already exists. Templates are real StrategySpec
// documents built in Go (not duplicated SQL), so they stay valid as the schema
// evolves.
func (r *SpecTemplateRepository) SeedIfEmpty(ctx context.Context) error {
	var count int
	if err := r.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM spec_templates`).Scan(&count); err != nil {
		return fmt.Errorf("count spec_templates: %w", err)
	}
	if count > 0 {
		return nil
	}

	for i, t := range seedTemplates() {
		specJSON, err := json.Marshal(t.spec)
		if err != nil {
			return fmt.Errorf("marshal template %s: %w", t.id, err)
		}
		_, err = r.db.ExecContext(ctx, `
			INSERT INTO spec_templates (template_id, name, description, regime, kind, spec_json, sort_order)
			VALUES (?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(template_id) DO NOTHING
		`, t.id, t.spec.Metadata.Name, t.spec.Metadata.Description, t.regime, "humano", string(specJSON), i)
		if err != nil {
			return fmt.Errorf("seed template %s: %w", t.id, err)
		}
	}
	r.log.Info("storage: seeded spec_templates")
	return nil
}

type seedTemplate struct {
	id     string
	regime string
	spec   strategy.StrategySpec
}

// seedTemplates returns the canonical starter templates as real StrategySpec
// documents. Two opposite archetypes: a ranging mean-reversion and a trending
// breakout. Validation block is left empty (only the validator may write it).
func seedTemplates() []seedTemplate {
	return []seedTemplate{
		{
			id:     "tpl-ranging-mean-reversion",
			regime: "RANGING",
			spec: strategy.StrategySpec{
				APIVersion: "anka.trading/v1",
				Kind:       "StrategySpec",
				Metadata: strategy.SpecMetadata{
					Name:        "Ranging Mean-Reversion",
					Description: "Fade extremes inside a range: enter on RSI oversold/overbought with a tight stop. Starter template — tune before submitting.",
				},
				Scope: strategy.SpecScope{
					Pairs:      []string{"SEI-USDT"},
					Exchange:   "kucoin",
					MarketType: "futures",
					Timeframes: []string{"5m"},
				},
				Entry: strategy.EntryConfig{
					Type:                "mean_reversion",
					ConfidenceThreshold: 0.65,
					OrderType:           "limit",
				},
				Exit: strategy.ExitConfig{
					TakeProfitPct: 1.0,
					StopLossPct:   1.0,
				},
				RiskLimits: strategy.RiskLimits{
					MaxPositionSizeUSD: 2.0,
					Leverage:           3,
					MaxDailyLossUSD:    5.0,
				},
				Indicators: strategy.IndicatorConfig{
					RSIPeriod:       14,
					BollingerPeriod: 20,
					BollingerStddev: 2.0,
				},
				Regime: strategy.RegimeConfig{AllowedRegimes: []string{"RANGING"}},
			},
		},
		{
			id:     "tpl-trending-breakout",
			regime: "TRENDING",
			spec: strategy.StrategySpec{
				APIVersion: "anka.trading/v1",
				Kind:       "StrategySpec",
				Metadata: strategy.SpecMetadata{
					Name:        "Trending Breakout",
					Description: "Ride momentum: enter on a confirmed breakout aligned with the higher-timeframe trend, let winners run. Starter template — tune before submitting.",
				},
				Scope: strategy.SpecScope{
					Pairs:      []string{"BTC-USDT"},
					Exchange:   "kucoin",
					MarketType: "futures",
					Timeframes: []string{"15m"},
				},
				Entry: strategy.EntryConfig{
					Type:                "breakout",
					ConfidenceThreshold: 0.70,
					OrderType:           "market",
				},
				Exit: strategy.ExitConfig{
					TakeProfitPct: 2.5,
					StopLossPct:   1.0,
				},
				RiskLimits: strategy.RiskLimits{
					MaxPositionSizeUSD: 2.0,
					Leverage:           5,
					MaxDailyLossUSD:    5.0,
				},
				Indicators: strategy.IndicatorConfig{
					RSIPeriod: 14,
					EMAPeriod: 50,
					ATRPeriod: 14,
				},
				Regime: strategy.RegimeConfig{AllowedRegimes: []string{"TRENDING", "BREAKOUT"}},
			},
		},
	}
}
