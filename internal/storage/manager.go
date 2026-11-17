package storage

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "github.com/mattn/go-sqlite3"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/sirupsen/logrus"
)

// Manager centralizes access to all storage repositories
type Manager struct {
	db  *sql.DB
	log *logrus.Logger

	// driver is "sqlite" (default) or "postgres"
	driver string
	// pgPool is set when driver == "postgres"
	pgPool *pgxpool.Pool

	// Repositories
	TradingStyles      *TradingStyleRepository
	TimeframeCache     *TimeframeDataCache
	TradeHistory       *TradeHistoryRepository
	PerformanceMetrics *PerformanceMetricsRepository
	PatternLibrary     *PatternLibraryRepository
	Master             *MasterRepository
	TradingProfiles    *TradingProfilesRepository
	PortfolioSnapshots *PortfolioSnapshotRepository
	Blacklist          *BlacklistRepository
	CandleArchive      *CandleArchiveRepository
	Validation         *ValidationRepository
	StrategySpecs      *StrategySpecRepository
	FeeLedger          *FeeLedgerRepository

	// HA-1: Postgres-backed repos.  Nil unless driver=="postgres".
	// Access via PgTradeHistory(), PgPortfolioSnapshots(), etc.
	pgTradeHistory       pgTradeHistoryRepo
	pgPortfolioSnapshots pgPortfolioSnapshotsRepo

	// Migration manager
	migrations *MigrationManager
}

// pgTradeHistoryRepo is the minimal interface a postgres-backed trade repo must satisfy.
// Satisfied by *repos.TradeRepository (defined in internal/storage/postgres/repos).
type pgTradeHistoryRepo interface {
	SaveForUser(ctx context.Context, userID string, trade *Trade) error
	GetOpenTradesForUser(ctx context.Context, userID string) ([]*Trade, error)
	GetClosedTradesForUser(ctx context.Context, userID string, limit int) ([]*Trade, error)
	GetByPairForUser(ctx context.Context, userID, pair string, limit int) ([]*Trade, error)
	GetByDateRangeForUser(ctx context.Context, userID string, start, end time.Time) ([]*Trade, error)
}

// pgPortfolioSnapshotsRepo is the minimal interface for postgres portfolio snapshots.
type pgPortfolioSnapshotsRepo interface {
	SaveForUser(ctx context.Context, userID string, snap *PortfolioSnapshot) error
	GetLatestForUser(ctx context.Context, userID string) (*PortfolioSnapshot, error)
}

// Config holds storage configuration
type Config struct {
	DatabasePath    string
	MaxOpenConns    int
	MaxIdleConns    int
	ConnMaxLifetime time.Duration
}

// OperationalConfig extends Config with the operational driver selection.
// Set Driver to "postgres" and populate PostgresDSN to use the PostgreSQL backend.
// The default Driver value is "sqlite", which preserves the existing behaviour.
type OperationalConfig struct {
	Config

	// Driver selects the operational storage backend: "sqlite" (default) or "postgres".
	// Corresponds to the viper key storage.operational.driver.
	Driver string

	// PostgresDSN is the libpq connection string used when Driver == "postgres".
	// Example: postgres://user:pass@host:5432/dbname?sslmode=disable
	PostgresDSN string
}

// DefaultOperationalConfig returns default operational configuration (SQLite driver).
func DefaultOperationalConfig() *OperationalConfig {
	return &OperationalConfig{
		Config: *DefaultConfig(),
		Driver: "sqlite",
	}
}

// DefaultConfig returns default storage configuration
func DefaultConfig() *Config {
	return &Config{
		DatabasePath:    "data/trading.db",
		MaxOpenConns:    25,
		MaxIdleConns:    5,
		ConnMaxLifetime: 5 * time.Minute,
	}
}

