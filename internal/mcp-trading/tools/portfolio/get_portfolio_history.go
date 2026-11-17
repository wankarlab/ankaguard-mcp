package portfolio

import (
	"context"
	"fmt"
	"math"
	"time"

	"github.com/rantipay/trading-mcp/internal/mcp-trading/gctclient"
	"github.com/rantipay/trading-mcp/internal/mcp-trading/models"
	"github.com/rantipay/trading-mcp/internal/storage"
)

// GetPortfolioHistoryTool provides historical portfolio value data from SQLite snapshots
type GetPortfolioHistoryTool struct {
	client *gctclient.Client
	repo   *storage.PortfolioSnapshotRepository // nil until storage is ready
}

// NewGetPortfolioHistoryTool creates a new instance
func NewGetPortfolioHistoryTool(client *gctclient.Client, repo *storage.PortfolioSnapshotRepository) *GetPortfolioHistoryTool {
	return &GetPortfolioHistoryTool{client: client, repo: repo}
}

func (t *GetPortfolioHistoryTool) Name() string {
	return "get_portfolio_history"
}

func (t *GetPortfolioHistoryTool) Description() string {
	return "Get historical portfolio value data for performance charts and analysis"
}

func (t *GetPortfolioHistoryTool) InputSchema() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"period": map[string]interface{}{
				"type":        "string",
				"description": "Time period for history (1d, 7d, 30d, 90d, 1y)",
				"enum":        []string{"1d", "7d", "30d", "90d", "1y"},
			},
			"include_metrics": map[string]interface{}{
				"type":        "boolean",
				"description": "Include performance metrics",
			},
			"exchange": map[string]interface{}{
				"type":        "string",
				"description": "Filter by exchange (optional)",
			},
		},
	}
}

type PortfolioSnapshot struct {
	Timestamp  string             `json:"timestamp"`
	TotalValue float64            `json:"total_value"`
	PnL        float64            `json:"pnl"`
	PnLPercent float64            `json:"pnl_percent"`
	Assets     map[string]float64 `json:"assets"`
}

type DayMetrics struct {
	Date          string  `json:"date"`
	Return        float64 `json:"return"`
	ReturnPercent float64 `json:"return_percent"`
}

type PerformanceMetrics struct {
	TotalReturn        float64     `json:"total_return"`
	TotalReturnPercent float64     `json:"total_return_percent"`
	MaxDrawdown        float64     `json:"max_drawdown"`
	MaxDrawdownPercent float64     `json:"max_drawdown_percent"`
	Volatility         float64     `json:"volatility"`
	SharpeRatio        float64     `json:"sharpe_ratio"`
	BestDay            *DayMetrics `json:"best_day"`
	WorstDay           *DayMetrics `json:"worst_day"`
}

type PortfolioHistoryOutput struct {
	History    []PortfolioSnapshot `json:"history"`
	Metrics    *PerformanceMetrics `json:"metrics"`
	Period     string              `json:"period"`
	DataPoints int                 `json:"data_points"`
	Message    string              `json:"message,omitempty"`
	Success    bool                `json:"success"`
}

func (t *GetPortfolioHistoryTool) Execute(ctx context.Context, params map[string]interface{}) (interface{}, error) {
	period := "7d"
	if p, ok := params["period"].(string); ok && p != "" {
		period = p
	}

	includeMetrics := true
	if im, ok := params["include_metrics"].(bool); ok {
		includeMetrics = im
	}

	duration, _ := parsePeriod(period)
	if duration == 0 {
		return nil, &models.MCPError{
			Code:    -32602,
			Message: "Invalid params",
			Data:    fmt.Sprintf("invalid period: %s", period),
		}
	}

	// No snapshots repository means tracking hasn't started yet
	if t.repo == nil {
		return &PortfolioHistoryOutput{
			History:    []PortfolioSnapshot{},
			Period:     period,
			DataPoints: 0,
			Message:    "Portfolio history tracking not initialized",
			Success:    true,
		}, nil
	}

	from := time.Now().Add(-duration)
	to := time.Now()

	rawSnapshots, err := t.repo.GetByTimeRange(from, to)
	if err != nil {
		return nil, fmt.Errorf("failed to query portfolio snapshots: %w", err)
	}

	// Convert to output format
	history := make([]PortfolioSnapshot, 0, len(rawSnapshots))
	for _, s := range rawSnapshots {
		history = append(history, PortfolioSnapshot{
			Timestamp:  s.SnapshotTime.UTC().Format(time.RFC3339),
			TotalValue: math.Round(s.TotalValueUSDT*100) / 100,
			PnL:        math.Round(s.UnrealizedPnL*100) / 100,
			PnLPercent: 0, // calculated below if initial value available
			Assets:     s.Assets,
		})
	}

	// Calculate PnL% relative to first snapshot
	if len(history) > 0 {
		initialValue := history[0].TotalValue
		if initialValue > 0 {
			for i := range history {
				pnl := history[i].TotalValue - initialValue
				history[i].PnL = math.Round(pnl*100) / 100
				history[i].PnLPercent = math.Round((pnl/initialValue)*10000) / 100
			}
		}
	}

	var metrics *PerformanceMetrics
	if includeMetrics && len(history) > 0 {
		metrics = calculatePortfolioMetrics(history)
	}

	output := &PortfolioHistoryOutput{
		History:    history,
		Metrics:    metrics,
		Period:     period,
		DataPoints: len(history),
		Success:    true,
	}

	if len(history) == 0 {
		output.Message = "No historical data yet — snapshots accumulate hourly after first startup"
	}

	return output, nil
}

