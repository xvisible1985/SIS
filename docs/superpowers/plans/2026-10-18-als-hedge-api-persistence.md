# ALS Hedge Strategy — API Persistence Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make `services/api-gateway` actually persist ALS's new per-strategy config
fields (`als_thresholds`, `unsafe_hedge_min_pct`/`max_pct`, `stop_market_size_k`,
`tp_control_enabled`, `initial_stop_be_pct`, `als_stop_cond_pct`, `als_stop_replace_pct`)
from a hedge bot's config all the way into the `strategies` table row the `pkg/strategy`
engine actually reads — with zero frontend changes (that's the next, separate plan).

**Architecture:** `services/api-gateway` never writes `strategies` columns directly from
a bot's raw JSON config — every write goes through one shared derivation
(`computeBotStrategyCols`) and a `botStrategyCols` struct, consumed by exactly four SQL
call sites: the bind endpoint's `applyBotConfigToStrategy`, the two paths inside
`createBotStrategy` (reactivate-a-stopped-row UPDATE and fresh INSERT), and
`syncBotStrategies`'s live-resync UPDATE (used when a user edits an already-active bot
with "применить к активным" checked). This plan threads the eight new fields through
`botCfgJSON` → `botStrategyCols` → all four SQL statements, the same way the existing
Matrix-only fields (`matrix_levels`, `safe_zone_pct`, …) already flow through this exact
pipeline.

**Tech Stack:** Go, PostgreSQL (via `pgxpool`), the existing `services/api-gateway`
`botCfgJSON`/`botStrategyCols` pipeline.

**Prerequisites — read before starting:**

1. `migrations/098_als_hedge_strategy.sql` (written by
   `docs/superpowers/plans/2026-10-18-als-hedge-foundations.md`, Task 1) and
   `migrations/099_als_stop_cond_fields.sql` (written by
   `docs/superpowers/plans/2026-10-18-als-hedge-polish.md`, Task 4, Step 2) must both
   already be **applied** to whatever Postgres instance the integration tests in this
   plan run against — they add the `als_thresholds`, `unsafe_hedge_min_pct`,
   `unsafe_hedge_max_pct`, `stop_market_size_k`, `tp_control_enabled`,
   `initial_stop_be_pct`, `als_stop_cond_pct`, `als_stop_replace_pct` columns on
   `strategies` this plan reads and writes. Per this project's standing rule, Claude does
   not run `migrate` itself — ask the user to confirm both migrations are applied before
   starting Task 3 (the first task that touches real SQL against those columns). Tasks 1
   and 2 (Go struct field additions) don't need the DB and can proceed regardless.
