# System Health Widget — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Show live CPU / RAM / Disk / DB metrics as compact colour-coded chips inside the admin bar in `TerminalPage.tsx`, backed by a new `GET /admin/system-health` endpoint.

**Architecture:** Three Go files handle the backend: a platform-neutral common file (`system_health_common.go`) holds the struct, types, and pure parsing helpers; a Linux-only file (`system_health_linux.go`) holds OS access, goroutines, and the public API; a stub (`system_health_stub.go`) makes Windows dev builds compile. The HTTP handler calls `LatestSystemHealth()` which assembles the snapshot synchronously. The frontend polls `/admin/system-health` every 10 s via a small hook and renders chips inside the existing `AdminUserPickerBar`.

**Tech Stack:** Go stdlib (`os`, `syscall`, `strconv`, `strings`, `sync`, `context`, `math`, `time`), `pgxpool`, React 18, TypeScript, Tailwind CSS, axios.

---

## File map

| Action | Path | Build tag | Purpose |
|--------|------|-----------|---------|
| Create | `services/api-gateway/system_health_common.go` | none | Struct, pure helpers, `dbSample` — cross-platform |
| Create | `services/api-gateway/system_health_linux.go` | `linux` | OS reads, goroutines, `StartSystemHealthMonitor`, `LatestSystemHealth` |
| Create | `services/api-gateway/system_health_stub.go` | `!linux` | Stub `StartSystemHealthMonitor` + `LatestSystemHealth` for Windows dev |
| Create | `services/api-gateway/system_health_test.go` | none | Unit tests for pure helpers — runs on any platform |
| Modify | `services/api-gateway/admin_handler.go` | — | Add `GetSystemHealth` HTTP handler |
| Modify | `services/api-gateway/main.go` | — | Register route + call `StartSystemHealthMonitor` |
| Modify | `frontend/src/types.ts` | — | Add `SystemHealthSnapshot` interface |
| Create | `frontend/src/hooks/useSystemHealth.ts` | — | Polling hook |
| Modify | `frontend/src/pages/TerminalPage.tsx` | — | Add chips to `AdminUserPickerBar` |

---

## Task 1: Backend — common types + pure helpers (TDD)

**Files:**
- Create: `services/api-gateway/system_health_test.go`
- Create: `services/api-gateway/system_health_common.go`

- [ ] **Step 1: Write the failing tests**

Create `services/api-gateway/system_health_test.go`:

```go
package main

import (
	"testing"
	"time"
)

func TestParseProcStat(t *testing.T) {
	// Real-looking /proc/stat first line: cpu user nice system idle iowait irq softirq steal ...
	line := "cpu  2255 34 2290 22625563 6290 127 456 0 0 0"
	idle, total := parseProcStat(line)

	wantIdle := uint64(22625563 + 6290) // idle + iowait
	wantTotal := uint64(2255 + 34 + 2290 + 22625563 + 6290 + 127 + 456)
	if idle != wantIdle {
		t.Errorf("idle: got %d, want %d", idle, wantIdle)
	}
	if total != wantTotal {
		t.Errorf("total: got %d, want %d", total, wantTotal)
	}
}

func TestParseProcStatInvalidLine(t *testing.T) {
	idle, total := parseProcStat("not a cpu line")
	if idle != 0 || total != 0 {
		t.Errorf("expected (0,0) for invalid input, got (%d,%d)", idle, total)
	}
}

func TestParseMemInfo(t *testing.T) {
	content := `MemTotal:       16384000 kB
