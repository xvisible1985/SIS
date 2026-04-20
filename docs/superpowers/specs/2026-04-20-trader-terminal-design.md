# Trader Service + Trading Terminal — Design Spec
**Date:** 2026-04-20
**Status:** Approved

---

## Overview

Расширение SaaS-платформы `sis` возможностью прямого исполнения ордеров через Bybit. Включает:
- **Trader Service** — модуль внутри `api-gateway` для размещения/отмены ордеров, управления позициями, фоновой синхронизации истории
- **Trading Terminal** — новая страница React-фронтенда с графиком, стаканом, формой ордеров, таблицами позиций/ордеров/истории

Биржа MVP: **Bybit** (linear + inverse + spot). Архитектура готова к добавлению Binance.

---

## Architecture

### Принцип

Trader — тонкий прокси к Bybit Private API внутри существующего `api-gateway`. Никакого нового сервиса. Общий код — новые пакеты `pkg/trader/` и `pkg/crypto/`.

```
Browser
  ├── GET  /ws/trader/positions?token=JWT  ← приватный WS (позиции + ордера real-time)
  ├── POST /api/v1/trader/order            ← разместить ордер
  ├── DEL  /api/v1/trader/order            ← отменить ордер
  ├── POST /api/v1/trader/leverage         ← установить плечо
  ├── GET  /api/v1/trader/orders           ← история ордеров (из нашей БД)
  ├── GET  /api/v1/trader/executions       ← сделки + фандинг + комиссии
  ├── GET  /api/v1/trader/stats            ← агрегированная статистика
  ├── POST /api/v1/accounts                ← добавить exchange account
  ├── GET  /api/v1/accounts                ← список аккаунтов (без ключей)
  ├── DEL  /api/v1/accounts/:id            ← удалить аккаунт
  └── GET  /api/v1/accounts/:id/verify     ← проверить ключи

api-gateway
  ├── pkg/trader/bybit.go     — sign(), timestamp(), REST-вызовы к Bybit
  ├── pkg/trader/ws.go        — PositionStream (прокси private WS per client)
  ├── pkg/trader/syncer.go    — фоновый синхронизатор истории
  └── pkg/crypto/aes.go       — AES-256-GCM шифрование API-ключей

PostgreSQL
  ├── exchange_accounts        — API-ключи (зашифрованы)
  ├── trader_orders            — наши ордера (orderLinkId = "sis_...")
  └── trader_executions        — сделки, фандинг, комиссии

Bybit Public WS  ← Browser напрямую (свечи, стакан) — без прокси
Bybit Private WS ← api-gateway проксирует per-client connection
```

---

## Database

### Миграция `005_exchange_accounts.sql`

