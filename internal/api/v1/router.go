// Package v1 provides REST API v1 implementation for trading-mcp service.
// This router integrates all handlers and applies authentication/tenant middleware.
package v1

import (
	"context"
	"github.com/rantipay/trading-mcp/internal/edition"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/sirupsen/logrus"
	"gorm.io/gorm"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rantipay/trading-mcp/internal/ai"
	"github.com/rantipay/trading-mcp/internal/ai/llm"
	"github.com/rantipay/trading-mcp/internal/ai/sentiment"
	"github.com/rantipay/trading-mcp/internal/api/v1/handlers"
	"github.com/rantipay/trading-mcp/internal/api/v1/models"
	"github.com/rantipay/trading-mcp/internal/config/dynamic"
	"github.com/rantipay/trading-mcp/internal/credentials"
	exch "github.com/rantipay/trading-mcp/internal/exchange"
	"github.com/rantipay/trading-mcp/internal/gct"
	confighandlers "github.com/rantipay/trading-mcp/internal/handlers"
	"github.com/rantipay/trading-mcp/internal/launch/launchapi"
	"github.com/rantipay/trading-mcp/internal/mcp-trading/gctclient"
	"github.com/rantipay/trading-mcp/internal/middleware"
	"github.com/rantipay/trading-mcp/internal/notifications"
	"github.com/rantipay/trading-mcp/internal/services"
	"github.com/rantipay/trading-mcp/internal/storage"
	"github.com/rantipay/trading-mcp/internal/strategy"
	"github.com/rantipay/trading-mcp/internal/subscription"
	tradingMiddleware "github.com/rantipay/trading-mcp/internal/trading/middleware"
	"github.com/rantipay/trading-mcp/internal/twofa"
	"github.com/rantipay/trading-mcp/internal/volatility"
	"github.com/rantipay/trading-mcp/internal/xsec"
)

// Ensure *services.BotManager satisfies handlers.AllStopper at compile time.
var _ handlers.AllStopper = (*services.BotManager)(nil)

// RouterConfig holds the configuration for the REST API router
type RouterConfig struct {
	// GCTClient is the GoCryptoTrader client
	GCTClient *gctclient.Client

	// CredentialService is the credential management service
	CredentialService credentials.Service

	// LLMAnalyzer is the AI LLM analyzer for trading signals
	LLMAnalyzer *llm.Analyzer

	// SentimentAnalyzer is the sentiment analysis service
	SentimentAnalyzer *sentiment.Analyzer

	// Backtester is the backtest simulation service
	Backtester *ai.Backtester

	// DB is the database connection for tenant context
	DB *gorm.DB

	// Logger for request logging
	Logger *logrus.Logger

	// JWTSecret for token validation
	JWTSecret string

	// ProductRoutes mounts the product API (paper account reset, product
	// status) on the authenticated /api/v1 group. nil = not mounted.
	ProductRoutes func(v1 *gin.RouterGroup)

	// PublicRoutes mounts routes that must stay OUTSIDE the JWT middleware
	// (the local login). nil = none.
	PublicRoutes func(engine *gin.Engine)

	// MCPHealth reports (gct connected, engine real money) in-process for
	// /api/v1/system/health. When nil the aggregator falls back to probing
	// MCP_HEALTH_URL, which no longer returns those fields publicly.
	MCPHealth func() (gctConnected, engineRealMoney bool)

	// AuthServiceURL is the base URL of the RantiPay Auth service that mints JWTs
	// (login/refresh/oauth). The public /api/v1/auth/* proxy forwards here.
	// Optional: empty defaults to http://localhost:10100 inside the handler.
	AuthServiceURL string

	// InternalGatewayToken is the shared secret that the internal API gateway
	// sets as X-Gateway-Token. When it matches, the JWT signature check is
	// skipped (gateway is trusted). Must come from configuration (env); empty
	// disables the bypass (fail closed).
	InternalGatewayToken string

	// Version is the service version
	Version string

	// StartTime is the service start time for uptime calculation
	StartTime time.Time

	// SubscriptionService for subscription management
	SubscriptionService *subscription.Service

	// WebhookProcessor for handling Odoo subscription webhooks
	WebhookProcessor *subscription.WebhookProcessor

	// WebhookSecret for signature verification (optional)
	WebhookSecret string

	// WebhookRateLimit is the maximum requests per minute for webhooks
	WebhookRateLimit int

	// UserPairSelectionRepo enables the pair selection endpoints (DM-2.3).
	// If nil the /api/v1/pairs/* routes are skipped.
	UserPairSelectionRepo *storage.UserPairSelectionRepository

	// PairAssetType is the asset type passed to GetTradablePairs (e.g. "futures").
	PairAssetType string

	// ExchangeRouter enables the /api/v1/exchanges/* routes (DM-5.4).
	// If nil the routes are skipped.
	ExchangeRouter exch.ExchangeRouter

	// TradingModeRepo enables the /api/v1/mode/* routes (Wave 2-A).
	// If nil the routes are skipped.
	TradingModeRepo *storage.TradingModeRepository

	// BotManager enables the /api/v1/bot/* routes (MU-5.2).
	// If nil the bot control routes are skipped.
	BotManager *services.BotManager

	// UserBotConfigRepo enables bot config persistence (MU-5.2).
	// If nil the bot config routes are skipped.
	UserBotConfigRepo *storage.UserBotConfigRepository

	// StrategySpecRepo enables the /api/v1/specs/* routes (API-1/4/6).
	// Optional: nil skips spec lifecycle endpoints.
	StrategySpecRepo *storage.StrategySpecRepository

	// SpecTemplateRepo backs GET /api/v1/specs/templates (C0.5).
	// Optional: nil → the templates endpoint returns an empty list.
	SpecTemplateRepo *storage.SpecTemplateRepository

	// ConsentRepo backs the informed-consent endpoints (C1.3).
	// Optional: nil → /api/v1/consent/* responds 503.
	ConsentRepo *storage.ConsentRepository

	// GovernanceRepo backs the adversarial review queue (C3.2) and the
	// tribunal appeals (C3.3) endpoints.
	// Optional: nil → /api/v1/adversarial/* and /api/v1/appeals respond 503.
	GovernanceRepo *storage.GovernanceRepository

	// GlobalRiskLimits are the server-side caps for StrategySpec validation.
	GlobalRiskLimits strategy.GlobalLimits

	// OnSpecLive is an optional callback invoked when a Spec is promoted to live.
	// G5: apply config_override to the running bot's dynamic config store.
	// Optional: nil → no override is applied.
	OnSpecLive handlers.OnLiveFunc

	// SpecMinRewardRiskRatio reads spec.min_reward_risk_ratio from the dynamic
	// config table at validation time (hot-reload). Optional: nil → no constraint
	// (TP may be below SL). Keeps the rule out of source — tournament bots vary.
	SpecMinRewardRiskRatio func() float64

	// SpecPromoteMinLevel reads spec.promote_min_level (default 3) and
	// SpecAllowSelfPromotion reads spec.allow_self_promotion (default false) from
	// the config table at promote time. Keep the real-money promote gates dynamic
	// (no fixed middleware) so the tournament can tune them. nil → defaults.
	SpecPromoteMinLevel    func() int
	SpecAllowSelfPromotion func() bool

	// KYCVerifier enables the /api/v1/kyc/* routes (API-9, RBAC-3).
	// Optional: nil → endpoints respond 503.
	KYCVerifier *services.KYCVerifier

	// FeeLedger enables the /api/v1/billing/fees/* routes (CP-2).
	// Optional: nil → endpoints respond 503.
	// Collection is always OFF at runtime; these endpoints are read-only accrual.
	FeeLedger handlers.FeeLedger

	// Fee economy readers (C0.4): the billing fee-ledger response includes
	// fee_rate / author_split / treasury_split + computed amounts. These are read
	// from the dynamic config table (billing.fee_rate / .author_split /
	// .treasury_split), never hardcoded. nil → manifest defaults.
	FeeRate          func() float64
	FeeAuthorSplit   func() float64
	FeeTreasurySplit func() float64

	// RBAC screen-gate level readers (C1.4): back GET /api/v1/me/permissions.
	// Read from rbac.level.* in the config table. nil → manifest defaults.
	RBACLevelPoliticas    func() int
	RBACLevelAprobaciones func() int
	RBACLevelTribunal     func() int
	RBACLevelPool         func() int

	// SessionManager enables per-user exchange sessions for portfolio/orders reads (MU-4.3).
	// If nil handlers fall back to the global GCT client (backward-compatible).
	SessionManager *gct.SessionManager

	// ConfigStore wires the dynamic config store into handlers that read live bot
	// parameters (e.g. the portfolio overview). Optional: nil → handlers fall back
	// to viper/YAML (audit P1).
	ConfigStore *dynamic.ConfigStore

	// LaunchDB is the config Postgres pool backing the AnkaLaunch console
	// (/api/v1/launch). Optional: nil → every launch endpoint answers 503.
	LaunchDB *pgxpool.Pool

	// OmitCompanionServices keeps the tournament, XSEC (status, jobs, alert
	// monitor with RantiPay push) and AnkaLaunch surfaces out of the router.
	// Set by the product build profile; the zero value keeps today's behaviour.
	OmitCompanionServices bool

	// RankedPairProvider backs the GET /api/v1/pairs/ranked endpoint.
	// Optional: nil → endpoint returns an empty list with a note.
	RankedPairProvider handlers.RankedPairProvider

	// VolatilityClassifier exposes per-pair volatility tier inspection.
	// Optional: nil → /api/v1/volatility/* responds 503.
	VolatilityClassifier *volatility.Classifier

	// RateLimiter applies per-user token-bucket limiting to bot/orders/credentials routes.
	// Optional: nil disables rate limiting.
	RateLimiter middleware.RateLimiter

	// QuotaResolver resolves per-user quotas. Optional; nil uses defaults.
	QuotaResolver middleware.QuotaResolver

	// TwoFAMiddleware applies 2FA enforcement to critical routes (H0.4.4).
	// Optional: nil skips 2FA checks until the twofa.go middleware is available.
	TwoFAMiddleware gin.HandlerFunc

	// TOTPService backs the /api/v1/twofa/* enrollment endpoints (BOT-04 fix).
	// Optional: nil (with DB nil too) skips registering those routes — in that
	// case TwoFAMiddleware, if set, can never see any user with 2FA enabled.
	TOTPService *twofa.TOTPService
}

