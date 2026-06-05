# Bot Approval Flow — Design Spec

## Goal

Добавить систему согласования публикации ботов: пользователь может опубликовать бота только после того, как тот накопил заданное количество активных дней и получил одобрение от администратора.

## Architecture

Новый lifecycle для ботов пользователя: накопление активного времени → заявка на согласование → одобрение/отклонение → публикация. Логика блокировки правок и переключателя `isPublic` — на бэкенде. Порог дней настраивается в admin defaults.

## Tech Stack

Go (pgx/v5, chi), PostgreSQL, React + TypeScript, Tailwind CSS

---

## State Machine

```
[Создан]
   │ StartBot
   ▼
[Активен] — накапливает active_seconds_acc
   │ strategy_config locked (PATCH → 422)
   │ StopBot
   ▼
[Остановлен] — таймер на паузе
   │ PATCH strategy_config → active_seconds_acc = 0
   │ StartBot
   ▲
   └──────────── (цикл пока не накопится порог)

[Порог: active_seconds_acc >= min_publish_days * 86400]
   │ POST /bots/{id}/request-approval
   ▼
[approval_status = 'pending'] — в блоке «На согласование» у админа
   │ admin reject              │ admin approve
   ▼                           ▼
[rejected]                 [approved]
   │ переотправить             │ пользователь нажимает «Опубликовать»
   └──► [pending] снова        ▼
                           [is_public = true]
```

**Правила:**
- `strategy_config` locked пока `status = 'active'`. Другие поля (name, description, avatar, autoMode) редактируются свободно.
- Редактирование `strategy_config` пока бот остановлен → `active_seconds_acc = 0`.
- После отклонения — немедленная переотправка без сброса таймера.
- `PublishBot` (`POST /bots/{id}/publish`) → 422, если `approval_status != 'approved'`.

---

## Data Model

### Миграция (064_bot_approval.sql)

```sql
ALTER TABLE bots
  ADD COLUMN IF NOT EXISTS active_seconds_acc BIGINT      NOT NULL DEFAULT 0,
  ADD COLUMN IF NOT EXISTS active_since       TIMESTAMPTZ,
  ADD COLUMN IF NOT EXISTS approval_status    TEXT
    CHECK (approval_status IN ('pending', 'approved', 'rejected'));

ALTER TABLE coin_filter_settings
  ADD COLUMN IF NOT EXISTS min_publish_days INTEGER NOT NULL DEFAULT 15;
```

**Семантика полей:**
- `active_seconds_acc` — накопленное активное время в секундах (всего, за всё время).
- `active_since` — когда бот последний раз стал активным (`NULL` если не активен).
- `approval_status` — `NULL` (ещё не подавал) / `'pending'` / `'approved'` / `'rejected'`.
- `min_publish_days` — минимум дней активности для подачи на согласование (глобальный, один на всех).

---

## Backend

### Новые/изменённые поля в `botResp`

```go
ActiveSecondsAcc int64   `json:"activeSecondsAcc"`
ActiveSince      *string `json:"activeSince"`   // ISO timestamp or null
ApprovalStatus   *string `json:"approvalStatus"` // null | "pending" | "approved" | "rejected"
```

### Изменения в существующих эндпоинтах

#### `StartBot` (`POST /bots/{id}/start`)
После текущей blacklist-проверки, перед `setBotStatus`:
```sql
UPDATE bots SET active_since = NOW(), updated_at = NOW()
WHERE id = $1 AND owner_id = $2
```

#### `StopBot` (`POST /bots/{id}/stop`)
Перед `setBotStatus`:
```sql
UPDATE bots
SET active_seconds_acc = active_seconds_acc
      + EXTRACT(EPOCH FROM NOW() - active_since)::BIGINT,
    active_since = NULL,
    updated_at = NOW()
WHERE id = $1 AND owner_id = $2 AND active_since IS NOT NULL
```

