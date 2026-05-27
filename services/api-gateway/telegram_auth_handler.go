// services/api-gateway/telegram_auth_handler.go
package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"
	"sis/pkg/auth"
)

// TelegramLoginRequest is called by the bot when a user sends /login.
// It finds or auto-creates a user for the given chat_id, then returns a magic URL.
// POST /auth/telegram  (requires BOT_SECRET)
func (s *Server) TelegramLoginRequest(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ChatID   int64  `json:"chat_id"`
		Username string `json:"username"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.ChatID == 0 {
		writeError(w, http.StatusBadRequest, "chat_id required")
		return
	}

	ctx := r.Context()

	// 1. Lookup existing telegram connection
	var userID string
	err := s.pool.QueryRow(ctx,
		`SELECT user_id FROM telegram_connections WHERE chat_id = $1`, req.ChatID,
	).Scan(&userID)

	if errors.Is(err, pgx.ErrNoRows) {
		// 2. Auto-register: create new user
		email := fmt.Sprintf("tg_%d@telegram.invalid", req.ChatID)
		err = s.pool.QueryRow(ctx,
			`INSERT INTO users (email, password_hash)
			 VALUES ($1, '')
			 ON CONFLICT (email) DO UPDATE SET email = EXCLUDED.email
			 RETURNING id`,
			email,
		).Scan(&userID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "internal error")
			return
		}
		// Link telegram connection
		_, err = s.pool.Exec(ctx,
			`INSERT INTO telegram_connections (user_id, chat_id, username)
			 VALUES ($1, $2, $3)
			 ON CONFLICT (user_id) DO UPDATE SET chat_id=$2, username=$3, connected_at=NOW()`,
			userID, req.ChatID, req.Username,
		)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "internal error")
			return
		}
	} else if err != nil {
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	// userID is set either from lookup or auto-registration

	// 3. Generate one-time auth token
	token := newUUID()
	_, err = s.pool.Exec(ctx,
		`INSERT INTO telegram_auth_tokens (token, chat_id) VALUES ($1, $2)`,
		token, req.ChatID,
	)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	appURL := getEnv("APP_URL", "https://app.novabot.io")
	writeJSON(w, http.StatusOK, map[string]any{
		"url": appURL + "/login?tg=" + token,
	})
}

// TelegramLoginCallback is called by the frontend when the user clicks the magic link.
// It exchanges the one-time token for a JWT.
// POST /auth/telegram-callback  (public)
func (s *Server) TelegramLoginCallback(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Token == "" {
		writeError(w, http.StatusBadRequest, "token required")
		return
	}

	ctx := r.Context()

	// Consume token (atomic delete-and-return)
	var chatID int64
	err := s.pool.QueryRow(ctx,
		`DELETE FROM telegram_auth_tokens
		 WHERE token = $1 AND expires_at > NOW()
		 RETURNING chat_id`,
		req.Token,
	).Scan(&chatID)
	if err != nil {
		writeError(w, http.StatusBadRequest, "token invalid or expired")
		return
	}

	// Resolve user
	var userID, email string
	var isBlocked bool
	err = s.pool.QueryRow(ctx,
		`SELECT u.id, u.email, u.is_blocked
		 FROM users u
		 JOIN telegram_connections tc ON tc.user_id = u.id
		 WHERE tc.chat_id = $1`, chatID,
	).Scan(&userID, &email, &isBlocked)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "user not found")
		return
	}
	if isBlocked {
		writeError(w, http.StatusForbidden, "account blocked")
		return
	}

	token, err := auth.GenerateToken(userID, string(s.jwtSecret), 24*time.Hour)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"token":    token,
		"user_id":  userID,
		"email":    email,
		"is_admin": s.adminEmails[email],
	})
}
