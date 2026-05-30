# Bot Type Picker Wiring Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Подключить существующий `BotTypePickerModal` в flow создания бота: кнопка «Создать бота» → выбор типа → форма; переименовать `trend` → `signal` (SignalBot); ParserBot и HedgeBot — видимы, но заблокированы.

**Architecture:** Чисто фронтендное изменение в 5 файлах. Сначала обновляем типы и метаданные, затем disabled-стилизацию пикера, затем подключаем к `BotsPage`, затем пробрасываем `initialKind` в `BotForm`. Каждый шаг независимо компилируется.

**Tech Stack:** React 18, TypeScript, Tailwind CSS, Vitest + @testing-library/react.

---

## Карта файлов

| Файл | Действие | Что меняется |
|---|---|---|
| `frontend/src/features/bots/types.ts` | Modify | `BotKind`: `'trend'` → `'signal'` |
| `frontend/src/features/bots/botKindMeta.ts` | Modify | Ключ, label, `disabled`, fallback в `getBotKindMeta` |
| `frontend/src/features/bots/components/BotTypePickerModal.tsx` | Modify | `KIND_ICONS` ключ, disabled-карточки |
| `frontend/src/pages/BotsPage.tsx` | Modify | `showTypePicker` state, `handleCreate`, `handleTypeSelect`, `initialKind` |
| `frontend/src/features/bots/components/BotForm.tsx` | Modify | Prop `initialKind`, `defaultConfig` принимает kind |
| `frontend/src/features/bots/__tests__/botKindMeta.test.ts` | Create | Тесты fallback и disabled |

---

## Task 1: Обновить тип `BotKind` и метаданные

**Files:**
- Modify: `frontend/src/features/bots/types.ts:17`
- Modify: `frontend/src/features/bots/botKindMeta.ts`
- Create: `frontend/src/features/bots/__tests__/botKindMeta.test.ts`

- [ ] **Шаг 1: Написать тест (падающий)**

Создать файл `frontend/src/features/bots/__tests__/botKindMeta.test.ts`:

```ts
import { describe, it, expect } from 'vitest'
import { BOT_KINDS, BOT_KIND_META, getBotKindMeta } from '../botKindMeta'
import type { BotKind } from '../types'

describe('botKindMeta', () => {
  it('BOT_KINDS содержит signal, parser, hedge', () => {
    expect(BOT_KINDS).toEqual(['signal', 'parser', 'hedge'])
  })

  it('signal имеет label SignalBot и не disabled', () => {
    expect(BOT_KIND_META['signal'].label).toBe('SignalBot')
    expect(BOT_KIND_META['signal'].disabled).toBeFalsy()
  })

  it('parser и hedge имеют disabled: true', () => {
    expect(BOT_KIND_META['parser'].disabled).toBe(true)
    expect(BOT_KIND_META['hedge'].disabled).toBe(true)
  })

  it('getBotKindMeta возвращает signal для неизвестного kind', () => {
    expect(getBotKindMeta('trend' as BotKind)).toBe(BOT_KIND_META['signal'])
    expect(getBotKindMeta(undefined)).toBe(BOT_KIND_META['signal'])
  })
})
```

- [ ] **Шаг 2: Запустить тест — убедиться что падает**

```
cd frontend && npx vitest run src/features/bots/__tests__/botKindMeta.test.ts
```

Ожидаем: FAIL — `BOT_KINDS` содержит `'trend'`, не `'signal'`.

- [ ] **Шаг 3: Обновить `types.ts` — переименовать `'trend'` → `'signal'`**

Файл `frontend/src/features/bots/types.ts`, строка 17:

```ts
// Было:
export type BotKind = 'trend' | 'parser' | 'hedge';

// Стало:
export type BotKind = 'signal' | 'parser' | 'hedge';
```

- [ ] **Шаг 4: Обновить `botKindMeta.ts` полностью**

Заменить содержимое `frontend/src/features/bots/botKindMeta.ts`:

