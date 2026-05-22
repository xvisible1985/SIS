# Matrix Strategy Engine — Design Spec

**Date:** 2026-05-22  
**Scope:** Backend engine for `strategy_type = "dca"` (Matrix). UI is complete. This spec covers Go + DB only.

---

## 1. What We're Building

A new execution engine for the Matrix strategy type. Unlike Grid (which places orders on one side only and uses a single global SL/TP), Matrix:

- Places orders **both above and below** the entry price
- Each filled level gets its **own independent SL order** (reduce-only, for that level's qty)
- TP is **global** (covers entire position), recalculated from the fill price of the latest filled level
- After a per-level SL fires, a **Safe Zone** is created; the slot can re-enter after the zone clears
- Levels "against the current price movement" use **Stop Market** or **Virtual** orders (not Limit)

---

## 2. Architecture

### New file
`pkg/strategy/matrix.go` — all matrix-specific logic. No matrix code in `cycle.go`.

### Branches in existing files
- `cycle.go / loadOrStart`: `if strategy_type == "dca"` → call `startMatrixCycle` or `loadMatrixCycle`
- `cycle.go / loadActiveCycle`: also load `sl_order_id`, `sl_price`, `sl_replaced`, `slot` per level; register per-level SL order IDs in `orderIndex` with type `"matrix_sl"`
- `engine.go / StrategyRunner`: two new fields (see below)

### New fields on StrategyRunner
```go
matrixSafeZone    *MatrixSafeZone      // nil = no active zone
matrixMonitorStop context.CancelFunc   // cancels the price-monitor goroutine
```

### New types (types.go)
```go
type MatrixSafeZone struct {
    Low, High float64
    CreatedAt time.Time
}
```

### Extended GridLevel (types.go)
```go
// added to existing struct:
SLOrderID  string
SLPrice    float64
SLReplaced bool
Slot       *int   // nil for grid; matrix slot index: -N … 0 … +N
```

---

## 3. DB Migration (040_matrix_sl.sql)

```sql
ALTER TABLE strategy_levels
  ADD COLUMN sl_order_id TEXT,
  ADD COLUMN sl_price    NUMERIC(18,8),
  ADD COLUMN sl_replaced BOOLEAN NOT NULL DEFAULT FALSE,
  ADD COLUMN slot        SMALLINT;
-- 'sl_closed' is now a valid value for the status column (TEXT, no constraint).
```

`slot` encodes the matrix position: `-2, -1, 0, 1, 2, …`  
Multiple rows with the same `slot` within one cycle are allowed (re-entries after SL).

---

## 4. Cycle Lifecycle

### 4.1 Starting a new matrix cycle (`startMatrixCycle`)

1. Cancel stale orders, close dust (same helpers as grid)
2. Close any open DB cycles (orphan guard)
3. Apply leverage, ensure position mode
4. Fetch current mark price → `startPrice` (= L(0) reference)
5. Insert cycle record
6. Build level rows from `MatrixLevels` config:

**Price calculation (Long example):**
```
L(0)   = startPrice              → Market order (immediate)
L(-1)  = startPrice × (1 − below[0].price_step_pct / 100)
L(-2)  = L(-1)      × (1 − below[1].price_step_pct / 100)   ← each step from previous
L(1)   = startPrice × (1 + above[0].price_step_pct / 100)
L(2)   = L(1)       × (1 + above[1].price_step_pct / 100)
Short: mirror (all orders Sell)
```

**Size per level:**  
`qty = level.size_pct / 100 × grid_size_usdt / targetPrice`  
(L(0) uses `matrix_entry_level.size_pct`)

7. Insert all levels into `strategy_levels` with `status='pending'`, `slot=N`
8. Place L(0) immediately as Market
9. Place remaining levels via `placeMatrixLevels`
10. Launch `matrixPriceMonitor` goroutine

### 4.2 Level placement decision (`placeMatrixLevel`)

```
For Long (Buy orders):
  targetPrice < currentPrice  → BUY LIMIT        (price hasn't dropped there yet)
  targetPrice > currentPrice  → BUY STOP MARKET  (or Virtual if order_type='virtual')

For Short (Sell orders): mirror logic.
```

Virtual levels: inserted to DB as `pending`, `exchange_order_id = NULL`. The price monitor fires them when price crosses `target_price`.

### 4.3 Loading an existing cycle (`loadMatrixCycle`)

Called from `loadOrStart` when `loadActiveCycle` succeeds and `strategy_type == "dca"`.  
Extra steps vs. grid:
- Read `sl_order_id`, `sl_price`, `sl_replaced`, `slot` for each level
- Register per-level SL IDs in `orderIndex` with `refType = "matrix_sl"`
- Restore `matrixSafeZone` — query the last `sl_closed` level's `sl_price`; if it was recent (< 1h) and price is still inside, rebuild the zone
- Re-launch `matrixPriceMonitor`

---

## 5. Per-Level SL and TP

### 5.1 When a level fills (`handleMatrixLevelFill`)

1. Mark level `status='filled'` in DB and memory
2. If `stop_pct != null`: place **reduce-only SL** for this level's qty
   - Long: `trigger = fill_price × (1 + stop_pct / 100)`  (stop_pct < 0 → below fill)
   - Short: `trigger = fill_price × (1 − stop_pct / 100)`
   - Store `sl_order_id` and `sl_price` in DB
   - Register in `orderIndex` with `refType = "matrix_sl"`
3. **Update global TP** (cancel old, place new):
   - Long:  `tp_price = fill_price × (1 + tp_pct / 100)`
   - Short: `tp_price = fill_price × (1 − tp_pct / 100)`
   - `qty = total filled qty across all active (non-sl_closed) levels`
4. If `stop_cond_pct != null`: monitor goroutine will watch this level

### 5.2 When a per-level SL fires (`handleMatrixSLFill`)

Triggered by `orderIndex` lookup → `refType == "matrix_sl"` → level ID.

1. Mark level `status='sl_closed'` in DB and memory  
   (level disappears from chart and active tracking)
2. Unregister SL order from `orderIndex`
3. Create Safe Zone:
   - `Low  = sl_trigger × (1 − safe_zone_pct / 100)`
   - `High = sl_trigger × (1 + safe_zone_pct / 100)`
   - Store in `sr.matrixSafeZone`
4. Recalculate and replace global TP:
   - Use the `tp_pct` and `fill_price` of the **latest non-sl_closed filled level** (most recent fill that is still active)
   - If no filled levels remain → cancel TP entirely
   - qty = total active filled qty
5. If `total_active_qty == 0` → close cycle (no position left)

### 5.3 Stop condition monitoring (in `matrixPriceMonitor`)

For each filled level where `sl_replaced == false` and `stop_cond_pct != null`:

```
Long:  threshold = fill_price × (1 + |stop_cond_pct| / 100)   [price rose from fill]
Short: threshold = fill_price × (1 − |stop_cond_pct| / 100)   [price fell from fill]

if currentPrice crossed threshold (in the profitable direction):
    cancel old SL (sl_order_id)
    Long:  new_sl_trigger = fill_price × (1 + stop_replace_pct / 100)
    Short: new_sl_trigger = fill_price × (1 − stop_replace_pct / 100)
    place new reduce-only SL at new_sl_trigger
    UPDATE strategy_levels SET sl_order_id=..., sl_price=..., sl_replaced=true
    re-register new SL in orderIndex
```

---

## 6. Safe Zone and Re-entry

### Safe Zone suppression
When placing any new level: if `targetPrice` falls within `[safeZone.Low, safeZone.High]` → skip (do not place).

### Safe Zone exit (in `matrixPriceMonitor`)
```
if safeZone != nil && (currentPrice < safeZone.Low || currentPrice > safeZone.High):
    sr.matrixSafeZone = nil
    → trigger re-placement check for all sl_closed slots
```

### Slot re-entry after SL
After Safe Zone clears, for each `slot` with no active level (no row with status `pending/placed/filled`):
- Compute target price fresh from config (same formula as initial start, relative to `cycle.StartPrice`)
- Call `placeMatrixLevel` for that slot
- Insert new `strategy_levels` row (new level_idx, same slot value)

---

## 7. Price Monitor Goroutine (`launchMatrixPriceMonitor`)

Runs for the lifetime of an active matrix cycle. Polls mark price every **5 seconds** via REST (simple, no new WS subscription needed for V1).

On each tick:
1. Check Safe Zone exit → clear if price outside bounds
2. Trigger re-placement for any sl_closed slots whose Safe Zone cleared
3. Check stop conditions for all filled levels (§5.3)
4. Trigger virtual level fills: if price crossed a virtual level's target → place Market order

Goroutine is stopped (`matrixMonitorStop()`) when cycle closes or strategy stops.

---

## 8. Cycle End Conditions

| Trigger | Action |
|---------|--------|
| Global TP fires | Cancel per-level SLs, close cycle, `maybeRestart` |
| All slots `sl_closed`, total qty = 0 | Close cycle, `maybeRestart` |
| Manual stop | Cancel all placed + SL orders, mark cycle ended |
| Strategy deleted | Cancel all, close cycle |

---

## 9. Reconcile

On `loadMatrixCycle` (service restart), `reconcileOrders` runs for matrix too:
- Per-level SL IDs are registered → existing reconcile detects missing SL → re-places or marks level `sl_closed`
- Missing entry level order → reset to `pending` or fill at market (same logic as grid)
- `matrixPriceMonitor` re-launches and re-checks all conditions

---

## 10. Out of Scope (V1)

- Virtual order type for **below** levels (V2)
- Safe Zone visualization on the chart (V2 — needs API endpoint returning zone bounds)
- Multiple simultaneous Safe Zones (V1: only the latest zone is tracked)
- `use_signal` per level (V2 — signal filter already works at cycle-start level)
