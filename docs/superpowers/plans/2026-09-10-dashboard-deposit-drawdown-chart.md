# Dashboard Deposit/Drawdown Chart Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the single-line "Кривая P&L" chart in the Dashboard's `HeroCard` with a three-line chart (Deposit / Free deposit / Drawdown) driven by real `equity` history from the existing `balance_snapshots` table, widen the center column, grow the card ~30% taller, and keep the old single-line chart as a fallback when no per-account equity history is available.

**Architecture:** Backend adds one new read-only aggregation (`equity_series`) to the existing `GetDashboard` handler, bucketed the same way as `daily_pnl` (`DATE_TRUNC` + `since`/`granularity`), reading the already-populated `balance_snapshots` table — no new data collection. Frontend merges `equity_series` onto the `daily_pnl` x-axis, derives `Deposit`/`FreeDeposit`/`Drawdown` client-side with a running-peak calculation, and renders them with a new SVG chart component that mirrors the existing axis/legend patterns already used in `PnLCurveCard` and `DailyBarsChart`.

**Tech Stack:** Go (`net/http`, `pgx/v5`) for `services/api-gateway`; React + TypeScript, hand-rolled SVG charts (no charting library) for `frontend/`.

**Design doc:** `docs/superpowers/specs/2026-09-10-dashboard-deposit-drawdown-chart-design.md` (approved) — authoritative for all formulas, colors, and layout decisions below.

---

### Task 1: Backend — `equity_series` field in `GetDashboard`

**Files:**
- Modify: `services/api-gateway/dashboard_handler.go`

- [ ] **Step 1: Add the `dashboardEquityPoint` type and the `EquitySeries` field on `dashboardResponse`**

In `services/api-gateway/dashboard_handler.go`, add a new type directly after the `dashboardRecentTrade` struct (currently ends at line 51, just before `type dashboardResponse struct {`):

```go
type dashboardEquityPoint struct {
	Day    string  `json:"day"`
	Equity float64 `json:"equity"`
}
```

Then add a field to `dashboardResponse` (currently lines 53-59):

```go
type dashboardResponse struct {
	Stats        dashboardPeriodStats   `json:"stats"`
	DailyPnL     []dashboardDayPnL      `json:"daily_pnl"`
	BotStats     []dashboardBotStat     `json:"bot_stats"`
	RecentTrades []dashboardRecentTrade `json:"recent_trades"`
	EquitySeries []dashboardEquityPoint `json:"equity_series"`
	Granularity  string                 `json:"granularity"` // "day" | "hour"
}
```

- [ ] **Step 2: Add the equity-series query block**

In `GetDashboard`, insert a new block after the "── 4. Recent trades ──" block (currently ends at line 251, right before `writeJSON(w, http.StatusOK, dashboardResponse{`):

```go
	// ── 5. Equity series (for the hero deposit/drawdown chart) ─────────────────
	// Only meaningful for a single selected account — balance_snapshots is written
	// per-account on GET /accounts/:id/balance, so there is no cross-account series to
	// bucket when accountID is empty (same "all accounts" limitation daily_pnl doesn't
	// have but the Equity widget already does).
	equitySeries := []dashboardEquityPoint{}
	if accountID != "" {
		trunc := "day"
		if granularity == "hour" {
			trunc = "hour"
		}
		args := []any{accountID}
		sinceSQL := ""
		if since != nil {
			sinceSQL = " AND created_at >= $2"
			args = append(args, *since)
		}
		rows, err := s.pool.Query(ctx, `
			SELECT bucket, equity FROM (
				SELECT DATE_TRUNC('`+trunc+`', created_at) AS bucket, equity,
					   ROW_NUMBER() OVER (
						   PARTITION BY DATE_TRUNC('`+trunc+`', created_at)
						   ORDER BY created_at DESC
					   ) AS rn
				FROM balance_snapshots
				WHERE account_id = $1`+sinceSQL+`
			) s WHERE rn = 1
			ORDER BY bucket ASC`,
			args...,
		)
		if err == nil {
			defer rows.Close()
			for rows.Next() {
				var bucket time.Time
				var entry dashboardEquityPoint
				if rows.Scan(&bucket, &entry.Equity) == nil {
					entry.Day = bucket.Format(time.RFC3339)
					equitySeries = append(equitySeries, entry)
				}
			}
		}
	}

```

Then update the final `writeJSON` call (currently lines 253-259) to include the new field:

