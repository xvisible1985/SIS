import { useState, useEffect, useMemo, useRef, useCallback } from 'react'
import { useSearchParams, Link } from 'react-router-dom'
import { Chart, type ChartOverlaySettings } from '../components/terminal/Chart'
import { Orderbook } from '../components/terminal/Orderbook'
import { OrderForm } from '../components/terminal/OrderForm'
import { PositionsTable } from '../components/terminal/PositionsTable'
import { OrdersTable } from '../components/terminal/OrdersTable'
import { HistoryTable } from '../components/terminal/HistoryTable'
import { ExecutionsTable } from '../components/terminal/ExecutionsTable'
import { TradeLog } from '../components/terminal/TradeLog'
import { PnlTable } from '../components/terminal/PnlTable'
import { GridForm } from '../components/terminal/GridForm'
import { CoinPicker } from '../components/common/CoinPicker'
import { usePositionsWs } from '../hooks/terminal/usePositionsWs'
import { useCandles } from '../hooks/terminal/useCandles'
import { useOrderbook } from '../hooks/terminal/useOrderbook'
import { useTickerPrices } from '../hooks/terminal/useTickerPrices'
import { listAccounts } from '../api/accounts'
import { useSelectedAccount } from '../contexts/AccountContext'
import { listStrategies, getStrategyState } from '../api/strategies'
import { listExecutions } from '../api/trader'
import { StrategyCard } from '../components/strategies/StrategyCard'
import { StrategyModal } from '../components/strategies/StrategyModal'
import { useBots } from '../features/bots/api'
import { useBotSignalCounts } from '../hooks/useBotSignalCounts'
import { useBotEventsWs, type BotEventCategory } from '../hooks/useBotEventsWs'
import { BotForm } from '../features/bots/components/BotForm'
import { BotScanModal } from '../features/bots/components/BotScanModal'
import { STRAT_META } from '../features/bots/strategyMeta'
import type { Bot } from '../features/bots/types'
import type { BotStrategy } from '../features/bots/ui-types'
import type { Strategy, ExchangeAccount, ActiveOrder, Position, ChartExecution, StrategyLevel } from '../types'

let _cachedSymbol = localStorage.getItem('t_symbol') ?? 'BTCUSDT'
let _cachedTf = localStorage.getItem('t_tf') ?? '60'

const TIMEFRAMES = [
  { label: '1м', value: '1' },
  { label: '5м', value: '5' },
  { label: '15м', value: '15' },
  { label: '1ч', value: '60' },
  { label: '4ч', value: '240' },
  { label: '1д', value: 'D' },
]

type BottomTab = 'positions' | 'orders' | 'history' | 'executions' | 'log' | 'pnl'
type RightTab = 'manual' | 'strategies' | 'bots'
type MobileTab = 'positions' | 'orders' | 'strategies' | 'trade'

const STATUS_ORDER: Record<string, number> = { active: 0, finishing: 1, stopped: 2 }
function sortStrategies<T extends { status: string; symbol: string }>(list: T[]): T[] {
  return [...list].sort((a, b) => {
    const sd = (STATUS_ORDER[a.status] ?? 9) - (STATUS_ORDER[b.status] ?? 9)
    return sd !== 0 ? sd : a.symbol.localeCompare(b.symbol)
  })
}

const DEFAULT_CHART_SETTINGS: ChartOverlaySettings = {
  showPositions: true,
  showPlacedOrders: true,
  showTakenOrders: true,
  showTakeProfit: true,
  showStopLoss: true,
  bothDirections: true,
}

function Toggle({ on }: { on: boolean }) {
  return (
    <div className={`relative flex-shrink-0 w-8 h-[18px] rounded-full transition-colors duration-150 ${on ? 'bg-[#4a7dff]' : 'bg-white/[.15]'}`}>
      <span className={`absolute top-[2px] w-[14px] h-[14px] rounded-full bg-white shadow-sm transition-all duration-150 ${on ? 'left-[18px]' : 'left-[2px]'}`} />
    </div>
  )
}

function ChartSettingsPopup({
  settings,
  onChange,
  strategyDir,
}: {
  settings: ChartOverlaySettings
  onChange: (s: ChartOverlaySettings) => void
  strategyDir: 'long' | 'short' | null
}) {
  const [open, setOpen] = useState(false)
  const wrapRef = useRef<HTMLDivElement>(null)

  useEffect(() => {
    if (!open) return
    function onDown(e: MouseEvent) {
      if (wrapRef.current && !wrapRef.current.contains(e.target as Node)) setOpen(false)
    }
    document.addEventListener('mousedown', onDown)
    return () => document.removeEventListener('mousedown', onDown)
  }, [open])

  function toggle(key: keyof ChartOverlaySettings) {
    onChange({ ...settings, [key]: !settings[key] })
  }

  const rows: { key: keyof ChartOverlaySettings; label: string }[] = [
    { key: 'showPositions',    label: 'Позиции' },
    { key: 'showPlacedOrders', label: 'Выставленные ордера' },
    { key: 'showTakenOrders',  label: 'Взятые ордера' },
    { key: 'showTakeProfit',   label: 'ТейкПрофит' },
    { key: 'showStopLoss',     label: 'СтопЛосс' },
  ]

  const anyOff = Object.values(settings).some(v => !v)

  return (
    <div className="relative" ref={wrapRef}>
      <button
        type="button"
        onClick={() => setOpen(v => !v)}
        title="Настройки графика"
        className={`w-7 h-7 flex items-center justify-center rounded-lg border transition-colors ${
          open || anyOff
            ? 'bg-[#4a7dff]/[.18] border-[#4a7dff]/40 text-[#5b8cff]'
            : 'border-white/[.08] bg-white/[.04] text-slate-400 hover:text-slate-200 hover:bg-white/[.06]'
        }`}
      >
        <svg width={14} height={14} viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth={1.9} strokeLinecap="round" strokeLinejoin="round">
          <line x1="4" y1="6" x2="20" y2="6"/><line x1="4" y1="12" x2="20" y2="12"/><line x1="4" y1="18" x2="20" y2="18"/>
          <circle cx="9" cy="6" r="2.5" fill="currentColor" stroke="none"/>
          <circle cx="15" cy="12" r="2.5" fill="currentColor" stroke="none"/>
          <circle cx="7" cy="18" r="2.5" fill="currentColor" stroke="none"/>
        </svg>
      </button>

      {open && (
        <div className="absolute right-0 top-full mt-2 z-50 w-[224px] rounded-[13px] border border-white/[.10] bg-[#0d1220] shadow-[0_16px_40px_-8px_rgba(0,0,0,.8)] overflow-hidden">
          <div className="px-3 pt-3 pb-1">
            <div className="text-[10px] font-bold uppercase tracking-[1.3px] text-slate-500">Слои графика</div>
          </div>
          <div className="px-1.5 pb-1.5">
            {rows.map(row => (
              <button key={row.key} type="button" onClick={() => toggle(row.key)}
                className="flex items-center justify-between w-full px-2.5 py-[7px] rounded-[8px] hover:bg-white/[.04] transition-colors">
                <span className={`text-[13px] font-medium ${settings[row.key] ? 'text-slate-200' : 'text-slate-500'}`}>{row.label}</span>
                <Toggle on={settings[row.key]} />
              </button>
            ))}
          </div>
          <div className="border-t border-white/[.07] mx-3" />
          <div className="px-3 pt-2.5 pb-1">
            <div className="text-[10px] font-bold uppercase tracking-[1.3px] text-slate-500">Направление</div>
          </div>
          <div className="px-1.5 pb-2">
            <button type="button" onClick={() => toggle('bothDirections')}
              className="flex items-center justify-between w-full px-2.5 py-[7px] rounded-[8px] hover:bg-white/[.04] transition-colors">
              <div className="text-left">
                <div className={`text-[13px] font-medium ${settings.bothDirections ? 'text-slate-200' : 'text-slate-500'}`}>Оба направления</div>
                {!settings.bothDirections && (
                  <div className="text-[11px] text-slate-500 mt-0.5">
                    {strategyDir ? `Только ${strategyDir === 'long' ? 'лонг' : 'шорт'}` : 'Выберите стратегию'}
                  </div>
                )}
              </div>
              <Toggle on={settings.bothDirections} />
            </button>
          </div>
        </div>
      )}
    </div>
  )
}

