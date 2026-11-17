package main

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
	"github.com/joho/godotenv"
	"github.com/shopspring/decimal"
	"github.com/spf13/viper"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"
	aiCore "github.com/rantipay/trading-mcp/internal/ai"
	"github.com/rantipay/trading-mcp/internal/ai/calendar"
	"github.com/rantipay/trading-mcp/internal/ai/featureeng"
	"github.com/rantipay/trading-mcp/internal/ai/feedback"
	"github.com/rantipay/trading-mcp/internal/ai/knowledge"
	"github.com/rantipay/trading-mcp/internal/ai/learning"
	"github.com/rantipay/trading-mcp/internal/ai/llm"
	"github.com/rantipay/trading-mcp/internal/ai/manipulation"
	aiMonitoring "github.com/rantipay/trading-mcp/internal/ai/monitoring"
	"github.com/rantipay/trading-mcp/internal/ai/patterns"
	"github.com/rantipay/trading-mcp/internal/ai/prompts"
	"github.com/rantipay/trading-mcp/internal/ai/regime"
	"github.com/rantipay/trading-mcp/internal/ai/sentiment"
	"github.com/rantipay/trading-mcp/internal/ai/validation"
	backtestCore "github.com/rantipay/trading-mcp/internal/backtest"
	backtestPg "github.com/rantipay/trading-mcp/internal/backtest/repository/postgres"
	aiConfig "github.com/rantipay/trading-mcp/internal/config"
	dynamic "github.com/rantipay/trading-mcp/internal/config/dynamic"
	configdomain "github.com/rantipay/trading-mcp/internal/config/dynamic/domain"
	configPg "github.com/rantipay/trading-mcp/internal/config/dynamic/infrastructure/postgres"
	"github.com/rantipay/trading-mcp/internal/config/dynamic/manifest"
	"github.com/rantipay/trading-mcp/internal/credentials"
	"github.com/rantipay/trading-mcp/internal/edition"
	exch "github.com/rantipay/trading-mcp/internal/exchange"
	"github.com/rantipay/trading-mcp/internal/gateway"
	"github.com/rantipay/trading-mcp/internal/gct"
	"github.com/rantipay/trading-mcp/internal/handlers"
	"github.com/rantipay/trading-mcp/internal/health"
	iatbWebsocket "github.com/rantipay/trading-mcp/internal/iatbws"
	"github.com/rantipay/trading-mcp/internal/killswitch"
	"github.com/rantipay/trading-mcp/internal/launch/launchapi"
	"github.com/rantipay/trading-mcp/internal/license"
	licensehttp "github.com/rantipay/trading-mcp/internal/license/httpapi"
	"github.com/rantipay/trading-mcp/internal/localauth"
	"github.com/rantipay/trading-mcp/internal/master"
	masterAdapters "github.com/rantipay/trading-mcp/internal/master/adapters"
	"github.com/rantipay/trading-mcp/internal/master/universe"
	"github.com/rantipay/trading-mcp/internal/mcp-trading/config"
	"github.com/rantipay/trading-mcp/internal/mcp-trading/gctclient"
	"github.com/rantipay/trading-mcp/internal/mcp-trading/server"
	"github.com/rantipay/trading-mcp/internal/mcp-trading/tools/backtest"
	"github.com/rantipay/trading-mcp/internal/mcp-trading/tools/futures"
	launchtools "github.com/rantipay/trading-mcp/internal/mcp-trading/tools/launch"
	llmtools "github.com/rantipay/trading-mcp/internal/mcp-trading/tools/llm"
	"github.com/rantipay/trading-mcp/internal/mcp-trading/tools/marketdata"
	modetools "github.com/rantipay/trading-mcp/internal/mcp-trading/tools/mode"
	notificationtools "github.com/rantipay/trading-mcp/internal/mcp-trading/tools/notifications"
	"github.com/rantipay/trading-mcp/internal/mcp-trading/tools/orders"
	pairtools "github.com/rantipay/trading-mcp/internal/mcp-trading/tools/pairs"
	"github.com/rantipay/trading-mcp/internal/mcp-trading/tools/portfolio"
	"github.com/rantipay/trading-mcp/internal/mcp-trading/tools/risk"
	"github.com/rantipay/trading-mcp/internal/mcp-trading/tools/scalping"
	"github.com/rantipay/trading-mcp/internal/mcp-trading/tools/technical"
	websockettools "github.com/rantipay/trading-mcp/internal/mcp-trading/tools/websocket"
	"github.com/rantipay/trading-mcp/internal/middleware"
	"github.com/rantipay/trading-mcp/internal/monitoring"
	"github.com/rantipay/trading-mcp/internal/notifications"
	"github.com/rantipay/trading-mcp/internal/optimization"
	optimizerPg "github.com/rantipay/trading-mcp/internal/optimization/repository/postgres"
	"github.com/rantipay/trading-mcp/internal/orderflow"
	"github.com/rantipay/trading-mcp/internal/paper"
	"github.com/rantipay/trading-mcp/internal/productapi"
	internalRisk "github.com/rantipay/trading-mcp/internal/risk"
	"github.com/rantipay/trading-mcp/internal/scalping/ai"
	"github.com/rantipay/trading-mcp/internal/scalping/coordinator"
	"github.com/rantipay/trading-mcp/internal/scalping/dashboard"
	"github.com/rantipay/trading-mcp/internal/scalping/engine"
	"github.com/rantipay/trading-mcp/internal/scalping/executor"
	"github.com/rantipay/trading-mcp/internal/scalping/models"
	scalpingRisk "github.com/rantipay/trading-mcp/internal/scalping/risk"
	"github.com/rantipay/trading-mcp/internal/scalping/strategies"
	"github.com/rantipay/trading-mcp/internal/services"
	"github.com/rantipay/trading-mcp/internal/storage"
	postgresStorage "github.com/rantipay/trading-mcp/internal/storage/postgres"
	portfoliotracker "github.com/rantipay/trading-mcp/internal/tracker"
	tradingcore "github.com/rantipay/trading-mcp/internal/trading"
	tradingmw "github.com/rantipay/trading-mcp/internal/trading/middleware"
	tradingmode "github.com/rantipay/trading-mcp/internal/trading/mode"
	"github.com/rantipay/trading-mcp/internal/utils"
	"github.com/rantipay/trading-mcp/internal/volatility"
	"github.com/rantipay/trading-mcp/internal/xsec"
	"github.com/sirupsen/logrus"
	gormsqlite "gorm.io/driver/sqlite"
	"gorm.io/gorm"

	apiv1 "github.com/rantipay/trading-mcp/internal/api/v1"
	apiv1handlers "github.com/rantipay/trading-mcp/internal/api/v1/handlers"
	"github.com/rantipay/trading-mcp/internal/billing"
	"github.com/rantipay/trading-mcp/internal/strategy"
	"github.com/rantipay/trading-mcp/internal/twofa"
)

var (
	configPath  = flag.String("config", "configs/mcp-trading/config.yaml", "Path to configuration file")
	showVersion = flag.Bool("version", false, "Print version, commit and edition, then exit")
	version     = "0.1.0"
)

// gctTickerAdapter converts *gctclient.Client to iatbws.tickerGetter.
// Only GetTicker is needed; the full gct.GCTClient interface is not required.
type gctTickerAdapter struct{ client *gctclient.Client }

func (a *gctTickerAdapter) GetTicker(ctx context.Context, exchange, symbol string) (*iatbWebsocket.Ticker, error) {
	ticker, err := a.client.GetTicker(ctx, exchange, symbol)
	if err != nil {
		return nil, err
	}
	return &iatbWebsocket.Ticker{
		Exchange:      ticker.Exchange,
		Symbol:        ticker.Pair,
		Bid:           decimal.NewFromFloat(ticker.Bid),
		Ask:           decimal.NewFromFloat(ticker.Ask),
		Last:          decimal.NewFromFloat(ticker.Last),
		High:          decimal.NewFromFloat(ticker.High24h),
		Low:           decimal.NewFromFloat(ticker.Low24h),
		Volume:        decimal.NewFromFloat(ticker.Volume),
		QuoteVolume:   decimal.NewFromFloat(ticker.Volume * ticker.Last),
		Open:          decimal.NewFromFloat(ticker.Last - ticker.Change24h),
		Change:        decimal.NewFromFloat(ticker.Change24h),
		ChangePercent: decimal.NewFromFloat((ticker.Change24h / (ticker.Last - ticker.Change24h)) * 100),
		Timestamp:     ticker.Timestamp,
	}, nil
}

// tradingModeRepoAdapter adapts *storage.TradingModeRepository to the
// tradingmode.ModeRepository interface without creating an import cycle.
// Lives in the cmd binary so the storage and trading/mode packages stay decoupled.
type tradingModeRepoAdapter struct {
	inner *storage.TradingModeRepository
}

func (a *tradingModeRepoAdapter) GetGlobalRow(ctx context.Context) (*tradingmode.TradingModeRow, error) {
	m, err := a.inner.GetGlobal(ctx)
	if err != nil || m == nil {
		return nil, err
	}
	return &tradingmode.TradingModeRow{
		Scope:         m.Scope,
		Pair:          m.Pair,
		MarketType:    m.MarketType,
		ModeSelection: m.ModeSelection,
		Leverage:      m.Leverage,
	}, nil
}

func (a *tradingModeRepoAdapter) GetForPairRow(ctx context.Context, pair string) (*tradingmode.TradingModeRow, error) {
	m, err := a.inner.GetForPair(ctx, pair)
	if err != nil || m == nil {
		return nil, err
	}
	return &tradingmode.TradingModeRow{
		Scope:         m.Scope,
		Pair:          m.Pair,
		MarketType:    m.MarketType,
		ModeSelection: m.ModeSelection,
		Leverage:      m.Leverage,
	}, nil
}

// xsecBookReaderAdapter bridges internal/xsec.ActiveLiveBookPairs to the
// services.XSECBookReader interface required by the scalper bot's XSEC
// exclusion guards. Lives in the cmd binary — internal/services cannot import
// internal/xsec directly (import cycle via internal/storage -> internal/master
// -> internal/services), so this thin wrapper is the decoupling point.
type xsecBookReaderAdapter struct {
	db *sql.DB
}

func (a *xsecBookReaderAdapter) ActiveLiveBookPairs() (map[string]int, error) {
	return xsec.ActiveLiveBookPairs(a.db)
}

// tradeStatsAdapter bridges storage.TradeHistoryRepository to the
// services.TradeStatsReader interface required by the bot for Kelly sizing.
// Lives in the cmd binary so the storage and services packages stay decoupled.
type tradeStatsAdapter struct {
	repo *storage.TradeHistoryRepository
}

// scalpingHistoryAdapter implements learning.HistoryLoader over TradeHistoryRepository.
type scalpingHistoryAdapter struct {
	repo *storage.TradeHistoryRepository
}

func (a *scalpingHistoryAdapter) GetDistinctScalpingPairs() ([]string, error) {
	return a.repo.GetDistinctPairs()
}

func (a *scalpingHistoryAdapter) GetScalpingClosedTrades(pair string, limit int) ([]learning.ClosedTradeRecord, error) {
	trades, err := a.repo.GetByPair(pair, limit)
	if err != nil {
		return nil, err
	}
	out := make([]learning.ClosedTradeRecord, 0, len(trades))
	for _, t := range trades {
		if t.TradingStyle != "scalping" || t.Status != "closed" {
			continue
		}
		out = append(out, learning.ClosedTradeRecord{
			ID:         t.ID,
			Pair:       t.Pair,
			Style:      t.TradingStyle,
			Side:       t.Side,
			Status:     t.Status,
			EntryPrice: t.EntryPrice,
			ExitPrice:  t.ExitPrice,
			StopLoss:   t.StopLoss,
			TakeProfit: t.TakeProfit,
			PnL:        t.PnL,
			PnLPct:     t.PnLPct,
			ExitReason: t.ExitReason,
		})
	}
	return out, nil
}

// userBotConfigStoreAdapter adapts *storage.UserBotConfigRepository to
// services.UserBotConfigStore so BotManager does not import internal/storage
// (which would create an import cycle via storage→master→services).
type userBotConfigStoreAdapter struct {
	repo *storage.UserBotConfigRepository
}

func (a *userBotConfigStoreAdapter) Get(ctx context.Context, userID, exchange string) (*services.UserBotCfg, error) {
	row, err := a.repo.Get(ctx, userID, exchange)
	if err != nil {
		return nil, err
	}
	return &services.UserBotCfg{
		UserID:              row.UserID,
		Exchange:            row.Exchange,
		Enabled:             row.Enabled,
		TradeSizeUSD:        row.TradeSizeUSD,
		Leverage:            row.Leverage,
		MarketType:          row.MarketType,
		ModeSelection:       row.ModeSelection,
		ConfidenceThreshold: row.ConfidenceThreshold,
		MaxDailyLossUSD:     row.MaxDailyLossUSD,
		MaxOpenPositions:    row.MaxOpenPositions,
		DryRun:              row.DryRun,
		ConfigJSON:          row.ConfigJSON,
	}, nil
}

func (a *userBotConfigStoreAdapter) Upsert(ctx context.Context, cfg *services.UserBotCfg) error {
	return a.repo.Upsert(ctx, &storage.UserBotConfig{
		UserID:              cfg.UserID,
		Exchange:            cfg.Exchange,
		Enabled:             cfg.Enabled,
		TradeSizeUSD:        cfg.TradeSizeUSD,
		Leverage:            cfg.Leverage,
		MarketType:          cfg.MarketType,
		ModeSelection:       cfg.ModeSelection,
		ConfidenceThreshold: cfg.ConfidenceThreshold,
		MaxDailyLossUSD:     cfg.MaxDailyLossUSD,
		MaxOpenPositions:    cfg.MaxOpenPositions,
		DryRun:              cfg.DryRun,
		ConfigJSON:          cfg.ConfigJSON,
	})
}

func (a *tradeStatsAdapter) GetTradeStatsByPair(pair string, limit int) ([]services.TradeStat, error) {
	trades, err := a.repo.GetByPair(pair, limit)
	if err != nil {
		return nil, err
	}
	out := make([]services.TradeStat, 0, len(trades))
	for _, t := range trades {
		if t == nil {
			continue
		}
		out = append(out, services.TradeStat{PnLPct: t.PnLPct})
	}
	return out, nil
}

// tradeHistoryWriterAdapter bridges *storage.TradeHistoryRepository to the
// services.TradeHistoryWriter interface so the bot can persist closed trades
// without importing internal/storage (which would create an import cycle).
type tradeHistoryWriterAdapter struct {
	repo *storage.TradeHistoryRepository
}

func (a *tradeHistoryWriterAdapter) SaveClosedTrade(userID string, t *services.ClosedTrade) error {
	if t == nil {
		return nil
	}
	return a.repo.SaveForUser(userID, &storage.Trade{
		ID:               t.ID,
		Pair:             t.Pair,
		Exchange:         t.Exchange,
		Side:             t.Side,
		TradingStyle:     "ai_bot",
		TradingStyleType: "ai_bot",
		PrimaryTimeframe: "multi",
		EntryPrice:       t.EntryPrice,
		EntryTime:        t.EntryTime,
		Size:             t.Size,
		Leverage:         t.Leverage,
		ExitPrice:        t.ExitPrice,
		ExitTime:         t.ExitTime,
		ExitReason:       t.ExitReason,
		StopLoss:         t.StopLoss,
		TakeProfit:       t.TakeProfit,
		PnL:              t.PnL,
		PnLPct:           t.PnLPct,
		Fees:             t.Fees,
		NetPnL:           t.NetPnL,
		Confidence:       t.Confidence,
		DecisionReason:   t.DecisionReason,
		Status:           "closed",
	})
}

// GetTodayNetLossForUser satisfies services.DailyLossLoader so the bot seeds
// dailyLoss on restart and the daily cap is not silently reset.
func (a *tradeHistoryWriterAdapter) GetTodayNetLossForUser(userID string) (float64, error) {
	return a.repo.GetTodayNetLossForUser(userID)
}

// GetTrailingConsecutiveLosses satisfies services.ConsecutiveLossSeeder so the
// per-pair auto-pause streaks survive a restart instead of resetting mid-streak.
func (a *tradeHistoryWriterAdapter) GetTrailingConsecutiveLosses(userID string) (map[string]int, error) {
	return a.repo.GetTrailingConsecutiveLosses(userID)
}

// GlobalTailLossStreak satisfies services.GlobalLossStreakReader so the global
// streak-pause throttle (bot.streak_pause.*) reads the account-wide loss tail
// straight from trade_history — shared across fleet bots and restart-proof.
func (a *tradeHistoryWriterAdapter) GlobalTailLossStreak(userID string) (int, time.Time, error) {
	return a.repo.GlobalTailLossStreak(userID)
}

// tradeHistoryReaderAdapter bridges *storage.TradeHistoryRepository to the
// server.TradeHistoryReader interface so the MCP server can serve the REST
// GET /api/v1/trade-history endpoint without importing internal/storage
// (which would create an import cycle).
type tradeHistoryReaderAdapter struct {
	repo *storage.TradeHistoryRepository
}

// toServerTradeRecords maps storage trades to the minimal server representation.
func toServerTradeRecords(trades []*storage.Trade) []*server.TradeRecord {
	out := make([]*server.TradeRecord, 0, len(trades))
	for _, t := range trades {
		if t == nil {
			continue
		}
		out = append(out, &server.TradeRecord{
			ID:             t.ID,
			Pair:           t.Pair,
			Exchange:       t.Exchange,
			Side:           t.Side,
			EntryPrice:     t.EntryPrice,
			EntryTime:      t.EntryTime,
			Size:           t.Size,
			Leverage:       t.Leverage,
			ExitPrice:      t.ExitPrice,
			ExitTime:       t.ExitTime,
			ExitReason:     t.ExitReason,
			PnL:            t.PnL,
			PnLPct:         t.PnLPct,
			NetPnL:         t.NetPnL,
			Confidence:     t.Confidence,
			DecisionReason: t.DecisionReason,
			Status:         t.Status,
		})
	}
	return out
}

func (a *tradeHistoryReaderAdapter) GetClosedTrades(limit int) ([]*server.TradeRecord, error) {
	trades, err := a.repo.GetClosedTrades(limit)
	if err != nil {
		return nil, err
	}
	return toServerTradeRecords(trades), nil
}

func (a *tradeHistoryReaderAdapter) GetOpenTrades() ([]*server.TradeRecord, error) {
	trades, err := a.repo.GetOpenTrades()
	if err != nil {
		return nil, err
	}
	return toServerTradeRecords(trades), nil
}

func (a *tradeHistoryReaderAdapter) GetByPair(pair string, limit int) ([]*server.TradeRecord, error) {
	trades, err := a.repo.GetByPair(pair, limit)
	if err != nil {
		return nil, err
	}
	return toServerTradeRecords(trades), nil
}

// applyGlobalScalarConfig pushes global-scoped dynamic-config values into the
// package-level setters of components that have no ConfigStore handle of their
// own (audit P1 follow-up). It only overrides a key when present in the DB, so
// an unconfigured key keeps the component's code default. Called once at startup
// and again after every hot-reload.
func applyGlobalScalarConfig(store *dynamic.ConfigStore, log *logrus.Logger) {
	if store == nil {
		return
	}
	snap := store.SnapshotForScope([]dynamic.ScopeRef{{Type: configdomain.ScopeGlobal, ID: "*"}})
	if snap == nil {
		return
	}
	if v, ok := snap.IntOk("ai.featureeng.min_candles"); ok {
		featureeng.SetMinCandles(v)
	}
	if v, ok := snap.FloatOk("ai.featureeng.flat_threshold_pct"); ok {
		featureeng.SetFlatThreshold(v)
	}
	if v, ok := snap.FloatOk("regime.atr.chaos_threshold"); ok {
		tradingmode.SetChaosATRThreshold(v)
	}
	if v, ok := snap.FloatOk("ai.feedback.ml_action_threshold"); ok {
		feedback.SetMLActionThreshold(v)
	}
	log.Debug("audit P1: applied global-scoped scalar config to package-level setters")
}

