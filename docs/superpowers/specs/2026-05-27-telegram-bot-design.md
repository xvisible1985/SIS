# Telegram Bot — Design Spec
**Date:** 2026-05-27

## Overview

Добавить Telegram-бот как отдельный микросервис, реализующий два направления:

1. **TG → Сайт**: пользователь пишет `/login` в бот, получает magic link, кликает — и попадает на сайт авторизованным (включая авторегистрацию по chat_id).
2. **Сайт → TG**: пользователь регистрируется на сайте, затем привязывает Telegram в настройках профиля — получает уведомления о сделках, сигналах, балансе.

Оба потока обслуживаются одним сервисом `services/tg-bot/`.

---

## Существующая инфраструктура (используем без изменений)

| Что | Где |
|---|---|
| `telegram_connections` | таблица: user_id ↔ chat_id/username |
| `telegram_pending_tokens` | одноразовые deep-link токены для привязки |
| `GET /account/telegram-link` | генерирует URL для привязки |
| `POST /account/telegram-verify` | бот вызывает после `/start TOKEN` |
| `DELETE /account/telegram` | отвязать Telegram |
| `GET/PATCH /account/notifications` | настройки уведомлений |
| `AccountPage.tsx` — блок Telegram | UI привязки и настроек уже готов |

---

## Архитектура

```
Telegram API
    ↕  (long polling)
services/tg-bot/
    ↕  (HTTP, BOT_SECRET)      ↕  (Redis pub/sub: tg:notify)
services/api-gateway  ────────────────────────────────────
    ↕
PostgreSQL
```

- **tg-bot → api-gateway**: команды `/login`, `/pause`, `/resume`, `/status` и т.д.
- **api-gateway → tg-bot**: уведомления через Redis-канал `tg:notify`
- **tg-bot → Telegram**: отправка сообщений и inline-кнопок

---

## Новый сервис `services/tg-bot/`

```
services/tg-bot/
├── main.go        — запуск, env, long polling + Redis subscriber
├── bot.go         — роутер входящих сообщений и callback_query
├── commands.go    — обработчики всех команд
└── notifier.go    — Redis subscriber → Telegram messages
```

### Зависимости
- `github.com/go-telegram-bot-api/telegram-bot-api/v5` — Telegram Bot API
- `github.com/redis/go-redis/v9` — уже в проекте
- `github.com/joho/godotenv` — уже в проекте

### Env-переменные

```
TELEGRAM_BOT_TOKEN=...          # токен от @BotFather
TELEGRAM_BOT_SECRET=...         # общий секрет бот↔gateway
GATEWAY_URL=http://api-gateway:8080
REDIS_URL=redis://redis:6379
APP_URL=https://app.novabot.io
TELEGRAM_GROUP_ID=-100123456789 # ID группы/канала для welcome (опционально)
WELCOME_ENABLED=true
```

---

## Команды бота

| Команда | Поведение |
|---|---|
| `/start [token]` | Без токена — приветствие + кнопки. С токеном — вызов `/account/telegram-verify`, привязка аккаунта |
| `/login` | Запрос magic link → кнопка «Войти в приложение» |
| `/status` | Сводка по активным стратегиям: символ · LIVE/PAUSED · P&L · объём |
| `/positions` | Открытые позиции с unrealized P&L |
| `/pnl` | Суммарный P&L за сегодня и неделю |
| `/pause` | Остановить все активные стратегии (`stopped`) |
| `/resume` | Запустить все остановленные стратегии (`active`) |
| `/notifications` | Inline-кнопки вкл/выкл: 🔔 Сделки · 📈 Сигналы · 💰 Баланс |
| `/mute 2h` | Заглушить уведомления на N минут/часов (записывает `mute_until`) |

Для незарегистрированных пользователей (нет в `telegram_connections`) все команды кроме `/start` и `/login` отвечают: _«Сначала привяжите аккаунт: /start»_.

---

## Уведомления

### Триггеры в api-gateway

При наступлении события api-gateway публикует сообщение в Redis-канал `tg:notify`:

| Событие | Поле `on_*` | Условие |
|---|---|---|
| Цикл закрыт (TP или SL) | `on_trade` | strategy event с ключевыми словами цикла |
| Ошибка стратегии | `on_trade` | event.level = `error` |
| Сигнал сработал | `on_signal` | signal_state изменился на buy/sell |
| Баланс изменился | `on_balance` | при пополнении/списании |

### Формат Redis-сообщения

```json
{
  "chat_id": 123456789,
  "type": "trade|signal|balance|error",
  "text": "BTCUSDT ✅ Цикл закрыт +2.4%",
  "strategy_id": "uuid-optional",
  "show_pause_btn": true
}
```

### Поведение notifier

1. Проверяет `mute_until` из `telegram_connections` — если активен, пропускает
2. Проверяет `telegram_notification_settings` для данного `user_id`
3. Если `show_pause_btn = true` — добавляет inline-кнопку «⏸ Остановить стратегию»
4. Отправляет сообщение в Telegram

---

## Приветствие новых участников группы

