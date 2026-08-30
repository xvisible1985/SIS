// services/api-gateway/tg_notifier.go
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"time"

	"github.com/jackc/pgx/v5"
)

// TgNotifyMsg is the message format published to Redis channel "tg:notify".
type TgNotifyMsg struct {
	ChatID       int64  `json:"chat_id"`
	Text         string `json:"text"`
	StrategyID   string `json:"strategy_id,omitempty"`
	ShowPauseBtn bool   `json:"show_pause_btn,omitempty"`
}

const tgNotifyChannel = "tg:notify"

// publishTgNotify publishes a notification message to Redis for the tg-bot to deliver.
func (s *Server) publishTgNotify(ctx context.Context, msg TgNotifyMsg) {
	if s.rdb == nil {
		return
	}
	data, err := json.Marshal(msg)
	if err != nil {
		return
	}
	if err := s.rdb.Publish(ctx, tgNotifyChannel, string(data)).Err(); err != nil {
		log.Printf("tg_notifier: publish error: %v", err)
	}
}

// onRiskEvent is registered as strategy.OnRiskEvent at startup (see server.go) — fires
// once per pause-state TRANSITION (checkRiskOnce already collapses per-tick repeats before
// calling this, so no extra de-duplication is needed here).
//
// Wrapped in its own recover(): called from pkg/strategy's risk-monitor loop, which runs
// inside safeLoop's panic recovery — lower stakes than onAccumulateChange's bare
// unrecovered goroutines, but still shouldn't rely solely on that outer safety net.
func (s *Server) onRiskEvent(accountID string, entering bool, mmRatePct, pausePct float64) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("onRiskEvent: recovered panic for account %s: %v", accountID, r)
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var label string
	var chatID int64
	err := s.pool.QueryRow(ctx, `
		SELECT ea.label, tc.chat_id
		FROM exchange_accounts ea
		JOIN telegram_connections tc ON tc.user_id = ea.owner_id
		LEFT JOIN telegram_notification_settings tns ON tns.user_id = ea.owner_id
		WHERE ea.id = $1
		  AND (tc.mute_until IS NULL OR tc.mute_until < NOW())
		  AND COALESCE(tns.on_trade, true) = true`,
		accountID,
	).Scan(&label, &chatID)
	if err != nil {
		if !errors.Is(err, pgx.ErrNoRows) {
			log.Printf("onRiskEvent: lookup account=%s: %v", accountID, err)
		}
		return
	}

	var text string
	if entering {
		text = fmt.Sprintf("🔴 *%s* — риск-пауза по счёту: margin ratio %.1f%% ≥ порога паузы %.1f%%. Новые входы (DCA/уровни) остановлены до снижения margin ratio ниже порога предупреждения.", label, mmRatePct, pausePct)
	} else {
		text = fmt.Sprintf("✅ *%s* — риск-пауза снята: margin ratio %.1f%%. Новые входы возобновлены.", label, mmRatePct)
	}
	s.publishTgNotify(ctx, TgNotifyMsg{ChatID: chatID, Text: text})
}

// startTgNotifier polls strategy_events for un-notified error/warn entries and
// publishes them to Redis. Runs as a background goroutine.
func (s *Server) startTgNotifier(ctx context.Context) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.flushPendingTgNotifications(ctx)
		}
	}
}

func (s *Server) flushPendingTgNotifications(ctx context.Context) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		log.Printf("tg_notifier: begin tx error: %v", err)
		return
	}
	defer tx.Rollback(ctx)

	rows, err := tx.Query(ctx, `
		SELECT se.id, se.message, se.level, se.strategy_id,
		       st.symbol, tc.chat_id,
		       COALESCE(tns.on_trade, true)
		FROM strategy_events se
		JOIN strategies st ON st.id = se.strategy_id
		JOIN telegram_connections tc ON tc.user_id = st.owner_id
		LEFT JOIN telegram_notification_settings tns ON tns.user_id = st.owner_id
		WHERE se.tg_notified = false
		  AND se.level IN ('error', 'warn')
		  AND se.created_at > NOW() - INTERVAL '1 hour'
		  AND (tc.mute_until IS NULL OR tc.mute_until < NOW())
		  AND COALESCE(tns.on_trade, true) = true
		ORDER BY se.created_at ASC
		LIMIT 50
		FOR UPDATE OF se SKIP LOCKED
	`)
	if err != nil {
		log.Printf("tg_notifier: query error: %v", err)
		return
	}

	type row struct {
		eventID    string
		message    string
		level      string
		strategyID string
		symbol     string
		chatID     int64
		onTrade    bool
	}
	var pending []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.eventID, &r.message, &r.level, &r.strategyID, &r.symbol, &r.chatID, &r.onTrade); err != nil {
			continue
		}
		pending = append(pending, r)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		log.Printf("tg_notifier: rows error: %v", err)
		return
	}

	for _, p := range pending {
		// Mark notified first to avoid duplicate sends on crash
		if _, err := tx.Exec(ctx,
			`UPDATE strategy_events SET tg_notified=true WHERE id=$1`, p.eventID); err != nil {
			log.Printf("tg_notifier: mark notified failed for %s: %v", p.eventID, err)
			continue
		}
	}
	if err := tx.Commit(ctx); err != nil {
		log.Printf("tg_notifier: commit error: %v", err)
		return
	}

	// Publish after commit so the DB state is final before notifying
	for _, p := range pending {
		icon := "⚠️"
		if p.level == "error" {
			icon = "🔴"
		}
		text := fmt.Sprintf("%s *%s* — %s", icon, p.symbol, p.message)
		s.publishTgNotify(ctx, TgNotifyMsg{
			ChatID:       p.chatID,
			Text:         text,
			StrategyID:   p.strategyID,
			ShowPauseBtn: p.level == "error",
		})
	}
}
