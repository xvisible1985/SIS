# Hedge-bot flip: promote in place instead of detach — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** When a hedge bot's main leg closes via TP with `hedge_tp_main_mode="flip"`, the surviving hedge leg becomes the new main **in place** — keeping its `bot_id`, `strategy_type`, and SL/TP config unchanged — instead of being detached into an unmanaged, SL-less standalone grid (today's `releaseHedgeToGrid` behavior).

**Architecture:** Add a new function `promoteHedgeToMain` in `services/api-gateway/hedge_engine.go` that mirrors `releaseHedgeToGrid`'s tail (delete old main, immediately open a new hedge against the promoted leg) but — unlike `releaseHedgeToGrid` — never touches the promoted strategy's own trading config, only clears its `hedged_strategy_id`. Rewire the two genuinely-"flip" call sites (`handleMainTpFlip`, and the missed-flip-recovery branch in `checkHedgeDeactivation`) to call it instead of `releaseHedgeToGrid`. The third call site (ordinary deactivation — main still alive, just recovered) keeps calling `releaseHedgeToGrid` unchanged.

**Tech Stack:** Go, PostgreSQL (pgx), existing `services/api-gateway` integration test harness (`//go:build integration`, real local Postgres via `newTestServer(t)`).

**Spec:** `docs/superpowers/specs/2026-10-26-hedge-flip-promote-in-place-design.md`

---

## Task 1: `promoteHedgeToMain` + rewire `handleMainTpFlip`

**Files:**
- Modify: `services/api-gateway/hedge_engine.go`
- Test: `services/api-gateway/promote_hedge_to_main_test.go` (new)

- [ ] **Step 1: Write the failing tests**

Create `services/api-gateway/promote_hedge_to_main_test.go`:

