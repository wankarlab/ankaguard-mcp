package futures

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/rantipay/trading-mcp/internal/mcp-trading/models"
	"github.com/rantipay/trading-mcp/internal/mcp-trading/tools/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeFunding struct {
	rate    float64
	err     error
	history []*models.FundingRateData
}

func (f *fakeFunding) GetFundingRate(_ context.Context, ex, pair string) (*models.FundingRateData, error) {
	if f.err != nil {
		return nil, f.err
	}
	return &models.FundingRateData{Exchange: ex, Pair: pair, FundingRate: f.rate, NextFunding: time.Now().Add(2 * time.Hour)}, nil
}

func (f *fakeFunding) GetFundingHistory(_ context.Context, _, _ string, _, _ time.Time) ([]*models.FundingRateData, error) {
	return f.history, nil
}

func fundingArgs() map[string]interface{} {
	return map[string]interface{}{"exchange": "kucoin", "pair": "BTC-USDT", "position_size": 1.0, "position_value_usd": 1000.0, "side": "long"}
}

func TestFundingCostsFollowRealRate(t *testing.T) {
	lo, err := (&CalculateFundingCostsTool{source: &fakeFunding{rate: 0.0001}}).Execute(context.Background(), fundingArgs())
	require.NoError(t, err)
	hi, err := (&CalculateFundingCostsTool{source: &fakeFunding{rate: 0.0005}}).Execute(context.Background(), fundingArgs())
	require.NoError(t, err)
	l, h := lo.(map[string]interface{}), hi.(map[string]interface{})
	assert.Equal(t, true, l["estimate"])
	assert.Equal(t, 0.1, l["single_period_cost"].(map[string]interface{})["amount_usd"])
	assert.Equal(t, 0.5, h["single_period_cost"].(map[string]interface{})["amount_usd"])
	assert.NotContains(t, l, "comparison")
	assert.NotContains(t, l, "arbitrage_opportunities")
	assert.Equal(t, false, l["historical_analysis"].(map[string]interface{})["available"])
}

func TestFundingCostsUsesRealHistory(t *testing.T) {
	f := &fakeFunding{rate: 0.0001, history: []*models.FundingRateData{{FundingRate: 0.0002}, {FundingRate: 0.0004}}}
	res, err := (&CalculateFundingCostsTool{source: f}).Execute(context.Background(), fundingArgs())
	require.NoError(t, err)
	h := res.(map[string]interface{})["historical_analysis"].(map[string]interface{})
	assert.Equal(t, 0.0003, h["average_rate"])
	assert.Equal(t, 2, h["periods"])
}

func TestFundingCostsDataUnavailable(t *testing.T) {
	_, err := (&CalculateFundingCostsTool{source: &fakeFunding{err: errors.New("rpc down")}}).Execute(context.Background(), fundingArgs())
	require.Error(t, err)
	assert.True(t, common.IsDataUnavailable(err))
}
