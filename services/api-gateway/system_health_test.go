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
	want := 95.37 // 100 MB (decimal) / 1048576 (binary MB)
	if got < want-2.0 || got > want+2.0 {
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