MemFree:         2048000 kB
MemAvailable:    8192000 kB
Buffers:          512000 kB
Cached:          4096000 kB
`
	totalKB, availKB := parseMemInfo(content)
	if totalKB != 16384000 {
		t.Errorf("totalKB: got %d, want 16384000", totalKB)
	}
	if availKB != 8192000 {
		t.Errorf("availKB: got %d, want 8192000", availKB)
	}
}

func TestParseMemInfoMissingFields(t *testing.T) {
	totalKB, availKB := parseMemInfo("Buffers: 1024 kB\n")
	if totalKB != 0 || availKB != 0 {
		t.Errorf("expected (0,0) for missing fields, got (%d,%d)", totalKB, availKB)
	}
}

func TestCalcGrowthMBPerDay(t *testing.T) {
	now := time.Now()
	samples := []dbSample{
		{t: now.Add(-24 * time.Hour), bytes: 1_000_000_000}, // 1 GB 24 h ago
		{t: now, bytes: 1_100_000_000},                      // 1.1 GB now → +100 MB/day
	}
	got := calcGrowthMBPerDay(samples)
	want := 100.0
	if got < want-1.0 || got > want+1.0 {
		t.Errorf("growth: got %.2f MB/day, want ~%.2f", got, want)
	}
}

func TestCalcGrowthMBPerDayFewSamples(t *testing.T) {
	if got := calcGrowthMBPerDay(nil); got != 0 {
		t.Errorf("nil samples: got %.2f, want 0", got)
	}
	single := []dbSample{{t: time.Now(), bytes: 500_000_000}}
	if got := calcGrowthMBPerDay(single); got != 0 {
		t.Errorf("single sample: got %.2f, want 0", got)
	}
}
```

- [ ] **Step 2: Run — confirm failure (types undefined)**

```bash
cd C:/Users/123/Projects/sis
go test -run "TestParseProcStat|TestParseMemInfo|TestCalcGrowth" ./services/api-gateway/
```

Expected: compilation error — `parseProcStat`, `parseMemInfo`, `calcGrowthMBPerDay`, `dbSample` undefined.

- [ ] **Step 3: Create `services/api-gateway/system_health_common.go`**

```go
package main

import (
	"strconv"
	"strings"
)

// SystemHealthSnapshot is returned by GET /admin/system-health.
type SystemHealthSnapshot struct {
	CpuPct           float64 `json:"cpu_pct"`
	RamUsedMB        uint64  `json:"ram_used_mb"`
	RamTotalMB       uint64  `json:"ram_total_mb"`
	RamPct           float64 `json:"ram_pct"`
	DiskUsedGB       float64 `json:"disk_used_gb"`
	DiskTotalGB      float64 `json:"disk_total_gb"`
	DiskPct          float64 `json:"disk_pct"`
	DbOk             bool    `json:"db_ok"`
	DbSizeMB         float64 `json:"db_size_mb"`
	DbGrowthMBPerDay float64 `json:"db_growth_mb_per_day"`
}

// dbSample is one DB-size measurement used to compute the growth rate.
type dbSample struct {
	t     time.Time
	bytes int64
}

// parseProcStat parses the aggregate "cpu" line from /proc/stat.
// Returns (idleJiffies, totalJiffies); both 0 on parse error.
func parseProcStat(line string) (idle, total uint64) {
	parts := strings.Fields(line)
	// Expect: "cpu" user nice system idle iowait irq softirq [steal guest ...]
	if len(parts) < 5 || parts[0] != "cpu" {
		return 0, 0
	}
	var vals [10]uint64
	for i := 1; i < len(parts) && i <= 10; i++ {
		v, _ := strconv.ParseUint(parts[i], 10, 64)
		vals[i-1] = v
	}
	idle = vals[3] + vals[4] // idle + iowait
	for _, v := range vals {
		total += v
	}
	return
}

// parseMemInfo parses /proc/meminfo text content.
// Returns (MemTotal kB, MemAvailable kB); both 0 on parse error.
func parseMemInfo(content string) (totalKB, availKB uint64) {
	for _, line := range strings.Split(content, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		switch fields[0] {
		case "MemTotal:":
			totalKB, _ = strconv.ParseUint(fields[1], 10, 64)
		case "MemAvailable:":
			availKB, _ = strconv.ParseUint(fields[1], 10, 64)
		}
	}
	return
}

// calcGrowthMBPerDay returns the rolling DB growth in MB/day from the ring buffer.
// Returns 0 when fewer than 2 samples are available.
func calcGrowthMBPerDay(samples []dbSample) float64 {
	if len(samples) < 2 {
		return 0
	}
	oldest := samples[0]
	latest := samples[len(samples)-1]
	delta := latest.bytes - oldest.bytes
	elapsed := latest.t.Sub(oldest.t).Hours()
	if elapsed < 0.1 {
		return 0
	}
	return float64(delta) / elapsed * 24 / (1024 * 1024)
}
```

