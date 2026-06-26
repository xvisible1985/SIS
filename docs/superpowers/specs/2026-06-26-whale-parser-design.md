# Whale Parser ("Толстосум") Design Spec

## Goal

Создать отдельный микросервис `services/parser/`, который:
1. Переносит существующий скрапер листингов Bybit (из api-gateway) к себе
2. Мониторит on-chain активность крупных кошельков (китов) на сетях Ethereum (ERC-20) и Tron (TRC-20)
3. Определяет направление входа кита (BUY/SELL) по анализу его активности за последние 30 дней
4. Публикует состояние в PostgreSQL + Redis для использования в signal-engine
5. Предоставляет полное управление и статистику в AdminPage → раздел "Парсеры"

---

## Architecture

```
Etherscan API (ETH)   Tronscan API (Tron)   Bybit Announcements API
        \                   /                        |
         \                 /                         |
          v               v                          v
         services/parser/main.go
              |              \
              |               \
              v                v
        whale/tracker.go   bybit_news.go (migrated)
              |
        +-----+-----+
        |           |
        v           v
  PostgreSQL     Redis Hash
  (durable)    whale:state:{sym}
        |           |
        v           v
  api-gateway   signal-engine
  (history,UI)  (whale signal)
```

**Ключевые принципы:**
- Block-driven (chain) и candle-driven (klines) логика разделены в разные сервисы
- api-gateway не меняет публичный API — только перестаёт запускать `newsScraper` горутину
- Redis — кэш состояния (TTL 2ч); PostgreSQL — источник правды
- При недоступности Redis signal-engine фоллбэчит на DB

---

## New Service: `services/parser/`

```
services/parser/
├── main.go           — старт, DB + Redis подключения, запуск горутин
├── bybit_news.go     — импортирует pkg/bybitnews и запускает Scraper.Start()
└── whale/
    ├── tracker.go    — главный polling loop (30s интервал)
    ├── eth.go        — Etherscan API client
    ├── tron.go       — Tronscan API client
    ├── discovery.go  — авто-обнаружение топ-N китов еженедельно
    └── direction.go  — алгоритм накопление/раздача → BUY/SELL/Neutral
```

### main.go

- Читает конфиг: `DATABASE_URL`, `REDIS_URL`, `ETHERSCAN_API_KEY`, `TRONSCAN_API_KEY`, `WHALE_THRESHOLD_USDT` (default 50000)
- Запускает горутины: `bybit_news.Run()`, `whale.Tracker.Start()`, `whale.Discovery.Start()`
- Heartbeat в Redis (как остальные сервисы) с именем `parser`

---

## Database Schema

### Новые таблицы (новая миграция)

```sql
-- Список отслеживаемых кошельков
CREATE TABLE whale_addresses (
  id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  address      TEXT NOT NULL,
  chain        TEXT NOT NULL CHECK (chain IN ('eth', 'tron')),
  label        TEXT,
  is_manual    BOOLEAN NOT NULL DEFAULT false,
  volume_30d   FLOAT8 NOT NULL DEFAULT 0,
  is_active    BOOLEAN NOT NULL DEFAULT true,
  last_seen_at TIMESTAMPTZ,
  created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (address, chain)
);

-- История китовых событий
CREATE TABLE whale_events (
  id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  address      TEXT NOT NULL,
  chain        TEXT NOT NULL,
  symbol       TEXT NOT NULL,     -- BTCUSDT, ETHUSDT и т.д.
  amount_usd   FLOAT8 NOT NULL,
  direction    TEXT NOT NULL CHECK (direction IN ('buy', 'sell', 'neutral')),
  score        FLOAT8 NOT NULL,   -- -1..+1 накопление/раздача
  tx_hash      TEXT NOT NULL,
  detected_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX ON whale_events (symbol, detected_at DESC);
CREATE INDEX ON whale_events (detected_at DESC);
```

### Redis

```
HSET whale:state BTCUSDT  "buy"
HSET whale:state ETHUSDT  "sell"
EXPIRE whale:state 7200   -- TTL 2 часа
```

---

## Whale Tracker Logic

### Polling loop (`tracker.go`, каждые 30 секунд)