func main() {
	flag.Parse()

	if *showVersion {
		printVersion()
		return
	}

	// Task 8.1: the one decision block of the product profile.
	profile := newProductProfile(edition.IsProduct())

	// Configurar logger básico PRIMERO
	log := logrus.New()
	log.SetFormatter(&logrus.JSONFormatter{})
	log.SetLevel(logrus.InfoLevel)
	log.AddHook(&middleware.LogScrubberHook{})

	// Cargar variables de entorno desde .env
	if err := godotenv.Load(); err != nil {
		log.Warn("No .env file found, using system environment variables")
	} else {
		log.Info("Loaded environment variables from .env file")
	}

	// Subcommand mode: ./trading-mcp-server config ...
	if len(os.Args) >= 2 && os.Args[1] == "config" {
		if err := handleConfigCLI(os.Args[2:]); err != nil {
			log.WithError(err).Fatal("config CLI failed")
		}
		return
	}

	// Subcommand mode: ./trading-mcp-server [-config f] operator <set-password|rotate-secret>
	// Runs before anything is connected: it only works on the auth data directory.
	if flag.NArg() >= 1 && flag.Arg(0) == "operator" {
		if err := runOperatorSubcommand(*configPath, flag.Args()[1:]); err != nil {
			fmt.Fprintln(os.Stderr, "operator:", err)
			os.Exit(1)
		}
		return
	}

	// Validar variables críticas
	requiredEnvVars := []string{"GCT_ADDRESS", "GCT_USERNAME", "GCT_PASSWORD"}
	missingVars := []string{}
	for _, envVar := range requiredEnvVars {
		if os.Getenv(envVar) == "" {
			missingVars = append(missingVars, envVar)
		}
	}
	if len(missingVars) > 0 {
		if profile.RequireGCTEnv {
			log.WithField("missing_vars", missingVars).Fatal("Required environment variables not set")
		}
		log.WithField("missing_vars", missingVars).Warn("GoCryptoTrader is not configured (GCT_ADDRESS, GCT_USERNAME, GCT_PASSWORD): the server starts without exchange connectivity, so market data tools and the paper broker will report DATA_UNAVAILABLE until GoCryptoTrader is configured and reachable")
	}

	log.WithFields(logrus.Fields{
		"version": version,
		"config":  *configPath,
	}).Info("Starting Trading MCP Server")

	// Cargar configuración
	cfg, err := config.Load(*configPath)
	if err != nil {
		log.WithError(err).Fatal("Failed to load configuration")
	}

	// Configurar logger según configuración
	configureLogger(log, cfg)

	log.WithFields(logrus.Fields{
		"edition":              edition.Build(),
		"run_ai_bot":           profile.RunAIBot,
		"run_scalping":         profile.RunScalping,
		"run_master_agent":     profile.RunMasterAgent,
		"run_xsec":             profile.RunXSEC,
		"run_ankalaunch":       profile.RunAnkaLaunch,
		"run_tournament":       profile.RunTournament,
		"run_rantipay_service": profile.RunRantiPayServices,
	}).Info("Boot profile")

	// ================================================================
	// DYNAMIC CONFIG SUBSYSTEM (PostgreSQL-backed)
	// Skipped gracefully when TRADING_CONFIG_DB_PASSWORD or host is unset.
	// ================================================================
	var cfgStore *dynamic.ConfigStore
	var configPool *pgxpool.Pool // shared reference used by backtest subsystem (TC-7)

	// config.Load uses a local viper instance, so the global viper is empty here.
	// Load the primary config file into the global viper so postgres.config_db.* resolves.
	viper.SetConfigFile(*configPath)
	if readErr := viper.ReadInConfig(); readErr != nil {
		log.WithError(readErr).Warn("Dynamic config: could not load config file into global viper")
	}

	pgHost := viper.GetString("postgres.config_db.host")
	pgPassword := os.Getenv("TRADING_CONFIG_DB_PASSWORD")

	if pgHost == "" || pgPassword == "" {
		log.Warn("Dynamic config DB not configured, skipping (bot will use YAML config only)")
		if profile.Product {
			log.Warn("Postgres is not configured (postgres.config_db.host and TRADING_CONFIG_DB_PASSWORD): running with the YAML defaults; runtime-tunable parameters, hot reload and the config backup are unavailable. Start the Postgres service and set TRADING_CONFIG_DB_PASSWORD to enable them")
		}
	} else {
		pgCfg := postgresStorage.Config{
			Host:     pgHost,
			Port:     viper.GetInt("postgres.config_db.port"),
			Database: viper.GetString("postgres.config_db.database"),
			User:     viper.GetString("postgres.config_db.user"),
			Password: pgPassword,
		}
		if v := viper.GetInt32("postgres.config_db.max_connections"); v > 0 {
			pgCfg.MaxConnections = v
		}
		if v := viper.GetInt32("postgres.config_db.min_connections"); v > 0 {
			pgCfg.MinConnections = v
		}
		if s := viper.GetString("postgres.config_db.max_conn_lifetime"); s != "" {
			if d, parseErr := time.ParseDuration(s); parseErr == nil {
				pgCfg.MaxConnLifetime = d
			}
		}
		if s := viper.GetString("postgres.config_db.health_check_period"); s != "" {
			if d, parseErr := time.ParseDuration(s); parseErr == nil {
				pgCfg.HealthCheckPeriod = d
			}
		}

		pgCtx, pgCancel := context.WithTimeout(context.Background(), 30*time.Second)
		pool, poolErr := postgresStorage.NewConfigPool(pgCtx, pgCfg, log)
		pgCancel()

		if poolErr != nil {
			log.WithError(poolErr).Warn("Dynamic config: failed to create PG pool — running in YAML-only mode")
			if profile.Product {
				log.WithField("host", pgHost).Warn("Postgres is not reachable: check that the Postgres service is running and that TRADING_CONFIG_DB_PASSWORD matches; continuing with the YAML defaults")
			}
		} else {
			configPool = pool // captured for backtest subsystem (TC-7)
			migrCtx := context.Background()
			if migErr := postgresStorage.RunConfigMigrations(migrCtx, pool, "migrations/trading-config", log); migErr != nil {
				log.WithError(migErr).Fatal("Dynamic config: failed to run DB migrations")
			}

			// Apply operational database migrations — OPT-IN (default OFF) to protect the live bot.
			// WARNING: these migrations target a DEDICATED operational Postgres DB. Running them
			// against the config pool (current wiring) can fail and break startup. Enable only
			// after wiring a separate operational pool. See migrations/operational/README.md.
			if os.Getenv("TRADING_OPERATIONAL_MIGRATIONS") == "true" {
				if err := postgresStorage.RunConfigMigrations(migrCtx, pool, "migrations/operational", log); err != nil {
					log.WithError(err).Error("operational migrations failed — continuing without them (SQLite mode)")
				} else {
					log.Info("operational migrations applied")
				}
			} else {
				log.Info("operational migrations skipped (set TRADING_OPERATIONAL_MIGRATIONS=true to enable)")
			}

			configRepo := configPg.New(pool)

			if seedErr := manifest.Seed(migrCtx, configRepo); seedErr != nil {
				log.WithError(seedErr).Fatal("Dynamic config: failed to seed manifest")
			}

			if seedErr := manifest.SeedPresets(migrCtx, configRepo, log); seedErr != nil {
				log.WithError(seedErr).Fatal("Dynamic config: failed to seed presets")
			}

			store, storeErr := dynamic.NewConfigStore(configRepo, log)
			if storeErr != nil {
				log.WithError(storeErr).Fatal("Dynamic config: failed to build ConfigStore")
			}

			if loadErr := store.Load(migrCtx); loadErr != nil {
				log.WithError(loadErr).Warn("Dynamic config: initial Load failed — values may be stale")
			}

			dynamic.SetDefaultStore(store)
			cfgStore = store

			// audit P1: push global-scoped scalar overrides into package-level
			// setters that have no ConfigStore handle of their own. Without this,
			// those keys are registered but inert (always the code default).
			applyGlobalScalarConfig(cfgStore, log)

			backupCtx, _ := context.WithCancel(context.Background()) //nolint:govet // cancel called via process exit
			go cfgStore.RunBackupWorker(backupCtx, "data/config_backup", time.Hour)

			// Periodic reload so runtime DB changes (e.g. kill-switches) take effect without restart.
			reloadCtx, _ := context.WithCancel(context.Background()) //nolint:govet
			go func() {
				ticker := time.NewTicker(30 * time.Second)
				defer ticker.Stop()
				lastHash := cfgStore.Snapshot().Hash
				for {
					select {
					case <-reloadCtx.Done():
						return
					case <-ticker.C:
						if reloadErr := cfgStore.Load(reloadCtx); reloadErr != nil {
							log.WithError(reloadErr).Warn("Dynamic config: periodic reload failed")
							continue
						}
						if newHash := cfgStore.Snapshot().Hash; newHash != lastHash {
							log.WithFields(logrus.Fields{
								"old_hash": lastHash[:8],
								"new_hash": newHash[:8],
							}).Info("🔄 Dynamic config reloaded — new values active")
							lastHash = newHash
							// audit P1: re-push package-level scalar setters on every reload.
							applyGlobalScalarConfig(cfgStore, log)
						}
					}
				}
			}()

			log.Info("✅ Dynamic config subsystem initialized (PostgreSQL-backed, 30s reload)")
		}
	}

	// Obtener IP local para acceso desde red
	localIP := utils.GetLocalIP()
	serverAddress := utils.GetServerAddress(cfg.Server.Port)
	serverURL := utils.GetServerURL(cfg.Server.Port)

	log.WithFields(logrus.Fields{
		"mode":           cfg.Server.Mode,
		"protocol":       cfg.Server.Protocol,
		"port":           cfg.Server.Port,
		"local_ip":       localIP,
		"server_address": serverAddress,
		"server_url":     serverURL,
		"gct_address":    cfg.GCT.GRPCAddress,
	}).Info("Configuration loaded successfully")

	// Inicializar cliente GoCryptoTrader
	gctClient := gctclient.NewClient(&cfg.GCT, log)

	// Configurar credenciales de KuCoin para el cliente principal
	kucoinAPIKey := os.Getenv("KUCOIN_API_KEY")
	kucoinAPISecret := os.Getenv("KUCOIN_API_SECRET")
	kucoinAPIPassword := os.Getenv("KUCOIN_API_PASSPHRASE")

	if kucoinAPIKey != "" && kucoinAPISecret != "" && kucoinAPIPassword != "" {
		gctClient.SetKuCoinCredentials(kucoinAPIKey, kucoinAPISecret, kucoinAPIPassword)
		log.WithFields(logrus.Fields{
			"api_key_prefix": kucoinAPIKey[:10],
		}).Info("KuCoin credentials configured for main GCT client")
	} else {
		log.Warn("KuCoin credentials not found in environment - futures trading will not work")
	}

	// Intentar conexión inicial con reintentos
	connectGCTWithRetries := func(client *gctclient.Client, maxRetries int) error {
		for i := 0; i < maxRetries; i++ {
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			err := client.Connect(ctx)
			cancel()

			if err == nil {
				log.Info("Successfully connected to GoCryptoTrader")
				return nil
			}

			log.WithError(err).Warnf("GCT connection attempt %d/%d failed, retrying in 3s...", i+1, maxRetries)
			time.Sleep(3 * time.Second)
		}
		return fmt.Errorf("failed to connect after %d attempts", maxRetries)
	}

	if err := connectGCTWithRetries(gctClient, profile.GCTConnectAttempts); err != nil {
		log.WithError(err).Warn("Failed to connect to GoCryptoTrader - starting in offline mode")
		if profile.Product {
			log.WithField("gct_address", cfg.GCT.GRPCAddress).Warn("GoCryptoTrader is not reachable: public market data and the paper broker are unavailable until it answers; the server keeps retrying every 30 seconds")
		}
	}

	// Iniciar reconexión automática en background
	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()

		for range ticker.C {
			if !gctClient.IsConnected() {
				log.Info("GCT disconnected, attempting reconnection...")
				if err := connectGCTWithRetries(gctClient, 3); err != nil {
					log.WithError(err).Debug("GCT reconnection failed, will retry later")
				}
			}
		}
	}()

	// Inicializar health manager
	healthManager := health.NewManager(log)

	// Wrap GCT client for health checks (ping-based and ticker-based).
	exchange := viper.GetString("scalping.exchange")
	if exchange == "" {
		exchange = "kucoin"
	}

	gctWrapper := health.NewGCTClientWrapperWithTickerFn(
		gctClient.IsConnected,
		func(ctx context.Context, ex, pair string) (interface{}, error) {
			return gctClient.GetTicker(ctx, ex, pair)
		},
		func(ctx context.Context, ex, pair string) (float64, error) {
			ticker, err := gctClient.GetTicker(ctx, ex, pair)
			if err != nil {
				return 0, err
			}
			return ticker.Last, nil
		},
	)

	// Registrar health checkers
	healthManager.RegisterChecker(health.NewGCTChecker(gctWrapper, log))
	healthManager.RegisterChecker(health.NewExchangeChecker("exchange_"+exchange, exchange, gctWrapper, log))
	healthManager.RegisterChecker(health.NewMemoryChecker(2048, log)) // 2GB max

	// Ticker-level GCT health checker (verifies live prices, not just connectivity).
	tickerChecker := health.NewGCTTickerChecker(gctWrapper, exchange, log)

	// Verificar que GoCryptoTrader tiene tickers (con timeout de 60s)
	healthCtx, healthCancel := context.WithCancel(context.Background())
	defer healthCancel()

	if err := tickerChecker.CheckGCTTickers(healthCtx); err != nil {
		log.WithError(err).Warn("⚠️  GoCryptoTrader health check failed - continuing anyway")
	}

	// Iniciar verificación periódica en background
	go tickerChecker.StartPeriodicCheck(healthCtx)

	// ================================================================
	// INITIALIZE WEBSOCKET HUB
	// ================================================================
	log.Info("Initializing WebSocket Hub...")

	// Load WebSocket configuration from environment
	wsConfig := iatbWebsocket.LoadFromEnv()
	if err := wsConfig.Validate(); err != nil {
		log.WithError(err).Warn("WebSocket config validation failed, using defaults")
		wsConfig = iatbWebsocket.DefaultConfig()
	}

	// Apply WebSocket configuration
	wsConfig.Apply()

	log.WithFields(logrus.Fields{
		"heartbeat_interval": wsConfig.HeartbeatInterval,
		"rate_limit_max":     wsConfig.RateLimitMax,
		"rate_limit_window":  wsConfig.RateLimitWindow,
		"max_message_size":   wsConfig.MaxMessageSize,
	}).Info("WebSocket configuration loaded")

	// Convert logrus logger to slog for WebSocket components
	slogLogger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))

	// Create and initialize WebSocket Hub
	wsHub := iatbWebsocket.NewHub(slogLogger)

	// Start Hub in background with context
	hubCtx, hubCancel := context.WithCancel(context.Background())
	defer hubCancel()
	go wsHub.Run(hubCtx)

	// Create Trading Broadcaster for trading services
	tradingBroadcaster := iatbWebsocket.NewTradingBroadcaster(wsHub, slogLogger)
	_ = tradingBroadcaster // Available for trading services integration

	// Create Market Price Broadcaster for price updates
	marketPriceBroadcaster := iatbWebsocket.NewMarketPriceBroadcaster(wsHub, slogLogger)

	// Create GCT Price Source for real-time prices
	// This integrates with GoCryptoTrader via the gct.Client interface
	gctPriceConfig := &iatbWebsocket.GCTPriceSourceConfig{
		Exchange:        "kucoin", // Can be configured via env
		UpdateInterval:  5 * time.Second,
		ChangeThreshold: 0.1, // 0.1% minimum change to broadcast
		BatchSize:       50,
		Enabled:         true,
	}

	gctPriceSource := iatbWebsocket.NewGCTPriceSource(&gctTickerAdapter{client: gctClient}, marketPriceBroadcaster, gctPriceConfig, slogLogger)
	if err := gctPriceSource.Start(hubCtx); err != nil {
		log.WithError(err).Warn("Failed to start GCT price source")
	} else {
		log.Info("✅ GCT Price Source started (fetching prices from GoCryptoTrader)")
	}

	log.Info("✅ WebSocket Hub initialized and running")
	log.Info("✅ Trading Broadcaster ready for trading events")
	log.Info("✅ Market Price Broadcaster ready for price updates")

	// Inicializar servidor MCP
	mcpServer := server.NewMCPServer(cfg, log, gctClient)

	// Create WebSocket Handler and register with MCP Server
	wsHandler := iatbWebsocket.NewHandler(wsHub, slogLogger)
	mcpServer.SetWebSocketHub(wsHandler)
	log.Info("✅ WebSocket Handler registered with MCP Server")

	// Registrar tools de Market Data
	if err := marketdata.RegisterAll(mcpServer, gctClient); err != nil {
		log.WithError(err).Fatal("Failed to register market data tools")
	}

	// Registrar tools de Technical Analysis
	if err := technical.RegisterAll(mcpServer, gctClient); err != nil {
		log.WithError(err).Fatal("Failed to register technical analysis tools")
	}

	// Registrar tools de Order Management
	if err := orders.RegisterAll(mcpServer, gctClient); err != nil {
		log.WithError(err).Fatal("Failed to register order management tools")
	}

	// Portfolio tools are registered after storage initialization (see below)
	// so the history tool gets access to the real snapshot repository.

	// Registrar tools de Notification Management
	log.Info("Registering notification management tools...")
	if err := notificationtools.RegisterAll(mcpServer, gctClient); err != nil {
		log.WithError(err).Warn("Failed to register notification tools - continuing without them")
	} else {
		log.Info("Notification management tools registered successfully")
	}

	// Registrar tools de WebSocket Management
	log.Info("Registering WebSocket management tools...")
	if err := websockettools.RegisterAll(mcpServer, gctClient); err != nil {
		log.WithError(err).Warn("Failed to register WebSocket tools - continuing without them")
	} else {
		log.Info("WebSocket management tools registered successfully")
	}

	// TOOL-02/03 fix: risk management tools (check_risk_limits, validate_trade,
	// get_risk_state, activate/deactivate_kill_switch, get_positions, update_sltp)
	// are registered further down via risk.RegisterAll(), once the real
	// RiskSentinel/PositionTracker/SLTPExecutor instances exist (they need
	// scalpingGCTClient, constructed below). Registering them here with nil
	// dependencies made check_risk_limits/validate_trade always report
	// daily_loss.passed=true regardless of real losses, and get_risk_state
	// always error — see docs/audits/trading-mcp/02_mcp_tools_2026-09-10.md.

	// Registrar tools de Backtesting
	if err := backtest.RegisterAll(mcpServer, gctClient); err != nil {
		log.WithError(err).Fatal("Failed to register backtesting tools")
	}

	// Registrar tools de LLM (AI Providers)
	log.Info("Registering LLM tools...")
	aiConfigPath := "config/ai.yaml"
	if aiCfg, err := aiConfig.LoadFromFile(aiConfigPath); err != nil {
		log.WithError(err).Warn("Failed to load AI config, LLM tools will be unavailable")
	} else if aiCfg.AI.LLM.Enabled {
		llmTools := llmtools.RegisterAll(mcpServer, &aiCfg.AI, log)
		if llmTools != nil {
			log.Info("LLM tools registered successfully")
		}
	} else {
		log.Info("LLM is disabled in config, skipping LLM tools registration")
	}

	// AnkaLaunch read-only tools (shadow console); nil pool → tools report unavailable.
	if profile.RunAnkaLaunch {
		if err := launchtools.RegisterAll(mcpServer, configPool, launchapi.RequirementsFromStore(cfgStore)); err != nil {
			log.WithError(err).Warn("Failed to register AnkaLaunch MCP tools")
		}
	}

	// Registrar tools de Trading Pair Conversion
	log.Info("Registering trading pair conversion tools...")
	if err := pairtools.RegisterAll(mcpServer, log); err != nil {
		log.WithError(err).Warn("Failed to register pair conversion tools")
	} else {
		log.Info("Trading pair conversion tools registered successfully")
	}

	// Registrar tools de Futures Calculator
	log.Info("Registering futures calculator tools...")
	if err := futures.RegisterAll(mcpServer, gctClient); err != nil {
		log.WithError(err).Warn("Failed to register futures calculator tools")
	} else {
		log.Info("Futures calculator tools registered successfully")
	}

	stats := mcpServer.GetStats()
	log.WithField("tools_count", stats["tools_count"]).Info("Tools registered")

	// Inicializar AI Service (integrado)
	log.Info("Initializing AI Service...")
	mcpClient := aiCore.NewMCPClient(mcpServer.GetToolRegistry(), log)
	dataCollector := aiCore.NewDataCollector(mcpClient, "kucoin", log)
	indicatorCalc := aiCore.NewIndicatorCalculator(mcpClient, "kucoin", log)
	// TC-5.2: subscribe to "indicators" group changes → invalidate indicator cache
	if cfgStore != nil {
		indicatorCalc.AttachConfigStore(cfgStore)
	}
	contextBuilder := aiCore.NewContextBuilder(dataCollector, indicatorCalc, log)
	decisionEngine := aiCore.NewDecisionEngine(contextBuilder, log)

	aiAnalysisService := services.NewAIAnalysisService(contextBuilder, decisionEngine, log)
	aiBotService := services.NewAIBotService(log)
	aiBotHandler := handlers.NewAIBotHandler(aiBotService, aiAnalysisService, contextBuilder, log)

	// Initialize LLM Analyzer if GOOGLE_API_KEY is configured
	apiKey := os.Getenv("GOOGLE_API_KEY")
	if apiKey == "" {
		log.Warn("GOOGLE_API_KEY not found in environment, LLM Analyzer disabled")
		log.Warn("To enable AI analysis, set GOOGLE_API_KEY in .env file")
	} else {
		log.WithField("api_key_length", len(apiKey)).Info("Initializing LLM Analyzer with Gemini...")
		aiCfg := &aiConfig.AIConfig{
			Enabled: true,
			LLM: aiConfig.LLMConfig{
				Enabled:        true,
				Provider:       "google",
				Model:          "gemini-2.5-flash",
				APIKey:         apiKey,
				MaxCallsPerDay: 100,
				TimeoutSeconds: 60, // Increased to 60 seconds for Gemini
				Temperature:    0.7,
				MaxTokens:      2048,
			},
		}

		llmAnalyzer, err := llm.NewAnalyzer(aiCfg, nil, log)
		if err != nil {
			log.WithError(err).Warn("Failed to initialize LLM Analyzer, continuing without Gemini")
		} else {
			aiBotHandler.SetLLMAnalyzer(llmAnalyzer)
			log.WithFields(logrus.Fields{
				"provider": "google",
				"model":    "gemini-2.5-flash",
			}).Info("✅ LLM Analyzer initialized with Gemini")
		}
	}

	log.Info("✓ AI Service initialized")

	// Cargar configuración del scalping engine
	if err := loadScalpingConfig(); err != nil {
		log.WithError(err).Warn("Failed to load scalping config, using defaults")
	}

	// scalpingGCTClient shares the same gRPC connection as gctClient — no second TCP dial.
	// gctclient.Client wraps gct.GCTClient internally; GetBaseClient() returns that inner
	// client which already has the KuCoin credentials set and the connection established.
	// Reconnection is handled by gctclient.Client.healthCheckLoop() (runs every 30s).
	scalpingGCTClient := gctClient.GetBaseClient()
	log.Info("✅ Scalping GCT client sharing primary gRPC connection (single TCP dial)")

	// TOOL-02/03 fix: construct the real RiskSentinel/PositionTracker/SLTPExecutor
	// here (as soon as scalpingGCTClient exists) instead of registering the MCP
	// risk tools with nil sentinel/positionManager. These are the single
	// instances shared by the risk MCP tools (check_risk_limits, validate_trade,
	// get_risk_state, activate/deactivate_kill_switch, get_positions, update_sltp)
	// registered below via risk.RegisterAll().
	riskSentinelCapital := cfg.MultiPair.TotalCapitalUSD
	if riskSentinelCapital <= 0 {
		riskSentinelCapital = cfg.RiskLimits.MaxPositionSizeUSD
	}
	if riskSentinelCapital <= 0 {
		riskSentinelCapital = 1.0 // never divide by zero below
	}
	pctOfCapital := func(usd float64) float64 {
		if riskSentinelCapital <= 0 {
			return 100
		}
		return (usd / riskSentinelCapital) * 100
	}
	riskSentinelConfig := &internalRisk.RiskConfig{
		// Daily/weekly/monthly drawdown ceilings derived from
		// risk_limits.max_daily_loss_usd as a % of the deployable capital
		// (multi_pair.total_capital_usd). Weekly/monthly are widened multiples
		// of the daily figure since config.yaml has no dedicated keys for them.
		MaxDrawdownDaily:     pctOfCapital(cfg.RiskLimits.MaxDailyLossUSD),
		MaxDrawdownWeekly:    pctOfCapital(cfg.RiskLimits.MaxDailyLossUSD) * 2,
		MaxDrawdownMonthly:   pctOfCapital(cfg.RiskLimits.MaxDailyLossUSD) * 4,
		MaxConsecutiveLosses: viper.GetInt("scalping.default_risk_params.max_consecutive_losses"),
		MaxExposurePerSymbol: pctOfCapital(cfg.RiskLimits.MaxExposurePerPairUSD),
		MaxExposureTotal:     pctOfCapital(cfg.RiskLimits.MaxPositionSizeUSD),
	}
	if riskSentinelConfig.MaxConsecutiveLosses <= 0 {
		riskSentinelConfig.MaxConsecutiveLosses = 3
	}
	riskSentinel := internalRisk.NewRiskSentinel(riskSentinelConfig, riskSentinelCapital, log)
	log.WithFields(logrus.Fields{
		"capital_usd":        riskSentinelCapital,
		"max_drawdown_daily": riskSentinelConfig.MaxDrawdownDaily,
		"max_exposure_total": riskSentinelConfig.MaxExposureTotal,
		"max_consec_losses":  riskSentinelConfig.MaxConsecutiveLosses,
	}).Info("✅ RiskSentinel initialized (real risk_limits gate, no longer nil)")

	// Wire the dynamic config store (when available) so the sentinel enforces
	// risk.max_order_size_usd live from the manifest/DB (audit P1) instead of
	// relying only on the percentage-derived RiskConfig above.
	if cfgStore != nil {
		if cs, ok := riskSentinel.(interface {
			SetConfigStore(*dynamic.ConfigStore)
		}); ok {
			cs.SetConfigStore(cfgStore)
			log.Info("✅ RiskSentinel: dynamic config store wired (risk.max_order_size_usd live override)")
		}
	}

	// TOOL-01/HTTP-06 fix: wire this same RiskSentinel into the shared GCT
	// client instance so SubmitOrder/SubmitFuturesOrder reject orders when the
	// kill switch is active or limits are exceeded — the single chokepoint
	// reached by every order-submission path (MCP submit_order/market/limit/
	// stop tools, submit_futures_order_direct, and the HTTP
	// /orders/direct/futures and /orders/direct/close handlers).
	if rg, ok := scalpingGCTClient.(interface {
		SetRiskSentinel(internalRisk.RiskSentinel)
	}); ok {
		rg.SetRiskSentinel(riskSentinel)
		log.Info("✅ RiskSentinel wired into GCT client order-submission chokepoint (SubmitOrder/SubmitFuturesOrder)")
	} else {
		log.Warn("⚠️  GCT client does not support SetRiskSentinel — order submission is NOT risk-gated")
	}

	riskTrackedPositions := tradingcore.NewPositionManager(scalpingGCTClient, riskSentinel, log)

	sltpExecutor := tradingcore.NewSLTPExecutor(&tradingcore.SLTPConfig{}, riskTrackedPositions, scalpingGCTClient, log)
	sltpCtx, sltpCancel := context.WithCancel(context.Background())
	defer sltpCancel()
	if err := sltpExecutor.Start(sltpCtx); err != nil {
		log.WithError(err).Warn("Failed to start SL/TP executor monitoring loop")
	} else {
		log.Info("✅ SL/TP executor started (update_sltp tool now backed by a running monitor)")
	}

	// Registrar tools de Risk Management with the real dependencies above —
	// replaces the previous nil-sentinel/nil-positionManager "basic" tool list.
	log.Info("Registering risk management tools (real RiskSentinel/PositionTracker)...")
	if err := risk.RegisterAll(mcpServer, gctClient, cfg, riskSentinel, riskTrackedPositions, sltpExecutor); err != nil {
		log.WithError(err).Fatal("Failed to register risk management tools")
	}
	log.Info("✅ Risk management tools registered successfully")

	// Create AI Trading Bot with safe defaults (after scalpingGCTClient is created)
	posConfig := &aiCore.PositionConfig{
		AutoClose:     true,
		CheckInterval: 5,
	}

	tradingBotConfig := services.DefaultTradingBotConfig()

	// Try loading strategy YAML as base
	strategyPath := "config/strategies/doge-scalping.yaml"
	if loadedStrategy, err := aiCore.LoadStrategy(strategyPath); err == nil {
		tradingBotConfig.Timeframes = loadedStrategy.GetTimeframeIntervals()
		tradingBotConfig.ConfidenceThreshold = loadedStrategy.Confidence
		tradingBotConfig.TradeSize = loadedStrategy.Risk.TradeSize
		tradingBotConfig.Leverage = loadedStrategy.Risk.Leverage
		tradingBotConfig.AnalysisInterval = time.Duration(loadedStrategy.Execution.CheckInterval) * time.Second
		tradingBotConfig.DefaultStopLossPct = loadedStrategy.Risk.StopLossPct
		tradingBotConfig.DefaultTakeProfitPct = loadedStrategy.Risk.TakeProfitPct

		// Map position management config from strategy
		if loadedStrategy.PositionManagement.Enabled {
			posConfig.AutoClose = loadedStrategy.PositionManagement.AutoClose
			posConfig.TrailingStop = loadedStrategy.PositionManagement.TrailingStop
			posConfig.TrailingStopPct = loadedStrategy.PositionManagement.TrailingStopPct
			posConfig.BreakevenAfterPct = loadedStrategy.PositionManagement.BreakevenAfterPct
			posConfig.CheckInterval = loadedStrategy.PositionManagement.CheckInterval
		}
	}

	positionManager := aiCore.NewPositionManager(posConfig, log)

	// Override with master config (mcp-trading/config.yaml) if enabled
	if cfg.Bot.Enabled {
		tradingBotConfig.DryRun = cfg.Bot.DryRun
		tradingBotConfig.AutoExecute = cfg.Bot.AutoExecute
		tradingBotConfig.AutonomousMode = cfg.Bot.AutonomousMode
		// market_type drives hasFuturesPositions in checkPortfolioRisk — critical
		// for avoiding false exposure alarms when futures use a separate wallet.
		if cfg.Bot.MarketType != "" {
			tradingBotConfig.MarketType = cfg.Bot.MarketType
		}
		// Empty pair is an explicit "wait for master agent" signal — always
		// honor it. The pair_selection logic populates it on the first cycle
		// that finds a qualifying opportunity.
		tradingBotConfig.Pair = cfg.Bot.Pair
		if cfg.Bot.Exchange != "" {
			tradingBotConfig.Exchange = cfg.Bot.Exchange
		}
		if cfg.Bot.TradeSizeUSD > 0 {
			tradingBotConfig.TradeSize = cfg.Bot.TradeSizeUSD
		}
		if cfg.Bot.Leverage > 0 {
			tradingBotConfig.Leverage = cfg.Bot.Leverage
		}
		if cfg.Bot.ConfidenceThreshold > 0 {
			tradingBotConfig.ConfidenceThreshold = cfg.Bot.ConfidenceThreshold
		}
		if cfg.Bot.MaxDailyLossUSD > 0 {
			tradingBotConfig.MaxDailyLoss = cfg.Bot.MaxDailyLossUSD
		}
		if cfg.Bot.MaxOpenPositions > 0 {
			tradingBotConfig.MaxOpenPositions = cfg.Bot.MaxOpenPositions
		}
		if cfg.Bot.DefaultStopLossPct > 0 {
			tradingBotConfig.DefaultStopLossPct = cfg.Bot.DefaultStopLossPct
		}
		if cfg.Bot.DefaultTakeProfitPct > 0 {
			tradingBotConfig.DefaultTakeProfitPct = cfg.Bot.DefaultTakeProfitPct
		}
		if cfg.Bot.AnalysisIntervalSeconds > 0 {
			tradingBotConfig.AnalysisInterval = time.Duration(cfg.Bot.AnalysisIntervalSeconds) * time.Second
		}
		if cfg.Bot.MonitorIntervalSeconds > 0 {
			tradingBotConfig.MonitorInterval = time.Duration(cfg.Bot.MonitorIntervalSeconds) * time.Second
		}
		if cfg.Bot.Breakeven.Enabled {
			tradingBotConfig.BreakevenConfig = &aiConfig.BreakevenConfig{
				Enabled:    cfg.Bot.Breakeven.Enabled,
				TriggerPct: cfg.Bot.Breakeven.TriggerPct,
				BufferPct:  cfg.Bot.Breakeven.BufferPct,
			}
		}
		if cfg.Bot.Trailing.Enabled {
			tradingBotConfig.TrailingConfig = &aiConfig.TrailingConfig{
				Enabled:     cfg.Bot.Trailing.Enabled,
				DistancePct: cfg.Bot.Trailing.DistancePct,
			}
		}
		tradingBotConfig.DynamicSizing = buildDynamicSizing(cfg.Bot.DynamicSizing)
		tradingBotConfig.PartialTP = buildPartialTP(cfg.Bot.PartialTP)
		tradingBotConfig.PairSelection = buildPairSelection(cfg.Bot.PairSelection)

		log.WithFields(logrus.Fields{
			"dry_run":              tradingBotConfig.DryRun,
			"auto_execute":         tradingBotConfig.AutoExecute,
			"pair":                 tradingBotConfig.Pair,
			"leverage":             tradingBotConfig.Leverage,
			"sl_pct":               tradingBotConfig.DefaultStopLossPct,
			"tp_pct":               tradingBotConfig.DefaultTakeProfitPct,
			"dynamic_sizing":       tradingBotConfig.DynamicSizing.Enabled,
			"dynamic_sizing_tiers": len(tradingBotConfig.DynamicSizing.Tiers),
			"partial_tp":           tradingBotConfig.PartialTP.Enabled,
			"partial_tp_levels":    len(tradingBotConfig.PartialTP.Levels),
		}).Info("✅ Applied bot configuration from master config.yaml")
	} else {
		// Start in dry run mode for safety if not explicitly configured
		tradingBotConfig.DryRun = true
		tradingBotConfig.AutoExecute = false
	}

	// Outer-scope declaration so the REST router can pick this up at the
	// RouterConfig assembly point further down. Wired inside the aiTradingBot
	// block when the bot is created.
	var volClassifier *volatility.Classifier

	// For now, create AI Trading Bot without risk calculator and SLTP executor
	// These will be initialized by the container system when needed
	aiTradingBot := services.NewAITradingBot(
		contextBuilder,
		decisionEngine,
		positionManager,
		nil,               // riskCalculator - will be set by container
		nil,               // sltpExecutor - will be set by container
		scalpingGCTClient, // Use the gct.GCTClient interface
		nil,               // llmAnalyzer - will be set below with real Gemini
		nil,               // notificationManager - optional, can be set later
		nil,               // learningSystem - optional, can be set later
		nil,               // autoConfigurator - optional, can be set later
		tradingBotConfig,
		log,
	)

	// MU-6: wire dynamic ConfigStore into the single-bot AITradingBot so it can
	// read per-user scoped snapshots when the store is available.
	if cfgStore != nil {
		aiTradingBot.SetConfigStore(cfgStore)
		log.Info("✅ MU-6: dynamic ConfigStore wired into AITradingBot")
	}

	// BOT-02 fix: wire the same RiskSentinel instance registered with
	// risk.RegisterAll() into the bot, so activate_kill_switch actually
	// halts the autonomous analysis/execution loop, not only MCP-driven
	// order submission at the GCT client chokepoint.
	aiTradingBot.SetRiskSentinel(riskSentinel)
	// capital-relative-sizing: risk.max_order_size_pct is evaluated against the
	// same account capital the bot sizes with.
	if cp, ok := riskSentinel.(interface {
		SetCapitalProvider(internalRisk.CapitalProvider)
	}); ok {
		cp.SetCapitalProvider(aiTradingBot.AccountCapitalUSD)
	}
	log.Info("✅ BOT-02: RiskSentinel wired into AITradingBot (kill switch now halts autonomous cycles)")

	// Watch for config file changes (Hot-Reload)
	go func() {
		lastModTime := time.Time{}
		ticker := time.NewTicker(10 * time.Second)
		defer ticker.Stop()

		for range ticker.C {
			stat, err := os.Stat(*configPath)
			if err != nil {
				continue
			}

			if lastModTime.IsZero() {
				lastModTime = stat.ModTime()
				continue
			}

			if stat.ModTime().After(lastModTime) {
				lastModTime = stat.ModTime()
				log.Info("🔄 Config file modified, reloading...")

				newCfg, err := config.Load(*configPath)
				if err != nil {
					log.WithError(err).Warn("Failed to reload config file")
					continue
				}

				if newCfg.Bot.Enabled {
					newBotConfig := services.DefaultTradingBotConfig()
					newBotConfig.DryRun = newCfg.Bot.DryRun
					newBotConfig.AutoExecute = newCfg.Bot.AutoExecute
					newBotConfig.AutonomousMode = newCfg.Bot.AutonomousMode
					newBotConfig.TradeSize = newCfg.Bot.TradeSizeUSD
					newBotConfig.Leverage = newCfg.Bot.Leverage
					newBotConfig.ConfidenceThreshold = newCfg.Bot.ConfidenceThreshold
					newBotConfig.MaxDailyLoss = newCfg.Bot.MaxDailyLossUSD
					newBotConfig.MaxOpenPositions = newCfg.Bot.MaxOpenPositions
					newBotConfig.DefaultStopLossPct = newCfg.Bot.DefaultStopLossPct
					newBotConfig.DefaultTakeProfitPct = newCfg.Bot.DefaultTakeProfitPct
					if newCfg.Bot.AnalysisIntervalSeconds > 0 {
						newBotConfig.AnalysisInterval = time.Duration(newCfg.Bot.AnalysisIntervalSeconds) * time.Second
					}
					if newCfg.Bot.MonitorIntervalSeconds > 0 {
						newBotConfig.MonitorInterval = time.Duration(newCfg.Bot.MonitorIntervalSeconds) * time.Second
					}
					if newCfg.Bot.Breakeven.Enabled {
						newBotConfig.BreakevenConfig = &aiConfig.BreakevenConfig{
							Enabled:    newCfg.Bot.Breakeven.Enabled,
							TriggerPct: newCfg.Bot.Breakeven.TriggerPct,
							BufferPct:  newCfg.Bot.Breakeven.BufferPct,
						}
					}
					if newCfg.Bot.Trailing.Enabled {
						newBotConfig.TrailingConfig = &aiConfig.TrailingConfig{
							Enabled:     newCfg.Bot.Trailing.Enabled,
							DistancePct: newCfg.Bot.Trailing.DistancePct,
						}
					}
					newBotConfig.DynamicSizing = buildDynamicSizing(newCfg.Bot.DynamicSizing)
					newBotConfig.PartialTP = buildPartialTP(newCfg.Bot.PartialTP)
					newBotConfig.PairSelection = buildPairSelection(newCfg.Bot.PairSelection)

					aiTradingBot.UpdateConfigFromStruct(newBotConfig)
				}
			}
		}
	}()

	// Initialize AI components for the trading bot
	log.Info("🤖 Initializing AI components...")

	// Initialize LLM Analyzer with REAL Gemini if API key is configured
	var botLLMAnalyzer *llm.Analyzer
	if realAPIKey := os.Getenv("GOOGLE_API_KEY"); realAPIKey != "" && realAPIKey != "your_google_api_key_here" {
		realAICfg := &aiConfig.AIConfig{
			Enabled: true,
			LLM: aiConfig.LLMConfig{
				Enabled:        true,
				Provider:       "google",
				Model:          "gemini-2.5-flash",
				APIKey:         realAPIKey,
				MaxCallsPerDay: 1000,
				TimeoutSeconds: 60,
				Temperature:    0.3, // Low temperature for consistent trading decisions
				MaxTokens:      2048,
			},
		}
		var llmErr error
		botLLMAnalyzer, llmErr = llm.NewAnalyzer(realAICfg, nil, log)
		if llmErr != nil {
			log.WithError(llmErr).Warn("Failed to initialize real Gemini LLM for trading bot")
		} else {
			aiTradingBot.SetLLMAnalyzer(botLLMAnalyzer)
			log.WithFields(logrus.Fields{
				"provider": "google",
				"model":    "gemini-2.5-flash",
			}).Info("✅ Real Gemini LLM Analyzer connected to AI Trading Bot")
		}
	} else {
		log.Warn("⚠️ GOOGLE_API_KEY not set - AI Trading Bot will use technical analysis only (no LLM)")
	}

	// Initialize Learning System (mock for now - no database required)
	learningSystem := aiCore.NewSLTPLearningSystem(nil, nil, scalpingGCTClient, nil, log)
	aiTradingBot.SetLearningSystem(learningSystem)
	log.Info("✅ Learning System initialized")

	// Pattern Library — identifies recurring market patterns with success rates.
	patternLib := patterns.NewLibrary(log)
	aiTradingBot.SetPatternLibrary(patternLib)
	log.Info("✅ Bot: pattern library initialized (will persist after storage init)")

	// Feedback Loop — adjusts SL/TP/confidence after each closed trade.
	feedbackLoop := learning.NewFeedbackLoop(log, learningSystem)
	aiTradingBot.SetFeedbackLoop(feedbackLoop)
	log.Info("✅ Bot: post-trade feedback loop initialized")

	// Warm-start FeedbackLoop from SQLite trade_history (populated by scalping bots).
	// Runs after storageManager is initialized below; stored as a deferred call.
	warmStartFeedback := func(repo *storage.TradeHistoryRepository) {
		adapter := &scalpingHistoryAdapter{repo: repo}
		if n, err := feedbackLoop.WarmStart(adapter, 100); err != nil {
			log.WithError(err).Warn("⚠️  FeedbackLoop warm-start failed")
		} else if n > 0 {
			log.WithField("trades_loaded", n).Info("✅ FeedbackLoop warm-start: loaded historical scalping outcomes from DB")
		}
	}

	// Initialize Notification Manager (using tradingBroadcaster to bridge signals to WebSocket clients)
	aiTradingBot.SetNotificationManager(tradingBroadcaster)
	log.Info("✅ Notification Manager connected to WebSocket Hub for live signals")

	// Set trading bot in handler
	aiBotHandler.SetTradingBot(aiTradingBot)
	aiBotHandler.SetLLMAnalyzer(botLLMAnalyzer)

	// === Plug analytical modules into the live bot (feature-flagged) ===

	// Multi-LLM consensus engine — votes across all configured providers in
	// parallel and only returns a decision when at least 2 of 3 agree.
	if botLLMAnalyzer != nil {
		if pm := botLLMAnalyzer.GetProviderManager(); pm != nil {
			// Require only 1 successful provider so a single working LLM unblocks the bot.
			// With 2+ providers, a second agreeing vote boosts confidence but is not mandatory.
			consensus := llm.NewConsensusEngine(pm, 1, log)
			aiTradingBot.SetConsensusEngine(consensus)
			log.Info("✅ Bot: multi-LLM consensus enabled (min 1 provider required)")
		} else {
			log.Debug("Bot: provider manager nil — consensus disabled (single-provider fallback)")
		}
	}

	// Regime detector — pure function, always safe to enable
	botRegimeDetector := regime.NewDetector(log)
	aiTradingBot.SetRegimeDetector(botRegimeDetector)
	log.Info("✅ Bot: regime detector enabled (BULL/BEAR/SIDEWAYS/VOLATILE classification)")

	// BTC correlation monitor — guards altcoin trades when BTC drops sharply.
	// BB-7: Start() is wired here; it was created but never started before this fix.
	btcMonitor := aiCore.NewBTCCorrelationMonitor(scalpingGCTClient, nil, log)
	aiTradingBot.SetBTCMonitorService(btcMonitor)
	if profile.RunAIBot {
		go func() {
			if err := btcMonitor.Start(context.Background()); err != nil {
				log.WithError(err).Warn("BTC correlation monitor exited")
			}
		}()
		log.Info("✅ Bot: BTC correlation monitor started (auto SL widening on BTC drops)")
	}

	// Manipulation/trap detector — pure function, always safe to enable
	aiTradingBot.SetTrapDetector(manipulation.NewDetector(log))
	log.Info("✅ Bot: manipulation/trap detector enabled (bull/bear traps, stop hunts)")

	// Kelly position sizing — opt-in via env var (default OFF for safety).
	// Note: trade stats reader is wired later, after storage is initialised.
	if os.Getenv("ENABLE_KELLY_SIZING") == "true" {
		aiTradingBot.EnableKellySizing(true)
		log.Info("✅ Bot: Kelly position sizing enabled (stats reader wired later)")
	}

	// Registrar AI handler en MCP Server
	if profile.RunAIBot {
		mcpServer.RegisterAIBotHandler(aiBotHandler)
	}

	log.Info("✓ AI Trading Bot initialized and registered with all components")

	// ================================================================
	// INITIALIZE AI SERVICES AND HANDLERS
	// ================================================================
	log.Info("Initializing AI services and handlers...")

	// scalpingTradeHistoryRepo and autoTunerRef are set during init blocks below
	// and shared across multiple wiring sections that appear later in startup.
	var scalpingTradeHistoryRepo *storage.TradeHistoryRepository
	var autoTunerRef *learning.AutoTuner

	// Initialize Storage Manager for handlers that need database access
	storageConfig := storage.DefaultConfig()
	storageManager, err := storage.NewManager(storageConfig, log)
	if err != nil {
		log.WithError(err).Warn("Failed to initialize storage manager - some handlers will be unavailable")
	} else {
		if err := storageManager.Initialize(); err != nil {
			log.WithError(err).Warn("Failed to run storage migrations")
		}
		defer storageManager.Close()
		log.Info("✅ Storage Manager initialized")

		// Wire trade history reader into the bot so Kelly sizing can derive
		// (winRate, avgWin, avgLoss) from real outcomes. Without this hook
		// Kelly stays in conservative 2%-of-capital fallback mode.
		if storageManager.TradeHistory != nil && aiTradingBot != nil {
			aiTradingBot.SetTradeStatsReader(&tradeStatsAdapter{repo: storageManager.TradeHistory})
			aiTradingBot.SetTradeHistoryWriter(&tradeHistoryWriterAdapter{repo: storageManager.TradeHistory})
			log.Info("✅ Bot: trade history reader+writer wired (Kelly win-rate + closed-trade persistence)")
		}

		// Wire trade history reader into the MCP server so the REST endpoint
		// GET /api/v1/trade-history can serve real trades to the Flutter client.
		// Without this the handler returns 503 "repository not initialized".
		if storageManager.TradeHistory != nil {
			mcpServer.SetTradeRepo(&tradeHistoryReaderAdapter{repo: storageManager.TradeHistory})
			log.Info("✅ MCP server: trade history reader wired (GET /api/v1/trade-history)")
		}

		// Wire the SQLite handle for the public xsec status mirror so the
		// dashboard can read GET /api/v1/xsec/status without a Kong JWT.
		if db := storageManager.DB(); db != nil && profile.RunXSEC {
			mcpServer.SetXSecDB(db)
			log.Info("✅ MCP server: xsec status mirror wired (GET /api/v1/xsec/status)")
		}

		// Wire the durable pair blacklist so toxic-pair auto-pause survives restarts.
		if storageManager.Blacklist != nil && aiTradingBot != nil {
			aiTradingBot.SetBlacklistStore(storageManager.Blacklist)
			log.Info("✅ Bot: persistent pair blacklist wired (auto-pause survives restarts)")
		}

		// Wire the xsec-live ownership registry so the scalper never adopts or
		// enters a pair the xsec basket trader currently holds (shared KuCoin
		// futures account, one-way mode — see internal/xsec/livebook.go).
		if db := storageManager.DB(); db != nil && aiTradingBot != nil && profile.RunXSEC {
			aiTradingBot.SetXSECBookReader(&xsecBookReaderAdapter{db: db})
			log.Info("✅ Bot: xsec-live ownership registry wired (scalper excludes xsec-owned pairs)")
		}

		// Wire trade history repo into feedback loop warm-start and scalping BotManager.
		if storageManager.TradeHistory != nil {
			warmStartFeedback(storageManager.TradeHistory)
			// BotManager reference is set later; store repo for deferred wiring below.
			_ = storageManager.TradeHistory // wired via scalpingTradeHistoryRepo below
		}
		scalpingTradeHistoryRepo = storageManager.TradeHistory

		// Enable pattern library persistence now that storage is ready.
		if storageManager.PatternLibrary != nil {
			if err := patternLib.EnablePersistence(storageManager.PatternLibrary); err != nil {
				log.WithError(err).Warn("Bot: pattern library persistence failed — running in-memory only")
			} else {
				log.Info("✅ Bot: pattern library persistence enabled (SQLite backend)")
			}
		}

		// Initialize TuningStore — persists bot tuning parameters in SQLite.
		// Wire into regime detector and decision engine so they read live values
		// instead of hardcoded fallbacks. The server can still boot if this fails.
		if tuningStore, tuningErr := storage.NewTuningStore(storageManager.DB()); tuningErr != nil {
			log.WithError(tuningErr).Warn("TuningStore init failed — components will use hardcoded defaults")
		} else {
			log.Info("✅ TuningStore initialized — bot params loaded from DB")

			// Propagate to regime detector (wired to aiTradingBot above).
			if botRegimeDetector != nil {
				botRegimeDetector.SetTuning(tuningStore)
				log.Info("✅ TuningStore → regime detector")
			}

			// Propagate to decision engine (created before aiTradingBot).
			if decisionEngine != nil {
				decisionEngine.SetTuning(tuningStore)
				log.Info("✅ TuningStore → decision engine")
			}

			// Register the REST endpoint so operators can read/patch params at runtime.
			// The product edition does not mount it (M4): the tuning store feeds
			// the AI bot, which the product does not run.
			if profile.RunAIBot {
				tuningHandler := handlers.NewTuningHandler(tuningStore, log)
				mcpServer.RegisterTuningHandler(tuningHandler)
				log.Info("✅ TuningHandler registered — /api/v1/bot/tuning ready")
			} else {
				log.Info("Bot tuning endpoints not mounted (product profile)")
			}
		}

		// ── P2: ML Predictor voter ──────────────────────────────────────────
		// Wire an HTTPPredictor when ML_SERVER_URL is set (ml-server running).
		// Falls back to NoopPredictor silently — bot continues with
		// technical+LLM analysis unchanged if the model server is absent.
		// Uses services.NewHTTPMLPredictor bridge to satisfy the private
		// mlPredictor interface without leaking internal types to main.
		if aiTradingBot != nil {
			if mlURL := os.Getenv("ML_SERVER_URL"); mlURL != "" {
				aiTradingBot.SetMLPredictor(services.NewHTTPMLPredictor(mlURL, 5*time.Second))
				log.WithField("url", mlURL).Info("✅ Bot: ML predictor wired (GBM voter active)")
			} else {
				aiTradingBot.SetMLPredictor(services.NewNoopMLPredictor())
				log.Info("⚠️  Bot: ML predictor noop (set ML_SERVER_URL=http://localhost:8765 to enable)")
			}
		}

		// ── P3: Feedback calibrator + labeler ──────────────────────────────
		// The Calibrator maintains Bayesian signal-source weights in SQLite
		// and recalibrates daily. The Labeler writes post-trade labels so the
		// calibrator and future model retraining have real outcome data.
		if aiTradingBot != nil && storageManager.DB() != nil {
			cal := feedback.NewCalibrator(storageManager.DB(), log)
			// Recalibration cadence is dynamic (feedback.calibrator.loop_interval,
			// default 30m). The old hardcoded 24h was too slow for per-regime weights
			// to ever converge. Read once at startup from the global config snapshot.
			calInterval := 30 * time.Minute
			if snap := dynamic.SnapshotFromContext(context.Background()); snap != nil {
				if d := snap.Duration("feedback.calibrator.loop_interval"); d > 0 {
					calInterval = d
				}
				if n := snap.Int("feedback.calibrator.min_obs_per_regime"); n > 0 {
					cal.SetMinObs(n)
				}
				if b, ok := snap.BoolOk("feedback.calibrator.bootstrap_from_feature_store"); ok {
					cal.SetBootstrap(b)
				}
				// audit P1: Beta-Binomial prior is now tunable.
				a0, aok := snap.FloatOk("ai.feedback.calibrator.alpha0")
				b0, bok := snap.FloatOk("ai.feedback.calibrator.beta0")
				if aok || bok {
					if !aok {
						a0 = 2.0
					}
					if !bok {
						b0 = 2.0
					}
					cal.SetPrior(a0, b0)
				}
			}
			cal.StartLoop(context.Background(), calInterval)
			log.WithField("interval", calInterval).Info("✅ Bot: calibrator recalibration loop interval")
			aiTradingBot.SetMLWeightStore(cal)
			aiTradingBot.SetFeedbackLabeler(services.NewFeedbackLabelerBridge(storageManager.DB(), log))
			log.Info("✅ Bot: P3 feedback calibrator + labeler wired (Bayesian signal weights)")
		}
	}

	// Register portfolio management tools now that storage is ready (history tool needs snapshot repo)
	var snapshotRepo *storage.PortfolioSnapshotRepository
	if storageManager != nil {
		snapshotRepo = storageManager.PortfolioSnapshots
	}
	if err := portfolio.RegisterAll(mcpServer, gctClient, snapshotRepo); err != nil {
		log.WithError(err).Fatal("Failed to register portfolio management tools")
	}
	log.Info("✅ Portfolio management tools registered")

	// Register universe pair selection MCP tools (DM-2.5).
	// Placed here so storageManager is guaranteed to be initialised.
	// The composite re-reads the store on every Pairs() call, giving hot-reload
	// behaviour with no extra cache invalidation needed.
	if storageManager != nil {
		upsRepoForMCP := storage.NewUserPairSelectionRepository(storageManager.DB())
		mcpAssetType := cfg.MultiPair.PairUniverse.AssetType
		if mcpAssetType == "" {
			mcpAssetType = "futures"
		}
		if err := pairtools.RegisterUniverseTools(mcpServer, gctClient, upsRepoForMCP, mcpAssetType, log); err != nil {
			log.WithError(err).Warn("Failed to register universe pair selection MCP tools")
		} else {
			log.Info("✅ Universe pair selection MCP tools registered (list_tradable_pairs, get_pair_selection, set_pair_selection)")
		}

		// Register trading mode MCP tools (Wave 2-A).
		tmRepoForMCP := storage.NewTradingModeRepository(storageManager.DB())
		if err := modetools.RegisterModeTools(mcpServer, tmRepoForMCP, log); err != nil {
			log.WithError(err).Warn("Failed to register trading mode MCP tools")
		} else {
			log.Info("✅ Trading mode MCP tools registered (get_trading_mode, set_trading_mode)")
		}
	} else {
		log.Warn("storageManager unavailable — universe pair selection MCP tools skipped")
	}

	// Start portfolio tracker — saves hourly snapshots for real performance history
	if snapshotRepo != nil && scalpingGCTClient != nil {
		portfolioTracker := portfoliotracker.NewPortfolioTracker(scalpingGCTClient, snapshotRepo, log)
		portfolioTracker.Start(context.Background())
		log.Info("✅ Portfolio tracker started (hourly snapshots)")
	}

	// Initialize Knowledge Base Service
	knowledgeConfig := knowledge.DefaultConfig()
	knowledgeService := knowledge.NewKnowledgeBaseService(knowledgeConfig)
	if err := knowledgeService.Initialize(context.Background()); err != nil {
		log.WithError(err).Warn("Failed to initialize knowledge service - knowledge endpoints will be unavailable")
	} else {
		log.Info("✅ Knowledge Base Service initialized")
	}
	var knowledgeSvc knowledge.Service = knowledgeService

	// Initialize Prompt Engine Service
	promptConfig := prompts.DefaultConfig()
	promptService := prompts.NewPromptEngineService(promptConfig)
	if err := promptService.Initialize(context.Background()); err != nil {
		log.WithError(err).Warn("Failed to initialize prompt service - prompt endpoints will be unavailable")
	} else {
		log.Info("✅ Prompt Engine Service initialized")
	}
	var promptSvc prompts.Service = promptService

	// Initialize Validation Service
	validationConfig := validation.DefaultConfig()
	validationService := validation.NewValidationService(validationConfig)
	if err := validationService.Initialize(context.Background()); err != nil {
		log.WithError(err).Warn("Failed to initialize validation service - validation endpoints will be unavailable")
	} else {
		log.Info("✅ Validation Service initialized")
	}
	var validationSvc validation.Service = validationService

	// AI-02 fix: wire the same validation service into the real trading path
	// (previously only reachable from POST /api/v1/validation/test).
	aiTradingBot.SetValidationService(validationSvc)
	log.Info("✅ AI-02: ValidationService wired into AITradingBot's real execution path")

	// Initialize Monitoring Service
	monitoringConfig := aiMonitoring.DefaultConfig()
	aiMonitoringService := aiMonitoring.NewPerformanceMonitor(monitoringConfig)
	if err := aiMonitoringService.Initialize(context.Background()); err != nil {
		log.WithError(err).Warn("Failed to initialize AI monitoring service - monitoring endpoints will be unavailable")
	} else {
		log.Info("✅ AI Monitoring Service initialized")
	}
	var aiMonitoringSvc aiMonitoring.Service = aiMonitoringService

	// Create MCP client wrapper for BacktestingHandler
	mcpClientWrapper := handlers.NewMCPClientWrapper(mcpServer.GetToolRegistry(), log)

	// Initialize all handlers
	log.Info("Registering API handlers...")

	// 1. Analysis Handler - handles market analysis with knowledge context
	analysisHandler := handlers.NewAnalysisHandler(&knowledgeSvc, &promptSvc, &validationSvc, log)
	log.Info("✅ AnalysisHandler initialized")

	// 2. Backtesting Handler - handles backtesting requests
	backtestingHandler := handlers.NewBacktestingHandler(mcpClientWrapper, log)
	log.Info("✅ BacktestingHandler initialized")

	// 3. Knowledge Handler - handles knowledge base API
	knowledgeHandler := handlers.NewKnowledgeHandler(&knowledgeSvc, log)
	log.Info("✅ KnowledgeHandler initialized")

	// 4. Metrics Handler - handles metrics and trade history
	var metricsHandler *handlers.MetricsHandler
	if storageManager != nil {
		metricsHandler = handlers.NewMetricsHandler(storageManager, log)
		log.Info("✅ MetricsHandler initialized")
	} else {
		log.Warn("MetricsHandler not initialized - storage manager unavailable")
	}

	// 5. Monitoring Handler - handles performance monitoring
	monitoringHandler := handlers.NewMonitoringHandler(&aiMonitoringSvc, log)
	log.Info("✅ MonitoringHandler initialized")

	// 6. Strategy Handler - handles strategy management
	var strategyHandler *handlers.StrategyHandler
	if storageManager != nil {
		strategyHandler = handlers.NewStrategyHandler(storageManager, log)
		log.Info("✅ StrategyHandler initialized")
	} else {
		log.Warn("StrategyHandler not initialized - storage manager unavailable")
	}

	// 7. Validation Handler - handles trade validation
	validationHandler := handlers.NewValidationHandler(&validationSvc, log)
	log.Info("✅ ValidationHandler initialized")

	mcpServer.RegisterHandlers(
		analysisHandler,
		backtestingHandler,
		knowledgeHandler,
		metricsHandler,
		monitoringHandler,
		strategyHandler,
		validationHandler,
	)

	log.Info("✓ All API handlers registered successfully")

	// Initialize Function Calling Analyzer for Autonomous Mode (NUEVO)
	// Note: Function calling will be available once LLM analyzer is configured
	// For now, we'll initialize it when autonomous mode is enabled via API
	log.Info("✓ AI Trading Bot ready (autonomous mode available via API)")

	// Inicializar componentes del scalping engine
	notificationMgr := notifications.NewNotificationManager(scalpingGCTClient, wsHub, log)
	monitoringMgr := monitoring.NewMetricsManager(log, viper.GetInt("monitoring.prometheus.port"))

	// Crear componentes del scalping
	aiPredictor := ai.NewAIPredictor(log)
	orchestrator := strategies.NewStrategyOrchestrator(log)
	executor := executor.NewHighFrequencyExecutor(log, scalpingGCTClient)
	riskManager := scalpingRisk.NewAdaptiveRiskManager(log, scalpingGCTClient, getScalpingRiskParameters())

	// Registrar estrategias
	registerScalpingStrategies(log, scalpingGCTClient, aiPredictor, orchestrator)

	// Crear scalping engine usando su propio cliente GCT
	scalpingEngine := engine.NewScalpingEngine(
		log,
		scalpingGCTClient,
		notificationMgr,
		monitoringMgr,
	)

	// SCALP-02: wire the same AdaptiveRiskManager the dashboard controller uses
	// into the engine so processSignal sizes/gates orders through it (kill
	// switch, exposure/daily-loss limits, Kelly sizing) instead of the fixed
	// unmanaged local riskParams.
	scalpingEngine.SetRiskManager(riskManager)

	// Determine BotManager concurrency limit from config.
	// Priority: multi_pair.max_concurrent_bots > scalping.max_bots > default 5.
	maxBots := viper.GetInt("multi_pair.max_concurrent_bots")
	if maxBots <= 0 {
		maxBots = viper.GetInt("scalping.max_bots")
	}
	if maxBots <= 0 {
		maxBots = 5
	}
	log.WithField("max_bots", maxBots).Info("BotManager concurrency limit configured")

	// ================================================================
	// MU-2 / MU-8.6: PER-USER BOT MANAGER (feature-flagged)
	// Gated behind cfg.MultiUser.Enabled (multi_user.enabled in config.yaml).
	// When the flag is false (default) none of the multi-user structs are
	// constructed and RouterConfig receives nil for BotManager/SessionManager/
	// UserBotConfigRepo — preserving byte-identical single-bot behaviour.
	// When the flag is true the full MU-2 path runs as before.
	// ================================================================
	var userBotManager *services.BotManager
	// muSessionMgr declared at package scope so RouterConfig (MU-5) can reference it.
	var muSessionMgr *gct.SessionManager
	var muBotConfigRepo *storage.UserBotConfigRepository

	if cfg.MultiUser.Enabled {
		log.Info("multi_user.enabled=true — activating per-user BotManager/SessionManager")
		muCredLoader := services.NewUnavailableCredentialLoader() // replaced in MU-5 with real service
		muSessionMgr = gct.NewSessionManager(muCredLoader, 30*time.Minute, log)
		if storageManager != nil {
			// Adapter bridges storage.UserBotConfigRepository → services.UserBotConfigStore
			// without importing internal/storage from internal/services (cycle guard).
			muBotConfigRepo = storage.NewUserBotConfigRepository(storageManager.DB())
			ubcStore := &userBotConfigStoreAdapter{repo: muBotConfigRepo}
			botDeps := services.BotDependencies{
				ContextBuilder: contextBuilder,
				DecisionEngine: decisionEngine,
				GCTClient:      scalpingGCTClient,
				LLMAnalyzer:    botLLMAnalyzer,
				ConfigStore:    cfgStore,                  // MU-6: forward dynamic ConfigStore to per-user bots
				ValidationGate: storageManager.Validation, // wire certified-pair gate to every BotManager-spawned bot
			}
			userBotManager = services.NewBotManager(ubcStore, muSessionMgr, nil, botDeps, maxBots, log)
			log.Info("✅ MU-2: BotManager initialized (pairCoord + real creds wired below)")
		} else {
			log.Warn("⚠️  MU-2: BotManager skipped — storage manager unavailable")
		}
	} else {
		log.Info("multi_user.enabled=false — single-bot mode (BotManager/SessionManager not constructed)")
	}

	// (autoTunerRef and scalpingTradeHistoryRepo declared above, before storage init)

	// Registrar scalping MCP tools
	log.Info("Registering scalping MCP tools...")
	// SCALP-03 fix: reuse the already-correctly-configured risk params (same
	// source as `riskManager` above) instead of letting the adapter build its
	// own AdaptiveRiskManager with nil params (which falls back to hardcoded
	// defaults 10-50x looser than configs/mcp-trading/config.yaml).
	// The product profile registers them on a scratch registry: the adapter and
	// its bot manager stay constructed (dormant), but no scalping tool is
	// exposed on /mcp.
	scalpingToolRegistry := mcpServer.GetToolRegistry()
	if !profile.RunScalping {
		scalpingToolRegistry = server.NewToolRegistry()
	}
	scalpingAdapter, err := scalping.RegisterScalpingTools(scalpingToolRegistry, log, scalpingGCTClient, scalpingEngine, maxBots, getScalpingRiskParameters())
	if err != nil {
		log.WithError(err).Error("Failed to register scalping tools - Flutter app functionality will be limited")
		log.Warn("⚠️  Scalping endpoints will not be available for Flutter app")
	} else {
		updatedStats := mcpServer.GetStats()
		log.WithField("total_tools", updatedStats["tools_count"]).Info("✅ Scalping tools registered successfully - Flutter app ready")

		// Wire dynamic exit components so paper-trading bots feed the learning loop
		// and can consult the same multi-LLM consensus used for entry decisions.
		// Paper-profile calls reach the scalping tools through this selector;
		// it fails closed (never the real client) when the broker is not ready.
		scalpingAdapter.SetClientSelector(gctClient.ClientFor)
		bm := scalpingAdapter.GetBotManager()
		bm.SetDynamicExitFeedbackLoop(feedbackLoop)
		// Ring 0: stop every running/spawning scalping bot of this manager.
		killswitch.RegisterEngine(killswitch.FuncEngine{
			EngineName: "scalping_bot_manager",
			StopFn: func(ctx context.Context) error {
				var errs []string
				for _, b := range bm.GetAllBots() {
					if b.Status != "running" && b.Status != "spawning" {
						continue
					}
					if err := bm.StopBot(b.ID, false); err != nil {
						errs = append(errs, fmt.Sprintf("%s: %v", b.ID, err))
					}
				}
				if len(errs) > 0 {
					return fmt.Errorf("%s", strings.Join(errs, "; "))
				}
				return nil
			},
		})
		log.Info("✅ Dynamic exit: feedback loop wired (per-pair parameter learning active)")

		// Wire dynamic config store so BotManager can read regime/volatility/orderbook params.
		if cfgStore != nil {
			bm.SetConfigStore(cfgStore)
			log.Info("✅ BotManager: dynamic config store wired (regime, volatility, orderbook filters)")
		}

		// Wire a regime detector into BotManager for entry filtering.
		bmRegimeDetector := regime.NewDetector(log)
		bm.SetRegimeDetector(bmRegimeDetector)
		log.Info("✅ BotManager: regime detector wired (TRENDING/RANGING/BREAKOUT filter)")

		// Fear & Greed Index — public endpoint, no key required.
		fgClient := sentiment.NewFearGreedClient(log)
		bm.SetFearGreedClient(fgClient)
		log.Info("✅ BotManager: Fear & Greed client wired (extreme sentiment entry filter)")

		// Trade history persistence — scalping bot closed trades → SQLite trade_history.
		if scalpingTradeHistoryRepo != nil {
			bm.SetTradeHistoryRepository(scalpingTradeHistoryRepo)
			log.Info("✅ BotManager: trade history repository wired (closed trades persisted to SQLite)")
		}

		// CryptoPanic news filter — requires CRYPTOPANIC_API_KEY in env.
		if cpKey := os.Getenv("CRYPTOPANIC_API_KEY"); cpKey != "" {
			cpClient := sentiment.NewCryptoPanicClient(cpKey, log)
			bm.SetCryptoPanicClient(cpClient)
			log.Info("✅ BotManager: CryptoPanic news filter wired (bearish/bullish news blocks entry)")
		} else {
			log.Warn("⚠️  BotManager: CRYPTOPANIC_API_KEY not set — news sentiment filter inactive")
		}

		// AutoTuner: persists FeedbackLoop adjustments to the dynamic config DB so
		// learned parameters survive restarts and are visible to all components.
		if cfgStore != nil {
			autoTunerInst := learning.NewAutoTuner(log, feedbackLoop, cfgStore)
			autoTunerInst.Start(context.Background())
			log.Info("✅ AutoTuner started — feedback loop will persist to dynamic config DB")
			// Keep reference so the backtest engine can be wired later (after btEngine is created).
			_ = autoTunerInst // used below when btEngine is available
			// Store in closure variable for late binding.
			autoTunerRef = autoTunerInst
		}

		// WickLearner (Layer 3): tracks wick-exit correctness and auto-tunes
		// exit.wick_body_ratio_threshold in the dynamic config DB.
		{
			exchange := tradingBotConfig.Exchange
			wickFetch := func(ctx context.Context, pair string) (float64, error) {
				resp, err := gctClient.GetTicker(ctx, exchange, pair)
				if err != nil {
					return 0, err
				}
				return resp.Last, nil
			}
			var wickPersist learning.ThresholdPersister
			if cfgStore != nil {
				wickPersist = func(ctx context.Context, threshold float64) error {
					val := &configdomain.ParameterValue{
						Key:       "exit.wick_body_ratio_threshold",
						ScopeType: configdomain.ScopeGlobal,
						ScopeID:   "*",
						Value:     fmt.Sprintf("%.2f", threshold),
						Source:    configdomain.SourceFeedbackLoop,
					}
					return cfgStore.Set(ctx, val, "wick_learner auto-tuned")
				}
			}
			wickLearner := learning.NewWickLearner(log, wickFetch, wickPersist)
			// Seed initial threshold from config store if available.
			if cfgStore != nil {
				snap := cfgStore.SnapshotForScope([]dynamic.ScopeRef{{Type: configdomain.ScopeGlobal, ID: "*"}})
				if v := snap.Float("exit.wick_body_ratio_threshold"); v > 0 {
					wickLearner.SetThreshold(v)
				}
			}
			wickLearner.Start(context.Background())
			aiTradingBot.SetWickLearner(wickLearner)
			log.Info("✅ WickLearner started — wick/body ratio will auto-tune from exit outcomes")
		}

		// Dynamic volatility classifier: tracks each pair's tier (low/normal/volatile/extreme)
		// based on live ATR/price observations. Refreshes every 15 min so pairs migrate
		// between tiers as their market behavior changes — no hardcoded volatile-pair lists.
		volClassifier = volatility.New(storageManager.DB(), log, aiTradingBot)
		// Apply dynamic-config-overridable thresholds when available.
		if cfgStore != nil {
			snap := cfgStore.SnapshotForScope([]dynamic.ScopeRef{{Type: configdomain.ScopeGlobal, ID: "*"}})
			th := volatility.DefaultThresholds()
			if v := snap.Float("volatility.tier.low_max"); v > 0 {
				th.LowMax = v
			}
			if v := snap.Float("volatility.tier.normal_max"); v > 0 {
				th.NormalMax = v
			}
			if v := snap.Float("volatility.tier.volatile_max"); v > 0 {
				th.VolatileMax = v
			}
			volClassifier.SetThresholds(th)
			if d := snap.Duration("volatility.refresh_interval"); d > 0 {
				volClassifier.SetRefreshInterval(d)
			}
		}
		if err := volClassifier.LoadCache(context.Background()); err != nil {
			log.WithError(err).Warn("volatility classifier: failed to load cache — starting empty")
		}
		volClassifier.Start(context.Background())
		aiTradingBot.SetVolatilityClassifier(volClassifier)
		log.Info("✅ Volatility classifier started — pair tiers will auto-update every 15min")

		// Pair coordinator: prevents AITradingBot and BotManager from running
		// simultaneously on the same pair (risk of duplicate orders).
		pairCoord := coordinator.NewPairCoordinator()
		aiTradingBot.SetPairCoordinator(pairCoord)
		bm.SetPairCoordinator(pairCoord)
		log.Info("✅ Pair coordinator wired — AITradingBot and BotManager share a pair registry")

		// MU-2: also wire pairCoord into the per-user BotManager so multi-user
		// bots participate in the same global cross-component guard.
		if userBotManager != nil {
			userBotManager.SetPairCoordinator(pairCoord)
			log.Info("✅ MU-2: BotManager pair coordinator wired")
		}

		if botLLMAnalyzer != nil {
			if pm := botLLMAnalyzer.GetProviderManager(); pm != nil {
				exitConsensus := llm.NewConsensusEngine(pm, 1, log)
				bm.SetDynamicExitConsensus(exitConsensus)
				log.Info("✅ Dynamic exit: AI consensus engine wired — AIEnabled=true, checks every 2 min on profitable live positions")
			}
		}
	}

	// Agregar pares configurados
	pairs := viper.GetStringSlice("scalping.pairs")
	scalpingExchange := viper.GetString("scalping.exchange")
	if scalpingExchange == "" {
		scalpingExchange = "kucoin"
	}

	for _, pair := range pairs {
		if err := scalpingEngine.AddPair(scalpingExchange, pair); err != nil {
			log.Warnf("Failed to add pair %s: %v", pair, err)
		}
	}

	// Auto-iniciar el scalping engine si está configurado
	if profile.RunScalping && viper.GetBool("scalping.auto_start") {
		log.Info("🚀 Auto-starting scalping engine...")
		engineCtx, engineCancel := context.WithCancel(context.Background())
		defer engineCancel()

		if err := scalpingEngine.Start(engineCtx); err != nil {
			log.WithError(err).Error("Failed to start scalping engine")
		} else {
			log.Info("✅ Scalping engine started and analyzing market")
		}
	}

	// Order Flow Feeder — polls the order book every 5s and feeds the analyzer.
	// The feeder runs in the background and is available for subscription.
	orderFlowFetcher := orderflow.NewGCTOrderBookFetcher(log, scalpingGCTClient)
	orderFlowAnalyzer := orderflow.NewAnalyzer(log)
	orderFlowFeeder := orderflow.NewFeeder(log, orderFlowFetcher, orderFlowAnalyzer, 0)
	orderFlowCtx, orderFlowCancel := context.WithCancel(context.Background())
	defer orderFlowCancel()
	if !profile.RunScalping && !profile.RunAIBot {
		log.Info("Order flow feeder not started (product profile)")
	} else if err := orderFlowFeeder.Start(orderFlowCtx); err != nil {
		log.WithError(err).Warn("Failed to start order flow feeder")
	} else {
		log.Info("Order Flow Feeder started (polling KuCoin order book every 5s)")
		orderFlowFeeder.Subscribe(aiTradingBot)
		orderFlowFeeder.AddPair(tradingBotConfig.Pair, "kucoin")
		log.WithField("pair", tradingBotConfig.Pair).Info("✅ AI Trading Bot subscribed to order flow feeder")
	}

	// Auto-start AI trading bot if configured. Read from the typed config, not the
	// global viper: the global instance is shared by several loaders and its
	// ai_bot.auto_start default (true) won over an explicit false in the YAML.
	log.WithFields(logrus.Fields{
		"typed_config":        cfg.AIBot.AutoStart,
		"global_viper":        viper.GetBool("ai_bot.auto_start"),
		"global_viper_source": viper.ConfigFileUsed(),
	}).Info("ai_bot.auto_start resolution")
	if !profile.RunAIBot {
		log.Info("AI trading bot not started (product profile)")
	} else if cfg.AIBot.AutoStart {
		if err := aiTradingBot.Start(); err != nil {
			log.WithError(err).Warn("AI trading bot auto-start failed")
		} else {
			log.WithField("pair", tradingBotConfig.Pair).Info("✅ AI Trading Bot auto-started")
		}
	} else {
		log.Info("ai_bot.auto_start=false — use POST /api/v1/ai-bot/start to launch")
	}

	// Ring 0 (global kill switch) must be able to stop every in-process trading
	// loop, not only the (usually nil) multi-user BotManager. Each stop treats
	// "already stopped" as success so the sweep stays idempotent.
	killswitch.RegisterEngine(killswitch.FuncEngine{
		EngineName: "ai_trading_bot",
		StopFn: func(ctx context.Context) error {
			return ignoreNotRunning(aiTradingBot.StopWithContext(ctx))
		},
	})
	killswitch.RegisterEngine(killswitch.FuncEngine{
		EngineName: "scalping_engine",
		StopFn: func(ctx context.Context) error {
			return ignoreNotRunning(scalpingEngine.Stop())
		},
	})

	// Crear dashboard controller
	dashboardCtrl := dashboard.NewDashboardController(
		log,
		scalpingEngine,
		orchestrator,
		executor,
		riskManager,
	)

	// Iniciar servidor HTTP para el dashboard del scalping
	port := viper.GetString("scalping.api.port")
	if port == "" {
		port = "8081"
	}

	router := gin.Default()

	// CORS: allow the Next.js console (anka-arena) and local development clients.
	router.Use(cors.New(cors.Config{
		AllowOrigins:     dashboardCORSOrigins,
		AllowMethods:     []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"},
		AllowHeaders:     []string{"Origin", "Authorization", "Content-Type", "X-Tenant-ID", "X-Company-ID", "X-Gateway-Token"},
		ExposeHeaders:    []string{"Content-Length", "Content-Type"},
		AllowCredentials: true,
		MaxAge:           12 * time.Hour,
	}))

	// SCALP-01: dashboardCtrl.RegisterRoutes is called further down, after the
	// Kong JWT secret/gateway-token/enable flag are computed, so the JWT
	// middleware can be attached directly to this controller's own route
	// group — see the call site below for details.

	// ================================================================
	// MATP TRADING API INTEGRATION - Kong Gateway JWT Middleware
	// ================================================================

	// Local authority (task 6.2/6.3). It is always built so GET
	// /api/v1/auth/local/mode can report the mode; only in standalone mode does
	// it become the token authority of EVERY JWT layer (mux :10600, gin :8081,
	// gateway :9090, WebSockets), which then read its secret on each request.
	// In external mode nothing changes: tokens are validated with KONG_JWT_SECRET.
	productCtx, productCancel := context.WithCancel(context.Background())
	defer productCancel()
	var productDB *sql.DB
	if storageManager != nil {
		productDB = storageManager.DB()
	}
	authStandalone := cfg.Auth.Mode == config.AuthModeStandalone
	localAuthMode := localauth.ModeExternal
	if authStandalone {
		localAuthMode = localauth.ModeStandalone
		if productDB == nil {
			log.Fatal("auth.mode=standalone requires the SQLite storage manager, which failed to initialize")
		}
	}
	localAuthSvc, err := localauth.New(localauth.Config{Mode: localAuthMode, DataDir: cfg.Auth.DataDir, DB: productDB, Logger: log})
	if err != nil {
		log.WithError(err).Fatal("Failed to initialize local authentication")
	}
	if authStandalone {
		localAuthSvc.Start(productCtx)
		defer localAuthSvc.Close()
		tradingmw.SetLocalAuthority(localAuthSvc.Authority())
		log.WithField("data_dir", cfg.Auth.DataDir).Info("Local authentication ACTIVE (auth.mode=standalone): KONG_JWT_SECRET is not used to validate tokens")
	} else {
		log.Info("Authentication mode: external (tokens validated with KONG_JWT_SECRET)")
	}

	// Get Kong Gateway JWT secret from environment (needed for REST API v1 and Gateway)
	kongJWTSecret := os.Getenv("KONG_JWT_SECRET")
	if kongJWTSecret == "" && authStandalone {
		// Non-empty placeholder so the "secret configured" checks pass; the
		// authority's secret is what validates tokens.
		kongJWTSecret = localAuthSvc.Secret()
	} else if kongJWTSecret == "" {
		if os.Getenv("MCP_SERVER_MODE") != "sandbox" {
			log.Fatal("KONG_JWT_SECRET env var is required; set MCP_SERVER_MODE=sandbox for development")
		}
		// Sandbox: generate ephemeral secret, never use in production
		ephemeralBytes := make([]byte, 32)
		if _, err := rand.Read(ephemeralBytes); err != nil {
			log.WithError(err).Fatal("failed to generate ephemeral JWT secret")
		}
		kongJWTSecret = base64.StdEncoding.EncodeToString(ephemeralBytes)
		log.Warn("SECURITY WARNING: using ephemeral JWT secret in sandbox mode — not for production")
	} else {
		log.WithField("secret_length", len(kongJWTSecret)).Info("Using Kong Gateway JWT secret from environment")
	}

	// HTTP-05: wire the same JWT secret into the iatbws WebSocket handler so
	// authenticateClient can validate connection tokens instead of leaving
	// every connection unauthenticated.
	wsHandler.SetJWTSecret(kongJWTSecret)

	// SEC-12: Real-money multi-tenant security gate status (2026-06-04):
	//   ✅ SEC-1  X-Gateway-Token bypass closed (INTERNAL_GATEWAY_TOKEN env, fail-closed)
	//   ✅ SEC-3  Real NewSessionCredentialLoader wired into SessionManager
	//   ✅ SEC-8  Withdraw deny-by-default for unsupported venues
	//   ✅ SEC-9  AuthServiceURL default corrected :10101 → :10100
	//   ✅ SEC-11 Deterministic clientOrderId (SHA-256, minute-bucket)
	//   ⚠️ SEC-5  KMS for KONG_JWT_SECRET — infra task, pending provisioning
	//   ⚠️ SEC-6  RLS verified on live DB — requires prod DB access
	//   ⚠️ SEC-7  JWT source audit — warning logged (gateway-bypass writes)
	//   ⚠️ SEC-10 Trade-by-trade PnL reconciliation vs exchange — pending
	// Scaling real-money multi-tenant REQUIRES closing SEC-5/6/10 first.

	// Get internal gateway shared token from environment. This is the trust
	// boundary between the API gateway and the backend MCP server: requests
	// proxied by the gateway carry it as X-Gateway-Token and skip JWT signature
	// re-validation. It MUST NOT be a source literal (mirrors KONG_JWT_SECRET).
	internalGatewayToken := os.Getenv("INTERNAL_GATEWAY_TOKEN")
	if internalGatewayToken == "" && authStandalone {
		// Gateway and backend live in this process: an ephemeral token is enough.
		ephemeralTok := make([]byte, 32)
		if _, err := rand.Read(ephemeralTok); err != nil {
			log.WithError(err).Fatal("failed to generate internal gateway token")
		}
		internalGatewayToken = base64.StdEncoding.EncodeToString(ephemeralTok)
	} else if internalGatewayToken == "" {
		if os.Getenv("MCP_SERVER_MODE") != "sandbox" {
			log.Fatal("INTERNAL_GATEWAY_TOKEN env var is required; set MCP_SERVER_MODE=sandbox for development")
		}
		// Sandbox: generate an ephemeral token, never use in production.
		ephemeralTok := make([]byte, 32)
		if _, err := rand.Read(ephemeralTok); err != nil {
			log.WithError(err).Fatal("failed to generate ephemeral internal gateway token")
		}
		internalGatewayToken = base64.StdEncoding.EncodeToString(ephemeralTok)
		log.Warn("SECURITY WARNING: using ephemeral internal gateway token in sandbox mode — not for production")
	} else {
		log.WithField("token_length", len(internalGatewayToken)).Info("Using internal gateway token from environment")
	}

	// Check if JWT authentication should be enabled
	enableJWTAuth := os.Getenv("ENABLE_JWT_AUTH") != "false" // Enabled by default
	if enableJWTAuth {
		log.Info("✅ Kong Gateway JWT authentication will be enabled in API Gateway")
	} else {
		log.Warn("⚠️  Kong Gateway JWT authentication is DISABLED - running in development mode")
	}

	// CORE-01: the MCP server's own raw HTTP mux (port 10600, /mcp, /jsonrpc,
	// ai-bot/* control endpoints) previously enforced no auth of its own —
	// it relied entirely on the separate API Gateway (port 9090) being the
	// only reachable entrypoint. Reachable directly on 10600, every tool call
	// and ai-bot control action (including emergency-stop/close-all-positions)
	// executed unauthenticated. Wire the SAME JWT secret/gateway-token/enable
	// flag used by the REST v1 router and API Gateway so the mux validates
	// requests itself too.
	mcpServer.SetJWTAuth(kongJWTSecret, internalGatewayToken, enableJWTAuth)
	// restJWT guards REST routes that main.go mounts outside the v1 router
	// (nil when JWT auth is disabled).
	if !profile.RunRantiPayServices {
		mcpServer.OmitDeviceRoutes()
	}
	var restJWT gin.HandlerFunc
	if enableJWTAuth {
		restJWT = tradingmw.KongJWTMiddleware(kongJWTSecret, internalGatewayToken, log)
	}
	wsHandler.SetRequireAuth(enableJWTAuth)

	// Task 6.12: trading.live_enabled master switch for live tool calls on /mcp.
	liveEnabled := liveEnabledFunc(cfg)
	mcpServer.SetLiveEnabledFunc(liveEnabled)
	log.WithField("live_enabled", liveEnabled()).Info("trading.live_enabled (live tool calls on /mcp; the AI bot and XSEC are not affected by it)")

	// Task 6.13: never run unauthenticated with real money reachable.
	if fatal, msg := jwtAuthGuard(enableJWTAuth, anyRealMoney(cfg), liveEnabled()); fatal {
		log.Fatal(msg)
	} else if msg != "" {
		log.Warn(msg)
	}

	// Licence (D8): verified offline from LICENSE_KEY / LICENSE_FILE or the
	// document stored in the auth data directory. Only the product edition
	// enforces it; the standard build reports it and nothing else changes.
	licenseManager := license.ManagerFromEnv(cfg.Auth.DataDir, log)
	go licenseManager.Run(productCtx)
	licenseSource := licenseStatusSource{licenseManager}
	if edition.IsProduct() {
		// Community (product without pro) has no real-money route at all.
		allowsLive := func() bool { return edition.IsPro() && licenseManager.AllowsLive() }
		mcpServer.SetLicenseGate(allowsLive)
		localAuthSvc.SetLiveAllowed(allowsLive)
		// Same rule at the client layer, for the real order paths that do not
		// go through the /mcp dispatcher (REST order routes, direct tools).
		if lg, ok := scalpingGCTClient.(interface{ SetLiveOrderGate(func() error) }); ok {
			lg.SetLiveOrderGate(liveOrderGate(edition.IsPro(), licenseManager.AllowsLive, liveEnabled))
			log.Info("Live order gate wired into the GCT client (license + trading.live_enabled; reductions always allowed)")
		} else {
			log.Fatal("GCT client does not support SetLiveOrderGate: refusing to start the product without the live order gate")
		}
	}
	log.WithFields(logrus.Fields{"build": edition.Build(), "license_state": licenseManager.Status().State, "enforced": edition.IsProduct()}).Info("Licence status")
	mcpServer.SetPublicHealth(publicHealthInfo(licenseSource))

	// Task 5.6/5.7: the paper broker. Built once the risk sentinel, the SQLite
	// database and the GCT client all exist.
	var paperBroker *paper.Client
	if pc, perr := buildPaperBroker(productCtx, log, productDB, gctClient, scalpingGCTClient, riskSentinel, mcpServer); perr != nil {
		log.WithError(perr).Error("Paper broker unavailable: paper-profile calls will be rejected with PAPER_UNAVAILABLE (they never fall back to the real account)")
	} else {
		paperBroker = pc
	}

	// SCALP-01: register the scalping dashboard routes now that the Kong JWT
	// secret/gateway-token are available, applying the same middleware used by
	// the REST v1 router directly to this controller's route group. Previously
	// this was called with no middleware at all on the raw engine before this
	// point existed, leaving /api/v1/scalping/pairs/add, /start, PUT
	// /risk/limits and /risk/sentinel/kill-switch reachable unauthenticated.
	if !profile.RunScalping {
		log.Info("Scalping dashboard routes not mounted (product profile)")
	} else if enableJWTAuth {
		dashboardCtrl.RegisterRoutes(router, tradingmw.KongJWTMiddleware(kongJWTSecret, internalGatewayToken, log))
	} else {
		log.Warn("⚠️  Scalping dashboard routes (/api/v1/scalping/*) registered WITHOUT JWT auth — ENABLE_JWT_AUTH=false")
		dashboardCtrl.RegisterRoutes(router)
	}

	// ================================================================
	// MULTI-EXCHANGE ROUTER (DM-5.4)
	// ================================================================
	// Initialize ExchangeManager and ExchangeRouter for spread/price-matrix endpoints.
	exchangeManager := server.NewExchangeManager()
	exchRouter := exch.NewRouter(gctClient, gctClient, exchangeManager, "futures")
	log.Info("✅ Multi-exchange router initialized (price-matrix and spreads endpoints ready)")

	// ================================================================
	// REST API V1 - Subscription & Webhook Endpoints
	// ================================================================
	log.Info("Initializing REST API v1 (Subscription & Webhook endpoints)...")

	// Initialize REST API v1 Router
	// Note: SubscriptionService and WebhookProcessor require database setup
	// They will be initialized when database configuration is available
	restAPIConfig := &apiv1.RouterConfig{
		GCTClient:            gctClient,
		Logger:               log,
		JWTSecret:            kongJWTSecret,
		InternalGatewayToken: internalGatewayToken,
		MCPHealth:            mcpServer.HealthSummary,
		PublicRoutes: func(engine *gin.Engine) {
			localAuthSvc.RegisterRoutes(engine.Group("/api/v1/auth/local"))
		},
		ProductRoutes: func(v1 *gin.RouterGroup) {
			pcfg := productapi.Config{
				Standalone:     authStandalone,
				AuthMode:       cfg.Auth.Mode,
				Build:          edition.Build(),
				LiveEnabled:    liveEnabled,
				License:        licenseSource,
				Log:            log,
				InitialBalance: func() float64 { return paperParams().InitialBalanceUSD },
			}
			if paperBroker != nil {
				pcfg.Paper = paperBroker.Broker()
			}
			productapi.New(pcfg).RegisterRoutes(v1)
			(&licensehttp.Handler{Manager: licenseManager}).Register(v1, operatorOnly(authStandalone))
		},
		AuthServiceURL:        os.Getenv("AUTH_SERVICE_URL"), // Fase A: empty → defaults to :10100
		Version:               version,
		StartTime:             time.Now(),
		SubscriptionService:   nil, // TODO: Initialize with database when available
		WebhookProcessor:      nil, // TODO: Initialize with database when available
		WebhookSecret:         os.Getenv("ODOO_WEBHOOK_SECRET"),
		VolatilityClassifier:  volClassifier, // nil-safe; /api/v1/volatility/* returns 503 when nil
		LaunchDB:              configPool,    // AnkaLaunch console (nil → 503)
		OmitCompanionServices: !profile.companionServices(),
		ConfigStore:           cfgStore, // audit P1: portfolio overview reads live config; nil → viper fallback
		// SessionManager, BotManager, UserBotConfigRepo are nil when multi_user.enabled=false
		// (single-bot mode); RouterConfig handles nil gracefully.
	}
	// Wire pair selection repo when storage is available (DM-2.3).
	if storageManager != nil {
		restAPIConfig.UserPairSelectionRepo = storage.NewUserPairSelectionRepository(storageManager.DB())
		restAPIConfig.PairAssetType = cfg.MultiPair.PairUniverse.AssetType
		// Wire trading mode repo (Wave 2-A).
		restAPIConfig.TradingModeRepo = storage.NewTradingModeRepository(storageManager.DB())
		// G1: Wire StrategySpec lifecycle endpoints.
		restAPIConfig.StrategySpecRepo = storageManager.StrategySpecs
		// C0.5: seeded spec templates (GET /api/v1/specs/templates). Seed-if-empty.
		if sdb := storageManager.DB(); sdb != nil {
			tmplRepo := storage.NewSpecTemplateRepository(sdb, log)
			if seedErr := tmplRepo.SeedIfEmpty(context.Background()); seedErr != nil {
				log.WithError(seedErr).Warn("spec_templates seed failed (non-fatal)")
			}
			restAPIConfig.SpecTemplateRepo = tmplRepo

			// C1.3: consent store (versioned legal text + acceptance audit trail).
			// Seed the copy_trading template if empty (real Spanish legal copy in DB).
			consentRepo := storage.NewConsentRepository(sdb, log)
			if seedErr := consentRepo.SeedIfEmpty(context.Background()); seedErr != nil {
				log.WithError(seedErr).Warn("consent_template seed failed (non-fatal)")
			}
			restAPIConfig.ConsentRepo = consentRepo

			// C3.2/C3.3: adversarial review queue + tribunal appeals.
			restAPIConfig.GovernanceRepo = storage.NewGovernanceRepository(sdb, log)
		}
		restAPIConfig.GlobalRiskLimits = strategy.GlobalLimits{
			MaxOrderSizeUSD: cfg.RiskLimits.MaxOrderSizeUSD,
			MaxLeverage:     20, // server-side cap; config does not expose per-spec leverage yet
			MaxDailyLossUSD: cfg.RiskLimits.MaxDailyLossUSD,
		}
		// CP-2: wire the performance fee ledger (reads performance_fee_ledger).
		// Without this the /api/v1/billing/fees/* endpoints respond 503.
		if db := storageManager.DB(); db != nil {
			restAPIConfig.FeeLedger = billing.NewFeeLedger(db)
		}
		// Spec submission gate: minimum reward:risk from the config table (hot-reload).
		// Default 0 → no constraint (TP may be below SL, e.g. mean-reversion). Operators
		// raise spec.min_reward_risk_ratio in the config DB to enforce a floor.
		if cfgStore != nil {
			restAPIConfig.SpecMinRewardRiskRatio = func() float64 {
				return cfgStore.SnapshotForScope(dynamic.BotScopeChain("", "", "")).Float("spec.min_reward_risk_ratio")
			}
			// Real-money promote gates (level + self-approval) from the config table.
			restAPIConfig.SpecPromoteMinLevel = func() int {
				return cfgStore.SnapshotForScope(dynamic.BotScopeChain("", "", "")).Int("spec.promote_min_level")
			}
			restAPIConfig.SpecAllowSelfPromotion = func() bool {
				return cfgStore.SnapshotForScope(dynamic.BotScopeChain("", "", "")).Bool("spec.allow_self_promotion")
			}
			// C0.4: fee ledger economy (rate + author/treasury split) from config.
			restAPIConfig.FeeRate = func() float64 {
				return cfgStore.SnapshotForScope(dynamic.BotScopeChain("", "", "")).Float("billing.fee_rate")
			}
			restAPIConfig.FeeAuthorSplit = func() float64 {
				return cfgStore.SnapshotForScope(dynamic.BotScopeChain("", "", "")).Float("billing.author_split")
			}
			restAPIConfig.FeeTreasurySplit = func() float64 {
				return cfgStore.SnapshotForScope(dynamic.BotScopeChain("", "", "")).Float("billing.treasury_split")
			}
			// C1.4: RBAC screen gates (me/permissions) from config.
			restAPIConfig.RBACLevelPoliticas = func() int {
				return cfgStore.SnapshotForScope(dynamic.BotScopeChain("", "", "")).Int("rbac.level.politicas")
			}
			restAPIConfig.RBACLevelAprobaciones = func() int {
				return cfgStore.SnapshotForScope(dynamic.BotScopeChain("", "", "")).Int("rbac.level.aprobaciones")
			}
			restAPIConfig.RBACLevelTribunal = func() int {
				return cfgStore.SnapshotForScope(dynamic.BotScopeChain("", "", "")).Int("rbac.level.tribunal")
			}
			restAPIConfig.RBACLevelPool = func() int {
				return cfgStore.SnapshotForScope(dynamic.BotScopeChain("", "", "")).Int("rbac.level.pool")
			}
		}

		// Wire credential service (exchange API key management).
		// Open the same SQLite file with GORM (credentials package uses GORM).
		gormDB, gormErr := gorm.Open(gormsqlite.Open(storageConfig.DatabasePath), &gorm.Config{})
		if gormErr != nil {
			log.WithError(gormErr).Warn("cannot open gorm DB for credentials — skipping")
		} else {
			// Wire gormDB into RouterConfig so the new AnkaConsole handlers (engine,
			// academy, marketplace, killswitch, policies, explain, tournament) can
			// auto-migrate their tables and serve real data.
			restAPIConfig.DB = gormDB
			log.Info("✅ GORM DB wired into RouterConfig — AnkaConsole handlers enabled")

			// BOT-04 fix: construct the real 2FA step-up middleware and the TOTP
			// service backing /api/v1/twofa/* enrollment, instead of leaving
			// RouterConfig.TwoFAMiddleware nil (which defaulted to a silent
			// no-op — activate_kill_switch-equivalent gap for credential/bot
			// mutations).
			totpSvc := twofa.NewTOTPService("RantiPay Trading")
			restAPIConfig.TOTPService = totpSvc
			restAPIConfig.TwoFAMiddleware = apiv1handlers.NewTwoFAEnforcer(gormDB, totpSvc, log)
			log.Info("✅ BOT-04: real 2FA step-up middleware wired (opt-in per user; enroll via POST /api/v1/twofa/setup)")

			credRepo := credentials.NewPostgresRepository(gormDB)
			if migrateErr := credRepo.Migrate(); migrateErr != nil {
				log.WithError(migrateErr).Warn("credential table migration failed — credential endpoints will be unavailable")
			} else {
				encKeyB64 := os.Getenv("CREDENTIAL_ENCRYPTION_KEY")
				if encKeyB64 == "" && profile.Product {
					log.Warn("CREDENTIAL_ENCRYPTION_KEY is not set: the exchange credential endpoints are disabled; public market data and the paper broker keep working. Set a base64 32-byte key to store exchange credentials")
				} else if encKeyB64 == "" {
					log.Fatal("CREDENTIAL_ENCRYPTION_KEY env var is required; refusing to fall back to KONG_JWT_SECRET or an ephemeral key (would break decryption of stored exchange credentials on every restart)")
				}
				var encSvc *credentials.EncryptionService
				if encKeyB64 != "" || !profile.Product {
					var encErr error
					encSvc, encErr = credentials.NewEncryptionServiceFromBase64(encKeyB64)
					if encErr != nil {
						log.WithError(encErr).Fatal("CREDENTIAL_ENCRYPTION_KEY is not a valid base64-encoded key")
					}
				}
				if encSvc != nil {
					// DATA-04/05 documented decision: KMSProvider is intentionally
					// left unset here. internal/credentials/kms/ (file_keyring.go,
					// vault.go, provider.go) implements a real envelope-encryption
					// KMS provider (file-keyring and Vault backends), but wiring it
					// in requires a deployment decision (which backend, key custody,
					// migration of already-encrypted rows to wrapped_dek/kms_key_id
					// columns) that is out of scope for a defense-in-depth pass —
					// see docs/audits/trading-mcp/08_storage_credentials_migrations_2026-09-10.md
					// DATA-04/DATA-05. credentials.NewEncryptionServiceFromBase64
					// above is the ACTIVE encryption path in production today; do
					// not assume KMS envelope encryption is protecting stored
					// exchange credentials until this is revisited.
					credSvc, svcErr := credentials.NewService(&credentials.ServiceConfig{
						Repository:        credRepo,
						EncryptionService: encSvc,
						Logger:            log,
						GCTClient:         gctClient.GetBaseClient(),
						// KMSProvider: nil — see comment above (DATA-04/05).
					})
					if svcErr != nil {
						log.WithError(svcErr).Warn("credential service init failed")
					} else {
						restAPIConfig.CredentialService = credSvc
						log.Info("✅ Credential service initialized — /api/v1/credentials ready")
						// MU-5: upgrade the SessionManager stub loader to the real one.
						// The session manager was built early with NewUnavailableCredentialLoader
						// so that BotManager could be constructed before the credential DB was
						// ready. Now that credSvc is initialised, swap in the real loader so
						// that BotManager.StartBot → GetSession can actually authenticate users.
						if muSessionMgr != nil {
							muSessionMgr.SetCredentialLoader(services.NewSessionCredentialLoader(credSvc))
							log.Info("✅ MU-5: SessionManager credential loader upgraded — per-user bots can authenticate")
						}
					}
				}
			}
		}
	}
	// MU-8.6: wire multi-user subsystem into router only when feature flag is on.
	if cfg.MultiUser.Enabled {
		restAPIConfig.SessionManager = muSessionMgr
		if userBotManager != nil && muBotConfigRepo != nil {
			restAPIConfig.BotManager = userBotManager
			restAPIConfig.UserBotConfigRepo = muBotConfigRepo
		}
	}

	// Wire multi-exchange router (DM-5.4).
	restAPIConfig.ExchangeRouter = exchRouter

	// Pre-create the ranked-pair provider with a nil agent so the handler is wired
	// before NewRouter runs. The agent pointer is filled in below (after the master
	// agent is constructed), so by the time the first HTTP request arrives the
	// provider is fully operational. Agent nil-guard in TopOpportunities prevents
	// any panic if a request races ahead of agent initialization.
	rankedPairProv := &masterPairProvider{}
	restAPIConfig.RankedPairProvider = rankedPairProv

	// Mirror the ranked-pair provider onto the public MCP mux (:10600) so the
	// Flutter client can read GET /api/v1/pairs/ranked without a Kong JWT. The
	// same *masterPairProvider instance gets its agent filled in below, so both
	// the JWT-scoped Gin route and the public mux route share live data.
	mcpServer.SetRankedPairProvider(&serverRankedPairAdapter{prov: rankedPairProv})
	log.Info("✅ MCP server: ranked pairs reader wired (GET /api/v1/pairs/ranked, public)")

	// G5: wire the OnSpecLive callback when cfgStore is available.
	// When a Spec is promoted to live, apply its config_override to the running bot.
	// Only keys already registered in the manifest are accepted by cfgStore.Set.
	if cfgStore != nil && restAPIConfig.StrategySpecRepo != nil {
		restAPIConfig.OnSpecLive = func(ctx context.Context, specID string,
			floats map[string]float64, bools map[string]bool, ints map[string]int) {
			var vals []*configdomain.ParameterValue
			for k, v := range floats {
				vals = append(vals, &configdomain.ParameterValue{
					Key:       k,
					ScopeType: configdomain.ScopeGlobal,
					ScopeID:   "*",
					Value:     fmt.Sprintf("%.6g", v),
					Source:    configdomain.SourcePreset,
				})
			}
			for k, v := range bools {
				boolVal := "false"
				if v {
					boolVal = "true"
				}
				vals = append(vals, &configdomain.ParameterValue{
					Key:       k,
					ScopeType: configdomain.ScopeGlobal,
					ScopeID:   "*",
					Value:     boolVal,
					Source:    configdomain.SourcePreset,
				})
			}
			for k, v := range ints {
				vals = append(vals, &configdomain.ParameterValue{
					Key:       k,
					ScopeType: configdomain.ScopeGlobal,
					ScopeID:   "*",
					Value:     fmt.Sprintf("%d", v),
					Source:    configdomain.SourcePreset,
				})
			}
			if len(vals) == 0 {
				return
			}
			// BulkSet validates each key against the manifest; unknown keys are rejected.
			// We set individually so one unknown key does not block the rest.
			applied, skipped := 0, 0
			for _, val := range vals {
				if setErr := cfgStore.Set(ctx, val, "spec "+specID+" promoted to live"); setErr != nil {
					log.WithError(setErr).WithFields(logrus.Fields{
						"spec_id": specID,
						"key":     val.Key,
					}).Debug("G5: spec override key not in manifest — skipped")
					skipped++
				} else {
					applied++
				}
			}
			log.WithFields(logrus.Fields{
				"spec_id": specID,
				"applied": applied,
				"skipped": skipped,
			}).Info("G5: spec config_override applied to runtime config store")
		}
	}

	restAPIRouter, err := apiv1.NewRouter(restAPIConfig)
	if err != nil {
		log.WithError(err).Warn("Failed to create REST API v1 router - subscription endpoints unavailable")
	} else {
		// Register REST API v1 routes on the scalping dashboard router
		restAPIRouter.RegisterRoutes(router)
		log.Info("✅ REST API v1 routes registered (portfolio, orders, market, credentials, analysis, bot endpoints)")
		log.Warn("⚠️  Subscription/Webhook endpoints require database configuration")
	}

	// Dynamic config HTTP endpoints — protected by admin token auth + rate limiting.
	// Mounted at /admin/config (not under /api/v1) so the Kong JWT middleware applied
	// to the /api/v1 group does not intercept these requests before AdminAuthRequired runs.
	if cfgStore != nil {
		configH := handlers.NewConfigHandler(cfgStore)
		adminToken := os.Getenv("ADMIN_API_TOKEN")
		cfgGroup := router.Group("/admin/config")
		cfgGroup.Use(middleware.AdminAuthRequired(adminToken, log))
		cfgGroup.Use(middleware.RateLimitByToken(60, log))
		configH.Register(cfgGroup)

		// Self-learning engine observability (Phase E): read-only views of the
		// model registry and Bayesian source weights. Opt-in (ml.observability.enabled).
		obsEnabled := true
		if snap := dynamic.SnapshotFromContext(context.Background()); snap != nil {
			if b, ok := snap.BoolOk("ml.observability.enabled"); ok {
				obsEnabled = b
			}
		}
		if obsEnabled && storageManager.DB() != nil {
			mlGroup := router.Group("/admin/ml")
			mlGroup.Use(middleware.AdminAuthRequired(adminToken, log))
			mlGroup.Use(middleware.RateLimitByToken(60, log))
			handlers.NewMLObservabilityHandler(storageManager.DB()).Register(mlGroup)
			log.Info("✅ ML observability endpoints ready — /admin/ml/registry, /admin/ml/weights")
		}

		// TC-7: Backtest subsystem — repository, engine, runner, and endpoints.
		if configPool != nil {
			btRepo := backtestPg.New(configPool)
			btEngine := backtestCore.NewEngine(gctClient.GetBaseClient(), cfgStore, decisionEngine, log)
			if autoTunerRef != nil {
				autoTunerRef.SetBacktestEngine(btEngine)
				log.Info("✅ AutoTuner: backtest engine wired (validation enabled when autotuner.backtest_validate=true)")
			}
			btRunner := backtestCore.NewRunner(btEngine, btRepo, log)
			btCtx, _ := context.WithCancel(context.Background()) //nolint:govet // cancel via process exit
			go btRunner.Run(btCtx)

			btHandler := handlers.NewConfigBacktestHandler(btRepo)
			btHandler.Register(cfgGroup)
			log.Info("✅ Backtest endpoints registered: /admin/config/backtest/*")

			// ── Strategy validation gate ──────────────────────────────────────
			// Certify that each pair has a demonstrable out-of-sample edge before
			// it trades live. The validator backtests across rolling historical
			// windows (from the durable candle_archive) and stores a PASS/FAIL
			// verdict; the live bot consults it when bot.validation_gate.enabled.
			if storageManager != nil && storageManager.CandleArchive != nil && storageManager.Validation != nil {
				validator := backtestCore.NewValidator(btEngine, storageManager.CandleArchive, log)
				valSvc := backtestCore.NewValidationService(
					validator, storageManager.CandleArchive, storageManager.Validation,
					gct.NewKuCoinRESTClient(), "kucoin",
					backtestCore.DefaultValidationConfig(), 12*time.Hour, 21, log,
				)
				// Make the validator thresholds DB-backed and hot-reloadable. Defaults
				// in the manifest reproduce DefaultValidationConfig() exactly, so with no
				// overrides the gate behaves identically; operators can loosen PF /
				// consistency / windows for thin scalping edges without a redeploy.
				if cfgStore != nil {
					valSvc.SetConfigProvider(func() (backtestCore.ValidationConfig, int) {
						snap := cfgStore.SnapshotForScope(dynamic.BotScopeChain("", "", ""))
						cfg := backtestCore.DefaultValidationConfig() // keeps Costs model
						cfg.Windows = snap.Int("bot.validation.windows")
						cfg.MinTradesTotal = snap.Int("bot.validation.min_trades_total")
						cfg.MinProfitFactor = snap.Float("bot.validation.min_profit_factor")
						cfg.MinConsistency = snap.Float("bot.validation.min_consistency")
						cfg.MaxDrawdownPct = snap.Float("bot.validation.max_drawdown_pct")
						return cfg, snap.Int("bot.validation.backfill_days")
					})
					log.Info("✅ Validation thresholds are DB-backed (bot.validation.*) — hot-reloadable per cycle")
				}
				svHandler := handlers.NewStrategyValidationHandler(valSvc, storageManager.Validation)
				// Product: behind the JWT middleware (M2); standard unchanged.
				svHandler.RegisterRoutesGin(productGuarded(router, profile, restJWT))

				// Wire the certified-pair gate into the live bot (opt-in via config).
				if aiTradingBot != nil {
					aiTradingBot.SetValidationGate(storageManager.Validation)
				}

				valCtx, _ := context.WithCancel(context.Background()) //nolint:govet // cancel via process exit
				go valSvc.Loop(valCtx, 30*time.Minute, func() []string {
					pairs, err := storageManager.DistinctTradedPairs(time.Now().Add(-7 * 24 * time.Hour))
					if err != nil || len(pairs) == 0 {
						return []string{"WLD-USDT", "PIEVERSE-USDT", "FET-USDT"}
					}
					return pairs
				})
				log.Info("✅ Strategy validation gate registered: /api/v1/strategy-validation/*")

				// Spec certification loop: bridge the StrategySpec lifecycle to the
				// walk-forward validator. Picks up specs in `validating` and certifies
				// (→validated) or rejects them so they can be promoted. Enabled via the
				// config table (spec.validation.enabled, default true), checked each tick.
				if storageManager.StrategySpecs != nil {
					specCertCtx, _ := context.WithCancel(context.Background()) //nolint:govet // cancel via process exit
					enabledFn := func() bool {
						if cfgStore == nil {
							return true
						}
						return cfgStore.SnapshotForScope(dynamic.BotScopeChain("", "", "")).Bool("spec.validation.enabled")
					}
					go runSpecCertificationLoop(specCertCtx, storageManager.StrategySpecs, valSvc, 2*time.Minute, enabledFn, log)
					log.Info("✅ Spec certification loop started (validating → validated/rejected)")
				}
			}

			// TC-8: Optimizer subsystem — auto-tuning with human-review workflow.
			optPgRepo := optimizerPg.New(configPool)
			optSvc := optimization.NewOptimizerService(btEngine, cfgStore.Repo(), optPgRepo, log)
			optWorker := optimization.NewWorker(optSvc, optPgRepo, log)
			optCtx, _ := context.WithCancel(context.Background()) //nolint:govet // cancel via process exit
			go optWorker.RunScheduled(optCtx)

			optHandler := handlers.NewOptimizerHandler(optSvc, optPgRepo, log)
			optHandler.Register(cfgGroup)
			log.Info("✅ Optimizer endpoints registered: /admin/config/optimizer/*")
		}

		log.Info("✅ Dynamic config endpoints registered: /admin/config/* (X-Admin-Token required)")
	} else {
		log.Warn("Dynamic config endpoints disabled (no DB)")
	}

	// ================================================================
	// G4: PERFORMANCE FEE LEDGER — /api/v1/fees/*
	// GET /current, GET /history, POST /accrue (internal).
	// Gated on storageManager.FeeLedger being non-nil.
	// ================================================================
	if storageManager != nil && storageManager.FeeLedger != nil {
		feeHandler := apiv1handlers.NewFeeLedgerHandler(storageManager.FeeLedger, log)
		// Product: behind the JWT middleware (M2); standard unchanged (same
		// /api/v1/fees/* routes RegisterRoutesGin used to mount).
		feeHandler.RegisterRoutes(productGuarded(router, profile, restJWT).Group("/api/v1"))
		log.Info("✅ Fee ledger endpoints registered: /api/v1/fees/*")
	} else {
		log.Warn("Fee ledger endpoints skipped (storage manager unavailable)")
	}

	// ================================================================
	// MASTER AGENT — autonomous multi-pair orchestrator
	// Mounted always so the /api/v1/master/* and /api/v1/bots* endpoints
	// respond (with 503 when disabled). The Master Agent loop only runs
	// when master_agent.enabled=true in config.
	// ================================================================
	var masterHandler *handlers.MasterHandler
	if !profile.RunMasterAgent {
		log.Info("Master agent endpoints not mounted (product profile)")
	} else if scalpingAdapter != nil {
		log.Info("Wiring master agent endpoints...")

		// Always create the handler (endpoints respond with 503 when agent is absent).
		masterHandler = handlers.NewMasterHandler(log, nil, nil)

		// MUTUAL EXCLUSION (canonical = AI orchestrator): there are two independent
		// multi-pair lifecycle managers — the scalping spawner (multi_pair.enabled,
		// YAML) and the AI orchestrator (ai_bot.multi_pair.enabled, DB). Running both
		// at once would have TWO managers spawning bots against the SAME ~$35 account
		// with no shared capital accounting → double allocation and uncoordinated
		// concurrent positions. The AI orchestrator is the canonical system, so if it
		// is enabled we hard-refuse to wire the scalping spawner regardless of the
		// YAML flag (fail-safe: never let both run).
		aiOrchestratorEnabled := false
		if snap := dynamic.SnapshotFromContext(context.Background()); snap != nil {
			aiOrchestratorEnabled = snap.Bool("ai_bot.multi_pair.enabled")
		}

		// Only wire the BotManagerSpawner when multi-pair trading is explicitly enabled.
		// The MasterAgent still initialises below (for single-bot pair selection), but
		// bot spawning/killing must not occur unless the operator opts in.
		var spawner *masterAdapters.BotManagerSpawner
		switch {
		case cfg.MultiPair.Enabled && aiOrchestratorEnabled:
			log.Error("⛔ MUTUAL EXCLUSION: multi_pair.enabled (scalping spawner) AND ai_bot.multi_pair.enabled (AI orchestrator) are BOTH on. " +
				"Refusing to wire the scalping spawner — the AI orchestrator is canonical. " +
				"Set multi_pair.enabled=false in configs/mcp-trading/config.yaml to silence this. Running AI orchestrator only.")
		case cfg.MultiPair.Enabled:
			spawner = masterAdapters.NewBotManagerSpawner(log, scalpingAdapter.GetBotManager())
			spawner.SetPaperMode(cfg.MasterAgent.PaperTrading.Enabled)
			masterHandler.SetSpawner(spawner)
			log.Info("multi_pair.enabled=true — BotManagerSpawner wired for autonomous bot lifecycle")
		default:
			log.Info("multi_pair.enabled=false — BotManagerSpawner skipped; master agent runs in scan-only mode")
		}

		if cfg.MasterAgent.Enabled {
			log.Info("master_agent.enabled=true — initialising autonomous loop")

			gctAdapter := masterAdapters.NewGCTAdapter(log, gctClient)

			// Wire the CompositeUniverseProvider into the adapter's stage-1 discovery
			// funnel. The composite re-reads the SelectionStore on every Pairs() call,
			// so a user preference change is picked up on the next scanner tick
			// (≤ master_agent.loop_interval) without any explicit cache invalidation.
			puCfg := cfg.MultiPair.PairUniverse
			assetType := puCfg.AssetType
			if assetType == "" {
				assetType = "futures"
			}
			cacheTTL := puCfg.CacheTTL
			if cacheTTL == 0 {
				cacheTTL = 15 * time.Minute
			}
			dyn := universe.NewDynamicExchangeUniverse(gctClient, assetType, cacheTTL)
			filters := universe.DiscoveryFilters{
				MinVolume24hUSD:     puCfg.DiscoveryFilters.MinVolume24hUSD,
				MinVolatilityATRPct: puCfg.DiscoveryFilters.MinVolatilityATRPct,
				MaxVolatilityATRPct: puCfg.DiscoveryFilters.MaxVolatilityATRPct,
				MaxBTCCorrelation:   puCfg.DiscoveryFilters.MaxBTCCorrelation,
				ExcludeStablecoins:  puCfg.DiscoveryFilters.ExcludeStablecoins,
				Exclude:             puCfg.DiscoveryFilters.Exclude,
			}

			// Build the composite provider (DM-2.4).
			// When storageManager is nil, fall back to the raw dynamic provider.
			defaultMode := universe.UniverseMode(puCfg.Source)
			if defaultMode == "" {
				defaultMode = universe.ModeAuto
			}
			var universeProvider universe.PairUniverseProvider = dyn
			if storageManager != nil {
				upsRepo := storage.NewUserPairSelectionRepository(storageManager.DB())
				universeProvider = universe.NewCompositeUniverseProvider(
					selectionStoreAdapter{upsRepo},
					dyn,
					"default",
					defaultMode,
				)
				log.Info("✅ Master: CompositeUniverseProvider wired (user selections override auto universe, hot-reload per tick)")
			} else {
				log.Warn("⚠️  Master: storageManager unavailable — composite universe skipped, using AUTO only")
			}
			gctAdapter.SetUniverse(universeProvider, filters)
			log.WithFields(logrus.Fields{
				"asset_type":   assetType,
				"cache_ttl":    cacheTTL,
				"default_mode": defaultMode,
			}).Info("✅ Master: dynamic pair universe wired into scanner")

			regimeDetector := regime.NewDetector(log)
			regimeAdapter := masterAdapters.NewRegimeAdapter(regimeDetector)

			// Calendar with default macro events (FOMC, CPI, NFP for 2026).
			// Adapter is exposed for future use; the calendar lives for the
			// process lifetime so it can be plugged into bots/handlers later.
			econCalendar := calendar.NewCalendar(log)
			econCalendar.LoadDefaultCryptoEvents()
			calendarAdapter := masterAdapters.NewCalendarAdapter(log, econCalendar)
			// Wire calendar to bot so entries are blocked during FOMC/CPI/NFP windows.
			aiTradingBot.SetCalendarGate(calendarAdapter)
			log.Info("✅ Bot: macro calendar gate enabled (FOMC/CPI/NFP events will block entries)")
			log.Info("✅ Master Agent calendar loaded (default macro events: FOMC, CPI, NFP)")

			// Try to build a real sentiment analyzer from cfg.AI.Sentiment.
			// Falls back to neutral-0 adapter if no sources are configured.
			var sentimentAnalyzerForMaster *sentiment.Analyzer
			sentimentCfg := &aiConfig.SentimentConfig{
				Enabled: true,
				Sources: make(map[string]aiConfig.SourceConfig),
			}
			if key := os.Getenv("CRYPTOPANIC_API_KEY"); key != "" {
				sentimentCfg.Sources["cryptopanic"] = aiConfig.SourceConfig{
					Enabled: true,
					APIKey:  key,
				}
				sentimentAnalyzerForMaster = sentiment.NewAnalyzer(sentimentCfg, botLLMAnalyzer, log)
				log.Info("✅ Master: real sentiment analyzer connected (CryptoPanic)")
			} else {
				log.Warn("⚠️  Master: CRYPTOPANIC_API_KEY not set — sentiment returns neutral 0")
			}
			var sentimentSource master.SentimentSource
			sentimentSource = masterAdapters.NewSentimentAdapter(log, sentimentAnalyzerForMaster, 5*time.Minute)

			// Build scoring weights: dynamic config takes precedence over YAML when the
			// ConfigStore is available; YAML values serve as the fallback.
			weights := &master.ScoringWeights{
				Volatility:            cfg.MasterAgent.ScoringWeights.Volatility,
				Volume:                cfg.MasterAgent.ScoringWeights.Volume,
				TrendStrength:         cfg.MasterAgent.ScoringWeights.TrendStrength,
				Sentiment:             cfg.MasterAgent.ScoringWeights.Sentiment,
				RegimeMatch:           cfg.MasterAgent.ScoringWeights.RegimeMatch,
				BTCCorrelationPenalty: cfg.MasterAgent.ScoringWeights.BTCCorrelationPenalty,
				RecentLossPenalty:     cfg.MasterAgent.ScoringWeights.RecentLossPenalty,
				ProfitFactor:          cfg.MasterAgent.ScoringWeights.ProfitFactor,
			}
			// Override weights from dynamic config only when the value is non-zero.
			// A zero from the snapshot means the key is unset (DB unavailable or
			// manifest not seeded) — in that case we keep the static YAML value.
			if snap := dynamic.SnapshotFromContext(context.Background()); snap != nil {
				if v := snap.Float("master.scoring.volatility_weight"); v > 0 {
					weights.Volatility = v
				}
				if v := snap.Float("master.scoring.volume_weight"); v > 0 {
					weights.Volume = v
				}
				if v := snap.Float("master.scoring.trend_strength_weight"); v > 0 {
					weights.TrendStrength = v
				}
				if v := snap.Float("master.scoring.sentiment_weight"); v > 0 {
					weights.Sentiment = v
				}
				if v := snap.Float("master.scoring.regime_match_weight"); v > 0 {
					weights.RegimeMatch = v
				}
				if v := snap.Float("master.scoring.btc_correlation_penalty"); v > 0 {
					weights.BTCCorrelationPenalty = v
				}
				if v := snap.Float("master.scoring.recent_loss_penalty"); v > 0 {
					weights.RecentLossPenalty = v
				}
				if v := snap.Float("master.scoring.profit_factor_weight"); v > 0 {
					weights.ProfitFactor = v
				}
			}

			// Connect BTC correlation monitor to the Master Agent's MarketScanner.
			// Pairs that move in lockstep with BTC get penalized; decorrelated
			// pairs (better hedges) get a relative boost. We only wire the
			// adapter when btcMonitor is non-nil; otherwise the scanner falls
			// back to a 0 BTC-penalty term.
			var btcMasterMonitor master.BTCMonitor
			if btcMonitor != nil {
				btcMasterMonitor = masterAdapters.NewBTCMonitorAdapter(btcMonitor, log)
				log.Info("✅ Master: BTC correlation monitor wired into market scanner")
			} else {
				log.Warn("⚠️  Master: BTC monitor unavailable — scanner BTC penalty disabled")
			}
			scanner := master.NewMarketScanner(log, gctAdapter, regimeAdapter, sentimentSource, btcMasterMonitor, weights)
			configGen := master.NewConfigGenerator(log)

			// Avoid wrapping a typed-nil *BotManagerSpawner in a non-nil BotSpawner
			// interface — that would panic on the first method call. Explicitly
			// convert to the interface only when the concrete pointer is non-nil.
			var lifecycleSpawner master.BotSpawner
			if spawner != nil {
				lifecycleSpawner = spawner
			}
			lifecycle := master.NewBotLifecycle(
				log,
				lifecycleSpawner,
				cfg.MultiPair.BotLifecycle.CooldownAfterLoss,
				cfg.MultiPair.BotLifecycle.SpawnThreshold,
				cfg.MultiPair.BotLifecycle.KillThreshold,
			)

			var tradeReader master.TradeHistoryReader
			if storageManager != nil {
				tradeReader = masterAdapters.NewTradeHistoryAdapter(log, storageManager)
			}
			feedback := master.NewFeedbackCollector(log, tradeReader, weights)

			// Build allocator config with dynamic overrides for multi_pair.capital_allocation.* params.
			// Guards (v > 0) prevent a zero-initialized dynamic DB from silently clobbering
			// the config.yaml values before the operator sets them explicitly.
			totalCapital := cfg.MultiPair.TotalCapitalUSD
			minPerBot := cfg.MultiPair.CapitalAllocation.MinPerBotUSD
			maxPerBot := cfg.MultiPair.CapitalAllocation.MaxPerBotUSD
			reservePct := cfg.MultiPair.CapitalAllocation.ReservePercent
			maxCorrExp := cfg.MultiPair.CapitalAllocation.MaxCorrelatedExposure
			minPairsDeploy := cfg.MultiPair.CapitalAllocation.MinPairsForFullDeploy
			if snap := dynamic.SnapshotFromContext(context.Background()); snap != nil {
				if v := snap.Float("multi_pair.total_capital_usd"); v > 0 {
					totalCapital = v
				}
				if v := snap.Float("multi_pair.capital_allocation.min_per_bot_usd"); v > 0 {
					minPerBot = v
				}
				if v := snap.Float("multi_pair.capital_allocation.max_per_bot_usd"); v > 0 {
					maxPerBot = v
				}
				if v := snap.Float("multi_pair.capital_allocation.reserve_percent"); v > 0 {
					reservePct = v
				}
				if v := snap.Float("multi_pair.capital_allocation.max_correlated_exposure"); v > 0 {
					maxCorrExp = v
				}
				if v := snap.Int("multi_pair.capital_allocation.min_pairs_for_full_deploy"); v > 0 {
					minPairsDeploy = v
				}
			}
			allocatorCfg := &services.AllocatorConfig{
				Mode:                  services.AllocationMode(cfg.MultiPair.CapitalAllocation.Mode),
				TotalCapitalUSD:       totalCapital,
				MinPerBotUSD:          minPerBot,
				MaxPerBotUSD:          maxPerBot,
				ReservePercent:        reservePct,
				MaxCorrelatedExposure: maxCorrExp,
				MinPairsForFullDeploy: minPairsDeploy,
			}
			allocator := services.NewCapitalAllocator(allocatorCfg, log)

			masterCfg := master.MasterConfig{
				Exchange:         cfg.MasterAgent.Exchange,
				LoopInterval:     cfg.MasterAgent.LoopInterval,
				ScanTopN:         cfg.MasterAgent.ScanTopN,
				SpawnThreshold:   cfg.MultiPair.BotLifecycle.SpawnThreshold,
				KillThreshold:    cfg.MultiPair.BotLifecycle.KillThreshold,
				FeedbackInterval: cfg.MasterAgent.Feedback.FeedbackInterval,
				PaperTrading:     cfg.MasterAgent.PaperTrading.Enabled,
				MultiPairEnabled: cfg.MultiPair.Enabled,
			}

			masterAgent := master.NewMasterAgent(log, masterCfg, scanner, configGen, lifecycle, feedback, allocator)

			// Wire the pre-created ranked-pair provider so the REST endpoint
			// GET /api/v1/pairs/ranked can serve live master-agent data.
			rankedPairProv.agent = masterAgent

			// Multi-exchange scan matrix (DM-9.2): only enable matrix iteration
			// when multi_exchange.enabled is set; otherwise leave scanExchanges
			// unset so the agent scans cfg.MasterAgent.Exchange alone (behavior
			// byte-for-byte identical to single-exchange today).
			// TODO wire multi_exchange.enabled → enabled exchanges. The static
			// config.Config struct has no multi_exchange.enabled flag yet (only
			// the dynamic-config manifest defines multi_exchange.enabled). Once
			// the static config exposes it, gate the call below on that flag:
			//   if cfg.MultiExchange.Enabled {
			//       masterAgent.SetScanExchanges(exchangeManager.GetEnabledExchanges())
			//   }
			// Until then we take the default single-exchange path (no-op).

			// Hand the master to the main bot so smart pair selection can route
			// to the highest-scored opportunity at the start of each cycle.
			aiTradingBot.SetPairProvider(&masterPairProvider{agent: masterAgent})

			// Persistence: warm-restart of weights and last state snapshot
			if storageManager != nil && storageManager.Master != nil {
				masterAgent.EnablePersistence(storageManager.Master)
				log.Info("✅ Master Agent persistence enabled (warm restart, opportunity history)")
			}

			// Persist generated configs to trading_profiles
			if storageManager != nil && storageManager.TradingProfiles != nil {
				configStorage := masterAdapters.NewConfigStorageAdapter(log, storageManager.TradingProfiles)
				configGen.EnableStorage(configStorage)
				log.Info("✅ Master Agent config storage enabled (configs persisted to trading_profiles)")
			}

			// Prometheus metrics — attach to default registerer.
			// TODO: switch to monitoringMgr.GetRegistry() once the manager
			// exposes a public accessor for its prometheus.Registry.
			masterMetrics := master.NewMetrics()
			var promReg prometheus.Registerer = prometheus.DefaultRegisterer
			if err := masterMetrics.Register(promReg); err != nil {
				log.WithError(err).Warn("master metrics registration failed (continuing without metrics)")
			} else {
				masterAgent.EnableMetrics(masterMetrics)
				log.Info("✅ Master Agent Prometheus metrics enabled")
			}

			masterHandler.SetMasterAgent(masterAgent)
			log.Info("✅ Master Agent initialised")
			// Ring 0: the master agent's scan loop spawns/kills bots; stop it too.
			killswitch.RegisterEngine(killswitch.FuncEngine{
				EngineName: "master_agent",
				StopFn: func(ctx context.Context) error {
					if !masterAgent.IsRunning() {
						return nil
					}
					return masterAgent.Stop()
				},
			})

			if cfg.MasterAgent.AutoStart {
				if err := masterAgent.Start(context.Background()); err != nil {
					log.WithError(err).Warn("master agent auto-start failed")
				} else {
					log.Info("✅ Master Agent auto-started (scanning loop active)")
				}
			} else {
				log.Info("master_agent.auto_start=false — use POST /api/v1/master/start to launch")
			}

			// ── Multi-pair AI orchestrator ─────────────────────────────────────
			// When ai_bot.multi_pair.enabled=true, spawn one independent AITradingBot
			// per top-scored pair from the master agent, up to max_bots in parallel.
			// The legacy single-bot continues running on its own pair; the orchestrator
			// operates on the remaining top opportunities without overlapping.
			if snap := dynamic.SnapshotFromContext(context.Background()); snap != nil && snap.Bool("ai_bot.multi_pair.enabled") {
				orcCfg := services.OrchestratorConfig{
					MaxBots:           snap.Int("ai_bot.multi_pair.max_bots"),
					SpawnThreshold:    snap.Float("ai_bot.multi_pair.spawn_threshold"),
					KillThreshold:     snap.Float("ai_bot.multi_pair.kill_threshold"),
					MinCapitalPerBot:  snap.Float("ai_bot.multi_pair.min_capital_per_bot_usd"),
					MaxCapitalPerBot:  snap.Float("ai_bot.multi_pair.max_capital_per_bot_usd"),
					ReservePercent:    cfg.MultiPair.CapitalAllocation.ReservePercent,
					ReconcileInterval: 5 * time.Minute,
				}

				// Capture dependencies for the factory closure (same deps as the legacy bot).
				factoryContextBuilder := contextBuilder
				factoryDecisionEngine := decisionEngine
				factoryGCTClient := scalpingGCTClient
				factoryLog := log
				factoryDryRun := tradingBotConfig.DryRun
				factoryAutoExecute := tradingBotConfig.AutoExecute
				factoryExchange := tradingBotConfig.Exchange
				factoryLLMAnalyzer := botLLMAnalyzer // may be nil if GOOGLE_API_KEY unset

				factoryMarketType := tradingBotConfig.MarketType
				factoryLeverage := tradingBotConfig.Leverage

				botFactory := func(pair string, capitalUSD float64) (*services.AITradingBot, error) {
					botCfg := services.DefaultTradingBotConfig()
					botCfg.Pair = pair
					botCfg.Exchange = factoryExchange
					botCfg.TradeSize = capitalUSD
					botCfg.DryRun = factoryDryRun
					botCfg.AutoExecute = factoryAutoExecute
					botCfg.MaxOpenPositions = 1
					botCfg.MarketType = factoryMarketType
					botCfg.Leverage = factoryLeverage
					// Disable smart pair-switching: each orchestrated bot stays on its pair.
					botCfg.PairSelection.Enabled = false

					posMan := aiCore.NewPositionManager(&aiCore.PositionConfig{
						AutoClose:     true,
						CheckInterval: 5,
					}, factoryLog)

					b := services.NewAITradingBot(
						factoryContextBuilder,
						factoryDecisionEngine,
						posMan,
						nil, // riskCalculator
						nil, // sltpExecutor
						factoryGCTClient,
						factoryLLMAnalyzer,
						nil, // notificationManager
						nil, // learningSystem
						nil, // autoConfigurator
						botCfg,
						factoryLog,
					)
					if cfgStore != nil {
						b.SetConfigStore(cfgStore)
					}
					// BOT-02 fix: every orchestrated (multi-pair) bot must also
					// respect the central RiskSentinel kill switch, not just the
					// legacy single-pair aiTradingBot.
					b.SetRiskSentinel(riskSentinel)
					// AI-02 fix: same pre-trade validation for every orchestrated bot.
					b.SetValidationService(validationSvc)
					// Propagate shared learners/classifier to orchestrated bots so
					// per-pair feedback (wick threshold tuning + volatility tier
					// classification) is collected across the entire bot fleet,
					// not just the legacy single-pair bot.
					if aiTradingBot != nil {
						if vc := aiTradingBot.GetVolatilityClassifier(); vc != nil {
							b.SetVolatilityClassifier(vc)
						}
						if wl := aiTradingBot.GetWickLearner(); wl != nil {
							b.SetWickLearner(wl)
						}
					}
					// Persist orchestrated bots' closed trades to trade_history too,
					// otherwise multi-pair fleet trades never reach the DB.
					if storageManager != nil && storageManager.TradeHistory != nil {
						b.SetTradeStatsReader(&tradeStatsAdapter{repo: storageManager.TradeHistory})
						b.SetTradeHistoryWriter(&tradeHistoryWriterAdapter{repo: storageManager.TradeHistory})
					}
					if storageManager != nil && storageManager.Blacklist != nil {
						b.SetBlacklistStore(storageManager.Blacklist)
					}
					// Same xsec-live exclusion as the legacy bot — every orchestrated
					// fleet bot shares the same KuCoin account, so all of them must
					// respect the ownership registry, not just the legacy single-pair bot.
					if storageManager != nil {
						if db := storageManager.DB(); db != nil {
							b.SetXSECBookReader(&xsecBookReaderAdapter{db: db})
						}
					}
					// Wire the regime detector and ML predictor into EVERY orchestrated
					// fleet bot. Without this they were nil on fleet bots, so both entry
					// gates silently no-op'd ON THE BOTS THAT ACTUALLY TRADE in multi_pair
					// mode: checkRegime returned nil → the regime gate never ran (trades
					// fired in RANGING), and mlPredictor==nil → mlEntryVote failed open →
					// the ML gate never vetoed. The legacy bot had both; the fleet did not.
					// regime.NewDetector is a pure/stateless classifier, safe per-bot.
					b.SetRegimeDetector(regime.NewDetector(factoryLog))
					if mlURL := os.Getenv("ML_SERVER_URL"); mlURL != "" {
						b.SetMLPredictor(services.NewHTTPMLPredictor(mlURL, 5*time.Second))
					} else {
						b.SetMLPredictor(services.NewNoopMLPredictor())
					}
					// Certified-pair validation gate — SAME bug class as the regime/ML
					// gates above: only the legacy bot was wired, so every fleet bot
					// had validationGate==nil and silently skipped the gate. Observed
					// 2026-06-11: orchestrated STG/AAVE bots entered real trades with
					// 0 certified pairs while bot.validation_gate.enabled=true.
					if storageManager != nil && storageManager.Validation != nil {
						b.SetValidationGate(storageManager.Validation)
					}
					return b, nil
				}

				orcSource := &masterOrchestratorSource{agent: masterAgent}
				multiPairOrchestrator := services.NewMultiPairAIOrchestrator(orcCfg, log, botFactory)

				// Re-point the Motor control panel at the canonical AI orchestrator:
				// /api/v1/bots* now lists/kills/toggles these bots instead of the
				// (disabled) scalping spawner. Status.FleetActive flips true so the UI
				// renders the fleet.
				masterHandler.SetFleet(&orchestratorFleetAdapter{orc: multiPairOrchestrator})
				log.Info("✅ Motor control panel wired to AI orchestrator fleet (/api/v1/bots*)")

				// Dynamically exclude whatever pair the legacy bot is currently operating on.
				// pair_selection may switch it away from the config pair, so we read live.
				multiPairOrchestrator.SetDynamicExclude(func() string {
					if cfg := aiTradingBot.GetConfig(); cfg != nil {
						return cfg.Pair
					}
					return ""
				})
				// Also statically exclude the config pair as a fallback.
				if tradingBotConfig.Pair != "" {
					multiPairOrchestrator.ExcludePair(tradingBotConfig.Pair)
				}
				// H-USDT: the scalping gRPC client returns spot candles while the
				// live ticker returns futures price → 22% drift blocks every trade.
				// Only the legacy bot (main gRPC client) can resolve H-USDT correctly.
				multiPairOrchestrator.ExcludePair("H-USDT")

				orcCtx, orcCancel := context.WithCancel(context.Background())
				multiPairOrchestrator.Start(orcCtx, orcSource, cfg.MultiPair.TotalCapitalUSD)
				killswitch.RegisterEngine(killswitch.FuncEngine{
					EngineName: "multi_pair_ai_orchestrator",
					StopFn: func(ctx context.Context) error {
						orcCancel()
						multiPairOrchestrator.Stop()
						return nil
					},
				})
				defer func() {
					orcCancel()
					multiPairOrchestrator.Stop()
				}()

				log.WithFields(logrus.Fields{
					"max_bots":          orcCfg.MaxBots,
					"spawn_threshold":   orcCfg.SpawnThreshold,
					"total_capital_usd": cfg.MultiPair.TotalCapitalUSD,
					"excluded_pair":     tradingBotConfig.Pair,
				}).Info("✅ Multi-pair AI orchestrator started")
			} else {
				log.Info("ai_bot.multi_pair.enabled=false — single-bot mode (set to true to enable multi-pair)")
			}
		} else {
			log.Info("master_agent.enabled=false — endpoints respond 503 until enabled")
		}

		// Gate the state-mutating master/bots control routes behind the same JWT
		// middleware the v1 group uses; reads stay public. Without this they were
		// reachable unauthenticated on the raw REST port (0.0.0.0:8081).
		if enableJWTAuth && kongJWTSecret != "" {
			masterAuthMW := tradingmw.KongJWTMiddleware(kongJWTSecret, internalGatewayToken, log)
			masterHandler.RegisterRoutesGin(router, masterAuthMW)
			log.Info("✅ Master Agent endpoints registered (control routes JWT-gated): /api/v1/master/* and /api/v1/bots*")
		} else {
			masterHandler.RegisterRoutesGin(router)
			log.Warn("⚠️  Master Agent endpoints registered WITHOUT auth (JWT disabled): /api/v1/master/* and /api/v1/bots*")
		}
	} else {
		log.Warn("⚠️  scalping adapter unavailable — master agent endpoints not registered")
	}

	// Register AI/analysis/storage handlers (previously unreachable via mux).
	// They have no authentication: the product edition does not mount them.
	legacy := []legacyRouteMounter{analysisHandler, monitoringHandler, knowledgeHandler, validationHandler}
	if strategyHandler != nil {
		legacy = append(legacy, strategyHandler)
	}
	if metricsHandler != nil {
		legacy = append(legacy, metricsHandler)
	}
	if mountLegacyRoutes(router, profile, legacy...) > 0 {
		log.Info("✅ Analysis/monitoring/strategy/validation/knowledge/metrics endpoints registered")
	} else {
		log.Info("Legacy analysis/monitoring/strategy/validation/knowledge/metrics endpoints not mounted (product profile)")
	}

	// Usar 0.0.0.0 para permitir acceso desde la red
	dashboardAddress := httpListenAddr(cfg.Server.BindAddress, port)
	warnIfExposed(log, "rest-dashboard", port, cfg.Server.BindAddress)
	dashboardURL := fmt.Sprintf("http://%s:%s", localIP, port)

	go func() {
		log.WithFields(logrus.Fields{
			"address": dashboardAddress,
			"url":     dashboardURL,
		}).Info("Starting Scalping Dashboard API")
		if err := router.Run(dashboardAddress); err != nil {
			log.WithError(err).Error("Failed to start scalping dashboard API")
		}
	}()

	// Inicializar y arrancar API Gateway con JWT authentication
	// gateway.port (env GATEWAY_PORT) defaults to 9090, the historical port.
	gatewayPort := 9090
	if v := viper.GetInt("gateway.port"); v > 0 {
		gatewayPort = v
	}
	if v, convErr := strconv.Atoi(os.Getenv("GATEWAY_PORT")); convErr == nil && v > 0 {
		gatewayPort = v
	}
	metricsPort := 9091
	if v, convErr := strconv.Atoi(os.Getenv("GATEWAY_METRICS_PORT")); convErr == nil && v > 0 {
		metricsPort = v
	}
	gatewayConfig := &gateway.GatewayConfig{
		Port:                 gatewayPort,
		MetricsPort:          metricsPort,
		MCPServerURL:         fmt.Sprintf("http://localhost:%d", cfg.Server.Port),
		ScalpingAPIURL:       fmt.Sprintf("http://localhost:%s", port),
		EnableCORS:           true,
		CORSAllowedOrigins:   dashboardCORSOrigins,
		BindAddress:          cfg.Server.BindAddress,
		EnableJWTAuth:        enableJWTAuth,
		JWTSecret:            kongJWTSecret,
		InternalGatewayToken: internalGatewayToken,
	}

	warnIfExposed(log, "api-gateway", strconv.Itoa(gatewayPort), cfg.Server.BindAddress)
	apiGateway, err := gateway.NewGateway(gatewayConfig, log)
	if err != nil {
		log.WithError(err).Fatal("Failed to create API Gateway")
	}

	// Set health manager for comprehensive health checks
	// Note: apiGateway is *gateway.Gateway, need to cast properly
	log.Info("Health manager integrated with API Gateway")

	if err := apiGateway.Start(context.Background()); err != nil {
		log.WithError(err).Fatal("Failed to start API Gateway")
	}

	gatewayURL := fmt.Sprintf("http://%s:9090", localIP)
	log.WithFields(logrus.Fields{
		"gateway_url": gatewayURL,
		"port":        9090,
	}).Info("API Gateway started - Single endpoint for Flutter client")

	// Every tool is registered by now: log an error for each one that has no
	// permission class (it will be neither listed nor executable).
	mcpServer.AuditToolClassification()

	// Iniciar servidor MCP en goroutine
	serverCtx, serverCancel := context.WithCancel(context.Background())
	defer serverCancel()

	serverErrChan := make(chan error, 1)
	go func() {
		if err := mcpServer.Start(serverCtx); err != nil {
			serverErrChan <- err
		}
	}()

	// HFT metrics are automatically initialized by gctClient
	enableHFTMetrics := os.Getenv("ENABLE_HFT_METRICS") != "false" // Enabled by default
	if enableHFTMetrics {
		log.WithFields(logrus.Fields{
			"endpoint": fmt.Sprintf("http://%s:9090/metrics", localIP),
		}).Info("✓ HFT Prometheus metrics enabled (auto-initialized)")
	}

	log.Info("Trading MCP Server with Scalping Engine initialized successfully")

	// Esperar señal de terminación o error del servidor
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	select {
	case sig := <-sigChan:
		log.WithField("signal", sig).Info("Received shutdown signal")
	case err := <-serverErrChan:
		log.WithError(err).Error("Server error")
	}

	log.Info("Shutting down Trading MCP Server...")

	// Shutdown graceful
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()

	// Detener WebSocket Hub primero
	log.Info("Shutting down WebSocket Hub...")
	hubCancel()  // Cancel Hub context
	wsHub.Stop() // Graceful shutdown of all connections
	log.Info("✅ WebSocket Hub stopped")

	// Detener scalping engine si está corriendo
	if scalpingEngine != nil && scalpingEngine.IsRunning() {
		if err := scalpingEngine.Stop(); err != nil {
			log.WithError(err).Error("Error stopping scalping engine")
		}
	}

	if err := mcpServer.Stop(shutdownCtx); err != nil {
		log.WithError(err).Error("Error during server shutdown")
	}

	if err := gctClient.Disconnect(); err != nil {
		log.WithError(err).Error("Error disconnecting MCP GoCryptoTrader client")
	}

	if err := scalpingGCTClient.Disconnect(); err != nil {
		log.WithError(err).Error("Error disconnecting scalping GoCryptoTrader client")
	}

	log.Info("Trading MCP Server stopped")
}

