// pkg/signals/evaluator.go
package signals

import (
	"fmt"
	"math"

	"sis/pkg/indicators"
	"sis/pkg/models"
)

// SignalResolver resolves a signal_ref node by signal ID.
// Returns true if the referenced signal fires at candle index i.
type SignalResolver func(signalID string, candles []models.Candle, i int) (bool, error)

// IndicatorCache caches computed indicator series to avoid recomputation per candle.
type IndicatorCache map[string]interface{}

// Evaluate evaluates the condition tree at candle index i.
// resolver is called only for signal_ref nodes; pass nil if no signal refs exist.
func Evaluate(node Node, candles []models.Candle, i int, cache IndicatorCache, resolver SignalResolver) (bool, error) {
	switch n := node.(type) {
	case *ANDNode:
		for _, child := range n.Children {
			ok, err := Evaluate(child, candles, i, cache, resolver)
			if err != nil {
				return false, err
			}
			if !ok {
				return false, nil
			}
		}
		return true, nil

	case *ORNode:
		for _, child := range n.Children {
			ok, err := Evaluate(child, candles, i, cache, resolver)
			if err != nil {
				return false, err
			}
			if ok {
				return true, nil
			}
		}
		return false, nil

	case *ConditionNode:
		return evalCondition(n, candles, i, cache)

	case *SignalRefNode:
		if resolver == nil {
			return false, fmt.Errorf("signals: signal_ref encountered but no resolver provided")
		}
		return resolver(n.SignalID, candles, i)

	default:
		return false, fmt.Errorf("signals: unknown node type %T", node)
	}
}

func evalCondition(n *ConditionNode, candles []models.Candle, i int, cache IndicatorCache) (bool, error) {
	lhs, err := indicatorValue(n.Indicator, n.Params, candles, i, cache)
	if err != nil {
		return false, err
	}
	if math.IsNaN(lhs) {
		return false, nil // not enough data
	}

	var rhs float64
	if n.Value != nil {
		rhs = *n.Value
	} else if n.CompareTo != nil {
		rhs, err = indicatorValue(n.CompareTo.Indicator, n.CompareTo.Params, candles, i, cache)
		if err != nil {
			return false, err
		}
		if math.IsNaN(rhs) {
			return false, nil
		}
	} else {
		return false, fmt.Errorf("signals: condition has neither value nor compare_to")
	}

	switch n.Operator {
	case "<":
		return lhs < rhs, nil
	case ">":
		return lhs > rhs, nil
	case "=":
		return math.Abs(lhs-rhs) < 1e-9, nil
	case "!=":
		return math.Abs(lhs-rhs) >= 1e-9, nil
	case "crosses_above":
		if i == 0 {
			return false, nil
		}
		prev, err := indicatorValue(n.Indicator, n.Params, candles, i-1, cache)
		if err != nil || math.IsNaN(prev) {
			return false, err
		}
		var prevRHS float64
		if n.Value != nil {
			prevRHS = *n.Value
		} else if n.CompareTo != nil {
			prevRHS, err = indicatorValue(n.CompareTo.Indicator, n.CompareTo.Params, candles, i-1, cache)
			if err != nil || math.IsNaN(prevRHS) {
				return false, err
			}
		}
		return prev <= prevRHS && lhs > rhs, nil
	case "crosses_below":
		if i == 0 {
			return false, nil
		}
		prev, err := indicatorValue(n.Indicator, n.Params, candles, i-1, cache)
		if err != nil || math.IsNaN(prev) {
			return false, err
		}
		var prevRHS float64
		if n.Value != nil {
			prevRHS = *n.Value
		} else if n.CompareTo != nil {
			prevRHS, err = indicatorValue(n.CompareTo.Indicator, n.CompareTo.Params, candles, i-1, cache)
			if err != nil || math.IsNaN(prevRHS) {
				return false, err
			}
		}
		return prev >= prevRHS && lhs < rhs, nil
	default:
		return false, fmt.Errorf("signals: unknown operator %q", n.Operator)
	}
}

