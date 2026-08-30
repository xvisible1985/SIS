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
	ID        string     `json:"id"`
	Exchange  string     `json:"exchange"`
	Label     string     `json:"label"`
	IsActive  bool       `json:"is_active"`
	CreatedAt time.Time  `json:"created_at"`
	ExpiresAt *time.Time `json:"expires_at"`

	// Risk guardrails — configurable via PATCH /accounts/{id}/risk-settings, enforced by
	// pkg/strategy's risk-monitor loop and placeMatrixLevel's risk gate.
	MarginWarnPct        float64 `json:"margin_warn_pct"`
	MarginPausePct       float64 `json:"margin_pause_pct"`
	MaxSymbolNotionalPct float64 `json:"max_symbol_notional_pct"`
	// RiskGuardEnabled is the account-level master switch for the whole risk guard above —
	// when false, riskGate (pkg/strategy/risk.go) skips both the margin-pause check and the
	// notional cap; the three threshold values stay stored but unenforced.
	RiskGuardEnabled bool `json:"risk_guard_enabled"`

	// Live risk snapshot from the running AccountRunner, if one exists for this account
	// right now (nil fields when the engine hasn't loaded this account, e.g. inactive).
	CurrentMMRatePct *float64   `json:"current_mm_rate_pct,omitempty"`
	CurrentEquity    *float64   `json:"current_equity,omitempty"`
	RiskPaused       *bool      `json:"risk_paused,omitempty"`
	RiskUpdatedAt    *time.Time `json:"risk_updated_at,omitempty"`

	// StatsClearedAt, if set, is the Dashboard "Очистить статистику" marker — GetDashboard
	// hides trade_history rows closed before it for this account. Non-destructive.
	StatsClearedAt *time.Time `json:"stats_cleared_at,omitempty"`
}

// ListAccounts returns exchange accounts for the authenticated user (no keys).
// GET /accounts
func (s *Server) ListAccounts(w http.ResponseWriter, r *http.Request) {
	userID := UserIDFromCtx(r.Context())
	rows, err := s.pool.Query(r.Context(),
		`SELECT id, exchange, label, is_active, created_at, expires_at,
		        margin_warn_pct, margin_pause_pct, max_symbol_notional_pct, risk_guard_enabled, stats_cleared_at
		 FROM exchange_accounts WHERE owner_id=$1 ORDER BY created_at DESC`, userID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "db error")
		return
	}
	defer rows.Close()
	result := make([]accountRow, 0)
	for rows.Next() {
		var a accountRow
		if err := rows.Scan(&a.ID, &a.Exchange, &a.Label, &a.IsActive, &a.CreatedAt, &a.ExpiresAt,
			&a.MarginWarnPct, &a.MarginPausePct, &a.MaxSymbolNotionalPct, &a.RiskGuardEnabled, &a.StatsClearedAt); err != nil {
			writeError(w, http.StatusInternalServerError, "scan error")
			return
		}
		if ar := s.engine.GetAccountRunner(a.ID); ar != nil {
			equity, mmRate, paused, updatedAt := ar.RiskSnapshot()
			if !updatedAt.IsZero() {
				a.CurrentMMRatePct = &mmRate
				a.CurrentEquity = &equity
				a.RiskPaused = &paused
				a.RiskUpdatedAt = &updatedAt
			}
		}
		result = append(result, a)
	}
	writeJSON(w, http.StatusOK, result)
}

