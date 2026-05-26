// services/api-gateway/bots_ws_handler.go
package main

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"path/filepath"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/gorilla/websocket"
	"github.com/jackc/pgx/v5"
	"sis/pkg/auth"
	"sis/pkg/signal"
	"sis/pkg/trader"
)

type botSignalUpdate struct {
	ID          string `json:"id"`
	SignalCount int    `json:"signalCount"`
	TotalCount  int    `json:"totalCount"`
}

// BotSignalUpdatesStream pushes signal match counts for all active bots of the user.
// GET /ws/bots/updates?token=<jwt>
// Emits a full JSON array of {id, signalCount, totalCount} on connect, then every 60s.
func (s *Server) BotSignalUpdatesStream(w http.ResponseWriter, r *http.Request) {
	tokenStr := r.URL.Query().Get("token")
	userID, err := auth.ValidateToken(tokenStr, string(s.jwtSecret))
	if err != nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("ws bots/updates: upgrade: %v", err)
		return
	}
	defer conn.Close()

	emit := func() bool {
		ctx, cancel := context.WithTimeout(r.Context(), 45*time.Second)
		defer cancel()
		updates := s.computeBotSignalCounts(ctx, userID)
		data, _ := json.Marshal(updates)
		return conn.WriteMessage(websocket.TextMessage, data) == nil
	}

	if !emit() {
		return
	}

	ticker := time.NewTicker(60 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
			if !emit() {
				return
			}
		}
	}
}

func (s *Server) computeBotSignalCounts(ctx context.Context, userID string) []botSignalUpdate {
	rows, err := s.pool.Query(ctx,
		`SELECT id, symbol_whitelist, symbol_blacklist, strategy_config
         FROM bots WHERE owner_id = $1 AND status = 'active'`, userID)
	if err != nil {
		return []botSignalUpdate{}
	}
	defer rows.Close()

	allSymbols, _ := trader.FetchAllLinearSymbols(ctx)

	var results []botSignalUpdate
	for rows.Next() {
		var id string
		var whitelist, blacklist []string
		var stratCfg []byte
		if err := rows.Scan(&id, &whitelist, &blacklist, &stratCfg); err != nil {
			continue
		}
		if upd, ok := s.computeOneBotSignalCount(ctx, id, whitelist, blacklist, stratCfg, allSymbols); ok {
			results = append(results, upd)
		}
	}

	if results == nil {
		return []botSignalUpdate{}
	}
	return results
}

func (s *Server) computeOneBotSignalCount(
	ctx context.Context,
	id string,
	whitelist, blacklist []string,
	strategyConfig []byte,
	allSymbols []string,
) (botSignalUpdate, bool) {
	var cfg struct {
		ActivationSignals []struct {
			Name   string                 `json:"name"`
			Params map[string]interface{} `json:"params"`
		} `json:"activation_signals"`
		Direction string `json:"direction"`
	}
	if err := json.Unmarshal(strategyConfig, &cfg); err != nil || len(cfg.ActivationSignals) == 0 {
		return botSignalUpdate{}, false
	}

	sigCfgs := make([]signal.Config, len(cfg.ActivationSignals))
	interval := "15"
	for i, a := range cfg.ActivationSignals {
		sigCfgs[i] = signal.Config{Name: a.Name, Params: a.Params}
		if v, ok := a.Params["tf"]; ok {
			if sv, ok2 := v.(string); ok2 && sv != "" {
				interval = sv
			}
		}
	}
	direction := cfg.Direction

	globalBlacklist := s.GetDelistingSymbols()
	symbols := resolveSymbolList(whitelist, blacklist, globalBlacklist, allSymbols)
	total := len(symbols)
	if total == 0 {
		return botSignalUpdate{ID: id, SignalCount: 0, TotalCount: 0}, true
	}

	sem := make(chan struct{}, 20)
	var cntMu sync.Mutex
	var wg sync.WaitGroup
	count := 0

	for _, sym := range symbols {
		sym := sym
		wg.Add(1)
		go func() {
			defer wg.Done()
			select {
			case <-ctx.Done():
				return
			case sem <- struct{}{}:
			}
			defer func() { <-sem }()
			st := s.signalEngine.ComputeStateForce(sym, interval, sigCfgs)
			match := false
			switch direction {
			case "long":
				match = st == signal.Buy
			case "short":
				match = st == signal.Sell
			default:
				match = st != signal.Neutral
			}
			if match {
				cntMu.Lock()
				count++
				cntMu.Unlock()
			}
		}()
	}
	wg.Wait()

	return botSignalUpdate{ID: id, SignalCount: count, TotalCount: total}, true
}