```go
	writeJSON(w, http.StatusOK, dashboardResponse{
		Stats:        stats,
		DailyPnL:     dailyPnL,
		BotStats:     botStats,
		RecentTrades: recentTrades,
		EquitySeries: equitySeries,
		Granularity:  granularity,
	})
}
```

- [ ] **Step 3: Build to verify it compiles**

Run: `go build ./services/api-gateway/...`
Expected: no output, exit code 0.

- [ ] **Step 4: Commit**

```bash
git add services/api-gateway/dashboard_handler.go
git commit -m "feat(dashboard): add equity_series to GetDashboard for the deposit chart

Co-Authored-By: Claude Sonnet 5 <noreply@anthropic.com>"
```

---

### Task 2: Backend regression tests for `equity_series`

**Files:**
- Modify: `services/api-gateway/dashboard_handler_test.go`

This file already has `createTestAccountLabeled`, `seedTradeHistory`, and `getDashboardStats` helpers (see lines 18-60) — reuse them. `getDashboardStats` already decodes into `dashboardResponse`, so `resp.EquitySeries` is available with no changes needed there.

- [ ] **Step 1: Add a `seedBalanceSnapshot` helper**

Add this after `seedTradeHistory` (currently ends at line 44):

```go
func seedBalanceSnapshot(t *testing.T, s *Server, accountID string, equity float64, createdAt time.Time) {
	t.Helper()
	ctx := context.Background()
	var id string
	if err := s.pool.QueryRow(ctx,
		`INSERT INTO balance_snapshots (account_id, equity, created_at) VALUES ($1,$2,$3) RETURNING id`,
		accountID, equity, createdAt,
	).Scan(&id); err != nil {
		t.Fatalf("seed balance_snapshots: %v", err)
	}
	t.Cleanup(func() { s.pool.Exec(context.Background(), "DELETE FROM balance_snapshots WHERE id=$1", id) })
}
```

- [ ] **Step 2: Write the failing bucketing test**

Add at the end of the file:

```go
// TestGetDashboard_EquitySeries_BucketsByDayUsingLastSnapshot is the regression for the
// equity_series aggregation: two snapshots on the same day must collapse into one bucket,
// keeping the LATEST snapshot in that bucket (equity "as of end of bucket"), not an
// average or the first one.
func TestGetDashboard_EquitySeries_BucketsByDayUsingLastSnapshot(t *testing.T) {
	s := newTestServer(t)
	userID := createWHUser(t, s, "dasheq1")
	accID := createTestAccount(t, s, userID)

	now := time.Now().UTC()
	day := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC).AddDate(0, 0, -1)
	seedBalanceSnapshot(t, s, accID, 100.0, day.Add(9*time.Hour))
	seedBalanceSnapshot(t, s, accID, 105.5, day.Add(15*time.Hour))

	resp := getDashboardStats(t, s, userID, url.Values{"period": {"30d"}, "account_id": {accID}})
	if len(resp.EquitySeries) != 1 {
		t.Fatalf("EquitySeries = %+v, want exactly 1 bucket (both snapshots fall on the same day)", resp.EquitySeries)
	}
	if resp.EquitySeries[0].Equity != 105.5 {
		t.Errorf("Equity = %v, want 105.5 (the later of the two same-day snapshots)", resp.EquitySeries[0].Equity)
	}
}

// TestGetDashboard_EquitySeries_EmptyWithoutAccountID documents the intentional limitation:
// balance_snapshots is per-account, so there is no equity series to show in the "all
// accounts" aggregate view — the field must come back empty, not an error, so the frontend
// can fall back to the old cumulative P&L chart.
func TestGetDashboard_EquitySeries_EmptyWithoutAccountID(t *testing.T) {
	s := newTestServer(t)
	userID := createWHUser(t, s, "dasheq2")
	accID := createTestAccount(t, s, userID)
	seedBalanceSnapshot(t, s, accID, 100.0, time.Now().Add(-time.Hour))

	resp := getDashboardStats(t, s, userID, url.Values{"period": {"30d"}})
	if len(resp.EquitySeries) != 0 {
		t.Errorf("EquitySeries = %+v, want empty when no account_id is given (aggregate view)", resp.EquitySeries)
	}
}
```

- [ ] **Step 3: Run the new tests**

Run: `go test -tags=integration ./services/api-gateway/... -run TestGetDashboard_EquitySeries -v`
Expected: both tests `PASS`.

- [ ] **Step 4: Run the full existing dashboard test suite to confirm no regression**

