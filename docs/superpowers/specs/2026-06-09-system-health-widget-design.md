# System Health Widget — Design Spec

**Date:** 2026-06-09  
**Author:** Claude (brainstorming session)

---

## Goal

Display real-time server health metrics (CPU, RAM, Disk, DB) to admins as compact colour-coded chips inside the existing `AdminUserPickerBar` in `TerminalPage.tsx`. Metrics are fetched from a new backend endpoint and polled every 10 seconds.

---

## Placement

The widget is embedded inside the existing `AdminUserPickerBar` component (44 px height, dark background, amber accent), visible only to admins. The chips fill the central `flex-1` empty space between the user-badge section and the user-picker button.

**Visual layout:**

```
[🛡 Admin] | [Alex ×] [acc1 ▼]      CPU 12%  RAM 47%  Disk 23%  DB ✓2.3G      [🔍 Выбрать пользователя]
```

No changes to layout height or other terminal components.

---

## Metrics

| Chip | Content | Source |
|------|---------|--------|
| CPU | `CPU 12%` | Background goroutine sampling `/proc/stat` every 10 s |
| RAM | `RAM 47%` | `/proc/meminfo` (MemTotal, MemAvailable) on each request |
| Disk | `Disk 23%` | `syscall.Statfs("/")` on each request |
| DB | `DB ✓2.3G` or `DB ✗` | `pool.Ping()` + `pg_database_size()` |
| DB growth | `+5M/д` appended to DB chip | Shown only when ≥ 50 MB/day |

---

## Colour Thresholds (CPU, RAM, Disk)

| Range | Colour (Tailwind) | Meaning |
|-------|-------------------|---------|
| < 60% | `text-emerald-400` | Healthy |
| 60–80% | `text-amber-400` | Warning |
| > 80% | `text-rose-400` | Critical |

DB status: `text-emerald-400` when `db_ok = true`, `text-rose-400` when `false`.

---

## Backend

### New file: `services/api-gateway/system_health.go`

**Exported API:**

```go
// StartSystemHealthMonitor launches CPU-sampling and DB-size-tracking goroutines.
// Must be called once at startup after the DB pool is ready.
func StartSystemHealthMonitor(pool *pgxpool.Pool)

// LatestSystemHealth returns the latest cached snapshot.
// Named distinctly from the (*Server).GetSystemHealth handler to avoid
// a same-package name collision in package main.
func LatestSystemHealth() SystemHealthSnapshot

type SystemHealthSnapshot struct {
    CpuPct          float64 `json:"cpu_pct"`
    RamUsedMB       uint64  `json:"ram_used_mb"`
    RamTotalMB      uint64  `json:"ram_total_mb"`
    RamPct          float64 `json:"ram_pct"`
    DiskUsedGB      float64 `json:"disk_used_gb"`
    DiskTotalGB     float64 `json:"disk_total_gb"`
    DiskPct         float64 `json:"disk_pct"`
    DbOk            bool    `json:"db_ok"`
    DbSizeMB        float64 `json:"db_size_mb"`
    DbGrowthMBPerDay float64 `json:"db_growth_mb_per_day"`
}
```

**CPU sampling goroutine:**
- Reads `/proc/stat` (line 0) at T=0 and T=1 s, computes `(idle₂−idle₁)/(total₂−total₁)`, stores `100 − idle%` as CPU pct.
- Runs loop every 10 s; first value is available after 1 s.
- Stores result in `var cpuPct float64` protected by `sync.RWMutex`.

**DB size tracking goroutine:**
- Queries `SELECT pg_database_size(current_database())` every 5 minutes.
- Keeps a ring buffer of up to 288 entries (24 h ÷ 5 min).
- `DbGrowthMBPerDay = (latest_bytes − oldest_bytes) / elapsed_hours * 24 / 1_048_576`.
- When fewer than 2 samples exist, `DbGrowthMBPerDay = 0`.

**`readMemInfo()` helper:**
- Reads `/proc/meminfo`, parses `MemTotal` and `MemAvailable` (kB).
- Returns `(totalMB, usedMB, pct)`.

**`readDiskInfo(path string)` helper:**
- Calls `syscall.Statfs(path, &stat)`.
- `total = stat.Blocks × stat.Bsize`, `free = stat.Bavail × stat.Bsize`.
- Returns `(totalGB, usedGB, pct)`.

**`LatestSystemHealth()` implementation:**
- Calls `readMemInfo()`, `readDiskInfo("/")`, reads `cpuPct` under RLock.
- Runs `pool.Ping(ctx)` with 2 s timeout; sets `DbOk`.
- If `DbOk`, queries `pg_database_size` synchronously (fast, single row).
- Returns assembled `SystemHealthSnapshot`.

