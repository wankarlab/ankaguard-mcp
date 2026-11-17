package adapters

import (
	"fmt"

	"github.com/rantipay/trading-mcp/internal/ai"
	"github.com/rantipay/trading-mcp/internal/ai/regime"
)

// RegimeAdapter is a thin wrapper around regime.Detector that satisfies
// master.RegimeAnalyzer.
type RegimeAdapter struct {
	detector *regime.Detector
}

// NewRegimeAdapter constructs the wrapper.
func NewRegimeAdapter(detector *regime.Detector) *RegimeAdapter {
	return &RegimeAdapter{detector: detector}
}

// Analyze delegates to the underlying detector.
func (r *RegimeAdapter) Analyze(candles []ai.Candle) (*regime.RegimeAnalysis, error) {
	if r.detector == nil {
		return nil, fmt.Errorf("regime detector not initialized")
	}
	return r.detector.Analyze(candles)
}