// PatchAccountRiskSettings updates the configurable risk thresholds for an account.
// PATCH /accounts/{id}/risk-settings
func (s *Server) PatchAccountRiskSettings(w http.ResponseWriter, r *http.Request) {
	userID := UserIDFromCtx(r.Context())
	id := chi.URLParam(r, "id")
	var req struct {
		MarginWarnPct        float64 `json:"margin_warn_pct"`
		MarginPausePct       float64 `json:"margin_pause_pct"`
		MaxSymbolNotionalPct float64 `json:"max_symbol_notional_pct"`
		RiskGuardEnabled     bool    `json:"risk_guard_enabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if req.MarginWarnPct < 0 || req.MarginWarnPct > 100 ||
		req.MarginPausePct < 0 || req.MarginPausePct > 100 ||
		req.MaxSymbolNotionalPct <= 0 || req.MaxSymbolNotionalPct > 100 {
		writeError(w, http.StatusBadRequest, "thresholds must be within 0-100 (max_symbol_notional_pct > 0)")
		return
	}
	if req.MarginPausePct < req.MarginWarnPct {
		writeError(w, http.StatusBadRequest, "margin_pause_pct must be >= margin_warn_pct")
		return
	}
	tag, err := s.pool.Exec(r.Context(),
		`UPDATE exchange_accounts
		 SET margin_warn_pct=$1, margin_pause_pct=$2, max_symbol_notional_pct=$3, risk_guard_enabled=$4
		 WHERE id=$5 AND owner_id=$6`,
		req.MarginWarnPct, req.MarginPausePct, req.MaxSymbolNotionalPct, req.RiskGuardEnabled, id, userID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "db error")
		return
	}
	if tag.RowsAffected() == 0 {
		writeError(w, http.StatusNotFound, "account not found")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ClearAccountStats sets stats_cleared_at=NOW() for an account — the Dashboard's "Очистить
// статистику" button. Non-destructive: trade_history rows are never touched, GetDashboard
// just stops showing anything closed before this timestamp for this account.
// PATCH /accounts/{id}/clear-stats
func (s *Server) ClearAccountStats(w http.ResponseWriter, r *http.Request) {
	userID := UserIDFromCtx(r.Context())
	id := chi.URLParam(r, "id")
	tag, err := s.pool.Exec(r.Context(),
		`UPDATE exchange_accounts SET stats_cleared_at=NOW() WHERE id=$1 AND owner_id=$2`,
		id, userID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "db error")
		return
	}
	if tag.RowsAffected() == 0 {
		writeError(w, http.StatusNotFound, "account not found")
		return
	}
	w.WriteHeader(http.StatusNoContent)
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
		 RETURNING id, exchange, label, is_active, created_at, expires_at`,
		userID, req.Exchange, req.Label, encKey, encSecret,
	).Scan(&a.ID, &a.Exchange, &a.Label, &a.IsActive, &a.CreatedAt, &a.ExpiresAt)
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
	// Deliberately unrestricted for this call: VerifyAccount exists to (re)discover the
	// whitelist, so it must not be constrained by whatever was previously stored — a
	// stale or malformed whitelisted_ips (e.g. no proxy matching it) would otherwise
	// self-lock the account out of ever correcting it. Mirrors Syncer.refreshWhitelistedIPs
	// (pkg/trader/syncer.go), which learned this the same way.
	unrestricted := trader.Credentials{APIKey: apiKey, SecretKey: secret, AccountID: id}
	raw, err := trader.QueryAPI(r.Context(), unrestricted)
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
	var expiresAt *time.Time
	if parsed.ExpiredTime > 0 {
		t := time.UnixMilli(parsed.ExpiredTime)
		expiresAt = &t
		_, _ = s.pool.Exec(r.Context(),
			`UPDATE exchange_accounts SET expires_at=$1 WHERE id=$2 AND owner_id=$3`,
			expiresAt, id, userID)
	}
	// Persist the key's actual IP whitelist so future requests route only through
	// proxies whose exit IP is in it (pkg/proxy.PickForIPs). Bybit returns ["*"] (not [])
	// for a key with no IP restriction — NormalizeWhitelistedIPs maps both to nil/NULL.
	normalizedIPs := trader.NormalizeWhitelistedIPs(parsed.IPs)
	_, _ = s.pool.Exec(r.Context(),
		`UPDATE exchange_accounts SET whitelisted_ips=$1 WHERE id=$2 AND owner_id=$3`,
		normalizedIPs, id, userID)
	var proxyHost string
	if s.proxyManager != nil {
		proxyHost = s.proxyManager.LastPickedHost()
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":          true,
		"read_only":   parsed.ReadOnly == 1,
		"permissions": parsed.Permissions,
		"ips":         parsed.IPs,
		"expires_at":  parsed.ExpiredTime,
		"proxy_host":  proxyHost,
	})
}