```ts
import type { BotKind } from './types'

export interface BotKindMeta {
  id:         BotKind
  label:      string
  tagline:    string
  desc:       string
  color:      string
  border:     string
  bg:         string
  bgHeader:   string
  iconBg:     string
  disabled?:  boolean
}

export const BOT_KINDS: BotKind[] = ['signal', 'parser', 'hedge']

export const BOT_KIND_META: Record<BotKind, BotKindMeta> = {
  signal: {
    id:       'signal',
    label:    'SignalBot',
    tagline:  'Следует за рыночным трендом',
    desc:     'Открывает позиции в направлении доминирующего тренда по сигналам индикаторов. Использует сетку ордеров для усреднения входа и автоматически переворачивает позицию при смене тренда.',
    color:    '#60a5fa',
    border:   'rgba(59,130,246,0.35)',
    bg:       'rgba(59,130,246,0.10)',
    bgHeader: 'linear-gradient(135deg,rgba(59,130,246,0.20) 0%,rgba(37,99,235,0.06) 100%)',
    iconBg:   'rgba(59,130,246,0.18)',
  },
  parser: {
    id:       'parser',
    label:    'ParserBot',
    tagline:  'Торгует на новостях и листингах',
    desc:     'Парсит анонсы бирж, новости и листинги в реальном времени. При появлении триггера мгновенно открывает позицию до основной реакции рынка, фиксируя быструю волатильность.',
    color:    '#22d3ee',
    border:   'rgba(6,182,212,0.35)',
    bg:       'rgba(6,182,212,0.10)',
    bgHeader: 'linear-gradient(135deg,rgba(6,182,212,0.20) 0%,rgba(8,145,178,0.06) 100%)',
    iconBg:   'rgba(6,182,212,0.18)',
    disabled: true,
  },
  hedge: {
    id:       'hedge',
    label:    'HedgeBot',
    tagline:  'Хеджирует риски портфеля',
    desc:     'Удерживает контр-позиции для защиты от просадок. Автоматически балансирует лонг и шорт на основе текущей рыночной экспозиции и заданного коэффициента хеджирования.',
    color:    '#f59e0b',
    border:   'rgba(180,83,9,0.45)',
    bg:       'rgba(180,83,9,0.15)',
    bgHeader: 'linear-gradient(135deg,rgba(180,83,9,0.24) 0%,rgba(120,53,15,0.06) 100%)',
    iconBg:   'rgba(180,83,9,0.20)',
    disabled: true,
  },
}

export function getBotKindMeta(kind: BotKind | string | undefined): BotKindMeta {
  return BOT_KIND_META[kind as BotKind] ?? BOT_KIND_META['signal']
}
```

- [ ] **Шаг 5: Запустить тест — убедиться что проходит**

```
cd frontend && npx vitest run src/features/bots/__tests__/botKindMeta.test.ts
```

Ожидаем: PASS (4 теста).

- [ ] **Шаг 6: Проверить что TypeScript компилируется без ошибок**

```
cd frontend && npx tsc --noEmit 2>&1 | head -30
```

Ожидаем: нет ошибок (или только несвязанные с нашими файлами).

- [ ] **Шаг 7: Коммит**

```
git add frontend/src/features/bots/types.ts \
        frontend/src/features/bots/botKindMeta.ts \
        frontend/src/features/bots/__tests__/botKindMeta.test.ts
git commit -m "feat(bots): rename BotKind trend→signal, add disabled flag to meta"
```

---

## Task 2: Обновить `BotTypePickerModal` — disabled-карточки

**Files:**
- Modify: `frontend/src/features/bots/components/BotTypePickerModal.tsx`

Текущее состояние файла: карточки рендерятся через `BOT_KINDS.map(kind => ...)` с hover-стилями через inline event handlers. `KIND_ICONS` имеет ключ `trend`.

- [ ] **Шаг 1: Заменить содержимое `BotTypePickerModal.tsx` полностью**

