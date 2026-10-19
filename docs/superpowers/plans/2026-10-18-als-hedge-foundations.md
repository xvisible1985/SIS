# ALS Hedge Strategy — Foundations Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make `strategy_type = 'als'` a safe, recognized third strategy type across the
whole `pkg/strategy` engine — it loads from DB, starts/stops/restarts cleanly, and is
correctly excluded from every matrix-specific code path that currently assumes "not
matrix" means "grid" — with **zero actual ALS trading logic yet** (that's
`2026-10-18-als-hedge-engine.md`, the next plan). This plan's sole job is to make the
scaffolding safe so the real engine can be built on top of it without risk to the
existing, live Matrix/Grid strategies.

**Architecture:** New DB table + columns (migration), new `Strategy`/`Cycle` struct
fields (`pkg/strategy/types.go`), the two existing SELECT queries + shared `scanStrategy`
scanner extended (`pkg/strategy/engine.go`), and every one of the 14 `StrategyType ==
"matrix"` (or `!= "matrix"`) dispatch sites in `pkg/strategy/cycle.go` updated to
explicitly handle `"als"` — some route to new no-op placeholder functions in a new
`pkg/strategy/als.go` file (fleshed out in the next plan), others are explicitly excluded
alongside matrix (they manage per-level stops/TP differently and must not run matrix's or
grid's shared logic).

**Tech Stack:** Go, PostgreSQL (via `pgxpool`), the existing `pkg/strategy` `StrategyRunner`
architecture.

**Related docs:**
- Design spec: `docs/superpowers/specs/2026-10-18-als-hedge-strategy-design.md`
- Split precedent: `docs/superpowers/plans/` large-plan 32k-token-limit convention (same
  reason this feature's plan is split into multiple files)
- Next plan (not yet written): `docs/superpowers/plans/2026-10-18-als-hedge-engine.md` —
  the actual threshold evaluation / sizing / memory-bank / TP pause-resume logic.
- Plan after that (not yet written):
  `docs/superpowers/plans/2026-10-18-als-hedge-api-frontend.md` — `services/api-gateway`
  config persistence + `HedgeBotForm.tsx` UI.

---

### Task 1: Migration — new table and columns

**Files:**
- Create: `migrations/098_als_hedge_strategy.sql`

- [ ] **Step 1: Write the migration**

```sql
-- ALS (Anti-Liquidation System) hedge strategy — foundational schema.
-- See docs/superpowers/specs/2026-10-18-als-hedge-strategy-design.md for the full design.

ALTER TABLE strategies
    ADD COLUMN als_thresholds JSONB,
    ADD COLUMN unsafe_hedge_min_pct NUMERIC(10,4),
    ADD COLUMN unsafe_hedge_max_pct NUMERIC(10,4),
    ADD COLUMN stop_market_size_k NUMERIC(10,4),
    ADD COLUMN tp_control_enabled BOOLEAN NOT NULL DEFAULT false,
    ADD COLUMN initial_stop_be_pct NUMERIC(10,4);

-- No new strategy_cycles columns: TP pause/resume (see
-- 2026-10-18-als-hedge-polish.md) reuses the EXISTING hedge_tp_suppressed column on
-- strategies and Engine.Notify — the exact mechanism services/api-gateway/hedge_engine.go
-- already uses for the Matrix hedge bot, which always recomputes the TP fresh from the
-- main's current avg entry/tp_pct once un-suppressed rather than restoring a saved
-- snapshot. A design-spec revision from the original "save/restore paused_tp_price" plan,
-- found during implementation planning — reuses more of the existing, already-proven
-- code path instead of adding new columns that would duplicate it.

CREATE TABLE als_stop_memory (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    strategy_id   UUID NOT NULL REFERENCES strategies(id) ON DELETE CASCADE,
    level_id      UUID NOT NULL REFERENCES strategy_levels(id) ON DELETE CASCADE,
    symbol        TEXT NOT NULL,
    side          TEXT NOT NULL,
    trigger_price NUMERIC(18,8) NOT NULL,
    qty           TEXT NOT NULL,
    evicted_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX idx_als_stop_memory_strategy ON als_stop_memory (strategy_id);
```

- [ ] **Step 2: Check the project's migration-running convention**

Run: `grep -n "func.*runMigrations\|migrate\b" services/api-gateway/main.go | head -5`

This project uses a `migrate.exe` binary (seen in the repo root during earlier session
work) — do NOT run it yourself per the project's standing rule that the user runs/builds
binaries. Instead, after writing the file, tell the user a new migration
(`098_als_hedge_strategy.sql`) needs to be applied, the same way every other migration in
this session was handled — by them, via their own `migrate` invocation.

- [ ] **Step 3: Commit**

```bash
git add migrations/098_als_hedge_strategy.sql
git commit -m "Add ALS hedge strategy schema: als_stop_memory table, strategies/strategy_cycles columns"
```

---

### Task 2: `Strategy` struct — new fields and `AlsThreshold` type

**Files:**
- Modify: `pkg/strategy/types.go`

- [ ] **Step 1: Add the `AlsThreshold` type**

Add this type definition right after the existing `MatrixEntryLevel` struct (after line 88,
before `type Strategy struct {` at line 90):

```go
// AlsThreshold is one row of an ALS strategy's "Пороги" (thresholds) table — see
// docs/superpowers/specs/2026-10-18-als-hedge-strategy-design.md. TriggerType mirrors
// the existing hedge-activation act_type convention (0 = % from the last filled order,
// 1 = % from the main position's average entry price) rather than the source ALS's own
// ROI-based units — a deliberate adaptation, not an oversight.
type AlsThreshold struct {
	TriggerType    int     `json:"trigger_type"`    // 0 = from last order, 1 = from avg entry (ТВХ)
	TriggerPct     float64 `json:"trigger_pct"`      // negative = adverse move, matches act_value convention
	TargetHedgePct float64 `json:"target_hedge_pct"` // cumulative target hedge volume, % of main's CURRENT live volume
}
```

- [ ] **Step 2: Add the new `Strategy` fields**

In the `Strategy` struct, right after the existing `SizeAsMain bool` field (line 129),
add:

```go
	// ALS (Anti-Liquidation System) hedge strategy fields — meaningful only when
	// StrategyType == "als". See docs/superpowers/specs/2026-10-18-als-hedge-strategy-design.md.
	AlsThresholds     []AlsThreshold
	UnsafeHedgeMinPct float64
	UnsafeHedgeMaxPct float64
	StopMarketSizeK   float64
	TPControlEnabled  bool
	InitialStopBEPct  *float64
```

- [ ] **Step 3: No new `Cycle` fields needed**

TP pause/resume (`2026-10-18-als-hedge-polish.md`) reuses the existing
`Strategy.HedgeTpSuppressed` field (already in this struct, added for the Matrix hedge
bot) rather than a saved price/qty snapshot — skip this step, it's a no-op now. Kept here
as an explicit step (not silently removed) so anyone re-reading this plan after Task 1's
migration edit understands why `Cycle` gets no new fields in this plan, unlike `Strategy`.

- [ ] **Step 4: Build to confirm it compiles**

Run: `go build ./pkg/strategy/...`
Expected: no output (clean build) — these are additive struct fields, nothing references
them yet.

- [ ] **Step 5: Commit**

```bash
git add pkg/strategy/types.go
git commit -m "Add ALS fields to Strategy/Cycle structs"
```

---

### Task 3: Wire new columns into `engine.go`'s load queries

**Files:**
- Modify: `pkg/strategy/engine.go:58-113` (`Engine.Start`'s SELECT)
- Modify: `pkg/strategy/engine.go:167-200ish` (`Engine.Notify`'s SELECT — find via the
  second occurrence of the same column list)
- Modify: `pkg/strategy/engine.go:749-822` (`scanStrategy`)

- [ ] **Step 1: Write a failing test**

Create `pkg/strategy/als_scan_test.go`:

```go
//go:build integration

package strategy

import (
	"context"
	"testing"
)

// TestScanStrategy_ReadsAlsFields is the regression test for wiring the new ALS columns
// into scanStrategy — without it, every ALS field silently stays at its Go zero value
// regardless of what's actually stored in the DB.
func TestScanStrategy_ReadsAlsFields(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()

	var ownerID, accID, stratID string
	if err := pool.QueryRow(ctx, `INSERT INTO users (email, password_hash) VALUES ($1,'x') RETURNING id`,
		"als-scan-"+t.Name()+"@example.com").Scan(&ownerID); err != nil {
		t.Fatalf("create user: %v", err)
	}
	t.Cleanup(func() { pool.Exec(ctx, "DELETE FROM users WHERE id=$1", ownerID) })
	if err := pool.QueryRow(ctx, `INSERT INTO exchange_accounts (owner_id, exchange, label, api_key_enc, secret_enc) VALUES ($1,'binance','x','','') RETURNING id`,
		ownerID).Scan(&accID); err != nil {
		t.Fatalf("create account: %v", err)
	}
	t.Cleanup(func() { pool.Exec(ctx, "DELETE FROM exchange_accounts WHERE id=$1", accID) })

	thresholdsJSON := `[{"trigger_type":1,"trigger_pct":-10,"target_hedge_pct":30},{"trigger_type":1,"trigger_pct":-30,"target_hedge_pct":50}]`
	if err := pool.QueryRow(ctx, `
		INSERT INTO strategies (owner_id, account_id, symbol, direction, strategy_type,
		  als_thresholds, unsafe_hedge_min_pct, unsafe_hedge_max_pct, stop_market_size_k,
		  tp_control_enabled, initial_stop_be_pct)
		VALUES ($1,$2,'ALSUSDT','long','als',$3::jsonb,5,40,1.2,true,2.5)
		RETURNING id`,
		ownerID, accID, thresholdsJSON,
	).Scan(&stratID); err != nil {
		t.Fatalf("create als strategy: %v", err)
	}
	t.Cleanup(func() { pool.Exec(ctx, "DELETE FROM strategies WHERE id=$1", stratID) })

	var s Strategy
	row := pool.QueryRow(ctx, `
		SELECT id, owner_id, account_id, symbol, category, direction, status,
		       grid_levels, grid_active, COALESCE(max_stop_active,0), grid_step_pct, grid_size_usdt,
		       tp_mode, tp_pct, sl_type, sl_pct, signal_filter, hedge_mode,
		       leverage, COALESCE(margin_type,'isolated'),
		       entry_order_type, COALESCE(steps::text,'[]'),
		       COALESCE(signal_configs::text,'[]'),
		       cycle_count, max_cycles, bot_id, COALESCE(matrix_levels::text,''), COALESCE(safe_zone_pct,0), COALESCE(matrix_entry_level::text,''),
		       COALESCE(protected_build,false),
		       COALESCE(matrix_rebuild_on_sl,false),
		       COALESCE(matrix_rebuild_from_entry,false),
		       COALESCE(relative_slots,false),
		       COALESCE(strategy_type,'grid'),
		       COALESCE(size_as_main,false),
		       COALESCE(hedge_tp_suppressed,false),
		       COALESCE(hedge_sl_suppressed,false),
		       hedge_stopped_by::text,
		       COALESCE(adopt_position_data::text,''),
		       tp_signal_dir,
		       sl_signal_dir,
		       tp_signal_configs::text,
		       sl_signal_configs::text,
		       COALESCE(als_thresholds::text,'[]'),
		       COALESCE(unsafe_hedge_min_pct,0),
		       COALESCE(unsafe_hedge_max_pct,0),
		       COALESCE(stop_market_size_k,0),
		       COALESCE(tp_control_enabled,false),
		       initial_stop_be_pct
		FROM strategies WHERE id=$1`, stratID)
	if err := scanStrategyRow(row, &s); err != nil {
		t.Fatalf("scanStrategyRow: %v", err)
	}

	if len(s.AlsThresholds) != 2 {
		t.Fatalf("AlsThresholds = %d rows, want 2", len(s.AlsThresholds))
	}
	if s.AlsThresholds[0].TriggerType != 1 || s.AlsThresholds[0].TriggerPct != -10 || s.AlsThresholds[0].TargetHedgePct != 30 {
		t.Errorf("AlsThresholds[0] = %+v, want {1 -10 30}", s.AlsThresholds[0])
	}
	if s.UnsafeHedgeMinPct != 5 || s.UnsafeHedgeMaxPct != 40 {
		t.Errorf("UnsafeHedgeMinPct/MaxPct = %v/%v, want 5/40", s.UnsafeHedgeMinPct, s.UnsafeHedgeMaxPct)
	}
	if s.StopMarketSizeK != 1.2 {
		t.Errorf("StopMarketSizeK = %v, want 1.2", s.StopMarketSizeK)
	}
	if !s.TPControlEnabled {
		t.Error("TPControlEnabled = false, want true")
	}
	if s.InitialStopBEPct == nil || *s.InitialStopBEPct != 2.5 {
		t.Errorf("InitialStopBEPct = %v, want 2.5", s.InitialStopBEPct)
	}
}
```

- [ ] **Step 2: Run it to confirm it fails**

Run: `go test ./pkg/strategy/... -tags integration -run TestScanStrategy_ReadsAlsFields -v`
Expected: FAIL — `als_thresholds` etc. are not yet selected/scanned (compile error on the
unknown columns doesn't apply here since this test builds its own ad-hoc query string, so
the actual failure is a Postgres "column does not exist" error if Task 1's migration
hasn't been applied yet by the user, OR a scan-arg-count mismatch against the as-yet
unmodified `scanStrategyRow`/`scanStrategy`).

**Before running this test, confirm with the user that migration `098_als_hedge_strategy.sql`
has been applied** — this test depends on the new columns existing.

- [ ] **Step 3: Extend `scanStrategy` to read the new columns**

In `pkg/strategy/engine.go`, modify `scanStrategy` (currently lines 749-822):

Find:
```go
func scanStrategy(rows interface{ Scan(...any) error }, s *Strategy) error {
	var dir, stat, tpm, slt, stepsJSON, signalConfigsJSON, matrixJSON, entryLevelJSON, adoptJSON string
	var sf, hm bool
	var stoppedByStr *string
	var tpSignalDir, slSignalDir *string
	var tpSigCfgJSON, slSigCfgJSON *string
	err := rows.Scan(
		&s.ID, &s.OwnerID, &s.AccountID, &s.Symbol, &s.Category,
		&dir, &stat,
		&s.GridLevels, &s.GridActive, &s.MaxStopActive, &s.GridStepPct, &s.GridSizeUSDT,
		&tpm, &s.TPPct, &slt, &s.SLPct, &sf, &hm,
		&s.Leverage, &s.MarginType,
		&s.EntryOrderType, &stepsJSON, &signalConfigsJSON,
		&s.CycleCount, &s.MaxCycles, &s.BotID, &matrixJSON, &s.SafeZonePct, &entryLevelJSON,
		&s.ProtectedBuild,
		&s.RebuildOnSL,
		&s.RebuildFromEntry,
		&s.RelativeSlots,
		&s.StrategyType,
		&s.SizeAsMain,
		&s.HedgeTpSuppressed,
		&s.HedgeSlSuppressed,
		&stoppedByStr,
		&adoptJSON,
		&tpSignalDir,
		&slSignalDir,
		&tpSigCfgJSON,
		&slSigCfgJSON,
	)
	if err != nil {
		return err
	}
```

Replace with:
```go
func scanStrategy(rows interface{ Scan(...any) error }, s *Strategy) error {
	var dir, stat, tpm, slt, stepsJSON, signalConfigsJSON, matrixJSON, entryLevelJSON, adoptJSON string
	var sf, hm bool
	var stoppedByStr *string
	var tpSignalDir, slSignalDir *string
	var tpSigCfgJSON, slSigCfgJSON *string
	var alsThresholdsJSON string
	err := rows.Scan(
		&s.ID, &s.OwnerID, &s.AccountID, &s.Symbol, &s.Category,
		&dir, &stat,
		&s.GridLevels, &s.GridActive, &s.MaxStopActive, &s.GridStepPct, &s.GridSizeUSDT,
		&tpm, &s.TPPct, &slt, &s.SLPct, &sf, &hm,
		&s.Leverage, &s.MarginType,
		&s.EntryOrderType, &stepsJSON, &signalConfigsJSON,
		&s.CycleCount, &s.MaxCycles, &s.BotID, &matrixJSON, &s.SafeZonePct, &entryLevelJSON,
		&s.ProtectedBuild,
		&s.RebuildOnSL,
		&s.RebuildFromEntry,
		&s.RelativeSlots,
		&s.StrategyType,
		&s.SizeAsMain,
		&s.HedgeTpSuppressed,
		&s.HedgeSlSuppressed,
		&stoppedByStr,
		&adoptJSON,
		&tpSignalDir,
		&slSignalDir,
		&tpSigCfgJSON,
		&slSigCfgJSON,
		&alsThresholdsJSON,
		&s.UnsafeHedgeMinPct,
		&s.UnsafeHedgeMaxPct,
		&s.StopMarketSizeK,
		&s.TPControlEnabled,
		&s.InitialStopBEPct,
	)
	if err != nil {
		return err
	}
```

Then, right after the existing block that unmarshals `matrixJSON` into `s.MatrixLevels`
(find `if matrixJSON != "" && matrixJSON != "[]" && matrixJSON != "null" {` — the block
ending a few lines later), add:

```go
	if alsThresholdsJSON != "" && alsThresholdsJSON != "[]" && alsThresholdsJSON != "null" {
		_ = json.Unmarshal([]byte(alsThresholdsJSON), &s.AlsThresholds)
	}
```

- [ ] **Step 4: Add the new columns to both SELECT queries**

In `pkg/strategy/engine.go`, `Engine.Start` (the query starting around line 59-87):

Find the last three lines of the SELECT's column list:
```go
		        tp_signal_dir,
		        sl_signal_dir,
		        tp_signal_configs::text,
		        sl_signal_configs::text
		 FROM strategies
```

Replace with:
```go
		        tp_signal_dir,
		        sl_signal_dir,
		        tp_signal_configs::text,
		        sl_signal_configs::text,
		        COALESCE(als_thresholds::text,'[]'),
		        COALESCE(unsafe_hedge_min_pct,0),
		        COALESCE(unsafe_hedge_max_pct,0),
		        COALESCE(stop_market_size_k,0),
		        COALESCE(tp_control_enabled,false),
		        initial_stop_be_pct
		 FROM strategies
```

This exact same `tp_signal_dir, sl_signal_dir, tp_signal_configs::text, sl_signal_configs::text`
tail also appears in `Engine.Notify`'s query (around line 188). Apply the identical
replacement there — use the same find/replace; there are exactly 2 occurrences of this
4-line tail in the file (verify with `grep -n "sl_signal_configs::text$" pkg/strategy/engine.go`
before editing — it must report exactly 2 matches, both immediately followed by
`FROM strategies`, before you touch anything).

- [ ] **Step 5: Run the test again to confirm it passes**

Run: `go test ./pkg/strategy/... -tags integration -run TestScanStrategy_ReadsAlsFields -v`
Expected: `PASS`

- [ ] **Step 6: Run the full pkg/strategy integration suite for regressions**

Run: `go test ./pkg/strategy/... -tags integration 2>&1 | grep -- "--- FAIL"`
Expected: exactly the 2 known baseline failures
(`TestResolveSignalConfigs_ExpandsCustomSignalIntoComponents`,
`TestResolveSignalConfigs_MixedCatalogAndCustom`), nothing new. If anything else appears,
stop and investigate before continuing — it means the SELECT column list edit broke
column-position alignment for an existing field.

- [ ] **Step 7: Commit**

```bash
git add pkg/strategy/engine.go pkg/strategy/als_scan_test.go
git commit -m "Wire ALS columns into engine.go's strategy load/scan"
```

---

### Task 4: `pkg/strategy/als.go` — no-op placeholders so dispatchers compile

**Files:**
- Create: `pkg/strategy/als.go`

- [ ] **Step 1: Create the file with stub functions**

These exist purely so Task 5's dispatcher edits have something real to call. Each is
fleshed out for real in `2026-10-18-als-hedge-engine.md` (the next plan) — for now they
only need to keep an ALS strategy from crashing or silently doing nothing wrong.

```go
package strategy

import "context"

// startAlsCycle starts a brand-new ALS hedge cycle. Stub — see
// docs/superpowers/plans/2026-10-18-als-hedge-engine.md for the real implementation
// (will create the strategy_cycles row and seed strategy_levels from AlsThresholds).
// Must be called with startMu held and WITHOUT sr.mu held — mirrors startCycle/startMatrixCycle.
func (sr *StrategyRunner) startAlsCycle(ctx context.Context) error {
	return nil
}

// resumeAlsCycle resumes an ALS cycle that was already running (e.g. after a process
// restart). Stub — see the next plan. Must be called WITHOUT sr.mu held.
func (sr *StrategyRunner) resumeAlsCycle(ctx context.Context) {
}

// restartAlsCycle re-applies strategy config to an already-running ALS cycle (settings
// changed mid-flight). Stub — see the next plan. Must be called WITHOUT sr.mu held.
func (sr *StrategyRunner) restartAlsCycle(ctx context.Context) {
}

// resumeAlsAfterSignal resumes an ALS cycle that was waiting on an activation signal.
// Stub — see the next plan. Must be called with startMu held and WITHOUT sr.mu held.
func (sr *StrategyRunner) resumeAlsAfterSignal(ctx context.Context) {
}

// handleAlsLevelFill reacts to one ALS threshold level's order filling. Stub — see the
// next plan (will update strategy_levels status and place the level's breakeven stop).
// Must be called with sr.mu held.
func (sr *StrategyRunner) handleAlsLevelFill(ctx context.Context, levelID string, filledPrice float64) {
}

// alsCancelPerLevelSLs cancels every stop-order placed for this ALS strategy's filled
// levels (mirrors matrixCancelPerLevelSLs) — called on stop/restart. Stub — see the next
// plan. Must be called with sr.mu held.
func (sr *StrategyRunner) alsCancelPerLevelSLs(ctx context.Context) {
}
```

- [ ] **Step 2: Build to confirm it compiles**

Run: `go build ./pkg/strategy/...`
Expected: no output.

- [ ] **Step 3: Commit**

```bash
git add pkg/strategy/als.go
git commit -m "Add als.go stub functions for the dispatcher wiring"
```

---

### Task 5: Update the "ByType" dispatcher functions to handle `"als"`

**Files:**
- Modify: `pkg/strategy/cycle.go` (6 sites — the ones that already follow a clean
  `if == "matrix" {...} else {...}` or early-return pattern)

These are the lowest-risk sites: converting a 2-way branch into a 3-way `switch` changes
nothing about the existing `"matrix"` and default/grid branches — they stay byte-for-byte
identical.

- [ ] **Step 1: `startCycleByType` (currently around line 3271-3277)**

Find:
```go
func (sr *StrategyRunner) startCycleByType(ctx context.Context) error {
	if sr.strategy.StrategyType == "matrix" {
		return sr.startMatrixCycle(ctx)
	}
	return sr.startCycle(ctx)
}
```

Replace with:
```go
func (sr *StrategyRunner) startCycleByType(ctx context.Context) error {
	switch sr.strategy.StrategyType {
	case "matrix":
		return sr.startMatrixCycle(ctx)
	case "als":
		return sr.startAlsCycle(ctx)
	default:
		return sr.startCycle(ctx)
	}
}
```

- [ ] **Step 2: `resumeCycleAfterSignalByType` (currently around line 3280-3286)**

Find:
```go
func (sr *StrategyRunner) resumeCycleAfterSignalByType(ctx context.Context) {
	if sr.strategy.StrategyType == "matrix" {
		sr.resumeMatrixAfterSignal(ctx)
	} else {
		sr.resumeGridAfterSignal(ctx)
	}
}
```

Replace with:
```go
func (sr *StrategyRunner) resumeCycleAfterSignalByType(ctx context.Context) {
	switch sr.strategy.StrategyType {
	case "matrix":
		sr.resumeMatrixAfterSignal(ctx)
	case "als":
		sr.resumeAlsAfterSignal(ctx)
	default:
		sr.resumeGridAfterSignal(ctx)
	}
}
```

- [ ] **Step 3: `resumeCycle`'s matrix/grid branch (currently around line 346)**

Find (the enclosing function body around line 343-349):
```go
	if sr.strategy.StrategyType == "matrix" {
		sr.resumeMatrixCycle(ctx)
	} else {
		sr.resumeGridCycle(ctx)
	}
}
```

Replace with:
```go
	switch sr.strategy.StrategyType {
	case "matrix":
		sr.resumeMatrixCycle(ctx)
	case "als":
		sr.resumeAlsCycle(ctx)
	default:
		sr.resumeGridCycle(ctx)
	}
}
```

- [ ] **Step 4: `restartCycle`'s matrix/grid branch (currently around line 4083-4089)**

Find:
```go
	sr.startMu.Lock()
	defer sr.startMu.Unlock()
	sr.clearManualAlert(ctx)
	if sr.strategy.StrategyType == "matrix" {
		sr.restartMatrixCycle(ctx)
	} else {
		sr.restartGridCycle(ctx)
	}
}
```

Replace with:
```go
	sr.startMu.Lock()
	defer sr.startMu.Unlock()
	sr.clearManualAlert(ctx)
	switch sr.strategy.StrategyType {
	case "matrix":
		sr.restartMatrixCycle(ctx)
	case "als":
		sr.restartAlsCycle(ctx)
	default:
		sr.restartGridCycle(ctx)
	}
}
```

- [ ] **Step 5: `updateTPByType` (currently around line 2702-2709)**

ALS hedge legs don't carry their own TP — exit is via the breakeven stop or the shared
pair-close ("Завершение" tab), exactly like a Matrix hedge leg typically doesn't rely on
its own TP either. No-op for `"als"`.

Find:
```go
func (sr *StrategyRunner) updateTPByType(ctx context.Context) error {
	if sr.strategy.StrategyType == "matrix" {
		sr.matrixUpdateTP(ctx)
		return nil
	}
	return sr.updateTP(ctx)
}
```

Replace with:
```go
func (sr *StrategyRunner) updateTPByType(ctx context.Context) error {
	switch sr.strategy.StrategyType {
	case "matrix":
		sr.matrixUpdateTP(ctx)
		return nil
	case "als":
		return nil
	default:
		return sr.updateTP(ctx)
	}
}
```

- [ ] **Step 6: `updateSL`'s matrix early-return (currently around line 2744-2748)**

ALS manages its own per-level stops (built in the next plan), not the shared single-SL
grid mechanism — same reasoning as matrix's existing early-return.

Find:
```go
func (sr *StrategyRunner) updateSL(ctx context.Context) error {
	defer sr.setOp("update-sl")()
	if sr.strategy.StrategyType == "matrix" {
		return nil // Matrix manages SL per-level via matrixPlacePerLevelSL
	}
```

Replace with:
```go
func (sr *StrategyRunner) updateSL(ctx context.Context) error {
	defer sr.setOp("update-sl")()
	if sr.strategy.StrategyType == "matrix" || sr.strategy.StrategyType == "als" {
		return nil // Matrix/ALS manage SL per-level via their own mechanisms
	}
```

- [ ] **Step 7: Build and run the unit test suite**

Run: `go build ./pkg/strategy/... && go test ./pkg/strategy/...`
Expected: clean build, all unit tests pass (no new failures — these are pure dispatch
changes, matrix/grid behavior is untouched).

- [ ] **Step 8: Commit**

```bash
git add pkg/strategy/cycle.go
git commit -m "Route ALS strategies through their own cycle lifecycle dispatchers"
```

---

### Task 6: Exclude `"als"` from matrix-specific cleanup/reconcile sites

**Files:**
- Modify: `pkg/strategy/cycle.go` (remaining sites — these ones manage matrix-specific
  resources, like per-level SL orders and the matrix price monitor goroutine, that ALS
  does not use in the same shape)

- [ ] **Step 1: Orphaned SL cleanup — exclude `"als"` too (currently around line 1030)**

ALS places its own per-level stops with its own linkId scheme (built in the next plan) —
this grid-only cleanup (which assumes a single `SIS_STR-{id8}-sl-` prefix) must not run
for ALS, same as it already doesn't for matrix.

Find:
```go
	// --- Orphaned SL cleanup (grid only) ---
	// If updateSL's CancelOrder failed, the old stop order may still be on the exchange
	// alongside the new one. Detect any stop orders whose linkId matches our SL prefix
	// but doesn't match the current slOrderID and schedule them for cancellation.
	var orphanedSLs []trader.Order
	if sr.strategy.StrategyType != "matrix" {
```

Replace with:
```go
	// --- Orphaned SL cleanup (grid only) ---
	// If updateSL's CancelOrder failed, the old stop order may still be on the exchange
	// alongside the new one. Detect any stop orders whose linkId matches our SL prefix
	// but doesn't match the current slOrderID and schedule them for cancellation.
	var orphanedSLs []trader.Order
	if sr.strategy.StrategyType != "matrix" && sr.strategy.StrategyType != "als" {
```

- [ ] **Step 2a: Per-level SL cancellation site #1 — `closeCycle` (currently around
  line 3149-3157)**

Find:
```go
	// Cancel per-level matrix SL orders and stop price monitor (noop for grid strategies)
	if sr.strategy.StrategyType == "matrix" {
		sr.matrixCancelPerLevelSLs(ctx)
		if sr.matrixMonitorStop != nil {
			sr.matrixMonitorStop()
			sr.matrixMonitorStop = nil
		}
		sr.matrixWaitingSlots = make(map[int]float64)
	}
	sr.cycle = nil
	sr.levels = nil
	sr.partialCloseQty = 0
```

Replace with:
```go
	// Cancel per-level matrix SL orders and stop price monitor (noop for grid strategies)
	if sr.strategy.StrategyType == "matrix" {
		sr.matrixCancelPerLevelSLs(ctx)
		if sr.matrixMonitorStop != nil {
			sr.matrixMonitorStop()
			sr.matrixMonitorStop = nil
		}
		sr.matrixWaitingSlots = make(map[int]float64)
	}
	if sr.strategy.StrategyType == "als" {
		sr.alsCancelPerLevelSLs(ctx)
	}
	sr.cycle = nil
	sr.levels = nil
	sr.partialCloseQty = 0
```

- [ ] **Step 2b: Per-level SL cancellation site #2 — the stop handler (currently around
  line 4018-4030)**

Find:
```go
	if sr.cycle == nil {
		sr.info(ctx, "Стратегия остановлена (нет активного цикла)")
		return
	}
	if sr.strategy.StrategyType == "matrix" {
		sr.matrixCancelPerLevelSLs(ctx)
		if sr.matrixMonitorStop != nil {
			sr.matrixMonitorStop()
			sr.matrixMonitorStop = nil
		}
		sr.matrixWaitingSlots = make(map[int]float64)
	}
	sr.cancelPlacedLevels(ctx)
```

Replace with:
```go
	if sr.cycle == nil {
		sr.info(ctx, "Стратегия остановлена (нет активного цикла)")
		return
	}
	if sr.strategy.StrategyType == "matrix" {
		sr.matrixCancelPerLevelSLs(ctx)
		if sr.matrixMonitorStop != nil {
			sr.matrixMonitorStop()
			sr.matrixMonitorStop = nil
		}
		sr.matrixWaitingSlots = make(map[int]float64)
	}
	if sr.strategy.StrategyType == "als" {
		sr.alsCancelPerLevelSLs(ctx)
	}
	sr.cancelPlacedLevels(ctx)
```

- [ ] **Step 2c: Keep-cycle-alive-if-no-position branch — leave matrix-only (same
  function as Step 2b, currently around line 4041-4049)**

Immediately below Step 2b's edit, this function also has:
```go
	if !hasPosition {
		// For matrix strategy: keep the cycle alive so it can be resumed exactly
		// as-is (with its levels) when the strategy is re-activated.  Closing the
		// cycle here would cause loadOrStart to start a *new* cycle on restart.
		if sr.strategy.StrategyType == "matrix" {
			sr.info(ctx, "Стратегия остановлена — нет открытой позиции, матричный цикл сохранён")
			return
		}
		sr.closeCycle(ctx, "stopped")
```

**No change here** — ALS, like grid, should fall through to `sr.closeCycle(ctx, "stopped")`
when stopped with no open position (a fresh cycle starts next time, matching grid's
existing behavior rather than matrix's resume-in-place behavior) — this is the
foundations plan's intended default and needs no edit. Verification step only: read this
block and confirm `sr.strategy.StrategyType == "matrix"` is unchanged (ALS correctly
falls through to the `default` path below it).

- [ ] **Step 3: Batch cancel OrderFilter tagging — exclude, don't extend (currently
  around line 3212)**

This one is matrix-specific SL-order-cancellation batching logic keyed on `l.Slot != nil`
(a matrix-only `GridLevel` field). ALS levels also use `strategy_levels` rows but won't
set `Slot` (that's a matrix concept — ALS's own level identity is its `LevelIdx`, no slot
numbering). **No change needed here** — `l.Slot != nil` is naturally `false` for ALS
levels, so this code already correctly skips them. Confirm this assumption by reading
`pkg/strategy/cycle.go` around line 3212 and verifying `l.Slot` is read from `GridLevel`,
not set anywhere in the (still stub) ALS code added in Task 4 — it isn't, so this is
correct as-is. No edit — just a verification step. Mark this step done once confirmed.

- [ ] **Step 4: `handleLevelFill`'s matrix branch — add `"als"` branch (currently around
  line 2372-2378)**