```go
//go:build integration

package main

import (
	"context"
	"testing"
)

// seedFlipFixture creates a bot_kind="hedge", hedge_tp_main_mode="flip" bot, a stopped
// "old main" strategy, and an active hedge strategy (hedged_strategy_id -> old main) with
// one filled level in an open cycle — the minimum state handleMainTpFlip needs to act.
// Returns botID, oldMainID, hedgeID.
func seedFlipFixture(t *testing.T, s *Server, userID, accID, symbol, hedgeDir string) (botID, oldMainID, hedgeID string) {
	t.Helper()
	ctx := context.Background()

	cfgJSON := `{"bot_kind":"hedge","hedge_tp_main_mode":"flip","strategy_type":"grid","grid_size_usdt":20,"sl_type":"conditional","sl_pct":-5}`
	if err := s.pool.QueryRow(ctx,
		`INSERT INTO bots (owner_id, name, account_id, status, strategy_config)
		 VALUES ($1,'flip-bot','`+accID+`','active',$2::jsonb) RETURNING id`,
		userID, cfgJSON,
	).Scan(&botID); err != nil {
		t.Fatalf("insert bot: %v", err)
	}
	t.Cleanup(func() { s.pool.Exec(context.Background(), "DELETE FROM bots WHERE id=$1", botID) })

	mainDir := "long"
	if hedgeDir == "long" {
		mainDir = "short"
	}
	if err := s.pool.QueryRow(ctx,
		`INSERT INTO strategies (owner_id, account_id, symbol, direction, strategy_type, status, bot_id)
		 VALUES ($1,$2,$3,$4,'grid','stopped',$5) RETURNING id`,
		userID, accID, symbol, mainDir, botID,
	).Scan(&oldMainID); err != nil {
		t.Fatalf("insert old main: %v", err)
	}
	t.Cleanup(func() { s.pool.Exec(context.Background(), "DELETE FROM strategies WHERE id=$1", oldMainID) })

	slPct := -5.0
	if err := s.pool.QueryRow(ctx,
		`INSERT INTO strategies (owner_id, account_id, symbol, direction, strategy_type, status, bot_id, hedged_strategy_id, sl_type, sl_pct)
		 VALUES ($1,$2,$3,$4,'grid','active',$5,$6,'conditional',$7) RETURNING id`,
		userID, accID, symbol, hedgeDir, botID, oldMainID, slPct,
	).Scan(&hedgeID); err != nil {
		t.Fatalf("insert hedge: %v", err)
	}
	t.Cleanup(func() { s.pool.Exec(context.Background(), "DELETE FROM strategies WHERE id=$1", hedgeID) })

	var cycleID string
	if err := s.pool.QueryRow(ctx,
		`INSERT INTO strategy_cycles (strategy_id, cycle_num, started_at) VALUES ($1,1,NOW()) RETURNING id`,
		hedgeID,
	).Scan(&cycleID); err != nil {
		t.Fatalf("insert cycle: %v", err)
	}
	t.Cleanup(func() { s.pool.Exec(context.Background(), "DELETE FROM strategy_cycles WHERE id=$1", cycleID) })

	side := "Sell"
	if hedgeDir == "long" {
		side = "Buy"
	}
	if _, err := s.pool.Exec(ctx,
		`INSERT INTO strategy_levels (strategy_id, cycle_id, level_idx, side, target_price, size_usdt, qty, status, filled_price)
		 VALUES ($1,$2,1,$3,0,20,'400','filled',0.05)`,
		hedgeID, cycleID, side,
	); err != nil {
		t.Fatalf("insert filled level: %v", err)
	}

	return botID, oldMainID, hedgeID
}

// TestHandleMainTpFlip_PromotesHedgeInPlace_KeepsConfigAndBotId is the regression test for
// the live incident (2026-10-25/26, ALS-Bot, APRUSDT): handleMainTpFlip used to detach the
// hedge (bot_id=NULL, strategy_type forced to 'grid', sl_pct=0) via releaseHedgeToGrid. It
// must now promote the hedge in place via promoteHedgeToMain instead — bot_id, strategy_type
// and sl_pct stay exactly as they were, only hedged_strategy_id is cleared — and a new hedge
// must exist immediately (not gated on any drawdown threshold), linked back to the promoted
// strategy via its own hedged_strategy_id.
func TestHandleMainTpFlip_PromotesHedgeInPlace_KeepsConfigAndBotId(t *testing.T) {
	s := newTestServer(t)
	userID := createWHUser(t, s, "flip1")
	accID := createTestAccount(t, s, userID)
	stubBybitPublicInstrumentInfoOK(t)

	botID, oldMainID, hedgeID := seedFlipFixture(t, s, userID, accID, "FLPAUSDT", "short")
	t.Cleanup(func() {
		s.pool.Exec(context.Background(), "DELETE FROM strategies WHERE symbol='FLPAUSDT' AND bot_id=$1", botID)
	})

	s.handleMainTpFlip(context.Background(), oldMainID)

	var botIDAfter, stratType string
	var slPct *float64
	var hedgedStratID *string
	if err := s.pool.QueryRow(context.Background(),
		`SELECT COALESCE(bot_id::text,''), strategy_type, sl_pct, hedged_strategy_id::text
		 FROM strategies WHERE id=$1`, hedgeID,
	).Scan(&botIDAfter, &stratType, &slPct, &hedgedStratID); err != nil {
		t.Fatalf("read promoted strategy: %v", err)
	}
	if botIDAfter != botID {
		t.Errorf("bot_id = %q, want %q — promoted leg must stay attached to the bot", botIDAfter, botID)
	}
	if stratType != "grid" {
		t.Errorf("strategy_type = %q, want unchanged %q", stratType, "grid")
	}
	if slPct == nil || *slPct != -5.0 {
		t.Errorf("sl_pct = %v, want unchanged -5.0 — promotion must not strip SL protection", slPct)
	}
	if hedgedStratID != nil {
		t.Errorf("hedged_strategy_id = %v, want NULL — promoted leg is now the main, not anyone's hedge", *hedgedStratID)
	}

	var oldMainExists bool
	s.pool.QueryRow(context.Background(), `SELECT EXISTS(SELECT 1 FROM strategies WHERE id=$1)`, oldMainID).Scan(&oldMainExists) //nolint:errcheck
	if oldMainExists {
		t.Error("old main strategy still exists — must be deleted after promotion, same as releaseHedgeToGrid")
	}

	var newHedgeID, newHedgeDir string
	if err := s.pool.QueryRow(context.Background(),
		`SELECT id, direction FROM strategies WHERE hedged_strategy_id=$1 AND status IN ('active','finishing')`,
		hedgeID,
	).Scan(&newHedgeID, &newHedgeDir); err != nil {
		t.Fatalf("no new hedge found linked to the promoted strategy: %v", err)
	}
	if newHedgeDir != "long" {
		t.Errorf("new hedge direction = %q, want %q (opposite of the promoted short leg)", newHedgeDir, "long")
	}
}

// TestHandleMainTpFlip_ChainedFlips_NeverTwoRowsInSameSlot reproduces the live blocking
// incident directly: two flips in a row on the same symbol must never leave two active
// rows occupying the same (account, symbol, direction) slot — each flip reuses the
// existing row in place instead of leaving a parallel orphan behind.
func TestHandleMainTpFlip_ChainedFlips_NeverTwoRowsInSameSlot(t *testing.T) {
	s := newTestServer(t)
	userID := createWHUser(t, s, "flip2")
	accID := createTestAccount(t, s, userID)
	stubBybitPublicInstrumentInfoOK(t)

	botID, oldMainID, hedgeAID := seedFlipFixture(t, s, userID, accID, "FLPBUSDT", "short")
	t.Cleanup(func() {
		s.pool.Exec(context.Background(), "DELETE FROM strategies WHERE symbol='FLPBUSDT' AND bot_id=$1", botID)
	})

	// Flip 1: oldMain (long) closes by TP -> hedgeA (short) promoted to main, hedgeB (long) created.
	s.handleMainTpFlip(context.Background(), oldMainID)

	countActive := func(symbol, direction string) int {
		var n int
		s.pool.QueryRow(context.Background(), //nolint:errcheck
			`SELECT COUNT(*) FROM strategies WHERE symbol=$1 AND direction=$2 AND status IN ('active','finishing')`,
			symbol, direction,
		).Scan(&n)
		return n
	}
	if n := countActive("FLPBUSDT", "short"); n != 1 {
		t.Fatalf("after flip 1: %d active short rows, want 1", n)
	}
	if n := countActive("FLPBUSDT", "long"); n != 1 {
		t.Fatalf("after flip 1: %d active long rows, want 1", n)
	}

	var hedgeBID string
	if err := s.pool.QueryRow(context.Background(),
		`SELECT id FROM strategies WHERE symbol='FLPBUSDT' AND direction='long' AND status IN ('active','finishing')`,
	).Scan(&hedgeBID); err != nil {
		t.Fatalf("find hedgeB: %v", err)
	}

	// Give hedgeB a filled level too, so it qualifies for its own flip.
	var cycleBID string
	if err := s.pool.QueryRow(context.Background(),
		`INSERT INTO strategy_cycles (strategy_id, cycle_num, started_at) VALUES ($1,1,NOW()) RETURNING id`,
		hedgeBID,
	).Scan(&cycleBID); err != nil {
		t.Fatalf("insert hedgeB cycle: %v", err)
	}
	if _, err := s.pool.Exec(context.Background(),
		`INSERT INTO strategy_levels (strategy_id, cycle_id, level_idx, side, target_price, size_usdt, qty, status, filled_price)
		 VALUES ($1,$2,1,'Buy',0,20,'400','filled',0.05)`,
		hedgeBID, cycleBID,
	); err != nil {
		t.Fatalf("insert hedgeB filled level: %v", err)
	}
	// hedgeA (now promoted to main, short) must be 'stopped' for flip 2's cleanup DELETE to fire —
	// simulates hedgeA's own cycle later closing via TP, same as any main before a flip.
	if _, err := s.pool.Exec(context.Background(),
		`UPDATE strategies SET status='stopped' WHERE id=$1`, hedgeAID,
	); err != nil {
		t.Fatalf("stop hedgeA: %v", err)
	}

	// Flip 2: hedgeA (short, now main) closes by TP -> hedgeB (long) promoted to main,
	// a new short hedge (hedgeC) created. hedgeA must be deleted.
	s.handleMainTpFlip(context.Background(), hedgeAID)

	if n := countActive("FLPBUSDT", "short"); n != 1 {
		t.Errorf("after flip 2: %d active short rows, want 1 (no orphan accumulation)", n)
	}
	if n := countActive("FLPBUSDT", "long"); n != 1 {
		t.Errorf("after flip 2: %d active long rows, want 1", n)
	}
	var hedgeAExists bool
	s.pool.QueryRow(context.Background(), `SELECT EXISTS(SELECT 1 FROM strategies WHERE id=$1)`, hedgeAID).Scan(&hedgeAExists) //nolint:errcheck
	if hedgeAExists {
		t.Error("hedgeA (old main after flip 2) still exists — must be deleted")
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `cd services/api-gateway && go test -tags integration -run 'TestHandleMainTpFlip_PromotesHedgeInPlace_KeepsConfigAndBotId|TestHandleMainTpFlip_ChainedFlips_NeverTwoRowsInSameSlot' -v ./...`

Expected: FAIL — `TestHandleMainTpFlip_PromotesHedgeInPlace_KeepsConfigAndBotId` fails on `botIDAfter != botID` (today's `handleMainTpFlip` still calls `releaseHedgeToGrid`, which clears `bot_id` to NULL and forces `strategy_type='grid'`/`sl_pct=0` — the assertions on `stratType`/`slPct`/`hedgedStratID` will also fail once that one is fixed, so don't worry about which specific assertion trips first). `TestHandleMainTpFlip_ChainedFlips_NeverTwoRowsInSameSlot` fails with more than 1 active row in a slot, or on the slot-conflict path producing a stopped/missing hedgeC.

- [ ] **Step 3: Implement `promoteHedgeToMain` and rewire `handleMainTpFlip`**

In `services/api-gateway/hedge_engine.go`, insert the new function immediately after `releaseHedgeToGrid` ends (right before the `// handleMainTpFlip is called when a main strategy closes at TP.` comment, i.e. right after this existing closing block):

