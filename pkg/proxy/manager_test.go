package proxy

import (
	"net/url"
	"sync"
	"sync/atomic"
	"testing"
)

func TestPickRoundRobin(t *testing.T) {
	m := &Manager{
		proxies: []*Proxy{
			{ID: 1, Weight: 1, IsActive: true, HealthStatus: "healthy"},
			{ID: 2, Weight: 1, IsActive: true, HealthStatus: "healthy"},
			{ID: 3, Weight: 1, IsActive: true, HealthStatus: "healthy"},
		},
	}

	// With equal pending (all zero), round-robin should distribute evenly.
	counts := map[int]int{}
	for i := 0; i < 300; i++ {
		p := m.Pick()
		if p == nil {
			t.Fatal("expected proxy, got nil")
		}
		counts[p.ID]++
	}

	for id, c := range counts {
		if c < 80 || c > 120 {
			t.Errorf("proxy %d count %d, expected ~100", id, c)
		}
	}
}

func TestPickLeastConnections(t *testing.T) {
	p1 := &Proxy{ID: 1, Weight: 1, IsActive: true, HealthStatus: "healthy"}
	p2 := &Proxy{ID: 2, Weight: 1, IsActive: true, HealthStatus: "healthy"}
	p3 := &Proxy{ID: 3, Weight: 1, IsActive: true, HealthStatus: "healthy"}

	m := &Manager{proxies: []*Proxy{p1, p2, p3}}

	// Simulate p1 and p2 having pending requests
	p1.IncPending()
	p1.IncPending()
	p2.IncPending()

	// p3 has 0 pending, should always be picked
	for i := 0; i < 50; i++ {
		p := m.Pick()
		if p.ID != 3 {
			t.Errorf("expected proxy 3 (least pending), got %d", p.ID)
		}
	}
}

func TestPickWeight(t *testing.T) {
	p1 := &Proxy{ID: 1, Weight: 1, IsActive: true, HealthStatus: "healthy"}
	p2 := &Proxy{ID: 2, Weight: 3, IsActive: true, HealthStatus: "healthy"}

	m := &Manager{proxies: []*Proxy{p1, p2}}

	// Set equal pending so weight is the tie-breaker via score = pending/weight
	// Both have 0 pending, so score is 0 for both. Round-robin kicks in.
	// Instead, set different pending to test weight normalization.
	p1.IncPending()
	p1.IncPending()
	p1.IncPending() // pending=3, weight=1 => score=3
	p2.IncPending() // pending=1, weight=3 => score=0.33

	for i := 0; i < 50; i++ {
		p := m.Pick()
		if p.ID != 2 {
			t.Errorf("expected proxy 2 (lower score), got %d", p.ID)
		}
	}
}

func TestPickNoHealthyReturnsNil(t *testing.T) {
	p1 := &Proxy{ID: 1, Weight: 1, IsActive: true, HealthStatus: "unhealthy"}
	p2 := &Proxy{ID: 2, Weight: 1, IsActive: false, HealthStatus: "healthy"}

	m := &Manager{proxies: []*Proxy{p1, p2}}

	// No healthy proxies — should return nil (direct connection fallback)
	p := m.Pick()
	if p != nil {
		t.Errorf("expected nil for no healthy proxies, got %v", p)
	}
}

func TestPickEmptyPool(t *testing.T) {
	m := &Manager{proxies: []*Proxy{}}
	p := m.Pick()
	if p != nil {
		t.Error("expected nil for empty pool")
	}
}

func TestProxyCounters(t *testing.T) {
	p := &Proxy{ID: 1}
	p.IncPending()
	p.IncPending()
	if p.Pending() != 2 {
		t.Errorf("pending %d, expected 2", p.Pending())
	}
	p.DecPending()
	if p.Pending() != 1 {
		t.Errorf("pending %d, expected 1", p.Pending())
	}
	p.IncTotal()
	p.IncTotal()
	if p.Total() != 2 {
		t.Errorf("total %d, expected 2", p.Total())
	}
	p.IncFailures()
	if p.Failures() != 1 {
		t.Errorf("failures %d, expected 1", p.Failures())
	}
}

func TestPickConcurrency(t *testing.T) {
	proxies := []*Proxy{
		{ID: 1, Weight: 1, IsActive: true, HealthStatus: "healthy"},
		{ID: 2, Weight: 1, IsActive: true, HealthStatus: "healthy"},
	}
	m := &Manager{proxies: proxies}

	var wg sync.WaitGroup
	var total atomic.Uint64
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				p := m.Pick()
				if p != nil {
					p.IncPending()
					p.DecPending()
					total.Add(1)
				}
			}
		}()
	}
	wg.Wait()

	if total.Load() != 10000 {
		t.Errorf("total picks %d, expected 10000", total.Load())
	}
	for _, p := range proxies {
		if p.Pending() != 0 {
			t.Errorf("proxy %d pending %d, expected 0", p.ID, p.Pending())
		}
	}
}

func TestPortFromURL(t *testing.T) {
	u := mustURL("http://proxy.example.com:8080")
	if portFromURL(u) != 8080 {
		t.Errorf("port %d, expected 8080", portFromURL(u))
	}
	u = mustURL("https://proxy.example.com")
	if portFromURL(u) != 443 {
		t.Errorf("port %d, expected 443", portFromURL(u))
	}
	u = mustURL("http://proxy.example.com")
	if portFromURL(u) != 80 {
		t.Errorf("port %d, expected 80", portFromURL(u))
	}
}

func mustURL(s string) *url.URL {
	u, _ := url.Parse(s)
	return u
}
