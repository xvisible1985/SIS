# Cycle Audit Modal Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Real-time diagnostic modal on the strategy card showing per-level DB state vs. live exchange state vs. in-memory orderIndex, with computed flags to diagnose missed fills.

**Architecture:** New `GET /strategies/{id}/cycle-audit` endpoint combines DB state + Bybit live orders + Bybit position + in-memory `orderIndex` snapshot, computes flags, returns JSON. Frontend polls every 4 s and renders a table. New `CycleAuditModal.tsx` component; triggered by a new scope icon button in `StrategyCard.tsx` (separate from the existing `IcBug`/`DebugCycleWindow` button).

**Tech Stack:** Go (handler + engine methods), React + TypeScript (modal), existing Tailwind + dark theme.

---

## Files Changed

| File | Change |
|---|---|
| `pkg/strategy/engine.go` | Add `SnapshotOrderIndex()` on `AccountRunner`; add `GetAccountRunner()` on `Engine` |
| `services/api-gateway/strategy_handler.go` | Add `GetCycleAudit` handler + `computeTPSLFlag` helper |
| `services/api-gateway/main.go` | Register `GET /strategies/{id}/cycle-audit` route |
| `frontend/src/types.ts` | Add `CycleAuditLevel`, `CycleAuditTPSL`, `CycleAuditData` |
| `frontend/src/api/strategies.ts` | Add `getCycleAudit(id)` |
| `frontend/src/components/strategies/CycleAuditModal.tsx` | New modal component |
| `frontend/src/components/strategies/StrategyCard.tsx` | Add `IcScope` icon, `auditOpen` state, new button, render modal |

---

## Task 1: Backend engine methods

**Files:**
- Modify: `pkg/strategy/engine.go`

- [ ] **Step 1: Add `SnapshotOrderIndex` to `AccountRunner`**

Open `pkg/strategy/engine.go`. After the `newAccountRunner` function (around line 374), add:

```go
// SnapshotOrderIndex returns a copy of the in-memory order index as a set of orderIDs.
func (ar *AccountRunner) SnapshotOrderIndex() map[string]bool {
	ar.mu.RLock()
	defer ar.mu.RUnlock()
	snap := make(map[string]bool, len(ar.orderIndex))
	for id := range ar.orderIndex {
		snap[id] = true
	}
	return snap
}
```

- [ ] **Step 2: Add `GetAccountRunner` to `Engine`**

After the `GetSignalValues` method in `engine.go` (find it with grep), add:

```go
// GetAccountRunner returns the AccountRunner for the given account, or nil if not found.
func (e *Engine) GetAccountRunner(accountID string) *AccountRunner {
	e.mu.RLock()
	r := e.runners[accountID]
	e.mu.RUnlock()
	return r
}
```

- [ ] **Step 3: Build to verify no compilation errors**

Run: `cd c:\Users\123\Projects\sis && go build ./pkg/strategy/...`
Expected: exits 0 with no output.

- [ ] **Step 4: Commit**

```
git add pkg/strategy/engine.go
git commit -m "feat: add SnapshotOrderIndex and GetAccountRunner to strategy engine"
```

---

## Task 2: Backend handler + route

**Files:**
- Modify: `services/api-gateway/strategy_handler.go`
- Modify: `services/api-gateway/main.go`

- [ ] **Step 1: Add imports to strategy_handler.go**

The file currently imports `"context"`, `"encoding/json"`, `"fmt"`, `"math"`, `"net/http"`, `"strings"`, `"time"`, `"github.com/go-chi/chi/v5"`. Add `"strconv"` and `"sis/pkg/trader"` to the import block.

Find the import block and change it to:

```go
import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"sis/pkg/trader"
)
```

- [ ] **Step 2: Add `computeTPSLFlag` helper**

At the end of `strategy_handler.go`, add:

```go
func computeTPSLFlag(orderID string, live, inIndex bool) string {
	if orderID == "" {
		return "not_placed"
	}
	if live && inIndex {
		return "ok"
	}
	if !live {
		return "missing"
	}
	return "orphan"
}
```

- [ ] **Step 3: Add `GetCycleAudit` handler**

After `GetStrategyState` in `strategy_handler.go` (after the closing brace, around line 587), add the full handler:

