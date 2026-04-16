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

			if msg.Status == "done" {
				return
			}
		}
	}
}