func loadScalpingConfig() error {
	viper.SetConfigName("scalping")
	viper.SetConfigType("yaml")
	viper.AddConfigPath("./configs")
	viper.AddConfigPath("./config")
	viper.AddConfigPath(".")

	// Valores por defecto
	viper.SetDefault("scalping.exchange", "kucoin")
	viper.SetDefault("scalping.pairs", []string{"BTC-USDT"})
	viper.SetDefault("scalping.auto_start", false)
	viper.SetDefault("ai_bot.auto_start", true)
	viper.SetDefault("scalping.api.port", "8081")
	viper.SetDefault("monitoring.prometheus.port", 9090)

	if err := viper.ReadInConfig(); err != nil {
		if _, ok := err.(viper.ConfigFileNotFoundError); ok {
			return nil
		}
		return err
	}

	return nil
}

func registerScalpingStrategies(log *logrus.Logger, gctClient gct.GCTClient, aiPredictor *ai.AIPredictor, orchestrator *strategies.StrategyOrchestrator) {
	log.Info("Registering scalping strategies...")

	// Registrar estrategia RSI
	rsiStrategy := strategies.NewRSIScalpingStrategy(log, gctClient)
	if err := orchestrator.RegisterStrategy(rsiStrategy); err != nil {
		log.Warnf("Failed to register RSI strategy: %v", err)
	}

	// Registrar estrategia MACD
	macdStrategy := strategies.NewMACDScalpingStrategy(log, gctClient)
	if err := orchestrator.RegisterStrategy(macdStrategy); err != nil {
		log.Warnf("Failed to register MACD strategy: %v", err)
	}

	// Registrar estrategia Bollinger
	bollingerStrategy := strategies.NewBollingerScalpingStrategy(log, gctClient)
	if err := orchestrator.RegisterStrategy(bollingerStrategy); err != nil {
		log.Warnf("Failed to register Bollinger strategy: %v", err)
	}

	// Registrar estrategia Volume
	volumeStrategy := strategies.NewVolumeScalpingStrategy(log, gctClient)
	if err := orchestrator.RegisterStrategy(volumeStrategy); err != nil {
		log.Warnf("Failed to register Volume strategy: %v", err)
	}

	// Registrar estrategia AI (si el modelo está disponible)
	aiStrategy := strategies.NewAIScalpingStrategy(log, aiPredictor)
	if err := orchestrator.RegisterStrategy(aiStrategy); err != nil {
		log.Warnf("Failed to register AI strategy: %v", err)
	}

	log.Infof("Registered %d strategies", len(orchestrator.GetAllStrategies()))
}

