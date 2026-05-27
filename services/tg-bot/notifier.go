// services/tg-bot/notifier.go
package main

import (
	"context"
	"encoding/json"
	"log"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/redis/go-redis/v9"
)

const tgNotifyChannel = "tg:notify"

// TgNotifyMsg mirrors the struct published by api-gateway.
type TgNotifyMsg struct {
	ChatID       int64  `json:"chat_id"`
	Text         string `json:"text"`
	StrategyID   string `json:"strategy_id,omitempty"`
	ShowPauseBtn bool   `json:"show_pause_btn,omitempty"`
}

// startNotifier subscribes to Redis channel tg:notify and sends Telegram messages.
// Runs until ctx is cancelled.
func startNotifier(ctx context.Context, bot *tgbotapi.BotAPI, rdb *redis.Client) {
	sub := rdb.Subscribe(ctx, tgNotifyChannel)
	defer sub.Close()

	ch := sub.Channel()
	log.Printf("notifier: subscribed to %s", tgNotifyChannel)

	for {
		select {
		case <-ctx.Done():
			return
		case redisMsg, ok := <-ch:
			if !ok {
				return
			}
			var msg TgNotifyMsg
			if err := json.Unmarshal([]byte(redisMsg.Payload), &msg); err != nil {
				log.Printf("notifier: invalid message: %v", err)
				continue
			}
			sendNotification(bot, msg)
		}
	}
}

func sendNotification(bot *tgbotapi.BotAPI, msg TgNotifyMsg) {
	m := tgbotapi.NewMessage(msg.ChatID, msg.Text)
	m.ParseMode = "Markdown"

	if msg.ShowPauseBtn && msg.StrategyID != "" {
		m.ReplyMarkup = tgbotapi.NewInlineKeyboardMarkup(
			tgbotapi.NewInlineKeyboardRow(
				tgbotapi.NewInlineKeyboardButtonData("⏸ Остановить стратегию", "pause_"+msg.StrategyID),
			),
		)
	}

	if _, err := bot.Send(m); err != nil {
		log.Printf("notifier: send to %d failed: %v", msg.ChatID, err)
	}
}
