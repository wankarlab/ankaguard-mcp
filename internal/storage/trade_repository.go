package storage

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/sirupsen/logrus"
)

// TradeHistoryRepository handles persistence of trade history
type TradeHistoryRepository struct {
	db  *sql.DB
	log *logrus.Logger
}

// NewTradeHistoryRepository creates a new trade history repository
func NewTradeHistoryRepository(db *sql.DB, log *logrus.Logger) *TradeHistoryRepository {
	return &TradeHistoryRepository{
		db:  db,
		log: log,
	}
}

// Save persists a trade to the database attributed to the "default" user.
// Callers that operate in a multi-user context should use SaveForUser instead.
func (r *TradeHistoryRepository) Save(trade *Trade) error {
	return r.SaveForUser("default", trade)
}

// SaveForUser persists a trade to the database attributed to the given userID.
// On INSERT the user_id column is written; on UPDATE the user_id is left unchanged
// (the trade already belongs to whoever created it).
func (r *TradeHistoryRepository) SaveForUser(userID string, trade *Trade) error {
	if userID == "" {
		userID = "default"
	}

	// Serialize arrays and maps to JSON
	timeframesJSON, err := json.Marshal(trade.TimeframesUsed)
	if err != nil {
		return fmt.Errorf("failed to marshal timeframes: %w", err)
	}

	indicatorsJSON, err := json.Marshal(trade.IndicatorsSnapshot)
	if err != nil {
		return fmt.Errorf("failed to marshal indicators: %w", err)
	}

	// Check if exists
	var exists bool
	err = r.db.QueryRow("SELECT EXISTS(SELECT 1 FROM trade_history WHERE trade_id = ?)", trade.ID).Scan(&exists)
	if err != nil {
		return fmt.Errorf("failed to check if trade exists: %w", err)
	}

	now := time.Now()

	if exists {
		// Update existing — user_id is intentionally NOT updated so ownership is immutable
		_, err = r.db.Exec(`
			UPDATE trade_history
			SET exit_price = ?, exit_time = ?, exit_reason = ?, pnl = ?, pnl_pct = ?,
			    fees = ?, net_pnl = ?, status = ?, updated_at = ?
			WHERE trade_id = ?
		`, nullFloat64(trade.ExitPrice), nullTime(trade.ExitTime), nullString(trade.ExitReason),
			nullFloat64(trade.PnL), nullFloat64(trade.PnLPct), trade.Fees, nullFloat64(trade.NetPnL),
			trade.Status, now, trade.ID)

		if err != nil {
			return fmt.Errorf("failed to update trade: %w", err)
		}

		r.log.WithField("trade_id", trade.ID).Debug("Trade updated")
	} else {
		// Insert new — include user_id attribution.
		// Exit columns are included so callers that record an already-closed trade
		// in a single Save (e.g. the AI bot, which has no insert-on-open step) keep
		// their exit_price/pnl/exit_reason. For trades inserted while still open the
		// null* helpers leave these NULL, matching the open-then-update flow.
		_, err = r.db.Exec(`
			INSERT INTO trade_history
			(trade_id, pair, exchange, side, trading_style, trading_style_type, primary_timeframe,
			 timeframes_used, entry_price, entry_time, size, leverage, stop_loss, take_profit,
			 exit_price, exit_time, exit_reason, pnl, pnl_pct, fees, net_pnl,
			 confidence, decision_reason, indicators_snapshot, status, created_at, updated_at, user_id)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		`, trade.ID, trade.Pair, trade.Exchange, trade.Side, trade.TradingStyle, trade.TradingStyleType,
			trade.PrimaryTimeframe, string(timeframesJSON), trade.EntryPrice, trade.EntryTime,
			trade.Size, trade.Leverage, nullFloat64(trade.StopLoss), nullFloat64(trade.TakeProfit),
			nullFloat64(trade.ExitPrice), nullTime(trade.ExitTime), nullString(trade.ExitReason),
			nullFloat64(trade.PnL), nullFloat64(trade.PnLPct), trade.Fees, nullFloat64(trade.NetPnL),
			trade.Confidence, nullString(trade.DecisionReason), string(indicatorsJSON),
			trade.Status, now, now, userID)

		if err != nil {
			return fmt.Errorf("failed to insert trade: %w", err)
		}

		r.log.WithField("trade_id", trade.ID).WithField("user_id", userID).Info("Trade saved")
	}

	return nil
}