#### `PatchBot` (`PATCH /bots/{id}`)
В начале обработки, до применения патча:
1. Проверить, меняет ли запрос поле `strategyConfig`.
2. Если меняет и бот `status = 'active'` → 422 `"Нельзя изменить стратегию активного бота"`.
3. Если меняет и бот `status != 'active'` → добавить в UPDATE: `active_seconds_acc = 0`.

#### `PublishBot` (`POST /bots/{id}/publish`)
Перед `UPDATE bots SET is_public = true`:
```sql
SELECT approval_status FROM bots WHERE id = $1 AND owner_id = $2
```
Если `approval_status != 'approved'` → 422 `"Бот не прошёл согласование"`.

#### `GetCoinFilter` / `UpdateCoinFilter`
Добавить `min_publish_days int` в `coinFilterSettings` struct, SELECT/UPDATE.

### Новые эндпоинты

#### `POST /bots/{id}/request-approval` (RequireAuth)
```
1. SELECT active_seconds_acc, active_since, approval_status
   FROM bots WHERE id=$1 AND owner_id=$2 AND is_official=false
2. Если не найден → 404
3. Посчитать effective_secs = active_seconds_acc
   + CASE WHEN active_since IS NOT NULL
         THEN EXTRACT(EPOCH FROM NOW()-active_since)::BIGINT ELSE 0 END
4. SELECT min_publish_days FROM coin_filter_settings WHERE id=1
5. Если effective_secs < min_publish_days*86400 → 422
   "Недостаточно активных дней: X из Y"
6. UPDATE bots SET approval_status='pending', updated_at=NOW()
   WHERE id=$1 AND owner_id=$2
7. → 204
```

#### `POST /admin/bots/{id}/approve` (RequireAdmin)
```sql
UPDATE bots SET approval_status = 'approved', updated_at = NOW()
WHERE id = $1
```
→ 204

#### `POST /admin/bots/{id}/reject` (RequireAdmin)
```sql
UPDATE bots SET approval_status = 'rejected', updated_at = NOW()
WHERE id = $1
```
→ 204

### Маршруты (main.go)

```go
// RequireAuth group
r.Post("/bots/{id}/request-approval", s.RequestBotApproval)

// RequireAdmin group
r.Post("/admin/bots/{id}/approve", s.ApproveBotPublication)
r.Post("/admin/bots/{id}/reject",  s.RejectBotPublication)
```

---

## Frontend

### Типы (`frontend/src/features/bots/types.ts`)

В `Bot`:
```typescript
activeSecondsAcc: number;
activeSince: string | null;   // ISO timestamp or null
approvalStatus: 'pending' | 'approved' | 'rejected' | null;
```

В `CreateBotInput` — без изменений (эти поля не задаются при создании).

### `frontend/src/features/admin-defaults/types.ts`

`CoinFilterSettings`:
```typescript
min_publish_days: number;
```

### `frontend/src/features/admin-defaults/api.ts`

`getCoinFilter` и `updateCoinFilter` уже работают с `CoinFilterSettings` — обновить дефолт:
```typescript
_coinFilterCache = { ...d, blacklist: d.blacklist ?? [], min_publish_days: d.min_publish_days ?? 15 }
```

### `AdminDefaultsTab.tsx` — новая секция «Публикация ботов»

Новый компонент `PublicationSection` (по аналогии с `CoinFilterSection`):
- NumInput для `min_publish_days` (целые числа, min=1)
- Кнопка «Сохранить»

Рендерится после `CoinFilterSection`.

### `MyBotCard.tsx` — новые элементы

Вычислить `effectiveSecs` и `progressPct`:
```typescript
const effectiveSecs = bot.activeSecondsAcc
  + (bot.activeSince ? (Date.now() - new Date(bot.activeSince).getTime()) / 1000 : 0)
const thresholdSecs = minPublishDays * 86400
const progressPct = Math.min(100, (effectiveSecs / thresholdSecs) * 100)
const daysActive = Math.floor(effectiveSecs / 86400)
const thresholdReached = effectiveSecs >= thresholdSecs
```

