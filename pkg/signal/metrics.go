package signal

import (
	"context"
	"log"
	"sync"
	"sync/atomic"
	"time"
)

// ExecFn is a minimal DB executor — wrap pgxpool.Pool.Exec to satisfy it.
type ExecFn func(ctx context.Context, sql string, args ...any) error

// ── per-unit compute stats ─────────────────────────────────────────────────

type unitStats struct {
	mu           sync.Mutex
	computeCount int64
	totalNs      int64 // nanoseconds
}

func (u *unitStats) record(d time.Duration) {
	u.mu.Lock()
	u.computeCount++
	u.totalNs += d.Nanoseconds()
	u.mu.Unlock()
}

func (u *unitStats) avg() (count int64, avgMs float64) {
	u.mu.Lock()
	count = u.computeCount
	total := u.totalNs
	u.mu.Unlock()
	if count == 0 {
		return 0, 0
	}
	return count, float64(total) / float64(count) / 1e6
}

// ── rolling rate counter (computes per second) ─────────────────────────────

type rateCounter struct {
	mu      sync.Mutex
	buckets [60]int64 // one per second, circular
	cur     int       // current second bucket index
	lastSec int64     // unix second of cur
}

func (r *rateCounter) tick() {
	now := time.Now().Unix()
	r.mu.Lock()
	defer r.mu.Unlock()
	if now != r.lastSec {
		// Advance buckets
		diff := int(now - r.lastSec)
		if diff > 60 {
			diff = 60
		}
		for i := 1; i <= diff; i++ {
			r.cur = (r.cur + 1) % 60
			r.buckets[r.cur] = 0
		}
		r.lastSec = now
	}
	r.buckets[r.cur]++
}

func (r *rateCounter) rate() float64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	var total int64
	for _, v := range r.buckets {
		total += v
	}
	return float64(total) / 60.0
}

// ── Metrics ────────────────────────────────────────────────────────────────

type Metrics struct {
	activeUnits atomic.Int64
	activeSubs  atomic.Int64
	totalCPUNs  atomic.Int64
	totalComputes atomic.Int64

	rate      rateCounter
	unitStats sync.Map // hash → *unitStats

	exec ExecFn
	ctx  context.Context
}

func newMetrics(ctx context.Context, exec ExecFn) *Metrics {
	m := &Metrics{ctx: ctx, exec: exec}
	if exec != nil {
		go m.persistLoop()
	}
	return m
}

func (m *Metrics) unitAdded()   { m.activeUnits.Add(1) }
func (m *Metrics) unitRemoved() { m.activeUnits.Add(-1) }
func (m *Metrics) subAdded()    { m.activeSubs.Add(1) }
func (m *Metrics) subRemoved()  { m.activeSubs.Add(-1) }

func (m *Metrics) recordCompute(hash string, d time.Duration) {
	m.totalCPUNs.Add(d.Nanoseconds())
	m.totalComputes.Add(1)
	m.rate.tick()

	v, _ := m.unitStats.LoadOrStore(hash, &unitStats{})
	v.(*unitStats).record(d)
}

// ── MetricsSnapshot ────────────────────────────────────────────────────────

type UnitMetric struct {
	Hash            string  `json:"hash"`
	Symbol          string  `json:"symbol"`
	Interval        string  `json:"interval"`
	Signals         string  `json:"signals"`
	Subscribers     int     `json:"subscribers"`
	AvgComputeMs    float64 `json:"avgComputeMs"`
	ComputeCount    int64   `json:"computeCount"`
	LastState       State   `json:"lastState"`
	LastComputedSec int     `json:"lastComputedSec"`
}

type MetricsSnapshot struct {
	ActiveUnits    int64        `json:"activeUnits"`
	ActiveSubs     int64        `json:"activeSubs"`
	WSConnections  int          `json:"wsConnections"`
	ComputesPerSec float64      `json:"computesPerSec"`
	CPUTimeMs      int64        `json:"cpuTimeMs"`
	BufferMemMB    float64      `json:"bufferMemMB"`
	Units          []UnitMetric `json:"units"`
}

func (m *Metrics) snapshot(units []*computeUnit, wsConns int) MetricsSnapshot {
	uMetrics := make([]UnitMetric, 0, len(units))
	var totalBufBytes int64

	now := time.Now()
	for _, u := range units {
		u.mu.Lock()
		subs := len(u.subs)
		state := u.lastState
		lastAt := u.lastComputedAt
		u.mu.Unlock()

		var count int64
		var avgMs float64
		if v, ok := m.unitStats.Load(u.hash); ok {
			count, avgMs = v.(*unitStats).avg()
		}

		var lastComputedSec int
		if !lastAt.IsZero() {
			lastComputedSec = int(now.Sub(lastAt).Seconds())
		}

		// Estimate buffer memory: candleBufferSize candles × ~48 bytes each
		totalBufBytes += candleBufferSize * 48

		uMetrics = append(uMetrics, UnitMetric{
			Hash:            u.hash,
			Symbol:          u.symbol,
			Interval:        u.interval,
			Signals:         u.sigLabel,
			Subscribers:     subs,
			AvgComputeMs:    avgMs,
			ComputeCount:    count,
			LastState:       state,
			LastComputedSec: lastComputedSec,
		})
	}

	return MetricsSnapshot{
		ActiveUnits:    m.activeUnits.Load(),
		ActiveSubs:     m.activeSubs.Load(),
		WSConnections:  wsConns,
		ComputesPerSec: m.rate.rate(),
		CPUTimeMs:      m.totalCPUNs.Load() / 1e6,
		BufferMemMB:    float64(totalBufBytes) / 1024 / 1024,
		Units:          uMetrics,
	}
}

// ── DB persistence (every 10s) ─────────────────────────────────────────────

func (m *Metrics) persistLoop() {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-m.ctx.Done():
			return
		case <-ticker.C:
			m.persistSnapshot()
		}
	}
}

func (m *Metrics) persistSnapshot() {
	snap := m.snapshot(nil, 0) // units not needed for aggregate row
	err := m.exec(m.ctx,
		`INSERT INTO signal_metrics_snapshots
		 (active_units, active_subs, ws_conns, computes_per_sec, cpu_time_ms, buffer_mem_mb)
		 VALUES ($1,$2,$3,$4,$5,$6)`,
		snap.ActiveUnits,
		snap.ActiveSubs,
		snap.WSConnections,
		snap.ComputesPerSec,
		snap.CPUTimeMs,
		snap.BufferMemMB,
	)
	if err != nil {
		log.Printf("signal metrics: persist: %v", err)
	}
}