// GetByID retrieves a trade by ID
func (r *TradeHistoryRepository) GetByID(tradeID string) (*Trade, error) {
	var record TradeRecord

	err := r.db.QueryRow(`
		SELECT id, trade_id, pair, exchange, side, trading_style, trading_style_type, 
		       primary_timeframe, timeframes_used, entry_price, entry_time, size, leverage,
		       exit_price, exit_time, exit_reason, stop_loss, take_profit, pnl, pnl_pct, 
		       fees, net_pnl, confidence, decision_reason, indicators_snapshot, status, 
		       created_at, updated_at
		FROM trade_history
		WHERE trade_id = ?
	`, tradeID).Scan(
		&record.ID, &record.TradeID, &record.Pair, &record.Exchange, &record.Side,
		&record.TradingStyle, &record.TradingStyleType, &record.PrimaryTimeframe,
		&record.TimeframesUsed, &record.EntryPrice, &record.EntryTime, &record.Size,
		&record.Leverage, &record.ExitPrice, &record.ExitTime, &record.ExitReason,
		&record.StopLoss, &record.TakeProfit, &record.PnL, &record.PnLPct,
		&record.Fees, &record.NetPnL, &record.Confidence, &record.DecisionReason,
		&record.IndicatorsSnapshot, &record.Status, &record.CreatedAt, &record.UpdatedAt,
	)

	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("trade not found: %s", tradeID)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to load trade: %w", err)
	}

	return recordToTrade(&record)
}

// GetByStyle returns trades filtered by trading style
func (r *TradeHistoryRepository) GetByStyle(style string) ([]*Trade, error) {
	return r.queryTrades(`
		SELECT id, trade_id, pair, exchange, side, trading_style, trading_style_type, 
		       primary_timeframe, timeframes_used, entry_price, entry_time, size, leverage,
		       exit_price, exit_time, exit_reason, stop_loss, take_profit, pnl, pnl_pct, 
		       fees, net_pnl, confidence, decision_reason, indicators_snapshot, status, 
		       created_at, updated_at
		FROM trade_history
		WHERE trading_style = ?
		ORDER BY entry_time DESC
	`, style)
}

// GetByTimeframe returns trades filtered by primary timeframe
func (r *TradeHistoryRepository) GetByTimeframe(timeframe string) ([]*Trade, error) {
	return r.queryTrades(`
		SELECT id, trade_id, pair, exchange, side, trading_style, trading_style_type, 
		       primary_timeframe, timeframes_used, entry_price, entry_time, size, leverage,
		       exit_price, exit_time, exit_reason, stop_loss, take_profit, pnl, pnl_pct, 
		       fees, net_pnl, confidence, decision_reason, indicators_snapshot, status, 
		       created_at, updated_at
		FROM trade_history
		WHERE primary_timeframe = ?
		ORDER BY entry_time DESC
	`, timeframe)
}

// GetByDateRange returns trades within a date range
func (r *TradeHistoryRepository) GetByDateRange(start, end time.Time) ([]*Trade, error) {
	return r.queryTrades(`
		SELECT id, trade_id, pair, exchange, side, trading_style, trading_style_type, 
		       primary_timeframe, timeframes_used, entry_price, entry_time, size, leverage,
		       exit_price, exit_time, exit_reason, stop_loss, take_profit, pnl, pnl_pct, 
		       fees, net_pnl, confidence, decision_reason, indicators_snapshot, status, 
		       created_at, updated_at
		FROM trade_history
		WHERE entry_time BETWEEN ? AND ?
		ORDER BY entry_time DESC
	`, start, end)
}

// GetOpenTrades returns all open trades
func (r *TradeHistoryRepository) GetOpenTrades() ([]*Trade, error) {
	return r.queryTrades(`
		SELECT id, trade_id, pair, exchange, side, trading_style, trading_style_type, 
		       primary_timeframe, timeframes_used, entry_price, entry_time, size, leverage,
		       exit_price, exit_time, exit_reason, stop_loss, take_profit, pnl, pnl_pct, 
		       fees, net_pnl, confidence, decision_reason, indicators_snapshot, status, 
		       created_at, updated_at
		FROM trade_history
		WHERE status = 'open'
		ORDER BY entry_time DESC
	`)
}

