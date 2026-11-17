// Package xsec implements a market-neutral cross-sectional momentum paper-trading
// bot: rank a liquid universe of perps by trailing momentum, hold the strongest
// long and the weakest short in dollar-neutral baskets, rebalanced weekly.
package xsec

import "time"

// Baskets is the equal-weight long/short selection produced by a Ranker and
// consumed by a Portfolio. Longs are the top-k by momentum, Shorts the bottom-k.
type Baskets struct {
	Longs  []string  // equal-weight long pairs (top-k momentum)
	Shorts []string  // equal-weight short pairs (bottom-k momentum)
	AsOf   time.Time // timestamp of the latest candle used for ranking
}
