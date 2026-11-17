package gctclient

import (
	"context"
	"encoding/base64"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/rantipay/trading-mcp/internal/mcp-trading/models"
	"github.com/thrasher-corp/gocryptotrader/gctrpc"
	"google.golang.org/grpc/metadata"
)

// fundingAssetType is the GCT asset type that carries a funding rate. Funding
// only exists on perpetual futures, so every funding lookup is scoped to it.
const fundingAssetType = "futures"

// gctTimeLayout is the date format GoCryptoTrader's gRPC API expects for
// StartDate/EndDate (NOT RFC3339). Mismatch yields a server-side parse error.
const gctTimeLayout = "2006-01-02 15:04:05 MST"

// fundingRPC builds a GCT gRPC service client over the shared connection and
// attaches Basic-auth metadata, mirroring internal/gct.client.withAuth. Returns
// an error when the underlying connection is not available.
func (c *Client) fundingRPC(ctx context.Context) (gctrpc.GoCryptoTraderServiceClient, context.Context, error) {
	conn := c.gctClient.GetGRPCConn()
	if conn == nil {
		return nil, ctx, fmt.Errorf("no GCT gRPC connection available")
	}
	if c.config != nil && c.config.Username != "" && c.config.Password != "" {
		auth := base64.StdEncoding.EncodeToString(
			[]byte(fmt.Sprintf("%s:%s", c.config.Username, c.config.Password)))
		md := metadata.New(map[string]string{"authorization": "Basic " + auth})
		ctx = metadata.NewOutgoingContext(ctx, md)
	}
	return gctrpc.NewGoCryptoTraderServiceClient(conn), ctx, nil
}

// fundingCurrencyPair splits a pair like "BTC-USDT" or "XBTUSDTM" into a
// gctrpc.CurrencyPair. Delimited pairs are split on "-"; otherwise the whole
// string is passed as the base (GCT resolves the contract for the exchange).
func fundingCurrencyPair(pair string) *gctrpc.CurrencyPair {
	if i := strings.IndexAny(pair, "-_/"); i > 0 {
		delim := string(pair[i])
		return &gctrpc.CurrencyPair{
			Delimiter: delim,
			Base:      pair[:i],
			Quote:     pair[i+1:],
		}
	}
	return &gctrpc.CurrencyPair{Base: pair}
}

// GetFundingRate retrieves the current (latest realized) funding rate for a
// perpetual futures pair from GoCryptoTrader via gRPC.
//
// This used to return a hard-coded 0.0001 stub, which silently fed the funding
// engine a zero-signal constant. It now calls the real GetLatestFundingRate RPC
// and returns an error on failure rather than faking a neutral rate, so callers
// can distinguish "no data" from "genuinely neutral funding".
func (c *Client) GetFundingRate(ctx context.Context, exchange, pair string) (*models.FundingRateData, error) {
	rpc, authCtx, err := c.fundingRPC(ctx)
	if err != nil {
		return nil, err
	}

	resp, err := rpc.GetLatestFundingRate(authCtx, &gctrpc.GetLatestFundingRateRequest{
		Exchange:         exchange,
		Asset:            fundingAssetType,
		Pair:             fundingCurrencyPair(pair),
		IncludePredicted: true,
	})
	if err != nil {
		c.log.WithFields(map[string]interface{}{
			"exchange": exchange,
			"pair":     pair,
		}).WithError(err).Debug("live funding rate fetch failed")
		return nil, fmt.Errorf("get funding rate %s %s: %w", exchange, pair, err)
	}
	if resp == nil || resp.Rate == nil {
		return nil, fmt.Errorf("no funding rate data for %s %s", exchange, pair)
	}

	data := &models.FundingRateData{
		Exchange:  exchange,
		Pair:      pair,
		Timestamp: time.Now(),
	}

	if lr := resp.Rate.LatestRate; lr != nil {
		data.FundingRate = parseFundingFloat(lr.Rate)
	}
	if ur := resp.Rate.UpcomingRate; ur != nil {
		data.Predicted = parseFundingFloat(ur.Rate)
	} else {
		data.Predicted = data.FundingRate
	}

	if resp.Rate.TimeOfNextRate != "" {
		if t, perr := parseFundingTime(resp.Rate.TimeOfNextRate); perr == nil {
			data.NextFunding = t
		}
	}
	if data.NextFunding.IsZero() {
		data.NextFunding = c.calculateNextFunding()
	}

	c.log.WithFields(map[string]interface{}{
		"exchange":     exchange,
		"pair":         pair,
		"funding_rate": data.FundingRate,
		"predicted":    data.Predicted,
		"next_funding": data.NextFunding,
	}).Debug("live funding rate fetched")

	return data, nil
}