Notice: `time.Time` is used in `dbSample` but `time` is not imported — add the missing import. The full import block:

```go
import (
	"strconv"
	"strings"
	"time"
)
```

- [ ] **Step 4: Run the tests — confirm they pass**

```bash
cd C:/Users/123/Projects/sis
go test -run "TestParseProcStat|TestParseMemInfo|TestCalcGrowth" ./services/api-gateway/ -v
```

Expected:
```
--- PASS: TestParseProcStat (0.00s)
--- PASS: TestParseProcStatInvalidLine (0.00s)
--- PASS: TestParseMemInfo (0.00s)
--- PASS: TestParseMemInfoMissingFields (0.00s)
--- PASS: TestCalcGrowthMBPerDay (0.00s)
--- PASS: TestCalcGrowthMBPerDayFewSamples (0.00s)
PASS
```

- [ ] **Step 5: Commit**

```bash
cd C:/Users/123/Projects/sis
git add services/api-gateway/system_health_common.go services/api-gateway/system_health_test.go
git commit -m "feat: system_health common types and pure helper tests"
```

---

## Task 2: Backend — Linux implementation + Windows stub

**Files:**
- Create: `services/api-gateway/system_health_linux.go`
- Create: `services/api-gateway/system_health_stub.go`

- [ ] **Step 1: Create `services/api-gateway/system_health_linux.go`**

