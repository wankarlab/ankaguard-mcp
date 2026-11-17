package learning

import (
	"context"
	"fmt"
	"math"
	"sync"
	"time"

	"github.com/sirupsen/logrus"
)

const (
	wickVerifyDelay   = 15 * time.Minute
	wickCheckInterval = 5 * time.Minute
	wickMinSamples    = 10
	wickMinThreshold  = 1.0
	wickMaxThreshold  = 4.0
	wickAdjStep       = 0.2 // raise threshold when win rate < 50%
	wickAdjStepDown   = 0.1 // lower threshold when win rate > 70%
	wickDefaultThresh = 1.5
)

// PriceFetcher returns the current market price for a pair/symbol.
type PriceFetcher func(ctx context.Context, pair string) (float64, error)

// ThresholdPersister persists the learned wick/body ratio to the config store.
type ThresholdPersister func(ctx context.Context, threshold float64) error

// WickPatternStat tracks correctness of exits for one wick pattern label.
type WickPatternStat struct {
	Total   int
	Correct int // price moved in the predicted direction after wickVerifyDelay
}

func (s *WickPatternStat) WinRate() float64 {
	if s.Total == 0 {
		return 0
	}
	return float64(s.Correct) / float64(s.Total)
}

type pendingWickCheck struct {
	pair        string
	pattern     string  // "buyer_absorption" | "seller_absorption" | "doji_cluster"
	side        string  // position side being closed: "BUY" or "SELL"
	exitPrice   float64
	verifyAfter time.Time
}

// WickLearner tracks wick-triggered exit outcomes and auto-tunes the
// exit.wick_body_ratio_threshold dynamic parameter.
//
// Flow:
//  1. On wick-triggered close → RecordExit(pair, pattern, side, exitPrice)
//  2. Background goroutine fires every 5 min, verifies items past the 15-min mark
//  3. After wickMinSamples per pattern, adjusts the in-memory threshold and
//     persists it via ThresholdPersister (→ PostgreSQL ConfigStore)
type WickLearner struct {
	log           *logrus.Logger
	fetchPrice    PriceFetcher
	persistThresh ThresholdPersister

	mu        sync.Mutex
	stats     map[string]*WickPatternStat // pattern label → stats
	pending   []pendingWickCheck
	threshold float64 // current wick/body ratio threshold

	stopCh chan struct{}
}

// NewWickLearner creates a WickLearner. fetchPrice and persistThresh may be nil
// (learner still tracks stats but cannot verify prices or persist threshold).
func NewWickLearner(log *logrus.Logger, fetchPrice PriceFetcher, persistThresh ThresholdPersister) *WickLearner {
	if log == nil {
		log = logrus.New()
	}
	return &WickLearner{
		log:           log,
		fetchPrice:    fetchPrice,
		persistThresh: persistThresh,
		stats:         make(map[string]*WickPatternStat),
		threshold:     wickDefaultThresh,
		stopCh:        make(chan struct{}),
	}
}

// SetThreshold loads the initial threshold from the config store on startup.
func (wl *WickLearner) SetThreshold(v float64) {
	if v < wickMinThreshold || v > wickMaxThreshold {
		return
	}
	wl.mu.Lock()
	wl.threshold = v
	wl.mu.Unlock()
}

// GetThreshold returns the current wick/body ratio threshold.
func (wl *WickLearner) GetThreshold() float64 {
	wl.mu.Lock()
	defer wl.mu.Unlock()
	return wl.threshold
}

// GetStats returns a copy of accumulated pattern statistics.
func (wl *WickLearner) GetStats() map[string]WickPatternStat {
	wl.mu.Lock()
	defer wl.mu.Unlock()
	out := make(map[string]WickPatternStat, len(wl.stats))
	for k, v := range wl.stats {
		out[k] = *v
	}
	return out
}

// RecordExit queues a wick-triggered close for outcome verification after
// wickVerifyDelay. Only call when the exit reason starts with "wick:".
func (wl *WickLearner) RecordExit(pair, pattern, side string, exitPrice float64) {
	wl.mu.Lock()
	wl.pending = append(wl.pending, pendingWickCheck{
		pair:        pair,
		pattern:     pattern,
		side:        side,
		exitPrice:   exitPrice,
		verifyAfter: time.Now().Add(wickVerifyDelay),
	})
	wl.mu.Unlock()

	wl.log.WithFields(logrus.Fields{
		"pair":    pair,
		"pattern": pattern,
		"side":    side,
	}).Info("wick_learner: exit queued for verification in 15 min")
}

