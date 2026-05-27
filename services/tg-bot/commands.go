// services/tg-bot/commands.go
package main

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

// cmdStart handles /start [token].
// With a token: calls /account/telegram-verify to link the account.
// Without a token: sends welcome message with inline buttons.
func cmdStart(ctx context.Context, bot *tgbotapi.BotAPI, gw *GatewayClient, msg *tgbotapi.Message, appURL string) {
	token := strings.TrimSpace(msg.CommandArguments())
	chatID := msg.Chat.ID
	var username string
	if msg.From != nil {
		username = msg.From.UserName
	}

	if token != "" {
		// Link existing account via the account/telegram-verify endpoint
		err := gw.do(ctx, "POST", "/account/telegram-verify", map[string]any{
			"token":    token,
			"chat_id":  chatID,
			"username": username,
		}, nil)
		if err != nil {
			reply(bot, chatID, "❌ Ссылка недействительна или истекла. Получите новую в настройках профиля.")
			return
		}
		reply(bot, chatID, "✅ Telegram успешно привязан к вашему аккаунту!\n\nТеперь вы будете получать уведомления о сделках и сигналах.")
		return
	}

	kb := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonURL("🚀 Зарегистрироваться", appURL+"/register"),
			tgbotapi.NewInlineKeyboardButtonData("🔐 Войти", "cmd_login"),
		),
	)
	m := tgbotapi.NewMessage(chatID, "👋 Добро пожаловать в *Novabot*!\n\nАвтоматическая торговля на Bybit.\n\nДля начала войдите в аккаунт или создайте новый.")
	m.ParseMode = "Markdown"
	m.ReplyMarkup = kb
	bot.Send(m)
}

// cmdLogin handles /login — sends a magic-link button.
func cmdLogin(ctx context.Context, bot *tgbotapi.BotAPI, gw *GatewayClient, msg *tgbotapi.Message, appURL string) {
	chatID := msg.Chat.ID
	var username string
	if msg.From != nil {
		username = msg.From.UserName
	}

	loginURL, err := gw.TelegramLoginRequest(ctx, chatID, username)
	if err != nil {
		reply(bot, chatID, "❌ Не удалось создать ссылку для входа. Попробуйте позже.")
		return
	}

	kb := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonURL("🔐 Открыть приложение", loginURL),
		),
	)
	m := tgbotapi.NewMessage(chatID, "Нажмите кнопку ниже — вы будете автоматически авторизованы.\n\n_Ссылка действительна 5 минут._")
	m.ParseMode = "Markdown"
	m.ReplyMarkup = kb
	bot.Send(m)
}

// cmdStatus handles /status — shows active strategies summary.
func cmdStatus(ctx context.Context, bot *tgbotapi.BotAPI, gw *GatewayClient, msg *tgbotapi.Message) {
	chatID := msg.Chat.ID
	summary, err := gw.BotSummary(ctx, chatID)
	if err != nil {
		replyNotLinked(bot, chatID)
		return
	}

	if len(summary.Strategies) == 0 {
		reply(bot, chatID, "📊 У вас нет стратегий.")
		return
	}

	var sb strings.Builder
	sb.WriteString("📊 *Стратегии*\n\n")
	statusIcons := map[string]string{"active": "🟢", "finishing": "🟡", "stopped": "⏸"}
	for _, st := range summary.Strategies {
		icon := statusIcons[st.Status]
		if icon == "" {
			icon = "⚪"
		}
		sb.WriteString(fmt.Sprintf("%s *%s* %s — %d/%d уровней\n",
			icon, st.Symbol, st.Direction, st.ActiveLevels, st.GridLevels))
	}
	m := tgbotapi.NewMessage(chatID, sb.String())
	m.ParseMode = "Markdown"
	bot.Send(m)
}

// cmdPnl handles /pnl — shows P&L summary.
func cmdPnl(ctx context.Context, bot *tgbotapi.BotAPI, gw *GatewayClient, msg *tgbotapi.Message) {
	chatID := msg.Chat.ID
	summary, err := gw.BotSummary(ctx, chatID)
	if err != nil {
		replyNotLinked(bot, chatID)
		return
	}

	pnlTodaySign := "+"
	if summary.PnlToday < 0 {
		pnlTodaySign = ""
	}
	pnlWeekSign := "+"
	if summary.PnlWeek < 0 {
		pnlWeekSign = ""
	}

	text := fmt.Sprintf("💰 *P&L*\n\nСегодня: `%s%.2f$`\nЗа неделю: `%s%.2f$`",
		pnlTodaySign, summary.PnlToday, pnlWeekSign, summary.PnlWeek)
	m := tgbotapi.NewMessage(chatID, text)
	m.ParseMode = "Markdown"
	bot.Send(m)
}

