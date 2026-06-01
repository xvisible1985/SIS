# Log Visualizer Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Добавить вкладку «Визуализатор» в админку — пошаговый проигрыш истории работы стратегии на свечном графике с навигацией по событиям.

**Architecture:** Новый таб в AdminPage. Пять новых Go-эндпоинтов в `admin_log_visualizer_handler.go` для получения аккаунтов, стратегий, событий, уровней-ордеров и свечей. Весь массив данных за выбранный диапазон грузится один раз во фронтенде, анимация реализована через `setInterval` по массиву свечей; при достижении временной метки события — пауза.

**Tech Stack:** Go (api-gateway), React + TypeScript, `lightweight-charts` v4 (уже в проекте), Tailwind CSS.

---

## Данные и схема

### Существующие таблицы

```sql
-- strategy_events: текстовые логи
strategy_id  UUID
message      TEXT       -- "L3 filled at 0.4225", "TP placed"
level        TEXT       -- info | warn | error
created_at   TIMESTAMPTZ

-- strategy_levels: ордера
strategy_id  UUID
cycle_id     UUID
level_idx    INT
side         TEXT       -- Buy | Sell
target_price NUMERIC
qty          TEXT
status       TEXT       -- pending | filled | sl_closed | cancelled
filled_at    TIMESTAMPTZ  -- NULL если не исполнен
filled_price NUMERIC
slot         SMALLINT
```

### Новые API эндпоинты (файл: `admin_log_visualizer_handler.go`)

```
GET /admin/log-visualizer/accounts
    → [{id, label, owner_username}]

GET /admin/log-visualizer/strategies?account_id={id}
    → [{id, symbol, direction, strategy_type, status}]
    // label для UI: "{SYMBOL} · {direction} · {strategy_type}" + статус-badge

GET /admin/log-visualizer/events?strategy_id={id}&from={unix_ms}&to={unix_ms}
    → [{message, level, ts_ms}]   // из strategy_events

GET /admin/log-visualizer/levels?strategy_id={id}&from={unix_ms}&to={unix_ms}
    → [{level_idx, side, filled_price, qty, status, ts_ms}]
    // WHERE filled_at IS NOT NULL OR (status='sl_closed' AND closed_at IS NOT NULL)

GET /admin/log-visualizer/klines?symbol={sym}&interval={1|3|5|15|30|60|120|240|D}&from={unix_ms}&to={unix_ms}
    → [{t, o, h, l, c, v}]   // проксирует Bybit v5/market/kline
```

---

## Структура файлов

**Backend (новое):**
- `services/api-gateway/admin_log_visualizer_handler.go` — все 5 хендлеров
- `services/api-gateway/main.go` — регистрация маршрутов

**Frontend (новое):**
- `src/features/log-visualizer/LogVisualizerTab.tsx` — главный компонент вкладки
- `src/features/log-visualizer/LogVisualizerChart.tsx` — обёртка над lightweight-charts
- `src/features/log-visualizer/LogVisualizerEventsList.tsx` — боковая панель событий
- `src/features/log-visualizer/LogVisualizerControls.tsx` — нижняя панель управления
- `src/features/log-visualizer/types.ts` — TypeScript-типы
- `src/features/log-visualizer/api.ts` — API-запросы

**Frontend (изменения):**
- `src/pages/AdminPage.tsx` — добавить таб «Визуализатор»

---

## UI — детальное описание

### Верхняя панель (toolbar)

```
[Аккаунт ▾] [Стратегия ▾] [Дата от ────] [Дата до ────] [1m ▾] [1x ━━●━ 48x] [MAX□] [▶ Загрузить]
```

- **Аккаунт** — `select`, список из `/admin/log-visualizer/accounts`
- **Стратегия** — `select`, список из `/admin/log-visualizer/strategies?account_id=...`
- **Дата от / до** — `<input type="date">`, по умолчанию последние 7 дней
- **Интервал** — `select`: `1m | 5m | 15m | 30m | 1h | 4h | 1D`
- **Скорость** — `<input type="range" min=1 max=48>` + чекбокс «MAX»
- **Загрузить** — запускает три параллельных запроса, показывает спиннер

