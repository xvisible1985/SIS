// services/parser/main.go
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/joho/godotenv"
	"sis/pkg/cache"
	"sis/pkg/db"
	"sis/pkg/heartbeat"
)

func main() {
	_ = godotenv.Load()

	dsn      := mustEnv("DATABASE_URL")
	redisURL := mustEnv("REDIS_URL")

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	pool, err := db.Connect(ctx, dsn)
	if err != nil {
		log.Fatalf("db connect: %v", err)
	}
	defer pool.Close()

	rdb, err := cache.Connect(ctx, redisURL)
	if err != nil {
		log.Fatalf("redis connect: %v", err)
	}
	defer rdb.Close()

	go heartbeat.Start(ctx, rdb, "parser")

	// Bybit news scraper (migrated from api-gateway)
	go runBybitNews(ctx, pool)

	// TODO: whale tracker — uncomment after whale package is created
	// cfg := whale.Config{
	//     EtherscanKey:    getEnv("ETHERSCAN_API_KEY", ""),
	//     TronGridKey:     getEnv("TRONGRID_API_KEY", ""),
	//     ThresholdUSDT:   getEnvFloat("WHALE_THRESHOLD_USDT", 50000),
	//     PollInterval:    30 * time.Second,
	//     DiscoveryWeekly: true,
	// }
	// tracker := whale.NewTracker(pool, rdb, cfg)
	// go tracker.Start(ctx)

	_ = time.Second // used by whale tracker (keep import)

	log.Println("parser: started")
	<-ctx.Done()
	log.Println("parser: stopped")
}

func mustEnv(key string) string {
	v := os.Getenv(key)
	if v == "" {
		log.Fatalf("required env var %s is not set", key)
	}
	return v
}

func getEnv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func getEnvFloat(key string, def float64) float64 {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	var f float64
	if _, err := fmt.Sscanf(v, "%f", &f); err != nil {
		return def
	}
	return f
}
