package orders

import (
	"github.com/rantipay/trading-mcp/internal/mcp-trading/gctclient"
	"github.com/rantipay/trading-mcp/internal/mcp-trading/server"
	"github.com/sirupsen/logrus"
)

// RegisterAll registra todos los tools de order management.
// It accepts an optional logger; if nil, a default logger will be created.
func RegisterAll(srv *server.MCPServer, client *gctclient.Client) error {
	return RegisterAllWithLogger(srv, client, nil)
}

// RegisterAllWithLogger registra todos los tools de order management with a custom logger.
func RegisterAllWithLogger(srv *server.MCPServer, client *gctclient.Client, logger *logrus.Logger) error {
	// Initialize the package logger
	InitLogger(logger)

	tools := []server.Tool{
		// Submit orders
		NewSubmitOrderTool(client),
		NewSubmitMarketOrderTool(client),
		NewSubmitLimitOrderTool(client),
		NewSubmitStopOrderTool(client),

		// Simulate orders (paper trading)
		NewSimulateOrderTool(client),

		// Manage orders
		NewCancelOrderTool(client),
		NewCancelAllOrdersTool(client),
		NewModifyOrderTool(client),
		NewGetOrderStatusTool(client),
		NewGetOpenOrdersTool(client),
		NewGetOrderHistoryTool(client),

		// Direct KuCoin REST tools (bypass GCT enabled-currency restriction)
		NewClosePositionDirectTool(client),
		NewSubmitFuturesOrderDirectTool(client),

		// Protective trailing stop (reduce-only)
		NewCreateTrailingStopTool(client),
	}

	for _, tool := range tools {
		if err := srv.RegisterTool(tool); err != nil {
			return err
		}
	}

	return nil
}
