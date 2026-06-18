// services/api-gateway/bot_handler.go
package main

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"time"
)

type botStrategySummary struct {
	ID           string  `json:"id"`
	Symbol       string  `json:"symbol"`
	Direction    string  `json:"direction"`
	Status       string  `json:"status"`
	ActiveLevels int     `json:"active_levels"`
	GridLevels   int     `json:"grid_levels"`
}

// BotSummary returns strategy list and aggregated P&L for the given chat_id.
// GET /bot/summary?chat_id=N  (BOT_SECRET required)
func (s *Server) BotSummary(w http.ResponseWriter, r *http.Request) {
	chatID, err := strconv.ParseInt(r.URL.Query().Get("chat_id"), 10, 64)
	if err != nil || chatID == 0 {
		writeError(w, http.StatusBadRequest, "chat_id required")
		return
	}
	ctx := r.Context()
	userID, err := s.userIDFromChatID(ctx, chatID)
	if err != nil {
		writeError(w, http.StatusNotFound, "telegram account not linked")
		return
	}

	rows, err := s.pool.Query(ctx,
		`SELECT s.id, s.symbol, s.direction, s.status, s.grid_levels,
		 COALESCE((
		     SELECT COUNT(*)::int
		     FROM strategy_levels sl
		     JOIN strategy_cycles sc ON sl.cycle_id = sc.id
		     WHERE sc.strategy_id = s.id AND sc.ended_at IS NULL AND sl.status = 'filled'
		 ), 0) AS active_levels
		 FROM strategies s WHERE s.owner_id=$1 ORDER BY s.created_at DESC`, userID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	defer rows.Close()
	var strategies []botStrategySummary
	for rows.Next() {
		var st botStrategySummary
		if err := rows.Scan(&st.ID, &st.Symbol, &st.Direction, &st.Status, &st.GridLevels, &st.ActiveLevels); err != nil {
			continue
		}
		strategies = append(strategies, st)
	}
	if err := rows.Err(); err != nil {
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if strategies == nil {
		strategies = []botStrategySummary{}
	}

	var pnlToday, pnlWeek float64

	writeJSON(w, http.StatusOK, map[string]any{
		"strategies": strategies,
		"pnl_today":  pnlToday,
		"pnl_week":   pnlWeek,
	})
}

// BotPauseAll stops all active strategies for the given chat_id.
// POST /bot/pause-all  (BOT_SECRET required)
func (s *Server) BotPauseAll(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ChatID int64 `json:"chat_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.ChatID == 0 {
		writeError(w, http.StatusBadRequest, "chat_id required")
		return
	}
	ctx := r.Context()
	userID, err := s.userIDFromChatID(ctx, req.ChatID)
	if err != nil {
		writeError(w, http.StatusNotFound, "telegram account not linked")
		return
	}

	rows, err := s.pool.Query(ctx,
		`UPDATE strategies SET status='stopped', updated_at=NOW()
		 WHERE owner_id=$1 AND status='active'
		 RETURNING id`, userID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if rows.Scan(&id) == nil {
			ids = append(ids, id)
		}
	}
	if err := rows.Err(); err != nil {
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	go func() {
		for _, id := range ids {
			s.engine.Notify(context.Background(), id)
			s.engine.LogUserAction(context.Background(), id, "Остановлено через Telegram")
		}
	}()
	writeJSON(w, http.StatusOK, map[string]any{"stopped": len(ids)})
}

// BotResumeAll activates all stopped strategies for the given chat_id.
// POST /bot/resume-all  (BOT_SECRET required)
func (s *Server) BotResumeAll(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ChatID int64 `json:"chat_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.ChatID == 0 {
		writeError(w, http.StatusBadRequest, "chat_id required")
		return
	}
	ctx := r.Context()
	userID, err := s.userIDFromChatID(ctx, req.ChatID)
	if err != nil {
		writeError(w, http.StatusNotFound, "telegram account not linked")
		return
	}

	rows, err := s.pool.Query(ctx,
		`UPDATE strategies SET status='active', updated_at=NOW()
		 WHERE owner_id=$1 AND status='stopped'
		 RETURNING id`, userID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if rows.Scan(&id) == nil {
			ids = append(ids, id)
		}
	}
	if err := rows.Err(); err != nil {
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	go func() {
		for _, id := range ids {
			s.engine.Notify(context.Background(), id)
			s.engine.LogUserAction(context.Background(), id, "Запущено через Telegram")
		}
	}()
	writeJSON(w, http.StatusOK, map[string]any{"started": len(ids)})
}

// BotStrategyStatus sets status for a single strategy (used by inline button callbacks).
// POST /bot/strategy-status  (BOT_SECRET required)
func (s *Server) BotStrategyStatus(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ChatID     int64  `json:"chat_id"`
		StrategyID string `json:"strategy_id"`
		Status     string `json:"status"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if req.ChatID == 0 || req.StrategyID == "" {
		writeError(w, http.StatusBadRequest, "chat_id and strategy_id required")
		return
	}
	if req.Status != "active" && req.Status != "finishing" && req.Status != "stopped" {
		writeError(w, http.StatusBadRequest, "status must be active|finishing|stopped")
		return
	}
	ctx := r.Context()
	userID, err := s.userIDFromChatID(ctx, req.ChatID)
	if err != nil {
		writeError(w, http.StatusNotFound, "telegram account not linked")
		return
	}
	tag, err := s.pool.Exec(ctx,
		`UPDATE strategies SET status=$1, updated_at=NOW()
		 WHERE id=$2 AND owner_id=$3`, req.Status, req.StrategyID, userID)
	if err != nil || tag.RowsAffected() == 0 {
		writeError(w, http.StatusNotFound, "strategy not found")
		return
	}
	go func() {
		s.engine.Notify(context.Background(), req.StrategyID)
		s.engine.LogUserAction(context.Background(), req.StrategyID, "Статус изменён через Telegram: "+req.Status)
	}()
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// BotMute sets mute_until for the given chat_id.
// POST /bot/mute  (BOT_SECRET required)
func (s *Server) BotMute(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ChatID int64  `json:"chat_id"`
		Until  string `json:"until"` // RFC3339 timestamp
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.ChatID == 0 {
		writeError(w, http.StatusBadRequest, "chat_id required")
		return
	}
	if req.Until != "" {
		if _, err := time.Parse(time.RFC3339, req.Until); err != nil {
			writeError(w, http.StatusBadRequest, "until must be RFC3339 timestamp")
			return
		}
	}
	tag, err := s.pool.Exec(r.Context(),
		`UPDATE telegram_connections SET mute_until=$1 WHERE chat_id=$2`,
		req.Until, req.ChatID,
	)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if tag.RowsAffected() == 0 {
		writeError(w, http.StatusNotFound, "telegram account not linked")
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// userIDFromChatID resolves a Telegram chat_id to a user UUID.
func (s *Server) userIDFromChatID(ctx context.Context, chatID int64) (string, error) {
	var userID string
	err := s.pool.QueryRow(ctx,
		`SELECT user_id FROM telegram_connections WHERE chat_id=$1`, chatID,
	).Scan(&userID)
	return userID, err
}
