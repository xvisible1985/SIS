// services/api-gateway/tron_handler.go
package main

import (
	"encoding/json"
	"log"
	"math"
	"math/rand"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
)

// tronDepositResp — ответ на создание депозита и запрос статуса.
type tronDepositResp struct {
	ID          string  `json:"id"`
	AmountUSDT  float64 `json:"amount_usdt"`
	AmountExact float64 `json:"amount_exact"`
	Address     string  `json:"address"`
	Status      string  `json:"status"`
	ExpiresAt   string  `json:"expires_at"`
	ConfirmedAt *string `json:"confirmed_at,omitempty"`
	TxHash      *string `json:"tx_hash,omitempty"`
}

// CreateTronDeposit создаёт новый депозит с уникальной суммой.
// POST /payments/tron/deposit
func (s *Server) CreateTronDeposit(w http.ResponseWriter, r *http.Request) {
	if s.tronAddr == "" {
		writeError(w, http.StatusServiceUnavailable, "crypto payments not configured")
		return
	}
	userID := UserIDFromCtx(r.Context())

	var req struct {
		Amount float64 `json:"amount"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if req.Amount < 1 || req.Amount > 100000 {
		writeError(w, http.StatusBadRequest, "amount must be between 1 and 100000 USDT")
		return
	}

	ctx := r.Context()

	// Генерируем уникальную сумму: прибавляем случайные 1–90 центов.
	// Повторяем до 10 раз если такая сумма уже занята другим pending депозитом.
	var amountExact float64
	var depositID string
	for attempt := 0; attempt < 10; attempt++ {
		cents := float64(rand.Intn(90)+1) / 100.0
		candidate := math.Round((req.Amount+cents)*1e6) / 1e6

		// INSERT with ON CONFLICT DO NOTHING on the partial unique index
		// (tron_deposits_pending_amount_uniq: UNIQUE amount_exact WHERE status='pending').
		// This gives DB-level guarantee against duplicate pending amounts even under
		// concurrent requests — WHERE NOT EXISTS alone is not safe under race conditions.
		err := s.pool.QueryRow(ctx,
			`INSERT INTO tron_deposits (user_id, amount_usdt, amount_exact)
			 VALUES ($1, $2, $3)
			 ON CONFLICT (amount_exact) WHERE status = 'pending' DO NOTHING
			 RETURNING id, amount_exact`,
			userID, req.Amount, candidate,
		).Scan(&depositID, &amountExact)
		if err == nil {
			break
		}
	}
	if depositID == "" {
		writeError(w, http.StatusInternalServerError, "failed to generate unique deposit amount, try again")
		return
	}

	var expiresAt time.Time
	if err := s.pool.QueryRow(ctx,
		`SELECT expires_at FROM tron_deposits WHERE id=$1`, depositID,
	).Scan(&expiresAt); err != nil {
		log.Printf("tron: fetch expires_at for %s: %v", depositID, err)
		expiresAt = time.Now().Add(30 * time.Minute)
	}

	writeJSON(w, http.StatusCreated, tronDepositResp{
		ID:          depositID,
		AmountUSDT:  req.Amount,
		AmountExact: amountExact,
		Address:     s.tronAddr,
		Status:      "pending",
		ExpiresAt:   expiresAt.UTC().Format(time.RFC3339),
	})
}

// GetTronDeposit возвращает статус конкретного депозита.
// GET /payments/tron/deposit/{id}
func (s *Server) GetTronDeposit(w http.ResponseWriter, r *http.Request) {
	userID := UserIDFromCtx(r.Context())
	id := chi.URLParam(r, "id")
	ctx := r.Context()

	var dep tronDepositResp
	var confirmedAt *time.Time
	var expiresAt time.Time
	err := s.pool.QueryRow(ctx,
		`SELECT id, amount_usdt, amount_exact, status, expires_at, confirmed_at, tx_hash
		 FROM tron_deposits
		 WHERE id=$1 AND user_id=$2`,
		id, userID,
	).Scan(&dep.ID, &dep.AmountUSDT, &dep.AmountExact,
		&dep.Status, &expiresAt, &confirmedAt, &dep.TxHash)
	if err != nil {
		writeError(w, http.StatusNotFound, "deposit not found")
		return
	}
	dep.Address = s.tronAddr
	dep.ExpiresAt = expiresAt.UTC().Format(time.RFC3339)
	if confirmedAt != nil {
		ts := confirmedAt.UTC().Format(time.RFC3339)
		dep.ConfirmedAt = &ts
	}
	writeJSON(w, http.StatusOK, dep)
}

// ListTronDeposits возвращает историю депозитов пользователя (последние 50).
// GET /payments/tron/deposits
func (s *Server) ListTronDeposits(w http.ResponseWriter, r *http.Request) {
	userID := UserIDFromCtx(r.Context())
	ctx := r.Context()

	rows, err := s.pool.Query(ctx,
		`SELECT id, amount_usdt, amount_exact, status, expires_at, confirmed_at, tx_hash
		 FROM tron_deposits
		 WHERE user_id=$1
		 ORDER BY created_at DESC
		 LIMIT 50`,
		userID,
	)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	defer rows.Close()

	var deposits []tronDepositResp
	for rows.Next() {
		var dep tronDepositResp
		var confirmedAt *time.Time
		var expiresAt time.Time
		if err := rows.Scan(&dep.ID, &dep.AmountUSDT, &dep.AmountExact,
			&dep.Status, &expiresAt, &confirmedAt, &dep.TxHash); err != nil {
			log.Printf("tron: scan deposit row: %v", err)
			continue
		}
		dep.Address = s.tronAddr
		dep.ExpiresAt = expiresAt.UTC().Format(time.RFC3339)
		if confirmedAt != nil {
			ts := confirmedAt.UTC().Format(time.RFC3339)
			dep.ConfirmedAt = &ts
		}
		deposits = append(deposits, dep)
	}
	if deposits == nil {
		deposits = []tronDepositResp{}
	}
	writeJSON(w, http.StatusOK, deposits)
}
