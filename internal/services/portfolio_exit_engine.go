package services

// portfolioExitEngine — basket-level profit protection.
//
// Individual position monitors react slowly to sudden market reversals: a sharp
// correlated move across multiple pairs gains +$1.50 in 30 seconds, then gives
// it back in the next 30 seconds before any per-position SL or trailing can
// respond. This engine monitors aggregate unrealized PnL across all open positions
// and triggers a basket exit when the portfolio peaks and starts pulling back.
//
// Two exit modes (all DB-backed, hot-reloadable via portfolio.exit.*):
//
//   - trail (soft): when aggregate PnL drops portfolio.exit.trail_pct% from its
//     peak, evaluate each position individually. Profitable positions with a
//     reversal signal → close. Profitable positions with a continuation signal →
//     tighten SL to half the distance from current price. Losing positions → leave
//     (exchange SL handles them).
//
//   - close_all (hard): when aggregate PnL drops portfolio.exit.close_all_threshold_pct%
//     from its peak, close every profitable position immediately without evaluation.
//
// Disabled by default (portfolio.exit.enabled=false). Activate from DB when ready.

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/rantipay/trading-mcp/internal/ai"
	dynamic "github.com/rantipay/trading-mcp/internal/config/dynamic"
	"github.com/sirupsen/logrus"
)

// portfolioSample records aggregate portfolio PnL at a point in time.
type portfolioSample struct {
	t   time.Time
	pnl float64
}

// portfolioExitLoop monitors total unrealized PnL across all open positions and
// fires basket exits when the portfolio retreats from its high-water mark.
// Runs as a goroutine from Start(); cancelled via ctx.
func (bot *AITradingBot) portfolioExitLoop(ctx context.Context) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	var peakPnL float64
	var samples []portfolioSample // sliding window for velocity

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			// BOT-07: run each tick's body inside a recovered closure so a panic
			// (e.g. from calcPortfolioPnL/execPortfolioExit) doesn't kill this
			// goroutine — and, unrecovered anywhere up the stack, the whole
			// process (every user's bot, not just this one). `continue` above
			// became `return` below since we're now inside a nested func.
			func() {
				defer func() {
					if r := recover(); r != nil {
						bot.log.WithField("panic", r).Error("🚨 Panic in portfolioExitLoop tick - recovered")
					}
				}()

				snap := dynamic.SnapshotFromContext(ctx)
				if snap == nil || !snap.Bool("portfolio.exit.enabled") {
					// Reset state when disabled so peak doesn't carry over.
					peakPnL = 0
					samples = samples[:0]
					return
				}

				minProfitUSD := 0.30
				trailPct := 25.0
				closeAllPct := 40.0
				sharpMoveSec := 30
				tightenFraction := 0.50

				if v := snap.Float("portfolio.exit.min_profit_usd"); v > 0 {
					minProfitUSD = v
				}
				if v := snap.Float("portfolio.exit.trail_pct"); v > 0 {
					trailPct = v
				}
				if v := snap.Float("portfolio.exit.close_all_threshold_pct"); v > 0 {
					closeAllPct = v
				}
				if v := snap.Int("portfolio.exit.velocity_window_sec"); v > 0 {
					sharpMoveSec = v
				}
				if v := snap.Float("portfolio.exit.tighten_fraction"); v > 0 && v < 1 {
					tightenFraction = v
				}

				positions := bot.positionManager.GetAllPositions()
				if len(positions) == 0 {
					peakPnL = 0
					samples = samples[:0]
					return
				}

				totalPnL, pnlByID := bot.calcPortfolioPnL(ctx, positions)

				// Maintain velocity window.
				now := time.Now()
				samples = append(samples, portfolioSample{t: now, pnl: totalPnL})
				cutoff := now.Add(-time.Duration(sharpMoveSec) * time.Second)
				keep := 0
				for keep < len(samples) && samples[keep].t.Before(cutoff) {
					keep++
				}
				samples = samples[keep:]

				// Update high-water mark.
				if totalPnL > peakPnL {
					peakPnL = totalPnL
				}

				if peakPnL < minProfitUSD {
					return
				}

				drawdown := peakPnL - totalPnL
				if drawdown <= 0 {
					return
				}
				drawdownPct := drawdown / peakPnL * 100

				log := bot.log.WithFields(logrus.Fields{
					"peak_pnl":     fmt.Sprintf("$%.4f", peakPnL),
					"current_pnl":  fmt.Sprintf("$%.4f", totalPnL),
					"drawdown_pct": fmt.Sprintf("%.1f%%", drawdownPct),
					"positions":    len(positions),
				})

				if drawdownPct >= closeAllPct {
					log.Warn("📉 Portfolio exit: CLOSE_ALL — peak retrace exceeded hard threshold")
					bot.execPortfolioExit(ctx, positions, pnlByID, "portfolio_exit:close_all", false, 0)
					peakPnL = 0
					samples = samples[:0]
				} else if drawdownPct >= trailPct {
					log.Info("📊 Portfolio exit: SELECTIVE — trail threshold crossed")
					bot.execPortfolioExit(ctx, positions, pnlByID, "portfolio_exit:trail", true, tightenFraction)
					peakPnL = 0
					samples = samples[:0]
				}
			}()
		}
	}
}