// Router is the REST API v1 router
type Router struct {
	config               *RouterConfig
	portfolioHandler     *handlers.PortfolioHandler
	ordersHandler        *handlers.OrdersHandler
	directOrdersHandler  *handlers.DirectOrdersHandler // P1: direct-REST order endpoints (bypass GCT enabled-currency limit)
	marketHandler        *handlers.MarketHandler
	credHandler          *handlers.CredentialsHandler
	twofaHandler         *handlers.TwoFAHandler
	analysisHandler      *handlers.AnalysisHandler
	subscriptionHandler  *handlers.SubscriptionHandler
	webhookHandler       *handlers.WebhookHandler
	wsHandler            *handlers.WebSocketHandler
	wsHub                *handlers.Hub
	pairsHandler         *handlers.PairsHandler
	exchangesHandler     *handlers.ExchangesHandler
	modeHandler          *handlers.ModeHandler
	botConfigHandler     *handlers.BotConfigHandler     // MU-5.2: per-user bot control
	emergencyStopHandler *handlers.EmergencyStopHandler // H0.3.2: admin emergency-stop-all
	volatilityHandler    *handlers.VolatilityHandler
	specHandler          *handlers.SpecHandler        // API-1/4/6: StrategySpec lifecycle
	kycHandler           *handlers.KYCHandler         // API-9: KYC level + flow verification
	billingHandler       *handlers.BillingHandler     // CP-2: fee ledger endpoints
	permissionsHandler   *handlers.PermissionsHandler // C1.4: me/permissions effective gates
	governanceHandler    *handlers.GovernanceHandler  // C1.3/C3.2/C3.3: consent + adversarial + appeals
	// AnkaAI Console handlers
	engineHandler        *handlers.EngineHandler
	academyHandler       *handlers.AcademyHandler
	marketplaceHandler   *handlers.MarketplaceHandler
	killswitchHandler    *handlers.KillSwitchHandler
	policiesHandler      *handlers.PoliciesHandler
	explainHandler       *handlers.ExplainHandler
	tournamentHandler    *handlers.TournamentHandler
	xsecHandler          *handlers.XSecHandler          // F4.6: xsec market-neutral strategy status
	xsecJobsHandler      *handlers.XSecJobsHandler      // F4.7: xsec-live async job runner + params tuning
	accountEquityHandler *handlers.AccountEquityHandler // real account equity history (JWT-strict)
	systemHealthHandler  *handlers.SystemHealthHandler  // aggregated component health (JWT-strict)
	authProxyHandler     *handlers.AuthProxyHandler     // Fase A: proxy login/refresh to RantiPay Auth
	logger               *logrus.Logger
}

