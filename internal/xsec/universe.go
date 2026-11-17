package xsec

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"
)

// defaultContractsURL is the public KuCoin USDTM futures endpoint that lists
// all active perpetual contracts. It requires no authentication.
const defaultContractsURL = "https://api-futures.kucoin.com/api/v1/contracts/active"

// Universe provides the set of liquid, shorteable KuCoin USDTM perps.
type Universe struct {
	client   *http.Client
	baseURL  string
	klineURL string
}

// NewKuCoinUniverse creates a Universe with the given HTTP timeout.
func NewKuCoinUniverse(timeout time.Duration) *Universe {
	return &Universe{
		client:   &http.Client{Timeout: timeout},
		baseURL:  defaultContractsURL,
		klineURL: defaultKlineURL,
	}
}

// contractsResponse mirrors the KuCoin contracts/active payload.
type contractsResponse struct {
	Code string     `json:"code"`
	Data []contract `json:"data"`
}

// contract is a single active futures contract. turnoverOf24h is the 24h
// turnover quoted in USD and may be serialized as a JSON number. multiplier is
// the contract size (1 contract = multiplier × base units), needed to size a
// leg into whole contracts and to know a pair's minimum notional.
type contract struct {
	Symbol        string  `json:"symbol"`
	TurnoverOf24h float64 `json:"turnoverOf24h"`
	Multiplier    float64 `json:"multiplier"`
}

// LiquidPairs returns pairs in "BASE-USDT" format whose 24h turnover (USD) >= minUSD,
// sorted by turnover descending. Symbol mapping: "XBTUSDTM" -> "BTC-USDT",
// "<BASE>USDTM" -> "<BASE>-USDT". Skip non-USDTM symbols. Returns error on HTTP/JSON failure.
func (u *Universe) LiquidPairs(ctx context.Context, minUSD float64) ([]string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.baseURL, nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}

	resp, err := u.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch contracts: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status %d from %s", resp.StatusCode, u.baseURL)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read body: %w", err)
	}

	return parseLiquid(body, minUSD)
}

// parseLiquid is the pure parse+filter+map+sort core used by LiquidPairs.
// It decodes the contracts payload, keeps USDTM symbols whose 24h turnover
// meets minUSD, maps them to "BASE-USDT" format, and sorts by turnover desc.
func parseLiquid(data []byte, minUSD float64) ([]string, error) {
	var parsed contractsResponse
	if err := json.Unmarshal(data, &parsed); err != nil {
		return nil, fmt.Errorf("decode contracts: %w", err)
	}

	type rankedPair struct {
		pair     string
		turnover float64
	}

	ranked := make([]rankedPair, 0, len(parsed.Data))
	for _, c := range parsed.Data {
		if !strings.HasSuffix(c.Symbol, "USDTM") {
			continue
		}
		if c.TurnoverOf24h < minUSD {
			continue
		}

		base := strings.TrimSuffix(c.Symbol, "USDTM")
		if base == "" {
			continue
		}
		// KuCoin lists Bitcoin as XBT; normalize to the conventional BTC ticker.
		if base == "XBT" {
			base = "BTC"
		}

		ranked = append(ranked, rankedPair{
			pair:     base + "-USDT",
			turnover: c.TurnoverOf24h,
		})
	}

	sort.SliceStable(ranked, func(i, j int) bool {
		return ranked[i].turnover > ranked[j].turnover
	})

	pairs := make([]string, len(ranked))
	for i, r := range ranked {
		pairs[i] = r.pair
	}
	return pairs, nil
}

// ContractMultipliers returns pair→contract multiplier ("BASE-USDT" format) for
// every active USDTM perp. The executor uses this to pre-filter candidates to
// pairs whose 1-contract notional (multiplier × price) fits the per-leg budget,
// so it never selects a pair the notional guard would reject at execution —
// the asymmetric-fill / imbalance failure mode (2026-07-26: AKE/BEAT, 1 contract
// $31/$35, kept failing on the long side). Best-effort: callers skip the
// pre-filter on error and rely on the runtime guard as before.
func (u *Universe) ContractMultipliers(ctx context.Context) (map[string]float64, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.baseURL, nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	resp, err := u.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch contracts: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status %d from %s", resp.StatusCode, u.baseURL)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read body: %w", err)
	}
	var parsed contractsResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("decode contracts: %w", err)
	}
	out := make(map[string]float64, len(parsed.Data))
	for _, c := range parsed.Data {
		if !strings.HasSuffix(c.Symbol, "USDTM") || c.Multiplier == 0 {
			continue
		}
		base := strings.TrimSuffix(c.Symbol, "USDTM")
		if base == "" {
			continue
		}
		if base == "XBT" {
			base = "BTC"
		}
		out[base+"-USDT"] = c.Multiplier
	}
	return out, nil
}