// GetClosedTrades returns all closed trades
func (r *TradeHistoryRepository) GetClosedTrades(limit int) ([]*Trade, error) {
	return r.queryTrades(`
		SELECT id, trade_id, pair, exchange, side, trading_style, trading_style_type,
		       primary_timeframe, timeframes_used, entry_price, entry_time, size, leverage,
		       exit_price, exit_time, exit_reason, stop_loss, take_profit, pnl, pnl_pct,
		       fees, net_pnl, confidence, decision_reason, indicators_snapshot, status,
		       created_at, updated_at
		FROM trade_history
		WHERE status = 'closed'
		ORDER BY exit_time DESC
		LIMIT ?
	`, limit)
}

// GetByPair returns the most recent closed trades for a specific pair, ordered
// by exit_time descending. Open positions (NULL exit_time) are excluded by the
// status filter. If limit <= 0, a sane default is applied.
func (r *TradeHistoryRepository) GetByPair(pair string, limit int) ([]*Trade, error) {
	if limit <= 0 {
		limit = 200
	}
	return r.queryTrades(`
		SELECT id, trade_id, pair, exchange, side, trading_style, trading_style_type,
		       primary_timeframe, timeframes_used, entry_price, entry_time, size, leverage,
		       exit_price, exit_time, exit_reason, stop_loss, take_profit, pnl, pnl_pct,
		       fees, net_pnl, confidence, decision_reason, indicators_snapshot, status,
		       created_at, updated_at
		FROM trade_history
		WHERE pair = ? AND status = 'closed' AND exit_time IS NOT NULL
		ORDER BY exit_time DESC
		LIMIT ?
	`, pair, limit)
}

// GetDistinctPairs returns the set of distinct pairs that have at least one
// closed trade. Useful for feedback aggregation across the whole trade history.
func (r *TradeHistoryRepository) GetDistinctPairs() ([]string, error) {
	rows, err := r.db.Query(`
		SELECT DISTINCT pair
		FROM trade_history
		WHERE status = 'closed' AND pair IS NOT NULL AND pair <> ''
		ORDER BY pair ASC
	`)
	if err != nil {
		return nil, fmt.Errorf("failed to query distinct pairs: %w", err)
	}
	defer rows.Close()

	pairs := make([]string, 0)
	for rows.Next() {
		var pair string
		if err := rows.Scan(&pair); err != nil {
			return nil, fmt.Errorf("failed to scan pair: %w", err)
		}
		pairs = append(pairs, pair)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating pair rows: %w", err)
	}
	return pairs, nil
}

// GetOpenTradesForUser returns all open trades belonging to a specific user.
func (r *TradeHistoryRepository) GetOpenTradesForUser(userID string) ([]*Trade, error) {
	if userID == "" {
		userID = "default"
	}
	return r.queryTrades(`
		SELECT id, trade_id, pair, exchange, side, trading_style, trading_style_type,
		       primary_timeframe, timeframes_used, entry_price, entry_time, size, leverage,
		       exit_price, exit_time, exit_reason, stop_loss, take_profit, pnl, pnl_pct,
		       fees, net_pnl, confidence, decision_reason, indicators_snapshot, status,
		       created_at, updated_at
		FROM trade_history
		WHERE status = 'open' AND user_id = ?
		ORDER BY entry_time DESC
	`, userID)
}

// GetClosedTradesForUser returns the most recent closed trades for a specific user.
func (r *TradeHistoryRepository) GetClosedTradesForUser(userID string, limit int) ([]*Trade, error) {
	if userID == "" {
		userID = "default"
	}
	return r.queryTrades(`
		SELECT id, trade_id, pair, exchange, side, trading_style, trading_style_type,
		       primary_timeframe, timeframes_used, entry_price, entry_time, size, leverage,
		       exit_price, exit_time, exit_reason, stop_loss, take_profit, pnl, pnl_pct,
		       fees, net_pnl, confidence, decision_reason, indicators_snapshot, status,
		       created_at, updated_at
		FROM trade_history
		WHERE status = 'closed' AND user_id = ?
		ORDER BY exit_time DESC
		LIMIT ?
	`, userID, limit)
}

