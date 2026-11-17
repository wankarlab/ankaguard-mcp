package marketdata

import (
	"github.com/rantipay/trading-mcp/internal/mcp-trading/gctclient"
	"github.com/rantipay/trading-mcp/internal/mcp-trading/server"
	"github.com/sirupsen/logrus"
)

// RegisterAll registra todos los tools de market data
func RegisterAll(srv *server.MCPServer, client *gctclient.Client) error {
	// Initialize pairs cache and provider for get_markets tool
	pairsCache := NewPairsCache()

	// Get logger from server if available
	var logger *logrus.Logger
	if srv != nil {
		// Try to get logger from server context
		// For now, we'll pass nil and the provider will handle it
		logger = nil
	}

	pairsProvider := NewPairsProvider(client, pairsCache, logger)

	tools := []server.Tool{
		NewGetTickerTool(client),
		NewGetOrderbookTool(client),
		NewGetCandlesTool(client),
		NewGetTradesTool(client),
		NewGet24hStatsTool(client),
		NewGetMarketsTool(client, pairsProvider), // Enhanced with pairs provider
		NewGetExchangeInfoTool(client),
		NewGetFundingRateTool(client),
		NewGetMarkPriceTool(client),
		NewGetIndexPriceTool(client),
		NewGetMarketTypesTool(client), // Market type info
	}

	for _, tool := range tools {
		if err := srv.RegisterTool(tool); err != nil {
			return err
		}
	}

	return nil
}
