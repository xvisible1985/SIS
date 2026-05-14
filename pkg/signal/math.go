package signal

import "math"

// ── EMA ───────────────────────────────────────────────────────────────────

// ema returns an Exponential Moving Average series (k = 2/(period+1)).
// The returned slice is shorter than values by (period-1).
func ema(values []float64, period int) []float64 {
	if len(values) < period {
		return nil
	}
	k := 2.0 / float64(period+1)
	out := make([]float64, 0, len(values)-period+1)

	// Seed: SMA of first `period` values
	sum := 0.0
	for i := 0; i < period; i++ {
		sum += values[i]
	}
	e := sum / float64(period)
	out = append(out, e)

	for i := period; i < len(values); i++ {
		e = values[i]*k + e*(1-k)
		out = append(out, e)
	}
	return out
}

// ── SMA ───────────────────────────────────────────────────────────────────

// sma returns a Simple Moving Average series.
func sma(values []float64, period int) []float64 {
	if len(values) < period {
		return nil
	}
	out := make([]float64, 0, len(values)-period+1)
	sum := 0.0
	for i := 0; i < period; i++ {
		sum += values[i]
	}
	out = append(out, sum/float64(period))
	for i := period; i < len(values); i++ {
		sum += values[i] - values[i-period]
		out = append(out, sum/float64(period))
	}
	return out
}

// ── RSI (Wilder's smoothing) ──────────────────────────────────────────────

// rsi returns an RSI series matching the technicalindicators library output.
func rsi(closes []float64, period int) []float64 {
	if len(closes) < period+1 {
		return nil
	}
	gains := make([]float64, len(closes)-1)
	losses := make([]float64, len(closes)-1)
	for i := 1; i < len(closes); i++ {
		d := closes[i] - closes[i-1]
		if d > 0 {
			gains[i-1] = d
		} else {
			losses[i-1] = -d
		}
	}

	// Seed averages
	var ag, al float64
	for i := 0; i < period; i++ {
		ag += gains[i]
		al += losses[i]
	}
	ag /= float64(period)
	al /= float64(period)

	toRSI := func(g, l float64) float64 {
		if l == 0 {
			return 100
		}
		return 100 - 100/(1+g/l)
	}

	out := make([]float64, 0, len(gains)-period+1)
	out = append(out, toRSI(ag, al))

	for i := period; i < len(gains); i++ {
		ag = (ag*float64(period-1) + gains[i]) / float64(period)
		al = (al*float64(period-1) + losses[i]) / float64(period)
		out = append(out, toRSI(ag, al))
	}
	return out
}

// ── MACD ──────────────────────────────────────────────────────────────────

type macdPoint struct {
	MACD      float64
	Signal    float64
	Histogram float64
}

// macd returns MACD histogram series (EMA-based, matching technicalindicators).
func macdCalc(closes []float64, fast, slow, sigPeriod int) []macdPoint {
	fastEMA := ema(closes, fast)
	slowEMA := ema(closes, slow)
	if fastEMA == nil || slowEMA == nil {
		return nil
	}
	// Align: fastEMA is longer, trim its head so lengths match
	diff := len(fastEMA) - len(slowEMA)
	if diff < 0 {
		return nil
	}
	fastEMA = fastEMA[diff:]

	macdLine := make([]float64, len(slowEMA))
	for i := range slowEMA {
		macdLine[i] = fastEMA[i] - slowEMA[i]
	}

	sigLine := ema(macdLine, sigPeriod)
	if sigLine == nil {
		return nil
	}
	offset := len(macdLine) - len(sigLine)
	macdLine = macdLine[offset:]

	out := make([]macdPoint, len(sigLine))
	for i := range sigLine {
		out[i] = macdPoint{
			MACD:      macdLine[i],
			Signal:    sigLine[i],
			Histogram: macdLine[i] - sigLine[i],
		}
	}
	return out
}

// ── Bollinger Bands ───────────────────────────────────────────────────────

type bbPoint struct {
	Upper  float64
	Middle float64
	Lower  float64
}

// bollingerBands computes Bollinger Bands (SMA + population stddev).
func bollingerBands(closes []float64, period int, stdMult float64) []bbPoint {
	smaVals := sma(closes, period)
	if smaVals == nil {
		return nil
	}
	out := make([]bbPoint, len(smaVals))
	for i, mid := range smaVals {
		variance := 0.0
		for j := i; j < i+period; j++ {
			d := closes[j] - mid
			variance += d * d
		}
		std := math.Sqrt(variance / float64(period))
		out[i] = bbPoint{
			Upper:  mid + stdMult*std,
			Middle: mid,
			Lower:  mid - stdMult*std,
		}
	}
	return out
}

// ── Stochastic ────────────────────────────────────────────────────────────

type stochPoint struct {
	K float64
	D float64
}

// stochastic computes Stochastic Oscillator (%K/%D).
func stochastic(high, low, close []float64, kPeriod, dPeriod int) []stochPoint {
	n := len(close)
	if n < kPeriod {
		return nil
	}
	kVals := make([]float64, 0, n-kPeriod+1)
	for i := kPeriod - 1; i < n; i++ {
		maxH := high[i-kPeriod+1]
		minL := low[i-kPeriod+1]
		for j := i - kPeriod + 2; j <= i; j++ {
			if high[j] > maxH {
				maxH = high[j]
			}
			if low[j] < minL {
				minL = low[j]
			}
		}
		if maxH == minL {
			kVals = append(kVals, 50)
		} else {
			kVals = append(kVals, 100*(close[i]-minL)/(maxH-minL))
		}
	}
	dVals := sma(kVals, dPeriod)
	if dVals == nil {
		return nil
	}
	offset := len(kVals) - len(dVals)
	out := make([]stochPoint, len(dVals))
	for i := range dVals {
		out[i] = stochPoint{K: kVals[i+offset], D: dVals[i]}
	}
	return out
}

// ── ATR (Wilder's smoothing) ──────────────────────────────────────────────

// wilderATR returns ATR using Wilder's smoothing (matches technicalindicators ATR).
func wilderATR(high, low, close []float64, period int) []float64 {
	n := len(close)
	if n < period+1 {
		return nil
	}
	tr := make([]float64, n-1)
	for i := 1; i < n; i++ {
		hl := high[i] - low[i]
		hpc := math.Abs(high[i] - close[i-1])
		lpc := math.Abs(low[i] - close[i-1])
		tr[i-1] = math.Max(hl, math.Max(hpc, lpc))
	}
	if len(tr) < period {
		return nil
	}
	// Seed with SMA
	sum := 0.0
	for i := 0; i < period; i++ {
		sum += tr[i]
	}
	prev := sum / float64(period)
	out := make([]float64, 0, len(tr)-period+1)
	out = append(out, prev)
	// Wilder's: prev = (prev*(period-1) + tr[i]) / period
	for i := period; i < len(tr); i++ {
		prev = (prev*float64(period-1) + tr[i]) / float64(period)
		out = append(out, prev)
	}
	return out
}

// last returns the last element or zero if empty.
func lastF(s []float64) (float64, bool) {
	if len(s) == 0 {
		return 0, false
	}
	return s[len(s)-1], true
}

func prevF(s []float64) (float64, bool) {
	if len(s) < 2 {
		return 0, false
	}
	return s[len(s)-2], true
}
