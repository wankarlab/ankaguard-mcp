package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"time"

	"github.com/rantipay/trading-mcp/internal/gct"
	"github.com/rantipay/trading-mcp/internal/services"
)

// ManualEntryHTTP handles POST /api/v1/ai-bot/manual-entry.
//
// Operator order that travels through the bot's real execution path (kill
// switches, AI-02 validation, SL/TP normalization, position management) while
// skipping the signal-quality gates. Body: {"pair":"ICNT-USDT","side":"BUY","size_usd":2}.
func (h *AIBotHandler) ManualEntryHTTP(w http.ResponseWriter, r *http.Request) {
	if h.tradingBot == nil {
		h.writeError(w, http.StatusServiceUnavailable, "Trading bot not initialized")
		return
	}
	var req services.ManualEntryRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid body: %v", err))
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
	defer cancel()

	result, err := h.tradingBot.ExecuteManualEntry(ctx, req)
	if err != nil {
		h.writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	status, resp := manualEntryResponse(result)
	resp["positions"] = h.tradingBot.GetOpenPositions()
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(resp)
}

// manualEntryResponse maps an execution result to the HTTP status and body.
// A refused entry is 422; when the refusal is the minimum-contract guard the
// body also carries the contract cost and the smallest size that would pass.
func manualEntryResponse(result *services.ExecutionResult) (int, map[string]interface{}) {
	resp := map[string]interface{}{
		"success":   result.Success,
		"action":    result.Action,
		"order_id":  result.OrderID,
		"timestamp": result.Timestamp,
	}
	if result.Error != nil {
		resp["error"] = result.Error.Error()
		var minErr *gct.ErrMinContractTooLarge
		if errors.As(result.Error, &minErr) {
			resp["reason"] = "min_contract_too_large"
			resp["min_notional_usd"] = math.Round(minErr.MinContractNotional*100) / 100
			resp["min_size_usd"] = minErr.MinSizeUSD()
		}
	}
	if result.Success {
		return http.StatusOK, resp
	}
	return http.StatusUnprocessableEntity, resp
}

// ManualCloseHTTP handles POST /api/v1/ai-bot/manual-close.
// Body: {"position_id":"..."} or {"pair":"ICNT-USDT"}. The exit is recorded as
// "manual_close" through the bot's own close path.
func (h *AIBotHandler) ManualCloseHTTP(w http.ResponseWriter, r *http.Request) {
	if h.tradingBot == nil {
		h.writeError(w, http.StatusServiceUnavailable, "Trading bot not initialized")
		return
	}
	var req struct {
		PositionID string `json:"position_id"`
		Pair       string `json:"pair"`
	}
	if r.ContentLength != 0 {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			h.writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid body: %v", err))
			return
		}
	}

	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()

	pos, err := h.tradingBot.CloseManagedPosition(ctx, req.PositionID, req.Pair)
	if err != nil {
		status := http.StatusBadRequest
		if pos != nil {
			status = http.StatusBadGateway // the position exists; the exchange close failed
		}
		h.writeError(w, status, err.Error())
		return
	}
	h.writeSuccess(w, map[string]interface{}{
		"status":      "closed",
		"position_id": pos.ID,
		"symbol":      pos.Symbol,
		"side":        pos.Side,
		"positions":   h.tradingBot.GetOpenPositions(),
	})
}
