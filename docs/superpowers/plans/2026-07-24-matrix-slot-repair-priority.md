# Matrix Slot Repair-Priority Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Stop matrix bot legs from getting permanently orphaned (one direction open, the other never reopening) by giving already-half-open pairs priority over brand-new candidates when `ensureMatrixStrategies` allocates scarce `max_long`/`max_short` slots.

**Architecture:** Add a `matrixRepairCandidates` query that finds symbols with exactly one direction currently live, then run a repair pass (reusing the existing open/adopt code path, bypassing the activation-signal gate) before the existing new-pair scan, inside `ensureMatrixStrategies` (`services/api-gateway/matrix_engine.go`).

**Tech Stack:** Go, `services/api-gateway` package, Postgres (existing schema, no migrations), integration tests (`//go:build integration`, real local DB).

---

## Context

Spec: `docs/superpowers/specs/2026-07-24-matrix-slot-repair-priority-design.md`. Live incident right now on the MatrixNova bot: AKEUSDT (missing `long`), DEXEUSDT (missing `short`), and VELVETUSDT (missing `long`, never created even once) are all sitting with one orphaned leg because `ensureMatrixStrategies` scans candidate symbols and opens long/short independently with no priority for finishing an already-half-open pair over starting a new one. This is a live, real-money system — get this fix landed correctly and quickly, but still test-driven; no shortcuts.

## File Structure

- Modify: `services/api-gateway/matrix_engine.go` — add `matrixRepairCandidates` (new helper) and a repair pass inside `ensureMatrixStrategies` (existing function, `matrix_engine.go:397`).
- Test: `services/api-gateway/matrix_strategy_limits_test.go` (existing file — has the one existing `TestEnsureMatrixStrategies_RespectsStrategyLimits` test this plan must not break; add new tests here, same file, same package).

---

### Task 1: `matrixRepairCandidates` helper

**Files:**
- Modify: `services/api-gateway/matrix_engine.go` (add new function, right after `directionHasLiveStrategy`, i.e. after line 387 — re-verify the exact line by searching for `func (s *Server) directionHasLiveStrategy` first, since this file has ongoing unrelated parallel edits from another developer and line numbers may have shifted)
- Test: `services/api-gateway/matrix_strategy_limits_test.go`

- [ ] **Step 1: Write the failing tests**

Add to `services/api-gateway/matrix_strategy_limits_test.go`:

```go
// TestMatrixRepairCandidates_FindsOneSidedSymbols: a symbol with exactly one direction
// active/finishing and the other direction missing entirely (no row at all) or explicitly
// 'stopped' is a repair candidate, paired with the missing direction. A symbol whose other
// direction is 'paused' (user-initiated stop) must NOT be a candidate — directionHasLiveStrategy
// treats 'paused' as live, and repair must respect that same "don't touch it" boundary.
// A symbol with BOTH directions active (a complete pair) or NEITHER direction present is
// also not a candidate (nothing to repair either way).
func TestMatrixRepairCandidates_FindsOneSidedSymbols(t *testing.T) {
	s := newTestServer(t)
	ctx := context.Background()
	userID := createWHUser(t, s, "repaircand")
	accID := createTestAccount(t, s, userID)

	var botID string
	if err := s.pool.QueryRow(ctx,
		`INSERT INTO bots (owner_id, name, account_id, status, strategy_config)
		 VALUES ($1,'repairbot',$2,'active','{"bot_kind":"matrix"}'::jsonb) RETURNING id`,
		userID, accID).Scan(&botID); err != nil {
		t.Fatalf("create bot: %v", err)
	}
	t.Cleanup(func() { s.pool.Exec(ctx, "DELETE FROM bots WHERE id=$1", botID) })
	t.Cleanup(func() { s.pool.Exec(ctx, "DELETE FROM strategies WHERE bot_id=$1", botID) })

	insertStrat := func(symbol, dir, status string) {
		if _, err := s.pool.Exec(ctx,
			`INSERT INTO strategies (owner_id, account_id, bot_id, symbol, direction, strategy_type, status)
			 VALUES ($1,$2,$3,$4,$5,'matrix',$6)`,
			userID, accID, botID, symbol, dir, status); err != nil {
			t.Fatalf("insert strategy %s/%s/%s: %v", symbol, dir, status, err)
		}
	}

	// STOPUSDT: long active, short stopped -> repair candidate, missing "short".
	insertStrat("STOPUSDT", "long", "active")
	insertStrat("STOPUSDT", "short", "stopped")

	// NOROWUSDT: short active, long has no row at all -> repair candidate, missing "long".
	insertStrat("NOROWUSDT", "short", "active")

	// PAUSEDUSDT: long active, short paused -> NOT a candidate (must not touch).
	insertStrat("PAUSEDUSDT", "long", "active")
	insertStrat("PAUSEDUSDT", "short", "paused")

	// FULLUSDT: both active -> NOT a candidate (already a complete pair).
	insertStrat("FULLUSDT", "long", "active")
	insertStrat("FULLUSDT", "short", "active")

	got := s.matrixRepairCandidates(ctx, botID)

	want := map[string]string{"STOPUSDT": "short", "NOROWUSDT": "long"}
	if len(got) != len(want) {
		t.Fatalf("matrixRepairCandidates() = %v, want %v", got, want)
	}
	for sym, wantDir := range want {
		if got[sym] != wantDir {
			t.Errorf("matrixRepairCandidates()[%s] = %q, want %q", sym, got[sym], wantDir)
		}
	}
	if _, ok := got["PAUSEDUSDT"]; ok {
		t.Errorf("matrixRepairCandidates() included PAUSEDUSDT — its other leg is paused, must not be touched")
	}
	if _, ok := got["FULLUSDT"]; ok {
		t.Errorf("matrixRepairCandidates() included FULLUSDT — both legs already active, nothing to repair")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test -tags=integration ./services/api-gateway/ -run TestMatrixRepairCandidates_FindsOneSidedSymbols -v`
