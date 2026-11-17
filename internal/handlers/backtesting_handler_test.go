package handlers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/sirupsen/logrus"
)

type recordingMCP struct {
	calls  []string
	result map[string]interface{}
}

func (r *recordingMCP) CallTool(_ context.Context, name string, _ map[string]interface{}) (map[string]interface{}, error) {
	r.calls = append(r.calls, name)
	return r.result, nil
}

func TestRunBacktestUsesOnlyLiveToolsAndNoInventedData(t *testing.T) {
	mcp := &recordingMCP{result: map[string]interface{}{
		"roi": 3.0, "sharpe_ratio": 1.1, "max_drawdown": 2.0,
		"total_trades": 0.0, "winning_trades": 0.0, "losing_trades": 0.0,
	}}
	h := NewBacktestingHandler(mcp, logrus.New())

	body := `{"symbol":"BTC-USDT","strategy":"rsi_macd","start_date":"2026-01-01","end_date":"2026-02-01","optimize":true}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/backtesting/run-backtest", strings.NewReader(body))
	w := httptest.NewRecorder()
	h.RunBacktest(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	if len(mcp.calls) != 1 || mcp.calls[0] != "backtest_strategy" {
		t.Fatalf("only backtest_strategy may be called, got %v", mcp.calls)
	}
	out := w.Body.String()
	if strings.Contains(out, "4h 23m") {
		t.Fatalf("response contains the hardcoded duration: %s", out)
	}
	if !strings.Contains(out, "optimization is not available") {
		t.Fatalf("optimize request must carry an honest warning: %s", out)
	}
}
