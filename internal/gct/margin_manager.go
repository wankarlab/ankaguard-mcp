// Package gct provides the GoCryptoTrader gRPC client and supporting execution
// managers.  This file implements the Wave 4-A margin scaffold.
//
// # Current state
//
// The KuCoin REST client (kucoin_rest_client.go) exposes:
//   - SubmitFuturesOrder  → /api/v1/orders (futures)
//   - GetOpenOrders       → /api/v1/orders (spot + futures)
//   - SetMarginMode       → /api/v2/changeCrossUserSymbol (margin-mode on futures)
//
// It does NOT expose a dedicated KuCoin Margin Trading endpoint
// (/api/v3/hf/margin/order or /api/v1/margin/order).  Until that endpoint is
// wired in kucoin_rest_client.go, PlaceMarginOrder returns ErrMarginNotImplemented.
// This keeps the scaffold complete (validation, symbol conversion, gate check)
// while making the missing wiring explicit and discoverable.
//
// # Paper / sandbox safety
//
// The gct client layer has no unified paper-mode flag.  Callers that need
// paper-mode for margin MUST wrap the call site before reaching SubmitOrder
// (e.g. the scalping executor already has a paper-mode guard).  A TODO below
// marks the injection point for when a paper flag is plumbed to this layer.
package gct

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/sirupsen/logrus"
)

// ErrMarginNotImplemented is returned when margin order placement is requested
// but the underlying exchange REST endpoint has not been wired yet.
// Distinguish it from the gate error ("margin disabled") so callers can tell
// the two apart: gate → user config problem; not-implemented → dev task.
var ErrMarginNotImplemented = errors.New(
	"margin order endpoint not wired: KuCoinRESTClient lacks SubmitMarginOrder;" +
		" wire /api/v3/hf/margin/order (or /api/v1/margin/order) before enabling live margin")

// ErrMarginDisabled is returned when bot.margin_enabled=false (the default).
var ErrMarginDisabled = errors.New(
	"margin mode is disabled (set bot.margin_enabled=true to enable)")

// marginSide normalises order side to KuCoin margin API values ("buy"/"sell").
func marginSide(side string) (string, error) {
	s := strings.ToLower(strings.TrimSpace(side))
	if s != "buy" && s != "sell" {
		return "", fmt.Errorf("margin order side must be 'buy' or 'sell', got: %q", side)
	}
	return s, nil
}

// convertPairToMarginSymbol normalises a pair string to a spot-style symbol
// suitable for KuCoin Margin API.  Margin on KuCoin uses the same symbol format
// as spot (BTC-USDT), NOT the futures perpetual format (BTCUSDTM).
//
// Examples:
//
//	BTC-USDT   → BTC-USDT   (already correct)
//	BTC_USDT   → BTC-USDT
//	BTCUSDTM   → BTC-USDT   (strip futures suffix)
//	btc-usdt   → BTC-USDT   (uppercased)
func convertPairToMarginSymbol(pair string) string {
	if pair == "" {
		return ""
	}
	// Uppercase first so comparisons are consistent.
	s := strings.ToUpper(pair)

	// Strip futures suffix.
	s = strings.TrimSuffix(s, "M")

	// Normalise underscore delimiter to hyphen.
	s = strings.ReplaceAll(s, "_", "-")

	// If there is no hyphen yet (e.g. "BTCUSDT") try to insert before USDT/BTC.
	if !strings.Contains(s, "-") {
		for _, quote := range []string{"USDT", "BTC", "ETH", "USDC", "BNB"} {
			if idx := strings.LastIndex(s, quote); idx > 0 {
				s = s[:idx] + "-" + s[idx:]
				break
			}
		}
	}
	return s
}

// MarginOrderRequest holds the normalised parameters for a margin order.
// It is derived from SubmitOrderRequest after gate and validation checks.
type MarginOrderRequest struct {
	Symbol    string  // spot-style, e.g. BTC-USDT
	Side      string  // "buy" or "sell"
	OrderType string  // "limit" or "market"
	Size      float64 // base-currency quantity
	Price     float64 // required for limit orders
	ClientOID string
	// MarginMode is read from exchange/account config; the manager does not set
	// it per-order because KuCoin Margin Trading uses the account-level setting.
	MarginMode string // "ISOLATED" or "CROSSED" (informational only at this layer)
}

// MarginManager coordinates margin order operations.
// It mirrors the FuturesManager structure so the client.go routing layer treats
// both managers symmetrically.
//
// Until SubmitMarginOrder is implemented in KuCoinRESTClient, every call to
// PlaceMarginOrder returns ErrMarginNotImplemented.  The struct, constructor,
// and validation logic are complete so wiring the REST endpoint is a
// single-function addition.
type MarginManager struct {
	kucoinClient *KuCoinRESTClient
	logger       *logrus.Logger
}

// NewMarginManager creates a new MarginManager.
// Returns an error if required dependencies are nil.
func NewMarginManager(kucoinClient *KuCoinRESTClient, logger *logrus.Logger) (*MarginManager, error) {
	if kucoinClient == nil {
		return nil, fmt.Errorf("NewMarginManager: kucoinClient is nil")
	}
	if logger == nil {
		return nil, fmt.Errorf("NewMarginManager: logger is nil")
	}
	return &MarginManager{
		kucoinClient: kucoinClient,
		logger:       logger,
	}, nil
}

