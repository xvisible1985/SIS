# Crypto Signal Analyzer — Design Spec
**Date:** 2026-04-16  
**Status:** Approved

---

## Overview

SaaS-платформа для детального анализа работы торговых сигналов и индикаторов криптовалют. Пользователи создают сигналы из комбинаций индикаторов, прогоняют бэктесты на исторических данных (1+ год), анализируют статистику и паттерны, автоматически оптимизируют параметры, и подключают успешные сигналы к торговым платформам через вебхуки.

**Монетизация:** Freemium (Free / Pro / Enterprise)  
**Биржи:** Binance + Bybit (Spot + Futures)  
**Приоритеты:** скорость взаимодействия с биржами, надёжность, масштабируемость

---

## Architecture

### Подход: Сервисный монорепо (4 Go-сервиса)

Четыре отдельных Go-бинарника в одном репозитории. Общение между сервисами через Redis Streams / Pub-Sub. Общий код (типы, биржевые клиенты, утилиты) — shared Go пакеты внутри монорепо.

```
/
├── services/
│   ├── ingester/        # Data Ingester
│   ├── signal-engine/   # Signal Engine + Optimizer
│   ├── api-gateway/     # REST API + WebSocket
│   └── webhook/         # Webhook Dispatcher
├── pkg/
│   ├── exchange/        # Binance + Bybit клиенты
│   ├── indicators/      # RSI, MACD, EMA, BB, ...
│   ├── models/          # общие типы данных
│   └── metrics/         # Prometheus helpers
├── frontend/            # React SPA
└── infra/               # Docker, K8s, nginx конфиги
```

### Data Flow

**Real-time:**
```
Binance/Bybit WS → Data Ingester → TimescaleDB
                                 → Redis pub/sub → Signal Engine
                                                 → (сигнал сработал) Redis → Webhook Dispatcher → торговая платформа
                                                 → API Gateway WebSocket → браузер
```

**Бэктестинг / Оптимизация:**
```
Пользователь → API Gateway → Redis job queue → Signal Engine worker
                                              → TimescaleDB (чтение свечей)
                                              → TimescaleDB (запись результата)
                                              → Redis pub/sub (прогресс) → браузер
```

---

## Services

### 1. Data Ingester

Единственный stateful сервис. Поддерживает постоянные WebSocket-соединения к Binance и Bybit.

**Ответственности:**
- Подключение к WS-стримам свечей по всем торгуемым парам (Spot + Futures)
- Запись новых свечей в TimescaleDB
- Публикация новых закрытых свечей в Redis pub/sub канал `candles:{exchange}:{symbol}:{timeframe}`
- REST-загрузка исторических данных при первом добавлении пары или заполнении пробелов
- Автоматический реконнект при обрыве соединения

**Масштабирование:** 1 инстанс (stateful). При необходимости — партиционирование по биржам.

### 2. Signal Engine

Вычислительное ядро системы. Stateless, горизонтально масштабируется.

**Ответственности:**
- Подписка на Redis pub/sub, real-time вычисление активных сигналов пользователей
- Обработка заданий из очереди: бэктестинг, оптимизация
- Вычисление индикаторов из сырых свечей
- Поиск паттернов в результатах бэктестинга
- Публикация сработавших сигналов в Redis
- Экспорт метрик Prometheus

**Встроенные индикаторы v1:** RSI, MACD, EMA, SMA, Bollinger Bands, Volume, ATR, Stochastic.

**Масштабирование:** N инстансов, каждый берёт задачи из Redis-очереди.

### 3. API Gateway

Единственная точка входа для фронтенда. Stateless.

**Ответственности:**
- REST API (JWT-аутентификация)
- WebSocket-сервер для браузеров (подписка на свечи, прогресс задач, уведомления о сигналах)
- Управление пользователями, подписками, сигналами, вебхуками
- Freemium-гейтинг: проверка лимитов плана перед созданием задач
- Resource estimation: расчёт стоимости задачи до постановки в очередь
- Чтение системных метрик и агрегирование для UI-виджетов

### 4. Webhook Dispatcher

**Ответственности:**
- Подписка на Redis: сработавшие сигналы
- POST-запрос на зарегистрированные URL пользователя
- Retry-логика: 3 попытки, экспоненциальный backoff (1s → 5s → 30s)
- Логирование каждой отправки: статус, время ответа, тело ответа
- Поддерживаемые платформы: TradingView alerts, 3Commas, Alertatron, custom URL

**Payload:**
```json
{
  "signal_id": "uuid",
  "signal_name": "My RSI + EMA Signal",
  "symbol": "BTCUSDT",
  "exchange": "binance",
  "market": "futures",
  "direction": "LONG",
  "price": "67420.50",
  "timestamp": "2026-04-16T12:00:00Z"
}
```

---

## Data Model

### TimescaleDB