// ── Bots tab ─────────────────────────────────────────────────────────────────

const LEVEL_COLOR: Record<string, string> = {
  info:  'text-slate-400',
  warn:  'text-amber-400',
  error: 'text-rose-400',
}

const LOG_TABS: Array<{ label: string; value: BotEventCategory | null }> = [
  { label: 'Все',        value: null },
  { label: 'Тики',      value: 'tick' },
  { label: 'Стратегии', value: 'strategy' },
  { label: 'Сделки',    value: 'trade' },
  { label: 'Действия',  value: 'user' },
]

function BotEventLog({ botId, open }: { botId: string; open: boolean }) {
  const [activeTab, setActiveTab] = useState<BotEventCategory | null>(null)
  const events = useBotEventsWs(open ? botId : null, open, activeTab)

  return (
    <div className="mt-2">
      {/* Filter tabs */}
      <div className="flex gap-1 mb-1.5">
        {LOG_TABS.map(tab => (
          <button
            key={tab.label}
            onClick={() => setActiveTab(tab.value)}
            className={`px-2 py-0.5 rounded text-[10px] font-medium transition-colors ${
              activeTab === tab.value
                ? 'bg-white/10 text-white'
                : 'text-slate-500 hover:text-slate-300'
            }`}
          >
            {tab.label}
          </button>
        ))}
      </div>
      <div className="max-h-[180px] overflow-y-auto rounded-[8px] border border-white/[.06] bg-black/20 px-2 py-1.5 font-mono text-[10.5px] leading-relaxed space-y-0.5">
        {events.length === 0 && (
          <div className="py-3 text-center text-[11px] text-slate-600">Нет записей</div>
        )}
        {events.map((e, i) => (
          <div key={i} className="flex gap-2">
            <span className="shrink-0 text-slate-600">
              {new Date(e.created_at).toLocaleTimeString('ru', { hour: '2-digit', minute: '2-digit', second: '2-digit' })}
            </span>
            <span className={LEVEL_COLOR[e.level] ?? 'text-slate-400'}>{e.message}</span>
          </div>
        ))}
      </div>
    </div>
  )
}

// Svg shortcuts
const IcStop    = () => <svg width={12} height={12} viewBox="0 0 24 24" fill="currentColor"><rect x="4" y="4" width="16" height="16" rx="2"/></svg>
const IcPlay    = () => <svg width={11} height={11} viewBox="0 0 24 24" fill="currentColor"><path d="M5 3l14 9-14 9V3z"/></svg>
const IcGearSm  = () => <svg width={13} height={13} viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth={2} strokeLinecap="round" strokeLinejoin="round"><circle cx="12" cy="12" r="3"/><path d="M19.4 15a1.7 1.7 0 0 0 .3 1.8l.1.1a2 2 0 1 1-2.8 2.8l-.1-.1a1.7 1.7 0 0 0-1.8-.3 1.7 1.7 0 0 0-1 1.5V21a2 2 0 1 1-4 0v-.1A1.7 1.7 0 0 0 9 19.4a1.7 1.7 0 0 0-1.8.3l-.1.1a2 2 0 1 1-2.8-2.8l.1-.1a1.7 1.7 0 0 0 .3-1.8 1.7 1.7 0 0 0-1.5-1H3a2 2 0 1 1 0-4h.1A1.7 1.7 0 0 0 4.6 9a1.7 1.7 0 0 0-.3-1.8l-.1-.1a2 2 0 1 1 2.8-2.8l.1.1a1.7 1.7 0 0 0 1.8.3H9a1.7 1.7 0 0 0 1-1.5V3a2 2 0 1 1 4 0v.1a1.7 1.7 0 0 0 1 1.5 1.7 1.7 0 0 0 1.8-.3l.1-.1a2 2 0 1 1 2.8 2.8l-.1.1a1.7 1.7 0 0 0-.3 1.8V9a1.7 1.7 0 0 0 1.5 1H21a2 2 0 1 1 0 4h-.1a1.7 1.7 0 0 0-1.5 1z"/></svg>
const IcLogSm   = () => <svg width={13} height={13} viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth={2} strokeLinecap="round" strokeLinejoin="round"><path d="M4 6h16M4 12h16M4 18h10"/></svg>
const IcArchive = () => <svg width={12} height={12} viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth={2} strokeLinecap="round" strokeLinejoin="round"><path d="M21 8v13H3V8"/><path d="M23 3H1v5h22V3z"/><path d="M10 12h4"/></svg>
const IcZap     = () => <svg width={13} height={13} viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth={2} strokeLinecap="round" strokeLinejoin="round"><path d="M13 2L3 14h9l-1 8 10-12h-9l1-8z"/></svg>

function formatVolume(v: string): string {
  const n = parseFloat(v)
  if (isNaN(n)) return v
  if (n >= 1_000_000_000) return `${(n / 1_000_000_000).toFixed(2)}B`
  if (n >= 1_000_000) return `${(n / 1_000_000).toFixed(2)}M`
  if (n >= 1_000) return `${(n / 1_000).toFixed(2)}K`
  return n.toFixed(2)
}

