package technical

import (
	"fmt"
	"math"
	"sort"

	"github.com/rantipay/trading-mcp/internal/mcp-trading/models"
)

// Pure indicator math over ascending candles. Every function uses only the
// candles it is given and returns the value at the last candle.

func typicalPrice(c models.Candle) float64 { return (c.High + c.Low + c.Close) / 3 }

func highestHigh(c []models.Candle) float64 {
	v := c[0].High
	for _, x := range c {
		v = math.Max(v, x.High)
	}
	return v
}

func lowestLow(c []models.Candle) float64 {
	v := c[0].Low
	for _, x := range c {
		v = math.Min(v, x.Low)
	}
	return v
}

func trueRange(cur, prev models.Candle) float64 {
	return math.Max(cur.High-cur.Low, math.Max(math.Abs(cur.High-prev.Close), math.Abs(cur.Low-prev.Close)))
}

// atrValue is Wilder's Average True Range.
func atrValue(c []models.Candle, period int) float64 {
	sum := 0.0
	for i := 1; i <= period; i++ {
		sum += trueRange(c[i], c[i-1])
	}
	atr := sum / float64(period)
	for i := period + 1; i < len(c); i++ {
		atr = (atr*float64(period-1) + trueRange(c[i], c[i-1])) / float64(period)
	}
	return atr
}

// mfiValue is the Money Flow Index over the last `period` candles.
func mfiValue(c []models.Candle, period int) float64 {
	pos, neg := 0.0, 0.0
	for i := len(c) - period; i < len(c); i++ {
		flow := typicalPrice(c[i]) * c[i].Volume
		switch {
		case typicalPrice(c[i]) > typicalPrice(c[i-1]):
			pos += flow
		case typicalPrice(c[i]) < typicalPrice(c[i-1]):
			neg += flow
		}
	}
	if neg == 0 {
		if pos == 0 {
			return 50
		}
		return 100
	}
	return 100 - 100/(1+pos/neg)
}

// stochasticValue returns %K at the last candle and %D, the mean of the last dPeriod %K values.
func stochasticValue(c []models.Candle, kPeriod, dPeriod int) (k, d float64) {
	kAt := func(end int) float64 {
		w := c[end-kPeriod+1 : end+1]
		hh, ll := highestHigh(w), lowestLow(w)
		if hh == ll {
			return 50
		}
		return (c[end].Close - ll) / (hh - ll) * 100
	}
	sum := 0.0
	for i := len(c) - dPeriod; i < len(c); i++ {
		sum += kAt(i)
	}
	return kAt(len(c) - 1), sum / float64(dPeriod)
}

// adxValue returns ADX, +DI and -DI (Wilder smoothing).
func adxValue(c []models.Candle, period int) (adx, plusDI, minusDI float64) {
	n := float64(period)
	var trS, pdmS, mdmS float64
	for i := 1; i <= period; i++ {
		tr, pdm, mdm := dmStep(c[i], c[i-1])
		trS, pdmS, mdmS = trS+tr, pdmS+pdm, mdmS+mdm
	}
	dx := func() float64 {
		if trS == 0 {
			return 0
		}
		plusDI, minusDI = 100*pdmS/trS, 100*mdmS/trS
		if plusDI+minusDI == 0 {
			return 0
		}
		return 100 * math.Abs(plusDI-minusDI) / (plusDI + minusDI)
	}
	dxs := []float64{dx()}
	for i := period + 1; i < len(c); i++ {
		tr, pdm, mdm := dmStep(c[i], c[i-1])
		trS = trS - trS/n + tr
		pdmS = pdmS - pdmS/n + pdm
		mdmS = mdmS - mdmS/n + mdm
		dxs = append(dxs, dx())
	}
	if len(dxs) < period {
		return 0, plusDI, minusDI
	}
	adx = 0
	for _, v := range dxs[:period] {
		adx += v
	}
	adx /= n
	for _, v := range dxs[period:] {
		adx = (adx*(n-1) + v) / n
	}
	return adx, plusDI, minusDI
}

func dmStep(cur, prev models.Candle) (tr, pdm, mdm float64) {
	up, down := cur.High-prev.High, prev.Low-cur.Low
	if up > down && up > 0 {
		pdm = up
	}
	if down > up && down > 0 {
		mdm = down
	}
	return trueRange(cur, prev), pdm, mdm
}

// cciValue is the Commodity Channel Index over the last `period` candles.
func cciValue(c []models.Candle, period int) float64 {
	w := c[len(c)-period:]
	mean := 0.0
	for _, x := range w {
		mean += typicalPrice(x)
	}
	mean /= float64(period)
	dev := 0.0
	for _, x := range w {
		dev += math.Abs(typicalPrice(x) - mean)
	}
	dev /= float64(period)
	if dev == 0 {
		return 0
	}
	return (typicalPrice(w[len(w)-1]) - mean) / (0.015 * dev)
}

// williamsRValue is Williams %R (range -100..0).
func williamsRValue(c []models.Candle, period int) float64 {
	w := c[len(c)-period:]
	hh, ll := highestHigh(w), lowestLow(w)
	if hh == ll {
		return -50
	}
	return (hh - c[len(c)-1].Close) / (hh - ll) * -100
}

// obvValue is On Balance Volume accumulated over the given window (its absolute
// level depends on where the window starts; read it as a trend, not a level).
func obvValue(c []models.Candle) float64 {
	obv := 0.0
	for i := 1; i < len(c); i++ {
		switch {
		case c[i].Close > c[i-1].Close:
			obv += c[i].Volume
		case c[i].Close < c[i-1].Close:
			obv -= c[i].Volume
		}
	}
	return obv
}

// vwapValue is the volume weighted average of the typical price over the window.
func vwapValue(c []models.Candle) (float64, error) {
	pv, vol := 0.0, 0.0
	for _, x := range c {
		pv += typicalPrice(x) * x.Volume
		vol += x.Volume
	}
	if vol == 0 {
		return 0, fmt.Errorf("total volume of the window is zero")
	}
	return pv / vol, nil
}

// ichimokuValues returns the standard (9, 26, 52) Ichimoku lines at the last candle.
// Senkou A/B are the values that the cloud projects 26 periods ahead.
func ichimokuValues(c []models.Candle) map[string]float64 {
	mid := func(n int) float64 {
		w := c[len(c)-n:]
		return (highestHigh(w) + lowestLow(w)) / 2
	}
	tenkan, kijun := mid(9), mid(26)
	return map[string]float64{
		"tenkan_sen":    tenkan,
		"kijun_sen":     kijun,
		"senkou_span_a": (tenkan + kijun) / 2,
		"senkou_span_b": mid(52),
		"last_close":    c[len(c)-1].Close,
	}
}

// volatilityValue is the sample standard deviation of the last `period` simple returns.
func volatilityValue(c []models.Candle, period int) float64 {
	rets := make([]float64, 0, period)
	for i := len(c) - period; i < len(c); i++ {
		rets = append(rets, c[i].Close/c[i-1].Close-1)
	}
	m := 0.0
	for _, r := range rets {
		m += r
	}
	m /= float64(len(rets))
	v := 0.0
	for _, r := range rets {
		v += (r - m) * (r - m)
	}
	return math.Sqrt(v / float64(len(rets)-1))
}

// ascending sorts candles oldest first (some pairs come back newest first) and
// drops candles with no valid price.
func ascending(in []models.Candle) []models.Candle {
	out := make([]models.Candle, 0, len(in))
	for _, c := range in {
		if c.Close > 0 && c.High > 0 && c.Low > 0 {
			out = append(out, c)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Timestamp.Before(out[j].Timestamp) })
	return out
}