Run: `go test -tags=integration ./services/api-gateway/... -run TestGetDashboard -v` and `go test -tags=integration ./services/api-gateway/... -run TestClearAccountStats -v`
Expected: all tests (the 2 new ones plus `TestGetDashboard_ScopesToAccountID`, `TestGetDashboard_WithoutAccountID_AggregatesAllAccounts`, `TestClearAccountStats_HidesOlderTradesButNotNewerOnes`) `PASS`.

- [ ] **Step 5: Commit**

```bash
git add services/api-gateway/dashboard_handler_test.go
git commit -m "test(dashboard): add regression tests for equity_series bucketing

Co-Authored-By: Claude Sonnet 5 <noreply@anthropic.com>"
```

---

### Task 3: Frontend — `EquityPoint` type and `equity_series` field

**Files:**
- Modify: `frontend/src/api/dashboard.ts`

- [ ] **Step 1: Add the `EquityPoint` interface and wire it into `DashboardData`**

Add after the `DailyPnL` interface (currently lines 15-20):

```ts
export interface EquityPoint {
  day: string
  equity: number
}
```

Update `DashboardData` (currently lines 42-49):

```ts
export interface DashboardData {
  stats: DashboardStats
  daily_pnl: DailyPnL[]
  bot_stats: BotStat[]
  recent_trades: RecentTrade[]
  equity_series: EquityPoint[]
  /** "day" for all periods except "1d"; "hour" for the 1-day period. */
  granularity: 'day' | 'hour'
}
```

- [ ] **Step 2: Type-check**

Run: `cd frontend && npx tsc --noEmit`
Expected: no new errors introduced by this file (pre-existing unrelated errors, if any, are out of scope — but there should be none here since nothing consumes `equity_series` yet).

- [ ] **Step 3: Commit**

```bash
git add frontend/src/api/dashboard.ts
git commit -m "feat(dashboard): add equity_series to the DashboardData API type

Co-Authored-By: Claude Sonnet 5 <noreply@anthropic.com>"
```

---

### Task 4: Frontend — `DepositChart` and `ChartLegend` components

**Files:**
- Modify: `frontend/src/pages/DashboardPage.tsx`

