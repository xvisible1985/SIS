//go:build integration

package proxy

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// newTestPool connects to the same dev DB the other packages' integration tests use.
func newTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	pool, err := pgxpool.New(context.Background(), "postgres://sis:sis_secret@localhost:6432/sis")
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// TestUpdateProxy_SingleField_PersistsAndDoesNotError is the regression for a placeholder
// off-by-one in the dynamic UPDATE builder: the WHERE clause reused the SAME $N as
// updated_at (both landed on $(k+1) for a k-field update) instead of the next one, so
// Postgres inferred $(k+1)'s type from its WHERE-clause usage (id, integer) and rejected
// the updated_at assignment ("column \"updated_at\" is of type timestamp with time zone but
// expression is of type integer"). Found live (2026-08-26) via the admin Proxies page's
// Выключить/Включить button: every call — even this single-field one — failed with a 500
// that the frontend silently swallowed, so the toggle looked like it did nothing.
func TestUpdateProxy_SingleField_PersistsAndDoesNotError(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	m := &Manager{db: pool, encKey: "test"}

	var id int
	if err := pool.QueryRow(ctx,
		`INSERT INTO proxies (protocol, host, port, weight, is_active) VALUES ('http','198.51.100.1',3128,1,true) RETURNING id`,
	).Scan(&id); err != nil {
		t.Fatalf("create proxy: %v", err)
	}
	t.Cleanup(func() { pool.Exec(ctx, "DELETE FROM proxies WHERE id=$1", id) })

	if err := m.UpdateProxy(ctx, id, map[string]any{"is_active": false}); err != nil {
		t.Fatalf("UpdateProxy(single field): %v", err)
	}

	var isActive bool
	if err := pool.QueryRow(ctx, `SELECT is_active FROM proxies WHERE id=$1`, id).Scan(&isActive); err != nil {
		t.Fatalf("read back: %v", err)
	}
	if isActive {
		t.Error("is_active still true after UpdateProxy(is_active=false) — update did not persist")
	}
}

// TestUpdateProxy_MultiField_PersistsAndDoesNotError pins the same fix for a multi-field
// update, where the off-by-one would collide the WHERE clause with updated_at's placeholder
// regardless of how many fields are being set.
func TestUpdateProxy_MultiField_PersistsAndDoesNotError(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	m := &Manager{db: pool, encKey: "test"}

	var id int
	if err := pool.QueryRow(ctx,
		`INSERT INTO proxies (protocol, host, port, weight, is_active) VALUES ('http','198.51.100.2',3128,1,true) RETURNING id`,
	).Scan(&id); err != nil {
		t.Fatalf("create proxy: %v", err)
	}
	t.Cleanup(func() { pool.Exec(ctx, "DELETE FROM proxies WHERE id=$1", id) })

	if err := m.UpdateProxy(ctx, id, map[string]any{"is_active": false, "weight": 5}); err != nil {
		t.Fatalf("UpdateProxy(multi field): %v", err)
	}

	var isActive bool
	var weight int
	if err := pool.QueryRow(ctx, `SELECT is_active, weight FROM proxies WHERE id=$1`, id).Scan(&isActive, &weight); err != nil {
		t.Fatalf("read back: %v", err)
	}
	if isActive {
		t.Error("is_active still true after multi-field UpdateProxy — update did not persist")
	}
	if weight != 5 {
		t.Errorf("weight = %d, want 5", weight)
	}
}