// GetByPairForUser returns closed trades for a given pair attributed to a specific user.
func (r *TradeHistoryRepository) GetByPairForUser(userID, pair string, limit int) ([]*Trade, error) {
	if userID == "" {
		userID = "default"
	}
	if limit <= 0 {
		limit = 200
	}
	return r.queryTrades(`
		SELECT id, trade_id, pair, exchange, side, trading_style, trading_style_type,
		       primary_timeframe, timeframes_used, entry_price, entry_time, size, leverage,
		       exit_price, exit_time, exit_reason, stop_loss, take_profit, pnl, pnl_pct,
		       fees, net_pnl, confidence, decision_reason, indicators_snapshot, status,
		       created_at, updated_at
		FROM trade_history
		WHERE pair = ? AND user_id = ? AND status = 'closed' AND exit_time IS NOT NULL
		ORDER BY exit_time DESC
		LIMIT ?
	`, pair, userID, limit)
}

// GetByDateRangeForUser returns trades within a date range for a specific user.
func (r *TradeHistoryRepository) GetByDateRangeForUser(userID string, start, end time.Time) ([]*Trade, error) {
	if userID == "" {
		userID = "default"
	}
	return r.queryTrades(`
		SELECT id, trade_id, pair, exchange, side, trading_style, trading_style_type,
		       primary_timeframe, timeframes_used, entry_price, entry_time, size, leverage,
		       exit_price, exit_time, exit_reason, stop_loss, take_profit, pnl, pnl_pct,
		       fees, net_pnl, confidence, decision_reason, indicators_snapshot, status,
		       created_at, updated_at
		FROM trade_history
		WHERE entry_time BETWEEN ? AND ? AND user_id = ?
		ORDER BY entry_time DESC
	`, start, end, userID)
}

// queryTrades is a helper to execute trade queries
func (r *TradeHistoryRepository) queryTrades(query string, args ...interface{}) ([]*Trade, error) {
	rows, err := r.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to query trades: %w", err)
	}
	defer rows.Close()

	var trades []*Trade

	for rows.Next() {
		var record TradeRecord
		err := rows.Scan(
			&record.ID, &record.TradeID, &record.Pair, &record.Exchange, &record.Side,
			&record.TradingStyle, &record.TradingStyleType, &record.PrimaryTimeframe,
			&record.TimeframesUsed, &record.EntryPrice, &record.EntryTime, &record.Size,
			&record.Leverage, &record.ExitPrice, &record.ExitTime, &record.ExitReason,
			&record.StopLoss, &record.TakeProfit, &record.PnL, &record.PnLPct,
			&record.Fees, &record.NetPnL, &record.Confidence, &record.DecisionReason,
			&record.IndicatorsSnapshot, &record.Status, &record.CreatedAt, &record.UpdatedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan row: %w", err)
		}

		trade, err := recordToTrade(&record)
		if err != nil {
			r.log.WithError(err).WithField("trade_id", record.TradeID).Warn("Failed to convert record, skipping")
			continue
		}

		trades = append(trades, trade)
	}

	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating rows: %w", err)
	}

	return trades, nil
}

