package proxy

import (
	"net/http"
	"net/url"
	"sync"
	"time"
)

// BalancedTransport is an http.RoundTripper that routes each request
// through a dynamically-selected proxy.
type BalancedTransport struct {
	manager     *Manager
	base        *http.Transport
	proxyTrans  sync.Map // key: proxy URL string → *http.Transport
}

// NewBalancedTransport creates a transport backed by the given Manager.
func NewBalancedTransport(m *Manager) *BalancedTransport {
	return &BalancedTransport{
		manager: m,
		base: &http.Transport{
			MaxIdleConns:        100,
			MaxIdleConnsPerHost: 10,
			IdleConnTimeout:     90 * time.Second,
			TLSHandshakeTimeout: 10 * time.Second,
		},
	}
}

// transportFor returns a cached *http.Transport for the given proxy URL,
// creating one on first use. This preserves per-proxy connection pools
// instead of destroying them on every request via Clone().
func (t *BalancedTransport) transportFor(proxyURL *url.URL) *http.Transport {
	key := proxyURL.String()
	if v, ok := t.proxyTrans.Load(key); ok {
		return v.(*http.Transport)
	}
	tr := &http.Transport{
		Proxy:               http.ProxyURL(proxyURL),
		MaxIdleConns:        100,
		MaxIdleConnsPerHost: 10,
		IdleConnTimeout:     90 * time.Second,
		TLSHandshakeTimeout: 10 * time.Second,
	}
	actual, _ := t.proxyTrans.LoadOrStore(key, tr)
	return actual.(*http.Transport)
}

// RoundTrip selects a proxy, increments counters, and executes the request.
// If the proxy feature isn't in use at all (no proxies configured), it falls back to a
// direct connection. If proxies ARE configured but none are currently healthy, it fails
// closed with ErrNoHealthyProxy instead — see ErrNoHealthyProxy's doc comment for why a
// silent direct-connection fallback in that case is a real operational risk, not just a
// backward-compatible convenience.
func (t *BalancedTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	p := t.manager.Pick()
	if p == nil {
		if t.manager.Count() == 0 {
			// Proxy feature not in use at all — direct connection (backward-compatible).
			return t.base.RoundTrip(req)
		}
		return nil, ErrNoHealthyProxy
	}

	p.IncPending()
	p.IncTotal()
	defer p.DecPending()

	resp, err := t.transportFor(p.URL).RoundTrip(req)
	if err != nil {
		p.IncFailures()
	}
	return resp, err
}

// filteredTransport routes each request through a proxy selected via
// Manager.PickForIPs(allowedIPs), reusing the same per-proxy connection pools as
// BalancedTransport (via shared.transportFor). Used by HTTPClientFor for accounts with
// an IP-restricted exchange API key.
type filteredTransport struct {
	manager    *Manager
	allowedIPs []string
	shared     *BalancedTransport
}

func (t *filteredTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	p, err := t.manager.PickForIPs(t.allowedIPs)
	if err != nil {
		return nil, err
	}
	if p == nil {
		// Only reachable when allowedIPs is empty (a non-empty list with zero matches
		// already returned ErrNoWhitelistedProxy above) — same two cases as
		// BalancedTransport.RoundTrip: direct connection only if proxies aren't in use
		// at all, otherwise fail closed rather than leak out unproxied.
		if t.manager.Count() == 0 {
			return t.shared.base.RoundTrip(req)
		}
		return nil, ErrNoHealthyProxy
	}

	p.IncPending()
	p.IncTotal()
	defer p.DecPending()

	resp, err := t.shared.transportFor(p.URL).RoundTrip(req)
	if err != nil {
		p.IncFailures()
	}
	return resp, err
}
