package xsec

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/rantipay/trading-mcp/internal/ai"
	"github.com/rantipay/trading-mcp/internal/storage"
)

// defaultKlineURL is the public KuCoin USDTM futures kline endpoint. Like the
// contracts endpoint it requires no authentication. granularity is in minutes
// (1440 = 1 day); rows are [timeMs, open, high, low, close, volume, ...].
const defaultKlineURL = "https://api-futures.kucoin.com/api/v1/kline/query"

// klineResponse mirrors the KuCoin kline payload.
type klineResponse struct {
	Code string      `json:"code"`
	Data [][]float64 `json:"data"`
}

// EnsureDailyHistory guarantees each pair has at least minBars daily bars in
// the candle_archive with a reasonably fresh newest bar, backfilling gaps from
// the public kline endpoint. This closes the structural gap that starved the
// ranker: the archiver only covers pairs it historically tracked, so pairs
// that BECOME liquid (new listings, turnover growth) had zero daily history
// and silently shrank the rankable universe below 2k (observed 2026-07-23:
// 23 liquid pairs, only 19 scoreable -> "not enough pairs ... for k=10").
//
// Per-pair failures are non-fatal (logged; the ranker just omits that pair),
// so a transient exchange hiccup degrades the universe instead of aborting.
// Returns how many pairs were backfilled.
func (u *Universe) EnsureDailyHistory(ctx context.Context, db *sql.DB, pairs []string, minBars int, log *logrus.Logger) int {
	repo := storage.NewCandleArchiveRepository(db, log)
	// Fresh enough = newest closed bar is yesterday's. 36h tolerates the
	// UTC-midnight boundary plus archiver stamp skew without refetching
	// pairs that are already current.
	staleBefore := time.Now().Add(-36 * time.Hour)
	backfilled := 0
	for _, p := range pairs {
		count, _, newest, err := repo.Coverage(p, "1d")
		if err == nil && count >= minBars && newest.After(staleBefore) {
			continue
		}
		candles, err := u.fetchDailyKlines(ctx, p, minBars)
		if err != nil {
			log.WithError(err).WithField("pair", p).
				Warn("daily kline backfill failed — pair may be omitted from ranking")
			continue
		}
		if len(candles) == 0 {
			continue
		}
		if _, err := repo.Save(p, "1d", candles); err != nil {
			log.WithError(err).WithField("pair", p).Warn("saving backfilled candles failed")
			continue
		}
		backfilled++
	}
	return backfilled
}

// fetchDailyKlines fetches closed daily candles for a "BASE-USDT" pair from
// the public futures kline endpoint. The still-forming bar for the current
// UTC day is dropped so momentum is always computed over closed bars, matching
// what the archiver stores.
func (u *Universe) fetchDailyKlines(ctx context.Context, pair string, minBars int) ([]ai.Candle, error) {
	base := strings.TrimSuffix(pair, "-USDT")
	if base == pair || base == "" {
		return nil, fmt.Errorf("pair %q is not a BASE-USDT pair", pair)
	}
	// Inverse of parseLiquid's mapping: BTC is listed as XBT on KuCoin futures.
	if base == "BTC" {
		base = "XBT"
	}
	symbol := base + "USDTM"

	// A few extra days of margin so the response still yields minBars closed
	// bars after dropping today's forming bar.
	from := time.Now().AddDate(0, 0, -(minBars + 5)).UnixMilli()
	url := fmt.Sprintf("%s?symbol=%s&granularity=1440&from=%d", u.klineURL, symbol, from)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	resp, err := u.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch klines: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status %d from %s", resp.StatusCode, u.klineURL)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read body: %w", err)
	}
	return parseDailyKlines(body, time.Now())
}

// parseDailyKlines is the pure decode+filter core of fetchDailyKlines, split
// out for testability. now determines the current UTC day whose forming bar
// is excluded.
func parseDailyKlines(data []byte, now time.Time) ([]ai.Candle, error) {
	var parsed klineResponse
	if err := json.Unmarshal(data, &parsed); err != nil {
		return nil, fmt.Errorf("decode klines: %w", err)
	}
	todayStart := now.UTC().Truncate(24 * time.Hour)
	out := make([]ai.Candle, 0, len(parsed.Data))
	for _, row := range parsed.Data {
		if len(row) < 6 {
			continue
		}
		ts := time.UnixMilli(int64(row[0]))
		if !ts.Before(todayStart) {
			continue // still-forming bar for the current UTC day
		}
		out = append(out, ai.Candle{
			Timestamp: ts,
			Open:      row[1],
			High:      row[2],
			Low:       row[3],
			Close:     row[4],
			Volume:    row[5],
		})
	}
	return out, nil
}
