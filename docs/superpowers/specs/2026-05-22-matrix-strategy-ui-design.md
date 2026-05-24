# Matrix Strategy UI — Design Spec
Date: 2026-05-22

## Summary
Redesign of the Matrix (DCA) strategy configuration UI from a symmetric two-direction grid into
an independently-configurable two-sided level table with a zero-level entry row, per-level stop
management fields, and deposit-based volume sizing.

## Data Model

### MatrixLevel (per level above/below entry)
| Field | Type | Notes |
|---|---|---|
| `direction` | `"above" \| "below"` | Above = цена идёт против направления; Below = в сторону |
| `price_step_pct` | float | % от предыдущего уровня |
| `size_pct` | float | % от депозита |
| `stop_pct` | float \| null | Расстояние частичного стопа от ТВХ при заполнении |
| `stop_cond_pct` | float \| null | Условие перевыставления: цена ушла на X% от ТВХ |
| `stop_replace_pct` | float \| null | Новое расстояние стопа после перевыставления (отриц. = в прибыли) |
| `tp_pct` | float \| null | ТП % от ТВХ, закрывает ВСЮ позицию |

### MatrixEntryLevel (нулевой уровень L0)
Same fields except no `direction` and no `price_step_pct`.

### Strategy-level new fields
- `safe_zone_pct float` — зона после срабатывания SL, в которой новые ордера не выставляются
- `matrix_entry_level JSONB` — параметры нулевого уровня
- `grid_size_usdt` reused as deposit for matrix type (no DB rename)

## UI Layout (Tab 2 — Матрица)
- Global row: Депозит (USDT) | Safe-Zone от SL %
- Visualization strip: L(-n)…L(-1) ← ВХОД → L(1)…L(n) with legend labels
- **[+ Добавить уровень выше]** button
- Section header: "Выше точки входа"
- Levels L(-n) → L(-1) top-to-bottom (L(-1) closest to entry)
- **Golden-bordered zero level row L(0)**
- Levels L(1) → L(n)
- Section header: "Ниже точки входа"
- **[+ Добавить уровень ниже]** button
- Column headers with ? tooltip for each: Шаг% | Объём% | Стоп% | Усл% | Замена% | ТП%

## Filled Level Marking
Same mechanism as Grid strategy:
- `filledLevels` count fetched from `getStrategyState()` on modal open
- First N levels in the flattened order (above reversed + entry + below) are marked filled
- Filled rows: green tinted background, ✓ badge instead of level number, delete button disabled

## DB Migration
```sql
ALTER TABLE strategies
  ADD COLUMN IF NOT EXISTS safe_zone_pct float8,
  ADD COLUMN IF NOT EXISTS matrix_entry_level jsonb;
```

## Files Changed
1. `frontend/src/types.ts` — MatrixLevel, MatrixEntryLevel, StrategyFormData
2. `frontend/src/components/strategies/StrategyModal.tsx` — Tab 2 full redesign
3. `pkg/strategy/types.go` — MatrixLevel, MatrixEntryLevel structs
4. `pkg/strategy/engine.go` — scanStrategy() parse new fields
5. `services/api-gateway/strategy_handler.go` — create/update payload
6. `migrations/038_matrix_fields.sql` — new columns
