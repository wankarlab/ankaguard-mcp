package server

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"time"
)

// This is the public (no-JWT) mirror of GET /api/v1/xsec/status on the Gin
// admin port :8081 (behind Kong JWT) — same pattern as /api/v1/pairs/ranked.
// Read-only paper-strategy stats, no sensitive data. Every section degrades to
// an empty array when its table does not exist yet, never errors.

// SetXSecDB wires the shared SQLite handle (data/trading.db) into the MCP
// server so the public xsec status endpoint can read the xsec_* tables.
// database/sql is stdlib, so unlike the storage repos no adapter is needed.
func (s *MCPServer) SetXSecDB(db *sql.DB) {
	s.xsecDB = db
}

// xsecEquityPoint is one row of the xsec_equity curve.
type xsecEquityPoint struct {
	Ts     time.Time `json:"ts"`
	Equity float64   `json:"equity"`
	NLong  int       `json:"n_long"`
	NShort int       `json:"n_short"`
}

// xsecPosition is one open paper position (current target basket leg).
type xsecPosition struct {
	Pair     string    `json:"pair"`
	Dir      int       `json:"dir"` // +1 long, -1 short
	Entry    float64   `json:"entry"`
	Notional float64   `json:"notional"`
	OpenedAt time.Time `json:"opened_at"`
}

// xsecParam is one key/value row from xsec_params.
type xsecParam struct {
	Key       string    `json:"key"`
	Value     string    `json:"value"`
	UpdatedAt time.Time `json:"updated_at"`
}

// xsecShadowBasket is one row of xsec_shadow_baskets (payload kept as raw text;
// the table is owned by the research tooling and its schema may evolve).
type xsecShadowBasket struct {
	Ts      time.Time `json:"ts"`
	Payload string    `json:"payload"`
}

// xsecStatusPayload mirrors the Gin handler's response shape.
type xsecStatusPayload struct {
	EquityHistory []xsecEquityPoint  `json:"equity_history"`
	Positions     []xsecPosition     `json:"positions"`
	Params        []xsecParam        `json:"params"`
	Shadow        []xsecShadowBasket `json:"shadow"`
	UpdatedAt     time.Time          `json:"updated_at"`
}

// handleXSecStatus handles GET /api/v1/xsec/status on the public mux.
func (s *MCPServer) handleXSecStatus(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		json.NewEncoder(w).Encode(map[string]string{"error": "method not allowed"})
		return
	}

	resp := xsecStatusPayload{
		EquityHistory: []xsecEquityPoint{},
		Positions:     []xsecPosition{},
		Params:        []xsecParam{},
		Shadow:        []xsecShadowBasket{},
		UpdatedAt:     time.Now().UTC(),
	}
	db := s.xsecDB
	if db == nil {
		json.NewEncoder(w).Encode(map[string]interface{}{"success": true, "data": resp})
		return
	}
	ctx := r.Context()

	// Equity: last 26 rows (≈ half a year of weekly rebalances), oldest first.
	if rows, err := db.QueryContext(ctx,
		`SELECT ts, equity, n_long, n_short FROM
		   (SELECT ts, equity, n_long, n_short FROM xsec_equity ORDER BY ts DESC LIMIT 26)
		 ORDER BY ts ASC`); err == nil {
		for rows.Next() {
			var p xsecEquityPoint
			if rows.Scan(&p.Ts, &p.Equity, &p.NLong, &p.NShort) == nil {
				resp.EquityHistory = append(resp.EquityHistory, p)
			}
		}
		rows.Close()
	}

	// Current basket.
	if rows, err := db.QueryContext(ctx,
		`SELECT pair, dir, entry, notional, opened_at FROM xsec_positions ORDER BY dir DESC, pair`); err == nil {
		for rows.Next() {
			var p xsecPosition
			if rows.Scan(&p.Pair, &p.Dir, &p.Entry, &p.Notional, &p.OpenedAt) == nil {
				resp.Positions = append(resp.Positions, p)
			}
		}
		rows.Close()
	}

	// Strategy params (key/value).
	if rows, err := db.QueryContext(ctx,
		`SELECT key, value, updated_at FROM xsec_params ORDER BY key`); err == nil {
		for rows.Next() {
			var p xsecParam
			if rows.Scan(&p.Key, &p.Value, &p.UpdatedAt) == nil {
				resp.Params = append(resp.Params, p)
			}
		}
		rows.Close()
	}

	// Shadow baskets: last 8 rows, newest first.
	if rows, err := db.QueryContext(ctx,
		`SELECT ts, payload FROM xsec_shadow_baskets ORDER BY ts DESC LIMIT 8`); err == nil {
		for rows.Next() {
			var b xsecShadowBasket
			if rows.Scan(&b.Ts, &b.Payload) == nil {
				resp.Shadow = append(resp.Shadow, b)
			}
		}
		rows.Close()
	}

	json.NewEncoder(w).Encode(map[string]interface{}{"success": true, "data": resp})
}