```tsx
import { useEffect } from 'react'
import { TrendingUp, Search, Shield } from 'lucide-react'
import { BOT_KINDS, BOT_KIND_META } from '../botKindMeta'
import type { BotKind } from '../types'

const KIND_ICONS: Record<BotKind, React.ComponentType<{ size?: number; strokeWidth?: number }>> = {
  signal: TrendingUp,
  parser: Search,
  hedge:  Shield,
}

type Props = {
  onSelect: (kind: BotKind) => void
  onClose:  () => void
}

export function BotTypePickerModal({ onSelect, onClose }: Props) {
  useEffect(() => {
    const handler = (e: KeyboardEvent) => { if (e.key === 'Escape') onClose() }
    window.addEventListener('keydown', handler)
    return () => window.removeEventListener('keydown', handler)
  }, [onClose])

  return (
    <div
      className="fixed inset-0 z-50 flex items-center justify-center"
      style={{ background: 'rgba(0,0,0,0.72)', backdropFilter: 'blur(4px)' }}
      onClick={(e) => { if (e.target === e.currentTarget) onClose() }}
    >
      <div
        className="relative w-full max-w-[760px] rounded-2xl border p-6"
        style={{
          background:  'linear-gradient(180deg,#10141f 0%,#0c1018 100%)',
          borderColor: 'rgba(255,255,255,0.08)',
          boxShadow:   '0 32px 80px -20px rgba(0,0,0,0.8)',
        }}
        onClick={(e) => e.stopPropagation()}
      >
        {/* заголовок */}
        <div className="mb-5">
          <div className="text-[18px] font-bold tracking-tight text-slate-50">Выберите тип бота</div>
          <div className="mt-1 text-[13px] text-slate-400">
            Тип определяет логику работы. Настройки можно изменить позже.
          </div>
        </div>

        {/* карточки */}
        <div className="grid grid-cols-3 gap-3.5">
          {BOT_KINDS.map((kind) => {
            const m       = BOT_KIND_META[kind]
            const Icon    = KIND_ICONS[kind]
            const disabled = m.disabled === true

            if (disabled) {
              return (
                <div
                  key={kind}
                  className="relative flex flex-col overflow-hidden rounded-[14px] border text-left"
                  style={{
                    borderColor: 'rgba(255,255,255,0.07)',
                    background:  'rgba(255,255,255,0.015)',
                    opacity:     0.42,
                    cursor:      'not-allowed',
                  }}
                >
                  {/* бейдж «Скоро» */}
                  <div
                    className="absolute right-3 top-3 rounded-[5px] border px-1.5 py-px text-[9px] font-bold uppercase tracking-wide"
                    style={{ borderColor: m.border, color: m.color, background: m.iconBg }}
                  >
                    Скоро
                  </div>

                  {/* шапка */}
                  <div className="px-4 pt-4 pb-3" style={{ background: m.bgHeader }}>
                    <div
                      className="mb-3 flex h-10 w-10 items-center justify-center rounded-xl border"
                      style={{ background: m.iconBg, borderColor: m.border, color: m.color }}
                    >
                      <Icon size={18} strokeWidth={2} />
                    </div>
                    <div className="font-display text-[17px] font-bold tracking-tight text-slate-500">
                      {m.label}
                    </div>
                    <div className="mt-0.5 text-[12px] font-medium text-slate-600">
                      {m.tagline}
                    </div>
                  </div>

                  {/* описание */}
                  <div className="px-4 py-3">
                    <p className="text-[12px] leading-relaxed text-slate-600">{m.desc}</p>
                  </div>

                  {/* кнопка-заглушка */}
                  <div
                    className="mx-4 mb-4 flex items-center justify-center rounded-lg border py-1.5 text-[11px] font-semibold text-slate-600"
                    style={{ borderColor: 'rgba(255,255,255,0.06)', background: 'rgba(255,255,255,0.03)' }}
                  >
                    В разработке
                  </div>
                </div>
              )
            }

            // активная карточка
            return (
              <button
                key={kind}
                type="button"
                onClick={() => onSelect(kind)}
                className="group relative flex flex-col overflow-hidden rounded-[14px] border text-left transition-all duration-150"
                style={{ borderColor: m.border, background: 'rgba(255,255,255,0.02)' }}
                onMouseEnter={(e) => {
                  ;(e.currentTarget as HTMLElement).style.background = m.bg
                  ;(e.currentTarget as HTMLElement).style.boxShadow = `0 8px 28px -8px ${m.border}`
                }}
                onMouseLeave={(e) => {
                  ;(e.currentTarget as HTMLElement).style.background = 'rgba(255,255,255,0.02)'
                  ;(e.currentTarget as HTMLElement).style.boxShadow = 'none'
                }}
              >
                <div className="px-4 pt-4 pb-3" style={{ background: m.bgHeader }}>
                  <div
                    className="mb-3 flex h-10 w-10 items-center justify-center rounded-xl border"
                    style={{ background: m.iconBg, borderColor: m.border, color: m.color }}
                  >
                    <Icon size={18} strokeWidth={2} />
                  </div>
                  <div
                    className="font-display text-[17px] font-bold tracking-tight"
                    style={{ color: m.color }}
                  >
                    {m.label}
                  </div>
                  <div className="mt-0.5 text-[12px] font-medium text-slate-400">
                    {m.tagline}
                  </div>
                </div>

                <div className="px-4 py-3">
                  <p className="text-[12px] leading-relaxed text-slate-400">{m.desc}</p>
                </div>

                <div
                  className="mx-4 mb-4 flex items-center justify-center rounded-lg border py-1.5 text-[11px] font-semibold transition-colors"
                  style={{ borderColor: m.border, color: m.color, background: m.iconBg }}
                >
                  Выбрать {m.label} →
                </div>
              </button>
            )
          })}
        </div>

        {/* закрыть */}
        <button
          type="button"
          onClick={onClose}
          className="absolute right-4 top-4 flex h-7 w-7 items-center justify-center rounded-full text-slate-500 hover:bg-white/[.06] hover:text-slate-300 transition-colors"
        >
          <svg width="13" height="13" viewBox="0 0 13 13" fill="none">
            <path d="M1 1l11 11M12 1L1 12" stroke="currentColor" strokeWidth="1.6" strokeLinecap="round"/>
          </svg>
        </button>
      </div>
    </div>
  )
}
```

