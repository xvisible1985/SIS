// services/api-gateway/auth_handler.go
package main

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"sis/pkg/auth"
)

type authRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

// Register creates a new user account and returns a JWT.
// POST /auth/register
func (s *Server) Register(w http.ResponseWriter, r *http.Request) {
	var req authRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	req.Email = strings.TrimSpace(strings.ToLower(req.Email))
	if req.Email == "" || len(req.Password) < 8 {
		writeError(w, http.StatusBadRequest, "email required and password must be at least 8 characters")
		return
	}

	hash, err := auth.HashPassword(req.Password)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	var userID string
	err = s.pool.QueryRow(r.Context(),
		`INSERT INTO users (email, password_hash) VALUES ($1, $2) RETURNING id`,
		req.Email, hash,
	).Scan(&userID)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			writeError(w, http.StatusConflict, "email already registered")
			return
		}
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	token, err := auth.GenerateToken(userID, string(s.jwtSecret), 24*time.Hour)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{"token": token, "user_id": userID})
}

// Login authenticates a user and returns a JWT.
// POST /auth/login
func (s *Server) Login(w http.ResponseWriter, r *http.Request) {
	var req authRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	req.Email = strings.TrimSpace(strings.ToLower(req.Email))

	var userID, hash string
	err := s.pool.QueryRow(r.Context(),
		`SELECT id, password_hash FROM users WHERE email = $1`,
		req.Email,
	).Scan(&userID, &hash)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "invalid credentials")
		return
	}

	if !auth.CheckPassword(hash, req.Password) {
		writeError(w, http.StatusUnauthorized, "invalid credentials")
		return
	}

	token, err := auth.GenerateToken(userID, string(s.jwtSecret), 24*time.Hour)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"token": token, "user_id": userID})
}