// recordToTrade converts a database record to a Trade
func recordToTrade(record *TradeRecord) (*Trade, error) {
	trade := &Trade{
		ID:               record.TradeID,
		Pair:             record.Pair,
		Exchange:         record.Exchange,
		Side:             record.Side,
		TradingStyle:     record.TradingStyle,
		TradingStyleType: record.TradingStyleType,
		PrimaryTimeframe: record.PrimaryTimeframe,
		EntryPrice:       record.EntryPrice,
		EntryTime:        record.EntryTime,
		Size:             record.Size,
		Leverage:         record.Leverage,
		Confidence:       record.Confidence,
		Status:           record.Status,
		Fees:             record.Fees,
		CreatedAt:        record.CreatedAt,
		UpdatedAt:        record.UpdatedAt,
	}

	// Deserialize timeframes
	if err := json.Unmarshal([]byte(record.TimeframesUsed), &trade.TimeframesUsed); err != nil {
		return nil, fmt.Errorf("failed to unmarshal timeframes: %w", err)
	}

	// Deserialize indicators
	if record.IndicatorsSnapshot != nil && *record.IndicatorsSnapshot != "" {
		if err := json.Unmarshal([]byte(*record.IndicatorsSnapshot), &trade.IndicatorsSnapshot); err != nil {
			return nil, fmt.Errorf("failed to unmarshal indicators: %w", err)
		}
	}

	// Handle nullable fields
	if record.ExitPrice != nil {
		trade.ExitPrice = *record.ExitPrice
	}
	if record.ExitTime != nil {
		trade.ExitTime = *record.ExitTime
	}
	if record.ExitReason != nil {
		trade.ExitReason = *record.ExitReason
	}
	if record.StopLoss != nil {
		trade.StopLoss = *record.StopLoss
	}
	if record.TakeProfit != nil {
		trade.TakeProfit = *record.TakeProfit
	}
	if record.PnL != nil {
		trade.PnL = *record.PnL
	}
	if record.PnLPct != nil {
		trade.PnLPct = *record.PnLPct
	}
	if record.NetPnL != nil {
		trade.NetPnL = *record.NetPnL
	}
	if record.DecisionReason != nil {
		trade.DecisionReason = *record.DecisionReason
	}

	return trade, nil
}

// Helper functions for nullable values
func nullFloat64(v float64) *float64 {
	if v == 0 {
		return nil
	}
	return &v
}

func nullString(v string) *string {
	if v == "" {
		return nil
	}
	return &v
}

func nullTime(v time.Time) *time.Time {
	if v.IsZero() {
		return nil
	}
	return &v
}

// GetRecentClosedTradesSince returns closed trades for a user since a given time (SEC-10).
func (r *TradeHistoryRepository) GetRecentClosedTradesSince(userID string, since time.Time) ([]*Trade, error) {
	if userID == "" {
		userID = "default"
	}
	return r.queryTrades(`
		SELECT id, trade_id, pair, exchange, side, trading_style, trading_style_type,
		       primary_timeframe, timeframes_used, entry_price, entry_time, size, leverage,
		       exit_price, exit_time, exit_reason, stop_loss, take_profit, pnl, pnl_pct,
		       fees, net_pnl, confidence, decision_reason, indicators_snapshot, status,
		       created_at, updated_at
		FROM trade_history
		WHERE status = 'closed' AND user_id = ? AND exit_time >= ?
		ORDER BY exit_time DESC
	`, userID, since)
}

// MarkExternalClose records an exchange-sourced close for a trade (SEC-10).
// Called when exchange reports the position is closed but local DB shows it open.
func (r *TradeHistoryRepository) MarkExternalClose(tradeID string, exitPrice float64, exitTime time.Time) error {
	_, err := r.db.Exec(`
		UPDATE trade_history
		   SET exit_price  = ?,
		       exit_time   = ?,
		       exit_reason = 'exchange_reconciled_external_close',
		       status      = 'closed',
		       updated_at  = ?
		 WHERE trade_id = ? AND status = 'open'
	`, nullFloat64(exitPrice), nullTime(exitTime), time.Now(), tradeID)
	if err != nil {
		return fmt.Errorf("MarkExternalClose(%s): %w", tradeID, err)
	}
	return nil
}

// UpdateTradeFees overwrites fees and net_pnl for a trade (SEC-10 fee reconciliation).
func (r *TradeHistoryRepository) UpdateTradeFees(tradeID string, fees, netPnL float64) error {
	_, err := r.db.Exec(`
		UPDATE trade_history
		   SET fees       = ?,
		       net_pnl    = ?,
		       updated_at = ?
		 WHERE trade_id = ?
	`, fees, nullFloat64(netPnL), time.Now(), tradeID)
	if err != nil {
		return fmt.Errorf("UpdateTradeFees(%s): %w", tradeID, err)
	}
	return nil
}

