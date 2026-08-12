package main

import (
	"errors"
	"log"
	"net/http"

	"github.com/jackc/pgx/v5"
	"sis/pkg/auth"
	"sis/pkg/crypto"
	"sis/pkg/trader"
)

// PositionsStream streams Bybit private positions and orders to the client.
// GET /ws/trader/positions?token=<JWT>&account_id=<UUID>
func (s *Server) PositionsStream(w http.ResponseWriter, r *http.Request) {
	tokenStr := r.URL.Query().Get("token")
	if tokenStr == "" {
		http.Error(w, "missing token", http.StatusUnauthorized)
		return
	}
	userID, err := auth.ValidateToken(tokenStr, string(s.jwtSecret))
	if err != nil {
		http.Error(w, "invalid token", http.StatusUnauthorized)
		return
	}

	accountID := r.URL.Query().Get("account_id")
	if accountID == "" {
		http.Error(w, "account_id required", http.StatusBadRequest)
		return
	}

	var apiKeyEnc, secretEnc, label string
	var whitelistedIPs []string
	err = s.pool.QueryRow(r.Context(),
		`SELECT api_key_enc, secret_enc, label, whitelisted_ips FROM exchange_accounts WHERE id=$1 AND owner_id=$2 AND is_active=TRUE`,
		accountID, userID,
	).Scan(&apiKeyEnc, &secretEnc, &label, &whitelistedIPs)
	if err != nil {
		if !errors.Is(err, pgx.ErrNoRows) {
			log.Printf("trader ws: query account %s: %v", accountID, err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		if !s.isAdmin(r.Context(), userID) {
			http.Error(w, "account not found", http.StatusNotFound)
			return
		}
		// Admin fallback: allow streaming any account regardless of ownership.
		err = s.pool.QueryRow(r.Context(),
			`SELECT api_key_enc, secret_enc, label, whitelisted_ips FROM exchange_accounts WHERE id=$1 AND is_active=TRUE`,
			accountID,
		).Scan(&apiKeyEnc, &secretEnc, &label, &whitelistedIPs)
		if err != nil {
			if !errors.Is(err, pgx.ErrNoRows) {
				log.Printf("trader ws: query account %s (admin): %v", accountID, err)
				http.Error(w, "internal error", http.StatusInternalServerError)
				return
			}
			http.Error(w, "account not found", http.StatusNotFound)
			return
		}
	}

	apiKey, err := crypto.Decrypt(apiKeyEnc, s.encKey)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	secretKey, err := crypto.Decrypt(secretEnc, s.encKey)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("trader ws: upgrade: %v", err)
		return
	}
	defer conn.Close()

	creds := trader.Credentials{APIKey: apiKey, SecretKey: secretKey, AccountID: accountID, WhitelistedIPs: whitelistedIPs}
	broadcastCh, unsub := s.subscribeBroadcast(accountID)
	defer unsub()
	trader.RunPositionStream(r.Context(), conn, creds, label, broadcastCh)
}

