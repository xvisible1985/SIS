//go:build integration

package main

import (
	"context"
	"testing"
	"time"
)

func TestTradingLeader_MutualExclusion(t *testing.T) {
	s := newTestServer(t)
	ctx := context.Background()
	key := "test:leader:" + time.Now().Format("150405.000000")

	a := newTradingLeader(s.rdb, key, 10*time.Second)
	b := newTradingLeader(s.rdb, key, 10*time.Second)
	t.Cleanup(func() { a.Release(ctx); b.Release(ctx) })

	if !a.Acquire(ctx) {
		t.Fatal("leader A should acquire an unheld lock")
	}
	if b.Acquire(ctx) {
		t.Error("leader B must NOT acquire while A holds the lock")
	}
	a.Release(ctx)
	if !b.Acquire(ctx) {
		t.Error("leader B should acquire after A releases")
	}
}

func TestTradingLeader_ReleaseOnlyOwnLock(t *testing.T) {
	s := newTestServer(t)
	ctx := context.Background()
	key := "test:leader:release:" + time.Now().Format("150405.000000")

	a := newTradingLeader(s.rdb, key, 10*time.Second)
	b := newTradingLeader(s.rdb, key, 10*time.Second)
	t.Cleanup(func() { a.Release(ctx); b.Release(ctx) })

	if !a.Acquire(ctx) {
		t.Fatal("A should acquire")
	}
	// B never held the lock; B.Release must not remove A's lock.
	b.Release(ctx)
	if b.Acquire(ctx) {
		t.Error("A's lock must survive B.Release — B should still fail to acquire")
	}
}

// Regression for the 2026-08-12 incident: instance A died without releasing (killed,
// crashed — Release never ran), instance B started moments later within A's TTL
// window. A single Acquire attempt fails and (since main.go only calls Acquire once,
// at startup) B would then run for its entire lifetime with every order-managing
// engine silently off. AcquireWithRetry must wait out the remaining TTL and succeed
// once A's abandoned lock expires, instead of giving up immediately.
func TestTradingLeader_AcquireWithRetry_WaitsOutAbandonedLock(t *testing.T) {
	s := newTestServer(t)
	ctx := context.Background()
	key := "test:leader:retry:" + time.Now().Format("150405.000000")

	// A short TTL keeps the test fast — AcquireWithRetry's own 1s poll interval is
	// what's under test, not the production 30s TTL.
	const ttl = 2 * time.Second
	a := newTradingLeader(s.rdb, key, ttl)
	if !a.Acquire(ctx) {
		t.Fatal("A should acquire an unheld lock")
	}
	// A "dies" here without calling Release — simulates a kill/crash.

	b := newTradingLeader(s.rdb, key, ttl)
	t.Cleanup(func() { b.Release(ctx) })

	start := time.Now()
	if !b.AcquireWithRetry(ctx, 2*ttl) {
		t.Fatal("B should acquire once A's abandoned lock expires")
	}
	if elapsed := time.Since(start); elapsed < ttl {
		t.Errorf("acquired too fast (%v) — expected to wait out A's ~%v TTL", elapsed, ttl)
	}
}

// A live, still-renewing holder must never be pre-empted, no matter how long
// AcquireWithRetry is willing to wait — that's the core safety property the whole
// design exists for (see TradingLeader's doc comment).
func TestTradingLeader_AcquireWithRetry_DoesNotPreemptLiveHolder(t *testing.T) {
	s := newTestServer(t)
	ctx := context.Background()
	key := "test:leader:retry-live:" + time.Now().Format("150405.000000")

	a := newTradingLeader(s.rdb, key, 10*time.Second)
	t.Cleanup(func() { a.Release(ctx) })
	if !a.Acquire(ctx) {
		t.Fatal("A should acquire an unheld lock")
	}

	b := newTradingLeader(s.rdb, key, 10*time.Second)
	if b.AcquireWithRetry(ctx, 3*time.Second) {
		t.Fatal("B must not acquire while A's lock is still valid")
	}
}
