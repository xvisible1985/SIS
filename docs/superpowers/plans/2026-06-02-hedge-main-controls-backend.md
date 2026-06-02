# Hedge Main Controls — Backend Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** When a hedge bot activates, it can (per config) cancel TP/SL orders on the main strategy and/or stop the main strategy entirely. When the hedge deactivates, the main strategy is restored.

**Architecture:** DB suppression flags (`hedge_tp_suppressed`, `hedge_sl_suppressed`, `hedge_stopped_by`) drive everything. The hedge engine sets flags in DB and notifies the strategy engine. The strategy engine reacts to flag changes in `addStrategy` by submitting cancellation/restoration tasks.

**Tech Stack:** Go 1.22, pgx/v5, strategy engine (pkg/strategy), hedge engine (services/api-gateway)

---

## File Map

| File | Action | Responsibility |
|------|--------|---------------|
| `migrations/057_hedge_main_controls.sql` | Create | Add 3 columns to strategies table |
| `services/api-gateway/bot_engine.go` | Modify | Add 3 botCfgJSON fields; `createBotStrategy` returns `(string, error)` |
| `services/api-gateway/bots_handler.go` | Modify | Update `createBotStrategy` call site |
| `services/api-gateway/hedge_engine.go` | Modify | `applyHedgeMainControls`, `restoreHedgeMainControls` |
| `pkg/strategy/types.go` | Modify | Add 3 fields to Strategy struct |
| `pkg/strategy/engine.go` | Modify | SELECT queries + scanStrategy + addStrategy hooks |
| `pkg/strategy/hedge_support.go` | Create | `cancelTPForHedge`, `cancelSLForHedge`, `restoreTPAfterHedge`, `restoreSLAfterHedge` |
| `pkg/strategy/matrix.go` | Modify | Suppression guards in `matrixUpdateTP`, `matrixPlacePerLevelSL` |
| `pkg/strategy/cycle.go` | Modify | Suppression guards in `updateTP`, `updateSL` |
| `pkg/strategy/reconcile.go` | Modify | Suppression guards in TP/SL re-placement and matrix SL health-check |

---

### Task 1: DB Migration

**Files:**
- Create: `migrations/057_hedge_main_controls.sql`

- [ ] **Step 1: Create the migration file**

```sql
-- 057_hedge_main_controls.sql
-- Adds suppression flags for hedge→main control actions.
-- hedge_tp_suppressed: when true, TP orders must not be placed/re-placed on this strategy.
-- hedge_sl_suppressed: when true, SL orders (cycle-level + per-level) must not be placed/re-placed.
-- hedge_stopped_by:    UUID of the hedge strategy that stopped this main strategy; NULL when not stopped by hedge.

ALTER TABLE strategies
  ADD COLUMN IF NOT EXISTS hedge_tp_suppressed BOOLEAN NOT NULL DEFAULT FALSE,
  ADD COLUMN IF NOT EXISTS hedge_sl_suppressed BOOLEAN NOT NULL DEFAULT FALSE,
  ADD COLUMN IF NOT EXISTS hedge_stopped_by    UUID REFERENCES strategies(id) ON DELETE SET NULL;
```

- [ ] **Step 2: Verify the migration file is correct**

Read the file and confirm the SQL is valid.

- [ ] **Step 3: Commit**

```bash
git add migrations/057_hedge_main_controls.sql
git commit -m "feat: migration 057 — hedge main controls suppression columns"
```

---

### Task 2: botCfgJSON Fields + createBotStrategy Return Type

**Files:**
- Modify: `services/api-gateway/bot_engine.go`
- Modify: `services/api-gateway/bots_handler.go`

- [ ] **Step 1: Write failing test** (verify the struct has the new fields by checking compilation)

The "test" here is that the Go build succeeds. We verify after implementation.

- [ ] **Step 2: Add 3 fields to botCfgJSON struct in `services/api-gateway/bot_engine.go`**

After the existing `SizeAsMain bool \`json:"size_as_main"\`` line (around line 783), add:

```go
	// Hedge → Main control actions.
	HedgeCancelMainTp bool `json:"hedge_cancel_main_tp"`
	HedgeCancelMainSl bool `json:"hedge_cancel_main_sl"`
	HedgeStopMain     bool `json:"hedge_stop_main"`
```

