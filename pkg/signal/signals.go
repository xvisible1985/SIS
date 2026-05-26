package signal

import (
	"math"
	"sync"
	"time"
)

// ── RSI Oversold ──────────────────────────────────────────────────────────

type rsiOversold struct{ period int; threshold float64; kind string }

func (s *rsiOversold) Compute(c []Candle) State {
	if len(c) < s.period+2 {
		return Neutral
	}
	closes := closes(c)
	arr := rsi(closes, s.period)
	v, ok := lastF(arr)
	if !ok {
		return Neutral
	}
	prev, ok2 := prevF(arr)
	if !ok2 {
		return Neutral
	}
	switch s.kind {
	case "cross":
		if prev >= s.threshold && v < s.threshold {
			return Buy
		}
	case "enter":
		if prev > s.threshold && v <= s.threshold {
			return Buy
		}
	default: // "stay"
		if v < s.threshold {
			return Buy
		}
	}
	return Neutral
}

func (s *rsiOversold) Value(c []Candle) float64 {
	if len(c) < s.period+1 {
		return 0
	}
	arr := rsi(closes(c), s.period)
	v, ok := lastF(arr)
	if !ok {
		return 0
	}
	return math.Round(v*10) / 10
}

// ── MACD Crossover ────────────────────────────────────────────────────────

type macdCross struct{ fast, slow, sigPeriod int; dir string }

func (s *macdCross) Compute(c []Candle) State {
	if len(c) < s.slow+s.sigPeriod+1 {
		return Neutral
	}
	arr := macdCalc(closes(c), s.fast, s.slow, s.sigPeriod)
	if len(arr) < 2 {
		return Neutral
	}
	v, prev := arr[len(arr)-1], arr[len(arr)-2]
	ph, h := prev.Histogram, v.Histogram
	switch s.dir {
	case "вверх":
		if ph < 0 && h >= 0 {
			return Buy
		}
	case "вниз":
		if ph > 0 && h <= 0 {
			return Sell
		}
	default: // "оба"
		if ph < 0 && h >= 0 {
			return Buy
		}
		if ph > 0 && h <= 0 {
			return Sell
		}
	}
	return Neutral
}

// ── Golden Cross (EMA fast/slow) ──────────────────────────────────────────

type goldenCross struct{ fast, slow int; confirm string }

func (s *goldenCross) Compute(c []Candle) State {
	if len(c) < s.slow+3 {
		return Neutral
	}
	cl := closes(c)
	fastArr := ema(cl, s.fast)
	slowArr := ema(cl, s.slow)
	if len(fastArr) < 2 || len(slowArr) < 2 {
		return Neutral
	}
	f0, f1 := fastArr[len(fastArr)-1], fastArr[len(fastArr)-2]
	s0, s1 := slowArr[len(slowArr)-1], slowArr[len(slowArr)-2]
	if f1 <= s1 && f0 > s0 {
		return Buy
	}
	if f1 >= s1 && f0 < s0 {
		return Sell
	}
	if f0 > s0 {
		return Buy
	}
	return Sell
}

// ── BB Squeeze ────────────────────────────────────────────────────────────

type bbSqueeze struct{ period int; std, width float64 }

func (s *bbSqueeze) Compute(c []Candle) State {
	if len(c) < s.period {
		return Neutral
	}
	arr := bollingerBands(closes(c), s.period, s.std)
	if len(arr) == 0 {
		return Neutral
	}
	v := arr[len(arr)-1]
	if v.Middle == 0 {
		return Neutral
	}
	widthPct := ((v.Upper - v.Lower) / v.Middle) * 100
	if widthPct < s.width {
		return Buy // squeeze active
	}
	return Neutral
}

// ── Stochastic Cross ──────────────────────────────────────────────────────

type stochCross struct{ k, d int; zone float64 }

func (s *stochCross) Compute(c []Candle) State {
	if len(c) < s.k+s.d+1 {
		return Neutral
	}
	h := highs(c)
	l := lows(c)
	cl := closes(c)
	arr := stochastic(h, l, cl, s.k, s.d)
	if len(arr) < 2 {
		return Neutral
	}
	v, prev := arr[len(arr)-1], arr[len(arr)-2]
	if v.K < s.zone && prev.K <= prev.D && v.K > v.D {
		return Buy
	}
	if v.K > (100-s.zone) && prev.K >= prev.D && v.K < v.D {
		return Sell
	}
	return Neutral
}

