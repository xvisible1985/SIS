# Bots Page — Design Spec (Iteration 1)
**Date:** 2026-05-19
**Status:** Approved

---

## Overview

Страница «Боты» (`/bots`) — каталог торговых ботов и управление собственными. Бот — это шаблон поверх стратегий: он хранит конфигурацию стратегии, список монет (whitelist/blacklist) и триггеры запуска. В итерации 1 триггеры только сохраняются, движок их исполнения — итерация 2.

---

## Scope (итерация 1)

**Входит:**
- DB схема и миграция
- Backend CRUD API для ботов
- Frontend: каталог, карточки, фильтры, форма создания/редактирования, деплой, форк
- Навигация (сайдбар + роут)

**Не входит (итерация 2+):**
- Trigger engine (оценка условий в реальном времени)
- Автоматическое создание/активация стратегий
- Hedge mode оркестрация
- Синхронизация обновлений от автора к подписчикам (в iter 1 — кнопка «Синхронизировать» без реализации)

---

## База данных

### Миграция `022_bots.sql`

```sql
CREATE TABLE bots (
    id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    owner_id         UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    source_bot_id    UUID REFERENCES bots(id) ON DELETE SET NULL,
    is_fork          BOOLEAN NOT NULL DEFAULT FALSE,

    name             TEXT NOT NULL,
    description      TEXT NOT NULL DEFAULT '',
    is_public        BOOLEAN NOT NULL DEFAULT FALSE,
    status           TEXT NOT NULL DEFAULT 'stopped'
                         CHECK (status IN ('active', 'stopped', 'draft')),

    symbol_whitelist TEXT[]  NOT NULL DEFAULT '{}',
    symbol_blacklist TEXT[]  NOT NULL DEFAULT '{}',

    triggers         JSONB   NOT NULL DEFAULT '[]',
    strategy_config  JSONB   NOT NULL DEFAULT '{}',

    deploy_count     INT     NOT NULL DEFAULT 0,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX bots_owner   ON bots (owner_id);
CREATE INDEX bots_source  ON bots (source_bot_id) WHERE source_bot_id IS NOT NULL;
CREATE INDEX bots_catalog ON bots (created_at DESC) WHERE is_public = TRUE;
```

### Логика source_bot_id / is_fork

| source_bot_id | is_fork | Тип |
|---|---|---|
| NULL | — | Оригинал / кастомный бот |
| SET | false | Подписка (linked copy) |
| SET | true | Форк (отвязан, редактируется свободно) |

### Структура triggers (JSONB)

```json
[
  { "type": "signal", "signal_id": "rsi", "condition": "buy" },
  { "type": "pnl",    "direction": "long", "threshold_pct": -5.0 }
]
```

В итерации 1 хранится как есть, не исполняется.

### Структура strategy_config (JSONB)

Те же поля что в таблице `strategies`:

```json
{
  "symbol": "BTCUSDT",
  "category": "linear",
  "direction": "long",
  "grid_levels": 5,
  "grid_active": 3,
  "grid_step_pct": 1.0,
  "grid_size_usdt": 100,
  "tp_mode": "total",
  "tp_pct": 2.0,
  "sl_type": "conditional",
  "sl_pct": 5.0,
  "signal_filter": false
}
```

---

## Backend API

### Файл `services/api-gateway/bots_handler.go`

Все роуты под `RequireAuth`.

| Метод | Путь | Действие |
|-------|------|----------|
| `GET` | `/bots` | Каталог + свои боты |
| `POST` | `/bots` | Создать своего бота |
| `GET` | `/bots/{id}` | Детали бота |
| `PATCH` | `/bots/{id}` | Обновить (только owner) |
| `DELETE` | `/bots/{id}` | Удалить (только owner) |
| `POST` | `/bots/{id}/deploy` | Подписаться на публичного бота |
| `POST` | `/bots/{id}/fork` | Отвязать подписку (`is_fork = true`) |
| `POST` | `/bots/{id}/start` | `status = active` |
| `POST` | `/bots/{id}/stop` | `status = stopped` |
| `POST` | `/bots/{id}/publish` | `is_public = true` (только owner) |

### GET /bots — параметры и ответ