- [ ] **Step 3: Change createBotStrategy signature to return (string, error)**

Change the function signature at line 831 from:
```go
func (s *Server) createBotStrategy(ctx context.Context, b botEngineRow, cfg botCfgJSON, sym, dir string, leverageOverride int, hedgedStrategyID string) error {
```
to:
```go
func (s *Server) createBotStrategy(ctx context.Context, b botEngineRow, cfg botCfgJSON, sym, dir string, leverageOverride int, hedgedStrategyID string) (string, error) {
```

- [ ] **Step 4: Update the function body to return (string, error)**

In the function body, change:
```go
	var id string
	err := s.pool.QueryRow(ctx, `...`).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		// Another hedge bot already claimed this main strategy — silently skip.
		return nil
	}
	if err != nil {
		return err
	}

	// Notify strategy engine to load and start this new strategy
	go s.engine.Notify(context.Background(), id)
	return nil
```
to:
```go
	var id string
	err := s.pool.QueryRow(ctx, `...`).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		// Another hedge bot already claimed this main strategy — silently skip.
		return "", nil
	}
	if err != nil {
		return "", err
	}

	// Notify strategy engine to load and start this new strategy
	go s.engine.Notify(context.Background(), id)
	return id, nil
```

- [ ] **Step 5: Update call sites in bot_engine.go**

There are 3 call sites in bot_engine.go. For each one that was:
```go
if err := s.createBotStrategy(ctx, b, cfg, ...); err != nil {
```
change to:
```go
if _, err := s.createBotStrategy(ctx, b, cfg, ...); err != nil {
```

Call sites are at approximately lines 231, 1241, and in hedge_engine.go line 539 (hedge engine will handle differently — see Task 7).

- [ ] **Step 6: Update call site in bots_handler.go**

At approximately line 1166, change:
```go
if err := s.createBotStrategy(ctx, b, cfg, req.Symbol, req.Direction, 0, ""); err != nil {
```
to:
```go
if _, err := s.createBotStrategy(ctx, b, cfg, req.Symbol, req.Direction, 0, ""); err != nil {
```

- [ ] **Step 7: Build to verify no compilation errors**

```bash
cd C:\Users\123\Projects\sis\services\api-gateway
go build ./...
```
Expected: no errors.

- [ ] **Step 8: Commit**

```bash
git add services/api-gateway/bot_engine.go services/api-gateway/bots_handler.go
git commit -m "feat: hedge main controls — botCfgJSON fields + createBotStrategy returns ID"
```

---

### Task 3: Strategy Struct + DB Loading

**Files:**
- Modify: `pkg/strategy/types.go`
- Modify: `pkg/strategy/engine.go`

- [ ] **Step 1: Add 3 fields to Strategy struct in `pkg/strategy/types.go`**

After `SizeAsMain bool` (line 117), add:

```go
	// Hedge main control flags — set by hedge engine when a hedge activates/deactivates.
	HedgeTpSuppressed bool    // do not place/re-place TP orders while true
	HedgeSlSuppressed bool    // do not place/re-place SL orders while true
	HedgeStoppedBy    *string // ID of the hedge strategy that stopped this main; nil if not stopped by hedge
```

- [ ] **Step 2: Add columns to the first SELECT query in `pkg/strategy/engine.go`**

The query in `Load()` currently ends with:
```sql
        COALESCE(size_as_main,false)
 FROM strategies WHERE status IN ('active','finishing')
```

Change to:
```sql
        COALESCE(size_as_main,false),
        COALESCE(hedge_tp_suppressed,false),
        COALESCE(hedge_sl_suppressed,false),
        hedge_stopped_by::text
 FROM strategies WHERE status IN ('active','finishing')
```

- [ ] **Step 3: Add same columns to the Notify() SELECT query**

The query in `Notify()` currently ends with:
```sql
        COALESCE(size_as_main,false)
 FROM strategies WHERE id=$1
```

Change to:
```sql
        COALESCE(size_as_main,false),
        COALESCE(hedge_tp_suppressed,false),
        COALESCE(hedge_sl_suppressed,false),
        hedge_stopped_by::text
 FROM strategies WHERE id=$1
```

