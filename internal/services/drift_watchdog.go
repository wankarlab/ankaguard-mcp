package services

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/sirupsen/logrus"
)

// DriftAlert is emitted when a live Spec diverges too far from its shadow baseline.
// BB-6: backed by trade-by-trade reconciliation (SEC-10 dependency).
type DriftAlert struct {
	SpecID        string
	Pair          string
	LivePF        float64
	ShadowPF      float64
	DriftPct      float64   // (LivePF - ShadowPF) / ShadowPF × 100
	CalibHitRate  float64   // calibrator hit-rate [0,1]
	TriggeredAt   time.Time
	Acknowledged  bool
	AcknowledgeBy string
}

// DriftWatchdog monitors live Specs for PF drift vs shadow baseline.
// BB-6: DOES NOT exist as a live watchdog today; this is the implementation stub
// that can be wired once SEC-10 (trade-by-trade reconciliation) is complete.
type DriftWatchdog struct {
	mu     sync.RWMutex
	alerts map[string]*DriftAlert // key: specID+":"+pair
	log    *logrus.Logger

	// thresholds
	maxPFDropPct     float64 // default 30%: if live_pf drops >30% vs shadow → alert
	minCalibHitRate  float64 // default 0.5: below this → alert
}

// NewDriftWatchdog creates a DriftWatchdog.
// SEC-10 dependency: feed it reconciled PnL data once that is implemented.
func NewDriftWatchdog(log *logrus.Logger) *DriftWatchdog {
	return &DriftWatchdog{
		alerts:          make(map[string]*DriftAlert),
		log:             log,
		maxPFDropPct:    30.0,
		minCalibHitRate: 0.5,
	}
}

// Check evaluates whether the live Spec has drifted from its shadow baseline.
// Emits a DriftAlert if live_pf dropped >30% vs shadow or calibHitRate <0.5.
// BB-6: this is a stub — full implementation requires SEC-10 reconciled PnL.
func (w *DriftWatchdog) Check(ctx context.Context, specID, pair string, livePF, shadowPF, calibHitRate float64) *DriftAlert {
	_ = ctx

	driftPct := 0.0
	if shadowPF > 0 {
		driftPct = (livePF - shadowPF) / shadowPF * 100
	}

	hasDrift := (-driftPct) > w.maxPFDropPct || calibHitRate < w.minCalibHitRate
	if !hasDrift {
		return nil
	}

	alert := &DriftAlert{
		SpecID:       specID,
		Pair:         pair,
		LivePF:       livePF,
		ShadowPF:     shadowPF,
		DriftPct:     driftPct,
		CalibHitRate: calibHitRate,
		TriggeredAt:  time.Now(),
	}

	key := specID + ":" + pair
	w.mu.Lock()
	w.alerts[key] = alert
	w.mu.Unlock()

	w.log.WithFields(logrus.Fields{
		"spec_id":        specID,
		"pair":           pair,
		"live_pf":        fmt.Sprintf("%.3f", livePF),
		"shadow_pf":      fmt.Sprintf("%.3f", shadowPF),
		"drift_pct":      fmt.Sprintf("%.1f%%", driftPct),
		"calib_hit_rate": fmt.Sprintf("%.2f", calibHitRate),
	}).Warn("BB-6: DriftAlert triggered — live Spec diverged from shadow baseline")

	return alert
}

// GetAlert returns the current DriftAlert for (specID, pair), or nil if none.
func (w *DriftWatchdog) GetAlert(specID, pair string) *DriftAlert {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return w.alerts[specID+":"+pair]
}

// Acknowledge marks a DriftAlert as reviewed by the given operator.
func (w *DriftWatchdog) Acknowledge(specID, pair, operatorID string) error {
	key := specID + ":" + pair
	w.mu.Lock()
	defer w.mu.Unlock()
	alert, ok := w.alerts[key]
	if !ok {
		return fmt.Errorf("no drift alert for spec=%s pair=%s", specID, pair)
	}
	alert.Acknowledged = true
	alert.AcknowledgeBy = operatorID
	return nil
}

// ActiveAlerts returns all unacknowledged drift alerts.
func (w *DriftWatchdog) ActiveAlerts() []*DriftAlert {
	w.mu.RLock()
	defer w.mu.RUnlock()
	var out []*DriftAlert
	for _, a := range w.alerts {
		if !a.Acknowledged {
			out = append(out, a)
		}
	}
	return out
}