Query params: `?tab=catalog|mine&q=&direction=long|short|both&sort=new|popular`

```json
{
  "catalog": [ ...Bot ],
  "mine":    [ ...Bot ]
}
```

Структура `Bot`:

```json
{
  "id":              "uuid",
  "name":            "BTC Grid Pro",
  "description":     "...",
  "ownerId":         "uuid",
  "ownerName":       "user@example.com",
  "isOwn":           true,
  "isPublic":        false,
  "status":          "active",
  "sourceBotId":     null,
  "isFork":          false,
  "symbolWhitelist": ["BTCUSDT"],
  "symbolBlacklist": [],
  "triggers":        [...],
  "strategyConfig":  {...},
  "deployCount":     42,
  "createdAt":       "2026-05-19T00:00:00Z"
}
```

`isOwn` = `owner_id == текущий пользователь из JWT`.

### POST /bots/{id}/deploy

- Проверяет что бот существует и `is_public = true`
- Создаёт новую запись: `owner_id = caller`, `source_bot_id = id`, `is_fork = false`, `status = stopped`
- Копирует `name`, `description`, `triggers`, `strategy_config` из источника
- Инкрементирует `deploy_count` у источника атомарно
- Возвращает созданный бот (201)

### POST /bots/{id}/fork

- Проверяет что вызывающий — owner бота
- Проверяет что `source_bot_id IS NOT NULL AND is_fork = false` (иначе 400)
- Устанавливает `is_fork = true`
- Возвращает обновлённый бот (200)

### PATCH /bots/{id}

Тело: любые из полей `name`, `description`, `is_public`, `symbol_whitelist`, `symbol_blacklist`, `triggers`, `strategy_config`. Только owner может обновлять. Подписчик (`source_bot_id IS NOT NULL AND is_fork = false`) не может редактировать — возвращает `403 {"error": "fork first"}`. Сначала нужен вызов `/fork`.

### DELETE /bots/{id}

Только owner. Мягкое удаление не нужно — каскадное.

---

## Frontend

### Структура файлов

```
src/
  pages/
    BotsPage.tsx
  features/
    bots/
      types.ts
      api.ts
      index.ts
      components/
        BotCard.tsx        ← свёрнутая + развёрнутая карточка (из handoff)
        BotFilters.tsx     ← фильтры каталога (из handoff)
        BotForm.tsx        ← форма создания/редактирования (из handoff)
        DeployModal.tsx    ← whitelist/blacklist + деплой (из handoff)
        TriggerList.tsx    ← отображение триггеров, только чтение (из handoff)
```

### types.ts

```ts
export type BotStatus = 'active' | 'stopped' | 'draft';

export type TriggerSignal = {
  type: 'signal';
  signal_id: string;
  condition: 'buy' | 'sell' | 'neutral';
};

export type TriggerPnl = {
  type: 'pnl';
  direction: 'long' | 'short';
  threshold_pct: number;
};

export type Trigger = TriggerSignal | TriggerPnl;

export type StrategyConfig = {
  symbol: string;
  category: string;
  direction: 'long' | 'short' | 'both';
  grid_levels: number;
  grid_active: number;
  grid_step_pct: number;
  grid_size_usdt: number;
  tp_mode: 'per_level' | 'total';
  tp_pct: number;
  sl_type: 'conditional' | 'programmatic';
  sl_pct: number;
  signal_filter: boolean;
};

export type Bot = {
  id: string;
  name: string;
  description: string;
  ownerId: string;
  ownerName: string;
  isOwn: boolean;
  isPublic: boolean;
  status: BotStatus;
  sourceBotId: string | null;
  isFork: boolean;
  symbolWhitelist: string[];
  symbolBlacklist: string[];
  triggers: Trigger[];
  strategyConfig: StrategyConfig;
  deployCount: number;
  createdAt: Date;
};

export type CreateBotInput = {
  name: string;
  description?: string;
  symbolWhitelist?: string[];
  symbolBlacklist?: string[];
  triggers?: Trigger[];
  strategyConfig?: Partial<StrategyConfig>;
};

export type BotFilters = {
  q: string;
  direction: 'all' | 'long' | 'short' | 'both';
  sort: 'new' | 'popular';
};

export type BotAction =
  | { type: 'start';   botId: string }
  | { type: 'stop';    botId: string }
  | { type: 'deploy';  botId: string }
  | { type: 'fork';    botId: string }
  | { type: 'publish'; botId: string }
  | { type: 'update';  botId: string; data: Partial<CreateBotInput> }
  | { type: 'delete';  botId: string }
  | { type: 'create';  data: CreateBotInput };
```

