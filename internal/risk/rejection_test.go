package risk

import (
	"errors"
	"fmt"
	"testing"
)

func TestRejectionFrom(t *testing.T) {
	typed := NewRejectedError(&RiskDecision{Code: CodeOrderCapExceeded, Reason: "ORDER_CAP_EXCEEDED: order notional $64.68 exceeds the per-order cap $2.00"})
	if typed.Error() != "order rejected by risk sentinel: ORDER_CAP_EXCEEDED: order notional $64.68 exceeds the per-order cap $2.00" {
		t.Fatalf("message changed: %q", typed.Error())
	}
	for name, err := range map[string]error{
		"typed":   typed,
		"wrapped": fmt.Errorf("submit failed: %w", typed),
		"text":    fmt.Errorf("submit failed: %v", typed),
	} {
		re, ok := RejectionFrom(err)
		if !ok || re.Code != CodeOrderCapExceeded {
			t.Errorf("%s: got %+v ok=%v", name, re, ok)
		}
	}
	if re, ok := RejectionFrom(NewRejectedError(&RiskDecision{Reason: "kill switch active"})); !ok || re.Code != CodeRiskRejected {
		t.Errorf("generic rejection: %+v ok=%v", re, ok)
	}
	if _, ok := RejectionFrom(errors.New("network down")); ok {
		t.Error("unrelated error taken as a rejection")
	}
}