// NewRouter creates a new REST API v1 router
func NewRouter(cfg *RouterConfig) (*Router, error) {
	if cfg == nil {
		cfg = &RouterConfig{}
	}

	logger := cfg.Logger
	if logger == nil {
		logger = logrus.New()
	}

	router := &Router{
		config: cfg,
		logger: logger,
	}

	// Fase A: auth proxy to the RantiPay Auth service (always on; has a default URL).
	router.authProxyHandler = handlers.NewAuthProxyHandler(cfg.AuthServiceURL, logger)

	// Initialize handlers
	if cfg.GCTClient != nil {
		router.portfolioHandler = handlers.NewPortfolioHandler(cfg.GCTClient, logger)
		router.ordersHandler = handlers.NewOrdersHandler(cfg.GCTClient, logger)
		router.marketHandler = handlers.NewMarketHandler(cfg.GCTClient, logger)

		// P1: direct-REST order endpoints (close/submit) that bypass GCT's
		// enabled-currency restriction by using the shared base client.
		router.directOrdersHandler = handlers.NewDirectOrdersHandler(cfg.GCTClient, logger)

		// MU-4.3: wire SessionManager into portfolio/orders for per-user reads.
		// Both handlers fall back to the global GCT client when SessionManager is nil
		// so existing single-operator usage is fully backward-compatible.
		if cfg.SessionManager != nil {
			router.portfolioHandler = router.portfolioHandler.WithSessionManager(cfg.SessionManager)
			router.ordersHandler = router.ordersHandler.WithSessionManager(cfg.SessionManager)
		}

		// Audit P1: wire the dynamic config store so the portfolio overview reads
		// live bot parameters instead of the static viper snapshot. nil → viper fallback.
		if cfg.ConfigStore != nil {
			router.portfolioHandler.SetConfigStore(cfg.ConfigStore)
		}
	}

	if cfg.CredentialService != nil && cfg.GCTClient != nil {
		router.credHandler = handlers.NewCredentialsHandler(cfg.CredentialService, cfg.GCTClient, logger)
	}

	// BOT-04 fix: register the enrollment endpoints so users can actually turn
	// on 2FA. Without this the TwoFAMiddleware step-up check below (if wired)
	// could never see any user with Enabled=true — the middleware would be
	// reachable but functionally inert.
	if cfg.TOTPService != nil && cfg.DB != nil {
		router.twofaHandler = handlers.NewTwoFAHandler(cfg.TOTPService, cfg.DB, logger)
	}

	// Initialize analysis handler (AI endpoints)
	router.analysisHandler = handlers.NewAnalysisHandler(&handlers.AnalysisHandlerConfig{
		GCTClient:         cfg.GCTClient,
		LLMAnalyzer:       cfg.LLMAnalyzer,
		SentimentAnalyzer: cfg.SentimentAnalyzer,
		Backtester:        cfg.Backtester,
		Logger:            logger,
	})

	// Initialize subscription handler for subscription management endpoints
	if cfg.SubscriptionService != nil {
		router.subscriptionHandler = handlers.NewSubscriptionHandler(cfg.SubscriptionService, logger)
	}

	// Initialize webhook handler for Odoo subscription events
	if cfg.WebhookProcessor != nil {
		router.webhookHandler = handlers.NewWebhookHandler(&handlers.WebhookHandlerConfig{
			Processor: cfg.WebhookProcessor,
			Logger:    logger,
		})
	}

	// Initialize WebSocket hub and handler
	router.wsHub = handlers.NewHub(logger)
	router.wsHandler = handlers.NewWebSocketHandler(&handlers.WebSocketHandlerConfig{
		Hub:       router.wsHub,
		GCTClient: cfg.GCTClient,
		JWTSecret: cfg.JWTSecret,
		Logger:    logger,
	})

	// Start the WebSocket hub in a goroutine
	go router.wsHub.Run()

	// Initialize pairs handler (DM-2.3)
	if cfg.GCTClient != nil && cfg.UserPairSelectionRepo != nil {
		ph := handlers.NewPairsHandler(
			cfg.GCTClient,
			cfg.UserPairSelectionRepo,
			cfg.PairAssetType,
			logger,
		)
		if cfg.RankedPairProvider != nil {
			ph.SetRankedPairProvider(cfg.RankedPairProvider)
		}
		router.pairsHandler = ph
	}

	// Initialize exchanges handler (DM-5.4)
	if cfg.ExchangeRouter != nil {
		router.exchangesHandler = handlers.NewExchangesHandler(cfg.ExchangeRouter, logger)
	}

	// Initialize mode handler (Wave 2-A)
	if cfg.TradingModeRepo != nil {
		router.modeHandler = handlers.NewModeHandler(cfg.TradingModeRepo, logger)
	}

	// Initialize bot config handler (MU-5.2)
	// StrategySpec lifecycle endpoints (API-1/4/6).
	if cfg.StrategySpecRepo != nil {
		limits := cfg.GlobalRiskLimits
		if limits.MaxOrderSizeUSD == 0 {
			limits = strategy.GlobalLimits{MaxOrderSizeUSD: 2.0, MaxLeverage: 20, MaxDailyLossUSD: 5.0}
		}
		h := handlers.NewSpecHandler(cfg.StrategySpecRepo, limits, logger)
		// G5: wire the live-callback when provided.
		if cfg.OnSpecLive != nil {
			h = h.WithOnLive(cfg.OnSpecLive)
		}
		// Dynamic reward:risk floor from the config table (nil → no constraint).
		if cfg.SpecMinRewardRiskRatio != nil {
			h = h.WithDynamicLimits(cfg.SpecMinRewardRiskRatio)
		}
		// Dynamic real-money promote gates from the config table (nil → defaults).
		if cfg.SpecPromoteMinLevel != nil || cfg.SpecAllowSelfPromotion != nil {
			h = h.WithPromoteGates(cfg.SpecPromoteMinLevel, cfg.SpecAllowSelfPromotion)
		}
		// C0.5: seeded spec templates (nil → empty list).
		if cfg.SpecTemplateRepo != nil {
			h = h.WithTemplates(cfg.SpecTemplateRepo)
		}
		router.specHandler = h
	}

	// Both BotManager and UserBotConfigRepo must be non-nil to register bot endpoints.
	if cfg.BotManager != nil && cfg.UserBotConfigRepo != nil {
		router.botConfigHandler = handlers.NewBotConfigHandler(cfg.BotManager, cfg.UserBotConfigRepo, logger)
		// BOT-06 fix: wire the subscription service (when available) so StartBot
		// can enforce the per-tier bot quota. cfg.SubscriptionService is nil in
		// the current deployment (main.go has no DB-backed subscription service
		// yet — see its own TODO); the real-trading tier gate in StartBot/
		// PutConfig is active regardless.
		if cfg.SubscriptionService != nil {
			router.botConfigHandler.SetSubscriptionService(cfg.SubscriptionService)
		}
	}

	// Initialize emergency stop handler (H0.3.2).
	// Only requires BotManager (no config repo needed).
	if cfg.BotManager != nil {
		router.emergencyStopHandler = handlers.NewEmergencyStopHandler(cfg.BotManager, logger)
	}

	// Initialize volatility handler (always — handler tolerates nil classifier).
	router.volatilityHandler = handlers.NewVolatilityHandler(cfg.VolatilityClassifier, logger)

	// API-9: KYC handler — tolerates nil verifier (returns 503 when unconfigured).
	// Pass untyped nil when KYCVerifier is nil to avoid the Go nil-interface trap
	// (a nil *services.KYCVerifier wrapped in an interface is non-nil).
	if cfg.KYCVerifier != nil {
		router.kycHandler = handlers.NewKYCHandler(cfg.KYCVerifier, logger)
	} else {
		router.kycHandler = handlers.NewKYCHandler(nil, logger)
	}

	// CP-2: fee billing handler (read-only accrual; collection OFF).
	// C0.4: wire the dynamic fee economy (rate + author/treasury split).
	router.billingHandler = handlers.NewBillingHandler(cfg.FeeLedger, logger).
		WithEconomy(handlers.FeeEconomyProvider{
			FeeRate:       cfg.FeeRate,
			AuthorSplit:   cfg.FeeAuthorSplit,
			TreasurySplit: cfg.FeeTreasurySplit,
		})

	// C1.4: me/permissions effective gates (RBAC levels from the config table).
	router.permissionsHandler = handlers.NewPermissionsHandler(handlers.RBACLevels{
		Politicas:    cfg.RBACLevelPoliticas,
		Aprobaciones: cfg.RBACLevelAprobaciones,
		Tribunal:     cfg.RBACLevelTribunal,
		Pool:         cfg.RBACLevelPool,
	}, logger)

	// C1.3/C3.2/C3.3: consent + adversarial review + appeals. Each repo is
	// optional; the handler responds 503 for a domain whose repo is nil.
	router.governanceHandler = handlers.NewGovernanceHandler(cfg.ConsentRepo, cfg.GovernanceRepo, logger)

	// AnkaAI Console handlers — all require DB; safe when DB is nil.
	if cfg.DB != nil {
		// EngineHandler needs *sql.DB (underlying driver connection).
		sqlDB, err := cfg.DB.DB()
		if err != nil {
			logger.WithError(err).Warn("router: could not obtain *sql.DB for EngineHandler; engine routes will return defaults")
		} else {
			router.engineHandler = handlers.NewEngineHandler(sqlDB, logger)
		}

		router.academyHandler = handlers.NewAcademyHandler(cfg.DB, logger)
		router.marketplaceHandler = handlers.NewMarketplaceHandler(cfg.DB, logger)

		// KillSwitchHandler accepts an optional AllStopper (BotManager satisfies it).
		var stopper handlers.AllStopper
		if cfg.BotManager != nil {
			stopper = cfg.BotManager
		}
		router.killswitchHandler = handlers.NewKillSwitchHandler(cfg.DB, stopper, logger)
		// Ring 0 also cancels orders and closes EVERY open futures position via
		// the direct KuCoin REST path, keeping xsec_live_book consistent.
		// Other in-process engines register via killswitch.RegisterEngine.
		if cfg.GCTClient != nil {
			if ex := handlers.NewGCTRing0Exchange(cfg.GCTClient.GetBaseClient(), "kucoin"); ex != nil {
				router.killswitchHandler.SetExchange(ex)
			}
		}
		if sqlDB, err := cfg.DB.DB(); err == nil {
			if lb := handlers.NewSQLLiveBookMarker(sqlDB); lb != nil {
				router.killswitchHandler.SetLiveBook(lb)
			}
		}

		router.policiesHandler = handlers.NewPoliciesHandler(cfg.DB, logger)
		router.explainHandler = handlers.NewExplainHandler(cfg.DB, logger)
		if !cfg.OmitCompanionServices {
			router.tournamentHandler = handlers.NewTournamentHandler(cfg.DB, logger)
		}

		// F4.6: xsec strategy status. Reuses the same *sql.DB the EngineHandler
		// gets from the GORM connection — that GORM handle is opened over the same
		// SQLite file (data/trading.db) where cmd/xsec-bot writes the xsec_* tables,
		// so no separate read-only open is needed. nil sqlDB → empty sections.
		// cfg.GCTClient (may be nil) drives live price/PnL enrichment on the basket.
		if sqlDB != nil && cfg.OmitCompanionServices {
			router.accountEquityHandler = handlers.NewAccountEquityHandler(sqlDB, logger)
		}
		if sqlDB != nil && !cfg.OmitCompanionServices {
			// GetStatus's SELECT reads entry_price/notional_usd from xsec_live_book
			// unconditionally; those columns are only added lazily (inside
			// UpsertLiveBookEntry/CloseLiveBookEntry) otherwise, so a server that
			// hasn't processed a single live open/close since the column migration
			// shipped would 500/empty on every read until then. Run it here instead,
			// once, before any HTTP request can be served — idempotent (ADD COLUMN
			// IF MISSING), matches RecoverStaleJobs' run-before-serving pattern below.
			if err := xsec.EnsureLiveBookSchema(sqlDB); err != nil {
				logger.WithError(err).Warn("router: xsec live-book schema migration failed")
			}
			router.xsecHandler = handlers.NewXSecHandler(sqlDB, cfg.GCTClient, logger)
			router.accountEquityHandler = handlers.NewAccountEquityHandler(sqlDB, logger)

			// F4.7: xsec-live async job runner (sync-book / rebalance dry+live /
			// cancel-order) + xsec_params tuning. Crash recovery (stale 'running'
			// rows from a prior server instance) runs once here, before any HTTP
			// request can be served.
			jobRunner := handlers.NewXSecJobRunner(sqlDB, logger)
			if n, err := jobRunner.RecoverStaleJobs(); err != nil {
				logger.WithError(err).Warn("router: xsec job recovery failed")
			} else if n > 0 {
				logger.WithField("recovered", n).Warn("router: recovered stale xsec jobs left running by a prior server instance")
			}
			router.xsecJobsHandler = handlers.NewXSecJobsHandler(sqlDB, jobRunner, logger)

			// XSEC basket-health alert monitor: evaluates imbalance / staleness /
			// leg blow-out / job failure on a timer, pushes to the owner's phone
			// via the RantiPay notificationpush microservice, and records active
			// alerts for the polled /xsec/status. Self-gates each tick on the
			// xsec.alert.enabled param. Push is best-effort: the client is a no-op
			// when NOTIFICATION_SERVICE_URL / RANTIPAY_JWT_SECRET are unset, and
			// in-app visibility (active_alerts) works regardless. XSEC_ALERT_USER_ID
			// is the owner's user_id whose registered devices receive the push.
			pushClient := notifications.NewRantiPayPushClient(
				os.Getenv("NOTIFICATION_SERVICE_URL"),
				os.Getenv("RANTIPAY_JWT_SECRET"),
				os.Getenv("NOTIFICATION_INTERNAL_API_KEY"),
				logger,
			)
			alertMonitor := handlers.NewXSecAlertMonitor(router.xsecHandler, sqlDB, pushClient, os.Getenv("XSEC_ALERT_USER_ID"), logger)
			alertMonitor.Start(context.Background())
		}
	}

	router.systemHealthHandler = handlers.NewSystemHealthHandler(buildSystemHealthConfig(cfg), logger)

	return router, nil
}

