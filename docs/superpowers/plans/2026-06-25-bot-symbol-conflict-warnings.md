# Bot Symbol Conflict Warnings — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Предупреждать пользователя о пересечениях символов между whitelist'ами разных активных ботов — три варианта: в пикере монет, при сохранении настроек (бэкенд), и на карточке бота.

**Architecture:** Вариант 1 — фронтенд: добавить проп `takenSymbols` в CoinMultiPicker, вычислять из списка активных ботов в форме. Вариант 2 — бэкенд Go: проверять пересечения whitelist'ов в PatchBot, возвращать `warnings[]` в ответе; форма показывает предупреждение и остаётся открытой. Вариант 3 — UX: значок конфликта ⚡ на BotTerminalCard в панели ботов.

**Tech Stack:** React/TypeScript (frontend), Go + pgx (backend), PostgreSQL array overlap (`&&`)

---

## File Map

| Файл | Что меняем |
|------|-----------|
| `frontend/src/components/common/CoinMultiPicker.tsx` | новый проп `takenSymbols`, отображение ⚡ в чипах и дропдауне |
| `frontend/src/features/bots/components/HedgeBotForm.tsx` | принять + передать `takenSymbols` в CoinMultiPicker |
| `frontend/src/features/bots/components/MatrixBotForm.tsx` | принять + передать `takenSymbols` в CoinMultiPicker |
| `frontend/src/pages/BotsPage.tsx` | вычислять `takenSymbols` из `mine`, передавать в формы; обрабатывать warnings из ответа |
| `frontend/src/pages/TerminalPage.tsx` | inline onSubmit в TerminalBotsTab, conflictingBotIds, hasConflict prop |
| `frontend/src/features/bots/api.ts` | action('update') возвращает данные ответа |
| `services/api-gateway/bots_handler.go` | `checkWhitelistConflicts()`, patchBotWithWarningsResp, вызов в PatchBot |

---

## Task 1: CoinMultiPicker — конфликтные подсказки (фронтенд)

**Files:**
- Modify: `frontend/src/components/common/CoinMultiPicker.tsx` (строки 145–151, 357–368, 660–680)
- Modify: `frontend/src/features/bots/components/HedgeBotForm.tsx` (Props тип + строки 691–694)
- Modify: `frontend/src/features/bots/components/MatrixBotForm.tsx` (Props тип + строки 449)
- Modify: `frontend/src/pages/BotsPage.tsx` (строки 88–133)

- [ ] **Step 1: Добавить проп `takenSymbols` в CoinMultiPicker**

В `frontend/src/components/common/CoinMultiPicker.tsx`, интерфейс Props (строки 145–151):

```typescript
interface Props {
  values: string[]
  onChange: (v: string[]) => void
  color?: 'blue' | 'red'
  placeholder?: string
  takenSymbols?: Map<string, string>  // symbol → "Имя бота"
}

export function CoinMultiPicker({ values, onChange, color = 'blue', placeholder = 'Добавить монету...', takenSymbols }: Props) {
```

- [ ] **Step 2: Показать ⚡ в чипах выбранных монет**

В `CoinMultiPicker.tsx`, в блоке chip display (строка ~363), после существующей проверки `flaggedMap.has(val)`:

```tsx
{/* Добавить сразу после флага flaggedMap — строка ~364 */}
{!isPattern(val) && takenSymbols?.has(val) && (
  <span title={`Конфликт: ${takenSymbols.get(val)}`} className="text-orange-400 text-[9px] cursor-help">⚡</span>
)}
```

Блок целиком выглядит так (замена чипа, строки ~357–367):
```tsx
<span key={val}
  className={`inline-flex items-center gap-1 rounded-md border px-2 py-0.5 font-mono text-[11px] font-semibold ${isPattern(val) ? patternTagCls : coinTagCls}`}
  onClick={e => e.stopPropagation()}>
  {isPattern(val) ? <Asterisk size={9} className="opacity-70" /> : <CoinIcon symbol={val} className="w-3 h-3" />}
  {val.replace(/USDT$/i, '')}
  {!isPattern(val) && flaggedMap.has(val) && (
    <span title={flaggedMap.get(val)} className="text-amber-400 text-[9px] cursor-help">⚠️</span>
  )}
  {!isPattern(val) && takenSymbols?.has(val) && (
    <span title={`Конфликт: ${takenSymbols.get(val)}`} className="text-orange-400 text-[9px] cursor-help">⚡</span>
  )}
  <button type="button" onClick={() => remove(val)} className="opacity-60 hover:opacity-100"><X size={9} /></button>
</span>
```

