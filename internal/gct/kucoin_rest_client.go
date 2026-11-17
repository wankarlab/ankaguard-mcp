package gct

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// KuCoinRESTClient cliente REST directo para KuCoin
type KuCoinRESTClient struct {
	httpClient       *http.Client
	baseURL          string
	futuresURL       string
	apiKey           string
	apiSecret        string
	apiPassword      string
	userID           string                    // H0.3.7: user context for failure recording
	exchange         string                    // H0.3.7: exchange name for failure recording
	credFailRecorder CredentialFailureRecorder // H0.3.7: optional; can be nil
}

// NewKuCoinRESTClient crea un nuevo cliente REST para KuCoin
func NewKuCoinRESTClient() *KuCoinRESTClient {
	return &KuCoinRESTClient{
		httpClient: &http.Client{
			Timeout: 60 * time.Second,
		},
		baseURL:    "https://api.kucoin.com",
		futuresURL: "https://api-futures.kucoin.com",
	}
}

// SetUserContext H0.3.7: configure user and exchange context for failure recording
func (k *KuCoinRESTClient) SetUserContext(userID, exchange string) {
	k.userID = userID
	k.exchange = exchange
}

// SetCredentialFailureRecorder H0.3.7: set the credential failure recorder
func (k *KuCoinRESTClient) SetCredentialFailureRecorder(recorder CredentialFailureRecorder) {
	k.credFailRecorder = recorder
}

// SetCredentials configura las credenciales de API
func (k *KuCoinRESTClient) SetCredentials(apiKey, apiSecret, apiPassword string) {
	k.apiKey = apiKey
	k.apiSecret = apiSecret
	k.apiPassword = apiPassword
}

// HasCredentials verifica si las credenciales de API están configuradas
func (k *KuCoinRESTClient) HasCredentials() bool {
	return k.apiKey != "" && k.apiSecret != "" && k.apiPassword != ""
}

// KuCoinTickerResponse respuesta del ticker de KuCoin
type KuCoinTickerResponse struct {
	Code string `json:"code"`
	Data struct {
		Time     int64  `json:"time"`
		Sequence string `json:"sequence"`
		Price    string `json:"price"`
		Size     string `json:"size"`
		BestBid  string `json:"bestBid"`
		BestAsk  string `json:"bestAsk"`
	} `json:"data"`
}

// KuCoinCandlesResponse respuesta de candles de KuCoin
type KuCoinCandlesResponse struct {
	Code string     `json:"code"`
	Data [][]string `json:"data"`
}

// GetTicker obtiene el ticker actual de un par (spot o futures)
func (k *KuCoinRESTClient) GetTicker(ctx context.Context, symbol string) (*Ticker, error) {
	// Detectar si es futures (termina en M o tiene USDT en el nombre sin guión)
	isFutures := strings.HasSuffix(symbol, "M") || strings.Contains(symbol, "USDT") && !strings.Contains(symbol, "-")

	if isFutures {
		return k.GetFuturesTicker(ctx, symbol)
	}

	url := fmt.Sprintf("%s/api/v1/market/orderbook/level1?symbol=%s", k.baseURL, symbol)

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("User-Agent", "GoCryptoTrader-REST-Client")
	req.Header.Set("Content-Type", "application/json")

	resp, err := k.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to make request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, resp.Status)
	}

	var tickerResp KuCoinTickerResponse
	if err := json.NewDecoder(resp.Body).Decode(&tickerResp); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	if tickerResp.Code != "200000" {
		return nil, fmt.Errorf("KuCoin API error: %s", tickerResp.Code)
	}

	// Convertir strings a float64
	price, err := parseFloat(tickerResp.Data.Price)
	if err != nil {
		return nil, fmt.Errorf("failed to parse price: %w", err)
	}

	bid, err := parseFloat(tickerResp.Data.BestBid)
	if err != nil {
		return nil, fmt.Errorf("failed to parse bid: %w", err)
	}

	ask, err := parseFloat(tickerResp.Data.BestAsk)
	if err != nil {
		return nil, fmt.Errorf("failed to parse ask: %w", err)
	}

	return &Ticker{
		Exchange:  "kucoin",
		Pair:      symbol,
		Last:      price,
		Bid:       bid,
		Ask:       ask,
		Timestamp: time.Unix(tickerResp.Data.Time/1000, 0),
	}, nil
}

// GetFuturesTicker obtiene el ticker de un contrato de futures
func (k *KuCoinRESTClient) GetFuturesTicker(ctx context.Context, symbol string) (*Ticker, error) {
	// KuCoin Futures API usa un endpoint diferente
	futuresBaseURL := strings.Replace(k.baseURL, "https://api.kucoin.com", "https://api-futures.kucoin.com", 1)
	url := fmt.Sprintf("%s/api/v1/ticker?symbol=%s", futuresBaseURL, symbol)

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("User-Agent", "GoCryptoTrader-REST-Client")
	req.Header.Set("Content-Type", "application/json")

	resp, err := k.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to make request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("HTTP %d: %s - %s", resp.StatusCode, resp.Status, string(body))
	}

	var futuresResp struct {
		Code string `json:"code"`
		Data struct {
			Symbol       string `json:"symbol"`
			Sequence     int64  `json:"sequence"`
			Side         string `json:"side"`
			Size         int64  `json:"size"`
			Price        string `json:"price"`
			BestBidSize  int64  `json:"bestBidSize"`
			BestBidPrice string `json:"bestBidPrice"`
			BestAskPrice string `json:"bestAskPrice"`
			BestAskSize  int64  `json:"bestAskSize"`
			TradeId      string `json:"tradeId"`
			Ts           int64  `json:"ts"`
		} `json:"data"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&futuresResp); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	if futuresResp.Code != "200000" {
		return nil, fmt.Errorf("KuCoin Futures API error: %s", futuresResp.Code)
	}

	// Convertir strings a float64
	price, err := parseFloat(futuresResp.Data.Price)
	if err != nil {
		return nil, fmt.Errorf("failed to parse price '%s': %w", futuresResp.Data.Price, err)
	}

	bid, err := parseFloat(futuresResp.Data.BestBidPrice)
	if err != nil {
		return nil, fmt.Errorf("failed to parse bid '%s': %w", futuresResp.Data.BestBidPrice, err)
	}

	ask, err := parseFloat(futuresResp.Data.BestAskPrice)
	if err != nil {
		return nil, fmt.Errorf("failed to parse ask '%s': %w", futuresResp.Data.BestAskPrice, err)
	}

	// Validar que tengamos al menos un precio válido
	if price <= 0 && bid <= 0 && ask <= 0 {
		return nil, fmt.Errorf("all prices are zero or invalid for %s: price=%s, bid=%s, ask=%s",
			symbol, futuresResp.Data.Price, futuresResp.Data.BestBidPrice, futuresResp.Data.BestAskPrice)
	}

	// Si el precio last es 0, usar el mid price (bid+ask)/2
	if price <= 0 {
		if bid > 0 && ask > 0 {
			price = (bid + ask) / 2
		} else if bid > 0 {
			price = bid
		} else if ask > 0 {
			price = ask
		}
	}

	return &Ticker{
		Exchange:  "kucoin",
		Pair:      symbol,
		Last:      price,
		Bid:       bid,
		Ask:       ask,
		Timestamp: time.Unix(futuresResp.Data.Ts/1000000000, 0), // Ts está en nanosegundos
	}, nil
}

// GetContractMultiplier returns the face value (base units per contract) for a futures symbol.
// This is a public endpoint — no auth required.
// Example: NEARUSDTM → 1.0, BTCUSDTM → 0.001, DOGEUSDTM → 10.0
func (k *KuCoinRESTClient) GetContractMultiplier(ctx context.Context, symbol string) (float64, error) {
	url := fmt.Sprintf("%s/api/v1/contracts/%s", k.futuresURL, symbol)

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return 0, fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := k.httpClient.Do(req)
	if err != nil {
		return 0, fmt.Errorf("failed to make request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return 0, fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(body))
	}

	var result struct {
		Code string `json:"code"`
		Data struct {
			Multiplier float64 `json:"multiplier"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return 0, fmt.Errorf("failed to decode response: %w", err)
	}
	if result.Code != "200000" {
		return 0, fmt.Errorf("KuCoin API error code: %s", result.Code)
	}
	if result.Data.Multiplier <= 0 {
		return 0, fmt.Errorf("invalid multiplier %.6f for %s", result.Data.Multiplier, symbol)
	}
	return result.Data.Multiplier, nil
}

