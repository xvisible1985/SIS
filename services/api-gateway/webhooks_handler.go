// services/api-gateway/webhooks_handler.go
package main

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"sis/pkg/signal"
)

// validWebhookTimeframes mirrors pkg/signal/hub.go's tfToBybit key set — the timeframes
// the kline hub actually knows how to subscribe to.
var validWebhookTimeframes = map[string]bool{
	"1m": true, "3m": true, "5m": true, "15m": true, "30m": true,
	"1h": true, "2h": true, "4h": true, "6h": true, "12h": true,
	"1D": true, "1W": true, "1M": true,
}

type webhookRow struct {
	ID                string          `json:"id"`
	CatalogSignalID   *string         `json:"catalog_signal_id"`
	CustomSignalID    *string         `json:"custom_signal_id"`
	CatalogSignalName string          `json:"catalog_signal_name"`
	Symbol            string          `json:"symbol"`
	Timeframe         string          `json:"timeframe"`
	Params            json.RawMessage `json:"params"`
	URL               string          `json:"url"`
	Platform          string          `json:"platform"`
	IsActive          bool            `json:"is_active"`
	CreatedAt         time.Time       `json:"created_at"`
}

const webhookRowSelect = `
	SELECT wh.id, wh.catalog_signal_id, wh.custom_signal_id::text, COALESCE(st.name, cs.name, wh.catalog_signal_id),
	       wh.symbol, wh.timeframe, wh.params, wh.url, wh.platform, wh.is_active, wh.created_at
	FROM webhooks wh
	LEFT JOIN signal_types st ON st.id = wh.catalog_signal_id
	LEFT JOIN custom_signals cs ON cs.id = wh.custom_signal_id`

func scanWebhookRow(row pgx.Row) (webhookRow, error) {
	var r webhookRow
	err := row.Scan(&r.ID, &r.CatalogSignalID, &r.CustomSignalID, &r.CatalogSignalName,
		&r.Symbol, &r.Timeframe, &r.Params, &r.URL, &r.Platform, &r.IsActive, &r.CreatedAt)
	return r, err
}

// ListWebhooks returns all signal alerts owned by the authenticated user.
// GET /webhooks
func (s *Server) ListWebhooks(w http.ResponseWriter, r *http.Request) {
	userID := UserIDFromCtx(r.Context())
	rows, err := s.pool.Query(r.Context(),
		webhookRowSelect+` WHERE wh.owner_id = $1 ORDER BY wh.created_at DESC`,
		userID,
	)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "db error")
		return
	}
	defer rows.Close()
	result := make([]webhookRow, 0)
	for rows.Next() {
		row, err := scanWebhookRow(rows)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "scan error")
			return
		}
		result = append(result, row)
	}
	writeJSON(w, http.StatusOK, result)
}

