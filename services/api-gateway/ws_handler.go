// services/api-gateway/ws_handler.go
package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/gorilla/websocket"
	"sis/pkg/auth"
)

// StrategiesUpdatesStream pushes strategy status changes for all strategies
// owned by the authenticated user.
// GET /ws/strategies/updates?token=<jwt>
// Sends a JSON array of {id, status} objects whenever any status changed (2s poll).
func (s *Server) StrategiesUpdatesStream(w http.ResponseWriter, r *http.Request) {
	tokenStr := r.URL.Query().Get("token")
	userID, err := auth.ValidateToken(tokenStr, string(s.jwtSecret))
	if err != nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("ws strategies/updates: upgrade: %v", err)
		return
	}
	defer conn.Close()

	type statusUpdate struct {
		ID           string  `json:"id"`
		Status       string  `json:"status"`
		ActiveLevels int     `json:"active_levels"`
		VolumeUSDT   float64 `json:"volume_usdt"`
	}

	type lastState struct {
		status       string
		activeLevels int
		volumeUSDT   float64
	}
	lastSeen := map[string]lastState{}
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
			rows, err := s.pool.Query(r.Context(), `
				SELECT s.id, s.status,
					COALESCE((
						SELECT COUNT(*)::int FROM strategy_levels sl
						JOIN strategy_cycles sc ON sl.cycle_id = sc.id
						WHERE sc.strategy_id = s.id AND sc.ended_at IS NULL AND sl.status = 'filled'
					), 0),
					COALESCE((
						SELECT SUM(sl.size_usdt) FROM strategy_levels sl
						JOIN strategy_cycles sc ON sl.cycle_id = sc.id
						WHERE sc.strategy_id = s.id AND sc.ended_at IS NULL AND sl.status = 'filled'
					), 0)
				FROM strategies s WHERE s.owner_id=$1`, userID)
			if err != nil {
				continue
			}
			var updates []statusUpdate
			for rows.Next() {
				var id, status string
				var activeLevels int
				var volumeUSDT float64
				if rows.Scan(&id, &status, &activeLevels, &volumeUSDT) == nil {
					prev := lastSeen[id]
					if prev.status != status || prev.activeLevels != activeLevels || prev.volumeUSDT != volumeUSDT {
						updates = append(updates, statusUpdate{id, status, activeLevels, volumeUSDT})
						lastSeen[id] = lastState{status, activeLevels, volumeUSDT}
					}
				}
			}
			rows.Close()
			if len(updates) == 0 {
				continue
			}
			data, _ := json.Marshal(updates)
			if err := conn.WriteMessage(websocket.TextMessage, data); err != nil {
				return
			}
		}
	}
}

// StrategyEventsStream streams new strategy events over WebSocket.
// GET /ws/strategies/{id}/events?token=<jwt>&since=<rfc3339nano>
// Polls DB every 2s and pushes only new events as a JSON array (ASC order).
func (s *Server) StrategyEventsStream(w http.ResponseWriter, r *http.Request) {
	tokenStr := r.URL.Query().Get("token")
	userID, err := auth.ValidateToken(tokenStr, string(s.jwtSecret))
	if err != nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	stratID := chi.URLParam(r, "id")
	var exists bool
	if err := s.pool.QueryRow(r.Context(),
		`SELECT true FROM strategies WHERE id=$1 AND owner_id=$2`, stratID, userID,
	).Scan(&exists); err != nil {
		http.Error(w, "strategy not found", http.StatusNotFound)
		return
	}

	since := time.Now()
	if sinceStr := r.URL.Query().Get("since"); sinceStr != "" {
		if t, err := time.Parse(time.RFC3339Nano, sinceStr); err == nil {
			since = t
		}
	}

	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("ws strategy events: upgrade: %v", err)
		return
	}
	defer conn.Close()

	type eventRow struct {
		Message   string    `json:"message"`
		Level     string    `json:"level"`
		CreatedAt time.Time `json:"created_at"`
	}

	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
			rows, err := s.pool.Query(r.Context(),
				`SELECT message, level, created_at
				 FROM strategy_events
				 WHERE strategy_id = $1 AND created_at > $2
				 ORDER BY created_at ASC LIMIT 100`,
				stratID, since)
			if err != nil {
				continue
			}
			var events []eventRow
			for rows.Next() {
				var e eventRow
				if rows.Scan(&e.Message, &e.Level, &e.CreatedAt) == nil {
					events = append(events, e)
				}
			}
			rows.Close()
			if len(events) == 0 {
				continue
			}
			since = events[len(events)-1].CreatedAt
			data, _ := json.Marshal(events)
			if err := conn.WriteMessage(websocket.TextMessage, data); err != nil {
				return
			}
		}
	}
}

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}

type progressMessage struct {
	Pct       int    `json:"pct"`
	Status    string `json:"status"`
	UpdatedAt int64  `json:"updated_at"`
}

// JobProgress streams job progress over WebSocket.
// GET /ws/jobs/:id/progress?type=backtest|optimize&token=<jwt>
func (s *Server) JobProgress(w http.ResponseWriter, r *http.Request) {
	// Authenticate via query param (browsers can't set headers on WS)
	tokenStr := r.URL.Query().Get("token")
	if tokenStr == "" {
		http.Error(w, "missing token", http.StatusUnauthorized)
		return
	}
	if _, err := auth.ValidateToken(tokenStr, string(s.jwtSecret)); err != nil {
		http.Error(w, "invalid token", http.StatusUnauthorized)
		return
	}

	jobID := chi.URLParam(r, "id")
	jobType := r.URL.Query().Get("type")
	var progressKey string
	switch jobType {
	case "optimize":
		progressKey = fmt.Sprintf("jobs:%s:optimize:progress", jobID)
	default:
		progressKey = fmt.Sprintf("jobs:%s:progress", jobID)
	}

	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("ws: upgrade error: %v", err)
		return
	}
	defer conn.Close()

	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
			vals, err := s.rdb.HGetAll(r.Context(), progressKey).Result()
			if err != nil || len(vals) == 0 {
				continue
			}

			var msg progressMessage
			if pct, ok := vals["pct"]; ok {
				fmt.Sscanf(pct, "%d", &msg.Pct)
			}
			msg.Status = vals["status"]
			if ts, ok := vals["updated_at"]; ok {
				fmt.Sscanf(ts, "%d", &msg.UpdatedAt)
			}

			data, _ := json.Marshal(msg)
			if err := conn.WriteMessage(websocket.TextMessage, data); err != nil {
				return
			}

			if msg.Status == "done" || msg.Status == "error" {
				return
			}
		}
	}
}