// Start launches the background verification goroutine.
func (wl *WickLearner) Start(ctx context.Context) {
	go wl.loop(ctx)
}

// Stop signals the background loop to exit.
func (wl *WickLearner) Stop() {
	select {
	case <-wl.stopCh:
	default:
		close(wl.stopCh)
	}
}

func (wl *WickLearner) loop(ctx context.Context) {
	ticker := time.NewTicker(wickCheckInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-wl.stopCh:
			return
		case <-ticker.C:
			wl.checkPending(ctx)
		}
	}
}

func (wl *WickLearner) checkPending(ctx context.Context) {
	if wl.fetchPrice == nil {
		return
	}
	wl.mu.Lock()
	now := time.Now()
	var ready, remaining []pendingWickCheck
	for _, p := range wl.pending {
		if now.After(p.verifyAfter) {
			ready = append(ready, p)
		} else {
			remaining = append(remaining, p)
		}
	}
	wl.pending = remaining
	wl.mu.Unlock()

	for _, item := range ready {
		price, err := wl.fetchPrice(ctx, item.pair)
		if err != nil {
			wl.log.WithError(err).WithField("pair", item.pair).Warn("wick_learner: price fetch failed; skipping verification")
			continue
		}
		correct := isCorrectReversal(item.pattern, item.side, item.exitPrice, price)

		wl.mu.Lock()
		if wl.stats[item.pattern] == nil {
			wl.stats[item.pattern] = &WickPatternStat{}
		}
		wl.stats[item.pattern].Total++
		if correct {
			wl.stats[item.pattern].Correct++
		}
		stat := *wl.stats[item.pattern]
		wl.mu.Unlock()

		wl.log.WithFields(logrus.Fields{
			"pair":          item.pair,
			"pattern":       item.pattern,
			"side":          item.side,
			"exit_price":    fmt.Sprintf("%.6f", item.exitPrice),
			"verify_price":  fmt.Sprintf("%.6f", price),
			"correct":       correct,
			"win_rate":      fmt.Sprintf("%.1f%%", stat.WinRate()*100),
			"total_samples": stat.Total,
		}).Info("wick_learner: exit outcome verified")

		if stat.Total >= wickMinSamples {
			wl.adjustThreshold(ctx, item.pattern, stat)
		}
	}
}

// isCorrectReversal returns true when the price at verification confirms that
// the wick pattern predicted the direction correctly.
func isCorrectReversal(pattern, side string, exitPrice, verifyPrice float64) bool {
	switch pattern {
	case "buyer_absorption":
		// Exiting a SHORT (SELL side) because buyers are absorbing → correct if price rose.
		return verifyPrice > exitPrice
	case "seller_absorption":
		// Exiting a LONG (BUY side) because sellers are absorbing → correct if price fell.
		return verifyPrice < exitPrice
	case "doji_cluster":
		// Any ≥0.5% move in either direction validates the pause/reversal signal.
		return math.Abs(verifyPrice-exitPrice)/exitPrice >= 0.005
	}
	return false
}

func (wl *WickLearner) adjustThreshold(ctx context.Context, pattern string, stat WickPatternStat) {
	wl.mu.Lock()
	cur := wl.threshold
	winRate := stat.WinRate()
	var next float64
	switch {
	case winRate < 0.50:
		// Too many false signals → raise threshold (less sensitive).
		next = math.Min(cur+wickAdjStep, wickMaxThreshold)
	case winRate > 0.70:
		// Most signals correct → can afford to be slightly more sensitive.
		next = math.Max(cur-wickAdjStepDown, wickMinThreshold)
	default:
		wl.mu.Unlock()
		return
	}
	if next == cur {
		wl.mu.Unlock()
		return
	}
	wl.threshold = next
	wl.mu.Unlock()

	wl.log.WithFields(logrus.Fields{
		"pattern":   pattern,
		"win_rate":  fmt.Sprintf("%.1f%%", winRate*100),
		"old_ratio": fmt.Sprintf("%.2f", cur),
		"new_ratio": fmt.Sprintf("%.2f", next),
	}).Info("wick_learner: wick/body ratio threshold auto-tuned")

	if wl.persistThresh != nil {
		if err := wl.persistThresh(ctx, next); err != nil {
			wl.log.WithError(err).Warn("wick_learner: failed to persist threshold update")
		}
	}
}
