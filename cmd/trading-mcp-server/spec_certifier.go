package main

// spec_certifier.go — bridges the StrategySpec lifecycle to the walk-forward OOS
// validator. Specs submitted for validation (status=validating) are picked up
// here, validated out-of-sample, and certified (→validated) or rejected. This is
// the missing link that lets a draft Spec earn the "validated" status required to
// be promoted to live. Enabled dynamically via the config table.

import (
	"context"
	"encoding/json"
	"strconv"
	"time"

	backtestCore "github.com/rantipay/trading-mcp/internal/backtest"
	"github.com/rantipay/trading-mcp/internal/storage"
	"github.com/rantipay/trading-mcp/internal/strategy"
	"github.com/sirupsen/logrus"
)

// specRowIdentifier adapts a persisted StrategySpecRow to the minimal interface
// the OOS validator needs (SpecID / OOSWindowDays / ToOverrides).
type specRowIdentifier struct {
	specID    string
	overrides map[string]string
}

func (s specRowIdentifier) SpecID() string                 { return s.specID }
func (s specRowIdentifier) OOSWindowDays() int             { return 0 } // 0 → validator default
func (s specRowIdentifier) ToOverrides() map[string]string { return s.overrides }

// overridesFromRow flattens the persisted config_override ({floats,bools,ints})
// into the string map the validator applies over the baseline config.
func overridesFromRow(configOverride string) map[string]string {
	out := map[string]string{}
	if configOverride == "" {
		return out
	}
	var blob struct {
		Floats map[string]float64 `json:"floats"`
		Bools  map[string]bool    `json:"bools"`
		Ints   map[string]int     `json:"ints"`
	}
	if err := json.Unmarshal([]byte(configOverride), &blob); err != nil {
		return out
	}
	for k, v := range blob.Floats {
		out[k] = strconv.FormatFloat(v, 'f', -1, 64)
	}
	for k, v := range blob.Ints {
		out[k] = strconv.Itoa(v)
	}
	for k, v := range blob.Bools {
		out[k] = strconv.FormatBool(v)
	}
	return out
}

// primaryPairFromScope returns the first pair from a Spec's scope_json.
func primaryPairFromScope(scopeJSON string) string {
	var sc struct {
		Pairs []string `json:"pairs"`
	}
	_ = json.Unmarshal([]byte(scopeJSON), &sc)
	if len(sc.Pairs) > 0 {
		return sc.Pairs[0]
	}
	return ""
}

// runSpecCertificationLoop certifies specs in `validating` status every interval.
// enabledFn is read each tick (hot-reload) so an operator can pause it via config.
func runSpecCertificationLoop(
	ctx context.Context,
	repo *storage.StrategySpecRepository,
	valSvc *backtestCore.ValidationService,
	interval time.Duration,
	enabledFn func() bool,
	log *logrus.Logger,
) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if enabledFn != nil && !enabledFn() {
				continue
			}
			certifySpecsOnce(ctx, repo, valSvc, log)
		}
	}
}

// certifySpecsOnce processes every spec currently in `validating` status.
func certifySpecsOnce(
	ctx context.Context,
	repo *storage.StrategySpecRepository,
	valSvc *backtestCore.ValidationService,
	log *logrus.Logger,
) {
	rows, err := repo.ListByStatusAny(ctx, strategy.StatusValidating)
	if err != nil {
		log.WithError(err).Warn("spec-certifier: list validating failed")
		return
	}
	if len(rows) == 0 {
		return
	}
	log.WithField("count", len(rows)).Info("spec-certifier: validating specs to process")

	for _, row := range rows {
		pair := primaryPairFromScope(row.ScopeJSON)
		if pair == "" {
			log.WithField("spec_id", row.SpecID).Warn("spec-certifier: spec has no pair in scope — skipping")
			continue
		}

		ident := specRowIdentifier{specID: row.SpecID, overrides: overridesFromRow(row.ConfigOverride)}
		verdict, verr := valSvc.ValidateSpec(ctx, ident, pair)
		if verr != nil {
			// Transient (e.g. not enough candles yet) — leave validating, retry next tick.
			log.WithError(verr).WithFields(logrus.Fields{"spec_id": row.SpecID, "pair": pair}).
				Warn("spec-certifier: validation error — will retry")
			continue
		}

		block := strategy.ValidationBlock{
			AggPF:       verdict.AggProfitFactor,
			Consistency: verdict.Consistency,
			WorstDD:     verdict.WorstDrawdown,
			NTrades:     verdict.NTradesTotal,
			ExpiresAt:   time.Now().Add(14 * 24 * time.Hour).Format(time.RFC3339),
		}
		newStatus := strategy.StatusRejected
		block.Verdict = "rejected"
		if verdict.Pass {
			newStatus = strategy.StatusValidated
			block.Verdict = "certified"
		}

		if err := repo.SaveVerdictAny(ctx, row.SpecID, block); err != nil {
			log.WithError(err).WithField("spec_id", row.SpecID).Warn("spec-certifier: save verdict failed")
		}
		if err := repo.UpdateStatusAny(ctx, row.SpecID, newStatus); err != nil {
			log.WithError(err).WithField("spec_id", row.SpecID).Warn("spec-certifier: update status failed")
			continue
		}
		log.WithFields(logrus.Fields{
			"spec_id": row.SpecID, "pair": pair, "status": newStatus,
			"agg_pf": verdict.AggProfitFactor, "pass": verdict.Pass,
		}).Info("spec-certifier: spec verdict applied")
	}
}
