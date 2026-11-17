package handlers

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/rantipay/trading-mcp/internal/gct"
	"github.com/rantipay/trading-mcp/internal/services"
)

func TestManualEntryResponse_MinContractTooLarge(t *testing.T) {
	minErr := &gct.ErrMinContractTooLarge{Symbol: "XMRUSDTM", MinContractNotional: 11.6, TargetUSD: 2, Factor: 2}
	status, body := manualEntryResponse(&services.ExecutionResult{
		Success: false,
		Error:   fmt.Errorf("sizing: %w", minErr),
	})
	if status != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422", status)
	}
	if body["reason"] != "min_contract_too_large" || body["min_notional_usd"] != 11.6 || body["min_size_usd"] != 5.8 {
		t.Fatalf("unexpected body %+v", body)
	}
}

func TestManualEntryResponse_Success(t *testing.T) {
	status, body := manualEntryResponse(&services.ExecutionResult{Success: true, OrderID: "x"})
	if status != http.StatusOK || body["order_id"] != "x" {
		t.Fatalf("status=%d body=%+v", status, body)
	}
	if _, ok := body["min_size_usd"]; ok {
		t.Fatal("no min-contract fields on success")
	}
}
