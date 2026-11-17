package orderflow

import "time"

type OrderLevel struct {
	Price float64
	Size  float64
}

type OrderBookSnapshot struct {
	Pair      string
	Timestamp time.Time
	Bids      []OrderLevel
	Asks      []OrderLevel
}

type FlowAnalysis struct {
	Pair           string
	Timestamp      time.Time
	BidVolume      float64
	AskVolume      float64
	Imbalance      float64
	Spread         float64
	SpreadPercent  float64
	BestBid        float64
	BestAsk        float64
	MidPrice       float64
	LargestBidWall *OrderLevel
	LargestAskWall *OrderLevel
	BuyPressure    string
	SellPressure   string
	Signal         string
	SignalStrength float64
	Reasoning      string
}
