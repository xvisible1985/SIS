package strategy

import (
	"time"

	"sis/pkg/signal"
)

// signalGateCacheTTL bounds how often signalGateAllows() will actually call QueryState (and,
// on a cache miss inside the signal engine, potentially block on a real network fetch) — see
// the function's own doc comment for why this matters: gridVirtualPriceTick/matrixPriceTick
// call this on every price tick while holding sr.mu, and a strategy with UseSignal levels but
// no separate signal_filter-driven subscription has no other reason for pkg/signal to have
// already cached this symbol's state, so every call would otherwise risk hitting pkg/signal's
// slow fallback path (a real HTTPS fetch) on every single tick.
const signalGateCacheTTL = 3 * time.Second

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
// Cached for signalGateCacheTTL so a strategy with no primed pkg/signal subscription for this
// symbol (i.e. no signal_filter-driven Subscribe already running) can't turn every price tick
// into a potential blocking network call while holding sr.mu.
//
// Must be called with sr.mu held (same requirement as its callers in the price-tick paths).
func (sr *StrategyRunner) signalGateAllows() bool {
	if !sr.signalGateCachedAt.IsZero() && time.Since(sr.signalGateCachedAt) < signalGateCacheTTL {
		return sr.signalGateCachedResult
	}
	result := sr.computeSignalGateAllows()
	sr.signalGateCachedAt = time.Now()
	sr.signalGateCachedResult = result
	return result
}

// computeSignalGateAllows is signalGateAllows' uncached body — kept separate so the caching
// wrapper above stays trivial to read. See signalGateAllows for what this answers.
func (sr *StrategyRunner) computeSignalGateAllows() bool {
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
