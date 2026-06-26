package signal

import "sync"

var whaleCache struct {
	mu     sync.RWMutex
	states map[string]State
}

func init() { whaleCache.states = make(map[string]State) }

// SetWhaleState updates the cached whale state for a symbol.
func SetWhaleState(symbol string, state State) {
	whaleCache.mu.Lock()
	whaleCache.states[symbol] = state
	whaleCache.mu.Unlock()
}

// GetWhaleState returns the cached whale state for a symbol (Neutral if absent).
func GetWhaleState(symbol string) State {
	whaleCache.mu.RLock()
	defer whaleCache.mu.RUnlock()
	if s, ok := whaleCache.states[symbol]; ok {
		return s
	}
	return Neutral
}
