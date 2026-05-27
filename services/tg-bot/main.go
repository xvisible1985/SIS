// services/tg-bot/main.go
package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/joho/godotenv"
	"github.com/redis/go-redis/v9"
)

// Config holds all configuration for the bot.
type Config struct {
	BotToken       string
	BotSecret      string
	GatewayURL     string
	RedisURL       string
	AppURL         string
	GroupID        int64
	WelcomeEnabled bool
}

func main() {
	_ = godotenv.Load()

	cfg := Config{
		BotToken:   mustEnv("TELEGRAM_BOT_TOKEN"),
		BotSecret:  mustEnv("TELEGRAM_BOT_SECRET"),
		GatewayURL: getEnv("GATEWAY_URL", "http://localhost:8080"),
		RedisURL:   getEnv("REDIS_URL", "redis://localhost:6379"),
		AppURL:     getEnv("APP_URL", "https://app.novabot.io"),
	}

	if gidStr := os.Getenv("TELEGRAM_GROUP_ID"); gidStr != "" {
		cfg.GroupID, _ = strconv.ParseInt(gidStr, 10, 64)
	}
	cfg.WelcomeEnabled = os.Getenv("WELCOME_ENABLED") == "true"
	if !cfg.WelcomeEnabled {
		cfg.GroupID = 0
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// Telegram bot
	bot, err := tgbotapi.NewBotAPI(cfg.BotToken)
	if err != nil {
		log.Fatalf("bot init: %v", err)
	}
	log.Printf("tg-bot: authorized as @%s", bot.Self.UserName)

	// Redis
	opt, err := redis.ParseURL(cfg.RedisURL)
	if err != nil {
		log.Fatalf("redis parse url: %v", err)
	}
	rdb := redis.NewClient(opt)
	defer rdb.Close()

	// Gateway client
	gw := NewGatewayClient(cfg.GatewayURL, cfg.BotSecret)

	// Start notification subscriber
	go startNotifier(ctx, bot, rdb)

	// Long polling loop
	u := tgbotapi.NewUpdate(0)
	u.Timeout = 60
	updates := bot.GetUpdatesChan(u)

	log.Printf("tg-bot: polling started")
	for {
		select {
		case <-ctx.Done():
			log.Println("tg-bot: shutting down")
			bot.StopReceivingUpdates()
			time.Sleep(500 * time.Millisecond)
			return
		case update := <-updates:
			go handleUpdate(ctx, bot, gw, update, cfg)
		}
	}
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
