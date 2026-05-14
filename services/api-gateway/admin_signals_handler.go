package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
)

type ContentTypeItem struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Status string `json:"status"` // "enabled", "test", "disabled"
	Panel  string `json:"panel"`  // "indicator" or "signal"
}

type UserContentItem struct {
	ID    string `json:"id"`
	Panel string `json:"panel"`
}

// GET /admin/signal-types
func (s *Server) ListSignalTypes(w http.ResponseWriter, r *http.Request) {
	rows, err := s.pool.Query(r.Context(),
		`SELECT id, name, status, panel FROM signal_types ORDER BY name`)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "db error")
		return
	}
	defer rows.Close()
	items := make([]ContentTypeItem, 0)
	for rows.Next() {
		var t ContentTypeItem
		if err := rows.Scan(&t.ID, &t.Name, &t.Status, &t.Panel); err == nil {
			items = append(items, t)
		}
	}
	writeJSON(w, http.StatusOK, items)
}

// PATCH /admin/signal-types/:id — update status and/or panel
func (s *Server) ToggleSignalType(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	var body struct {
		Status *string `json:"status"`
		Panel  *string `json:"panel"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	sets := []string{}
	args := []interface{}{}
	idx := 1
	if body.Status != nil {
		sets = append(sets, fmt.Sprintf("status = $%d", idx))
		args = append(args, *body.Status)
		idx++
	}
	if body.Panel != nil {
		sets = append(sets, fmt.Sprintf("panel = $%d", idx))
		args = append(args, *body.Panel)
		idx++
	}
	if len(sets) == 0 {
		writeError(w, http.StatusBadRequest, "nothing to update")
		return
	}
	sets = append(sets, "updated_at = NOW()")
	args = append(args, id)
	query := fmt.Sprintf("UPDATE signal_types SET %s WHERE id = $%d",
		strings.Join(sets, ", "), idx)
	tag, err := s.pool.Exec(r.Context(), query, args...)
	if err != nil || tag.RowsAffected() == 0 {
		writeError(w, http.StatusNotFound, "signal type not found")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"ok": "1"})
}

// GET /signal-types — enabled signals with panel info (all authenticated users)
func (s *Server) ListEnabledSignalTypes(w http.ResponseWriter, r *http.Request) {
	rows, err := s.pool.Query(r.Context(),
		`SELECT id, panel FROM signal_types WHERE status = 'enabled' ORDER BY name`)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "db error")
		return
	}
	defer rows.Close()
	items := make([]UserContentItem, 0)
	for rows.Next() {
		var t UserContentItem
		if err := rows.Scan(&t.ID, &t.Panel); err == nil {
			items = append(items, t)
		}
	}
	writeJSON(w, http.StatusOK, items)
}

// GET /admin/indicator-types
func (s *Server) ListIndicatorTypes(w http.ResponseWriter, r *http.Request) {
	rows, err := s.pool.Query(r.Context(),
		`SELECT id, name, status, panel FROM indicator_types ORDER BY name`)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "db error")
		return
	}
	defer rows.Close()
	items := make([]ContentTypeItem, 0)
	for rows.Next() {
		var t ContentTypeItem
		if err := rows.Scan(&t.ID, &t.Name, &t.Status, &t.Panel); err == nil {
			items = append(items, t)
		}
	}
	writeJSON(w, http.StatusOK, items)
}

// PATCH /admin/indicator-types/:id — update status and/or panel
func (s *Server) ToggleIndicatorType(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	var body struct {
		Status *string `json:"status"`
		Panel  *string `json:"panel"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	sets := []string{}
	args := []interface{}{}
	idx := 1
	if body.Status != nil {
		sets = append(sets, fmt.Sprintf("status = $%d", idx))
		args = append(args, *body.Status)
		idx++
	}
	if body.Panel != nil {
		sets = append(sets, fmt.Sprintf("panel = $%d", idx))
		args = append(args, *body.Panel)
		idx++
	}
	if len(sets) == 0 {
		writeError(w, http.StatusBadRequest, "nothing to update")
		return
	}
	sets = append(sets, "updated_at = NOW()")
	args = append(args, id)
	query := fmt.Sprintf("UPDATE indicator_types SET %s WHERE id = $%d",
		strings.Join(sets, ", "), idx)
	tag, err := s.pool.Exec(r.Context(), query, args...)
	if err != nil || tag.RowsAffected() == 0 {
		writeError(w, http.StatusNotFound, "indicator type not found")
		return
	}

	// Sync to signal_types: when panel changes to 'signal', the indicator becomes
	// available in the strategy signal picker (it has a Go signal engine implementation).
	// When moved back to 'indicator' panel, remove it from signal_types.
	if body.Panel != nil {
		if *body.Panel == "signal" {
			var name, status string
			s.pool.QueryRow(r.Context(),
				`SELECT name, status FROM indicator_types WHERE id=$1`, id,
			).Scan(&name, &status)
			s.pool.Exec(r.Context(), //nolint:errcheck
				`INSERT INTO signal_types (id, name, status, panel)
				 VALUES ($1, $2, $3, 'signal')
				 ON CONFLICT (id) DO UPDATE SET
				   name=EXCLUDED.name, status=EXCLUDED.status,
				   panel='signal', updated_at=NOW()`,
				id, name, status,
			)
		} else {
			s.pool.Exec(r.Context(), `DELETE FROM signal_types WHERE id=$1`, id) //nolint:errcheck
		}
	}

	writeJSON(w, http.StatusOK, map[string]string{"ok": "1"})
}

// GET /indicator-types — enabled indicators with panel info (all authenticated users)
func (s *Server) ListEnabledIndicatorTypes(w http.ResponseWriter, r *http.Request) {
	rows, err := s.pool.Query(r.Context(),
		`SELECT id, panel FROM indicator_types WHERE status = 'enabled' ORDER BY name`)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "db error")
		return
	}
	defer rows.Close()
	items := make([]UserContentItem, 0)
	for rows.Next() {
		var t UserContentItem
		if err := rows.Scan(&t.ID, &t.Panel); err == nil {
			items = append(items, t)
		}
	}
	writeJSON(w, http.StatusOK, items)
}
