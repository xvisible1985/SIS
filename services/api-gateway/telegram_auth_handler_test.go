//go:build integration

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestTelegramLoginRequest_NewUser(t *testing.T) {
	s := newTestServer(t)
	chatID := int64(999001)
	body := fmt.Sprintf(`{"chat_id":%d,"username":"testuser"}`, chatID)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/auth/telegram", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	s.TelegramLoginRequest(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("got %d: %s", rec.Code, rec.Body.String())
	}
	var resp map[string]string
	json.NewDecoder(rec.Body).Decode(&resp)
	if resp["url"] == "" {
		t.Error("expected url in response")
	}
	t.Cleanup(func() {
		s.pool.Exec(context.Background(),
			`DELETE FROM users WHERE email=$1`, fmt.Sprintf("tg_%d@telegram.invalid", chatID))
		s.pool.Exec(context.Background(),
			`DELETE FROM telegram_auth_tokens WHERE chat_id=$1`, chatID)
	})
}

func TestTelegramLoginCallback_ValidToken(t *testing.T) {
	s := newTestServer(t)
	chatID := int64(999002)

	// Setup: create user + connection + token
	var userID string
	s.pool.QueryRow(context.Background(),
		`INSERT INTO users (email, password_hash) VALUES ($1,'') RETURNING id`,
		fmt.Sprintf("tg_%d@telegram.invalid", chatID),
	).Scan(&userID)
	s.pool.Exec(context.Background(),
		`INSERT INTO telegram_connections (user_id, chat_id) VALUES ($1,$2)`, userID, chatID)
	token := newUUID()
	s.pool.Exec(context.Background(),
		`INSERT INTO telegram_auth_tokens (token, chat_id) VALUES ($1,$2)`, token, chatID)

	t.Cleanup(func() {
		s.pool.Exec(context.Background(), `DELETE FROM users WHERE id=$1`, userID)
		s.pool.Exec(context.Background(), `DELETE FROM telegram_auth_tokens WHERE chat_id=$1`, chatID)
	})

	body := fmt.Sprintf(`{"token":%q}`, token)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/auth/telegram-callback", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	s.TelegramLoginCallback(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("got %d: %s", rec.Code, rec.Body.String())
	}
	var resp map[string]string
	json.NewDecoder(rec.Body).Decode(&resp)
	if resp["token"] == "" {
		t.Error("expected JWT token in response")
	}
}

func TestTelegramLoginCallback_InvalidToken(t *testing.T) {
	s := newTestServer(t)
	body := `{"token":"nonexistent-token"}`
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/auth/telegram-callback", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	s.TelegramLoginCallback(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("got %d, want 400", rec.Code)
	}
}