// CreateWebhook creates a new alert watching EITHER a catalog signal (pkg/signal registry)
// OR a saved custom combo (see custom_signals_handler.go) + symbol + timeframe. SIS
// generates the relay URL — the alert's OWN webhook.url, an internal endpoint that the
// standalone dispatcher (services/webhook) POSTs to once this signal fires; the relay then
// forwards to the owner's Telegram. See webhooks_engine.go.
// POST /webhooks
func (s *Server) CreateWebhook(w http.ResponseWriter, r *http.Request) {
	userID := UserIDFromCtx(r.Context())
	var req struct {
		CatalogSignalID string          `json:"catalog_signal_id"`
		CustomSignalID  string          `json:"custom_signal_id"`
		Symbol          string          `json:"symbol"`
		Timeframe       string          `json:"timeframe"`
		Params          json.RawMessage `json:"params"`
		Platform        string          `json:"platform"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	req.CatalogSignalID = strings.TrimSpace(req.CatalogSignalID)
	req.CustomSignalID = strings.TrimSpace(req.CustomSignalID)
	if (req.CatalogSignalID == "") == (req.CustomSignalID == "") {
		writeError(w, http.StatusBadRequest, "exactly one of catalog_signal_id or custom_signal_id is required")
		return
	}
	if req.Symbol == "" {
		writeError(w, http.StatusBadRequest, "symbol is required")
		return
	}
	if req.Timeframe == "" {
		req.Timeframe = "15m"
	}
	if !validWebhookTimeframes[req.Timeframe] {
		writeError(w, http.StatusBadRequest, "invalid timeframe")
		return
	}
	if req.Platform == "" {
		req.Platform = "custom"
	}
	if len(req.Params) == 0 {
		req.Params = json.RawMessage("{}")
	}
	var paramsMap map[string]any
	if err := json.Unmarshal(req.Params, &paramsMap); err != nil {
		writeError(w, http.StatusBadRequest, "params must be a JSON object")
		return
	}

	var catalogSignalIDArg, customSignalIDArg any
	if req.CatalogSignalID != "" {
		// Must actually be computable server-side (pkg/signal registry) — catches ids that
		// exist in signal_types (the admin catalog) but aren't a real candle-based signal,
		// e.g. "bybit-news" (a fundamental trigger with no Go registry entry).
		if _, err := signal.Build(signal.Config{Name: req.CatalogSignalID, Params: paramsMap}); err != nil {
			writeError(w, http.StatusBadRequest, "signal \""+req.CatalogSignalID+"\" cannot be used for an alert (not a computable catalog signal)")
			return
		}
		catalogSignalIDArg = req.CatalogSignalID
	} else {
		var owns bool
		if err := s.pool.QueryRow(r.Context(),
			`SELECT EXISTS(SELECT 1 FROM custom_signals WHERE id=$1 AND owner_id=$2)`,
			req.CustomSignalID, userID,
		).Scan(&owns); err != nil || !owns {
			writeError(w, http.StatusBadRequest, "custom signal not found")
			return
		}
		customSignalIDArg = req.CustomSignalID
		paramsMap = nil // a combo's legs each carry their own saved params, not a shared map
	}

	var id, token string
	err := s.pool.QueryRow(r.Context(),
		`INSERT INTO webhooks (owner_id, catalog_signal_id, custom_signal_id, symbol, timeframe, params, platform, url)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, '')
		 RETURNING id, token`,
		userID, catalogSignalIDArg, customSignalIDArg, req.Symbol, req.Timeframe, req.Params, req.Platform,
	).Scan(&id, &token)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "db error")
		return
	}
	relayURL := webhookRelayURL(token)
	if _, err := s.pool.Exec(r.Context(), `UPDATE webhooks SET url=$1 WHERE id=$2`, relayURL, id); err != nil {
		writeError(w, http.StatusInternalServerError, "db error")
		return
	}

	s.subscribeWebhookAlert(r.Context(), webhookAlert{
		ID: id, OwnerID: userID, CatalogSignalID: req.CatalogSignalID, CustomSignalID: req.CustomSignalID,
		Symbol: req.Symbol, Timeframe: req.Timeframe, Params: paramsMap,
	})

	row, err := scanWebhookRow(s.pool.QueryRow(r.Context(), webhookRowSelect+` WHERE wh.id=$1`, id))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "db error")
		return
	}
	writeJSON(w, http.StatusCreated, row)
}

// GetWebhook returns a single alert by ID (must be owned by caller).
// GET /webhooks/:id
func (s *Server) GetWebhook(w http.ResponseWriter, r *http.Request) {
	userID := UserIDFromCtx(r.Context())
	whID := chi.URLParam(r, "id")
	row, err := scanWebhookRow(s.pool.QueryRow(r.Context(),
		webhookRowSelect+` WHERE wh.id=$1 AND wh.owner_id=$2`, whID, userID))
	if err != nil {
		writeError(w, http.StatusNotFound, "webhook not found")
		return
	}
	writeJSON(w, http.StatusOK, row)
}

// UpdateWebhook toggles is_active and/or platform. The signal/symbol/timeframe/params a
// webhook alert watches are immutable after creation — changing what to watch means
// deleting and recreating the alert, so subscribe/unsubscribe bookkeeping (see
// webhooks_engine.go) never has to reconcile a live config change mid-flight.
// PUT /webhooks/:id
func (s *Server) UpdateWebhook(w http.ResponseWriter, r *http.Request) {
	userID := UserIDFromCtx(r.Context())
	whID := chi.URLParam(r, "id")
	var req struct {
		Platform string `json:"platform"`
		IsActive *bool  `json:"is_active"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}

	var alert webhookAlert
	var paramsRaw json.RawMessage
	var wasActive bool
	if err := s.pool.QueryRow(r.Context(),
		`SELECT id, owner_id, COALESCE(catalog_signal_id, ''), COALESCE(custom_signal_id::text, ''), symbol, timeframe, params, is_active
		 FROM webhooks WHERE id=$1 AND owner_id=$2`,
		whID, userID,
	).Scan(&alert.ID, &alert.OwnerID, &alert.CatalogSignalID, &alert.CustomSignalID, &alert.Symbol, &alert.Timeframe, &paramsRaw, &wasActive); err != nil {
		writeError(w, http.StatusNotFound, "webhook not found")
		return
	}
	_ = json.Unmarshal(paramsRaw, &alert.Params)

	tag, err := s.pool.Exec(r.Context(),
		`UPDATE webhooks SET
			platform  = COALESCE(NULLIF($3,''), platform),
			is_active = COALESCE($4, is_active)
		 WHERE id=$1 AND owner_id=$2`,
		whID, userID, req.Platform, req.IsActive,
	)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "db error")
		return
	}
	if tag.RowsAffected() == 0 {
		writeError(w, http.StatusNotFound, "webhook not found")
		return
	}

	if req.IsActive != nil && *req.IsActive != wasActive {
		if *req.IsActive {
			s.subscribeWebhookAlert(r.Context(), alert)
		} else {
			s.signalEngine.Unsubscribe(alert.ID)
		}
	}

	s.GetWebhook(w, r)
}