- [ ] **Step 4: Update scanStrategy in `pkg/strategy/engine.go` to scan the 3 new columns**

In `scanStrategy`, the current scan list ends with `&s.SizeAsMain`. Add 3 new variables and scan targets.

Current end of the `rows.Scan(...)` call:
```go
		&s.ProtectedBuild,
		&s.RebuildOnSL,
		&s.RebuildFromEntry,
		&s.StrategyType,
		&s.SizeAsMain,
```

Change to:
```go
		&s.ProtectedBuild,
		&s.RebuildOnSL,
		&s.RebuildFromEntry,
		&s.StrategyType,
		&s.SizeAsMain,
		&s.HedgeTpSuppressed,
		&s.HedgeSlSuppressed,
		&stoppedByStr,
```

And add `var stoppedByStr *string` at the top of the function where the other var declarations are, and after the Scan call:
```go
	s.HedgeStoppedBy = stoppedByStr
```

- [ ] **Step 5: Build pkg/strategy to verify**

```bash
cd C:\Users\123\Projects\sis
go build ./pkg/strategy/...
```
Expected: no errors.

- [ ] **Step 6: Commit**

```bash
git add pkg/strategy/types.go pkg/strategy/engine.go
git commit -m "feat: hedge main controls — Strategy struct + DB loading"
```

---

### Task 4: Suppression Guards in matrix.go, cycle.go, reconcile.go

**Files:**
- Modify: `pkg/strategy/matrix.go`
- Modify: `pkg/strategy/cycle.go`
- Modify: `pkg/strategy/reconcile.go`

- [ ] **Step 1: Add TP suppression guard in matrixUpdateTP (`pkg/strategy/matrix.go`)**

The function `matrixUpdateTP` starts at approximately line 1189:
```go
func (sr *StrategyRunner) matrixUpdateTP(ctx context.Context) {
	if sr.instr.QtyStep == 0 {
		return
	}
```

After the `QtyStep == 0` guard, add:
```go
	// TP suppressed by active hedge — do not place/re-place TP.
	if sr.strategy.HedgeTpSuppressed {
		return
	}
```

- [ ] **Step 2: Add SL suppression guard in matrixPlacePerLevelSL (`pkg/strategy/matrix.go`)**

The function starts at approximately line 1113:
```go
func (sr *StrategyRunner) matrixPlacePerLevelSL(ctx context.Context, l *GridLevel, fillPrice, stopPct float64) {
	// Negative slots (L-1, L-2…) are averaging positions for managed hedge — no per-level SL.
	if l.Slot != nil && *l.Slot < 0 {
		return
	}
```

After the negative-slot guard, add:
```go
	// SL suppressed by active hedge — do not place/re-place per-level SL.
	if sr.strategy.HedgeSlSuppressed {
		return
	}
```

- [ ] **Step 3: Add TP suppression guard in updateTP (non-matrix) in `pkg/strategy/cycle.go`**

Find the `updateTPByType` function (around line 2077). This delegates to `matrixUpdateTP` for matrix strategies (which already has the guard), and calls `updateTP` for grid strategies. Find `updateTP` function and add a guard at its top:

```go
func (sr *StrategyRunner) updateTP(ctx context.Context) error {
	// TP suppressed by active hedge — do not place/re-place TP.
	if sr.strategy.HedgeTpSuppressed {
		return nil
	}
	// ... existing code ...
```

- [ ] **Step 4: Add SL suppression guard in updateSL (non-matrix) in `pkg/strategy/cycle.go`**

Find the `updateSL` function (around line 2116). Add guard at top (after the matrix early return):

```go
func (sr *StrategyRunner) updateSL(ctx context.Context) error {
	if sr.strategy.StrategyType == "matrix" {
		return nil // Matrix manages SL per-level via matrixPlacePerLevelSL
	}
	// SL suppressed by active hedge — do not place/re-place SL.
	if sr.strategy.HedgeSlSuppressed {
		return nil
	}
	// ... existing code ...
```

- [ ] **Step 5: Add suppression guards in reconcile.go TP/SL re-place logic**

