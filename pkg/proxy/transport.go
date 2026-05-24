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
// If no proxy is available it falls back to a direct connection.
func (t *BalancedTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	p := t.manager.Pick()
	if p == nil {
		// No proxy available — direct connection (backward-compatible)
		return t.base.RoundTrip(req)
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
