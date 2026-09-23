//go:build integration

package main

import (
	"context"
	"testing"
	"time"
)

// TestDebugEventsQueries_FilterByAccount is the regression for the bug found live
// 2026-09-23: the debug-events feed (terminal "Отладка" tab) had no account filter at all
// — it only scoped by owner_id, so a user with bots on multiple accounts (e.g. two
// same-named "Gonchar 2.0" pairs, one on account SIS and one on account Semera) saw every
// account's events mixed together regardless of which account was selected in the
// terminal. Runs the exact production SQL (debugEventsFirstConnectQuery /
// debugEventsPollQuery) directly against seeded data for two accounts owned by the same
// user, proving: passing one account's id returns only that account's events, and passing
// nil (the pre-fix behavior, kept as a fallback) still returns both.
func TestDebugEventsQueries_FilterByAccount(t *testing.T) {
	s := newTestServer(t)
	ctx := context.Background()
	ownerID := createWHUser(t, s, "debugacctfilter")
	accA := createTestAccountLabeled(t, s, ownerID, "SIS")
	accB := createTestAccountLabeled(t, s, ownerID, "Semera")

	var botA, botB string
	if err := s.pool.QueryRow(ctx, `INSERT INTO bots (owner_id, account_id, name) VALUES ($1,$2,'Gonchar 2.0') RETURNING id`,
		ownerID, accA).Scan(&botA); err != nil {
		t.Fatalf("insert bot A: %v", err)
	}
	t.Cleanup(func() { s.pool.Exec(ctx, "DELETE FROM bots WHERE id=$1", botA) })
	if err := s.pool.QueryRow(ctx, `INSERT INTO bots (owner_id, account_id, name) VALUES ($1,$2,'Gonchar 2.0') RETURNING id`,
		ownerID, accB).Scan(&botB); err != nil {
		t.Fatalf("insert bot B: %v", err)
	}
	t.Cleanup(func() { s.pool.Exec(ctx, "DELETE FROM bots WHERE id=$1", botB) })

	if _, err := s.pool.Exec(ctx,
		`INSERT INTO bot_events (bot_id, message, level, category) VALUES ($1,'event on account A','info','system')`,
		botA); err != nil {
		t.Fatalf("insert bot_event A: %v", err)
	}
	if _, err := s.pool.Exec(ctx,
		`INSERT INTO bot_events (bot_id, message, level, category) VALUES ($1,'event on account B','info','system')`,
		botB); err != nil {
		t.Fatalf("insert bot_event B: %v", err)
	}

	var stratA, stratB string
	if err := s.pool.QueryRow(ctx,
		`INSERT INTO strategies (owner_id, account_id, bot_id, symbol, direction, strategy_type, status)
		 VALUES ($1,$2,$3,'DEBUGAUSDT','long','grid','active') RETURNING id`,
		ownerID, accA, botA).Scan(&stratA); err != nil {
		t.Fatalf("insert strategy A: %v", err)
	}
	t.Cleanup(func() { s.pool.Exec(ctx, "DELETE FROM strategies WHERE id=$1", stratA) })
	if err := s.pool.QueryRow(ctx,
		`INSERT INTO strategies (owner_id, account_id, bot_id, symbol, direction, strategy_type, status)
		 VALUES ($1,$2,$3,'DEBUGBUSDT','long','grid','active') RETURNING id`,
		ownerID, accB, botB).Scan(&stratB); err != nil {
		t.Fatalf("insert strategy B: %v", err)
	}
	t.Cleanup(func() { s.pool.Exec(ctx, "DELETE FROM strategies WHERE id=$1", stratB) })

	if _, err := s.pool.Exec(ctx,
		`INSERT INTO strategy_events (strategy_id, message, level) VALUES ($1,'strategy event on account A','info')`,
		stratA); err != nil {
		t.Fatalf("insert strategy_event A: %v", err)
	}
	if _, err := s.pool.Exec(ctx,
		`INSERT INTO strategy_events (strategy_id, message, level) VALUES ($1,'strategy event on account B','info')`,
		stratB); err != nil {
		t.Fatalf("insert strategy_event B: %v", err)
	}

	countMessages := func(query string, args ...any) int {
		rows, err := s.pool.Query(ctx, query, args...)
		if err != nil {
			t.Fatalf("query: %v", err)
		}
		defer rows.Close()
		n := 0
		for rows.Next() {
			n++
		}
		return n
	}

	// First-connect query, scoped to account A: only account A's 2 events (1 bot + 1 strategy).
	if n := countMessages(debugEventsFirstConnectQuery, ownerID, &accA); n != 2 {
		t.Errorf("first-connect query scoped to account A returned %d rows, want 2 (must not include account B's events)", n)
	}
	// Scoped to account B: only account B's 2 events.
	if n := countMessages(debugEventsFirstConnectQuery, ownerID, &accB); n != 2 {
		t.Errorf("first-connect query scoped to account B returned %d rows, want 2", n)
	}
	// nil account (fallback/all-accounts behavior): both accounts' events, 4 total.
	if n := countMessages(debugEventsFirstConnectQuery, ownerID, (*string)(nil)); n != 4 {
		t.Errorf("first-connect query with nil account returned %d rows, want 4 (owner-only fallback)", n)
	}

	epoch := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	if n := countMessages(debugEventsPollQuery, ownerID, epoch, &accA); n != 2 {
		t.Errorf("poll query scoped to account A returned %d rows, want 2", n)
	}
	if n := countMessages(debugEventsPollQuery, ownerID, epoch, (*string)(nil)); n != 4 {
		t.Errorf("poll query with nil account returned %d rows, want 4", n)
	}
}
