import { useState, useEffect, useRef, useMemo } from 'react'
import { Shield } from 'lucide-react'
import {
  getStrategyState, getStrategyEvents,
  setStrategyStatus, detachWithAction, getHedgeSession, deleteStrategy,
  type DetachPositionData,
} from '../../api/strategies'
import { placeOrder } from '../../api/trader'
import { ClosePositionModal, makeCloseConfirm, type CloseConfirm } from '../common/ClosePositionModal'
import { CoinIcon } from '../common/CoinIcon'
import type { Strategy, ExchangeAccount, ActiveOrder, Position, StrategyState, StrategyEvent, HedgeSession } from '../../types'
import type { Bot } from '../../features/bots/types'

export interface HedgePairCardProps {
  main: Strategy
  hedge: Strategy
  accounts: ExchangeAccount[]
  orders: ActiveOrder[]
  positions?: Position[]
  tickerPrices?: Map<string, number>
  selectedStrategyId?: string | null
  hedgeBot?: Bot | null
  onEdit: (s: Strategy, filledCount: number) => void
  onChanged: () => void
  onSelect?: (s: Strategy) => void
  onPairTargetUpdate?: (target: number | null) => void
}

// ── tiny icons ─────────────────────────────────────────────────────────────────
const IcUp = ({ s = 11, w = 2.6 }: { s?: number; w?: number }) => (
  <svg width={s} height={s} viewBox="0 0 24 24" fill="none" stroke="currentColor"
    strokeWidth={w} strokeLinecap="round" strokeLinejoin="round" style={{ display: 'block' }}>
    <path d="M7 14l5-5 5 5"/>
  </svg>
)
const IcDown = ({ s = 11, w = 2.6 }: { s?: number; w?: number }) => (
  <svg width={s} height={s} viewBox="0 0 24 24" fill="none" stroke="currentColor"
    strokeWidth={w} strokeLinecap="round" strokeLinejoin="round" style={{ display: 'block' }}>
    <path d="M7 10l5 5 5-5"/>
  </svg>
)
const IcGear = () => (
  <svg width={13} height={13} viewBox="0 0 24 24" fill="none" stroke="currentColor"
    strokeWidth={1.8} strokeLinecap="round" strokeLinejoin="round" style={{ display: 'block' }}>
    <circle cx="12" cy="12" r="3"/>
    <path d="M19.4 15a1.7 1.7 0 0 0 .3 1.8l.1.1a2 2 0 1 1-2.8 2.8l-.1-.1a1.7 1.7 0 0 0-1.8-.3 1.7 1.7 0 0 0-1 1.5V21a2 2 0 1 1-4 0v-.1A1.7 1.7 0 0 0 9 19.4a1.7 1.7 0 0 0-1.8.3l-.1.1a2 2 0 1 1-2.8-2.8l.1-.1a1.7 1.7 0 0 0 .3-1.8 1.7 1.7 0 0 0-1.5-1H3a2 2 0 1 1 0-4h.1A1.7 1.7 0 0 0 4.6 9a1.7 1.7 0 0 0-.3-1.8l-.1-.1a2 2 0 1 1 2.8-2.8l.1.1a1.7 1.7 0 0 0 1.8.3H9a1.7 1.7 0 0 0 1-1.5V3a2 2 0 1 1 4 0v.1a1.7 1.7 0 0 0 1 1.5 1.7 1.7 0 0 0 1.8-.3l.1-.1a2 2 0 1 1 2.8 2.8l-.1.1a1.7 1.7 0 0 0-.3 1.8V9a1.7 1.7 0 0 0 1.5 1H21a2 2 0 1 1 0 4h-.1a1.7 1.7 0 0 0-1.5 1z"/>
  </svg>
)
const IcMenu = () => (
  <svg width={14} height={14} viewBox="0 0 24 24" fill="none" stroke="currentColor"
    strokeWidth={1.7} strokeLinecap="round" style={{ display: 'block' }}>
    <path d="M3 6h18M3 12h18M3 18h18"/>
  </svg>
)
const IcChevron = ({ up }: { up: boolean }) => (
  <svg width={11} height={11} viewBox="0 0 24 24" fill="none" stroke="currentColor"
    strokeWidth={2.2} strokeLinecap="round" strokeLinejoin="round" style={{ display: 'block' }}>
    {up ? <path d="M18 15l-6-6-6 6"/> : <path d="M6 9l6 6 6-6"/>}
  </svg>
)

// ── helpers ───────────────────────────────────────────────────────────────────

function findPosition(s: Strategy, positions: Position[] | undefined) {
  if (!positions) return null
  const wantSide = s.direction === 'long' ? 'Buy' : 'Sell'
  const wantIdx = s.hedge_mode ? (s.direction === 'long' ? 1 : 2) : 0
  return positions.find(p =>
    p.symbol === s.symbol &&
    p.side === wantSide &&
    parseFloat(p.size) > 0 &&
    (!s.hedge_mode || p.positionIdx === wantIdx),
  ) ?? null
}

