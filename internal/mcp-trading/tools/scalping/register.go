package scalping

import (
	"github.com/rantipay/trading-mcp/internal/gct"
	"github.com/rantipay/trading-mcp/internal/mcp-trading/server"
	"github.com/rantipay/trading-mcp/internal/scalping/engine"
	"github.com/rantipay/trading-mcp/internal/scalping/models"
	"github.com/sirupsen/logrus"
)

// RegisterScalpingTools registers all scalping tools in the MCP Server and returns
// the created Adapter so the caller can wire sub-components (e.g. Master Agent).
// maxBots sets the BotManager concurrency limit; 0 falls back to the default (5).
// riskParams must be the risk parameters loaded from config.yaml
// (scalping.default_risk_params, e.g. via getScalpingRiskParameters()) — passing
// nil here silently falls back to hardcoded defaults 10-50x looser than
// production config (see SCALP-03 audit finding), so callers on the real
// order-submission path must always supply the real config-derived params.
func RegisterScalpingTools(
	registry *server.ToolRegistry,
	logger *logrus.Logger,
	gctClient gct.GCTClient,
	scalpingEngine *engine.ScalpingEngine,
	maxBots int,
	riskParams *models.RiskParameters,
) (*Adapter, error) {
	logger.Info("🔧 Registering FULL scalping tools implementation...")

	// Create adapter with the configured concurrency limit and the real,
	// config-derived risk parameters (never nil on the production path).
	adapter := NewAdapter(logger, gctClient, scalpingEngine, maxBots, riskParams)

	// Crear e instanciar todas las tools
	tools := []server.Tool{
		NewAnalyzeOpportunityTool(adapter),
		NewGetSignalsTool(adapter),
		NewExecuteTradeTool(adapter),
		NewStartBotTool(adapter),
		NewBotStatusTool(adapter),
		NewStopBotTool(adapter),
		NewGetFuturesPositionsTool(adapter),
		NewCloseFuturesPositionTool(adapter),
		NewGetBotTradesTool(adapter),
		NewGetBotPerformanceTool(adapter), // NEW: Performance data for charts
	}

	// Registrar cada tool
	for _, tool := range tools {
		if err := registry.Register(tool); err != nil {
			logger.Errorf("❌ Failed to register tool %s: %v", tool.Name(), err)
			return nil, err
		}
		logger.Infof("✅ Registered FULL scalping tool: %s", tool.Name())
	}

	logger.Infof("✅ Successfully registered %d FULL scalping tools (real market analysis)", len(tools))

	return adapter, nil
}
