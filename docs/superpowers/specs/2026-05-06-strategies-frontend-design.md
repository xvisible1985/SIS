# Strategies Frontend Page — Design Spec

## Goal

Отдельная страница `/strategies` для управления автоматическими торговыми стратегиями (grid/DCA). Создание, просмотр, редактирование и управление стратегиями через карточки с расширением и модальную форму с тремя вкладками.

## Architecture

### Navigation

Добавить пункт «Стратегии» в боковую навигацию (Layout / sidebar) с роутом `/strategies`.

### Pages & Components

```
frontend/src/pages/StrategiesPage.tsx           — основная страница
frontend/src/components/strategies/
  StrategyCard.tsx                              — разворачиваемая карточка
  SignalMiniCard.tsx                            — мини-карточка одного сигнала
  StrategyModal.tsx                             — модалка создания/редактирования
  TemplateSelector.tsx                          — строка выбора/сохранения шаблона
frontend/src/api/strategies.ts                  — REST-клиент стратегий
frontend/src/api/strategyTemplates.ts           — REST-клиент шаблонов
```

---

## Data Model Changes

### Таблица `strategies` — расширение (новая миграция `007_strategy_extensions.sql`)

Добавить столбцы:

```sql
ALTER TABLE strategies
  ADD COLUMN leverage          INT     NOT NULL DEFAULT 1,
  ADD COLUMN margin_type       VARCHAR(20) NOT NULL DEFAULT 'isolated', -- isolated | cross
  ADD COLUMN hedge_mode        BOOLEAN NOT NULL DEFAULT false,
  ADD COLUMN strategy_type     VARCHAR(20) NOT NULL DEFAULT 'grid',     -- grid | dca | manual
  ADD COLUMN signal_configs    JSONB   NOT NULL DEFAULT '[]',
  -- [{name: "RSI", params: {period: 14}}, ...]
  ADD COLUMN steps             JSONB   DEFAULT NULL,
  -- NULL = равномерная сетка (step_pct/order_size_usdt)
  -- array = [{price_move_pct: 1.0, lots: 1}, {price_move_pct: 1.5, lots: 2}, ...]
  ADD COLUMN trailing_stop_enabled    BOOLEAN NOT NULL DEFAULT false,
  ADD COLUMN trailing_activation_pct  DECIMAL(10,4),
  ADD COLUMN trailing_callback_pct    DECIMAL(10,4);
```

Когда `steps` не NULL, поля `grid_levels` = длина массива, `step_pct` и `order_size_usdt` задают базу (объём 1 лота). `lots` в каждом шаге — множитель: итоговый размер ордера = `lots * order_size_usdt`.

### Новая таблица `strategy_templates`

```sql
CREATE TABLE strategy_templates (
  id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  name       VARCHAR(200) NOT NULL,
  config     JSONB        NOT NULL,  -- полный снимок настроек стратегии
  created_at TIMESTAMPTZ  NOT NULL DEFAULT now()
);
```

---

## API

### Существующие эндпоинты (расширить поля)

| Метод | Путь | Описание |
|-------|------|----------|
| GET | `/strategies` | Список стратегий |
| POST | `/strategies` | Создать стратегию |
| PUT | `/strategies/:id` | Обновить стратегию |
| POST | `/strategies/:id/status` | Изменить статус (start/stop) |
| DELETE | `/strategies/:id` | Удалить (только stopped) |

### Новые эндпоинты для шаблонов

| Метод | Путь | Описание |
|-------|------|----------|
| GET | `/strategy-templates` | Список шаблонов |
| POST | `/strategy-templates` | Сохранить шаблон |
| DELETE | `/strategy-templates/:id` | Удалить шаблон |

---

## Components

### StrategiesPage

- При монтировании: `GET /strategies`, `GET /accounts`
- Рендерит список `StrategyCard`
- Внизу — кнопка-заглушка `+ Новая стратегия` (открывает `StrategyModal` в режиме создания)
- `StrategyModal` получает `onSave` → рефетч списка

### StrategyCard

**Шапка (всегда видна):**

| Поле | Откуда |
|------|--------|
| Символ, Long/Short | `strategy.symbol`, `strategy.direction` |
| Аккаунт | имя аккаунта по `account_id` |
| Grid X/Y | `activeFilledLevels / strategy.grid_levels` (из текущего цикла) |
| Объём USDT | сумма заполненных уровней × `order_size_usdt × lots` |
| P&L | `unrealisedPnl` из `GET /positions` (по symbol + account_id) — показывается при раскрытии карточки |
| Статус | `strategy.status` |

