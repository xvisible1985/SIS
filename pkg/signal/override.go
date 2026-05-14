package signal

import "sync"

// testOverrides stores manually-set signal values for test signals.
// Keyed by signal name (e.g. "rsi-test").
var testOverrides struct {
	mu     sync.RWMutex
	values map[string]float64
	active map[string]bool
}

func init() {
	testOverrides.values = make(map[string]float64)
	testOverrides.active = make(map[string]bool)
}

// SetTestOverride sets a manual value for a test signal.
func SetTestOverride(name string, value float64) {
	testOverrides.mu.Lock()
	testOverrides.values[name] = value
	testOverrides.active[name] = true
	testOverrides.mu.Unlock()
}

// ClearTestOverride disables the manual override for a test signal.
func ClearTestOverride(name string) {
	testOverrides.mu.Lock()
	testOverrides.active[name] = false
	testOverrides.mu.Unlock()
}

// GetTestOverride returns the manual value and whether it's active.
func GetTestOverride(name string) (float64, bool) {
	testOverrides.mu.RLock()
	defer testOverrides.mu.RUnlock()
	if !testOverrides.active[name] {
		return 0, false
	}
	return testOverrides.values[name], true
}
