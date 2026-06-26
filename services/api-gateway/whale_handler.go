// services/api-gateway/whale_handler.go
package main

import (
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
)

// GET /admin/whale/addresses
func (s *Server) ListWhaleAddresses(w http.ResponseWriter, r *http.Request) {
	rows, err := s.pool.Query(r.Context(), `
		SELECT id, address, chain, label, is_manual, volume_30d, is_active, last_seen_at, created_at
		FROM whale_addresses
		ORDER BY volume_30d DESC
		LIMIT 200
	`)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "db error")
		return
	}
	defer rows.Close()
	type row struct {
		ID         string     `json:"id"`
		Address    string     `json:"address"`
		Chain      string     `json:"chain"`
		Label      *string    `json:"label"`
		IsManual   bool       `json:"isManual"`
		Volume30d  float64    `json:"volume30d"`
		IsActive   bool       `json:"isActive"`
		LastSeenAt *time.Time `json:"lastSeenAt"`
		CreatedAt  time.Time  `json:"createdAt"`
	}
	var result []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.ID, &r.Address, &r.Chain, &r.Label, &r.IsManual,
			&r.Volume30d, &r.IsActive, &r.LastSeenAt, &r.CreatedAt); err != nil {
			continue
		}
		result = append(result, r)
	}
	if result == nil {
		result = []row{}
	}
	writeJSON(w, http.StatusOK, result)
}

// POST /admin/whale/addresses
func (s *Server) CreateWhaleAddress(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Address string  `json:"address"`
		Chain   string  `json:"chain"`
		Label   *string `json:"label"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Address == "" {
		writeError(w, http.StatusBadRequest, "address and chain required")
		return
	}
	if body.Chain != "eth" && body.Chain != "tron" {
		writeError(w, http.StatusBadRequest, "chain must be eth or tron")
		return
	}
	var id string
	err := s.pool.QueryRow(r.Context(), `
		INSERT INTO whale_addresses (address, chain, label, is_manual)
		VALUES ($1, $2, $3, true)
		ON CONFLICT (address, chain) DO UPDATE
		SET label = EXCLUDED.label, is_active = true, is_manual = true
		RETURNING id
	`, body.Address, body.Chain, body.Label).Scan(&id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "db error")
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{"id": id})
}

// PATCH /admin/whale/addresses/{id}
func (s *Server) PatchWhaleAddress(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	var body struct {
		Label    *string `json:"label"`
		IsActive *bool   `json:"isActive"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if body.Label != nil {
		s.pool.Exec(r.Context(), `UPDATE whale_addresses SET label=$1 WHERE id=$2`, *body.Label, id)
	}
	if body.IsActive != nil {
		s.pool.Exec(r.Context(), `UPDATE whale_addresses SET is_active=$1 WHERE id=$2`, *body.IsActive, id)
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// DELETE /admin/whale/addresses/{id}
func (s *Server) DeleteWhaleAddress(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	_, err := s.pool.Exec(r.Context(), `UPDATE whale_addresses SET is_active=false WHERE id=$1`, id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "db error")
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// GET /admin/whale/events
func (s *Server) ListWhaleEvents(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := s.pool.Query(r.Context(), `
		SELECT we.id, we.address, COALESCE(wa.label, ''), we.chain, we.symbol,
		       we.amount_usd, we.direction, we.score, we.tx_hash, we.detected_at
		FROM whale_events we
		LEFT JOIN whale_addresses wa ON wa.address = we.address AND wa.chain = we.chain
		ORDER BY we.detected_at DESC
		LIMIT $1
	`, limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "db error")
		return
	}
	defer rows.Close()
	type row struct {
		ID         string    `json:"id"`
		Address    string    `json:"address"`
		Label      string    `json:"label"`
		Chain      string    `json:"chain"`
		Symbol     string    `json:"symbol"`
		AmountUSD  float64   `json:"amountUsd"`
		Direction  string    `json:"direction"`
		Score      float64   `json:"score"`
		TxHash     string    `json:"txHash"`
		DetectedAt time.Time `json:"detectedAt"`
	}
	var result []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.ID, &r.Address, &r.Label, &r.Chain, &r.Symbol,
			&r.AmountUSD, &r.Direction, &r.Score, &r.TxHash, &r.DetectedAt); err != nil {
			continue
		}
		result = append(result, r)
	}
	if result == nil {
		result = []row{}
	}
	writeJSON(w, http.StatusOK, result)
}

