package storage

import (
	"database/sql"
	"fmt"
	"time"

	"github.com/sirupsen/logrus"
)

// PerformanceMetricsRepository handles persistence of performance metrics
type PerformanceMetricsRepository struct {
	db  *sql.DB
	log *logrus.Logger
}

// NewPerformanceMetricsRepository creates a new performance metrics repository
func NewPerformanceMetricsRepository(db *sql.DB, log *logrus.Logger) *PerformanceMetricsRepository {
	return &PerformanceMetricsRepository{
		db:  db,
		log: log,
	}
}

// SaveMetrics persists performance metrics attributed to the "default" user.
// Callers operating in a multi-user context should use SaveMetricsForUser instead.
func (r *PerformanceMetricsRepository) SaveMetrics(style, timeframe string, metrics *PerformanceMetrics) error {
	return r.SaveMetricsForUser("default", style, timeframe, metrics)
}

// SaveMetricsForUser persists performance metrics attributed to the given userID.
// On INSERT the user_id column is written; on UPDATE it is left unchanged.
func (r *PerformanceMetricsRepository) SaveMetricsForUser(userID, style, timeframe string, metrics *PerformanceMetrics) error {
	if userID == "" {
		userID = "default"
	}

	// Check if exists (scoped to user_id so different users have independent metric rows)
	var exists bool
	err := r.db.QueryRow(`
		SELECT EXISTS(
			SELECT 1 FROM performance_metrics
			WHERE user_id = ? AND trading_style = ? AND timeframe = ? AND period_start = ? AND period_end = ?
		)
	`, userID, style, nullString(timeframe), metrics.PeriodStart, metrics.PeriodEnd).Scan(&exists)

	if err != nil {
		return fmt.Errorf("failed to check if metrics exist: %w", err)
	}

	now := time.Now()

	if exists {
		// Update existing — user_id is not updated (immutable ownership)
		_, err = r.db.Exec(`
			UPDATE performance_metrics
			SET total_trades = ?, winning_trades = ?, losing_trades = ?, win_rate = ?,
			    profit_factor = ?, avg_win = ?, avg_loss = ?, largest_win = ?, largest_loss = ?,
			    max_drawdown = ?, max_drawdown_pct = ?, sharpe_ratio = ?, total_pnl = ?,
			    total_pnl_pct = ?, total_fees = ?, net_pnl = ?, avg_confidence = ?,
			    min_confidence = ?, max_confidence = ?, calculated_at = ?
			WHERE user_id = ? AND trading_style = ? AND timeframe = ? AND period_start = ? AND period_end = ?
		`, metrics.TotalTrades, metrics.WinningTrades, metrics.LosingTrades, metrics.WinRate,
			metrics.ProfitFactor, metrics.AvgWin, metrics.AvgLoss, metrics.LargestWin, metrics.LargestLoss,
			metrics.MaxDrawdown, metrics.MaxDrawdownPct, metrics.SharpeRatio, metrics.TotalPnL,
			metrics.TotalPnLPct, metrics.TotalFees, metrics.NetPnL, metrics.AvgConfidence,
			metrics.MinConfidence, metrics.MaxConfidence, now,
			userID, style, nullString(timeframe), metrics.PeriodStart, metrics.PeriodEnd)

		if err != nil {
			return fmt.Errorf("failed to update metrics: %w", err)
		}

		r.log.WithFields(logrus.Fields{
			"style":     style,
			"timeframe": timeframe,
			"user_id":   userID,
		}).Debug("Metrics updated")
	} else {
		// Insert new — include user_id attribution
		_, err = r.db.Exec(`
			INSERT INTO performance_metrics
			(trading_style, trading_style_type, timeframe, pair, period_start, period_end,
			 total_trades, winning_trades, losing_trades, win_rate, profit_factor, avg_win, avg_loss,
			 largest_win, largest_loss, max_drawdown, max_drawdown_pct, sharpe_ratio, total_pnl,
			 total_pnl_pct, total_fees, net_pnl, avg_confidence, min_confidence, max_confidence,
			 calculated_at, user_id)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		`, style, metrics.TradingStyleType, nullString(timeframe), nullString(metrics.Pair),
			metrics.PeriodStart, metrics.PeriodEnd, metrics.TotalTrades, metrics.WinningTrades,
			metrics.LosingTrades, metrics.WinRate, metrics.ProfitFactor, metrics.AvgWin, metrics.AvgLoss,
			metrics.LargestWin, metrics.LargestLoss, metrics.MaxDrawdown, metrics.MaxDrawdownPct,
			metrics.SharpeRatio, metrics.TotalPnL, metrics.TotalPnLPct, metrics.TotalFees, metrics.NetPnL,
			metrics.AvgConfidence, metrics.MinConfidence, metrics.MaxConfidence, now, userID)

		if err != nil {
			return fmt.Errorf("failed to insert metrics: %w", err)
		}

		r.log.WithFields(logrus.Fields{
			"style":     style,
			"timeframe": timeframe,
			"user_id":   userID,
		}).Info("Metrics saved")
	}

	return nil
}