- [ ] **Step 3: Показать ⚡ в списке дропдауна**

В `CoinMultiPicker.tsx`, в блоке coin list (строка ~668), добавить после `flaggedMap.has(row.symbol)`:

```tsx
{/* В блоке <span className="flex-1 text-left"> — после флага flaggedMap */}
{takenSymbols?.has(row.symbol) && (
  <span title={`Конфликт: ${takenSymbols.get(row.symbol)}`} className="text-orange-400 text-[10px] cursor-help ml-1">⚡</span>
)}
```

Строки ~660–680 целиком:
```tsx
<button key={row.symbol} type="button" onClick={() => toggle(row.symbol)}
  className={`flex w-full items-center gap-2.5 rounded-lg px-2.5 py-1.5 transition-colors hover:bg-white/[.04] ${selected ? 'bg-white/[.035]' : ''}`}>
  <CoinIcon symbol={row.symbol} className="w-[18px] h-[18px] shrink-0" />
  <span className="flex-1 text-left">
    <span className="text-[12px] font-semibold text-slate-200">{base}</span>
    <span className="ml-1 text-[10px] text-slate-500">USDT</span>
    {flaggedMap.has(row.symbol) && (
      <span title={flaggedMap.get(row.symbol)} className="ml-1 text-amber-400 text-[10px] cursor-help">⚠️</span>
    )}
    {takenSymbols?.has(row.symbol) && (
      <span title={`Конфликт: ${takenSymbols.get(row.symbol)}`} className="text-orange-400 text-[10px] cursor-help ml-1">⚡</span>
    )}
  </span>
  <span className={`text-[10px] font-mono ${pos ? 'text-emerald-400' : 'text-rose-400'}`}>
    {pos ? '+' : ''}{row.change.toFixed(2)}%
  </span>
  {selected && (
    <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="#5b8cff" strokeWidth="2.5" strokeLinecap="round" strokeLinejoin="round">
      <polyline points="20 6 9 17 4 12" />
    </svg>
  )}
</button>
```

- [ ] **Step 4: Добавить `takenSymbols` в HedgeBotForm**

В `frontend/src/features/bots/components/HedgeBotForm.tsx`:

Props тип (строка ~17):
```typescript
type Props = {
  bot?: BotType;
  onSubmit: (data: CreateBotInput) => Promise<{ warnings?: string[] } | void>;
  onClose: () => void;
  mode?: 'user' | 'admin';
  takenSymbols?: Map<string, string>;
};
```

Деструктуризация (строка ~147):
```typescript
export function HedgeBotForm({ bot, onSubmit, onClose, mode = 'user', takenSymbols }: Props) {
```

Передать в CoinMultiPicker whitelist (строки ~691–695):
```tsx
<CoinMultiPicker
  values={whitelist}
  onChange={setWhitelist}
  color="blue"
  placeholder="Выбрать монеты для whitelist..."
  takenSymbols={takenSymbols}
/>
```

- [ ] **Step 5: Добавить `takenSymbols` в MatrixBotForm**

В `frontend/src/features/bots/components/MatrixBotForm.tsx`:

Props тип (строка ~15):
```typescript
type Props = {
  bot?: BotType;
  onSubmit: (data: CreateBotInput) => Promise<{ warnings?: string[] } | void>;
  onClose: () => void;
  mode?: 'user' | 'admin';
  takenSymbols?: Map<string, string>;
};
```

Деструктуризация (строка ~95):
```typescript
export function MatrixBotForm({ bot, onSubmit, onClose, mode = 'user', takenSymbols }: Props) {
```

Передать в CoinMultiPicker whitelist (строка ~449):
```tsx
<CoinMultiPicker values={whitelist} onChange={setWhitelist} takenSymbols={takenSymbols} />
```

- [ ] **Step 6: Вычислять takenSymbols в BotsPage**

В `frontend/src/pages/BotsPage.tsx`, добавить после строки `const { catalog, mine, loading, action, refresh: load } = useBots()`:

```typescript
const takenSymbols = useMemo((): Map<string, string> => {
  const map = new Map<string, string>()
  for (const b of mine) {
    if (b.status !== 'active') continue
    for (const sym of b.symbolWhitelist) {
      if (!sym.includes('*') && !map.has(sym)) {
        map.set(sym, b.name)
      }
    }
  }
  return map
}, [mine])
```

