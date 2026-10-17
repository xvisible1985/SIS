//go:build integration

package proxy

import (
	"context"
	"testing"
)

// Regression (2026-10-16, same incident as transport_test.go's ErrNoHealthyProxy tests):
// recordFailure used to jump a proxy straight from "unknown" (the status every proxy is
// reset to on every process restart — see NewManager) to "unhealthy" on its very FIRST
// failed health check, skipping the maxFailCount grace period a "healthy" proxy gets.
// One transient blip on the first post-restart check was enough to blacklist a proxy that
// might otherwise have passed its very next check 30s later. Fixed: "unknown" now
// accumulates fail_count exactly like "healthy" does.
func TestRecordFailure_UnknownStatusGetsSameGraceAsHealthy(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	m := &Manager{db: pool, encKey: "test"}

	var id int
	if err := pool.QueryRow(ctx,
		`INSERT INTO proxies (protocol, host, port, weight, is_active, health_status, fail_count)
		 VALUES ('http','198.51.100.10',3128,1,true,'unknown',0) RETURNING id`,
	).Scan(&id); err != nil {
		t.Fatalf("create proxy: %v", err)
	}
	t.Cleanup(func() { pool.Exec(ctx, "DELETE FROM proxies WHERE id=$1", id) })

	p := &Proxy{ID: id, status: "unknown"}

	// First failure from "unknown": must NOT instantly become "unhealthy".
	m.recordFailure(ctx, p)
	var status string
	var failCount int
	if err := pool.QueryRow(ctx, `SELECT health_status, fail_count FROM proxies WHERE id=$1`, id).Scan(&status, &failCount); err != nil {
		t.Fatalf("read back: %v", err)
	}
	if status != "unknown" {
		t.Errorf("health_status = %q after 1 failure from unknown, want still %q (same grace as healthy)", status, "unknown")
	}
	if failCount != 1 {
		t.Errorf("fail_count = %d, want 1", failCount)
	}

	// Two more failures (3 total = maxFailCount): now it must flip to "unhealthy".
	m.recordFailure(ctx, p)
	m.recordFailure(ctx, p)
	if err := pool.QueryRow(ctx, `SELECT health_status, fail_count FROM proxies WHERE id=$1`, id).Scan(&status, &failCount); err != nil {
		t.Fatalf("read back: %v", err)
	}
	if status != "unhealthy" {
		t.Errorf("health_status = %q after 3 failures, want %q", status, "unhealthy")
	}
	if failCount != 3 {
		t.Errorf("fail_count = %d, want 3", failCount)
	}
}

// TestRunHealthChecksSync_BlocksUntilChecksComplete pins that NewManager's startup pass
// actually waits for checks to land (so Pick() has an accurate view immediately after
// NewManager returns) rather than firing them in the background like the periodic
// runHealthChecks does — closing the ~healthCheckInterval startup blackout described in
// NewManager's comment.
func TestRunHealthChecksSync_BlocksUntilChecksComplete(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()

	var id int
	// Deliberately unroutable host/port so the health check fails fast-ish and
	// deterministically, instead of depending on a real reachable proxy.
	if err := pool.QueryRow(ctx,
		`INSERT INTO proxies (protocol, host, port, weight, is_active, health_status, fail_count)
		 VALUES ('http','198.51.100.11',3128,1,true,'unknown',0) RETURNING id`,
	).Scan(&id); err != nil {
		t.Fatalf("create proxy: %v", err)
	}
	t.Cleanup(func() { pool.Exec(ctx, "DELETE FROM proxies WHERE id=$1", id) })

	m := &Manager{db: pool, encKey: "test"}
	if err := m.reload(ctx); err != nil {
		t.Fatalf("reload: %v", err)
	}

	m.runHealthChecksSync(ctx)

	// By the time runHealthChecksSync returns, the check must have already landed —
	// i.e. fail_count/health_status in DB reflect the outcome, not still "unknown"/0
	// waiting on a goroutine that hasn't finished yet.
	var status string
	var failCount int
	if err := pool.QueryRow(ctx, `SELECT health_status, fail_count FROM proxies WHERE id=$1`, id).Scan(&status, &failCount); err != nil {
		t.Fatalf("read back: %v", err)
	}
	if failCount == 0 {
		t.Error("fail_count = 0 after runHealthChecksSync — check did not actually run synchronously")
	}
}