// GetCandles obtiene candles históricos
func (k *KuCoinRESTClient) GetCandles(ctx context.Context, symbol, interval string, limit int) ([]*Candle, error) {
	// Detectar si es futures (termina en M)
	isFutures := strings.HasSuffix(symbol, "M")

	// Convertir intervalo a formato de KuCoin
	kucoinInterval := convertInterval(interval)
	if kucoinInterval == "" {
		return nil, fmt.Errorf("unsupported interval: %s", interval)
	}

	// Usar endpoint correcto según el tipo de mercado
	baseURL := k.baseURL
	if isFutures {
		baseURL = k.futuresURL
		// Para futures, el endpoint es diferente
		return k.GetFuturesCandles(ctx, symbol, interval, limit)
	}

	url := fmt.Sprintf("%s/api/v1/market/candles?symbol=%s&type=%s", baseURL, symbol, kucoinInterval)

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("User-Agent", "GoCryptoTrader-REST-Client")
	req.Header.Set("Content-Type", "application/json")

	resp, err := k.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to make request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, resp.Status)
	}

	var candlesResp KuCoinCandlesResponse
	if err := json.NewDecoder(resp.Body).Decode(&candlesResp); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	if candlesResp.Code != "200000" {
		return nil, fmt.Errorf("KuCoin API error: %s", candlesResp.Code)
	}

	// Convertir datos a candles
	candles := make([]*Candle, 0, len(candlesResp.Data))
	for _, data := range candlesResp.Data {
		if len(data) < 6 {
			continue
		}

		timestamp, err := parseFloat(data[0])
		if err != nil {
			continue
		}

		open, err := parseFloat(data[1])
		if err != nil {
			continue
		}

		close, err := parseFloat(data[2])
		if err != nil {
			continue
		}

		high, err := parseFloat(data[3])
		if err != nil {
			continue
		}

		low, err := parseFloat(data[4])
		if err != nil {
			continue
		}

		volume, err := parseFloat(data[5])
		if err != nil {
			continue
		}

		candles = append(candles, &Candle{
			Timestamp: time.Unix(int64(timestamp), 0),
			Open:      open,
			High:      high,
			Low:       low,
			Close:     close,
			Volume:    volume,
		})

		// Limitar número de candles si se especifica
		if limit > 0 && len(candles) >= limit {
			break
		}
	}

	// Normalise to ascending (oldest→newest). KuCoin spot returns NEWEST-FIRST;
	// the loop above collected the newest `limit` candles still in newest-first
	// order. The whole stack (RSI/MACD/decision/backtest/trend filter) expects
	// candles[len-1] to be the CURRENT candle, and the futures (line ~483) and
	// backfill (line ~564) paths already reverse — this brings the spot path in
	// line so EVERY GetCandles source returns the same ascending contract.
	// Omitting this made the bot analyse a ~lookback-hour-stale candle as "now"
	// on REST-only pairs (HYPE shorted reading 66.25 vs live 72.5, 2026-06-01).
	for i, j := 0, len(candles)-1; i < j; i, j = i+1, j-1 {
		candles[i], candles[j] = candles[j], candles[i]
	}

	return candles, nil
}