- [ ] **Шаг 2: Убедиться что TypeScript компилируется**

```
cd frontend && npx tsc --noEmit 2>&1 | grep BotTypePickerModal
```

Ожидаем: нет строк с ошибками.

- [ ] **Шаг 3: Визуальная проверка в браузере**

Запусти dev-сервер если не запущен:
```
cd frontend && npm run dev
```

Пока пикер ещё не подключён к кнопке — можно временно вызвать его напрямую в `pages/BotsPage.tsx` задав `showTypePicker = true` в начальном state (откатишь в Task 3). Открой страницу ботов и убедись:
- SignalBot — яркая, кликабельная карточка
- ParserBot и HedgeBot — серые, с бейджем «Скоро», некликабельные

- [ ] **Шаг 4: Коммит**

```
git add frontend/src/features/bots/components/BotTypePickerModal.tsx
git commit -m "feat(bots): add disabled card style to BotTypePickerModal"
```

---

## Task 3: Подключить пикер к кнопке «Создать бота» в `BotsPage`

**Files:**
- Modify: `frontend/src/pages/BotsPage.tsx`

Текущее состояние: `handleCreate` сразу вызывает `setFormMode('create')`. `BotTypePickerModal` нигде не импортируется.

- [ ] **Шаг 1: Заменить содержимое `frontend/src/pages/BotsPage.tsx`**

