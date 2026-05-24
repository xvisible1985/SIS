# Справочник по проекту SIS

> Дата составления: 2026-05-18  
> Путь к репозиторию: `C:\Users\123\Projects\sis`

---

## 1. Общее описание

**SIS** — это платформа для криптовалютной алгоритмической торговли и управления торговыми сигналами.

Проект объединяет в себе:
- **Сбор рыночных данных** (свечи OHLCV) с бирж в реальном времени.
- **Движок торговых сигналов** на базе технических индикаторов с бэктестингом и оптимизацией параметров.
- **Стратегийный движок** (grid/DCA/manual) с автоматическим размещением ордеров, TP/SL, trailing stop.
- **Ручной терминал** для торговли через API Bybit.
- **Вебхуки** и Telegram-уведомления по сигналам.
- **Веб-интерфейс** (React SPA) для создания сигналов, управления стратегиями и торговли.

---

## 2. Технологический стек

### Бэкенд
| Компонент | Технология |
|-----------|------------|
| Язык | Go 1.25 |
| HTTP-роутер | `chi/v5` |
| База данных | PostgreSQL 16 + TimescaleDB |
| Драйвер БД | `pgx/v5` |
| Кэш / Pub-Sub / Streams | Redis 7 (`go-redis/v9`) |
| WebSocket | Gorilla WebSocket |
| Аутентификация | JWT (HS256) + bcrypt |
| Шифрование секретов | AES-256-GCM |
| Сборка | `go build`, Makefile |

### Фронтенд
| Компонент | Технология |
|-----------|------------|
| Фреймворк | React 18 + TypeScript 5.7 |
| Сборщик | Vite 6 |
| Стили | Tailwind CSS 3.4 |
| Роутинг | React Router v6 |
| Графики | `lightweight-charts` v5 |
| HTTP-клиент | axios |
| Иконки | `lucide-react` |
| Тесты | Vitest + jsdom + React Testing Library |

### Инфраструктура
| Компонент | Описание |
|-----------|----------|
| База данных | TimescaleDB (hypertable `candles` со сжатием через 7 дней) |
| Кэш / Очереди | Redis (Pub/Sub для свечей, Streams для задач, Hashes для прогресса) |
| Контейнеры | Docker Compose (`timescaledb`, `redis`) |

---

## 3. Архитектура (микросервисы)

```
┌─────────────────────────────────────────────────────────────┐
│                        Клиент (React SPA)                    │
└──────────────────────┬──────────────────────────────────────┘
                       │ HTTP / WS
┌──────────────────────▼──────────────────────────────────────┐
│  api-gateway  (:8080) — центральный HTTP/WebSocket шлюз      │
│  • Auth, Signals, Strategies, Trader, Accounts, Webhooks    │
│  • Admin panel, WS-стримы позиций/стратегий/задач           │
└──┬───────────────────┬───────────────────┬──────────────────┘
   │                   │                   │
   ▼                   ▼                   ▼
┌──────────┐    ┌──────────────┐    ┌───────────────┐
│ ingester │    │ signal-engine│    │   webhook     │
│ (данные) │    │(бэктест/опт) │    │ (доставка)    │
└────┬─────┘    └──────┬───────┘    └───────────────┘
     │                 │
     ▼                 ▼
  TimescaleDB      TimescaleDB
  Redis Pub/Sub    Redis Streams
```

### 3.1. `api-gateway`
Центральный HTTP-сервер и WebSocket-шлюз.