```sql
-- API-ключи пользователей
CREATE TABLE exchange_accounts (
    id          UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    owner_id    UUID        NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    exchange    TEXT        NOT NULL CHECK (exchange IN ('bybit', 'binance')),
    label       TEXT        NOT NULL DEFAULT '',
    api_key_enc TEXT        NOT NULL,   -- AES-256-GCM, base64(nonce+ciphertext)
    secret_enc  TEXT        NOT NULL,
    is_active   BOOLEAN     NOT NULL DEFAULT TRUE,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (owner_id, exchange, label)
);
CREATE INDEX exchange_accounts_owner ON exchange_accounts (owner_id);

-- Ордера, размещённые через нашу платформу
CREATE TABLE trader_orders (
    id             UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    owner_id       UUID        NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    account_id     UUID        NOT NULL REFERENCES exchange_accounts(id) ON DELETE CASCADE,
    order_link_id  TEXT        NOT NULL UNIQUE,  -- "sis_{accountID8}_{unixMs}"
    order_id       TEXT,                          -- ID биржи (после подтверждения)
    exchange       TEXT        NOT NULL,
    symbol         TEXT        NOT NULL,
    category       TEXT        NOT NULL,          -- linear | inverse | spot
    side           TEXT        NOT NULL,          -- Buy | Sell
    order_type     TEXT        NOT NULL,          -- Limit | Market | Conditional
    qty            NUMERIC     NOT NULL,
    price          NUMERIC,
    trigger_price  NUMERIC,
    status         TEXT        NOT NULL DEFAULT 'New',
    cum_exec_qty   NUMERIC     NOT NULL DEFAULT 0,
    cum_exec_value NUMERIC     NOT NULL DEFAULT 0,
    cum_exec_fee   NUMERIC     NOT NULL DEFAULT 0,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX trader_orders_owner   ON trader_orders (owner_id, created_at DESC);
CREATE INDEX trader_orders_account ON trader_orders (account_id, status);
CREATE INDEX trader_orders_link    ON trader_orders (order_link_id);

-- Сделки, фандинг, комиссии (transaction log)
CREATE TABLE trader_executions (
    id            UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    owner_id      UUID        NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    account_id    UUID        NOT NULL REFERENCES exchange_accounts(id) ON DELETE CASCADE,
    exec_id       TEXT        NOT NULL,
    order_id      TEXT,
    order_link_id TEXT,                    -- ссылка на trader_orders если "sis_" префикс
    exchange      TEXT        NOT NULL,
    symbol        TEXT        NOT NULL,
    category      TEXT        NOT NULL,
    side          TEXT,
    exec_type     TEXT        NOT NULL,    -- Trade | Funding | Fee | Settlement
    qty           NUMERIC,
    price         NUMERIC,
    exec_value    NUMERIC,
    exec_fee      NUMERIC,
    fee_rate      NUMERIC,
    is_maker      BOOLEAN,
    exec_time     TIMESTAMPTZ NOT NULL,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (account_id, exec_id)
);
CREATE INDEX trader_executions_owner   ON trader_executions (owner_id, exec_time DESC);
CREATE INDEX trader_executions_account ON trader_executions (account_id, exec_type, exec_time DESC);
CREATE INDEX trader_executions_link    ON trader_executions (order_link_id) WHERE order_link_id IS NOT NULL;
```

---

## Backend Packages

### `pkg/crypto/aes.go`

AES-256-GCM шифрование для API-ключей. Ключ из переменной окружения `ENCRYPTION_KEY` (32 байта, hex).

```go
func Encrypt(plaintext string) (string, error)  // → base64(nonce || ciphertext)
func Decrypt(ciphertext string) (string, error)  // base64 → plaintext
```

Используется только при записи/чтении `exchange_accounts`. Plaintext-ключи нигде не логируются.

### `pkg/trader/bybit.go`

Адаптация логики `test1/server/utils/bybit.ts` на Go.

- Кэш offset серверного времени (HTTP к `/v5/market/time`, обновляется раз в 5 мин)
- `Sign(timestamp, apiKey, secret, recvWindow, payload) string` — HMAC-SHA256
- `PlaceOrder(ctx, creds, req OrderRequest) (OrderResponse, error)`
- `CancelOrder(ctx, creds, req CancelRequest) error`
- `SetLeverage(ctx, creds, symbol, category, leverage string) error`
- `FetchPositions(ctx, creds) ([]Position, error)` — parallel: linear USDT + USDC + inverse
- `FetchOpenOrders(ctx, creds) ([]Order, error)`
- `FetchOrderHistory(ctx, creds, from time.Time, cursor string) ([]Order, string, error)`
- `FetchExecutions(ctx, creds, from time.Time, cursor string) ([]Execution, string, error)`

### `pkg/trader/ws.go`

`PositionStream` — управляет одним приватным WS-соединением к Bybit на время жизни клиентского WS.

```
Клиент подключается (JWT проверен)
  → читаем account из БД → decrypt keys
  → открываем wss://stream.bybit.com/v5/private
  → auth: op=auth args=[apiKey, expires, hmac]
  → subscribe: position, order
  → fetchInitialSnapshot() → REST parallel fetch → шлём snapshot клиенту
  → ретранслируем WS-дельты клиенту
  → keepalive ping каждые 20 сек
  → при закрытии клиента → закрываем Bybit WS
```