In `pkg/strategy/reconcile.go` around lines 241-319 where missing TP/SL orders are re-placed, add guards inside the submitted tasks:

For TP re-place (inside the `sr.submit` callback at ~line 250):
```go
sr.submit(func(ctx context.Context) {
    sr.mu.Lock()
    defer sr.mu.Unlock()
    if sr.strategy.HedgeTpSuppressed {  // ADD THIS
        return
    }
    if sr.tpOrderID == tpOrderID {
        // ... existing logic ...
    }
})
```

For "TP never placed" case (~line 268):
```go
sr.submit(func(ctx context.Context) {
    sr.mu.Lock()
    defer sr.mu.Unlock()
    if sr.strategy.HedgeTpSuppressed {  // ADD THIS
        return
    }
    _, posQty := sr.avgEntry()
    // ... existing logic ...
})
```

For SL re-place (~line 290):
```go
sr.submit(func(ctx context.Context) {
    sr.mu.Lock()
    defer sr.mu.Unlock()
    if sr.strategy.HedgeSlSuppressed {  // ADD THIS
        return
    }
    // ... existing logic ...
})
```

For "SL never placed" case (~line 308):
```go
sr.submit(func(ctx context.Context) {
    sr.mu.Lock()
    defer sr.mu.Unlock()
    if sr.strategy.HedgeSlSuppressed {  // ADD THIS
        return
    }
    // ... existing logic ...
})
```

- [ ] **Step 6: Add suppression guard in matrix SL health-check in reconcile.go (~line 338)**

Inside the `sr.submit` callback in the matrix SL health-check loop:
```go
sr.submit(func(ctx context.Context) {
    sr.mu.Lock()
    defer sr.mu.Unlock()
    if sr.strategy.HedgeSlSuppressed {  // ADD THIS
        return
    }
    for i := range sr.levels {
        // ... existing loop ...
    }
})
```

- [ ] **Step 7: Build to verify**

```bash
cd C:\Users\123\Projects\sis
go build ./pkg/strategy/...
```
Expected: no errors.

- [ ] **Step 8: Commit**

```bash
git add pkg/strategy/matrix.go pkg/strategy/cycle.go pkg/strategy/reconcile.go
git commit -m "feat: hedge main controls — suppression guards in matrix, cycle, reconcile"
```

---

### Task 5: Suppression Helper Functions (hedge_support.go)

**Files:**
- Create: `pkg/strategy/hedge_support.go`

- [ ] **Step 1: Create `pkg/strategy/hedge_support.go` with 4 helper functions**