function BotTerminalCard({ bot, sc, onSymbolChange, onStop, onStart, onEdit, onArchive, onScan, onToggleAuto }: {
  bot: Bot
  sc: { signalCount: number; totalCount: number } | undefined
  onSymbolChange: (s: string) => void
  onStop: () => void
  onStart: () => void
  onEdit: () => void
  onArchive: () => void
  onScan: () => void
  onToggleAuto: () => void
}) {
  const [logOpen, setLogOpen] = useState(false)
  const running = bot.status === 'active'

  const stratKey = (bot.strategyConfig.direction === 'long' || bot.strategyConfig.direction === 'short'
    ? 'trend' : 'grid') as BotStrategy
  const m = STRAT_META[stratKey]
  const Icon = m.icon

  const sym = bot.strategyConfig.symbol
    ?? (bot.symbolWhitelist.length === 1 && !bot.symbolWhitelist[0].includes('*')
      ? bot.symbolWhitelist[0]
      : null)

  const symbolsTotal  = sc ? String(sc.totalCount) : bot.symbolWhitelist.length === 0 ? 'все' : String(bot.symbolWhitelist.length)
  const symbolsSignal = sc ? String(sc.signalCount) : '—'

  return (
    <div className={
      'rounded-[14px] border p-3.5 transition-colors ' +
      (running
        ? 'border-[#5b8cff]/25 bg-[linear-gradient(180deg,rgba(91,140,255,.05)_0%,rgba(123,91,255,.03)_100%)] shadow-[0_12px_28px_-16px_rgba(91,140,255,.35)]'
        : 'border-white/[.06] bg-white/[.02]')
    }>

      {/* head */}
      <div className="mb-3 flex items-start gap-2.5">
        <div className="relative h-9 w-9 shrink-0">
          {running && (
            <span className="pointer-events-none absolute inset-[-1.5px] overflow-hidden rounded-[11px]" aria-hidden>
              <span
                className="absolute animate-spin"
                style={{
                  width: '200%', height: '200%', top: '-50%', left: '-50%',
                  background: 'conic-gradient(from 0deg, transparent 0deg, rgba(91,224,160,0.9) 50deg, transparent 100deg)',
                  animationDuration: '2.5s',
                }}
              />
            </span>
          )}
          <div
            className={'h-9 w-9 overflow-hidden rounded-[9px] border flex items-center justify-center ' + (running ? '' : 'opacity-50')}
            style={{ background: m.bg, borderColor: m.border, color: m.color }}
          >
            {bot.avatarUrl
              ? <img src={bot.avatarUrl} alt="" className="h-full w-full object-cover" />
              : <Icon size={16} strokeWidth={2} />
            }
          </div>
        </div>

        <div className="min-w-0 flex-1">
          <div className="flex items-center gap-1.5">
            <span className={'truncate font-display text-[13px] font-bold tracking-tight ' + (running ? 'text-slate-50' : 'text-slate-400')}>
              {bot.name.length > 30 ? bot.name.slice(0, 30) + '…' : bot.name}
            </span>
            {!bot.sourceBotId && (
              <span className="rounded-[3px] border border-[#c14dff]/30 bg-[#c14dff]/[.16] px-1.5 py-px text-[9px] font-bold uppercase tracking-wider text-[#d8a4ff]">
                Custom
              </span>
            )}
          </div>
          {bot.description ? (
            <div className="mt-0.5 truncate text-[11px] text-slate-500">{bot.description}</div>
          ) : (
            <div className="mt-0.5 flex items-center gap-1.5 font-mono text-[11px] text-slate-600">
              {sym ? (
                <button type="button" onClick={() => onSymbolChange(sym)}
                  className="font-semibold text-slate-500 hover:text-blue-300 transition-colors">
                  {sym}
                </button>
              ) : (
                <span className="font-semibold text-slate-500">multi</span>
              )}
              <span>·</span><span>bybit</span>
              <span>·</span><span>×{bot.strategyConfig.leverage ?? 1}</span>
              <span>·</span><span>{bot.strategyConfig.margin_type ?? 'isolated'}</span>
            </div>
          )}
        </div>

        {/* status badge */}
        {running ? (
          <span className="inline-flex items-center gap-1.5 rounded-full border px-2 py-0.5 pl-1.5 text-[10px] font-bold uppercase tracking-wider"
            style={{ background: 'rgba(65,210,139,.14)', borderColor: 'rgba(65,210,139,.32)', color: '#5be0a0' }}>
            <span className="h-1.5 w-1.5 rounded-full animate-pulse"
              style={{ background: '#5be0a0', boxShadow: '0 0 6px #5be0a0' }} />
            Активен
          </span>
        ) : (
          <span className="inline-flex items-center gap-1.5 rounded-full border px-2 py-0.5 pl-1.5 text-[10px] font-bold uppercase tracking-wider"
            style={{ background: 'rgba(255,255,255,.04)', borderColor: 'rgba(255,255,255,.10)', color: '#64748b' }}>
            <span className="h-1.5 w-1.5 rounded-full" style={{ background: '#64748b' }} />
            Стоп
          </span>
        )}
      </div>

      {/* stats row */}
      <div className="mb-3 grid grid-cols-3 border-y border-white/[.05] py-2 text-center">
        <div>
          <div className="mb-0.5 text-[10px] font-bold uppercase tracking-wider text-slate-500">Монет</div>
          <div className={'font-mono text-[12px] font-semibold ' + (running ? 'text-slate-50' : 'text-slate-500')}>{symbolsTotal}</div>
        </div>
        <div>
          <div className="mb-0.5 text-[10px] font-bold uppercase tracking-wider text-slate-500">Сигнал</div>
          <div className={'font-mono text-[12px] font-semibold ' + (running && sc ? 'text-emerald-400' : 'text-slate-500')}>{symbolsSignal}</div>
        </div>
        <div>
          <div className="mb-0.5 text-[10px] font-bold uppercase tracking-wider text-slate-500">Стратегий</div>
          <div className={'font-mono text-[12px] font-semibold ' + (running ? 'text-slate-50' : 'text-slate-500')}>
            {bot.activeStrategiesCount}/{bot.maxStrategies === 0 ? '∞' : bot.maxStrategies}
          </div>
        </div>
      </div>

      {/* actions */}
      <div className="flex gap-1.5">
        {running ? (
          <button type="button" onClick={onStop}
            className="inline-flex flex-1 items-center justify-center gap-1.5 rounded-lg border border-rose-400/28 bg-rose-400/[.10] px-3.5 py-2 text-xs font-semibold text-rose-300 hover:bg-rose-400/[.18]">
            <IcStop /> Остановить
          </button>
        ) : (
          <>
            <button type="button" onClick={onStart}
              className="inline-flex flex-1 items-center justify-center gap-1.5 rounded-lg border border-emerald-400/30 bg-emerald-400/[.12] px-3.5 py-2 text-xs font-semibold text-emerald-300 hover:bg-emerald-400/[.18]">
              <IcPlay /> Запустить
            </button>
            <button type="button" onClick={onArchive}
              className="inline-flex items-center justify-center gap-1 rounded-lg border border-white/[.07] bg-white/[.03] px-2.5 py-2 text-xs font-semibold text-slate-500 hover:bg-white/[.07] hover:text-slate-300"
              title="Скрыть из терминала">
              <IcArchive /> В архив
            </button>
          </>
        )}
        <button
          type="button"
          onClick={onToggleAuto}
          title={bot.autoMode ? 'Авто-режим включён — нажмите чтобы выключить' : 'Включить авто-режим'}
          className={`flex h-[34px] items-center justify-center gap-1 rounded-md border px-2 text-[10px] font-bold uppercase tracking-wider transition-colors ${
            bot.autoMode
              ? 'border-emerald-400/40 bg-emerald-400/[.14] text-emerald-300 hover:bg-emerald-400/[.22]'
              : 'border-white/[.08] bg-white/[.04] text-slate-500 hover:bg-white/[.08] hover:text-slate-300'
          }`}
        >
          AUTO
        </button>
        <button type="button" onClick={onScan}
          className="flex h-[34px] w-[34px] items-center justify-center rounded-md border border-[#a78bfa]/25 bg-[#5b3aed]/[.10] text-[#a78bfa] hover:bg-[#5b3aed]/[.20] transition-colors"
          title="Тест бота">
          <IcZap />
        </button>
        <button type="button" onClick={onEdit}
          className="flex h-[34px] w-[34px] items-center justify-center rounded-md border border-white/[.08] bg-white/[.04] text-slate-300 hover:bg-white/[.08]"
          title="Настройки">
          <IcGearSm />
        </button>
        <button type="button" onClick={() => setLogOpen(v => !v)}
          className={`flex h-[34px] w-[34px] items-center justify-center rounded-md border text-slate-300 transition-colors ${logOpen ? 'border-blue-400/30 bg-blue-400/[.12] text-blue-300' : 'border-white/[.08] bg-white/[.04] hover:bg-white/[.08]'}`}
          title="Лог">
          <IcLogSm />
        </button>
      </div>

      {logOpen && <BotEventLog botId={bot.id} open={logOpen} />}
    </div>
  )
}

