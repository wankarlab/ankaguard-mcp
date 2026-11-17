package orderflow

import (
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/sirupsen/logrus"
)

const (
	defaultDepthLevels       = 20
	defaultWallThreshold     = 5.0
	defaultImbalanceThresh   = 0.3
	strongImbalanceThreshold = 0.5
	wallProximityLevels      = 5
)

type Analyzer struct {
	log                *logrus.Logger
	depthLevels        int
	wallThreshold      float64
	imbalanceThreshold float64
}

func NewAnalyzer(log *logrus.Logger) *Analyzer {
	if log == nil {
		log = logrus.New()
	}
	return &Analyzer{
		log:                log,
		depthLevels:        defaultDepthLevels,
		wallThreshold:      defaultWallThreshold,
		imbalanceThreshold: defaultImbalanceThresh,
	}
}

func (a *Analyzer) Analyze(snapshot *OrderBookSnapshot) (*FlowAnalysis, error) {
	if snapshot == nil {
		return nil, errors.New("snapshot is nil")
	}
	if len(snapshot.Bids) == 0 || len(snapshot.Asks) == 0 {
		return nil, errors.New("snapshot has empty bids or asks")
	}

	ts := snapshot.Timestamp
	if ts.IsZero() {
		ts = time.Now().UTC()
	}

	bidDepth := minInt(a.depthLevels, len(snapshot.Bids))
	askDepth := minInt(a.depthLevels, len(snapshot.Asks))

	bidVolume := sumVolume(snapshot.Bids[:bidDepth])
	askVolume := sumVolume(snapshot.Asks[:askDepth])

	totalVolume := bidVolume + askVolume
	imbalance := 0.0
	if totalVolume > 0 {
		imbalance = (bidVolume - askVolume) / totalVolume
	}

	bestBid := snapshot.Bids[0].Price
	bestAsk := snapshot.Asks[0].Price
	spread := bestAsk - bestBid
	midPrice := (bestBid + bestAsk) / 2.0
	spreadPercent := 0.0
	if midPrice > 0 {
		spreadPercent = spread / midPrice
	}

	bidAvg := 0.0
	if bidDepth > 0 {
		bidAvg = bidVolume / float64(bidDepth)
	}
	askAvg := 0.0
	if askDepth > 0 {
		askAvg = askVolume / float64(askDepth)
	}

	largestBidWall, bidWallIdx := a.detectWall(snapshot.Bids[:bidDepth], bidAvg)
	largestAskWall, askWallIdx := a.detectWall(snapshot.Asks[:askDepth], askAvg)

	buyPressure := classifyPressure(imbalance)
	sellPressure := classifyPressure(-imbalance)

	signal := "NEUTRAL"
	signalStrength := 0.0
	reasoning := ""

	askWallNear := largestAskWall != nil && askWallIdx >= 0 && askWallIdx < wallProximityLevels
	bidWallNear := largestBidWall != nil && bidWallIdx >= 0 && bidWallIdx < wallProximityLevels

	switch {
	case imbalance > a.imbalanceThreshold && !askWallNear:
		signal = "BULLISH"
		signalStrength = clamp01(imbalance)
		reasoning = fmt.Sprintf("imbalance=%.3f favors bids and no near ask wall", imbalance)
	case imbalance < -a.imbalanceThreshold && !bidWallNear:
		signal = "BEARISH"
		signalStrength = clamp01(math.Abs(imbalance))
		reasoning = fmt.Sprintf("imbalance=%.3f favors asks and no near bid wall", imbalance)
	case imbalance > a.imbalanceThreshold && askWallNear:
		signal = "NEUTRAL"
		signalStrength = 0.0
		reasoning = fmt.Sprintf("bid pressure capped by near ask wall at level %d", askWallIdx)
	case imbalance < -a.imbalanceThreshold && bidWallNear:
		signal = "NEUTRAL"
		signalStrength = 0.0
		reasoning = fmt.Sprintf("ask pressure capped by near bid wall at level %d", bidWallIdx)
	default:
		signal = "NEUTRAL"
		signalStrength = 0.0
		reasoning = fmt.Sprintf("imbalance=%.3f below threshold=%.2f", imbalance, a.imbalanceThreshold)
	}

	a.log.WithFields(logrus.Fields{
		"pair":      snapshot.Pair,
		"imbalance": imbalance,
		"signal":    signal,
		"strength":  signalStrength,
		"spread":    spread,
	}).Debug("order book flow analyzed")

	return &FlowAnalysis{
		Pair:           snapshot.Pair,
		Timestamp:      ts,
		BidVolume:      bidVolume,
		AskVolume:      askVolume,
		Imbalance:      imbalance,
		Spread:         spread,
		SpreadPercent:  spreadPercent,
		BestBid:        bestBid,
		BestAsk:        bestAsk,
		MidPrice:       midPrice,
		LargestBidWall: largestBidWall,
		LargestAskWall: largestAskWall,
		BuyPressure:    buyPressure,
		SellPressure:   sellPressure,
		Signal:         signal,
		SignalStrength: signalStrength,
		Reasoning:      reasoning,
	}, nil
}

func (a *Analyzer) detectWall(levels []OrderLevel, avg float64) (*OrderLevel, int) {
	if len(levels) == 0 || avg <= 0 {
		return nil, -1
	}
	threshold := a.wallThreshold * avg
	var largest *OrderLevel
	idx := -1
	for i := range levels {
		if levels[i].Size >= threshold {
			if largest == nil || levels[i].Size > largest.Size {
				lv := levels[i]
				largest = &lv
				idx = i
			}
		}
	}
	return largest, idx
}

func classifyPressure(imbalance float64) string {
	switch {
	case imbalance <= 0:
		return "NONE"
	case imbalance > strongImbalanceThreshold:
		return "STRONG"
	case imbalance > defaultImbalanceThresh:
		return "MODERATE"
	default:
		return "WEAK"
	}
}

func sumVolume(levels []OrderLevel) float64 {
	total := 0.0
	for i := range levels {
		total += levels[i].Size
	}
	return total
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func clamp01(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}
