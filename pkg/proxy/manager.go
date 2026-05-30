package proxy

import (
	"context"
	"fmt"
	"net/url"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"sis/pkg/crypto"
)

// Manager holds the proxy pool and provides selection.
type Manager struct {
	db      *pgxpool.Pool
	encKey  string
	mu      sync.RWMutex
	proxies []*Proxy
	rrIdx   atomic.Uint64

	transportOnce   sync.Once
	sharedTransport *BalancedTransport

	lastPickMu   sync.RWMutex
	lastPickHost string // "host:port" последнего выбранного прокси
}

// NewManager creates a Manager and starts background health checks.
func NewManager(ctx context.Context, db *pgxpool.Pool, encKey string) (*Manager, error) {
	m := &Manager{db: db, encKey: encKey}
	// Reset all proxies to unknown on startup so we don't trust stale health statuses.
	_, _ = db.Exec(ctx, `UPDATE proxies SET health_status = 'unknown', fail_count = 0`)
	if err := m.reload(ctx); err != nil {
		return nil, fmt.Errorf("proxy manager init: %w", err)
	}
	go m.healthCheckLoop(ctx)
	return m, nil
}

// reload fetches active proxies from DB and rebuilds the runtime list.
func (m *Manager) reload(ctx context.Context) error {
	rows, err := m.db.Query(ctx,
		`SELECT id, protocol, host, port, username, password_enc, weight, is_active, health_status
		 FROM proxies WHERE is_active = true ORDER BY id`)
	if err != nil {
		return err
	}
	defer rows.Close()

	var proxies []*Proxy
	for rows.Next() {
		var p DBProxy
		if err := rows.Scan(&p.ID, &p.Protocol, &p.Host, &p.Port, &p.Username, &p.PasswordEnc, &p.Weight, &p.IsActive, &p.HealthStatus); err != nil {
			return err
		}
		proxyURL, err := m.buildProxyURL(&p)
		if err != nil {
			continue // skip malformed
		}
		px := &Proxy{
			ID:       p.ID,
			URL:      proxyURL,
			Weight:   max(p.Weight, 1),
			IsActive: p.IsActive,
		}
		px.SetStatus(p.HealthStatus)
		proxies = append(proxies, px)
	}

	m.mu.Lock()
	m.proxies = proxies
	m.mu.Unlock()
	return rows.Err()
}

// buildProxyURL constructs a url.URL from DB fields. Decrypts the password if present.
func (m *Manager) buildProxyURL(p *DBProxy) (*url.URL, error) {
	u := &url.URL{
		Scheme: p.Protocol,
		Host:   fmt.Sprintf("%s:%d", p.Host, p.Port),
	}
	if p.Username != nil && *p.Username != "" {
		password := ""
		if p.PasswordEnc != nil && *p.PasswordEnc != "" && m.encKey != "" {
			dec, err := crypto.Decrypt(*p.PasswordEnc, m.encKey)
			if err == nil {
				password = dec
			}
		}
		if password != "" {
			u.User = url.UserPassword(*p.Username, password)
		} else {
			u.User = url.User(*p.Username)
		}
	}
	return u, nil
}

// Pick selects the best proxy using least-connections (primary) + round-robin (tie-break).
// Returns nil if no healthy proxies are available — caller should fall back to direct connection.
func (m *Manager) Pick() *Proxy {
	m.mu.RLock()
	proxies := m.proxies
	m.mu.RUnlock()

	if len(proxies) == 0 {
		return nil
	}

	var best *Proxy
	bestScore := float64(1<<63 - 1)
	candidates := 0

	for _, p := range proxies {
		if !p.IsActive || p.Status() != "healthy" {
			continue
		}
		score := float64(p.Pending()) / float64(p.Weight)
		if score < bestScore {
			bestScore = score
			best = p
			candidates = 1
		} else if score == bestScore {
			candidates++
		}
	}

	// Round-robin tie-break among equally-scored candidates.
	if candidates > 1 {
		start := int(m.rrIdx.Add(1))
		idx := 0
		for _, p := range proxies {
			if !p.IsActive || p.Status() != "healthy" {
				continue
			}
			score := float64(p.Pending()) / float64(p.Weight)
			if score == bestScore {
				if idx == start%candidates {
					best = p
					break
				}
				idx++
			}
		}
	}

	if best != nil {
		host := fmt.Sprintf("%s:%d", best.URL.Hostname(), portFromURL(best.URL))
		m.lastPickMu.Lock()
		m.lastPickHost = host
		m.lastPickMu.Unlock()
	}
	return best
}