// PlaceMarginOrder validates and dispatches a margin order.
//
// Caller contract:
//   - The bot.margin_enabled gate MUST be checked by the caller (client.go
//     SubmitOrder) BEFORE reaching this method.  This method does NOT re-check
//     the gate — it trusts the routing layer.
//   - req.MarketType must be MarketTypeMargin (not enforced here; routing
//     enforces it).
//
// Implementation status:
//
//	The scaffold validates the request, converts the symbol, generates a
//	client OID, and then returns ErrMarginNotImplemented because
//	KuCoinRESTClient.SubmitMarginOrder does not exist yet.
//
//	To fully wire live margin:
//	  1. Add SubmitMarginOrder(ctx, *MarginOrderRequest) (string, error) to
//	     KuCoinRESTClient, targeting /api/v3/hf/margin/order.
//	  2. Replace the ErrMarginNotImplemented return below with the actual call.
//	  3. Add auto-borrow option (isIsolated + autoBorrow params) if required.
//
// Paper/sandbox note:
//
//	There is no paper-mode flag at the gct client layer.  Callers (e.g.
//	scalping executor) must guard paper mode BEFORE calling SubmitOrder.  When
//	a unified paper flag is added to the client struct, insert the simulation
//	path here analogous to SimulateOrder in client.go.
func (mm *MarginManager) PlaceMarginOrder(ctx context.Context, req *SubmitOrderRequest) (*Order, error) {
	if mm == nil {
		return nil, fmt.Errorf("margin manager is nil")
	}
	if req == nil {
		return nil, ErrNilRequest
	}
	if ctx == nil {
		ctx = context.Background()
	}

	// ── Validation ───────────────────────────────────────────────────────────

	if req.Exchange == "" {
		return nil, fmt.Errorf("margin order: exchange is required")
	}
	if req.Pair == "" {
		return nil, fmt.Errorf("margin order: pair is required")
	}
	if req.Side != "buy" && req.Side != "sell" {
		return nil, fmt.Errorf("margin order: side must be 'buy' or 'sell', got: %s", req.Side)
	}
	if req.Amount <= 0 {
		return nil, fmt.Errorf("margin order: amount must be > 0, got: %.8f", req.Amount)
	}
	if req.Type == "" {
		req.Type = "market"
	}
	orderType := strings.ToLower(req.Type)
	if orderType != "limit" && orderType != "market" {
		return nil, fmt.Errorf("margin order: type must be 'limit' or 'market', got: %s", req.Type)
	}
	if orderType == "limit" && req.Price <= 0 {
		return nil, fmt.Errorf("margin order: price required for limit order, got: %.8f", req.Price)
	}
	// Leverage is not applicable to standard margin orders (no cross-leverage
	// on KuCoin Spot Margin beyond the account-level margin ratio).
	if req.Leverage > 0 && req.Leverage != 1 {
		mm.logger.Warnf("margin order: leverage %.0f ignored (not applicable for spot margin)", req.Leverage)
	}

	// ── Symbol conversion ─────────────────────────────────────────────────────

	symbol := convertPairToMarginSymbol(req.Pair)
	if symbol == "" {
		return nil, fmt.Errorf("margin order: could not derive margin symbol from pair %q", req.Pair)
	}

	// ── Client OID ────────────────────────────────────────────────────────────

	clientOID := req.ClientID
	if clientOID == "" {
		clientOID = fmt.Sprintf("mm_%s", uuid.New().String()[:8])
	}

	// ── Logging ───────────────────────────────────────────────────────────────

	mm.logger.WithFields(logrus.Fields{
		"exchange":   req.Exchange,
		"pair":       req.Pair,
		"symbol":     symbol,
		"side":       req.Side,
		"type":       orderType,
		"amount":     req.Amount,
		"price":      req.Price,
		"client_oid": clientOID,
	}).Info("MarginManager.PlaceMarginOrder: validated request (endpoint not yet wired)")

	// ── Dispatch ──────────────────────────────────────────────────────────────
	//
	// TODO(wave4-a): replace ErrMarginNotImplemented with:
	//
	//   marginReq := &MarginOrderRequest{
	//       Symbol:    symbol,
	//       Side:      req.Side,
	//       OrderType: orderType,
	//       Size:      req.Amount,
	//       Price:     req.Price,
	//       ClientOID: clientOID,
	//   }
	//   orderID, err := mm.kucoinClient.SubmitMarginOrder(ctx, marginReq)
	//   if err != nil { return nil, fmt.Errorf("margin order failed: %w", err) }
	//   return &Order{ ID: orderID, ... }, nil
	//
	_ = clientOID // suppress unused warning until TODO is implemented
	return nil, fmt.Errorf("%w (pair=%s symbol=%s)", ErrMarginNotImplemented, req.Pair, symbol)
}

// newSimulatedMarginOrder returns a synthetic Order for paper/sandbox callers.
// This helper is intentionally package-private; callers outside this package
// should gate paper mode before reaching SubmitOrder.
func newSimulatedMarginOrder(req *SubmitOrderRequest, symbol string) *Order {
	return &Order{
		ID:        fmt.Sprintf("sim_margin_%s", uuid.New().String()[:8]),
		Exchange:  req.Exchange,
		Pair:      req.Pair,
		Side:      req.Side,
		Type:      req.Type,
		Amount:    req.Amount,
		Price:     req.Price,
		Status:    "simulated",
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
}
