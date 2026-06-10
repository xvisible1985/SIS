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
