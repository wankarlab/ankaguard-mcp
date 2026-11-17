package server

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// TradeRecord is the minimal trade representation used by this handler.
// storage.TradeHistoryRepository satisfies TradeHistoryReader via an adapter
// in main.go because importing storage here would create a cycle.
type TradeRecord struct {
	ID             string
	Pair           string
	Exchange       string
	Side           string
	EntryPrice     float64
	EntryTime      time.Time
	Size           float64
	Leverage       int
	ExitPrice      float64
	ExitTime       time.Time
	ExitReason     string
	PnL            float64
	PnLPct         float64
	NetPnL         float64
	Confidence     float64
	DecisionReason string
	Status         string
}

// TradeHistoryReader abstracts the storage layer for the trade history endpoint.
// Implement this interface with an adapter in main.go wrapping
// *storage.TradeHistoryRepository so that this package stays cycle-free.
type TradeHistoryReader interface {
	GetClosedTrades(limit int) ([]*TradeRecord, error)
	GetOpenTrades() ([]*TradeRecord, error)
	GetByPair(pair string, limit int) ([]*TradeRecord, error)
}

// SetTradeRepo wires a TradeHistoryReader into the MCP server.
// Call this after storage is initialized in main.go.
func (s *MCPServer) SetTradeRepo(repo TradeHistoryReader) {
	s.tradeRepo = repo
}

// tradeHistoryItem is the JSON shape returned for each trade.
type tradeHistoryItem struct {
	TradeID        string  `json:"trade_id"`
	Pair           string  `json:"pair"`
	Exchange       string  `json:"exchange"`
	Side           string  `json:"side"`
	EntryPrice     float64 `json:"entry_price"`
	EntryTime      string  `json:"entry_time"`
	ExitPrice      float64 `json:"exit_price"`
	ExitTime       string  `json:"exit_time"`
	ExitReason     string  `json:"exit_reason"`
	PnL            float64 `json:"pnl"`
	PnLPct         float64 `json:"pnl_pct"`
	NetPnL         float64 `json:"net_pnl"`
	Size           float64 `json:"size"`
	Leverage       int     `json:"leverage"`
	Confidence     float64 `json:"confidence"`
	DecisionReason string  `json:"decision_reason"`
	Status         string  `json:"status"`
}

// tradeHistoryStats holds aggregate performance metrics.
type tradeHistoryStats struct {
	TotalClosed  int     `json:"total_closed"`
	Wins         int     `json:"wins"`
	Losses       int     `json:"losses"`
	WinRate      float64 `json:"win_rate"`
	TotalPnL     float64 `json:"total_pnl"`
	ProfitFactor float64 `json:"profit_factor"`
}

// tradeHistoryResponse is the top-level response envelope.
type tradeHistoryResponse struct {
	Trades []tradeHistoryItem `json:"trades"`
	Total  int                `json:"total"`
	Stats  tradeHistoryStats  `json:"stats"`
}

// handleTradeHistory handles GET /api/v1/trade-history
//
// Query parameters:
//
//	limit  - max records to return (default 50, max 200)
//	pair   - optional pair filter (e.g. "BTC-USDT")
//	status - "closed" | "open" | "all" (default "closed")
func (s *MCPServer) handleTradeHistory(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		json.NewEncoder(w).Encode(map[string]string{"error": "method not allowed"})
		return
	}

	if s.tradeRepo == nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		json.NewEncoder(w).Encode(map[string]string{"error": "trade history repository not initialized"})
		return
	}

	// Parse query parameters.
	q := r.URL.Query()

	limit := 50
	if raw := q.Get("limit"); raw != "" {
		if v, err := strconv.Atoi(raw); err == nil && v > 0 {
			limit = v
		}
	}
	if limit > 200 {
		limit = 200
	}

	pair := strings.TrimSpace(q.Get("pair"))

	statusFilter := "closed"
	if sv := q.Get("status"); sv == "open" || sv == "all" || sv == "closed" {
		statusFilter = sv
	}

	// Fetch trades based on filters.
	var trades []*TradeRecord
	var fetchErr error

	switch {
	case pair != "" && statusFilter == "closed":
		trades, fetchErr = s.tradeRepo.GetByPair(pair, limit)
	case statusFilter == "open":
		trades, fetchErr = s.tradeRepo.GetOpenTrades()
	case statusFilter == "all":
		closed, err := s.tradeRepo.GetClosedTrades(limit)
		if err != nil {
			fetchErr = err
			break
		}
		open, err := s.tradeRepo.GetOpenTrades()
		if err != nil {
			fetchErr = err
			break
		}
		trades = append(closed, open...)
	default:
		// statusFilter == "closed" (default)
		trades, fetchErr = s.tradeRepo.GetClosedTrades(limit)
	}

	if fetchErr != nil {
		s.log.WithError(fetchErr).Error("handleTradeHistory: failed to fetch trades")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"error": fetchErr.Error()})
		return
	}

	// Apply pair filter when status is "open" or "all"
	// (GetByPair only covers closed trades in the repository).
	if pair != "" && statusFilter != "closed" {
		filtered := trades[:0]
		for _, t := range trades {
			if strings.EqualFold(t.Pair, pair) {
				filtered = append(filtered, t)
			}
		}
		trades = filtered
	}

	// Enforce limit for "all" and "open" paths.
	if len(trades) > limit {
		trades = trades[:limit]
	}

	// Build response items and compute stats.
	items := make([]tradeHistoryItem, 0, len(trades))
	var totalPnL, grossWin, grossLoss float64
	wins, losses := 0, 0

	for _, t := range trades {
		exitTimeStr := ""
		if !t.ExitTime.IsZero() {
			exitTimeStr = t.ExitTime.UTC().Format("2006-01-02T15:04:05Z")
		}

		items = append(items, tradeHistoryItem{
			TradeID:        t.ID,
			Pair:           t.Pair,
			Exchange:       t.Exchange,
			Side:           t.Side,
			EntryPrice:     t.EntryPrice,
			EntryTime:      t.EntryTime.UTC().Format("2006-01-02T15:04:05Z"),
			ExitPrice:      t.ExitPrice,
			ExitTime:       exitTimeStr,
			ExitReason:     t.ExitReason,
			PnL:            t.PnL,
			PnLPct:         t.PnLPct,
			NetPnL:         t.NetPnL,
			Size:           t.Size,
			Leverage:       t.Leverage,
			Confidence:     t.Confidence,
			DecisionReason: t.DecisionReason,
			Status:         t.Status,
		})

		if t.Status == "closed" {
			totalPnL += t.NetPnL
			if t.NetPnL >= 0 {
				wins++
				grossWin += t.NetPnL
			} else {
				losses++
				grossLoss += -t.NetPnL
			}
		}
	}

	closedCount := wins + losses
	winRate := 0.0
	if closedCount > 0 {
		winRate = float64(wins) / float64(closedCount)
	}
	profitFactor := 0.0
	if grossLoss > 0 {
		profitFactor = grossWin / grossLoss
	}

	resp := tradeHistoryResponse{
		Trades: items,
		Total:  len(items),
		Stats: tradeHistoryStats{
			TotalClosed:  closedCount,
			Wins:         wins,
			Losses:       losses,
			WinRate:      winRate,
			TotalPnL:     totalPnL,
			ProfitFactor: profitFactor,
		},
	}

	json.NewEncoder(w).Encode(resp)
}
