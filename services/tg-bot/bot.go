// services/tg-bot/bot.go
package main

import (
	"context"
	"fmt"
	"log"
	"strings"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

// handleUpdate routes an incoming Telegram update to the correct handler.
func handleUpdate(ctx context.Context, bot *tgbotapi.BotAPI, gw *GatewayClient, update tgbotapi.Update, cfg Config) {
	// Inline button callbacks (e.g. pause button from notifications)
	if update.CallbackQuery != nil {
		handleCallback(ctx, bot, gw, update.CallbackQuery)
		return
	}

	msg := update.Message
	if msg == nil {
		return
	}

	// New member joined configured group — send welcome
	if cfg.GroupID != 0 && msg.Chat.ID == cfg.GroupID && msg.NewChatMembers != nil {
		for _, member := range msg.NewChatMembers {
			if member.IsBot {
				continue
			}
			var mention string
			if member.UserName != "" {
				mention = "@" + member.UserName
			} else {
				mention = member.FirstName
			}
			text := fmt.Sprintf(
				"👋 Добро пожаловать, %s!\n\n*Novabot* — платформа автоматической торговли на Bybit.\n\n🚀 Зарегистрироваться: %s/register\n🔐 Уже есть аккаунт? /login\n📊 Привязать Telegram: /start",
				mention, cfg.AppURL,
			)
			m := tgbotapi.NewMessage(msg.Chat.ID, text)
			m.ParseMode = "Markdown"
			bot.Send(m)
		}
		return
	}

	if !msg.IsCommand() {
		return
	}

	cmd := msg.Command()
	log.Printf("cmd: /%s from chat_id=%d", cmd, msg.Chat.ID)

	switch cmd {
	case "start":
		cmdStart(ctx, bot, gw, msg, cfg.AppURL)
	case "login":
		cmdLogin(ctx, bot, gw, msg, cfg.AppURL)
	case "status":
		cmdStatus(ctx, bot, gw, msg)
	case "pnl":
		cmdPnl(ctx, bot, gw, msg)
	case "positions":
		cmdPositions(ctx, bot, gw, msg)
	case "pause":
		cmdPause(ctx, bot, gw, msg)
	case "resume":
		cmdResume(ctx, bot, gw, msg)
	case "notifications":
		cmdNotifications(ctx, bot, gw, msg, cfg.AppURL)
	case "mute":
		cmdMute(ctx, bot, gw, msg)
	default:
		reply(bot, msg.Chat.ID, "Неизвестная команда. Попробуйте /status, /login, /pnl")
	}
}

// handleCallback handles inline keyboard button presses.
func handleCallback(ctx context.Context, bot *tgbotapi.BotAPI, gw *GatewayClient, cb *tgbotapi.CallbackQuery) {
	// Acknowledge the callback immediately
	bot.Request(tgbotapi.NewCallback(cb.ID, ""))

	// cb.Message is nil for inline-mode callbacks
	if cb.Message == nil {
		return
	}
	chatID := cb.Message.Chat.ID
	data := cb.Data

	switch {
	case data == "cmd_login":
		// /login triggered from start keyboard
		fakemsg := &tgbotapi.Message{
			From: cb.From,
			Chat: cb.Message.Chat,
		}
		cmdLogin(ctx, bot, gw, fakemsg, "")
	case strings.HasPrefix(data, "pause_"):
		strategyID := strings.TrimPrefix(data, "pause_")
		if err := gw.StrategyStatus(ctx, chatID, strategyID, "stopped"); err != nil {
			reply(bot, chatID, "❌ Не удалось остановить стратегию.")
			return
		}
		reply(bot, chatID, "⏸ Стратегия остановлена.")
	}
}
