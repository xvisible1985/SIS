import { useState, useEffect, useMemo, useRef, useCallback } from 'react'
import { createPortal } from 'react-dom'
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
import { DebugLogTab } from '../components/terminal/DebugLogTab'
import { GridForm } from '../components/terminal/GridForm'
import { CoinPicker } from '../components/common/CoinPicker'
import { usePositionsWs } from '../hooks/terminal/usePositionsWs'
import { useCandles } from '../hooks/terminal/useCandles'
import { useOrderbook } from '../hooks/terminal/useOrderbook'
import { useTickerPrices } from '../hooks/terminal/useTickerPrices'
import { listAccounts } from '../api/accounts'
import { useSelectedAccount } from '../contexts/AccountContext'
import { listStrategies, getStrategyState, setStrategyStatus, deleteStrategy } from '../api/strategies'
import { listExecutions, placeOrder } from '../api/trader'
import { StrategyCard } from '../components/strategies/StrategyCard'
import { TAKER_FEE } from '../components/common/ClosePositionModal'
import { HedgePairCard } from '../components/strategies/HedgePairCard'
import { StrategyModal } from '../components/strategies/StrategyModal'
import { useBots } from '../features/bots/api'
import { useAuth } from '../hooks/useAuth'
import { useSystemHealth } from '../hooks/useSystemHealth'
import { useAdminUsers } from '../features/admin-users/api'
import type { AdminUser } from '../features/admin-users/types'
import { useBotSignalCounts } from '../hooks/useBotSignalCounts'
import { useBotEventsWs, type BotEventCategory } from '../hooks/useBotEventsWs'
import { BotForm } from '../features/bots/components/BotForm'
import { HedgeBotForm } from '../features/bots/components/HedgeBotForm'
import { MatrixBotForm } from '../features/bots/components/MatrixBotForm'
import { BotScanModal } from '../features/bots/components/BotScanModal'
import { getBotKindMeta } from '../features/bots/botKindMeta'
import { TrendingUp, Search, Shield, Layers, X } from 'lucide-react'
import type { LucideIcon } from 'lucide-react'
import type { Bot, BotKind, BotAction } from '../features/bots/types'
import type { Strategy, ExchangeAccount, ActiveOrder, Position, ChartExecution, StrategyLevel } from '../types'
import { HedgeBotOverlay } from '../components/terminal/HedgeBotOverlay'

const KIND_ICONS: Record<BotKind, LucideIcon> = {
  signal: TrendingUp,
  parser: Search,
  hedge:  Shield,
  matrix: Layers,
}

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