**Развёрнутый вид:**

1. **Сигналы** — горизонтальный ряд `SignalMiniCard` для каждого сигнала в `signal_configs`. MVP: только имя + параметры + статичный бейдж «Норма» (live-значения — будущий этап).

2. **Ждёт** — две колонки:
   - Левая: список уровней цикла (`strategy_levels`): ◉ ожидает / ✓ заполнен
   - Правая: карточки TP и SL — цена ордера + `%` до текущей цены

3. **Кнопки управления** — набор зависит от статуса:
   - `active`: Редактировать, Остановить, Завершить
   - `stopped`: Редактировать, Включить, Удалить

### SignalMiniCard

Props: `name: string`, `params: Record<string, number>`

Отображает:
- Бейдж направления (топ-лево): MVP — всегда «Норма»
- Иконка по категории (Тренд/Импульс/Объём/Волатильность)
- Имя сигнала + параметры
- Основное значение (MVP: `—`)

### StrategyModal

Открывается в двух режимах: `create` / `edit` (передаётся `strategy?: Strategy`).

**Строка шаблонов** (над вкладками):
- Дропдаун «Без шаблона» → список из `GET /strategy-templates`
- Выбор шаблона заполняет все три вкладки
- Кнопка «💾 Сохранить» → inline-поле ввода имени → `POST /strategy-templates` с текущими настройками

**Вкладка 1 — Вход:**

| Поле | Тип |
|------|-----|
| Символ | text input |
| Аккаунт | select (список из `GET /accounts`) |
| Направление | Long / Short toggle |
| Тип стратегии | Grid / DCA / Manual toggle |
| Плечо | number input |
| Объём 1 лота (USDT) | number input |
| Тип маржи | Isolated / Cross toggle |
| Хедж режим | Нет / Да toggle |

**Вкладка 2 — Усреднение:**

Таблица шагов (динамическая):

| # | Движение % | Лотов | Объём USDT (вычислен) |
|---|-----------|-------|----------------------|
| 1 | 1.0% | 1 | = lots × size |

- Кнопки `✕` на каждой строке, `+ Добавить шаг` снизу
- Объём USDT вычисляется автоматически: `lots × order_size_usdt` (read-only)
- Блок сигналов: теги с именем + параметрами, `+ Добавить`, выбор из предустановленного списка

**Вкладка 3 — Выход:**

- TP: `tp_pct`, режим (total / per level)
- SL: `sl_pct`, тип (conditional / market)
- Трейлинг-стоп: toggle вкл/выкл; при включении — `trailing_activation_pct`, `trailing_callback_pct`

**Футер:** кнопки «Отмена» и «Создать стратегию» / «Сохранить изменения»

---

## File Map

### Создать

| Файл | Содержимое |
|------|-----------|
| `frontend/src/pages/StrategiesPage.tsx` | Страница со списком карточек |
| `frontend/src/components/strategies/StrategyCard.tsx` | Карточка |
| `frontend/src/components/strategies/SignalMiniCard.tsx` | Мини-карточка сигнала |
| `frontend/src/components/strategies/StrategyModal.tsx` | Модалка (3 вкладки) |
| `frontend/src/components/strategies/TemplateSelector.tsx` | Строка шаблонов |
| `frontend/src/api/strategies.ts` | Клиент `/strategies` |
| `frontend/src/api/strategyTemplates.ts` | Клиент `/strategy-templates` |
| `migrations/007_strategy_extensions.sql` | ALTER TABLE + strategy_templates |
| `services/api-gateway/strategy_template_handler.go` | Хендлеры шаблонов |

### Изменить

| Файл | Изменение |
|------|-----------|
| `frontend/src/App.tsx` | Добавить роут `/strategies` |
| `frontend/src/components/Layout.tsx` | Добавить «Стратегии» в навигацию |
| `services/api-gateway/main.go` | Добавить роуты шаблонов |
| `services/api-gateway/strategy_handler.go` | Поддержка новых полей |
| `pkg/strategy/types.go` | Расширить struct Strategy |
| `pkg/trader/bybit.go` | Добавить SetLeverage, SetMarginType (вызываются движком при старте цикла, до первого ордера) |

---

## Out of Scope (MVP)

- Live-значения сигналов (требует отдельного signal API)
- Трейлинг-стоп в движке (только UI + хранение; исполнение — следующий этап)
- DCA и Manual типы стратегий (поле сохраняется, но движок пока работает только с Grid)