No new external dependencies. Uses only `os`, `bufio`, `strconv`, `strings`, `syscall`, `sync`, `time`, `context`, `fmt`.

### New handler in `admin_handler.go`

```go
// GetSystemHealth returns a live server-health snapshot.
// GET /admin/system-health
func (s *Server) GetSystemHealth(w http.ResponseWriter, r *http.Request) {
    snap := LatestSystemHealth()
    writeJSON(w, http.StatusOK, snap)
}
```

### Route registration in `main.go`

Inside the existing `r.Group(func(r chi.Router) { r.Use(s.RequireAdmin) ... })` block:

```go
r.Get("/admin/system-health", s.GetSystemHealth)
```

`StartSystemHealthMonitor(s.db)` called once in the server startup section of `main.go`, after the DB pool is initialised.

---

## Frontend

### New type in `src/types.ts`

```typescript
export interface SystemHealthSnapshot {
  cpu_pct: number
  ram_used_mb: number
  ram_total_mb: number
  ram_pct: number
  disk_used_gb: number
  disk_total_gb: number
  disk_pct: number
  db_ok: boolean
  db_size_mb: number
  db_growth_mb_per_day: number
}
```

### New hook `src/hooks/useSystemHealth.ts`

```typescript
import { useEffect, useRef, useState } from 'react'
import { apiClient } from '../api/client'
import type { SystemHealthSnapshot } from '../types'

export function useSystemHealth() {
  const [data, setData] = useState<SystemHealthSnapshot | null>(null)
  const cancelledRef = useRef(false)

  useEffect(() => {
    cancelledRef.current = false

    async function fetch_() {
      try {
        const res = await apiClient.get<SystemHealthSnapshot>('/admin/system-health')
        if (!cancelledRef.current) setData(res.data)
      } catch { /* silent — chip disappears on error */ }
    }

    fetch_()
    const iv = setInterval(fetch_, 10_000)
    return () => { cancelledRef.current = true; clearInterval(iv) }
  }, [])

  return data
}
```

### Modified `TerminalPage.tsx`

**Add helper `SystemMetricChip`** (inline, near `AdminUserPickerBar`):

```typescript
function metricColor(pct: number): string {
  if (pct < 60) return 'text-emerald-400'
  if (pct < 80) return 'text-amber-400'
  return 'text-rose-400'
}

function SystemMetricChip({ label, pct, extra }: { label: string; pct: number; extra?: string }) {
  return (
    <span className="flex items-baseline gap-[3px]">
      <span className="text-[9px] font-bold uppercase tracking-[0.8px] text-slate-600">{label}</span>
      <span className={`text-[11px] font-semibold tabular-nums ${metricColor(pct)}`}>
        {Math.round(pct)}%{extra}
      </span>
    </span>
  )
}
```

**Inside `AdminUserPickerBar`:**
- Import and call `useSystemHealth()`.
- Replace `<div className="flex-1" />` with a flex row containing the chips and a shrinking spacer:

```typescript
const health = useSystemHealth()

// …inside the JSX, between user-badge section and user-picker button:
<div className="flex flex-1 items-center justify-center gap-3 min-w-0 px-2">
  {health && (
    <>
      <SystemMetricChip label="CPU"  pct={health.cpu_pct} />
      <SystemMetricChip label="RAM"  pct={health.ram_pct} />
      <SystemMetricChip label="Disk" pct={health.disk_pct} />
      {/* DB chip */}
      <span className="flex items-baseline gap-[3px]">
        <span className="text-[9px] font-bold uppercase tracking-[0.8px] text-slate-600">DB</span>
        <span className={`text-[11px] font-semibold ${health.db_ok ? 'text-emerald-400' : 'text-rose-400'}`}>
          {health.db_ok ? `✓${(health.db_size_mb / 1024).toFixed(1)}G` : '✗'}
        </span>
        {health.db_ok && health.db_growth_mb_per_day >= 50 && (
          <span className="text-[10px] text-slate-500">
            +{Math.round(health.db_growth_mb_per_day)}M/д
          </span>
        )}
      </span>
    </>
  )}
</div>
```

---

## Error Handling

- Backend: if `/proc/meminfo` or `syscall.Statfs` fails, the affected fields default to `0`. Handler never returns 500.
- Frontend: fetch errors are silently swallowed; chips simply don't appear until a successful response.
- CPU goroutine: if `/proc/stat` parsing fails, `cpuPct` remains `0` and is displayed as `0%`.

---

## Out of Scope

- Historical charts or sparklines
- Per-process memory breakdown
- Configurable thresholds
- Notifications / alerts
- Mobile layout changes (chips hidden on `<md` via `hidden md:flex` if needed)
