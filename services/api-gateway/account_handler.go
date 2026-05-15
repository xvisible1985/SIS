// services/api-gateway/account_handler.go
package main

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"strings"

	"github.com/jackc/pgx/v5/pgconn"
	"sis/pkg/auth"
)

var usernameRe = regexp.MustCompile(`^[a-zA-Z0-9_]{3,30}$`)

// GetProfile returns the authenticated user's profile.
// GET /account/profile
func (s *Server) GetProfile(w http.ResponseWriter, r *http.Request) {
	userID := UserIDFromCtx(r.Context())
	var email, plan string
	var username, telegramUsername *string
	err := s.pool.QueryRow(r.Context(),
		`SELECT u.email, u.plan, u.username, tc.username
		 FROM users u
		 LEFT JOIN telegram_connections tc ON tc.user_id = u.id
		 WHERE u.id = $1`, userID,
	).Scan(&email, &plan, &username, &telegramUsername)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"email":             email,
		"username":          username,
		"plan":              plan,
		"telegram_username": telegramUsername,
	})
}

// UpdateProfile updates the authenticated user's username.
// PATCH /account/profile
func (s *Server) UpdateProfile(w http.ResponseWriter, r *http.Request) {
	userID := UserIDFromCtx(r.Context())
	var req struct {
		Username string `json:"username"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	req.Username = strings.TrimSpace(req.Username)
	if !usernameRe.MatchString(req.Username) {
		writeError(w, http.StatusBadRequest, "username must be 3-30 chars, letters/digits/underscore only")
		return
	}
	var email, plan string
	var telegramUsername *string
	err := s.pool.QueryRow(r.Context(),
		`UPDATE users SET username=$1 WHERE id=$2
		 RETURNING email, plan`,
		req.Username, userID,
	).Scan(&email, &plan)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			writeError(w, http.StatusConflict, "username already taken")
			return
		}
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	// fetch telegram username separately
	s.pool.QueryRow(r.Context(),
		`SELECT username FROM telegram_connections WHERE user_id=$1`, userID,
	).Scan(&telegramUsername)
	writeJSON(w, http.StatusOK, map[string]any{
		"email":             email,
		"username":          req.Username,
		"plan":              plan,
		"telegram_username": telegramUsername,
	})
}

// ChangePassword verifies the current password and replaces it.
// POST /account/change-password
func (s *Server) ChangePassword(w http.ResponseWriter, r *http.Request) {
	userID := UserIDFromCtx(r.Context())
	var req struct {
		CurrentPassword string `json:"current_password"`
		NewPassword     string `json:"new_password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if len(req.NewPassword) < 8 {
		writeError(w, http.StatusBadRequest, "new password must be at least 8 characters")
		return
	}
	var hash string
	if err := s.pool.QueryRow(r.Context(),
		`SELECT password_hash FROM users WHERE id=$1`, userID,
	).Scan(&hash); err != nil {
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if !auth.CheckPassword(hash, req.CurrentPassword) {
		writeError(w, http.StatusUnauthorized, "current password is incorrect")
		return
	}
	newHash, err := auth.HashPassword(req.NewPassword)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if _, err := s.pool.Exec(r.Context(),
		`UPDATE users SET password_hash=$1 WHERE id=$2`, newHash, userID,
	); err != nil {
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// generateReferralCode returns a random 8-char uppercase alphanumeric string.
func generateReferralCode() string {
	const chars = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"
	b := make([]byte, 8)
	rand.Read(b)
	for i := range b {
		b[i] = chars[int(b[i])%len(chars)]
	}
	return string(b)
}

// maskEmail returns "ab***@domain.com" style masked email.
func maskEmail(email string) string {
	parts := strings.SplitN(email, "@", 2)
	if len(parts) != 2 {
		return "***"
	}
	local := parts[0]
	if len(local) > 2 {
		local = local[:2] + "***"
	}
	return local + "@" + parts[1]
}