```tsx
import { useState } from 'react';
import { BotsPage as BotsPageUI } from '../features/bots/BotsPage';
import { BotForm } from '../features/bots/components/BotForm';
import { BotTypePickerModal } from '../features/bots/components/BotTypePickerModal';
import { DeployModal } from '../features/bots/components/DeployModal';
import { useBots } from '../features/bots/api';
import { useBotSignalCounts } from '../hooks/useBotSignalCounts';
import type { BotSignalCount } from '../hooks/useBotSignalCounts';
import type { Bot, CreateBotInput, BotKind } from '../features/bots/types';
import type { MyBot, FeaturedBot, BotStrategy, RiskLevel, TradeMode } from '../features/bots/ui-types';

function toMyBot(b: Bot, sc: BotSignalCount | undefined): MyBot {
  const wl = b.symbolWhitelist ?? [];
  const symbolsTotal = sc
    ? String(sc.totalCount)
    : wl.length === 0 ? 'все' : String(wl.length);

  return {
    id:               b.id,
    tplId:            b.sourceBotId,
    name:             b.name,
    description:      b.description ?? '',
    avatarUrl:        b.avatarUrl,
    strategy: (b.strategyConfig.direction === 'long'
      ? 'trend'
      : b.strategyConfig.direction === 'short'
        ? 'trend'
        : 'grid') as BotStrategy,
    status:           b.status === 'active' ? 'running' : b.status === 'stopped' ? 'stopped' : 'paused',
    pair:             b.strategyConfig.symbol ?? 'BTC/USDT',
    exchange:         'bybit',
    capital:          b.strategyConfig.grid_size_usdt ?? 0,
    started:          new Date(b.createdAt),
    lev:              1,
    mode:             'futures' as TradeMode,
    symbolsTotal,
    symbolsWithSignal: sc ? String(sc.signalCount) : '—',
    symbolsLimit:      '—',
    custom:            !b.sourceBotId,
    config:            b.strategyConfig as Record<string, unknown>,
  };
}

function toFeaturedBot(b: Bot): FeaturedBot {
  return {
    id:        b.id,
    name:      b.name,
    author:    b.ownerName,
    strategy:  'grid' as BotStrategy,
    risk:      'medium' as RiskLevel,
    verified:  b.isOfficial,
    fire:      b.deployCount > 10,
    price:     0,
    desc:      b.description,
    pairs:     b.symbolWhitelist.length > 0 ? b.symbolWhitelist : ['BTC'],
    minCap:    0,
    fee:       0,
    users:     b.deployCount,
    rating:    4.0,
    perfMonth: 0,
    perfTotal: 0,
    winRate:   0,
    sharpe:    0,
    drawdown:  0,
    spark:     [0, 0],
    exchanges: ['bybit'],
    lev:       1,
    mode:      'futures' as TradeMode,
    tags:      b.symbolWhitelist,
  };
}

export function BotsPage() {
  const { catalog, mine, loading, action } = useBots();
  const signalCounts = useBotSignalCounts(!loading);

  const [showTypePicker, setShowTypePicker] = useState(false);
  const [initialKind,    setInitialKind]    = useState<BotKind>('signal');
  const [formMode,       setFormMode]       = useState<'create' | 'edit' | null>(null);
  const [editBot,        setEditBot]        = useState<Bot | null>(null);
  const [deployBot,      setDeployBot]      = useState<Bot | null>(null);

  if (loading) {
    return (
      <div className="flex h-full items-center justify-center text-sm text-slate-500">
        Загрузка...
      </div>
    );
  }

  // Кнопка «Создать бота» → показываем пикер
  const handleCreate = () => {
    setShowTypePicker(true);
  };

  // Пользователь выбрал тип в пикере → открываем форму
  const handleTypeSelect = (kind: BotKind) => {
    setShowTypePicker(false);
    setInitialKind(kind);
    setEditBot(null);
    setFormMode('create');
  };

  // Редактирование существующего бота — пикер не показываем
  const handleEditBot = (id: string) => {
    const bot = mine.find((b) => b.id === id) ?? null;
    setEditBot(bot);
    setFormMode('edit');
  };

  const handleFormClose = () => {
    setFormMode(null);
    setEditBot(null);
    setInitialKind('signal');
  };

  const handleFormSubmit = async (data: CreateBotInput) => {
    if (formMode === 'create') {
      await action({ type: 'create', data });
    } else if (formMode === 'edit' && editBot) {
      await action({ type: 'update', botId: editBot.id, data });
    }
  };

  const handleLaunchTpl = (tplId: string) => {
    const bot = catalog.find((b) => b.id === tplId) ?? null;
    if (bot) {
      setDeployBot(bot);
    } else {
      action({ type: 'deploy', botId: tplId });
    }
  };

  const handleDeploy = (whitelist: string[], blacklist: string[]) => {
    if (!deployBot) return;
    action({
      type: 'deploy',
      botId: deployBot.id,
      symbolWhitelist: whitelist.length > 0 ? whitelist : undefined,
      symbolBlacklist: blacklist.length > 0 ? blacklist : undefined,
    });
  };

  return (
    <>
      <BotsPageUI
        myBots={mine.map(b => toMyBot(b, signalCounts.get(b.id)))}
        featured={catalog.map(toFeaturedBot)}
        onCreateBot={handleCreate}
        onExportBots={() => {}}
        onToggleBot={(id, next) =>
          action({ type: next === 'running' ? 'start' : 'stop', botId: id })
        }
        onEditBot={handleEditBot}
        onDeleteBot={(id) => action({ type: 'delete', botId: id })}
        onLaunchTpl={handleLaunchTpl}
        onConfigureTpl={handleLaunchTpl}
        onCloneTpl={(tplId) => action({ type: 'fork', botId: tplId })}
      />

      {showTypePicker && (
        <BotTypePickerModal
          onSelect={handleTypeSelect}
          onClose={() => setShowTypePicker(false)}
        />
      )}

      {formMode !== null && (
        <BotForm
          bot={editBot ?? undefined}
          initialKind={initialKind}
          onSubmit={handleFormSubmit}
          onClose={handleFormClose}
        />
      )}

      {deployBot && (
        <DeployModal
          bot={deployBot}
          onDeploy={handleDeploy}
          onClose={() => setDeployBot(null)}
        />
      )}
    </>
  );
}
```

- [ ] **Шаг 2: Убедиться что TypeScript компилируется**

```
cd frontend && npx tsc --noEmit 2>&1 | grep -E "BotsPage|BotTypePickerModal|initialKind"
```

