package main

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"sis/pkg/crypto"
	"sis/pkg/trader"
)

type accountRow struct {
	ID        string    `json:"id"`
	Exchange  string    `json:"exchange"`
	Label     string    `json:"label"`
	IsActive  bool      `json:"is_active"`
	CreatedAt time.Time `json:"created_at"`
}

// ListAccounts returns exchange accounts for the authenticated user (no keys).
// GET /accounts
func (s *Server) ListAccounts(w http.ResponseWriter, r *http.Request) {
	userID := UserIDFromCtx(r.Context())
	rows, err := s.pool.Query(r.Context(),
		`SELECT id, exchange, label, is_active, created_at
		 FROM exchange_accounts WHERE owner_id=$1 ORDER BY created_at DESC`, userID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "db error")
		return
	}
	defer rows.Close()
	result := make([]accountRow, 0)
	for rows.Next() {
		var a accountRow
		if err := rows.Scan(&a.ID, &a.Exchange, &a.Label, &a.IsActive, &a.CreatedAt); err != nil {
			writeError(w, http.StatusInternalServerError, "scan error")
			return
		}
		result = append(result, a)
	}
	writeJSON(w, http.StatusOK, result)
}

// CreateAccount encrypts and stores a new exchange account.
// POST /accounts
func (s *Server) CreateAccount(w http.ResponseWriter, r *http.Request) {
	userID := UserIDFromCtx(r.Context())
	var req struct {
		Exchange string `json:"exchange"`
		Label    string `json:"label"`
		APIKey   string `json:"api_key"`
		Secret   string `json:"secret"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if req.Exchange == "" || req.APIKey == "" || req.Secret == "" {
		writeError(w, http.StatusBadRequest, "exchange, api_key and secret are required")
		return
	}
	encKey, err := crypto.Encrypt(req.APIKey, s.encKey)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "encryption error")
		return
	}
	encSecret, err := crypto.Encrypt(req.Secret, s.encKey)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "encryption error")
		return
	}
	var a accountRow
	err = s.pool.QueryRow(r.Context(),
		`INSERT INTO exchange_accounts (owner_id, exchange, label, api_key_enc, secret_enc)
		 VALUES ($1,$2,$3,$4,$5)
		 RETURNING id, exchange, label, is_active, created_at`,
		userID, req.Exchange, req.Label, encKey, encSecret,
	).Scan(&a.ID, &a.Exchange, &a.Label, &a.IsActive, &a.CreatedAt)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "db error")
		return
	}
	writeJSON(w, http.StatusCreated, a)
}

// DeleteAccount removes an exchange account owned by the caller.
// DELETE /accounts/:id
func (s *Server) DeleteAccount(w http.ResponseWriter, r *http.Request) {
	userID := UserIDFromCtx(r.Context())
	id := chi.URLParam(r, "id")
	_, err := s.pool.Exec(r.Context(),
		`DELETE FROM exchange_accounts WHERE id=$1 AND owner_id=$2`, id, userID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "db error")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// VerifyAccount checks that the stored API keys are valid via Bybit.
// GET /accounts/:id/verify
func (s *Server) VerifyAccount(w http.ResponseWriter, r *http.Request) {
	userID := UserIDFromCtx(r.Context())
	id := chi.URLParam(r, "id")
	var apiKeyEnc, secretEnc string
	if err := s.pool.QueryRow(r.Context(),
		`SELECT api_key_enc, secret_enc FROM exchange_accounts WHERE id=$1 AND owner_id=$2`,
		id, userID,
	).Scan(&apiKeyEnc, &secretEnc); err != nil {
		writeError(w, http.StatusNotFound, "account not found")
		return
	}
	apiKey, err1 := crypto.Decrypt(apiKeyEnc, s.encKey)
	secret, err2 := crypto.Decrypt(secretEnc, s.encKey)
	if err1 != nil || err2 != nil {
		writeError(w, http.StatusInternalServerError, "decryption error")
		return
	}
	creds := trader.Credentials{APIKey: apiKey, SecretKey: secret}
	raw, err := trader.QueryAPI(r.Context(), creds)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "message": err.Error()})
		return
	}
	var parsed struct {
		ReadOnly    int                 `json:"readOnly"`
		Permissions map[string][]string `json:"permissions"`
		IPs         []string            `json:"ips"`
		ExpiredTime int64               `json:"expiredTime"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":          true,
		"read_only":   parsed.ReadOnly == 1,
		"permissions": parsed.Permissions,
		"ips":         parsed.IPs,
		"expires_at":  parsed.ExpiredTime,
	})
}

// GetAccountBalance returns wallet balance (equity + available) from Bybit.
// GET /accounts/:id/balance
func (s *Server) GetAccountBalance(w http.ResponseWriter, r *http.Request) {
	userID := UserIDFromCtx(r.Context())
	id := chi.URLParam(r, "id")
	var apiKeyEnc, secretEnc string
	if err := s.pool.QueryRow(r.Context(),
		`SELECT api_key_enc, secret_enc FROM exchange_accounts WHERE id=$1 AND owner_id=$2`,
		id, userID,
	).Scan(&apiKeyEnc, &secretEnc); err != nil {
		writeError(w, http.StatusNotFound, "account not found")
		return
	}
	apiKey, err1 := crypto.Decrypt(apiKeyEnc, s.encKey)
	secret, err2 := crypto.Decrypt(secretEnc, s.encKey)
	if err1 != nil || err2 != nil {
		writeError(w, http.StatusInternalServerError, "decryption error")
		return
	}
	creds := trader.Credentials{APIKey: apiKey, SecretKey: secret}
	equity, available, err := trader.GetWalletBalance(r.Context(), creds)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "message": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "equity": equity, "available": available})
}

// ToggleAccountActive flips is_active for an account.
// PATCH /accounts/:id/active
func (s *Server) ToggleAccountActive(w http.ResponseWriter, r *http.Request) {
	userID := UserIDFromCtx(r.Context())
	id := chi.URLParam(r, "id")
	var a accountRow
	err := s.pool.QueryRow(r.Context(),
		`UPDATE exchange_accounts SET is_active = NOT is_active
		 WHERE id=$1 AND owner_id=$2
		 RETURNING id, exchange, label, is_active, created_at`,
		id, userID,
	).Scan(&a.ID, &a.Exchange, &a.Label, &a.IsActive, &a.CreatedAt)
	if err != nil {
		writeError(w, http.StatusNotFound, "account not found")
		return
	}
	writeJSON(w, http.StatusOK, a)
}