```go
// pkg/strategy/hedge_support.go
//
// Helper functions called by the strategy engine when a hedge bot
// activates or deactivates TP/SL suppression on a main strategy.

package strategy

import (
	"fmt"

	"github.com/yourorg/sis/pkg/trader"
)

// cancelTPForHedge cancels the current TP order (if any) and clears it from DB.
// Called when a hedge bot activates TP suppression on this strategy.
// Must NOT be called with sr.mu held.
func (sr *StrategyRunner) cancelTPForHedge(ctx context.Context) {
	sr.mu.Lock()
	defer sr.mu.Unlock()
	if sr.tpOrderID == "" {
		return
	}
	old := sr.tpOrderID
	sr.runner.UnregisterOrder(old)
	sr.tpOrderID = ""
	if sr.cycle != nil {
		sr.runner.pool.Exec(ctx, //nolint:errcheck
			`UPDATE strategy_cycles SET tp_order_id=NULL WHERE id=$1`, sr.cycle.ID)
	}
	if err := sr.runner.tradeStream.CancelOrder(ctx, trader.CancelRequest{
		Symbol:   sr.strategy.Symbol,
		Category: sr.strategy.Category,
		OrderId:  old,
	}); err != nil && !isOrderGone(err) {
		sr.warn(ctx, fmt.Sprintf("cancelTPForHedge: %v", err))
	}
}

// cancelSLForHedge cancels all SL orders (cycle-level + per-level matrix) and clears them from DB.
// Called when a hedge bot activates SL suppression on this strategy.
// Must NOT be called with sr.mu held.
func (sr *StrategyRunner) cancelSLForHedge(ctx context.Context) {
	sr.mu.Lock()
	defer sr.mu.Unlock()
	// Cancel cycle-level SL (grid strategies)
	if sr.slOrderID != "" {
		old := sr.slOrderID
		sr.runner.UnregisterOrder(old)
		sr.slOrderID = ""
		if sr.cycle != nil {
			sr.runner.pool.Exec(ctx, //nolint:errcheck
				`UPDATE strategy_cycles SET sl_order_id=NULL WHERE id=$1`, sr.cycle.ID)
		}
		if err := sr.runner.tradeStream.CancelOrder(ctx, trader.CancelRequest{
			Symbol:   sr.strategy.Symbol,
			Category: sr.strategy.Category,
			OrderId:  old,
		}); err != nil && !isOrderGone(err) {
			sr.warn(ctx, fmt.Sprintf("cancelSLForHedge (cycle): %v", err))
		}
	}
	// Cancel all per-level SLs (matrix strategies)
	sr.matrixCancelPerLevelSLs(ctx)
}

// restoreTPAfterHedge re-places the TP order after a hedge deactivates TP suppression.
// Must NOT be called with sr.mu held.
func (sr *StrategyRunner) restoreTPAfterHedge(ctx context.Context) {
	sr.mu.Lock()
	defer sr.mu.Unlock()
	if sr.strategy.Status != StatusActive {
		return
	}
	if err := sr.updateTPByType(ctx); err != nil {
		sr.warn(ctx, fmt.Sprintf("restoreTPAfterHedge: %v", err))
	}
}

// restoreSLAfterHedge re-places SL orders after a hedge deactivates SL suppression.
// For grid: re-places cycle-level SL. For matrix: re-places all filled per-level SLs.
// Must NOT be called with sr.mu held.
func (sr *StrategyRunner) restoreSLAfterHedge(ctx context.Context) {
	sr.mu.Lock()
	defer sr.mu.Unlock()
	if sr.strategy.Status != StatusActive {
		return
	}
	// Grid strategies: re-place cycle-level SL
	if sr.strategy.StrategyType != "matrix" {
		if err := sr.updateSL(ctx); err != nil {
			sr.warn(ctx, fmt.Sprintf("restoreSLAfterHedge (cycle): %v", err))
		}
		return
	}
	// Matrix strategies: re-place all per-level SLs for filled levels
	for i := range sr.levels {
		l := &sr.levels[i]
		if l.Status != LevelFilled || l.SLOrderID != "" || l.Slot == nil {
			continue
		}
		_, stopPct, _, _ := sr.matrixLevelConfig(*l.Slot)
		if stopPct == nil || l.FilledPrice <= 0 {
			continue
		}
		sr.matrixPlacePerLevelSL(ctx, l, l.FilledPrice, *stopPct)
	}
}
```

Note: check the actual import path for `trader` by looking at existing imports in `pkg/strategy/cycle.go`.

- [ ] **Step 2: Check the import path used by cycle.go**

Read the top of `pkg/strategy/cycle.go` to get the exact import path for `trader` package.

- [ ] **Step 3: Fix the import path in hedge_support.go**

Update the import to match the actual import path found in step 2. Also add the `context` import.

- [ ] **Step 4: Build to verify**

```bash
cd C:\Users\123\Projects\sis
go build ./pkg/strategy/...
```
Expected: no errors.

- [ ] **Step 5: Commit**

```bash
git add pkg/strategy/hedge_support.go
git commit -m "feat: hedge main controls — cancelTPForHedge, cancelSLForHedge, restoreTP/SLAfterHedge"
```

---

### Task 6: addStrategy Hooks in engine.go

**Files:**
- Modify: `pkg/strategy/engine.go`

- [ ] **Step 1: Add suppression detection in addStrategy**

In `addStrategy` (around line 663), the existing pattern captures previous values and then updates `existing.strategy = s`. 

