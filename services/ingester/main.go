// services/ingester/main.go
package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/joho/godotenv"
	"sis/pkg/cache"
	"sis/pkg/db"
	"sis/pkg/exchange"
	binanceclient "sis/pkg/exchange/binance"
	bybitclient "sis/pkg/exchange/bybit"
	"sis/pkg/models"
)

func main() {
	_ = godotenv.Load()

	dsn := mustEnv("DATABASE_URL")
	redisURL := mustEnv("REDIS_URL")
	symbolsRaw := getEnv("SYMBOLS", "BTCUSDT,ETHUSDT")
	marketsRaw := getEnv("MARKETS", "spot,futures")
	tfsRaw := getEnv("TIMEFRAMES", "1m,5m,15m,1h")

	symbols := strings.Split(symbolsRaw, ",")
	markets := parseMarkets(marketsRaw)
	tfs := parseTimeframes(tfsRaw)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	pool, err := db.Connect(ctx, dsn)
	if err != nil {
		log.Fatalf("db connect: %v", err)
	}
	defer pool.Close()

	if err := db.Migrate(ctx, pool, "migrations"); err != nil {
		log.Fatalf("migrate: %v", err)
	}

	rdb, err := cache.Connect(ctx, redisURL)
	if err != nil {
		log.Fatalf("redis connect: %v", err)
	}
	defer rdb.Close()

	clients := []exchange.Client{
		binanceclient.New(),
		bybitclient.New(),
	}

	ingester := NewIngester(pool, rdb, symbols, markets, tfs)
	log.Println("ingester: starting")

	if err := ingester.Run(ctx, clients); err != nil {
		log.Fatalf("ingester: %v", err)
	}
	log.Println("ingester: stopped")
}

func mustEnv(key string) string {
	v := os.Getenv(key)
	if v == "" {
		log.Fatalf("required env var %s is not set", key)
	}
	return v
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func parseMarkets(raw string) []models.Market {
	parts := strings.Split(raw, ",")
	markets := make([]models.Market, 0, len(parts))
	for _, p := range parts {
		switch strings.TrimSpace(p) {
		case "spot":
			markets = append(markets, models.MarketSpot)
		case "futures":
			markets = append(markets, models.MarketFutures)
		}
	}
	return markets
}

func parseTimeframes(raw string) []models.Timeframe {
	parts := strings.Split(raw, ",")
	tfs := make([]models.Timeframe, 0, len(parts))
	for _, p := range parts {
		tfs = append(tfs, models.Timeframe(strings.TrimSpace(p)))
	}
	return tfs
}