// GetFuturesCandles obtiene candles históricos de futures
func (k *KuCoinRESTClient) GetFuturesCandles(ctx context.Context, symbol, interval string, limit int) ([]*Candle, error) {
	// Convertir símbolo: DOGE-USDTM -> DOGEUSDTM (quitar guiones)
	futuresSymbol := strings.ReplaceAll(symbol, "-", "")

	// Convertir intervalo a granularidad en minutos para futures
	granularity := convertIntervalToGranularity(interval)
	if granularity == 0 {
		return nil, fmt.Errorf("unsupported interval for futures: %s", interval)
	}

	// Calcular timestamps en milisegundos
	endTime := time.Now().UnixMilli()
	startTime := endTime - int64(granularity*60*limit*1000)

	url := fmt.Sprintf("%s/api/v1/kline/query?symbol=%s&granularity=%d&from=%d&to=%d",
		k.futuresURL, futuresSymbol, granularity, startTime, endTime)

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("User-Agent", "GoCryptoTrader-REST-Client")
	req.Header.Set("Content-Type", "application/json")

	resp, err := k.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to make request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("HTTP %d: %s - %s", resp.StatusCode, resp.Status, string(body))
	}

	var futuresCandlesResp struct {
		Code string          `json:"code"`
		Data [][]interface{} `json:"data"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&futuresCandlesResp); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	if futuresCandlesResp.Code != "200000" {
		return nil, fmt.Errorf("KuCoin Futures API error: %s", futuresCandlesResp.Code)
	}

	// Convertir datos a candles
	candles := make([]*Candle, 0, len(futuresCandlesResp.Data))
	for _, data := range futuresCandlesResp.Data {
		if len(data) < 6 {
			continue
		}

		// Formato: [timestamp, open, high, low, close, volume]
		timestamp, ok := data[0].(float64)
		if !ok {
			continue
		}

		open, ok := data[1].(float64)
		if !ok {
			continue
		}

		high, ok := data[2].(float64)
		if !ok {
			continue
		}

		low, ok := data[3].(float64)
		if !ok {
			continue
		}

		close, ok := data[4].(float64)
		if !ok {
			continue
		}

		volume, ok := data[5].(float64)
		if !ok {
			continue
		}

		candles = append(candles, &Candle{
			Timestamp: time.Unix(int64(timestamp), 0),
			Open:      open,
			High:      high,
			Low:       low,
			Close:     close,
			Volume:    volume,
		})
	}

	// Invertir el orden (KuCoin devuelve más reciente primero)
	for i, j := 0, len(candles)-1; i < j; i, j = i+1, j-1 {
		candles[i], candles[j] = candles[j], candles[i]
	}

	// Limitar si es necesario
	if limit > 0 && len(candles) > limit {
		candles = candles[len(candles)-limit:]
	}

	return candles, nil
}

// GetFuturesCandlesRange fetches futures candles for an explicit [from, to] window.
// KuCoin caps each call at ~200 candles, so callers that need a wider window must
// paginate (see BackfillFuturesCandles). Timestamps from the futures kline endpoint
// are in MILLISECONDS and parsed as such here (the limit-based GetFuturesCandles
// mis-parses them as seconds; this method does it correctly for archival use).
func (k *KuCoinRESTClient) GetFuturesCandlesRange(ctx context.Context, symbol, interval string, from, to time.Time) ([]*Candle, error) {
	// KuCoin futures symbols have no separator and a trailing "M" (e.g.
	// "WLD-USDT" → "WLDUSDTM"); without the M the kline endpoint returns empty.
	futuresSymbol := strings.ReplaceAll(strings.ReplaceAll(symbol, "-", ""), "_", "")
	if !strings.HasSuffix(futuresSymbol, "M") {
		futuresSymbol += "M"
	}
	// KuCoin's BTC perpetual is XBTUSDTM (not BTCUSDTM) — without this mapping
	// the kline endpoint silently returns an empty list for BTC-USDT.
	if futuresSymbol == "BTCUSDTM" {
		futuresSymbol = "XBTUSDTM"
	}
	granularity := convertIntervalToGranularity(interval)
	if granularity == 0 {
		return nil, fmt.Errorf("unsupported interval for futures: %s", interval)
	}

	url := fmt.Sprintf("%s/api/v1/kline/query?symbol=%s&granularity=%d&from=%d&to=%d",
		k.futuresURL, futuresSymbol, granularity, from.UnixMilli(), to.UnixMilli())

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("User-Agent", "GoCryptoTrader-REST-Client")
	req.Header.Set("Content-Type", "application/json")

	resp, err := k.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to make request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(body))
	}

	var out struct {
		Code string          `json:"code"`
		Data [][]interface{} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}
	if out.Code != "200000" {
		return nil, fmt.Errorf("KuCoin Futures API error: %s", out.Code)
	}

	candles := make([]*Candle, 0, len(out.Data))
	for _, d := range out.Data {
		if len(d) < 6 {
			continue
		}
		ts, ok0 := d[0].(float64)
		o, ok1 := d[1].(float64)
		h, ok2 := d[2].(float64)
		l, ok3 := d[3].(float64)
		c, ok4 := d[4].(float64)
		v, ok5 := d[5].(float64)
		if !(ok0 && ok1 && ok2 && ok3 && ok4 && ok5) {
			continue
		}
		candles = append(candles, &Candle{
			Timestamp: time.UnixMilli(int64(ts)),
			Open:      o, High: h, Low: l, Close: c, Volume: v,
		})
	}
	// KuCoin returns newest-first; normalise to ascending.
	for i, j := 0, len(candles)-1; i < j; i, j = i+1, j-1 {
		candles[i], candles[j] = candles[j], candles[i]
	}
	return candles, nil
}

// BackfillFuturesCandles fetches the full [from, to] window by paginating around
// KuCoin's ~200-candle-per-call cap. It walks forward from `from` in page-sized
// time steps, merging and de-duplicating into a single ascending slice. A small
// pause between pages keeps it under KuCoin's rate limit. Returns whatever it
// collected even if a page errors (best-effort backfill).
func (k *KuCoinRESTClient) BackfillFuturesCandles(ctx context.Context, symbol, interval string, from, to time.Time) ([]*Candle, error) {
	granularity := convertIntervalToGranularity(interval)
	if granularity == 0 {
		return nil, fmt.Errorf("unsupported interval: %s", interval)
	}
	const pageCandles = 200
	pageSpan := time.Duration(granularity) * time.Minute * pageCandles

	seen := make(map[int64]struct{})
	var all []*Candle
	cursor := from
	for cursor.Before(to) {
		pageEnd := cursor.Add(pageSpan)
		if pageEnd.After(to) {
			pageEnd = to
		}
		page, err := k.GetFuturesCandlesRange(ctx, symbol, interval, cursor, pageEnd)
		if err != nil {
			// Best-effort: stop on error but keep what we have.
			if len(all) == 0 {
				return nil, err
			}
			break
		}
		for _, c := range page {
			key := c.Timestamp.Unix()
			if _, dup := seen[key]; dup {
				continue
			}
			seen[key] = struct{}{}
			all = append(all, c)
		}
		// Advance past this page. If KuCoin returned nothing, jump a full page to
		// avoid an infinite loop on gaps (e.g. pre-listing periods).
		if len(page) > 0 {
			last := page[len(page)-1].Timestamp
			if last.After(cursor) {
				cursor = last.Add(time.Duration(granularity) * time.Minute)
			} else {
				cursor = pageEnd
			}
		} else {
			cursor = pageEnd
		}
		select {
		case <-ctx.Done():
			return all, ctx.Err()
		case <-time.After(120 * time.Millisecond):
		}
	}
	return all, nil
}

// convertIntervalToGranularity convierte intervalos a granularidad en minutos para futures
func convertIntervalToGranularity(interval string) int {
	switch interval {
	case "1", "1m":
		return 1
	case "5", "5m":
		return 5
	case "15", "15m":
		return 15
	case "30", "30m":
		return 30
	case "60", "1h":
		return 60
	case "120", "2h":
		return 120
	case "240", "4h":
		return 240
	case "480", "8h":
		return 480
	case "720", "12h":
		return 720
	case "1440", "1d", "24h":
		return 1440
	default:
		return 0
	}
}

// convertInterval convierte intervalos comunes al formato de KuCoin
func convertInterval(interval string) string {
	switch interval {
	case "1", "1m":
		return "1min"
	case "3", "3m":
		return "3min"
	case "5", "5m":
		return "5min"
	case "15", "15m":
		return "15min"
	case "30", "30m":
		return "30min"
	case "60", "1h":
		return "1hour"
	case "120", "2h":
		return "2hour"
	case "240", "4h":
		return "4hour"
	case "360", "6h":
		return "6hour"
	case "480", "8h":
		return "8hour"
	case "720", "12h":
		return "12hour"
	case "1440", "1d":
		return "1day"
	case "10080", "1w":
		return "1week"
	default:
		return ""
	}
}

// futuresGranularityOrder and spotGranularityOrder are the canonical ascending
// ladders of timeframes each market supports, derived from the two converters
// above (convertIntervalToGranularity for futures, convertInterval for spot).
// They are the single source of truth for any feature that must pick a
// supported timeframe (e.g. the exit-confirmation TF ladder). Futures notably
// does NOT support 3m / 6h / 1w.
var (
	futuresGranularityOrder = []string{"1m", "5m", "15m", "30m", "1h", "2h", "4h", "8h", "12h", "1d"}
	spotGranularityOrder    = []string{"1m", "3m", "5m", "15m", "30m", "1h", "2h", "4h", "6h", "8h", "12h", "1d", "1w"}
)

// SupportedGranularities returns the canonical ascending list of timeframes the
// given market supports. isFutures selects the futures ladder (no 3m/6h/1w).
// The returned slice is a copy and safe for the caller to mutate.
func SupportedGranularities(isFutures bool) []string {
	src := spotGranularityOrder
	if isFutures {
		src = futuresGranularityOrder
	}
	out := make([]string, len(src))
	copy(out, src)
	return out
}

// IsGranularitySupported reports whether tf is a supported timeframe for the
// given market.
func IsGranularitySupported(tf string, isFutures bool) bool {
	for _, g := range SupportedGranularities(isFutures) {
		if g == tf {
			return true
		}
	}
	return false
}

// granularityMinutes maps a timeframe label to its length in minutes so the
// ladders can be ranked/compared.
var granularityMinutes = map[string]int{
	"1m": 1, "3m": 3, "5m": 5, "15m": 15, "30m": 30,
	"1h": 60, "2h": 120, "4h": 240, "6h": 360, "8h": 480,
	"12h": 720, "1d": 1440, "1w": 10080,
}

// DeriveConfirmationTF returns the timeframe `stepsDown` steps FINER than
// primaryTF on the market's supported ladder — the basis of the exit-confirmation
// ladder ("operate on X, confirm the exit on a finer TF"). Returns "" when
// primaryTF is already the finest supported timeframe. On futures the 3m/6h/1w
// rungs are absent, so e.g. 5m→1m, 15m→5m, 30m→15m, 1h→30m, 4h→1h, 1d→4h. If
// primaryTF is not itself on the ladder, the largest supported TF strictly
// smaller than it is returned.
func DeriveConfirmationTF(primaryTF string, isFutures bool, stepsDown int) string {
	if stepsDown < 1 {
		stepsDown = 1
	}
	ladder := SupportedGranularities(isFutures)
	idx := -1
	for i, tf := range ladder {
		if tf == primaryTF {
			idx = i
			break
		}
	}
	if idx < 0 {
		// primaryTF off this market's ladder (e.g. 3m on futures): return the
		// largest supported TF strictly smaller than it.
		pm, ok := granularityMinutes[primaryTF]
		if !ok {
			return ""
		}
		best := ""
		for _, tf := range ladder { // ascending: last match is the largest below
			if granularityMinutes[tf] < pm {
				best = tf
			}
		}
		return best
	}
	target := idx - stepsDown
	if target < 0 {
		target = 0
	}
	if target == idx {
		return "" // primaryTF is already the finest supported TF
	}
	return ladder[target]
}

// parseFloat convierte string a float64
func parseFloat(s string) (float64, error) {
	if s == "" {
		return 0, nil
	}
	return strconv.ParseFloat(s, 64)
}

// SetLeverage configura el leverage para un símbolo de futuros
// symbol: símbolo del contrato (ej: "DOGEUSDTM")
// leverage: valor del leverage (ej: 5 para 5x)
func (k *KuCoinRESTClient) SetLeverage(ctx context.Context, symbol string, leverage int) error {
	if k.apiKey == "" || k.apiSecret == "" {
		return fmt.Errorf("API credentials not configured")
	}

	endpoint := "/api/v1/position/risk-limit-level/change"
	url := k.futuresURL + endpoint

	// Crear el payload
	payload := map[string]interface{}{
		"symbol": symbol,
		"level":  leverage,
	}

	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to marshal payload: %w", err)
	}

	// Crear request con body
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewBuffer(payloadBytes))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	// Timestamp en milisegundos
	timestamp := strconv.FormatInt(time.Now().UnixMilli(), 10)

	// Crear firma HMAC-SHA256
	// signString = timestamp + method + endpoint + body
	signString := timestamp + "POST" + endpoint + string(payloadBytes)
	h := hmac.New(sha256.New, []byte(k.apiSecret))
	h.Write([]byte(signString))
	signature := base64.StdEncoding.EncodeToString(h.Sum(nil))

	// Encriptar passphrase con HMAC-SHA256
	passphraseHmac := hmac.New(sha256.New, []byte(k.apiSecret))
	passphraseHmac.Write([]byte(k.apiPassword))
	encryptedPassphrase := base64.StdEncoding.EncodeToString(passphraseHmac.Sum(nil))

	// Agregar headers de autenticación
	req.Header.Set("KC-API-KEY", k.apiKey)
	req.Header.Set("KC-API-SIGN", signature)
	req.Header.Set("KC-API-TIMESTAMP", timestamp)
	req.Header.Set("KC-API-PASSPHRASE", encryptedPassphrase)
	req.Header.Set("KC-API-KEY-VERSION", "2")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "GoCryptoTrader-REST-Client")

	resp, err := k.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to make request: %w", err)
	}
	defer resp.Body.Close()

	// H0.3.7: record auth failure for circuit breaker
	if (resp.StatusCode == 401 || resp.StatusCode == 403) && k.credFailRecorder != nil && k.userID != "" && k.exchange != "" {
		k.credFailRecorder.RecordFailure(context.Background(), k.userID, k.exchange)
	}

	if resp.StatusCode != http.StatusOK {
		var errorBody map[string]interface{}
		json.NewDecoder(resp.Body).Decode(&errorBody)
		return fmt.Errorf("HTTP %d: %v", resp.StatusCode, errorBody)
	}

	var result map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return fmt.Errorf("failed to decode response: %w", err)
	}

	code, ok := result["code"].(string)
	if !ok || code != "200000" {
		return fmt.Errorf("KuCoin API error: %v", result)
	}

	fmt.Printf("✓ Leverage set to %dx for %s\n", leverage, symbol)
	return nil
}

// SetMarginMode configura el modo de margen para un símbolo de futuros
// symbol: símbolo del contrato (ej: "DOGEUSDTM")
// mode: "ISOLATED" o "CROSSED"
func (k *KuCoinRESTClient) SetMarginMode(ctx context.Context, symbol string, mode string) error {
	if k.apiKey == "" || k.apiSecret == "" {
		return fmt.Errorf("API credentials not configured")
	}

	endpoint := "/api/v2/changeCrossMode"
	url := k.futuresURL + endpoint

	// Crear el payload - true para CROSSED, false para ISOLATED
	payload := map[string]interface{}{
		"symbol":    symbol,
		"crossMode": mode == "CROSSED",
	}

	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to marshal payload: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewBuffer(payloadBytes))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	// Timestamp en milisegundos
	timestamp := strconv.FormatInt(time.Now().UnixMilli(), 10)

	// Crear firma HMAC-SHA256
	signString := timestamp + "POST" + endpoint + string(payloadBytes)
	h := hmac.New(sha256.New, []byte(k.apiSecret))
	h.Write([]byte(signString))
	signature := base64.StdEncoding.EncodeToString(h.Sum(nil))

	// Encriptar passphrase
	passphraseHmac := hmac.New(sha256.New, []byte(k.apiSecret))
	passphraseHmac.Write([]byte(k.apiPassword))
	encryptedPassphrase := base64.StdEncoding.EncodeToString(passphraseHmac.Sum(nil))

	// Agregar headers de autenticación
	req.Header.Set("KC-API-KEY", k.apiKey)
	req.Header.Set("KC-API-SIGN", signature)
	req.Header.Set("KC-API-TIMESTAMP", timestamp)
	req.Header.Set("KC-API-PASSPHRASE", encryptedPassphrase)
	req.Header.Set("KC-API-KEY-VERSION", "2")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "GoCryptoTrader-REST-Client")

	resp, err := k.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to make request: %w", err)
	}
	defer resp.Body.Close()

	// H0.3.7: record auth failure for circuit breaker
	if (resp.StatusCode == 401 || resp.StatusCode == 403) && k.credFailRecorder != nil && k.userID != "" && k.exchange != "" {
		k.credFailRecorder.RecordFailure(context.Background(), k.userID, k.exchange)
	}

	if resp.StatusCode != http.StatusOK {
		var errorBody map[string]interface{}
		json.NewDecoder(resp.Body).Decode(&errorBody)
		return fmt.Errorf("HTTP %d: %v", resp.StatusCode, errorBody)
	}

	var result map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return fmt.Errorf("failed to decode response: %w", err)
	}

	code, ok := result["code"].(string)
	if !ok || code != "200000" {
		return fmt.Errorf("KuCoin API error: %v", result)
	}

	fmt.Printf("✓ Margin mode set to %s for %s\n", mode, symbol)
	return nil
}

// GetPositions retrieves all open positions
func (k *KuCoinRESTClient) GetPositions(ctx context.Context) ([]map[string]interface{}, error) {
	if k == nil {
		return nil, fmt.Errorf("kucoin client is nil")
	}
	if k.apiKey == "" || k.apiSecret == "" {
		return nil, ErrInvalidCredentials
	}
	if ctx == nil {
		ctx = context.Background()
	}

	endpoint := "/api/v1/positions"

	req, err := k.createAuthenticatedRequest(ctx, "GET", endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	resp, err := k.doRequestWithRetry(ctx, req, 3)
	if err != nil {
		return nil, fmt.Errorf("failed to execute request: %w", err)
	}
	defer resp.Body.Close()

	var result struct {
		Code string                   `json:"code"`
		Data []map[string]interface{} `json:"data"`
		Msg  string                   `json:"msg"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	if result.Code != "200000" {
		return nil, MapKuCoinError(result.Code, result.Msg)
	}

	return result.Data, nil
}

