# Bot Type Picker Wiring — Design Spec

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Подключить существующий `BotTypePickerModal` в flow создания бота, переименовать тип `trend` → `signal` (SignalBot), сделать ParserBot и HedgeBot видимыми но недоступными.

**Architecture:** Минимальное изменение — добавляем один шаг между кнопкой «Создать бота» и формой `BotForm`. Пикер уже существует, нужно только его подключить и обновить метаданные типов.

**Tech Stack:** React, TypeScript. Только фронтенд, бэкенд не затрагивается.

---

## User Flow

```
Кнопка «Создать бота» (MyBotsSection)
  → BotTypePickerModal открывается
      ├── SignalBot (активный, индиго) → закрыть пикер → открыть BotForm с bot_kind='signal'
      ├── ParserBot (disabled, серый, badge «Скоро») → клик игнорируется
      └── HedgeBot  (disabled, серый, badge «Скоро») → клик игнорируется
  → Escape / клик вне → закрыть пикер, ничего не открывать
```

Редактирование существующего бота (`onEditBot`) — пикер **не показывается**, `BotForm` открывается напрямую как сейчас.

---

## Изменения по файлам

### 1. `frontend/src/features/bots/types.ts`

Переименовать значение `'trend'` → `'signal'` в union-типе:

```ts
// Было:
export type BotKind = 'trend' | 'parser' | 'hedge';

// Стало:
export type BotKind = 'signal' | 'parser' | 'hedge';
```

### 2. `frontend/src/features/bots/botKindMeta.ts`

- Переименовать ключ словаря `'trend'` → `'signal'`
- Обновить `BOT_KINDS = ['signal', 'parser', 'hedge']`
- Обновить `label: 'SignalBot'` (было `'TrendBot'`)
- Добавить поле `disabled?: boolean` в интерфейс `BotKindMeta`
- Поставить `disabled: true` для `'parser'` и `'hedge'`
- Обновить `getBotKindMeta`: fallback для неизвестных значений (в т.ч. старого `'trend'`) → возвращать метаданные `'signal'`

```ts
export interface BotKindMeta {
  // ...existing fields...
  disabled?: boolean;   // если true — карточка недоступна
}

export const BOT_KINDS: BotKind[] = ['signal', 'parser', 'hedge']

export const BOT_KIND_META: Record<BotKind, BotKindMeta> = {
  signal: {
    id: 'signal',
    label: 'SignalBot',
    tagline: 'Следует за рыночным трендом',
    // ...цвета остаются прежними (индиго/blue-400)...
  },
  parser: {
    // ...существующие данные...
    disabled: true,
  },
  hedge: {
    // ...существующие данные...
    disabled: true,
  },
}

export function getBotKindMeta(kind: BotKind | string | undefined): BotKindMeta {
  return BOT_KIND_META[kind as BotKind] ?? BOT_KIND_META['signal']
}
```

### 3. `frontend/src/features/bots/components/BotTypePickerModal.tsx`

Обновить иконку для `'signal'` (была `TrendingUp` на ключе `'trend'`). Добавить disabled-состояние карточек.

Обновить `KIND_ICONS`: переименовать ключ `trend` → `signal` (иконка остаётся `TrendingUp`):

```tsx
const KIND_ICONS: Record<BotKind, ...> = {
  signal: TrendingUp,
  parser: Search,
  hedge:  Shield,
}
```

**Disabled-карточка** (когда `m.disabled === true`):
- `cursor: not-allowed`, `pointer-events: none` на кнопке выбора
- opacity `0.38` на всей карточке
- Badge «Скоро» в правом верхнем углу шапки (в акцентном цвете типа, opacity 0.7)
- Кнопка внизу — текст «В разработке» вместо «Выбрать X →»
- `onClick` — `undefined` (не вызывать `onSelect`)

**Активная карточка** — без изменений в логике, только ключ `'trend'` → `'signal'` в `KIND_ICONS`.

### 4. `frontend/src/pages/BotsPage.tsx`

Добавить state для пикера и изменить `handleCreate`:

```tsx
const [showTypePicker, setShowTypePicker] = useState(false)

// Было:
const handleCreate = () => {
  setEditBot(null)
  setFormMode('create')
}

// Стало:
const handleCreate = () => {
  setShowTypePicker(true)
}

const handleTypeSelect = (kind: BotKind) => {
  setShowTypePicker(false)
  setEditBot(null)
  setFormMode('create')
  setInitialKind(kind)   // новый state
}
```

Добавить state `initialKind: BotKind | null`:

```tsx
const [initialKind, setInitialKind] = useState<BotKind | null>(null)

const handleTypeSelect = (kind: BotKind) => {
  setShowTypePicker(false)
  setInitialKind(kind)
  setEditBot(null)
  setFormMode('create')
}

// При закрытии формы сбрасывать оба state:
const handleFormClose = () => {
  setFormMode(null)
  setEditBot(null)
  setInitialKind(null)
}
```

В JSX: пикер и форма с `initialKind`:

```tsx
{showTypePicker && (
  <BotTypePickerModal
    onSelect={handleTypeSelect}
    onClose={() => setShowTypePicker(false)}
  />
)}

{formMode !== null && (
  <BotForm
    bot={editBot ?? undefined}
    initialKind={initialKind ?? 'signal'}
    onSubmit={handleFormSubmit}
    onClose={handleFormClose}
  />
)}
```

### 5. `frontend/src/features/bots/components/BotForm.tsx`

Принять `initialKind?: BotKind` в `Props` и использовать в `defaultConfig`:

```ts
// defaultConfig получает kind и проставляет bot_kind в начальный конфиг
function defaultConfig(bot?: BotType, kind?: BotKind): StrategyConfig {
  const s = bot?.strategyConfig ?? {}
  return {
    bot_kind: s.bot_kind ?? kind ?? 'signal',
    // ...rest unchanged...
  }
}
```

---

## Обратная совместимость

Существующие боты могут хранить `strategyConfig.bot_kind = 'trend'` (старый ключ) или `undefined`. `getBotKindMeta` с fallback → `'signal'` покрывает оба случая. Никаких миграций не нужно — данные в JSONB, бэкенд их просто хранит.

---

## Что не входит в scope

- Формы ParserBot и HedgeBot — отдельные задачи
- Backend-валидация `bot_kind` — не нужна сейчас
- Отображение `bot_kind` в карточках `MyBotCard` — отдельная задача
