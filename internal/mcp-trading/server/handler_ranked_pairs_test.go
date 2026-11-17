package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// fakeRankProvider is a deterministic RankedPairProvider for tests.
type fakeRankProvider struct {
	entries []RankedPairEntry
}

func (f *fakeRankProvider) TopRankedPairs(n int) []RankedPairEntry {
	if n > 0 && n < len(f.entries) {
		return f.entries[:n]
	}
	return f.entries
}

// newBareServer returns an MCPServer with only the fields the ranked-pairs
// handler touches, avoiding the full NewMCPServer construction path.
func newBareServer() *MCPServer {
	return &MCPServer{}
}

func TestHandleRankedPairs_NoProvider(t *testing.T) {
	s := newBareServer()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/pairs/ranked", nil)
	rec := httptest.NewRecorder()

	s.handleRankedPairs(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	var resp rankedPairsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Count != 0 || len(resp.Pairs) != 0 {
		t.Fatalf("expected empty list, got count=%d len=%d", resp.Count, len(resp.Pairs))
	}
	if resp.Note == "" {
		t.Fatal("expected a note when provider is nil")
	}
}

func TestHandleRankedPairs_WithProvider(t *testing.T) {
	s := newBareServer()
	s.SetRankedPairProvider(&fakeRankProvider{entries: []RankedPairEntry{
		{Pair: "BTC-USDT", Exchange: "kucoin", Score: 0.91},
		{Pair: "ETH-USDT", Exchange: "kucoin", Score: 0.77},
	}})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/pairs/ranked", nil)
	rec := httptest.NewRecorder()
	s.handleRankedPairs(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	var resp rankedPairsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Count != 2 || len(resp.Pairs) != 2 {
		t.Fatalf("expected 2 pairs, got count=%d len=%d", resp.Count, len(resp.Pairs))
	}
	if resp.Pairs[0].Pair != "BTC-USDT" || resp.Pairs[0].Score != 0.91 {
		t.Fatalf("unexpected first pair: %+v", resp.Pairs[0])
	}
}

func TestHandleRankedPairs_LimitClamped(t *testing.T) {
	s := newBareServer()
	entries := make([]RankedPairEntry, 5)
	for i := range entries {
		entries[i] = RankedPairEntry{Pair: "P", Score: float64(5 - i)}
	}
	s.SetRankedPairProvider(&fakeRankProvider{entries: entries})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/pairs/ranked?limit=2", nil)
	rec := httptest.NewRecorder()
	s.handleRankedPairs(rec, req)

	var resp rankedPairsResponse
	json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp.Count != 2 {
		t.Fatalf("expected limit=2 to return 2, got %d", resp.Count)
	}
}

func TestHandleRankedPairs_MethodNotAllowed(t *testing.T) {
	s := newBareServer()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/pairs/ranked", nil)
	rec := httptest.NewRecorder()
	s.handleRankedPairs(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", rec.Code)
	}
}