// LoadMetrics retrieves the most recent metrics for a specific style and timeframe
// for the "default" user. Use LoadMetricsForUser for multi-user contexts.
func (r *PerformanceMetricsRepository) LoadMetrics(style, timeframe string) (*PerformanceMetrics, error) {
	return r.LoadMetricsForUser("default", style, timeframe)
}

// LoadMetricsForUser retrieves metrics for a specific user, style, and timeframe.
func (r *PerformanceMetricsRepository) LoadMetricsForUser(userID, style, timeframe string) (*PerformanceMetrics, error) {
	if userID == "" {
		userID = "default"
	}

	var record PerformanceMetricsRecord

	err := r.db.QueryRow(`
		SELECT id, trading_style, trading_style_type, timeframe, pair, period_start, period_end,
		       total_trades, winning_trades, losing_trades, win_rate, profit_factor, avg_win, avg_loss,
		       largest_win, largest_loss, max_drawdown, max_drawdown_pct, sharpe_ratio, total_pnl,
		       total_pnl_pct, total_fees, net_pnl, avg_confidence, min_confidence, max_confidence, calculated_at
		FROM performance_metrics
		WHERE user_id = ? AND trading_style = ? AND timeframe = ?
		ORDER BY period_end DESC
		LIMIT 1
	`, userID, style, nullString(timeframe)).Scan(
		&record.ID, &record.TradingStyle, &record.TradingStyleType, &record.Timeframe, &record.Pair,
		&record.PeriodStart, &record.PeriodEnd, &record.TotalTrades, &record.WinningTrades,
		&record.LosingTrades, &record.WinRate, &record.ProfitFactor, &record.AvgWin, &record.AvgLoss,
		&record.LargestWin, &record.LargestLoss, &record.MaxDrawdown, &record.MaxDrawdownPct,
		&record.SharpeRatio, &record.TotalPnL, &record.TotalPnLPct, &record.TotalFees, &record.NetPnL,
		&record.AvgConfidence, &record.MinConfidence, &record.MaxConfidence, &record.CalculatedAt,
	)

	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("metrics not found for user: %s, style: %s, timeframe: %s", userID, style, timeframe)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to load metrics: %w", err)
	}

	return recordToMetrics(&record), nil
}

// GetAggregated returns aggregated metrics for a trading style (across all timeframes)
func (r *PerformanceMetricsRepository) GetAggregated(style string) (*PerformanceMetrics, error) {
	var record PerformanceMetricsRecord

	err := r.db.QueryRow(`
		SELECT id, trading_style, trading_style_type, timeframe, pair, period_start, period_end,
		       total_trades, winning_trades, losing_trades, win_rate, profit_factor, avg_win, avg_loss,
		       largest_win, largest_loss, max_drawdown, max_drawdown_pct, sharpe_ratio, total_pnl,
		       total_pnl_pct, total_fees, net_pnl, avg_confidence, min_confidence, max_confidence, calculated_at
		FROM performance_metrics
		WHERE trading_style = ? AND timeframe IS NULL
		ORDER BY period_end DESC
		LIMIT 1
	`, style).Scan(
		&record.ID, &record.TradingStyle, &record.TradingStyleType, &record.Timeframe, &record.Pair,
		&record.PeriodStart, &record.PeriodEnd, &record.TotalTrades, &record.WinningTrades,
		&record.LosingTrades, &record.WinRate, &record.ProfitFactor, &record.AvgWin, &record.AvgLoss,
		&record.LargestWin, &record.LargestLoss, &record.MaxDrawdown, &record.MaxDrawdownPct,
		&record.SharpeRatio, &record.TotalPnL, &record.TotalPnLPct, &record.TotalFees, &record.NetPnL,
		&record.AvgConfidence, &record.MinConfidence, &record.MaxConfidence, &record.CalculatedAt,
	)

	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("aggregated metrics not found for style: %s", style)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to load aggregated metrics: %w", err)
	}

	return recordToMetrics(&record), nil
}

