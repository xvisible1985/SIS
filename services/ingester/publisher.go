// services/ingester/publisher.go
package main

import (
	"context"
	"log"

	"github.com/redis/go-redis/v9"
	"sis/pkg/cache"
	"sis/pkg/models"
)

// Publisher publishes closed candles to Redis pub/sub.
type Publisher struct {
	rdb *redis.Client
}

func NewPublisher(rdb *redis.Client) *Publisher {
	return &Publisher{rdb: rdb}
}

// Publish sends a closed candle to Redis. Non-fatal on error.
func (p *Publisher) Publish(ctx context.Context, candle models.Candle) {
	if !candle.Closed {
		return
	}
	if err := cache.PublishCandle(ctx, p.rdb, candle); err != nil {
		log.Printf("publisher: %v", err)
	}
}