func getScalpingRiskParameters() *models.RiskParameters {
	// QUANT/SCALP-03 fix: config.yaml stores these under
	// scalping.default_risk_params.*, not scalping.risk.* — the previous keys
	// here never matched anything in configs/mcp-trading/config.yaml, so every
	// viper.Get* call silently returned its zero value. That made this
	// "correctly-configured" risk manager actually all-zeros (worse than the
	// hardcoded fallback), which is why it was never safe to wire into the
	// production scalping tool path until now.
	maxTotalExposure := viper.GetFloat64("scalping.default_risk_params.max_total_exposure_usd")
	if maxTotalExposure <= 0 {
		// scalping.default_risk_params has no total-exposure key; fall back to
		// the platform-wide per-pair exposure cap (risk_limits.max_exposure_per_pair_usd),
		// then to risk_limits.max_position_size_usd.
		maxTotalExposure = viper.GetFloat64("risk_limits.max_exposure_per_pair_usd")
	}
	if maxTotalExposure <= 0 {
		maxTotalExposure = viper.GetFloat64("risk_limits.max_position_size_usd")
	}

	return &models.RiskParameters{
		MaxPositionSizeUSD:   viper.GetFloat64("scalping.default_risk_params.max_position_size_usd"),
		MaxTotalExposureUSD:  maxTotalExposure,
		StopLossPercent:      viper.GetFloat64("scalping.default_risk_params.stop_loss_percent"),
		TakeProfitPercent:    viper.GetFloat64("scalping.default_risk_params.take_profit_percent"),
		MaxDailyLossUSD:      viper.GetFloat64("scalping.default_risk_params.max_daily_loss_usd"),
		MaxConsecutiveLosses: viper.GetInt("scalping.default_risk_params.max_consecutive_losses"),
		VolatilityMultiplier: viper.GetFloat64("scalping.default_risk_params.volatility_multiplier"),
		KellyFraction:        viper.GetFloat64("scalping.default_risk_params.kelly_fraction"),
		RiskFreeRate:         viper.GetFloat64("scalping.default_risk_params.risk_free_rate"),
		ConfidenceThreshold:  viper.GetFloat64("scalping.default_risk_params.confidence_threshold"),
	}
}

