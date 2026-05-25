package signal

// GlobalWarmer subscribes to ALL linear Bybit symbols for the intervals
// needed by active bots, keeping candle buffers permanently warm.
// This eliminates REST fetches during bot scanning ticks.

import (
	"context"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"sis/pkg/proxy"
)

const (
	bybitInstrumentsURL = "https://api.bybit.com/v5/market/instruments-info?category=linear&status=Trading&limit=1000"
	symbolRefreshPeriod = 5 * time.Minute
)

// GlobalWarmerMetrics is a live snapshot of warmer state.
type GlobalWarmerMetrics struct {
	Symbols    int      `json:"symbols"`
	Intervals  []string `json:"intervals"`
	WarmCount  int      `json:"warm_count"`   // buffers with ≥2 candles
	TotalSlots int      `json:"total_slots"`  // symbols × intervals
	WarmPct    float64  `json:"warm_pct"`     // warm_count / total_slots * 100
	PrefetchMs int64    `json:"prefetch_ms"`  // ms taken for initial prefetch
}

// GlobalWarmer subscribes to all active linear USDT symbols for all
// registered intervals, keeping the KlineHub's candle buffers permanently warm.
type GlobalWarmer struct {
	hub       *KlineHub
	tickerHub *TickerHub

	mu        sync.RWMutex
	intervals map[string]bool // bybit interval strings, e.g. "15", "60"
	symbols   []string        // all linear USDT symbols

	startedAt  time.Time
	prefetchMs int64 // atomic; ms taken for initial bulk prefetch
}

// NewGlobalWarmer creates a GlobalWarmer backed by the given KlineHub and TickerHub.
func NewGlobalWarmer(hub *KlineHub, tickerHub *TickerHub) *GlobalWarmer {
	return &GlobalWarmer{
		hub:       hub,
		tickerHub: tickerHub,
		intervals: make(map[string]bool),
		startedAt: time.Now(),
	}
}

// Start fetches all linear USDT symbols, subscribes them for every currently
// registered interval, then refreshes the symbol list every 5 minutes.
// Call in a goroutine: go warmer.Start(ctx)
func (w *GlobalWarmer) Start(ctx context.Context) {
	if err := w.loadAndSubscribeAll(ctx); err != nil {
		log.Printf("global warmer: initial load: %v", err)
	}

	ticker := time.NewTicker(symbolRefreshPeriod)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := w.loadAndSubscribeAll(ctx); err != nil {
				log.Printf("global warmer: refresh: %v", err)
			}
		}
	}
}

// EnsureIntervals adds any new interval strings not yet subscribed,
// subscribing ALL known symbols to each new interval.
// Safe to call concurrently. No-op for already-registered intervals.
func (w *GlobalWarmer) EnsureIntervals(intervals []string) {
	w.mu.Lock()
	var newIntervals []string
	for _, iv := range intervals {
		biv := bybitInterval(iv)
		if !w.intervals[biv] {
			w.intervals[biv] = true
			newIntervals = append(newIntervals, biv)
		}
	}
	syms := make([]string, len(w.symbols))
	copy(syms, w.symbols)
	w.mu.Unlock()

	for _, iv := range newIntervals {
		for _, sym := range syms {
			w.hub.Subscribe(sym, iv, nil)
		}
	}
}

// Metrics returns a live snapshot of warmer state.
func (w *GlobalWarmer) Metrics() GlobalWarmerMetrics {
	w.mu.RLock()
	syms := make([]string, len(w.symbols))
	copy(syms, w.symbols)
	ivs := make([]string, 0, len(w.intervals))
	for iv := range w.intervals {
		ivs = append(ivs, iv)
	}
	w.mu.RUnlock()

	totalSlots := len(syms) * len(ivs)
	warmCount := 0
	for _, sym := range syms {
		for _, iv := range ivs {
			if snap := w.hub.Snapshot(sym, iv); len(snap) >= 2 {
				warmCount++
			}
		}
	}

	warmPct := 0.0
	if totalSlots > 0 {
		warmPct = float64(warmCount) / float64(totalSlots) * 100
	}

	return GlobalWarmerMetrics{
		Symbols:    len(syms),
		Intervals:  ivs,
		WarmCount:  warmCount,
		TotalSlots: totalSlots,
		WarmPct:    warmPct,
		PrefetchMs: atomic.LoadInt64(&w.prefetchMs),
	}
}

// loadAndSubscribeAll fetches the current symbol list and subscribes every
// (symbol, interval) pair that is not already covered by the hub.
func (w *GlobalWarmer) loadAndSubscribeAll(ctx context.Context) error {
	syms, err := fetchLinearUSDTSymbols(ctx)
	if err != nil {
		return err
	}

	w.mu.Lock()
	w.symbols = syms
	ivs := make([]string, 0, len(w.intervals))
	for iv := range w.intervals {
		ivs = append(ivs, iv)
	}
	w.mu.Unlock()

	// Warm TickerHub for all symbols regardless of whether kline intervals exist.
	for _, sym := range syms {
		w.tickerHub.Subscribe(sym, nil)
	}

	if len(ivs) == 0 {
		return nil // no intervals registered yet; subscriptions will come via EnsureIntervals
	}

	start := time.Now()
	for _, sym := range syms {
		for _, iv := range ivs {
			w.hub.Subscribe(sym, iv, nil)
		}
	}
	ms := time.Since(start).Milliseconds()
	// Only record timing on the first pass (prefetchMs == 0)
	atomic.CompareAndSwapInt64(&w.prefetchMs, 0, ms)

	return nil
}

// fetchLinearUSDTSymbols retrieves all Trading linear USDT symbols from Bybit.
func fetchLinearUSDTSymbols(ctx context.Context) ([]string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, bybitInstrumentsURL, nil)
	if err != nil {
		return nil, err
	}
	resp, err := proxy.HTTPClient().Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var result struct {
		Result struct {
			List []struct {
				Symbol     string `json:"symbol"`
				SettleCoin string `json:"settleCoin"`
			} `json:"list"`
		} `json:"result"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, err
	}

	symbols := make([]string, 0, len(result.Result.List))
	for _, item := range result.Result.List {
		if item.SettleCoin == "USDT" {
			symbols = append(symbols, item.Symbol)
		}
	}
	return symbols, nil
}