The current code looks like:
```go
		prevStatus := existing.strategy.Status
		prevSignalFilter := existing.strategy.SignalFilter
		prevConfigsLen := len(existing.strategy.SignalConfigs)
		if existing.strategy.HedgeMode != s.HedgeMode {
			existing.positionModeVerified = false
		}
		existing.strategy = s
		existing.mu.Unlock()
		// Re-activate: ...
		if prevStatus != StatusActive && s.Status == StatusActive {
			existing.submit(func(ctx context.Context) { existing.loadOrStart(ctx) })
		}
		// Stop: ...
		if prevStatus != StatusStopped && s.Status == StatusStopped {
			existing.submit(func(ctx context.Context) { existing.handleStopRequest(ctx) })
		}
		// Signal filter ...
		if prevStatus == StatusActive && s.Status == StatusActive &&
			(prevSignalFilter != s.SignalFilter || len(s.SignalConfigs) != prevConfigsLen) {
			existing.submit(func(ctx context.Context) { existing.handleSignalConfigUpdate(ctx) })
		}
		return
```

Change to add the 4 suppression hooks:
```go
		prevStatus := existing.strategy.Status
		prevSignalFilter := existing.strategy.SignalFilter
		prevConfigsLen := len(existing.strategy.SignalConfigs)
		prevTpSuppressed := existing.strategy.HedgeTpSuppressed  // ADD
		prevSlSuppressed := existing.strategy.HedgeSlSuppressed  // ADD
		if existing.strategy.HedgeMode != s.HedgeMode {
			existing.positionModeVerified = false
		}
		existing.strategy = s
		existing.mu.Unlock()
		// Re-activate: runner was stopped/idle, now active again — start a fresh cycle.
		if prevStatus != StatusActive && s.Status == StatusActive {
			existing.submit(func(ctx context.Context) { existing.loadOrStart(ctx) })
		}
		// Stop: cancel placed L-orders but keep TP/SL so the cycle ends naturally.
		if prevStatus != StatusStopped && s.Status == StatusStopped {
			existing.submit(func(ctx context.Context) { existing.handleStopRequest(ctx) })
		}
		// Signal filter or configs changed while the strategy is running.
		if prevStatus == StatusActive && s.Status == StatusActive &&
			(prevSignalFilter != s.SignalFilter || len(s.SignalConfigs) != prevConfigsLen) {
			existing.submit(func(ctx context.Context) { existing.handleSignalConfigUpdate(ctx) })
		}
		// Hedge TP suppression activated — cancel existing TP immediately.  // ADD BLOCK
		if !prevTpSuppressed && s.HedgeTpSuppressed {
			existing.submit(func(ctx context.Context) { existing.cancelTPForHedge(ctx) })
		}
		// Hedge TP suppression cleared — re-place TP if strategy is active.
		if prevTpSuppressed && !s.HedgeTpSuppressed && s.Status == StatusActive {
			existing.submit(func(ctx context.Context) { existing.restoreTPAfterHedge(ctx) })
		}
		// Hedge SL suppression activated — cancel all existing SL orders immediately.
		if !prevSlSuppressed && s.HedgeSlSuppressed {
			existing.submit(func(ctx context.Context) { existing.cancelSLForHedge(ctx) })
		}
		// Hedge SL suppression cleared — re-place SL orders if strategy is active.
		if prevSlSuppressed && !s.HedgeSlSuppressed && s.Status == StatusActive {
			existing.submit(func(ctx context.Context) { existing.restoreSLAfterHedge(ctx) })
		}
		return
```

- [ ] **Step 2: Build to verify**

```bash
cd C:\Users\123\Projects\sis
go build ./pkg/strategy/...
```
Expected: no errors.

- [ ] **Step 3: Commit**

```bash
git add pkg/strategy/engine.go
git commit -m "feat: hedge main controls — addStrategy hooks for suppression detection"
```

---

### Task 7: applyHedgeMainControls + restoreHedgeMainControls in hedge_engine.go

**Files:**
- Modify: `services/api-gateway/hedge_engine.go`

- [ ] **Step 1: Add applyHedgeMainControls function to `services/api-gateway/hedge_engine.go`**

Add after the end of `checkHedgeActivation` (after line ~551):

