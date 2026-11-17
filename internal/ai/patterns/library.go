package patterns

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"math"
	"sort"
	"strings"
	"sync"

	"github.com/sirupsen/logrus"
)

// PersistenceBackend is the optional interface a Library can use to persist
// pattern statistics. When nil, the Library operates 100% in memory
// (the original behavior).
type PersistenceBackend interface {
	Upsert(stats *PatternStats) error
	LoadAll() ([]*PatternStats, error)
}

type Library struct {
	log           *logrus.Logger
	patterns      map[string]*Pattern
	stats         map[string]*PatternStats
	outcomes      []*PatternOutcome
	minSamples    int
	minSuccess    float64
	patternLength int
	persistence   PersistenceBackend
	mu            sync.RWMutex
}

func NewLibrary(log *logrus.Logger) *Library {
	if log == nil {
		log = logrus.New()
	}
	return &Library{
		log:           log,
		patterns:      make(map[string]*Pattern),
		stats:         make(map[string]*PatternStats),
		outcomes:      make([]*PatternOutcome, 0, 1024),
		minSamples:    10,
		minSuccess:    0.65,
		patternLength: 5,
	}
}

func (l *Library) ExtractPattern(pair, timeframe string, candles []Candle) *Pattern {
	if len(candles) < 2 {
		return nil
	}

	n := len(candles)
	features := make([]float64, 0, n*3)

	priceMin := candles[0].Low
	priceMax := candles[0].High
	for _, c := range candles {
		if c.Low < priceMin {
			priceMin = c.Low
		}
		if c.High > priceMax {
			priceMax = c.High
		}
	}
	priceRange := priceMax - priceMin
	if priceRange <= 0 {
		priceRange = 1
	}

	var volSum float64
	for _, c := range candles {
		volSum += c.Volume
	}
	volAvg := volSum / float64(n)
	if volAvg <= 0 {
		volAvg = 1
	}

	for i := 0; i < n; i++ {
		c := candles[i]
		var pctChange float64
		if i > 0 && candles[i-1].Close != 0 {
			pctChange = (c.Close - candles[i-1].Close) / candles[i-1].Close
		}
		features = append(features, pctChange*100)

		var rangeRel float64
		if c.Close != 0 {
			rangeRel = (c.High - c.Low) / c.Close
		}
		features = append(features, rangeRel*100)

		var bodyPos float64
		hl := c.High - c.Low
		if hl > 0 {
			bodyPos = (c.Close - c.Low) / hl
		}
		features = append(features, bodyPos)

		volNorm := c.Volume / volAvg
		features = append(features, volNorm)
	}

	hash := hashFeatures(pair, timeframe, features)

	desc := describePattern(candles)

	return &Pattern{
		Hash:        hash,
		Pair:        pair,
		Timeframe:   timeframe,
		Length:      n,
		Description: desc,
		Features:    features,
	}
}

func hashFeatures(pair, timeframe string, features []float64) string {
	parts := make([]string, 0, len(features)+2)
	parts = append(parts, pair, timeframe)
	for _, f := range features {
		q := math.Round(f*10) / 10
		parts = append(parts, fmt.Sprintf("%.1f", q))
	}
	joined := strings.Join(parts, "|")
	sum := sha256.Sum256([]byte(joined))
	return hex.EncodeToString(sum[:])[:16]
}

func describePattern(candles []Candle) string {
	if len(candles) == 0 {
		return ""
	}
	first := candles[0].Close
	last := candles[len(candles)-1].Close
	var dir string
	switch {
	case last > first*1.005:
		dir = "uptrend"
	case last < first*0.995:
		dir = "downtrend"
	default:
		dir = "sideways"
	}
	bull, bear := 0, 0
	for _, c := range candles {
		if c.Close > c.Open {
			bull++
		} else if c.Close < c.Open {
			bear++
		}
	}
	return fmt.Sprintf("%d-candle %s (%d bull, %d bear)", len(candles), dir, bull, bear)
}

func (l *Library) MatchPattern(pattern *Pattern) *PatternStats {
	if pattern == nil {
		return nil
	}
	l.mu.RLock()
	defer l.mu.RUnlock()
	st, ok := l.stats[pattern.Hash]
	if !ok {
		return nil
	}
	if st.SampleSize < l.minSamples {
		return nil
	}
	c := *st
	return &c
}