1. Загружаем активные адреса из `whale_addresses` (is_active=true)
2. Для каждого адреса запрашиваем транзакции за последние 10 минут через Etherscan/Tronscan
3. Фильтруем: только переводы на известные горячие кошельки Bybit (ETH + Tron, список захардкожен + обновляется вручную)
4. Для каждого подходящего перевода: `amount_usd >= threshold_usdt`
5. Запускаем `direction.Analyze(address, chain)` → score + top_token
6. Определяем символ:
   - Перевод нативным токеном (WBTC/WETH/TRX) → symbol напрямую (BTCUSDT/ETHUSDT/TRXUSDT)
   - Перевод USDT → берём токен с наибольшим |net_score| из 30д активности адреса → его symbol
7. Записываем в `whale_events`
8. Обновляем Redis: `HSET whale:state {symbol} {buy|sell|neutral}`
9. Обновляем `whale_addresses.last_seen_at`, `volume_30d`

### Direction Analysis (`direction.go`)

```
Вход: address, chain
Выход: score float64 (-1..+1), direction string

1. Запрос: все транзакции адреса за последние 30 дней
2. Считаем:
   received_usd = сумма входящих переводов токенов (не стейблкоинов)
   sent_usd     = сумма исходящих переводов токенов (не стейблкоинов)
3. score = (received_usd - sent_usd) / (received_usd + sent_usd + 1)
4. direction:
   score > +0.3  → "buy"
   score < -0.3  → "sell"
   иначе         → "neutral"
```

### Auto-Discovery (`discovery.go`, еженедельно)

1. Запрашиваем Etherscan/Tronscan: топ-100 уникальных отправителей на Bybit hot wallets за 30 дней с объёмом >= threshold
2. Новые адреса → INSERT в `whale_addresses` (is_manual=false)
3. Адреса которых не видели 90 дней → `is_active=false`

**Известные горячие кошельки Bybit (ETH):**
- `0xf89d7b9c864f589bbF53a82105107622B35EaA40` (основной депозитный)
- `0x2b5c7025998f88550Ef2fece8bf87935f542C190`
- (список пополняется вручную через конфиг)

---

## Migration: Bybit News из api-gateway в parser

### В parser

`services/parser/bybit_news.go`:
```go
package main

import (
    "context"
    "sis/pkg/bybitnews"
)

func runBybitNews(ctx context.Context, db *pgxpool.Pool) {
    scraper := bybitnews.NewScraper(db)
    scraper.Start(ctx)
}
```

### В api-gateway

- Убрать `s.newsScraper` поле из `Server`
- Убрать `newsScraper.Start()` из `main.go`
- Endpoints `ListBybitAnnouncements`, `GetLatestBybitNews`, `RefreshBybitNews`, `GetDelistingSymbolsHandler` — **остаются без изменений**, читают из той же DB
- `RefreshBybitNews` endpoint: убирается из api-gateway. В parser-сервисе появляется внутренний endpoint `POST /internal/refresh-news` (недоступен снаружи). Кнопка "Обновить" в UI листингов вызывает этот endpoint через api-gateway proxy `/admin/parser/refresh-news`.

---

## Signal Engine: новый сигнал `whale`

В `pkg/signal/signals.go` — новая регистрация:

```go
// Сигнал "whale" — читает состояние китов из Redis
// Params:
//   threshold_usdt: минимальный объём перевода (default 50000)
//   window_hours:   TTL окна в часах (default 2)
```

**Compute logic:**
1. `HGET whale:state {symbol}` из Redis
2. Если ключ не найден → фоллбэк: `SELECT direction FROM whale_events WHERE symbol=$1 AND detected_at > now()-interval '2 hours' ORDER BY detected_at DESC LIMIT 1`
3. Вернуть Buy / Sell / Neutral

**Прогрев при старте signal-engine:**
```
SELECT DISTINCT ON (symbol) symbol, direction
FROM whale_events
WHERE detected_at > now() - interval '2 hours'
ORDER BY symbol, detected_at DESC
→ HSET whale:state {symbol} {direction}
```

---

## API Endpoints (api-gateway)

### Whale addresses management

```
GET    /admin/whale/addresses          — список адресов (с пагинацией)
POST   /admin/whale/addresses          — добавить вручную {address, chain, label}
PATCH  /admin/whale/addresses/:id      — обновить label / is_active
DELETE /admin/whale/addresses/:id      — деактивировать (soft delete)
POST   /admin/whale/discover           — запустить авто-discovery немедленно
```