Протокол сообщений (идентичен `test1`):
```json
{ "type": "account",  "accountName": "..." }
{ "type": "log",      "message": "...", "error": false }
{ "type": "position", "dataType": "snapshot|delta", "data": [...] }
{ "type": "order",    "dataType": "snapshot|delta", "data": [...] }
```

### `pkg/trader/syncer.go`

Фоновый синхронизатор. Запускается в `api-gateway` при старте.

```
Для каждого active exchange_account:
  Каждые 60 сек:
    → /v5/execution/list (cursor-based, от last_exec_time)
    → upsert в trader_executions
    → обновить статусы trader_orders по order_id / order_link_id

  Каждые 5 мин:
    → /v5/order/history (фильтр по orderLinkId prefix "sis_")
    → upsert в trader_orders (статус, cum_exec_qty, cum_exec_fee)

При старте (backfill):
  → История за последние 30 дней (конфигурируется TRADER_SYNC_DAYS=30)
```

Параллельный запуск аккаунтов через `errgroup`. Каждый аккаунт — отдельная горутина с `time.Ticker`. При добавлении нового аккаунта — горутина стартует сразу через канал.

---

## API Endpoints (api-gateway)

### Exchange Accounts

```
POST   /api/v1/accounts              — добавить аккаунт (шифрует ключи)
GET    /api/v1/accounts              — список (id, label, exchange, is_active — без ключей)
DELETE /api/v1/accounts/:id          — удалить
GET    /api/v1/accounts/:id/verify   — проверить ключи через Bybit /v5/user/query-api
```

### Trader Actions

```
POST /api/v1/trader/order
  body: { accountId, symbol, side, orderType, qty, price?, triggerPrice?,
          category, reduceOnly, positionIdx, timeInForce?, triggerBy?,
          triggerDirection?, conditionalOrderType?, orderFilter? }
  → генерируем orderLinkId = "sis_{accountID8}_{unixMs}"
  → сохраняем в trader_orders (status=New)
  → PlaceOrder к Bybit с orderLinkId
  → обновляем order_id в trader_orders
  → return { ok, orderId, orderLinkId }

DELETE /api/v1/trader/order
  body: { accountId, symbol, orderId, category, orderFilter }
  → CancelOrder к Bybit
  → обновляем status=Cancelled в trader_orders
  → return { ok }

POST /api/v1/trader/leverage
  body: { accountId, symbol, category, leverage }
  → SetLeverage к Bybit → return { ok }
```

### History & Stats

```
GET /api/v1/trader/orders
  query: accountId, status=open|closed|all, symbol?, page=1, limit=50
  → SELECT из trader_orders WHERE owner_id=$jwt_user
  → return { orders: [...], total, page }

GET /api/v1/trader/executions
  query: accountId, type=Trade|Funding|Fee|all, symbol?, from?, to?, page=1, limit=100
  → SELECT из trader_executions WHERE owner_id=$jwt_user
  → return { executions: [...], total, page }

GET /api/v1/trader/stats
  query: accountId, from?, to?
  → SELECT SUM(exec_fee), SUM(exec_fee WHERE type=Funding), COUNT(*) WHERE type=Trade
  → return { totalFee, totalFunding, realizedPnl, tradeCount }
```

### WebSocket

```
GET /ws/trader/positions?token=<JWT>&accountId=<UUID>
  → JWT валидируется из query param (браузер не поддерживает WS-заголовки)
  → accountId обязателен; проверяем что account.owner_id == jwt.user_id
  → запускает PositionStream для указанного аккаунта
```

---

## Frontend

### Маршрут

```tsx
// App.tsx — новый маршрут
<Route path="/terminal" element={<TerminalPage />} />
```

Ссылка в навигации рядом с существующими страницами.

### Файловая структура