// LastPickedHost returns the "host:port" of the last proxy selected by Pick.
// Returns empty string if no proxy was ever picked or no proxies are configured.
func (m *Manager) LastPickedHost() string {
	m.lastPickMu.RLock()
	defer m.lastPickMu.RUnlock()
	return m.lastPickHost
}

// Snapshots returns a copy of all proxies with current metrics.
func (m *Manager) Snapshots() []Snapshot {
	m.mu.RLock()
	proxies := m.proxies
	m.mu.RUnlock()

	out := make([]Snapshot, 0, len(proxies))
	for _, p := range proxies {
		out = append(out, p.ToSnapshot())
	}
	return out
}

// Count returns the number of active proxies in memory.
func (m *Manager) Count() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.proxies)
}

// Transport returns the shared BalancedTransport for this manager.
// The transport is created once and reused across all HTTP clients,
// preserving per-proxy connection pools.
func (m *Manager) Transport() *BalancedTransport {
	m.transportOnce.Do(func() {
		m.sharedTransport = NewBalancedTransport(m)
	})
	return m.sharedTransport
}

// CreateProxy inserts a new proxy and reloads the pool.
func (m *Manager) CreateProxy(ctx context.Context, protocol, host string, port int, username, passwordEnc *string, weight int) (int, error) {
	var id int
	err := m.db.QueryRow(ctx,
		`INSERT INTO proxies (protocol, host, port, username, password_enc, weight)
		 VALUES ($1,$2,$3,$4,$5,$6) RETURNING id`,
		protocol, host, port, username, passwordEnc, weight,
	).Scan(&id)
	if err != nil {
		return 0, err
	}
	_ = m.reload(ctx)
	return id, nil
}

// UpdateProxy partially updates a proxy and reloads the pool.
func (m *Manager) UpdateProxy(ctx context.Context, id int, updates map[string]any) error {
	if len(updates) == 0 {
		return nil
	}
	// Build dynamic SQL with correct parameter numbering
	i := 1
	args := []any{}
	setParts := []string{}
	for col, val := range updates {
		setParts = append(setParts, fmt.Sprintf("%s = $%d", col, i))
		args = append(args, val)
		i++
	}
	setParts = append(setParts, fmt.Sprintf("updated_at = $%d", i))
	args = append(args, time.Now())
	i++
	args = append(args, id)

	query := fmt.Sprintf("UPDATE proxies SET %s WHERE id = $%d", 
		joinStrings(setParts, ", "), i-1)
	_, err := m.db.Exec(ctx, query, args...)
	if err != nil {
		return err
	}
	return m.reload(ctx)
}

func joinStrings(parts []string, sep string) string {
	if len(parts) == 0 {
		return ""
	}
	result := parts[0]
	for i := 1; i < len(parts); i++ {
		result += sep + parts[i]
	}
	return result
}

// DeleteProxy removes a proxy and reloads the pool.
func (m *Manager) DeleteProxy(ctx context.Context, id int) error {
	_, err := m.db.Exec(ctx, `DELETE FROM proxies WHERE id = $1`, id)
	if err != nil {
		return err
	}
	return m.reload(ctx)
}

// SetHealthStatus updates the health_status and fail_count for a proxy in DB.
func (m *Manager) SetHealthStatus(ctx context.Context, id int, status string, failCount int) error {
	_, err := m.db.Exec(ctx,
		`UPDATE proxies SET health_status = $1, fail_count = $2, last_checked = $3, updated_at = $3 WHERE id = $4`,
		status, failCount, time.Now(), id)
	return err
}

// ListDBProxies returns all proxies from DB (for admin list endpoint).
func (m *Manager) ListDBProxies(ctx context.Context) ([]DBProxy, error) {
	rows, err := m.db.Query(ctx,
		`SELECT id, protocol, host, port, username, weight, is_active, health_status, last_checked, fail_count, total_reqs, active_reqs, created_at, updated_at
		 FROM proxies ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []DBProxy
	for rows.Next() {
		var p DBProxy
		if err := rows.Scan(&p.ID, &p.Protocol, &p.Host, &p.Port, &p.Username, &p.Weight, &p.IsActive, &p.HealthStatus,
			&p.LastChecked, &p.FailCount, &p.TotalReqs, &p.ActiveReqs, &p.CreatedAt, &p.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// HealthCheckURL is the endpoint used to verify proxy health.
const HealthCheckURL = "https://api.bybit.com/v5/market/time"

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
