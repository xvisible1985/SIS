package main

import (
	"strconv"
	"strings"
	"time"
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