// GetPosition retrieves a specific position.
// In Hedge Mode accounts, the single-position endpoint returns currentQty=0 even when a position
// is open. We fall back to the positions list and pick the matching symbol.
func (k *KuCoinRESTClient) GetPosition(ctx context.Context, symbol string) (map[string]interface{}, error) {
	if k == nil {
		return nil, fmt.Errorf("kucoin client is nil")
	}
	if symbol == "" {
		return nil, fmt.Errorf("symbol is empty")
	}
	if k.apiKey == "" || k.apiSecret == "" {
		return nil, ErrInvalidCredentials
	}
	if ctx == nil {
		ctx = context.Background()
	}

	endpoint := fmt.Sprintf("/api/v1/position?symbol=%s", symbol)

	req, err := k.createAuthenticatedRequest(ctx, "GET", endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	resp, err := k.doRequestWithRetry(ctx, req, 3)
	if err != nil {
		return nil, fmt.Errorf("failed to execute request: %w", err)
	}
	defer resp.Body.Close()

	var result struct {
		Code string                 `json:"code"`
		Data map[string]interface{} `json:"data"`
		Msg  string                 `json:"msg"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	if result.Code != "200000" {
		return nil, MapKuCoinError(result.Code, result.Msg)
	}

	// Check if position exists (currentQty != 0)
	if result.Data != nil {
		if qty, ok := result.Data["currentQty"].(float64); ok && qty != 0 {
			return result.Data, nil
		}
	}

	// Hedge Mode: single endpoint returns currentQty=0 even when a position is open.
	// Fall back to list endpoint and find the active position for this symbol.
	allPositions, err := k.GetPositions(ctx)
	if err != nil {
		return nil, ErrPositionNotFound
	}
	for _, p := range allPositions {
		sym, _ := p["symbol"].(string)
		if sym != symbol {
			continue
		}
		if qty, ok := p["currentQty"].(float64); ok && qty != 0 {
			return p, nil
		}
	}

	return nil, ErrPositionNotFound
}

// SubmitFuturesOrder submits a futures order
func (k *KuCoinRESTClient) SubmitFuturesOrder(ctx context.Context, req *FuturesOrderRequest) (string, error) {
	if k == nil {
		return "", fmt.Errorf("kucoin client is nil")
	}
	if req == nil {
		return "", ErrNilRequest
	}
	if k.apiKey == "" || k.apiSecret == "" {
		return "", ErrInvalidCredentials
	}
	if ctx == nil {
		ctx = context.Background()
	}

	// Validate request
	if err := req.Validate(); err != nil {
		return "", fmt.Errorf("invalid request: %w", err)
	}

	endpoint := "/api/v1/orders"

	// Build payload. marginMode is included explicitly so KuCoin uses our
	// requested mode for this order instead of falling back to the per-symbol
	// stored setting (which causes error 330005 MARGIN_MODE_MISMATCH when the
	// account default differs). "CROSS" is the safest universal default.
	payload := map[string]interface{}{
		"clientOid":  req.ClientOID,
		"side":       req.Side,
		"symbol":     req.Symbol,
		"type":       req.Type,
		"leverage":   fmt.Sprintf("%d", req.Leverage),
		"marginMode": "CROSS",
	}

	// CRITICAL: Add positionSide for Hedge Mode support
	// KuCoin requires positionSide when account is in Hedge Mode
	// Logic:
	//   - Opening LONG: side="buy", positionSide="LONG"
	//   - Closing LONG: side="sell", positionSide="LONG", closeOrder=true
	//   - Opening SHORT: side="sell", positionSide="SHORT"
	//   - Closing SHORT: side="buy", positionSide="SHORT", closeOrder=true
	if req.CloseOrder {
		// When closing a position, positionSide is opposite of the order side
		if req.Side == "buy" {
			payload["positionSide"] = "SHORT" // Closing short position
		} else if req.Side == "sell" {
			payload["positionSide"] = "LONG" // Closing long position
		}
	} else {
		// When opening a position, positionSide matches the order side
		if req.Side == "buy" {
			payload["positionSide"] = "LONG" // Opening long position
		} else if req.Side == "sell" {
			payload["positionSide"] = "SHORT" // Opening short position
		}
	}

	// Size is already in contracts from the API
	// Don't divide by 100 - that was causing partial closes
	contracts := int(req.Size)
	if contracts < 1 {
		contracts = 1 // Minimum 1 contract
	}

	if req.Type == "limit" {
		payload["price"] = fmt.Sprintf("%.8f", req.Price)
		payload["size"] = contracts
		if req.PostOnly {
			payload["postOnly"] = true
		}
	} else {
		payload["size"] = contracts
	}

	if req.ReduceOnly {
		payload["reduceOnly"] = true
	}

	if req.CloseOrder {
		payload["closeOrder"] = true
	}

	// Add stop order parameters if stopPrice is provided
	if req.StopPrice > 0 {
		payload["stopPrice"] = fmt.Sprintf("%.8f", req.StopPrice)

		// Set stopPriceType (default to TP if not specified)
		stopPriceType := req.StopPriceType
		if stopPriceType == "" {
			stopPriceType = "TP" // Trade Price (default)
		}
		payload["stopPriceType"] = stopPriceType

		// Determine stop direction based on side and closeOrder
		// For stop-loss closing a LONG: side="sell", stop="down"
		// For stop-loss closing a SHORT: side="buy", stop="up"
		if req.CloseOrder {
			if req.Side == "sell" {
				payload["stop"] = "down" // Stop-loss for long position
			} else if req.Side == "buy" {
				payload["stop"] = "up" // Stop-loss for short position
			}
		} else {
			// For opening positions with stop orders (stop-limit entries)
			if req.Side == "buy" {
				payload["stop"] = "up" // Buy when price goes up
			} else if req.Side == "sell" {
				payload["stop"] = "down" // Sell when price goes down
			}
		}
	}

	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("failed to marshal payload: %w", err)
	}

	httpReq, err := k.createAuthenticatedRequest(ctx, "POST", endpoint, payloadBytes)
	if err != nil {
		return "", fmt.Errorf("failed to create request: %w", err)
	}

	resp, err := k.doRequestWithRetry(ctx, httpReq, 3)
	if err != nil {
		return "", fmt.Errorf("failed to execute request: %w", err)
	}
	defer resp.Body.Close()

	var result struct {
		Code string `json:"code"`
		Data struct {
			OrderID string `json:"orderId"`
		} `json:"data"`
		Msg string `json:"msg"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("failed to decode response: %w", err)
	}

	if result.Code != "200000" {
		// CRITICAL FIX: Check if order was actually placed despite error
		// KuCoin sometimes returns error 200004 (insufficient balance) for SL/TP orders
		// even when the main order executed successfully
		if result.Data.OrderID != "" {
			// Order was placed successfully despite error code
			// This typically happens when:
			// 1. Main order succeeds
			// 2. Automatic SL/TP orders fail due to insufficient balance
			// 3. KuCoin returns error but includes the main order ID
			fmt.Printf("⚠️  Order %s placed successfully but received error %s: %s (likely SL/TP issue)\n",
				result.Data.OrderID, result.Code, result.Msg)

			// Return the order ID since the main order succeeded
			return result.Data.OrderID, nil
		}

		// No order ID means the order truly failed
		return "", MapKuCoinError(result.Code, result.Msg)
	}

	return result.Data.OrderID, nil
}

// SpotOrderRequest represents a KuCoin SPOT order (no leverage, no
// positionSide/marginMode/reduceOnly/closeOrder — those are futures-only
// concepts). CORE-04: introduced so per-user spot orders can actually be
// placed against the KuCoin SPOT REST API instead of being silently routed
// through FuturesManager.PlaceFuturesOrder.
type SpotOrderRequest struct {
	Symbol    string  // e.g. "BTC-USDT" (spot uses the dashed format, unlike futures' "BTCUSDTM")
	Side      string  // "buy" or "sell"
	Type      string  // "market" or "limit"
	Size      float64 // order size in BASE currency units (e.g. 0.001 BTC)
	Price     float64 // required for "limit", ignored for "market"
	ClientOID string  // optional; generated if empty
}

// Validate validates the spot order request.
func (r *SpotOrderRequest) Validate() error {
	if r == nil {
		return fmt.Errorf("spot order request is nil")
	}
	if r.Symbol == "" {
		return fmt.Errorf("symbol is required")
	}
	if r.Side != "buy" && r.Side != "sell" {
		return fmt.Errorf("side must be 'buy' or 'sell', got: %s", r.Side)
	}
	if r.Type != "market" && r.Type != "limit" {
		return fmt.Errorf("type must be 'market' or 'limit', got: %s", r.Type)
	}
	if r.Size <= 0 {
		return fmt.Errorf("size must be greater than 0, got: %.8f", r.Size)
	}
	if r.Type == "limit" && r.Price <= 0 {
		return fmt.Errorf("price must be greater than 0 for limit orders")
	}
	return nil
}

// SubmitSpotOrder places a real SPOT order via KuCoin's spot REST API
// (baseURL, not futuresURL — signed via createSpotAuthenticatedRequest with
// this client's own credentials, preserving per-user isolation). Returns the
// exchange order ID.
func (k *KuCoinRESTClient) SubmitSpotOrder(ctx context.Context, req *SpotOrderRequest) (string, error) {
	if k == nil {
		return "", fmt.Errorf("kucoin client is nil")
	}
	if req == nil {
		return "", ErrNilRequest
	}
	if k.apiKey == "" || k.apiSecret == "" {
		return "", ErrInvalidCredentials
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := req.Validate(); err != nil {
		return "", fmt.Errorf("invalid request: %w", err)
	}

	clientOID := req.ClientOID
	if clientOID == "" {
		clientOID = fmt.Sprintf("spot_%d", time.Now().UnixNano())
	}

	endpoint := "/api/v1/orders"
	payload := map[string]interface{}{
		"clientOid": clientOID,
		"side":      req.Side,
		"symbol":    req.Symbol,
		"type":      req.Type,
		"size":      fmt.Sprintf("%.8f", req.Size),
	}
	if req.Type == "limit" {
		payload["price"] = fmt.Sprintf("%.8f", req.Price)
	}

	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("failed to marshal payload: %w", err)
	}

	httpReq, err := k.createSpotAuthenticatedRequest(ctx, "POST", endpoint, payloadBytes)
	if err != nil {
		return "", fmt.Errorf("failed to create request: %w", err)
	}

	resp, err := k.doRequestWithRetry(ctx, httpReq, 3)
	if err != nil {
		return "", fmt.Errorf("failed to execute request: %w", err)
	}
	defer resp.Body.Close()

	var result struct {
		Code string `json:"code"`
		Data struct {
			OrderID string `json:"orderId"`
		} `json:"data"`
		Msg string `json:"msg"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("failed to decode response: %w", err)
	}

	if result.Code != "200000" {
		if result.Data.OrderID != "" {
			return result.Data.OrderID, nil
		}
		return "", MapKuCoinError(result.Code, result.Msg)
	}

	return result.Data.OrderID, nil
}

// GetFuturesOrderFill queries a single futures order by ID and returns its
// total size and filled quantity, both in CONTRACTS (matching how
// SubmitFuturesOrder sizes orders). TOOL-05: needed so modify_order can
// compute the unfilled remainder instead of blindly resubmitting the
// original requested amount after a partial fill.
func (k *KuCoinRESTClient) GetFuturesOrderFill(ctx context.Context, orderID string) (size, filled float64, isActive bool, err error) {
	if k == nil {
		return 0, 0, false, fmt.Errorf("kucoin client is nil")
	}
	if orderID == "" {
		return 0, 0, false, fmt.Errorf("orderID is required")
	}
	if ctx == nil {
		ctx = context.Background()
	}

	req, err := k.createAuthenticatedRequest(ctx, "GET", "/api/v1/orders/"+orderID, nil)
	if err != nil {
		return 0, 0, false, fmt.Errorf("failed to create request: %w", err)
	}

	resp, err := k.doRequestWithRetry(ctx, req, 3)
	if err != nil {
		return 0, 0, false, fmt.Errorf("failed to execute request: %w", err)
	}
	defer resp.Body.Close()

	var result struct {
		Code string `json:"code"`
		Data struct {
			Size        int64 `json:"size"`
			DealSize    int64 `json:"dealSize"`
			IsActive    bool  `json:"isActive"`
			CancelExist bool  `json:"cancelExist"`
		} `json:"data"`
		Msg string `json:"msg"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return 0, 0, false, fmt.Errorf("failed to decode response: %w", err)
	}
	if result.Code != "200000" {
		return 0, 0, false, MapKuCoinError(result.Code, result.Msg)
	}

	return float64(result.Data.Size), float64(result.Data.DealSize), result.Data.IsActive, nil
}

// GetSpotOrderFill queries a single spot order by ID and returns its total
// size and filled quantity, both in BASE currency units. See
// GetFuturesOrderFill for why this exists (TOOL-05).
func (k *KuCoinRESTClient) GetSpotOrderFill(ctx context.Context, orderID string) (size, filled float64, isActive bool, err error) {
	if k == nil {
		return 0, 0, false, fmt.Errorf("kucoin client is nil")
	}
	if orderID == "" {
		return 0, 0, false, fmt.Errorf("orderID is required")
	}
	if ctx == nil {
		ctx = context.Background()
	}

	req, err := k.createSpotAuthenticatedRequest(ctx, "GET", "/api/v1/orders/"+orderID, nil)
	if err != nil {
		return 0, 0, false, fmt.Errorf("failed to create request: %w", err)
	}

	resp, err := k.doRequestWithRetry(ctx, req, 3)
	if err != nil {
		return 0, 0, false, fmt.Errorf("failed to execute request: %w", err)
	}
	defer resp.Body.Close()

	var result struct {
		Code string `json:"code"`
		Data struct {
			Size        string `json:"size"`
			DealSize    string `json:"dealSize"`
			IsActive    bool   `json:"isActive"`
			CancelExist bool   `json:"cancelExist"`
		} `json:"data"`
		Msg string `json:"msg"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return 0, 0, false, fmt.Errorf("failed to decode response: %w", err)
	}
	if result.Code != "200000" {
		return 0, 0, false, MapKuCoinError(result.Code, result.Msg)
	}

	size, _ = parseFloat(result.Data.Size)
	filled, _ = parseFloat(result.Data.DealSize)
	return size, filled, result.Data.IsActive, nil
}

// GetOrderFill tries the futures order-status endpoint first, then falls
// back to spot, since callers here (ModifyOrderTool) don't always know the
// market type up front. Returns size/filled in whatever unit that market
// uses (contracts for futures, base currency for spot) — callers must not
// mix units across the two markets.
func (k *KuCoinRESTClient) GetOrderFill(ctx context.Context, orderID string) (size, filled float64, market string, err error) {
	if size, filled, _, ferr := k.GetFuturesOrderFill(ctx, orderID); ferr == nil {
		return size, filled, "futures", nil
	}
	if size, filled, _, serr := k.GetSpotOrderFill(ctx, orderID); serr == nil {
		return size, filled, "spot", nil
	}
	return 0, 0, "", fmt.Errorf("order %s not found on futures or spot order-status endpoints", orderID)
}

// doRequestWithRetry executes HTTP request with retry logic
func (k *KuCoinRESTClient) doRequestWithRetry(ctx context.Context, req *http.Request, maxRetries int) (*http.Response, error) {
	if k == nil {
		return nil, fmt.Errorf("kucoin client is nil")
	}
	if req == nil {
		return nil, fmt.Errorf("request is nil")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if maxRetries < 0 {
		maxRetries = 0
	}

	var lastErr error
	delay := 1 * time.Second
	retryableStatuses := map[int]bool{
		500: true,
		502: true,
		503: true,
		504: true,
		429: true,
	}

	for attempt := 0; attempt <= maxRetries; attempt++ {
		// Clone request for retry
		reqClone := req.Clone(ctx)

		resp, err := k.httpClient.Do(reqClone)

		// Success case
		if err == nil && resp.StatusCode >= 200 && resp.StatusCode < 300 {
			return resp, nil
		}

		// Handle error
		if err != nil {
			lastErr = fmt.Errorf("%w: %v", ErrNetworkError, err)
		} else {
			// H0.3.7: record auth failures for circuit breaker
			if (resp.StatusCode == 401 || resp.StatusCode == 403) && k.credFailRecorder != nil && k.userID != "" && k.exchange != "" {
				k.credFailRecorder.RecordFailure(ctx, k.userID, k.exchange)
			}

			// Check if status is retryable
			if !retryableStatuses[resp.StatusCode] {
				// Not retryable, return immediately
				return resp, nil
			}

			lastErr = fmt.Errorf("HTTP %d", resp.StatusCode)

			// Handle rate limiting
			if resp.StatusCode == 429 {
				if retryAfter := resp.Header.Get("Retry-After"); retryAfter != "" {
					if seconds, err := strconv.Atoi(retryAfter); err == nil {
						delay = time.Duration(seconds) * time.Second
					}
				}
			}

			resp.Body.Close()
		}

		// Don't sleep on last attempt
		if attempt < maxRetries {
			select {
			case <-time.After(delay):
				// Exponential backoff
				delay *= 2
				if delay > 10*time.Second {
					delay = 10 * time.Second
				}
			case <-ctx.Done():
				return nil, fmt.Errorf("%w: %v", ErrTimeout, ctx.Err())
			}
		}
	}

	return nil, fmt.Errorf("max retries (%d) exceeded: %w", maxRetries, lastErr)
}

// createAuthenticatedRequest creates an authenticated HTTP request
func (k *KuCoinRESTClient) createAuthenticatedRequest(ctx context.Context, method, endpoint string, body []byte) (*http.Request, error) {
	if k == nil {
		return nil, fmt.Errorf("kucoin client is nil")
	}
	if method == "" {
		return nil, fmt.Errorf("method is empty")
	}
	if endpoint == "" {
		return nil, fmt.Errorf("endpoint is empty")
	}
	if ctx == nil {
		ctx = context.Background()
	}

	url := k.futuresURL + endpoint

	var req *http.Request
	var err error

	if body != nil {
		req, err = http.NewRequestWithContext(ctx, method, url, bytes.NewBuffer(body))
	} else {
		req, err = http.NewRequestWithContext(ctx, method, url, nil)
	}

	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	// Generate timestamp
	timestamp := strconv.FormatInt(time.Now().UnixMilli(), 10)

	// Create signature string
	signString := timestamp + method + endpoint
	if body != nil {
		signString += string(body)
	}

	// Generate HMAC-SHA256 signature
	h := hmac.New(sha256.New, []byte(k.apiSecret))
	h.Write([]byte(signString))
	signature := base64.StdEncoding.EncodeToString(h.Sum(nil))

	// Encrypt passphrase
	passphraseHmac := hmac.New(sha256.New, []byte(k.apiSecret))
	passphraseHmac.Write([]byte(k.apiPassword))
	encryptedPassphrase := base64.StdEncoding.EncodeToString(passphraseHmac.Sum(nil))

	// Set headers
	req.Header.Set("KC-API-KEY", k.apiKey)
	req.Header.Set("KC-API-SIGN", signature)
	req.Header.Set("KC-API-TIMESTAMP", timestamp)
	req.Header.Set("KC-API-PASSPHRASE", encryptedPassphrase)
	req.Header.Set("KC-API-KEY-VERSION", "2")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "GoCryptoTrader-REST-Client")

	return req, nil
}

// createSpotAuthenticatedRequest creates an authenticated HTTP request for spot API
func (k *KuCoinRESTClient) createSpotAuthenticatedRequest(ctx context.Context, method, endpoint string, body []byte) (*http.Request, error) {
	if k == nil {
		return nil, fmt.Errorf("kucoin client is nil")
	}
	if method == "" {
		return nil, fmt.Errorf("method is empty")
	}
	if endpoint == "" {
		return nil, fmt.Errorf("endpoint is empty")
	}
	if ctx == nil {
		ctx = context.Background()
	}

	url := k.baseURL + endpoint

	var req *http.Request
	var err error

	if body != nil {
		req, err = http.NewRequestWithContext(ctx, method, url, bytes.NewBuffer(body))
	} else {
		req, err = http.NewRequestWithContext(ctx, method, url, nil)
	}

	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	// Generate timestamp
	timestamp := strconv.FormatInt(time.Now().UnixMilli(), 10)

	// Create signature string
	signString := timestamp + method + endpoint
	if body != nil {
		signString += string(body)
	}

	// Generate HMAC-SHA256 signature
	h := hmac.New(sha256.New, []byte(k.apiSecret))
	h.Write([]byte(signString))
	signature := base64.StdEncoding.EncodeToString(h.Sum(nil))

	// Encrypt passphrase
	passphraseHmac := hmac.New(sha256.New, []byte(k.apiSecret))
	passphraseHmac.Write([]byte(k.apiPassword))
	encryptedPassphrase := base64.StdEncoding.EncodeToString(passphraseHmac.Sum(nil))

	// Set headers
	req.Header.Set("KC-API-KEY", k.apiKey)
	req.Header.Set("KC-API-SIGN", signature)
	req.Header.Set("KC-API-TIMESTAMP", timestamp)
	req.Header.Set("KC-API-PASSPHRASE", encryptedPassphrase)
	req.Header.Set("KC-API-KEY-VERSION", "2")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "GoCryptoTrader-REST-Client")

	return req, nil
}

// GetSpotBalance obtiene el balance de una moneda en la cuenta spot
func (k *KuCoinRESTClient) GetSpotBalance(ctx context.Context, currency string) (map[string]interface{}, error) {
	if !k.HasCredentials() {
		return nil, fmt.Errorf("API credentials not configured")
	}

	endpoint := "/api/v1/accounts"
	req, err := k.createSpotAuthenticatedRequest(ctx, "GET", endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	resp, err := k.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to make request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(body))
	}

	var result struct {
		Code string `json:"code"`
		Data []struct {
			Currency  string `json:"currency"`
			Type      string `json:"type"`
			Balance   string `json:"balance"`
			Available string `json:"available"`
			Holds     string `json:"holds"`
		} `json:"data"`
	}

	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	if result.Code != "200000" {
		return nil, fmt.Errorf("KuCoin API error: code=%s", result.Code)
	}

	// Buscar la moneda en las cuentas
	var totalBalance, availableBalance, holds float64
	found := false

	for _, account := range result.Data {
		if account.Currency == currency && (account.Type == "trade" || account.Type == "main") {
			found = true
			balance, _ := strconv.ParseFloat(account.Balance, 64)
			available, _ := strconv.ParseFloat(account.Available, 64)
			hold, _ := strconv.ParseFloat(account.Holds, 64)

			totalBalance += balance
			availableBalance += available
			holds += hold
		}
	}

	if !found {
		return map[string]interface{}{
			"currency":  currency,
			"total":     0.0,
			"available": 0.0,
			"in_order":  0.0,
		}, nil
	}

	return map[string]interface{}{
		"currency":  currency,
		"total":     totalBalance,
		"available": availableBalance,
		"in_order":  holds,
	}, nil
}

// GetFuturesBalance obtiene el balance de una moneda en la cuenta de futuros
func (k *KuCoinRESTClient) GetFuturesBalance(ctx context.Context, currency string) (map[string]interface{}, error) {
	if !k.HasCredentials() {
		return nil, fmt.Errorf("API credentials not configured")
	}

	endpoint := fmt.Sprintf("/api/v1/account-overview?currency=%s", currency)
	req, err := k.createAuthenticatedRequest(ctx, "GET", endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	resp, err := k.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to make request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(body))
	}

	var result struct {
		Code string `json:"code"`
		Data struct {
			AccountEquity    float64 `json:"accountEquity"`
			AvailableBalance float64 `json:"availableBalance"`
			PositionMargin   float64 `json:"positionMargin"`
			OrderMargin      float64 `json:"orderMargin"`
			FrozenFunds      float64 `json:"frozenFunds"`
			Currency         string  `json:"currency"`
		} `json:"data"`
	}

	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	if result.Code != "200000" {
		return nil, fmt.Errorf("KuCoin API error: code=%s", result.Code)
	}

	return map[string]interface{}{
		"currency":  currency,
		"total":     result.Data.AccountEquity,
		"available": result.Data.AvailableBalance,
		"in_order":  result.Data.OrderMargin + result.Data.FrozenFunds,
		"margin":    result.Data.PositionMargin,
	}, nil
}

// GetAllSpotBalances obtiene todos los balances de la cuenta spot
func (k *KuCoinRESTClient) GetAllSpotBalances(ctx context.Context) (map[string]map[string]interface{}, error) {
	if !k.HasCredentials() {
		return nil, fmt.Errorf("API credentials not configured")
	}

	endpoint := "/api/v1/accounts"
	req, err := k.createSpotAuthenticatedRequest(ctx, "GET", endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	resp, err := k.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to make request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(body))
	}

	var result struct {
		Code string `json:"code"`
		Data []struct {
			ID        string `json:"id"`
			Currency  string `json:"currency"`
			Type      string `json:"type"`
			Balance   string `json:"balance"`
			Available string `json:"available"`
			Holds     string `json:"holds"`
		} `json:"data"`
	}

	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	if result.Code != "200000" {
		return nil, fmt.Errorf("KuCoin API error: code=%s", result.Code)
	}

	// Agregar balances por moneda (sumar trade y main)
	balances := make(map[string]map[string]interface{})

	for _, account := range result.Data {
		// Solo procesar cuentas trade y main
		if account.Type != "trade" && account.Type != "main" {
			continue
		}

		balance, _ := strconv.ParseFloat(account.Balance, 64)
		available, _ := strconv.ParseFloat(account.Available, 64)
		holds, _ := strconv.ParseFloat(account.Holds, 64)

		// Si la moneda ya existe, sumar los valores
		if existing, ok := balances[account.Currency]; ok {
			existing["total"] = existing["total"].(float64) + balance
			existing["available"] = existing["available"].(float64) + available
			existing["in_order"] = existing["in_order"].(float64) + holds
		} else {
			balances[account.Currency] = map[string]interface{}{
				"currency":  account.Currency,
				"total":     balance,
				"available": available,
				"in_order":  holds,
			}
		}
	}

	return balances, nil
}

// GetPortfolioViaREST obtiene el portfolio completo via REST API
func (k *KuCoinRESTClient) GetPortfolioViaREST(ctx context.Context) (*Portfolio, error) {
	if !k.HasCredentials() {
		return nil, fmt.Errorf("API credentials not configured")
	}

	// Obtener balances spot
	spotBalances, err := k.GetAllSpotBalances(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get spot balances: %w", err)
	}

	// Obtener balance de futuros
	futuresBalance, futuresErr := k.GetFuturesBalance(ctx, "USDT")

	// Calcular valor total del portfolio
	totalValue := 0.0
	holdings := make(map[string]*Holding)

	// Procesar balances spot
	for currency, balance := range spotBalances {
		total := balance["total"].(float64)
		available := balance["available"].(float64)
		inOrder := balance["in_order"].(float64)

		// Solo incluir si hay balance
		if total > 0 {
			value := total
			// Si es USDT, el valor es directo
			if currency == "USDT" {
				totalValue += total
			}

			holdings[currency] = &Holding{
				Currency:  currency,
				Total:     total,
				Available: available,
				InOrder:   inOrder,
				Value:     value,
				PnL:       0,
				PnLPct:    0,
			}
		}
	}

	// Agregar balance de futuros si está disponible
	if futuresErr == nil && futuresBalance != nil {
		futuresEquity := futuresBalance["total"].(float64)
		if futuresEquity > 0 {
			totalValue += futuresEquity

			// Agregar como holding separado
			if existing, ok := holdings["USDT"]; ok {
				existing.Total += futuresEquity
				existing.Value += futuresEquity
			} else {
				holdings["USDT-FUTURES"] = &Holding{
					Currency:  "USDT",
					Total:     futuresEquity,
					Available: futuresBalance["available"].(float64),
					InOrder:   futuresBalance["in_order"].(float64),
					Value:     futuresEquity,
					PnL:       0,
					PnLPct:    0,
				}
			}
		}
	}

	return &Portfolio{
		TotalValue:    totalValue,
		TotalPnL:      0, // No podemos calcular P&L sin datos históricos
		PnL:           0,
		PnLPercentage: 0,
		Holdings:      holdings,
		Allocations:   make(map[string]float64),
		UpdatedAt:     time.Now(),
	}, nil
}

// GetOpenOrders retrieves open orders from KuCoin (both spot and futures)
// CancelFuturesOrder cancels a futures order via KuCoin's REST API directly
// (DELETE /api/v1/orders/{orderID}). Orders submitted through this client's
// SubmitFuturesOrder bypass GCT's order manager, so GCT's gRPC CancelOrder
// cannot find them (fails with "currency pair unset") — cancellation must go
// through the same direct path that placed them.
func (k *KuCoinRESTClient) CancelFuturesOrder(ctx context.Context, orderID string) error {
	if k == nil {
		return fmt.Errorf("kucoin client is nil")
	}
	if orderID == "" {
		return fmt.Errorf("orderID is required")
	}
	if k.apiKey == "" || k.apiSecret == "" {
		return ErrInvalidCredentials
	}
	if ctx == nil {
		ctx = context.Background()
	}

	endpoint := "/api/v1/orders/" + orderID

	httpReq, err := k.createAuthenticatedRequest(ctx, "DELETE", endpoint, nil)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	resp, err := k.doRequestWithRetry(ctx, httpReq, 3)
	if err != nil {
		return fmt.Errorf("failed to execute request: %w", err)
	}
	defer resp.Body.Close()

	var result struct {
		Code string `json:"code"`
		Data struct {
			CancelledOrderIDs []string `json:"cancelledOrderIds"`
		} `json:"data"`
		Msg string `json:"msg"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return fmt.Errorf("failed to decode response: %w", err)
	}
	if result.Code != "200000" {
		return MapKuCoinError(result.Code, result.Msg)
	}
	return nil
}

func (k *KuCoinRESTClient) GetOpenOrders(ctx context.Context, pair string) ([]*Order, error) {
	if k == nil {
		return nil, fmt.Errorf("kucoin client is nil")
	}
	if !k.HasCredentials() {
		return nil, ErrInvalidCredentials
	}
	if ctx == nil {
		ctx = context.Background()
	}

	var allOrders []*Order

	// Determine if we should query spot, futures, or both based on pair
	isFutures := pair != "" && (strings.HasSuffix(strings.ToUpper(pair), "M") ||
		strings.Contains(strings.ToUpper(pair), "USDTM") ||
		strings.Contains(strings.ToUpper(pair), "PERP"))

	// Get spot orders if not explicitly futures
	if !isFutures {
		spotOrders, err := k.getSpotOpenOrders(ctx, pair)
		if err == nil {
			allOrders = append(allOrders, spotOrders...)
		}
	}

	// Get futures orders
	futuresOrders, err := k.getFuturesOpenOrders(ctx, pair)
	if err == nil {
		allOrders = append(allOrders, futuresOrders...)
	}

	return allOrders, nil
}

// getSpotOpenOrders retrieves open orders from KuCoin spot market
func (k *KuCoinRESTClient) getSpotOpenOrders(ctx context.Context, pair string) ([]*Order, error) {
	endpoint := "/api/v1/orders?status=active"
	if pair != "" {
		// Convert pair format if needed (BTC-USDT -> BTC-USDT)
		formattedPair := pair
		if !strings.Contains(pair, "-") {
			// Try to add delimiter for common quote currencies
			for _, quote := range []string{"USDT", "USDC", "BTC", "ETH"} {
				if strings.HasSuffix(strings.ToUpper(pair), quote) {
					base := pair[:len(pair)-len(quote)]
					formattedPair = strings.ToUpper(base) + "-" + quote
					break
				}
			}
		}
		endpoint += "&symbol=" + formattedPair
	}

	req, err := k.createSpotAuthenticatedRequest(ctx, "GET", endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	resp, err := k.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to make request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(body))
	}

	var result struct {
		Code string `json:"code"`
		Data struct {
			CurrentPage int `json:"currentPage"`
			PageSize    int `json:"pageSize"`
			TotalNum    int `json:"totalNum"`
			TotalPage   int `json:"totalPage"`
			Items       []struct {
				ID            string `json:"id"`
				Symbol        string `json:"symbol"`
				OpType        string `json:"opType"`
				Type          string `json:"type"`
				Side          string `json:"side"`
				Price         string `json:"price"`
				Size          string `json:"size"`
				Funds         string `json:"funds"`
				DealFunds     string `json:"dealFunds"`
				DealSize      string `json:"dealSize"`
				Fee           string `json:"fee"`
				FeeCurrency   string `json:"feeCurrency"`
				Stp           string `json:"stp"`
				Stop          string `json:"stop"`
				StopTriggered bool   `json:"stopTriggered"`
				StopPrice     string `json:"stopPrice"`
				TimeInForce   string `json:"timeInForce"`
				PostOnly      bool   `json:"postOnly"`
				Hidden        bool   `json:"hidden"`
				Iceberg       bool   `json:"iceberg"`
				VisibleSize   string `json:"visibleSize"`
				CancelAfter   int64  `json:"cancelAfter"`
				Channel       string `json:"channel"`
				ClientOid     string `json:"clientOid"`
				Remark        string `json:"remark"`
				Tags          string `json:"tags"`
				IsActive      bool   `json:"isActive"`
				CancelExist   bool   `json:"cancelExist"`
				CreatedAt     int64  `json:"createdAt"`
				TradeType     string `json:"tradeType"`
			} `json:"items"`
		} `json:"data"`
		Msg string `json:"msg"`
	}

	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	if result.Code != "200000" {
		return nil, fmt.Errorf("KuCoin API error: code=%s, msg=%s", result.Code, result.Msg)
	}

	var orders []*Order
	for _, item := range result.Data.Items {
		price, _ := parseFloat(item.Price)
		size, _ := parseFloat(item.Size)
		dealSize, _ := parseFloat(item.DealSize)

		// Parse pair into base and quote
		parts := strings.Split(item.Symbol, "-")
		baseCurrency := ""
		quoteCurrency := ""
		if len(parts) >= 2 {
			baseCurrency = parts[0]
			quoteCurrency = parts[1]
		}

		order := &Order{
			ID:            item.ID,
			Exchange:      "kucoin",
			Pair:          item.Symbol,
			BaseCurrency:  baseCurrency,
			QuoteCurrency: quoteCurrency,
			Side:          item.Side,
			Type:          item.Type,
			Price:         price,
			Amount:        size,
			Remaining:     size - dealSize,
			OpenVolume:    size - dealSize,
			Status:        "open",
			CreatedAt:     time.Unix(item.CreatedAt/1000, 0),
			UpdatedAt:     time.Unix(item.CreatedAt/1000, 0),
			UpdateTime:    time.Unix(item.CreatedAt/1000, 0),
		}
		orders = append(orders, order)
	}

	return orders, nil
}

