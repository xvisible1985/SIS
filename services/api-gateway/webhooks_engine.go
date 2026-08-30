// services/api-gateway/webhooks_engine.go
//
// Wires the Webhooks tab's alerts into pkg/signal's continuous computation engine and
// completes the outbound delivery pipe (see services/webhook's dispatcher, which already
// consumed the Redis stream "signals:fired" and POSTed to a registered URL, but had no
// producer — nothing ever published to that stream). An alert = one webhooks row = one
// (catalog signal, symbol, timeframe, params) subscription. When that computed state
// flips to buy/sell, this publishes a FiredSignal to the stream; the dispatcher POSTs it
// to the alert's own relay URL (webhookRelayURL); WebhookRelay below receives that POST
// and forwards to the owner's Telegram.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/redis/go-redis/v9"
	"sis/pkg/signal"
)

const signalsFiredStream = "signals:fired"

// webhookAlert is the subset of a webhooks row needed to (un)subscribe it from the signal
// engine and to build its FiredSignal payload when it fires. Exactly one of
// CatalogSignalID/CustomSignalID is set (webhooks_signal_source_xor) — CatalogName is the
// display name either way (the signal_types row's name, or the custom_signals combo's own
// name), and Params only applies to the CatalogSignalID case (a saved combo's legs each
// carry their own params — see resolveSignalConfigs).
type webhookAlert struct {
	ID              string
	OwnerID         string
	CatalogSignalID string
	CustomSignalID  string
	CatalogName     string
	Symbol          string
	Timeframe       string
	Params          map[string]any
}

// webhookRelayURL builds the internal relay endpoint the dispatcher POSTs a fired alert
// to. Never reachable from outside this deployment's own network — nothing external ever
// calls it, only services/webhook's dispatcher — so it's addressed via an internal base
// URL, not the public app URL.
func webhookRelayURL(token string) string {
	base := getEnv("INTERNAL_API_BASE_URL", "http://localhost:8081")
	return base + "/webhooks/relay/" + token
}

// loadWebhookAlerts subscribes every active alert to the signal engine at startup. Called
// once from main.go alongside the other startup loops.
func (s *Server) loadWebhookAlerts(ctx context.Context) {
	rows, err := s.pool.Query(ctx,
		`SELECT wh.id, wh.owner_id, COALESCE(wh.catalog_signal_id, ''), COALESCE(wh.custom_signal_id::text, ''),
		        COALESCE(st.name, cs.name, wh.catalog_signal_id), wh.symbol, wh.timeframe, wh.params
		 FROM webhooks wh
		 LEFT JOIN signal_types st ON st.id = wh.catalog_signal_id
		 LEFT JOIN custom_signals cs ON cs.id = wh.custom_signal_id
		 WHERE wh.is_active = TRUE`,
	)
	if err != nil {
		log.Printf("webhooks: load active alerts: %v", err)
		return
	}
	defer rows.Close()
	count := 0
	for rows.Next() {
		var a webhookAlert
		var paramsRaw json.RawMessage
		if err := rows.Scan(&a.ID, &a.OwnerID, &a.CatalogSignalID, &a.CustomSignalID, &a.CatalogName, &a.Symbol, &a.Timeframe, &paramsRaw); err != nil {
			continue
		}
		_ = json.Unmarshal(paramsRaw, &a.Params)
		s.subscribeWebhookAlert(ctx, a)
		count++
	}
	log.Printf("webhooks: subscribed %d active alert(s) to the signal engine", count)
}

// subscribeWebhookAlert registers the alert with the signal engine — cb fires whenever the
// computed state for (symbol, timeframe, [signal config(s)]) changes (Subscribe only calls
// back on an actual change, not every tick). Works identically for a single catalog signal
// or a multi-leg custom combo — resolveSignalConfigs returns one config either way, and
// Engine.Subscribe already AND-combines however many configs it's given.
func (s *Server) subscribeWebhookAlert(ctx context.Context, a webhookAlert) {
	configs, err := s.resolveSignalConfigs(ctx, a.CatalogSignalID, a.CustomSignalID, a.Params)
	if err != nil {
		log.Printf("webhooks: resolve signal for alert %s: %v", a.ID, err)
		return
	}
	if err := s.signalEngine.Subscribe(a.ID, a.Symbol, a.Timeframe, configs, func(st signal.State) {
		s.onWebhookAlertFired(a, st)
	}); err != nil {
		log.Printf("webhooks: subscribe alert %s (%s %s): %v", a.ID, a.CatalogSignalID, a.Symbol, err)
	}
}