func (l *Library) RecordOutcome(outcome *PatternOutcome) {
	if outcome == nil || outcome.PatternHash == "" {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()

	l.outcomes = append(l.outcomes, outcome)

	st, ok := l.stats[outcome.PatternHash]
	if !ok {
		pair := ""
		if p, exists := l.patterns[outcome.PatternHash]; exists {
			pair = p.Pair
		}
		st = &PatternStats{
			Hash: outcome.PatternHash,
			Pair: pair,
		}
		l.stats[outcome.PatternHash] = st
	}

	prevTotalChange := st.AvgPriceChange * float64(st.SampleSize)
	st.SampleSize++
	switch outcome.Resolution {
	case "BULLISH":
		st.BullishCount++
	case "BEARISH":
		st.BearishCount++
	default:
		st.NeutralCount++
	}
	st.AvgPriceChange = (prevTotalChange + outcome.PriceChange) / float64(st.SampleSize)
	st.LastSeen = outcome.DetectedAt

	dominant := st.BullishCount
	if st.BearishCount > dominant {
		dominant = st.BearishCount
	}
	if st.NeutralCount > dominant {
		dominant = st.NeutralCount
	}
	if st.SampleSize > 0 {
		st.SuccessRate = float64(dominant) / float64(st.SampleSize)
	}

	switch {
	case st.SampleSize < l.minSamples:
		st.Reliability = "INSUFFICIENT"
	case st.SuccessRate >= 0.75:
		st.Reliability = "HIGH"
	case st.SuccessRate >= l.minSuccess:
		st.Reliability = "MEDIUM"
	default:
		st.Reliability = "LOW"
	}

	l.log.WithFields(logrus.Fields{
		"hash":         outcome.PatternHash,
		"sample_size":  st.SampleSize,
		"success_rate": st.SuccessRate,
		"reliability":  st.Reliability,
	}).Debug("pattern outcome recorded")

	// Async persistence: take a snapshot under the lock to avoid races,
	// then upsert in a goroutine. If no backend configured, this is a no-op.
	if l.persistence != nil {
		snapshot := *st
		backend := l.persistence
		log := l.log
		go func(s PatternStats) {
			if err := backend.Upsert(&s); err != nil {
				log.WithError(err).WithField("hash", s.Hash).Warn("pattern library: upsert failed")
			}
		}(snapshot)
	}
}

// EnablePersistence wires an optional persistence backend into the Library.
// When called at startup, it seeds the in-memory cache from any patterns
// previously stored. Subsequent RecordOutcome calls will asynchronously
// upsert through the backend. Pass nil to detach a previously installed
// backend (no seeding occurs).
func (l *Library) EnablePersistence(backend PersistenceBackend) error {
	if backend == nil {
		l.mu.Lock()
		l.persistence = nil
		l.mu.Unlock()
		return nil
	}

	seeded, err := backend.LoadAll()
	if err != nil {
		return fmt.Errorf("pattern library: load persisted patterns: %w", err)
	}

	l.mu.Lock()
	l.persistence = backend
	for _, s := range seeded {
		if s == nil || s.Hash == "" {
			continue
		}
		c := *s
		l.stats[s.Hash] = &c
	}
	count := len(seeded)
	l.mu.Unlock()

	l.log.Infof("pattern library: loaded %d persisted patterns", count)
	return nil
}

func (l *Library) RegisterPattern(pattern *Pattern) {
	if pattern == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.patterns[pattern.Hash] = pattern
}

func (l *Library) GetSignal(pattern *Pattern) (string, float64, string, bool) {
	if pattern == nil {
		return "WAIT", 0, "nil pattern", false
	}
	l.mu.RLock()
	defer l.mu.RUnlock()

	st, ok := l.stats[pattern.Hash]
	if !ok || st.SampleSize < l.minSamples {
		return "WAIT", 0, "insufficient samples", false
	}
	if st.SuccessRate < l.minSuccess {
		return "WAIT", 0, "low reliability", false
	}

	bull := st.BullishCount
	bear := st.BearishCount
	neutralHalf := st.NeutralCount / 2

	if bull > bear+neutralHalf {
		return "BUY", st.SuccessRate, fmt.Sprintf("bullish pattern %.1f%% success over %d samples", st.SuccessRate*100, st.SampleSize), true
	}
	if bear > bull+neutralHalf {
		return "SELL", st.SuccessRate, fmt.Sprintf("bearish pattern %.1f%% success over %d samples", st.SuccessRate*100, st.SampleSize), true
	}
	return "WAIT", 0, "no clear bias", false
}

func (l *Library) GetTopPatterns(pair string, n int) []*PatternStats {
	l.mu.RLock()
	defer l.mu.RUnlock()

	filtered := make([]*PatternStats, 0)
	for _, st := range l.stats {
		if st.Pair != pair {
			continue
		}
		if st.SampleSize < l.minSamples {
			continue
		}
		c := *st
		filtered = append(filtered, &c)
	}

	sort.Slice(filtered, func(i, j int) bool {
		if filtered[i].SuccessRate != filtered[j].SuccessRate {
			return filtered[i].SuccessRate > filtered[j].SuccessRate
		}
		return filtered[i].SampleSize > filtered[j].SampleSize
	})

	if n > 0 && len(filtered) > n {
		filtered = filtered[:n]
	}
	return filtered
}