This task only adds new, self-contained components — it does not wire them into `HeroCard` yet (that's Task 5), so nothing changes visually after this task.

- [ ] **Step 1: Import `EquityPoint`**

Update the import at the top of the file (currently line 3):

```tsx
import { getDashboard, type DashboardData, type DailyPnL, type EquityPoint } from '../api/dashboard'
```

- [ ] **Step 2: Add the merge helper and the chart components**

Add this block right after the `DrawdownChart` function (currently ends at line 270, right before `// ─── Chart: Donut ───`):

```tsx
// ─── Deposit series: merge equity_series onto the daily_pnl x-axis ───────────
// equity_series is not guaranteed to have a snapshot in every daily_pnl bucket (a bucket
// might have trades but no balance fetch, or vice versa) — forward-fill from the last known
// snapshot, and back-fill any leading gap with the first known snapshot, so the resulting
// series is always fully defined and the line never has a gap.
function mergeEquityToDailyBuckets(dailyPnL: DailyPnL[], equitySeries: EquityPoint[]): number[] {
  const equityByDay = new Map(equitySeries.map(e => [e.day, e.equity]))
  let last = equitySeries.length > 0 ? equitySeries[0].equity : 0
  return dailyPnL.map(d => {
    const v = equityByDay.get(d.day)
    if (v != null) last = v
    return last
  })
}

// ─── Legend for the deposit/drawdown chart ────────────────────────────────────
function ChartLegend() {
  const items = [
    { label: 'Депозит', color: T.blue },
    { label: 'Свободный', color: T.green },
    { label: 'Просадка', color: T.red },
  ]
  return (
    <div style={{ display: 'flex', gap: 10, alignItems: 'center' }}>
      {items.map(it => (
        <span key={it.label} style={{ display: 'flex', alignItems: 'center', gap: 4, fontSize: 10, fontWeight: 600, color: T.dim }}>
          <span style={{ width: 7, height: 7, borderRadius: 2, background: it.color, display: 'inline-block' }} />
          {it.label}
        </span>
      ))}
    </div>
  )
}

// ─── Chart: Deposit / Free deposit / Drawdown (hero) ──────────────────────────
function DepositChart({ dailyPnL, equitySeries, granularity }: {
  dailyPnL: DailyPnL[]; equitySeries: EquityPoint[]; granularity: 'day' | 'hour'
}) {
  const id = useMemo(() => 'dep' + Math.random().toString(36).slice(2, 7), [])

  const deposit = useMemo(() => mergeEquityToDailyBuckets(dailyPnL, equitySeries), [dailyPnL, equitySeries])
  const { free, drawdown } = useMemo(() => {
    let peak = -Infinity
    const free: number[] = []
    const drawdown: number[] = []
    for (const v of deposit) {
      if (v > peak) peak = v
      const dd = peak - v
      drawdown.push(dd)
      free.push(v - dd)
    }
    return { free, drawdown }
  }, [deposit])

  if (deposit.length < 2) return <NoData height={140} />

  const W = 400, H = 210
  const pad = { t: 10, r: 8, b: 20, l: 40 }
  const ddH = 30, ddGap = 8
  const mainH = H - pad.t - pad.b - ddH - ddGap
  const mainTop = pad.t
  const ddTop = mainTop + mainH + ddGap
  const ddBottom = ddTop + ddH
  const cw = W - pad.l - pad.r

  const allMin = Math.min(...deposit, ...free)
  const allMax = Math.max(...deposit, ...free)
  const vRange = Math.max(allMax - allMin, 0.01)
  const yMin = allMin - vRange * 0.08
  const yMax = allMax + vRange * 0.08
  const yRange = yMax - yMin

  const maxDD = Math.max(...drawdown, 0.01)
  const step = deposit.length > 1 ? cw / (deposit.length - 1) : 0

  const toMainPts = (arr: number[]): [number, number][] =>
    arr.map((v, i) => [pad.l + i * step, mainTop + mainH - ((v - yMin) / yRange) * mainH])
  const depositPts = toMainPts(deposit)
  const freePts = toMainPts(free)
  const ddPts: [number, number][] = drawdown.map((v, i) => [pad.l + i * step, ddBottom - (v / maxDD) * ddH])

  const depositPath = smoothPath(depositPts)
  const freePath = smoothPath(freePts)
  const ddPath = smoothPath(ddPts)

  const yTicks = 3
  const lastIdx = dailyPnL.length - 1
  const xIdxs = [0, Math.floor(lastIdx * 0.25), Math.floor(lastIdx * 0.5), Math.floor(lastIdx * 0.75), lastIdx]
  const xLabels = [...new Set(xIdxs)].map(i => ({ i, label: periodShortLabel(dailyPnL[i].day, granularity) }))

  const clipId = id + 'c'
  return (
    <svg viewBox={`0 0 ${W} ${H}`} width="100%" preserveAspectRatio="none" style={{ display: 'block', height: '100%', flex: 1 }}>
      <defs>
        <clipPath id={clipId}>
          <rect x="0" y="0" width={0} height={H}>
            <animate attributeName="width" from={0} to={W} dur="1.1s"
              calcMode="spline" keySplines="0.25 0.46 0.45 0.94" fill="freeze" />
          </rect>
        </clipPath>
      </defs>
      {Array.from({ length: yTicks + 1 }).map((_, i) => {
        const v = yMin + (yRange * i) / yTicks
        const y = mainTop + mainH - ((v - yMin) / yRange) * mainH
        return (
          <g key={i}>
            <line x1={pad.l} x2={pad.l + cw} y1={y} y2={y} stroke={T.border} strokeDasharray="2 4" />
            <text x={pad.l - 6} y={y + 3} fill={T.faint} fontSize="9" fontFamily="'JetBrains Mono',monospace" textAnchor="end">
              {fmt$(v, 0)}
            </text>
          </g>
        )
      })}
      <g clipPath={`url(#${clipId})`}>
        <path d={freePath} fill="none" stroke={T.green} strokeWidth="1.6" strokeLinecap="round" strokeLinejoin="round" />
        <path d={depositPath} fill="none" stroke={T.blue} strokeWidth="2.2" strokeLinecap="round" strokeLinejoin="round" />
        <path d={ddPath} fill="none" stroke={T.red} strokeWidth="1.4" strokeLinecap="round" strokeLinejoin="round" />
      </g>
      {xLabels.map(({ i, label }) => (
        <text key={i} x={pad.l + i * step} y={H - 4} fill={T.faint} fontSize="9" fontFamily="'Inter',sans-serif" textAnchor="middle">{label}</text>
      ))}
    </svg>
  )
}