// getFuturesOpenOrders retrieves open orders from KuCoin futures market
func (k *KuCoinRESTClient) getFuturesOpenOrders(ctx context.Context, pair string) ([]*Order, error) {
	endpoint := "/api/v1/orders?status=active"
	if pair != "" {
		// Convert pair format for futures (DOGE-USDTM -> DOGEUSDTM)
		formattedPair := strings.ReplaceAll(strings.ToUpper(pair), "-", "")
		// Ensure it ends with M for futures
		if !strings.HasSuffix(formattedPair, "M") && strings.Contains(formattedPair, "USDT") {
			formattedPair = strings.Replace(formattedPair, "USDT", "USDTM", 1)
		}
		endpoint += "&symbol=" + formattedPair
	}

	req, err := k.createAuthenticatedRequest(ctx, "GET", endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	resp, err := k.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to make request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(body))
	}

	var result struct {
		Code string `json:"code"`
		Data struct {
			CurrentPage int `json:"currentPage"`
			PageSize    int `json:"pageSize"`
			TotalNum    int `json:"totalNum"`
			TotalPage   int `json:"totalPage"`
			Items       []struct {
				ID             string `json:"id"`
				Symbol         string `json:"symbol"`
				Type           string `json:"type"`
				Side           string `json:"side"`
				Price          string `json:"price"`
				Size           int64  `json:"size"`
				Value          string `json:"value"`
				DealValue      string `json:"dealValue"`
				DealSize       int64  `json:"dealSize"`
				Stp            string `json:"stp"`
				Stop           string `json:"stop"`
				StopPriceType  string `json:"stopPriceType"`
				StopTriggered  bool   `json:"stopTriggered"`
				StopPrice      string `json:"stopPrice"`
				TimeInForce    string `json:"timeInForce"`
				PostOnly       bool   `json:"postOnly"`
				Hidden         bool   `json:"hidden"`
				Iceberg        bool   `json:"iceberg"`
				Leverage       string `json:"leverage"`
				ForceHold      bool   `json:"forceHold"`
				CloseOrder     bool   `json:"closeOrder"`
				VisibleSize    int64  `json:"visibleSize"`
				ClientOid      string `json:"clientOid"`
				Remark         string `json:"remark"`
				Tags           string `json:"tags"`
				IsActive       bool   `json:"isActive"`
				CancelExist    bool   `json:"cancelExist"`
				CreatedAt      int64  `json:"createdAt"`
				UpdatedAt      int64  `json:"updatedAt"`
				EndAt          int64  `json:"endAt"`
				OrderTime      int64  `json:"orderTime"`
				SettleCurrency string `json:"settleCurrency"`
				Status         string `json:"status"`
				FilledSize     int64  `json:"filledSize"`
				FilledValue    string `json:"filledValue"`
				ReduceOnly     bool   `json:"reduceOnly"`
			} `json:"items"`
		} `json:"data"`
		Msg string `json:"msg"`
	}

	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	if result.Code != "200000" {
		return nil, fmt.Errorf("KuCoin Futures API error: code=%s, msg=%s", result.Code, result.Msg)
	}

	var orders []*Order
	for _, item := range result.Data.Items {
		price, _ := parseFloat(item.Price)

		// For futures, size is in contracts
		size := float64(item.Size)
		remaining := float64(item.Size - item.FilledSize)

		// Parse symbol to get base/quote
		symbol := item.Symbol
		baseCurrency := ""
		quoteCurrency := "USDT"

		// Remove trailing M for display
		if strings.HasSuffix(symbol, "M") {
			basePart := strings.TrimSuffix(symbol, "USDTM")
			if basePart != "" {
				baseCurrency = basePart
			}
		}

		// Determine status
		status := "open"
		if !item.IsActive {
			status = "closed"
		}

		order := &Order{
			ID:            item.ID,
			Exchange:      "kucoin",
			Pair:          item.Symbol,
			BaseCurrency:  baseCurrency,
			QuoteCurrency: quoteCurrency,
			Side:          item.Side,
			Type:          item.Type,
			Price:         price,
			Amount:        size,
			Remaining:     remaining,
			OpenVolume:    remaining,
			Status:        status,
			CreatedAt:     time.Unix(item.CreatedAt/1000, 0),
			UpdatedAt:     time.Unix(item.UpdatedAt/1000, 0),
			UpdateTime:    time.Unix(item.UpdatedAt/1000, 0),
			MarketType:    MarketTypeFutures,
		}
		orders = append(orders, order)
	}

	return orders, nil
}

