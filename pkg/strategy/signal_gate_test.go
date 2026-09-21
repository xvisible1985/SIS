package strategy

import (
	"context"
	"testing"
	"time"

	"sis/pkg/signal"
)

// TestSignalGateAllows_NoSignalEngine_AllowsByDefault mirrors awaitSignal's own fallback
// (cycle.go: "if signalEngine == nil || len(configs) == 0 { ...start unconditionally... }")
// — a UseSignal=true level must never block a bot's trading indefinitely just because the
// signal engine isn't wired up in this test/runtime context.
func TestSignalGateAllows_NoSignalEngine_AllowsByDefault(t *testing.T) {
	sr := &StrategyRunner{
		strategy: Strategy{
			ID:            "11111111-2222-3333-4444-555555555555",
			Direction:     DirectionLong,
			Symbol:        "TESTUSDT",
			SignalConfigs: []SignalConfig{{Name: "rsi-os"}},
		},
		runner: &AccountRunner{}, // signalEngine left nil
	}
	if !sr.signalGateAllows() {
		t.Error("signalGateAllows() = false, want true — no signal engine must fall back to allow")
	}
}

// TestSignalGateAllows_NoConfigs_AllowsByDefault is the other half of the same fallback —
// SignalConfigs empty even though a signal engine exists.
func TestSignalGateAllows_NoConfigs_AllowsByDefault(t *testing.T) {
	sr := &StrategyRunner{
		strategy: Strategy{
			ID:        "11111111-2222-3333-4444-555555555555",
			Direction: DirectionLong,
			Symbol:    "TESTUSDT",
			// SignalConfigs deliberately empty
		},
		runner: &AccountRunner{signalEngine: &signal.Engine{}},
	}
	if !sr.signalGateAllows() {
		t.Error("signalGateAllows() = false, want true — empty SignalConfigs must fall back to allow")
	}
}

// TestSignalGateAllows_CachesResultWithinTTL locks in the TTL cache added to bound how often
// signalGateAllows() can hit pkg/signal's slow (real network) fallback path — a strategy with
// UseSignal levels but no separate signal_filter-driven Subscribe has no other reason for
// pkg/signal to already have this symbol's state cached, so gridVirtualPriceTick calling this
// on every price tick while holding sr.mu could otherwise stall the mutex on a slow/unreachable
// fetch, repeatedly, every tick.
//
// Uses the same real-engine technique as grid_signal_gate_test.go: Subscribe primes a compute
// unit whose lastState deterministically stays signal.Neutral for the test's lifetime (no
// confirmed WS kline can arrive in time), so the first call computes a real, genuine false.
// The engine is then swapped to nil, which would flip an actual re-evaluation to the
// no-engine fallback (true) — proving a second call within the TTL is served from the cache
// rather than recomputed, by observing it does NOT flip.
func TestSignalGateAllows_CachesResultWithinTTL(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	se := signal.NewEngine(ctx, nil)
	runner := &AccountRunner{signalEngine: se}
	sigConfigs := []SignalConfig{{Name: "rsi-os"}}
	goCfgs := runner.resolveSignalConfigs(sigConfigs)
	if err := se.Subscribe("test-sub-cache", "TESTUSDT", "1h", goCfgs, func(signal.State) {}); err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	defer se.Unsubscribe("test-sub-cache")

	sr := &StrategyRunner{
		strategy: Strategy{
			ID:            "11111111-2222-3333-4444-555555555555",
			Direction:     DirectionLong,
			Symbol:        "TESTUSDT",
			SignalConfigs: sigConfigs,
		},
		runner: runner,
	}

	first := sr.signalGateAllows()
	if first {
		t.Fatal("test setup invalid: first call = true, want false (engine should still be reporting the Neutral default)")
	}
	if sr.signalGateCachedAt.IsZero() {
		t.Fatal("signalGateCachedAt was never set — caching not wired up")
	}

	// Swap to a bare AccountRunner (nil signalEngine): a genuine re-evaluation right now would
	// take the no-engine fallback and return true.
	sr.runner = &AccountRunner{}

	second := sr.signalGateAllows()
	if second != first {
		t.Errorf("second call within TTL = %v, want cached %v — result changed, meaning it was recomputed instead of served from cache", second, first)
	}
}

// TestSignalGateAllows_RecomputesAfterTTLExpires is the flip side of the caching test above —
// once signalGateCacheTTL has elapsed, the next call must genuinely re-evaluate rather than
// serve a stale cached value forever.
func TestSignalGateAllows_RecomputesAfterTTLExpires(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	se := signal.NewEngine(ctx, nil)
	runner := &AccountRunner{signalEngine: se}
	sigConfigs := []SignalConfig{{Name: "rsi-os"}}
	goCfgs := runner.resolveSignalConfigs(sigConfigs)
	if err := se.Subscribe("test-sub-ttl", "TESTUSDT", "1h", goCfgs, func(signal.State) {}); err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	defer se.Unsubscribe("test-sub-ttl")

	sr := &StrategyRunner{
		strategy: Strategy{
			ID:            "11111111-2222-3333-4444-555555555555",
			Direction:     DirectionLong,
			Symbol:        "TESTUSDT",
			SignalConfigs: sigConfigs,
		},
		runner: runner,
	}

	if sr.signalGateAllows() {
		t.Fatal("test setup invalid: first call = true, want false")
	}

	// Force the cache to look expired, then swap to a nil-engine runner — if the TTL is
	// respected, this next call must recompute and observe the fallback true.
	sr.signalGateCachedAt = time.Now().Add(-signalGateCacheTTL - time.Second)
	sr.runner = &AccountRunner{}

	if !sr.signalGateAllows() {
		t.Error("signalGateAllows() after TTL expiry = false, want true — stale cache was served instead of recomputing")
	}
}
