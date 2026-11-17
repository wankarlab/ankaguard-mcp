package portfolio

import (
	"github.com/rantipay/trading-mcp/internal/mcp-trading/gctclient"
	"github.com/rantipay/trading-mcp/internal/mcp-trading/server"
	"github.com/rantipay/trading-mcp/internal/storage"
)

// RegisterAll registers all portfolio management tools.
// snapshotRepo may be nil if storage is not yet initialised; the history
// tool will return an empty response in that case.
func RegisterAll(srv *server.MCPServer, client *gctclient.Client, snapshotRepo *storage.PortfolioSnapshotRepository) error {
	tools := []server.Tool{
		NewGetPortfolioTool(client),
		NewGetPortfolioOverviewTool(client),
		NewGetBalanceTool(client),
		NewGetPositionsTool(client),
		NewGetPnLTool(client),
		NewGetTradeHistoryTool(client),
		NewGetTradeHistoryToolV2(client),
		NewGetAccountInfoTool(client),
		NewCalculateDrawdownTool(client),
		NewGetAdvancedPortfolioMetricsTool(snapshotRepo),
		NewGetPortfolioRiskMetricsTool(client, snapshotRepo),
		NewGetPortfolioHistoryTool(client, snapshotRepo),
		NewGetRebalanceRecommendationsTool(client),
	}

	for _, tool := range tools {
		if err := srv.RegisterTool(tool); err != nil {
			return err
		}
	}

	return nil
}
