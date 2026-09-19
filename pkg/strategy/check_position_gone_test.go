package strategy

import (
	"context"
	"testing"
	"time"

	"sis/pkg/trader"
)

// TestCheckPositionGone_TransientEmptySnapshot_ConfirmsBeforeClosing reproduces the bug
// found live 2026-09-18: right after a process restart, a single FetchPositions call can
// return an empty/stale snapshot even though the real exchange position is still open
// (the same class of staleness engine.go's seedPositionCache/applyPositionSnapshot
// already guards against for the WS cache). checkPositionGone must not trust a single
// "not found" response — it has to confirm with a second fetch before destructively
// cancelling orders and closing the cycle.
func TestCheckPositionGone_TransientEmptySnapshot_ConfirmsBeforeClosing(t *testing.T) {
	origDelay := positionGoneConfirmDelay
	positionGoneConfirmDelay = time.Millisecond
	defer func() { positionGoneConfirmDelay = origDelay }()

	fake := &fakeExchange{}
	// First call: empty/stale snapshot (transient API glitch right after restart).
	fake.fetchPositionsQ.push([]trader.Position{}, nil)
	// Second call (confirmation): the real position IS there.
	fake.fetchPositionsQ.push([]trader.Position{
		{Symbol: "ICXUSDT", PositionIdx: 2, Side: "Sell", Size: "6778"},
	}, nil)

	ar := newTestAccountRunner(t, fake)
	sr := &StrategyRunner{
		strategy: Strategy{
			ID:        "11111111-2222-3333-4444-555555555555",
			Symbol:    "ICXUSDT",
			Direction: DirectionShort,
			HedgeMode: true,
		},
		runner: ar,
		cycle:  &Cycle{ID: "cccccccc-2222-3333-4444-555555555555", CycleNum: 1},
		levels: []GridLevel{
			{ID: "dddddddd-2222-3333-4444-555555555555", Status: LevelFilled},
		},
	}

	gone := sr.checkPositionGone(context.Background())

	if gone {
		t.Errorf("checkPositionGone = true, want false — the confirmation fetch found the position still open, the cycle must survive")
	}
	if fake.fetchPositionsCalls != 2 {
		t.Errorf("FetchPositions called %d times, want 2 (initial + confirmation)", fake.fetchPositionsCalls)
	}
	sr.mu.RLock()
	levelStatus := sr.levels[0].Status
	sr.mu.RUnlock()
	if levelStatus != LevelFilled {
		t.Errorf("level status = %q, want still %q — must not have been cancelled", levelStatus, LevelFilled)
	}
}

// TestCheckPositionGone_ConfirmedGone_ClosesCycle verifies the retry does not break the
// legitimate case: when the position really is gone, both fetches agree and the cycle
// must still be cleaned up as before.
func TestCheckPositionGone_ConfirmedGone_ClosesCycle(t *testing.T) {
	origDelay := positionGoneConfirmDelay
	positionGoneConfirmDelay = time.Millisecond
	defer func() { positionGoneConfirmDelay = origDelay }()

	fake := &fakeExchange{}
	fake.fetchPositionsQ.push([]trader.Position{}, nil)
	fake.fetchPositionsQ.push([]trader.Position{}, nil)

	ar := newTestAccountRunner(t, fake)
	sr := &StrategyRunner{
		strategy: Strategy{
			ID:        "11111111-2222-3333-4444-555555555555",
			Symbol:    "ICXUSDT",
			Direction: DirectionShort,
			HedgeMode: true,
		},
		runner: ar,
		cycle:  &Cycle{ID: "cccccccc-2222-3333-4444-555555555555", CycleNum: 1},
		levels: []GridLevel{
			{ID: "dddddddd-2222-3333-4444-555555555555", Status: LevelFilled},
		},
	}

	gone := sr.checkPositionGone(context.Background())

	if !gone {
		t.Errorf("checkPositionGone = false, want true — both fetches agreed the position is gone")
	}
	if fake.fetchPositionsCalls != 2 {
		t.Errorf("FetchPositions called %d times, want 2 (initial + confirmation)", fake.fetchPositionsCalls)
	}
}

// TestCheckPositionGone_FoundOnFirstFetch_NoConfirmationCall verifies the retry only
// fires for the "not found" branch — when the first fetch already sees the position open,
// there must be no wasted second call (and no confirmation delay).
func TestCheckPositionGone_FoundOnFirstFetch_NoConfirmationCall(t *testing.T) {
	fake := &fakeExchange{}
	fake.fetchPositionsQ.push([]trader.Position{
		{Symbol: "ICXUSDT", PositionIdx: 2, Side: "Sell", Size: "6778"},
	}, nil)

	ar := newTestAccountRunner(t, fake)
	sr := &StrategyRunner{
		strategy: Strategy{
			ID:        "11111111-2222-3333-4444-555555555555",
			Symbol:    "ICXUSDT",
			Direction: DirectionShort,
			HedgeMode: true,
		},
		runner: ar,
		cycle:  &Cycle{ID: "cccccccc-2222-3333-4444-555555555555", CycleNum: 1},
		levels: []GridLevel{
			{ID: "dddddddd-2222-3333-4444-555555555555", Status: LevelFilled},
		},
	}

	gone := sr.checkPositionGone(context.Background())

	if gone {
		t.Errorf("checkPositionGone = true, want false")
	}
	if fake.fetchPositionsCalls != 1 {
		t.Errorf("FetchPositions called %d times, want 1 — no confirmation call needed when the position is found immediately", fake.fetchPositionsCalls)
	}
}