### api.ts — useBots

По паттерну проекта (без React Query):

```ts
function useBots() {
  const [catalog, setCatalog] = useState<Bot[]>([])
  const [mine, setMine]       = useState<Bot[]>([])
  const [loading, setLoading] = useState(true)

  const load = useCallback(async () => {
    setLoading(true)
    try {
      const res = await apiClient.get<{ catalog: RawBot[]; mine: RawBot[] }>('/bots')
      setCatalog(res.data.catalog.map(parseBot))
      setMine(res.data.mine.map(parseBot))
    } finally {
      setLoading(false)
    }
  }, [])

  useEffect(() => { load() }, [load])

  const action = useCallback(async (a: BotAction) => {
    switch (a.type) {
      case 'start':   await apiClient.post(`/bots/${a.botId}/start`); break
      case 'stop':    await apiClient.post(`/bots/${a.botId}/stop`); break
      case 'deploy':  await apiClient.post(`/bots/${a.botId}/deploy`); break
      case 'fork':    await apiClient.post(`/bots/${a.botId}/fork`); break
      case 'publish': await apiClient.post(`/bots/${a.botId}/publish`); break
      case 'update':  await apiClient.patch(`/bots/${a.botId}`, a.data); break
      case 'delete':  await apiClient.delete(`/bots/${a.botId}`); break
      case 'create':  await apiClient.post('/bots', a.data); break
    }
    await load()
  }, [load])

  return { catalog, mine, loading, action, refresh: load }
}
```

### BotsPage.tsx

- Две вкладки: «Каталог» / «Мои боты»
- `useBots()` тянет данные
- `BotFilters` для каталога (поиск, direction, sort)
- Сетка карточек `BotCard`
- Кнопка «Создать бота» открывает `BotForm`
- `DeployModal` для деплоя чужого бота

### BotCard — поведение

**Свёрнутая:**
- Название, автор (`ownerName`), статус-пилюля, кол-во деплоев
- Кнопка вкл/выкл (только для своих и подписок)
- Кнопка «Развернуть»

**Развёрнутая:**
- Описание
- Список триггеров (`TriggerList` — только чтение)
- Ключевые параметры стратегии (symbol, direction, grid_size_usdt, tp_pct, sl_pct)
- Кнопки (в зависимости от контекста):
  - Чужой публичный → «Задеплоить»
  - Подписка (is_fork=false) → «Форкнуть» + «Синхронизировать» (заглушка)
  - Форк или свой → «Редактировать» + «Опубликовать» (если не публичный)
  - Все свои/подписки → «Удалить»

### Изменения в существующих файлах

**`Sidebar.tsx`** — добавить в NAV:
```ts
{ to: '/bots', label: 'Боты', icon: Bot }
```

**`App.tsx`** — добавить роут:
```tsx
<Route path="bots" element={<BotsPage />} />
```

---

## Что НЕ входит в итерацию 1

- Trigger engine (оценка условий, запуск стратегий)
- Автоматическая синхронизация шаблона с подписчиками (кнопка есть, 501 Not Implemented)
- Рейтинг / лайки ботов
- Комментарии к ботам
- Hedge mode оркестрация
- История запусков бота

---

## Резюме изменений

| Слой | Файлы |
|------|-------|
| Миграция | `migrations/022_bots.sql` |
| Backend — новый | `services/api-gateway/bots_handler.go` |
| Backend — изменить | `services/api-gateway/main.go` (роуты) |
| Frontend — новые | `src/features/bots/**` (handoff + api.ts + types.ts) |
| Frontend — новая страница | `src/pages/BotsPage.tsx` |
| Frontend — изменить | `src/components/Sidebar/Sidebar.tsx` (пункт меню) |
| Frontend — изменить | `src/App.tsx` (роут /bots) |
