package main

import (
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"sis/pkg/auth"
	"sis/pkg/crypto"
)

// ── Response types ──────────────────────────────────────────────────────────

type adminAccountResp struct {
	ID       string    `json:"id"`
	Exchange string    `json:"exchange"`
	Label    string    `json:"label"`
	APIKey   string    `json:"apiKey"`
	Perms    []string  `json:"perms"`
	Added    time.Time `json:"added"`
}

type adminUserResp struct {
	ID            string             `json:"id"`
	Email         string             `json:"email"`
	Name          string             `json:"name"`
	Role          string             `json:"role"`
	Curator       bool               `json:"curator"`
	Status        string             `json:"status"`
	Balance       float64            `json:"balance"`
	Joined        time.Time          `json:"joined"`
	LastActive    time.Time          `json:"lastActive"`
	EmailVerified bool               `json:"emailVerified"`
	ReferrerID    *string            `json:"refererId"`
	BlockReason   *string            `json:"blockReason"`
	Accounts      []adminAccountResp `json:"accounts"`
}

// ── Helpers ─────────────────────────────────────────────────────────────────

func userStatus(isBlocked, emailVerified bool) string {
	if isBlocked {
		return "blocked"
	}
	if !emailVerified {
		return "pending"
	}
	return "active"
}

// ── Handlers ─────────────────────────────────────────────────────────────────

// ListAdminUsers returns all users with decrypted exchange accounts.
// GET /admin/users
func (s *Server) ListAdminUsers(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	rows, err := s.pool.Query(ctx, `
		SELECT id, email, role, is_curator, is_blocked, email_verified,
		       referrer_id, novabot_balance, block_reason, created_at
		FROM users
		ORDER BY created_at DESC
	`)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "db error")
		return
	}
	defer rows.Close()

	users := make([]adminUserResp, 0)
	idx := make(map[string]int)

	for rows.Next() {
		var u adminUserResp
		var isBlocked, curator, emailVerified bool
		if err := rows.Scan(
			&u.ID, &u.Email, &u.Role, &curator, &isBlocked, &emailVerified,
			&u.ReferrerID, &u.Balance, &u.BlockReason, &u.Joined,
		); err != nil {
			writeError(w, http.StatusInternalServerError, "scan error")
			return
		}
		u.Name = u.Email
		u.Curator = curator
		u.Status = userStatus(isBlocked, emailVerified)
		u.EmailVerified = emailVerified
		u.LastActive = u.Joined
		u.Accounts = []adminAccountResp{}
		idx[u.ID] = len(users)
		users = append(users, u)
	}

	if err := rows.Err(); err != nil {
		writeError(w, http.StatusInternalServerError, "db error")
		return
	}

	accRows, err := s.pool.Query(ctx, `
		SELECT id, owner_id, exchange, label, api_key_enc, created_at
		FROM exchange_accounts
		ORDER BY created_at DESC
	`)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "db error")
		return
	}
	defer accRows.Close()

	for accRows.Next() {
		var id, ownerID, exchange, label, keyEnc string
		var added time.Time
		if err := accRows.Scan(&id, &ownerID, &exchange, &label, &keyEnc, &added); err != nil {
			continue
		}
		apiKey, err := crypto.Decrypt(keyEnc, s.encKey)
		if err != nil {
			apiKey = "***"
		}
		i, ok := idx[ownerID]
		if !ok {
			continue
		}
		users[i].Accounts = append(users[i].Accounts, adminAccountResp{
			ID:       id,
			Exchange: exchange,
			Label:    label,
			APIKey:   apiKey,
			Perms:    []string{},
			Added:    added,
		})
	}

	if err := accRows.Err(); err != nil {
		writeError(w, http.StatusInternalServerError, "db error")
		return
	}

	writeJSON(w, http.StatusOK, users)
}

// PatchAdminUser updates role, curator flag, and/or referrer for a user.
// PATCH /admin/users/{id}
func (s *Server) PatchAdminUser(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	ctx := r.Context()

	// Use raw map so we can distinguish absent keys from explicit null (for refererId).
	var raw map[string]json.RawMessage
	if err := json.NewDecoder(r.Body).Decode(&raw); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}

	if roleRaw, ok := raw["role"]; ok {
		var role string
		if err := json.Unmarshal(roleRaw, &role); err != nil {
			writeError(w, http.StatusBadRequest, "invalid role")
			return
		}
		if role != "user" && role != "admin" {
			writeError(w, http.StatusBadRequest, "role must be user or admin")
			return
		}
		if _, err := s.pool.Exec(ctx, `UPDATE users SET role=$1 WHERE id=$2`, role, id); err != nil {
			writeError(w, http.StatusInternalServerError, "db error")
			return
		}
	}

	if curatorRaw, ok := raw["curator"]; ok {
		var curator bool
		if err := json.Unmarshal(curatorRaw, &curator); err != nil {
			writeError(w, http.StatusBadRequest, "invalid curator")
			return
		}
		if _, err := s.pool.Exec(ctx, `UPDATE users SET is_curator=$1 WHERE id=$2`, curator, id); err != nil {
			writeError(w, http.StatusInternalServerError, "db error")
			return
		}
	}

	if refRaw, ok := raw["refererId"]; ok {
		var refID *string
		if err := json.Unmarshal(refRaw, &refID); err != nil {
			writeError(w, http.StatusBadRequest, "invalid refererId")
			return
		}
		if _, err := s.pool.Exec(ctx, `UPDATE users SET referrer_id=$1 WHERE id=$2`, refID, id); err != nil {
			writeError(w, http.StatusInternalServerError, "db error")
			return
		}
	}

	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// AdminVerifyEmail manually sets email_verified=true.