```
frontend/src/
  pages/
    TerminalPage.tsx
  components/terminal/
    Chart.tsx           — lightweight-charts, свечи, entry price lines
    Orderbook.tsx       — стакан, Bybit public WS
    OrderForm.tsx       — Limit / Market / Conditional, leverage, reduce-only
    PositionsTable.tsx  — позиции + "Закрыть"
    OrdersTable.tsx     — активные ордера + "Снять"
    HistoryTable.tsx    — закрытые ордера + пагинация
    ExecutionsTable.tsx — сделки/фандинг/комиссии + фильтр по типу и датам
    TradeLog.tsx        — WS-лог событий (debug)
  hooks/terminal/
    usePositionsWs.ts   — WS /ws/trader/positions, snapshot+delta merge
    useOrderbook.ts     — Bybit public WS orderbook.50
    useCandles.ts       — Bybit public WS kline + REST история
    useTraderApi.ts     — place/cancel/leverage/history/executions/stats
```

### Лейаут

```
┌─────────────────────────────────────┬──────────────────┐
│                                     │   OrderForm      │
│           Chart (65%)               │   (Limit /       │
│     lightweight-charts              │    Market /      │
│     entry lines (позиции/ордера)    │    Conditional)  │
│                                     ├──────────────────┤
├─────────────────────────────────────┤                  │
│  Tabs: Позиции | Ордера | История   │   Orderbook      │
│         Сделки | Лог                │   (стакан)       │
│  [таблица]                          │                  │
└─────────────────────────────────────┴──────────────────┘
   65% ширины                            35% ширины
   65vh верх / 35vh низ
```

### Адаптация от `test1`

| Аспект | test1 (Nuxt) | sis (React) |
|---|---|---|
| WS позиций | `/_ws/positions` (no auth) | `/ws/trader/positions?token=JWT` |
| Order API | `/api/exchange/order` | `/api/v1/trader/order` |
| Cancel API | `/api/exchange/order-cancel` | `DELETE /api/v1/trader/order` |
| Leverage API | `/api/exchange/set-leverage` | `/api/v1/trader/leverage` |
| Аккаунт | один, хардкод в БД | JWT → owner_id → exchange_accounts |
| История ордеров | нет | `HistoryTable` + `/api/v1/trader/orders` |
| Сделки/фандинг | нет | `ExecutionsTable` + `/api/v1/trader/executions` |

### Таблица Positions

Колонки: Символ | Сторона (Long/Short badge) | Размер (USDT) | Цена входа | Mark Price | Unrealised PnL (%) | Плечо | [Закрыть]

Клик по строке — переключает чарт на этот символ и рисует entry price line.

### Таблица Orders (активные)

Колонки: Символ | Сторона | Тип | Цена / Триггер | Кол-во | Исполнено | Статус | Маркер "sis" | [Снять]

### Таблица History (закрытые)

Колонки: Дата | Символ | Сторона | Тип | Цена | Кол-во | Комиссия | Статус | Маркер "sis"

Пагинация: 50 строк / страница.

### Таблица Executions

Фильтры: тип (Trade / Funding / Fee / All), диапазон дат, символ.
Колонки: Время | Символ | Тип | Сторона | Цена | Кол-во | Значение | Комиссия | Мейкер?

### OrderForm

Поля: Limit / Market / Conditional (табы) → Buy/Sell → Цена → Кол-во / Объём USDT (синхронизированы) → TIF (GTC/IOC/FOK для Limit) → Leverage → Reduce Only → [Купить/Продать]

Для Conditional: триггер-цена, направление (↑/↓), тип триггера (Mark/Last/Index), тип ордера после триггера (Market/Limit).

Order log под кнопкой — пошаговый статус (pending → ok/error).

---

## Security

- API-ключи хранятся только в зашифрованном виде (AES-256-GCM)
- Plaintext ключи существуют только в памяти на время HTTP-запроса / WS-сессии
- JWT обязателен для всех `/api/v1/trader/*` и `/ws/trader/*` эндпоинтов
- `owner_id` из JWT — обязательная WHERE-условие во всех SQL-запросах (пользователь не может получить данные другого)
- `orderLinkId` генерируется сервером, не клиентом

---

## Configuration (новые env-переменные)

```
ENCRYPTION_KEY=<32 байта hex>    # обязательно, для шифрования API-ключей
TRADER_SYNC_INTERVAL=60          # секунды между синхронизациями executions
TRADER_SYNC_DAYS=30              # глубина первоначального backfill в днях
```
