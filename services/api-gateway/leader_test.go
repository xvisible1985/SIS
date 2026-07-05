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
