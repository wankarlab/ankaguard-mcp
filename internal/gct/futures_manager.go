package gct

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/sirupsen/logrus"
)

// FuturesManager coordinates all futures operations
type FuturesManager struct {
	kucoinClient     *KuCoinRESTClient
	positionManager  *PositionCache
	leverageCtrl     *LeverageController
	logger           *logrus.Logger
	credFailRecorder CredentialFailureRecorder // H0.3.7: optional; can be nil

	// Contract multiplier cache: symbol → base units per contract (static per symbol, safe to cache indefinitely)
	multiplierCache   map[string]float64
	multiplierCacheMu sync.RWMutex
}

// NewFuturesManager creates a new futures manager
func NewFuturesManager(kucoinClient *KuCoinRESTClient, logger *logrus.Logger) (*FuturesManager, error) {
	if kucoinClient == nil {
		return nil, fmt.Errorf("kucoin client is nil")
	}
	if logger == nil {
		return nil, fmt.Errorf("logger is nil")
	}

	// Create position manager
	positionManager, err := NewPositionCache(kucoinClient, logger)
	if err != nil {
		return nil, fmt.Errorf("failed to create position manager: %w", err)
	}

	// Create leverage controller
	leverageCtrl, err := NewLeverageController(kucoinClient, logger)
	if err != nil {
		return nil, fmt.Errorf("failed to create leverage controller: %w", err)
	}

	return &FuturesManager{
		kucoinClient:    kucoinClient,
		positionManager: positionManager,
		leverageCtrl:    leverageCtrl,
		logger:          logger,
		multiplierCache: make(map[string]float64),
	}, nil
}

// SetCredentialFailureRecorder H0.3.7: configure credential failure recorder for auth failure tracking
func (fm *FuturesManager) SetCredentialFailureRecorder(recorder CredentialFailureRecorder) {
	if fm == nil {
		return
	}
	fm.credFailRecorder = recorder
	if fm.kucoinClient != nil {
		fm.kucoinClient.SetCredentialFailureRecorder(recorder)
	}
}

// contractMultiplierFor returns the face value (base units per contract) for the given
// futures symbol, fetching from KuCoin once and caching the result. Falls back to 1.0
// on any error so the caller always gets a usable value.
func (fm *FuturesManager) contractMultiplierFor(ctx context.Context, symbol string) float64 {
	fm.multiplierCacheMu.RLock()
	if m, ok := fm.multiplierCache[symbol]; ok {
		fm.multiplierCacheMu.RUnlock()
		return m
	}
	fm.multiplierCacheMu.RUnlock()

	m, err := fm.kucoinClient.GetContractMultiplier(ctx, symbol)
	if err != nil || m <= 0 {
		fm.logger.Warnf("Could not fetch contract multiplier for %s (%v), using 1.0", symbol, err)
		return 1.0
	}

	fm.multiplierCacheMu.Lock()
	fm.multiplierCache[symbol] = m
	fm.multiplierCacheMu.Unlock()

	fm.logger.Debugf("Cached contract multiplier for %s: %.6f", symbol, m)
	return m
}