// GetFuturesRecentFills fetches the account's futures FILLS (executed trades)
// from the last 24h via /api/v1/recentFills, optionally filtered by pair.
// This is what external-close resolution needs: a closing fill is a COMPLETED
// order and therefore never appears in GetOpenOrders (status=active), which is
// why the old GetOrders REST fallback could never see it. Each fill is mapped
// to an Order with Filled = executed contracts and Price = real fill price.
func (k *KuCoinRESTClient) GetFuturesRecentFills(ctx context.Context, pair string) ([]*Order, error) {
	endpoint := "/api/v1/recentFills"

	req, err := k.createAuthenticatedRequest(ctx, "GET", endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	resp, err := k.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to make request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(body))
	}

	var result struct {
		Code string `json:"code"`
		Data []struct {
			Symbol    string `json:"symbol"`
			TradeID   string `json:"tradeId"`
			OrderID   string `json:"orderId"`
			Side      string `json:"side"`
			Price     string `json:"price"`
			Size      int64  `json:"size"`
			OrderType string `json:"orderType"`
			TradeTime int64  `json:"tradeTime"` // nanoseconds
		} `json:"data"`
		Msg string `json:"msg"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}
	if result.Code != "200000" {
		return nil, fmt.Errorf("KuCoin Futures API error: code=%s, msg=%s", result.Code, result.Msg)
	}

	// Normalize the requested pair to the futures symbol for filtering
	// ("TAO-USDT" -> "TAOUSDTM"); empty pair returns all fills.
	wantSymbol := ""
	if pair != "" {
		wantSymbol = strings.ReplaceAll(strings.ToUpper(pair), "-", "")
		if !strings.HasSuffix(wantSymbol, "M") && strings.Contains(wantSymbol, "USDT") {
			wantSymbol = strings.Replace(wantSymbol, "USDT", "USDTM", 1)
		}
	}

	var orders []*Order
	for _, f := range result.Data {
		if wantSymbol != "" && !strings.EqualFold(f.Symbol, wantSymbol) {
			continue
		}
		price, _ := parseFloat(f.Price)
		ts := time.Unix(0, f.TradeTime) // tradeTime is in nanoseconds
		baseCurrency := ""
		if strings.HasSuffix(f.Symbol, "USDTM") {
			baseCurrency = strings.TrimSuffix(f.Symbol, "USDTM")
		}
		orders = append(orders, &Order{
			ID:            f.OrderID,
			Exchange:      "kucoin",
			Pair:          f.Symbol,
			BaseCurrency:  baseCurrency,
			QuoteCurrency: "USDT",
			Side:          f.Side,
			Type:          f.OrderType,
			Price:         price,
			Amount:        float64(f.Size),
			Filled:        float64(f.Size),
			Status:        "filled",
			CreatedAt:     ts,
			UpdatedAt:     ts,
			UpdateTime:    ts,
			MarketType:    MarketTypeFutures,
		})
	}
	return orders, nil
}