func (s *stochCross) Value(c []Candle) float64 {
	if len(c) < s.k+s.d+1 {
		return 0
	}
	arr := stochastic(highs(c), lows(c), closes(c), s.k, s.d)
	if len(arr) == 0 {
		return 0
	}
	return math.Round(arr[len(arr)-1].K*10) / 10
}

// ── Volume Spike ──────────────────────────────────────────────────────────

type volSpike struct{ period int; mult float64; candle string }

func (s *volSpike) Compute(c []Candle) State {
	if len(c) < s.period+1 {
		return Neutral
	}
	vols := volumes(c)
	maVals := sma(vols, s.period)
	if len(maVals) == 0 {
		return Neutral
	}
	ma := maVals[len(maVals)-1]
	cur := vols[len(vols)-1]
	if cur < ma*s.mult {
		return Neutral
	}
	last := c[len(c)-1]
	isGreen := last.Close >= last.Open
	switch s.candle {
	case "зелён.":
		if !isGreen {
			return Neutral
		}
	case "красн.":
		if isGreen {
			return Neutral
		}
	}
	if isGreen {
		return Buy
	}
	return Sell
}

func (s *volSpike) Value(c []Candle) float64 {
	if len(c) < s.period+1 {
		return 0
	}
	vols := volumes(c)
	maVals := sma(vols, s.period)
	if len(maVals) == 0 {
		return 0
	}
	ma := maVals[len(maVals)-1]
	if ma == 0 {
		return 0
	}
	ratio := vols[len(vols)-1] / ma
	return math.Round(ratio*100) / 100
}

// ── Range Breakout ────────────────────────────────────────────────────────

type rangeBreakout struct{ period int; buffer float64; dir string }

func (s *rangeBreakout) Compute(c []Candle) State {
	if len(c) < s.period+1 {
		return Neutral
	}
	window := c[len(c)-s.period-1 : len(c)-1]
	price := c[len(c)-1].Close
	buf := s.buffer / 100
	if s.dir != "down" {
		maxH := window[0].High
		for _, bar := range window[1:] {
			if bar.High > maxH {
				maxH = bar.High
			}
		}
		if price > maxH*(1+buf) {
			return Buy
		}
	}
	if s.dir != "up" {
		minL := window[0].Low
		for _, bar := range window[1:] {
			if bar.Low < minL {
				minL = bar.Low
			}
		}
		if price < minL*(1-buf) {
			return Sell
		}
	}
	return Neutral
}

// ── EMA Crossover ─────────────────────────────────────────────────────────

type emaCross struct{ fast, slow int; dir string }

func (s *emaCross) Compute(c []Candle) State {
	if len(c) < s.slow+2 {
		return Neutral
	}
	cl := closes(c)
	fastArr := ema(cl, s.fast)
	slowArr := ema(cl, s.slow)
	if len(fastArr) < 2 || len(slowArr) < 2 {
		return Neutral
	}
	f0, f1 := fastArr[len(fastArr)-1], fastArr[len(fastArr)-2]
	s0, s1 := slowArr[len(slowArr)-1], slowArr[len(slowArr)-2]
	switch s.dir {
	case "вверх":
		if f1 <= s1 && f0 > s0 {
			return Buy
		}
	case "вниз":
		if f1 >= s1 && f0 < s0 {
			return Sell
		}
	default: // "оба"
		if f1 <= s1 && f0 > s0 {
			return Buy
		}
		if f1 >= s1 && f0 < s0 {
			return Sell
		}
	}
	return Neutral
}

// ── RSI Divergence ────────────────────────────────────────────────────────

type rsiDivergence struct{ period, lookback int; dir string }

func (s *rsiDivergence) Compute(c []Candle) State {
	n := s.lookback
	if n > len(c) {
		n = len(c)
	}
	window := c[len(c)-n:]
	if len(window) < s.period+5 {
		return Neutral
	}
	rsiArr := rsi(closes(window), s.period)
	priceArr := closes(window)[len(window)-len(rsiArr):]
	if len(rsiArr) < 4 {
		return Neutral
	}
	idx := len(rsiArr) - 1

	findPrev := func(arr []float64, from int, typ string) int {
		for i := from - 2; i >= 1; i-- {
			if typ == "min" && arr[i] < arr[i-1] && arr[i] < arr[i+1] {
				return i
			}
			if typ == "max" && arr[i] > arr[i-1] && arr[i] > arr[i+1] {
				return i
			}
		}
		return -1
	}

	if s.dir != "bear" {
		pi2 := findPrev(priceArr, idx, "min")
		ri2 := findPrev(rsiArr, idx, "min")
		if pi2 > 0 && ri2 > 0 {
			if priceArr[idx] < priceArr[pi2] && rsiArr[idx] > rsiArr[ri2] {
				return Buy
			}
		}
	}
	if s.dir != "bull" {
		pi2 := findPrev(priceArr, idx, "max")
		ri2 := findPrev(rsiArr, idx, "max")
		if pi2 > 0 && ri2 > 0 {
			if priceArr[idx] > priceArr[pi2] && rsiArr[idx] < rsiArr[ri2] {
				return Sell
			}
		}
	}
	return Neutral
}

