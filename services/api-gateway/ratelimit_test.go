//go:build integration

package main

import (
	"context"
	"testing"
	"time"
)

func TestRateLimit_BlocksAfterLimit(t *testing.T) {
	s := newTestServer(t)
	ctx := context.Background()
	key := "test:rl:blocks:" + time.Now().Format("150405.000000")
	t.Cleanup(func() { s.rdb.Del(ctx, "ratelimit:"+key) })

	// First `limit` calls are allowed.
	for i := 0; i < 3; i++ {
		allowed, _, err := s.rateLimit(ctx, key, 3, time.Minute)
		if err != nil {
			t.Fatalf("rateLimit call %d: %v", i, err)
		}
		if !allowed {
			t.Errorf("call %d should be allowed", i)
		}
	}
	// The next call exceeds the limit.
	allowed, _, err := s.rateLimit(ctx, key, 3, time.Minute)
	if err != nil {
		t.Fatalf("rateLimit over-limit: %v", err)
	}
	if allowed {
		t.Error("4th call should be blocked")
	}
}

func TestRateLimit_IndependentKeys(t *testing.T) {
	s := newTestServer(t)
	ctx := context.Background()
	stamp := time.Now().Format("150405.000000")
	keyA := "test:rl:a:" + stamp
	keyB := "test:rl:b:" + stamp
	t.Cleanup(func() {
		s.rdb.Del(ctx, "ratelimit:"+keyA)
		s.rdb.Del(ctx, "ratelimit:"+keyB)
	})

	// Exhaust keyA.
	for i := 0; i < 2; i++ {
		s.rateLimit(ctx, keyA, 2, time.Minute)
	}
	blocked, _, _ := s.rateLimit(ctx, keyA, 2, time.Minute)
	if blocked {
		t.Error("keyA should be blocked after 2 calls")
	}
	// keyB must be unaffected.
	allowed, _, _ := s.rateLimit(ctx, keyB, 2, time.Minute)
	if !allowed {
		t.Error("keyB should be allowed — independent from keyA")
	}
}
