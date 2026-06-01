# Log Visualizer — Слои графика + Мини-карточка стратегии

## Контекст

Дополнение к существующей фиче Log Visualizer (вкладка «Визуализатор» в AdminPage).
Текущий код: `frontend/src/features/log-visualizer/`.
Бэкенд: `services/api-gateway/admin_log_visualizer_handler.go`.

---

## Фича 1 — Попап «Слои графика»

### LayerSettings

Новый тип в `types.ts`:

```typescript
export interface LayerSettings {
  showOrderMarkers: boolean   // стрелки ▲▼ для level-событий (Buy/Sell)
  showLogMarkers:   boolean   // кружки ● для log-событий
  showPriceLines:   boolean   // горизонтальные ценовые линии по filledPrice каждого уровня
  showInfo:         boolean   // фильтр: отображать log-события уровня info
  showWarn:         boolean   // фильтр: отображать log-события уровня warn
  showError:        boolean   // фильтр: отображать log-события уровня error
}

export const DEFAULT_LAYER_SETTINGS: LayerSettings = {
  showOrderMarkers: true,
  showLogMarkers:   true,
  showPriceLines:   false,
  showInfo:         true,
  showWarn:         true,
  showError:        true,
}
```

Фильтры `showInfo/showWarn/showError` применяются только когда `showLogMarkers = true`; если маркеры логов выключены, чипы выглядят задизабленными (opacity-40, не кликабельны).

### Компонент LogVisualizerLayersPopup

Новый файл `LogVisualizerLayersPopup.tsx`.

**Props:**
```typescript
interface Props {
  settings: LayerSettings
  onChange: (s: LayerSettings) => void
}
```

**UI:**
- Кнопка в тулбаре `LogVisualizerTab` — между селектом interval и кнопкой «▶ Загрузить»
- Иконка ≡ (lines), активный стейт (≥1 слой выключен) подсвечивается
- Клик открывает/закрывает попап; клик вне (useEffect + mousedown) — закрывает
- Попап: `absolute z-50` позиционируется под кнопкой, тёмный фон `#0d1220`, border `rgba(255,255,255,.10)`, border-radius 12px, ширина 220px
- Содержимое:
  - Заголовок «СЛОИ ГРАФИКА» (uppercase, 10px)
  - Ряд: «Маркеры ордеров» + toggle
  - Ряд: «Маркеры событий» + toggle
  - Ряд: «Ценовые линии» + toggle
  - Divider
  - Подзаголовок «УРОВЕНЬ ЛОГА» (uppercase, 10px)
  - Три чипа: `info` / `warn` / `error` (кликабельны, выделяются цветом когда активны; недоступны если showLogMarkers=false)

Toggle-элемент: pill-переключатель (28×15px), синий `#4a7dff` = on, серый = off.

Состояние `layerSettings` хранится в `LogVisualizerTab`.

### Изменения LogVisualizerChart

`LogVisualizerChart` получает дополнительный prop:
```typescript
layerSettings: LayerSettings
```

**Фильтрация маркеров** — существующий `useEffect([events])` расширяется до `useEffect([events, layerSettings])` (зависимость добавляется, логика внутри заменяется):
```typescript
const filteredEvents = events.filter(ev => {
  if (ev.kind === 'level') return layerSettings.showOrderMarkers
  if (ev.kind === 'log') {
    if (!layerSettings.showLogMarkers) return false
    const lvl = ev.log?.level
    if (lvl === 'info'  && !layerSettings.showInfo)  return false
    if (lvl === 'warn'  && !layerSettings.showWarn)  return false
    if (lvl === 'error' && !layerSettings.showError) return false
    return true
  }
  return true
})
markersRef.current.setMarkers(/* построенные из filteredEvents */)
```

**Ценовые линии** — отдельный `useEffect([events, layerSettings.showPriceLines])`:
- Хранятся в `priceLinesRef = useRef<IPriceLine[]>([])`
- При каждом вызове: удалить все старые `priceLinesRef.current.forEach(pl => series.removePriceLine(pl))`
- Если `showPriceLines = true`: создать по одной линии на каждый level-event из `events`:
  ```typescript
  series.createPriceLine({
    price: ev.level!.filledPrice,
    color: ev.level!.side === 'Buy' ? '#34d399' : '#f87171',
    lineWidth: 1,
    lineStyle: 2,          // dashed
    axisLabelVisible: true,
    title: `L${ev.level!.levelIdx}`,
  })
  ```
- При `showPriceLines = false` или чистке — просто обнуляем `priceLinesRef.current = []`

Cleanup в `useEffect([], [])` (mount/unmount) — уже существует, `chart.remove()` уберёт линии автоматически.

---

## Фича 2 — Мини-карточка стратегии

### Бэкенд

**`admin_log_visualizer_handler.go`:**