**Основные группы эндпоинтов:**
- `POST /auth/register`, `POST /auth/login` — регистрация и вход.
- `GET/POST /signals`, `GET/PUT/DELETE /signals/{id}` — CRUD сигналов.
- `POST /signals/{id}/backtest`, `POST /signals/{id}/optimize` — запуск задач.
- `GET/POST /strategies`, `PUT /strategies/{id}`, `POST /strategies/{id}/status` — управление стратегиями.
- `GET /strategies/{id}/state`, `GET /strategies/{id}/events` — мониторинг стратегий.
- `GET/POST /accounts`, `DELETE /accounts/{id}`, `GET /accounts/{id}/balance` — биржевые аккаунты.
- `POST /trader/order`, `DELETE /trader/order`, `GET /trader/orders`, `GET /trader/executions`, `GET /trader/pnl` — ручной терминал.
- `GET/POST /webhooks`, `GET/PUT/DELETE /webhooks/{id}` — вебхуки.
- `GET /admin/metrics`, `GET/PATCH /admin/signal-types/{id}` — админка.

**WebSocket эндпоинты:**
- `GET /ws/jobs/{id}/progress` — прогресс бэктеста/оптимизации.
- `GET /ws/trader/positions?account_id=` — live позиции и ордера с Bybit.
- `GET /ws/strategies/updates` — обновления статуса стратегий.
- `GET /ws/strategies/{id}/events` — события стратегии.

### 3.2. `ingester`
Сервис сбора рыночных данных. Подключается по WebSocket к **Binance** и **Bybit**, получает свечи (klines), сохраняет их в TimescaleDB и публикует закрытые свечи в Redis Pub/Sub.

**Конфигурация через env:**
- `SYMBOLS` — список символов (`BTCUSDT,ETHUSDT`).
- `MARKETS` — `spot,futures`.
- `TIMEFRAMES` — `1m,5m,15m,1h`.

### 3.3. `signal-engine`
Фоновый воркер для бэктестинга и оптимизации сигналов.

**Компоненты:**
- `worker.go` — потребитель из `jobs:backtest` (Redis Stream), выполняет бэктест.
- `optimizer_consumer.go` — потребитель из `jobs:optimize`, запускает grid search / walk-forward.
- `backtest.go` — движок симуляции сделок на исторических свечах.
- `optimizer.go` — генерация комбинаций параметров и скоринг.
- `patterns.go` — анализ паттернов (день недели, час, рыночный режим).

**Режимы оптимизации:**
- `fast` — полный перебор параметров (Cartesian product).
- `walk_forward` — кросс-валидация на N фолдах (in-sample → out-of-sample).

### 3.4. `webhook`
Диспетчер вебхуков. Читает `signals:fired` (Redis Stream), находит активные webhook-и по `signal_id`, доставляет HTTP POST с ретраями (3 попытки с задержками 1с, 5с, 30с). Логирует результаты в `webhook_logs`.

---

## 4. Общие пакеты (`pkg/`)

| Пакет | Назначение |
|-------|------------|
| `pkg/auth` | bcrypt-хеширование паролей, JWT HS256 генерация/валидация. |
| `pkg/cache` | Redis-клиент, публикация свечей в Pub/Sub. |
| `pkg/coinicons` | Кэширование PNG-иконок криптовалют из публичных CDN. |
| `pkg/crypto` | AES-256-GCM шифрование/дешифрование (API-ключи бирж). |
| `pkg/db` | Подключение к pgxpool, раннер SQL-миграций. |
| `pkg/exchange` | Абстракция биржевого клиента (`FetchCandles`, `Subscribe`). |
| `pkg/exchange/binance` | REST + WebSocket клиент Binance (spot, futures). |
| `pkg/exchange/bybit` | REST + WebSocket клиент Bybit (spot, linear). |
| `pkg/indicators` | Технические индикаторы (RSI, EMA, SMA, MACD, BB, ATR, Stochastic, Volume). |
| `pkg/models` | Доменные модели: `Candle`, `Exchange`, `Market`, `Timeframe`. |
| `pkg/signal` | Движок live-сигналов: `Engine`, `KlineHub`, регистр сигналов, метрики. |
| `pkg/signals` | Древовидный evaluator условий сигналов (AND/OR/Condition/SignalRef). |
| `pkg/strategy` | Стратегийный движок: `Engine` → `AccountRunner` → `StrategyRunner`, циклы, уровни, реконсиляция. |
| `pkg/trader` | Bybit торговый API: REST (ордера, позиции, исполнения), Trade WS, Private WS, синхронизация. |

