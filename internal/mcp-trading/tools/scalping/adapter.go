package scalping

import (
	"context"

	"github.com/rantipay/trading-mcp/internal/gct"
	"github.com/rantipay/trading-mcp/internal/mcp-trading/resilience"
	"github.com/rantipay/trading-mcp/internal/notifications"
	"github.com/rantipay/trading-mcp/internal/paper"
	"github.com/rantipay/trading-mcp/internal/scalping/engine"
	"github.com/rantipay/trading-mcp/internal/scalping/executor"
	"github.com/rantipay/trading-mcp/internal/scalping/manager"
	"github.com/rantipay/trading-mcp/internal/scalping/models"
	"github.com/rantipay/trading-mcp/internal/scalping/risk"
	"github.com/rantipay/trading-mcp/internal/scalping/strategies"
	"github.com/sirupsen/logrus"
)

// Adapter adapts the ScalpingEngine for use in MCP Tools.
type Adapter struct {
	logger          *logrus.Logger
	gctClient       gct.GCTClient
	engine          *engine.ScalpingEngine
	orchestrator    *strategies.StrategyOrchestrator
	riskManager     *risk.AdaptiveRiskManager
	executor        *executor.HighFrequencyExecutor
	botManager      *manager.BotManager
	resilientClient *resilience.ResilientClient
	notifier        *notifications.NotificationManager
	// clientSelector picks the client for account and order calls of one MCP
	// request (paper broker for a paper profile). Wired from main.go with
	// gctclient.Client.ClientFor.
	clientSelector func(ctx context.Context) (gct.GCTClient, error)
}

// NewAdapter creates a new adapter for scalping tools.
// maxBots controls the BotManager concurrency limit; 0 falls back to the
// BotManager default (5).
// riskParams are the real risk parameters loaded from config.yaml
// (scalping.default_risk_params). Passing nil falls back to
// risk.getDefaultRiskParameters() hardcoded defaults, which are 10-50x looser
// than production config — only acceptable for tests, never for the
// production registration path (see SCALP-03 audit finding).
func NewAdapter(
	logger *logrus.Logger,
	gctClient gct.GCTClient,
	scalpingEngine *engine.ScalpingEngine,
	maxBots int,
	riskParams *models.RiskParameters,
) *Adapter {
	// Build sub-components
	orchestrator := strategies.NewStrategyOrchestrator(logger)
	riskManager := risk.NewAdaptiveRiskManager(logger, gctClient, riskParams)
	executor := executor.NewHighFrequencyExecutor(logger, gctClient)
	botManager := manager.NewBotManagerWithConfig(logger, gctClient, orchestrator, riskManager, executor, manager.BotManagerConfig{MaxBots: maxBots})

	// Registrar estrategias en el orchestrator
	orchestrator.RegisterStrategy(strategies.NewRSIScalpingStrategy(logger, gctClient))
	orchestrator.RegisterStrategy(strategies.NewMACDScalpingStrategy(logger, gctClient))

	// Crear cliente resiliente para manejo robusto de errores
	resilientClient := resilience.NewResilientClient(logger)

	// Crear sistema de notificaciones (nil por ahora, se puede configurar después)
	var notifier *notifications.NotificationManager = nil

	return &Adapter{
		logger:          logger,
		gctClient:       gctClient,
		engine:          scalpingEngine,
		orchestrator:    orchestrator,
		riskManager:     riskManager,
		executor:        executor,
		botManager:      botManager,
		resilientClient: resilientClient,
		notifier:        notifier,
	}
}

// GetOrchestrator retorna el orchestrator de estrategias
func (a *Adapter) GetOrchestrator() *strategies.StrategyOrchestrator {
	return a.orchestrator
}

// GetRiskManager retorna el risk manager
func (a *Adapter) GetRiskManager() *risk.AdaptiveRiskManager {
	return a.riskManager
}

// GetExecutor retorna el executor
func (a *Adapter) GetExecutor() *executor.HighFrequencyExecutor {
	return a.executor
}

// GetBotManager returns the bot manager.
func (a *Adapter) GetBotManager() *manager.BotManager {
	return a.botManager
}

// SetClientSelector wires the per-request client selector used by the tools
// that read the account or close positions (get_futures_positions,
// close_futures_position).
func (a *Adapter) SetClientSelector(sel func(ctx context.Context) (gct.GCTClient, error)) {
	a.clientSelector = sel
}

// ClientFor returns the client that must serve account and order operations of
// the request carried by ctx. Fail closed: a paper request without a selector
// is rejected instead of falling back to the real client.
func (a *Adapter) ClientFor(ctx context.Context) (gct.GCTClient, error) {
	if a.clientSelector != nil {
		return a.clientSelector(ctx)
	}
	if _, isPaper := paper.HolderFromContext(ctx); isPaper {
		return nil, &paper.Error{Code: "PAPER_UNAVAILABLE", Message: "no paper client selector is wired into the scalping tools"}
	}
	return a.gctClient, nil
}

// GetGCTClient retorna el GCT client
func (a *Adapter) GetGCTClient() gct.GCTClient {
	return a.gctClient
}

// GetResilientClient retorna el cliente resiliente
func (a *Adapter) GetResilientClient() *resilience.ResilientClient {
	return a.resilientClient
}

// GetLogger retorna el logger
func (a *Adapter) GetLogger() *logrus.Logger {
	return a.logger
}

// GetNotifier retorna el notifier
func (a *Adapter) GetNotifier() *notifications.NotificationManager {
	return a.notifier
}