2. The admin-only gate for `strategy_type="als"` is **already implemented and committed**
   this session (commit `68eedb6`): `CreateStrategy`/`UpdateStrategy`
   (`strategy_handler.go`) and `CreateBot`/`PatchBot` (`bots_handler.go`) all reject
   `strategy_type="als"` with 403 unless the caller is an admin
   (`s.isAdmin(ctx, callerID)`). Nothing in this plan touches that gate or needs to
   re-implement it — it already protects every entry point this plan's new fields flow
   through (a non-admin can't reach `computeBotStrategyCols` with `strategy_type="als"`
   in the first place, since `CreateBot`/`PatchBot` reject the request before any of this
   plan's code runs).

---

### Task 1: `botCfgJSON` — new ALS fields

**Files:**
- Modify: `services/api-gateway/bot_engine.go:1048-1049`

- [ ] **Step 1: Read the exact insertion point**

`botCfgJSON` (`bot_engine.go`) currently has, right where the new fields go:

```go
	SizeAsMain bool `json:"size_as_main"`

	// Hedge → Main control actions (hedge bots only).
```

- [ ] **Step 2: Add the eight new fields**

Insert between those two lines:

```go
	SizeAsMain bool `json:"size_as_main"`

	// ALS (Anti-Liquidation System) hedge strategy fields — read/written only when
	// StrategyType="als". Mirrors pkg/strategy/types.go's Strategy.AlsThresholds etc.
	// (same shape; this package never imports pkg/strategy — it's a separate JSON
	// contract that happens to line up). See
	// docs/superpowers/specs/2026-10-18-als-hedge-strategy-design.md.
	AlsThresholds     json.RawMessage `json:"als_thresholds"`
	UnsafeHedgeMinPct float64         `json:"unsafe_hedge_min_pct"`
	UnsafeHedgeMaxPct float64         `json:"unsafe_hedge_max_pct"`
	StopMarketSizeK   float64         `json:"stop_market_size_k"`
	TPControlEnabled  bool            `json:"tp_control_enabled"`
	InitialStopBEPct  *float64        `json:"initial_stop_be_pct,omitempty"`
	AlsStopCondPct    float64         `json:"als_stop_cond_pct"`
	AlsStopReplacePct float64         `json:"als_stop_replace_pct"`

	// Hedge → Main control actions (hedge bots only).
```

- [ ] **Step 3: Build to confirm it compiles**

Run: `go build ./services/api-gateway/...`
Expected: no output (clean build) — purely additive struct fields, nothing references
them yet.

- [ ] **Step 4: Commit**

```bash
git add services/api-gateway/bot_engine.go
git commit -m "Add ALS fields to botCfgJSON"
```

---

### Task 2: `botStrategyCols` — new fields, computed in `computeBotStrategyCols`

**Files:**
- Modify: `services/api-gateway/bot_engine.go:1154-1165` (struct), `:1300-1340` (compute)

- [ ] **Step 1: Add the new fields to `botStrategyCols`**

Current struct (`bot_engine.go:1154-1165`):

```go
type botStrategyCols struct {
	category, tpMode, slType, marginType, stratType, entryType string
	gridLevels, gridActive, leverage                           int
	gridStep, gridSize, tpPct, slPct, safeZonePct              float64
	scJSON                                                     string
	stepsParam, matrixLevelsParam, matrixEntryParam            *string
	trailingEnabled, hedgeMode, sizeAsMain, signalFilter       bool
	protectedBuild, matrixRebuildOnSL, matrixRebuildFromEntry  bool
	relativeSlots                                              bool
	trailingActPct, trailingCallPct                            *float64
	maxCycles                                                  int
}
```

Replace with:

```go
type botStrategyCols struct {
	category, tpMode, slType, marginType, stratType, entryType string
	gridLevels, gridActive, leverage                           int
	gridStep, gridSize, tpPct, slPct, safeZonePct              float64
	scJSON                                                     string
	stepsParam, matrixLevelsParam, matrixEntryParam            *string
	trailingEnabled, hedgeMode, sizeAsMain, signalFilter       bool
	protectedBuild, matrixRebuildOnSL, matrixRebuildFromEntry  bool
	relativeSlots                                              bool
	trailingActPct, trailingCallPct                            *float64
	maxCycles                                                  int

	// ALS-only columns — see botCfgJSON's matching fields (bot_engine.go) for meaning.
	alsThresholdsParam                  *string
	unsafeHedgeMinPct, unsafeHedgeMaxPct float64
	stopMarketSizeK                     float64
	tpControlEnabled                    bool
	initialStopBEPct                    *float64
	alsStopCondPct, alsStopReplacePct   float64
}
```

- [ ] **Step 2: Compute the new fields in `computeBotStrategyCols`**

Current (`bot_engine.go:1300-1339`):

```go
	// Matrix level params — NULL when not a matrix strategy so the columns stay clean.
	matrixLevelsParam := nullableJSONB(cfg.MatrixLevels)
	matrixEntryParam := nullableJSONB(cfg.MatrixEntryLevel)

	// signal_filter is always false for hedge-bot strategies: signal_configs serves
	// only as a pool for per-level use_signal buttons and must never block cycle start.
	// Ignoring cfg.SignalFilter intentionally — the backend is authoritative here.
	signalFilter := false

	return botStrategyCols{
		category:               category,
		tpMode:                 tpMode,
		slType:                 slType,
		marginType:             marginType,
		stratType:              stratType,
		entryType:              entryType,
		gridLevels:             gridLevels,
		gridActive:             gridActive,
		leverage:               leverage,
		gridStep:               gridStep,
		gridSize:               gridSize,
		tpPct:                  tpPct,
		slPct:                  slPct,
		safeZonePct:            cfg.SafeZonePct,
		scJSON:                 string(scJSON),
		stepsParam:             stepsParam,
		matrixLevelsParam:      matrixLevelsParam,
		matrixEntryParam:       matrixEntryParam,
		trailingEnabled:        cfg.TrailingEnabled,
		hedgeMode:              cfg.HedgeMode,
		sizeAsMain:             cfg.SizeAsMain,
		signalFilter:           signalFilter,
		protectedBuild:         cfg.ProtectedBuild,
		matrixRebuildOnSL:      cfg.MatrixRebuildOnSL,
		matrixRebuildFromEntry: cfg.MatrixRebuildFromEntry,
		relativeSlots:          cfg.RelativeSlots,
		trailingActPct:         trailingActPct,
		trailingCallPct:        trailingCallPct,
		maxCycles:              cfg.MaxCycles,
	}, nil
}
```

Replace with:

```go
	// Matrix level params — NULL when not a matrix strategy so the columns stay clean.
	matrixLevelsParam := nullableJSONB(cfg.MatrixLevels)
	matrixEntryParam := nullableJSONB(cfg.MatrixEntryLevel)

	// ALS threshold table — NULL when not an ALS strategy, same convention as matrix.
	alsThresholdsParam := nullableJSONB(cfg.AlsThresholds)

	// signal_filter is always false for hedge-bot strategies: signal_configs serves
	// only as a pool for per-level use_signal buttons and must never block cycle start.
	// Ignoring cfg.SignalFilter intentionally — the backend is authoritative here.
	signalFilter := false

	return botStrategyCols{
		category:               category,
		tpMode:                 tpMode,
		slType:                 slType,
		marginType:             marginType,
		stratType:              stratType,
		entryType:              entryType,
		gridLevels:             gridLevels,
		gridActive:             gridActive,
		leverage:               leverage,
		gridStep:               gridStep,
		gridSize:               gridSize,
		tpPct:                  tpPct,
		slPct:                  slPct,
		safeZonePct:            cfg.SafeZonePct,
		scJSON:                 string(scJSON),
		stepsParam:             stepsParam,
		matrixLevelsParam:      matrixLevelsParam,
		matrixEntryParam:       matrixEntryParam,
		trailingEnabled:        cfg.TrailingEnabled,
		hedgeMode:              cfg.HedgeMode,
		sizeAsMain:             cfg.SizeAsMain,
		signalFilter:           signalFilter,
		protectedBuild:         cfg.ProtectedBuild,
		matrixRebuildOnSL:      cfg.MatrixRebuildOnSL,
		matrixRebuildFromEntry: cfg.MatrixRebuildFromEntry,
		relativeSlots:          cfg.RelativeSlots,
		trailingActPct:         trailingActPct,
		trailingCallPct:        trailingCallPct,
		maxCycles:              cfg.MaxCycles,
		alsThresholdsParam:     alsThresholdsParam,
		unsafeHedgeMinPct:      cfg.UnsafeHedgeMinPct,
		unsafeHedgeMaxPct:      cfg.UnsafeHedgeMaxPct,
		stopMarketSizeK:        cfg.StopMarketSizeK,
		tpControlEnabled:       cfg.TPControlEnabled,
		initialStopBEPct:       cfg.InitialStopBEPct,
		alsStopCondPct:         cfg.AlsStopCondPct,
		alsStopReplacePct:      cfg.AlsStopReplacePct,
	}, nil
}
```

- [ ] **Step 3: Build to confirm it compiles**

Run: `go build ./services/api-gateway/...`
Expected: no output.

- [ ] **Step 4: Commit**

```bash
git add services/api-gateway/bot_engine.go
git commit -m "Compute ALS columns in computeBotStrategyCols"
```

---

### Task 3: Confirm migrations are applied, then write the round-trip test (drives Tasks 4-6)

**Files:**
- Create: `services/api-gateway/als_bot_persistence_test.go`

This single test file drives Tasks 4, 5, and 6 below — write it now (it will fail with
"column does not exist" until each task's SQL is extended), then come back to each
`t.Run` as you complete the matching task.

- [ ] **Step 1: Ask the user to confirm migrations 098 and 099 are applied**

Tell the user: "Before I continue, please confirm `098_als_hedge_strategy.sql` and
`099_als_stop_cond_fields.sql` are applied to the database the integration tests run
against." Wait for confirmation before running any test in this task.

- [ ] **Step 2: Write the failing test**

```go
//go:build integration

package main

import (
	"context"
	"encoding/json"
	"testing"

	"sis/pkg/trader"
)

// TestAlsFieldsRoundTrip covers all four SQL call sites that turn a hedge bot's
// strategy_config into strategies-table columns (see computeBotStrategyCols's doc
// comment): createBotStrategy's fresh-INSERT and reuse-stopped-row-UPDATE paths,
// applyBotConfigToStrategy (the bind endpoint), and syncBotStrategies (apply-to-active).
// Each must carry the eight new ALS columns through unchanged.
func TestAlsFieldsRoundTrip(t *testing.T) {
	s := newTestServer(t)
	ctx := context.Background()
	userID := createWHUser(t, s, "als_persist")
	accID := createTestAccount(t, s, userID)
	botID := createZombieBot(t, s, userID, "als_persist")
	t.Cleanup(func() { s.pool.Exec(ctx, "DELETE FROM strategies WHERE bot_id=$1", botID) })

	origBybit := bybitGetPublicInstrumentInfo
	t.Cleanup(func() { bybitGetPublicInstrumentInfo = origBybit })
	bybitGetPublicInstrumentInfo = func(ctx context.Context, category, symbol string) (trader.PubInstrumentInfo, error) {
		return trader.PubInstrumentInfo{TickSize: 0.1, QtyStep: 0.001, MinQty: 0.001, MinNotionalValue: 5}, nil
	}

	initialStop := 2.5
	cfg := botCfgJSON{
		StrategyType:      "als",
		GridSizeUSDT:      20,
		HedgeMode:         true,
		AlsThresholds:     json.RawMessage(`[{"trigger_type":1,"trigger_pct":-10,"target_hedge_pct":30}]`),
		UnsafeHedgeMinPct: 5,
		UnsafeHedgeMaxPct: 80,
		StopMarketSizeK:   1.2,
		TPControlEnabled:  true,
		InitialStopBEPct:  &initialStop,
		AlsStopCondPct:    3,
		AlsStopReplacePct: 1,
	}
	b := botEngineRow{id: botID, ownerID: userID, accountID: accID}

	type alsRow struct {
		thresholds        *string
		minPct, maxPct    float64
		stopK             float64
		tpControl         bool
		initialStop       *float64
		stopCond, stopRep float64
	}
	readAls := func(id string) alsRow {
		var r alsRow
		s.pool.QueryRow(ctx, `
			SELECT als_thresholds::text, unsafe_hedge_min_pct, unsafe_hedge_max_pct,
			       stop_market_size_k, tp_control_enabled, initial_stop_be_pct,
			       als_stop_cond_pct, als_stop_replace_pct
			FROM strategies WHERE id=$1`, id,
		).Scan(&r.thresholds, &r.minPct, &r.maxPct, &r.stopK, &r.tpControl, &r.initialStop, &r.stopCond, &r.stopRep)
		return r
	}
	assertRow := func(t *testing.T, r alsRow, label string) {
		t.Helper()
		if r.thresholds == nil || *r.thresholds == "" {
			t.Errorf("%s: als_thresholds is NULL, want the config jsonb", label)
		}
		if r.minPct != 5 || r.maxPct != 80 {
			t.Errorf("%s: unsafe_hedge_min/max_pct = %v/%v, want 5/80", label, r.minPct, r.maxPct)
		}
		if r.stopK != 1.2 {
			t.Errorf("%s: stop_market_size_k = %v, want 1.2", label, r.stopK)
		}
		if !r.tpControl {
			t.Errorf("%s: tp_control_enabled = false, want true", label)
		}
		if r.initialStop == nil || *r.initialStop != 2.5 {
			t.Errorf("%s: initial_stop_be_pct = %v, want 2.5", label, r.initialStop)
		}
		if r.stopCond != 3 || r.stopRep != 1 {
			t.Errorf("%s: als_stop_cond/replace_pct = %v/%v, want 3/1", label, r.stopCond, r.stopRep)
		}
	}

	t.Run("createBotStrategy INSERT path", func(t *testing.T) {
		id, err := s.createBotStrategy(ctx, b, cfg, "ALSINSUSDT", "long", 0, "", nil)
		if err != nil {
			t.Fatalf("createBotStrategy: %v", err)
		}
		assertRow(t, readAls(id), "INSERT")
	})

	t.Run("createBotStrategy reuse-stopped-row UPDATE path", func(t *testing.T) {
		var stoppedID string
		if err := s.pool.QueryRow(ctx,
			`INSERT INTO strategies (owner_id, account_id, bot_id, symbol, category, direction, strategy_type, status, created_at)
			 VALUES ($1,$2,$3,'ALSREUUSDT','linear','long','als','stopped',NOW()-INTERVAL '1 hour') RETURNING id`,
			userID, accID, botID,
		).Scan(&stoppedID); err != nil {
			t.Fatalf("seed stopped: %v", err)
		}
		id, err := s.createBotStrategy(ctx, b, cfg, "ALSREUUSDT", "long", 0, "", nil)
		if err != nil {
			t.Fatalf("createBotStrategy reuse: %v", err)
		}
		if id != stoppedID {
			t.Fatalf("expected reuse of %s, got %s", stoppedID, id)
		}
		assertRow(t, readAls(id), "reuse-UPDATE")
	})

	t.Run("applyBotConfigToStrategy (bind endpoint)", func(t *testing.T) {
		var stratID string
		if err := s.pool.QueryRow(ctx,
			`INSERT INTO strategies (owner_id, account_id, symbol, category, direction, status)
			 VALUES ($1,$2,'ALSBINDUSDT','linear','long','stopped') RETURNING id`,
			userID, accID,
		).Scan(&stratID); err != nil {
			t.Fatalf("seed: %v", err)
		}
		cols, err := s.computeBotStrategyCols(ctx, cfg, "ALSBINDUSDT", accID, 0)
		if err != nil {
			t.Fatalf("computeBotStrategyCols: %v", err)
		}
		if err := s.applyBotConfigToStrategy(ctx, s.pool, stratID, botID, cols, "", nil); err != nil {
			t.Fatalf("applyBotConfigToStrategy: %v", err)
		}
		assertRow(t, readAls(stratID), "bind-UPDATE")
	})

	t.Run("syncBotStrategies (apply to active)", func(t *testing.T) {
		var stratID string
		if err := s.pool.QueryRow(ctx,
			`INSERT INTO strategies (owner_id, account_id, bot_id, symbol, category, direction, strategy_type, status)
			 VALUES ($1,$2,$3,'ALSSYNCUSDT','linear','long','als','active') RETURNING id`,
			userID, accID, botID,
		).Scan(&stratID); err != nil {
			t.Fatalf("seed active: %v", err)
		}
		cfgBytes, _ := json.Marshal(cfg)
		if _, err := s.pool.Exec(ctx, `UPDATE bots SET strategy_config=$1 WHERE id=$2`, cfgBytes, botID); err != nil {
			t.Fatalf("seed bot config: %v", err)
		}
		s.syncBotStrategies(ctx, botID, false)
		assertRow(t, readAls(stratID), "sync-UPDATE")
	})
}
```

- [ ] **Step 3: Run it to confirm it fails**

Run: `go test -tags integration -run TestAlsFieldsRoundTrip ./services/api-gateway/... -v`
Expected: FAIL — `computeBotStrategyCols`/the struct literal don't yet reference
`AlsThresholds` etc. in the derivation sense this test needs (the struct fields exist
after Tasks 1-2, but the SQL in Tasks 4-6 hasn't been extended, so `assertRow` fails with
`als_thresholds is NULL` / wrong zero-values for every sub-test).

---

### Task 4: Extend `applyBotConfigToStrategy` (the bind endpoint)

**Files:**
- Modify: `services/api-gateway/bot_engine.go:1353-1381`

- [ ] **Step 1: Replace the function**

Current:

```go
func (s *Server) applyBotConfigToStrategy(ctx context.Context, db pgxExec, strategyID, botID string, cols botStrategyCols, hedgedStrategyID string, adoptJSON *string) error {
	_, err := db.Exec(ctx, `
		UPDATE strategies SET
		   status='active', cycle_count=0, manual_alert=NULL, updated_at=NOW(),
		   bot_id=$2::uuid, origin_bot_id=COALESCE(origin_bot_id, $2::uuid),
		   category=$3,
		   grid_levels=$4, grid_active=$5, grid_step_pct=$6, grid_size_usdt=$7,
		   tp_mode=$8, tp_pct=$9, sl_type=$10, sl_pct=$11, signal_filter=$12,
		   leverage=$13, margin_type=$14, hedge_mode=$15, strategy_type=$16, entry_order_type=$17,
		   signal_configs=$18::jsonb, steps=($19::text)::jsonb,
		   trailing_stop_enabled=$20, trailing_activation_pct=$21, trailing_callback_pct=$22,
		   max_cycles=$23, size_as_main=$24,
		   matrix_levels=($25::text)::jsonb, matrix_entry_level=($26::text)::jsonb, safe_zone_pct=$27,
		   protected_build=$28, matrix_rebuild_on_sl=$29, matrix_rebuild_from_entry=$30, relative_slots=$31,
		   hedged_strategy_id=NULLIF($32,'')::uuid, adopt_position_data=($33::text)::jsonb
		 WHERE id=$1`,
		strategyID, botID, cols.category,
		cols.gridLevels, cols.gridActive, cols.gridStep, cols.gridSize,
		cols.tpMode, cols.tpPct, cols.slType, cols.slPct, cols.signalFilter,
		cols.leverage, cols.marginType, cols.hedgeMode, cols.stratType, cols.entryType,
		cols.scJSON, cols.stepsParam,
		cols.trailingEnabled, cols.trailingActPct, cols.trailingCallPct,
		cols.maxCycles, cols.sizeAsMain,
		cols.matrixLevelsParam, cols.matrixEntryParam, cols.safeZonePct,
		cols.protectedBuild, cols.matrixRebuildOnSL, cols.matrixRebuildFromEntry, cols.relativeSlots,
		hedgedStrategyID, adoptJSON,
	)
	return err
}
```

Replace with:

```go
func (s *Server) applyBotConfigToStrategy(ctx context.Context, db pgxExec, strategyID, botID string, cols botStrategyCols, hedgedStrategyID string, adoptJSON *string) error {
	_, err := db.Exec(ctx, `
		UPDATE strategies SET
		   status='active', cycle_count=0, manual_alert=NULL, updated_at=NOW(),
		   bot_id=$2::uuid, origin_bot_id=COALESCE(origin_bot_id, $2::uuid),
		   category=$3,
		   grid_levels=$4, grid_active=$5, grid_step_pct=$6, grid_size_usdt=$7,
		   tp_mode=$8, tp_pct=$9, sl_type=$10, sl_pct=$11, signal_filter=$12,
		   leverage=$13, margin_type=$14, hedge_mode=$15, strategy_type=$16, entry_order_type=$17,
		   signal_configs=$18::jsonb, steps=($19::text)::jsonb,
		   trailing_stop_enabled=$20, trailing_activation_pct=$21, trailing_callback_pct=$22,
		   max_cycles=$23, size_as_main=$24,
		   matrix_levels=($25::text)::jsonb, matrix_entry_level=($26::text)::jsonb, safe_zone_pct=$27,
		   protected_build=$28, matrix_rebuild_on_sl=$29, matrix_rebuild_from_entry=$30, relative_slots=$31,
		   hedged_strategy_id=NULLIF($32,'')::uuid, adopt_position_data=($33::text)::jsonb,
		   als_thresholds=($34::text)::jsonb, unsafe_hedge_min_pct=$35, unsafe_hedge_max_pct=$36,
		   stop_market_size_k=$37, tp_control_enabled=$38, initial_stop_be_pct=$39,
		   als_stop_cond_pct=$40, als_stop_replace_pct=$41
		 WHERE id=$1`,
		strategyID, botID, cols.category,
		cols.gridLevels, cols.gridActive, cols.gridStep, cols.gridSize,
		cols.tpMode, cols.tpPct, cols.slType, cols.slPct, cols.signalFilter,
		cols.leverage, cols.marginType, cols.hedgeMode, cols.stratType, cols.entryType,
		cols.scJSON, cols.stepsParam,
		cols.trailingEnabled, cols.trailingActPct, cols.trailingCallPct,
		cols.maxCycles, cols.sizeAsMain,
		cols.matrixLevelsParam, cols.matrixEntryParam, cols.safeZonePct,
		cols.protectedBuild, cols.matrixRebuildOnSL, cols.matrixRebuildFromEntry, cols.relativeSlots,
		hedgedStrategyID, adoptJSON,
		cols.alsThresholdsParam, cols.unsafeHedgeMinPct, cols.unsafeHedgeMaxPct,
		cols.stopMarketSizeK, cols.tpControlEnabled, cols.initialStopBEPct,
		cols.alsStopCondPct, cols.alsStopReplacePct,
	)
	return err
}
```

- [ ] **Step 2: Run the bind sub-test**

Run: `go test -tags integration -run "TestAlsFieldsRoundTrip/applyBotConfigToStrategy" ./services/api-gateway/... -v`
Expected: PASS.

- [ ] **Step 3: Commit**

```bash
git add services/api-gateway/bot_engine.go
git commit -m "Persist ALS columns in applyBotConfigToStrategy (bind endpoint)"
```

---

### Task 5: Extend `createBotStrategy`'s reuse-UPDATE and INSERT

**Files:**
- Modify: `services/api-gateway/bot_engine.go:1420-1447` (reuse-UPDATE), `:1471-1505` (INSERT)

- [ ] **Step 1: Replace the reuse-UPDATE**

Current:

```go
		reErr := s.pool.QueryRow(ctx, `
			UPDATE strategies SET
			   status='active', cycle_count=0, manual_alert=NULL, updated_at=NOW(),
			   category=$2,
			   grid_levels=$3, grid_active=$4, grid_step_pct=$5, grid_size_usdt=$6,
			   tp_mode=$7, tp_pct=$8, sl_type=$9, sl_pct=$10, signal_filter=$11,
			   leverage=$12, margin_type=$13, hedge_mode=$14, strategy_type=$15, entry_order_type=$16,
			   signal_configs=$17::jsonb, steps=($18::text)::jsonb,
			   trailing_stop_enabled=$19, trailing_activation_pct=$20, trailing_callback_pct=$21,
			   max_cycles=$22, size_as_main=$23,
			   matrix_levels=($24::text)::jsonb, matrix_entry_level=($25::text)::jsonb, safe_zone_pct=$26,
			   protected_build=$27, matrix_rebuild_on_sl=$28, matrix_rebuild_from_entry=$29, relative_slots=$30,
			   hedged_strategy_id=NULLIF($31,'')::uuid, adopt_position_data=($32::text)::jsonb,
			   origin_bot_id=COALESCE(origin_bot_id, $33::uuid)
			 WHERE id=$1 AND status='stopped'
			 RETURNING id`,
			reuseID, cols.category,
			cols.gridLevels, cols.gridActive, cols.gridStep, cols.gridSize,
			cols.tpMode, cols.tpPct, cols.slType, cols.slPct, cols.signalFilter,
			cols.leverage, cols.marginType, cols.hedgeMode, cols.stratType, cols.entryType,
			cols.scJSON, cols.stepsParam,
			cols.trailingEnabled, cols.trailingActPct, cols.trailingCallPct,
			cols.maxCycles, cols.sizeAsMain,
			cols.matrixLevelsParam, cols.matrixEntryParam, cols.safeZonePct,
			cols.protectedBuild, cols.matrixRebuildOnSL, cols.matrixRebuildFromEntry, cols.relativeSlots,
			hedgedStrategyID, adoptJSON,
			b.id,
		).Scan(&rid)
```

Replace with:

```go
		reErr := s.pool.QueryRow(ctx, `
			UPDATE strategies SET
			   status='active', cycle_count=0, manual_alert=NULL, updated_at=NOW(),
			   category=$2,
			   grid_levels=$3, grid_active=$4, grid_step_pct=$5, grid_size_usdt=$6,
			   tp_mode=$7, tp_pct=$8, sl_type=$9, sl_pct=$10, signal_filter=$11,
			   leverage=$12, margin_type=$13, hedge_mode=$14, strategy_type=$15, entry_order_type=$16,
			   signal_configs=$17::jsonb, steps=($18::text)::jsonb,
			   trailing_stop_enabled=$19, trailing_activation_pct=$20, trailing_callback_pct=$21,
			   max_cycles=$22, size_as_main=$23,
			   matrix_levels=($24::text)::jsonb, matrix_entry_level=($25::text)::jsonb, safe_zone_pct=$26,
			   protected_build=$27, matrix_rebuild_on_sl=$28, matrix_rebuild_from_entry=$29, relative_slots=$30,
			   hedged_strategy_id=NULLIF($31,'')::uuid, adopt_position_data=($32::text)::jsonb,
			   origin_bot_id=COALESCE(origin_bot_id, $33::uuid),
			   als_thresholds=($34::text)::jsonb, unsafe_hedge_min_pct=$35, unsafe_hedge_max_pct=$36,
			   stop_market_size_k=$37, tp_control_enabled=$38, initial_stop_be_pct=$39,
			   als_stop_cond_pct=$40, als_stop_replace_pct=$41
			 WHERE id=$1 AND status='stopped'
			 RETURNING id`,
			reuseID, cols.category,
			cols.gridLevels, cols.gridActive, cols.gridStep, cols.gridSize,
			cols.tpMode, cols.tpPct, cols.slType, cols.slPct, cols.signalFilter,
			cols.leverage, cols.marginType, cols.hedgeMode, cols.stratType, cols.entryType,
			cols.scJSON, cols.stepsParam,
			cols.trailingEnabled, cols.trailingActPct, cols.trailingCallPct,
			cols.maxCycles, cols.sizeAsMain,
			cols.matrixLevelsParam, cols.matrixEntryParam, cols.safeZonePct,
			cols.protectedBuild, cols.matrixRebuildOnSL, cols.matrixRebuildFromEntry, cols.relativeSlots,
			hedgedStrategyID, adoptJSON,
			b.id,
			cols.alsThresholdsParam, cols.unsafeHedgeMinPct, cols.unsafeHedgeMaxPct,
			cols.stopMarketSizeK, cols.tpControlEnabled, cols.initialStopBEPct,
			cols.alsStopCondPct, cols.alsStopReplacePct,
		).Scan(&rid)
```

- [ ] **Step 2: Replace the INSERT**

Current:

```go
	var id string
	err := s.pool.QueryRow(ctx, `
		INSERT INTO strategies
		  (owner_id, account_id, bot_id, symbol, category, direction, status,
		   grid_levels, grid_active, grid_step_pct, grid_size_usdt,
		   tp_mode, tp_pct, sl_type, sl_pct, signal_filter,
		   leverage, margin_type, hedge_mode, strategy_type, entry_order_type,
		   signal_configs, steps,
		   trailing_stop_enabled, trailing_activation_pct, trailing_callback_pct,
		   cycle_count, max_cycles, size_as_main,
		   matrix_levels, matrix_entry_level, safe_zone_pct,
		   protected_build, matrix_rebuild_on_sl, matrix_rebuild_from_entry, relative_slots,
		   hedged_strategy_id, adopt_position_data, origin_bot_id)
		VALUES ($1,$2,$3,$4,$5,$6,'active',
		        $7,$8,$9,$10,
		        $11,$12,$13,$14,$15,
		        $16,$17,$18,$19,$20,
		        $21::jsonb, ($22::text)::jsonb,
		        $23,$24,$25,
		        $26, $27, $28,
		        ($29::text)::jsonb, ($30::text)::jsonb, $31,
		        $32, $33, $34, $35,
		        NULLIF($36,'')::uuid, ($37::text)::jsonb, $38)
		ON CONFLICT DO NOTHING
		RETURNING id`,
		b.ownerID, b.accountID, b.id, sym, cols.category, dir,
		cols.gridLevels, cols.gridActive, cols.gridStep, cols.gridSize,
		cols.tpMode, cols.tpPct, cols.slType, cols.slPct, cols.signalFilter,
		cols.leverage, cols.marginType, cols.hedgeMode, cols.stratType, cols.entryType,
		cols.scJSON, cols.stepsParam,
		cols.trailingEnabled, cols.trailingActPct, cols.trailingCallPct,
		0, cols.maxCycles, cols.sizeAsMain,
		cols.matrixLevelsParam, cols.matrixEntryParam, cols.safeZonePct,
		cols.protectedBuild, cols.matrixRebuildOnSL, cols.matrixRebuildFromEntry, cols.relativeSlots,
		hedgedStrategyID, adoptJSON, b.id,
	).Scan(&id)
```

Replace with:

```go
	var id string
	err := s.pool.QueryRow(ctx, `
		INSERT INTO strategies
		  (owner_id, account_id, bot_id, symbol, category, direction, status,
		   grid_levels, grid_active, grid_step_pct, grid_size_usdt,
		   tp_mode, tp_pct, sl_type, sl_pct, signal_filter,
		   leverage, margin_type, hedge_mode, strategy_type, entry_order_type,
		   signal_configs, steps,
		   trailing_stop_enabled, trailing_activation_pct, trailing_callback_pct,
		   cycle_count, max_cycles, size_as_main,
		   matrix_levels, matrix_entry_level, safe_zone_pct,
		   protected_build, matrix_rebuild_on_sl, matrix_rebuild_from_entry, relative_slots,
		   hedged_strategy_id, adopt_position_data, origin_bot_id,
		   als_thresholds, unsafe_hedge_min_pct, unsafe_hedge_max_pct, stop_market_size_k,
		   tp_control_enabled, initial_stop_be_pct, als_stop_cond_pct, als_stop_replace_pct)
		VALUES ($1,$2,$3,$4,$5,$6,'active',
		        $7,$8,$9,$10,
		        $11,$12,$13,$14,$15,
		        $16,$17,$18,$19,$20,
		        $21::jsonb, ($22::text)::jsonb,
		        $23,$24,$25,
		        $26, $27, $28,
		        ($29::text)::jsonb, ($30::text)::jsonb, $31,
		        $32, $33, $34, $35,
		        NULLIF($36,'')::uuid, ($37::text)::jsonb, $38,
		        ($39::text)::jsonb, $40, $41, $42,
		        $43, $44, $45, $46)
		ON CONFLICT DO NOTHING
		RETURNING id`,
		b.ownerID, b.accountID, b.id, sym, cols.category, dir,
		cols.gridLevels, cols.gridActive, cols.gridStep, cols.gridSize,
		cols.tpMode, cols.tpPct, cols.slType, cols.slPct, cols.signalFilter,
		cols.leverage, cols.marginType, cols.hedgeMode, cols.stratType, cols.entryType,
		cols.scJSON, cols.stepsParam,
		cols.trailingEnabled, cols.trailingActPct, cols.trailingCallPct,
		0, cols.maxCycles, cols.sizeAsMain,
		cols.matrixLevelsParam, cols.matrixEntryParam, cols.safeZonePct,
		cols.protectedBuild, cols.matrixRebuildOnSL, cols.matrixRebuildFromEntry, cols.relativeSlots,
		hedgedStrategyID, adoptJSON, b.id,
		cols.alsThresholdsParam, cols.unsafeHedgeMinPct, cols.unsafeHedgeMaxPct, cols.stopMarketSizeK,
		cols.tpControlEnabled, cols.initialStopBEPct, cols.alsStopCondPct, cols.alsStopReplacePct,
	).Scan(&id)
```

- [ ] **Step 3: Run the two `createBotStrategy` sub-tests**

Run: `go test -tags integration -run "TestAlsFieldsRoundTrip/createBotStrategy" ./services/api-gateway/... -v`
Expected: PASS (both "INSERT path" and "reuse-stopped-row UPDATE path").

- [ ] **Step 4: Commit**

```bash
git add services/api-gateway/bot_engine.go
git commit -m "Persist ALS columns in createBotStrategy (insert + reuse paths)"
```

---

### Task 6: Extend `syncBotStrategies` and `botConfigStructuralFieldsChanged`

**Files:**
- Modify: `services/api-gateway/bot_engine.go` (syncBotStrategies, ~lines 938-974), `services/api-gateway/bots_handler.go:804-815` (botConfigStructuralFieldsChanged)
- Test: `services/api-gateway/bots_config_structural_test.go`

- [ ] **Step 1: Write the failing unit test for the structural-change check**

Add to `bots_config_structural_test.go`, inside `TestBotConfigStructuralFieldsChanged`'s
existing `t.Run` table (after the `"true when matrix_levels genuinely differs"` case):

```go
	t.Run("true when als_thresholds genuinely differs", func(t *testing.T) {
		oldCfg := botCfgJSON{AlsThresholds: json.RawMessage(`[{"trigger_pct":-10}]`)}
		newCfg := botCfgJSON{AlsThresholds: json.RawMessage(`[{"trigger_pct":-20}]`)}

		if !botConfigStructuralFieldsChanged(oldCfg, newCfg) {
			t.Errorf("expected true: als_thresholds content differs")
		}
	})
```

- [ ] **Step 2: Run it to confirm it fails**

Run: `go test -run TestBotConfigStructuralFieldsChanged ./services/api-gateway/... -v`
Expected: FAIL — `als_thresholds` isn't compared yet, so both configs look structurally
equal.

- [ ] **Step 3: Add the comparison**

Current (`bots_handler.go:804-815`):

```go
func botConfigStructuralFieldsChanged(oldCfg, newCfg botCfgJSON) bool {
	return oldCfg.GridLevels != newCfg.GridLevels ||
		oldCfg.GridActive != newCfg.GridActive ||
		diffFloat(oldCfg.GridStepPct, newCfg.GridStepPct) ||
		diffFloat(oldCfg.GridSizeUSDT, newCfg.GridSizeUSDT) ||
		oldCfg.Direction != newCfg.Direction ||
		oldCfg.EntryOrderType != newCfg.EntryOrderType ||
		oldCfg.Leverage != newCfg.Leverage ||
		!reflect.DeepEqual(oldCfg.Steps, newCfg.Steps) ||
		!jsonRawEqual(oldCfg.MatrixLevels, newCfg.MatrixLevels) ||
		!jsonRawEqual(oldCfg.MatrixEntryLevel, newCfg.MatrixEntryLevel)
}
```

Replace with:

```go
func botConfigStructuralFieldsChanged(oldCfg, newCfg botCfgJSON) bool {
	return oldCfg.GridLevels != newCfg.GridLevels ||
		oldCfg.GridActive != newCfg.GridActive ||
		diffFloat(oldCfg.GridStepPct, newCfg.GridStepPct) ||
		diffFloat(oldCfg.GridSizeUSDT, newCfg.GridSizeUSDT) ||
		oldCfg.Direction != newCfg.Direction ||
		oldCfg.EntryOrderType != newCfg.EntryOrderType ||
		oldCfg.Leverage != newCfg.Leverage ||
		!reflect.DeepEqual(oldCfg.Steps, newCfg.Steps) ||
		!jsonRawEqual(oldCfg.MatrixLevels, newCfg.MatrixLevels) ||
		!jsonRawEqual(oldCfg.MatrixEntryLevel, newCfg.MatrixEntryLevel) ||
		// ALS's threshold table plays the same role matrix_levels does — it defines the
		// strategy_levels rows seeded at cycle start (see pkg/strategy/als.go's
		// startAlsCycle), so a genuine change must restart the cycle the same way editing
		// matrix_levels does.
		!jsonRawEqual(oldCfg.AlsThresholds, newCfg.AlsThresholds)
}
```

- [ ] **Step 4: Run it to confirm it passes**

Run: `go test -run TestBotConfigStructuralFieldsChanged ./services/api-gateway/... -v`
Expected: PASS, all sub-tests.

- [ ] **Step 5: Extend `syncBotStrategies`**

Current (`bot_engine.go`, inside `syncBotStrategies`):

```go
	// Matrix params (nil = keep existing when not a matrix bot)
	matrixLevelsParam := nullableJSONB(cfg.MatrixLevels)
	matrixEntryParam := nullableJSONB(cfg.MatrixEntryLevel)

	// Hedge-bot strategies must never block cycle-start on a signal:
	// signal_configs is a pool for per-level use_signal buttons only.
	syncSignalFilter := cfg.SignalFilter
	if cfg.BotKind == "hedge" {
		syncSignalFilter = false
	}

	// Update all active/finishing strategies belonging to this bot
	rows, err := s.pool.Query(ctx,
		`UPDATE strategies SET
		   grid_levels = $1, grid_active = $2, grid_step_pct = $3, grid_size_usdt = $4,
		   tp_mode = $5, tp_pct = $6, sl_type = $7, sl_pct = $8, signal_filter = $9,
		   leverage = $10, margin_type = $11, hedge_mode = $12,
		   signal_configs = $13::jsonb, steps = ($14::text)::jsonb,
		   trailing_stop_enabled = $15, trailing_activation_pct = $16, trailing_callback_pct = $17,
		   matrix_levels      = COALESCE(($18::text)::jsonb, matrix_levels),
		   matrix_entry_level = COALESCE(($19::text)::jsonb, matrix_entry_level),
		   safe_zone_pct      = CASE WHEN $20::float8 > 0 THEN $20 ELSE safe_zone_pct END,
		   protected_build    = $21,
		   matrix_rebuild_on_sl      = $22,
		   matrix_rebuild_from_entry = $23,
		   relative_slots            = $24
		 WHERE bot_id = $25 AND status IN ('active','finishing')
		 RETURNING id`,
		gridLevels, gridActive, gridStep, gridSize,
		tpMode, tpPct, slType, slPct, syncSignalFilter,
		leverage, marginType, cfg.HedgeMode,
		string(scJSON), stepsParam,
		cfg.TrailingEnabled, trailingActPct, trailingCallPct,
		matrixLevelsParam, matrixEntryParam, cfg.SafeZonePct,
		cfg.ProtectedBuild, cfg.MatrixRebuildOnSL, cfg.MatrixRebuildFromEntry, cfg.RelativeSlots,
		botID,
	)
```

Replace with:

```go
	// Matrix params (nil = keep existing when not a matrix bot)
	matrixLevelsParam := nullableJSONB(cfg.MatrixLevels)
	matrixEntryParam := nullableJSONB(cfg.MatrixEntryLevel)

	// ALS params (nil/0 = keep existing when not an ALS bot — same convention as matrix
	// above; numeric fields use the same "0 means absent" CASE pattern safe_zone_pct
	// already uses, so a sync of unrelated fields can't zero these out).
	alsThresholdsParam := nullableJSONB(cfg.AlsThresholds)

	// Hedge-bot strategies must never block cycle-start on a signal:
	// signal_configs is a pool for per-level use_signal buttons only.
	syncSignalFilter := cfg.SignalFilter
	if cfg.BotKind == "hedge" {
		syncSignalFilter = false
	}

	// Update all active/finishing strategies belonging to this bot
	rows, err := s.pool.Query(ctx,
		`UPDATE strategies SET
		   grid_levels = $1, grid_active = $2, grid_step_pct = $3, grid_size_usdt = $4,
		   tp_mode = $5, tp_pct = $6, sl_type = $7, sl_pct = $8, signal_filter = $9,
		   leverage = $10, margin_type = $11, hedge_mode = $12,
		   signal_configs = $13::jsonb, steps = ($14::text)::jsonb,
		   trailing_stop_enabled = $15, trailing_activation_pct = $16, trailing_callback_pct = $17,
		   matrix_levels      = COALESCE(($18::text)::jsonb, matrix_levels),
		   matrix_entry_level = COALESCE(($19::text)::jsonb, matrix_entry_level),
		   safe_zone_pct      = CASE WHEN $20::float8 > 0 THEN $20 ELSE safe_zone_pct END,
		   protected_build    = $21,
		   matrix_rebuild_on_sl      = $22,
		   matrix_rebuild_from_entry = $23,
		   relative_slots            = $24,
		   als_thresholds       = COALESCE(($26::text)::jsonb, als_thresholds),
		   unsafe_hedge_min_pct = CASE WHEN $27::float8 > 0 THEN $27 ELSE unsafe_hedge_min_pct END,
		   unsafe_hedge_max_pct = CASE WHEN $28::float8 > 0 THEN $28 ELSE unsafe_hedge_max_pct END,
		   stop_market_size_k   = CASE WHEN $29::float8 > 0 THEN $29 ELSE stop_market_size_k END,
		   tp_control_enabled   = $30,
		   initial_stop_be_pct  = $31,
		   als_stop_cond_pct    = CASE WHEN $32::float8 > 0 THEN $32 ELSE als_stop_cond_pct END,
		   als_stop_replace_pct = CASE WHEN $33::float8 > 0 THEN $33 ELSE als_stop_replace_pct END
		 WHERE bot_id = $25 AND status IN ('active','finishing')
		 RETURNING id`,
		gridLevels, gridActive, gridStep, gridSize,
		tpMode, tpPct, slType, slPct, syncSignalFilter,
		leverage, marginType, cfg.HedgeMode,
		string(scJSON), stepsParam,
		cfg.TrailingEnabled, trailingActPct, trailingCallPct,
		matrixLevelsParam, matrixEntryParam, cfg.SafeZonePct,
		cfg.ProtectedBuild, cfg.MatrixRebuildOnSL, cfg.MatrixRebuildFromEntry, cfg.RelativeSlots,
		botID,
		alsThresholdsParam, cfg.UnsafeHedgeMinPct, cfg.UnsafeHedgeMaxPct, cfg.StopMarketSizeK,
		cfg.TPControlEnabled, cfg.InitialStopBEPct, cfg.AlsStopCondPct, cfg.AlsStopReplacePct,
	)
```

Note `$26`-`$33` are used in the `SET` clause while `$25` (bot_id) appears in `WHERE` —
Postgres placeholders don't need to be in textual order, only consistent with the
positional args list, which here is: 1-17 unchanged, 18-24 unchanged (matrix/protected
params), `botID` as arg #25, then the eight new args as #26-#33, matching `$26`-`$33` in
the SQL text above.

- [ ] **Step 6: Run the sync sub-test**

Run: `go test -tags integration -run "TestAlsFieldsRoundTrip/syncBotStrategies" ./services/api-gateway/... -v`
Expected: PASS.

- [ ] **Step 7: Run the full new test file**

Run: `go test -tags integration -run TestAlsFieldsRoundTrip ./services/api-gateway/... -v`
Expected: PASS, all four sub-tests.

- [ ] **Step 8: Commit**

```bash
git add services/api-gateway/bot_engine.go services/api-gateway/bots_handler.go services/api-gateway/bots_config_structural_test.go services/api-gateway/als_bot_persistence_test.go
git commit -m "Persist ALS columns in syncBotStrategies; treat als_thresholds as structural"
```

---

### Task 7: Full regression run

**Files:** none (verification only)

- [ ] **Step 1: Run the full `services/api-gateway` suite**

Run: `go test -tags integration ./services/api-gateway/...`
Expected: the same pre-existing failures already on baseline before this plan
(`TestCreateAccount_NoEncKey`, `TestPatchBot_LinkedSubscriptionBlocked`, `TestDeployBot`,
`TestForkBot`, `TestPatchBot_StrategyLockWhileActive`, the custom-signal/webhook tests,
`TestMatrixRepairCandidates_FindsOneSidedSymbols` — confirmed via `git stash` in the
admin-gate commit, `68eedb6`, to be environmental/pre-existing, not regressions), plus
every new ALS test passing. If a *different* test newly fails, that's a real regression
from this plan's SQL edits — stop and investigate before continuing (most likely cause:
a placeholder-numbering mistake in one of Tasks 4-6's SQL rewrites).

- [ ] **Step 2: Report to the user**

State plainly which tests ran, which passed, and which of the pre-existing failures
(if any) are still present unchanged — per this project's standing rule that a test
review must be reported explicitly, not just "tests passed."

---

## Self-review notes (completed while writing this plan)

- **Spec coverage**: this plan covers the "Data model changes" table's five `strategies`
  columns from `Task 1`/`98_...` plus the two `als_stop_cond_pct`/`als_stop_replace_pct`
  columns from the Polish plan's Task 4 — all eight flow through all four SQL call
  sites. It deliberately does **not** cover `als_stop_memory` CRUD (that's `pkg/strategy`
  internal state, written by `als.go`, never by `services/api-gateway` directly — no API
  surface exists for it per the design spec) or any frontend change (next plan).
- **Placeholder scan**: no TBD/"add validation"/bare prose steps — every SQL/Go change
  is shown as a literal before/after block, re-read from the live file before writing
  (bot_engine.go's three sites and bots_handler.go's one).
- **Type consistency**: `botCfgJSON`'s new field names (Task 1) are used identically in
  `computeBotStrategyCols` (Task 2), `botStrategyCols`'s new field names are used
  identically across all four SQL call sites (Tasks 4-6), and all match the exact names
  already committed in `pkg/strategy/types.go` by the Foundations and Polish plans
  (`AlsThresholds`, `UnsafeHedgeMinPct`, `UnsafeHedgeMaxPct`, `StopMarketSizeK`,
  `TPControlEnabled`, `InitialStopBEPct`, `AlsStopCondPct`, `AlsStopReplacePct`) — this
  package has its own separate `botCfgJSON` struct (no shared import), but using the same
  names avoids confusion when cross-referencing the two plans.
