package risk

import (
	"github.com/rantipay/trading-mcp/internal/mcp-trading/config"
	"github.com/rantipay/trading-mcp/internal/mcp-trading/gctclient"
	"github.com/rantipay/trading-mcp/internal/mcp-trading/server"
	internalRisk "github.com/rantipay/trading-mcp/internal/risk"
	"github.com/rantipay/trading-mcp/internal/trading"
)

// RegisterAll registra todos los tools de risk management
func RegisterAll(
	srv *server.MCPServer,
	client *gctclient.Client,
	cfg *config.Config,
	riskSentinel internalRisk.RiskSentinel,
	positionManager trading.PositionTracker,
	sltpExecutor trading.SLTPExecutor,
) error {
	tools := []server.Tool{
		// Original risk tools
		NewCheckRiskLimitsTool(client, cfg),
		NewCalculatePositionSizeTool(client, cfg),
		NewCalculateStopLossTool(client),
		NewCalculateTakeProfitTool(client),
		NewGetExposureTool(client),

		// New safety control tools
		NewGetRiskStateTool(client, riskSentinel),
		NewActivateKillSwitchTool(client, riskSentinel),
		NewDeactivateKillSwitchTool(client, riskSentinel),
		NewGetPositionsTool(client, positionManager),
		NewUpdateSLTPTool(client, sltpExecutor),

		// Trade validation tool
		NewValidateTradeTool(client, cfg, riskSentinel, positionManager),
	}

	for _, tool := range tools {
		if err := srv.RegisterTool(tool); err != nil {
			return err
		}
	}

	return nil
}