Важно: `takenSymbols` включает все активные боты, включая текущий редактируемый — бот видит собственные символы без конфликта. Это нормально, т.к. его символы уже выбраны. Если нужно исключить текущего бота, заменить `for (const b of mine)` на `for (const b of mine.filter(b => b.id !== editBot?.id))`. Включаем текущего — симплициальная логика: если у бота уже есть этот символ, он выбран и иконка не показывается.

Передать `takenSymbols` в формы (строки ~162–183):
```tsx
{formMode !== null && selectedKind === 'hedge' && (
  <HedgeBotForm
    bot={editBot ?? undefined}
    takenSymbols={takenSymbols}
    onSubmit={handleFormSubmit}
    onClose={handleFormClose}
  />
)}

{formMode !== null && selectedKind === 'matrix' && (
  <MatrixBotForm
    bot={editBot ?? undefined}
    takenSymbols={takenSymbols}
    onSubmit={handleFormSubmit}
    onClose={handleFormClose}
  />
)}
```

Добавить `useMemo` в импорты если его нет: `import { useState, useMemo } from 'react'`

- [ ] **Step 7: Сборка и проверка**

```bash
cd C:\Users\123\Projects\sis\frontend
npm run build 2>&1 | Select-String -Pattern "error TS|error:"
```

Ожидание: нет ошибок TypeScript. Открыть форму Matrix/Hedge бота и убедиться:
- Символы, использованные другими активными ботами, отображают ⚡ в чипах и дропдауне
- Ховер на ⚡ показывает имя конфликтующего бота

---

## Task 2: Backend — проверка конфликтов в PatchBot

**Files:**
- Modify: `services/api-gateway/bots_handler.go`

- [ ] **Step 1: Добавить struct для ответа с warnings**

В `bots_handler.go`, после определения `botResp` struct (строка ~64, перед `listBotsResp`):

```go
type patchBotWithWarningsResp struct {
	botResp
	Warnings []string `json:"warnings,omitempty"`
}
```

- [ ] **Step 2: Добавить метод checkWhitelistConflicts**

В `bots_handler.go`, добавить новый метод после `PatchBot` (после строки ~511):

```go
// checkWhitelistConflicts returns warning strings for each other active bot
// whose symbol_whitelist overlaps with the given whitelist.
func (s *Server) checkWhitelistConflicts(ctx context.Context, botID, ownerID string, whitelist []string) []string {
	if len(whitelist) == 0 {
		return nil
	}
	// Build set of concrete (non-pattern) symbols to check
	wlSet := make(map[string]bool, len(whitelist))
	for _, sym := range whitelist {
		if !strings.Contains(sym, "*") {
			wlSet[sym] = true
		}
	}
	if len(wlSet) == 0 {
		return nil
	}

	rows, err := s.pool.Query(ctx, `
		SELECT name, COALESCE(strategy_config->>'bot_kind', 'bot'), symbol_whitelist
		FROM bots
		WHERE owner_id=$1 AND id != $2 AND status='active'
		  AND cardinality(symbol_whitelist) > 0
	`, ownerID, botID)
	if err != nil {
		return nil
	}
	defer rows.Close()

	var warnings []string
	for rows.Next() {
		var otherName, otherKind string
		var otherWL []string
		if rows.Scan(&otherName, &otherKind, &otherWL) != nil {
			continue
		}
		var conflicts []string
		for _, sym := range otherWL {
			if wlSet[sym] {
				conflicts = append(conflicts, sym)
			}
		}
		if len(conflicts) > 0 {
			warnings = append(warnings, fmt.Sprintf("%v пересекается с ботом «%s» (%s)", conflicts, otherName, otherKind))
		}
	}
	return warnings
}
```

Проверь что `strings` уже импортирован (он должен быть). Если нет — добавить в блок `import`.

- [ ] **Step 3: Вызвать проверку в PatchBot**

В `PatchBot`, заменить последние строки (строки ~505–511):

Старый код:
```go
	bot, ok := fetchBot(s, r, botID, callerID)
	if !ok {
		writeError(w, http.StatusInternalServerError, "db error")
		return
	}
	writeJSON(w, http.StatusOK, bot)
}
```