function computePnl(pos: Position | null, tickerPrices?: Map<string, number>): number | null {
  if (!pos) return null
  const entry = parseFloat(pos.entryPrice)
  const size  = parseFloat(pos.size)
  const livePrice = tickerPrices?.get(pos.symbol)
  const mark = livePrice ?? parseFloat(pos.markPrice)
  return livePrice != null
    ? (pos.side === 'Buy' ? (mark - entry) : (entry - mark)) * size
    : parseFloat(pos.unrealisedPnl)
}

function fmtPnl(v: number | null): string {
  if (v === null) return '—'
  const sign = v > 0 ? '+' : ''
  return `${sign}${v.toFixed(2)}$`
}

function pnlClass(v: number | null): string {
  if (v === null) return 'text-slate-500'
  if (v > 0) return 'text-emerald-300'
  if (v < 0) return 'text-rose-300'
  return 'text-slate-500'
}

function fmtPrice(v: number | null, d = 4): string {
  if (v === null || v === 0) return '—'
  return v.toFixed(d)
}

function fmtTime(iso: string): string {
  const d = new Date(iso)
  return d.toLocaleTimeString('ru-RU', { hour: '2-digit', minute: '2-digit', second: '2-digit' })
}

function fmtDateTime(iso: string): string {
  const d = new Date(iso)
  return d.toLocaleString('ru-RU', { day: '2-digit', month: '2-digit', hour: '2-digit', minute: '2-digit' })
}

function autoDec(price: number): number {
  if (price >= 10000) return 0
  if (price >= 1000)  return 1
  if (price >= 100)   return 2
  if (price >= 10)    return 3
  return 4
}

// ── StatRow ───────────────────────────────────────────────────────────────────

function StatRow({ label, value, color }: { label: string; value: string; color?: string }) {
  return (
    <div className="flex items-baseline justify-between gap-2">
      <span className="text-[11px] text-slate-500 truncate">{label}</span>
      <span className="text-[12px] font-semibold tabular-nums whitespace-nowrap"
        style={{ color: color ?? '#94a3b8' }}>
        {value}
      </span>
    </div>
  )
}

// ── StrategyRow ───────────────────────────────────────────────────────────────

