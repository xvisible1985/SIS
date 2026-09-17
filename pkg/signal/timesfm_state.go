package signal

import (
	"fmt"
	"sync"
	"time"
)

// timesfmEntry is one cached forecast result for a symbol/timeframe/context/horizon key.
type timesfmEntry struct {
	predictedPct float64
	updatedAt    time.Time
}

var timesfmCache struct {
	mu       sync.RWMutex
	entries  map[string]timesfmEntry
	inflight map[string]bool
}

func init() {
	timesfmCache.entries = make(map[string]timesfmEntry)
	timesfmCache.inflight = make(map[string]bool)
}

func timesfmCacheKey(symbol, timeframe string, contextBars, horizonBars int) string {
	return fmt.Sprintf("%s:%s:%d:%d", symbol, timeframe, contextBars, horizonBars)
}

// SetTimesfmForecast updates the cached forecast for a symbol/timeframe/context/horizon
// combination. Called only from services/api-gateway's TimesfmRefreshFunc once a real model
// call finishes — pkg/signal never calls this on its own. Only the raw predicted percentage
// is cached, NOT a Buy/Sell/Neutral state: two different bots can reference this signal on
// the same symbol/timeframe with different threshold_pct, so state must be derived at read
// time from each caller's own threshold (see timesfm.go's deriveTimesfmState), not baked in
// here from whichever config happened to trigger the refresh.
func SetTimesfmForecast(symbol, timeframe string, contextBars, horizonBars int, predictedPct float64) {
	key := timesfmCacheKey(symbol, timeframe, contextBars, horizonBars)
	timesfmCache.mu.Lock()
	timesfmCache.entries[key] = timesfmEntry{predictedPct: predictedPct, updatedAt: time.Now()}
	timesfmCache.mu.Unlock()
}

// GetTimesfmForecast returns the cached predicted percentage and whether the entry is still
// within maxAge. A missing entry returns (0, false). A stale entry still returns its last
// known value (fresh=false) — callers use the value as a best-effort answer while a refresh
// may or may not be in flight.
func GetTimesfmForecast(symbol, timeframe string, contextBars, horizonBars int, maxAge time.Duration) (predictedPct float64, fresh bool) {
	key := timesfmCacheKey(symbol, timeframe, contextBars, horizonBars)
	timesfmCache.mu.RLock()
	defer timesfmCache.mu.RUnlock()
	e, ok := timesfmCache.entries[key]
	if !ok {
		return 0, false
	}
	return e.predictedPct, time.Since(e.updatedAt) < maxAge
}

// tryStartTimesfmRefresh marks a key as having a refresh in flight and returns true if this
// call is the one that should actually run it — false means another goroutine already has
// one in flight for the same key, and the caller must not start a second.
func tryStartTimesfmRefresh(symbol, timeframe string, contextBars, horizonBars int) bool {
	key := timesfmCacheKey(symbol, timeframe, contextBars, horizonBars)
	timesfmCache.mu.Lock()
	defer timesfmCache.mu.Unlock()
	if timesfmCache.inflight[key] {
		return false
	}
	timesfmCache.inflight[key] = true
	return true
}

// finishTimesfmRefresh clears the in-flight flag for a key. Must be called (typically via
// defer) once a refresh started by tryStartTimesfmRefresh completes, success or failure.
func finishTimesfmRefresh(symbol, timeframe string, contextBars, horizonBars int) {
	key := timesfmCacheKey(symbol, timeframe, contextBars, horizonBars)
	timesfmCache.mu.Lock()
	delete(timesfmCache.inflight, key)
	timesfmCache.mu.Unlock()
}

// TimesfmRefreshFunc, when set, is invoked from a background goroutine (never inline on the
// signal engine's hot compute path — see timesfm.go's ComputeWithSymbol) whenever the cache
// is stale for a symbol/timeframe/context/horizon combination. Left nil by default —
// services/api-gateway sets it once at startup, since it owns the DB pool and HTTP client
// the real refresh needs, neither of which pkg/signal itself ever holds (mirrors why
// whale/leverage state is only ever pushed in from outside, never fetched by pkg/signal
// itself — see whale_state.go, leverage_state.go).
var TimesfmRefreshFunc func(symbol, timeframe string, candles []Candle, horizonBars int)
