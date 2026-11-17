package services

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/rantipay/trading-mcp/internal/ai"
)

// Operator-driven entries and exits for end-to-end verification of the real
// execution path (added 2026-11-03 after the reactivated bot had not opened a
// single position because the automatic signals were systematically blocked).
//
// A manual entry travels through executeTrade exactly like an automatic one —
// Ring 0 / RiskSentinel kill switches, the AI-02 pre-trade validation
// (risk, capital, R:R, rule engine, circuit breaker), SL/TP normalization,
// position tracking and the exchange stop — but skips the SIGNAL-quality
// gates that only make sense for a model-generated signal: the macro trend
// filter, the order-flow imbalance gate, the confluence requirement and the
// confidence-tier sizing. The operator states the size explicitly.

// ManualEntryStrategy tags a Decision created by the operator. The tag that
// follows the colon identifies the waiter that receives the ExecutionResult.
const ManualEntryStrategy = "manual_entry"

// manualEntryConfluenceCredit satisfies the validator's minimum confluence
// (DefaultConfig.MinConfluence = 3) for an operator order.
const manualEntryConfluenceCredit = 3

// manualEntryTimeout bounds how long ExecuteManualEntry waits for the result.
const manualEntryTimeout = 90 * time.Second

func manualDecisionStrategy(tag string) string {
	return ManualEntryStrategy + ":" + tag
}

// isManualEntry reports whether the decision was created by the operator.
func isManualEntry(decision *ai.Decision) bool {
	return decision != nil && strings.HasPrefix(decision.Strategy, ManualEntryStrategy)
}

// manualTag extracts the waiter tag of a manual decision ("" otherwise).
func manualTag(decision *ai.Decision) string {
	if !isManualEntry(decision) {
		return ""
	}
	if i := strings.IndexByte(decision.Strategy, ':'); i >= 0 {
		return decision.Strategy[i+1:]
	}
	return ""
}

// manualWaiters hands ExecutionResults tagged by executeTrade back to the
// operator call that is waiting for them.
type manualWaiters struct {
	mu      sync.Mutex
	waiters map[string]chan *ExecutionResult
}

func (m *manualWaiters) register(tag string) chan *ExecutionResult {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.waiters == nil {
		m.waiters = make(map[string]chan *ExecutionResult)
	}
	ch := make(chan *ExecutionResult, 1)
	m.waiters[tag] = ch
	return ch
}

func (m *manualWaiters) unregister(tag string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.waiters, tag)
}

// deliver passes the result to its waiter; it never blocks.
func (m *manualWaiters) deliver(result *ExecutionResult) {
	if result == nil || result.Tag == "" {
		return
	}
	m.mu.Lock()
	ch := m.waiters[result.Tag]
	m.mu.Unlock()
	if ch == nil {
		return
	}
	select {
	case ch <- result:
	default:
	}
}

// ManualEntryRequest is the operator's order.
type ManualEntryRequest struct {
	Pair    string  `json:"pair"`     // optional: defaults to the bot's current pair
	Side    string  `json:"side"`     // BUY or SELL
	SizeUSD float64 `json:"size_usd"` // notional in USD; 0 = resolved base notional
}