### Поддерживаемые биржи
| Биржа | Данные | Торговля |
|-------|--------|----------|
| **Binance** | Spot, Futures (REST + WS) | Нет |
| **Bybit** | Spot, Linear, Inverse (REST + WS) | Полная |

### Технические индикаторы
RSI, EMA, SMA, MACD (сигнал/гистограмма), Bollinger Bands, ATR, Stochastic (%K/%D), Volume.

---

## 5. База данных (схема)

**Ключевые таблицы и домены:**

| Домен | Таблицы |
|-------|---------|
| Рыночные данные | `candles` (TimescaleDB hypertable, 7-дневное сжатие) |
| Пользователи | `users`, `telegram_connections`, `telegram_pending_tokens`, `telegram_notification_settings`, `referral_codes`, `referral_signups` |
| Сигналы | `signals`, `backtest_results`, `optimization_results` |
| Таксономии | `signal_types`, `indicator_types` |
| Биржевые аккаунты | `exchange_accounts` |
| Стратегии | `strategies`, `strategy_cycles`, `strategy_levels`, `strategy_events`, `strategy_templates` |
| Торговля | `trader_orders`, `trader_executions`, `sis_order_seq` |
| Вебхуки | `webhooks`, `webhook_logs` |
| Системные | `coin_icons`, `signal_metrics_snapshots` (7-дневный retention) |

Всего **18 миграций** в директории `migrations/`.

---

## 6. Фронтенд

Одностраничное приложение (SPA), билдится Vite.

**Основные функции (по роутам/экранам):**
- **Авторизация** — вход/регистрация, JWT, защищённые роуты.
- **Сигналы** — конструктор условий (дерево индикаторов + операторы), запуск бэктеста, оптимизация (fast / walk-forward).
- **Терминал** — live-торговля с WebSocket-стримом позиций и ордеров, график свечей с аннотациями.
- **Стратегии** — создание grid/DCA/manual стратегий, управление циклами, уровнями, TP/SL, trailing stop, фильтры по сигналам.
- **Аккаунты** — подключение API-ключей Bybit/Binance, проверка, баланс.
- **Вебхуки** — интеграции с внешними сервисами.
- **Админка** — метрики, управление типами сигналов/индикаторов.

**Прокси разработки:** `vite.config.ts` проксирует API на `localhost:8080`.

---

## 7. Переменные окружения

Ключевые переменные (из `.env.example`):

```bash
DATABASE_URL=postgres://sis:sis_secret@localhost:5432/sis
REDIS_URL=redis://localhost:6379/0

# API-ключи бирж (для тестов/ingester)
BINANCE_API_KEY=
BINANCE_API_SECRET=
BYBIT_API_KEY=
BYBIT_API_SECRET=

# Ingester
SYMBOLS=BTCUSDT,ETHUSDT
MARKETS=spot,futures
TIMEFRAMES=1m,5m,15m,1h

LOG_LEVEL=info
```

Также используются (в коде):
- `ADMIN_EMAILS` — список email администраторов.
- `JWT_SECRET` — секрет для подписи JWT.
- `AES_KEY` — ключ для AES-256-GCM.

---

## 8. Команды разработки

Команды из `Makefile`:

```bash
# Инфраструктура
make up              # docker compose up -d (timescaledb + redis)
make down            # docker compose down

# Миграции
make migrate         # go run ./cmd/migrate/

# Сборка и запуск
make build-ingester  # go build -o bin/ingester ./services/ingester/
make run-ingester    # go run ./services/ingester/

# Тесты
make test            # go test ./...
make test-integration# go test ./tests/integration/... -v -tags=integration
```

