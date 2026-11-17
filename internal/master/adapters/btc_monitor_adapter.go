package adapters

import (
	"github.com/sirupsen/logrus"

	aiCore "github.com/rantipay/trading-mcp/internal/ai"
	"github.com/rantipay/trading-mcp/internal/master"
)

// BTCMonitorAdapter wraps *aiCore.BTCCorrelationMonitor to satisfy
// master.BTCMonitor. It is used by the Master Agent's MarketScanner to
// penalize altcoin opportunities that move in lockstep with BTC (lower
// hedge value) and favor decorrelated pairs.
//
// The underlying monitor exposes correlations as a snapshot map keyed by
// trading symbol (e.g. "ETHUSDT"). When the requested pair is not yet
// tracked or correlations have not been computed, the adapter returns 0
// (neutral) so the scanner's BTC penalty term naturally collapses to 0
// without producing an error.
type BTCMonitorAdapter struct {
	monitor *aiCore.BTCCorrelationMonitor
	log     *logrus.Logger
}

// NewBTCMonitorAdapter wraps an aiCore.BTCCorrelationMonitor as a
// master.BTCMonitor. The logger is used to emit Debug-level traces when
// the underlying monitor cannot answer a correlation query (e.g. pair
// not tracked, monitor not started, insufficient history). A nil logger
// is replaced with a default logrus instance.
func NewBTCMonitorAdapter(monitor *aiCore.BTCCorrelationMonitor, log *logrus.Logger) *BTCMonitorAdapter {
	if log == nil {
		log = logrus.New()
	}
	return &BTCMonitorAdapter{monitor: monitor, log: log}
}

// GetCorrelation returns the Pearson correlation between the given pair
// and BTC over the monitor's configured window. Returns 0 when the
// underlying monitor is nil, the pair is not tracked yet, or insufficient
// price history has been gathered. Any miss is logged at Debug so it
// does not pollute production logs but remains diagnosable.
//
// Note on key formatting: aiCore.BTCCorrelationMonitor stores
// correlations keyed by the exchange symbol format used at ingestion
// time (typically "BASEUSDT", e.g. "ETHUSDT"), while the master.MarketScanner
// works with hyphenated pairs (e.g. "ETH-USDT"). The adapter tries the
// caller-supplied key first, then a hyphen-stripped variant, before
// giving up and returning 0.
func (a *BTCMonitorAdapter) GetCorrelation(pair string) float64 {
	if a == nil || a.monitor == nil {
		return 0
	}

	correlations := a.monitor.GetCorrelations()

	// Try the caller-supplied key verbatim.
	if r, ok := correlations[pair]; ok {
		return r.Correlation
	}

	// Try hyphen-stripped variant (e.g. "ETH-USDT" -> "ETHUSDT").
	stripped := stripHyphens(pair)
	if stripped != pair {
		if r, ok := correlations[stripped]; ok {
			return r.Correlation
		}
	}

	a.log.WithField("pair", pair).Debug("btc_monitor_adapter: no correlation tracked for pair, returning 0")
	return 0
}

// stripHyphens removes ASCII hyphens from s without allocating when the
// input has none.
func stripHyphens(s string) string {
	hasHyphen := false
	for i := 0; i < len(s); i++ {
		if s[i] == '-' {
			hasHyphen = true
			break
		}
	}
	if !hasHyphen {
		return s
	}
	b := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		if s[i] != '-' {
			b = append(b, s[i])
		}
	}
	return string(b)
}

// Ensure BTCMonitorAdapter satisfies the master.BTCMonitor interface
// at compile time.
var _ master.BTCMonitor = (*BTCMonitorAdapter)(nil)
