# Hedge Flip on Main TP — Design Spec

**Date:** 2026-06-18  
**Status:** Approved for implementation

---

## Overview

When a main strategy closes at TP while a hedge strategy has an active open position, the hedge position is promoted to become the new main. The hedge bot immediately takes it under monitoring and can activate a new hedge if conditions are met again.

---

## Behavior

### Trigger condition

All of the following must be true simultaneously:

1. Main strategy cycle closes with `close_reason = 'tp'`
2. The associated hedge strategy has at least one filled level (position exists on exchange)
3. The hedge bot config has `hedge_tp_main_mode = 'flip'`

### Flip action (executed immediately, < 1 second)

1. Cancel all open matrix/DCA orders on the hedge strategy
2. Place a TP order on the hedge position at **±0.5% from current mark price** (+ for short hedge, − for long hedge). No SL.
3. `UPDATE strategies SET hedged_strategy_id = NULL` — hedge strategy becomes standalone main
4. Close the hedge session: `UPDATE hedge_sessions SET ended_at = NOW()`
5. Restore main controls on the former main (clear `hedge_tp_suppressed`, `hedge_sl_suppressed`) — no-op since main is already stopped, but keeps DB clean
6. Hedge engine immediately begins monitoring the promoted strategy as the new main

### Minimum lot edge case

When the hedge engine tries to activate a **new** hedge for the promoted main:

- Check if `position_qty * mark_price / leverage >= exchange_min_order_usdt`
- If position is **too small** → skip placing hedge orders, log `"position too small for hedge, monitoring only"`, continue monitoring on every tick
- When position grows (additional fills from DCA on the promoted strategy) → retry hedge activation normally

### What "Отменять" mode means (existing behavior, unchanged)

`hedge_cancel_main_tp = true`: the hedge bot suppresses the main's TP order while hedge is active. Main cannot close at TP while hedge is running. Paired-close logic governs exit.

---

## UI

**Location:** HedgeBotForm → вкладка «Стратегия» → подсекция «Управление мейн»

**Control:** Rename the existing «Отменять ТП на Main» toggle to **«ТП на Main»** and extend it from 2 to 2 semantically different states:

| State | Label | Behavior |
|-------|-------|----------|
| `cancel` (default) | Отменять | Existing behavior — TP on main is suppressed while hedge is active |
| `flip` | 🔄 Переворот | TP on main fires normally; on hit → hedge becomes new main |

**Tooltip (hover only, on `?` icon):**
- Отменять: *«ТП мейна снимается, пока хедж активен. Бот ждёт парного закрытия в направлении хеджа.»*
- Переворот: *«ТП мейна не снимается. При срабатывании — хедж-позиция становится новым мейном, выставляется TP +0.5%, без SL. Бот продолжает мониторинг.»*

No hint text visible by default — only on hover.

---

## Data model

### Bot strategy config (JSONB — no migration needed)

Add field `hedge_tp_main_mode string` with values `"cancel"` | `"flip"`. Default: `"cancel"` (preserves existing behavior for all existing bots).

The existing `hedge_cancel_main_tp bool` field maps to `hedge_tp_main_mode == "cancel"`. When serializing to the engine, derive:
- `HedgeCancelMainTp = (mode == "cancel")`
- `HedgeFlipOnMainTp = (mode == "flip")`

### No new columns on `strategies` table needed

The flip detection happens at cycle-close time via the signal channel. No DB flag needed on the strategy row.

---

## Technical approach (C): DB flag + channel wake

### Signal injection (`pkg/strategy/engine.go`)

```go
type Engine struct {
    // existing fields...
    OnMainTpClosed func(ctx context.Context, mainStrategyID string) // injected by hedge engine
}
```

### Cycle close (`pkg/strategy/cycle.go`)

In `closeCycle(ctx, "tp")`, after the cycle is recorded, fire the callback unconditionally if set — the hedge engine decides what to do (checks bot config and whether a hedge is active):

```go
if sr.engine.OnMainTpClosed != nil {
    sr.engine.OnMainTpClosed(ctx, sr.strategy.ID)
}
```

### Hedge engine (`services/api-gateway/hedge_engine.go`)

```go
type HedgeEngine struct {
    flipChan chan string  // mainStrategyID
}

func (s *Server) wireHedgeFlipCallback() {
    s.engine.OnMainTpClosed = func(ctx context.Context, mainStrategyID string) {
        select {
        case s.hedgeEngine.flipChan <- mainStrategyID:
        default: // channel full, will be picked up on next tick
        }
    }
}

// In hedge engine run loop:
select {
case <-ticker.C:
    s.hedgeEngineTick(ctx)
case mainID := <-s.hedgeEngine.flipChan:
    s.handleMainTpFlip(ctx, mainID)
case <-ctx.Done():
    return
}
```

### Flip handler (`handleMainTpFlip`)

```go
func (s *Server) handleMainTpFlip(ctx context.Context, mainStrategyID string) {
    // 1. Find active hedge strategy for this main
    hedge := findHedgeForMain(ctx, mainStrategyID)
    if hedge == nil { return }

    // 2. Check hedge bot config
    bot := loadBot(ctx, hedge.botID)
    if bot.StrategyConfig.HedgeTpMainMode != "flip" { return }

    // 3. Check hedge has open filled position
    if !s.hedgeHasOpenFilledLevels(ctx, hedge.id) { return }

    // 4. Cancel all open matrix orders on hedge strategy
    s.cancelAllHedgeOrders(ctx, hedge)

    // 5. Place TP at ±0.5%
    markPrice := s.getMarkPrice(hedge.symbol)
    // Short hedge profits when price falls → TP below current price
    // Long hedge profits when price rises  → TP above current price
    tpPrice := markPrice * (1 - 0.005)   // short hedge
    if hedge.direction == "long" { tpPrice = markPrice * (1 + 0.005) }
    s.placeHedgeTpOrder(ctx, hedge, tpPrice)

    // 6. Promote hedge to standalone main
    db.Exec(`UPDATE strategies SET hedged_strategy_id = NULL WHERE id = $1`, hedge.id)
    db.Exec(`UPDATE hedge_sessions SET ended_at = NOW() WHERE hedge_strategy_id = $1 AND ended_at IS NULL`, hedge.id)

    // 7. Restore main controls (cleanup only)
    s.restoreHedgeMainControls(ctx, hedge.botID, hedge.id, hedge.symbol)
}
```

---

## Files to change

| File | Change |
|------|--------|
| `pkg/strategy/engine.go` | Add `OnMainTpClosed func(ctx, strategyID string)` callback field |
| `pkg/strategy/cycle.go` | Call `OnMainTpClosed` in `closeCycle("tp")` if strategy is being hedged |
| `services/api-gateway/hedge_engine.go` | Add `flipChan`, `handleMainTpFlip()`, `wireHedgeFlipCallback()`, min-lot check |
| `services/api-gateway/main.go` | Call `wireHedgeFlipCallback()` at startup |
| `frontend/src/features/bots/components/HedgeBotForm.tsx` | Change «Отменять ТП на Main» toggle to 2-state «ТП на Main» with tooltip-only hints |
| `frontend/src/features/bots/types.ts` | Add `hedge_tp_main_mode: 'cancel' | 'flip'` to `StrategyConfig` |

---

## Out of scope

- Configurable flip TP % (fixed at 0.5%)
- Flip on SL close (only TP is supported)
- Flip counter / max flip limit
- UI indicator showing a strategy has been flipped N times