// buildSystemHealthConfig resolves the probe targets from the environment
// (same variables the rest of the service already uses) and the config store.
func buildSystemHealthConfig(cfg *RouterConfig) handlers.SystemHealthConfig {
	hc := handlers.SystemHealthConfig{
		MCPHealthURL: envOr("MCP_HEALTH_URL", "http://127.0.0.1:10600/health"),
		MLHealthURL:  strings.TrimRight(envOr("ML_SERVER_URL", "http://localhost:10601"), "/") + "/health",
	}
	hc.MCPHealthFunc = cfg.MCPHealth
	hc.OmitAuxiliary = edition.IsProduct()
	// notificationpush exposes an unauthenticated GET /api/v1/health.
	if base := strings.TrimRight(strings.TrimSpace(os.Getenv("NOTIFICATION_SERVICE_URL")), "/"); base != "" {
		hc.PushHealthURL = base + "/api/v1/health"
	}
	if cfg.ConfigStore != nil {
		store := cfg.ConfigStore
		// The repo has no explicit Ping; a cheap indexed read round-trips Postgres
		// and honours the context deadline.
		hc.ConfigDBPing = func(ctx context.Context) error {
			_, err := store.Repo().ListPresetsByCategory(ctx, "mode")
			return err
		}
	}
	return hc
}

func envOr(key, def string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return def
}

// RegisterRoutes registers all REST API v1 routes on the given Gin engine
func (r *Router) RegisterRoutes(engine *gin.Engine) {
	// Create API v1 group
	v1 := engine.Group("/api/v1")

	// Apply authentication middleware
	if r.config.JWTSecret != "" || tradingMiddleware.HasLocalAuthority() {
		v1.Use(tradingMiddleware.KongJWTMiddleware(r.config.JWTSecret, r.config.InternalGatewayToken, r.logger))
	}

	// Apply tenant middleware if database is configured
	if r.config.DB != nil {
		tenantMiddleware := middleware.NewTenantMiddleware(&middleware.TenantConfig{
			JWTSecret:           r.config.JWTSecret,
			RequireTenant:       false, // Don't require tenant for all endpoints
			AllowHeaderOverride: false,
			SkipPaths:           []string{"/api/v1/health", "/api/v1/credentials/exchanges"},
			Logger:              r.logger,
		}, r.config.DB)
		v1.Use(tenantMiddleware.Handler())
	}

	// Health endpoint (no auth required)
	engine.GET("/api/v1/health", r.healthCheck)

	// Local login (standalone mode): outside the JWT middleware by design.
	if r.config.PublicRoutes != nil {
		r.config.PublicRoutes(engine)
	}
	// Product API (paper reset, product status): behind the JWT middleware.
	if r.config.ProductRoutes != nil {
		r.config.ProductRoutes(v1)
	}

	// WebSocket endpoint (no middleware - handles auth internally)
	// This must be registered on the engine directly, not in the v1 group with middleware
	engine.GET("/api/v1/stream", r.wsHandler.HandleWebSocket)

	// AnkaAI Console — public read-only stats (no auth, no sensitive data).
	// Registered on the engine directly so the JWT middleware on v1 does not block them.
	// Write/admin endpoints (killswitch, policies, copy) are still gated under v1 with auth.
	if r.engineHandler != nil {
		engine.GET("/api/v1/engine/stats", r.engineHandler.GetStats)
		engine.GET("/api/v1/flywheel/events", r.engineHandler.GetFlywheelEvents)
		engine.GET("/api/v1/capital/ladder", r.engineHandler.GetCapitalLadder)
	}
	// F4.6: xsec market-neutral strategy status — public read-only stats, same
	// class as /engine/stats (paper equity curve + basket, no credentials/PII).
	// NOTE: the GET /api/v1/pairs/ranked public mirror on the :10600 MCP mux is
	// implemented in internal/mcp-trading/server + cmd/trading-mcp-server/main.go;
	// mirroring /xsec/status there requires touching those files (out of scope for
	// this change). Until then the endpoint is served wherever this Gin router is
	// mounted (REST API port).
	if r.xsecHandler != nil {
		// Optional auth: anonymous callers get paper-only data; a valid JWT
		// unlocks the real-account sections (see XSecHandler.GetStatus).
		engine.GET("/api/v1/xsec/status",
			tradingMiddleware.OptionalKongJWTMiddleware(r.config.JWTSecret, r.config.InternalGatewayToken, r.logger),
			r.xsecHandler.GetStatus)
		engine.GET("/api/v1/xsec/funding",
			tradingMiddleware.OptionalKongJWTMiddleware(r.config.JWTSecret, r.config.InternalGatewayToken, r.logger),
			r.xsecHandler.GetFunding)
		engine.GET("/api/v1/xsec/shadow", r.xsecHandler.GetShadowComparison)
		engine.GET("/api/v1/xsec/suggest-params", r.xsecHandler.SuggestParams)
	}
	if r.tournamentHandler != nil {
		engine.GET("/api/v1/tournament", r.tournamentHandler.List)
		engine.GET("/api/v1/tournament/:id", r.tournamentHandler.Get)
		engine.GET("/api/v1/tournament/:id/leaderboard", r.tournamentHandler.GetLeaderboard)
	}
	if r.marketplaceHandler != nil {
		engine.GET("/api/v1/marketplace/strategies", r.marketplaceHandler.GetStrategies)
	}
	if r.academyHandler != nil {
		engine.GET("/api/v1/academy/cohorts/current", r.academyHandler.GetCohorts)
	}
	// Exchanges list — read-only, no sensitive data.
	if r.exchangesHandler != nil {
		engine.GET("/api/v1/exchanges", r.exchangesHandler.GetExchanges)
	}
	// Specs — public read endpoints for Gate theater (no auth needed).
	if r.specHandler != nil {
		engine.GET("/api/v1/specs", r.specHandler.ListSpecs)
		// C0.5: seeded spec templates (public). Static path registered before the
		// "/specs/:id" param routes so gin routes "templates" as a literal.
		engine.GET("/api/v1/specs/templates", r.specHandler.GetTemplates)
		engine.POST("/api/v1/specs/:id/validate", r.specHandler.ValidateSpecPublic)
		engine.GET("/api/v1/specs/:id/validate/stream", r.specHandler.ValidateStream)
	}

	// C1.3 — consent template is public legal text (no auth needed to read it
	// before login/copy). Acceptance + status are JWT-gated (registerGovernanceRoutes).
	if r.governanceHandler != nil {
		engine.GET("/api/v1/consent/template/:kind", r.governanceHandler.GetConsentTemplate)
	}

	// Fase A — auth proxy (public): mints/refreshes the JWT via RantiPay Auth.
	// These are the only write endpoints allowed outside the JWT group because
	// they are the login entry point. The browser hits them same-origin.
	if r.authProxyHandler != nil {
		engine.POST("/api/v1/auth/login/email", r.authProxyHandler.LoginEmail)
		engine.POST("/api/v1/auth/login/phone", r.authProxyHandler.LoginPhone)
		engine.POST("/api/v1/auth/login/phone/verify", r.authProxyHandler.LoginPhoneVerify)
		engine.POST("/api/v1/auth/register/phone", r.authProxyHandler.RegisterPhone)
		engine.POST("/api/v1/auth/register/phone/verify", r.authProxyHandler.RegisterPhoneVerify)
		engine.POST("/api/v1/auth/login/social", r.authProxyHandler.LoginSocial)
		// Passwordless session orchestrator (D7): email/phone OTP login.
		engine.POST("/api/v1/auth/session", r.authProxyHandler.SessionCreate)
		engine.POST("/api/v1/auth/session/:id/answer", r.authProxyHandler.SessionAnswer)
		engine.POST("/api/v1/auth/session/:id/resend", r.authProxyHandler.SessionResend)
		engine.POST("/api/v1/auth/refresh", r.authProxyHandler.Refresh)
		engine.GET("/api/v1/auth/oauth/:provider/authorize", r.authProxyHandler.OAuthAuthorize)
		engine.POST("/api/v1/auth/oauth/:provider/native", r.authProxyHandler.OAuthNative)
		engine.GET("/api/v1/auth/providers", r.authProxyHandler.Providers)
	}

	// Register all route groups
	r.registerPortfolioRoutes(v1)
	r.registerOrderRoutes(v1)
	r.registerMarketRoutes(v1)
	r.registerCredentialRoutes(v1)
	r.registerTwoFARoutes(v1)
	r.registerAnalysisRoutes(v1)
	r.registerSubscriptionRoutes(v1)
	r.registerWebhookRoutes(engine) // Webhooks registered outside v1 group (no auth for Odoo callbacks)
	r.registerWebSocketStatusRoutes(v1)
	r.registerPairRoutes(v1)
	r.registerExchangeRoutes(v1)
	r.registerModeRoutes(v1)
	r.registerBotRoutes(v1)
	r.registerAdminRoutes(v1)
	r.registerVolatilityRoutes(v1)
	r.registerKYCRoutes(v1)
	r.registerBillingRoutes(v1)
	r.registerMeRoutes(v1)
	r.registerGovernanceRoutes(v1)
	r.registerAnkaAIRoutes(v1)
	r.registerXSecJobRoutes(v1)
	r.registerOperatorConsoleRoutes(v1)
	r.registerLaunchRoutes(v1)
	// NOTE (MU-5.3): the legacy /api/v1/ai-bot/* endpoints (single global bot) are
	// registered by mcpServer.RegisterAIBotHandler in main.go (not in this router).
	// They remain for backward compatibility and admin use. The JWT-scoped
	// /api/v1/bot/* endpoints introduced in MU-5.2 supersede them for per-user control.

	r.logger.Info("REST API v1 routes registered")
}

