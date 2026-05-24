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
	"sis/pkg/signal"
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
		ID           string             `json:"id"`
		Status       string             `json:"status"`
		ActiveLevels int                `json:"active_levels"`
		VolumeUSDT   float64            `json:"volume_usdt"`
		SignalState  string             `json:"signal_state"`
		SignalValues map[string]float64 `json:"signal_values,omitempty"`
		ManualAlert  *string            `json:"manual_alert,omitempty"`
		CycleNum     int                `json:"cycle_num"`
	}

	type lastState struct {
		status       string
		activeLevels int
		volumeUSDT   float64
		signalState  string
		manualAlert  *string
		cycleNum     int
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
				SELECT s.id, s.status, s.symbol,
					COALESCE(s.signal_configs::text, '[]'),
					COALESCE((
						SELECT COUNT(*)::int FROM strategy_levels sl
						JOIN strategy_cycles sc ON sl.cycle_id = sc.id
						WHERE sc.strategy_id = s.id AND sc.ended_at IS NULL AND sl.status = 'filled'
					), 0),
					COALESCE((
						SELECT SUM(sl.size_usdt) FROM strategy_levels sl
						JOIN strategy_cycles sc ON sl.cycle_id = sc.id
						WHERE sc.strategy_id = s.id AND sc.ended_at IS NULL AND sl.status = 'filled'
					), 0),
					s.manual_alert,
					COALESCE((
						SELECT cycle_num FROM strategy_cycles
						WHERE strategy_id = s.id AND ended_at IS NULL
						ORDER BY cycle_num DESC LIMIT 1
					), 0)
				FROM strategies s WHERE s.owner_id=$1`, userID)
			if err != nil {
				continue
			}
			var updates []statusUpdate
			for rows.Next() {
				var id, status, symbol, sigConfigsJSON string
				var activeLevels int
				var volumeUSDT float64
				var manualAlert *string
				var cycleNum int
				if rows.Scan(&id, &status, &symbol, &sigConfigsJSON, &activeLevels, &volumeUSDT, &manualAlert, &cycleNum) != nil {
					continue
				}
				// Compute signal state directly — works for active AND stopped strategies
				sigState := computeSignalState(s.signalEngine, symbol, sigConfigsJSON)
				prev := lastSeen[id]
				if prev.status != status || prev.activeLevels != activeLevels || prev.volumeUSDT != volumeUSDT || prev.signalState != sigState || !ptrStrEqual(prev.manualAlert, manualAlert) || prev.cycleNum != cycleNum {
					upd := statusUpdate{
						ID:           id,
						Status:       status,
						ActiveLevels: activeLevels,
						VolumeUSDT:   volumeUSDT,
						SignalState:  sigState,
						ManualAlert:  manualAlert,
						CycleNum:     cycleNum,
					}
					if sigState != prev.signalState {
						upd.SignalValues = s.engine.GetSignalValues(id)
						if upd.SignalValues == nil {
							upd.SignalValues = computeSignalValues(s.signalEngine, symbol, sigConfigsJSON)
						}
					}
					updates = append(updates, upd)
					lastSeen[id] = lastState{status, activeLevels, volumeUSDT, sigState, manualAlert, cycleNum}
				}
			}
			rows.Close()

			// Detect strategies deleted from DB: present in lastSeen but no longer in DB.
			if len(lastSeen) > 0 {
				idRows, err2 := s.pool.Query(r.Context(), `SELECT id FROM strategies WHERE owner_id=$1`, userID)
				if err2 == nil {
					currentIDs := make(map[string]struct{}, len(lastSeen))
					for idRows.Next() {
						var sid string
						if idRows.Scan(&sid) == nil {
							currentIDs[sid] = struct{}{}
						}
					}
					idRows.Close()
					for id := range lastSeen {
						if _, ok := currentIDs[id]; !ok {
							updates = append(updates, statusUpdate{ID: id, Status: "deleted"})
							delete(lastSeen, id)
						}
					}
				}
			}

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

func ptrStrEqual(a, b *string) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	return *a == *b
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

// computeSignalState computes the combined signal state for a strategy directly
// using the signal engine. Works for both active and stopped strategies.
func computeSignalState(se *signal.Engine, symbol, sigConfigsJSON string) string {
	cfgs, tf := parseSignalConfigs(sigConfigsJSON)
	if len(cfgs) == 0 {
		return ""
	}
	return string(se.ComputeStateForce(symbol, tf, cfgs))
}

// computeSignalValues queries per-signal numeric values for display.
func computeSignalValues(se *signal.Engine, symbol, sigConfigsJSON string) map[string]float64 {
	cfgs, tf := parseSignalConfigs(sigConfigsJSON)
	if len(cfgs) == 0 {
		return nil
	}
	return se.QueryValues(symbol, tf, cfgs)
}

// parseSignalConfigs unmarshals the JSON signal_configs column and extracts
// the timeframe (tf param) from the first config that has one.
func parseSignalConfigs(sigConfigsJSON string) ([]signal.Config, string) {
	var raw []struct {
		Name   string                 `json:"name"`
		Params map[string]interface{} `json:"params"`
	}
	if err := json.Unmarshal([]byte(sigConfigsJSON), &raw); err != nil || len(raw) == 0 {
		return nil, ""
	}
	cfgs := make([]signal.Config, len(raw))
	tf := "60"
	for i, r := range raw {
		cfgs[i] = signal.Config{Name: r.Name, Params: r.Params}
		if v, ok := r.Params["tf"]; ok {
			if sv, ok2 := v.(string); ok2 && sv != "" {
				tf = sv
			}
		}
	}
	return cfgs, tf
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
