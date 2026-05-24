package proxy

import (
	"context"
	"log"
	"net/http"
	"time"
)

const (
	healthCheckInterval = 30 * time.Second
	healthCheckTimeout  = 10 * time.Second
	maxFailCount        = 3
)

func (m *Manager) healthCheckLoop(ctx context.Context) {
	ticker := time.NewTicker(healthCheckInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			m.runHealthChecks(ctx)
		}
	}
}

func (m *Manager) runHealthChecks(ctx context.Context) {
	m.mu.RLock()
	proxies := make([]*Proxy, len(m.proxies))
	copy(proxies, m.proxies)
	m.mu.RUnlock()

	for _, p := range proxies {
		if !p.IsActive {
			continue
		}
		go m.checkOne(ctx, p)
	}
}

func (m *Manager) checkOne(ctx context.Context, p *Proxy) {
	client := &http.Client{
		Timeout: healthCheckTimeout,
		Transport: &http.Transport{
			Proxy: http.ProxyURL(p.URL),
		},
	}
	defer client.CloseIdleConnections()

	req, err := http.NewRequestWithContext(ctx, http.MethodHead, HealthCheckURL, nil)
	if err != nil {
		m.recordFailure(ctx, p)
		return
	}

	resp, err := client.Do(req)
	if err != nil {
		m.recordFailure(ctx, p)
		return
	}
	resp.Body.Close()

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		m.recordSuccess(ctx, p)
	} else {
		m.recordFailure(ctx, p)
	}
}

func (m *Manager) recordSuccess(ctx context.Context, p *Proxy) {
	p.SetStatus("healthy")
	if err := m.SetHealthStatus(ctx, p.ID, "healthy", 0); err != nil {
		log.Printf("proxy health: failed to update success for proxy %d: %v", p.ID, err)
	}
}

func (m *Manager) recordFailure(ctx context.Context, p *Proxy) {
	// Fetch current fail_count from DB to avoid drift
	var failCount int
	var status string
	err := m.db.QueryRow(ctx, `SELECT fail_count, health_status FROM proxies WHERE id = $1`, p.ID).Scan(&failCount, &status)
	if err != nil {
		log.Printf("proxy health: failed to read fail_count for proxy %d: %v", p.ID, err)
		failCount = 0
	}

	failCount++
	newStatus := status
	if failCount >= maxFailCount {
		newStatus = "unhealthy"
	}
	if status == "unknown" {
		newStatus = "unhealthy"
	}

	p.SetStatus(newStatus)
	if err := m.SetHealthStatus(ctx, p.ID, newStatus, failCount); err != nil {
		log.Printf("proxy health: failed to update failure for proxy %d: %v", p.ID, err)
	}
}
