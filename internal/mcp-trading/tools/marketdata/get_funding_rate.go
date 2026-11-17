package marketdata

import (
	"context"

	"github.com/rantipay/trading-mcp/internal/mcp-trading/gctclient"
	"github.com/rantipay/trading-mcp/internal/mcp-trading/models"
)

// GetFundingRateTool obtiene funding rate para futuros
type GetFundingRateTool struct {
	BaseTool
}

func NewGetFundingRateTool(client *gctclient.Client) *GetFundingRateTool {
	return &GetFundingRateTool{BaseTool: BaseTool{client: client}}
}

func (t *GetFundingRateTool) Name() string {
	return "get_funding_rate"
}

func (t *GetFundingRateTool) Description() string {
	return "Get current funding rate for perpetual futures contracts"
}

func (t *GetFundingRateTool) InputSchema() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"exchange":    map[string]interface{}{"type": "string", "description": "Exchange name"},
			"pair":        map[string]interface{}{"type": "string", "description": "Futures pair (e.g., 'BTCUSDTM' or 'BTC-USDT' with market_type=futures)"},
			"market_type": map[string]interface{}{"type": "string", "description": "Market type (must be 'futures' for this tool)", "enum": []string{"futures"}},
		},
		"required": []string{"exchange", "pair"},
	}
}

func (t *GetFundingRateTool) Execute(ctx context.Context, params map[string]interface{}) (interface{}, error) {
	exchange, pair, err := t.validateExchangeAndPair(params)
	if err != nil {
		return nil, err
	}

	// Validate that market_type is futures (if provided)
	if marketType, ok := params["market_type"].(string); ok && marketType != "" {
		if marketType != "futures" {
			return nil, &models.MCPError{
				Code:    -32602,
				Message: "Invalid market_type for funding rate",
				Data:    "Funding rate is only available for futures markets. Use market_type='futures'",
			}
		}

		// Convert symbol using market_type
		convertedPair, err := t.convertSymbol(pair, marketType, exchange)
		if err != nil {
			return nil, err
		}
		pair = convertedPair
	}

	// Get funding rate from GCT
	fundingData, err := t.client.GetFundingRate(ctx, exchange, pair)
	if err != nil {
		// If GCT doesn't support it, return error with helpful message
		return nil, &models.MCPError{
			Code:    -32603,
			Message: "Failed to get funding rate",
			Data:    err.Error(),
		}
	}

	return fundingData, nil
}