const TERMINAL_HIDDEN_KEY = 'terminal_bots_hidden'

function loadHidden(): Set<string> {
  try { return new Set(JSON.parse(localStorage.getItem(TERMINAL_HIDDEN_KEY) ?? '[]')) }
  catch { return new Set() }
}
function saveHidden(s: Set<string>) {
  localStorage.setItem(TERMINAL_HIDDEN_KEY, JSON.stringify([...s]))
}

function TerminalBotsTab({ onSymbolChange }: { onSymbolChange: (sym: string) => void }) {
  const { mine, loading, action } = useBots()
  const { selectedAccountId } = useSelectedAccount()
  const signalCounts = useBotSignalCounts(true)
  const [editBotId, setEditBotId] = useState<string | null>(null)
  const [scanBot, setScanBot]     = useState<Bot | null>(null)
  const [hidden, setHidden] = useState<Set<string>>(loadHidden)

  // Боты текущего аккаунта + непривязанные боты пользователя
  const accountBots   = mine.filter(b => b.accountId === selectedAccountId)
  const unassignedBots = mine.filter(b => !b.accountId)
  const allVisibleBots = [...accountBots, ...unassignedBots]
  const visibleBots    = allVisibleBots.filter(b => !hidden.has(b.id))
  const runningCount   = visibleBots.filter(b => b.status === 'active').length
  const editBot        = editBotId ? allVisibleBots.find(b => b.id === editBotId) ?? null : null

  const [binding, setBinding] = useState(false)
  const bindAll = async () => {
    if (!selectedAccountId || unassignedBots.length === 0) return
    setBinding(true)
    try {
      await Promise.all(
        unassignedBots
          .filter(b => !hidden.has(b.id))
          .map(b => action({ type: 'update', botId: b.id, data: { accountId: selectedAccountId } }))
      )
    } finally {
      setBinding(false)
    }
  }

  function archive(botId: string) {
    setHidden(prev => {
      const next = new Set(prev)
      next.add(botId)
      saveHidden(next)
      return next
    })
  }

  const visibleUnassigned = unassignedBots.filter(b => !hidden.has(b.id))

  return (
    <div className="flex flex-col h-full">
      <div className="px-3 py-2 flex items-center justify-between border-b border-gray-200 dark:border-gray-700 flex-shrink-0">
        <span className="text-xs text-gray-500 dark:text-gray-400">
          {runningCount} активно · {visibleBots.length} всего
        </span>
        <Link to="/bots" className="text-xs text-blue-400 hover:text-blue-300 transition-colors">
          Все боты →
        </Link>
      </div>

      {/* Баннер: есть непривязанные боты */}
      {selectedAccountId && visibleUnassigned.length > 0 && (
        <div className="mx-2 mt-2 flex items-center gap-2 rounded-lg border border-amber-500/25 bg-amber-500/[.08] px-3 py-2">
          <span className="flex-1 text-[11px] text-amber-300">
            {visibleUnassigned.length} {visibleUnassigned.length === 1 ? 'бот не привязан' : 'ботов не привязаны'} к аккаунту
          </span>
          <button
            type="button"
            onClick={bindAll}
            disabled={binding}
            className="shrink-0 rounded-md border border-amber-500/30 bg-amber-500/[.15] px-2.5 py-1 text-[11px] font-semibold text-amber-200 hover:bg-amber-500/[.25] disabled:opacity-50 transition-colors"
          >
            {binding ? 'Привязываем...' : 'Привязать все'}
          </button>
        </div>
      )}

      <div className="flex-1 overflow-y-auto p-2 space-y-2">
        {loading && visibleBots.length === 0 && (
          <div className="p-8 text-center text-sm text-gray-400">Загрузка…</div>
        )}
        {!loading && visibleBots.length === 0 && (
          <div className="flex flex-col items-center gap-3 p-8 text-center">
            <span className="text-2xl opacity-20">🤖</span>
            <p className="text-sm text-gray-400">Нет ботов</p>
            <Link to="/bots" className="text-xs text-blue-400 hover:text-blue-300 transition-colors">
              Перейти к ботам
            </Link>
          </div>
        )}
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
          />
        ))}
      </div>

      {editBot && (
        <BotForm
          bot={editBot}
          onSubmit={async (data) => { await action({ type: 'update', botId: editBot.id, data }); setEditBotId(null) }}
          onClose={() => setEditBotId(null)}
        />
      )}
      {scanBot && (
        <BotScanModal bot={scanBot} onClose={() => setScanBot(null)} />
      )}
    </div>
  )
}

// ── Strategies tab ───────────────────────────────────────────────────────────
type LiveSignal = { signal_state: string; signal_values: Record<string, number> }

