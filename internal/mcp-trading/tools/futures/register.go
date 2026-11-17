package futures

import (
	"github.com/rantipay/trading-mcp/internal/mcp-trading/gctclient"
	"github.com/rantipay/trading-mcp/internal/mcp-trading/server"
)

// RegisterAll registers all futures calculator tools
func RegisterAll(srv *server.MCPServer, client *gctclient.Client) error {
	tools := []server.Tool{
		NewCalculateLiquidationPriceTool(client),
		NewCalculateFundingCostsTool(client),
	}

	for _, tool := range tools {
		if err := srv.RegisterTool(tool); err != nil {
			return err
		}
	}

	return nil
}