```sql
-- Свечи (hypertable, партиционирование по open_time)
candles (
  exchange    TEXT,       -- 'binance' | 'bybit'
  symbol      TEXT,       -- 'BTCUSDT'
  market      TEXT,       -- 'spot' | 'futures'
  timeframe   TEXT,       -- '1m' | '5m' | '15m' | '1h' | '4h' | '1d'
  open_time   TIMESTAMPTZ,
  open        NUMERIC,
  high        NUMERIC,
  low         NUMERIC,
  close       NUMERIC,
  volume      NUMERIC,
  PRIMARY KEY (exchange, symbol, market, timeframe, open_time)
)

-- Пользователи
users (id, email, password_hash, plan, created_at, ...)

-- Сигналы
signals (
  id          UUID PRIMARY KEY,
  owner_id    UUID REFERENCES users,
  name        TEXT,
  description TEXT,
  exchange    TEXT,
  symbol      TEXT,
  market      TEXT,
  timeframe   TEXT,
  direction   TEXT,       -- 'LONG' | 'SHORT' | 'BOTH'
  conditions  JSONB,      -- дерево условий
  is_active   BOOLEAN,
  created_at  TIMESTAMPTZ
)

-- Результаты бэктестов
backtest_results (
  id              UUID PRIMARY KEY,
  signal_id       UUID REFERENCES signals,
  symbol          TEXT,
  timeframe       TEXT,
  period_from     TIMESTAMPTZ,
  period_to       TIMESTAMPTZ,
  mode            TEXT,       -- 'fast' | 'walk_forward'
  total_signals   INT,
  win_count       INT,
  loss_count      INT,
  win_rate        NUMERIC,
  avg_gain        NUMERIC,
  max_drawdown    NUMERIC,
  profit_factor   NUMERIC,
  patterns        JSONB,      -- найденные паттерны
  trades          JSONB,      -- список сделок
  created_at      TIMESTAMPTZ
)

-- Результаты оптимизации
optimization_results (
  id              UUID PRIMARY KEY,
  signal_id       UUID REFERENCES signals,
  job_params      JSONB,      -- диапазоны параметров поиска
  mode            TEXT,       -- 'fast' | 'walk_forward'
  top_combinations JSONB,     -- топ-10 комбинаций с метриками
  best_params     JSONB,      -- лучшая комбинация
  created_at      TIMESTAMPTZ
)

-- Вебхуки
webhooks (
  id          UUID PRIMARY KEY,
  owner_id    UUID REFERENCES users,
  signal_id   UUID REFERENCES signals,
  url         TEXT,
  platform    TEXT,
  is_active   BOOLEAN,
  created_at  TIMESTAMPTZ
)

-- Лог вебхуков
webhook_logs (
  id              UUID,
  webhook_id      UUID REFERENCES webhooks,
  sent_at         TIMESTAMPTZ,
  status_code     INT,
  response_ms     INT,
  success         BOOLEAN,
  error           TEXT
)
```

### Redis

| Ключ | Тип | Назначение |
|---|---|---|
| `candles:{exchange}:{symbol}:{tf}:last` | Hash | Последняя закрытая свеча |
| `candles:{exchange}:{symbol}:{tf}` | Pub/Sub | Новые свечи real-time |
| `signals:fired` | Stream | Сработавшие сигналы |
| `jobs:backtest` | Stream | Очередь бэктест-заданий |
| `jobs:optimize` | Stream | Очередь оптимизаций |
| `jobs:{id}:progress` | Hash | Прогресс задания (%) |

---

## Signal Model

Сигнал хранится как JSON-дерево условий. Поддерживает вложенность и ссылки на другие сигналы.

```json
{
  "type": "AND",
  "children": [
    {
      "type": "condition",
      "indicator": "RSI",
      "params": { "period": 14 },
      "operator": "<",
      "value": 35
    },
    {
      "type": "condition",
      "indicator": "EMA",
      "params": { "period": 9 },
      "operator": "crosses_above",
      "compare_to": { "indicator": "EMA", "params": { "period": 21 } }
    },
    {
      "type": "OR",
      "children": [
        {
          "type": "signal_ref",
          "signal_id": "uuid-of-another-signal"
        },
        {
          "type": "condition",
          "indicator": "MACD",
          "params": {},
          "operator": "histogram_>",
          "value": 0
        }
      ]
    }
  ]
}
```

**Поддерживаемые операторы:** `<`, `>`, `=`, `!=`, `crosses_above`, `crosses_below`, `% change >`, `relative_to` (кратно другому индикатору).

---

## Backtesting & Optimization

### Бэктестинг