```go
// GetCycleAudit returns a real-time audit snapshot of the active cycle:
// DB levels, live exchange orders, in-memory orderIndex, and computed flags.
// GET /strategies/{id}/cycle-audit
func (s *Server) GetCycleAudit(w http.ResponseWriter, r *http.Request) {
	userID := UserIDFromCtx(r.Context())
	id := chi.URLParam(r, "id")

	// 1. Verify ownership + load strategy meta.
	var accountID, symbol, category, direction string
	err := s.pool.QueryRow(r.Context(),
		`SELECT account_id, symbol, category, direction FROM strategies WHERE id=$1 AND owner_id=$2`,
		id, userID,
	).Scan(&accountID, &symbol, &category, &direction)
	if err != nil {
		writeError(w, http.StatusNotFound, "strategy not found")
		return
	}

	// 2. Load the active cycle.
	var cycleID, tpOrderID, slOrderID string
	var cycleNum int
	var startedAt time.Time
	err = s.pool.QueryRow(r.Context(),
		`SELECT id, cycle_num, started_at, COALESCE(tp_order_id,''), COALESCE(sl_order_id,'')
		 FROM strategy_cycles WHERE strategy_id=$1 AND ended_at IS NULL
		 ORDER BY cycle_num DESC LIMIT 1`,
		id,
	).Scan(&cycleID, &cycleNum, &startedAt, &tpOrderID, &slOrderID)
	if err != nil {
		// No active cycle.
		writeJSON(w, http.StatusOK, map[string]any{"no_active_cycle": true})
		return
	}

	// 3. Load DB levels.
	type levelRow struct {
		Idx             int     `json:"idx"`
		Side            string  `json:"side"`
		TargetPrice     float64 `json:"target_price"`
		SizeUSDT        float64 `json:"size_usdt"`
		Qty             string  `json:"qty"`
		DbStatus        string  `json:"db_status"`
		FilledPrice     float64 `json:"filled_price"`
		ExchangeOrderID string  `json:"exchange_order_id"`
		LiveOnExchange  bool    `json:"live_on_exchange"`
		InOrderIndex    bool    `json:"in_order_index"`
		Flag            string  `json:"flag"`
	}
	lrows, err := s.pool.Query(r.Context(),
		`SELECT level_idx, side, target_price, size_usdt, qty, status,
		        COALESCE(filled_price,0), COALESCE(exchange_order_id,'')
		 FROM strategy_levels WHERE cycle_id=$1 ORDER BY level_idx`,
		cycleID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "db error")
		return
	}
	var levels []levelRow
	for lrows.Next() {
		var l levelRow
		if lrows.Scan(&l.Idx, &l.Side, &l.TargetPrice, &l.SizeUSDT, &l.Qty,
			&l.DbStatus, &l.FilledPrice, &l.ExchangeOrderID) == nil {
			levels = append(levels, l)
		}
	}
	lrows.Close()
	if levels == nil {
		levels = []levelRow{}
	}

	// 4. Load exchange credentials.
	creds, err := s.loadCreds(r, accountID, userID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "creds error")
		return
	}

	// 5. Fetch live open orders.
	exchangeOrders, err := trader.FetchOpenOrders(r.Context(), creds)
	if err != nil {
		writeError(w, http.StatusBadGateway, "exchange error: "+err.Error())
		return
	}
	liveOrders := make(map[string]bool, len(exchangeOrders))
	for _, o := range exchangeOrders {
		liveOrders[o.OrderId] = true
	}

	// 6. Fetch current position.
	positions, err := trader.FetchPositions(r.Context(), creds)
	if err != nil {
		writeError(w, http.StatusBadGateway, "exchange error: "+err.Error())
		return
	}
	wantSide := "Buy"
	if direction == "short" {
		wantSide = "Sell"
	}
	var matchedPos *trader.Position
	for i := range positions {
		p := &positions[i]
		if p.Symbol == symbol && p.Side == wantSide {
			sz, _ := strconv.ParseFloat(p.Size, 64)
			if sz > 0 {
				matchedPos = p
				break
			}
		}
	}

	// 7. Snapshot in-memory orderIndex.
	var orderIndex map[string]bool
	if runner := s.engine.GetAccountRunner(accountID); runner != nil {
		orderIndex = runner.SnapshotOrderIndex()
	} else {
		orderIndex = map[string]bool{}
	}

	// 8. Compute qty discrepancy.
	var posSize float64
	if matchedPos != nil {
		posSize, _ = strconv.ParseFloat(matchedPos.Size, 64)
	}
	var filledQtySum float64
	for _, l := range levels {
		if l.DbStatus == "filled" {
			q, _ := strconv.ParseFloat(l.Qty, 64)
			filledQtySum += q
		}
	}
	qtyDiscrepancy := posSize - filledQtySum

	// 9. Compute per-level flags.
	for i := range levels {
		l := &levels[i]
		if l.ExchangeOrderID != "" {
			l.LiveOnExchange = liveOrders[l.ExchangeOrderID]
			l.InOrderIndex = orderIndex[l.ExchangeOrderID]
		}
		switch l.DbStatus {
		case "filled":
			l.Flag = "ok"
		case "pending":
			l.Flag = "pending"
		case "cancelled":
			l.Flag = "cancelled"
		case "placed":
			if l.LiveOnExchange && l.InOrderIndex {
				l.Flag = "ok"
			} else if l.LiveOnExchange && !l.InOrderIndex {
				l.Flag = "orphan"
			} else { // !l.LiveOnExchange
				if qtyDiscrepancy > 0.000001 {
					l.Flag = "missing_fill"
				} else {
					l.Flag = "cancelled"
				}
			}
		default:
			l.Flag = "ok"
		}
	}

	// 10. Build position output.
	type tpslOut struct {
		OrderID        string `json:"order_id"`
		LiveOnExchange bool   `json:"live_on_exchange"`
		InOrderIndex   bool   `json:"in_order_index"`
		Flag           string `json:"flag"`
	}
	var posOut any
	if matchedPos != nil {
		posOut = map[string]any{
			"size":           matchedPos.Size,
			"avg_entry":      matchedPos.EntryPrice,
			"side":           matchedPos.Side,
			"unrealised_pnl": matchedPos.UnrealisedPnl,
		}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"no_active_cycle":         false,
		"cycle_num":               cycleNum,
		"started_at":              startedAt,
		"position":                posOut,
		"expected_qty_from_fills": fmt.Sprintf("%.6f", filledQtySum),
		"qty_discrepancy":         fmt.Sprintf("%.6f", qtyDiscrepancy),
		"levels":                  levels,
		"tp": tpslOut{
			OrderID:        tpOrderID,
			LiveOnExchange: liveOrders[tpOrderID],
			InOrderIndex:   orderIndex[tpOrderID],
			Flag:           computeTPSLFlag(tpOrderID, liveOrders[tpOrderID], orderIndex[tpOrderID]),
		},
		"sl": tpslOut{
			OrderID:        slOrderID,
			LiveOnExchange: liveOrders[slOrderID],
			InOrderIndex:   orderIndex[slOrderID],
			Flag:           computeTPSLFlag(slOrderID, liveOrders[slOrderID], orderIndex[slOrderID]),
		},
	})
}
```

