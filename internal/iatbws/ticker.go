package websocket

import (
	"time"

	"github.com/shopspring/decimal"
)

// Ticker is the market snapshot consumed by GCTPriceSource. It mirrors the
// fields of the iatb gct.Ticker type that the price source reads, so this
// package no longer depends on the iatb module (the product build ships
// without it).
type Ticker struct {
	Exchange      string
	Symbol        string
	Bid           decimal.Decimal
	Ask           decimal.Decimal
	Last          decimal.Decimal
	High          decimal.Decimal
	Low           decimal.Decimal
	Volume        decimal.Decimal
	QuoteVolume   decimal.Decimal
	Open          decimal.Decimal
	Change        decimal.Decimal
	ChangePercent decimal.Decimal
	Timestamp     time.Time
}
