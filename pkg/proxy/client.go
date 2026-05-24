package proxy

import (
	"net/http"
	"sync"
	"time"
)

var (
	globalManager  *Manager
	globalInitOnce sync.Once
	globalInitErr  error
)

// InitGlobalManager initialises the singleton proxy Manager.
// Safe to call multiple times — only the first call succeeds.
func InitGlobalManager(m *Manager) {
	globalInitOnce.Do(func() {
		globalManager = m
	})
}

// GlobalManager returns the singleton Manager or nil if not initialised.
func GlobalManager() *Manager {
	return globalManager
}

// HTTPClient returns an *http.Client that routes requests through the proxy pool.
// Falls back to direct connections if no proxies are configured or healthy.
// Always sets a timeout — never returns http.DefaultClient (which has no timeout).
func HTTPClient() *http.Client {
	m := globalManager
	if m == nil || m.Count() == 0 {
		return &http.Client{Timeout: 10 * time.Second}
	}
	return &http.Client{
		Transport: m.Transport(),
		Timeout:   30 * time.Second,
	}
}