// GetAccountBalance returns wallet balance (equity + available) from Bybit.
// Also saves a snapshot and returns 24h change if history exists.
// GET /accounts/:id/balance
func (s *Server) GetAccountBalance(w http.ResponseWriter, r *http.Request) {
	userID := UserIDFromCtx(r.Context())
	id := chi.URLParam(r, "id")
	var apiKeyEnc, secretEnc string
	var whitelistedIPs []string
	if err := s.pool.QueryRow(r.Context(),
		`SELECT api_key_enc, secret_enc, whitelisted_ips FROM exchange_accounts WHERE id=$1 AND owner_id=$2`,
		id, userID,
	).Scan(&apiKeyEnc, &secretEnc, &whitelistedIPs); err != nil {
		writeError(w, http.StatusNotFound, "account not found")
		return
	}
	apiKey, err1 := crypto.Decrypt(apiKeyEnc, s.encKey)
	secret, err2 := crypto.Decrypt(secretEnc, s.encKey)
	if err1 != nil || err2 != nil {
		writeError(w, http.StatusInternalServerError, "decryption error")
		return
	}
	creds := trader.Credentials{APIKey: apiKey, SecretKey: secret, AccountID: id, WhitelistedIPs: whitelistedIPs}
	equity, available, err := trader.GetWalletBalance(r.Context(), creds)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "message": err.Error()})
		return
	}

	// Save snapshot
	s.pool.Exec(r.Context(),
		`INSERT INTO balance_snapshots (account_id, equity) VALUES ($1, $2)`,
		id, equity,
	)

	// Find snapshot ~24h ago
	var equity24hAgo *float64
	s.pool.QueryRow(r.Context(),
		`SELECT equity FROM balance_snapshots
		 WHERE account_id=$1 AND created_at <= NOW() - INTERVAL '24 hours'
		 ORDER BY created_at DESC LIMIT 1`,
		id,
	).Scan(&equity24hAgo)

	resp := map[string]any{"ok": true, "equity": equity, "available": available}
	if equity24hAgo != nil {
		change := equity - *equity24hAgo
		var pct float64
		if *equity24hAgo != 0 {
			pct = (change / *equity24hAgo) * 100
		}
		resp["equity_24h_ago"] = *equity24hAgo
		resp["equity_change_usd"] = change
		resp["equity_change_percent"] = pct
	}

	writeJSON(w, http.StatusOK, resp)
}

// GetAccountPositions returns current open positions for an account.
// GET /accounts/:id/positions
func (s *Server) GetAccountPositions(w http.ResponseWriter, r *http.Request) {
	userID := UserIDFromCtx(r.Context())
	id := chi.URLParam(r, "id")
	var apiKeyEnc, secretEnc string
	var whitelistedIPs []string
	if err := s.pool.QueryRow(r.Context(),
		`SELECT api_key_enc, secret_enc, whitelisted_ips FROM exchange_accounts WHERE id=$1 AND owner_id=$2`,
		id, userID,
	).Scan(&apiKeyEnc, &secretEnc, &whitelistedIPs); err != nil {
		writeError(w, http.StatusNotFound, "account not found")
		return
	}
	apiKey, err1 := crypto.Decrypt(apiKeyEnc, s.encKey)
	secret, err2 := crypto.Decrypt(secretEnc, s.encKey)
	if err1 != nil || err2 != nil {
		writeError(w, http.StatusInternalServerError, "decryption error")
		return
	}
	creds := trader.Credentials{APIKey: apiKey, SecretKey: secret, AccountID: id, WhitelistedIPs: whitelistedIPs}
	positions, err := trader.FetchPositions(r.Context(), creds)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "message": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "positions": positions})
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
		 RETURNING id, exchange, label, is_active, created_at, expires_at`,
		id, userID,
	).Scan(&a.ID, &a.Exchange, &a.Label, &a.IsActive, &a.CreatedAt, &a.ExpiresAt)
	if err != nil {
		writeError(w, http.StatusNotFound, "account not found")
		return
	}
	writeJSON(w, http.StatusOK, a)
}