// GetByStyleType returns metrics for all styles of a specific type
func (r *PerformanceMetricsRepository) GetByStyleType(styleType string) ([]*PerformanceMetrics, error) {
	rows, err := r.db.Query(`
		SELECT id, trading_style, trading_style_type, timeframe, pair, period_start, period_end,
		       total_trades, winning_trades, losing_trades, win_rate, profit_factor, avg_win, avg_loss,
		       largest_win, largest_loss, max_drawdown, max_drawdown_pct, sharpe_ratio, total_pnl,
		       total_pnl_pct, total_fees, net_pnl, avg_confidence, min_confidence, max_confidence, calculated_at
		FROM performance_metrics
		WHERE trading_style_type = ?
		ORDER BY period_end DESC
	`, styleType)

	if err != nil {
		return nil, fmt.Errorf("failed to query metrics by type: %w", err)
	}
	defer rows.Close()

	return r.scanMetricsRows(rows)
}

// GetByPeriod returns metrics for a specific time period
func (r *PerformanceMetricsRepository) GetByPeriod(start, end time.Time) ([]*PerformanceMetrics, error) {
	rows, err := r.db.Query(`
		SELECT id, trading_style, trading_style_type, timeframe, pair, period_start, period_end,
		       total_trades, winning_trades, losing_trades, win_rate, profit_factor, avg_win, avg_loss,
		       largest_win, largest_loss, max_drawdown, max_drawdown_pct, sharpe_ratio, total_pnl,
		       total_pnl_pct, total_fees, net_pnl, avg_confidence, min_confidence, max_confidence, calculated_at
		FROM performance_metrics
		WHERE period_start >= ? AND period_end <= ?
		ORDER BY period_end DESC
	`, start, end)

	if err != nil {
		return nil, fmt.Errorf("failed to query metrics by period: %w", err)
	}
	defer rows.Close()

	return r.scanMetricsRows(rows)
}

// CalculateMetricsFromTrades calculates metrics from trade history for the "default" user.
// Use CalculateMetricsFromTradesForUser in multi-user contexts.
func (r *PerformanceMetricsRepository) CalculateMetricsFromTrades(
	style, timeframe string,
	start, end time.Time,
) (*PerformanceMetrics, error) {
	return r.CalculateMetricsFromTradesForUser("default", style, timeframe, start, end)
}