// PlaceFuturesOrder handles complete futures order flow with margin mode setup
func (fm *FuturesManager) PlaceFuturesOrder(ctx context.Context, req *SubmitOrderRequest) (*Order, error) {
	if fm == nil {
		return nil, fmt.Errorf("futures manager is nil")
	}
	if req == nil {
		return nil, ErrNilRequest
	}
	if ctx == nil {
		ctx = context.Background()
	}

	// Validate basic request fields
	if req.Exchange == "" {
		return nil, fmt.Errorf("exchange is required")
	}
	if req.Pair == "" {
		return nil, fmt.Errorf("pair is required")
	}
	if req.Side != "buy" && req.Side != "sell" {
		return nil, fmt.Errorf("side must be 'buy' or 'sell', got: %s", req.Side)
	}
	if req.Amount <= 0 {
		return nil, fmt.Errorf("amount must be greater than 0, got: %.8f", req.Amount)
	}
	if req.Leverage <= 0 {
		req.Leverage = 1 // Default to 1x if not specified
	}

	// Convert pair to futures symbol (BTC-USDT -> BTCUSDTM)
	symbol := convertPairToFuturesSymbol(req.Pair)

	fm.logger.WithFields(logrus.Fields{
		"exchange": req.Exchange,
		"symbol":   symbol,
		"side":     req.Side,
		"amount":   req.Amount,
		"leverage": req.Leverage,
		"type":     req.Type,
	}).Info("Placing futures order")

	// Step 1: Margin mode is now sent directly in the order payload (marginMode
	// field in SubmitFuturesOrder). KuCoin deprecated the /api/v2/changeCrossMode
	// endpoint (returns 404). Inlining the mode per-order avoids the dead
	// endpoint entirely while still aligning the order with our preferred mode.
	if currentMode, err := fm.leverageCtrl.GetMarginMode(ctx, symbol); err == nil && currentMode != "" {
		fm.logger.Debugf("Current margin mode for %s: %s (order will request CROSS)", symbol, currentMode)
	}

	// Step 2: Set leverage (skip if already configured to avoid API errors)
	leverage := int(req.Leverage)
	fm.logger.Infof("Using leverage %dx for %s (assuming already configured in KuCoin web)", leverage, symbol)

	// Step 3: Check available balance BEFORE placing order
	// NOTE: Balance validation is now delegated to KuCoin API for accuracy
	// Local validation was too conservative; KuCoin's validation is more precise
	fm.logger.Infof("Skipping local balance check - KuCoin API will validate balance")

	// Still log balance info for debugging if available
	balance, balErr := fm.kucoinClient.GetFuturesBalance(ctx, "USDT")
	if balErr != nil {
		fm.logger.Warnf("Could not fetch balance info: %v", balErr)
	} else {
		availableBalance := 0.0
		if avail, ok := balance["available"].(float64); ok {
			availableBalance = avail
		}
		fm.logger.WithFields(logrus.Fields{
			"available_balance": availableBalance,
			"leverage":          req.Leverage,
		}).Debug("Available balance for reference")
	}

	// Step 4: Generate client order ID if not provided
	clientOID := req.ClientID
	if clientOID == "" {
		clientOID = fmt.Sprintf("fm_%s", uuid.New().String()[:8])
	}

	// Step 5: Convert USD amount → number of contracts and submit.
	// req.Amount is always in USD (e.g. 7.0 = $7).
	// KuCoin formula: contracts = USD / (price × multiplier)
	//   multiplier = base units per contract (e.g. NEARUSDTM=1, BTCUSDTM=0.001)
	var currentPrice float64
	if req.Price > 0 {
		currentPrice = req.Price // limit order — price is already known
	} else {
		// Market order — fetch mark price from the futures ticker
		if ticker, terr := fm.kucoinClient.GetFuturesTicker(ctx, symbol); terr == nil && ticker.Last > 0 {
			currentPrice = ticker.Last
		}
	}

	multiplier := fm.contractMultiplierFor(ctx, symbol)

	var contractSize float64
	if currentPrice > 0 {
		rawContracts := req.Amount / (currentPrice * multiplier)
		contractSize = futuresContracts(req.Amount, currentPrice, multiplier) // shared with the notional calc (order_notional.go)
		fm.logger.WithFields(logrus.Fields{
			"usd_amount":    req.Amount,
			"price":         currentPrice,
			"multiplier":    multiplier,
			"raw_contracts": rawContracts,
			"contracts":     contractSize,
		}).Info("USD → contracts conversion")

		// Notional safety guard: if a SINGLE contract costs more than N× the
		// intended USD amount, the multiplier almost certainly defaulted to 1.0
		// (API failure) instead of the real value (e.g. 0.001 for XMR). Trading
		// in that state means a $3.50 target becomes a $357 position — refuse.
		// The factor comes from the caller (dynamic config risk.max_notional_factor);
		// 0 falls back to 5.0 so callers that don't set it stay protected.
		//
		// This guard only applies to OPENING orders, where Amount is a USD sizing
		// target. Close / reduce-only / stop orders just exit an existing position
		// (Amount is the position size, not a USD target) — they REDUCE risk and
		// must never be refused, otherwise an adopted position is left with no hard
		// exchange stop (this rejected the exchange SL for adopted SOL: "1 contract
		// = $7.34 vs $1 target", leaving software-only monitoring).
		isProtectiveExit := req.CloseOrder || req.ReduceOnly || req.StopPrice > 0
		if !isProtectiveExit {
			maxNotionalFactor := req.MaxNotionalFactor
			if maxNotionalFactor <= 0 {
				maxNotionalFactor = 5.0
			}
			minContractNotional := multiplier * currentPrice
			if minContractNotional > maxNotionalFactor*req.Amount {
				return nil, &ErrMinContractTooLarge{
					Symbol:              symbol,
					MinContractNotional: minContractNotional,
					TargetUSD:           req.Amount,
					Factor:              maxNotionalFactor,
					Multiplier:          multiplier,
					Price:               currentPrice,
				}
			}
		}
	} else {
		// Cannot price the order — use minimum of 1 contract as safe fallback
		contractSize = 1
		fm.logger.Warnf("Cannot fetch price for %s, using minimum 1 contract (intended $%.2f)", symbol, req.Amount)
	}

	futuresReq := &FuturesOrderRequest{
		Symbol:     symbol,
		Side:       req.Side,
		Type:       req.Type,
		Size:       contractSize, // Number of contracts, not DOGE quantity
		Price:      req.Price,
		Leverage:   leverage,
		ClientOID:  clientOID,
		ReduceOnly: req.ReduceOnly,
		CloseOrder: req.CloseOrder,
		// F2 (XSEC execution plan): PostOnly now flows from the caller instead of
		// being hardcoded to false. Default remains false (taker) for any caller
		// that doesn't explicitly opt in, so behavior for existing paths (live
		// directional scalper) is unchanged.
		PostOnly:      req.PostOnly,
		StopPrice:     req.StopPrice,
		StopPriceType: req.StopPriceType,
	}

	// H0.3.7: set user context for auth failure recording
	if req.UserID != "" {
		fm.kucoinClient.SetUserContext(req.UserID, req.Exchange)
		if fm.credFailRecorder != nil {
			fm.kucoinClient.SetCredentialFailureRecorder(fm.credFailRecorder)
		}
	}

	orderID, err := fm.kucoinClient.SubmitFuturesOrder(ctx, futuresReq)
	if err != nil {
		// Enhance error message with actionable information
		enhancedErr := EnhanceKuCoinError(err, symbol)
		fm.logger.WithFields(logrus.Fields{
			"symbol": symbol,
			"error":  enhancedErr.Error(),
		}).Error("Failed to submit futures order")
		return nil, fmt.Errorf("failed to submit futures order: %w", enhancedErr)
	}

	fm.logger.WithFields(logrus.Fields{
		"order_id": orderID,
		"symbol":   symbol,
		"side":     req.Side,
		"amount":   req.Amount,
		"leverage": leverage,
	}).Info("✓ Futures order placed successfully")

	// Step 6: Return order details
	return &Order{
		ID:        orderID,
		Exchange:  req.Exchange,
		Pair:      req.Pair,
		Side:      req.Side,
		Type:      req.Type,
		Amount:    req.Amount,
		Price:     req.Price,
		Status:    "open",
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}, nil
}