```go
			if newID, err := s.createBotStrategy(ctx, b, cfg, symbol, newHedgeDir, 0, strategyID, nil); err != nil {
				s.logBotEvent(ctx, botID,
					fmt.Sprintf("Хедж: ошибка создания нового хеджа %s после переворота: %v", symbol, err),
					"error", "hedge")
			} else {
				s.logBotEvent(ctx, botID,
					fmt.Sprintf("Хедж: новый %s хедж создан после переворота %s → %s", newHedgeDir, symbol, newID[:8]),
					"info", "hedge")
			}
		}
	}
}
```

Add this new function right after that closing `}`:

```go
// promoteHedgeToMain is releaseHedgeToGrid's counterpart for a genuine flip
// (hedge_tp_main_mode="flip"): instead of detaching the hedge and resetting its trading
// config to a bare grid with no SL, the hedge keeps its bot_id, strategy_type, and SL/TP
// config exactly as they were — it simply becomes the new main in place. The only field
// this touches on the promoted strategy is its own hedged_strategy_id (it was pointing at
// the now-closed old main; cleared since this strategy is no longer anyone's hedge). A
// new hedge is then opened against it immediately — same tail as releaseHedgeToGrid's own
// immediate re-hedge, just without resetting the promoted leg's config first.
func (s *Server) promoteHedgeToMain(ctx context.Context, botID, strategyID, symbol string, hedgePos hedgePosInfo, reason string) {
	// Remember the old main ID before clearing the link — we'll delete it below.
	var oldMainID string
	s.pool.QueryRow(ctx, //nolint:errcheck
		`SELECT COALESCE(hedged_strategy_id::text,'') FROM strategies WHERE id=$1`, strategyID,
	).Scan(&oldMainID)

	if _, err := s.pool.Exec(ctx,
		`UPDATE strategies SET hedged_strategy_id = NULL, updated_at = NOW() WHERE id = $1`,
		strategyID,
	); err != nil {
		s.logBotEvent(ctx, botID,
			fmt.Sprintf("Хедж: %s — ошибка переворота (promote): %v", symbol, err),
			"error", "hedge")
		return
	}

	go s.engine.Notify(context.Background(), strategyID)

	s.pool.Exec(ctx, //nolint:errcheck
		`UPDATE hedge_sessions SET ended_at=NOW(), end_reason='deactivation' WHERE hedge_strategy_id=$1 AND ended_at IS NULL`,
		strategyID)
	s.restoreHedgeMainControls(ctx, botID, strategyID)

	s.logBotEvent(ctx, botID,
		fmt.Sprintf("Хедж: %s — нога стала новым мейном (под ботом, без сброса SL/TP): %s", symbol, reason),
		"info", "hedge")

	// Delete the old stopped main (it has zero exchange position — TP already fired).
	// ON DELETE CASCADE removes its cycles, levels and events automatically.
	if oldMainID != "" {
		var acctID string
		if err := s.pool.QueryRow(ctx,
			`DELETE FROM strategies WHERE id=$1 AND status='stopped' RETURNING account_id`,
			oldMainID,
		).Scan(&acctID); err == nil {
			go s.engine.ForceRemoveStrategy(context.Background(), oldMainID, acctID)
			s.logBotEvent(ctx, botID,
				fmt.Sprintf("Хедж: старый мейн %s удалён после переворота", oldMainID[:8]),
				"info", "hedge")
		}
	}

	// Immediately create a new hedge for the promoted (still bot-managed) position.
	// Direction is the opposite of the promoted strategy's side.
	newHedgeDir := "short"
	if hedgePos.Side == "Sell" {
		newHedgeDir = "long"
	}
	var ownerID, acctID string
	var wl, bl []string
	var cfgRaw []byte
	if err := s.pool.QueryRow(ctx,
		`SELECT owner_id, account_id, symbol_whitelist, symbol_blacklist, strategy_config
		 FROM bots WHERE id=$1`, botID,
	).Scan(&ownerID, &acctID, &wl, &bl, &cfgRaw); err == nil {
		var cfg botCfgJSON
		if json.Unmarshal(cfgRaw, &cfg) == nil {
			b := botEngineRow{id: botID, ownerID: ownerID, accountID: acctID, whitelist: wl, blacklist: bl}
			s.cleanupStoppedHedgeCards(ctx, botID, symbol, newHedgeDir)
			if newID, err := s.createBotStrategy(ctx, b, cfg, symbol, newHedgeDir, 0, strategyID, nil); err != nil {
				s.logBotEvent(ctx, botID,
					fmt.Sprintf("Хедж: ошибка создания нового хеджа %s после переворота: %v", symbol, err),
					"error", "hedge")
			} else {
				s.logBotEvent(ctx, botID,
					fmt.Sprintf("Хедж: новый %s хедж создан после переворота %s → %s", newHedgeDir, symbol, newID[:8]),
					"info", "hedge")
			}
		}
	}
}
```