// ExecuteManualEntry opens a position through the bot's real execution path and
// returns its outcome. The bot must be running so the position is managed
// (SL/TP, trailing, breakeven, exchange stop) after the fill.
func (bot *AITradingBot) ExecuteManualEntry(ctx context.Context, req ManualEntryRequest) (*ExecutionResult, error) {
	side := strings.ToUpper(strings.TrimSpace(req.Side))
	if side != "BUY" && side != "SELL" {
		return nil, fmt.Errorf("side must be BUY or SELL, got %q", req.Side)
	}
	if req.SizeUSD < 0 {
		return nil, fmt.Errorf("size_usd must be positive")
	}

	bot.mu.Lock()
	if !bot.running {
		bot.mu.Unlock()
		return nil, fmt.Errorf("bot is not running: start it first so the position is managed")
	}
	if bot.emergencyStop {
		bot.mu.Unlock()
		return nil, fmt.Errorf("bot is in emergency stop: reset its state first")
	}
	pair := strings.ToUpper(strings.TrimSpace(req.Pair))
	if pair == "" {
		pair = bot.config.Pair
	} else if pair != bot.config.Pair {
		bot.log.WithFields(logrus.Fields{"from": bot.config.Pair, "to": pair}).
			Warn("🧑‍✈️ Manual entry: switching bot pair at operator request")
		bot.config.Pair = pair
	}
	timeframes := bot.config.Timeframes
	leverage := bot.config.Leverage
	// 0 = let executeTrade resolve the base notional (capital-relative sizing).
	size := req.SizeUSD
	bot.mu.Unlock()

	if strings.TrimSpace(pair) == "" {
		return nil, fmt.Errorf("no pair: pass one in the request")
	}
	if bot.contextBuilder == nil {
		return nil, fmt.Errorf("context builder unavailable")
	}

	// Detach from the HTTP request: the order must not be cancelled midway
	// because the client went away. Keep the dynamic-config pair scope.
	execCtx, cancel := context.WithTimeout(bot.withPairScope(context.Background()), manualEntryTimeout)
	defer cancel()

	var (
		marketCtx *ai.MarketContext
		err       error
	)
	if len(timeframes) > 0 {
		marketCtx, err = bot.contextBuilder.BuildContext(execCtx, pair, bot.createDynamicStrategy(timeframes))
	} else {
		marketCtx, err = bot.contextBuilder.BuildContext(execCtx, pair)
	}
	if err != nil {
		return nil, fmt.Errorf("build market context for %s: %w", pair, err)
	}
	if marketCtx == nil || marketCtx.CurrentPrice <= 0 {
		return nil, fmt.Errorf("no current price for %s", pair)
	}

	tag := fmt.Sprintf("%s-%d", pair, time.Now().UnixNano())
	decision := &ai.Decision{
		Action:     side,
		Confidence: 1.0,
		Reason:     "operator manual entry (end-to-end verification)",
		Entry:      marketCtx.CurrentPrice,
		Size:       size,
		Leverage:   leverage,
		Strategy:   manualDecisionStrategy(tag),
	}

	bot.log.WithFields(logrus.Fields{
		"pair":     pair,
		"side":     side,
		"size_usd": size,
		"leverage": leverage,
		"entry":    decision.Entry,
	}).Warn("🧑‍✈️ Manual entry requested by operator — executing through the real path")

	waiter := bot.manual.register(tag)
	defer bot.manual.unregister(tag)

	go bot.executeTrade(execCtx, decision, marketCtx)

	select {
	case result := <-waiter:
		return result, nil
	case <-execCtx.Done():
		return nil, fmt.Errorf("manual entry timed out after %s waiting for the execution result", manualEntryTimeout)
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// CloseManagedPosition closes one bot-managed position (by id, or the only
// open position of a pair) through the bot's own close path, so the exit is
// recorded in trade_history with reason "manual_close" like any other close.
func (bot *AITradingBot) CloseManagedPosition(ctx context.Context, positionID, pair string) (*ai.ManagedPosition, error) {
	if bot.positionManager == nil {
		return nil, fmt.Errorf("position manager unavailable")
	}
	var pos *ai.ManagedPosition
	if positionID != "" {
		p, ok := bot.positionManager.GetPosition(positionID)
		if !ok {
			return nil, fmt.Errorf("position %q not found", positionID)
		}
		pos = p
	} else {
		pair = strings.ToUpper(strings.TrimSpace(pair))
		var matches []*ai.ManagedPosition
		for _, p := range bot.positionManager.GetAllPositions() {
			if pair == "" || strings.EqualFold(p.Symbol, pair) {
				matches = append(matches, p)
			}
		}
		switch len(matches) {
		case 0:
			return nil, fmt.Errorf("no open managed position for %q", pair)
		case 1:
			pos = matches[0]
		default:
			return nil, fmt.Errorf("%d positions match; pass position_id", len(matches))
		}
	}

	pnl, _ := bot.exchangeUnrealizedPnL(ctx, pos)
	bot.log.WithFields(logrus.Fields{
		"position_id": pos.ID,
		"symbol":      pos.Symbol,
		"side":        pos.Side,
		"pnl":         pnl,
	}).Warn("🧑‍✈️ Manual close requested by operator")

	bot.closePosition(ctx, pos, "manual_close", pnl)

	if _, still := bot.positionManager.GetPosition(pos.ID); still {
		return pos, fmt.Errorf("close order failed; position kept for retry on the next monitoring tick (see log)")
	}
	return pos, nil
}

// closePnLMemo carries the UnrealizedPnL the exchange reported for a position
// at the moment executeCloseOrder closed it, so closePosition can record it
// when its caller had no PnL figure. Entries are consumed once.
type closePnLMemo struct {
	mu   sync.Mutex
	pnls map[string]float64
}

func (bot *AITradingBot) rememberClosePnL(positionID string, pnl float64) {
	bot.closePnL.mu.Lock()
	defer bot.closePnL.mu.Unlock()
	if bot.closePnL.pnls == nil {
		bot.closePnL.pnls = make(map[string]float64)
	}
	bot.closePnL.pnls[positionID] = pnl
}

func (bot *AITradingBot) takeClosePnL(positionID string) (float64, bool) {
	bot.closePnL.mu.Lock()
	defer bot.closePnL.mu.Unlock()
	v, ok := bot.closePnL.pnls[positionID]
	if ok {
		delete(bot.closePnL.pnls, positionID)
	}
	return v, ok
}
