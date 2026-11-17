-- Migration 001: Initial Schema for Dynamic Trading System
-- Created: 2025-12-03
-- Description: Creates tables for trading styles, timeframe data cache, trade history, and performance metrics

-- ============================================================================
-- Table: trading_styles
-- Purpose: Store trading strategy configurations dynamically
-- ============================================================================
CREATE TABLE IF NOT EXISTS trading_styles (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    name TEXT NOT NULL UNIQUE,
    type TEXT NOT NULL, -- scalping, day_trading, swing_trading, position_trading
    config_yaml TEXT NOT NULL, -- Full YAML configuration
    primary_timeframe TEXT NOT NULL,
    secondary_timeframes TEXT, -- JSON array of timeframes
    required_timeframes TEXT, -- JSON array of required timeframes
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    active BOOLEAN DEFAULT 1
);

CREATE INDEX idx_trading_styles_name ON trading_styles(name);
CREATE INDEX idx_trading_styles_type ON trading_styles(type);
CREATE INDEX idx_trading_styles_active ON trading_styles(active);

-- ============================================================================
-- Table: timeframe_data_cache
-- Purpose: Cache market data by pair and timeframe to reduce exchange calls
-- ============================================================================
CREATE TABLE IF NOT EXISTS timeframe_data_cache (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    pair TEXT NOT NULL,
    timeframe TEXT NOT NULL,
    timestamp DATETIME NOT NULL,
    open REAL NOT NULL,
    high REAL NOT NULL,
    low REAL NOT NULL,
    close REAL NOT NULL,
    volume REAL NOT NULL,
    cached_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    expires_at DATETIME NOT NULL,
    UNIQUE(pair, timeframe, timestamp)
);

CREATE INDEX idx_cache_pair_tf ON timeframe_data_cache(pair, timeframe);
CREATE INDEX idx_cache_expires ON timeframe_data_cache(expires_at);
CREATE INDEX idx_cache_timestamp ON timeframe_data_cache(timestamp DESC);

-- ============================================================================
-- Table: trade_history
-- Purpose: Store complete trade history with dynamic metadata
-- ============================================================================
CREATE TABLE IF NOT EXISTS trade_history (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    trade_id TEXT NOT NULL UNIQUE,
    
    -- Basic trade info
    pair TEXT NOT NULL,
    exchange TEXT NOT NULL,
    side TEXT NOT NULL, -- buy, sell
    
    -- Trading style context
    trading_style TEXT NOT NULL,
    trading_style_type TEXT NOT NULL,
    primary_timeframe TEXT NOT NULL,
    timeframes_used TEXT, -- JSON array of all timeframes used
    
    -- Entry details
    entry_price REAL NOT NULL,
    entry_time DATETIME NOT NULL,
    size REAL NOT NULL,
    leverage INTEGER DEFAULT 1,
    
    -- Exit details
    exit_price REAL,
    exit_time DATETIME,
    exit_reason TEXT, -- stop_loss, take_profit, manual, timeout
    
    -- Risk management
    stop_loss REAL,
    take_profit REAL,
    
    -- Performance
    pnl REAL,
    pnl_pct REAL,
    fees REAL DEFAULT 0,
    net_pnl REAL,
    
    -- AI Decision context
    confidence REAL NOT NULL,
    decision_reason TEXT,
    indicators_snapshot TEXT, -- JSON of indicators at entry
    
    -- Status
    status TEXT NOT NULL, -- open, closed, cancelled
    
    -- Timestamps
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX idx_trade_pair ON trade_history(pair);
CREATE INDEX idx_trade_style ON trade_history(trading_style);
CREATE INDEX idx_trade_style_type ON trade_history(trading_style_type);
CREATE INDEX idx_trade_timeframe ON trade_history(primary_timeframe);
CREATE INDEX idx_trade_status ON trade_history(status);
CREATE INDEX idx_trade_entry_time ON trade_history(entry_time DESC);
CREATE INDEX idx_trade_pnl ON trade_history(pnl);

-- ============================================================================
-- Table: performance_metrics
-- Purpose: Store aggregated performance metrics by style and timeframe
-- ============================================================================
CREATE TABLE IF NOT EXISTS performance_metrics (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    
    -- Grouping keys
    trading_style TEXT NOT NULL,
    trading_style_type TEXT NOT NULL,
    timeframe TEXT, -- NULL for aggregated across all timeframes
    pair TEXT, -- NULL for aggregated across all pairs
    
    -- Time period
    period_start DATETIME NOT NULL,
    period_end DATETIME NOT NULL,
    
    -- Trade counts
    total_trades INTEGER DEFAULT 0,
    winning_trades INTEGER DEFAULT 0,
    losing_trades INTEGER DEFAULT 0,
    
    -- Performance metrics
    win_rate REAL DEFAULT 0,
    profit_factor REAL DEFAULT 0,
    avg_win REAL DEFAULT 0,
    avg_loss REAL DEFAULT 0,
    largest_win REAL DEFAULT 0,
    largest_loss REAL DEFAULT 0,
    
    -- Risk metrics
    max_drawdown REAL DEFAULT 0,
    max_drawdown_pct REAL DEFAULT 0,
    sharpe_ratio REAL DEFAULT 0,
    
    -- PnL
    total_pnl REAL DEFAULT 0,
    total_pnl_pct REAL DEFAULT 0,
    total_fees REAL DEFAULT 0,
    net_pnl REAL DEFAULT 0,
    
    -- Confidence metrics
    avg_confidence REAL DEFAULT 0,
    min_confidence REAL DEFAULT 0,
    max_confidence REAL DEFAULT 0,
    
    -- Timestamps
    calculated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    
    UNIQUE(trading_style, timeframe, pair, period_start, period_end)
);

CREATE INDEX idx_metrics_style ON performance_metrics(trading_style);
CREATE INDEX idx_metrics_style_type ON performance_metrics(trading_style_type);
CREATE INDEX idx_metrics_timeframe ON performance_metrics(timeframe);
CREATE INDEX idx_metrics_period ON performance_metrics(period_start, period_end);
CREATE INDEX idx_metrics_win_rate ON performance_metrics(win_rate DESC);

-- ============================================================================
-- Table: schema_migrations
-- Purpose: Track applied migrations
-- ============================================================================
CREATE TABLE IF NOT EXISTS schema_migrations (
    version INTEGER PRIMARY KEY,
    name TEXT NOT NULL,
    applied_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

-- Insert this migration
INSERT OR IGNORE INTO schema_migrations (version, name) VALUES (1, '001_initial_schema');