Новый код:
```go
	bot, ok := fetchBot(s, r, botID, callerID)
	if !ok {
		writeError(w, http.StatusInternalServerError, "db error")
		return
	}

	// Check for symbol whitelist conflicts with other active bots.
	if _, changingWL := body["symbolWhitelist"]; changingWL {
		if warnings := s.checkWhitelistConflicts(ctx, botID, ownerID, bot.SymbolWhitelist); len(warnings) > 0 {
			writeJSON(w, http.StatusOK, patchBotWithWarningsResp{botResp: bot, Warnings: warnings})
			return
		}
	}
	writeJSON(w, http.StatusOK, bot)
}
```

- [ ] **Step 4: Перезапустить Go backend**

Пользователь перезапускает `api-gateway` самостоятельно (через `go run` в директории сервиса).

Проверка: в терминале `go build ./...` не должен выдавать ошибок:
```bash
cd C:\Users\123\Projects\sis\services\api-gateway
go build ./...
```

---

## Task 3: Frontend — показать warnings из бэкенда в форме

**Files:**
- Modify: `frontend/src/features/bots/api.ts`
- Modify: `frontend/src/features/bots/components/MatrixBotForm.tsx`
- Modify: `frontend/src/features/bots/components/HedgeBotForm.tsx`
- Modify: `frontend/src/pages/BotsPage.tsx`
- Modify: `frontend/src/pages/TerminalPage.tsx`

- [ ] **Step 1: action('update') возвращает данные ответа**

В `frontend/src/features/bots/api.ts`, функция `action` (строка ~78–96):

Изменить return type с `Promise<void>` на `Promise<Record<string, unknown> | void>`:
```typescript
const action = useCallback(async (a: BotAction): Promise<Record<string, unknown> | void> => {
    try {
      switch (a.type) {
        case 'start':   await apiClient.post(`/bots/${a.botId}/start`); break;
        case 'stop':    await apiClient.post(`/bots/${a.botId}/stop`); break;
        case 'deploy':  await apiClient.post(`/bots/${a.botId}/deploy`, {
          symbolWhitelist: a.symbolWhitelist,
          symbolBlacklist: a.symbolBlacklist,
        }); break;
        case 'fork':    await apiClient.post(`/bots/${a.botId}/fork`); break;
        case 'publish': await apiClient.post(`/bots/${a.botId}/publish`); break;
        case 'request-approval': await apiClient.post(`/bots/${a.botId}/request-approval`); break;
        case 'update': {
          const res = await apiClient.patch<Record<string, unknown>>(`/bots/${a.botId}`, a.data)
          return res.data
        }
        case 'delete':  await apiClient.delete(`/bots/${a.botId}`); break;
        case 'create':  await apiClient.post('/bots', a.data); break;
      }
    } finally {
      await load();
    }
}, [load]);

return { catalog, mine, loading, action, refresh: load };
```

- [ ] **Step 2: Добавить отображение warnings в MatrixBotForm**

В `frontend/src/features/bots/components/MatrixBotForm.tsx`:

Добавить state (после других useState, примерно строка ~100):
```typescript
const [submitWarnings, setSubmitWarnings] = useState<string[]>([])
```

Изменить `doSubmit` (строки ~243–280), заменить `onClose()` на проверку warnings:
```typescript
const doSubmit = async () => {
  setSubmitting(true);
  setSubmitError(null);
  setSubmitWarnings([]);
  const totalMatrixLevels = aboveLevels.length + 1 + belowLevels.length;
  try {
    const result = await onSubmit({
      name: name.trim(),
      description: description.trim(),
      fullDescription: fullDescription.trim() || undefined,
      isPublic,
      avatarUrl: avatarUrl || undefined,
      symbolWhitelist: whitelist,
      symbolBlacklist: blacklist,
      strategyConfig: {
        ...config,
        bot_kind:      'matrix',
        direction:     'both',
        strategy_type: 'matrix',
        grid_levels:   totalMatrixLevels,
        grid_step_pct: belowLevels[0]?.price_step_pct ?? aboveLevels[0]?.price_step_pct ?? 0,
        signal_filter: false,
        hedge_deact_type: 4,
      },
      maxStrategies,
      maxLongStrategies,
      maxShortStrategies,
      maxMarginUsdt,
      maxSymConsecutiveRuns,
      accountId: selectedAccountId || null,
      autoMode,
    });
    const warnings = (result as { warnings?: string[] })?.warnings ?? []
    if (warnings.length > 0) {
      setSubmitWarnings(warnings)
    } else {
      onClose()
    }
  } catch (e) {
    setSubmitError(e instanceof Error ? e.message : 'Неизвестная ошибка');
  } finally {
    setSubmitting(false);
  }
};
```

