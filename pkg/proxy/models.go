package proxy

import (
	"fmt"
	"net/url"
	"sync"
	"sync/atomic"
	"time"
)

// DBProxy represents a row in the proxies table.
type DBProxy struct {
	ID           int        `db:"id"`
	Protocol     string     `db:"protocol"`
	Host         string     `db:"host"`
	Port         int        `db:"port"`
	Username     *string    `db:"username"`
	PasswordEnc  *string    `db:"password_enc"`
	Weight       int        `db:"weight"`
	IsActive     bool       `db:"is_active"`
	HealthStatus string     `db:"health_status"`
	LastChecked  *time.Time `db:"last_checked"`
	FailCount    int        `db:"fail_count"`
	TotalReqs    int64      `db:"total_reqs"`
	ActiveReqs   int        `db:"active_reqs"`
	CreatedAt    *time.Time `db:"created_at"`
	UpdatedAt    *time.Time `db:"updated_at"`
}

// Proxy is a runtime proxy with atomic counters.
type Proxy struct {
	ID       int
	URL      *url.URL
	Weight   int
	IsActive bool // set once at construction, immutable afterwards

	statusMu sync.RWMutex
	status   string // "healthy" | "unhealthy" | "unknown"

	pending  atomic.Int64
	total    atomic.Uint64
	failures atomic.Uint64
}

// Status returns the current health status (safe for concurrent use).
func (p *Proxy) Status() string {
	p.statusMu.RLock()
	defer p.statusMu.RUnlock()
	return p.status
}

// SetStatus updates the health status (safe for concurrent use).
func (p *Proxy) SetStatus(s string) {
	p.statusMu.Lock()
	p.status = s
	p.statusMu.Unlock()
}

// IncPending atomically increments active requests.
func (p *Proxy) IncPending() { p.pending.Add(1) }

// DecPending atomically decrements active requests.
func (p *Proxy) DecPending() { p.pending.Add(-1) }

// Pending returns the current number of in-flight requests.
func (p *Proxy) Pending() int64 { return p.pending.Load() }

// IncTotal atomically increments total requests.
func (p *Proxy) IncTotal() { p.total.Add(1) }

// Total returns the total number of requests handled.
func (p *Proxy) Total() uint64 { return p.total.Load() }

// IncFailures atomically increments failure count.
func (p *Proxy) IncFailures() { p.failures.Add(1) }

// Failures returns the failure count.
func (p *Proxy) Failures() uint64 { return p.failures.Load() }

// Snapshot is a read-only view of a proxy for metrics export.
type Snapshot struct {
	ID           int    `json:"id"`
	Protocol     string `json:"protocol"`
	Host         string `json:"host"`
	Port         int    `json:"port"`
	Weight       int    `json:"weight"`
	IsActive     bool   `json:"is_active"`
	HealthStatus string `json:"health_status"`
	Pending      int64  `json:"pending"`
	Total        uint64 `json:"total"`
	Failures     uint64 `json:"failures"`
}

// ToSnapshot builds a Snapshot from a Proxy.
func (p *Proxy) ToSnapshot() Snapshot {
	return Snapshot{
		ID:           p.ID,
		Protocol:     p.URL.Scheme,
		Host:         p.URL.Hostname(),
		Port:         portFromURL(p.URL),
		Weight:       p.Weight,
		IsActive:     p.IsActive,
		HealthStatus: p.Status(),
		Pending:      p.Pending(),
		Total:        p.Total(),
		Failures:     p.Failures(),
	}
}

func portFromURL(u *url.URL) int {
	if u.Port() != "" {
		var p int
		fmt.Sscanf(u.Port(), "%d", &p)
		return p
	}
	if u.Scheme == "https" {
		return 443
	}
	return 80
}