func cacheKey(name string, params map[string]float64) string {
	key := name
	type kv struct {
		k string
		v float64
	}
	kvs := make([]kv, 0, len(params))
	for k, v := range params {
		kvs = append(kvs, kv{k, v})
	}
	for i := 1; i < len(kvs); i++ {
		for j := i; j > 0 && kvs[j-1].k > kvs[j].k; j-- {
			kvs[j-1], kvs[j] = kvs[j], kvs[j-1]
		}
	}
	for _, kv := range kvs {
		key += fmt.Sprintf("_%s=%.4g", kv.k, kv.v)
	}
	return key
}

func indicatorValue(name string, params map[string]float64, candles []models.Candle, i int, cache IndicatorCache) (float64, error) {
	key := cacheKey(name, params)
	if cached, ok := cache[key]; ok {
		series := cached.([]float64)
		if i >= len(series) {
			return math.NaN(), nil
		}
		return series[i], nil
	}

	series, err := computeIndicator(name, params, candles)
	if err != nil {
		return 0, err
	}
	cache[key] = series

	if i >= len(series) {
		return math.NaN(), nil
	}
	return series[i], nil
}

func computeIndicator(name string, params map[string]float64, candles []models.Candle) ([]float64, error) {
	period := int(params["period"])
	if period == 0 {
		period = defaultPeriod(name)
	}
	switch name {
	case "RSI":
		return indicators.RSI(candles, period), nil
	case "EMA":
		return indicators.EMA(candles, period), nil
	case "SMA":
		return indicators.SMA(candles, period), nil
	case "MACD":
		fast := int(params["fast"])
		slow := int(params["slow"])
		sig := int(params["signal"])
		if fast == 0 {
			fast = 12
		}
		if slow == 0 {
			slow = 26
		}
		if sig == 0 {
			sig = 9
		}
		return indicators.MACD(candles, fast, slow, sig).MACD, nil
	case "MACD_signal":
		fast := int(params["fast"])
		slow := int(params["slow"])
		sig := int(params["signal"])
		if fast == 0 {
			fast = 12
		}
		if slow == 0 {
			slow = 26
		}
		if sig == 0 {
			sig = 9
		}
		return indicators.MACD(candles, fast, slow, sig).Signal, nil
	case "MACD_histogram":
		fast := int(params["fast"])
		slow := int(params["slow"])
		sig := int(params["signal"])
		if fast == 0 {
			fast = 12
		}
		if slow == 0 {
			slow = 26
		}
		if sig == 0 {
			sig = 9
		}
		return indicators.MACD(candles, fast, slow, sig).Histogram, nil
	case "BB_upper":
		mult := params["multiplier"]
		if mult == 0 {
			mult = 2.0
		}
		return indicators.BollingerBands(candles, period, mult).Upper, nil
	case "BB_lower":
		mult := params["multiplier"]
		if mult == 0 {
			mult = 2.0
		}
		return indicators.BollingerBands(candles, period, mult).Lower, nil
	case "BB_middle":
		mult := params["multiplier"]
		if mult == 0 {
			mult = 2.0
		}
		return indicators.BollingerBands(candles, period, mult).Middle, nil
	case "ATR":
		return indicators.ATR(candles, period), nil
	case "Stochastic_K":
		dPeriod := int(params["d_period"])
		if dPeriod == 0 {
			dPeriod = 3
		}
		return indicators.Stochastic(candles, period, dPeriod).K, nil
	case "Stochastic_D":
		dPeriod := int(params["d_period"])
		if dPeriod == 0 {
			dPeriod = 3
		}
		return indicators.Stochastic(candles, period, dPeriod).D, nil
	case "Volume":
		return indicators.Volume(candles), nil
	default:
		return nil, fmt.Errorf("signals: unknown indicator %q", name)
	}
}

func defaultPeriod(name string) int {
	switch name {
	case "RSI":
		return 14
	case "EMA":
		return 20
	case "SMA":
		return 20
	case "ATR":
		return 14
	case "BB_upper", "BB_lower", "BB_middle":
		return 20
	case "Stochastic_K", "Stochastic_D":
		return 14
	default:
		return 14
	}
}
