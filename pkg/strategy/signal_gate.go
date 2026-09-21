package strategy

import "sis/pkg/signal"

// signalGateAllows reports whether the strategy's own configured signal (SignalConfigs)
// currently agrees with its trading direction — the shared decision every UseSignal=true
// level's placement/cancellation logic consults. Mirrors awaitSignal's (cycle.go)
// no-engine/no-configs fallback exactly: a UseSignal level must never block trading
// indefinitely just because nothing is configured to evaluate.
//
// Unlike awaitSignal (a one-shot Subscribe that fires once and unsubscribes), this is a
// synchronous, repeatable query — signal.Engine.QueryState is a cheap cached lookup (falls
// back to a fresh compute only if no subscription unit exists yet for this exact
// symbol/interval/config hash), safe to call on every price tick.
//
// Must be called with sr.mu held (same requirement as its callers in the price-tick paths).
func (sr *StrategyRunner) signalGateAllows() bool {
	configs := sr.strategy.SignalConfigs
	signalEngine := sr.runner.signalEngine
	if signalEngine == nil || len(configs) == 0 {
		return true
	}

	sigConfigs := sr.runner.resolveSignalConfigs(configs)

	tf := "1h"
	if v, ok := configs[0].Params["tf"]; ok {
		if s, ok2 := v.(string); ok2 && s != "" {
			tf = s
		}
	}

	state, ok := signalEngine.QueryState(sr.strategy.Symbol, tf, sigConfigs)
	if !ok {
		return false
	}

	var want signal.State
	switch sr.strategy.Direction {
	case DirectionLong:
		want = signal.Buy
	case DirectionShort:
		want = signal.Sell
	default: // both — accept either non-neutral direction, matching awaitSignal's own "both" handling
		return state != signal.Neutral
	}
	return state == want
}