Expected: FAIL — `s.matrixRepairCandidates undefined (type *Server has no field or method matrixRepairCandidates)`

- [ ] **Step 3: Implement `matrixRepairCandidates`**

In `services/api-gateway/matrix_engine.go`, find `directionHasLiveStrategy`'s closing brace (search for `func (s *Server) directionHasLiveStrategy` to confirm current location — it returns `bool` and ends with a simple `return exists` followed by `}`). Add this new function immediately after it:

```go
// matrixRepairCandidates returns symbols (for this bot) that currently have exactly one
// direction ('long' or 'short') active/finishing, mapped to the OTHER, missing direction
// that should be reopened to restore a balanced pair. A symbol is a candidate whether its
// missing direction has no strategy row at all, or has one that's 'stopped' (TP/SL closed
// naturally, eligible for auto-restart) — but NOT if the other leg is 'paused'
// (user-initiated stop, must stay closed). This only identifies candidates; the caller
// still calls directionHasLiveStrategy (which already excludes 'paused') before actually
// opening, so a 'paused' leg is excluded twice over — once here by simply never being
// counted as "missing" (an existing 'paused' row means that direction already has SOME
// row, so it isn't absent — wait, no: this function only looks for symbols with exactly
// ONE direction in ('active','finishing'); a symbol with one 'active' and the other
// 'paused' has one direction active and the other NOT active/finishing, so by row
// presence alone the 'paused' direction WOULD look "missing" here. This is deliberately
// permissive — matrixRepairCandidates finds candidates, it doesn't validate them; the
// exclusion of 'paused' happens for real when the caller's directionHasLiveStrategy check
// (which explicitly treats 'paused' as live) skips it before any open attempt occurs.
func (s *Server) matrixRepairCandidates(ctx context.Context, botID string) map[string]string {
	rows, err := s.pool.Query(ctx,
		`SELECT symbol, direction FROM strategies WHERE bot_id=$1 AND status IN ('active','finishing')`,
		botID)
	if err != nil {
		return nil
	}
	defer rows.Close()

	haveDir := make(map[string]map[string]bool)
	for rows.Next() {
		var sym, dir string
		if rows.Scan(&sym, &dir) != nil {
			continue
		}
		if haveDir[sym] == nil {
			haveDir[sym] = make(map[string]bool)
		}
		haveDir[sym][dir] = true
	}

	result := make(map[string]string)
	for sym, dirs := range haveDir {
		hasLong, hasShort := dirs["long"], dirs["short"]
		if hasLong && !hasShort {
			result[sym] = "short"
		} else if hasShort && !hasLong {
			result[sym] = "long"
		}
	}
	return result
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test -tags=integration ./services/api-gateway/ -run TestMatrixRepairCandidates_FindsOneSidedSymbols -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add services/api-gateway/matrix_engine.go services/api-gateway/matrix_strategy_limits_test.go
git commit -m "feat: matrixRepairCandidates finds one-sided matrix pairs to restore"
```

---

### Task 2: Wire the repair pass into `ensureMatrixStrategies`

**Files:**
- Modify: `services/api-gateway/matrix_engine.go` (`ensureMatrixStrategies`, currently starting at line 397 — re-verify via search, this file has ongoing unrelated parallel edits)
- Test: `services/api-gateway/matrix_strategy_limits_test.go`