// resolveSymbolList expands whitelist/blacklist masks against the full symbol universe
// and returns the final flat list of symbols.
func resolveSymbolList(whitelist, blacklist, globalBlacklist []string, allSymbols []string) []string {
	if len(allSymbols) == 0 {
		// No universe available — use whitelist entries as-is (no glob expansion)
		if len(whitelist) == 0 {
			return nil
		}
		blackSet := make(map[string]bool, len(blacklist)+len(globalBlacklist))
		for _, b := range blacklist {
			blackSet[b] = true
		}
		for _, b := range globalBlacklist {
			blackSet[b] = true
		}
		var result []string
		for _, w := range whitelist {
			if !blackSet[w] {
				result = append(result, w)
			}
		}
		return result
	}

	blackSet := make(map[string]bool)
	for _, pat := range blacklist {
		for _, sym := range allSymbols {
			if globMatch(pat, sym) {
				blackSet[sym] = true
			}
		}
	}
	for _, sym := range globalBlacklist {
		blackSet[sym] = true
	}

	if len(whitelist) == 0 {
		var result []string
		for _, sym := range allSymbols {
			if !blackSet[sym] {
				result = append(result, sym)
			}
		}
		return result
	}

	seen := make(map[string]bool)
	var result []string
	for _, pat := range whitelist {
		for _, sym := range allSymbols {
			if !seen[sym] && !blackSet[sym] && globMatch(pat, sym) {
				seen[sym] = true
				result = append(result, sym)
			}
		}
	}
	return result
}

func globMatch(pattern, s string) bool {
	ok, err := filepath.Match(pattern, s)
	return err == nil && ok
}

// BotEventsStream streams new bot events over WebSocket.
// GET /ws/bots/{id}/events?token=<jwt>&since=<rfc3339nano>
// Polls DB every 2s and pushes only new events as a JSON array (DESC order).
func (s *Server) BotEventsStream(w http.ResponseWriter, r *http.Request) {
	tokenStr := r.URL.Query().Get("token")
	userID, err := auth.ValidateToken(tokenStr, string(s.jwtSecret))
	if err != nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	botID := chi.URLParam(r, "id")
	var exists bool
	if err := s.pool.QueryRow(r.Context(),
		`SELECT true FROM bots WHERE id=$1 AND owner_id=$2`, botID, userID,
	).Scan(&exists); err != nil {
		http.Error(w, "bot not found", http.StatusNotFound)
		return
	}

	// When reconnecting with ?since=..., start from that point.
	// On first connect (no since), load last 100 events as history.
	sinceStr := r.URL.Query().Get("since")
	firstConnect := sinceStr == ""
	var since time.Time
	if !firstConnect {
		if t, err := time.Parse(time.RFC3339Nano, sinceStr); err == nil {
			since = t
		} else {
			firstConnect = true
		}
	}

	category := r.URL.Query().Get("category")

	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("ws bot events: upgrade: %v", err)
		return
	}
	defer conn.Close()

	type eventRow struct {
		Message   string    `json:"message"`
		Level     string    `json:"level"`
		Category  string    `json:"category"`
		CreatedAt time.Time `json:"created_at"`
	}

	sendEvents := func(rows interface {
		Next() bool
		Scan(...any) error
		Close()
		Err() error
	}) ([]eventRow, bool) {
		var events []eventRow
		for rows.Next() {
			var e eventRow
			if rows.Scan(&e.Message, &e.Level, &e.Category, &e.CreatedAt) == nil {
				events = append(events, e)
			}
		}
		rows.Close()
		if len(events) == 0 {
			return nil, true
		}
		data, _ := json.Marshal(events)
		return events, conn.WriteMessage(websocket.TextMessage, data) == nil
	}

	queryHistory := func() (pgx.Rows, error) {
		if category != "" {
			return s.pool.Query(r.Context(),
				`SELECT message, level, category, created_at
				 FROM bot_events
				 WHERE bot_id = $1 AND category = $2
				 ORDER BY created_at DESC LIMIT 100`,
				botID, category)
		}
		return s.pool.Query(r.Context(),
			`SELECT message, level, category, created_at
			 FROM bot_events
			 WHERE bot_id = $1
			 ORDER BY created_at DESC LIMIT 100`,
			botID)
	}

	queryNew := func(since time.Time) (pgx.Rows, error) {
		if category != "" {
			return s.pool.Query(r.Context(),
				`SELECT message, level, category, created_at
				 FROM bot_events
				 WHERE bot_id = $1 AND created_at > $2 AND category = $3
				 ORDER BY created_at ASC LIMIT 100`,
				botID, since, category)
		}
		return s.pool.Query(r.Context(),
			`SELECT message, level, category, created_at
			 FROM bot_events
			 WHERE bot_id = $1 AND created_at > $2
			 ORDER BY created_at ASC LIMIT 100`,
			botID, since)
	}

	// On first connect: send last 100 events as history (newest first for the client to prepend)
	if firstConnect {
		rows, err := queryHistory()
		if err == nil {
			if events, ok := sendEvents(rows); ok && len(events) > 0 {
				since = events[0].CreatedAt // events[0] is newest (DESC)
			} else if !ok {
				return
			}
		}
		if since.IsZero() {
			since = time.Now()
		}
	}

	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
			rows, err := queryNew(since)
			if err != nil {
				continue
			}
			events, ok := sendEvents(rows)
			if !ok {
				return
			}
			if len(events) > 0 {
				since = events[len(events)-1].CreatedAt
			}
		}
	}
}
