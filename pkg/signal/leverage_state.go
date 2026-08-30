package signal

import "sync"

// leverageCache holds the exchange's max allowed leverage per symbol — populated by
// services/api-gateway's leverage refresher (10-min periodic sweep of active symbols) and
// its live-fallback lookup (getSymbolMaxLeverage, warmed opportunistically whenever any
// caller — including this signal — needs a symbol not yet cached). Mirrors whaleCache's
// pattern: pkg/signal never reaches out to a DB or exchange API itself, it only reads
// whatever an external updater has pushed in.
var leverageCache struct {
	mu    sync.RWMutex
	byKey map[string]float64 // key: symbol+"/"+category
}

func init() { leverageCache.byKey = make(map[string]float64) }

func leverageCacheKey(symbol, category string) string { return symbol + "/" + category }

// SetLeverageState updates the cached max leverage for a symbol/category.
func SetLeverageState(symbol, category string, maxLeverage float64) {
	leverageCache.mu.Lock()
	leverageCache.byKey[leverageCacheKey(symbol, category)] = maxLeverage
	leverageCache.mu.Unlock()
}

// GetLeverageState returns the cached max leverage for a symbol/category, or 0 if unknown
// (never cached yet) — callers must treat 0 as "unknown", not "zero leverage allowed".
func GetLeverageState(symbol, category string) float64 {
	leverageCache.mu.RLock()
	defer leverageCache.mu.RUnlock()
	return leverageCache.byKey[leverageCacheKey(symbol, category)]
}