function TerminalStrategiesTab({ onSymbolChange, orders, positions, tickerPrices, accountId, onStrategySelect, onCycleNumUpdate }: { onSymbolChange: (sym: string) => void; orders: ActiveOrder[]; positions: Position[]; tickerPrices?: Map<string, number>; accountId: string | null; onStrategySelect?: (s: Strategy | null) => void; onCycleNumUpdate?: (id: string, cycleNum: number) => void }) {
  const [strategies, setStrategies] = useState<Strategy[]>([])
  const [accounts, setAccounts] = useState<ExchangeAccount[]>([])
  const [loading, setLoading] = useState(true)
  const [editTarget, setEditTarget] = useState<Strategy | undefined>()
  const [modalOpen, setModalOpen] = useState(false)
  const [selectedId, setSelectedId] = useState<string | null>(() => localStorage.getItem('t_sel'))
  const [expandedId, setExpandedId] = useState<string | null>(() => localStorage.getItem('t_exp'))
  const [signalStates, setSignalStates] = useState<Record<string, LiveSignal>>({})

  async function load() {
    setLoading(true)
    try {
      const [strats, accs] = await Promise.all([listStrategies(), listAccounts()])
      setStrategies(strats)
      setAccounts(accs)
    } catch {
      setStrategies([])
    } finally {
      setLoading(false)
    }
  }

  useEffect(() => { load() }, [])

  // Restore parent state after refresh when selected strategy was saved in localStorage.
  const restoredRef = useRef(false)
  useEffect(() => {
    if (restoredRef.current) return
    if (!loading && selectedId && strategies.length > 0) {
      const s = strategies.find(st => st.id === selectedId)
      if (s) {
        restoredRef.current = true
        onSymbolChange(s.symbol)
        onStrategySelect?.(s)
      }
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [loading, selectedId, strategies])

  useEffect(() => {
    let ws: WebSocket | null = null
    let reconnectTimer: ReturnType<typeof setTimeout> | null = null
    function connect() {
      const token = localStorage.getItem('token') ?? ''
      const proto = window.location.protocol === 'https:' ? 'wss:' : 'ws:'
      ws = new WebSocket(`${proto}//${window.location.host}/ws/strategies/updates?token=${encodeURIComponent(token)}`)
      ws.onmessage = (evt) => {
        try {
          const updates: { id: string; status: string; active_levels: number; volume_usdt: number; signal_state?: string; signal_values?: Record<string, number>; manual_alert?: string; cycle_num?: number }[] = JSON.parse(evt.data as string)
          if (updates.length > 0) {
            const deletedIds = new Set(updates.filter(u => u.status === 'deleted').map(u => u.id))
            setStrategies(prev => {
              let next = prev.filter(s => !deletedIds.has(s.id))
              next = next.map(s => {
                const upd = updates.find(u => u.id === s.id && u.status !== 'deleted')
                return upd ? { ...s, status: upd.status as Strategy['status'], active_levels: upd.active_levels, volume_usdt: upd.volume_usdt, manual_alert: upd.manual_alert, current_cycle_num: upd.cycle_num ?? s.current_cycle_num } : s
              })
              return next
            })
            for (const u of updates) {
              if (u.cycle_num != null) onCycleNumUpdate?.(u.id, u.cycle_num)
            }
            const sigUpdates = updates.filter(u => u.signal_state !== undefined && u.status !== 'deleted')
            if (sigUpdates.length > 0) {
              setSignalStates(prev => {
                const next = { ...prev }
                for (const u of sigUpdates) {
                  const newVals = u.signal_values && Object.keys(u.signal_values).length > 0
                    ? u.signal_values
                    : prev[u.id]?.signal_values ?? {}
                  next[u.id] = { signal_state: u.signal_state!, signal_values: newVals }
                }
                return next
              })
            }
          }
        } catch { /* ignore */ }
      }
      ws.onclose = () => { reconnectTimer = setTimeout(connect, 5000) }
      ws.onerror = () => ws?.close()
    }
    connect()
    return () => {
      if (reconnectTimer) clearTimeout(reconnectTimer)
      if (ws) { ws.onclose = null; ws.close() }
    }
  }, [])

  function handleSelect(s: Strategy) {
    setSelectedId(s.id)
    localStorage.setItem('t_sel', s.id)
    onSymbolChange(s.symbol)
    onStrategySelect?.(s)
    setExpandedId(prev => (prev !== null && prev !== s.id ? null : prev))
  }

  const activeAccountIds = useMemo(
    () => new Set(accounts.filter(a => a.is_active).map(a => a.id)),
    [accounts]
  )

  const visibleStrategies = strategies.filter(s => {
    if (!activeAccountIds.has(s.account_id)) return false
    if (accountId) return s.account_id === accountId
    return true
  })

  return (
    <div className="flex flex-col h-full">
      <div className="px-3 py-2 flex items-center justify-between border-b border-gray-200 dark:border-gray-700 flex-shrink-0">
        <span className="text-xs text-gray-500 dark:text-gray-400">{visibleStrategies.length} стратегий</span>
        <button
          disabled={!accountId}
          onClick={() => { setEditTarget(undefined); setModalOpen(true) }}
          title={!accountId ? 'Выберите активный аккаунт' : undefined}
          className="text-xs px-2.5 py-1 bg-blue-600 text-white rounded-lg hover:bg-blue-700 transition-colors disabled:opacity-40 disabled:cursor-not-allowed"
        >+ Новая стратегия</button>
      </div>
      <div className="flex-1 overflow-y-auto p-2 space-y-1.5">
        {loading && visibleStrategies.length === 0 && <div className="p-8 text-center text-sm text-gray-400">Загрузка…</div>}
        {!loading && visibleStrategies.length === 0 && (
          <div className="p-8 text-center text-sm text-gray-400">Нет стратегий</div>
        )}
        {sortStrategies(visibleStrategies).map(s => (
          <div key={s.id} className="origin-top-left scale-[0.96]">
            <StrategyCard
              strategy={s}
              accounts={accounts}
              orders={orders}
              positions={positions}
              tickerPrices={tickerPrices}
              onEdit={s => { setEditTarget(s); setModalOpen(true) }}
              onChanged={load}
              selected={s.id === selectedId}
              onSelect={handleSelect}
              isOpen={s.id === expandedId}
              liveSignal={signalStates[s.id]}
              onToggleOpen={() => {
                const isExpanding = expandedId !== s.id
                const newExp = isExpanding ? s.id : null
                setExpandedId(newExp)
                localStorage.setItem('t_exp', newExp ?? '')
                if (isExpanding) {
                  setSelectedId(s.id)
                  localStorage.setItem('t_sel', s.id)
                  onSymbolChange(s.symbol)
                }
              }}
            />
          </div>
        ))}
      </div>
      {modalOpen && (
        <StrategyModal
          strategy={editTarget}
          defaultAccountId={accountId ?? undefined}
          onClose={() => setModalOpen(false)}
          onSaved={() => { setModalOpen(false); load() }}
        />
      )}
    </div>
  )
}

// ── Main page ────────────────────────────────────────────────────────────────
export function TerminalPage() {
  const { selectedAccountId } = useSelectedAccount()
  const accountId = selectedAccountId || null
  const [searchParams, setSearchParams] = useSearchParams()

  const [symbol, setSymbol] = useState(() => searchParams.get('symbol') ?? _cachedSymbol)
  const [tf, setTf] = useState(() => searchParams.get('tf') ?? _cachedTf)
  const [bottomTab, setBottomTab] = useState<BottomTab>(() => (localStorage.getItem('t_bottom') as BottomTab) ?? 'positions')
  const [rightTab, setRightTab] = useState<RightTab>(() => (localStorage.getItem('t_right') as RightTab) ?? 'manual')
  const [mobileTab, setMobileTab] = useState<MobileTab>(() => (localStorage.getItem('t_mob') as MobileTab) ?? 'positions')

  function handleBottomTab(tab: BottomTab) { setBottomTab(tab); localStorage.setItem('t_bottom', tab) }
  function handleRightTab(tab: RightTab) {
    setRightTab(tab)
    localStorage.setItem('t_right', tab)
    // restore column width for this tab
    const key = tab === 'strategies' ? 't_col_strategies' : tab === 'bots' ? 't_col_bots' : 't_col'
    setColSplit(parseFloat(localStorage.getItem(key) ?? '68'))
  }
  function handleMobileTab(tab: MobileTab) { setMobileTab(tab); localStorage.setItem('t_mob', tab) }

  const [chartSettings, setChartSettings] = useState<ChartOverlaySettings>(() => {
    try {
      const stored = localStorage.getItem('t_chart_settings')
      if (stored) return { ...DEFAULT_CHART_SETTINGS, ...JSON.parse(stored) }
    } catch {}
    return DEFAULT_CHART_SETTINGS
  })
  const [selectedStrategy, setSelectedStrategy] = useState<Strategy | null>(null)
  const [strategyCycleNums, setStrategyCycleNums] = useState<Record<string, number>>({})
  const [strategyLevels, setStrategyLevels] = useState<StrategyLevel[]>([])

  useEffect(() => {
    if (!selectedStrategy?.id) { setStrategyLevels([]); return }
    const id = selectedStrategy.id
    const fetch = () => getStrategyState(id).then(s => {
      setStrategyLevels(s.levels ?? [])
      if (s.cycle_num) setStrategyCycleNums(prev => ({ ...prev, [id]: s.cycle_num }))
    }).catch(() => {})
    fetch()
    const t = setInterval(fetch, 3000)
    return () => clearInterval(t)
  }, [selectedStrategy?.id])
  const currentCycleNum = selectedStrategy ? (strategyCycleNums[selectedStrategy.id] ?? null) : null
  const stratIdShort = selectedStrategy ? selectedStrategy.id.slice(0, 8) : null

  const [rowSplit, setRowSplit] = useState(() => parseFloat(localStorage.getItem('t_row') ?? '65'))
  const containerRef = useRef<HTMLDivElement>(null)
  const rightTabRef = useRef(rightTab)
  useEffect(() => { rightTabRef.current = rightTab }, [rightTab])

  const [colSplit, setColSplit] = useState(() => {
    const tab = (localStorage.getItem('t_right') as RightTab) ?? 'manual'
    const key = tab === 'strategies' ? 't_col_strategies' : tab === 'bots' ? 't_col_bots' : 't_col'
    return parseFloat(localStorage.getItem(key) ?? '68')
  })

  const startDrag = useCallback((e: React.MouseEvent, type: 'col' | 'row') => {
    e.preventDefault()
    const rect = containerRef.current!.getBoundingClientRect()
    document.body.style.userSelect = 'none'
    document.body.style.cursor = type === 'col' ? 'col-resize' : 'row-resize'

    function onMove(ev: MouseEvent) {
      if (type === 'col') {
        const pct = Math.max(30, Math.min(82, ((ev.clientX - rect.left) / rect.width) * 100))
        setColSplit(pct)
        const key = rightTabRef.current === 'strategies' ? 't_col_strategies' : rightTabRef.current === 'bots' ? 't_col_bots' : 't_col'
        localStorage.setItem(key, String(pct))
      } else {
        const pct = Math.max(20, Math.min(80, ((ev.clientY - rect.top) / rect.height) * 100))
        setRowSplit(pct)
        localStorage.setItem('t_row', String(pct))
      }
    }
    function onUp() {
      document.removeEventListener('mousemove', onMove)
      document.removeEventListener('mouseup', onUp)
      document.body.style.userSelect = ''
      document.body.style.cursor = ''
    }
    document.addEventListener('mousemove', onMove)
    document.addEventListener('mouseup', onUp)
  }, [])


  useEffect(() => {
    _cachedSymbol = symbol
    localStorage.setItem('t_symbol', symbol)
    setSearchParams(prev => {
      const next = new URLSearchParams(prev)
      next.set('symbol', symbol)
      return next
    }, { replace: true })
  }, [symbol, setSearchParams])

  useEffect(() => {
    _cachedTf = tf
    localStorage.setItem('t_tf', tf)
    setSearchParams(prev => {
      const next = new URLSearchParams(prev)
      next.set('tf', tf)
      return next
    }, { replace: true })
  }, [tf, setSearchParams])

  const { positions, orders, executions, log, status, accountName, loading, reconnect, removeOrder } = usePositionsWs(accountId)

  const [historicalExecs, setHistoricalExecs] = useState<ChartExecution[]>([])
  useEffect(() => {
    if (!accountId) return
    listExecutions({ account_id: accountId, symbol, type: 'Trade', limit: 200 })
      .then(res => setHistoricalExecs(
        res.executions
          .filter(e => e.side && e.price && e.order_link_id?.startsWith('SIS_STR'))
          .map(e => ({
            execId: e.exec_id,
            symbol: e.symbol,
            side: e.side as 'Buy' | 'Sell',
            price: parseFloat(e.price!),
            qty: e.qty ?? '',
            timeMs: new Date(e.exec_time).getTime(),
            orderLinkId: e.order_link_id ?? undefined,
          }))
      ))
      .catch(() => {})
  }, [accountId, symbol])

  const allExecutions = useMemo<ChartExecution[]>(() => {
    const wsIds = new Set(executions.map(e => e.execId))
    return [...historicalExecs.filter(e => !wsIds.has(e.execId)), ...executions]
  }, [historicalExecs, executions])

  const { candles, candleSymbol, lastPrice, priceChange, turnover24h, loadMore } = useCandles(symbol, tf)
  const { bids, asks, spread } = useOrderbook(symbol)

  const positionSymbols = useMemo(() => [...new Set(positions.map(p => p.symbol))], [positions])
  const tickerPrices = useTickerPrices(positionSymbols)

  // Hedge mode (shared across right panel)
  const hedgeModeFromPositions = positions.some(p => p.symbol === symbol && p.positionIdx !== 0)
  const [hedgeModeOverride, setHedgeModeOverride] = useState<boolean | null>(null)
  const hedgeStorageKey = accountId ? `sis_hedge_${accountId}_${symbol}` : null

  useEffect(() => {
    if (!hedgeStorageKey) return
    const stored = localStorage.getItem(hedgeStorageKey)
    setHedgeModeOverride(stored === 'true' ? true : stored === 'false' ? false : null)
  }, [hedgeStorageKey])

  useEffect(() => {
    if (!hedgeStorageKey || hedgeModeOverride === null) return
    localStorage.setItem(hedgeStorageKey, String(hedgeModeOverride))
  }, [hedgeStorageKey, hedgeModeOverride])

  useEffect(() => {
    if (hedgeModeFromPositions) setHedgeModeOverride(true)
  }, [hedgeModeFromPositions])

  const hedgeMode = hedgeModeOverride !== null ? hedgeModeOverride : hedgeModeFromPositions


  const statusColor = {
    connecting: 'bg-yellow-400 animate-pulse',
    connected: 'bg-green-500',
    error: 'bg-red-500',
    closed: 'bg-red-500',
  }[status]

  const bottomTabs: { key: BottomTab; label: string; count?: number }[] = [
    { key: 'positions', label: 'Позиции', count: positions.length },
    { key: 'orders', label: 'Ордера', count: orders.length },
    { key: 'history', label: 'История' },
    { key: 'executions', label: 'Сделки' },
    { key: 'log', label: 'Лог' },
    { key: 'pnl', label: 'P&L' },
  ]

  const dividerV = (
    <div
      onMouseDown={e => startDrag(e, 'col')}
      className="flex-shrink-0 flex items-center justify-center group"
      style={{ width: 8, cursor: 'col-resize' }}
    >
      <div className="w-[3px] h-12 rounded-full bg-gray-700/40 group-hover:bg-blue-500/70 transition-colors" />
    </div>
  )

  const dividerH = (
    <div
      onMouseDown={e => startDrag(e, 'row')}
      className="flex-shrink-0 flex items-center justify-center group"
      style={{ height: 8, cursor: 'row-resize' }}
    >
      <div className="h-[3px] w-12 rounded-full bg-gray-700/40 group-hover:bg-blue-500/70 transition-colors" />
    </div>
  )

  const chartToolbar = (
    <div className="flex items-center gap-2 px-3 py-2 border-b border-gray-200 dark:border-gray-700 flex-shrink-0 overflow-x-auto">
      <CoinPicker value={symbol} onChange={setSymbol} />
      <div className="flex gap-1 flex-shrink-0">
        {TIMEFRAMES.map(t => (
          <button key={t.value} onClick={() => setTf(t.value)}
            className={`px-2 py-0.5 rounded text-xs font-medium transition-colors ${tf === t.value ? 'bg-blue-500 text-white' : 'text-gray-500 dark:text-gray-400 hover:text-gray-900 dark:hover:text-white'}`}>
            {t.label}
          </button>
        ))}
      </div>
      <div className="ml-auto flex items-center gap-2 flex-shrink-0">
        {lastPrice && (
          <>
            <span className="font-mono font-bold text-base text-gray-900 dark:text-white">{lastPrice}</span>
            <span className={`text-xs font-medium ${priceChange >= 0 ? 'text-green-500' : 'text-red-500'}`}>
              {priceChange >= 0 ? '+' : ''}{priceChange.toFixed(2)}%
            </span>
          </>
        )}
        {turnover24h && (
          <span className="text-[11px] text-slate-400 hidden sm:inline">
            Vol {formatVolume(turnover24h)}
          </span>
        )}
        <ChartSettingsPopup
          settings={chartSettings}
          onChange={s => { setChartSettings(s); localStorage.setItem('t_chart_settings', JSON.stringify(s)) }}
          strategyDir={selectedStrategy?.direction as 'long' | 'short' | null ?? null}
        />
      </div>
    </div>
  )

  const mobileTabs: { key: MobileTab; label: string; count?: number }[] = [
    { key: 'positions', label: 'Позиции', count: positions.length },
    { key: 'orders', label: 'Ордера', count: orders.length },
    { key: 'strategies', label: 'Стратегии' },
    { key: 'trade', label: 'Торговля' },
  ]

  return (
    <div
      ref={containerRef}
      className="bg-gray-100 dark:bg-[#07070f]"
      style={{ display: 'flex', height: 'calc(100vh - 65px)', padding: 10 }}
    >
      {/* ── Mobile layout ───────────────────────────────────────── */}
      <div className="flex md:hidden flex-col w-full h-full gap-2">
        {/* Chart */}
        <div className="bg-white dark:bg-gray-900/50 border border-gray-200 dark:border-gray-700/50 rounded-xl flex flex-col overflow-hidden" style={{ height: '42vh' }}>
          {chartToolbar}
          <div className="flex-1 min-h-0">
            <Chart candles={candles} candleSymbol={candleSymbol} positions={positions} orders={orders} executions={allExecutions} symbol={symbol} lastPrice={lastPrice} onLoadMore={loadMore} overlaySettings={chartSettings} strategyDir={selectedStrategy?.direction as 'long' | 'short' | null ?? null} stratIdShort={stratIdShort} currentCycleNum={currentCycleNum} strategyLevels={strategyLevels} tickerPrices={tickerPrices} />
          </div>
        </div>
        {/* Mobile tabs */}
        <div className="bg-white dark:bg-gray-900/50 border border-gray-200 dark:border-gray-700/50 rounded-xl flex flex-col overflow-hidden flex-1 min-h-0">
          <div className="flex items-center border-b border-gray-200 dark:border-gray-700 flex-shrink-0 px-2 overflow-x-auto">
            {mobileTabs.map(t => (
              <button key={t.key} onClick={() => handleMobileTab(t.key)}
                className={`flex items-center gap-1 px-3 py-2 text-xs font-medium border-b-2 transition-colors whitespace-nowrap ${mobileTab === t.key ? 'border-blue-500 text-blue-600 dark:text-white' : 'border-transparent text-gray-500 dark:text-gray-400 hover:text-gray-900 dark:hover:text-white'}`}>
                {t.label}
                {t.count != null && t.count > 0 && (
                  <span className="bg-gray-200 dark:bg-gray-700 text-[10px] px-1.5 py-px rounded-full">{t.count}</span>
                )}
              </button>
            ))}
            <div className="ml-auto flex items-center gap-1.5 flex-shrink-0 px-2">
              <span className={`w-2 h-2 rounded-full ${statusColor}`} />
              {(status === 'closed' || status === 'error') && (
                <button onClick={reconnect} className="text-gray-500 dark:text-gray-400 hover:text-gray-900 dark:hover:text-white text-xs">↺</button>
              )}
            </div>
          </div>
          <div className="flex-1 overflow-auto">
            {mobileTab === 'positions' && <PositionsTable accountId={accountId ?? ''} positions={positions} onSelect={setSymbol} loading={loading} tickerPrices={tickerPrices} />}
            {mobileTab === 'orders' && <OrdersTable accountId={accountId ?? ''} orders={orders} loading={loading} onSelect={setSymbol} onRemoveOrder={removeOrder} />}
            {mobileTab === 'strategies' && <TerminalStrategiesTab onSymbolChange={setSymbol} orders={orders} positions={positions} tickerPrices={tickerPrices} accountId={accountId} onStrategySelect={setSelectedStrategy} onCycleNumUpdate={(id, num) => setStrategyCycleNums(prev => ({ ...prev, [id]: num }))} />}
            {mobileTab === 'trade' && (
              <div className="flex flex-col gap-2 p-2 overflow-y-auto">
                {accountId ? (
                  <OrderForm accountId={accountId} symbol={symbol} onSymbolChange={setSymbol} lastPrice={lastPrice} orders={orders} hedgeMode={hedgeMode} />
                ) : (
                  <div className="flex flex-col items-center justify-center p-6 text-center text-gray-500 dark:text-gray-400 gap-3">
                    <span className="text-3xl opacity-30">🔑</span>
                    <p className="text-sm">Нет аккаунта Bybit. Добавьте API-ключи в настройках.</p>
                  </div>
                )}
                <GridForm accountId={accountId ?? ''} symbol={symbol} onSymbolChange={setSymbol} hedgeMode={hedgeMode} lastPrice={lastPrice} />
                <div style={{ height: 240 }} className="flex-shrink-0">
                  <Orderbook bids={bids} asks={asks} spread={spread} symbol={symbol} />
                </div>
              </div>
            )}
          </div>
        </div>
      </div>{/* /Mobile layout */}

      {/* ── Desktop layout ──────────────────────────────────────── */}
      <div className="hidden md:flex w-full h-full gap-2">
      {/* ── Left column ─────────────────────────────────────────── */}
      <div style={{ flex: `0 0 ${colSplit}%`, display: 'flex', flexDirection: 'column', minWidth: 0 }}>

        {/* Chart */}
        <div style={{ flex: `0 0 ${rowSplit}%`, minHeight: 0 }} className="bg-white dark:bg-gray-900/50 border border-gray-200 dark:border-gray-700/50 rounded-xl flex flex-col overflow-hidden">
          <div className="flex items-center gap-3 px-4 py-2 border-b border-gray-200 dark:border-gray-700 flex-shrink-0">
            <CoinPicker value={symbol} onChange={setSymbol} />
            <div className="flex gap-1">
              {TIMEFRAMES.map(t => (
                <button key={t.value} onClick={() => setTf(t.value)}
                  className={`px-2 py-0.5 rounded text-xs font-medium transition-colors ${tf === t.value ? 'bg-blue-500 text-white' : 'text-gray-500 dark:text-gray-400 hover:text-gray-900 dark:hover:text-white'}`}>
                  {t.label}
                </button>
              ))}
            </div>
            <div className="ml-auto flex items-center gap-2">
              {lastPrice && (
                <>
                  <span className="font-mono font-bold text-lg text-gray-900 dark:text-white">{lastPrice}</span>
                  <span className={`text-sm font-medium ${priceChange >= 0 ? 'text-green-500' : 'text-red-500'}`}>
                    {priceChange >= 0 ? '+' : ''}{priceChange.toFixed(2)}%
                  </span>
                </>
              )}
              {turnover24h && (
                <span className="text-[11px] text-slate-400 hidden sm:inline">
                  Vol {formatVolume(turnover24h)}
                </span>
              )}
              <ChartSettingsPopup
                settings={chartSettings}
                onChange={s => { setChartSettings(s); localStorage.setItem('t_chart_settings', JSON.stringify(s)) }}
                strategyDir={selectedStrategy?.direction as 'long' | 'short' | null ?? null}
              />
            </div>
          </div>
          <div className="flex-1 min-h-0">
            <Chart candles={candles} candleSymbol={candleSymbol} positions={positions} orders={orders} executions={allExecutions} symbol={symbol} lastPrice={lastPrice} onLoadMore={loadMore} overlaySettings={chartSettings} strategyDir={selectedStrategy?.direction as 'long' | 'short' | null ?? null} stratIdShort={stratIdShort} currentCycleNum={currentCycleNum} strategyLevels={strategyLevels} tickerPrices={tickerPrices} />
          </div>
        </div>

        {/* Horizontal divider */}
        {dividerH}

        {/* Bottom panel */}
        <div style={{ flex: 1, minHeight: 0 }} className="bg-white dark:bg-gray-900/50 border border-gray-200 dark:border-gray-700/50 rounded-xl flex flex-col overflow-hidden">
          <div className="flex items-center border-b border-gray-200 dark:border-gray-700 flex-shrink-0 px-2">
            {bottomTabs.map(t => (
              <button key={t.key} onClick={() => handleBottomTab(t.key)}
                className={`flex items-center gap-1.5 px-3 py-2 text-xs font-medium border-b-2 transition-colors ${bottomTab === t.key ? 'border-blue-500 text-blue-600 dark:text-white' : 'border-transparent text-gray-500 dark:text-gray-400 hover:text-gray-900 dark:hover:text-white'}`}>
                {t.label}
                {t.count != null && t.count > 0 && (
                  loading && (t.key === 'positions' || t.key === 'orders')
                    ? <span className="inline-block w-2 h-2 rounded-full bg-yellow-400 animate-pulse" />
                    : <span className="bg-gray-200 dark:bg-gray-700 text-xs px-1.5 py-0.5 rounded-full">{t.count}</span>
                )}
              </button>
            ))}
            {accountName && <span className="text-xs text-gray-500 dark:text-gray-400 ml-2">{accountName}</span>}
            <div className="ml-auto flex items-center gap-1.5">
              <span className={`w-2 h-2 rounded-full ${statusColor}`} />
              {(status === 'closed' || status === 'error') && (
                <button onClick={reconnect} className="text-gray-500 dark:text-gray-400 hover:text-gray-900 dark:hover:text-white text-xs">↺</button>
              )}
            </div>
          </div>
          <div className="flex-1 overflow-auto">
            {bottomTab === 'positions' && <PositionsTable accountId={accountId ?? ''} positions={positions} onSelect={setSymbol} loading={loading} tickerPrices={tickerPrices} />}
            {bottomTab === 'orders' && <OrdersTable accountId={accountId ?? ''} orders={orders} loading={loading} onSelect={setSymbol} onRemoveOrder={removeOrder} />}
            {bottomTab === 'history' && <HistoryTable accountId={accountId ?? undefined} symbol={symbol} />}
            {bottomTab === 'executions' && <ExecutionsTable accountId={accountId ?? undefined} />}
            {bottomTab === 'log' && <TradeLog log={log} />}
            {bottomTab === 'pnl' && <PnlTable accountId={accountId ?? undefined} />}
          </div>
        </div>

      </div>{/* /Left column */}

      {/* Vertical divider */}
      {dividerV}

      {/* ── Right panel ─────────────────────────────────────────── */}
      <div style={{ flex: 1, minWidth: 0 }} className="bg-white dark:bg-gray-900/50 border border-gray-200 dark:border-gray-700/50 rounded-xl flex flex-col overflow-hidden min-w-[360px]">

        {/* Tab bar */}
        <div className="p-3 pb-2.5 border-b border-gray-200 dark:border-white/[.06] flex-shrink-0">
          <div className="grid grid-cols-3 gap-0.5 p-[3px] bg-white/[.04] border border-white/[.06] rounded-[11px]">
            {([
              { key: 'manual'    , label: 'Торговля',  icon: <svg width={14} height={14} viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeLinecap="round" strokeLinejoin="round"><path d="M3 17l5-5 4 4 4-7 5 8"/><circle cx="21" cy="5" r="1.4" fill="currentColor" stroke="none"/></svg> },
              { key: 'strategies', label: 'Стратегии', icon: <svg width={14} height={14} viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeLinecap="round" strokeLinejoin="round"><rect x="3" y="3" width="7" height="7" rx="1.5"/><rect x="14" y="3" width="7" height="7" rx="1.5"/><rect x="3" y="14" width="7" height="7" rx="1.5"/><rect x="14" y="14" width="7" height="7" rx="1.5"/></svg> },
              { key: 'bots'      , label: 'Боты',      icon: <svg width={14} height={14} viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeLinecap="round" strokeLinejoin="round"><rect x="4" y="7" width="16" height="12" rx="3"/><circle cx="9" cy="13" r="1.2" fill="currentColor" stroke="none"/><circle cx="15" cy="13" r="1.2" fill="currentColor" stroke="none"/><path d="M12 4v3M9 19v2M15 19v2"/></svg> },
            ] as { key: RightTab; label: string; icon: React.ReactNode }[]).map(t => (
              <button
                key={t.key}
                onClick={() => handleRightTab(t.key)}
                className={`inline-flex items-center justify-center gap-1.5 py-2 px-1.5 rounded-[8px] text-[12.5px] font-semibold transition-all leading-none ${
                  rightTab === t.key
                    ? 'bg-[linear-gradient(180deg,#4a7dff,#3a67e6)] text-white shadow-[inset_0_1px_0_rgba(255,255,255,.18),0_4px_10px_-6px_rgba(74,125,255,.6)]'
                    : 'text-slate-400 hover:text-slate-200'
                }`}
                style={{ strokeWidth: rightTab === t.key ? 2 : 1.8 }}
              >
                {t.icon}{t.label}
              </button>
            ))}
          </div>
        </div>

        {/* Tab content */}
        <div className="flex-1 overflow-y-auto flex flex-col">
          {rightTab === 'manual' && (
            <>
              {accountId ? (
                <OrderForm
                  accountId={accountId}
                  symbol={symbol}
                  onSymbolChange={setSymbol}
                  lastPrice={lastPrice}
                  orders={orders}
                  hedgeMode={hedgeMode}
                />
              ) : (
                <div className="flex flex-col items-center justify-center p-6 text-center text-gray-500 dark:text-gray-400 gap-3 border-b border-gray-200 dark:border-gray-700">
                  <span className="text-3xl opacity-30">🔑</span>
                  <p className="text-sm">Нет аккаунта Bybit. Добавьте API-ключи в настройках.</p>
                </div>
              )}
              <GridForm
                accountId={accountId ?? ''}
                symbol={symbol}
                onSymbolChange={setSymbol}
                hedgeMode={hedgeMode}
                lastPrice={lastPrice}
              />
              <div style={{ height: 280 }} className="flex-shrink-0">
                <Orderbook bids={bids} asks={asks} spread={spread} symbol={symbol} />
              </div>
            </>
          )}
          {rightTab === 'strategies' && <TerminalStrategiesTab onSymbolChange={setSymbol} orders={orders} positions={positions} tickerPrices={tickerPrices} accountId={accountId} onStrategySelect={setSelectedStrategy} onCycleNumUpdate={(id, num) => setStrategyCycleNums(prev => ({ ...prev, [id]: num }))} />}
          {rightTab === 'bots' && <TerminalBotsTab onSymbolChange={setSymbol} />}
        </div>

      </div>{/* /Right panel */}

    </div>{/* /Desktop layout */}
    </div>
  )
}
