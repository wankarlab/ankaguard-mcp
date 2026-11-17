package llm

import (
	"github.com/rantipay/trading-mcp/internal/config"
	"github.com/rantipay/trading-mcp/internal/mcp-trading/server"
	"github.com/sirupsen/logrus"
)

// RegisterAll registers all LLM tools
func RegisterAll(srv *server.MCPServer, cfg *config.AIConfig, log *logrus.Logger) error {
	// Create and register analyze market tool
	analyzeMarketTool, err := NewAnalyzeMarketTool(cfg, log)
	if err != nil {
		log.WithError(err).Warn("Failed to create LLM analyze market tool - LLM features will be unavailable")
		// Don't fail, just skip LLM tools
		return nil
	}

	if err := srv.RegisterTool(analyzeMarketTool); err != nil {
		return err
	}

	// Create and register provider status tool
	providerStatusTool, err := NewGetProviderStatusTool(cfg, log)
	if err != nil {
		log.WithError(err).Warn("Failed to create LLM provider status tool")
		// Don't fail, just skip this tool
		return nil
	}

	if err := srv.RegisterTool(providerStatusTool); err != nil {
		return err
	}

	log.Info("LLM tools registered successfully")
	return nil
}