Find:
```go
	// Matrix strategies use their own fill handler.
	if sr.strategy.StrategyType == "matrix" {
		sr.handleMatrixLevelFill(ctx, levelID, filledPrice)
		return
	}
```

Replace with:
```go
	// Matrix/ALS strategies use their own fill handlers.
	if sr.strategy.StrategyType == "matrix" {
		sr.handleMatrixLevelFill(ctx, levelID, filledPrice)
		return
	}
	if sr.strategy.StrategyType == "als" {
		sr.handleAlsLevelFill(ctx, levelID, filledPrice)
		return
	}
```

- [ ] **Step 5: `manualCloseStatus` — leave matrix-only (currently around line 4643)**

Read the function and its doc comment (lines ~4641-4649) before deciding. It returns
`StatusPaused` only for matrix bot-owned strategies on manual close (so the bot doesn't
recreate it, preserving matrix's own levels for later resume), `StatusStopped` for
everything else. **No change needed** — ALS, like grid, should go to `StatusStopped` on a
manual close (its bot-engine-driven re-creation path, built in a later plan, starts a
fresh cycle rather than resuming levels in place, matching grid's existing behavior more
than matrix's). No edit — verification step only, mark done once confirmed by reading.

- [ ] **Step 6: `handlePositionCloseRetry` gate — leave matrix-only (currently around
  line 4549-4556)**

Read the function and context before deciding. This defers to `handleMatrixSLFill`
specifically when a filled matrix level still has an open `SLOrderID` — a matrix-only
per-level-SL detail. ALS's own per-level SL handling (built in the next plan, inside
`handleAlsLevelFill`/a future `handleAlsLevelSLFill`) will need its own equivalent
retry-safety check, but that belongs in the ALS engine plan, not here — this foundations
plan only needs ALS to not crash or misbehave by falling into matrix's branch, and it
currently doesn't (the `if` here is `StrategyType == "matrix"` only, so ALS correctly
falls through to the generic path below it). **No change needed** — verification step
only, mark done once confirmed by reading.