// registerPortfolioRoutes registers portfolio-related endpoints
func (r *Router) registerPortfolioRoutes(v1 *gin.RouterGroup) {
	if r.portfolioHandler == nil {
		r.logger.Warn("Portfolio handler not configured, skipping routes")
		return
	}

	portfolio := v1.Group("/portfolio")
	{
		portfolio.GET("", r.portfolioHandler.GetPortfolio)
		portfolio.GET("/balances", r.portfolioHandler.GetBalances)
		portfolio.GET("/overview", r.portfolioHandler.GetPortfolioOverview)
	}

	positions := v1.Group("/positions")
	{
		positions.GET("", r.portfolioHandler.GetPositions)
	}

	// GET /api/v1/bot/positions — open positions annotated with the bot's adoption
	// state (Phase 2 control panel). Mounted here (not in registerBotRoutes) so it
	// is available whenever the portfolio handler is configured, independent of the
	// bot config handler.
	v1.GET("/bot/positions", r.portfolioHandler.GetBotPositions)
}

// registerOrderRoutes registers order-related endpoints
func (r *Router) registerOrderRoutes(v1 *gin.RouterGroup) {
	if r.ordersHandler == nil {
		r.logger.Warn("Orders handler not configured, skipping routes")
		return
	}

	orders := v1.Group("/orders")
	if r.config.RateLimiter != nil {
		orders.Use(middleware.RateLimitMiddleware(r.config.RateLimiter, r.config.QuotaResolver))
	}
	{
		// Ring 0 (global stop) refuses new orders with 423 KILLSWITCH_ACTIVE.
		// Cancels and the direct close below stay available (de-risking).
		orders.POST("", handlers.Ring0Guard(), r.ordersHandler.CreateOrder)
		orders.GET("", r.ordersHandler.ListOrders)
		orders.GET("/:id", r.ordersHandler.GetOrder)
		orders.DELETE("/:id", r.ordersHandler.CancelOrder)

		// P1: direct-REST order endpoints. Bypass GoCryptoTrader's
		// enabled-currency restriction so any futures contract/position is
		// manageable. directOrdersHandler is constructed alongside ordersHandler
		// (both require cfg.GCTClient), so if we reached this point it is non-nil.
		orders.POST("/direct/close", r.directOrdersHandler.ClosePositionDirect)
		orders.POST("/direct/futures", r.directOrdersHandler.SubmitFuturesOrderDirect)
	}
}

// registerMarketRoutes registers market data endpoints
func (r *Router) registerMarketRoutes(v1 *gin.RouterGroup) {
	if r.marketHandler == nil {
		r.logger.Warn("Market handler not configured, skipping routes")
		return
	}

	market := v1.Group("/market")
	{
		market.GET("/ticker/:pair", r.marketHandler.GetTicker)
		market.GET("/orderbook/:pair", r.marketHandler.GetOrderbook)
		market.GET("/candles/:pair", r.marketHandler.GetCandles)
		market.GET("/trades/:pair", r.marketHandler.GetTrades)
	}
}

// registerCredentialRoutes registers credential management endpoints
// registerTwoFARoutes registers the /api/v1/twofa/* enrollment endpoints
// (BOT-04 fix). These sit behind the v1 group's global JWT auth (each handler
// reads userID from context) and are the only way an account can ever turn on
// the step-up 2FA that TwoFAMiddleware enforces on credential/bot mutations.
func (r *Router) registerTwoFARoutes(v1 *gin.RouterGroup) {
	if r.twofaHandler == nil {
		r.logger.Warn("2FA handler not configured (nil TOTPService or DB), skipping /twofa routes — TwoFAMiddleware, if set, can never see an enrolled user")
		return
	}

	twofaGroup := v1.Group("/twofa")
	{
		twofaGroup.POST("/setup", r.twofaHandler.Setup)
		twofaGroup.POST("/verify", r.twofaHandler.Verify)
		twofaGroup.POST("/disable", r.twofaHandler.Disable)
		twofaGroup.POST("/recover", r.twofaHandler.Recover)
	}
}

func (r *Router) registerCredentialRoutes(v1 *gin.RouterGroup) {
	if r.credHandler == nil {
		r.logger.Warn("Credentials handler not configured, skipping routes")
		return
	}

	// Build optional 2FA middleware chain for credential mutations
	twoFA := r.noopMiddleware()
	if r.config.TwoFAMiddleware != nil {
		twoFA = r.config.TwoFAMiddleware
	}

	creds := v1.Group("/credentials")
	if r.config.RateLimiter != nil {
		creds.Use(middleware.RateLimitMiddleware(r.config.RateLimiter, r.config.QuotaResolver))
	}
	{
		// Public endpoint - no auth needed
		creds.GET("/exchanges", r.credHandler.GetSupportedExchanges)

		// Authenticated endpoints
		creds.POST("", twoFA, r.credHandler.AddCredentials)
		creds.GET("", r.credHandler.ListCredentials)
		creds.DELETE("/:exchange", twoFA, r.credHandler.DeleteCredentials)
		creds.POST("/:exchange/test", r.credHandler.TestCredentials)

		// H0.3.5: verify exchange API key permissions (withdraw, ip-lock)
		creds.POST("/:exchange/verify-permissions", r.credHandler.VerifyPermissions)
	}
}