- [ ] **Step 4: Register route in main.go**

Find the block in `main.go` around line 134:
```go
r.Get("/strategies/{id}/state", s.GetStrategyState)
r.Get("/strategies/{id}/events", s.GetStrategyEvents)
```

Add one line after the `state` route:
```go
r.Get("/strategies/{id}/state", s.GetStrategyState)
r.Get("/strategies/{id}/cycle-audit", s.GetCycleAudit)
r.Get("/strategies/{id}/events", s.GetStrategyEvents)
```

- [ ] **Step 5: Build to verify no errors**

Run: `cd c:\Users\123\Projects\sis && go build ./services/api-gateway/...`
Expected: exits 0 with no output.

- [ ] **Step 6: Commit**

```
git add services/api-gateway/strategy_handler.go services/api-gateway/main.go
git commit -m "feat: add GET /strategies/{id}/cycle-audit endpoint"
```

---

## Task 3: Frontend types + API function

**Files:**
- Modify: `frontend/src/types.ts`
- Modify: `frontend/src/api/strategies.ts`

- [ ] **Step 1: Add types to types.ts**

Find the end of `frontend/src/types.ts` and append:

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

- [ ] **Step 2: Add getCycleAudit to api/strategies.ts**

Find the end of the functions section in `frontend/src/api/strategies.ts` (after `getStrategyEvents`) and add:

```ts
export async function getCycleAudit(id: string): Promise<CycleAuditData> {
  const res = await apiClient.get<CycleAuditData>(`/strategies/${id}/cycle-audit`)
  return res.data
}
```

Also add `CycleAuditData` to the import at the top of the file:
```ts
import type { Strategy, StrategyState, StrategyEvent, StrategyFormData, CycleAuditData } from '../types'
```

- [ ] **Step 3: Verify TypeScript**

Run: `cd c:\Users\123\Projects\sis\frontend && npx tsc --noEmit`
Expected: exits 0 with no errors.

- [ ] **Step 4: Commit**

```
git add frontend/src/types.ts frontend/src/api/strategies.ts
git commit -m "feat: add CycleAuditData types and getCycleAudit API function"
```

---

## Task 4: CycleAuditModal component

**Files:**
- Create: `frontend/src/components/strategies/CycleAuditModal.tsx`

- [ ] **Step 1: Create the modal file**

Create `frontend/src/components/strategies/CycleAuditModal.tsx` with the full content:

```tsx
import { useState, useEffect, useCallback } from 'react'
import { getCycleAudit } from '../../api/strategies'
import type { CycleAuditData } from '../../types'

interface Props {
  strategyId: string
  strategySymbol: string
  onClose: () => void
}

function FlagBadge({ flag }: { flag: string }) {
  if (flag === 'ok') return null
  const styles: Record<string, string> = {
    pending:      'bg-slate-500/20 text-slate-400 border-slate-500/30',
    missing_fill: 'bg-orange-500/20 text-orange-300 border-orange-500/30',
    cancelled:    'bg-red-500/20 text-red-300 border-red-500/30',
    orphan:       'bg-purple-500/20 text-purple-300 border-purple-500/30',
    missing:      'bg-red-500/20 text-red-300 border-red-500/30',
    not_placed:   'bg-slate-500/20 text-slate-400 border-slate-500/30',
  }
  const labels: Record<string, string> = {
    pending:      'ожидание',
    missing_fill: '⚠ пропущен fill',
    cancelled:    'отменён',
    orphan:       'orphan',
    missing:      'отсутствует',
    not_placed:   'не размещён',
  }
  const cls = styles[flag] ?? 'bg-slate-500/20 text-slate-400 border-slate-500/30'
  return (
    <span className={`text-[10px] px-1.5 py-[2px] rounded-[4px] border font-mono font-semibold ${cls}`}>
      {labels[flag] ?? flag}
    </span>
  )
}

function DbStatusBadge({ status }: { status: string }) {
  const styles: Record<string, string> = {
    pending:   'bg-slate-500/20 text-slate-400',
    placed:    'bg-blue-500/20 text-blue-300',
    filled:    'bg-emerald-500/20 text-emerald-300',
    cancelled: 'bg-red-500/20 text-red-400',
  }
  return (
    <span className={`text-[10px] px-1.5 py-[2px] rounded-[4px] font-mono font-semibold ${styles[status] ?? 'text-slate-400'}`}>
      {status}
    </span>
  )
}

export function CycleAuditModal({ strategyId, strategySymbol, onClose }: Props) {
  const [data, setData] = useState<CycleAuditData | null>(null)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [lastUpdated, setLastUpdated] = useState<Date | null>(null)
  const [spinning, setSpinning] = useState(false)

  const fetchData = useCallback(async () => {
    setSpinning(true)
    try {
      const d = await getCycleAudit(strategyId)
      setData(d)
      setLastUpdated(new Date())
      setError(null)
    } catch (e: any) {
      setError(e?.message ?? 'ошибка')
    } finally {
      setSpinning(false)
      setLoading(false)
    }
  }, [strategyId])

  useEffect(() => {
    fetchData()
    const t = setInterval(fetchData, 4000)
    return () => clearInterval(t)
  }, [fetchData])

  function rowBg(flag: string) {
    if (flag === 'missing_fill' || flag === 'orphan') return 'bg-orange-500/[.06]'
    if (flag === 'cancelled' || flag === 'missing') return 'bg-red-500/[.06]'
    return ''
  }

  function liveCell(live: boolean, status: string) {
    if (status === 'pending' || status === 'filled') return <span className="text-slate-600">—</span>
    return live
      ? <span className="text-emerald-400">✓</span>
      : <span className="text-red-400">✗</span>
  }

  function indexCell(inIndex: boolean, status: string) {
    if (status === 'pending' || status === 'filled') return <span className="text-slate-600">—</span>
    return inIndex
      ? <span className="text-emerald-400">✓</span>
      : <span className="text-red-400">✗</span>
  }

  const pos = data?.position
  const delta = data && !data.no_active_cycle ? parseFloat(data.qty_discrepancy) : 0
  const isNoCycle = data?.no_active_cycle

  return (
    <div
      className="fixed inset-0 z-50 flex items-center justify-center"
      style={{ background: 'rgba(0,0,0,.60)' }}
      onClick={onClose}
    >
      <div
        className="max-w-3xl w-full mx-4 flex flex-col rounded-[16px] border border-white/[.10]"
        style={{ background: '#0d1220', maxHeight: '85vh' }}
        onClick={e => e.stopPropagation()}
      >
        {/* Header */}
        <div className="flex items-center justify-between px-5 py-3 border-b border-white/[.08] shrink-0">
          <div className="flex items-center gap-3">
            <span className="text-[13px] font-semibold text-slate-200">
              Аудит цикла {data && !isNoCycle ? `#${data.cycle_num}` : ''} — {strategySymbol}
            </span>
            {spinning && (
              <svg className="animate-spin w-3 h-3 text-blue-400" viewBox="0 0 24 24" fill="none">
                <circle className="opacity-25" cx="12" cy="12" r="10" stroke="currentColor" strokeWidth="4" />
                <path className="opacity-75" fill="currentColor" d="M4 12a8 8 0 018-8V0C5.37 0 0 5.37 0 12h4z" />
              </svg>
            )}
            {lastUpdated && (
              <span className="text-[11px] text-slate-500">
                ↻ {lastUpdated.toLocaleTimeString('ru-RU')}
              </span>
            )}
          </div>
          <button
            onClick={onClose}
            className="w-6 h-6 flex items-center justify-center text-slate-500 hover:text-slate-200 text-[13px] rounded transition-colors"
          >
            ✕
          </button>
        </div>

        {/* Body */}
        <div className="flex-1 overflow-auto p-5 flex flex-col gap-4">
          {loading && (
            <div className="text-center text-slate-500 text-[12px] py-10">Загрузка…</div>
          )}

          {error && !loading && (
            <div className="px-3 py-2 rounded-[8px] bg-red-500/10 border border-red-500/20 text-red-300 text-[12px]">
              {error}
            </div>
          )}

          {!loading && isNoCycle && (
            <div className="text-center text-slate-500 text-[12px] py-10">Нет активного цикла</div>
          )}

          {!loading && data && !isNoCycle && (
            <>
              {/* Summary row */}
              <div className="flex gap-2 flex-wrap">
                <span className="px-3 py-1.5 rounded-[8px] bg-white/[.04] border border-white/[.08] text-[12px] font-mono text-slate-200">
                  Позиция: {pos ? `${pos.size} ${pos.side} @ ${pos.avg_entry}` : '—'}
                </span>
                <span className="px-3 py-1.5 rounded-[8px] bg-white/[.04] border border-white/[.08] text-[12px] font-mono text-slate-200">
                  Ожидаемо из fills: {data.expected_qty_from_fills}
                </span>
                <span className={`px-3 py-1.5 rounded-[8px] bg-white/[.04] border text-[12px] font-mono font-semibold ${
                  delta === 0
                    ? 'border-white/[.08] text-emerald-400'
                    : 'border-yellow-500/30 text-yellow-300'
                }`}>
                  Дельта: {delta >= 0 ? '+' : ''}{data.qty_discrepancy}
                </span>
              </div>

              {/* Levels table */}
              <div className="rounded-[10px] border border-white/[.08] overflow-hidden">
                <table className="w-full text-[11px] font-mono">
                  <thead>
                    <tr className="border-b border-white/[.06]" style={{ background: 'rgba(255,255,255,.03)' }}>
                      <th className="px-3 py-2 text-left text-[10px] text-slate-500 font-semibold uppercase tracking-wide">#</th>
                      <th className="px-3 py-2 text-left text-[10px] text-slate-500 font-semibold uppercase tracking-wide">Сторона</th>
                      <th className="px-3 py-2 text-right text-[10px] text-slate-500 font-semibold uppercase tracking-wide">Цена цели</th>
                      <th className="px-3 py-2 text-right text-[10px] text-slate-500 font-semibold uppercase tracking-wide">Кол-во</th>
                      <th className="px-3 py-2 text-center text-[10px] text-slate-500 font-semibold uppercase tracking-wide">Статус БД</th>
                      <th className="px-3 py-2 text-right text-[10px] text-slate-500 font-semibold uppercase tracking-wide">Цена исп.</th>
                      <th className="px-3 py-2 text-center text-[10px] text-slate-500 font-semibold uppercase tracking-wide">Биржа</th>
                      <th className="px-3 py-2 text-center text-[10px] text-slate-500 font-semibold uppercase tracking-wide">Память</th>
                      <th className="px-3 py-2 text-left text-[10px] text-slate-500 font-semibold uppercase tracking-wide">Флаг</th>
                    </tr>
                  </thead>
                  <tbody>
                    {data.levels.map(l => (
                      <tr key={l.idx} className={`border-b border-white/[.04] ${rowBg(l.flag)}`}>
                        <td className="px-3 py-2 text-slate-400">L{l.idx}</td>
                        <td className={`px-3 py-2 font-semibold ${l.side === 'Buy' ? 'text-emerald-400' : 'text-red-400'}`}>
                          {l.side}
                        </td>
                        <td className="px-3 py-2 text-right text-slate-200">{l.target_price.toFixed(2)}</td>
                        <td className="px-3 py-2 text-right text-slate-300">{l.qty}</td>
                        <td className="px-3 py-2 text-center"><DbStatusBadge status={l.db_status} /></td>
                        <td className="px-3 py-2 text-right text-slate-300">
                          {l.filled_price > 0 ? l.filled_price.toFixed(2) : '—'}
                        </td>
                        <td className="px-3 py-2 text-center">{liveCell(l.live_on_exchange, l.db_status)}</td>
                        <td className="px-3 py-2 text-center">{indexCell(l.in_order_index, l.db_status)}</td>
                        <td className="px-3 py-2"><FlagBadge flag={l.flag} /></td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>

              {/* TP/SL section */}
              {(data.tp || data.sl) && (
                <div className="rounded-[10px] border border-white/[.08] overflow-hidden">
                  <table className="w-full text-[11px] font-mono">
                    <thead>
                      <tr className="border-b border-white/[.06]" style={{ background: 'rgba(255,255,255,.03)' }}>
                        <th className="px-3 py-2 text-left text-[10px] text-slate-500 font-semibold uppercase tracking-wide">Тип</th>
                        <th className="px-3 py-2 text-left text-[10px] text-slate-500 font-semibold uppercase tracking-wide">ID ордера</th>
                        <th className="px-3 py-2 text-center text-[10px] text-slate-500 font-semibold uppercase tracking-wide">Биржа</th>
                        <th className="px-3 py-2 text-center text-[10px] text-slate-500 font-semibold uppercase tracking-wide">Память</th>
                        <th className="px-3 py-2 text-left text-[10px] text-slate-500 font-semibold uppercase tracking-wide">Флаг</th>
                      </tr>
                    </thead>
                    <tbody>
                      {[{ label: 'TP', item: data.tp }, { label: 'SL', item: data.sl }].map(({ label, item }) => {
                        if (!item) return null
                        return (
                          <tr key={label} className={`border-b border-white/[.04] ${rowBg(item.flag)}`}>
                            <td className={`px-3 py-2 font-semibold ${label === 'TP' ? 'text-emerald-400' : 'text-rose-400'}`}>
                              {label}
                            </td>
                            <td className="px-3 py-2 text-slate-400">
                              {item.order_id ? item.order_id.slice(0, 12) + '…' : '—'}
                            </td>
                            <td className="px-3 py-2 text-center">
                              {item.order_id
                                ? item.live_on_exchange
                                  ? <span className="text-emerald-400">✓</span>
                                  : <span className="text-red-400">✗</span>
                                : <span className="text-slate-600">—</span>
                              }
                            </td>
                            <td className="px-3 py-2 text-center">
                              {item.order_id
                                ? item.in_order_index
                                  ? <span className="text-emerald-400">✓</span>
                                  : <span className="text-red-400">✗</span>
                                : <span className="text-slate-600">—</span>
                              }
                            </td>
                            <td className="px-3 py-2"><FlagBadge flag={item.flag} /></td>
                          </tr>
                        )
                      })}
                    </tbody>
                  </table>
                </div>
              )}
            </>
          )}
        </div>
      </div>
    </div>
  )
}
```

- [ ] **Step 2: Verify TypeScript compiles**

Run: `cd c:\Users\123\Projects\sis\frontend && npx tsc --noEmit`
Expected: exits 0 with no errors.

- [ ] **Step 3: Commit**

```
git add frontend/src/components/strategies/CycleAuditModal.tsx
git commit -m "feat: add CycleAuditModal component"
```

---

## Task 5: Wire CycleAuditModal into StrategyCard

**Files:**
- Modify: `frontend/src/components/strategies/StrategyCard.tsx`

- [ ] **Step 1: Add import**

At line 8 in `StrategyCard.tsx`, after the existing `DebugCycleWindow` import, add:

```ts
import { CycleAuditModal } from './CycleAuditModal'
```

- [ ] **Step 2: Add `IcScope` icon definition**

After the `IcBug` icon definition (line 56), add:

```tsx
const IcScope  = (p: IcProps) => <Ic {...p}><circle cx="11" cy="11" r="7"/><path d="M21 21l-4-4"/><path d="M8 11h6M11 8v6"/></Ic>
```

(Magnifier with crosshair — distinct from the bug icon.)

- [ ] **Step 3: Add `auditOpen` state**

Find the existing `const [debugOpen, setDebugOpen] = useState(false)` line (around line 195) and add after it:

```ts
const [auditOpen, setAuditOpen] = useState(false)
```

- [ ] **Step 4: Add the audit icon button**

Find the existing `IcBug` button block (lines 459–466):
```tsx
<button
  type="button"
  onClick={e => { e.stopPropagation(); setDebugOpen(o => !o) }}
  className={`w-7 h-7 inline-flex items-center justify-center rounded-[7px] border transition-colors shrink-0 ${debugOpen ? 'bg-amber-400/[.15] border-amber-400/40 text-amber-400' : 'text-slate-500 bg-white/[.04] border-white/[.08] hover:bg-white/[.08]'}`}
  title="Debug cycle window"
>
  <IcBug s={13} w={1.6} />
</button>
```

Add the new audit button AFTER this block (before the `IcGear` button):

```tsx
<button
  type="button"
  onClick={e => { e.stopPropagation(); setAuditOpen(o => !o) }}
  className={`w-7 h-7 inline-flex items-center justify-center rounded-[7px] border transition-colors shrink-0 ${auditOpen ? 'bg-blue-400/[.15] border-blue-400/40 text-blue-400' : 'text-slate-500 bg-white/[.04] border-white/[.08] hover:bg-white/[.08]'}`}
  title="Аудит цикла"
>
  <IcScope s={13} w={1.6} />
</button>
```

- [ ] **Step 5: Render CycleAuditModal**

Find the `{debugOpen && (` block near the bottom of the component (around line 818):
```tsx
{debugOpen && (
  <DebugCycleWindow strategy={s} orders={strategyOrders} onClose={() => setDebugOpen(false)} liveSignal={liveSignal} />
)}
```

Add the audit modal render after it (before `</li>`):

```tsx
{auditOpen && (
  <CycleAuditModal strategyId={s.id} strategySymbol={s.symbol} onClose={() => setAuditOpen(false)} />
)}
```

- [ ] **Step 6: Verify TypeScript compiles**

Run: `cd c:\Users\123\Projects\sis\frontend && npx tsc --noEmit`
Expected: exits 0 with no errors.

- [ ] **Step 7: Commit**

```
git add frontend/src/components/strategies/StrategyCard.tsx
git commit -m "feat: add cycle audit icon button and modal to StrategyCard"
```