`minPublishDays` передаётся как prop в `MyBotCard` (родитель `MyBotsSection` грузит его через `getCoinFilter`).

**Прогресс-бар** (показывать пока `approvalStatus` не `'approved'`):
```tsx
<div className="h-1 rounded-full bg-white/[.06]">
  <div className="h-1 rounded-full bg-blue-500" style={{ width: `${progressPct}%` }} />
</div>
<span>{daysActive} / {minPublishDays} дн.</span>
```

**Кнопки под прогресс-баром:**
- `approvalStatus === null && thresholdReached` → кнопка «Отправить на согласование»
- `approvalStatus === 'pending'` → badge «На рассмотрении 🕐»
- `approvalStatus === 'approved'` → badge «Одобрен ✓» (зелёный)
- `approvalStatus === 'rejected'` → кнопка «Отклонён — переотправить»

**`isPublic` toggle (в BotForm):**
- `disabled` если `approvalStatus !== 'approved'`
- Tooltip: «Требуется одобрение администратора»

**Settings кнопка (в MyBotCard):**
- Когда `bot.status === 'active'`: кнопка не дизейблится, но BotForm показывает предупреждение о блокировке `strategy_config`.

**BotForm — блокировка `strategy_config` вкладок:**
- Если `bot.status === 'active'`: вкладки «Стратегия» / «Триггеры» / «Сигналы» заблокированы (pointer-events-none + overlay с текстом «Остановите бота для изменения стратегии»).
- Если `bot.status !== 'active'` и `bot.activeSecondsAcc > 0`: предупреждение «Изменение стратегии сбросит таймер активности».

### `AdminBotsTab.tsx` — блок «На согласование»

Над основной сеткой ботов, если есть pending-боты:
```tsx
{pendingBots.length > 0 && (
  <section>
    <h3>На согласование <span>{pendingBots.length}</span></h3>
    <div className="grid ...">
      {pendingBots.map(bot => (
        <AdminBotCard bot={bot} onApprove={...} onReject={...} ... />
      ))}
    </div>
  </section>
)}
```

`AdminBotCard` получает опциональные `onApprove` / `onReject` коллбэки (показываются только для pending ботов).

### `admin-bots/api.ts`

Добавить функции:
```typescript
approveBot(botId: string): Promise<void>
rejectBot(botId: string): Promise<void>
```

### `bots/api.ts` (или существующий файл API пользователя)

Добавить:
```typescript
requestBotApproval(botId: string): Promise<void>
  // POST /bots/{id}/request-approval
```

`parseBot` обновить: добавить `activeSecondsAcc`, `activeSince`, `approvalStatus`.

---

## File Map

| Action | Path |
|--------|------|
| Create | `migrations/064_bot_approval.sql` |
| Create | `services/api-gateway/bot_approval_handler.go` |
| Modify | `services/api-gateway/bots_handler.go` |
| Modify | `services/api-gateway/coin_filter_handler.go` |
| Modify | `services/api-gateway/main.go` |
| Modify | `frontend/src/features/bots/types.ts` |
| Modify | `frontend/src/features/admin-defaults/types.ts` |
| Modify | `frontend/src/features/admin-defaults/api.ts` |
| Modify | `frontend/src/features/admin-defaults/AdminDefaultsTab.tsx` |
| Modify | `frontend/src/features/bots/components/MyBotCard.tsx` |
| Modify | `frontend/src/features/bots/components/BotForm.tsx` |
| Modify | `frontend/src/features/bots/sections/MyBotsSection.tsx` |
| Modify | `frontend/src/features/bots/api.ts` |
| Modify | `frontend/src/features/admin-bots/api.ts` |
| Modify | `frontend/src/features/admin-bots/AdminBotCard.tsx` |
| Modify | `frontend/src/features/admin-bots/AdminBotsTab.tsx` |