// DeleteWebhook deletes an alert owned by the caller and stops watching its signal.
// DELETE /webhooks/:id
func (s *Server) DeleteWebhook(w http.ResponseWriter, r *http.Request) {
	userID := UserIDFromCtx(r.Context())
	whID := chi.URLParam(r, "id")
	tag, err := s.pool.Exec(r.Context(),
		`DELETE FROM webhooks WHERE id=$1 AND owner_id=$2`,
		whID, userID,
	)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "db error")
		return
	}
	if tag.RowsAffected() > 0 {
		s.signalEngine.Unsubscribe(whID)
	}
	w.WriteHeader(http.StatusNoContent)
}

type webhookLogRow struct {
	ID         string    `json:"id"`
	SentAt     time.Time `json:"sent_at"`
	StatusCode int       `json:"status_code"`
	ResponseMs int       `json:"response_ms"`
	Success    bool      `json:"success"`
	Error      string    `json:"error"`
}

// ListWebhookLogs returns the delivery history for one alert (owner-scoped).
// GET /webhooks/:id/logs
func (s *Server) ListWebhookLogs(w http.ResponseWriter, r *http.Request) {
	userID := UserIDFromCtx(r.Context())
	whID := chi.URLParam(r, "id")
	var owns bool
	if err := s.pool.QueryRow(r.Context(),
		`SELECT EXISTS(SELECT 1 FROM webhooks WHERE id=$1 AND owner_id=$2)`, whID, userID,
	).Scan(&owns); err != nil || !owns {
		writeError(w, http.StatusNotFound, "webhook not found")
		return
	}
	rows, err := s.pool.Query(r.Context(),
		`SELECT id, sent_at, status_code, response_ms, success, error
		 FROM webhook_logs WHERE webhook_id=$1 ORDER BY sent_at DESC LIMIT 50`,
		whID,
	)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "db error")
		return
	}
	defer rows.Close()
	result := make([]webhookLogRow, 0)
	for rows.Next() {
		var lg webhookLogRow
		if err := rows.Scan(&lg.ID, &lg.SentAt, &lg.StatusCode, &lg.ResponseMs, &lg.Success, &lg.Error); err == nil {
			result = append(result, lg)
		}
	}
	writeJSON(w, http.StatusOK, result)
}