### Whale state & events

```
GET /admin/whale/state                 — текущее состояние по символам (из Redis + DB)
GET /admin/whale/events?limit=50       — лента событий
POST /admin/whale/simulate             — пересчёт по параметрам (what-if)
  body: { threshold_usdt, window_hours }
  response: { events_count, by_symbol: [{symbol, direction, total_usd}], buckets: [...] }
```

---

## Frontend: AdminPage → Парсеры

### Новый раздел "Парсеры" в AdminPage

Два суб-таба:
1. **"Листинги"** — существующий `BybitNewsTab` (просто перемещён в новый раздел)
2. **"Киты"** — новый `WhaleParserTab`

### Новые файлы

```
frontend/src/features/parsers/
  ParsersSection.tsx    — контейнер с таб-свитчером (Листинги / Киты)

frontend/src/features/whale-parser/
  WhaleParserTab.tsx    — главный компонент вкладки
  api.ts                — вызовы API
  types.ts              — TypeScript типы
```

### WhaleParserTab layout

```
┌─────────────────────────────────────────────────────────┐
│ СТАТУС  parser: ● online   Адресов: 87 (ETH:52 Tron:35) │
│         Событий за 24ч: 14   Последний скан: 2 мин назад│
├─────────────────────────────────────────────────────────┤
│ ТЕКУЩЕЕ СОСТОЯНИЕ                                        │
│  BTCUSDT  🟢 BUY   $2.1M за 4ч   12 мин назад          │
│  ETHUSDT  🔴 SELL  $890K за 6ч   1ч назад              │
│  SOLUSDT  ⚪ —                                           │
├─────────────────────────────────────────────────────────┤
│ СИМУЛЯЦИЯ                                               │
│  Порог: [______50 000 USDT______]  Окно: [__2__ ч]     │
│                               [Пересчитать]             │
│  → Событий: 14  BUY: BTCUSDT, SOLUSDT  SELL: ETHUSDT  │
│  → 50k-100k: 6  100k-500k: 5  500k+: 3                │
├─────────────────────────────────────────────────────────┤
│ АДРЕСА             [+ Добавить]  [↻ Переобнаружить]     │
│  Адрес       Сеть  Лейбл   30д Vol   Последний  Статус │
│  0xf89d...   ETH   Whale1  $14M  2ч назад  ● active    │
│  TRx9a...    Tron  —       $2M   вчера     ● active    │
├─────────────────────────────────────────────────────────┤
│ ЛЕНТА СОБЫТИЙ                                           │
│  14:23  Whale1 · BTCUSDT · 🟢 BUY · $1.2M  [tx↗]     │
│  13:51  0xf4.. · ETHUSDT · 🔴 SELL · $890K [tx↗]     │
└─────────────────────────────────────────────────────────┘
```

---

## docker-compose

Новый сервис:
```yaml
parser:
  build:
    context: .
    dockerfile: services/parser/Dockerfile
  environment:
    - DATABASE_URL=${DATABASE_URL}
    - REDIS_URL=${REDIS_URL}
    - ETHERSCAN_API_KEY=${ETHERSCAN_API_KEY}
    - TRONSCAN_API_KEY=${TRONSCAN_API_KEY}
    - WHALE_THRESHOLD_USDT=50000
  restart: unless-stopped
  depends_on:
    - db
    - redis
```

`services/parser/Dockerfile` — аналогичен другим Go-сервисам.

---

## Scope Summary

**Новые файлы:**
- `services/parser/` (новый сервис, ~5 Go файлов)
- `pkg/whalestate/` (shared types опционально, можно встроить в parser)
- `frontend/src/features/parsers/ParsersSection.tsx`
- `frontend/src/features/whale-parser/` (3 файла)
- Миграция БД (1 SQL файл)

**Изменённые файлы:**
- `services/api-gateway/main.go` — убрать newsScraper
- `pkg/signal/signals.go` — добавить `whale` сигнал
- `services/signal-engine/main.go` — прогрев Redis при старте
- `frontend/src/pages/AdminPage.tsx` — добавить раздел "Парсеры"
- `docker-compose.yml` — добавить parser сервис

**Без изменений:**
- `pkg/bybitnews/` — остаётся, переиспользуется
- Все публичные API endpoints листингов
- Существующий signal-engine compute pipeline