func parsePeriod(period string) (time.Duration, int) {
	switch period {
	case "1d":
		return 24 * time.Hour, 24
	case "7d":
		return 7 * 24 * time.Hour, 168
	case "30d":
		return 30 * 24 * time.Hour, 30
	case "90d":
		return 90 * 24 * time.Hour, 90
	case "1y":
		return 365 * 24 * time.Hour, 365
	default:
		return 0, 0
	}
}

func calculatePortfolioMetrics(history []PortfolioSnapshot) *PerformanceMetrics {
	if len(history) == 0 {
		return nil
	}

	initialValue := history[0].TotalValue
	finalValue := history[len(history)-1].TotalValue

	totalReturn := finalValue - initialValue
	totalReturnPercent := 0.0
	if initialValue > 0 {
		totalReturnPercent = (totalReturn / initialValue) * 100
	}

	returns := make([]float64, len(history)-1)
	for i := 1; i < len(history); i++ {
		if history[i-1].TotalValue > 0 {
			returns[i-1] = (history[i].TotalValue - history[i-1].TotalValue) / history[i-1].TotalValue
		}
	}

	mean := portfolioMean(returns)
	volatility := portfolioStdDev(returns, mean) * 100

	sharpeRatio := 0.0
	if volatility > 0 {
		sharpeRatio = (mean * 252) / (volatility / 100)
	}

	maxDrawdown, maxDrawdownPercent := calculateMaxDrawdown(history)
	bestDay, worstDay := findBestWorstDays(history)

	return &PerformanceMetrics{
		TotalReturn:        math.Round(totalReturn*100) / 100,
		TotalReturnPercent: math.Round(totalReturnPercent*100) / 100,
		MaxDrawdown:        math.Round(maxDrawdown*100) / 100,
		MaxDrawdownPercent: math.Round(maxDrawdownPercent*100) / 100,
		Volatility:         math.Round(volatility*100) / 100,
		SharpeRatio:        math.Round(sharpeRatio*100) / 100,
		BestDay:            bestDay,
		WorstDay:           worstDay,
	}
}

func calculateMaxDrawdown(history []PortfolioSnapshot) (float64, float64) {
	if len(history) == 0 {
		return 0, 0
	}

	maxValue := history[0].TotalValue
	maxDrawdown := 0.0

	for _, snapshot := range history {
		if snapshot.TotalValue > maxValue {
			maxValue = snapshot.TotalValue
		}
		drawdown := maxValue - snapshot.TotalValue
		if drawdown > maxDrawdown {
			maxDrawdown = drawdown
		}
	}

	maxDrawdownPercent := 0.0
	if maxValue > 0 {
		maxDrawdownPercent = (maxDrawdown / maxValue) * 100
	}

	return maxDrawdown, maxDrawdownPercent
}

func findBestWorstDays(history []PortfolioSnapshot) (*DayMetrics, *DayMetrics) {
	if len(history) < 2 {
		return nil, nil
	}

	var bestDay, worstDay *DayMetrics
	maxReturn := -math.MaxFloat64
	minReturn := math.MaxFloat64

	for i := 1; i < len(history); i++ {
		if history[i-1].TotalValue <= 0 {
			continue
		}
		returnValue := history[i].TotalValue - history[i-1].TotalValue
		returnPercent := (returnValue / history[i-1].TotalValue) * 100

		if returnValue > maxReturn {
			maxReturn = returnValue
			timestamp, _ := time.Parse(time.RFC3339, history[i].Timestamp)
			bestDay = &DayMetrics{
				Date:          timestamp.Format("2006-01-02"),
				Return:        math.Round(returnValue*100) / 100,
				ReturnPercent: math.Round(returnPercent*100) / 100,
			}
		}

		if returnValue < minReturn {
			minReturn = returnValue
			timestamp, _ := time.Parse(time.RFC3339, history[i].Timestamp)
			worstDay = &DayMetrics{
				Date:          timestamp.Format("2006-01-02"),
				Return:        math.Round(returnValue*100) / 100,
				ReturnPercent: math.Round(returnPercent*100) / 100,
			}
		}
	}

	return bestDay, worstDay
}

func portfolioMean(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}
	sum := 0.0
	for _, v := range values {
		sum += v
	}
	return sum / float64(len(values))
}

func portfolioStdDev(values []float64, mean float64) float64 {
	if len(values) == 0 {
		return 0
	}
	sumSquares := 0.0
	for _, v := range values {
		diff := v - mean
		sumSquares += diff * diff
	}
	return math.Sqrt(sumSquares / float64(len(values)))
}