```

- [ ] **Step 3: Type-check**

Run: `cd frontend && npx tsc --noEmit`
Expected: no new errors (the new components are not referenced anywhere yet, but must still type-check standalone).

- [ ] **Step 4: Commit**

```bash
git add frontend/src/pages/DashboardPage.tsx
git commit -m "feat(dashboard): add DepositChart and ChartLegend components

Co-Authored-By: Claude Sonnet 5 <noreply@anthropic.com>"
```

---

### Task 5: Frontend — wire `DepositChart` into `HeroCard`, widen center column, grow card height

**Files:**
- Modify: `frontend/src/pages/DashboardPage.tsx`

- [ ] **Step 1: Replace the desktop `HeroCard` center column and grid ratio**

In `HeroCard` (desktop branch, currently lines 477-545), make two changes.

First, the root container's `gridTemplateColumns` and height (currently lines 477-486):

```tsx
  return (
    <div style={{
      flex: 1,
      background: 'linear-gradient(135deg,#131a30 0%,#16182d 40%,#1f1932 100%)',
      border: '1px solid rgba(123,140,255,.22)', borderRadius: 18,
      boxShadow: '0 28px 70px -32px rgba(91,140,255,.4)',
      position: 'relative', overflow: 'hidden',
      display: 'grid', gridTemplateColumns: 'minmax(0,0.8fr) minmax(0,2.6fr) minmax(0,0.8fr)',
      gridTemplateRows: '1fr', minHeight: 275,
    }}>
```

Second, the CENTER column body (currently lines 511-525):

```tsx
      {/* CENTER */}
      <div style={{ padding: '22px 4px 12px 12px', position: 'relative', display: 'flex', flexDirection: 'column' }}>
        <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'baseline', padding: '0 16px 8px', flexShrink: 0 }}>
          <Lbl>{data.equity_series.length > 0 ? 'Кривая депозита' : 'Кривая P&L'}</Lbl>
          {data.equity_series.length > 0
            ? <ChartLegend />
            : daily_pnl.length > 0 && (
              <span style={{ ...mono, fontSize: 11, color: T.dim }}>
                {periodFullLabel(daily_pnl[0].day, data.granularity)} — {periodFullLabel(daily_pnl[daily_pnl.length - 1].day, data.granularity)}
              </span>
            )
          }
        </div>
        {data.equity_series.length > 0
          ? <DepositChart dailyPnL={daily_pnl} equitySeries={data.equity_series} granularity={data.granularity} />
          : cumSeries.length >= 2
            ? <AreaChart data={cumSeries} width={540} height={170} color="#b8c8ff" fullHeight />
            : <NoData height={140} />
        }
      </div>
```

- [ ] **Step 2: Type-check**

Run: `cd frontend && npx tsc --noEmit`
Expected: no errors.

- [ ] **Step 3: Manual verification in the browser**

Follow the `webapp-testing` skill if available; otherwise start the frontend dev server and check manually:

```bash
cd frontend && npm run dev
```

With a specific account selected (not "all accounts"), on the Dashboard page, verify:
1. The hero card's center chart shows three lines (blue Deposit, green Free deposit, red Drawdown band at the bottom) with a legend and Y-axis $ ticks and 5 X-axis date ticks — not the old single blue area chart.
2. Switch through all periods (1д/7д/30д/90д/1г/all) — the chart updates and does not error or go blank.
3. Switch to "все аккаунты" (no account selected) — the chart falls back to the old single-line cumulative P&L chart (since `equity_series` is empty for the aggregate view), not a blank area.
4. The `HeroCard` is visibly taller than before, and `RecentTradesCard` to its right (desktop layout) still ends at the same bottom edge as `HeroCard` + `DailyStatsStrip` below it.
5. Mobile layout (narrow viewport) is unchanged — still the old single-line chart, since the mobile branch of `HeroCard` was not touched.

Report the outcome of each of these 5 checks explicitly before considering the task done.

- [ ] **Step 4: Commit**

```bash
git add frontend/src/pages/DashboardPage.tsx
git commit -m "feat(dashboard): show deposit/free-deposit/drawdown chart in HeroCard

Co-Authored-By: Claude Sonnet 5 <noreply@anthropic.com>"
```

---

## Post-implementation checklist (for the coordinating session, not a subagent task)

- Remind the user that `api-gateway` needs a rebuild/restart to pick up the Go changes (it runs via `go run` on their host — do not restart it yourself).
- Run `superpowers:finishing-a-development-branch` once all 5 tasks are done and reviewed.
