package proxy

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

// Regression (2026-10-16): a Binance FetchPositions call surfaced a confusing "-2015
// invalid API-key, IP..." error whose request IP matched neither of the account's 2
// configured proxies. Root cause: BalancedTransport.RoundTrip fell back to a direct,
// unproxied connection (using this server's own bare egress IP) whenever Pick() found
// zero healthy candidates — which isn't the same thing as "no proxies configured", but
// was treated identically. These tests pin the fixed behavior: direct fallback is for
// "proxy feature not in use" ONLY; "proxies configured but all unhealthy" must fail
// closed with ErrNoHealthyProxy instead of silently leaking the request out unproxied.

func TestBalancedTransport_NoProxiesConfigured_FallsBackToDirect(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	m := &Manager{proxies: []*Proxy{}} // Count() == 0: proxy feature genuinely not in use
	tr := NewBalancedTransport(m)
	client := &http.Client{Transport: tr}

	resp, err := client.Get(srv.URL)
	if err != nil {
		t.Fatalf("expected direct connection to succeed, got: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
}

func TestBalancedTransport_ProxiesConfiguredButAllUnhealthy_FailsClosed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	// 2 proxies configured (matches the user's real pool size), both unhealthy — the
	// exact scenario this incident hit during the post-restart startup window.
	m := &Manager{proxies: []*Proxy{
		{ID: 1, URL: mustURL("http://10.0.0.1:3128"), Weight: 1, IsActive: true, status: "unknown"},
		{ID: 2, URL: mustURL("http://10.0.0.2:3128"), Weight: 1, IsActive: true, status: "unknown"},
	}}
	tr := NewBalancedTransport(m)
	client := &http.Client{Transport: tr}

	_, err := client.Get(srv.URL)
	if err == nil {
		t.Fatal("expected request to fail — must not silently leak out via a direct connection")
	}
	if !errors.Is(err, ErrNoHealthyProxy) {
		t.Errorf("expected ErrNoHealthyProxy, got: %v", err)
	}
}

func TestFilteredTransport_NoProxiesConfigured_EmptyAllowedIPs_FallsBackToDirect(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	m := &Manager{proxies: []*Proxy{}}
	tr := &filteredTransport{manager: m, allowedIPs: nil, shared: NewBalancedTransport(m)}
	client := &http.Client{Transport: tr}

	resp, err := client.Get(srv.URL)
	if err != nil {
		t.Fatalf("expected direct connection to succeed, got: %v", err)
	}
	resp.Body.Close()
}

func TestFilteredTransport_ProxiesConfiguredButAllUnhealthy_EmptyAllowedIPs_FailsClosed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	m := &Manager{proxies: []*Proxy{
		{ID: 1, URL: mustURL("http://10.0.0.1:3128"), Weight: 1, IsActive: true, status: "unknown"},
		{ID: 2, URL: mustURL("http://10.0.0.2:3128"), Weight: 1, IsActive: true, status: "unknown"},
	}}
	tr := &filteredTransport{manager: m, allowedIPs: nil, shared: NewBalancedTransport(m)}
	client := &http.Client{Transport: tr}

	_, err := client.Get(srv.URL)
	if err == nil {
		t.Fatal("expected request to fail — must not silently leak out via a direct connection")
	}
	if !errors.Is(err, ErrNoHealthyProxy) {
		t.Errorf("expected ErrNoHealthyProxy, got: %v", err)
	}
}
