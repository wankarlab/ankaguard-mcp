package llm

import (
	"context"
	"fmt"
	"maps"
	"strings"
	"sync"
	"time"

	dynamic "github.com/rantipay/trading-mcp/internal/config/dynamic"
	"github.com/sirupsen/logrus"
)

// ConsensusEngine queries multiple LLM providers in parallel and votes on the result.
type ConsensusEngine struct {
	manager      *ProviderManager
	minProviders int
	timeout      time.Duration
	log          *logrus.Logger
}

// ConsensusResult is the aggregated outcome of a parallel multi-provider call.
type ConsensusResult struct {
	Action              string
	Confidence          float64
	AgreementRatio      float64
	Responses           []*LLMAnalysis
	WinningProviders    []string
	DissentingProviders []string
	HasConsensus        bool
}

// providerVote is the internal per-provider result carried through the worker channel.
type providerVote struct {
	provider ProviderType
	analysis *LLMAnalysis
	err      error
}

// NewConsensusEngine creates a new consensus engine. If minProviders <= 0, defaults to 2.
func NewConsensusEngine(manager *ProviderManager, minProviders int, log *logrus.Logger) *ConsensusEngine {
	if log == nil {
		log = logrus.New()
	}
	if minProviders <= 0 {
		minProviders = 2
	}
	// TODO(TC-6.1): missing manifest key llm.consensus.timeout — read dynamically once added
	return &ConsensusEngine{
		manager:      manager,
		minProviders: minProviders,
		timeout:      30 * time.Second,
		log:          log,
	}
}

// Analyze fans the request out to every configured provider in parallel, parses each
// response into LLMAnalysis, and votes on the action. The winning Confidence is the
// average of the winners' confidences scaled by the agreement ratio.
func (ce *ConsensusEngine) Analyze(ctx context.Context, request *LLMRequest) (*ConsensusResult, error) {
	if ce.manager == nil {
		return nil, fmt.Errorf("consensus engine: provider manager is nil")
	}

	ce.manager.mu.RLock()
	providers := make(map[ProviderType]Provider, len(ce.manager.providers))
	maps.Copy(providers, ce.manager.providers)
	ce.manager.mu.RUnlock()

	if len(providers) == 0 {
		return nil, fmt.Errorf("consensus engine: no providers configured")
	}

	callCtx, cancel := context.WithTimeout(ctx, ce.timeout)
	defer cancel()

	votes := make(chan providerVote, len(providers))
	var wg sync.WaitGroup

	for pt, provider := range providers {
		wg.Add(1)
		go func(providerType ProviderType, p Provider) {
			defer wg.Done()

			start := time.Now()
			resp, err := p.Call(callCtx, request)
			latency := time.Since(start)

			if err != nil {
				ce.log.WithError(err).WithFields(logrus.Fields{
					"provider": providerType,
					"latency":  latency,
				}).Warn("Consensus: provider call failed")
				votes <- providerVote{provider: providerType, err: err}
				return
			}
			resp.Latency = latency

			analysis, parseErr := ParseMarketAnalysis(resp)
			if parseErr != nil {
				ce.log.WithError(parseErr).WithField("provider", providerType).Warn("Consensus: parse failed")
				votes <- providerVote{provider: providerType, err: parseErr}
				return
			}
			if analysis.Response != nil {
				analysis.Response.Provider = string(providerType)
			}
			votes <- providerVote{provider: providerType, analysis: analysis}
		}(pt, provider)
	}

	wg.Wait()
	close(votes)

	result := &ConsensusResult{
		Responses:           make([]*LLMAnalysis, 0, len(providers)),
		WinningProviders:    []string{},
		DissentingProviders: []string{},
	}

	actionCounts := make(map[string]int)
	actionConfidences := make(map[string][]float64)
	actionProviders := make(map[string][]string)

	successful := 0
	for v := range votes {
		if v.err != nil || v.analysis == nil {
			continue
		}
		successful++
		action := strings.ToUpper(strings.TrimSpace(v.analysis.Action))
		if action == "" {
			action = "WAIT"
		}
		result.Responses = append(result.Responses, v.analysis)
		actionCounts[action]++
		actionConfidences[action] = append(actionConfidences[action], v.analysis.Confidence)
		actionProviders[action] = append(actionProviders[action], string(v.provider))
	}

	effectiveMin := ce.minProviders
	if effectiveMin > successful && successful >= 1 {
		effectiveMin = successful
		ce.log.WithFields(logrus.Fields{
			"configured_min": ce.minProviders,
			"effective_min":  effectiveMin,
			"successful":     successful,
		}).Warn("LLM consensus: degraded mode — fewer providers than configured minimum")
	}

	if successful < effectiveMin {
		ce.log.WithFields(logrus.Fields{
			"successful":    successful,
			"min_providers": ce.minProviders,
		}).Warn("Consensus: insufficient successful providers")
		result.HasConsensus = false
		result.Action = "WAIT"
		return result, nil
	}

	// Pick winner: action with most votes; ties yield no consensus.
	winningAction := ""
	winningCount := 0
	tied := false
	for action, count := range actionCounts {
		if count > winningCount {
			winningAction = action
			winningCount = count
			tied = false
		} else if count == winningCount {
			tied = true
		}
	}

	result.AgreementRatio = float64(winningCount) / float64(successful)

	if tied || winningCount < effectiveMin {
		ce.log.WithFields(logrus.Fields{
			"action_counts": actionCounts,
			"successful":    successful,
			"min_providers": ce.minProviders,
			"tied":          tied,
		}).Info("Consensus: no agreement reached")
		result.HasConsensus = false
		result.Action = "WAIT"
		for action, provs := range actionProviders {
			if action == winningAction && !tied {
				result.WinningProviders = append(result.WinningProviders, provs...)
			} else {
				result.DissentingProviders = append(result.DissentingProviders, provs...)
			}
		}
		return result, nil
	}

	// Average the winners' confidences and penalize by agreement ratio.
	var sum float64
	for _, c := range actionConfidences[winningAction] {
		sum += c
	}
	avgConfidence := sum / float64(len(actionConfidences[winningAction]))

	result.Action = winningAction
	result.Confidence = avgConfidence * result.AgreementRatio
	result.HasConsensus = true

	if effectiveMin < ce.minProviders {
		// llm.consensus.degraded_penalty (default 0.85) — hot-reloadable via dynamic config
		degradedPenalty := 0.85
		if snap := dynamic.SnapshotFromContext(context.Background()); snap != nil {
			degradedPenalty = snap.Float("llm.consensus.degraded_penalty")
		}
		result.Confidence *= degradedPenalty
	}
	result.WinningProviders = actionProviders[winningAction]
	for action, provs := range actionProviders {
		if action != winningAction {
			result.DissentingProviders = append(result.DissentingProviders, provs...)
		}
	}

	ce.log.WithFields(logrus.Fields{
		"action":            result.Action,
		"confidence":        result.Confidence,
		"agreement_ratio":   result.AgreementRatio,
		"winners":           result.WinningProviders,
		"dissenters":        result.DissentingProviders,
		"successful_calls":  successful,
		"total_providers":   len(providers),
	}).Info("Consensus reached")

	return result, nil
}
