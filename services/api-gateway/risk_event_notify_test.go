//go:build integration

package main

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

// subscribeTgNotify subscribes to the tg:notify Redis channel and returns a channel that
// yields the next published TgNotifyMsg, or times out after 3s (nil).
func subscribeTgNotify(t *testing.T, s *Server) <-chan *TgNotifyMsg {
	t.Helper()
	sub := s.rdb.Subscribe(context.Background(), tgNotifyChannel)
	t.Cleanup(func() { sub.Close() })
	// Block until the subscription is actually active — otherwise a publish sent
	// immediately after this call can race the SUBSCRIBE and be missed.
	if _, err := sub.Receive(context.Background()); err != nil {
		t.Fatalf("subscribe: %v", err)
	}

	out := make(chan *TgNotifyMsg, 1)
	go func() {
		ch := sub.Channel()
		select {
		case redisMsg, ok := <-ch:
			if !ok {
				out <- nil
				return
			}
			var msg TgNotifyMsg
			if json.Unmarshal([]byte(redisMsg.Payload), &msg) != nil {
				out <- nil
				return
			}
			out <- &msg
		case <-time.After(3 * time.Second):
			out <- nil
		}
	}()
	return out
}

func TestOnRiskEvent_PublishesToConnectedTelegramChat(t *testing.T) {
	s := newTestServer(t)
	userID := createWHUser(t, s, "risknotify1")
	accID := createTestAccount(t, s, userID)
	ctx := context.Background()

	const chatID int64 = 918273645
	if _, err := s.pool.Exec(ctx,
		`INSERT INTO telegram_connections (user_id, chat_id) VALUES ($1,$2)`, userID, chatID,
	); err != nil {
		t.Fatalf("seed telegram_connections: %v", err)
	}
	t.Cleanup(func() { s.pool.Exec(context.Background(), "DELETE FROM telegram_connections WHERE user_id=$1", userID) })

	got := subscribeTgNotify(t, s)
	s.onRiskEvent(accID, true, 82.5, 75.0)
	msg := <-got
	if msg == nil {
		t.Fatal("expected a tg:notify message for entering pause, got none (timeout)")
	}
	if msg.ChatID != chatID {
		t.Errorf("ChatID = %d, want %d", msg.ChatID, chatID)
	}
	if msg.Text == "" {
		t.Error("Text must not be empty")
	}
}

func TestOnRiskEvent_SkipsMutedChat(t *testing.T) {
	s := newTestServer(t)
	userID := createWHUser(t, s, "risknotify2")
	accID := createTestAccount(t, s, userID)
	ctx := context.Background()

	if _, err := s.pool.Exec(ctx,
		`INSERT INTO telegram_connections (user_id, chat_id, mute_until) VALUES ($1,111,NOW()+INTERVAL '1 hour')`, userID,
	); err != nil {
		t.Fatalf("seed muted telegram_connections: %v", err)
	}
	t.Cleanup(func() { s.pool.Exec(context.Background(), "DELETE FROM telegram_connections WHERE user_id=$1", userID) })

	got := subscribeTgNotify(t, s)
	s.onRiskEvent(accID, true, 82.5, 75.0)
	if msg := <-got; msg != nil {
		t.Errorf("expected no message while muted, got: %+v", msg)
	}
}

func TestOnRiskEvent_NoTelegramConnection_NoPanic(t *testing.T) {
	s := newTestServer(t)
	userID := createWHUser(t, s, "risknotify3")
	accID := createTestAccount(t, s, userID)

	got := subscribeTgNotify(t, s)
	s.onRiskEvent(accID, false, 40.0, 75.0) // no telegram_connections row for this user at all
	if msg := <-got; msg != nil {
		t.Errorf("expected no message without a telegram connection, got: %+v", msg)
	}
}