Добавить блок предупреждений в JSX — найди кнопку сохранения (около конца формы) и добавь после неё:
```tsx
{submitWarnings.length > 0 && (
  <div className="rounded-lg border border-orange-500/30 bg-orange-500/[.08] p-3 flex flex-col gap-1.5">
    <div className="text-[11px] font-bold text-orange-400">⚡ Конфликты символов</div>
    {submitWarnings.map((w, i) => (
      <div key={i} className="text-[11px] text-orange-300/90">{w}</div>
    ))}
    <button
      type="button"
      onClick={onClose}
      className="mt-1 self-start rounded-md bg-orange-500/[.15] px-3 py-1 text-[11px] font-semibold text-orange-300 hover:bg-orange-500/[.25] transition-colors"
    >
      Понятно, закрыть
    </button>
  </div>
)}
```

- [ ] **Step 3: То же в HedgeBotForm**

В `frontend/src/features/bots/components/HedgeBotForm.tsx`:

Добавить state:
```typescript
const [submitWarnings, setSubmitWarnings] = useState<string[]>([])
```

Найти `doSubmit` (или аналогичную функцию, строка ~364):
```typescript
// Изменить:
const result = await onSubmit({ ... })
const warnings = (result as { warnings?: string[] })?.warnings ?? []
if (warnings.length > 0) {
  setSubmitWarnings(warnings)
} else {
  onClose()
}
```

Добавить блок предупреждений в JSX (аналогично MatrixBotForm).

- [ ] **Step 4: handleFormSubmit в BotsPage возвращает result**

В `frontend/src/pages/BotsPage.tsx`, изменить `handleFormSubmit` (строки ~127–133):

```typescript
const handleFormSubmit = async (data: CreateBotInput): Promise<{ warnings?: string[] } | void> => {
  if (formMode === 'create') {
    await action({ type: 'create', data });
    return;
  } else if (formMode === 'edit' && editBot) {
    return action({ type: 'update', botId: editBot.id, data }) as Promise<{ warnings?: string[] } | void>;
  }
};
```

Удалить вызов `handleFormClose()` из `handleFormSubmit`, если он там есть — форма сама вызывает `onClose()` после сохранения.

- [ ] **Step 5: Обновить inline onSubmit в TerminalBotsTab**

В `frontend/src/pages/TerminalPage.tsx`, строки ~574 и ~581:

Старый код (строка ~574):
```typescript
onSubmit={async (data) => { await action({ type: 'update', botId: editBot.id, data }); setEditBotId(null) }}
```

Новый код:
```typescript
onSubmit={async (data) => action({ type: 'update', botId: editBot.id, data }) as Promise<{ warnings?: string[] } | void>}
```

Аналогично для MatrixBotForm (строка ~581):
```typescript
onSubmit={async (data) => action({ type: 'update', botId: editBot.id, data }) as Promise<{ warnings?: string[] } | void>}
```

`setEditBotId(null)` убирается из onSubmit — форма вызывает `onClose()` сама, а `onClose={()=>setEditBotId(null)}` остаётся.

- [ ] **Step 6: Сборка и проверка**

```bash
cd C:\Users\123\Projects\sis\frontend
npm run build 2>&1 | Select-String -Pattern "error TS|error:"
```

Проверка вручную:
1. Открыть форму MatrixBot или HedgeBot
2. Добавить монету, которая есть в whitelist другого активного бота
3. Сохранить — форма должна показать блок предупреждений с именем конфликтующего бота
4. После нажатия "Понятно, закрыть" форма закрывается

---

## Task 4: Значок конфликта на BotTerminalCard

**Files:**
- Modify: `frontend/src/pages/TerminalPage.tsx`

- [ ] **Step 1: Вычислить conflictingBotIds в TerminalBotsTab**

В `TerminalBotsTab` (функция, строка ~468), добавить useMemo после деструктуризации:

```typescript
const conflictingBotIds = useMemo((): Set<string> => {
  const active = mine.filter(b => b.status === 'active' && b.symbolWhitelist.length > 0)
  const result = new Set<string>()
  for (let i = 0; i < active.length; i++) {
    const a = active[i]
    const aSet = new Set(a.symbolWhitelist.filter(s => !s.includes('*')))
    if (aSet.size === 0) continue
    for (let j = i + 1; j < active.length; j++) {
      const b = active[j]
      if (b.symbolWhitelist.filter(s => !s.includes('*')).some(s => aSet.has(s))) {
        result.add(a.id)
        result.add(b.id)
      }
    }
  }
  return result
}, [mine])
```

