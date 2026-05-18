# Cycle Audit Modal Design

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task.

**Goal:** A real-time diagnostic modal on the strategy card that shows the backend's in-memory cycle state vs. live exchange state, highlighting discrepancies to diagnose missed fills.

**Architecture:** New backend REST endpoint combines DB state + Bybit live orders + Bybit position + in-memory orderIndex. Frontend polls every 4 seconds and renders a table with per-level flags.

**Tech Stack:** Go (backend endpoint), React + TypeScript (modal), existing Tailwind + dark theme patterns.

---

## Backend: New Endpoint

### `GET /strategies/{id}/cycle-audit`

Auth: JWT (same as all strategy endpoints). Only the owner can call it.

**Steps:**
1. Load active cycle from `strategy_cycles` (WHERE `strategy_id=$id AND ended_at IS NULL`)
2. Load all levels from `strategy_levels` (WHERE `cycle_id=$cycleID`)
3. Fetch live open orders from Bybit via `FetchOpenOrders` — build `map[orderID]bool`
4. Fetch current position from Bybit via `FetchPositions` — find matching symbol+side
5. Read `orderIndex` snapshot from the in-memory `AccountRunner` for this account — build `map[orderID]bool`
6. Compute per-level flags and qty discrepancy
7. Return JSON

**Response shape:**

```json
{
  "cycle_num": 3,
  "started_at": "2026-05-18T10:00:00Z",
  "position": {
    "size": "0.25",
    "avg_entry": "610.5",
    "side": "Buy",
    "unrealised_pnl": "1.2"
  },
  "expected_qty_from_fills": "0.25",
  "qty_discrepancy": "0.00",
  "levels": [
    {
      "idx": 0,
      "side": "Buy",
      "target_price": 620.0,
      "size_usdt": 30.0,
      "qty": "0.05",
      "db_status": "filled",
      "filled_price": 619.8,
      "exchange_order_id": "ord-abc123",
      "live_on_exchange": false,
      "in_order_index": false,
      "flag": "ok"
    }
  ],
  "tp": {
    "order_id": "tp-001",
    "price": 650.0,
    "live_on_exchange": true,
    "in_order_index": true,
    "flag": "ok"
  },
  "sl": {
    "order_id": "sl-001",
    "trigger_price": 580.0,
    "live_on_exchange": true,
    "in_order_index": true,
    "flag": "ok"
  },
  "no_active_cycle": false
}
```

If no active cycle: return `{ "no_active_cycle": true }`.

**Flag logic (per level):**

| Condition | Flag |
|---|---|
| `db_status == "filled"` | `ok` |
| `db_status == "pending"` | `pending` |
| `db_status == "placed"` AND `live_on_exchange == true` AND `in_order_index == true` | `ok` |
| `db_status == "placed"` AND `live_on_exchange == false` AND position delta suggests fill | `missing_fill` |
| `db_status == "placed"` AND `live_on_exchange == false` AND no position delta | `cancelled` |
| `db_status == "placed"` AND `live_on_exchange == true` AND `in_order_index == false` | `orphan` |

**Position delta heuristic for `missing_fill`:**
`qty_discrepancy = position.size - sum(qty of db_status="filled" levels)`.

For each level where `db_status="placed"` AND `live_on_exchange=false`:
- If `qty_discrepancy > 0.000001` → flag as `missing_fill` (position is larger than fills account for, so something was filled but not recorded)
- If `qty_discrepancy ≤ 0.000001` → flag as `cancelled` (position fully explained by known fills)

Note: multiple placed-but-missing levels are ALL flagged `missing_fill` when discrepancy > 0; the system cannot determine which specific one triggered it without order history.

**TP/SL flag logic:**
- `order_id == ""` → `not_placed`
- `live_on_exchange == true` AND `in_order_index == true` → `ok`
- `live_on_exchange == false` → `missing` (reconcile will re-place)
- `live_on_exchange == true` AND `in_order_index == false` → `orphan`

**Route registration:** `GET /strategies/{id}/cycle-audit` in `server.go`, handler in `strategy_handler.go`.

---

## Frontend: Modal Component

### Trigger

In `StrategyCard.tsx`: add a bug icon button (SVG insect/bug shape) alongside the existing Edit button. `onClick` sets `auditOpen = true`.

### New file: `frontend/src/components/strategies/CycleAuditModal.tsx`

**Props:**
```ts
interface Props {
  strategyId: string
  strategySymbol: string
  onClose: () => void
}
```

