// services/tg-bot/client.go
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// GatewayClient calls api-gateway bot-internal endpoints.
type GatewayClient struct {
	baseURL   string
	botSecret string
	http      *http.Client
}

func NewGatewayClient(baseURL, botSecret string) *GatewayClient {
	return &GatewayClient{
		baseURL:   baseURL,
		botSecret: botSecret,
		http:      &http.Client{Timeout: 10 * time.Second},
	}
}

func (c *GatewayClient) do(ctx context.Context, method, path string, body any, out any) error {
	var buf bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&buf).Encode(body); err != nil {
			return err
		}
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, &buf)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.botSecret)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		var e map[string]string
		json.NewDecoder(resp.Body).Decode(&e)
		return fmt.Errorf("gateway %d: %s", resp.StatusCode, e["error"])
	}
	if out != nil {
		return json.NewDecoder(resp.Body).Decode(out)
	}
	return nil
}

// TelegramLoginRequest gets a magic-link URL for the given chat_id.
func (c *GatewayClient) TelegramLoginRequest(ctx context.Context, chatID int64, username string) (string, error) {
	var resp struct {
		URL string `json:"url"`
	}
	err := c.do(ctx, http.MethodPost, "/auth/telegram", map[string]any{
		"chat_id":  chatID,
		"username": username,
	}, &resp)
	return resp.URL, err
}

type StrategySummary struct {
	ID           string `json:"id"`
	Symbol       string `json:"symbol"`
	Direction    string `json:"direction"`
	Status       string `json:"status"`
	ActiveLevels int    `json:"active_levels"`
	GridLevels   int    `json:"grid_levels"`
}

type BotSummaryResp struct {
	Strategies []StrategySummary `json:"strategies"`
	PnlToday   float64           `json:"pnl_today"`
	PnlWeek    float64           `json:"pnl_week"`
}

// BotSummary fetches strategies + P&L for a chat_id.
func (c *GatewayClient) BotSummary(ctx context.Context, chatID int64) (*BotSummaryResp, error) {
	u := fmt.Sprintf("/bot/summary?chat_id=%d", chatID)
	var resp BotSummaryResp
	err := c.do(ctx, http.MethodGet, u, nil, &resp)
	return &resp, err
}

// PauseAll stops all active strategies for the given chat_id.
func (c *GatewayClient) PauseAll(ctx context.Context, chatID int64) (int, error) {
	var resp struct {
		Stopped int `json:"stopped"`
	}
	err := c.do(ctx, http.MethodPost, "/bot/pause-all", map[string]any{"chat_id": chatID}, &resp)
	return resp.Stopped, err
}

// ResumeAll activates all stopped strategies for the given chat_id.
func (c *GatewayClient) ResumeAll(ctx context.Context, chatID int64) (int, error) {
	var resp struct {
		Started int `json:"started"`
	}
	err := c.do(ctx, http.MethodPost, "/bot/resume-all", map[string]any{"chat_id": chatID}, &resp)
	return resp.Started, err
}

// StrategyStatus changes the status of a single strategy.
func (c *GatewayClient) StrategyStatus(ctx context.Context, chatID int64, strategyID, status string) error {
	return c.do(ctx, http.MethodPost, "/bot/strategy-status", map[string]any{
		"chat_id":     chatID,
		"strategy_id": strategyID,
		"status":      status,
	}, nil)
}

// MuteUntil sets mute_until for the given chat_id.
func (c *GatewayClient) MuteUntil(ctx context.Context, chatID int64, until string) error {
	return c.do(ctx, http.MethodPost, "/bot/mute", map[string]any{
		"chat_id": chatID,
		"until":   until,
	}, nil)
}
