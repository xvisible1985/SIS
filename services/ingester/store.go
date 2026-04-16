// services/ingester/store.go
package main

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"sis/pkg/models"
)

// StoreBatch upserts a batch of candles into TimescaleDB.
func StoreBatch(ctx context.Context, pool *pgxpool.Pool, candles []models.Candle) error {
	if len(candles) == 0 {
		return nil
	}
	batch := &pgx.Batch{}
	sql := `INSERT INTO candles (exchange,symbol,market,timeframe,open_time,open,high,low,close,volume)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
			ON CONFLICT DO NOTHING`
	for _, c := range candles {
		batch.Queue(sql, string(c.Exchange), c.Symbol, string(c.Market), string(c.Timeframe),
			c.OpenTime, c.Open, c.High, c.Low, c.Close, c.Volume)
	}
	results := pool.SendBatch(ctx, batch)
	defer results.Close()
	for range candles {
		if _, err := results.Exec(); err != nil {
			return fmt.Errorf("store upsert: %w", err)
		}
	}
	return nil
}