// POST /admin/users/{id}/email/verify
func (s *Server) AdminVerifyEmail(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if _, err := s.pool.Exec(r.Context(), `UPDATE users SET email_verified=true WHERE id=$1`, id); err != nil {
		writeError(w, http.StatusInternalServerError, "db error")
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// AdminResetEmail sets email_verified=false.
// POST /admin/users/{id}/email/reset
func (s *Server) AdminResetEmail(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if _, err := s.pool.Exec(r.Context(), `UPDATE users SET email_verified=false WHERE id=$1`, id); err != nil {
		writeError(w, http.StatusInternalServerError, "db error")
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// AdminResendEmail is a stub — email service not implemented.
// POST /admin/users/{id}/email/resend
func (s *Server) AdminResendEmail(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// AdminSetPassword changes a user's password.
// POST /admin/users/{id}/password
func (s *Server) AdminSetPassword(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	var req struct {
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || len(req.Password) < 6 {
		writeError(w, http.StatusBadRequest, "password must be at least 6 characters")
		return
	}
	hash, err := auth.HashPassword(req.Password)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "hash error")
		return
	}
	if _, err := s.pool.Exec(r.Context(), `UPDATE users SET password_hash=$1 WHERE id=$2`, hash, id); err != nil {
		writeError(w, http.StatusInternalServerError, "db error")
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// AdjustNovabotBalance atomically adjusts a user's novabot balance and records the transaction.
// POST /admin/users/{id}/balance/adjust
func (s *Server) AdjustNovabotBalance(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	adminID := UserIDFromCtx(r.Context())

	var req struct {
		Amount float64 `json:"amount"`
		Note   string  `json:"note"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if req.Amount == 0 {
		writeError(w, http.StatusBadRequest, "amount must be non-zero")
		return
	}

	ctx := r.Context()
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "tx error")
		return
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx,
		`UPDATE users SET novabot_balance = novabot_balance + $1 WHERE id = $2`,
		req.Amount, id,
	); err != nil {
		writeError(w, http.StatusInternalServerError, "db error")
		return
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO novabot_transactions (user_id, admin_id, amount, note) VALUES ($1, $2, $3, $4)`,
		id, adminID, req.Amount, req.Note,
	); err != nil {
		writeError(w, http.StatusInternalServerError, "db error")
		return
	}
	if err := tx.Commit(ctx); err != nil {
		writeError(w, http.StatusInternalServerError, "commit error")
		return
	}

	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// ListNovabotTransactions returns the transaction history for a user.
// Query params: limit (default 50, max 200), offset (default 0), type (all|credit|debit, default all)
// GET /admin/users/{id}/transactions
func (s *Server) ListNovabotTransactions(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")

	limit := 50
	if v, _ := strconv.Atoi(r.URL.Query().Get("limit")); v > 0 && v <= 200 {
		limit = v
	}
	offset := 0
	if v, _ := strconv.Atoi(r.URL.Query().Get("offset")); v > 0 {
		offset = v
	}
	txType := r.URL.Query().Get("type")
	if txType == "" {
		txType = "all"
	}

	var where string
	var args []any
	args = append(args, id)
	switch txType {
	case "credit":
		where = " AND amount > 0"
	case "debit":
		where = " AND amount < 0"
	}

	rows, err := s.pool.Query(r.Context(),
		`SELECT id, admin_id, amount, note, created_at
		 FROM novabot_transactions WHERE user_id=$1`+where+
		` ORDER BY created_at DESC LIMIT $2 OFFSET $3`, append(args, limit, offset)...)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "db error")
		return
	}
	defer rows.Close()
	type txRow struct {
		ID        string    `json:"id"`
		AdminID   *string   `json:"admin_id"`
		Amount    float64   `json:"amount"`
		Note      string    `json:"note"`
		CreatedAt time.Time `json:"created_at"`
	}
	var result []txRow
	for rows.Next() {
		var t txRow
		var adminID *string
		if err := rows.Scan(&t.ID, &adminID, &t.Amount, &t.Note, &t.CreatedAt); err != nil {
			writeError(w, http.StatusInternalServerError, "scan error")
			return
		}
		t.AdminID = adminID
		result = append(result, t)
	}
	writeJSON(w, http.StatusOK, result)
}

// BlockAdminUser sets is_blocked=true with a reason.
// POST /admin/users/{id}/block
func (s *Server) BlockAdminUser(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	var req struct {
		Reason string `json:"reason"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil && r.ContentLength > 0 {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if _, err := s.pool.Exec(r.Context(),
		`UPDATE users SET is_blocked=true, block_reason=$1 WHERE id=$2`,
		req.Reason, id,
	); err != nil {
		writeError(w, http.StatusInternalServerError, "db error")
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// UnblockAdminUser sets is_blocked=false and clears block_reason.
// POST /admin/users/{id}/unblock
func (s *Server) UnblockAdminUser(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if _, err := s.pool.Exec(r.Context(),
		`UPDATE users SET is_blocked=false, block_reason=NULL WHERE id=$1`, id,
	); err != nil {
		writeError(w, http.StatusInternalServerError, "db error")
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// DeleteAdminAccount deletes an exchange account belonging to a user.
// DELETE /admin/users/{id}/accounts/{aid}
func (s *Server) DeleteAdminAccount(w http.ResponseWriter, r *http.Request) {
	userID := chi.URLParam(r, "id")
	accID := chi.URLParam(r, "aid")
	tag, err := s.pool.Exec(r.Context(),
		`DELETE FROM exchange_accounts WHERE id=$1 AND owner_id=$2`, accID, userID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "db error")
		return
	}
	if tag.RowsAffected() == 0 {
		writeError(w, http.StatusNotFound, "account not found")
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}
