package gct

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"sync"
)

// multiplierCache memoizes contract multipliers per futures symbol. It lives
// outside KuCoinRESTClient's struct literal so zero-value clients keep working.
var multiplierCache sync.Map // symbol -> float64

// CachedContractMultiplier returns the face value (base units per contract)
// for a futures symbol, caching successful lookups for the process lifetime
// (multipliers are static contract specs). On any error it returns 1.0 and the
// error so callers can decide whether to log; the value is always usable.
func (k *KuCoinRESTClient) CachedContractMultiplier(ctx context.Context, symbol string) (float64, error) {
	if v, ok := multiplierCache.Load(symbol); ok {
		return v.(float64), nil
	}
	m, err := k.GetContractMultiplier(ctx, symbol)
	if err != nil || m <= 0 {
		if err == nil {
			err = fmt.Errorf("invalid multiplier for %s", symbol)
		}
		return 1.0, err
	}
	multiplierCache.Store(symbol, m)
	return m, nil
}

// CancelAllFuturesOrders cancels every open futures order of the account —
// both regular (limit) orders and untriggered stop/conditional orders — via
// DELETE /api/v1/orders and DELETE /api/v1/stopOrders. symbol may be empty to
// cancel across all contracts. It returns the number of orders KuCoin reports
// as cancelled and the first error encountered (both endpoints are always
// attempted).
func (k *KuCoinRESTClient) CancelAllFuturesOrders(ctx context.Context, symbol string) (int, error) {
	if k == nil {
		return 0, fmt.Errorf("kucoin client is nil")
	}
	if k.apiKey == "" || k.apiSecret == "" {
		return 0, ErrInvalidCredentials
	}
	if ctx == nil {
		ctx = context.Background()
	}

	total := 0
	var firstErr error
	for _, base := range []string{"/api/v1/orders", "/api/v1/stopOrders"} {
		endpoint := base
		if symbol != "" {
			endpoint += "?symbol=" + url.QueryEscape(strings.ToUpper(symbol))
		}
		n, err := k.deleteAndCount(ctx, endpoint)
		total += n
		if err != nil && firstErr == nil {
			firstErr = fmt.Errorf("%s: %w", base, err)
		}
	}
	return total, firstErr
}

// deleteAndCount issues an authenticated DELETE and returns len(cancelledOrderIds).
func (k *KuCoinRESTClient) deleteAndCount(ctx context.Context, endpoint string) (int, error) {
	req, err := k.createAuthenticatedRequest(ctx, "DELETE", endpoint, nil)
	if err != nil {
		return 0, fmt.Errorf("failed to create request: %w", err)
	}
	resp, err := k.doRequestWithRetry(ctx, req, 3)
	if err != nil {
		return 0, fmt.Errorf("failed to execute request: %w", err)
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
		return 0, fmt.Errorf("failed to decode response: %w", err)
	}
	if result.Code != "200000" {
		return 0, MapKuCoinError(result.Code, result.Msg)
	}
	return len(result.Data.CancelledOrderIDs), nil
}

// CancelAllFuturesOrders cancels every open futures order (including stop
// orders) through the direct KuCoin REST path. Deliberately NOT part of the
// GCTClient interface so existing test mocks keep compiling; callers reach it
// via a narrow type assertion.
func (c *client) CancelAllFuturesOrders(ctx context.Context, exchange string) (int, error) {
	if !strings.EqualFold(exchange, "kucoin") || c.kucoinClient == nil || !c.kucoinClient.HasCredentials() {
		return 0, fmt.Errorf("CancelAllFuturesOrders: not supported for exchange %q", exchange)
	}
	return c.kucoinClient.CancelAllFuturesOrders(ctx, "")
}

// ContractMultiplier returns the cached contract face value for a futures
// symbol (e.g. "QNTUSDTM" -> 0.01). Returns 1.0 plus an error when it cannot
// be resolved. Not part of the GCTClient interface (narrow type assertion).
func (c *client) ContractMultiplier(ctx context.Context, exchange, symbol string) (float64, error) {
	if c.kucoinClient == nil || !strings.EqualFold(exchange, "kucoin") {
		return 1.0, fmt.Errorf("ContractMultiplier: not supported for exchange %q", exchange)
	}
	return c.kucoinClient.CachedContractMultiplier(ctx, symbol)
}