```go
//go:build linux

package main

import (
	"context"
	"fmt"
	"math"
	"os"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// ── Package-level state ────────────────────────────────────────────────────────

const maxDBSamples = 288 // 24 h / 5 min

var (
	shmMu      sync.RWMutex
	shmCpuPct  float64
	shmPool    *pgxpool.Pool
	shmSamples []dbSample
)

// ── OS-access helpers ─────────────────────────────────────────────────────────

func readCPUStat() (idle, total uint64, err error) {
	data, err := os.ReadFile("/proc/stat")
	if err != nil {
		return 0, 0, err
	}
	for _, line := range strings.SplitN(string(data), "\n", 5) {
		if strings.HasPrefix(line, "cpu ") {
			idle, total = parseProcStat(line)
			return
		}
	}
	return 0, 0, fmt.Errorf("cpu line not found in /proc/stat")
}

func readSysMemInfo() (totalMB, usedMB uint64, pct float64) {
	data, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return
	}
	totalKB, availKB := parseMemInfo(string(data))
	if totalKB == 0 {
		return
	}
	totalMB = totalKB / 1024
	usedMB = (totalKB - availKB) / 1024
	pct = float64(totalKB-availKB) / float64(totalKB) * 100
	return
}

func readDiskInfo(path string) (totalGB, usedGB, pct float64) {
	var stat syscall.Statfs_t
	if err := syscall.Statfs(path, &stat); err != nil || stat.Bsize <= 0 {
		return
	}
	bsize := uint64(stat.Bsize)
	total := stat.Blocks * bsize
	avail := stat.Bavail * bsize
	if total == 0 {
		return
	}
	used := total - avail
	gb := float64(1 << 30)
	totalGB = float64(total) / gb
	usedGB = float64(used) / gb
	pct = float64(used) / float64(total) * 100
	return
}

// ── Background goroutines ─────────────────────────────────────────────────────

func cpuSamplerLoop() {
	for {
		idle1, total1, err := readCPUStat()
		if err != nil || total1 == 0 {
			time.Sleep(10 * time.Second)
			continue
		}
		time.Sleep(time.Second)
		idle2, total2, err2 := readCPUStat()
		if err2 == nil && total2 > total1 {
			deltaTotal := total2 - total1
			deltaIdle := idle2 - idle1
			usage := 100.0 * float64(deltaTotal-deltaIdle) / float64(deltaTotal)
			shmMu.Lock()
			shmCpuPct = usage
			shmMu.Unlock()
		}
		time.Sleep(9 * time.Second)
	}
}

func sampleDBSize(ctx context.Context) {
	shmMu.RLock()
	pool := shmPool
	shmMu.RUnlock()
	if pool == nil {
		return
	}
	var sizeBytes int64
	qctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	if err := pool.QueryRow(qctx, `SELECT pg_database_size(current_database())`).Scan(&sizeBytes); err != nil {
		return
	}
	shmMu.Lock()
	defer shmMu.Unlock()
	shmSamples = append(shmSamples, dbSample{t: time.Now(), bytes: sizeBytes})
	if len(shmSamples) > maxDBSamples {
		shmSamples = shmSamples[len(shmSamples)-maxDBSamples:]
	}
}

func dbSamplerLoop(ctx context.Context) {
	sampleDBSize(ctx) // immediate first sample
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			sampleDBSize(ctx)
		}
	}
}

// ── Public API ─────────────────────────────────────────────────────────────────

// StartSystemHealthMonitor launches the CPU-sampling and DB-size-tracking goroutines.
// Call once at startup after the DB pool is ready.
func StartSystemHealthMonitor(ctx context.Context, pool *pgxpool.Pool) {
	shmMu.Lock()
	shmPool = pool
	shmMu.Unlock()
	go cpuSamplerLoop()
	go dbSamplerLoop(ctx)
}

// LatestSystemHealth assembles and returns the current system health snapshot.
func LatestSystemHealth() SystemHealthSnapshot {
	shmMu.RLock()
	cpuPct := shmCpuPct
	samples := make([]dbSample, len(shmSamples))
	copy(samples, shmSamples)
	pool := shmPool
	shmMu.RUnlock()

	totalMB, usedMB, ramPct := readSysMemInfo()
	totalGB, usedGB, diskPct := readDiskInfo("/")

	round1 := func(v float64) float64 { return math.Round(v*10) / 10 }

	snap := SystemHealthSnapshot{
		CpuPct:           round1(cpuPct),
		RamUsedMB:        usedMB,
		RamTotalMB:       totalMB,
		RamPct:           round1(ramPct),
		DiskUsedGB:       round1(usedGB),
		DiskTotalGB:      round1(totalGB),
		DiskPct:          round1(diskPct),
		DbGrowthMBPerDay: round1(calcGrowthMBPerDay(samples)),
	}

	if pool != nil {
		pingCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		snap.DbOk = pool.Ping(pingCtx) == nil
	}

	if len(samples) > 0 {
		snap.DbSizeMB = round1(float64(samples[len(samples)-1].bytes) / (1024 * 1024))
	} else if snap.DbOk && pool != nil {
		// Before the first 5-min tick: query directly once.
		qctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		var sizeBytes int64
		if pool.QueryRow(qctx, `SELECT pg_database_size(current_database())`).Scan(&sizeBytes) == nil {
			snap.DbSizeMB = round1(float64(sizeBytes) / (1024 * 1024))
		}
	}

	return snap
}
```

- [ ] **Step 2: Create `services/api-gateway/system_health_stub.go`**

```go
//go:build !linux

package main

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"
)

// StartSystemHealthMonitor is a no-op on non-Linux platforms.
func StartSystemHealthMonitor(_ context.Context, _ *pgxpool.Pool) {}

// LatestSystemHealth returns a zero snapshot on non-Linux platforms.
func LatestSystemHealth() SystemHealthSnapshot { return SystemHealthSnapshot{} }
```

- [ ] **Step 3: Build to confirm no errors (Windows)**

```bash
cd C:/Users/123/Projects/sis
go build ./services/api-gateway/
```

Expected: no errors, binary produced.

- [ ] **Step 4: Commit**

```bash
cd C:/Users/123/Projects/sis
git add services/api-gateway/system_health_linux.go services/api-gateway/system_health_stub.go
git commit -m "feat: system_health Linux implementation and Windows stub"
```

---

## Task 3: Backend — HTTP handler + route registration

