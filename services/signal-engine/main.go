// services/signal-engine/main.go
package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/joho/godotenv"
	"github.com/redis/go-redis/v9"
	"sis/pkg/cache"
	"sis/pkg/db"
	"sis/pkg/heartbeat"
	sig "sis/pkg/signal"
)

func main() {
	_ = godotenv.Load()

	dsn := mustEnv("DATABASE_URL")
	redisURL := mustEnv("REDIS_URL")

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

	go heartbeat.Start(ctx, rdb, "signal-engine")

	warmWhaleState(ctx, pool)
	go func() {
		t := time.NewTicker(30 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				pollWhaleState(ctx, rdb)
			}
		}
	}()

	worker := NewWorker(pool, rdb)
	log.Println("signal-engine: starting")
	go worker.RunOptimizer(ctx)
	worker.Start(ctx)
	log.Println("signal-engine: stopped")
}

func mustEnv(key string) string {
	v := os.Getenv(key)
	if v == "" {
		log.Fatalf("required env var %s is not set", key)
	}
	return v
}

func warmWhaleState(ctx context.Context, pool *pgxpool.Pool) {
	rows, err := pool.Query(ctx, `
		SELECT DISTINCT ON (symbol) symbol, direction
		FROM whale_events
		WHERE detected_at > now() - interval '2 hours'
		ORDER BY symbol, detected_at DESC
	`)
	if err != nil {
		log.Printf("whale warm: db query: %v", err)
		return
	}
	defer rows.Close()
	count := 0
	for rows.Next() {
		var sym, dir string
		if err := rows.Scan(&sym, &dir); err != nil {
			continue
		}
		sig.SetWhaleState(sym, sig.State(dir))
		count++
	}
	log.Printf("whale warm: loaded %d symbols from DB", count)
}

func pollWhaleState(ctx context.Context, rdb *redis.Client) {
	states, err := rdb.HGetAll(ctx, "whale:state").Result()
	if err != nil {
		log.Printf("whale poll: redis: %v", err)
		return
	}
	for sym, dir := range states {
		sig.SetWhaleState(sym, sig.State(dir))
	}
}
