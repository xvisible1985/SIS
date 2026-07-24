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

// TestPairedCloseSemaphore_CapsConcurrencyPerAccount: no more than the configured number
// of verify-and-close checks run concurrently for the SAME account — a burst of triggers
// queues past the cap instead of running unbounded.
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
			s.runPairedCloseCheckForAccount("acc-burst", fmt.Sprintf("SYM%d", i%20), fakeCheck)
		}(i)
	}
	time.Sleep(100 * time.Millisecond) // let goroutines pile up against the semaphore
	close(release)
	wg.Wait()

	if maxConcurrent > pairedCloseSemaphoreSize {
		t.Errorf("max concurrent = %d, want <= %d (pairedCloseSemaphoreSize)", maxConcurrent, pairedCloseSemaphoreSize)
	}
}