// GetTodayNetLossForUser returns the sum of net_pnl for losing trades closed today (local day).
// Returns a negative value (or 0 if no losses today). Used to seed dailyLoss on bot restart
// so the daily loss cap is not reset by a process restart.
func (r *TradeHistoryRepository) GetTodayNetLossForUser(userID string) (float64, error) {
	// When userID is empty (legacy single-bot mode) match any user_id since
	// SaveForUser stores "default" for the global singleton.
	query := `
		SELECT COALESCE(SUM(net_pnl), 0)
		FROM trade_history
		WHERE net_pnl < 0
		  AND status = 'closed'
		  AND exit_time >= date('now', 'localtime', 'start of day')
		  AND (? = '' OR user_id = ?)`
	var sum float64
	err := r.db.QueryRow(query, userID, userID).Scan(&sum)
	if err != nil {
		return 0, fmt.Errorf("GetTodayNetLossForUser: %w", err)
	}
	return sum, nil
}

// GetTrailingConsecutiveLosses returns, for each pair traded in the last 24h,
// the count of consecutive losing closes ending at that pair's most recent
// close (a win/breakeven seals the streak at 0 and the pair is omitted). Used
// to seed the auto-pause counter on restart so a deploy mid-streak doesn't
// silently reset it. Phantom rows (|pnl_pct| >= 99) are excluded.
func (r *TradeHistoryRepository) GetTrailingConsecutiveLosses(userID string) (map[string]int, error) {
	rows, err := r.db.Query(`
		SELECT pair, pnl
		FROM trade_history
		WHERE status = 'closed'
		  AND exit_time >= datetime('now', '-1 day')
		  AND abs(pnl_pct) < 99
		  AND (? = '' OR user_id = ?)
		ORDER BY exit_time DESC`, userID, userID)
	if err != nil {
		return nil, fmt.Errorf("GetTrailingConsecutiveLosses: %w", err)
	}
	defer rows.Close()

	streaks := make(map[string]int)
	sealed := make(map[string]bool)
	for rows.Next() {
		var pair string
		var pnl float64
		if err := rows.Scan(&pair, &pnl); err != nil {
			return nil, fmt.Errorf("GetTrailingConsecutiveLosses scan: %w", err)
		}
		if sealed[pair] {
			continue
		}
		if pnl < 0 {
			streaks[pair]++
		} else {
			sealed[pair] = true
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("GetTrailingConsecutiveLosses rows: %w", err)
	}
	return streaks, nil
}

// GlobalTailLossStreak returns the count of consecutive non-winning closes
// ending at the account's most recent close ACROSS ALL PAIRS, plus that
// close's exit time. A net-positive close seals the streak at 0. This is
// deliberately global, not per-pair: losses cluster in account-wide time
// windows (a choppy market hits every pair at once), which the per-pair
// streaks in GetTrailingConsecutiveLosses cannot see. Reading trade_history
// on demand keeps the state restart-proof and shared across fleet bots with
// nothing to seed. Phantom rows (|pnl_pct| >= 99) are excluded.
func (r *TradeHistoryRepository) GlobalTailLossStreak(userID string) (int, time.Time, error) {
	rows, err := r.db.Query(`
		SELECT COALESCE(net_pnl, pnl, 0), exit_time
		FROM trade_history
		WHERE status = 'closed'
		  AND exit_time IS NOT NULL
		  AND exit_time >= datetime('now', '-2 day')
		  AND abs(COALESCE(pnl_pct, 0)) < 99
		  AND (? = '' OR user_id = ?)
		ORDER BY exit_time DESC
		LIMIT 100`, userID, userID)
	if err != nil {
		return 0, time.Time{}, fmt.Errorf("GlobalTailLossStreak: %w", err)
	}
	defer rows.Close()

	streak := 0
	var lastExit time.Time
	for rows.Next() {
		var pnl float64
		var exitTime time.Time
		if err := rows.Scan(&pnl, &exitTime); err != nil {
			return 0, time.Time{}, fmt.Errorf("GlobalTailLossStreak scan: %w", err)
		}
		if lastExit.IsZero() {
			lastExit = exitTime
		}
		if pnl > 0 {
			break
		}
		streak++
	}
	if err := rows.Err(); err != nil {
		return 0, time.Time{}, fmt.Errorf("GlobalTailLossStreak rows: %w", err)
	}
	return streak, lastExit, nil
}