// calcPortfolioPnL fetches live prices for all positions and returns:
//   - total: sum of all unrealized PnL in USD
//   - byID: map of position ID → individual unrealized PnL
//
// Positions whose price cannot be fetched are skipped (not counted).
func (bot *AITradingBot) calcPortfolioPnL(ctx context.Context, positions []*ai.ManagedPosition) (total float64, byID map[string]float64) {
	byID = make(map[string]float64, len(positions))
	for _, pos := range positions {
		price, err := bot.livePriceForPosition(ctx, pos)
		if err != nil || price <= 0 {
			continue
		}
		side := strings.ToUpper(pos.Side)
		isLong := side == "BUY" || side == "LONG"
		var moveFrac float64
		if isLong {
			moveFrac = (price - pos.EntryPrice) / pos.EntryPrice
		} else {
			moveFrac = (pos.EntryPrice - price) / pos.EntryPrice
		}
		var pnl float64
		if pos.NotionalUSD > 0 {
			pnl = moveFrac * pos.NotionalUSD
		} else {
			pnl = moveFrac * pos.EntryPrice * pos.Size
		}
		byID[pos.ID] = pnl
		total += pnl
	}
	return total, byID
}

// execPortfolioExit acts on every open position based on the trigger mode.
//
//   - selective=false (close_all): close every position that is currently
//     profitable. Losing positions are left for their exchange SL.
//
//   - selective=true (trail): for each profitable position, check whether
//     the most recent AI signal supports continuation or reversal.
//     Reversal → close immediately.
//     Continuation → tighten SL to tightenFraction of the gap between the
//     current price and the existing SL (moves the stop up toward price).
//     Losing positions are left for their exchange SL.
func (bot *AITradingBot) execPortfolioExit(
	ctx context.Context,
	positions []*ai.ManagedPosition,
	pnlByID map[string]float64,
	reason string,
	selective bool,
	tightenFraction float64,
) {
	for _, pos := range positions {
		posID := pos.ID
		pnl, known := pnlByID[posID]
		if !known {
			continue // price fetch failed, skip
		}
		if pnl <= 0 {
			continue // losing — exchange SL handles it, don't touch
		}

		// Fetch live price for SL tightening math.
		currentPrice, err := bot.livePriceForPosition(ctx, pos)
		if err != nil || currentPrice <= 0 {
			continue
		}

		if !selective {
			// Hard close: capture the gain now.
			bot.log.WithFields(logrus.Fields{
				"symbol":  pos.Symbol,
				"side":    pos.Side,
				"pnl_usd": fmt.Sprintf("$%.4f", pnl),
				"reason":  reason,
			}).Info("🏁 Portfolio exit: closing profitable position")
			bot.closePosition(ctx, pos, reason, pnl)
			continue
		}

		// Selective: classify this position as continuation or reversal.
		if bot.positionHasReversalSignal(pos) {
			bot.log.WithFields(logrus.Fields{
				"symbol":  pos.Symbol,
				"side":    pos.Side,
				"pnl_usd": fmt.Sprintf("$%.4f", pnl),
			}).Info("🔄 Portfolio trail: reversal signal — closing")
			bot.closePosition(ctx, pos, reason+":reversal", pnl)
		} else {
			// Continuation: tighten SL toward current price.
			bot.portfolioTightenSL(pos, currentPrice, tightenFraction, reason)
		}
	}
}

