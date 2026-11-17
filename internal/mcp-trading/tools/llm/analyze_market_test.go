package llm

import (
	"context"
	"errors"
	"testing"

	"github.com/rantipay/trading-mcp/internal/ai"
	aillm "github.com/rantipay/trading-mcp/internal/ai/llm"
	"github.com/rantipay/trading-mcp/internal/mcp-trading/tools/common"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeAnalyzer struct {
	analysis *aillm.LLMAnalysis
	err      error
}

func (f fakeAnalyzer) AnalyzeMarket(context.Context, *ai.MarketContext) (*aillm.LLMAnalysis, error) {
	return f.analysis, f.err
}

func args() map[string]interface{} {
	return map[string]interface{}{"pair": "BTC-USDT", "current_price": 100.0, "primary_timeframe": map[string]interface{}{"timeframe": "1h", "rsi": 20.0}}
}

func TestAnalyzeMarketReturnsProviderAnswer(t *testing.T) {
	tool := &AnalyzeMarketTool{log: logrus.New(), analyzer: fakeAnalyzer{analysis: &aillm.LLMAnalysis{Action: "BUY", Confidence: 0.4}}}
	res, err := tool.Execute(context.Background(), args())
	require.NoError(t, err)
	assert.Equal(t, "BUY", res.(map[string]interface{})["action"])

	tool.analyzer = fakeAnalyzer{analysis: &aillm.LLMAnalysis{Action: "SELL", Confidence: 0.9}}
	res, err = tool.Execute(context.Background(), args())
	require.NoError(t, err)
	assert.Equal(t, "SELL", res.(map[string]interface{})["action"])
}

func TestAnalyzeMarketProviderFailureReturnsErrorNotFiller(t *testing.T) {
	tool := &AnalyzeMarketTool{log: logrus.New(), analyzer: fakeAnalyzer{err: errors.New("provider timeout")}}
	res, err := tool.Execute(context.Background(), args())
	assert.Nil(t, res)
	require.Error(t, err)
	assert.True(t, common.IsDataUnavailable(err))
}

func TestAnalyzeMarketWithoutProviderReturnsError(t *testing.T) {
	tool := &AnalyzeMarketTool{log: logrus.New()}
	res, err := tool.Execute(context.Background(), args())
	assert.Nil(t, res)
	assert.True(t, common.IsDataUnavailable(err))
}