- [ ] **Step 7: Build and run the full regression suite**

Run: `go build ./... && go vet ./...`
Expected: clean.

Run: `go test ./pkg/strategy/... -tags integration 2>&1 | grep -- "--- FAIL"`
Expected: exactly the 2 known baseline failures, nothing new.

- [ ] **Step 8: Commit**

```bash
git add pkg/strategy/cycle.go
git commit -m "Exclude ALS strategies from matrix-only cleanup/reconcile paths"
```

---

### Task 7: End-to-end foundations test — an ALS strategy loads, starts, and stops cleanly

**Files:**
- Create: `pkg/strategy/als_lifecycle_test.go`

- [ ] **Step 1: Write the failing test**

This is the task that actually proves the foundations work: create an `als`-typed
strategy row directly in the DB (bypassing any HTTP handler, since those aren't wired
until the API/frontend plan), load it through the real `Engine.Start` path, and confirm
it doesn't panic and ends up with a running `StrategyRunner` whose `StrategyType` is
`"als"`.

```go
//go:build integration

package strategy

import (
	"context"
	"testing"
	"time"
)

// TestEngineStart_LoadsAlsStrategyWithoutPanicking is the foundations plan's acceptance
// test: an 'als' strategy_type row must load through Engine.Start exactly like grid/matrix
// do today — no panic, no silent misrouting into matrix's or grid's own cycle logic.
func TestEngineStart_LoadsAlsStrategyWithoutPanicking(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()

	var ownerID, accID, stratID string
	if err := pool.QueryRow(ctx, `INSERT INTO users (email, password_hash) VALUES ($1,'x') RETURNING id`,
		"als-lifecycle-"+t.Name()+"@example.com").Scan(&ownerID); err != nil {
		t.Fatalf("create user: %v", err)
	}
	t.Cleanup(func() { pool.Exec(ctx, "DELETE FROM users WHERE id=$1", ownerID) })
	if err := pool.QueryRow(ctx, `INSERT INTO exchange_accounts (owner_id, exchange, label, api_key_enc, secret_enc) VALUES ($1,'binance','x','','') RETURNING id`,
		ownerID).Scan(&accID); err != nil {
		t.Fatalf("create account: %v", err)
	}
	t.Cleanup(func() { pool.Exec(ctx, "DELETE FROM exchange_accounts WHERE id=$1", accID) })
	if err := pool.QueryRow(ctx, `
		INSERT INTO strategies (owner_id, account_id, symbol, direction, strategy_type, status)
		VALUES ($1,$2,'ALSLIFEUSDT','long','als','stopped') RETURNING id`,
		ownerID, accID,
	).Scan(&stratID); err != nil {
		t.Fatalf("create als strategy: %v", err)
	}
	t.Cleanup(func() { pool.Exec(ctx, "DELETE FROM strategies WHERE id=$1", stratID) })

	// status='stopped' with no open cycle means Engine.Start's WHERE clause won't pick
	// it up for auto-load — that's correct (matches grid/matrix's own idle-strategy
	// behavior) and is exactly what this test wants to confirm: loading the ENGINE
	// itself must not panic or error just because an 'als' row exists in the table
	// alongside grid/matrix rows, even if this particular row isn't active.
	engine := New(pool, "")
	done := make(chan struct{})
	go func() {
		defer close(done)
		engine.Start(ctx)
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("Engine.Start did not return within 10s — possible deadlock/panic in ALS dispatch wiring")
	}
}
```