// GetPositions retrieves all open futures positions
func (fm *FuturesManager) GetPositions(ctx context.Context) ([]*Position, error) {
	if fm == nil {
		return nil, fmt.Errorf("futures manager is nil")
	}
	if ctx == nil {
		ctx = context.Background()
	}

	return fm.positionManager.GetPositions(ctx)
}

// GetPosition retrieves a specific position
func (fm *FuturesManager) GetPosition(ctx context.Context, symbol string) (*Position, error) {
	if fm == nil {
		return nil, fmt.Errorf("futures manager is nil")
	}
	if symbol == "" {
		return nil, fmt.Errorf("symbol is empty")
	}
	if ctx == nil {
		ctx = context.Background()
	}

	return fm.positionManager.GetPosition(ctx, symbol)
}

// ClosePosition closes a specific futures position
func (fm *FuturesManager) ClosePosition(ctx context.Context, symbol string) error {
	if fm == nil {
		return fmt.Errorf("futures manager is nil")
	}
	if symbol == "" {
		return fmt.Errorf("symbol is empty")
	}
	if ctx == nil {
		ctx = context.Background()
	}

	fm.logger.Infof("Closing position for %s", symbol)

	// Get current position
	position, err := fm.positionManager.GetPosition(ctx, symbol)
	if err != nil {
		if err == ErrPositionNotFound {
			fm.logger.Warnf("No position found for %s", symbol)
			return nil // Not an error if position doesn't exist
		}
		return fmt.Errorf("failed to get position: %w", err)
	}

	if position == nil {
		fm.logger.Warnf("Position is nil for %s", symbol)
		return nil
	}

	// Determine opposite side
	closeSide := "sell"
	if position.Side == "short" {
		closeSide = "buy"
	}

	fm.logger.WithFields(logrus.Fields{
		"symbol":     symbol,
		"side":       position.Side,
		"size":       position.Size,
		"close_side": closeSide,
	}).Info("Placing closing order")

	// Generate client order ID
	clientOID := fmt.Sprintf("close_%s", uuid.New().String()[:8])

	// Place closing order. CloseOrder=true is required for Hedge Mode accounts;
	// SubmitFuturesOrder sets positionSide automatically based on side+CloseOrder.
	futuresReq := &FuturesOrderRequest{
		Symbol:     symbol,
		Side:       closeSide,
		Type:       "market",
		Size:       position.Size,
		Leverage:   position.Leverage,
		ClientOID:  clientOID,
		CloseOrder: true,
	}

	orderID, err := fm.kucoinClient.SubmitFuturesOrder(ctx, futuresReq)
	if err != nil {
		return fmt.Errorf("failed to submit closing order: %w", err)
	}

	fm.logger.WithFields(logrus.Fields{
		"order_id": orderID,
		"symbol":   symbol,
		"pnl":      position.UnrealizedPnL,
	}).Info("✓ Position closed successfully")

	// Clear cache for this symbol
	fm.positionManager.ClearCacheForSymbol(symbol)

	return nil
}