Ожидаем: ошибка про `initialKind` на `BotForm` — это нормально, исправим в Task 4.

- [ ] **Шаг 3: Коммит**

```
git add frontend/src/pages/BotsPage.tsx
git commit -m "feat(bots): wire BotTypePickerModal into create flow"
```

---

## Task 4: Добавить `initialKind` в `BotForm`

**Files:**
- Modify: `frontend/src/features/bots/components/BotForm.tsx:12-17` (Props)
- Modify: `frontend/src/features/bots/components/BotForm.tsx:26-58` (defaultConfig)
- Modify: `frontend/src/features/bots/components/BotForm.tsx:83` (component signature)

- [ ] **Шаг 1: Добавить `BotKind` в import в `BotForm.tsx`**

Найти строку 8:
```ts
import type { Bot as BotType, CreateBotInput, StrategyConfig } from '../types';
```

Заменить на:
```ts
import type { Bot as BotType, BotKind, CreateBotInput, StrategyConfig } from '../types';
```

- [ ] **Шаг 2: Обновить `Props` — добавить `initialKind`**

Найти блок (строки 12–17):
```ts
type Props = {
  bot?: BotType;
  onSubmit: (data: CreateBotInput) => Promise<void> | void;
  onClose: () => void;
  mode?: 'user' | 'admin';
};
```

Заменить на:
```ts
type Props = {
  bot?: BotType;
  initialKind?: BotKind;
  onSubmit: (data: CreateBotInput) => Promise<void> | void;
  onClose: () => void;
  mode?: 'user' | 'admin';
};
```

- [ ] **Шаг 3: Обновить `defaultConfig` — принять `kind`**

Найти строку 26:
```ts
function defaultConfig(bot?: BotType): StrategyConfig {
  const s = bot?.strategyConfig ?? {};
  return {
```

Заменить начало функции:
```ts
function defaultConfig(bot?: BotType, kind?: BotKind): StrategyConfig {
  const s = bot?.strategyConfig ?? {};
  return {
    bot_kind: s.bot_kind ?? kind ?? 'signal',
```

Строка `bot_kind` добавляется первой в возвращаемый объект (остальное без изменений).

- [ ] **Шаг 4: Обновить сигнатуру `BotForm` — деструктурировать `initialKind`**

Найти строку 83:
```ts
export function BotForm({ bot, onSubmit, onClose, mode = 'user' }: Props) {
```

Заменить на:
```ts
export function BotForm({ bot, initialKind, onSubmit, onClose, mode = 'user' }: Props) {
```

Найти строку 94 (инициализация state конфига):
```ts
  const [config, setConfig] = useState<StrategyConfig>(defaultConfig(bot));
```

Заменить на:
```ts
  const [config, setConfig] = useState<StrategyConfig>(defaultConfig(bot, initialKind));
```

- [ ] **Шаг 5: Убедиться что TypeScript компилируется без ошибок**

```
cd frontend && npx tsc --noEmit 2>&1 | head -20
```

Ожидаем: нет ошибок.

- [ ] **Шаг 6: Коммит**

```
git add frontend/src/features/bots/components/BotForm.tsx
git commit -m "feat(bots): pass initialKind from type picker into BotForm"
```

---

## Task 5: E2E-проверка полного flow

Этот task — ручная проверка, коммитов нет.

- [ ] **Шаг 1: Запустить dev-сервер**

```
cd frontend && npm run dev
```

- [ ] **Шаг 2: Проверить flow создания**

1. Открыть страницу Боты
2. Нажать «Создать бота»
3. Убедиться: открывается `BotTypePickerModal` с тремя карточками
4. Убедиться: ParserBot и HedgeBot серые, с бейджем «Скоро», кнопки «В разработке», клик не срабатывает
5. Нажать «Выбрать SignalBot →»
6. Убедиться: пикер закрывается, открывается `BotForm`
7. Убедиться: `BotForm` не показывает ошибок в консоли

- [ ] **Шаг 3: Проверить flow редактирования**

1. Нажать «Редактировать» на любом существующем боте
2. Убедиться: пикер **не** показывается, `BotForm` открывается напрямую

- [ ] **Шаг 4: Проверить Escape**

1. Нажать «Создать бота» → пикер открывается
2. Нажать Escape — пикер закрывается, форма не открывается

- [ ] **Шаг 5: Запустить все тесты**

```
cd frontend && npx vitest run
```

Ожидаем: все тесты проходят, включая `botKindMeta.test.ts`.
