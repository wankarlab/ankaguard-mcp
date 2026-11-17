package patterns

import (
	"time"

	"github.com/rantipay/trading-mcp/internal/ai"
)

type Candle = ai.Candle

type Pattern struct {
	Hash        string
	Pair        string
	Timeframe   string
	Length      int
	Description string
	Features    []float64
}

type PatternOutcome struct {
	PatternHash string
	DetectedAt  time.Time
	PriceAt     float64
	Resolution  string
	PriceChange float64
	NCandles    int
	Won         bool
}

type PatternStats struct {
	Hash           string
	Pair           string
	SampleSize     int
	BullishCount   int
	BearishCount   int
	NeutralCount   int
	AvgPriceChange float64
	SuccessRate    float64
	LastSeen       time.Time
	Reliability    string
}