Добавить `useMemo` в импорт React если его нет.

- [ ] **Step 2: Передать hasConflict в BotTerminalCard**

В `TerminalBotsTab`, render ботов (строки ~555–568):
```tsx
{visibleBots.map(bot => (
  <BotTerminalCard
    key={bot.id}
    bot={bot}
    sc={signalCounts.get(bot.id)}
    onSymbolChange={onSymbolChange}
    onStop={() => action({ type: 'stop', botId: bot.id })}
    onStart={() => action({ type: 'start', botId: bot.id })}
    onEdit={() => setEditBotId(bot.id)}
    onArchive={() => archive(bot.id)}
    onScan={() => setScanBot(bot)}
    onToggleAuto={() => action({ type: 'update', botId: bot.id, data: { autoMode: !bot.autoMode } })}
    hasConflict={conflictingBotIds.has(bot.id)}
  />
))}
```

- [ ] **Step 3: Добавить проп и значок в BotTerminalCard**

В `BotTerminalCard` (строка ~261), добавить `hasConflict?: boolean` в деструктуризацию:

```typescript
function BotTerminalCard({ bot, sc, onSymbolChange, onStop, onStart, onEdit, onArchive, onScan, onToggleAuto, hasConflict }: {
  bot: Bot
  sc: { signalCount: number; totalCount: number } | undefined
  onSymbolChange: (s: string) => void
  onStop: () => void
  onStart: () => void
  onEdit: () => void
  onArchive: () => void
  onScan: () => void
  onToggleAuto: () => void
  hasConflict?: boolean
}) {
```

Добавить значок рядом с именем бота (строки ~341–349, в блоке `<div className="flex items-center gap-1.5">`):

```tsx
<div className="flex items-center gap-1.5">
  <span className={'truncate font-display text-[13px] font-bold tracking-tight ' + (running ? 'text-slate-50' : 'text-slate-400')}>
    {bot.name.length > 30 ? bot.name.slice(0, 30) + '…' : bot.name}
  </span>
  {!bot.sourceBotId && (
    <span className="rounded-[3px] border border-[#c14dff]/30 bg-[#c14dff]/[.16] px-1.5 py-px text-[9px] font-bold uppercase tracking-wider text-[#d8a4ff]">
      Custom
    </span>
  )}
  {hasConflict && (
    <span
      title="Символы пересекаются с другим активным ботом"
      className="rounded-[3px] border border-orange-500/30 bg-orange-500/[.16] px-1.5 py-px text-[9px] font-bold text-orange-400 cursor-help"
    >
      ⚡ конфликт
    </span>
  )}
</div>
```

- [ ] **Step 4: Сборка и проверка**

```bash
cd C:\Users\123\Projects\sis\frontend
npm run build 2>&1 | Select-String -Pattern "error TS|error:"
```

Проверка: если два активных бота имеют общий символ в whitelist, оба должны показывать оранжевый тег "⚡ конфликт" на BotTerminalCard.

---

## Self-Review

**Spec coverage:**
- ✓ Вариант 1: CoinMultiPicker показывает ⚡ на конфликтных символах — Task 1
- ✓ Вариант 2: Backend возвращает warnings при пересечении whitelist'ов — Task 2 + Task 3
- ✓ Вариант 3: Карточка бота в панели показывает конфликт — Task 4

**Placeholders:** отсутствуют.

**Type consistency:**
- `takenSymbols: Map<string, string>` используется одинаково в CoinMultiPicker, HedgeBotForm, MatrixBotForm
- `{ warnings?: string[] }` — тип возврата onSubmit согласован во всех формах и вызывающих местах
- `conflictingBotIds: Set<string>` — вычисляется в TerminalBotsTab, передаётся в BotTerminalCard как `hasConflict: boolean`

**Edge cases:**
- Паттерн-символы (с `*`) исключаются из проверки конфликтов — они динамические и не позволяют точно определить пересечение
- Бот с пустым whitelist не участвует в проверке конфликтов (все монеты = нет ограничений)
- Остановленные боты не учитываются (`status !== 'active'`)

---

## Execution Handoff

**Plan complete and saved to `docs/superpowers/plans/2026-06-25-bot-symbol-conflict-warnings.md`.**

Two execution options:

**1. Subagent-Driven (recommended)** — dispatch a fresh subagent per task, review between tasks

**2. Inline Execution** — execute tasks in this session using executing-plans

Which approach?
