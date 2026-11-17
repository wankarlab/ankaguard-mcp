package adapters

import (
	"github.com/sirupsen/logrus"

	"github.com/rantipay/trading-mcp/internal/master"
	"github.com/rantipay/trading-mcp/internal/storage"
)

// TradeHistoryAdapter adapts storage.TradeHistoryRepository to master.TradeHistoryReader.
type TradeHistoryAdapter struct {
	log     *logrus.Logger
	storage *storage.Manager
}

// NewTradeHistoryAdapter constructs the adapter. storage may be nil — the
// adapter will then return empty results.
func NewTradeHistoryAdapter(log *logrus.Logger, storage *storage.Manager) *TradeHistoryAdapter {
	if log == nil {
		log = logrus.New()
	}
	return &TradeHistoryAdapter{log: log, storage: storage}
}

// GetRecentTrades returns the most recent closed trades for a pair, mapped into TradeRecord.
// Filtering and ordering are pushed into SQL via TradeHistoryRepository.GetByPair.
func (t *TradeHistoryAdapter) GetRecentTrades(pair string, limit int) ([]master.TradeRecord, error) {
	if t.storage == nil || t.storage.TradeHistory == nil {
		t.log.Debug("trade_history_adapter: storage not initialized, returning empty")
		return []master.TradeRecord{}, nil
	}
	if limit <= 0 {
		limit = 200
	}

	trades, err := t.storage.TradeHistory.GetByPair(pair, limit)
	if err != nil {
		return nil, err
	}

	out := make([]master.TradeRecord, 0, len(trades))
	for _, tr := range trades {
		if tr == nil {
			continue
		}
		out = append(out, toMasterTradeRecord(tr))
	}
	return out, nil
}

// GetAllPairsWithTrades returns the unique pairs that have closed trades,
// pushed down to a SELECT DISTINCT query in the repository.
func (t *TradeHistoryAdapter) GetAllPairsWithTrades() ([]string, error) {
	if t.storage == nil || t.storage.TradeHistory == nil {
		return []string{}, nil
	}
	return t.storage.TradeHistory.GetDistinctPairs()
}

func toMasterTradeRecord(tr *storage.Trade) master.TradeRecord {
	rec := master.TradeRecord{
		Pair:       tr.Pair,
		Side:       tr.Side,
		EntryTime:  tr.EntryTime,
		ExitTime:   tr.ExitTime,
		EntryPrice: tr.EntryPrice,
		ExitPrice:  tr.ExitPrice,
		PnLPercent: tr.PnLPct,
		Won:        tr.PnL > 0,
	}
	return rec
}
