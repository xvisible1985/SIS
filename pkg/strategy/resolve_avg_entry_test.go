package strategy

import (
	"context"
	"testing"

	"sis/pkg/trader"
)

func TestResolveExchangeAvgEntry_FreshWSCache_TrustsCacheWithoutFetchingPositions(t *testing.T) {
	fake := &fakeExchange{}
	ar := newTestAccountRunner(t, fake)
	// WS cache already reflects the full 150-coin position — computedQty (150) matches
	// exactly, so it must be trusted as-is.
	setWSPosition(ar, "TESTUSDT", 0, 150, 102.5)
	// If resolveExchangeAvgEntry wrongly falls back to FetchPositions anyway, this canned
	// value (999) would leak into the result — proves the fallback path was NOT taken.
	fake.fetchPositionsQ.push([]trader.Position{
		{Symbol: "TESTUSDT", PositionIdx: 0, Size: "150", EntryPrice: "999"},
	}, nil)

	sr := &StrategyRunner{
		strategy: Strategy{ID: "11111111-2222-3333-4444-555555555555", Symbol: "TESTUSDT"},
		runner:   ar,
	}

	got := sr.resolveExchangeAvgEntry(context.Background(), 0, 101.0, 150)

	if got != 102.5 {
		t.Errorf("resolveExchangeAvgEntry = %v, want 102.5 (the fresh WS-cached value)", got)
	}
	if fake.fetchPositionsCalls != 0 {
		t.Errorf("FetchPositions called %d times, want 0 — a fresh WS cache must not trigger a fallback", fake.fetchPositionsCalls)
	}
}

func TestResolveExchangeAvgEntry_StaleWSCache_FallsBackToFetchPositions(t *testing.T) {
	fake := &fakeExchange{}
	ar := newTestAccountRunner(t, fake)
	// WS cache still shows the PRE-fill size (50 coins, stale) — the fill that just
	// happened brought the real position to 150 coins (computedQty below), but the WS
	// position-update event carrying that hasn't arrived yet. This is the exact race
	// found live on NIULAIUSDT (2026-09-07) and 1000NEIROCTOUSDT (2026-09-08/09).
	setWSPosition(ar, "TESTUSDT", 0, 50, 100.0)
	fake.fetchPositionsQ.push([]trader.Position{
		{Symbol: "TESTUSDT", PositionIdx: 0, Size: "150", EntryPrice: "102.5"},
	}, nil)

	sr := &StrategyRunner{
		strategy: Strategy{ID: "11111111-2222-3333-4444-555555555555", Symbol: "TESTUSDT"},
		runner:   ar,
	}

	got := sr.resolveExchangeAvgEntry(context.Background(), 0, 101.0, 150)

	if got != 102.5 {
		t.Errorf("resolveExchangeAvgEntry = %v, want 102.5 (from FetchPositions) — a stale WS cache (qty=50 < computedQty=150) must not be trusted, got the stale cached value 100.0 instead", got)
	}
	if fake.fetchPositionsCalls != 1 {
		t.Errorf("FetchPositions called %d times, want 1 — a stale WS cache must trigger exactly one fallback call", fake.fetchPositionsCalls)
	}
}

// TestResolveExchangeAvgEntry_SizeFreshButAvgStale_FallsBackToFetchPositions is the
// regression for the incident found live 2026-09-20 on NIULAIUSDT: a WS position event can
// carry a freshly updated size with avgPrice=0 (Bybit sent size=1030 but avgPrice=0 right
// after the 3rd DCA level filled). OnPositionEvent correctly leaves the avg entry cache
// untouched in that case, but the general size cache still advances to 1030 — so comparing
// computedQty against GetPositionSizeCoins alone would wrongly read the stale avg (still
// paired with the old size 410) as "fresh". The check must compare against
// GetPositionAvgEntrySize (the size the avg was actually last confirmed at) instead.
func TestResolveExchangeAvgEntry_SizeFreshButAvgStale_FallsBackToFetchPositions(t *testing.T) {
	fake := &fakeExchange{}
	ar := newTestAccountRunner(t, fake)
	// Avg entry cache last confirmed at size=410 (before the 3rd level's fill).
	setWSPosition(ar, "TESTUSDT", 0, 410, 0.1077)
	// The general size cache then advanced to 1030 on its own (WS payload carried a fresh
	// size with avgPrice=0) — simulate that without touching the avg-entry pairing.
	ar.posMu.Lock()
	ar.positions["TESTUSDT:0"] = 1030
	ar.posMu.Unlock()

	fake.fetchPositionsQ.push([]trader.Position{
		{Symbol: "TESTUSDT", PositionIdx: 0, Size: "1030", EntryPrice: "0.1003"},
	}, nil)

	sr := &StrategyRunner{
		strategy: Strategy{ID: "11111111-2222-3333-4444-555555555555", Symbol: "TESTUSDT"},
		runner:   ar,
	}

	got := sr.resolveExchangeAvgEntry(context.Background(), 0, 0.1003, 1030)

	if got != 0.1003 {
		t.Errorf("resolveExchangeAvgEntry = %v, want 0.1003 (from FetchPositions) — the WS avg (0.1077) is stale relative to computedQty=1030, must not be trusted", got)
	}
	if fake.fetchPositionsCalls != 1 {
		t.Errorf("FetchPositions called %d times, want 1 — a size-fresh-but-avg-stale cache must trigger exactly one fallback call", fake.fetchPositionsCalls)
	}
}

func TestResolveExchangeAvgEntry_ColdWSCache_FallsBackToFetchPositions(t *testing.T) {
	fake := &fakeExchange{}
	ar := newTestAccountRunner(t, fake)
	// No setWSPosition call at all — cache is cold (avg=0), e.g. right after a restart
	// before any position event has arrived. This is the pre-existing fallback path the
	// staleness fix must not have broken.
	fake.fetchPositionsQ.push([]trader.Position{
		{Symbol: "TESTUSDT", PositionIdx: 0, Size: "150", EntryPrice: "103.0"},
	}, nil)

	sr := &StrategyRunner{
		strategy: Strategy{ID: "11111111-2222-3333-4444-555555555555", Symbol: "TESTUSDT"},
		runner:   ar,
	}

	got := sr.resolveExchangeAvgEntry(context.Background(), 0, 101.0, 150)

	if got != 103.0 {
		t.Errorf("resolveExchangeAvgEntry = %v, want 103.0 (from FetchPositions on a cold cache)", got)
	}
	if fake.fetchPositionsCalls != 1 {
		t.Errorf("FetchPositions called %d times, want 1", fake.fetchPositionsCalls)
	}
}