### Основная область

```
┌─────────────────────────────────────┬────────────────────────┐
│                                     │ СОБЫТИЯ (42)           │
│        lightweight-charts           │ ┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄ │
│        свечной график               │ ○ 12:30 cycle started  │
│                                     │ ● 12:34 L3 filled ←── │
│        ← маркеры ордеров            │ ○ 12:41 TP placed      │
│          стрелки вверх/вниз         │ ○ 13:15 L4 triggered   │
│                                     │ ○ …                    │
│         70% ширины                  │   30% ширины           │
└─────────────────────────────────────┴────────────────────────┘
```

**График (LogVisualizerChart):**
- Тёмная тема, совпадает с Chart.tsx
- Свечи добавляются через `series.setData(candles.slice(0, candleIndex + 1))`
- При каждом событии-паузе добавляется маркер: Buy-ордер → зелёная стрелка вверх, Sell → красная вниз, text-log → жёлтый круг
- `chart.timeScale().scrollToPosition(0, false)` — автоскролл к последней свече

**Панель событий (LogVisualizerEventsList):**
- Список `MergedEvent[]` отсортирован по `ts_ms`
- Текущее событие (`eventIndex`) подсвечено amber-рамкой, автопрокрутка к нему
- Клик на событие → `jumpToEvent(idx)`: выставляет `candleIndex` на ближайшую свечу, перерисовывает граф

### Нижняя панель управления (LogVisualizerControls)

```
[⏮ Начало]  [◀ Пред.]  [▶ Играть / ⏸ Пауза]  [▶ След.]  [⏭ Конец]

ℹ 12:34:15 — L3 filled по 0.4225 · qty 47 USDT · [info]
```

- **⏮ / ⏭** — прыжок к первому / последнему событию
- **◀ Пред.** — `eventIndex--`, `candleIndex` = индекс свечи перед `events[eventIndex].ts_ms`
- **▶ / ⏸** — старт/стоп анимации
- **▶ След.** — пауза + прыжок к следующему событию без анимации
- **Инфо-строка** — текст последнего пройденного события

---

## Типы TypeScript

```typescript
// src/features/log-visualizer/types.ts

export interface LVAccount { id: string; label: string; ownerUsername: string }
// Strategies не имеют поля name — отображаем как "SYMBOL · direction · type"
export interface LVStrategy { id: string; symbol: string; direction: string; strategyType: string; status: string }
export interface LVEvent    { message: string; level: string; tsMs: number }
export interface LVLevel    { levelIdx: number; side: 'Buy'|'Sell'; filledPrice: number; qty: string; status: string; tsMs: number }
export interface LVCandle   { t: number; o: number; h: number; l: number; c: number; v: number }

export interface MergedEvent {
  tsMs:    number
  kind:    'log' | 'level'
  log?:    LVEvent
  level?:  LVLevel
  label:   string   // итоговая строка для отображения
}
```

---

## Логика анимации

```typescript
// LogVisualizerTab.tsx

const INTERVAL_MS = 50 // базовый тик анимации

// Один шаг анимации:
function animationTick() {
  const next = candleIndex + 1
  if (next >= candles.length) { setIsPlaying(false); return }

  const nextCandle = candles[next]
  const nextEvent  = events[eventIndex + 1]

  if (nextEvent && nextCandle.t * 1000 >= nextEvent.tsMs) {
    // достигли следующего события
    setCandleIndex(next)
    setEventIndex(ei => ei + 1)
    setIsPlaying(false)          // пауза
  } else {
    setCandleIndex(next)
  }
}

// speed: 1-48 → INTERVAL_MS / speed; MAX → пропускаем к следующему событию
useEffect(() => {
  if (!isPlaying) return
  if (speed === Infinity) {
    jumpToNextEvent(); setIsPlaying(false); return
  }
  const id = setInterval(animationTick, INTERVAL_MS / speed)
  return () => clearInterval(id)
}, [isPlaying, speed, candleIndex, eventIndex])
```

**jumpToEvent(idx):**
```typescript
function jumpToEvent(idx: number) {
  const ev = events[idx]
  const ci = candles.findIndex(c => c.t * 1000 >= ev.tsMs)
  setEventIndex(idx)
  setCandleIndex(ci >= 0 ? ci : candles.length - 1)
  setIsPlaying(false)
}
```

