package main

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
)

// ListTemplates returns all strategy templates for the authenticated user.
// GET /strategy-templates
func (s *Server) ListTemplates(w http.ResponseWriter, r *http.Request) {
	userID := UserIDFromCtx(r.Context())
	rows, err := s.pool.Query(r.Context(),
		`SELECT id, name, config::text, created_at
		 FROM strategy_templates WHERE owner_id=$1 ORDER BY created_at DESC`, userID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer rows.Close()

	type row struct {
		ID        string          `json:"id"`
		Name      string          `json:"name"`
		Config    json.RawMessage `json:"config"`
		CreatedAt time.Time       `json:"created_at"`
	}
	result := make([]row, 0)
	for rows.Next() {
		var r row
		var configStr string
		if err := rows.Scan(&r.ID, &r.Name, &configStr, &r.CreatedAt); err != nil {
			writeError(w, http.StatusInternalServerError, "scan error")
			return
		}
		r.Config = json.RawMessage(configStr)
		result = append(result, r)
	}
	writeJSON(w, http.StatusOK, result)
}

// CreateTemplate saves current strategy settings as a named template.
// POST /strategy-templates
func (s *Server) CreateTemplate(w http.ResponseWriter, r *http.Request) {
	userID := UserIDFromCtx(r.Context())
	var req struct {
		Name   string          `json:"name"`
		Config json.RawMessage `json:"config"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if req.Name == "" || req.Config == nil {
		writeError(w, http.StatusBadRequest, "name and config are required")
		return
	}
	var id string
	err := s.pool.QueryRow(r.Context(),
		`INSERT INTO strategy_templates (owner_id, name, config)
		 VALUES ($1, $2, $3::jsonb) RETURNING id`,
		userID, req.Name, string(req.Config),
	).Scan(&id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{"id": id})
}

// DeleteTemplate removes a template.
// DELETE /strategy-templates/{id}
func (s *Server) DeleteTemplate(w http.ResponseWriter, r *http.Request) {
	userID := UserIDFromCtx(r.Context())
	id := chi.URLParam(r, "id")
	tag, err := s.pool.Exec(r.Context(),
		`DELETE FROM strategy_templates WHERE id=$1 AND owner_id=$2`, id, userID)
	if err != nil || tag.RowsAffected() == 0 {
		writeError(w, http.StatusNotFound, "template not found")
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}