// registerAnalysisRoutes registers AI analysis endpoints
func (r *Router) registerAnalysisRoutes(v1 *gin.RouterGroup) {
	if r.analysisHandler == nil {
		r.logger.Warn("Analysis handler not configured, skipping routes")
		return
	}

	analysis := v1.Group("/analysis")
	{
		// Comprehensive analysis - Unified endpoint for Flutter (NEW)
		analysis.POST("/comprehensive", r.analysisHandler.GetComprehensiveAnalysis)

		// AI trading signal - Get trading recommendation
		analysis.POST("/signal", r.analysisHandler.GetTradingSignal)

		// Market sentiment analysis
		analysis.POST("/sentiment", r.analysisHandler.GetSentiment)

		// Backtest simulation
		analysis.POST("/backtest", r.analysisHandler.RunBacktest)

		// Technical indicators for a pair
		analysis.GET("/indicators/:pair", r.analysisHandler.GetIndicators)
	}
}

// registerSubscriptionRoutes registers subscription management endpoints
func (r *Router) registerSubscriptionRoutes(v1 *gin.RouterGroup) {
	if r.subscriptionHandler == nil {
		r.logger.Warn("Subscription handler not configured, skipping routes")
		return
	}

	sub := v1.Group("/subscription")
	{
		sub.GET("", r.subscriptionHandler.GetSubscription)
		sub.GET("/usage", r.subscriptionHandler.GetUsage)
		sub.POST("/validate", r.subscriptionHandler.ValidateSubscription)
		sub.GET("/tiers", r.subscriptionHandler.GetTiers)
		sub.GET("/compare", r.subscriptionHandler.CompareTiers)
		sub.GET("/features/:feature", r.subscriptionHandler.CheckFeature)
		sub.GET("/limits/:limit_type", r.subscriptionHandler.CheckLimit)
		sub.GET("/events", r.subscriptionHandler.GetEvents)
		sub.POST("/upgrade", r.subscriptionHandler.Upgrade)
	}
}

// registerWebhookRoutes registers webhook endpoints for external callbacks
func (r *Router) registerWebhookRoutes(engine *gin.Engine) {
	if r.webhookHandler == nil {
		r.logger.Warn("Webhook handler not configured, skipping routes")
		return
	}

	// Webhooks are registered without auth middleware as they use signature verification
	webhooks := engine.Group("/api/v1/webhooks")
	{
		webhooks.POST("/odoo", r.webhookHandler.HandleOdooSubscriptionWebhook)
		webhooks.POST("/odoo/subscription", r.webhookHandler.HandleOdooSubscriptionWebhook)
		webhooks.GET("/status", r.webhookHandler.GetWebhookStatus)
		webhooks.GET("/health", r.webhookHandler.HealthCheck)
	}
}

// healthCheck handles GET /api/v1/health
func (r *Router) healthCheck(c *gin.Context) {
	uptime := int64(0)
	if !r.config.StartTime.IsZero() {
		uptime = int64(time.Since(r.config.StartTime).Seconds())
	}

	version := r.config.Version
	if version == "" {
		version = "unknown"
	}

	response := models.HealthResponse{
		Status:    "healthy",
		Service:   "trading-mcp",
		Version:   version,
		Uptime:    uptime,
		Timestamp: time.Now(),
	}

	c.JSON(http.StatusOK, models.NewSuccessResponse(response))
}

// registerWebSocketStatusRoutes registers WebSocket status endpoints
func (r *Router) registerWebSocketStatusRoutes(v1 *gin.RouterGroup) {
	ws := v1.Group("/websocket")
	{
		ws.GET("/status", r.wsStatus)
	}
}

// wsStatus returns WebSocket hub status
func (r *Router) wsStatus(c *gin.Context) {
	if r.wsHub == nil {
		c.JSON(http.StatusServiceUnavailable, models.NewErrorResponse("WS_UNAVAILABLE", "WebSocket service not available"))
		return
	}

	c.JSON(http.StatusOK, models.NewSuccessResponse(gin.H{
		"status":        "operational",
		"total_clients": r.wsHub.GetClientCount(),
	}))
}

// GetWebSocketHub returns the WebSocket hub for external publishing
func (r *Router) GetWebSocketHub() *handlers.Hub {
	return r.wsHub
}

// registerPairRoutes registers user pair selection endpoints (DM-2.3).
func (r *Router) registerPairRoutes(v1 *gin.RouterGroup) {
	if r.pairsHandler == nil {
		r.logger.Warn("Pairs handler not configured, skipping /pairs routes")
		return
	}

	pairs := v1.Group("/pairs")
	{
		pairs.GET("/available", r.pairsHandler.GetAvailablePairs)
		pairs.GET("/selection", r.pairsHandler.GetSelection)
		pairs.PUT("/selection", r.pairsHandler.PutSelection)
		pairs.GET("/ranked", r.pairsHandler.GetRankedPairs)
	}
}

// registerVolatilityRoutes exposes the dynamic per-pair volatility tier table.
func (r *Router) registerVolatilityRoutes(v1 *gin.RouterGroup) {
	if r.volatilityHandler == nil {
		return
	}
	vol := v1.Group("/volatility")
	{
		vol.GET("", r.volatilityHandler.ListClassifications)
		vol.GET("/:pair", r.volatilityHandler.GetClassification)
		vol.POST("/:pair/override", r.volatilityHandler.SetOverride)
	}
}

// registerExchangeRoutes registers multi-exchange spread endpoints (DM-5.4).
func (r *Router) registerExchangeRoutes(v1 *gin.RouterGroup) {
	if r.exchangesHandler == nil {
		r.logger.Warn("Exchanges handler not configured, skipping /exchanges routes")
		return
	}

	exchanges := v1.Group("/exchanges")
	{
		// GET "" is registered on the engine directly (public, no JWT) in RegisterRoutes.
		exchanges.GET("/price-matrix/:pair", r.exchangesHandler.GetPriceMatrix)
		exchanges.GET("/spreads", r.exchangesHandler.GetSpreads)
		exchanges.GET("/:exchange/status", r.exchangesHandler.GetExchangeStatus) // EX-3: exchange status
	}
}

// registerModeRoutes registers trading mode control endpoints (Wave 2-A).
func (r *Router) registerModeRoutes(v1 *gin.RouterGroup) {
	if r.modeHandler == nil {
		r.logger.Warn("Mode handler not configured, skipping /mode routes")
		return
	}

	v1.GET("/mode", r.modeHandler.GetMode)
	v1.PUT("/mode", r.modeHandler.PutGlobalMode)
	v1.PUT("/mode/:pair", r.modeHandler.PutPairMode)
	v1.DELETE("/mode/:pair", r.modeHandler.DeletePairMode)
}

// registerAdminRoutes registers admin-only endpoints (H0.3.2).
// All routes under /admin require user_level >= 4 (Enterprise tier).
func (r *Router) registerAdminRoutes(v1 *gin.RouterGroup) {
	if r.emergencyStopHandler == nil {
		r.logger.Warn("Emergency stop handler not configured, skipping /admin routes")
		return
	}

	authMiddleware := tradingMiddleware.NewKongAuthMiddleware(r.logger)

	admin := v1.Group("/admin")
	admin.Use(authMiddleware.RequireLevel(4))
	{
		admin.POST("/bots/emergency-stop-all", r.emergencyStopHandler.EmergencyStopAll)
	}

	// StrategySpec lifecycle routes (API-1/2/3/4/5/6)
	if r.specHandler != nil {
		specs := v1.Group("/specs")
		{
			// Level 1+: read-only. GET "" is public (on engine, no JWT) in RegisterRoutes.
			specs.POST("", r.specHandler.CreateSpec)
			// JWT-authenticated own-specs (C0.5). The public GET /specs route has
			// no JWT middleware, so ?mine=true there cannot identify the caller and
			// 401s — which the web client treats as a dead session. This static
			// sibling of /:id is served under the JWT group so the user is known.
			specs.GET("/mine", r.specHandler.ListOwnSpecs)
			specs.GET("/:id", r.specHandler.GetSpec)
			specs.GET("/:id/verdict", r.specHandler.GetVerdict)
			specs.GET("/:id/robustness", r.specHandler.GetRobustness)
			specs.GET("/:id/copilot", r.specHandler.CopilotAnalyze)
			specs.POST("/:id/backtest", r.specHandler.BacktestSpec)
			specs.DELETE("/:id", r.specHandler.DeleteSpec)

			// Level 2+: demote. validate is public (on engine, no JWT) in RegisterRoutes.
			specsL2 := specs.Group("/:id")
			specsL2.Use(authMiddleware.RequireLevel(2))
			{
				specsL2.POST("/demote", r.specHandler.DemoteSpec)
			}

			// Promote (real-money human-gate). The required level and self-approval
			// are NOT a fixed middleware: PromoteSpec enforces them from the config
			// table (spec.promote_min_level / spec.allow_self_promotion) so the
			// tournament can tune them without code changes. Authenticated via v1 JWT.
			specs.POST("/:id/promote", r.specHandler.PromoteSpec)
		}
	}
}

