// services/ingester/ingester.go
package main

import (
	"context"
	"log"
	"sync"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
	"sis/pkg/exchange"
	"sis/pkg/models"
)

// Ingester streams candles from multiple exchanges and persists them.
type Ingester struct {
	pool       *pgxpool.Pool
	publisher  *Publisher
	symbols    []string
	markets    []models.Market
	timeframes []models.Timeframe
}

func NewIngester(pool *pgxpool.Pool, rdb *redis.Client, symbols []string, markets []models.Market, tfs []models.Timeframe) *Ingester {
	return &Ingester{
		pool:       pool,
		publisher:  NewPublisher(rdb),
		symbols:    symbols,
		markets:    markets,
		timeframes: tfs,
	}
}

// Run starts all exchange subscriptions concurrently. Blocks until ctx is cancelled.
func (ing *Ingester) Run(ctx context.Context, clients []exchange.Client) error {
	var wg sync.WaitGroup

	for _, client := range clients {
		for _, market := range ing.markets {
			for _, tf := range ing.timeframes {
				client := client
				market := market
				tf := tf

				wg.Add(1)
				go func() {
					defer wg.Done()
					log.Printf("ingester: subscribing %s %s %s symbols=%v",
						client.Name(), market, tf, ing.symbols)

					err := client.Subscribe(ctx, ing.symbols, market, tf, func(candle models.Candle) {
						ing.handleCandle(ctx, candle)
					})
					if err != nil {
						log.Printf("ingester: subscribe error %s %s %s: %v", client.Name(), market, tf, err)
					}
				}()
			}
		}
	}

	wg.Wait()
	return nil
}

func (ing *Ingester) handleCandle(ctx context.Context, candle models.Candle) {
	if err := StoreBatch(ctx, ing.pool, []models.Candle{candle}); err != nil {
		log.Printf("ingester: store error: %v", err)
	}
	ing.publisher.Publish(ctx, candle)
}