// Stop stops the futures manager and its components
func (fm *FuturesManager) Stop() {
	if fm == nil {
		return
	}

	if fm.positionManager != nil {
		fm.positionManager.Stop()
	}

	fm.logger.Info("Futures manager stopped")
}

// convertPairToFuturesSymbol converts spot pair to futures symbol
// BTC-USDT -> BTCUSDTM
// ETH-USDT -> ETHUSDTM
// DOGE-USDT -> DOGEUSDTM
// DOGE_USDTM -> DOGEUSDTM
func convertPairToFuturesSymbol(pair string) string {
	if pair == "" {
		return ""
	}

	// Remove hyphens and underscores
	symbol := strings.ReplaceAll(pair, "-", "")
	symbol = strings.ReplaceAll(symbol, "_", "")

	// If already ends with M, return as is
	if strings.HasSuffix(symbol, "M") {
		return symbol
	}

	return symbol + "M"
}

// convertFuturesSymbolToPair converts futures symbol to spot pair
// BTCUSDTM -> BTC-USDT
// ETHUSDTM -> ETH-USDT
func convertFuturesSymbolToPair(symbol string) string {
	if symbol == "" {
		return ""
	}

	// Remove M suffix
	symbol = strings.TrimSuffix(symbol, "M")

	// Find USDT position and insert hyphen
	if idx := strings.Index(symbol, "USDT"); idx > 0 {
		return symbol[:idx] + "-" + symbol[idx:]
	}

	return symbol
}