// calculateNextFunding calculates the next funding time (every 8 hours at 00:00, 08:00, 16:00 UTC)
func (c *Client) calculateNextFunding() time.Time {
	now := time.Now().UTC()
	hour := now.Hour()

	// Find next funding hour (0, 8, or 16)
	var nextHour int
	switch {
	case hour < 8:
		nextHour = 8
	case hour < 16:
		nextHour = 16
	default:
		nextHour = 24 // Next day at 00:00
	}

	nextFunding := time.Date(now.Year(), now.Month(), now.Day(), nextHour, 0, 0, 0, time.UTC)
	if nextHour == 24 {
		nextFunding = nextFunding.AddDate(0, 0, 1)
		nextFunding = time.Date(nextFunding.Year(), nextFunding.Month(), nextFunding.Day(), 0, 0, 0, 0, time.UTC)
	}

	return nextFunding
}

// GetFundingHistory retrieves historical funding rates from GoCryptoTrader via
// the GetFundingRates RPC. Returns one entry per settled funding period in the
// requested window, oldest first. Used to backtest funding-carry economics
// offline before risking capital.
func (c *Client) GetFundingHistory(ctx context.Context, exchange, pair string, startTime, endTime time.Time) ([]*models.FundingRateData, error) {
	rpc, authCtx, err := c.fundingRPC(ctx)
	if err != nil {
		return nil, err
	}

	resp, err := rpc.GetFundingRates(authCtx, &gctrpc.GetFundingRatesRequest{
		Exchange:         exchange,
		Asset:            fundingAssetType,
		Pair:             fundingCurrencyPair(pair),
		StartDate:        startTime.UTC().Format(gctTimeLayout),
		EndDate:          endTime.UTC().Format(gctTimeLayout),
		IncludePredicted: true,
	})
	if err != nil {
		c.log.WithFields(map[string]interface{}{
			"exchange": exchange,
			"pair":     pair,
		}).WithError(err).Debug("funding history fetch failed")
		return nil, fmt.Errorf("get funding history %s %s: %w", exchange, pair, err)
	}
	if resp == nil || resp.Rates == nil {
		return []*models.FundingRateData{}, nil
	}

	out := make([]*models.FundingRateData, 0, len(resp.Rates.Rates))
	for _, r := range resp.Rates.Rates {
		if r == nil {
			continue
		}
		ts, _ := parseFundingTime(r.Date)
		out = append(out, &models.FundingRateData{
			Exchange:    exchange,
			Pair:        pair,
			FundingRate: parseFundingFloat(r.Rate),
			Timestamp:   ts,
		})
	}
	return out, nil
}

// AnalyzeFundingRate provides analysis of funding rate conditions
func (c *Client) AnalyzeFundingRate(rate float64) string {
	switch {
	case rate > 0.001: // >0.1%
		return "EXTREME_POSITIVE - Very high funding, many longs, potential squeeze down"
	case rate > 0.0003: // >0.03%
		return "ELEVATED - High funding, longs dominating, consider shorts"
	case rate > 0.0001: // >0.01%
		return "POSITIVE - Slightly bullish positioning"
	case rate >= -0.0001 && rate <= 0.0001: // Between -0.01% and 0.01%
		return "NEUTRAL - Balanced market"
	case rate < -0.001: // <-0.1%
		return "EXTREME_NEGATIVE - Very negative funding, consider longs"
	case rate < -0.0003: // <-0.03%
		return "NEGATIVE - High funding for shorts, many shorts, potential squeeze up"
	default:
		return "SLIGHTLY_NEGATIVE - Slightly bearish positioning"
	}
}

// parseFundingFloat parses a funding rate string into a float64, returning 0 on
// failure. GCT returns rates as decimal strings (e.g. "0.0001" = 0.01%).
func parseFundingFloat(s string) float64 {
	if s == "" {
		return 0
	}
	v, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	if err != nil {
		return 0
	}
	return v
}

// parseFundingTime parses GCT funding timestamps, tolerating RFC3339 and a few
// common fallbacks.
func parseFundingTime(s string) (time.Time, error) {
	s = strings.TrimSpace(s)
	layouts := []string{time.RFC3339Nano, time.RFC3339, gctTimeLayout, "2006-01-02 15:04:05 -0700 MST", "2006-01-02 15:04:05"}
	for _, l := range layouts {
		if t, err := time.Parse(l, s); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("unparseable funding time %q", s)
}
