// services/api-gateway/webhooks_handler.go
package main

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
)

type webhookRow struct {
	ID        string    `json:"id"`
	SignalID  string    `json:"signal_id"`
	URL       string    `json:"url"`
	Platform  string    `json:"platform"`
	IsActive  bool      `json:"is_active"`
	CreatedAt time.Time `json:"created_at"`
}

// ListWebhooks returns all webhooks owned by the authenticated user.
// GET /webhooks
func (s *Server) ListWebhooks(w http.ResponseWriter, r *http.Request) {
	userID := UserIDFromCtx(r.Context())
	rows, err := s.pool.Query(r.Context(),
		`SELECT id, signal_id, url, platform, is_active, created_at
		 FROM webhooks WHERE owner_id = $1 ORDER BY created_at DESC`,
		userID,
	)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "db error")
		return
	}
	defer rows.Close()
	result := make([]webhookRow, 0)
	for rows.Next() {
		var row webhookRow
		if err := rows.Scan(&row.ID, &row.SignalID, &row.URL, &row.Platform, &row.IsActive, &row.CreatedAt); err != nil {
			writeError(w, http.StatusInternalServerError, "scan error")
			return
		}
		result = append(result, row)
	}
	writeJSON(w, http.StatusOK, result)
}

// CreateWebhook creates a new webhook for a signal owned by the caller.
// POST /webhooks
func (s *Server) CreateWebhook(w http.ResponseWriter, r *http.Request) {
	userID := UserIDFromCtx(r.Context())
	var req struct {
		SignalID string `json:"signal_id"`
		URL      string `json:"url"`
		Platform string `json:"platform"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if req.SignalID == "" || req.URL == "" {
		writeError(w, http.StatusBadRequest, "signal_id and url are required")
		return
	}
	if req.Platform == "" {
		req.Platform = "custom"
	}

	var exists bool
	s.pool.QueryRow(r.Context(),
		`SELECT EXISTS(SELECT 1 FROM signals WHERE id=$1 AND owner_id=$2)`,
		req.SignalID, userID,
	).Scan(&exists)
	if !exists {
		writeError(w, http.StatusNotFound, "signal not found")
		return
	}

	var row webhookRow
	err := s.pool.QueryRow(r.Context(),
		`INSERT INTO webhooks (owner_id, signal_id, url, platform)
		 VALUES ($1, $2, $3, $4)
		 RETURNING id, signal_id, url, platform, is_active, created_at`,
		userID, req.SignalID, req.URL, req.Platform,
	).Scan(&row.ID, &row.SignalID, &row.URL, &row.Platform, &row.IsActive, &row.CreatedAt)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "db error")
		return
	}
	writeJSON(w, http.StatusCreated, row)
}

// GetWebhook returns a single webhook by ID (must be owned by caller).
// GET /webhooks/:id
func (s *Server) GetWebhook(w http.ResponseWriter, r *http.Request) {
	userID := UserIDFromCtx(r.Context())
	whID := chi.URLParam(r, "id")
	var row webhookRow
	err := s.pool.QueryRow(r.Context(),
		`SELECT id, signal_id, url, platform, is_active, created_at
		 FROM webhooks WHERE id=$1 AND owner_id=$2`,
		whID, userID,
	).Scan(&row.ID, &row.SignalID, &row.URL, &row.Platform, &row.IsActive, &row.CreatedAt)
	if err != nil {
		writeError(w, http.StatusNotFound, "webhook not found")
		return
	}
	writeJSON(w, http.StatusOK, row)
}

// UpdateWebhook updates url, platform, is_active.
// PUT /webhooks/:id
func (s *Server) UpdateWebhook(w http.ResponseWriter, r *http.Request) {
	userID := UserIDFromCtx(r.Context())
	whID := chi.URLParam(r, "id")
	var req struct {
		URL      string `json:"url"`
		Platform string `json:"platform"`
		IsActive *bool  `json:"is_active"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	_, err := s.pool.Exec(r.Context(),
		`UPDATE webhooks SET
			url       = COALESCE(NULLIF($3,''), url),
			platform  = COALESCE(NULLIF($4,''), platform),
			is_active = COALESCE($5, is_active)
		 WHERE id=$1 AND owner_id=$2`,
		whID, userID, req.URL, req.Platform, req.IsActive,
	)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "db error")
		return
	}
	s.GetWebhook(w, r)
}

// DeleteWebhook deletes a webhook owned by the caller.
// DELETE /webhooks/:id
func (s *Server) DeleteWebhook(w http.ResponseWriter, r *http.Request) {
	userID := UserIDFromCtx(r.Context())
	whID := chi.URLParam(r, "id")
	_, err := s.pool.Exec(r.Context(),
		`DELETE FROM webhooks WHERE id=$1 AND owner_id=$2`,
		whID, userID,
	)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "db error")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