**State:**
- `data: CycleAuditData | null`
- `loading: boolean`
- `error: string | null`
- `lastUpdated: Date | null`

**Polling:** `useEffect` with `setInterval` at 4000ms. First fetch on mount. Clears interval on unmount.

**API call:** `GET /api/strategies/{id}/cycle-audit` via `apiClient`.

**New API function** in `frontend/src/api/strategies.ts`:
```ts
export async function getCycleAudit(id: string): Promise<CycleAuditData>
```

**New types** in `frontend/src/types.ts`:
```ts
export interface CycleAuditLevel {
  idx: number
  side: string
  target_price: number
  size_usdt: number
  qty: string
  db_status: string
  filled_price: number
  exchange_order_id: string
  live_on_exchange: boolean
  in_order_index: boolean
  flag: 'ok' | 'pending' | 'missing_fill' | 'cancelled' | 'orphan'
}

export interface CycleAuditTPSL {
  order_id: string
  price?: number
  trigger_price?: number
  live_on_exchange: boolean
  in_order_index: boolean
  flag: 'ok' | 'not_placed' | 'missing' | 'orphan'
}

export interface CycleAuditData {
  no_active_cycle?: boolean
  cycle_num: number
  started_at: string
  position: { size: string; avg_entry: string; side: string; unrealised_pnl: string } | null
  expected_qty_from_fills: string
  qty_discrepancy: string
  levels: CycleAuditLevel[]
  tp: CycleAuditTPSL | null
  sl: CycleAuditTPSL | null
}
```

### Layout

Modal overlay (fixed, z-50, dark backdrop). Panel: `max-w-3xl w-full`, dark background matching terminal panels (`bg-[#0d1220]`), `rounded-[16px]`, `border border-white/[.10]`.

**Header:** `Аудит цикла #N — SYMBOL` + last updated timestamp + spinning refresh indicator + close button.

**Summary row** (below header): three chips —
- `Позиция: 0.25 BUY @ 610.5`
- `Ожидаемо из fills: 0.20`
- `Дельта: +0.05` (yellow if non-zero, green if zero)

**Levels table columns:**
| # | Сторона | Цена цели | Кол-во | Статус БД | Цена исп. | Биржа | Память | Флаг |

**Column details:**
- `#`: `L0`, `L1`, etc.
- `Сторона`: `Buy`/`Sell` colored green/red
- `Цена цели`: formatted float
- `Кол-во`: qty string
- `Статус БД`: colored badge — pending=gray, placed=blue, filled=green, cancelled=red
- `Цена исп.`: filled_price or `—`
- `Биржа`: `✓` green if live, `✗` red if not (show `—` if status=pending or status=filled)
- `Память`: `✓` green if in orderIndex, `✗` red if not (same omission rule)
- `Флаг`: badge

**Flag badge colors:**
- `ok` → no badge (clean row)
- `pending` → gray `ожидание`
- `missing_fill` → orange `⚠ пропущен fill`
- `cancelled` → red `отменён`
- `orphan` → purple `orphan`

**Row highlight:** rows with `flag != 'ok' && flag != 'pending'` get subtle `bg-orange-500/[.08]` or `bg-red-500/[.08]` background.

**TP/SL section** (below levels table): two rows labeled `TP` and `SL` showing order_id, price, Биржа ✓/✗, Память ✓/✗, flag badge.

**No active cycle state:** centered message "Нет активного цикла".

**Error state:** red banner with error message.

---

## Files Changed

| File | Change |
|---|---|
| `services/api-gateway/strategy_handler.go` | Add `GetCycleAudit` handler |
| `services/api-gateway/server.go` | Register `GET /strategies/{id}/cycle-audit` route |
| `pkg/strategy/engine.go` | Add `SnapshotOrderIndex() map[string]bool` method on AccountRunner; add `GetAccountRunner(accountID string) *AccountRunner` on Engine |
| `services/api-gateway/server.go` | Store `*strategy.Engine` reference in Server struct (Engine already exists as the top-level coordinator); pass it from main.go |
| `frontend/src/types.ts` | Add `CycleAuditData`, `CycleAuditLevel`, `CycleAuditTPSL` types |
| `frontend/src/api/strategies.ts` | Add `getCycleAudit` function |
| `frontend/src/components/strategies/CycleAuditModal.tsx` | New modal component |
| `frontend/src/components/strategies/StrategyCard.tsx` | Add bug icon button, import and render CycleAuditModal |