type BottomTab = 'positions' | 'orders' | 'history' | 'executions' | 'log' | 'pnl' | 'debug'
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
  showSafeZone: true,
  showSLMarkers: true,
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
    { key: 'showSafeZone',     label: 'Safe Zone' },
    { key: 'showSLMarkers',    label: 'Маркеры SL' },
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
              {new Date(e.created_at).toLocaleString('ru', { day: '2-digit', month: '2-digit', hour: '2-digit', minute: '2-digit', second: '2-digit' })}
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

  const km   = getBotKindMeta(bot.strategyConfig.bot_kind)
  const Icon = KIND_ICONS[bot.strategyConfig.bot_kind ?? 'signal']

  const sym = bot.strategyConfig.symbol
    ?? (bot.symbolWhitelist.length === 1 && !bot.symbolWhitelist[0].includes('*')
      ? bot.symbolWhitelist[0]
      : null)

  const symbolsTotal  = sc ? String(sc.totalCount) : bot.symbolWhitelist.length === 0 ? 'все' : String(bot.symbolWhitelist.length)
  const symbolsSignal = sc ? String(sc.signalCount) : '—'

  return (
    <div
      className="rounded-[14px] border p-3.5 transition-colors"
      style={running ? {
        borderColor: km.border,
        background:  km.bgHeader,
        boxShadow:   `0 12px 28px -16px ${km.border}`,
      } : {
        borderColor: 'rgba(255,255,255,0.06)',
        background:  'rgba(255,255,255,0.02)',
      }}
    >

      {/* head */}
      <div className="mb-3 flex items-start gap-2.5">
        <div className="relative h-9 w-9 shrink-0">
          {running && (
            bot.strategyConfig.bot_kind === 'hedge'
              ? [0, 0.8, 1.6].map((delay, i) => (
                  <span
                    key={i}
                    aria-hidden
                    className="pointer-events-none absolute inset-0 rounded-[9px] border"
                    style={{
                      borderColor: km.color,
                      opacity: 0,
                      animation: `hedge-ring 2.4s ease-out ${delay}s infinite`,
                    }}
                  />
                ))
              : (
                  <span aria-hidden className="pointer-events-none absolute inset-[-1.5px] overflow-hidden rounded-[11px]">
                    <span
                      className="absolute animate-spin"
                      style={{
                        width: '200%', height: '200%', top: '-50%', left: '-50%',
                        background: `conic-gradient(from 0deg, transparent 0deg, ${km.color}cc 50deg, transparent 100deg)`,
                        animationDuration: '2.5s',
                      }}
                    />
                  </span>
                )
          )}
          <div
            className={'h-9 w-9 overflow-hidden rounded-[9px] border flex items-center justify-center ' + (running ? '' : 'opacity-50')}
            style={{ background: km.iconBg, borderColor: km.border, color: km.color }}
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
            <div className="mt-0.5 flex items-center gap-1.5 text-[11px] text-slate-600">
              {sym ? (
                <button type="button" onClick={() => onSymbolChange(sym)}
                  className="font-mono font-semibold text-slate-500 hover:text-blue-300 transition-colors">
                  {sym}
                </button>
              ) : (
                <span className="font-mono font-semibold text-slate-500">multi</span>
              )}
              <span>·</span>
              <span style={{ color: km.color }}>{km.label}</span>
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

function TerminalBotsTab({ onSymbolChange, mine, loading, action }: {
  onSymbolChange: (sym: string) => void
  mine: Bot[]
  loading: boolean
  action: (a: BotAction) => Promise<void>
}) {
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

      {editBot && editBot.strategyConfig.bot_kind === 'hedge' && (
        <HedgeBotForm
          bot={editBot}
          onSubmit={async (data) => { await action({ type: 'update', botId: editBot.id, data }); setEditBotId(null) }}
          onClose={() => setEditBotId(null)}
        />
      )}
      {editBot && editBot.strategyConfig.bot_kind === 'matrix' && (
        <MatrixBotForm
          bot={editBot}
          onSubmit={async (data) => { await action({ type: 'update', botId: editBot.id, data }); setEditBotId(null) }}
          onClose={() => setEditBotId(null)}
        />
      )}
      {editBot && editBot.strategyConfig.bot_kind !== 'hedge' && editBot.strategyConfig.bot_kind !== 'matrix' && (
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

// ── Hedge monitoring helper ───────────────────────────────────────────────────

/**
 * Returns true if `strategy` is being monitored as a potential main position
 * by at least one active hedge bot.
 *
 * Rules (mirror the backend hedge_engine.go logic):
 *  - Hedge bot must be active and linked to the same account
 *  - Strategy symbol must pass the bot's symbol whitelist/blacklist
 *  - Strategy direction must match the bot's direction filter
 *  - Strategy's bot_id must pass the bot's hedge_bot_whitelist/blacklist
 *  - The strategy itself must NOT be a hedge strategy created by this hedge bot
 */
function countHedgeWatchers(strategy: Strategy, hedgeBots: Bot[]): number {
  return hedgeBots.filter(bot => {
    if (bot.status !== 'active') return false
    if (bot.strategyConfig.bot_kind !== 'hedge') return false
    if (bot.accountId && bot.accountId !== strategy.account_id) return false
    // Exclude the hedge bot's own strategies (the hedge strategies themselves)
    if (strategy.bot_id === bot.id) return false

    // Symbol filter
    const sym = strategy.symbol
    if (bot.symbolBlacklist.includes(sym)) return false
    if (bot.symbolWhitelist.length > 0 && !bot.symbolWhitelist.includes(sym)) return false

    // Direction filter (matches engine: bot.direction = direction of MAIN position)
    const botDir = bot.strategyConfig.direction ?? 'both'
    if (botDir !== 'both' && botDir !== strategy.direction) return false

    // Bot whitelist/blacklist (hedge_bot_whitelist/blacklist filter on main strategy's bot)
    const hwl = (bot.strategyConfig.hedge_bot_whitelist ?? []) as string[]
    const hbl = (bot.strategyConfig.hedge_bot_blacklist ?? []) as string[]
    const stratBot = strategy.bot_id ?? ''
    if (hbl.includes(stratBot)) return false
    if (hwl.length > 0 && !hwl.includes(stratBot)) return false

    return true
  }).length
}

// ── Strategies tab ───────────────────────────────────────────────────────────
type LiveSignal = { signal_state: string; signal_values: Record<string, number> }

function TerminalStrategiesTab({ onSymbolChange, orders, positions, tickerPrices, accountId, asAccountId, onStrategySelect, onCycleNumUpdate, onStrategiesChange, onPairTargetUpdate, freeMargin, hedgeBots }: { onSymbolChange: (sym: string) => void; orders: ActiveOrder[]; positions: Position[]; tickerPrices?: Map<string, number>; accountId: string | null; asAccountId?: string; onStrategySelect?: (s: Strategy | null) => void; onCycleNumUpdate?: (id: string, cycleNum: number) => void; onStrategiesChange?: (strategies: Strategy[]) => void; onPairTargetUpdate?: (target: number | null) => void; freeMargin?: number | null; hedgeBots: Bot[] }) {
  const [strategies, setStrategies] = useState<Strategy[]>([])
  const [accounts, setAccounts] = useState<ExchangeAccount[]>([])
  const [loading, setLoading] = useState(true)
  const [editTarget, setEditTarget] = useState<Strategy | undefined>()
  const [modalOpen, setModalOpen] = useState(false)
  const [selectedId, setSelectedId] = useState<string | null>(() => localStorage.getItem('t_sel'))
  const [expandedId, setExpandedId] = useState<string | null>(null)
  const [signalStates, setSignalStates] = useState<Record<string, LiveSignal>>({})
  const [selectedIds, setSelectedIds] = useState<Set<string>>(new Set())
  const [lastSelectedId, setLastSelectedId] = useState<string | null>(null)
  const [bulkAction, setBulkAction] = useState<{ type: 'close' | 'delete'; ids: Set<string> } | null>(null)

  // Ref для быстрого доступа к текущему списку из WS-обработчика (без closure-захвата)
  const strategiesRef = useRef<Strategy[]>([])
  useEffect(() => { strategiesRef.current = strategies }, [strategies])

  // Bubble strategies up to TerminalPage so HedgeBotOverlay always has fresh data
  useEffect(() => { onStrategiesChange?.(strategies) }, [strategies, onStrategiesChange])

  async function load() {
    setLoading(true)
    try {
      const [strats, accs] = await Promise.all([listStrategies(asAccountId), listAccounts()])
      setStrategies(strats)
      setAccounts(accs)
    } catch {
      setStrategies([])
    } finally {
      setLoading(false)
    }
  }


  // eslint-disable-next-line react-hooks/exhaustive-deps
  useEffect(() => { load() }, [asAccountId])

  useEffect(() => {
    function onCreated() { load() }
    window.addEventListener('strategy-created', onCreated)
    return () => window.removeEventListener('strategy-created', onCreated)
  }, [])

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

            // Новые стратегии — ID есть в апдейте, но нет в текущем списке
            const existingIds = new Set(strategiesRef.current.map(s => s.id))
            const hasNew = updates.some(u => u.status !== 'deleted' && !existingIds.has(u.id))
            if (hasNew) {
              // Тихий рефетч — без setLoading, список придёт с полными полями
              void listStrategies().then(strats => setStrategies(strats)).catch(() => {})
            }

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

  function handleSelect(s: Strategy, e?: React.MouseEvent) {
    if (e?.ctrlKey || e?.metaKey) {
      // Ctrl/Cmd+click: toggle this card
      setLastSelectedId(s.id)
      setSelectedIds(prev => {
        const next = new Set(prev)
        next.has(s.id) ? next.delete(s.id) : next.add(s.id)
        return next
      })
      return
    }
    if (e?.shiftKey) {
      // Shift+click: range from last selected to this
      if (lastSelectedId && allSingleIds.length > 0) {
        const fromIdx = allSingleIds.indexOf(lastSelectedId)
        const toIdx = allSingleIds.indexOf(s.id)
        if (fromIdx !== -1 && toIdx !== -1) {
          const [a, b] = [Math.min(fromIdx, toIdx), Math.max(fromIdx, toIdx)]
          setSelectedIds(prev => {
            const next = new Set(prev)
            allSingleIds.slice(a, b + 1).forEach(id => next.add(id))
            return next
          })
        }
      } else {
        setSelectedIds(new Set([s.id]))
      }
      setLastSelectedId(s.id)
      return
    }
    // Normal click: clear multi-selection, single-select
    setSelectedIds(new Set())
    setLastSelectedId(s.id)
    setSelectedId(s.id)
    localStorage.setItem('t_sel', s.id)
    onSymbolChange(s.symbol)
    onStrategySelect?.(s)
    setExpandedId(prev => (prev !== null && prev !== s.id ? null : prev))
    onPairTargetUpdate?.(null)
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

  // Вычисляем hedge-флаги один раз для всего списка
  const hedgeBotIds = useMemo(
    () => new Set(hedgeBots.filter(b => b.strategyConfig.bot_kind === 'hedge').map(b => b.id)),
    [hedgeBots],
  )

  const hedgeInfoMap = useMemo(() => {
    const map = new Map<string, { hasActiveHedge: boolean; isHedgeItself: boolean }>()
    for (const s of visibleStrategies) {
      const isHedgeItself = !!s.bot_id && hedgeBotIds.has(s.bot_id)
      let hasActiveHedge = false
      if (!isHedgeItself) {
        const oppDir = s.direction === 'long' ? 'short' : 'long'
        hasActiveHedge = strategies.some(h =>
          !!h.bot_id &&
          hedgeBotIds.has(h.bot_id) &&
          h.symbol === s.symbol &&
          h.account_id === s.account_id &&
          h.direction === oppDir &&
          (h.status === 'active' || h.status === 'finishing'),
        )
      }
      map.set(s.id, { hasActiveHedge, isHedgeItself })
    }
    return map
  }, [visibleStrategies, strategies, hedgeBotIds])

  // Group strategies: hedge pairs → HedgePairCard, standalone → StrategyCard
  const renderItems = useMemo(() => {
    const sorted = sortStrategies(visibleStrategies)
    const usedIds = new Set<string>()
    const items: Array<
      | { type: 'single'; strategy: typeof sorted[0] }
      | { type: 'pair'; main: typeof sorted[0]; hedge: typeof sorted[0] }
    > = []

    for (const s of sorted) {
      if (usedIds.has(s.id)) continue

      // Method 1: direct link via hedged_strategy_id (most reliable — set by backend)
      if (s.hedged_strategy_id) {
        const partner = sorted.find(h => !usedIds.has(h.id) && h.id === s.hedged_strategy_id)
        if (partner) {
          usedIds.add(s.id)
          usedIds.add(partner.id)
          // Determine roles using isHedgeItself: the hedge-bot-controlled strategy is "hedge"
          const sIsHedge = !!hedgeInfoMap.get(s.id)?.isHedgeItself
          const [main, hedge] = sIsHedge ? [partner, s] : [s, partner]
          items.push({ type: 'pair', main, hedge })
          continue
        }
      }

      // Method 2: hedgeInfoMap-based detection (handles reverse link too)
      const info = hedgeInfoMap.get(s.id)
      if (info?.hasActiveHedge || info?.isHedgeItself) {
        const isMain = !!info.hasActiveHedge
        const partner = sorted.find(h =>
          !usedIds.has(h.id) &&
          h.symbol === s.symbol &&
          h.account_id === s.account_id &&
          h.direction !== s.direction &&
          (isMain
            ? hedgeInfoMap.get(h.id)?.isHedgeItself
            : hedgeInfoMap.get(h.id)?.hasActiveHedge),
        )
        if (partner) {
          usedIds.add(s.id)
          usedIds.add(partner.id)
          const [main, hedge] = isMain ? [s, partner] : [partner, s]
          items.push({ type: 'pair', main, hedge })
          continue
        }
      }

      usedIds.add(s.id)
      // Hedge-bot strategies (isHedgeItself) that couldn't pair are suppressed —
      // they're managed from HedgePairCard. When bot is detached, bot_id becomes
      // null → isHedgeItself = false → strategy reappears as a standalone card.
      if (hedgeInfoMap.get(s.id)?.isHedgeItself) continue
      items.push({ type: 'single', strategy: s })
    }

    // Post-filter: suppress stopped standalone strategies whose symbol+account+direction
    // matches the hedge side of an active pair — these are "ghost" hedges left after a
    // detach → bot-creates-new-hedge cycle. Active/finishing singles are never suppressed.
    const hedgeCoveredCombos = new Set<string>()
    for (const item of items) {
      if (item.type === 'pair') {
        hedgeCoveredCombos.add(`${item.hedge.symbol}:${item.hedge.account_id}:${item.hedge.direction}`)
      }
    }
    return items.filter(item => {
      if (item.type !== 'single') return true
      const s = item.strategy
      if (s.status !== 'stopped') return true
      return !hedgeCoveredCombos.has(`${s.symbol}:${s.account_id}:${s.direction}`)
    })
  }, [visibleStrategies, hedgeInfoMap])

  const allSingleIds = useMemo(
    () => renderItems.filter(i => i.type === 'single').map(i => (i as { type: 'single'; strategy: Strategy }).strategy.id),
    [renderItems],
  )
  const allSelected = allSingleIds.length > 0 && allSingleIds.every(id => selectedIds.has(id))

  function handleSelectAll() {
    if (allSelected) { setSelectedIds(new Set()); setLastSelectedId(null) }
    else setSelectedIds(new Set(allSingleIds))
  }

  async function handleBulkStatus(status: 'active' | 'finishing' | 'stopped') {
    if (selectedIds.size === 0) return
    await Promise.allSettled([...selectedIds].map(id => setStrategyStatus(id, status)))
    load()
  }

  async function handleBulkDelete(ids: Set<string>) {
    if (ids.size === 0) return
    await Promise.allSettled([...ids].map(id => deleteStrategy(id)))
    setSelectedIds(new Set())
    load()
  }

  async function handleBulkClose(ids: Set<string>) {
    if (ids.size === 0) return
    const toClose = strategies.filter(s => ids.has(s.id))
    await Promise.allSettled(toClose.map(s => {
      const pos = positions.find(p =>
        p.symbol === s.symbol &&
        p.positionIdx === (s.direction === 'long' ? 1 : 2) &&
        parseFloat(p.size) > 0,
      )
      if (!pos) return Promise.resolve()
      return placeOrder({
        account_id: s.account_id,
        symbol: pos.symbol,
        category: pos.category,
        side: pos.side === 'Buy' ? 'Sell' : 'Buy',
        order_type: 'Market',
        qty: pos.size,
        reduce_only: true,
        position_idx: pos.positionIdx,
      })
    }))
    load()
  }

  return (
    <div className="flex flex-col h-full">
      <div className="flex-shrink-0">
        <div className="px-3 py-2 flex items-center justify-between">
          <span className="text-xs text-gray-500 dark:text-gray-400">{visibleStrategies.length} стратегий</span>
          <button
            disabled={!accountId}
            onClick={() => { setEditTarget(undefined); setModalOpen(true) }}
            title={!accountId ? 'Выберите активный аккаунт' : undefined}
            className="text-xs px-2.5 py-1 bg-blue-600 text-white rounded-lg hover:bg-blue-700 transition-colors disabled:opacity-40 disabled:cursor-not-allowed"
          >+ Новая стратегия</button>
        </div>
        <div className="border-t border-gray-200 dark:border-white/[.07] px-3 py-1.5 flex items-center gap-1.5">
          <span className="text-[11px] text-slate-500 uppercase tracking-[.6px] font-semibold">Свободная маржа</span>
          <span className={`text-[12px] font-semibold ${freeMargin == null ? 'text-slate-600' : 'text-slate-200'}`}>
            {freeMargin == null ? '—' : `${freeMargin.toFixed(2)}$`}
          </span>
        </div>
      </div>
      <div className="flex-1 overflow-y-auto p-2 space-y-1.5 strategies-scroll">
        {loading && visibleStrategies.length === 0 && <div className="p-8 text-center text-sm text-gray-400">Загрузка…</div>}
        {!loading && visibleStrategies.length === 0 && (
          <div className="p-8 text-center text-sm text-gray-400">Нет стратегий</div>
        )}
        {renderItems.map(item => {
          if (item.type === 'pair') {
            return (
              <div key={`pair-${item.main.id}`} className="origin-top-left scale-[0.96]">
                <HedgePairCard
                  main={item.main}
                  hedge={item.hedge}
                  accounts={accounts}
                  orders={orders}
                  positions={positions}
                  tickerPrices={tickerPrices}
                  selectedStrategyId={selectedId}
                  hedgeBot={hedgeBots.find(b => b.id === item.hedge.bot_id) ?? null}
                  onEdit={s => { setEditTarget(s); setModalOpen(true) }}
                  onChanged={load}
                  onSelect={handleSelect}
                  onPairTargetUpdate={onPairTargetUpdate}
                />
              </div>
            )
          }
          const s = item.strategy
          return (
            <div key={s.id} className="origin-top-left scale-[0.96]">
              <StrategyCard
                strategy={s}
                accounts={accounts}
                orders={orders}
                positions={positions}
                tickerPrices={tickerPrices}
                onEdit={s => { setEditTarget(s); setModalOpen(true) }}
                onChanged={load}
                selected={selectedIds.size > 0 ? selectedIds.has(s.id) : s.id === selectedId}
                onSelect={handleSelect}
                bulkMode={selectedIds.size > 0}
                selectedCount={selectedIds.size}
                onBulkStatus={handleBulkStatus}
                onBulkDelete={() => setBulkAction({ type: 'delete', ids: new Set(selectedIds) })}
                onBulkClose={() => setBulkAction({ type: 'close', ids: new Set(selectedIds) })}
                isOpen={s.id === expandedId}
                liveSignal={signalStates[s.id]}
                hedgeWatcherCount={countHedgeWatchers(s, hedgeBots)}
                hasActiveHedge={hedgeInfoMap.get(s.id)?.hasActiveHedge}
                isHedgeItself={hedgeInfoMap.get(s.id)?.isHedgeItself}
                onToggleOpen={() => {
                  const isExpanding = expandedId !== s.id
                  const newExp = isExpanding ? s.id : null
                  setExpandedId(newExp)
                  if (isExpanding) {
                    setSelectedId(s.id)
                    localStorage.setItem('t_sel', s.id)
                    onSymbolChange(s.symbol)
                  }
                }}
              />
            </div>
          )
        })}
      </div>
      {modalOpen && (
        <StrategyModal
          strategy={editTarget}
          defaultAccountId={accountId ?? undefined}
          onClose={() => setModalOpen(false)}
          onSaved={() => { setModalOpen(false); load() }}
          liveSignal={editTarget ? signalStates[editTarget.id] : undefined}
        />
      )}

      {bulkAction && (() => {
        const { type: actionType, ids: actionIds } = bulkAction
        const selectedStrategies = strategies.filter(s => actionIds.has(s.id))
        const closablePositions = actionType === 'close'
          ? selectedStrategies.map(s => positions.find(p =>
              p.symbol === s.symbol &&
              p.positionIdx === (s.direction === 'long' ? 1 : 2) &&
              parseFloat(p.size) > 0,
            )).filter(Boolean) as typeof positions
          : []
        const totalPnl = closablePositions.reduce((sum, p) => sum + parseFloat(p.unrealisedPnl), 0)
        const totalOpenFee = closablePositions.reduce((sum, p) => sum + parseFloat(p.size) * parseFloat(p.entryPrice) * TAKER_FEE, 0)
        const totalCloseFee = closablePositions.reduce((sum, p) => sum + p.sizeUsdt * TAKER_FEE, 0)
        const netPnl = totalPnl - totalOpenFee - totalCloseFee
        const isClose = actionType === 'close'
        return createPortal(
          <div className="fixed inset-0 z-[9999] flex items-center justify-center" style={{ background: 'rgba(0,0,0,0.80)' }} onClick={() => setBulkAction(null)}>
            <div className="rounded-xl p-5 w-80" style={{ background: '#12141c', border: '1px solid rgba(255,255,255,.14)', boxShadow: '0 32px 80px rgba(0,0,0,1)' }} onClick={e => e.stopPropagation()}>
              <h3 className="text-sm font-semibold text-white mb-4">
                {isClose ? `Закрыть позиции (${closablePositions.length} из ${selectedStrategies.length})` : `Удалить ${selectedStrategies.length} стратегий?`}
              </h3>

              {isClose && closablePositions.length > 0 && (
                <div className="mb-4 space-y-2 text-xs">
                  <div className="flex justify-between text-slate-400">
                    <span>Нереализованный P&L</span>
                    <span className={totalPnl >= 0 ? 'text-emerald-400' : 'text-rose-400'}>
                      {totalPnl >= 0 ? '+' : ''}{totalPnl.toFixed(4)} USDT
                    </span>
                  </div>
                  <div className="flex justify-between text-slate-400">
                    <span>Комиссия открытия (0.055%)</span>
                    <span className="text-rose-400">−{totalOpenFee.toFixed(4)} USDT</span>
                  </div>
                  <div className="flex justify-between text-slate-400">
                    <span>Комиссия закрытия (0.055%)</span>
                    <span className="text-rose-400">−{totalCloseFee.toFixed(4)} USDT</span>
                  </div>
                  <div className="h-px bg-white/[.08]" />
                  <div className="flex justify-between font-semibold text-white">
                    <span>Итого</span>
                    <span className={netPnl >= 0 ? 'text-emerald-300' : 'text-rose-300'}>
                      {netPnl >= 0 ? '+' : ''}{netPnl.toFixed(4)} USDT
                    </span>
                  </div>
                </div>
              )}

              {isClose && closablePositions.length === 0 && (
                <p className="text-xs text-slate-400 mb-4">Нет открытых позиций среди выбранных стратегий.</p>
              )}

              {!isClose && (
                <p className="text-xs text-slate-400 mb-4">Ордера будут отменены. Позиции на бирже останутся открытыми.</p>
              )}

              <div className="flex gap-2">
                <button onClick={() => setBulkAction(null)} className="flex-1 px-3 py-2 text-xs rounded-lg text-slate-300 hover:text-white transition-colors" style={{ background: 'rgba(255,255,255,.07)', border: '1px solid rgba(255,255,255,.10)' }}>Отмена</button>
                <button
                  disabled={isClose && closablePositions.length === 0}
                  onClick={() => { setBulkAction(null); if (isClose) handleBulkClose(actionIds); else handleBulkDelete(actionIds) }}
                  className={`flex-1 px-3 py-2 text-xs text-white rounded-lg transition-colors font-semibold disabled:opacity-40 ${isClose ? 'bg-amber-700 hover:bg-amber-600' : 'bg-rose-700 hover:bg-rose-600'}`}
                >{isClose ? 'Закрыть позиции' : 'Удалить'}</button>
              </div>
            </div>
          </div>,
          document.body
        )
      })()}
    </div>
  )
}

// ── Admin user picker bar ────────────────────────────────────────────────────

function metricColor(pct: number): string {
  if (pct < 60) return 'text-emerald-400'
  if (pct < 80) return 'text-amber-400'
  return 'text-rose-400'
}

function SystemMetricChip({ label, pct }: { label: string; pct: number }) {
  return (
    <span className="hidden md:flex items-baseline gap-[3px]">
      <span className="text-[9px] font-bold uppercase tracking-[0.8px] text-slate-600">{label}</span>
      <span className={`text-[11px] font-semibold tabular-nums ${metricColor(pct)}`}>
        {Math.round(pct)}%
      </span>
    </span>
  )
}

function AdminUserPickerBar({
  selectedUser,
  selectedAccountId: selectedAccId,
  onSelectUser,
  onSelectAccount,
}: {
  selectedUser: AdminUser | null
  selectedAccountId: string | null
  onSelectUser: (u: AdminUser | null) => void
  onSelectAccount: (id: string) => void
}) {
  const { users, loading } = useAdminUsers()
  const [search, setSearch] = useState('')
  const [open, setOpen] = useState(false)
  const wrapRef = useRef<HTMLDivElement>(null)
  const health = useSystemHealth()

  useEffect(() => {
    if (!open) return
    function onDown(e: MouseEvent) {
      if (wrapRef.current && !wrapRef.current.contains(e.target as Node)) setOpen(false)
    }
    document.addEventListener('mousedown', onDown)
    return () => document.removeEventListener('mousedown', onDown)
  }, [open])

  const filtered = useMemo(() => {
    const q = search.toLowerCase().trim()
    if (!q) return users.slice(0, 15)
    return users
      .filter(u =>
        (u.name ?? '').toLowerCase().includes(q) ||
        u.email.toLowerCase().includes(q) ||
        u.id.toLowerCase().includes(q),
      )
      .slice(0, 15)
  }, [users, search])

  const displayName = selectedUser?.name || selectedUser?.email || ''

  return (
    <div className="flex items-center gap-2 px-3 flex-shrink-0 border-b border-amber-500/15 bg-[#0d0a00]" style={{ height: 44 }}>
      {/* Admin indicator */}
      <div className="flex items-center gap-1.5 text-amber-500/60">
        <Shield size={12} strokeWidth={2} />
        <span className="text-[10px] font-bold uppercase tracking-[1px]">Admin</span>
      </div>

      <div className="h-3 w-px bg-white/[.07]" />

      {/* Selected user badge + account switcher */}
      {selectedUser && (
        <>
          <div className="flex items-center gap-1.5 rounded-md border border-amber-400/25 bg-amber-400/[.10] px-2 py-0.5">
            <span className="max-w-[140px] truncate text-[11px] font-semibold text-amber-200">{displayName}</span>
            <button
              type="button"
              onClick={() => onSelectUser(null)}
              className="text-amber-400/60 hover:text-amber-200 transition-colors"
            >
              <X size={10} />
            </button>
          </div>
          {selectedUser.accounts.length > 1 && (
            <select
              value={selectedAccId ?? ''}
              onChange={e => onSelectAccount(e.target.value)}
              className="h-6 rounded border border-white/[.10] bg-white/[.05] px-1.5 text-[11px] text-slate-300 outline-none focus:border-white/[.20]"
            >
              {selectedUser.accounts.map(a => (
                <option key={a.id} value={a.id}>{a.label || a.exchange}</option>
              ))}
            </select>
          )}
        </>
      )}

      {/* System health chips */}
      <div className="flex flex-1 items-center justify-center gap-3 min-w-0 overflow-hidden px-2">
        {health && (
          <>
            <SystemMetricChip label="CPU"  pct={health.cpu_pct} />
            <SystemMetricChip label="RAM"  pct={health.ram_pct} />
            <SystemMetricChip label="Disk" pct={health.disk_pct} />
            {/* DB chip */}
            <span className="hidden md:flex items-baseline gap-[3px]">
              <span className="text-[9px] font-bold uppercase tracking-[0.8px] text-slate-600">DB</span>
              <span className={`text-[11px] font-semibold ${health.db_ok ? 'text-emerald-400' : 'text-rose-400'}`}>
                {health.db_ok
                  ? `✓${(health.db_size_mb / 1024).toFixed(1)}G`
                  : '✗'}
              </span>
              {health.db_ok && health.db_growth_mb_per_day >= 50 && (
                <span className="text-[10px] text-slate-500">
                  +{Math.round(health.db_growth_mb_per_day)}M/д
                </span>
              )}
            </span>
          </>
        )}
      </div>

      {/* User search picker */}
      <div className="relative" ref={wrapRef}>
        <button
          type="button"
          onClick={() => { setOpen(v => !v); setSearch('') }}
          className={`flex items-center gap-1.5 rounded-lg border px-2.5 py-1 text-[11px] font-medium transition-colors ${
            open
              ? 'border-amber-400/40 bg-amber-400/[.12] text-amber-200'
              : 'border-white/[.10] bg-white/[.04] text-slate-400 hover:text-slate-200 hover:bg-white/[.07]'
          }`}
        >
          <Search size={11} />
          {selectedUser ? 'Сменить пользователя' : 'Выбрать пользователя'}
        </button>

        {open && (
          <div className="absolute right-0 top-full mt-1.5 z-[100] w-[300px] overflow-hidden rounded-[13px] border border-white/[.10] bg-[#0d1220] shadow-[0_16px_40px_-8px_rgba(0,0,0,.9)]">
            <div className="border-b border-white/[.07] p-2">
              <input
                autoFocus
                value={search}
                onChange={e => setSearch(e.target.value)}
                placeholder="Имя, email или ID..."
                className="w-full rounded-lg border border-white/[.08] bg-white/[.04] px-2.5 py-1.5 text-[12px] text-slate-200 placeholder-slate-600 outline-none focus:border-white/[.18]"
              />
            </div>
            <div className="max-h-[260px] overflow-y-auto py-1">
              {loading && (
                <div className="py-4 text-center text-[11px] text-slate-600">Загрузка...</div>
              )}
              {!loading && filtered.length === 0 && (
                <div className="py-4 text-center text-[11px] text-slate-600">Не найдено</div>
              )}
              {filtered.map(u => (
                <button
                  key={u.id}
                  type="button"
                  onClick={() => { onSelectUser(u); setOpen(false); setSearch('') }}
                  className="flex w-full items-center gap-2.5 px-3 py-2 text-left transition-colors hover:bg-white/[.04]"
                >
                  <div className="flex h-6 w-6 shrink-0 items-center justify-center rounded-md bg-[linear-gradient(135deg,#6b8cff,#c14dff)] text-[9px] font-bold text-white">
                    {(u.name || u.email).slice(0, 2).toUpperCase()}
                  </div>
                  <div className="min-w-0 flex-1">
                    <div className="truncate text-[12px] font-semibold text-slate-200">{u.name || u.email}</div>
                    {u.name && <div className="truncate text-[10px] text-slate-500">{u.email}</div>}
                  </div>
                  <div className="text-[10px] text-slate-600 shrink-0">{u.accounts.length} акк.</div>
                </button>
              ))}
            </div>
          </div>
        )}
      </div>
    </div>
  )
}

// ── Main page ────────────────────────────────────────────────────────────────
export function TerminalPage() {
  const { selectedAccountId } = useSelectedAccount()
  const { isAdmin } = useAuth()
  const [impersonatedUser, setImpersonatedUser] = useState<AdminUser | null>(null)
  const [impersonatedAccountId, setImpersonatedAccountId] = useState<string | null>(null)

  // Auto-select first account when impersonated user changes
  useEffect(() => {
    if (impersonatedUser) {
      setImpersonatedAccountId(impersonatedUser.accounts[0]?.id ?? null)
    } else {
      setImpersonatedAccountId(null)
    }
  }, [impersonatedUser])

  const accountId = (impersonatedAccountId ?? selectedAccountId) || null
  const [searchParams, setSearchParams] = useSearchParams()

  // Bots state lifted here so it can feed both the bots tab and the hedge overlay.
  const { mine: myBots, loading: botsLoading, action: botAction } = useBots()

  // Strategies state lifted here so it can feed the hedge overlay regardless of which tab is active.
  // eslint-disable-next-line @typescript-eslint/no-unused-vars
  const [_strategies, setStrategies] = useState<Strategy[]>([])
  useEffect(() => {
    let cancelled = false
    const fetchStrategies = () =>
      listStrategies().then(s => { if (!cancelled) setStrategies(s) }).catch(() => {})
    fetchStrategies()
    const t = setInterval(fetchStrategies, 15_000)
    return () => { cancelled = true; clearInterval(t) }
  }, [])

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
  const [hedgePairTarget, setHedgePairTarget] = useState<number | null>(null)
  // Clear pair target when switching symbols
  useEffect(() => { setHedgePairTarget(null) }, [symbol])
  const [strategyCycleNums, setStrategyCycleNums] = useState<Record<string, number>>({})
  const [strategyLevels, setStrategyLevels] = useState<StrategyLevel[]>([])
  const [strategySafeZone, setStrategySafeZone] = useState<{ low: number; high: number } | null>(null)

  useEffect(() => {
    if (!selectedStrategy?.id) { setStrategyLevels([]); setStrategySafeZone(null); return }
    const id = selectedStrategy.id
    const fetch = () => getStrategyState(id).then(s => {
      setStrategyLevels(s.levels ?? [])
      setStrategySafeZone(s.safe_zone ?? null)
      if (s.cycle_num) setStrategyCycleNums(prev => ({ ...prev, [id]: s.cycle_num }))
    }).catch(() => {})
    fetch()
    const t = setInterval(fetch, 3000)
    return () => clearInterval(t)
  }, [selectedStrategy?.id])
  const stratMatchesSymbol = selectedStrategy?.symbol === symbol
  const currentCycleNum = stratMatchesSymbol ? (strategyCycleNums[selectedStrategy!.id] ?? null) : null
  const stratIdShort = stratMatchesSymbol ? selectedStrategy!.id.slice(0, 8) : null

  const [rowSplit, setRowSplit] = useState(() => parseFloat(localStorage.getItem('t_row') ?? '65'))
  const containerRef = useRef<HTMLDivElement>(null)
  const rightTabRef = useRef(rightTab)
  useEffect(() => { rightTabRef.current = rightTab }, [rightTab])
  // Ref to the tab-content wrapper — scrollWidth reflects actual content width dynamically
  const rightPanelContentRef = useRef<HTMLDivElement>(null)

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
        // Dynamically compute the minimum right-panel width from actual rendered content.
        // scrollWidth of the tab-content wrapper reflects the true content width even
        // when the panel is already squeezed (overflow-y:auto forces overflow-x:auto).
        const isContentTab = rightTabRef.current === 'bots' || rightTabRef.current === 'strategies'
        const contentScrollW = rightPanelContentRef.current?.scrollWidth ?? 0
        const minRightPx = isContentTab ? Math.max(360, contentScrollW + 4) : 0
        const maxPct = minRightPx > 0
          ? Math.min(82, ((rect.width - minRightPx) / rect.width) * 100)
          : 82
        const pct = Math.max(30, Math.min(maxPct, ((ev.clientX - rect.left) / rect.width) * 100))
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

  const { positions, orders, executions, log, status, accountName, loading, reconnect, removeOrder, freeMargin } = usePositionsWs(accountId)

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
    // When a strategy is selected, only include its own historical fills to avoid
    // cross-strategy pollution (multiple strategies on the same symbol all showing at once).
    // When no strategy is selected (e.g. Bots tab), skip historical entirely — real-time
    // WS executions still appear as they arrive.
    const filteredHistorical = stratIdShort
      ? historicalExecs.filter(e => !wsIds.has(e.execId) && e.orderLinkId?.includes(stratIdShort))
      : []
    return [...filteredHistorical, ...executions]
  }, [historicalExecs, executions, stratIdShort])

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
    { key: 'debug', label: 'Дебаг' },
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
    <div style={{ display: 'flex', flexDirection: 'column', height: '100%' }}>
      {isAdmin && (
        <AdminUserPickerBar
          selectedUser={impersonatedUser}
          selectedAccountId={impersonatedAccountId}
          onSelectUser={setImpersonatedUser}
          onSelectAccount={setImpersonatedAccountId}
        />
      )}
      <div
        ref={containerRef}
        className="bg-gray-100 dark:bg-[#07070f]"
        style={{ display: 'flex', flex: 1, minHeight: 0, padding: 10 }}
      >
      {/* ── Mobile layout ───────────────────────────────────────── */}
      <div className="flex md:hidden flex-col w-full h-full gap-2">
        {/* Chart */}
        <div className="bg-white dark:bg-gray-900/50 border border-gray-200 dark:border-gray-700/50 rounded-xl flex flex-col overflow-hidden" style={{ height: '42vh' }}>
          {chartToolbar}
          <div className="flex-1 min-h-0 relative">
            <Chart candles={candles} candleSymbol={candleSymbol} positions={positions} orders={orders} executions={allExecutions} symbol={symbol} lastPrice={lastPrice} onLoadMore={loadMore} overlaySettings={chartSettings} strategyDir={stratMatchesSymbol ? selectedStrategy?.direction as 'long' | 'short' | null ?? null : null} stratIdShort={stratIdShort} currentCycleNum={currentCycleNum} strategyLevels={stratMatchesSymbol ? strategyLevels : []} tickerPrices={tickerPrices} safeZone={stratMatchesSymbol ? strategySafeZone : null} hedgePairTarget={hedgePairTarget} />
            <HedgeBotOverlay symbol={symbol} positions={positions} bots={myBots} accountId={accountId} tickerPrices={tickerPrices} strategies={_strategies} />
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
            {mobileTab === 'orders' && <OrdersTable accountId={accountId ?? ''} orders={orders} loading={loading} onSelect={setSymbol} onRemoveOrder={removeOrder} strategyLevels={strategyLevels} />}
            {mobileTab === 'strategies' && <TerminalStrategiesTab onSymbolChange={setSymbol} orders={orders} positions={positions} tickerPrices={tickerPrices} accountId={accountId} asAccountId={impersonatedAccountId ?? undefined} onStrategySelect={setSelectedStrategy} onCycleNumUpdate={(id, num) => setStrategyCycleNums(prev => ({ ...prev, [id]: num }))} onStrategiesChange={setStrategies} onPairTargetUpdate={setHedgePairTarget} freeMargin={freeMargin} hedgeBots={myBots} />}
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
          <div className="flex-1 min-h-0 relative">
            <Chart candles={candles} candleSymbol={candleSymbol} positions={positions} orders={orders} executions={allExecutions} symbol={symbol} lastPrice={lastPrice} onLoadMore={loadMore} overlaySettings={chartSettings} strategyDir={stratMatchesSymbol ? selectedStrategy?.direction as 'long' | 'short' | null ?? null : null} stratIdShort={stratIdShort} currentCycleNum={currentCycleNum} strategyLevels={stratMatchesSymbol ? strategyLevels : []} tickerPrices={tickerPrices} safeZone={stratMatchesSymbol ? strategySafeZone : null} hedgePairTarget={hedgePairTarget} />
            <HedgeBotOverlay symbol={symbol} positions={positions} bots={myBots} accountId={accountId} tickerPrices={tickerPrices} strategies={_strategies} />
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
            {bottomTab === 'orders' && <OrdersTable accountId={accountId ?? ''} orders={orders} loading={loading} onSelect={setSymbol} onRemoveOrder={removeOrder} strategyLevels={strategyLevels} />}
            {bottomTab === 'history' && <HistoryTable accountId={accountId ?? undefined} symbol={symbol} />}
            {bottomTab === 'executions' && <ExecutionsTable accountId={accountId ?? undefined} />}
            {bottomTab === 'log' && <TradeLog log={log} />}
            {bottomTab === 'pnl' && <PnlTable accountId={accountId ?? undefined} />}
            {bottomTab === 'debug' && <DebugLogTab />}
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
        <div ref={rightPanelContentRef} className="flex-1 overflow-y-auto flex flex-col">
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
          {rightTab === 'strategies' && <TerminalStrategiesTab onSymbolChange={setSymbol} orders={orders} positions={positions} tickerPrices={tickerPrices} accountId={accountId} asAccountId={impersonatedAccountId ?? undefined} onStrategySelect={setSelectedStrategy} onCycleNumUpdate={(id, num) => setStrategyCycleNums(prev => ({ ...prev, [id]: num }))} onStrategiesChange={setStrategies} onPairTargetUpdate={setHedgePairTarget} freeMargin={freeMargin} hedgeBots={myBots} />}
          {rightTab === 'bots' && <TerminalBotsTab onSymbolChange={setSymbol} mine={myBots} loading={botsLoading} action={botAction} />}
        </div>

      </div>{/* /Right panel */}

    </div>{/* /Desktop layout */}
      </div>
    </div>
  )
}