`lvStrategy` расширяется двумя полями:
```go
type lvStrategy struct {
  ID           string   `json:"id"`
  Symbol       string   `json:"symbol"`
  Direction    string   `json:"direction"`
  StrategyType string   `json:"strategyType"`
  Status       string   `json:"status"`
  GridLevels   int      `json:"gridLevels"`
  LastPnl      *float64 `json:"lastPnl"`  // null если нет завершённых циклов
}
```

Запрос в `LVGetStrategies` расширяется подзапросом:
```sql
SELECT id, symbol, direction, strategy_type, status, grid_levels,
  (SELECT realized_pnl FROM strategy_cycles
   WHERE strategy_id = s.id AND ended_at IS NOT NULL
   ORDER BY cycle_num DESC LIMIT 1) AS last_pnl
FROM strategies s
WHERE account_id = $1
ORDER BY created_at DESC
```

Scan: `rows.Scan(&s.ID, &s.Symbol, &s.Direction, &s.StrategyType, &s.Status, &s.GridLevels, &s.LastPnl)`

### Frontend: types.ts

`LVStrategy` расширяется:
```typescript
export interface LVStrategy {
  id:           string
  symbol:       string
  direction:    string
  strategyType: string
  status:       string
  gridLevels:   number
  lastPnl:      number | null
}
```

### Компонент LogVisualizerStrategyCard

Новый файл `LogVisualizerStrategyCard.tsx`.

**Props:**
```typescript
interface Props {
  strategy:      LVStrategy   // содержит strategy.lastPnl напрямую
  visibleEvents: MergedEvent[]
}
```

Компонент самостоятельно вычисляет:
```typescript
const filledLevels = visibleEvents.filter(ev => ev.kind === 'level')
const filledCount  = filledLevels.length
const volumeUsdt   = filledLevels.reduce(
  (sum, ev) => sum + parseFloat(ev.level!.qty) * ev.level!.filledPrice,
  0
)
```

**Макет (абсолютное позиционирование):**
```
┌─────────────────────────────┐
│ BTCUSDT              [LONG] │  ← symbol + badge (зелёный Long, красный Short)
│ matrix · active             │  ← strategyType · status (10px, slate-500)
│                             │
│ Взято ордеров   Объём       │
│ 7 / 12          840.20 $    │  ← анимируется по событиям
├─────────────────────────────┤
│ Last PnL стратегии          │
│ +124.50 $                   │  ← статичный, зелёный/красный/серый
└─────────────────────────────┘
```

- Позиция: `absolute bottom-4 right-4` — внутри chart-wrapper div (должен быть `relative`)
- Стиль: `bg-[rgba(6,6,12,0.85)] border border-white/10 rounded-xl p-3 w-[200px] backdrop-blur-sm`
- Не кликабельна, не коллапсибельна
- Если `lastPnl === null` — строку «Last PnL» скрываем (нет данных)
- Форматирование PnL: `(lastPnl >= 0 ? '+' : '') + lastPnl.toFixed(2) + ' $'`

### Интеграция в LogVisualizerTab

```tsx
{/* Chart area — добавить relative */}
<div className="flex-1 relative min-w-0">
  <LogVisualizerChart
    candles={visibleCandles}
    events={visibleEvents}
    layerSettings={layerSettings}
  />
  {hasData && strategy && (
    <LogVisualizerStrategyCard
      strategy={strategy}
      visibleEvents={visibleEvents}
    />
  )}
</div>
```

Где `strategy` — `strategies.find(s => s.id === strategyId) ?? null`.

---

## Файловый план

| Статус   | Файл                                                              | Действие                                          |
|----------|-------------------------------------------------------------------|---------------------------------------------------|
| НОВЫЙ    | `frontend/src/features/log-visualizer/LogVisualizerLayersPopup.tsx` | Попап со слоями                                 |
| НОВЫЙ    | `frontend/src/features/log-visualizer/LogVisualizerStrategyCard.tsx` | Мини-карточка стратегии                        |
| ИЗМЕНИТЬ | `frontend/src/features/log-visualizer/types.ts`                   | `LayerSettings`, `DEFAULT_LAYER_SETTINGS`, расширение `LVStrategy` |
| ИЗМЕНИТЬ | `frontend/src/features/log-visualizer/LogVisualizerChart.tsx`     | prop `layerSettings`, фильтрация маркеров, ценовые линии |
| ИЗМЕНИТЬ | `frontend/src/features/log-visualizer/LogVisualizerTab.tsx`       | `layerSettings` state, рендер новых компонентов, `relative` на chart-div |
| ИЗМЕНИТЬ | `services/api-gateway/admin_log_visualizer_handler.go`            | `lvStrategy` + `LVGetStrategies` запрос          |

---

## Тесты

- `LogVisualizerStrategyCard.test.tsx` — unit-тесты: filledCount, volumeUsdt при разных visibleEvents; рендер без lastPnl; формат PnL со знаком
- `LogVisualizerChart` — тест фильтрации маркеров: вынести логику фильтрации в чистую функцию `filterEvents(events, settings)` и тестировать её: mix level/log/warn/error events + разные `LayerSettings`, проверить что результат соответствует ожидаемому набору