func configureLogger(log *logrus.Logger, cfg *config.Config) {
	// Configurar nivel
	level, err := logrus.ParseLevel(cfg.Logging.Level)
	if err != nil {
		level = logrus.InfoLevel
	}
	log.SetLevel(level)

	// Configurar formato
	if cfg.Logging.Format == "json" {
		log.SetFormatter(&logrus.JSONFormatter{})
	} else {
		log.SetFormatter(&logrus.TextFormatter{
			FullTimestamp: true,
		})
	}

	// Configurar output
	switch cfg.Logging.Output {
	case "file":
		file, err := os.OpenFile(cfg.Logging.File, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0666)
		if err != nil {
			log.WithError(err).Warn("Failed to open log file, using stdout")
		} else {
			log.SetOutput(file)
		}
	case "both":
		// TODO: Implementar output a archivo y stdout simultáneamente
		log.Warn("'both' output mode not yet implemented, using stdout")
	default:
		log.SetOutput(os.Stdout)
	}
}

// buildDynamicSizing translates the mcp-trading config-package representation
// of dynamic sizing into the services-package representation consumed by the
// bot. Returns a zero (disabled) value when the YAML section is missing or
// empty, so callers can assign unconditionally.
func buildDynamicSizing(src config.BotDynamicSizingConfig) services.DynamicSizingConfig {
	out := services.DynamicSizingConfig{Enabled: src.Enabled}
	if len(src.Tiers) == 0 {
		return out
	}
	out.Tiers = make([]services.SizingTier, 0, len(src.Tiers))
	for _, t := range src.Tiers {
		out.Tiers = append(out.Tiers, services.SizingTier{
			Name:          t.Name,
			MinConfidence: t.MinConfidence,
			MaxConfidence: t.MaxConfidence,
			SizeUSD:       t.SizeUSD,
			TakeProfitPct: t.TakeProfitPct,
			StopLossPct:   t.StopLossPct,
		})
	}
	return out
}

