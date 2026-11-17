package server

import (
	"encoding/json"
	"net/http"
	"strconv"
)

// RankedPairEntry is a single scored pair returned by GET /api/v1/pairs/ranked.
type RankedPairEntry struct {
	Pair     string  `json:"pair"`
	Exchange string  `json:"exchange,omitempty"`
	Score    float64 `json:"score"`
}

// RankedPairProvider abstracts the master-agent ranking for the public REST
// endpoint. Implement this with an adapter in main.go wrapping the master pair
// provider so this package stays cycle-free.
type RankedPairProvider interface {
	// TopRankedPairs returns pairs sorted by score descending, limited to n
	// entries. n <= 0 means "return all available".
	TopRankedPairs(n int) []RankedPairEntry
}

// SetRankedPairProvider wires a RankedPairProvider into the MCP server.
// Call this after the master agent is constructed in main.go.
func (s *MCPServer) SetRankedPairProvider(p RankedPairProvider) {
	s.rankProvider = p
}

// rankedPairsResponse is the response envelope for GET /api/v1/pairs/ranked.
type rankedPairsResponse struct {
	Pairs []RankedPairEntry `json:"pairs"`
	Count int               `json:"count"`
	Note  string            `json:"note,omitempty"`
}

// handleRankedPairs handles GET /api/v1/pairs/ranked.
//
// This is the public (no-JWT) mirror of the Gin route registered on the admin
// port :8081, which sits behind Kong JWT middleware and is therefore
// unreachable from the Flutter client. The shape matches the Gin handler.
//
// Query parameters:
//
//	limit - max entries to return (default 20, max 100)
func (s *MCPServer) handleRankedPairs(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		json.NewEncoder(w).Encode(map[string]string{"error": "method not allowed"})
		return
	}

	limit := 20
	if raw := r.URL.Query().Get("limit"); raw != "" {
		if v, err := strconv.Atoi(raw); err == nil && v > 0 {
			if v > 100 {
				v = 100
			}
			limit = v
		}
	}

	if s.rankProvider == nil {
		json.NewEncoder(w).Encode(rankedPairsResponse{
			Pairs: []RankedPairEntry{},
			Count: 0,
			Note:  "master agent not available",
		})
		return
	}

	pairs := s.rankProvider.TopRankedPairs(limit)
	if pairs == nil {
		pairs = []RankedPairEntry{}
	}

	json.NewEncoder(w).Encode(rankedPairsResponse{
		Pairs: pairs,
		Count: len(pairs),
	})
}