**Files:**
- Modify: `services/api-gateway/admin_handler.go`
- Modify: `services/api-gateway/main.go`

- [ ] **Step 1: Add `GetSystemHealth` to `admin_handler.go`**

Open `services/api-gateway/admin_handler.go`. After the closing `}` of `GetAdminMetrics` (around line 79), add:

```go
// GetSystemHealth returns a live server-health snapshot (CPU, RAM, Disk, DB).
// GET /admin/system-health
func (s *Server) GetSystemHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, LatestSystemHealth())
}
```

No new imports needed — `writeJSON` is already in scope.

- [ ] **Step 2: Register route in `main.go`**

Open `services/api-gateway/main.go`. Find the admin-only `r.Group` block containing `r.Use(s.RequireAdmin)` (around line 266). Add inside that block, after the `// Admin: log visualizer` section:

```go
// Admin: system health
r.Get("/admin/system-health", s.GetSystemHealth)
```

- [ ] **Step 3: Call `StartSystemHealthMonitor` in `main.go`**

In `main.go`, after the line `bootstrapAdmins(ctx, pool, adminEmails)` (around line 81), add:

```go
// Start system health monitor (CPU sampler every 10 s, DB size tracker every 5 min).
StartSystemHealthMonitor(ctx, pool)
```

- [ ] **Step 4: Build to confirm no errors**

```bash
cd C:/Users/123/Projects/sis
go build ./services/api-gateway/
```

Expected: no errors.

- [ ] **Step 5: Commit**

```bash
cd C:/Users/123/Projects/sis
git add services/api-gateway/admin_handler.go services/api-gateway/main.go
git commit -m "feat: GET /admin/system-health endpoint and startup wiring"
```

---

## Task 4: Frontend — `SystemHealthSnapshot` type + hook

**Files:**
- Modify: `frontend/src/types.ts`
- Create: `frontend/src/hooks/useSystemHealth.ts`

- [ ] **Step 1: Add `SystemHealthSnapshot` to `frontend/src/types.ts`**

Open `frontend/src/types.ts`. Append at the very end of the file:

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

- [ ] **Step 2: Create `frontend/src/hooks/useSystemHealth.ts`**

```typescript
import { useEffect, useRef, useState } from 'react'
import { apiClient } from '../api/client'
import type { SystemHealthSnapshot } from '../types'

/** Polls GET /admin/system-health every 10 s. Returns null until first successful response. */
export function useSystemHealth(): SystemHealthSnapshot | null {
  const [data, setData] = useState<SystemHealthSnapshot | null>(null)
  const cancelled = useRef(false)

  useEffect(() => {
    cancelled.current = false

    async function fetch_() {
      try {
        const res = await apiClient.get<SystemHealthSnapshot>('/admin/system-health')
        if (!cancelled.current) setData(res.data)
      } catch {
        // Silently ignore — chips remain hidden on error
      }
    }

    fetch_()
    const iv = setInterval(fetch_, 10_000)
    return () => {
      cancelled.current = true
      clearInterval(iv)
    }
  }, [])

  return data
}
```

- [ ] **Step 3: Verify TypeScript compilation**

```bash
cd C:/Users/123/Projects/sis/frontend
npx tsc --noEmit
```

Expected: no errors.

- [ ] **Step 4: Commit**

```bash
cd C:/Users/123/Projects/sis
git add frontend/src/types.ts frontend/src/hooks/useSystemHealth.ts
git commit -m "feat: SystemHealthSnapshot type and useSystemHealth hook"
```

---

## Task 5: Frontend — system health chips in `AdminUserPickerBar`

**Files:**
- Modify: `frontend/src/pages/TerminalPage.tsx`

The `AdminUserPickerBar` function starts around line 1015 in `TerminalPage.tsx`. The JSX contains `<div className="flex-1" />` (around line 1091) as a spacer — we replace it with the chips layout.

- [ ] **Step 1: Add `useSystemHealth` import to `TerminalPage.tsx`**

Find the imports block at the top of `frontend/src/pages/TerminalPage.tsx` where hooks are imported (e.g., near `import { usePositionsWs }`). Add:

