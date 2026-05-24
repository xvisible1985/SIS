package main

import (
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"sis/pkg/crypto"
	"sis/pkg/proxy"
)

func fmtTime(t *time.Time) *string {
	if t == nil {
		return nil
	}
	s := t.Format(time.RFC3339)
	return &s
}

// ListProxies returns all proxies from DB (without passwords).
func (s *Server) ListProxies(w http.ResponseWriter, r *http.Request) {
	if s.proxyManager == nil {
		writeJSON(w, http.StatusOK, []any{})
		return
	}
	rows, err := s.proxyManager.ListDBProxies(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	// Strip password_enc from response
	type safeProxy struct {
		ID           int     `json:"id"`
		Protocol     string  `json:"protocol"`
		Host         string  `json:"host"`
		Port         int     `json:"port"`
		Username     *string `json:"username,omitempty"`
		Weight       int     `json:"weight"`
		IsActive     bool    `json:"is_active"`
		HealthStatus string  `json:"health_status"`
		LastChecked  *string `json:"last_checked,omitempty"`
		FailCount    int     `json:"fail_count"`
		TotalReqs    int64   `json:"total_reqs"`
		ActiveReqs   int     `json:"active_reqs"`
		CreatedAt    *string `json:"created_at,omitempty"`
		UpdatedAt    *string `json:"updated_at,omitempty"`
	}

	out := make([]safeProxy, 0, len(rows))
	for _, p := range rows {
		out = append(out, safeProxy{
			ID:           p.ID,
			Protocol:     p.Protocol,
			Host:         p.Host,
			Port:         p.Port,
			Username:     p.Username,
			Weight:       p.Weight,
			IsActive:     p.IsActive,
			HealthStatus: p.HealthStatus,
			LastChecked:  fmtTime(p.LastChecked),
			FailCount:    p.FailCount,
			TotalReqs:    p.TotalReqs,
			ActiveReqs:   p.ActiveReqs,
			CreatedAt:    fmtTime(p.CreatedAt),
			UpdatedAt:    fmtTime(p.UpdatedAt),
		})
	}
	writeJSON(w, http.StatusOK, out)
}

// CreateProxy adds a new proxy.
func (s *Server) CreateProxy(w http.ResponseWriter, r *http.Request) {
	if s.proxyManager == nil {
		writeError(w, http.StatusServiceUnavailable, "proxy manager disabled")
		return
	}
	var body struct {
		Protocol string  `json:"protocol"`
		Host     string  `json:"host"`
		Port     int     `json:"port"`
		Username *string `json:"username,omitempty"`
		Password *string `json:"password,omitempty"`
		Weight   int     `json:"weight"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if body.Protocol == "" || body.Host == "" || body.Port == 0 {
		writeError(w, http.StatusBadRequest, "protocol, host and port are required")
		return
	}
	if body.Weight <= 0 {
		body.Weight = 1
	}

	var passwordEnc *string
	if body.Password != nil && *body.Password != "" {
		enc, err := crypto.Encrypt(*body.Password, s.encKey)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "encryption failed")
			return
		}
		passwordEnc = &enc
	}

	id, err := s.proxyManager.CreateProxy(r.Context(), body.Protocol, body.Host, body.Port, body.Username, passwordEnc, body.Weight)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, map[string]int{"id": id})
}

// UpdateProxy partially updates a proxy.
func (s *Server) UpdateProxy(w http.ResponseWriter, r *http.Request) {
	if s.proxyManager == nil {
		writeError(w, http.StatusServiceUnavailable, "proxy manager disabled")
		return
	}
	idStr := chi.URLParam(r, "id")
	id, err := strconv.Atoi(idStr)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}

	var body map[string]any
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if len(body) == 0 {
		writeError(w, http.StatusBadRequest, "no fields to update")
		return
	}

	updates := make(map[string]any)
	if v, ok := body["protocol"]; ok {
		updates["protocol"] = v
	}
	if v, ok := body["host"]; ok {
		updates["host"] = v
	}
	if v, ok := body["port"]; ok {
		updates["port"] = v
	}
	if v, ok := body["username"]; ok {
		if v == nil {
			updates["username"] = nil
		} else {
			updates["username"] = v
		}
	}
	if v, ok := body["password"]; ok {
		if pass, ok := v.(string); ok && pass != "" {
			enc, err := crypto.Encrypt(pass, s.encKey)
			if err != nil {
				writeError(w, http.StatusInternalServerError, "encryption failed")
				return
			}
			updates["password_enc"] = enc
		} else {
			updates["password_enc"] = nil
		}
	}
	if v, ok := body["weight"]; ok {
		updates["weight"] = v
	}
	if v, ok := body["is_active"]; ok {
		updates["is_active"] = v
	}

	if err := s.proxyManager.UpdateProxy(r.Context(), id, updates); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// DeleteProxy removes a proxy.
func (s *Server) DeleteProxy(w http.ResponseWriter, r *http.Request) {
	if s.proxyManager == nil {
		writeError(w, http.StatusServiceUnavailable, "proxy manager disabled")
		return
	}
	idStr := chi.URLParam(r, "id")
	id, err := strconv.Atoi(idStr)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	if err := s.proxyManager.DeleteProxy(r.Context(), id); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// GetProxyMetrics returns runtime metrics for all proxies.
func (s *Server) GetProxyMetrics(w http.ResponseWriter, r *http.Request) {
	if s.proxyManager == nil {
		writeJSON(w, http.StatusOK, []proxy.Snapshot{})
		return
	}
	snaps := s.proxyManager.Snapshots()
	writeJSON(w, http.StatusOK, snaps)
}

// Ensure Server implements proxy handler interface expectations
var _ = proxy.Snapshot{}
