package gctclient

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// kucoinAllTickersURL is KuCoin's public spot market summary endpoint. It
// returns 24h stats (vol, volValue, last, changeRate) for every spot pair in a
// single call — no authentication required.
const kucoinAllTickersURL = "https://api.kucoin.com/api/v1/market/allTickers"

// SpotTickerStat holds the 24h market stats needed for volume-based ranking.
type SpotTickerStat struct {
	Last      float64 // last traded price
	VolumeUSD float64 // 24h quote volume (volValue), already in quote currency (USDT)
	ChangePct float64 // 24h change as a percentage
}

// allTickersHTTP is a dedicated client for the public batch endpoint so its
// timeout is independent of the gRPC transport.
var allTickersHTTP = &http.Client{Timeout: 15 * time.Second}

// GetSpotTickerStats fetches 24h spot market stats for every pair on the
// exchange in a single batch call, keyed by canonical upper-case "BASE-QUOTE"
// symbol (e.g. "BTC-USDT").
//
// Why this exists: per-pair GetTicker (gRPC) only returns populated 24h volume
// for the handful of pairs GCT actively streams; every other pair comes back
// with volume=0, which silently fails the volume filter in the discovery
// funnel and collapses the universe to ~4 streamed majors. The batch endpoint
// returns real 24h volume for the full universe in one request (and is far
// faster than 500+ sequential ticker calls).
//
// Only KuCoin is supported today (the only exchange with live credentials).
// For any other exchange it returns an empty map so callers fall back to the
// per-pair path, preserving previous behavior.
func (c *Client) GetSpotTickerStats(ctx context.Context, exchange string) (map[string]SpotTickerStat, error) {
	if !strings.EqualFold(strings.TrimSpace(exchange), "kucoin") {
		return map[string]SpotTickerStat{}, nil
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, kucoinAllTickersURL, nil)
	if err != nil {
		return nil, fmt.Errorf("build allTickers request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "trading-mcp")

	resp, err := allTickersHTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("allTickers request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("allTickers HTTP %d", resp.StatusCode)
	}

	var body struct {
		Code string `json:"code"`
		Data struct {
			Ticker []struct {
				Symbol     string `json:"symbol"`
				Last       string `json:"last"`
				Vol        string `json:"vol"`        // 24h base volume
				VolValue   string `json:"volValue"`   // 24h quote volume (USDT)
				ChangeRate string `json:"changeRate"` // fraction, e.g. 0.0123 = +1.23%
			} `json:"ticker"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, fmt.Errorf("decode allTickers: %w", err)
	}
	if body.Code != "200000" {
		return nil, fmt.Errorf("allTickers API code %s", body.Code)
	}

	out := make(map[string]SpotTickerStat, len(body.Data.Ticker))
	for _, t := range body.Data.Ticker {
		sym := strings.ToUpper(strings.TrimSpace(t.Symbol))
		if sym == "" {
			continue
		}
		last := parseFloatSafe(t.Last)
		volUSD := parseFloatSafe(t.VolValue)
		// Fallback: derive quote volume from base volume × last when volValue is
		// missing or zero but the raw fields are present.
		if volUSD == 0 {
			if base := parseFloatSafe(t.Vol); base > 0 && last > 0 {
				volUSD = base * last
			}
		}
		out[sym] = SpotTickerStat{
			Last:      last,
			VolumeUSD: volUSD,
			ChangePct: parseFloatSafe(t.ChangeRate) * 100.0,
		}
	}
	return out, nil
}

// parseFloatSafe parses a string to float64, returning 0 on empty/invalid input.
func parseFloatSafe(s string) float64 {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0
	}
	return f
}