// buildPartialTP translates the YAML partial_tp ladder into the bot's
// internal type. Returns a zero (disabled) value when the section is missing
// or empty, so callers can assign unconditionally.
func buildPartialTP(src config.BotPartialTPConfig) services.PartialTPConfig {
	out := services.PartialTPConfig{Enabled: src.Enabled}
	if len(src.Levels) == 0 {
		return out
	}
	out.Levels = make([]services.PartialTPLevel, 0, len(src.Levels))
	for _, l := range src.Levels {
		out.Levels = append(out.Levels, services.PartialTPLevel{
			TPPct:             l.TPPct,
			ClosePct:          l.ClosePct,
			MoveSLToBreakeven: l.MoveSLToBreakeven,
		})
	}
	return out
}

// buildPairSelection translates the YAML pair_selection section.
func buildPairSelection(src config.BotPairSelectionConfig) services.PairSelectionConfig {
	return services.PairSelectionConfig{
		Enabled:               src.Enabled,
		MinScore:              src.MinScore,
		SwitchCooldownSeconds: src.SwitchCooldownSeconds,
		FallbackPair:          src.FallbackPair,
	}
}

// masterPairProvider adapts the master agent's state into the services.PairProvider
// interface so the bot can route to the highest-scored opportunity without
// importing the master package. Returns opportunities sorted by score desc.
type masterPairProvider struct {
	agent *master.MasterAgent
}