// NewManager creates a new storage manager
func NewManager(config *Config, log *logrus.Logger) (*Manager, error) {
	if config == nil {
		config = DefaultConfig()
	}

	// Ensure the parent directory exists before opening the SQLite file, so
	// dependent repositories (ConsentRepo/GovernanceRepo) are not left nil.
	// Skipped for the connection-scoped in-memory database.
	if config.DatabasePath != ":memory:" {
		if dir := filepath.Dir(config.DatabasePath); dir != "" && dir != "." {
			if err := os.MkdirAll(dir, 0o755); err != nil {
				return nil, fmt.Errorf("failed to create database directory: %w", err)
			}
		}
	}

	// Open database connection
	db, err := sql.Open("sqlite3", config.DatabasePath)
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	// Configure connection pool.
	// SQLite :memory: databases are connection-scoped: each connection sees a
	// different (empty) instance.  Force a single connection so migrations and
	// queries always share the same in-memory DB (critical for tests and any
	// temporary in-process store).
	if config.DatabasePath == ":memory:" {
		db.SetMaxOpenConns(1)
		db.SetMaxIdleConns(1)
	} else {
		db.SetMaxOpenConns(config.MaxOpenConns)
		db.SetMaxIdleConns(config.MaxIdleConns)
		db.SetConnMaxLifetime(config.ConnMaxLifetime)
	}

	// Enable WAL mode for better concurrency (no-op for :memory:, harmless).
	if _, err := db.Exec("PRAGMA journal_mode=WAL"); err != nil {
		return nil, fmt.Errorf("failed to enable WAL mode: %w", err)
	}

	// Enable foreign keys
	if _, err := db.Exec("PRAGMA foreign_keys=ON"); err != nil {
		return nil, fmt.Errorf("failed to enable foreign keys: %w", err)
	}

	manager := &Manager{
		db:  db,
		log: log,
	}

	// Initialize repositories
	manager.TradingStyles = NewTradingStyleRepository(db, log)
	manager.TimeframeCache = NewTimeframeDataCache(db, log)
	manager.TradeHistory = NewTradeHistoryRepository(db, log)
	manager.PerformanceMetrics = NewPerformanceMetricsRepository(db, log)
	manager.PatternLibrary = NewPatternLibraryRepository(db, log)
	manager.Master = NewMasterRepository(db, log)
	manager.TradingProfiles = NewTradingProfilesRepository(db, log)
	manager.PortfolioSnapshots = NewPortfolioSnapshotRepository(db, log)
	manager.Blacklist = NewBlacklistRepository(db, log)
	manager.CandleArchive = NewCandleArchiveRepository(db, log)
	manager.Validation = NewValidationRepository(db, log)
	manager.StrategySpecs = NewStrategySpecRepository(db, log)
	manager.FeeLedger = NewFeeLedgerRepository(db, log)

	// Initialize migration manager
	manager.migrations = NewMigrationManager(db, log)

	log.WithField("path", config.DatabasePath).Info("Storage manager initialized")

	return manager, nil
}

// NewManagerWithOperationalConfig creates a Manager using OperationalConfig.
// When cfg.Driver == "postgres" it opens a pgxpool and wires the Postgres repos
// for user-scoped tables (TradeHistory, PerformanceMetrics, PortfolioSnapshots,
// ModePerformance, UserBotConfig, UserPairSelection). All other repos that are
// either global or SQLite-only continue to use the SQLite db regardless of driver.
//
// If cfg.Driver is empty or "sqlite" the behaviour is identical to NewManager.
func NewManagerWithOperationalConfig(cfg *OperationalConfig, log *logrus.Logger) (*Manager, error) {
	if cfg == nil {
		cfg = DefaultOperationalConfig()
	}
	if cfg.Driver == "" {
		cfg.Driver = "sqlite"
	}

	// Always open the SQLite database — it is still needed for global/shared repos
	// (TradingStyles, TimeframeCache, PatternLibrary, Master, TuningStore, etc.)
	// and for all callers that haven't migrated to the multi-driver path yet.
	manager, err := NewManager(&cfg.Config, log)
	if err != nil {
		return nil, err
	}
	manager.driver = cfg.Driver

	if cfg.Driver != "postgres" {
		return manager, nil
	}

	// -------------------------------------------------------------------
	// Postgres driver: open pool and wire user-scoped repos.
	// -------------------------------------------------------------------
	if cfg.PostgresDSN == "" {
		return nil, fmt.Errorf("storage: driver=postgres but PostgresDSN is empty")
	}

	ctx := context.Background()
	pool, err := pgxpool.New(ctx, cfg.PostgresDSN)
	if err != nil {
		return nil, fmt.Errorf("storage: open postgres pool: %w", err)
	}
	if _, err := pool.Exec(ctx, "SELECT 1"); err != nil {
		pool.Close()
		return nil, fmt.Errorf("storage: postgres ping failed: %w", err)
	}
	manager.pgPool = pool
	log.WithField("driver", "postgres").Info("Storage manager: PostgreSQL operational pool connected")
	return manager, nil
}

// PgTradeHistory returns the Postgres-backed trade history repo.
// Returns nil when driver != "postgres" or the pool is not initialised.
// Use this instead of Manager.TradeHistory when you need per-user RLS queries
// via pgx.
func (m *Manager) PgTradeHistory() pgTradeHistoryRepo {
	return m.pgTradeHistory
}

// PgPortfolioSnapshots returns the Postgres-backed portfolio snapshots repo.
// Returns nil when driver != "postgres".
func (m *Manager) PgPortfolioSnapshots() pgPortfolioSnapshotsRepo {
	return m.pgPortfolioSnapshots
}