- [ ] **Step 2: Run it**

Run: `go test ./pkg/strategy/... -tags integration -run TestEngineStart_LoadsAlsStrategyWithoutPanicking -v`
Expected: `PASS`. If it panics, the panic message will point at exactly which dispatcher
in Task 5/6 is missing its `"als"` case — fix that site and re-run.

- [ ] **Step 3: Run the full regression suite one more time**

Run: `go build ./... && go vet ./... && go test ./pkg/strategy/... -tags integration 2>&1 | grep -- "--- FAIL"`
Expected: clean build/vet, exactly the 2 known baseline failures.

Run: `go test ./services/api-gateway/... -tags integration 2>&1 | grep -- "--- FAIL"`
Expected: exactly the 10 known baseline failures (this plan doesn't touch
`services/api-gateway` at all, but running it confirms nothing elsewhere broke).

- [ ] **Step 4: Commit**

```bash
git add pkg/strategy/als_lifecycle_test.go
git commit -m "Add ALS strategy lifecycle foundations test"
```

---

## What this plan does NOT do (by design — see next plans)

- No actual threshold evaluation, hedge sizing, or order placement — `als.go`'s functions
  are all no-op stubs.
- No stop-order memory-bank (`als_stop_memory` table exists but nothing reads/writes it
  yet).
- No TP pause/resume logic (will reuse the existing `HedgeTpSuppressed` field/`Engine.Notify`
  mechanism already in this plan's struct/query wiring — nothing sets it for ALS yet).
- No `services/api-gateway` HTTP handler changes — an ALS strategy can only be created by
  direct DB insert (as the tests in this plan do) until the API/frontend plan lands.
- No frontend changes — the "ALS скоро" button stays disabled until the frontend plan.

Each of these is a separate, already-scoped follow-up plan (see this file's header).
