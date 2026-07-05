package main

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"sis/pkg/signal"
)

// GetSignalOverride returns the current manual override for a test signal.
// GET /admin/signal-override/{name}
func (s *Server) GetSignalOverride(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	v, ok := signal.GetTestOverride(name)
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"active": ok,
		"value":  v,
	})
}

// SetSignalOverride sets or clears the manual override for a test signal.
// PUT /admin/signal-override/{name}  body: {"value":45.5} or {"value":null}
func (s *Server) SetSignalOverride(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	var body struct {
		Value *float64 `json:"value"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if body.Value == nil {
		signal.ClearTestOverride(name)
	} else {
		signal.SetTestOverride(name, *body.Value)
	}
	s.signalEngine.ForceRecompute(name)
	s.engine.PushSignalOverride(name)
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// GetAdminMetrics returns a live snapshot of Signal Engine + Strategy Engine metrics.
func (s *Server) GetAdminMetrics(w http.ResponseWriter, r *http.Request) {
	sigSnap := s.signalEngine.Metrics()

	// Strategy engine counters
	active, cycles := s.engine.ActiveStats()

	warmerMetrics := s.globalWarmer.Metrics()
	lastAt, ms, bots, groups, opps := botEngineStats()

	var lastAtStr string
	if !lastAt.IsZero() {
		lastAtStr = lastAt.UTC().Format(time.RFC3339)
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"signal": sigSnap,
		"strategy": map[string]interface{}{
			"activeStrategies": active,
			"activeCycles":     cycles,
			"ordersToday":      0, // TODO: query DB
			"fillsToday":       0, // TODO: query DB
			"accounts":         []interface{}{},
		},
		"strategy_workers": s.engine.WorkerStats(),
		"global_warmer":    warmerMetrics,
		"ticker_hub":       s.signalEngine.PriceHub().Metrics(),
		"bot_engine": map[string]interface{}{
			"last_tick_at":    lastAtStr,
			"last_tick_ms":    ms,
			"bots_active":     bots,
			"groups_computed": groups,
			"opportunities":   opps,
		},
	})
}

// GetSystemHealth returns a live server-health snapshot (CPU, RAM, Disk, DB).
// GET /admin/system-health
func (s *Server) GetSystemHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, LatestSystemHealth())
}

// serviceInfo describes a Go service in the system.
type serviceInfo struct {
	Name        string `json:"name"`
	Binary      string `json:"binary"`
	Description string `json:"description"`
	StartCond   string `json:"startCond"`
	Online      bool   `json:"online"`
	LastSeenMs  *int64 `json:"lastSeenMs"`
}

var knownServices = []struct {
	name        string
	binary      string
	description string
	startCond   string
}{
	{
		name:        "api-gateway",
		binary:      "api-gateway",
		description: "HTTP-сервер, движок стратегий (grid/matrix), bot engine, TG-уведомления, WebSocket стримы",
		startCond:   "Всегда — основной сервис",
	},
	{
		name:        "ingester",
		binary:      "ingester",
		description: "Сборщик свечей (OHLCV) с Bybit и Binance → TimescaleDB + Redis-кэш",
		startCond:   "Всегда — фоновый процесс",
	},
	{
		name:        "signal-engine",
		binary:      "signal-engine",
		description: "Обработчик backtest/optimize задач: читает Redis-очередь, пишет результаты в БД",
		startCond:   "Всегда — слушает Redis-очередь",
	},
	{
		name:        "tg-bot",
		binary:      "tg-bot",
		description: "Telegram-бот: уведомления пользователям, команды /status /pause /resume",
		startCond:   "Если задан TELEGRAM_BOT_TOKEN",
	},
	{
		name:        "webhook",
		binary:      "webhook",
		description: "Диспетчер исходящих webhook-ов к внешним сервисам пользователей",
		startCond:   "Всегда — слушает Redis-очередь",
	},
	{
		name:        "migrate",
		binary:      "migrate",
		description: "Применяет SQL-миграции к БД при деплое",
		startCond:   "Один раз при деплое",
	},
}

// GetServices returns descriptions and live heartbeat status for all Go services.
// GET /admin/services
func (s *Server) GetServices(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	out := make([]serviceInfo, 0, len(knownServices))
	for _, svc := range knownServices {
		info := serviceInfo{
			Name:        svc.name,
			Binary:      svc.binary,
			Description: svc.description,
			StartCond:   svc.startCond,
		}
		key := "service:heartbeat:" + svc.name
		res := s.rdb.Get(ctx, key)
		if res.Err() == nil {
			info.Online = true
			now := time.Now().UnixMilli()
			info.LastSeenMs = &now
		}
		out = append(out, info)
	}
	writeJSON(w, http.StatusOK, out)
}

/*
// AdminSignAgreement manually signs the Bybit trading agreement for an exchange account.
// POST /admin/accounts/{id}/sign-agreement  body: {"categoryV2": 0} (optional)
func (s *Server) AdminSignAgreement(w http.ResponseWriter, r *http.Request) {
	accID := chi.URLParam(r, "id")

	var body struct {
		CategoryV2 *int `json:"categoryV2"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil && err.Error() != "EOF" {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}

	var apiKeyEnc, secretEnc string
	err := s.pool.QueryRow(r.Context(),
		`SELECT api_key_enc, secret_enc FROM exchange_accounts WHERE id=$1`, accID,
	).Scan(&apiKeyEnc, &secretEnc)
	if err != nil {
		writeError(w, http.StatusNotFound, "account not found")
		return
	}
	apiKey, err := crypto.Decrypt(apiKeyEnc, s.encKey)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "decrypt api key failed")
		return
	}
	secret, err := crypto.Decrypt(secretEnc, s.encKey)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "decrypt secret failed")
		return
	}
	creds := trader.Credentials{APIKey: apiKey, SecretKey: secret}

	if err := trader.SignAgreement(r.Context(), creds, body.CategoryV2); err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}
*/