```go
// applyHedgeMainControls sets suppression flags on the main strategy when a hedge activates.
// If HedgeStopMain: sets status='stopped' + tp/sl suppressed + hedge_stopped_by.
// If HedgeCancelMainTp/Sl (without stop): only sets suppression flags.
// In all cases notifies the strategy engine to react immediately.
func (s *Server) applyHedgeMainControls(ctx context.Context, botID, mainStrategyID, hedgeStrategyID string, cfg botCfgJSON) {
	if mainStrategyID == "" {
		return
	}
	if !cfg.HedgeCancelMainTp && !cfg.HedgeCancelMainSl && !cfg.HedgeStopMain {
		return
	}

	tpSuppressed := cfg.HedgeCancelMainTp || cfg.HedgeStopMain
	slSuppressed := cfg.HedgeCancelMainSl || cfg.HedgeStopMain

	var err error
	if cfg.HedgeStopMain {
		// Hard stop: set stopped status, suppress both, record which hedge stopped it.
		_, err = s.pool.Exec(ctx,
			`UPDATE strategies
			 SET status='stopped', hedge_tp_suppressed=true, hedge_sl_suppressed=true, hedge_stopped_by=$1
			 WHERE id=$2`,
			hedgeStrategyID, mainStrategyID)
	} else {
		// Only suppress TP and/or SL — do not stop the main strategy.
		_, err = s.pool.Exec(ctx,
			`UPDATE strategies
			 SET hedge_tp_suppressed=$1, hedge_sl_suppressed=$2
			 WHERE id=$3`,
			tpSuppressed, slSuppressed, mainStrategyID)
	}
	if err != nil {
		s.logBotEvent(ctx, botID,
			fmt.Sprintf("Хедж: ошибка применения управления Main (%s): %v", mainStrategyID[:8], err),
			"error", "hedge")
		return
	}

	go s.engine.Notify(context.Background(), mainStrategyID)
	s.logBotEvent(ctx, botID,
		fmt.Sprintf("Хедж: управление Main применено (tp_sup=%v sl_sup=%v stop=%v) → %s",
			tpSuppressed, slSuppressed, cfg.HedgeStopMain, mainStrategyID[:8]),
		"info", "hedge")
}
```

- [ ] **Step 2: Add restoreHedgeMainControls function**

Add after `applyHedgeMainControls`:

```go
// restoreHedgeMainControls clears suppression flags on the main strategy when a hedge deactivates.
// If the hedge had stopped the main (hedge_stopped_by = this hedge), also restores status='active'.
func (s *Server) restoreHedgeMainControls(ctx context.Context, botID, hedgeStrategyID string) {
	// Look up the main strategy that this hedge was covering.
	var mainStrategyID string
	if err := s.pool.QueryRow(ctx,
		`SELECT COALESCE(hedged_strategy_id::text,'') FROM strategies WHERE id=$1`,
		hedgeStrategyID).Scan(&mainStrategyID); err != nil || mainStrategyID == "" {
		return
	}

	// Check whether this hedge stopped the main strategy.
	var stoppedByID string
	if err := s.pool.QueryRow(ctx,
		`SELECT COALESCE(hedge_stopped_by::text,'') FROM strategies WHERE id=$1`,
		mainStrategyID).Scan(&stoppedByID); err != nil {
		return
	}

	var err error
	if stoppedByID == hedgeStrategyID {
		// We stopped the main — restore it to active and clear all suppression.
		_, err = s.pool.Exec(ctx,
			`UPDATE strategies
			 SET status='active', hedge_tp_suppressed=false, hedge_sl_suppressed=false, hedge_stopped_by=NULL
			 WHERE id=$1`,
			mainStrategyID)
	} else {
		// We only suppressed TP/SL — clear those flags.
		_, err = s.pool.Exec(ctx,
			`UPDATE strategies
			 SET hedge_tp_suppressed=false, hedge_sl_suppressed=false
			 WHERE id=$1`,
			mainStrategyID)
	}
	if err != nil {
		s.logBotEvent(ctx, botID,
			fmt.Sprintf("Хедж: ошибка восстановления Main (%s): %v", mainStrategyID[:8], err),
			"error", "hedge")
		return
	}

	go s.engine.Notify(context.Background(), mainStrategyID)
	s.logBotEvent(ctx, botID,
		fmt.Sprintf("Хедж: управление Main сброшено → %s (restored=%v)",
			mainStrategyID[:8], stoppedByID == hedgeStrategyID),
		"info", "hedge")
}
```