Then, inside `handleMainTpFlip`, replace this block:

```go
	s.releaseHedgeToGrid(ctx, botID, hedgeID, symbol, hedgePosInfo{
		Symbol:     symbol,
		Side:       side,
		Size:       hedgeSize,
		EntryPrice: hedgeEntry,
	}, "переворот: мейн закрылся по TP")
```

with:

```go
	s.promoteHedgeToMain(ctx, botID, hedgeID, symbol, hedgePosInfo{
		Symbol:     symbol,
		Side:       side,
		Size:       hedgeSize,
		EntryPrice: hedgeEntry,
	}, "переворот: мейн закрылся по TP")
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `cd services/api-gateway && go test -tags integration -run 'TestHandleMainTpFlip_PromotesHedgeInPlace_KeepsConfigAndBotId|TestHandleMainTpFlip_ChainedFlips_NeverTwoRowsInSameSlot' -v ./...`

Expected: both tests PASS.

- [ ] **Step 5: Commit**

```bash
git add services/api-gateway/hedge_engine.go services/api-gateway/promote_hedge_to_main_test.go
git commit -m "Promote hedge in place on flip instead of detaching it from the bot

Co-Authored-By: Claude Sonnet 5 <noreply@anthropic.com>"
```

---

## Task 2: Rewire the missed-flip-recovery call site

**Files:**
- Modify: `services/api-gateway/hedge_engine.go`
- Test: `services/api-gateway/promote_hedge_to_main_test.go`

- [ ] **Step 1: Write the failing test**

Append to `services/api-gateway/promote_hedge_to_main_test.go`:

```go
// TestCheckHedgeDeactivation_MissedFlipRecovery_PromotesInPlace covers the second
// flip-related call site: checkHedgeDeactivation's recovery path for a flip that didn't
// fire in time (main already stopped, its last cycle ended with result='tp', but the main
// position is gone from the exchange while the hedge still has filled levels). This must
// also promote the hedge in place, not detach it via releaseHedgeToGrid.
func TestCheckHedgeDeactivation_MissedFlipRecovery_PromotesInPlace(t *testing.T) {
	s := newTestServer(t)
	userID := createWHUser(t, s, "flip3")
	accID := createTestAccount(t, s, userID)
	stubBybitPublicInstrumentInfoOK(t)

	botID, oldMainID, hedgeID := seedFlipFixture(t, s, userID, accID, "FLPCUSDT", "short")
	t.Cleanup(func() {
		s.pool.Exec(context.Background(), "DELETE FROM strategies WHERE symbol='FLPCUSDT' AND bot_id=$1", botID)
	})

	// Simulate the missed flip: old main's cycle already ended via TP.
	if _, err := s.pool.Exec(context.Background(),
		`INSERT INTO strategy_cycles (strategy_id, cycle_num, started_at, ended_at, result)
		 VALUES ($1,1,NOW()-INTERVAL '2 minutes',NOW()-INTERVAL '1 minute','tp')`,
		oldMainID,
	); err != nil {
		t.Fatalf("insert old main closed cycle: %v", err)
	}

	cfg := botCfgJSON{BotKind: "hedge", HedgeTpMainMode: "flip"}
	// No position on the main side (it's gone), but the hedge side is still open on the exchange.
	posMap := map[string]map[string]hedgePosInfo{
		"FLPCUSDT": {
			"Sell": {Symbol: "FLPCUSDT", Side: "Sell", Size: 400, EntryPrice: 0.05, MarkPrice: 0.05, Leverage: 1},
		},
	}

	s.checkHedgeDeactivation(context.Background(), botID, accID, cfg, nil, posMap)

	var botIDAfter string
	var hedgedStratID *string
	if err := s.pool.QueryRow(context.Background(),
		`SELECT COALESCE(bot_id::text,''), hedged_strategy_id::text FROM strategies WHERE id=$1`, hedgeID,
	).Scan(&botIDAfter, &hedgedStratID); err != nil {
		t.Fatalf("read promoted strategy: %v", err)
	}
	if botIDAfter != botID {
		t.Errorf("bot_id = %q, want %q — missed-flip recovery must also keep the leg under the bot", botIDAfter, botID)
	}
	if hedgedStratID != nil {
		t.Errorf("hedged_strategy_id = %v, want NULL", *hedgedStratID)
	}

	var oldMainExists bool
	s.pool.QueryRow(context.Background(), `SELECT EXISTS(SELECT 1 FROM strategies WHERE id=$1)`, oldMainID).Scan(&oldMainExists) //nolint:errcheck
	if oldMainExists {
		t.Error("old main strategy still exists — must be deleted after recovery promotion")
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `cd services/api-gateway && go test -tags integration -run 'TestCheckHedgeDeactivation_MissedFlipRecovery_PromotesInPlace' -v ./...`

Expected: FAIL on `botIDAfter != botID` — the recovery branch still calls `releaseHedgeToGrid`, which clears `bot_id`.

- [ ] **Step 3: Rewire the call site**

In `services/api-gateway/hedge_engine.go`, inside `checkHedgeDeactivation`'s missed-flip-recovery branch, replace:

```go
									s.releaseHedgeToGrid(ctx, botID, h.id, h.symbol, hedgeSidePos,
										"переворот (восстановление): мейн закрылся по TP")
```

with:

```go
									s.promoteHedgeToMain(ctx, botID, h.id, h.symbol, hedgeSidePos,
										"переворот (восстановление): мейн закрылся по TP")
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `cd services/api-gateway && go test -tags integration -run 'TestCheckHedgeDeactivation_MissedFlipRecovery_PromotesInPlace' -v ./...`

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add services/api-gateway/hedge_engine.go services/api-gateway/promote_hedge_to_main_test.go
git commit -m "Apply the same in-place promotion to the missed-flip recovery path

Co-Authored-By: Claude Sonnet 5 <noreply@anthropic.com>"
```

---

## Task 3: Full regression pass

**Files:** none (verification only)

- [ ] **Step 1: Run the full new test file**

Run: `cd services/api-gateway && go test -tags integration -run 'TestHandleMainTpFlip|TestCheckHedgeDeactivation_MissedFlipRecovery' -v ./...`

Expected: all 3 new tests PASS.

- [ ] **Step 2: Run the full `services/api-gateway` integration suite**

Run: `cd services/api-gateway && go test -tags integration ./...`

Expected: no new failures. Compare against a baseline run on the unmodified branch
(`git stash` the two commits, run the same command, note any pre-existing failures, then
`git stash pop`) — only pre-existing failures (unrelated to this change) are acceptable;
anything newly broken (especially `resolve_hedge_slot_conflict_test.go`,
`hedge_session_test.go`, `hedge_session_carryforward_test.go`,
`matrix_hedge_session_sync_test.go`, `paired_close_*_test.go`) must be investigated before
proceeding.

- [ ] **Step 3: Run `go build ./...` and `go vet ./services/api-gateway/...`**

Run: `go build ./... && go vet ./services/api-gateway/...`

Expected: clean, no errors.

- [ ] **Step 4: Report to user**

Per this project's CLAUDE.md test-review requirement: tell the user exactly which test
commands were run, which passed, and explicitly confirm the three scenario call sites
(`handleMainTpFlip`, missed-flip recovery, ordinary deactivation) were each checked —
the first two now promote in place, the third (`releaseHedgeToGrid`, untouched) still
detaches as before, matching the approved spec's scope.

Do not commit anything in this task — it's verification only, no files change.
