// services/api-gateway/bot_approval_handler.go
package main

import (
	"fmt"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
)

// POST /bots/{id}/request-approval (RequireAuth)
// Submits a user bot for admin review.
// Requires: is_official = false, accumulated active time >= min_publish_days.
func (s *Server) RequestBotApproval(w http.ResponseWriter, r *http.Request) {
	callerID := UserIDFromCtx(r.Context())
	botID := chi.URLParam(r, "id")
	ctx := r.Context()

	var accSecs int64
	var activeSince *time.Time
	if err := s.pool.QueryRow(ctx,
		`SELECT active_seconds_acc, active_since
		 FROM bots WHERE id = $1 AND owner_id = $2 AND is_official = false`,
		botID, callerID,
	).Scan(&accSecs, &activeSince); err != nil {
		writeError(w, http.StatusNotFound, "bot not found")
		return
	}

	// Include current active session in effective total.
	effectiveSecs := accSecs
	if activeSince != nil {
		effectiveSecs += int64(time.Since(*activeSince).Seconds())
	}

	// Load threshold from platform settings.
	var minDays int
	if err := s.pool.QueryRow(ctx,
		`SELECT min_publish_days FROM coin_filter_settings WHERE id = 1`,
	).Scan(&minDays); err != nil {
		minDays = 15
	}

	thresholdSecs := int64(minDays) * 86400
	if effectiveSecs < thresholdSecs {
		daysActive := effectiveSecs / 86400
		writeError(w, http.StatusUnprocessableEntity,
			fmt.Sprintf("Недостаточно активных дней: %d из %d", daysActive, minDays))
		return
	}

	if _, err := s.pool.Exec(ctx,
		`UPDATE bots SET approval_status = 'pending', updated_at = NOW()
		 WHERE id = $1 AND owner_id = $2`,
		botID, callerID,
	); err != nil {
		writeError(w, http.StatusInternalServerError, "db error")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// POST /admin/bots/{id}/approve (RequireAdmin)
func (s *Server) ApproveBotPublication(w http.ResponseWriter, r *http.Request) {
	botID := chi.URLParam(r, "id")
	tag, err := s.pool.Exec(r.Context(),
		`UPDATE bots SET approval_status = 'approved', updated_at = NOW() WHERE id = $1`,
		botID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "db error")
		return
	}
	if tag.RowsAffected() == 0 {
		writeError(w, http.StatusNotFound, "bot not found")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// POST /admin/bots/{id}/reject (RequireAdmin)
func (s *Server) RejectBotPublication(w http.ResponseWriter, r *http.Request) {
	botID := chi.URLParam(r, "id")
	tag, err := s.pool.Exec(r.Context(),
		`UPDATE bots SET approval_status = 'rejected', updated_at = NOW() WHERE id = $1`,
		botID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "db error")
		return
	}
	if tag.RowsAffected() == 0 {
		writeError(w, http.StatusNotFound, "bot not found")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