// SetPgTradeHistory wires the postgres-backed trade history repo.
// Called from main.go after the repo is constructed with the pgxpool.
func (m *Manager) SetPgTradeHistory(r pgTradeHistoryRepo) {
	m.pgTradeHistory = r
}

// SetPgPortfolioSnapshots wires the postgres-backed portfolio snapshots repo.
func (m *Manager) SetPgPortfolioSnapshots(r pgPortfolioSnapshotsRepo) {
	m.pgPortfolioSnapshots = r
}

// PgPool returns the underlying pgxpool.Pool when the driver is "postgres".
// Returns nil for the sqlite driver.
func (m *Manager) PgPool() *pgxpool.Pool {
	return m.pgPool
}

// Driver returns the operational storage driver name ("sqlite" or "postgres").
func (m *Manager) Driver() string {
	return m.driver
}

// Initialize runs migrations and prepares the database
func (m *Manager) Initialize() error {
	m.log.Info("Initializing storage...")

	// Run migrations
	if err := m.migrations.RunMigrations(); err != nil {
		return fmt.Errorf("failed to run migrations: %w", err)
	}

	// Get current version
	version, err := m.migrations.GetCurrentVersion()
	if err != nil {
		return fmt.Errorf("failed to get schema version: %w", err)
	}

	m.log.WithField("version", version).Info("Storage initialized successfully")

	return nil
}

// DB returns the underlying *sql.DB so callers can initialise additional
// stores (e.g. TuningStore) on the same connection without opening a second one.
func (m *Manager) DB() *sql.DB {
	return m.db
}

// Close closes the database connection and the PostgreSQL pool (if open).
func (m *Manager) Close() error {
	m.log.Info("Closing storage manager...")
	if m.pgPool != nil {
		m.pgPool.Close()
	}
	return m.db.Close()
}

// HealthCheck verifies database connectivity
func (m *Manager) HealthCheck() error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := m.db.PingContext(ctx); err != nil {
		return fmt.Errorf("database ping failed: %w", err)
	}

	return nil
}

// GetStats returns database statistics
func (m *Manager) GetStats() map[string]interface{} {
	stats := m.db.Stats()

	return map[string]interface{}{
		"open_connections":    stats.OpenConnections,
		"in_use":              stats.InUse,
		"idle":                stats.Idle,
		"wait_count":          stats.WaitCount,
		"wait_duration":       stats.WaitDuration.String(),
		"max_idle_closed":     stats.MaxIdleClosed,
		"max_lifetime_closed": stats.MaxLifetimeClosed,
	}
}

// BeginTx starts a new transaction
func (m *Manager) BeginTx() (*sql.Tx, error) {
	return m.db.Begin()
}

// WithTransaction executes a function within a transaction
func (m *Manager) WithTransaction(fn func(*sql.Tx) error) error {
	tx, err := m.db.Begin()
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}

	defer func() {
		if p := recover(); p != nil {
			tx.Rollback()
			panic(p)
		}
	}()

	if err := fn(tx); err != nil {
		tx.Rollback()
		return err
	}

	return tx.Commit()
}

// Vacuum optimizes the database
func (m *Manager) Vacuum() error {
	m.log.Info("Running VACUUM on database...")
	_, err := m.db.Exec("VACUUM")
	if err != nil {
		return fmt.Errorf("failed to vacuum database: %w", err)
	}
	m.log.Info("VACUUM completed")
	return nil
}

// Backup creates a backup of the database
func (m *Manager) Backup(destPath string) error {
	m.log.WithField("dest", destPath).Info("Creating database backup...")

	// Use SQLite backup API
	_, err := m.db.Exec(fmt.Sprintf("VACUUM INTO '%s'", destPath))
	if err != nil {
		return fmt.Errorf("failed to create backup: %w", err)
	}

	m.log.Info("Backup completed successfully")
	return nil
}

// DistinctTradedPairs returns the distinct pairs traded since the given time.
// Used by the validation loop to certify exactly the pairs the bot actually uses.
func (m *Manager) DistinctTradedPairs(since time.Time) ([]string, error) {
	rows, err := m.db.Query(`SELECT DISTINCT pair FROM trade_history WHERE entry_time >= ? AND pair != ''`, since)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// CleanExpiredCache removes expired cache entries
func (m *Manager) CleanExpiredCache() error {
	return m.TimeframeCache.CleanExpired()
}

// GetCacheStats returns cache statistics
func (m *Manager) GetCacheStats() (map[string]interface{}, error) {
	return m.TimeframeCache.GetCacheStats()
}