**Назад (prevEvent):**
```typescript
function prevEvent() {
  if (eventIndex <= 0) return
  jumpToEvent(eventIndex - 1)
}
```

---

## Backend — ключевые детали

### `/admin/log-visualizer/accounts`
```sql
SELECT a.id, a.label, u.username
FROM exchange_accounts a
JOIN users u ON u.id = a.owner_id
ORDER BY u.username, a.label
```

### `/admin/log-visualizer/strategies?account_id=`
```sql
SELECT id, symbol, direction, strategy_type, status
FROM strategies
WHERE account_id = $1
ORDER BY created_at DESC
```
-- Поля `name` в таблице нет; UI строит лейбл как "SYMBOL · direction · strategy_type"

### `/admin/log-visualizer/events?strategy_id=&from=&to=`
```sql
SELECT message, level, EXTRACT(EPOCH FROM created_at)*1000 AS ts_ms
FROM strategy_events
WHERE strategy_id = $1
  AND created_at >= to_timestamp($2::bigint / 1000.0)
  AND created_at <  to_timestamp($3::bigint / 1000.0)
ORDER BY created_at ASC
```

### `/admin/log-visualizer/levels?strategy_id=&from=&to=`
```sql
SELECT level_idx, side, filled_price, qty, status,
       EXTRACT(EPOCH FROM filled_at)*1000 AS ts_ms
FROM strategy_levels
WHERE strategy_id = $1
  AND filled_at IS NOT NULL
  AND filled_at >= to_timestamp($2::bigint / 1000.0)
  AND filled_at <  to_timestamp($3::bigint / 1000.0)
ORDER BY filled_at ASC
```

### `/admin/log-visualizer/klines?symbol=&interval=&from=&to=`
Маппинг интервалов: `1m→1, 5m→5, 15m→15, 30m→30, 1h→60, 4h→240, 1D→D`

Прокси-запрос к Bybit:
```
GET https://api.bybit.com/v5/market/kline
    ?category=linear&symbol={sym}&interval={iv}&start={from}&end={to}&limit=1000
```
Bybit возвращает максимум 1000 свечей за запрос. При диапазоне > 1000 свечей — цикл пагинации:
```go
// Пагинация: сдвигаем end на время первой свечи предыдущей порции
allCandles := []LVCandle{}
cursor := toMs
for {
    batch := fetchBybitKlines(symbol, interval, fromMs, cursor, 1000)
    allCandles = append(batch, allCandles...)
    if len(batch) < 1000 || batch[0].T <= fromMs { break }
    cursor = batch[0].T - 1  // сдвигаемся назад
}
```

---

## Маршруты (main.go)

```go
// Внутри блока r.Group("/admin", adminOnly):
r.Get("/log-visualizer/accounts",  s.LVGetAccounts)
r.Get("/log-visualizer/strategies", s.LVGetStrategies)
r.Get("/log-visualizer/events",    s.LVGetEvents)
r.Get("/log-visualizer/levels",    s.LVGetLevels)
r.Get("/log-visualizer/klines",    s.LVGetKlines)
```

---

## Ограничения и граничные случаи

- **Нет событий в диапазоне** → показываем пустую панель событий и анимируем чистый граф (просто свечи)
- **Нет свечей** (неверный символ или нет данных на Bybit) → ошибка с сообщением
- **Диапазон > 30 дней на 1m** → предупреждение: «Большой диапазон. 1m даст ~43K свечей. Продолжить?»
- **Конец массива** → анимация останавливается, кнопка Play неактивна
- **MAX-режим** — при нажатии «Играть» с MAX: все события применяются мгновенно, граф показывает полный диапазон со всеми маркерами сразу

---

## Интеграция в AdminPage

```typescript
// Добавить в TABS:
{ id: 'log-visualizer', label: 'Визуализатор' }

// Добавить в JSX:
{tab === 'log-visualizer' && (
  <div className="flex flex-1 flex-col overflow-hidden">
    <LogVisualizerTab />
  </div>
)}
```