// CalculateMetricsFromTradesForUser calculates metrics from trade history scoped to a specific user.
func (r *PerformanceMetricsRepository) CalculateMetricsFromTradesForUser(
	userID, style, timeframe string,
	start, end time.Time,
) (*PerformanceMetrics, error) {
	if userID == "" {
		userID = "default"
	}

	var metrics PerformanceMetrics
	var styleType string

	err := r.db.QueryRow(`
		SELECT
			trading_style_type,
			COUNT(*) as total_trades,
			SUM(CASE WHEN pnl > 0 THEN 1 ELSE 0 END) as winning_trades,
			SUM(CASE WHEN pnl < 0 THEN 1 ELSE 0 END) as losing_trades,
			AVG(CASE WHEN pnl > 0 THEN pnl ELSE 0 END) as avg_win,
			AVG(CASE WHEN pnl < 0 THEN ABS(pnl) ELSE 0 END) as avg_loss,
			MAX(pnl) as largest_win,
			MIN(pnl) as largest_loss,
			SUM(pnl) as total_pnl,
			SUM(fees) as total_fees,
			AVG(confidence) as avg_confidence,
			MIN(confidence) as min_confidence,
			MAX(confidence) as max_confidence
		FROM trade_history
		WHERE trading_style = ?
		  AND (? IS NULL OR primary_timeframe = ?)
		  AND entry_time BETWEEN ? AND ?
		  AND status = 'closed'
		  AND user_id = ?
	`, style, nullString(timeframe), nullString(timeframe), start, end, userID).Scan(
		&styleType, &metrics.TotalTrades, &metrics.WinningTrades, &metrics.LosingTrades,
		&metrics.AvgWin, &metrics.AvgLoss, &metrics.LargestWin, &metrics.LargestLoss,
		&metrics.TotalPnL, &metrics.TotalFees, &metrics.AvgConfidence,
		&metrics.MinConfidence, &metrics.MaxConfidence,
	)

	if err != nil {
		return nil, fmt.Errorf("failed to calculate metrics: %w", err)
	}

	// Calculate derived metrics
	metrics.TradingStyle = style
	metrics.TradingStyleType = styleType
	metrics.Timeframe = timeframe
	metrics.PeriodStart = start
	metrics.PeriodEnd = end
	metrics.NetPnL = metrics.TotalPnL - metrics.TotalFees

	if metrics.TotalTrades > 0 {
		metrics.WinRate = float64(metrics.WinningTrades) / float64(metrics.TotalTrades)
	}

	if metrics.AvgLoss > 0 {
		metrics.ProfitFactor = metrics.AvgWin / metrics.AvgLoss
	}

	metrics.CalculatedAt = time.Now()

	return &metrics, nil
}

// scanMetricsRows is a helper to scan multiple metrics rows
func (r *PerformanceMetricsRepository) scanMetricsRows(rows *sql.Rows) ([]*PerformanceMetrics, error) {
	var metricsList []*PerformanceMetrics

	for rows.Next() {
		var record PerformanceMetricsRecord
		err := rows.Scan(
			&record.ID, &record.TradingStyle, &record.TradingStyleType, &record.Timeframe, &record.Pair,
			&record.PeriodStart, &record.PeriodEnd, &record.TotalTrades, &record.WinningTrades,
			&record.LosingTrades, &record.WinRate, &record.ProfitFactor, &record.AvgWin, &record.AvgLoss,
			&record.LargestWin, &record.LargestLoss, &record.MaxDrawdown, &record.MaxDrawdownPct,
			&record.SharpeRatio, &record.TotalPnL, &record.TotalPnLPct, &record.TotalFees, &record.NetPnL,
			&record.AvgConfidence, &record.MinConfidence, &record.MaxConfidence, &record.CalculatedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan row: %w", err)
		}

		metricsList = append(metricsList, recordToMetrics(&record))
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating rows: %w", err)
	}

	return metricsList, nil
}

// recordToMetrics converts a database record to PerformanceMetrics
func recordToMetrics(record *PerformanceMetricsRecord) *PerformanceMetrics {
	metrics := &PerformanceMetrics{
		TradingStyle:     record.TradingStyle,
		TradingStyleType: record.TradingStyleType,
		PeriodStart:      record.PeriodStart,
		PeriodEnd:        record.PeriodEnd,
		TotalTrades:      record.TotalTrades,
		WinningTrades:    record.WinningTrades,
		LosingTrades:     record.LosingTrades,
		WinRate:          record.WinRate,
		ProfitFactor:     record.ProfitFactor,
		AvgWin:           record.AvgWin,
		AvgLoss:          record.AvgLoss,
		LargestWin:       record.LargestWin,
		LargestLoss:      record.LargestLoss,
		MaxDrawdown:      record.MaxDrawdown,
		MaxDrawdownPct:   record.MaxDrawdownPct,
		SharpeRatio:      record.SharpeRatio,
		TotalPnL:         record.TotalPnL,
		TotalPnLPct:      record.TotalPnLPct,
		TotalFees:        record.TotalFees,
		NetPnL:           record.NetPnL,
		AvgConfidence:    record.AvgConfidence,
		MinConfidence:    record.MinConfidence,
		MaxConfidence:    record.MaxConfidence,
		CalculatedAt:     record.CalculatedAt,
	}

	if record.Timeframe != nil {
		metrics.Timeframe = *record.Timeframe
	}
	if record.Pair != nil {
		metrics.Pair = *record.Pair
	}

	return metrics
}