Для запуска всех сервисов в dev-режиме:
```bash
cd frontend && npm run dev      # фронтенд (порт 5173)
go run ./services/api-gateway/  # API шлюз (порт 8080)
go run ./services/ingester/     # сбор данных
go run ./services/signal-engine/ # бэктест/оптимизация
go run ./services/webhook/      # диспетчер вебхуков
```

---

## 9. Тесты

- **Unit-тесты** — в каждом пакете (`*_test.go`): `auth`, `cache`, `crypto`, `models`, `indicators`, `exchange/binance`, `exchange/bybit`, `signals`, `strategy`, `trader`, `signal-engine`, `api-gateway`, `webhook`.
- **Интеграционные тесты** — в `tests/integration/` (требуют тег `-tags=integration`).

---

## 10. Важные архитектурные решения

1. **Idempotency ingester** — `ON CONFLICT DO NOTHING` при вставке свечей позволяет безопасно перезапускать сервис.
2. **Redis Streams для задач** — backtest и optimize обрабатываются через Redis Streams (`jobs:backtest`, `jobs:optimize`) с consumer groups.
3. **Прогресс через Redis Hashes** — статус выполнения задач (`jobs:{id}:progress`) читается WebSocket-ом в реальном времени.
4. **AES-шифрование API-ключей** — ключи бирж хранятся в БД в зашифрованном виде.
5. **Strategy Engine внутри api-gateway** — стратегийный движок (`pkg/strategy`) запускается как фоновый процесс в api-gateway, а не как отдельный сервис.
6. **Bybit-only торговля** — хотя данные собираются с Binance и Bybit, исполнение ордеров и управление позициями реализовано только для Bybit.
7. **Тёмная тема** — фронтенд работает только в тёмном режиме (`dark` класс жёстко задан в `main.tsx`).

---

## 11. Структура репозитория

```
sis/
├── cmd/migrate/          # CLI-утилита для миграций БД
├── frontend/             # React SPA (Vite + Tailwind)
├── migrations/           # SQL-миграции (001–018)
├── pkg/                  # Общие Go-пакеты
│   ├── auth/
│   ├── cache/
│   ├── coinicons/
│   ├── crypto/
│   ├── db/
│   ├── exchange/
│   │   ├── binance/
│   │   └── bybit/
│   ├── indicators/
│   ├── models/
│   ├── signal/
│   ├── signals/
│   ├── strategy/
│   └── trader/
├── services/             # Микросервисы
│   ├── api-gateway/      # HTTP/WebSocket шлюз + стратегии
│   ├── ingester/         # Сбор свечей с бирж
│   ├── signal-engine/    # Бэктест и оптимизация
│   └── webhook/          # Доставка webhook-ов
├── tests/integration/    # Интеграционные тесты
├── .env / .env.example   # Переменные окружения
├── docker-compose.yml    # TimescaleDB + Redis
├── go.mod / go.sum       # Go-зависимости
└── Makefile              # Команды разработки
```

---

## 12. Точки входа для новых разработчиков

| Задача | Где смотреть |
|--------|--------------|
| Добавить новый HTTP эндпоинт | `services/api-gateway/server.go` + новый `*_handler.go` |
| Добавить новый индикатор | `pkg/indicators/` + зарегистрировать в `pkg/signal/registry.go` |
| Добавить поддержку новой биржи (данные) | `pkg/exchange/` — реализовать интерфейс `exchange.Client` |
| Добавить поддержку новой биржи (торговля) | `pkg/trader/` — аналогично Bybit |
| Изменить схему БД | `migrations/XXX_name.sql` + `pkg/db/` |
| Добавить фронтенд-страницу | `frontend/src/App.tsx` (роуты) + новый компонент |
| Починить/улучшить бэктест | `services/signal-engine/backtest.go`, `pkg/signals/evaluator.go` |
| Изменить логику стратегий | `pkg/strategy/engine.go`, `pkg/strategy/cycle.go` |

---

*Справка сгенерирована автоматически на основе анализа кодовой базы.*