// registerBotRoutes registers per-user bot control endpoints (MU-5.2).
// All endpoints are on the JWT-protected v1 group; user identity is derived
// from the validated JWT "sub" claim via userIDFromContext.
func (r *Router) registerBotRoutes(v1 *gin.RouterGroup) {
	if r.botConfigHandler == nil {
		r.logger.Warn("Bot config handler not configured, skipping /bot routes")
		return
	}

	// Build optional 2FA middleware for critical bot mutations (H0.4.4)
	twoFA := r.noopMiddleware()
	if r.config.TwoFAMiddleware != nil {
		twoFA = r.config.TwoFAMiddleware
	}

	bot := v1.Group("/bot")
	if r.config.RateLimiter != nil {
		bot.Use(middleware.RateLimitMiddleware(r.config.RateLimiter, r.config.QuotaResolver))
	}
	{
		bot.GET("/config", r.botConfigHandler.GetConfig)
		bot.PUT("/config", twoFA, r.botConfigHandler.PutConfig)
		bot.POST("/start", handlers.Ring0Guard(), r.botConfigHandler.StartBot)
		bot.POST("/stop", r.botConfigHandler.StopBot)
		bot.GET("/status", r.botConfigHandler.GetStatus)
		bot.POST("/emergency-stop", twoFA, r.botConfigHandler.EmergencyStop)
	}
}

// registerKYCRoutes registers KYC level inspection and flow-gating endpoints (API-9).
// GET /api/v1/kyc/level/:user_id — returns verified KYC level from Auth :10100.
// POST /api/v1/kyc/verify/:flow  — checks if caller's KYC meets a flow's requirement.
func (r *Router) registerKYCRoutes(v1 *gin.RouterGroup) {
	kyc := v1.Group("/kyc")
	{
		kyc.GET("/level/:user_id", r.kycHandler.GetLevel)
		kyc.POST("/verify/:flow", r.kycHandler.VerifyFlow)
	}
}

// registerBillingRoutes registers the performance fee ledger endpoints (CP-2).
// Collection is always OFF at runtime; these are read-only accrual endpoints.
func (r *Router) registerBillingRoutes(v1 *gin.RouterGroup) {
	billing := v1.Group("/billing")
	{
		billing.GET("/fees/:user_id", r.billingHandler.GetFees)
		billing.GET("/fees/:user_id/:period_id", r.billingHandler.GetFeePeriod)
	}
}

// registerMeRoutes registers caller-scoped identity/permission endpoints (C1.4).
// GET /api/v1/me/permissions — effective gates (level, required levels, kyc, locks).
func (r *Router) registerMeRoutes(v1 *gin.RouterGroup) {
	if r.permissionsHandler == nil {
		return
	}
	me := v1.Group("/me")
	{
		me.GET("/permissions", r.permissionsHandler.GetPermissions)
	}
}

// registerGovernanceRoutes wires the JWT-gated governance endpoints (C1.3/C3.2/C3.3).
// The public consent-template read is mounted on the engine in RegisterRoutes.
func (r *Router) registerGovernanceRoutes(v1 *gin.RouterGroup) {
	if r.governanceHandler == nil {
		return
	}

	// C1.3: consent acceptance + status (per-user, JWT).
	consent := v1.Group("/consent")
	{
		consent.GET("/status", r.governanceHandler.GetConsentStatus)
		consent.POST("", r.governanceHandler.PostConsent)
	}

	// C3.2: adversarial review queue (JWT).
	adversarial := v1.Group("/adversarial")
	{
		adversarial.GET("/queue", r.governanceHandler.GetAdversarialQueue)
		adversarial.POST("/:id/flag", r.governanceHandler.PostAdversarialFlag)
	}

	// C3.3: appeals / tribunal (JWT).
	appeals := v1.Group("/appeals")
	{
		appeals.GET("", r.governanceHandler.GetAppeals)
		appeals.POST("", r.governanceHandler.PostAppeal)
	}
}

// noopMiddleware returns a pass-through Gin handler used as a placeholder when
// an optional middleware (e.g. TwoFAMiddleware) has not been configured.
func (r *Router) noopMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		// BOT-04 fix: this used to be a silent pass-through, which is how the
		// 2FA gate on critical routes ended up a no-op with nobody noticing.
		// A loud Warn here means a regression (TwoFAMiddleware not wired again
		// in main.go) shows up in logs/alerts instead of silently degrading.
		r.logger.Warn("⚠️ noopMiddleware invoked — an optional security middleware (e.g. TwoFAMiddleware) is not configured; request allowed through unguarded")
		c.Next()
	}
}

// handler503 returns a Gin handler that always responds 503 Service Unavailable.
// Used to guard AnkaAI endpoints when a required handler was not initialised.
func handler503(name string) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.JSON(http.StatusServiceUnavailable, models.NewErrorResponse(
			"SERVICE_UNAVAILABLE",
			name+" handler not configured (DB may be nil)",
		))
	}
}

