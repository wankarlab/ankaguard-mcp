package services

import (
	"fmt"
	"math"
	"sort"
	"sync"
	"time"

	"github.com/sirupsen/logrus"
)

type AllocationMode string

const (
	AllocationEqual    AllocationMode = "equal"
	AllocationWeighted AllocationMode = "weighted"
	AllocationDynamic  AllocationMode = "dynamic"
)

type PairOpportunity struct {
	Pair             string
	OpportunityScore float64
	Volatility       float64
	Confidence       float64
	WinRate          float64
	AvgWin           float64
	AvgLoss          float64
	BTCCorrelation   float64
	HasOpenPosition  bool
}

type AllocationResult struct {
	Pair         string
	AllocatedUSD float64
	Reasoning    string
	AppliedCaps  []string
}

type AllocationDelta struct {
	Pair        string
	PreviousUSD float64
	NewUSD      float64
	DeltaUSD    float64
	Action      string
}

type AllocatorConfig struct {
	Mode                  AllocationMode
	TotalCapitalUSD       float64
	MinPerBotUSD          float64
	MaxPerBotUSD          float64
	ReservePercent        float64
	MaxCorrelatedExposure float64
	MinPairsForFullDeploy int
}

type CapitalAllocator struct {
	config *AllocatorConfig
	log    *logrus.Logger
	mu     sync.RWMutex

	lastAllocations map[string]*AllocationResult
	lastRebalance   time.Time
}

func NewCapitalAllocator(config *AllocatorConfig, log *logrus.Logger) *CapitalAllocator {
	if log == nil {
		log = logrus.New()
	}
	if config == nil {
		config = &AllocatorConfig{
			Mode:                  AllocationWeighted,
			TotalCapitalUSD:       0,
			MinPerBotUSD:          5.0,
			MaxPerBotUSD:          30.0,
			ReservePercent:        0.10,
			MaxCorrelatedExposure: 0.50,
			MinPairsForFullDeploy: 3,
		}
	}
	return &CapitalAllocator{
		config:          config,
		log:             log,
		lastAllocations: make(map[string]*AllocationResult),
	}
}

func (ca *CapitalAllocator) Allocate(opportunities []*PairOpportunity) map[string]*AllocationResult {
	ca.mu.Lock()
	defer ca.mu.Unlock()

	result := make(map[string]*AllocationResult)

	filtered := make([]*PairOpportunity, 0, len(opportunities))
	for _, o := range opportunities {
		if o == nil {
			continue
		}
		if o.OpportunityScore <= 0 {
			continue
		}
		filtered = append(filtered, o)
	}

	if len(filtered) == 0 {
		ca.log.Info("CapitalAllocator: no valid opportunities, returning empty allocation")
		ca.lastAllocations = result
		ca.lastRebalance = time.Now()
		return result
	}

	deployable := ca.config.TotalCapitalUSD * (1 - ca.config.ReservePercent)
	if len(filtered) < ca.config.MinPairsForFullDeploy {
		deployable = deployable * 0.5
		ca.log.WithFields(logrus.Fields{
			"pairs":             len(filtered),
			"min_for_full":      ca.config.MinPairsForFullDeploy,
			"deployable_capped": deployable,
		}).Info("CapitalAllocator: limited pairs, halving deployable capital")
	}

	if deployable <= 0 {
		ca.log.Warn("CapitalAllocator: deployable capital <= 0, skipping allocation")
		ca.lastAllocations = result
		ca.lastRebalance = time.Now()
		return result
	}

	// Concentrate capital on the highest-scoring pairs. Never spread across more
	// pairs than the deployable capital can fund at MinPerBotUSD — otherwise
	// every pair's share falls below the minimum and is zeroed, which silently
	// killed ALL allocations once the universe widened from ~4 to ~50 candidates
	// (deployable/N < MinPerBotUSD for large N). Keep the best maxFundable pairs.
	if ca.config.MinPerBotUSD > 0 && len(filtered) > 1 {
		maxFundable := int(deployable / ca.config.MinPerBotUSD)
		if maxFundable < 1 {
			maxFundable = 1
		}
		if len(filtered) > maxFundable {
			sort.Slice(filtered, func(i, j int) bool {
				return filtered[i].OpportunityScore > filtered[j].OpportunityScore
			})
			ca.log.WithFields(logrus.Fields{
				"candidates":   len(filtered),
				"max_fundable": maxFundable,
				"deployable":   deployable,
				"min_per_bot":  ca.config.MinPerBotUSD,
			}).Info("CapitalAllocator: capping candidates to what capital can fund at the per-bot minimum")
			filtered = filtered[:maxFundable]
		}
	}

	weights := ca.computeWeights(filtered)

	if ca.config.Mode == AllocationDynamic {
		weights = ca.applyCorrelationAdjustment(filtered, weights)
	}

	for _, op := range filtered {
		w := weights[op.Pair]
		rawUSD := w * deployable
		caps := []string{}

		if rawUSD > ca.config.MaxPerBotUSD {
			rawUSD = ca.config.MaxPerBotUSD
			caps = append(caps, "max_per_bot")
		}
		if rawUSD < ca.config.MinPerBotUSD {
			rawUSD = 0
			caps = append(caps, "below_min_no_alloc")
		}

		reasoning := fmt.Sprintf("weight=%.2f%%, raw=$%.2f, final=$%.2f",
			w*100, w*deployable, rawUSD)

		result[op.Pair] = &AllocationResult{
			Pair:         op.Pair,
			AllocatedUSD: rawUSD,
			Reasoning:    reasoning,
			AppliedCaps:  caps,
		}

		ca.log.WithFields(logrus.Fields{
			"pair":      op.Pair,
			"weight":    w,
			"allocated": rawUSD,
			"caps":      caps,
		}).Debug("CapitalAllocator: pair allocated")
	}

	ca.lastAllocations = result
	ca.lastRebalance = time.Now()

	ca.log.WithFields(logrus.Fields{
		"mode":       ca.config.Mode,
		"total":      ca.config.TotalCapitalUSD,
		"deployable": deployable,
		"pairs":      len(result),
	}).Info("CapitalAllocator: allocation completed")

	return result
}

