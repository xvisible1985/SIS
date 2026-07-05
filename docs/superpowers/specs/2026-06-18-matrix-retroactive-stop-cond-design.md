# Matrix Retroactive Stop-Cond SL Placement on Restart

**Date:** 2026-06-18  
**File:** `pkg/strategy/matrix.go`  
**Type:** Bug fix / reliability

## Problem

When the SIS server restarts, matrix strategy levels that had their `stop_cond_pct` threshold already crossed (but the server was down at the time) do not get their stop-loss orders placed retroactively.

**Root cause:** `matrixPriceTick` section 3 checks stop conditions on each real-time price tick. At startup it fires only for the current price — if price crossed the threshold _before_ the restart and has since stayed above the threshold, section 3 fires correctly. But if price crossed the threshold and then fell back below it while the server was down, the condition `condMet = (currentPrice >= threshold)` evaluates to `false` on every subsequent tick, and no SL is ever placed.

**Impact:** Filled matrix levels accumulate without per-position protection. The matrix bot also blocks taking the next level until the current level's stop is in place.

## Approach: Shared Helper + Startup Call

**Conservative** behavior chosen: retroactive SL is placed only if current price is still at or above the stop_cond threshold (same rule as live). Price that has already fallen below the threshold is not acted on at startup — the live monitor will handle it when price returns.

### Changes

**1. Extract section 3 of `matrixPriceTick` into `matrixApplyStopCondSLs`**

```go
// matrixApplyStopCondSLs checks all filled levels for stop-condition SL placement.
// Places a replacement SL when currentPrice has crossed the stop_cond threshold
// and no SL is already active for the level.
// Must be called with sr.mu held.
func (sr *StrategyRunner) matrixApplyStopCondSLs(ctx context.Context, currentPrice float64)
```

Body: verbatim move of current section 3 code (lines 819–901 in matrix.go). No logic changes.

**2. Replace section 3 body in `matrixPriceTick` with a call to the helper**

```go
// Section 3. Check stop conditions for filled levels.
sr.matrixApplyStopCondSLs(ctx, currentPrice)
```

**3. Call helper from `loadMatrixCycle` after `restoreMatrixWaitingSlots`**

```go
func (sr *StrategyRunner) loadMatrixCycle(ctx context.Context) error {
    if err := sr.loadActiveCycle(ctx); err != nil {
        return err
    }

    // Fetch mark price before taking lock — used for retroactive stop-cond check.
    price, err := trader.FetchMarkPrice(ctx, sr.runner.creds, sr.strategy.Category, sr.strategy.Symbol)
    if err != nil {
        log.Printf("strategy %s: loadMatrixCycle: fetch price for stop-cond check: %v", sr.strategy.ID, err)
    }

    sr.mu.Lock()
    sr.restoreMatrixWaitingSlots()
    if price > 0 {
        sr.matrixApplyStopCondSLs(ctx, price)
    }
    sr.mu.Unlock()

    // ... existing virtual L0 block ...
    sr.launchMatrixPriceMonitor()
    return nil
}
```

## Behaviour Matrix

| Scenario | Result |
|----------|--------|
| `FetchMarkPrice` fails at startup | Log warn; skip retroactive check; live monitor handles it on first tick |
| Current price ≥ stop_cond threshold | SL placed immediately during `loadMatrixCycle` |
| Current price < stop_cond threshold (price came back down while server was down) | No SL placed at startup; live monitor fires when price returns to threshold |
| Current price ≤ SL trigger (SL would fire immediately) | Skipped by the existing guard inside `matrixApplyStopCondSLs` |
| `SLReplaced = true` | Skipped — already protected |
| `HedgeSlSuppressed = true` | Skipped — same guard as in live section 3 |

## What Is Not Changed

- Section 4 of `matrixPriceTick` (initial `stopPct` retry) — untouched
- `restoreMatrixWaitingSlots` — untouched
- SL `linkID` format — unchanged (`SIS_STR-{id8}-msl-{slot}-{seq}`)
- DB schema — no migrations needed
- All other matrix logic — untouched
