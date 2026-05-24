// services/api-gateway/account_handler.go
package main

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"strings"
	"time"

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
	var novabotBalance float64
	err := s.pool.QueryRow(r.Context(),
		`SELECT u.email, u.plan, u.username, tc.username, u.novabot_balance
		 FROM users u
		 LEFT JOIN telegram_connections tc ON tc.user_id = u.id
		 WHERE u.id = $1`, userID,
	).Scan(&email, &plan, &username, &telegramUsername, &novabotBalance)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"email":             email,
		"username":          username,
		"plan":              plan,
		"telegram_username": telegramUsername,
		"novabot_balance":   novabotBalance,
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
	if len(local) >= 1 {
		local = local[:1] + "***"
	}
	return local + "@" + parts[1]
}

// GetTelegramLink generates a one-time deep-link token.
// GET /account/telegram-link
func (s *Server) GetTelegramLink(w http.ResponseWriter, r *http.Request) {
	userID := UserIDFromCtx(r.Context())
	token := newUUID() + newUUID() // 72-char token
	_, err := s.pool.Exec(r.Context(),
		`INSERT INTO telegram_pending_tokens (token, user_id)
		 VALUES ($1, $2)
		 ON CONFLICT (token) DO NOTHING`,
		token, userID,
	)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	botName := getEnv("TELEGRAM_BOT_NAME", "novabot")
	url := "https://t.me/" + botName + "?start=" + token
	writeJSON(w, http.StatusOK, map[string]string{"url": url})
}

// TelegramVerify is called by the Telegram bot after the user clicks the deep link.
// POST /account/telegram-verify  (no auth — token IS the secret)
func (s *Server) TelegramVerify(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Token    string `json:"token"`
		ChatID   int64  `json:"chat_id"`
		Username string `json:"username"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if req.Token == "" || req.ChatID == 0 {
		writeError(w, http.StatusBadRequest, "token and chat_id are required")
		return
	}
	var userID string
	err := s.pool.QueryRow(r.Context(),
		`DELETE FROM telegram_pending_tokens
		 WHERE token=$1 AND expires_at > NOW()
		 RETURNING user_id`,
		req.Token,
	).Scan(&userID)
	if err != nil {
		writeError(w, http.StatusBadRequest, "token invalid or expired")
		return
	}
	_, err = s.pool.Exec(r.Context(),
		`INSERT INTO telegram_connections (user_id, chat_id, username)
		 VALUES ($1, $2, $3)
		 ON CONFLICT (user_id) DO UPDATE SET chat_id=$2, username=$3, connected_at=NOW()`,
		userID, req.ChatID, req.Username,
	)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// TelegramDisconnect removes the Telegram connection.
// DELETE /account/telegram
func (s *Server) TelegramDisconnect(w http.ResponseWriter, r *http.Request) {
	userID := UserIDFromCtx(r.Context())
	if _, err := s.pool.Exec(r.Context(),
		`DELETE FROM telegram_connections WHERE user_id=$1`, userID,
	); err != nil {
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// GetNotifications returns notification settings (defaults TRUE if row not yet created).
// GET /account/notifications
func (s *Server) GetNotifications(w http.ResponseWriter, r *http.Request) {
	userID := UserIDFromCtx(r.Context())
	var onTrade, onSignal, onBalance bool
	err := s.pool.QueryRow(r.Context(),
		`SELECT on_trade, on_signal, on_balance
		 FROM telegram_notification_settings WHERE user_id=$1`, userID,
	).Scan(&onTrade, &onSignal, &onBalance)
	if err != nil {
		onTrade, onSignal, onBalance = true, true, true
	}
	writeJSON(w, http.StatusOK, map[string]bool{
		"on_trade": onTrade, "on_signal": onSignal, "on_balance": onBalance,
	})
}

// UpdateNotifications upserts notification settings.
// PATCH /account/notifications
func (s *Server) UpdateNotifications(w http.ResponseWriter, r *http.Request) {
	userID := UserIDFromCtx(r.Context())
	var req struct {
		OnTrade   *bool `json:"on_trade"`
		OnSignal  *bool `json:"on_signal"`
		OnBalance *bool `json:"on_balance"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	// Read current values, defaulting to true for new users
	onTrade, onSignal, onBalance := true, true, true
	s.pool.QueryRow(r.Context(),
		`SELECT on_trade, on_signal, on_balance
		 FROM telegram_notification_settings WHERE user_id=$1`, userID,
	).Scan(&onTrade, &onSignal, &onBalance)
	if req.OnTrade != nil {
		onTrade = *req.OnTrade
	}
	if req.OnSignal != nil {
		onSignal = *req.OnSignal
	}
	if req.OnBalance != nil {
		onBalance = *req.OnBalance
	}
	_, err := s.pool.Exec(r.Context(),
		`INSERT INTO telegram_notification_settings (user_id, on_trade, on_signal, on_balance)
		 VALUES ($1, $2, $3, $4)
		 ON CONFLICT (user_id) DO UPDATE SET on_trade=$2, on_signal=$3, on_balance=$4`,
		userID, onTrade, onSignal, onBalance,
	)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// GetReferral returns (creating if needed) the user's referral code, link, and stats.
// GET /account/referral
func (s *Server) GetReferral(w http.ResponseWriter, r *http.Request) {
	userID := UserIDFromCtx(r.Context())

	// Get or create referral code atomically.
	// ON CONFLICT DO UPDATE SET code=existing keeps the original value,
	// and RETURNING always gives us the winning code.
	var code string
	err := s.pool.QueryRow(r.Context(),
		`INSERT INTO referral_codes (user_id, code) VALUES ($1, $2)
		 ON CONFLICT (user_id) DO UPDATE SET code = referral_codes.code
		 RETURNING code`,
		userID, generateReferralCode(),
	).Scan(&code)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	// Stats
	var count int
	s.pool.QueryRow(r.Context(),
		`SELECT COUNT(*) FROM referral_signups WHERE referrer_id=$1`, userID,
	).Scan(&count)

	// Signups list
	type signup struct {
		Date        string `json:"date"`
		EmailMasked string `json:"email_masked"`
		Active      bool   `json:"active"`
	}
	rows, err := s.pool.Query(r.Context(),
		`SELECT rs.created_at, u.email,
		        (SELECT COUNT(*) > 0 FROM exchange_accounts ea WHERE ea.owner_id = u.id) AS active
		 FROM referral_signups rs
		 JOIN users u ON u.id = rs.referee_id
		 WHERE rs.referrer_id=$1
		 ORDER BY rs.created_at DESC
		 LIMIT 100`, userID,
	)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	defer rows.Close()
	var signups []signup
	for rows.Next() {
		var su signup
		var email string
		var createdAt time.Time
		if err := rows.Scan(&createdAt, &email, &su.Active); err != nil {
			continue
		}
		su.Date = createdAt.UTC().Format(time.RFC3339)
		su.EmailMasked = maskEmail(email)
		signups = append(signups, su)
	}
	if err := rows.Err(); err != nil {
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if signups == nil {
		signups = []signup{}
	}

	appURL := getEnv("APP_URL", "https://app.novabot.io")
	writeJSON(w, http.StatusOK, map[string]any{
		"code":          code,
		"link":          appURL + "/r/" + code,
		"count":         count,
		"total_rewards": 0,
		"signups":       signups,
	})
}