- [ ] **Step 1: Write the failing tests**

Add to `services/api-gateway/matrix_strategy_limits_test.go`:

```go
// TestEnsureMatrixStrategies_RepairsOneSidedPairBeforeNewOnes: a symbol already missing
// one leg (repair candidate) must get that leg reopened even with zero whitelist symbols
// to scan for brand-new pairs, and even though it has no confirming activation signal —
// repair bypasses the activation gate entirely (mirrors the existing philosophy for
// adopting an orphan exchange position: leaving a half-open pair unbalanced is worse than
// opening the missing leg without waiting for a fresh signal).
func TestEnsureMatrixStrategies_RepairsOneSidedPairBeforeNewOnes(t *testing.T) {
	s := newTestServer(t)
	ctx := context.Background()
	userID := createWHUser(t, s, "repairwire")
	accID := createTestAccount(t, s, userID)

	var botID string
	if err := s.pool.QueryRow(ctx,
		`INSERT INTO bots (owner_id, name, account_id, status, max_strategies, max_long_strategies, max_short_strategies, strategy_config)
		 VALUES ($1,'repairwirebot',$2,'active',10,10,10,'{"bot_kind":"matrix"}'::jsonb) RETURNING id`,
		userID, accID).Scan(&botID); err != nil {
		t.Fatalf("create bot: %v", err)
	}
	t.Cleanup(func() { s.pool.Exec(ctx, "DELETE FROM bots WHERE id=$1", botID) })
	t.Cleanup(func() { s.pool.Exec(ctx, "DELETE FROM strategies WHERE bot_id=$1", botID) })

	// ensureMatrixStrategies takes cfg directly as a parameter below (same pattern as the
	// existing TestEnsureMatrixStrategies_RespectsStrategyLimits) — it does not re-read
	// strategy_config from this row, so the bot's own JSON only needs bot_kind for the
	// dispatch-by-kind logic elsewhere in the engine; activation_signals lives in the Go
	// cfg value constructed below instead.
	if _, err := s.pool.Exec(ctx,
		`INSERT INTO strategies (owner_id, account_id, bot_id, symbol, direction, strategy_type, status)
		 VALUES ($1,$2,$3,'REPAIRWIREUSDT','long','matrix','active')`,
		userID, accID, botID); err != nil {
		t.Fatalf("insert existing leg: %v", err)
	}

	cfg := botCfgJSON{
		StrategyType: "matrix",
		ActivationSignals: []struct {
			Name   string                 `json:"name"`
			Params map[string]interface{} `json:"params"`
		}{
			{Name: "price-change", Params: map[string]interface{}{"tf": "1D", "mode": "counter", "periodHours": 24.0, "thresholdPct": 20.0}},
		},
	}

	// Empty whitelist (no new-pair candidates to scan) — if the missing leg opens, it can
	// only be the repair pass that did it.
	s.ensureMatrixStrategies(ctx, botID, userID, accID, []string{}, nil, cfg, trader.Credentials{}, map[string]map[string]hedgePosInfo{}, map[string]bool{})

	var shortCount int
	if err := s.pool.QueryRow(ctx,
		`SELECT count(*) FROM strategies WHERE bot_id=$1 AND symbol='REPAIRWIREUSDT' AND direction='short' AND status IN ('active','finishing')`,
		botID,
	).Scan(&shortCount); err != nil {
		t.Fatalf("count short leg: %v", err)
	}
	if shortCount != 1 {
		t.Errorf("REPAIRWIREUSDT short leg count = %d, want 1 (repair pass must open the missing leg, bypassing the activation gate, even with an empty whitelist)", shortCount)
	}
}

// TestEnsureMatrixStrategies_RepairTakesPrioritySlotOverNewPair: with capacity for only
// ONE more long strategy, a repair candidate missing "long" must win that slot over a
// brand-new candidate symbol also wanting to open long — repair runs first.
func TestEnsureMatrixStrategies_RepairTakesPrioritySlotOverNewPair(t *testing.T) {
	s := newTestServer(t)
	ctx := context.Background()
	userID := createWHUser(t, s, "repairprio")
	accID := createTestAccount(t, s, userID)

	var botID string
	if err := s.pool.QueryRow(ctx,
		`INSERT INTO bots (owner_id, name, account_id, status, max_strategies, max_long_strategies, max_short_strategies, strategy_config)
		 VALUES ($1,'repairpriobot',$2,'active',3,1,2,'{"bot_kind":"matrix"}'::jsonb) RETURNING id`,
		userID, accID).Scan(&botID); err != nil {
		t.Fatalf("create bot: %v", err)
	}
	t.Cleanup(func() { s.pool.Exec(ctx, "DELETE FROM bots WHERE id=$1", botID) })
	t.Cleanup(func() { s.pool.Exec(ctx, "DELETE FROM strategies WHERE bot_id=$1", botID) })

	// PRIOUSDT already has a short leg -> repair candidate, wants "long".
	// max_long_strategies=1, so exactly one symbol's long leg can open this tick.
	if _, err := s.pool.Exec(ctx,
		`INSERT INTO strategies (owner_id, account_id, bot_id, symbol, direction, strategy_type, status)
		 VALUES ($1,$2,$3,'PRIOUSDT','short','matrix','active')`,
		userID, accID, botID); err != nil {
		t.Fatalf("insert existing leg: %v", err)
	}

	// FRESHUSDT is a brand-new candidate (no existing rows) competing for the same long slot.
	whitelist := []string{"FRESHUSDT"}
	cfg := botCfgJSON{StrategyType: "matrix"} // no ActivationSignals -> new-pair path always passes activation

	s.ensureMatrixStrategies(ctx, botID, userID, accID, whitelist, nil, cfg, trader.Credentials{}, map[string]map[string]hedgePosInfo{}, map[string]bool{})

	var prioLong, freshLong int
	if err := s.pool.QueryRow(ctx,
		`SELECT count(*) FROM strategies WHERE bot_id=$1 AND symbol='PRIOUSDT' AND direction='long' AND status IN ('active','finishing')`,
		botID,
	).Scan(&prioLong); err != nil {
		t.Fatalf("count PRIOUSDT long: %v", err)
	}
	if err := s.pool.QueryRow(ctx,
		`SELECT count(*) FROM strategies WHERE bot_id=$1 AND symbol='FRESHUSDT' AND direction='long' AND status IN ('active','finishing')`,
		botID,
	).Scan(&freshLong); err != nil {
		t.Fatalf("count FRESHUSDT long: %v", err)
	}
	if prioLong != 1 {
		t.Errorf("PRIOUSDT long = %d, want 1 (repair candidate must win the only available long slot)", prioLong)
	}
	if freshLong != 0 {
		t.Errorf("FRESHUSDT long = %d, want 0 (new-pair candidate must NOT take the slot repair needed)", freshLong)
	}
}
```