Бот должен быть **администратором** группы/супергруппы.

При событии `new_chat_members`:
- Бот отправляет приветственное сообщение в группу с упоминанием `@username`
- Содержание:

```
👋 Добро пожаловать, @username!

Novabot — платформа автоматической торговли на Bybit.

🚀 Зарегистрироваться: {APP_URL}/register
🔐 Уже есть аккаунт? Напиши /login
📊 Подключить Telegram к аккаунту: /start
```

- Если `WELCOME_ENABLED=false` или `TELEGRAM_GROUP_ID` не задан — функция отключена
- Бот обрабатывает только события из группы с ID равным `TELEGRAM_GROUP_ID`

---

## Новые эндпоинты api-gateway

### `POST /auth/telegram` — внутренний (BOT_SECRET)

Вызывается ботом при команде `/login`.

**Заголовок:** `Authorization: Bearer {TELEGRAM_BOT_SECRET}`

**Запрос:**
```json
{ "chat_id": 123456789, "username": "johndoe" }
```

**Логика:**
1. Ищет `chat_id` в `telegram_connections`
2. Если найден → берёт `user_id`
3. Если не найден → **авторегистрация**: создаёт пользователя (`tg_{chatID}@telegram.invalid`, `password_hash = ''`), вставляет в `telegram_connections`
4. Создаёт запись в `telegram_auth_tokens`
5. Возвращает URL

**Ответ:**
```json
{ "url": "https://app.novabot.io/login?tg=TOKEN", "is_new": true }
```

---

### `POST /auth/telegram-callback` — публичный

Вызывается фронтендом когда пользователь открывает magic link.

**Запрос:**
```json
{ "token": "uuid-xxx" }
```

**Логика:**
1. Находит токен в `telegram_auth_tokens` (проверяет `expires_at`)
2. Удаляет токен (одноразовый)
3. Находит `user_id` по `chat_id` из токена
4. Генерирует JWT

**Ответ:**
```json
{ "token": "eyJ...", "user_id": "...", "email": "...", "is_admin": false }
```

---

### `POST /strategies/pause-all` — внутренний (BOT_SECRET)

Устанавливает `status = 'stopped'` для всех активных стратегий пользователя.

**Тело:** `{ "chat_id": 123456789 }`

---

### `POST /strategies/resume-all` — внутренний (BOT_SECRET)

Устанавливает `status = 'active'` для всех остановленных стратегий пользователя.

**Тело:** `{ "chat_id": 123456789 }`

---

## Новая миграция `047_telegram_auth.sql`

```sql
-- One-time tokens for magic link auth (TG → web)
CREATE TABLE IF NOT EXISTS telegram_auth_tokens (
    token      TEXT PRIMARY KEY,
    chat_id    BIGINT NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL DEFAULT NOW() + INTERVAL '5 minutes'
);

CREATE INDEX IF NOT EXISTS telegram_auth_tokens_chat
    ON telegram_auth_tokens (chat_id);

-- Mute notifications until a specific time
ALTER TABLE telegram_connections
    ADD COLUMN IF NOT EXISTS mute_until TIMESTAMPTZ;
```

---

## Изменения фронтенда

### `LoginPage.tsx`

При монтировании компонента проверяет `?tg=TOKEN` в URL:
1. Если токен есть — вызывает `POST /auth/telegram-callback`
2. Сохраняет JWT, перенаправляет на `/`
3. Показывает состояние «Входим через Telegram...» пока идёт запрос

### `api/auth.ts`

Добавить функцию:
```ts
export async function telegramCallback(token: string): Promise<AuthResponse> {
  const res = await apiClient.post<AuthResponse>('/auth/telegram-callback', { token })
  return res.data
}
```

---

## docker-compose.yml

```yaml
tg-bot:
  build:
    context: .
    dockerfile: services/tg-bot/Dockerfile
  restart: unless-stopped
  environment:
    TELEGRAM_BOT_TOKEN: ${TELEGRAM_BOT_TOKEN}
    TELEGRAM_BOT_SECRET: ${TELEGRAM_BOT_SECRET}
    GATEWAY_URL: http://api-gateway:8080
    REDIS_URL: redis://redis:6379
    APP_URL: ${APP_URL}
    TELEGRAM_GROUP_ID: ${TELEGRAM_GROUP_ID:-}
    WELCOME_ENABLED: ${WELCOME_ENABLED:-false}
  depends_on:
    - redis
    - api-gateway
```

---

## Безопасность

- Все bot-to-gateway вызовы защищены заголовком `Authorization: Bearer {TELEGRAM_BOT_SECRET}`
- Токены `telegram_auth_tokens` одноразовые, TTL 5 минут
- Авторегистрированные пользователи (`tg_*@telegram.invalid`) не могут войти через форму email/password (нет пароля) — только через Telegram
- `/mute` принимает только значения от 5 минут до 24 часов

---

## Что НЕ входит в эту задачу

- Создание и редактирование стратегий через бота
- Административные команды через бота
- Telegram Login Widget на сайте
- Telegram Web App (TWA)