// registerAnkaAIRoutes wires the AnkaAI Console endpoints:
//   - Engine stats / flywheel / capital ladder  (no extra level gate)
//   - Academy progress/courses/cohorts          (JWT already applied to v1 group)
//   - Marketplace strategies/copy               (KYC gate inside handler)
//   - Trade explain + feedback                  (JWT already applied)
//   - Kill-switch (admin, level 4 group)
//   - Policies + tiers + moonshots              (level 3+ sub-group)
//   - Tournament CRUD                           (level gate inside handler / admin group)
func (r *Router) registerAnkaAIRoutes(v1 *gin.RouterGroup) {
	authMiddleware := tradingMiddleware.NewKongAuthMiddleware(r.logger)

	// Engine / flywheel / capital / tournament-read / marketplace-read are
	// registered on the raw engine (no JWT) in RegisterRoutes for public access.
	// Here we only wire the auth-required variants: user-specific academy progress
	// and marketplace write (copy setup).

	// ── Academy (JWT-gated: progress is per-user) ──────────────────────────────
	academy := v1.Group("/academy")
	if r.academyHandler != nil {
		academy.GET("/progress", r.academyHandler.GetProgress)
		academy.POST("/courses/:id/complete", r.academyHandler.CompleteStep)
		// cohorts/current is public (mounted on engine in RegisterRoutes)
	} else {
		unavail := handler503("academy")
		academy.GET("/progress", unavail)
		academy.POST("/courses/:id/complete", unavail)
	}

	// ── Marketplace (write endpoints require JWT) ──────────────────────────────
	marketplace := v1.Group("/marketplace")
	if r.marketplaceHandler != nil {
		// strategies-read is public (mounted on engine in RegisterRoutes)
		marketplace.POST("/copy", r.marketplaceHandler.PostCopy)
		marketplace.GET("/copy/status", r.marketplaceHandler.GetCopyStatus)
		marketplace.DELETE("/copy/:id", r.marketplaceHandler.DeleteCopy)
	} else {
		unavail := handler503("marketplace")
		marketplace.POST("/copy", unavail)
		marketplace.GET("/copy/status", unavail)
		marketplace.DELETE("/copy/:id", unavail)
	}

	// ── Trade history + explain + feedback ─────────────────────────────────────
	// History at GET /trades/history (per-user, JWT). NOTE: /api/v1/trades and
	// /api/v1/trades/:id are owned by MetricsHandler (public) — gin allows the
	// static "history" sibling alongside the "/:id" param, so this is safe.
	if r.explainHandler != nil {
		v1.GET("/trades/history", r.explainHandler.GetHistory)
		v1.GET("/trades/:id/explain", r.explainHandler.GetExplain)
		v1.POST("/trades/:id/feedback", r.explainHandler.PostFeedback)
	} else {
		unavail := handler503("explain")
		v1.GET("/trades/history", unavail)
		v1.GET("/trades/:id/explain", unavail)
		v1.POST("/trades/:id/feedback", unavail)
	}

	// ── Kill-switch (level 4 — same gate as existing admin group) ─────────────
	adminL4 := v1.Group("/admin")
	adminL4.Use(authMiddleware.RequireLevel(4))
	if r.killswitchHandler != nil {
		ks := adminL4.Group("/killswitch")
		{
			ks.GET("/status", r.killswitchHandler.GetStatus)
			ks.POST("/ring/1", r.killswitchHandler.Ring1)
			ks.POST("/ring/2", r.killswitchHandler.Ring2)
			ks.POST("/ring/0", r.killswitchHandler.Ring0)
			ks.POST("/ring/0/reset", r.killswitchHandler.Ring0Reset)
		}
	}

	// ── Policies (level 3+ read; level 4 write enforced inside handler) ────────
	adminL3 := v1.Group("/admin")
	adminL3.Use(authMiddleware.RequireLevel(3))
	if r.policiesHandler != nil {
		// C1.4 — Governance config is READ-ONLY visible to any authenticated user
		// (the Sala de Políticas renders it in read-only mode + a lock banner when
		// the caller lacks write level). These values are platform policy/economy,
		// not secrets. WRITES stay gated at level 3 + the sign-off ceremony (C1.6).
		adminRead := v1.Group("/admin")
		{
			adminRead.GET("/policies", r.policiesHandler.GetPolicies)
			adminRead.GET("/policies/robustness", r.policiesHandler.GetRobustness)
			adminRead.GET("/tiers", r.policiesHandler.GetTiers)
			adminRead.GET("/moonshots", r.policiesHandler.GetMoonshots)
		}
		adminL3.PUT("/policies", r.policiesHandler.UpdatePolicies)
	}

	// ── Dynamic config params (gate knobs / scalping control panel) ───────────
	// Generic dynamic-config API on the JWT plane so the web console can read and
	// tune the bot's gate without an admin token in the browser. Reuses the SAME
	// ConfigHandler also mounted at /admin/config (admin-token plane, for
	// CLI/retrain) — identical logic, different auth.
	//
	// Reads: any authenticated user. Writes: level 1 (any authenticated user) —
	// a deliberate choice for this single-operator install so the owner can drive
	// the scalping gate from their normal account. The writes are still
	// JWT-authenticated and fully audited (every change records by/reason/old→new),
	// and the admin-token plane stays the stricter path for automation. Raise this
	// for a multi-tenant deployment where bot config must not be operator-writable.
	if r.config.ConfigStore != nil {
		configH := confighandlers.NewConfigHandler(r.config.ConfigStore)
		// Read + write on ONE JWT group with no extra RequireLevel. The v1
		// KongJWTMiddleware already authenticates the token and floors any valid
		// JWT to level 1, and getRequiredLevelForPath returns 1 for /admin/config.
		// A RequireLevel(1) here instead read user_level=0 (KongAuthMiddleware does
		// not re-populate it on this path) and 403'd every write — which is exactly
		// why the GET reads (no RequireLevel) work while writes failed. Mirroring
		// the reads makes writes work, and the effective gate is unchanged: any
		// authenticated user (level 1). Every write stays JWT-authed + audited.
		cfgGrp := v1.Group("/admin/config")
		configH.Register(cfgGrp)
	} else {
		// P3b: ConfigStore not wired — register the same group with a clean 503
		// so the web console gets a well-formed error instead of a missing route.
		unavail := handler503("config_store")
		cfgGrp := v1.Group("/admin/config")
		cfgGrp.Any("", unavail)
		cfgGrp.Any("/*path", unavail)
	}

	// ── Tournament write (read endpoints are public, mounted on engine) ─────────
	// GET /tournament, /:id, /:id/leaderboard are on the engine (no JWT).
	// POST vote requires JWT (per-user rate limit). Admin actions require level 4.
	tournament := v1.Group("/tournament")
	if r.tournamentHandler != nil {
		tournament.POST("/:id/vote", r.tournamentHandler.Vote)
		adminL4.POST("/tournament", r.tournamentHandler.Create)
		adminL4.POST("/tournament/:id/control", r.tournamentHandler.Control)
	} else {
		unavail := handler503("tournament")
		tournament.POST("/:id/vote", unavail)
	}
}

// registerOperatorConsoleRoutes registers the strict-JWT read endpoints of the
// operator console: real account equity history and aggregated system health.
// Both reveal account/topology data, so they live only on the v1 group.
func (r *Router) registerOperatorConsoleRoutes(v1 *gin.RouterGroup) {
	// Without a JWT secret the v1 group has no auth middleware; never expose
	// account or topology data in that configuration.
	if r.config.JWTSecret == "" {
		r.logger.Warn("JWT secret not configured, skipping /account/equity-history and /system/health routes")
		return
	}
	if r.accountEquityHandler != nil {
		v1.GET("/account/equity-history", r.accountEquityHandler.GetEquityHistory)
	}
	if r.systemHealthHandler != nil {
		v1.GET("/system/health", r.systemHealthHandler.GetSystemHealth)
	}
}

// registerLaunchRoutes registers the AnkaLaunch phase-1 console endpoints
// under /launch on the strict-JWT v1 group. Level/feature checks
// (launch_operate = level 3, launch_analyze = level 2) are done inside the
// handlers. Contract: docs/bot/launch-api-phase1.md.
func (r *Router) registerLaunchRoutes(v1 *gin.RouterGroup) {
	if r.config.OmitCompanionServices {
		return
	}
	if r.config.JWTSecret == "" {
		r.logger.Warn("JWT secret not configured, skipping /launch routes")
		return
	}
	h := handlers.NewLaunchHandler(r.config.LaunchDB, launchapi.RequirementsFromStore(r.config.ConfigStore), r.logger)
	g := v1.Group("/launch")
	{
		g.GET("/status", h.GetStatus)
		g.GET("/opportunities", h.ListOpportunities)
		g.GET("/opportunities/:id", h.GetOpportunity)
		g.POST("/opportunities/:id/decision", h.Decide)
		g.GET("/approvals", h.ListApprovals)
		g.GET("/positions", h.ListPositions)
		g.GET("/positions/:id", h.GetPosition)
		g.GET("/strategies", h.ListStrategies)
		g.POST("/strategies/:id/versions", h.CreateStrategyVersion)
		g.GET("/gate", h.GetGate)
		g.GET("/reports/human-lift", h.GetHumanLift)
		g.POST("/kill-switch", h.KillSwitch)
		g.POST("/resume", h.Resume)
		g.POST("/real-mode/enable", h.EnableRealMode)
	}
}

// registerXSecJobRoutes registers the xsec-live async job runner endpoints
// (F4.7). All routes are on the JWT-protected v1 group — job creation for
// modes rebalance-live/cancel-order and every xsec_params write additionally
// require account Level 2+, enforced inside the handlers (job creation is
// gated by request body mode, not a static path, so it cannot be a route
// middleware; the params write route uses the standard RequireLevel(2)).
// GET /api/v1/xsec/status (read-only paper strategy stats) stays public,
// registered on the engine directly in RegisterRoutes — unrelated to jobs.
func (r *Router) registerXSecJobRoutes(v1 *gin.RouterGroup) {
	if r.xsecJobsHandler == nil {
		r.logger.Warn("XSec jobs handler not configured, skipping /xsec/jobs and /xsec/params routes")
		return
	}
	authMiddleware := tradingMiddleware.NewKongAuthMiddleware(r.logger)

	jobs := v1.Group("/xsec/jobs")
	{
		jobs.POST("", r.xsecJobsHandler.CreateJob)
		jobs.GET("", r.xsecJobsHandler.ListJobs)
		jobs.GET("/:id", r.xsecJobsHandler.GetJob)
	}

	params := v1.Group("/xsec/params")
	{
		params.GET("", r.xsecJobsHandler.GetParams)
		paramsL2 := params.Group("")
		paramsL2.Use(authMiddleware.RequireLevel(handlers.XSecParamsWriteLevel))
		paramsL2.PUT("", r.xsecJobsHandler.PutParam)
	}

	// Shadow A/B reset — Level 2 (discards paper history). The GET comparison is
	// the public /xsec/shadow route registered with the other read endpoints.
	shadow := v1.Group("/xsec/shadow")
	{
		shadowL2 := shadow.Group("")
		shadowL2.Use(authMiddleware.RequireLevel(handlers.XSecParamsWriteLevel))
		shadowL2.DELETE("/:scorer", r.xsecJobsHandler.ResetShadow)
	}
}

// Shutdown gracefully shuts down the router components
func (r *Router) Shutdown() {
	if r.wsHub != nil {
		r.logger.Info("Shutting down WebSocket hub")
		r.wsHub.Stop()
	}
}