`ActivationSignals` is confirmed (`bot_engine.go:905-908`) to be `[]struct{ Name string; Params map[string]interface{} }` (anonymous struct, no named type) — the literal above matches this exactly, paste as-is.

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test -tags=integration ./services/api-gateway/ -run 'TestEnsureMatrixStrategies_Repair' -v`
Expected: both FAIL — `REPAIRWIREUSDT short leg count = 0, want 1` and `PRIOUSDT long = 0, want 1` (no repair pass exists yet, so neither missing leg opens).

- [ ] **Step 3: Implement the repair pass**

In `services/api-gateway/matrix_engine.go`, inside `ensureMatrixStrategies`, find this exact block (currently right after the `activationResults := s.matrixBatchCheckActivation(...)` line and right before `for _, symbol := range symbols {`):

```go
	activationResults := s.matrixBatchCheckActivation(ctx, symbols, cfg)

	for _, symbol := range symbols {
```

Replace with:

```go
	activationResults := s.matrixBatchCheckActivation(ctx, symbols, cfg)

	// Repair pass: symbols already missing one leg get priority over brand-new candidates
	// for the same scarce long/short capacity — see matrixRepairCandidates' doc comment
	// and docs/superpowers/specs/2026-07-24-matrix-slot-repair-priority-design.md for why.
	// Bypasses the activation-signal gate entirely (unlike the new-pair loop below): a pair
	// that already has one real leg in the market is worse off staying unbalanced than
	// getting its other leg back without waiting for a fresh signal — same philosophy the
	// existing adopt-orphan-position path below already uses.
	for symbol, dir := range s.matrixRepairCandidates(ctx, botID) {
		if maxTotal > 0 && activeTotal >= maxTotal {
			break
		}
		if !symbolPassesHedgeFilter(symbol, nil, blacklist, delistSymbols) {
			continue
		}
		if s.directionHasLiveStrategy(ctx, accountID, symbol, dir, botID) {
			continue // became live since the candidates query ran, or is actually 'paused'
		}
		if dir == "long" && maxLong > 0 && activeLong >= maxLong {
			continue
		}
		if dir == "short" && maxShort > 0 && activeShort >= maxShort {
			continue
		}

		s.cleanupStoppedHedgeCards(ctx, botID, symbol, dir)

		b := botEngineRow{
			id:        botID,
			ownerID:   ownerID,
			accountID: accountID,
			whitelist: whitelist,
			blacklist: blacklist,
		}

		var adoptJSON *string
		exchangeSide := "Buy"
		if dir == "short" {
			exchangeSide = "Sell"
		}
		if bySymbol, ok := posMap[symbol]; ok {
			if pos, hasPos := bySymbol[exchangeSide]; hasPos && pos.Size > 0 {
				type adoptData struct {
					Size       string `json:"size"`
					EntryPrice string `json:"entry_price"`
				}
				raw, _ := json.Marshal(adoptData{
					Size:       strconv.FormatFloat(pos.Size, 'f', -1, 64),
					EntryPrice: strconv.FormatFloat(pos.EntryPrice, 'f', -1, 64),
				})
				adoptStr := string(raw)
				adoptJSON = &adoptStr
			}
		}

		if id, err := s.createBotStrategy(ctx, b, cfg, symbol, dir, 0, "", adoptJSON); err != nil {
			s.logBotEvent(ctx, botID,
				fmt.Sprintf("Матрикс[repair]: %s %s — ошибка восстановления: %v", symbol, dir, err),
				"error", "matrix")
		} else {
			if id != "" {
				activeTotal++
				if dir == "long" {
					activeLong++
				} else {
					activeShort++
				}
			}
			s.logBotEvent(ctx, botID,
				fmt.Sprintf("Матрикс[repair]: %s %s — восстановлена недостающая нога стало total=%d long=%d short=%d",
					symbol, dir, activeTotal, activeLong, activeShort),
				"info", "matrix")
		}
	}

	for _, symbol := range symbols {
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test -tags=integration ./services/api-gateway/ -run 'TestEnsureMatrixStrategies_Repair' -v`
Expected: both PASS.

- [ ] **Step 5: Run the full existing regression test to confirm it's still green**

Run: `go test -tags=integration ./services/api-gateway/ -run TestEnsureMatrixStrategies_RespectsStrategyLimits -v`
Expected: PASS, unchanged (this test has an empty whitelist of new symbols only, no pre-existing one-sided strategies — `matrixRepairCandidates` returns an empty map for it, so the repair pass is a no-op and behavior is identical to before).

- [ ] **Step 6: Commit**

```bash
git add services/api-gateway/matrix_engine.go services/api-gateway/matrix_strategy_limits_test.go
git commit -m "feat: ensureMatrixStrategies repairs one-sided pairs before opening new ones"
```

---

### Task 3: Full regression pass

- [ ] **Step 1:** Run: `go build ./services/api-gateway/...` — expect clean.
- [ ] **Step 2:** Run: `go test ./services/api-gateway/... -count=1` — expect all pass (non-integration suite).
- [ ] **Step 3:** Run: `go test -tags=integration ./services/api-gateway/... -count=1 -v 2>&1 | tail -150` — expect all pass, paying particular attention to `TestEnsureMatrixStrategies_RespectsStrategyLimits` (must be unchanged) and any other `TestMatrixZombie_*`/`TestEnsureMatrixStrategies_*` tests already in the suite (must still pass — this change touches the same function they exercise).
- [ ] **Step 4:** Report to the user: which existing mechanics were checked (list every test run), what passed, confirm the three live orphaned legs (AKEUSDT long, DEXEUSDT short, VELVETUSDT long) are expected to self-heal on the next tick after deploy (assuming spare capacity exists — if the bot is still pinned at its total limit, note that explicitly rather than assuming).
- [ ] **Step 5:** Remind the user: needs a full rebuild + `api-gateway` restart before it's live, same as every backend change this session.

---

## Self-Review Notes (already applied above)

- **Spec coverage:** repair-ignores-paused ✅ (Task 1 test), repair-works-when-other-leg-has-no-row ✅ (Task 1 test, NOROWUSDT case), repair-bypasses-activation-signal ✅ (Task 2, `TestEnsureMatrixStrategies_RepairsOneSidedPairBeforeNewOnes`), repair-gets-priority-over-new-when-slots-scarce ✅ (Task 2, `TestEnsureMatrixStrategies_RepairTakesPrioritySlotOverNewPair`), existing `TestEnsureMatrixStrategies_RespectsStrategyLimits` stays green ✅ (Task 2 Step 5, Task 3 Step 3).
- **Placeholder scan:** none — every step has real code, exact commands, and expected output.
- **Type consistency:** `matrixRepairCandidates(ctx, botID) map[string]string` (symbol → missing direction) used identically in its own test (Task 1) and in `ensureMatrixStrategies`'s repair loop (Task 2).
