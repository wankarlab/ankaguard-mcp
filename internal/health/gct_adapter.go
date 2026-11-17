package health

import (
	"context"
	"fmt"
)

// GCTClientWrapper wraps a GCT client for health checks.
// It satisfies both the Ping-based interface used by GCTChecker and the
// GCTTickerClient interface used by GCTTickerChecker.
type GCTClientWrapper struct {
	isConnected    func() bool
	getTicker      func(ctx context.Context, exchange, pair string) (interface{}, error)
	getTickerLast  func(ctx context.Context, exchange, pair string) (float64, error)
}

// tickerWithLast is the minimal interface we require from a returned ticker value.
type tickerWithLast interface {
	GetLast() float64
}

// NewGCTClientWrapperFromClient creates a wrapper from a gctclient.Client.
// The client must expose IsConnected() and GetTicker(); the Last price is
// extracted via type assertion on the returned ticker (must have a Last float64 field
// accessible through a GetLast() method, or fall back to the getTickerLastFn).
func NewGCTClientWrapperFromClient(client interface {
	IsConnected() bool
	GetTicker(ctx context.Context, exchange, pair string) (interface{}, error)
}) *GCTClientWrapper {
	return &GCTClientWrapper{
		isConnected: client.IsConnected,
		getTicker:   client.GetTicker,
		// getTickerLast falls back to nil; callers that need it should use
		// NewGCTClientWrapper and supply the function explicitly.
	}
}

// NewGCTClientWrapper creates a wrapper with custom functions.
// getTickerLast may be nil; in that case GCTTickerChecker is not usable via this wrapper.
func NewGCTClientWrapper(
	isConnected func() bool,
	getTicker func(ctx context.Context, exchange, pair string) (interface{}, error),
) *GCTClientWrapper {
	return &GCTClientWrapper{
		isConnected: isConnected,
		getTicker:   getTicker,
	}
}

// NewGCTClientWrapperWithTickerFn creates a wrapper that supports both ping-based checks
// and ticker-level checks.  getTickerLast should call the underlying client and return
// the last-traded price (or an error when unavailable).
func NewGCTClientWrapperWithTickerFn(
	isConnected func() bool,
	getTicker func(ctx context.Context, exchange, pair string) (interface{}, error),
	getTickerLast func(ctx context.Context, exchange, pair string) (float64, error),
) *GCTClientWrapper {
	return &GCTClientWrapper{
		isConnected:   isConnected,
		getTicker:     getTicker,
		getTickerLast: getTickerLast,
	}
}

// Ping checks if GCT is responsive (satisfies the GCTChecker client interface).
func (w *GCTClientWrapper) Ping(ctx context.Context) error {
	if !w.isConnected() {
		return fmt.Errorf("not connected")
	}
	return nil
}

// GetTicker gets a ticker for health check (satisfies the ExchangeChecker client interface).
func (w *GCTClientWrapper) GetTicker(ctx context.Context, exchange, pair string) (interface{}, error) {
	return w.getTicker(ctx, exchange, pair)
}

// IsConnected reports whether the wrapped client is currently connected.
// This satisfies the GCTTickerClient interface.
func (w *GCTClientWrapper) IsConnected() bool {
	return w.isConnected()
}

// GetTickerLast returns the last-traded price for the given exchange/pair.
// This satisfies the GCTTickerClient interface.
func (w *GCTClientWrapper) GetTickerLast(ctx context.Context, exchange, pair string) (float64, error) {
	if w.getTickerLast != nil {
		return w.getTickerLast(ctx, exchange, pair)
	}
	// Fallback: call getTicker and type-assert the Last field.
	if w.getTicker == nil {
		return 0, fmt.Errorf("no ticker function configured")
	}
	raw, err := w.getTicker(ctx, exchange, pair)
	if err != nil {
		return 0, err
	}
	// Extract Last via reflection-free struct accessor.
	if t, ok := raw.(interface{ GetLast() float64 }); ok {
		return t.GetLast(), nil
	}
	// Best-effort: the caller-supplied wrapper may return a *models.Ticker-like
	// struct. We cannot type-assert without importing that package, so surface a
	// clear error instead of silently returning 0.
	return 0, fmt.Errorf("ticker value does not implement GetLast(); provide getTickerLast via NewGCTClientWrapperWithTickerFn")
}
