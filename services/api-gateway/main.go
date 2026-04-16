// services/api-gateway/main.go
package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/joho/godotenv"
	"sis/pkg/cache"
	"sis/pkg/db"
)

func main() {
	_ = godotenv.Load()

	dsn := mustEnv("DATABASE_URL")
	redisURL := mustEnv("REDIS_URL")
	jwtSecret := mustEnv("JWT_SECRET")
	listenAddr := getEnv("LISTEN_ADDR", ":8080")

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

	s := NewServer(pool, rdb, jwtSecret)
	r := chi.NewRouter()
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)

	// Auth routes — no JWT required
	r.Post("/auth/register", s.Register)
	r.Post("/auth/login", s.Login)

	// Protected routes
	r.Group(func(r chi.Router) {
		r.Use(s.RequireAuth)

		r.Get("/signals", s.ListSignals)
		r.Post("/signals", s.CreateSignal)
		r.Get("/signals/{id}", s.GetSignal)
		r.Put("/signals/{id}", s.UpdateSignal)
		r.Delete("/signals/{id}", s.DeleteSignal)

		r.Post("/signals/{id}/backtest", s.SubmitBacktest)
		r.Post("/signals/{id}/optimize", s.SubmitOptimize)
		r.Get("/signals/{id}/backtest-results", s.GetBacktestResults)
		r.Get("/signals/{id}/optimization-results", s.GetOptimizationResults)

		r.Get("/webhooks", s.ListWebhooks)
		r.Post("/webhooks", s.CreateWebhook)
		r.Get("/webhooks/{id}", s.GetWebhook)
		r.Put("/webhooks/{id}", s.UpdateWebhook)
		r.Delete("/webhooks/{id}", s.DeleteWebhook)
	})

	// WebSocket — auth via ?token= query param
	r.Get("/ws/jobs/{id}/progress", s.JobProgress)

	srv := &http.Server{Addr: listenAddr, Handler: r}

	go func() {
		<-ctx.Done()
		shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		srv.Shutdown(shutCtx)
	}()

	log.Printf("api-gateway: listening on %s", listenAddr)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatalf("listen: %v", err)
	}
	log.Println("api-gateway: stopped")
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