// cmdPositions handles /positions — shows open positions from strategies.
func cmdPositions(ctx context.Context, bot *tgbotapi.BotAPI, gw *GatewayClient, msg *tgbotapi.Message) {
	chatID := msg.Chat.ID
	summary, err := gw.BotSummary(ctx, chatID)
	if err != nil {
		replyNotLinked(bot, chatID)
		return
	}

	var active []StrategySummary
	for _, st := range summary.Strategies {
		if st.ActiveLevels > 0 {
			active = append(active, st)
		}
	}
	if len(active) == 0 {
		reply(bot, chatID, "📈 Нет открытых позиций.")
		return
	}

	var sb strings.Builder
	sb.WriteString("📈 *Открытые позиции*\n\n")
	for _, st := range active {
		sb.WriteString(fmt.Sprintf("• *%s* %s — %d уровней заполнено\n",
			st.Symbol, st.Direction, st.ActiveLevels))
	}
	m := tgbotapi.NewMessage(chatID, sb.String())
	m.ParseMode = "Markdown"
	bot.Send(m)
}

// cmdPause handles /pause — stops all active strategies.
func cmdPause(ctx context.Context, bot *tgbotapi.BotAPI, gw *GatewayClient, msg *tgbotapi.Message) {
	chatID := msg.Chat.ID
	n, err := gw.PauseAll(ctx, chatID)
	if err != nil {
		replyNotLinked(bot, chatID)
		return
	}
	if n == 0 {
		reply(bot, chatID, "⏸ Нет активных стратегий для остановки.")
		return
	}
	reply(bot, chatID, fmt.Sprintf("⏸ Остановлено стратегий: *%d*", n))
}

// cmdResume handles /resume — activates all stopped strategies.
func cmdResume(ctx context.Context, bot *tgbotapi.BotAPI, gw *GatewayClient, msg *tgbotapi.Message) {
	chatID := msg.Chat.ID
	n, err := gw.ResumeAll(ctx, chatID)
	if err != nil {
		replyNotLinked(bot, chatID)
		return
	}
	if n == 0 {
		reply(bot, chatID, "🟢 Нет остановленных стратегий.")
		return
	}
	reply(bot, chatID, fmt.Sprintf("🟢 Запущено стратегий: *%d*", n))
}

// cmdNotifications handles /notifications — links to notification settings.
func cmdNotifications(ctx context.Context, bot *tgbotapi.BotAPI, gw *GatewayClient, msg *tgbotapi.Message, appURL string) {
	chatID := msg.Chat.ID
	text := fmt.Sprintf("🔔 *Уведомления*\n\nНастройте уведомления в профиле:\n%s/account", appURL)
	m := tgbotapi.NewMessage(chatID, text)
	m.ParseMode = "Markdown"
	m.ReplyMarkup = tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonURL("⚙️ Открыть настройки", appURL+"/account"),
		),
	)
	bot.Send(m)
}

// cmdMute handles /mute [duration] — mutes notifications.
// Example: /mute 2h, /mute 30m, /mute 24h
func cmdMute(ctx context.Context, bot *tgbotapi.BotAPI, gw *GatewayClient, msg *tgbotapi.Message) {
	chatID := msg.Chat.ID
	arg := strings.TrimSpace(msg.CommandArguments())
	if arg == "" {
		reply(bot, chatID, "Использование: /mute 30m | 2h | 24h")
		return
	}
	d, err := time.ParseDuration(arg)
	if err != nil || d < 5*time.Minute || d > 24*time.Hour {
		reply(bot, chatID, "❌ Неверный формат. Примеры: /mute 30m, /mute 2h, /mute 24h\n\nМинимум 5 минут, максимум 24 часа.")
		return
	}
	until := time.Now().Add(d)
	if err := gw.MuteUntil(ctx, chatID, until.UTC().Format(time.RFC3339)); err != nil {
		replyNotLinked(bot, chatID)
		return
	}
	reply(bot, chatID, fmt.Sprintf("🔕 Уведомления заглушены до %s", until.Format("15:04 02.01")))
}

// reply sends a plain Markdown message.
func reply(bot *tgbotapi.BotAPI, chatID int64, text string) {
	m := tgbotapi.NewMessage(chatID, text)
	m.ParseMode = "Markdown"
	if _, err := bot.Send(m); err != nil {
		log.Printf("reply to %d failed: %v", chatID, err)
	}
}

func replyNotLinked(bot *tgbotapi.BotAPI, chatID int64) {
	reply(bot, chatID, "⚠️ Ваш Telegram не привязан к аккаунту.\n\nИспользуйте /start или привяжите в настройках профиля на сайте.")
}