func (m *masterPairProvider) TopOpportunities() []services.RankedPair {
	if m == nil || m.agent == nil {
		return nil
	}
	state := m.agent.GetState()
	if state == nil || len(state.Opportunities) == 0 {
		return nil
	}
	out := make([]services.RankedPair, 0, len(state.Opportunities))
	for _, o := range state.Opportunities {
		if o == nil || o.Pair == "" {
			continue
		}
		// Master surfaces OpportunityScore on a 0-100 raw scale. Normalize to
		// 0-1 here so callers can use natural thresholds (e.g. min_score: 0.55).
		out = append(out, services.RankedPair{
			Pair:     o.Pair,
			Score:    o.OpportunityScore / 100.0,
			Exchange: o.Exchange,
		})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Score > out[j].Score })
	return out
}

// TopRankedPairs satisfies apiv1handlers.RankedPairProvider so masterPairProvider
// can be wired into PairsHandler.GetRankedPairs without a separate adapter type.
// n <= 0 means return all. Results are sorted by score descending.
func (m *masterPairProvider) TopRankedPairs(n int) []apiv1handlers.RankedPairEntry {
	opps := m.TopOpportunities()
	if len(opps) == 0 {
		return nil
	}
	if n > 0 && n < len(opps) {
		opps = opps[:n]
	}
	out := make([]apiv1handlers.RankedPairEntry, 0, len(opps))
	for _, o := range opps {
		out = append(out, apiv1handlers.RankedPairEntry{
			Pair:     o.Pair,
			Exchange: o.Exchange,
			Score:    o.Score,
		})
	}
	return out
}