```typescript
import { useSystemHealth } from '../hooks/useSystemHealth'
```

- [ ] **Step 2: Add helper functions before `AdminUserPickerBar`**

Find this comment in `TerminalPage.tsx` (around line 1013):
```typescript
// ── Admin user picker bar ────────────────────────────────────────────────────
```

Insert the following two functions immediately after that comment, before `function AdminUserPickerBar`:

```typescript
function metricColor(pct: number): string {
  if (pct < 60) return 'text-emerald-400'
  if (pct < 80) return 'text-amber-400'
  return 'text-rose-400'
}

function SystemMetricChip({ label, pct }: { label: string; pct: number }) {
  return (
    <span className="hidden md:flex items-baseline gap-[3px]">
      <span className="text-[9px] font-bold uppercase tracking-[0.8px] text-slate-600">{label}</span>
      <span className={`text-[11px] font-semibold tabular-nums ${metricColor(pct)}`}>
        {Math.round(pct)}%
      </span>
    </span>
  )
}
```

- [ ] **Step 3: Call the hook inside `AdminUserPickerBar`**

Inside `function AdminUserPickerBar(...)`, after the existing hook calls (`useAdminUsers`, `useState`, etc.), add:

```typescript
const health = useSystemHealth()
```

- [ ] **Step 4: Replace the spacer with chips in the JSX**

Find this exact line inside the `AdminUserPickerBar` return JSX (around line 1091):
```typescript
      <div className="flex-1" />
```

Replace it with:

```typescript
      {/* System health chips */}
      <div className="flex flex-1 items-center justify-center gap-3 min-w-0 overflow-hidden px-2">
        {health && (
          <>
            <SystemMetricChip label="CPU"  pct={health.cpu_pct} />
            <SystemMetricChip label="RAM"  pct={health.ram_pct} />
            <SystemMetricChip label="Disk" pct={health.disk_pct} />
            {/* DB chip */}
            <span className="hidden md:flex items-baseline gap-[3px]">
              <span className="text-[9px] font-bold uppercase tracking-[0.8px] text-slate-600">DB</span>
              <span className={`text-[11px] font-semibold ${health.db_ok ? 'text-emerald-400' : 'text-rose-400'}`}>
                {health.db_ok
                  ? `✓${(health.db_size_mb / 1024).toFixed(1)}G`
                  : '✗'}
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

- [ ] **Step 5: Verify TypeScript compilation**

```bash
cd C:/Users/123/Projects/sis/frontend
npx tsc --noEmit
```

Expected: no errors.

- [ ] **Step 6: Visual check**

Start the dev server and open the terminal page as an admin:

```bash
cd C:/Users/123/Projects/sis/frontend
npm run dev
```

Expected result in the admin bar (values depend on server load):
```
🛡 Admin  ·  CPU 8%  RAM 45%  Disk 23%  DB ✓2.3G  ·  [🔍 Выбрать пользователя]
```

Confirm:
- Chips appear only when logged in as admin.
- Values below 60% appear in `emerald-400` (green).
- Values 60–80% appear in `amber-400` (yellow).
- Values above 80% appear in `rose-400` (red).
- DB chip shows `✗` in red if the backend cannot reach the DB.
- Growth suffix (e.g., `+85M/д`) only appears when `db_growth_mb_per_day ≥ 50`.
- On narrow screens (< md breakpoint) chips are hidden (`hidden md:flex`), leaving the bar uncluttered.

- [ ] **Step 7: Commit**

```bash
cd C:/Users/123/Projects/sis
git add frontend/src/pages/TerminalPage.tsx
git commit -m "feat: system health chips in AdminUserPickerBar"
```

---

## Final: production build

- [ ] **Step 1: Build the Go binary**

```bash
cd C:/Users/123/Projects/sis
go build -o services/api-gateway/api-gateway.exe ./services/api-gateway/
```

Expected: no errors.

- [ ] **Step 2: Build the frontend**

```bash
cd C:/Users/123/Projects/sis/frontend
npm run build
```

Expected: no errors, `dist/` updated.
