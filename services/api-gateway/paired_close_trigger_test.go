//go:build integration

package main

import (
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestPairedCloseInFlight_DropsOverlappingTrigger: a second recompute-and-verify call for
// the same symbol, arriving while one is already running, must be dropped rather than
// running concurrently or queuing — the in-flight one already has the freshest data.
// Verified with a fake, blockable close-check function standing in for the real
// checkHedgeDeactivation/checkMatrixPairedClose call.
func TestPairedCloseInFlight_DropsOverlappingTrigger(t *testing.T) {
	s := newTestServer(t)
	var calls int32
	release := make(chan struct{})
	fakeCheck := func() {
		atomic.AddInt32(&calls, 1)
		<-release // block until the test lets it finish
	}

	go s.runPairedCloseCheck("SYMUSDT", fakeCheck)
	// Give the first goroutine a moment to enter the in-flight state before firing the
	// second — this is inherently timing-sensitive; a short sleep matches the pattern
	// already accepted elsewhere in this codebase for similar goroutine-timing tests.
	time.Sleep(50 * time.Millisecond)
	s.runPairedCloseCheck("SYMUSDT", fakeCheck) // must be a no-op (dropped), not block, not call fakeCheck again

	close(release)
	time.Sleep(50 * time.Millisecond)

	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Errorf("fakeCheck called %d times, want 1 (second overlapping trigger must be dropped)", got)
	}
}

// TestPairedCloseInFlight_AllowsSequentialTriggers: once an in-flight check completes, a
// later trigger for the same symbol must run normally (the in-flight flag is per-attempt,
// not a permanent one-shot lock).
func TestPairedCloseInFlight_AllowsSequentialTriggers(t *testing.T) {
	s := newTestServer(t)
	var calls int32
	fakeCheck := func() { atomic.AddInt32(&calls, 1) }

	s.runPairedCloseCheck("SEQUSDT", fakeCheck)
	s.runPairedCloseCheck("SEQUSDT", fakeCheck)

	if got := atomic.LoadInt32(&calls); got != 2 {
		t.Errorf("fakeCheck called %d times, want 2 (sequential, non-overlapping triggers must both run)", got)
	}
}

// TestRecomputeAndPushPairedClose_UsesLivePriceForCurrentAndPct: recomputeAndPushPairedClose
// must populate Current and Pct in the broadcast pairedCloseMsg using the live price from
// s.signalEngine.PriceHub() — previously these silently stayed at their zero value,
// permanently breaking the frontend's progress bar. Seeds a price via TickerHub.SetPrice
// (no live WS connection needed), then verifies the broadcast message's Current/Pct match
// the expected mode-0 (pnl$) combined-PnL formula at that price. Uses a bogus accountID so
// the subsequent verify-and-close fast path (triggered because unequal leg sizes give a
// finite target price) safely no-ops on "account not found" rather than needing a full
// trader/exchange mock — recomputeAndPushPairedClose broadcasts BEFORE that fast path runs,
// so the broadcast assertion is unaffected either way.
func TestRecomputeAndPushPairedClose_UsesLivePriceForCurrentAndPct(t *testing.T) {
	s := newTestServer(t)

	mainEntry, hedgeEntry := 100.0, 100.0
	mainSize, hedgeSize := 2.0, 1.0
	mainLev, hedgeLev := 10.0, 10.0
	closeValue := 5.0
	livePrice := 110.0 // combined = (110-100)*2 + (100-110)*1 = 20-10 = 10

	s.signalEngine.PriceHub().SetPrice("RECOMPUTEUSDT", livePrice)

	entry := pairedCloseWatchEntry{
		accountID:     "acc-recompute-bogus",
		botKind:       "matrix",
		cfg:           botCfgJSON{HedgeDeactCloseType: 0, HedgeDeactCloseValue: closeValue},
		symbol:        "RECOMPUTEUSDT",
		mainID:        "main-recompute",
		hedgeID:       "hedge-recompute",
		mainDir:       "long",
		hedgeDir:      "short",
		mainEntry:     mainEntry,
		hedgeEntry:    hedgeEntry,
		mainSize:      mainSize,
		hedgeSize:     hedgeSize,
		mainLeverage:  mainLev,
		hedgeLeverage: hedgeLev,
	}

	ch, unsub := s.subscribeBroadcast("acc-recompute-bogus")
	defer unsub()

	s.recomputeAndPushPairedClose(entry)

	select {
	case got := <-ch:
		msg, ok := got.(pairedCloseMsg)
		if !ok {
			t.Fatalf("broadcast message type = %T, want pairedCloseMsg", got)
		}
		wantCurrent := 10.0
		wantPct := wantCurrent / closeValue * 100
		if msg.Current < wantCurrent-0.0001 || msg.Current > wantCurrent+0.0001 {
			t.Errorf("msg.Current = %v, want %v", msg.Current, wantCurrent)
		}
		if msg.Pct < wantPct-0.0001 || msg.Pct > wantPct+0.0001 {
			t.Errorf("msg.Pct = %v, want %v", msg.Pct, wantPct)
		}
		if msg.Threshold != closeValue {
			t.Errorf("msg.Threshold = %v, want %v", msg.Threshold, closeValue)
		}
	default:
		t.Fatal("expected a pairedCloseMsg broadcast, got none")
	}
}

// TestPairedCloseSemaphore_CapsConcurrencyPerAccount: no more than the configured number
// of verify-and-close checks run concurrently for the SAME account — a burst of triggers
// queues past the cap instead of running unbounded.
//
// Uses MORE unique symbols (30) than pairedCloseSemaphoreSize (20) so the per-symbol
// in-flight dedup gate in runPairedCloseCheck can never be the limiting factor — with only
// 20 unique symbols (the previous version of this test used fmt.Sprintf("SYM%d", i%20)),
// at most 20 goroutines could ever be concurrently inside fakeCheck regardless of the
// semaphore's actual size, which made the test pass even with the semaphore effectively
// disabled (empirically verified: bumping pairedCloseSemaphoreSize to 1000 against the old
// 20-symbol version still produced maxConcurrent==20). With 30 unique symbols, all 30
// goroutines can reach fakeCheck and block on <-release, so the semaphore is the only thing
// that can cap concurrency — letting the test assert maxConcurrent == pairedCloseSemaphoreSize
// exactly (not just <=), proving the cap is both enforced AND actually reached.
func TestPairedCloseSemaphore_CapsConcurrencyPerAccount(t *testing.T) {
	s := newTestServer(t)
	var concurrent, maxConcurrent int32
	var mu sync.Mutex
	release := make(chan struct{})
	fakeCheck := func() {
		n := atomic.AddInt32(&concurrent, 1)
		mu.Lock()
		if n > maxConcurrent {
			maxConcurrent = n
		}
		mu.Unlock()
		<-release
		atomic.AddInt32(&concurrent, -1)
	}

	var wg sync.WaitGroup
	for i := 0; i < 30; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			s.runPairedCloseCheckForAccount("acc-burst", fmt.Sprintf("SYM%d", i), fakeCheck)
		}(i)
	}
	time.Sleep(100 * time.Millisecond) // let goroutines pile up against the semaphore
	close(release)
	wg.Wait()

	if maxConcurrent != pairedCloseSemaphoreSize {
		t.Errorf("max concurrent = %d, want exactly %d (pairedCloseSemaphoreSize) — cap should be both enforced and reached", maxConcurrent, pairedCloseSemaphoreSize)
	}
}
