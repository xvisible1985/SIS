// services/webhook/dispatcher.go
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

const (
	streamFired    = "signals:fired"
	consumerGroup  = "webhook-dispatcher"
	consumerName   = "dispatcher-1"
	deliverTimeout = 10 * time.Second
	maxAttempts    = 3
)

// FiredSignal is the payload published to the signals:fired Redis Stream.
type FiredSignal struct {
	SignalID   string `json:"signal_id"`
	SignalName string `json:"signal_name"`
	Symbol     string `json:"symbol"`
	Exchange   string `json:"exchange"`
	Market     string `json:"market"`
	Direction  string `json:"direction"`
	Price      string `json:"price"`
	Timestamp  string `json:"timestamp"`
}

// WebhookTarget holds the data needed to deliver to one webhook endpoint.
type WebhookTarget struct {
	ID  string
	URL string
}

// DeliveryResult records the outcome of a single HTTP POST attempt.
type DeliveryResult struct {
	StatusCode int
	ResponseMs int64
	Success    bool
	Error      string
}

// Dispatcher reads fired signals from Redis and delivers webhooks.
type Dispatcher struct {
	pool   *pgxpool.Pool
	rdb    *redis.Client
	client *http.Client
}

// NewDispatcher creates a Dispatcher with a 10-second HTTP timeout.
func NewDispatcher(pool *pgxpool.Pool, rdb *redis.Client) *Dispatcher {
	return &Dispatcher{
		pool:   pool,
		rdb:    rdb,
		client: &http.Client{Timeout: deliverTimeout},
	}
}

// retryDelays returns the wait durations between delivery attempts.
func retryDelays() []time.Duration {
	return []time.Duration{1 * time.Second, 5 * time.Second, 30 * time.Second}
}

// parseSignalPayload unmarshals a JSON string from a Redis stream message.
func parseSignalPayload(raw string) (FiredSignal, error) {
	var s FiredSignal
	if err := json.Unmarshal([]byte(raw), &s); err != nil {
		return FiredSignal{}, fmt.Errorf("parseSignalPayload: %w", err)
	}
	return s, nil
}

// deliverOnce sends a single HTTP POST with the FiredSignal payload to url.
func deliverOnce(ctx context.Context, client *http.Client, url string, payload FiredSignal) DeliveryResult {
	body, _ := json.Marshal(payload)
	start := time.Now()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return DeliveryResult{Error: err.Error()}
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	elapsed := time.Since(start).Milliseconds()
	if err != nil {
		return DeliveryResult{ResponseMs: elapsed, Error: err.Error()}
	}
	defer resp.Body.Close()

	return DeliveryResult{
		StatusCode: resp.StatusCode,
		ResponseMs: elapsed,
		Success:    resp.StatusCode >= 200 && resp.StatusCode < 300,
	}
}

// Run starts the consumer loop. Blocks until ctx is cancelled.
func (d *Dispatcher) Run(ctx context.Context) {
	d.rdb.XGroupCreateMkStream(ctx, streamFired, consumerGroup, "0")
	log.Printf("webhook-dispatcher: listening on stream %s", streamFired)
	for {
		if ctx.Err() != nil {
			return
		}
		msgs, err := d.rdb.XReadGroup(ctx, &redis.XReadGroupArgs{
			Group:    consumerGroup,
			Consumer: consumerName,
			Streams:  []string{streamFired, ">"},
			Count:    1,
			Block:    5 * time.Second,
		}).Result()
		if err == redis.Nil {
			continue
		}
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			log.Printf("webhook-dispatcher: xreadgroup error: %v", err)
			continue
		}
		for _, stream := range msgs {
			for _, msg := range stream.Messages {
				d.handleMessage(ctx, msg)
			}
		}
	}
}

func (d *Dispatcher) handleMessage(ctx context.Context, msg redis.XMessage) {
	raw, ok := msg.Values["payload"]
	if !ok {
		log.Printf("webhook-dispatcher: message %s missing payload field", msg.ID)
		d.ack(ctx, msg.ID)
		return
	}
	signal, err := parseSignalPayload(raw.(string))
	if err != nil {
		log.Printf("webhook-dispatcher: parse error message %s: %v", msg.ID, err)
		d.ack(ctx, msg.ID)
		return
	}
	targets, err := d.fetchWebhooks(ctx, signal.SignalID)
	if err != nil {
		log.Printf("webhook-dispatcher: fetch webhooks signal %s: %v", signal.SignalID, err)
		d.ack(ctx, msg.ID)
		return
	}
	for _, target := range targets {
		d.deliverWithRetry(ctx, target, signal)
	}
	d.ack(ctx, msg.ID)
}

func (d *Dispatcher) fetchWebhooks(ctx context.Context, signalID string) ([]WebhookTarget, error) {
	rows, err := d.pool.Query(ctx,
		`SELECT id, url FROM webhooks WHERE signal_id=$1 AND is_active=TRUE`,
		signalID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var targets []WebhookTarget
	for rows.Next() {
		var t WebhookTarget
		if err := rows.Scan(&t.ID, &t.URL); err != nil {
			return nil, err
		}
		targets = append(targets, t)
	}
	return targets, nil
}

func (d *Dispatcher) deliverWithRetry(ctx context.Context, target WebhookTarget, payload FiredSignal) {
	delays := retryDelays()
	for attempt := 0; attempt < maxAttempts; attempt++ {
		if ctx.Err() != nil {
			return
		}
		result := deliverOnce(ctx, d.client, target.URL, payload)
		d.saveLog(ctx, target.ID, result)
		if result.Success {
			log.Printf("webhook-dispatcher: delivered webhook %s (attempt %d)", target.ID, attempt+1)
			return
		}
		log.Printf("webhook-dispatcher: failed webhook %s attempt %d status=%d err=%s",
			target.ID, attempt+1, result.StatusCode, result.Error)
		if attempt < len(delays) {
			select {
			case <-ctx.Done():
				return
			case <-time.After(delays[attempt]):
			}
		}
	}
	log.Printf("webhook-dispatcher: giving up on webhook %s after %d attempts", target.ID, maxAttempts)
}

func (d *Dispatcher) saveLog(ctx context.Context, webhookID string, r DeliveryResult) {
	_, err := d.pool.Exec(ctx,
		`INSERT INTO webhook_logs (webhook_id, status_code, response_ms, success, error)
		 VALUES ($1, $2, $3, $4, $5)`,
		webhookID, r.StatusCode, r.ResponseMs, r.Success, r.Error,
	)
	if err != nil {
		log.Printf("webhook-dispatcher: saveLog webhook %s: %v", webhookID, err)
	}
}

func (d *Dispatcher) ack(ctx context.Context, msgID string) {
	if err := d.rdb.XAck(ctx, streamFired, consumerGroup, msgID).Err(); err != nil {
		log.Printf("webhook-dispatcher: ack error %s: %v", msgID, err)
	}
}

func main() {}
