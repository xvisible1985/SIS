# Log Visualizer — UI улучшения: дата, фильтр, перетаскивание карточки

## Контекст

Дополнение к существующему Log Visualizer (`frontend/src/features/log-visualizer/`).
Три независимых UI улучшения без изменений бэкенда.

---

## Фича 1 — Дата в сайдбаре событий

**Файл:** `frontend/src/features/log-visualizer/LogVisualizerEventsList.tsx`

Функция `fmtTime` сейчас возвращает только `HH:MM:SS`. Заменяем на `DD.MM HH:MM:SS`.

```typescript
// было
function fmtTime(tsMs: number) {
  return new Date(tsMs).toLocaleTimeString('ru-RU', {
    hour: '2-digit', minute: '2-digit', second: '2-digit',
  })
}

// стало
function fmtTime(tsMs: number) {
  const d = new Date(tsMs)
  const date = d.toLocaleDateString('ru-RU', { day: '2-digit', month: '2-digit' })
  const time = d.toLocaleTimeString('ru-RU', { hour: '2-digit', minute: '2-digit', second: '2-digit' })
  return `${date} ${time}`
}
```

Результат: `02.06 14:23:11`. Год не показывается.

---

## Фича 2 — Фильтр событий

### Новый тип `EventListFilter`

В `types.ts`:

```typescript
export type EventListFilter = 'all' | 'orders' | 'closes' | 'errors'
```

### Новая функция `filterEventsList` в `utils.ts`

```typescript
export function filterEventsList(
  events: MergedEvent[],
  filter: EventListFilter,
): MergedEvent[] {
  if (filter === 'all') return events
  return events.filter(ev => {
    switch (filter) {
      case 'orders':
        return ev.kind === 'level'
      case 'closes':
        return (
          (ev.kind === 'level' && ev.level?.status === 'sl_closed') ||
          (ev.kind === 'log'   && /TP исполнен|SL сработал/i.test(ev.log?.message ?? ''))
        )
      case 'errors':
        return ev.kind === 'log' && ev.log?.level === 'error'
      default:
        return true
    }
  })
}
```

### Изменения `LogVisualizerEventsList`

**Props** — не меняются (фильтр хранится внутри компонента).

**Состояние:**
```typescript
const [filter, setFilter] = useState<EventListFilter>('all')
```

**Отображаемые события:**
```typescript
const filtered = filterEventsList(events, filter)
```

Счётчик в заголовке: `{currentIndex + 1}/{events.length}` → показываем только `total` (количество всех событий), не меняем логику `currentIndex` — она привязана к оригинальному массиву.

**UI — чипы под заголовком:**

```
┌─────────────────────────────────────────┐
│ СОБЫТИЯ  47/312                         │
│ [Все] [Ордера] [Закрытия] [Ошибки]     │
├─────────────────────────────────────────┤
│ • 02.06 14:23:11  ▲ L0 Buy @ 97421     │
│ • 02.06 14:25:33  Matrix SL выставлен  │
│ ...                                     │
```

Стиль чипов — аналогичен chip-элементам в `LogVisualizerLayersPopup`:
- Активный: `background: rgba(74,125,255,.15)`, `color: #b8c8ff`, `border: 1px solid rgba(74,125,255,.3)`
- Неактивный: `background: rgba(255,255,255,.04)`, `color: #475569`, `border: 1px solid transparent`
- Размер: `font-size: 10px`, `padding: 2px 8px`, `border-radius: 5px`

**Важно:** `currentIndex` и `onJump` работают с исходным `events`, не с `filtered`. Клик по событию в отфильтрованном списке вызывает `onJump(originalIdx)` — поэтому нужно отслеживать оригинальный индекс:

```typescript
filtered.map(ev => {
  const originalIdx = events.indexOf(ev)
  // ...
  onClick={() => onJump(originalIdx)}
})
```

---

## Фича 3 — Перетаскиваемая карточка стратегии

**Файл:** `frontend/src/features/log-visualizer/LogVisualizerStrategyCard.tsx`

### Изменения

1. Убрать `pointer-events-none` из `className` (карточка должна ловить события мыши).
2. Добавить `cursor: grab` / `grabbing`.
3. Добавить `useState` для позиции и `useRef` для элемента карточки.

### Состояние и логика

```typescript
const cardRef = useRef<HTMLDivElement>(null)
const [pos, setPos] = useState<{ x: number; y: number } | null>(null)
// null = позиция по умолчанию (bottom-4 right-4 через CSS)

function handleMouseDown(e: React.MouseEvent) {
  e.preventDefault()
  const card = cardRef.current
  if (!card) return
  const container = card.parentElement
  if (!container) return

  const cardRect = card.getBoundingClientRect()
  const containerRect = container.getBoundingClientRect()

  // При первом перетаскивании вычисляем начальную позицию из текущего рендера
  const startX = pos?.x ?? containerRect.width  - cardRect.width  - 16
  const startY = pos?.y ?? containerRect.height - cardRect.height - 16

  const offsetX = e.clientX - containerRect.left - startX
  const offsetY = e.clientY - containerRect.top  - startY

  const onMove = (me: MouseEvent) => {
    if (!card.parentElement) return
    const cr = card.parentElement.getBoundingClientRect()
    const x = Math.max(0, Math.min(cr.width  - card.offsetWidth,  me.clientX - cr.left - offsetX))
    const y = Math.max(0, Math.min(cr.height - card.offsetHeight, me.clientY - cr.top  - offsetY))
    setPos({ x, y })
  }

  const onUp = () => {
    document.removeEventListener('mousemove', onMove)
    document.removeEventListener('mouseup',   onUp)
  }

  document.addEventListener('mousemove', onMove)
  document.addEventListener('mouseup',   onUp)
}
```

### JSX

```tsx
<div
  ref={cardRef}
  onMouseDown={handleMouseDown}
  className="absolute z-10 backdrop-blur-sm select-none"
  style={pos
    ? { left: pos.x, top: pos.y, cursor: 'grab' }
    : { bottom: 16, right: 16, cursor: 'grab' }
  }
>
  {/* содержимое карточки без изменений */}
</div>
```

Позиция сбрасывается при перезагрузке страницы (не сохраняется в localStorage).

---

## Файловый план

| Файл | Действие |
|------|----------|
| `frontend/src/features/log-visualizer/types.ts` | Добавить `EventListFilter` |
| `frontend/src/features/log-visualizer/utils.ts` | Добавить `filterEventsList` |
| `frontend/src/__tests__/LogVisualizerTab.test.tsx` | Тесты для `filterEventsList` |
| `frontend/src/features/log-visualizer/LogVisualizerEventsList.tsx` | Дата + фильтр-чипы |
| `frontend/src/features/log-visualizer/LogVisualizerStrategyCard.tsx` | Drag & drop |

---

## Тесты

`filterEventsList` — unit-тесты в `LogVisualizerTab.test.tsx`:
- `'all'` возвращает все события
- `'orders'` возвращает только `kind === 'level'`
- `'closes'` возвращает `sl_closed` уровни + лог с "TP исполнен" + лог с "SL сработал"
- `'errors'` возвращает только `kind === 'log' && level === 'error'`
- Пустой массив → пустой результат для всех фильтров