// GET /admin/whale/state
func (s *Server) GetWhaleState(w http.ResponseWriter, r *http.Request) {
	states, err := s.rdb.HGetAll(r.Context(), "whale:state").Result()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "redis error")
		return
	}
	rows, _ := s.pool.Query(r.Context(), `
		SELECT DISTINCT ON (symbol) symbol, direction, amount_usd, detected_at
		FROM whale_events
		WHERE detected_at > now() - interval '4 hours'
		ORDER BY symbol, detected_at DESC
	`)
	type stateRow struct {
		Symbol     string    `json:"symbol"`
		Direction  string    `json:"direction"`
		AmountUSD  float64   `json:"amountUsd"`
		DetectedAt time.Time `json:"detectedAt"`
		Source     string    `json:"source"`
	}
	var result []stateRow
	if rows != nil {
		defer rows.Close()
		for rows.Next() {
			var sr stateRow
			if err := rows.Scan(&sr.Symbol, &sr.Direction, &sr.AmountUSD, &sr.DetectedAt); err != nil {
				continue
			}
			if _, ok := states[sr.Symbol]; ok {
				sr.Source = "redis"
			} else {
				sr.Source = "db"
			}
			result = append(result, sr)
		}
	}
	if result == nil {
		result = []stateRow{}
	}
	writeJSON(w, http.StatusOK, result)
}

// POST /admin/whale/simulate
func (s *Server) SimulateWhale(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ThresholdUSDT float64 `json:"threshold_usdt"`
		WindowHours   int     `json:"window_hours"`
	}
	body.ThresholdUSDT = 50000
	body.WindowHours = 2
	json.NewDecoder(r.Body).Decode(&body)
	if body.WindowHours <= 0 || body.WindowHours > 168 {
		body.WindowHours = 2
	}

	rows, err := s.pool.Query(r.Context(), `
		SELECT symbol, direction, amount_usd
		FROM whale_events
		WHERE detected_at > now() - ($1 || ' hours')::interval
		  AND amount_usd >= $2
		ORDER BY detected_at DESC
	`, body.WindowHours, body.ThresholdUSDT)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "db error")
		return
	}
	defer rows.Close()

	type bucket struct {
		Label string `json:"label"`
		Count int    `json:"count"`
	}
	bySymbol := make(map[string]string)
	buckets := map[string]int{"50k-100k": 0, "100k-500k": 0, "500k+": 0}
	total := 0
	for rows.Next() {
		var sym, dir string
		var amt float64
		if err := rows.Scan(&sym, &dir, &amt); err != nil {
			continue
		}
		total++
		bySymbol[sym] = dir
		switch {
		case amt < 100_000:
			buckets["50k-100k"]++
		case amt < 500_000:
			buckets["100k-500k"]++
		default:
			buckets["500k+"]++
		}
	}

	type symState struct {
		Symbol    string `json:"symbol"`
		Direction string `json:"direction"`
	}
	var symStates []symState
	for sym, dir := range bySymbol {
		symStates = append(symStates, symState{sym, dir})
	}
	if symStates == nil {
		symStates = []symState{}
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"events_count": total,
		"by_symbol":    symStates,
		"buckets": []bucket{
			{"50k-100k", buckets["50k-100k"]},
			{"100k-500k", buckets["100k-500k"]},
			{"500k+", buckets["500k+"]},
		},
	})
}