// positionHasReversalSignal returns true when the most recent AI signal in the
// exit window is in the OPPOSITE direction to the position. Uses the last
// recorded signal only — no confidence threshold here; the portfolio engine
// already triggered because of meaningful drawdown, so any opposite signal is
// informative.
func (bot *AITradingBot) positionHasReversalSignal(pos *ai.ManagedPosition) bool {
	bot.exitSignalWindowsMu.Lock()
	window := bot.exitSignalWindows[pos.Symbol]
	bot.exitSignalWindowsMu.Unlock()

	if len(window) == 0 {
		return false // no data — assume continuation, let trailing handle it
	}

	last := window[len(window)-1]
	// Only consider signals recorded in the last 5 minutes.
	if time.Since(last.recordedAt) > 5*time.Minute {
		return false
	}

	side := strings.ToUpper(pos.Side)
	isLong := side == "BUY" || side == "LONG"
	lastUp := strings.ToUpper(last.action) == "BUY"

	// Opposite: long position + last signal is SELL, or short + last is BUY.
	if isLong && !lastUp {
		return true
	}
	if !isLong && lastUp {
		return true
	}
	return false
}

// portfolioTightenSL moves the stop-loss a fraction of the way from its current
// level toward the current price. Never widens; only moves in the profitable
// direction. Replaces the exchange stop order at the new level.
func (bot *AITradingBot) portfolioTightenSL(pos *ai.ManagedPosition, currentPrice, fraction float64, reason string) {
	// BOT-08: the StopLoss read-compute-write below races with monitoringLoop/
	// analysisLoop mutating the same field on the same *ManagedPosition
	// pointer. Serialize it with the position's own lock.
	pos.Lock()
	if pos.StopLoss <= 0 || currentPrice <= 0 || fraction <= 0 {
		pos.Unlock()
		return
	}

	side := strings.ToUpper(pos.Side)
	isLong := side == "BUY" || side == "LONG"
	oldSL := pos.StopLoss
	newSL := pos.StopLoss + (currentPrice-pos.StopLoss)*fraction

	improved := (isLong && newSL > oldSL) || (!isLong && newSL < oldSL)
	if !improved {
		pos.Unlock()
		return
	}

	pos.StopLoss = newSL
	pos.Unlock()

	bot.log.WithFields(logrus.Fields{
		"symbol":   pos.Symbol,
		"side":     pos.Side,
		"old_sl":   fmt.Sprintf("%.6f", oldSL),
		"new_sl":   fmt.Sprintf("%.6f", newSL),
		"current":  fmt.Sprintf("%.6f", currentPrice),
		"fraction": fmt.Sprintf("%.0f%%", fraction*100),
		"reason":   reason,
	}).Info("🔒 Portfolio trail: tightened SL toward current price")

	// BOT-09: sequential reissue (cancel then place), not two unordered
	// goroutines racing on pos.ExchangeStopOrderID.
	go bot.reissueExchangeStopLoss(context.Background(), pos)
}