1. Пользователь задаёт: сигнал, символ, таймфрейм, период, take profit %, stop loss %
2. API Gateway считает resource estimate (кол-во свечей × сложность дерева условий)
3. Задание → Redis job queue
4. Signal Engine worker читает свечи из TimescaleDB, прогоняет условия свеча за свечой
5. При срабатывании сигнала симулирует сделку (фиксированный TP/SL)
6. Прогресс публикуется в Redis каждые 5% → пользователь видит по WebSocket
7. Результат: win_rate, avg_gain, max_drawdown, profit_factor, список сделок, паттерны

**Автоматические паттерны:** день недели, время суток, режим рынка (ATR-based: тренд/боковик/высокая волатильность).

### Оптимизатор

Автоматический перебор параметров сигнала для поиска наилучшей конфигурации.

**Параметры поиска:** значения индикаторов (периоды, пороги), take profit %, таймфреймы.

**Режимы:**
- **Быстрый** — Grid Search по всему историческому периоду. Быстро, риск overfitting.
- **Walk-Forward** — история делится на N скользящих окон (train 80% / test 20%). Медленнее, результат валидный для реального рынка.

**Алгоритм:** Grid Search (v1). В дальнейшем — Random Search или Bayesian Optimization.

**Метрика ранжирования:** `Profit Factor × Win Rate` (настраивается пользователем).

**Результат:** топ-10 комбинаций с метриками, автоматическое предложение лучшей к активации.

**Freemium:** Free — ограниченный диапазон параметров, 1 задача в очереди. Pro — полный диапазон, приоритетная очередь.

---

## Resource Management

### Resource Estimation

Перед постановкой задачи в очередь API Gateway вычисляет её стоимость:
- `N_combinations = product(range_sizes)` для оптимизации
- `N_candles = period_days × candles_per_day(timeframe)`
- `estimated_ops = N_combinations × N_candles × tree_depth`
- Из `estimated_ops` → приблизительное время и RAM

Пользователь видит предупреждение до запуска. Задачи сверх лимита плана блокируются с предложением апгрейда.

### Мониторинг ресурсов

**Системный уровень (admin-панель):**
- CPU, RAM, disk I/O per сервис
- Redis memory usage
- TimescaleDB size, query latency
- Кол-во активных WebSocket-соединений
- Длина очередей заданий

**Пользовательский уровень (личный кабинет):**
- CPU-время потраченное на задачи за период
- Кол-во активных / в очереди / завершённых задач
- Остаток квоты по текущему плану (сигналы, вебхуки, история)

**Стек:** каждый Go-сервис экспортирует `/metrics` (Prometheus format). API Gateway агрегирует и отдаёт фронтенду. История метрик хранится в TimescaleDB. Grafana для admin-дашборда.

---

## Frontend

**Стек:** React + TypeScript, D3.js для графиков.

**Основные экраны:**
- **Dashboard** — список активных сигналов, последние срабатывания, виджеты ресурсов
- **Chart** — интерактивный ценовой график с наложением индикаторов и точек срабатывания сигналов
- **Signal Builder** — конструктор AND/OR условий, drag-and-drop, предпросмотр на графике
- **Backtest** — запуск, прогресс, результаты (win rate, equity curve, список сделок, паттерны)
- **Optimizer** — настройка диапазонов параметров, выбор режима, таблица топ-комбинаций
- **Webhooks** — управление вебхуками, лог отправок
- **Settings / Billing** — профиль, план, квоты, API-ключи

**Real-time:** WebSocket-соединение с API Gateway. Обновление графика, прогресс задач, уведомления о сигналах — без перезагрузки страницы.

---

## Freemium Tiers

| Функция | Free | Pro | Enterprise |
|---|---|---|---|
| Сигналов | 3 | Безлимит | Безлимит |
| Вебхуков | 1 | 10 | Безлимит |
| История бэктеста | 90 дней | 2 года | Всё доступное |
| Оптимизация | Ограниченный диапазон | Полный диапазон | Полный диапазон |
| Приоритет очереди | Обычный | Высокий | Максимальный |
| Символов | 3 | 20 | Безлимит |

---

## Deployment

```
Nginx (TLS, Let's Encrypt)
  └── API Gateway (N инстансов, load balanced)

Data Ingester     (1 инстанс, stateful)
Signal Engine     (N инстансов, stateless)
Webhook Dispatcher (1–2 инстанса)

TimescaleDB       (primary + replica)
Redis             (single или Sentinel)

Prometheus + Grafana (мониторинг)
```

**Dev:** Docker Compose (все сервисы локально).  
**Prod:** Docker Swarm (старт) → Kubernetes (при росте нагрузки).

Signal Engine масштабируется горизонтально: новый инстанс берёт задачи из Redis-очереди без дополнительной конфигурации.

---

## Out of Scope (v1)

- ML-модели для предсказания сигналов
- Автоматическое исполнение ордеров (только вебхуки)
- Мобильное приложение
- Backtesting с учётом комиссий и проскальзывания (добавить в v2)
- Bayesian / Random Search оптимизация (добавить в v2)
- Поддержка других бирж кроме Binance + Bybit