// onWebhookAlertFired publishes a FiredSignal for the dispatcher to deliver. Neutral is
// not a "fire" — an alert exists to notify entering buy/sell, not leaving it.
func (s *Server) onWebhookAlertFired(a webhookAlert, st signal.State) {
	if st == signal.Neutral {
		return
	}
	name := a.CatalogName
	if name == "" {
		name = a.CatalogSignalID
	}
	payload := map[string]string{
		"signal_id":   a.ID,
		"signal_name": name,
		"symbol":      a.Symbol,
		"direction":   string(st),
		"timestamp":   time.Now().UTC().Format(time.RFC3339),
	}
	body, _ := json.Marshal(payload)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := s.rdb.XAdd(ctx, &redis.XAddArgs{
		Stream: signalsFiredStream,
		Values: map[string]any{"payload": string(body)},
	}).Err(); err != nil {
		log.Printf("webhooks: publish fired alert %s: %v", a.ID, err)
	}
}

// WebhookRelay is the alert's own generated URL (webhooks.url) — services/webhook's
// dispatcher POSTs the FiredSignal JSON here once an alert fires. Forwards to the alert
// owner's Telegram if linked. Deliberately public (no auth): the token in the path is the
// credential, matching the rest of this deployment's internal-only relay pattern — nothing
// outside this network can reach it, and nothing sensitive is exposed by a wrong guess
// (a miss just 404s).
// POST /webhooks/relay/{token}
func (s *Server) WebhookRelay(w http.ResponseWriter, r *http.Request) {
	token := chi.URLParam(r, "token")

	var alert webhookAlert
	err := s.pool.QueryRow(r.Context(),
		`SELECT wh.id, wh.owner_id, COALESCE(wh.catalog_signal_id, ''), COALESCE(st.name, cs.name, wh.catalog_signal_id), wh.symbol
		 FROM webhooks wh
		 LEFT JOIN signal_types st ON st.id = wh.catalog_signal_id
		 LEFT JOIN custom_signals cs ON cs.id = wh.custom_signal_id
		 WHERE wh.token=$1 AND wh.is_active=TRUE`,
		token,
	).Scan(&alert.ID, &alert.OwnerID, &alert.CatalogSignalID, &alert.CatalogName, &alert.Symbol)
	if err != nil {
		if !errors.Is(err, pgx.ErrNoRows) {
			log.Printf("webhooks: relay lookup token: %v", err)
		}
		writeError(w, http.StatusNotFound, "unknown or inactive alert")
		return
	}

	var body struct {
		Direction string `json:"direction"`
		Timestamp string `json:"timestamp"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body) // best-effort — still relay even if unparsable

	var chatID int64
	err = s.pool.QueryRow(r.Context(), `
		SELECT tc.chat_id FROM telegram_connections tc
		WHERE tc.user_id=$1 AND (tc.mute_until IS NULL OR tc.mute_until < NOW())`,
		alert.OwnerID,
	).Scan(&chatID)
	if err == nil {
		dir := "🔔"
		if body.Direction == "sell" {
			dir = "🔻"
		} else if body.Direction == "buy" {
			dir = "🔺"
		}
		text := fmt.Sprintf("%s *%s* — %s: сработал сигнал %s", dir, alert.Symbol, alert.CatalogName, body.Direction)
		s.publishTgNotify(r.Context(), TgNotifyMsg{ChatID: chatID, Text: text})
	} else if !errors.Is(err, pgx.ErrNoRows) {
		log.Printf("webhooks: relay telegram lookup owner=%s: %v", alert.OwnerID, err)
	}

	w.WriteHeader(http.StatusOK)
}