func (s *rsiDivergence) Value(c []Candle) float64 {
	if len(c) < s.period+1 {
		return 0
	}
	arr := rsi(closes(c), s.period)
	v, ok := lastF(arr)
	if !ok {
		return 0
	}
	return math.Round(v*10) / 10
}

// ── SuperTrend Flip ───────────────────────────────────────────────────────

type superTrendFlip struct {
	atrPeriod int
	mult      float64
	dir       string
	ttl       time.Duration
	mu        sync.Mutex
	firedAt   time.Time
}

func (s *superTrendFlip) Compute(c []Candle) State {
	dir := superTrendDir(c, s.atrPeriod, s.mult)
	var base State
	switch s.dir {
	case "bull":
		if dir == Buy {
			base = Buy
		} else {
			base = Neutral
		}
	case "bear":
		if dir == Sell {
			base = Sell
		} else {
			base = Neutral
		}
	default: // "оба"
		base = dir
	}

	if s.ttl <= 0 {
		return base
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if base == Neutral {
		s.firedAt = time.Time{}
		return Neutral
	}
	now := time.Now()
	if s.firedAt.IsZero() {
		if fired := superTrendLookbackFiredAt(c, s.atrPeriod, s.mult, dir); !fired.IsZero() && fired.Before(now) {
			s.firedAt = fired
		} else {
			s.firedAt = now
		}
	}
	if now.Sub(s.firedAt) >= s.ttl {
		return Neutral
	}
	return base
}

func (s *superTrendFlip) TTLRemainingSec() float64 {
	if s.ttl <= 0 {
		return -1
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.firedAt.IsZero() {
		return -1
	}
	rem := s.ttl - time.Since(s.firedAt)
	if rem <= 0 {
		return 0
	}
	return rem.Seconds()
}

func (s *superTrendContinuous) TTLRemainingSec() float64 {
	if s.ttl <= 0 {
		return -1
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.firedAt.IsZero() {
		return -1
	}
	rem := s.ttl - time.Since(s.firedAt)
	if rem <= 0 {
		return 0
	}
	return rem.Seconds()
}

func superTrendDir(c []Candle, period int, mult float64) State {
	if len(c) < period+1 {
		return Neutral
	}
	h := highs(c)
	l := lows(c)
	cl := closes(c)
	atrArr := wilderATR(h, l, cl, period)
	if len(atrArr) == 0 {
		return Neutral
	}
	off := len(c) - len(atrArr)
	var fub, flb float64
	dir := Sell
	for i, atrVal := range atrArr {
		idx := i + off
		mid := (h[idx] + l[idx]) / 2
		bub := mid + mult*atrVal
		blb := mid - mult*atrVal
		if i == 0 {
			fub, flb = bub, blb
			continue
		}
		pc := cl[idx-1]
		if bub < fub || pc > fub {
			fub = bub
		}
		if blb > flb || pc < flb {
			flb = blb
		}
		if dir == Sell {
			if cl[idx] > fub {
				dir = Buy
			}
		} else {
			if cl[idx] < flb {
				dir = Sell
			}
		}
	}
	return dir
}

// superTrendDirAll runs the Supertrend algorithm over all candles and returns
// the direction at each index. Entries before the ATR warm-up are Neutral.
func superTrendDirAll(c []Candle, period int, mult float64) []State {
	result := make([]State, len(c))
	for i := range result {
		result[i] = Neutral
	}
	if len(c) < period+1 {
		return result
	}
	h := highs(c)
	l := lows(c)
	cl := closes(c)
	atrArr := wilderATR(h, l, cl, period)
	if len(atrArr) == 0 {
		return result
	}
	off := len(c) - len(atrArr)
	var fub, flb float64
	dir := Sell
	for i, atrVal := range atrArr {
		idx := i + off
		mid := (h[idx] + l[idx]) / 2
		bub := mid + mult*atrVal
		blb := mid - mult*atrVal
		if i == 0 {
			fub, flb = bub, blb
			result[idx] = dir
			continue
		}
		pc := cl[idx-1]
		if bub < fub || pc > fub {
			fub = bub
		}
		if blb > flb || pc < flb {
			flb = blb
		}
		if dir == Sell {
			if cl[idx] > fub {
				dir = Buy
			}
		} else {
			if cl[idx] < flb {
				dir = Sell
			}
		}
		result[idx] = dir
	}
	return result
}

// superTrendLookbackFiredAt finds when the current Supertrend direction streak
// started by scanning the candle history backwards. Returns the open time of
// the first candle in that streak, so TTL is calculated from the actual signal
// origin rather than from bot startup time.
func superTrendLookbackFiredAt(c []Candle, period int, mult float64, currentDir State) time.Time {
	dirs := superTrendDirAll(c, period, mult)
	n := len(dirs)
	if n == 0 {
		return time.Time{}
	}
	startIdx := n - 1
	for i := n - 2; i >= 0; i-- {
		if dirs[i] == Neutral || dirs[i] != currentDir {
			break
		}
		startIdx = i
	}
	return time.UnixMilli(c[startIdx].Time)
}

// ── helpers ───────────────────────────────────────────────────────────────

func closes(c []Candle) []float64 {
	out := make([]float64, len(c))
	for i, bar := range c {
		out[i] = bar.Close
	}
	return out
}

func highs(c []Candle) []float64 {
	out := make([]float64, len(c))
	for i, bar := range c {
		out[i] = bar.High
	}
	return out
}

func lows(c []Candle) []float64 {
	out := make([]float64, len(c))
	for i, bar := range c {
		out[i] = bar.Low
	}
	return out
}

func volumes(c []Candle) []float64 {
	out := make([]float64, len(c))
	for i, bar := range c {
		out[i] = bar.Volume
	}
	return out
}

// ensure math is imported (used by bollingerBands indirectly)
var _ = math.Sqrt

// ── RSI Zone — continuous (indicator 'rsi') ───────────────────────────────

type rsiZone struct{ period int; lower, upper float64 }

func (s *rsiZone) Compute(c []Candle) State {
	if len(c) < s.period+2 {
		return Neutral
	}
	arr := rsi(closes(c), s.period)
	v, ok := lastF(arr)
	if !ok {
		return Neutral
	}
	if v <= s.lower {
		return Buy
	}
	if v >= s.upper {
		return Sell
	}
	return Neutral
}

func (s *rsiZone) Value(c []Candle) float64 {
	if len(c) < s.period+1 {
		return 0
	}
	arr := rsi(closes(c), s.period)
	v, ok := lastF(arr)
	if !ok {
		return 0
	}
	return math.Round(v*10) / 10
}

// ── RSI Test — manual override for strategy testing ──────────────────────
// Identical logic to rsiOversold; when override is active the RSI value
// is taken from the admin panel instead of being computed from candles.

type rsiTest struct{ period int; threshold float64; kind string }

func (s *rsiTest) Compute(c []Candle) State {
	v, ok := GetTestOverride("rsi-test")
	if !ok {
		// No override — use real candle RSI with full cross/enter/stay logic
		if len(c) < s.period+2 {
			return Neutral
		}
		arr := rsi(closes(c), s.period)
		val, ok2 := lastF(arr)
		if !ok2 {
			return Neutral
		}
		prev, ok3 := prevF(arr)
		if !ok3 {
			return Neutral
		}
		switch s.kind {
		case "cross":
			if prev >= s.threshold && val < s.threshold {
				return Buy
			}
		case "enter":
			if prev > s.threshold && val <= s.threshold {
				return Buy
			}
		default: // "stay"
			if val < s.threshold {
				return Buy
			}
		}
		return Neutral
	}
	// Override active — single static value, always use "stay" semantics
	if v < s.threshold {
		return Buy
	}
	return Neutral
}

func (s *rsiTest) Value(c []Candle) float64 {
	v, ok := GetTestOverride("rsi-test")
	if !ok {
		if len(c) < s.period+1 {
			return 0
		}
		arr := rsi(closes(c), s.period)
		val, ok2 := lastF(arr)
		if !ok2 {
			return 0
		}
		return math.Round(val*10) / 10
	}
	return math.Round(v*10) / 10
}

// ── MACD Trend — continuous (indicator 'macd') ────────────────────────────

type macdTrend struct{ fast, slow, sigPeriod int }

func (s *macdTrend) Compute(c []Candle) State {
	if len(c) < s.slow+s.sigPeriod+1 {
		return Neutral
	}
	arr := macdCalc(closes(c), s.fast, s.slow, s.sigPeriod)
	if len(arr) == 0 {
		return Neutral
	}
	h := arr[len(arr)-1].Histogram
	if h > 0 {
		return Buy
	}
	if h < 0 {
		return Sell
	}
	return Neutral
}

func (s *macdTrend) Value(c []Candle) float64 {
	if len(c) < s.slow+s.sigPeriod+1 {
		return 0
	}
	arr := macdCalc(closes(c), s.fast, s.slow, s.sigPeriod)
	if len(arr) == 0 {
		return 0
	}
	return math.Round(arr[len(arr)-1].Histogram*10000) / 10000
}

// ── EMA Trend — price vs EMA (indicator 'ema') ────────────────────────────

type emaTrend struct{ period int }

func (s *emaTrend) Compute(c []Candle) State {
	if len(c) < s.period {
		return Neutral
	}
	cl := closes(c)
	arr := ema(cl, s.period)
	if len(arr) == 0 {
		return Neutral
	}
	price := cl[len(cl)-1]
	emaVal := arr[len(arr)-1]
	if price > emaVal {
		return Buy
	}
	if price < emaVal {
		return Sell
	}
	return Neutral
}

// ── SMA Trend — price vs SMA (indicator 'sma') ────────────────────────────

type smaTrend struct{ period int }

func (s *smaTrend) Compute(c []Candle) State {
	if len(c) < s.period {
		return Neutral
	}
	cl := closes(c)
	arr := sma(cl, s.period)
	if len(arr) == 0 {
		return Neutral
	}
	price := cl[len(cl)-1]
	if price > arr[len(arr)-1] {
		return Buy
	}
	if price < arr[len(arr)-1] {
		return Sell
	}
	return Neutral
}

// ── BB Zone — price vs bands (indicator 'bb') ─────────────────────────────

type bbZone struct{ period int; std float64 }

func (s *bbZone) Compute(c []Candle) State {
	if len(c) < s.period {
		return Neutral
	}
	cl := closes(c)
	arr := bollingerBands(cl, s.period, s.std)
	if len(arr) == 0 {
		return Neutral
	}
	v := arr[len(arr)-1]
	price := cl[len(cl)-1]
	if price <= v.Lower {
		return Buy
	}
	if price >= v.Upper {
		return Sell
	}
	return Neutral
}

// ── Stoch Trend — K vs D (indicator 'stoch') ─────────────────────────────

type stochTrend struct{ k, d int }

func (s *stochTrend) Compute(c []Candle) State {
	if len(c) < s.k+s.d+1 {
		return Neutral
	}
	arr := stochastic(highs(c), lows(c), closes(c), s.k, s.d)
	if len(arr) == 0 {
		return Neutral
	}
	v := arr[len(arr)-1]
	if v.K > v.D {
		return Buy
	}
	if v.K < v.D {
		return Sell
	}
	return Neutral
}

func (s *stochTrend) Value(c []Candle) float64 {
	if len(c) < s.k+s.d+1 {
		return 0
	}
	arr := stochastic(highs(c), lows(c), closes(c), s.k, s.d)
	if len(arr) == 0 {
		return 0
	}
	return math.Round(arr[len(arr)-1].K*10) / 10
}

// ── SuperTrend Continuous — direction (indicator 'st') ────────────────────

type superTrendContinuous struct {
	atrPeriod int
	mult      float64
	ttl       time.Duration
	mu        sync.Mutex
	firedAt   time.Time
	lastDir   State
}

func (s *superTrendContinuous) Compute(c []Candle) State {
	base := superTrendDir(c, s.atrPeriod, s.mult)

	if s.ttl <= 0 {
		return base
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if base == Neutral {
		s.firedAt = time.Time{}
		s.lastDir = Neutral
		return Neutral
	}
	now := time.Now()
	if base != s.lastDir {
		s.lastDir = base
		if s.firedAt.IsZero() {
			// First computation after start/restart — look back in history
			if fired := superTrendLookbackFiredAt(c, s.atrPeriod, s.mult, base); !fired.IsZero() && fired.Before(now) {
				s.firedAt = fired
			} else {
				s.firedAt = now
			}
		} else {
			// Live direction change — TTL starts from this candle
			s.firedAt = now
		}
	} else if s.firedAt.IsZero() {
		if fired := superTrendLookbackFiredAt(c, s.atrPeriod, s.mult, base); !fired.IsZero() && fired.Before(now) {
			s.firedAt = fired
		} else {
			s.firedAt = now
		}
	}
	if now.Sub(s.firedAt) >= s.ttl {
		return Neutral
	}
	return base
}