// serverRankedPairAdapter bridges *masterPairProvider to the
// server.RankedPairProvider interface so the public MCP mux can serve
// GET /api/v1/pairs/ranked without the server package importing api/v1/handlers.
type serverRankedPairAdapter struct {
	prov *masterPairProvider
}

func (a *serverRankedPairAdapter) TopRankedPairs(n int) []server.RankedPairEntry {
	src := a.prov.TopRankedPairs(n)
	out := make([]server.RankedPairEntry, 0, len(src))
	for _, e := range src {
		out = append(out, server.RankedPairEntry{
			Pair:     e.Pair,
			Exchange: e.Exchange,
			Score:    e.Score,
		})
	}
	return out
}

// masterOrchestratorSource adapts *master.MasterAgent into the
// services.OpportunitySource interface consumed by MultiPairAIOrchestrator.
type masterOrchestratorSource struct {
	agent *master.MasterAgent
}

func (s *masterOrchestratorSource) GetOpportunities() []services.OrchestratorOpportunity {
	if s == nil || s.agent == nil {
		return nil
	}
	state := s.agent.GetState()
	if state == nil {
		return nil
	}
	out := make([]services.OrchestratorOpportunity, 0, len(state.Opportunities))
	for _, o := range state.Opportunities {
		if o == nil || o.Pair == "" {
			continue
		}
		out = append(out, services.OrchestratorOpportunity{
			Pair:             o.Pair,
			Exchange:         o.Exchange,
			OpportunityScore: o.OpportunityScore,
		})
	}
	return out
}

// orchestratorFleetAdapter adapts *services.MultiPairAIOrchestrator to the
// handlers.FleetController interface so the Motor control panel serves from the
// canonical AI orchestrator instead of the legacy scalping spawner. It maps the
// orchestrator's view type into the backend-agnostic handler view.
type orchestratorFleetAdapter struct {
	orc *services.MultiPairAIOrchestrator
}

func (a *orchestratorFleetAdapter) ListManagedBots() []handlers.FleetBotView {
	if a == nil || a.orc == nil {
		return nil
	}
	bots := a.orc.ListManagedBots()
	out := make([]handlers.FleetBotView, 0, len(bots))
	for _, b := range bots {
		out = append(out, handlers.FleetBotView{
			Pair:          b.Pair,
			Exchange:      b.Exchange,
			Status:        "running",
			PaperMode:     b.PaperMode,
			OpenPositions: b.OpenPositions,
			CapitalUSD:    b.CapitalUSD,
			StartedAt:     b.StartedAt,
		})
	}
	return out
}

func (a *orchestratorFleetAdapter) KillBot(pair string, reason string) error {
	return a.orc.KillBot(pair, reason)
}

func (a *orchestratorFleetAdapter) SetBotPaperMode(pair string, enabled bool) error {
	return a.orc.SetBotPaperMode(pair, enabled)
}

// ignoreNotRunning maps the "X is not running" stop errors to success: ring 0
// must be idempotent over engines that were never started or already stopped.
func ignoreNotRunning(err error) error {
	if err != nil && strings.Contains(err.Error(), "not running") {
		return nil
	}
	return err
}