- [ ] **Step 3: Update the hedge activation call in checkHedgeActivation**

The current call at approximately line 539:
```go
			if err := s.createBotStrategy(ctx, b, cfg, pos.Symbol, hedgeDir, 0, mainStrategyID); err != nil {
				s.logBotEvent(ctx, botID,
					fmt.Sprintf("Хедж: ошибка создания %s %s: %v", pos.Symbol, hedgeDir, err),
					"error", "hedge")
			} else {
				s.logBotEvent(ctx, botID,
					fmt.Sprintf("Хедж: активирован %s %s (тип=%d, порог=%.4g)",
						pos.Symbol, hedgeDir, cfg.HedgeActType, cfg.HedgeActValue),
					"info", "hedge")
			}
```

Change to:
```go
			hedgeStrategyID, err := s.createBotStrategy(ctx, b, cfg, pos.Symbol, hedgeDir, 0, mainStrategyID)
			if err != nil {
				s.logBotEvent(ctx, botID,
					fmt.Sprintf("Хедж: ошибка создания %s %s: %v", pos.Symbol, hedgeDir, err),
					"error", "hedge")
			} else {
				s.logBotEvent(ctx, botID,
					fmt.Sprintf("Хедж: активирован %s %s (тип=%d, порог=%.4g)",
						pos.Symbol, hedgeDir, cfg.HedgeActType, cfg.HedgeActValue),
					"info", "hedge")
				// Apply main controls if configured and if a main strategy was identified.
				if hedgeStrategyID != "" {
					s.applyHedgeMainControls(ctx, botID, mainStrategyID, hedgeStrategyID, cfg)
				}
			}
```

- [ ] **Step 4: Update stopHedgeStrategy to call restoreHedgeMainControls**

The current `stopHedgeStrategy` function:
```go
func (s *Server) stopHedgeStrategy(ctx context.Context, botID, strategyID, symbol, reason string) {
	if _, err := s.pool.Exec(ctx,
		`UPDATE strategies SET status='stopped', updated_at=NOW() WHERE id=$1`,
		strategyID); err != nil {
		s.logBotEvent(ctx, botID,
			fmt.Sprintf("Хедж: %s — ошибка остановки стратегии: %v", symbol, err),
			"error", "hedge")
		return
	}
	go s.engine.Notify(context.Background(), strategyID)
	s.logBotEvent(ctx, botID,
		fmt.Sprintf("Хедж: %s — стратегия остановлена (%s)", symbol, reason),
		"info", "hedge")
}
```

Change to:
```go
func (s *Server) stopHedgeStrategy(ctx context.Context, botID, strategyID, symbol, reason string) {
	if _, err := s.pool.Exec(ctx,
		`UPDATE strategies SET status='stopped', updated_at=NOW() WHERE id=$1`,
		strategyID); err != nil {
		s.logBotEvent(ctx, botID,
			fmt.Sprintf("Хедж: %s — ошибка остановки стратегии: %v", symbol, err),
			"error", "hedge")
		return
	}
	go s.engine.Notify(context.Background(), strategyID)
	// Restore main strategy controls if a hedge was controlling it.
	s.restoreHedgeMainControls(ctx, botID, strategyID)
	s.logBotEvent(ctx, botID,
		fmt.Sprintf("Хедж: %s — стратегия остановлена (%s)", symbol, reason),
		"info", "hedge")
}
```

- [ ] **Step 5: Build both packages to verify**

```bash
cd C:\Users\123\Projects\sis
go build ./services/api-gateway/...
go build ./pkg/strategy/...
```
Expected: no errors.

- [ ] **Step 6: Commit**

```bash
git add services/api-gateway/hedge_engine.go
git commit -m "feat: hedge main controls — applyHedgeMainControls + restoreHedgeMainControls"
```

---

### Task 8: Full Build + Push

- [ ] **Step 1: Full project build**

```bash
cd C:\Users\123\Projects\sis
go build ./...
```
Expected: no errors.

- [ ] **Step 2: Run tests if any**

```bash
cd C:\Users\123\Projects\sis
go test ./...
```

- [ ] **Step 3: Push to origin**

```bash
git push origin master
```