func (ca *CapitalAllocator) computeWeights(ops []*PairOpportunity) map[string]float64 {
	weights := make(map[string]float64, len(ops))
	n := len(ops)

	switch ca.config.Mode {
	case AllocationEqual:
		w := 1.0 / float64(n)
		for _, o := range ops {
			weights[o.Pair] = w
		}

	case AllocationWeighted:
		total := 0.0
		for _, o := range ops {
			total += o.OpportunityScore
		}
		if total <= 0 {
			w := 1.0 / float64(n)
			for _, o := range ops {
				weights[o.Pair] = w
			}
			return weights
		}
		for _, o := range ops {
			weights[o.Pair] = o.OpportunityScore / total
		}

	case AllocationDynamic:
		raw := make(map[string]float64, n)
		totalRaw := 0.0
		for _, o := range ops {
			var kelly float64
			if o.WinRate > 0 && o.AvgWin > 0 && o.AvgLoss > 0 {
				R := o.AvgWin / o.AvgLoss
				kelly = (o.WinRate*(1+R) - 1) / R
				kelly = math.Max(0, math.Min(0.25, kelly))
				kelly = kelly * 0.5 * o.Confidence
			} else {
				kelly = 0.02 * o.Confidence
			}
			rw := kelly * (o.OpportunityScore / 100.0)
			raw[o.Pair] = rw
			totalRaw += rw
		}
		if totalRaw <= 0 {
			w := 1.0 / float64(n)
			for _, o := range ops {
				weights[o.Pair] = w
			}
			return weights
		}
		for _, o := range ops {
			weights[o.Pair] = raw[o.Pair] / totalRaw
		}

	default:
		w := 1.0 / float64(n)
		for _, o := range ops {
			weights[o.Pair] = w
		}
	}

	return weights
}

