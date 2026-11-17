package gctclient

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/rantipay/trading-mcp/internal/mcp-trading/models"
)

// kucoinStatsURL is KuCoin's public 24h statistics endpoint for one spot pair.
// It is a variable so tests can point it at a local server.
var kucoinStatsURL = "https://api.kucoin.com/api/v1/market/stats"

var statsHTTP = &http.Client{Timeout: 5 * time.Second}

// GetTicker24h returns the ticker with its 24h statistics filled in.
//
// GoCryptoTrader's KuCoin ticker carries no 24h high/low and reports the last
// trade size as volume, and the change was never computed. Callers that show
// 24h statistics (get_ticker, get_24h_stats) would otherwise publish zeros as
// if they were market data. For a KuCoin spot pair whose 24h fields are
// missing, they are taken from the public stats endpoint. If that call fails
// the ticker is returned as it came, with the zeros, never with guessed values.
func (c *Client) GetTicker24h(ctx context.Context, exchange, pair string) (*models.Ticker, error) {
	t, err := c.GetTicker(ctx, exchange, pair)
	if err != nil {
		return nil, err
	}
	if needs24hStats(exchange, pair, t) {
		if s, err := fetchKuCoinStats(ctx, pair); err == nil {
			apply24hStats(t, s)
		} else if c.log != nil {
			c.log.WithError(err).WithField("pair", pair).Debug("24h stats unavailable; ticker keeps GCT values")
		}
	}
	return t, nil
}

// needs24hStats reports whether the ticker lacks 24h data that the KuCoin spot
// stats endpoint can provide (futures symbols such as XBTUSDTM are not spot).
func needs24hStats(exchange, pair string, t *models.Ticker) bool {
	if t == nil || !strings.EqualFold(strings.TrimSpace(exchange), "kucoin") {
		return false
	}
	if !strings.Contains(pair, "-") {
		return false
	}
	return t.High24h == 0 || t.Low24h == 0 || t.Change24h == 0
}

type kucoinStats struct {
	Vol, High, Low, ChangeRate float64
}

func fetchKuCoinStats(ctx context.Context, pair string) (kucoinStats, error) {
	u := kucoinStatsURL + "?symbol=" + url.QueryEscape(strings.ToUpper(strings.TrimSpace(pair)))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return kucoinStats{}, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "trading-mcp")
	resp, err := statsHTTP.Do(req)
	if err != nil {
		return kucoinStats{}, fmt.Errorf("stats request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return kucoinStats{}, fmt.Errorf("stats HTTP %d", resp.StatusCode)
	}
	var body struct {
		Code string `json:"code"`
		Data struct {
			Vol        string `json:"vol"` // 24h base volume
			High       string `json:"high"`
			Low        string `json:"low"`
			ChangeRate string `json:"changeRate"` // fraction, 0.0123 = +1.23%
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return kucoinStats{}, fmt.Errorf("decode stats: %w", err)
	}
	if body.Code != "200000" {
		return kucoinStats{}, fmt.Errorf("stats API code %s", body.Code)
	}
	s := kucoinStats{
		Vol:        parseFloatSafe(body.Data.Vol),
		High:       parseFloatSafe(body.Data.High),
		Low:        parseFloatSafe(body.Data.Low),
		ChangeRate: parseFloatSafe(body.Data.ChangeRate),
	}
	// An unknown symbol answers 200000 with empty fields.
	if s.High == 0 || s.Low == 0 {
		return kucoinStats{}, fmt.Errorf("stats for %s are empty", pair)
	}
	return s, nil
}

// apply24hStats sets volume (24h base volume), high, low and change (percent).
func apply24hStats(t *models.Ticker, s kucoinStats) {
	t.Volume = s.Vol
	t.High24h = s.High
	t.Low24h = s.Low
	t.Change24h = s.ChangeRate * 100
}