function StrategyRow({
  strategy, isMain, positions, tickerPrices, selectedStrategyId, onEdit, onChanged, onSelect,
}: {
  strategy: Strategy
  isMain: boolean
  positions?: Position[]
  tickerPrices?: Map<string, number>
  selectedStrategyId?: string | null
  onEdit: (s: Strategy, filledCount: number) => void
  onChanged: () => void
  onSelect?: (s: Strategy) => void
}) {
  const [acting, setActing] = useState(false)

  const pos = findPosition(strategy, positions)
  const pnl = computePnl(pos, tickerPrices)
  const volUsdt    = pos ? pos.sizeUsdt : 0
  const isStopped  = strategy.status === 'stopped'
  const gridType   = strategy.strategy_type === 'matrix' ? 'Matrix' : 'Grid'
  const stoppedNoPos = isStopped && !pos
  const isLong     = strategy.direction === 'long'
  const isSelected = selectedStrategyId === strategy.id

  const handleStop = async () => {
    setActing(true)
    try { await setStrategyStatus(strategy.id, 'stopped'); onChanged() } catch { /* ignore */ }
    setActing(false)
  }
  const handleStart = async () => {
    setActing(true)
    try { await setStrategyStatus(strategy.id, 'active'); onChanged() } catch { /* ignore */ }
    setActing(false)
  }

  return (
    <div
      className="flex items-center gap-2 px-3 py-2 rounded-lg transition-colors"
      style={isSelected ? { background: 'rgba(255,255,255,.07)', boxShadow: 'inset 2px 0 0 rgba(255,255,255,.25)' } : undefined}
    >
      {/* Clickable left: open chart */}
      <button
        type="button"
        onClick={() => onSelect?.(strategy)}
        className="flex items-center gap-2 flex-1 min-w-0 text-left cursor-pointer hover:opacity-80 transition-opacity"
      >
        <span
          className="shrink-0 inline-flex items-center justify-center w-[18px] h-[18px] rounded-[4px]"
          style={isLong
            ? { background: 'rgba(65,210,139,.18)', color: '#5be0a0' }
            : { background: 'rgba(248,113,113,.18)', color: '#fca5a5' }}
        >
          {isLong ? <IcUp /> : <IcDown />}
        </span>

        <span
          className="text-[11px] font-bold uppercase tracking-[.6px] shrink-0 w-[36px]"
          style={{ color: stoppedNoPos ? '#475569' : isMain ? '#60a5fa' : '#f59e0b' }}
        >
          {isMain ? 'Main' : 'Hedge'}
        </span>

        <div className="flex items-baseline gap-1 shrink-0">
          <span className={`text-[11px] uppercase tracking-[.8px] font-semibold ${stoppedNoPos ? 'text-slate-600' : 'text-slate-400'}`}>{gridType}</span>
          <span className={`text-[13px] font-semibold ${stoppedNoPos ? 'text-slate-600' : strategy.active_levels > 0 ? 'text-slate-200' : 'text-slate-500'}`}>
            {strategy.active_levels}/{strategy.grid_levels}
          </span>
        </div>

        <div className="w-px h-3 bg-white/[.07] shrink-0" />

        <div className="flex items-baseline gap-1 shrink-0">
          <span className={`text-[11px] uppercase tracking-[.8px] font-semibold ${stoppedNoPos ? 'text-slate-600' : 'text-slate-400'}`}>Объём</span>
          <span className={`text-[13px] font-semibold whitespace-nowrap ${stoppedNoPos ? 'text-slate-600' : volUsdt > 0 ? 'text-slate-200' : 'text-slate-500'}`}>
            {volUsdt > 0 ? `${volUsdt.toFixed(1)}$` : '0$'}
          </span>
        </div>

        <div className="w-px h-3 bg-white/[.07] shrink-0" />

        <div className="flex items-baseline gap-1 shrink-0">
          <span className={`text-[11px] uppercase tracking-[.8px] font-semibold ${stoppedNoPos ? 'text-slate-600' : 'text-slate-400'}`}>P&L</span>
          <span className={`text-[13px] font-semibold whitespace-nowrap ${stoppedNoPos ? 'text-slate-600' : pnlClass(pnl)}`}>
            {fmtPnl(pnl)}
          </span>
        </div>
      </button>

      {/* Configure */}
      <button
        type="button" title="Настроить"
        onClick={() => onEdit(strategy, strategy.active_levels)}
        className="shrink-0 w-6 h-6 inline-flex items-center justify-center rounded-[5px] transition-colors hover:bg-white/[.08] text-slate-400 bg-white/[.04] border border-white/[.08]"
      >
        <IcGear />
      </button>

      {/* Stop / Start */}
      {!isStopped ? (
        <button
          type="button" title="Остановить" disabled={acting} onClick={handleStop}
          className="shrink-0 w-6 h-6 inline-flex items-center justify-center rounded-[5px] transition-colors hover:bg-red-500/10 text-rose-400/60 bg-white/[.03] border border-white/[.06] disabled:opacity-40"
        >
          {acting ? <span className="text-[9px]">…</span> : (
            <svg width={8} height={8} viewBox="0 0 24 24" fill="currentColor" style={{ display: 'block' }}>
              <rect x="4" y="4" width="16" height="16" rx="2"/>
            </svg>
          )}
        </button>
      ) : (
        <button
          type="button" title="Запустить" disabled={acting} onClick={handleStart}
          className="shrink-0 w-6 h-6 inline-flex items-center justify-center rounded-[5px] transition-colors hover:bg-emerald-500/10 text-emerald-400/60 bg-white/[.03] border border-white/[.06] disabled:opacity-40"
        >
          {acting ? <span className="text-[9px]">…</span> : (
            <svg width={8} height={8} viewBox="0 0 24 24" fill="currentColor" style={{ display: 'block' }}>
              <polygon points="5,3 19,12 5,21"/>
            </svg>
          )}
        </button>
      )}
    </div>
  )
}

// ── main component ────────────────────────────────────────────────────────────