func (ca *CapitalAllocator) applyCorrelationAdjustment(ops []*PairOpportunity, weights map[string]float64) map[string]float64 {
	groups := map[string][]string{
		"btc_correlated":  {},
		"neutral":         {},
		"anti_correlated": {},
	}
	for _, o := range ops {
		switch {
		case o.BTCCorrelation > 0.7:
			groups["btc_correlated"] = append(groups["btc_correlated"], o.Pair)
		case o.BTCCorrelation < -0.7:
			groups["anti_correlated"] = append(groups["anti_correlated"], o.Pair)
		default:
			groups["neutral"] = append(groups["neutral"], o.Pair)
		}
	}

	cap := ca.config.MaxCorrelatedExposure
	if cap <= 0 || cap >= 1 {
		return weights
	}

	groupNames := []string{"btc_correlated", "neutral", "anti_correlated"}
	sort.Strings(groupNames)

	totalRecovered := 0.0
	overflowGroups := map[string]bool{}

	for _, gname := range groupNames {
		members := groups[gname]
		if len(members) == 0 {
			continue
		}
		sum := 0.0
		for _, p := range members {
			sum += weights[p]
		}
		if sum > cap {
			scale := cap / sum
			recovered := sum - cap
			for _, p := range members {
				weights[p] = weights[p] * scale
			}
			totalRecovered += recovered
			overflowGroups[gname] = true
			ca.log.WithFields(logrus.Fields{
				"group":     gname,
				"sum":       sum,
				"cap":       cap,
				"recovered": recovered,
			}).Info("CapitalAllocator: group exceeded correlation cap, scaled down")
		}
	}

	if totalRecovered <= 0 {
		return weights
	}

	eligibleSum := 0.0
	for _, gname := range groupNames {
		if overflowGroups[gname] {
			continue
		}
		for _, p := range groups[gname] {
			eligibleSum += weights[p]
		}
	}

	if eligibleSum <= 0 {
		// All groups overflowed (e.g. all pairs are BTCCorrelation≈0 → all land in
		// "neutral"). No eligible recipient exists for the recovered capital, so we
		// normalize the scaled weights back to sum=1.0 proportionally instead of
		// discarding the recovered capital — otherwise every pair ends up under
		// MinPerBotUSD and no bots are ever spawned.
		currentSum := 0.0
		for _, w := range weights {
			currentSum += w
		}
		if currentSum > 0 {
			for p := range weights {
				weights[p] = weights[p] / currentSum
			}
		}
		return weights
	}

	for _, gname := range groupNames {
		if overflowGroups[gname] {
			continue
		}
		for _, p := range groups[gname] {
			share := weights[p] / eligibleSum
			weights[p] += totalRecovered * share
		}
	}

	return weights
}

func (ca *CapitalAllocator) Rebalance(opportunities []*PairOpportunity) map[string]*AllocationDelta {
	ca.mu.RLock()
	previous := make(map[string]*AllocationResult, len(ca.lastAllocations))
	for k, v := range ca.lastAllocations {
		copy := *v
		previous[k] = &copy
	}
	ca.mu.RUnlock()

	newAlloc := ca.Allocate(opportunities)

	deltas := make(map[string]*AllocationDelta)

	seen := map[string]bool{}

	for pair, na := range newAlloc {
		seen[pair] = true
		prevUSD := 0.0
		if pa, ok := previous[pair]; ok {
			prevUSD = pa.AllocatedUSD
		}
		deltaUSD := na.AllocatedUSD - prevUSD
		var action string
		switch {
		case prevUSD == 0 && na.AllocatedUSD > 0:
			action = "ADD"
		case prevUSD > 0 && na.AllocatedUSD == 0:
			action = "REMOVE"
		case prevUSD > 0 && math.Abs(deltaUSD)/prevUSD > 0.20:
			if deltaUSD > 0 {
				action = "INCREASE"
			} else {
				action = "DECREASE"
			}
		default:
			action = "MAINTAIN"
		}
		deltas[pair] = &AllocationDelta{
			Pair:        pair,
			PreviousUSD: prevUSD,
			NewUSD:      na.AllocatedUSD,
			DeltaUSD:    deltaUSD,
			Action:      action,
		}
	}

	for pair, pa := range previous {
		if seen[pair] {
			continue
		}
		deltas[pair] = &AllocationDelta{
			Pair:        pair,
			PreviousUSD: pa.AllocatedUSD,
			NewUSD:      0,
			DeltaUSD:    -pa.AllocatedUSD,
			Action:      "REMOVE",
		}
	}

	ca.log.WithField("changes", len(deltas)).Info("CapitalAllocator: rebalance computed")
	return deltas
}

func (ca *CapitalAllocator) SetMode(mode AllocationMode) {
	ca.mu.Lock()
	defer ca.mu.Unlock()
	ca.config.Mode = mode
	ca.log.WithField("mode", mode).Info("CapitalAllocator: mode updated")
}

func (ca *CapitalAllocator) GetLastAllocations() map[string]*AllocationResult {
	ca.mu.RLock()
	defer ca.mu.RUnlock()
	out := make(map[string]*AllocationResult, len(ca.lastAllocations))
	for k, v := range ca.lastAllocations {
		copy := *v
		out[k] = &copy
	}
	return out
}

func (ca *CapitalAllocator) SetTotalCapital(usd float64) {
	ca.mu.Lock()
	defer ca.mu.Unlock()
	ca.config.TotalCapitalUSD = usd
	ca.log.WithField("total_capital_usd", usd).Info("CapitalAllocator: total capital updated")
}
