package proxy

import (
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"
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

// HTTPClientFor returns an *http.Client whose requests are routed only through proxies
// in the pool whose host is in allowedIPs. Empty allowedIPs = no restriction (identical
// to HTTPClient()). If allowedIPs is non-empty and, at request time, no proxy in the
// pool matches, requests made with this client fail with ErrNoWhitelistedProxy rather
// than falling back to direct connection or an unlisted proxy.
func HTTPClientFor(allowedIPs []string) *http.Client {
	if len(allowedIPs) == 0 {
		return HTTPClient()
	}
	m := globalManager
	if m == nil {
		m = &Manager{} // zero-value: PickForIPs always returns ErrNoWhitelistedProxy for non-empty allowedIPs
	}
	return &http.Client{
		Transport: &filteredTransport{manager: m, allowedIPs: allowedIPs, shared: NewBalancedTransport(m)},
		Timeout:   30 * time.Second,
	}
}

// WSDialer returns a *websocket.Dialer that routes through the proxy pool using an
// unrestricted pick (no IP whitelist) — for connections with no associated exchange
// account/API key (e.g. public market-data subscriptions). Falls back to
// websocket.DefaultDialer if no proxies are configured or healthy.
func WSDialer() *websocket.Dialer {
	m := globalManager
	if m == nil || m.Count() == 0 {
		return websocket.DefaultDialer
	}
	p := m.Pick()
	if p == nil {
		return websocket.DefaultDialer
	}
	return &websocket.Dialer{Proxy: http.ProxyURL(p.URL), HandshakeTimeout: 45 * time.Second}
}

// WSDialerFor returns a *websocket.Dialer restricted to proxies whose host is in
// allowedIPs (empty allowedIPs = no restriction). Returns ErrNoWhitelistedProxy if
// allowedIPs is non-empty and no proxy matches, or ErrNoHealthyProxy if proxies are
// configured but none are currently healthy — the caller must not dial in either case (a
// direct/unlisted-IP connection could be rejected by the exchange's own IP whitelist, or
// simply isn't the IP this account's traffic is supposed to go out on).
//
// Deliberately does NOT delegate the empty-allowedIPs case to WSDialer(): WSDialer()
// always falls back to a direct dial when Pick() finds nothing healthy, which is correct
// for its actual callers (public, keyless market-data streams in pkg/signal, where load
// distribution — not IP control — is the only goal) but wrong here, since WSDialerFor is
// used exclusively for private/authenticated account streams (pkg/trader's WS/REST
// clients) where a silent unproxied leak is the same operational risk ErrNoHealthyProxy
// guards against in transport.go.
func WSDialerFor(allowedIPs []string) (*websocket.Dialer, error) {
	m := globalManager
	if m == nil || m.Count() == 0 {
		if len(allowedIPs) == 0 {
			// Proxy feature not in use at all — direct connection (backward-compatible).
			return websocket.DefaultDialer, nil
		}
		return nil, ErrNoWhitelistedProxy
	}
	p, err := m.PickForIPs(allowedIPs)
	if err != nil {
		return nil, err
	}
	if p == nil {
		// Only reachable when allowedIPs is empty (see PickForIPs/Pick) — proxies are
		// configured but none are currently healthy.
		return nil, ErrNoHealthyProxy
	}
	return &websocket.Dialer{Proxy: http.ProxyURL(p.URL), HandshakeTimeout: 45 * time.Second}, nil
}