export function HedgePairCard({
  main, hedge, positions, tickerPrices, selectedStrategyId,
  hedgeBot, onEdit, onChanged, onSelect, onPairTargetUpdate,
}: HedgePairCardProps) {
  const mainPos  = findPosition(main,  positions)
  const hedgePos = findPosition(hedge, positions)
  const mainPnl  = computePnl(mainPos,  tickerPrices)
  const hedgePnl = computePnl(hedgePos, tickerPrices)
  const netPnl   = mainPnl !== null || hedgePnl !== null
    ? (mainPnl ?? 0) + (hedgePnl ?? 0)
    : null

  const symbol  = main.symbol
  const botName = hedge.bot_name ?? main.bot_name ?? null
  const botId   = hedge.bot_id ?? null
  const isAnyActive = main.status === 'active' || hedge.status === 'active'

  // ── menu ──────────────────────────────────────────────────────────────────
  const [menuOpen, setMenuOpen] = useState(false)
  const menuRef = useRef<HTMLDivElement>(null)

  useEffect(() => {
    if (!menuOpen) return
    const h = (e: MouseEvent) => {
      if (menuRef.current && !menuRef.current.contains(e.target as Node)) setMenuOpen(false)
    }
    document.addEventListener('mousedown', h)
    return () => document.removeEventListener('mousedown', h)
  }, [menuOpen])

  // ── detach flow ───────────────────────────────────────────────────────────
  const [step, setStep]         = useState<'idle' | 'detach-dialog'>('idle')
  const [acting, setActing]     = useState(false)
  const [detachBlacklist, setDetachBlacklist] = useState(false)

  const handleDetachAction = async (action: 'adopt' | 'close' | 'leave') => {
    setActing(true)
    try {
      const pos: DetachPositionData | undefined = hedgePos
        ? {
            size:         hedgePos.size,
            side:         hedgePos.side,
            entry_price:  hedgePos.entryPrice,
            position_idx: hedgePos.positionIdx,
          }
        : undefined

      try {
        await detachWithAction(hedge.id, action, {
          addBlacklist: detachBlacklist,
          position: pos,
        })
      } catch { /* backend returns ok:true even on partial errors */ }

      // For "close": stop main strategy too (full pair dissolution)
      if (action === 'close') {
        try { await setStrategyStatus(main.id, 'stopped') } catch {}
      }

      if (detachBlacklist && botId) {
        window.dispatchEvent(new CustomEvent('bot-updated'))
      }

      setStep('idle')
      setDetachBlacklist(false)
      onChanged()
    } finally {
      setActing(false)
    }
  }

  // ── delete pair ──────────────────────────────────────────────────────────
  const [deleteConfirm, setDeleteConfirm] = useState(false)
  const [deleting, setDeleting]           = useState(false)

  async function handleDeletePair() {
    if (!deleteConfirm) { setDeleteConfirm(true); return }
    setDeleteConfirm(false)
    setDeleting(true)
    try {
      await Promise.all([deleteStrategy(main.id), deleteStrategy(hedge.id)])
      onChanged()
    } catch {
      setDeleting(false)
    }
  }

  // ── close position ────────────────────────────────────────────────────────
  const [closeConfirm, setCloseConfirm] = useState<CloseConfirm | null>(null)
  const [closing, setClosing]           = useState(false)

  const handleConfirmClose = async () => {
    if (!closeConfirm) return
    setClosing(true)
    try {
      await placeOrder({
        account_id:   closeConfirm.accountId,
        symbol:       closeConfirm.pos.symbol,
        category:     closeConfirm.pos.category,
        side:         closeConfirm.pos.side === 'Buy' ? 'Sell' : 'Buy',
        order_type:   'Market',
        qty:          closeConfirm.pos.size,
        reduce_only:  true,
        position_idx: closeConfirm.pos.positionIdx,
      })
      onChanged()
    } catch { /* backend closes cycle via WS position event */ }
    setClosing(false)
    setCloseConfirm(null)
  }

  // ── expand ────────────────────────────────────────────────────────────────
  const [expanded, setExpanded]       = useState(false)
  const [expandTab, setExpandTab]     = useState<'stats' | 'log'>('stats')
  const [mainState, setMainState]     = useState<StrategyState | null>(null)
  const [hedgeState, setHedgeState]   = useState<StrategyState | null>(null)
  const [mainEvents, setMainEvents]   = useState<StrategyEvent[]>([])
  const [hedgeEvents, setHedgeEvents] = useState<StrategyEvent[]>([])
  const [dataLoading, setDataLoading] = useState(false)
  const [hedgeSession, setHedgeSession] = useState<HedgeSession | null>(null)

  useEffect(() => {
    if (!expanded) return
    setDataLoading(true)
    Promise.all([
      getStrategyState(main.id).catch(() => null),
      getStrategyState(hedge.id).catch(() => null),
      getStrategyEvents(main.id,  { limit: 60 }).catch(() => ({ total: 0, events: [] as StrategyEvent[] })),
      getStrategyEvents(hedge.id, { limit: 60 }).catch(() => ({ total: 0, events: [] as StrategyEvent[] })),
      getHedgeSession(hedge.id).catch(() => null),
    ]).then(([ms, hs, me, he, session]) => {
      setMainState(ms)
      setHedgeState(hs)
      setMainEvents(me.events)
      setHedgeEvents(he.events)
      setHedgeSession(session)
    }).finally(() => setDataLoading(false))
  }, [expanded, main.id, hedge.id])

  // ── stats computation ─────────────────────────────────────────────────────
  const mainEntry  = (mainState?.avg_entry  ?? 0) > 0 ? (mainState?.avg_entry  ?? null) : null
  const hedgeEntry = (hedgeState?.avg_entry ?? 0) > 0 ? (hedgeState?.avg_entry ?? null) : null
  const currentGap = mainEntry !== null && hedgeEntry !== null ? Math.abs(mainEntry - hedgeEntry) : null

  const gapReduced = hedgeSession?.gap_at_start != null && currentGap !== null
    ? hedgeSession.gap_at_start - currentGap
    : null

  // ── 30s polling: refresh states + session ────────────────────────────────
  useEffect(() => {
    if (!expanded) return
    const id = setInterval(() => {
      Promise.all([
        getStrategyState(main.id).catch(() => null),
        getStrategyState(hedge.id).catch(() => null),
        getHedgeSession(hedge.id).catch(() => null),
      ]).then(([ms, hs, session]) => {
        if (ms) setMainState(ms)
        if (hs) setHedgeState(hs)
        if (session) setHedgeSession(session)
      }).catch(() => { /* ignore transient poll errors */ })
    }, 30_000)
    return () => clearInterval(id)
  }, [expanded, main.id, hedge.id])

  // ── Paired close target ───────────────────────────────────────────────────
  // Uses live position data (real-time WebSocket) + hedge bot config (close condition).
  //
  // Combined PnL formula (for any direction):
  //   pnl(P) = dm×(P − Em)×Sm + dh×(P − Eh)×Sh
  //           = P×(dm×Sm + dh×Sh) − (dm×Em×Sm + dh×Eh×Sh)
  //
  // Setting pnl(P) = threshold:
  //   P = (threshold + dm×Em×Sm + dh×Eh×Sh) / (dm×Sm + dh×Sh)
  //
  // Breakeven with fees (open already paid + future close fee):
  //   P = (Em×Sm×(dm+r) + Eh×Sh×(dh+r)) / (dm×Sm + dh×Sh − r×(Sm+Sh))
  //   where r = taker fee rate (Bybit default 0.055%)
  const pairedCloseTarget = useMemo(() => {
    if (!mainPos || !hedgePos) return null
    const Em = parseFloat(mainPos.entryPrice)
    const Eh = parseFloat(hedgePos.entryPrice)
    const Sm = parseFloat(mainPos.size)
    const Sh = parseFloat(hedgePos.size)
    if (!Em || !Eh || !Sm || !Sh) return null

    const dm = main.direction === 'long' ? 1 : -1
    const dh = hedge.direction === 'long' ? 1 : -1

    const cfg = hedgeBot?.strategyConfig
    const closeType  = cfg?.hedge_deact_close_type  ?? 0  // 0=pnl$, 1=roi%, 2=breakeven
    const closeValue = cfg?.hedge_deact_close_value ?? 0

    if (closeType === 2) {
      // Breakeven: account for open (already paid) + future close fees
      const r = 0.00055  // Bybit taker rate ~0.055%
      const denom = dm * Sm + dh * Sh - r * (Sm + Sh)
      if (Math.abs(denom) < 1e-9) return null
      return (Em * Sm * (dm + r) + Eh * Sh * (dh + r)) / denom
    }

    let threshold: number
    if (closeType === 1) {
      // roi%: threshold = % of total notional
      threshold = (Em * Sm + Eh * Sh) * closeValue / 100
    } else {
      // pnl$: direct $ target
      threshold = closeValue
    }

    const denom = dm * Sm + dh * Sh
    if (Math.abs(denom) < 1e-9) return null  // perfectly hedged (equal size) — PnL doesn't change with price
    return (threshold + dm * Em * Sm + dh * Eh * Sh) / denom
  }, [mainPos, hedgePos, main.direction, hedge.direction, hedgeBot])

  const currentPrice = tickerPrices?.get(symbol) ?? null
  const distanceToClose = pairedCloseTarget !== null && currentPrice !== null
    ? main.direction === 'long'
      ? pairedCloseTarget - currentPrice
      : currentPrice - pairedCloseTarget
    : null

  const dec = autoDec(currentPrice ?? mainEntry ?? 1000)

  // Notify parent of paired close target whenever this pair is active/selected
  useEffect(() => {
    if (selectedStrategyId === main.id || selectedStrategyId === hedge.id) {
      onPairTargetUpdate?.(pairedCloseTarget)
    }
  }, [selectedStrategyId, pairedCloseTarget, main.id, hedge.id, onPairTargetUpdate])

  // ── merged log ────────────────────────────────────────────────────────────
  const mergedLog = useMemo(() => {
    const tagged = [
      ...mainEvents.map(e  => ({ ...e, tag: 'Main'  as const })),
      ...hedgeEvents.map(e => ({ ...e, tag: 'Hedge' as const })),
    ]
    return tagged.sort((a, b) => new Date(b.created_at).getTime() - new Date(a.created_at).getTime())
  }, [mainEvents, hedgeEvents])

  // ── render ────────────────────────────────────────────────────────────────
  return (
    <div
      className="rounded-xl font-sans transition-all"
      style={{
        background: isAnyActive ? 'rgba(255,255,255,.06)' : 'rgba(255,255,255,.02)',
        border: `1px solid ${isAnyActive ? 'rgba(255,255,255,.11)' : 'rgba(255,255,255,.06)'}`,
      }}
    >
      {/* ── Header ── */}
      <div className="flex items-center gap-2 px-3 py-2.5">
        <CoinIcon symbol={symbol} className="w-5 h-5 shrink-0" />
        <span className="font-display font-bold text-[15px] tracking-[-0.2px] leading-none text-[#f2f5fb]">
          {symbol}
        </span>

        {botName && (
          <>
            <div className="w-px h-3 bg-white/[.10] shrink-0 ml-1" />
            <span className="inline-flex items-center gap-1.5 text-[13px] font-bold uppercase tracking-[.5px] text-amber-400/80 truncate max-w-[140px]">
              <Shield size={17} className="shrink-0" style={{ color: '#f59e0b' }} strokeWidth={2} />
              {botName}
            </span>
          </>
        )}

        {/* P&L + menu */}
        <div className="ml-auto flex items-center gap-1.5 shrink-0">
          <span className="text-[11px] uppercase tracking-[.8px] font-semibold text-slate-400">P&L</span>
          <span className={`text-[13px] font-semibold tabular-nums whitespace-nowrap ${pnlClass(netPnl)}`}>
            {fmtPnl(netPnl)}
          </span>

          {/* Hamburger menu */}
          <div className="relative ml-1" ref={menuRef}>
            <button
              type="button"
              onClick={() => setMenuOpen(v => !v)}
              className="w-7 h-7 inline-flex items-center justify-center rounded-[7px] transition-colors hover:bg-white/[.08] text-slate-400 bg-white/[.04] border border-white/[.08]"
              title="Меню"
            >
              <IcMenu />
            </button>

            {menuOpen && (
              <div
                className="absolute right-0 top-8 z-50 min-w-[170px] rounded-lg py-1 shadow-xl"
                style={{ background: 'rgba(16,20,36,.98)', border: '1px solid rgba(255,255,255,.13)' }}
              >
                {botId && (
                  <button
                    type="button"
                    onClick={() => { setMenuOpen(false); setDetachBlacklist(false); setStep('detach-dialog') }}
                    className="w-full text-left px-3 py-2 text-[12px] text-slate-300 hover:bg-white/[.06] transition-colors flex items-center gap-2"
                  >
                    <svg width={11} height={11} viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth={2} strokeLinecap="round" style={{ display: 'block' }}>
                      <path d="M18 6 6 18M6 6l12 12"/>
                    </svg>
                    Открепить от бота
                  </button>
                )}
                <button
                  type="button"
                  onClick={() => { setMenuOpen(false); setExpanded(v => !v) }}
                  className="w-full text-left px-3 py-2 text-[12px] text-slate-300 hover:bg-white/[.06] transition-colors flex items-center gap-2"
                >
                  <IcChevron up={expanded} />
                  {expanded ? 'Свернуть' : 'Развернуть'}
                </button>
              </div>
            )}
          </div>
        </div>
      </div>

      {/* ── Detach dialog ── */}
      {step === 'detach-dialog' && (
        <div
          className="mx-3 mb-2 px-3 py-3 rounded-lg flex flex-col gap-2.5"
          style={{ background: 'rgba(30,41,59,.95)', border: '1px solid rgba(255,255,255,.10)' }}
        >
          {hedgePos && (
            <div className="flex items-center gap-3 pb-1 border-b border-white/[.07]">
              <span className="text-[11px] text-slate-400 uppercase tracking-[.6px]">Хедж</span>
              <span className="text-[12px] text-slate-200 font-semibold ml-auto">
                {hedgePos.size} {symbol.replace('USDT', '')}
              </span>
              <span className="text-[11px] text-slate-400">
                @ {parseFloat(hedgePos.entryPrice).toFixed(4)}
              </span>
              {hedgePnl !== null && (
                <span className={`text-[11px] font-semibold ${hedgePnl >= 0 ? 'text-emerald-400' : 'text-rose-400'}`}>
                  {hedgePnl >= 0 ? '+' : ''}{hedgePnl.toFixed(2)}$
                </span>
              )}
            </div>
          )}
          {botId && (
            <label className="flex items-center gap-2 cursor-pointer select-none">
              <input
                type="checkbox"
                checked={detachBlacklist}
                onChange={e => setDetachBlacklist(e.target.checked)}
                className="w-3.5 h-3.5 rounded accent-amber-400"
              />
              <span className="text-[11px] text-slate-400">
                Блеклист <span className="text-amber-300/80 font-semibold">{symbol}</span>
                {botName ? ` у бота «${botName}»` : ''}
              </span>
            </label>
          )}
          <div className="grid grid-cols-2 gap-1.5">
            <button
              type="button" disabled={acting}
              onClick={() => handleDetachAction('adopt')}
              className="px-2 py-1.5 rounded text-[11px] font-semibold disabled:opacity-40 text-emerald-300 border border-emerald-500/30 bg-emerald-500/[.08] hover:bg-emerald-500/[.15] transition-colors"
            >
              {acting ? '…' : '🔄 Поглотить'}
            </button>
            <button
              type="button" disabled={acting}
              onClick={() => handleDetachAction('close')}
              className="px-2 py-1.5 rounded text-[11px] font-semibold disabled:opacity-40 text-rose-300 border border-rose-500/30 bg-rose-500/[.08] hover:bg-rose-500/[.15] transition-colors"
            >
              {acting ? '…' : '✖ Закрыть'}
            </button>
            <button
              type="button" disabled={acting}
              onClick={() => handleDetachAction('leave')}
              className="px-2 py-1.5 rounded text-[11px] font-semibold disabled:opacity-40 text-slate-300 border border-white/[.10] bg-white/[.04] hover:bg-white/[.08] transition-colors"
            >
              {acting ? '…' : '📌 Оставить'}
            </button>
            <button
              type="button" disabled={acting}
              onClick={() => { setStep('idle'); setDetachBlacklist(false) }}
              className="px-2 py-1.5 rounded text-[11px] font-semibold text-slate-500 border border-white/[.06] hover:bg-white/[.04] transition-colors"
            >
              Отмена
            </button>
          </div>
          <div className="text-[10px] text-slate-500 leading-snug space-y-0.5">
            <div><span className="text-emerald-500/70 font-medium">Поглотить</span> — бот продолжит управление, позиция не задвоится</div>
            <div><span className="text-rose-500/70 font-medium">Закрыть</span> — рыночно закрыть хедж и остановить пару</div>
            <div><span className="text-slate-400/70 font-medium">Оставить</span> — стратегия продолжит работу самостоятельно</div>
          </div>
        </div>
      )}

      {/* ── Divider ── */}
      <div className="h-px mx-3 bg-white/[.06]" />

      {/* ── Strategy rows ── */}
      <StrategyRow
        strategy={main} isMain={true}
        positions={positions} tickerPrices={tickerPrices}
        selectedStrategyId={selectedStrategyId}
        onEdit={onEdit} onChanged={onChanged} onSelect={onSelect}
      />
      <div className="h-px mx-3 bg-white/[.04]" />
      <StrategyRow
        strategy={hedge} isMain={false}
        positions={positions} tickerPrices={tickerPrices}
        selectedStrategyId={selectedStrategyId}
        onEdit={onEdit} onChanged={onChanged} onSelect={onSelect}
      />

      {/* ── Expanded panel ── */}
      {expanded && (
        <div className="border-t border-white/[.06] mt-1">
          {/* Tabs */}
          <div className="flex gap-1 px-3 pt-2 pb-1">
            {(['stats', 'log'] as const).map(tab => (
              <button
                key={tab}
                type="button"
                onClick={() => setExpandTab(tab)}
                className="px-3 py-1 rounded-md text-[11px] font-semibold uppercase tracking-[.5px] transition-colors"
                style={expandTab === tab
                  ? { background: 'rgba(255,255,255,.09)', color: '#e2e8f0' }
                  : { color: '#475569' }
                }
              >
                {tab === 'stats' ? 'Статистика' : 'Лог'}
              </button>
            ))}
          </div>

          {dataLoading ? (
            <div className="px-3 py-4 text-center text-[11px] text-slate-600">Загрузка…</div>
          ) : expandTab === 'stats' ? (

            // ── Stats ──
            <div className="px-3 pb-3 pt-1 grid grid-cols-2 gap-x-3">

              {/* ── Левая колонка ── */}
              <div className="space-y-1.5">
                <StatRow label="Вход Main"  value={fmtPrice(mainEntry, dec)} />
                <StatRow label="Вход Hedge" value={fmtPrice(hedgeEntry, dec)} />

                <div className="h-px bg-white/[.05] my-1.5" />

                <StatRow
                  label="Разрыв сейчас"
                  value={fmtPrice(currentGap, dec)}
                />
                <StatRow
                  label="Разрыв на старте"
                  value={hedgeSession?.gap_at_start != null ? fmtPrice(hedgeSession.gap_at_start, dec) : '—'}
                />
                <StatRow
                  label="Сокращение разрыва"
                  value={gapReduced !== null
                    ? `${gapReduced >= 0 ? '▼ ' : '▲ '}${fmtPrice(Math.abs(gapReduced), dec)}`
                    : '—'}
                  color={gapReduced !== null ? (gapReduced >= 0 ? '#6ee7b7' : '#fca5a5') : undefined}
                />

                <div className="h-px bg-white/[.05] my-1.5" />

                <StatRow
                  label={(() => {
                    const ct = hedgeBot?.strategyConfig?.hedge_deact_close_type ?? 0
                    const cv = hedgeBot?.strategyConfig?.hedge_deact_close_value ?? 0
                    if (ct === 2) return 'Цель закрытия (безубыток)'
                    if (ct === 1) return `Цель закрытия (ROI ${cv}%)`
                    return `Цель закрытия (P&L ${cv > 0 ? '+' : ''}${cv}$)`
                  })()}
                  value={fmtPrice(pairedCloseTarget, dec)}
                />
                {pairedCloseTarget !== null && (
                  <StatRow
                    label="До цели"
                    value={distanceToClose !== null
                      ? `${distanceToClose >= 0 ? '+' : ''}${fmtPrice(distanceToClose, dec)}`
                      : '—'}
                    color={distanceToClose !== null ? (distanceToClose >= 0 ? '#6ee7b7' : '#fca5a5') : undefined}
                  />
                )}

                <div className="h-px bg-white/[.05] my-1.5" />

                <StatRow
                  label="Накоплено хеджем"
                  value={hedgeSession != null ? fmtPnl(hedgeSession.cumulative_hedge_pnl) : '—'}
                  color={hedgeSession != null ? (hedgeSession.cumulative_hedge_pnl > 0 ? '#6ee7b7' : hedgeSession.cumulative_hedge_pnl < 0 ? '#fca5a5' : undefined) : undefined}
                />
                <StatRow
                  label="Сессия начата"
                  value={hedgeSession != null ? fmtDateTime(hedgeSession.started_at) : '—'}
                />
              </div>

              {/* ── Правая колонка ── */}
              <div className="space-y-1.5">
                {/* будет заполнена позже */}
              </div>

            </div>

          ) : (

            // ── Log ──
            <div className="px-3 pb-3 pt-1">
              {mergedLog.length === 0 ? (
                <div className="text-center text-[11px] text-slate-600 py-3">Событий нет</div>
              ) : (
                <div className="space-y-0.5 max-h-52 overflow-y-auto pr-0.5">
                  {mergedLog.map((e, i) => (
                    <div key={i} className="flex items-start gap-2 py-0.5">
                      <span
                        className="shrink-0 text-[9px] font-bold uppercase tracking-[.4px] mt-[2px] w-[34px]"
                        style={{ color: e.tag === 'Main' ? '#60a5fa' : '#f59e0b' }}
                      >
                        {e.tag}
                      </span>
                      <span className="shrink-0 text-[10px] text-slate-600 tabular-nums mt-[1px] w-[58px]">
                        {fmtTime(e.created_at)}
                      </span>
                      <span
                        className="text-[11px] leading-tight break-words min-w-0"
                        style={{ color: e.level === 'error' ? '#fca5a5' : e.level === 'warn' ? '#fde68a' : '#94a3b8' }}
                      >
                        {e.message}
                      </span>
                    </div>
                  ))}
                </div>
              )}
            </div>

          )}

          {/* ── Footer: close buttons ── */}
          <div className="px-3 pb-3 pt-2 border-t border-white/[.05] flex gap-2">
            <button
              type="button"
              disabled={!hedgePos}
              onClick={() => hedgePos && setCloseConfirm(makeCloseConfirm(hedgePos, hedge.account_id))}
              className="flex-1 py-1.5 text-[11px] font-semibold rounded-[8px] border text-amber-300 hover:text-amber-200 transition-colors disabled:opacity-40"
              style={{ background: 'rgba(245,158,11,.07)', borderColor: 'rgba(245,158,11,.25)' }}
            >
              Закрыть Хедж
            </button>
            <button
              type="button"
              disabled={!mainPos}
              onClick={() => mainPos && setCloseConfirm(makeCloseConfirm(mainPos, main.account_id))}
              className="flex-1 py-1.5 text-[11px] font-semibold rounded-[8px] border text-blue-300 hover:text-blue-200 transition-colors disabled:opacity-40"
              style={{ background: 'rgba(96,165,250,.07)', borderColor: 'rgba(96,165,250,.25)' }}
            >
              Закрыть Мэйн
            </button>
            {/* Удалить пару — всегда доступна (позиций может не быть) */}
            <button
              type="button"
              disabled={deleting}
              onClick={handleDeletePair}
              className={`py-1.5 px-3 text-[11px] font-semibold rounded-[8px] border transition-colors disabled:opacity-40 ${
                deleteConfirm
                  ? 'flex-1 text-white bg-rose-600/80 border-rose-500/60 hover:bg-rose-600'
                  : 'text-rose-400 hover:text-rose-200'
              }`}
              style={deleteConfirm ? {} : { background: 'rgba(248,113,113,.07)', borderColor: 'rgba(248,113,113,.25)' }}
              title="Удалить обе стратегии пары"
            >
              {deleting ? '…' : deleteConfirm ? '⚠ Подтвердить удаление' : 'Удалить пару'}
            </button>
          </div>
        </div>
      )}

      {closeConfirm && (
        <ClosePositionModal
          confirm={closeConfirm}
          onConfirm={handleConfirmClose}
          onCancel={() => setCloseConfirm(null)}
          closing={closing}
        />
      )}
    </div>
  )
}
