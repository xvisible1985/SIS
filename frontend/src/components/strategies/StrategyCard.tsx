import { useState, useEffect, useRef, useMemo, useCallback } from 'react'
import { createPortal } from 'react-dom'
import { useNavigate } from 'react-router-dom'
import { Calendar } from 'lucide-react'
import { setSignalChartIntent } from '../../stores/signalChartStore'
import { getStrategyState, getStrategyEvents, setStrategyStatus, deleteStrategy, updateStrategy, detachFromBot } from '../../api/strategies'
import { placeOrder } from '../../api/trader'
import { ClosePositionModal, makeCloseConfirm, type CloseConfirm } from '../common/ClosePositionModal'
import { useStrategyEventsWs } from '../../hooks/useStrategyEventsWs'
import { DebugCycleWindow } from './DebugCycleWindow'
import { CycleAuditModal } from './CycleAuditModal'
import { CoinIcon } from '../common/CoinIcon'

import { SIGNALS } from '../../features/indicators/signals'
import { INDICATORS } from '../../features/indicators/indicators'
import type { Strategy, StrategyState, StrategyEvent, ExchangeAccount, ActiveOrder, Position } from '../../types'

interface LiveSignal {
  signal_state: string
  signal_values: Record<string, number>
}

interface Props {
  strategy: Strategy
  accounts: ExchangeAccount[]
  orders: ActiveOrder[]
  positions?: Position[]
  tickerPrices?: Map<string, number>
  onEdit: (s: Strategy, filledCount: number) => void
  onChanged: () => void
  selected?: boolean
  onSelect?: (s: Strategy) => void
  isOpen?: boolean
  onToggleOpen?: () => void
  liveSignal?: LiveSignal
}

interface CardState {
  state: StrategyState | null
  events: StrategyEvent[]
  eventTotal: number
  loading: boolean
  acting: boolean
  actionError: string | null
}

// ── icons ──────────────────────────────────────────────────────────────────────
type IcProps = { s?: number; w?: number; c?: string }
const Ic = ({ s = 16, w = 1.7, c = 'currentColor', children }: IcProps & { children: React.ReactNode }) => (
  <svg width={s} height={s} viewBox="0 0 24 24" fill="none" stroke={c} strokeWidth={w}
    strokeLinecap="round" strokeLinejoin="round" style={{ display: 'block', flex: '0 0 auto' }}>{children}</svg>
)
const IcGear   = (p: IcProps) => <Ic {...p}><circle cx="12" cy="12" r="3"/><path d="M19.4 15a1.7 1.7 0 0 0 .3 1.8l.1.1a2 2 0 1 1-2.8 2.8l-.1-.1a1.7 1.7 0 0 0-1.8-.3 1.7 1.7 0 0 0-1 1.5V21a2 2 0 1 1-4 0v-.1A1.7 1.7 0 0 0 9 19.4a1.7 1.7 0 0 0-1.8.3l-.1.1a2 2 0 1 1-2.8-2.8l.1-.1a1.7 1.7 0 0 0 .3-1.8 1.7 1.7 0 0 0-1.5-1H3a2 2 0 1 1 0-4h.1A1.7 1.7 0 0 0 4.6 9a1.7 1.7 0 0 0-.3-1.8l-.1-.1a2 2 0 1 1 2.8-2.8l.1.1a1.7 1.7 0 0 0 1.8.3H9a1.7 1.7 0 0 0 1-1.5V3a2 2 0 1 1 4 0v.1a1.7 1.7 0 0 0 1 1.5 1.7 1.7 0 0 0 1.8-.3l.1-.1a2 2 0 1 1 2.8 2.8l-.1.1a1.7 1.7 0 0 0-.3 1.8V9a1.7 1.7 0 0 0 1.5 1H21a2 2 0 1 1 0 4h-.1a1.7 1.7 0 0 0-1.5 1z"/></Ic>
const IcUp     = (p: IcProps) => <Ic {...p}><path d="M7 14l5-5 5 5"/></Ic>
const IcDown   = (p: IcProps) => <Ic {...p}><path d="M7 10l5 5 5-5"/></Ic>
const IcSpark  = (p: IcProps) => <Ic {...p}><path d="M3 17l4-6 4 4 4-8 6 10"/></Ic>
const IcBolt   = (p: IcProps) => <Ic {...p}><path d="M13 3L4 14h7l-1 7 9-11h-7l1-7z"/></Ic>
const IcLog    = (p: IcProps) => <Ic {...p}><path d="M4 6h16M4 12h16M4 18h10"/></Ic>
const IcCheck  = (p: IcProps) => <Ic {...p}><circle cx="12" cy="12" r="9"/><path d="M9 12l2 2 4-4"/></Ic>
const IcCopy   = (p: IcProps) => <Ic {...p}><rect x="8" y="3" width="12" height="14" rx="2"/><path d="M16 21H6a2 2 0 0 1-2-2V8"/></Ic>
const IcTrash  = (p: IcProps) => <Ic {...p}><path d="M4 7h16M9 7V5a1 1 0 0 1 1-1h4a1 1 0 0 1 1 1v2M6 7l1 12a2 2 0 0 0 2 2h6a2 2 0 0 0 2-2l1-12"/></Ic>
const IcBug    = (p: IcProps) => <Ic {...p}><path d="M8 2l1.5 1.5M16 2l-1.5 1.5M9 9h6M9 13h6M3 9l3 1M21 9l-3 1M3 17l3-1M21 17l-3-1"/><path d="M6.5 6.5A5.5 5.5 0 0 1 17.5 6.5V17a5.5 5.5 0 0 1-11 0V6.5z"/></Ic>
const IcScope  = (p: IcProps) => <Ic {...p}><circle cx="11" cy="11" r="7"/><path d="M21 21l-4-4"/><path d="M8 11h6M11 8v6"/></Ic>

const IcChev   = (_: { open: boolean }) => (
  <svg width={9} height={9} viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth={2.6}
    strokeLinecap="round" strokeLinejoin="round" style={{ display: 'block', opacity: 0.6 }}>
    <path d="M6 9l6 6 6-6"/>
  </svg>
)

// ── yellow tooltip for card areas ─────────────────────────────────────────────
function CardTip({ text }: { text: string }) {
  return (
    <span
      className="pointer-events-none absolute bottom-full left-1/2 -translate-x-1/2 mb-1.5 w-56 rounded-[9px] px-3 py-2.5 text-[11px] leading-relaxed shadow-2xl opacity-0 group-hover/ctip:opacity-100 transition-opacity duration-150 whitespace-normal font-medium text-center"
      style={{ background: '#2d2500', border: '1px solid rgba(247,166,0,.35)', color: '#f5d97a', zIndex: 60 }}
    >
      {text}
    </span>
  )
}

// ── status dropdown picker ─────────────────────────────────────────────────────
type StratStatus = 'active' | 'finishing' | 'stopped'

const STATUS_ITEMS = [
  {
    id: 'active'    as StratStatus, label: 'Актив',   sub: 'live',
    dot:    { background: '#5be0a0', boxShadow: '0 0 6px #5be0a0' },
    dotCls: 'animate-pulse',
    btn:    { background: '#163327', border: '1px solid rgba(65,210,139,.40)', color: '#5be0a0' },
    tip:    'Полный автопилот. Стратегия сама выставляет ордера по сетке, двигает TP и SL по мере набора позиции, а после закрытия цикла сразу запускает следующий.',
  },
  {
    id: 'finishing' as StratStatus, label: 'Заверш.', sub: 'done',
    dot:    { background: '#f7a600' },
    dotCls: '',
    btn:    { background: '#2e2208', border: '1px solid rgba(247,166,0,.40)', color: '#f7a600' },
    tip:    'Мягкое завершение. Текущий цикл доживает до конца — сетка продолжает добирать ордера, TP и SL работают. Как только позиция закрыта, новый цикл уже не запускается и стратегия переходит в Стоп.',
  },
  {
    id: 'stopped'   as StratStatus, label: 'Стоп',    sub: 'paused',
    dot:    { background: '#9aa6c8' },
    dotCls: '',
    btn:    { background: '#1e2235', border: '1px solid rgba(255,255,255,.18)', color: '#cfd5e1' },
    tip:    'Жёсткая пауза. Все отложенные L-ордера снимаются с биржи немедленно, но TP и SL остаются — открытая позиция под защитой. Когда она закроется по TP или SL, цикл завершается и стратегия замолкает.',
  },
]

function StatusPicker({ value, acting, onChange, onInteract }: { value: StratStatus; acting: boolean; onChange: (v: StratStatus) => void; onInteract?: () => void }) {
  const [open, setOpen] = useState(false)
  const [menuPos, setMenuPos] = useState({ top: 0, right: 0 })
  const wrapRef = useRef<HTMLDivElement>(null)
  const btnRef = useRef<HTMLButtonElement>(null)
  const menuRef = useRef<HTMLDivElement>(null)
  const cur = STATUS_ITEMS.find(i => i.id === value) ?? STATUS_ITEMS[2]

  useEffect(() => {
    if (!open) return
    const handler = (e: MouseEvent) => {
      const target = e.target as Node
      const inWrap = wrapRef.current?.contains(target) ?? false
      const inMenu = menuRef.current?.contains(target) ?? false
      if (!inWrap && !inMenu) setOpen(false)
    }
    document.addEventListener('mousedown', handler)
    return () => document.removeEventListener('mousedown', handler)
  }, [open])

  const handleToggle = () => {
    if (!open && btnRef.current) {
      const r = btnRef.current.getBoundingClientRect()
      setMenuPos({ top: r.bottom + 4, right: window.innerWidth - r.right })
    }
    setOpen(o => !o)
  }

  return (
    <div ref={wrapRef} className="relative flex-shrink-0" onClick={e => { onInteract?.(); e.stopPropagation() }}>
      <button
        ref={btnRef}
        type="button"
        disabled={acting}
        onClick={handleToggle}
        className="h-7 inline-flex items-center gap-1.5 px-2.5 text-[10px] font-bold uppercase tracking-[.5px] rounded-[7px] cursor-pointer leading-none font-sans select-none disabled:opacity-40 transition-colors"
        style={cur.btn}
      >
        <span className={`w-[5px] h-[5px] rounded-full shrink-0 ${cur.dotCls}`} style={cur.dot} />
        {cur.label}
        <IcChev open={open} />
      </button>
      {open && createPortal(
        <div
          ref={menuRef}
          className="fixed z-[9999] min-w-[156px] rounded-[9px] p-1 flex flex-col gap-px"
          style={{
            top: menuPos.top,
            right: menuPos.right,
            background: '#181b28',
            border: '1px solid rgba(255,255,255,.22)',
            boxShadow: '0 8px 32px rgba(0,0,0,.85), 0 0 0 1px rgba(255,255,255,.06)',
          }}
        >
          {STATUS_ITEMS.map(it => (
            <div key={it.id} className="relative group/tip">
              <button
                type="button"
                onClick={() => { onChange(it.id); setOpen(false) }}
                className={`flex items-center gap-2 px-[9px] py-[8px] text-[12px] font-semibold text-[#cfd5e1] rounded-[6px] cursor-pointer w-full text-left transition-colors hover:bg-white/[.06] ${value === it.id ? 'bg-white/[.06] text-white' : ''}`}
              >
                <span className={`w-[6px] h-[6px] rounded-full shrink-0 ${it.dotCls}`} style={it.dot} />
                {it.label}
                <span className="ml-auto text-[10px] text-[#5b6479] uppercase tracking-[.5px] font-mono">{it.sub}</span>
              </button>
              <div className="pointer-events-none absolute right-full top-1/2 -translate-y-1/2 mr-2 w-56 rounded-[9px] px-3 py-2.5 text-[11px] leading-relaxed shadow-2xl z-30 opacity-0 group-hover/tip:opacity-100 transition-opacity duration-150 whitespace-normal font-medium"
                style={{ background: '#2d2500', border: '1px solid rgba(247,166,0,.35)', color: '#f5d97a' }}>
                {it.tip}
              </div>
            </div>
          ))}
        </div>,
        document.body
      )}
    </div>
  )
}

// ── bot badge with detach action ──────────────────────────────────────────────
function BotBadge({ botName, strategyId, onDetached, onInteract }: {
  botName: string
  strategyId: string
  onDetached: () => void
  onInteract?: () => void
}) {
  const [open, setOpen] = useState(false)
  const [menuPos, setMenuPos] = useState({ top: 0, right: 0 })
  const [acting, setActing] = useState(false)
  const btnRef = useRef<HTMLButtonElement>(null)
  const menuRef = useRef<HTMLDivElement>(null)

  useEffect(() => {
    if (!open) return
    const handler = (e: MouseEvent) => {
      const target = e.target as Node
      if (!btnRef.current?.contains(target) && !menuRef.current?.contains(target)) setOpen(false)
    }
    document.addEventListener('mousedown', handler)
    return () => document.removeEventListener('mousedown', handler)
  }, [open])

  const handleToggle = () => {
    if (!open && btnRef.current) {
      const r = btnRef.current.getBoundingClientRect()
      setMenuPos({ top: r.bottom + 4, right: window.innerWidth - r.right })
    }
    setOpen(o => !o)
  }

  const handleDetach = async () => {
    setActing(true)
    setOpen(false)
    try {
      await detachFromBot(strategyId)
      onDetached()
    } finally {
      setActing(false)
    }
  }

  return (
    <div className="relative flex-shrink-0" onClick={e => { onInteract?.(); e.stopPropagation() }}>
      <button
        ref={btnRef}
        type="button"
        disabled={acting}
        onClick={handleToggle}
        className="h-7 inline-flex items-center gap-1.5 px-2.5 text-[10px] font-bold uppercase tracking-[.5px] rounded-[7px] cursor-pointer leading-none font-sans select-none disabled:opacity-40 transition-colors"
        style={{ background: '#2d1b6b', border: '1px solid rgba(139,92,246,.50)', color: '#c4b5fd' }}
      >
        <span className="w-[5px] h-[5px] rounded-full shrink-0 bg-[#a78bfa]" />
        {botName}
        <IcChev open={open} />
      </button>
      {open && createPortal(
        <div
          ref={menuRef}
          className="fixed z-[9999] min-w-[156px] rounded-[9px] p-1"
          style={{
            top: menuPos.top,
            right: menuPos.right,
            background: '#181b28',
            border: '1px solid rgba(255,255,255,.22)',
            boxShadow: '0 8px 32px rgba(0,0,0,.85), 0 0 0 1px rgba(255,255,255,.06)',
          }}
        >
          <button
            type="button"
            onClick={handleDetach}
            className="flex items-center gap-2 px-[9px] py-[8px] text-[12px] font-semibold text-[#c4b5fd] rounded-[6px] cursor-pointer w-full text-left transition-colors hover:bg-white/[.06]"
          >
            открепить от бота
          </button>
        </div>,
        document.body
      )}
    </div>
  )
}

// ── signal direction helper ────────────────────────────────────────────────────
function inferSignalDir(sc: { name: string; params?: Record<string, unknown> }): 'buy' | 'sell' | 'neutral' {
  const dir = sc.params?.dir as string | undefined
  if (dir === 'вверх' || dir === 'up' || dir === 'bull') return 'buy'
  if (dir === 'вниз' || dir === 'down' || dir === 'bear') return 'sell'
  if (dir === 'оба') return 'neutral'
  const sig = SIGNALS.find(s => s.id === sc.name) ?? (INDICATORS as any[]).find(s => s.id === sc.name)
  return (sig?.state as 'buy' | 'sell' | 'neutral' | undefined) ?? 'neutral'
}

// ── helpers ────────────────────────────────────────────────────────────────────
function relTime(iso: string) {
  const d = (Date.now() - new Date(iso).getTime()) / 1000
  if (d < 60)    return `${Math.round(d)}с`
  if (d < 3600)  return `${Math.round(d / 60)}м`
  if (d < 86400) return `${Math.round(d / 3600)}ч`
  return `${Math.round(d / 86400)}д`
}

function lvlTag(status: string) {
  if (status === 'filled')    return { cls: 'bg-emerald-400/[.14] border border-emerald-400/20 text-emerald-300',  label: 'filled'    }
  if (status === 'placed')    return { cls: 'bg-amber-400/[.14] text-amber-300',                                   label: 'placed'    }
  if (status === 'cancelled') return { cls: 'bg-white/[.04] border border-white/[.06] text-slate-500',             label: 'cancel'    }
  if (status === 'sl_closed') return { cls: 'bg-rose-400/[.14] border border-rose-400/20 text-rose-300',           label: 'sl-closed' }
  return                             { cls: 'bg-white/[.04] border border-white/[.06] text-slate-500',             label: 'wait'      }
}

function logLvlStyle(lvl: string) {
  if (lvl === 'error') return { bg: 'bg-rose-400/15',   icon: <IcBolt  s={10} w={2.4} c="#fca5a5" />, color: 'text-rose-300'   }
  if (lvl === 'warn')  return { bg: 'bg-amber-400/18',  icon: <IcBolt  s={10} w={2.4} c="#f7a600"  />, color: 'text-amber-300'  }
  return                      { bg: 'bg-[#5b8cff]/18',  icon: <IcCheck s={10} w={2.4} c="#5b8cff"  />, color: 'text-[#5b8cff]'  }
}

// ── main component ─────────────────────────────────────────────────────────────
export function StrategyCard({ strategy: s, accounts, orders, positions, tickerPrices, onEdit, onChanged, selected, onSelect, isOpen, onToggleOpen, liveSignal }: Props) {
  const navigate = useNavigate()
  const [cs, setCs] = useState<CardState>({ state: null, events: [], eventTotal: 0, loading: false, acting: false, actionError: null })
  const [localEvents, setLocalEvents] = useState<StrategyEvent[] | null>(null)
  const [copiedLogIdx, setCopiedLogIdx] = useState<number | null>(null)
  const [logOpen, setLogOpen] = useState(false)
  const [logFilter, setLogFilter] = useState<'all' | 'error' | 'warn' | 'info'>('all')
  const [logDate, setLogDate] = useState('')
  const [logLoading, setLogLoading] = useState(false)

  // reload log when filter/date changes (only when card is open)
  useEffect(() => {
    if (!isOpen || !s.id) return
    loadLog(true)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [logFilter, logDate, isOpen, s.id])

  const [closeConfirm, setCloseConfirm] = useState<CloseConfirm | null>(null)
  const [closing, setClosing] = useState(false)
  const [debugOpen, setDebugOpen] = useState(false)
  const [auditOpen, setAuditOpen] = useState(false)
  const [hovered, setHovered] = useState(false)

  const accountName = accounts.find(a => a.id === s.account_id)?.label ?? s.account_id.slice(0, 8)
  const isLong = s.direction === 'long'

  // ── data loading ─────────────────────────────────────────────────────────────
  useEffect(() => {
    if (isOpen && !cs.state && !cs.loading) {
      setCs(p => ({ ...p, loading: true }))
      Promise.all([getStrategyState(s.id), getStrategyEvents(s.id)])
        .then(([state, { total, events }]) => {
          setCs(p => ({ ...p, state, events, eventTotal: total, loading: false }))
          setLocalEvents(null)
        })
        .catch(() => setCs(p => ({ ...p, loading: false })))
    }
  }, [isOpen])

  const strategyOrders = useMemo(() => {
    const id8 = s.id.slice(0, 8)
    return orders.filter(o => {
      const lk = o.orderLinkId ?? ''
      return lk.startsWith('SIS_STR-' + id8) || lk.startsWith('STR-' + id8) || lk.startsWith('STP-' + id8)
    })
  }, [orders, s.id])

  const strategyOrdersKey = useMemo(
    () => strategyOrders.map(o => o.orderId + ':' + o.orderStatus).sort().join(','),
    [strategyOrders],
  )

  const stratPosition = useMemo(() => {
    if (!positions) return null
    const wantSide = s.direction === 'long' ? 'Buy' : 'Sell'
    const wantIdx = s.hedge_mode ? (s.direction === 'long' ? 1 : 2) : 0
    return positions.find(p =>
      p.symbol === s.symbol &&
      p.side === wantSide &&
      parseFloat(p.size) > 0 &&
      (!s.hedge_mode || p.positionIdx === wantIdx)
    ) ?? null
  }, [positions, s.symbol, s.direction, s.hedge_mode])

  const pnlUsdt = useMemo(() => {
    if (!stratPosition) return null
    const entry = parseFloat(stratPosition.entryPrice)
    const size = parseFloat(stratPosition.size)
    const livePrice = tickerPrices?.get(stratPosition.symbol)
    const mark = livePrice ?? parseFloat(stratPosition.markPrice)
    return livePrice != null
      ? (stratPosition.side === 'Buy' ? (mark - entry) : (entry - mark)) * size
      : parseFloat(stratPosition.unrealisedPnl)
  }, [stratPosition, tickerPrices])

  const pnlPct = useMemo(() => {
    if (pnlUsdt === null || !stratPosition) return null
    const entry = parseFloat(stratPosition.entryPrice)
    const size = parseFloat(stratPosition.size)
    return entry > 0 && size > 0 ? (pnlUsdt / (size * entry)) * 100 : null
  }, [pnlUsdt, stratPosition])

  useEffect(() => {
    if (!isOpen || cs.acting) return
    Promise.all([getStrategyState(s.id), getStrategyEvents(s.id)])
      .then(([state, { total, events }]) => setCs(p => ({ ...p, state, events, eventTotal: total })))
      .catch(() => {})
  }, [strategyOrdersKey])

  const levelsScrollRef = useRef<HTMLDivElement>(null)
  const userScrolledRef = useRef(false)
  const programmaticRef = useRef(false)

  // Reset manual-scroll flag each time the card is closed
  useEffect(() => {
    if (!isOpen) userScrolledRef.current = false
  }, [isOpen])

  // Track manual scrolls (ignore programmatic ones)
  useEffect(() => {
    const container = levelsScrollRef.current
    if (!container) return
    const onScroll = () => {
      if (!programmaticRef.current) userScrolledRef.current = true
    }
    container.addEventListener('scroll', onScroll, { passive: true })
    return () => container.removeEventListener('scroll', onScroll)
  }, [])

  // Auto-center on the boundary between filled and placed levels
  useEffect(() => {
    const container = levelsScrollRef.current
    if (!container || !cs.state?.levels || userScrolledRef.current) return
    const els = Array.from(container.querySelectorAll<HTMLElement>('[data-lvl-status]'))
    const filled = els.filter(el => el.dataset.lvlStatus === 'filled')
    const lf = filled.length > 0 ? filled[filled.length - 1] : null
    const fp = els.find(el => el.dataset.lvlStatus === 'placed') ?? null
    const boundary =
      lf && fp ? (lf.offsetTop + lf.offsetHeight + fp.offsetTop) / 2
      : fp      ? fp.offsetTop + fp.offsetHeight / 2
      : lf      ? lf.offsetTop + lf.offsetHeight / 2
      : null
    if (boundary !== null) {
      programmaticRef.current = true
      container.scrollTop = boundary - container.clientHeight / 2
      requestAnimationFrame(() => { programmaticRef.current = false })
    }
  }, [cs.state?.levels])

  const hadPositionRef = useRef(false)
  useEffect(() => {
    if (!positions) return
    const expectedSide = s.direction === 'long' ? 'Buy' : 'Sell'
    const hasPos = positions.some(p => p.symbol === s.symbol && p.side === expectedSide && parseFloat(p.size) > 0)
    if (hadPositionRef.current && !hasPos) {
      Promise.all([getStrategyState(s.id), getStrategyEvents(s.id)])
        .then(([state, { total, events }]) => setCs(p => ({ ...p, state, events, eventTotal: total })))
        .catch(() => {})
      onChanged()
    }
    hadPositionRef.current = hasPos
  }, [positions])

  const onWsEvents = useCallback((newEvents: StrategyEvent[]) => {
    const reversed = newEvents.slice().reverse()
    const eventKey = (e: StrategyEvent) => `${e.created_at}||${e.message}`
    setCs(p => {
      const seen = new Set(p.events.map(eventKey))
      const fresh = reversed.filter(e => !seen.has(eventKey(e)))
      if (!fresh.length) return p
      return { ...p, events: [...fresh, ...p.events].slice(0, 200), eventTotal: p.eventTotal + fresh.length }
    })
    setLocalEvents(prev => {
      if (prev === null) return null
      const seen = new Set(prev.map(eventKey))
      const fresh = reversed.filter(e => !seen.has(eventKey(e)))
      return fresh.length ? [...fresh, ...prev].slice(0, 200) : prev
    })
  }, [])

  const wsSince = cs.events[0]?.created_at ?? null
  useStrategyEventsWs(s.id, !!isOpen, wsSince, onWsEvents)

  // ── actions ──────────────────────────────────────────────────────────────────
  async function handleStatus(status: StratStatus) {
    setCs(p => ({ ...p, acting: true, actionError: null }))
    try {
      await setStrategyStatus(s.id, status)
      onChanged()
      await new Promise(r => setTimeout(r, 300))
      const [state, evRes] = await Promise.all([
        getStrategyState(s.id).catch(() => null),
        getStrategyEvents(s.id).catch(() => ({ total: 0, events: [] as StrategyEvent[] })),
      ])
      setCs(p => ({ ...p, state, events: evRes.events, eventTotal: evRes.total, acting: false }))
    } catch (e: any) {
      const msg = e?.response?.data?.error ?? e?.message ?? 'Ошибка'
      setCs(p => ({ ...p, acting: false, actionError: msg }))
    }
  }

  async function handleDelete() {
    setCs(p => ({ ...p, acting: true }))
    try {
      await deleteStrategy(s.id)
      onChanged()
    } catch {
      setCs(p => ({ ...p, acting: false }))
    }
  }

  function handleCloseClick() {
    if (!stratPosition) return
    setCloseConfirm(makeCloseConfirm(stratPosition, s.account_id))
  }

  async function handleConfirmClose() {
    if (!closeConfirm) return
    setClosing(true)
    try {
      await placeOrder({
        account_id: closeConfirm.accountId,
        symbol: closeConfirm.pos.symbol,
        category: closeConfirm.pos.category,
        side: closeConfirm.pos.side === 'Buy' ? 'Sell' : 'Buy',
        order_type: 'Market',
        qty: closeConfirm.pos.size,
        reduce_only: true,
        position_idx: closeConfirm.pos.positionIdx,
      })
      onChanged()
    } catch { /* backend closes cycle via WS position event */ }
    setClosing(false)
    setCloseConfirm(null)
  }


  function tpPrice() {
    if (!cs.state || cs.state.avg_entry === 0) return null
    return s.direction === 'short'
      ? cs.state.avg_entry * (1 - s.tp_pct / 100)
      : cs.state.avg_entry * (1 + s.tp_pct / 100)
  }

  function slPrice() {
    if (!cs.state || cs.state.start_price === 0 || cs.state.avg_entry === 0) return null
    return s.direction === 'short'
      ? cs.state.start_price * (1 + s.sl_pct / 100)
      : cs.state.start_price * (1 - s.sl_pct / 100)
  }

  function handleHeaderClick() {
    onSelect?.(s)
  }

  const displayEvents = localEvents ?? cs.events

  async function copyToClipboard(text: string): Promise<boolean> {
    if (navigator.clipboard && window.isSecureContext) {
      try { await navigator.clipboard.writeText(text); return true } catch { /* fallthrough */ }
    }
    // Fallback for non-secure contexts (HTTP over remote IP)
    const ta = document.createElement('textarea')
    ta.value = text
    ta.style.position = 'fixed'
    ta.style.opacity = '0'
    ta.style.left = '-9999px'
    document.body.appendChild(ta)
    ta.focus()
    ta.select()
    let ok = false
    try { ok = document.execCommand('copy') } catch { /* ignore */ }
    document.body.removeChild(ta)
    return ok
  }

  async function copyLog() {
    const text = displayEvents.map(e =>
      `${new Date(e.created_at).toLocaleString('ru-RU')} [${e.level}] ${e.message}`
    ).join('\n')
    await copyToClipboard(text)
  }

  async function loadLog(reset = true) {
    if (!s.id) return
    setLogLoading(true)
    try {
      const res = await getStrategyEvents(s.id, {
        level: logFilter === 'all' ? undefined : logFilter,
        date: logDate || undefined,
        limit: 200,
        offset: reset ? 0 : cs.events.length,
      })
      setCs(p => ({
        ...p,
        events: reset ? res.events : [...p.events, ...res.events],
        eventTotal: res.total,
      }))
      if (reset) setLocalEvents(null)
    } finally {
      setLogLoading(false)
    }
  }

  async function copyLogRow(ev: StrategyEvent, idx: number) {
    const d = new Date(ev.created_at)
    const dateStr = d.toLocaleDateString('ru-RU', { day: '2-digit', month: '2-digit' })
    const timeStr = d.toLocaleTimeString('ru-RU', { hour: '2-digit', minute: '2-digit', second: '2-digit' })
    const meta = `acc:${accountName} · accid:${s.account_id.slice(0, 8)} · id:${s.id.slice(0, 8)}`
    const text = `${dateStr} ${timeStr}  [${ev.level}]  ${ev.message}  [${meta}]`
    const ok = await copyToClipboard(text)
    if (ok) {
      setCopiedLogIdx(idx)
      setTimeout(() => setCopiedLogIdx(null), 1400)
    }
  }

  // ── card border/bg — matches AccountCard style ───────────────────────────────
  const isActive = s.status === 'active'
  const baseBg     = isActive ? 'rgba(255,255,255,.05)' : 'rgba(255,255,255,.02)'
  const hoverBg    = isActive ? 'rgba(255,255,255,.08)' : 'rgba(255,255,255,.045)'
  const baseBorder = isActive ? 'rgba(255,255,255,.10)' : 'rgba(255,255,255,.06)'
  const cardStyle: React.CSSProperties = {
    background: selected
      ? 'linear-gradient(90deg,rgba(74,125,255,.18),rgba(74,125,255,.04))'
      : hovered ? hoverBg : baseBg,
    border: `1px solid ${selected ? 'rgba(74,125,255,.22)' : baseBorder}`,
    ...(selected ? { boxShadow: 'inset 2px 0 0 #5b8cff' } : {}),
  }

  // ────────────────────────────────────────────────────────────────────────────
  return (
    <div className="rounded-xl overflow-visible transition-all font-sans" style={cardStyle} onMouseEnter={() => setHovered(true)} onMouseLeave={() => setHovered(false)}>

      {/* ── collapsed header ── */}
      <div
        className="flex flex-col gap-2 px-3 py-2.5 cursor-pointer"
        onClick={handleHeaderClick}
      >
        {/* row 1: sym · side tag · account · gear · status */}
        <div className="flex items-center gap-2 flex-wrap">
          {/* left: symbol + badges */}
          <div className="min-w-0 flex-1 flex items-center gap-2 flex-wrap">
            <CoinIcon symbol={s.symbol} className="w-5 h-5" />
            <span className="font-display font-bold text-[15px] text-[#f2f5fb] tracking-[-0.2px] leading-none">{s.symbol}</span>
            <span
              className="inline-flex items-center gap-[3px] px-1.5 py-[2px] rounded-[5px] text-[10px] font-bold uppercase tracking-[.5px] leading-none"
              style={isLong
                ? { background: 'rgba(65,210,139,.14)', border: '1px solid rgba(65,210,139,.25)', color: '#5be0a0' }
                : { background: 'rgba(248,113,113,.14)', border: '1px solid rgba(248,113,113,.40)', color: '#fca5a5' }}
            >
              {isLong ? <IcUp s={9} w={2.6} /> : <IcDown s={9} w={2.6} />}
              <span style={{ transform: 'translateY(-.5px)', display: 'inline-block' }}>{s.direction}</span>
            </span>
          </div>

          {/* right: status · gear */}
          {s.bot_id
            ? <BotBadge botName={s.bot_name ?? 'Bot'} strategyId={s.id} onDetached={onChanged} onInteract={() => onSelect?.(s)} />
            : (
              <div className="flex flex-col items-end gap-0.5">
                <StatusPicker value={s.status} acting={cs.acting} onChange={handleStatus} onInteract={() => onSelect?.(s)} />
                {cs.actionError && (
                  <span className="text-[9px] text-red-400 max-w-[140px] text-right leading-tight">{cs.actionError}</span>
                )}
              </div>
            )
          }
          <button
            type="button"
            onClick={e => { e.stopPropagation(); setDebugOpen(o => !o) }}
            className={`w-7 h-7 inline-flex items-center justify-center rounded-[7px] border transition-colors shrink-0 ${debugOpen ? 'bg-amber-400/[.15] border-amber-400/40 text-amber-400' : 'text-slate-500 bg-white/[.04] border-white/[.08] hover:bg-white/[.08]'}`}
            title="Debug cycle window"
          >
            <IcBug s={13} w={1.6} />
          </button>
          <button
            type="button"
            onClick={e => { e.stopPropagation(); onSelect?.(s); onEdit(s, cs.state?.levels.filter(l => l.status === 'filled').length ?? 0) }}
            className="w-7 h-7 inline-flex items-center justify-center text-slate-400 rounded-[7px] bg-white/[.04] border border-white/[.08] hover:bg-white/[.08] transition-colors shrink-0"
          >
            <IcGear s={14} w={1.7} />
          </button>
        </div>

        {/* row 2: metrics · chev */}
        <div className="flex items-center gap-2 flex-wrap">
          <div className="min-w-0 flex-1 flex items-center gap-3.5 flex-wrap">
            <div className="relative group/ctip flex items-baseline gap-1.5">
              <span className="text-[11px] text-slate-400 uppercase tracking-[.8px] font-semibold">
                {s.strategy_type === 'matrix' ? 'Матрица' : 'Grid'}
              </span>
              <span className={`text-[13px] font-semibold ${s.active_levels > 0 ? 'text-slate-200' : 'text-slate-500'}`}>
                {s.active_levels}/{s.grid_levels}
              </span>
              <CardTip text={s.strategy_type === 'matrix' ? 'Заполненных слотов матрицы / всего слотов в цикле' : 'Исполненных уровней сетки / всего уровней в текущем цикле'} />
            </div>
            <div className="w-px h-2.5 bg-white/[.08]" />
            <div className="relative group/ctip flex items-baseline gap-1.5">
              <span className="text-[11px] text-slate-400 uppercase tracking-[.8px] font-semibold">Объём</span>
              <span className={`text-[13px] font-semibold ${(stratPosition?.sizeUsdt ?? 0) > 0 ? 'text-slate-200' : 'text-slate-500'}`}>
                {(stratPosition?.sizeUsdt ?? 0) > 0 ? `${(stratPosition!.sizeUsdt).toFixed(1)}$` : '0$'}
              </span>
              <CardTip text="Реальный объём открытой позиции с биржи (size × mark price)" />
            </div>
            <div className="w-px h-2.5 bg-white/[.08]" />
            <div className="relative group/ctip flex items-baseline gap-1.5">
              <span className="text-[11px] text-slate-400 uppercase tracking-[.8px] font-semibold">Маржа</span>
              <span className={`text-[13px] font-semibold ${stratPosition?.positionIM ? 'text-slate-200' : 'text-slate-500'}`}>
                {stratPosition?.positionIM ? `${stratPosition.positionIM.toFixed(2)}$` : '—'}
              </span>
              <CardTip text="Начальная маржа позиции (из данных биржи)" />
            </div>
            <div className="w-px h-2.5 bg-white/[.08]" />
            <div className="relative group/ctip flex items-baseline gap-1.5">
              <span className="text-[11px] text-slate-400 uppercase tracking-[.8px] font-semibold">P&L</span>
              <span className={`text-[13px] font-semibold ${
                pnlUsdt === null ? 'text-slate-500' : pnlUsdt > 0 ? 'text-emerald-300' : pnlUsdt < 0 ? 'text-rose-300' : 'text-slate-500'
              }`}>
                {pnlUsdt === null
                  ? '—'
                  : `${pnlUsdt >= 0 ? '+' : ''}${pnlUsdt.toFixed(2)}$ (${pnlPct !== null ? (pnlPct >= 0 ? '+' : '') + pnlPct.toFixed(1) + '%' : '—'})`
                }
              </span>
              <CardTip text="Нереализованный P&L по открытой позиции относительно вложенного капитала" />
            </div>
          </div>
          <div className="h-7 invisible w-16 shrink-0" aria-hidden="true" />
          <button
            type="button"
            onClick={e => { e.stopPropagation(); setAuditOpen(o => !o) }}
            className={`w-7 h-7 inline-flex items-center justify-center rounded-[7px] border transition-colors shrink-0 ${auditOpen ? 'bg-blue-400/[.15] border-blue-400/40 text-blue-400' : 'text-slate-500 bg-white/[.04] border-white/[.08] hover:bg-white/[.08]'}`}
            title="Аудит цикла"
          >
            <IcScope s={13} w={1.6} />
          </button>
          <button type="button" onClick={e => { e.stopPropagation(); onToggleOpen?.() }} className="w-7 h-7 inline-flex items-center justify-center text-slate-400 rounded-[7px] bg-white/[.04] border border-white/[.08] hover:bg-white/[.08] transition-colors shrink-0">
            <svg width={14} height={14} viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth={1.9} strokeLinecap="round" strokeLinejoin="round"
              className={`transition-transform duration-200 ${isOpen ? 'rotate-180' : ''}`}>
              <path d="M6 9l6 6 6-6" />
            </svg>
          </button>
        </div>
      </div>

      {/* ── expanded body ── */}
      <div
        className="grid transition-[grid-template-rows] duration-200 ease-in-out"
        style={{ gridTemplateRows: isOpen ? '1fr' : '0fr' }}
      >
        <div className="overflow-hidden">
          <div className="border-t border-white/[.06] flex flex-col gap-2.5 px-3 pt-3 pb-3.5">

            {cs.loading && (
              <div className="py-6 text-center text-[13px] text-slate-500">Загрузка…</div>
            )}

            {!cs.loading && (
              <>
                {/* signal rows */}
                {s.signal_configs?.length > 0 && (
                  <div className="flex flex-col gap-1.5">
                    {s.signal_configs.map((sc, idx) => {
                      const sig = SIGNALS.find(x => x.id === sc.name) ?? (INDICATORS as any[]).find(x => x.id === sc.name)
                      const tf = sc.params?.tf as string | undefined

                      const liveStateRaw = liveSignal?.signal_state ?? cs.state?.signal_state
                      const statusKey: 'buy' | 'sell' | 'neutral' =
                        liveStateRaw === 'buy' || liveStateRaw === 'sell' || liveStateRaw === 'neutral'
                          ? liveStateRaw
                          : inferSignalDir(sc)

                      const CARD_STYLE = {
                        buy:     { bg: 'rgba(65,210,139,.07)',  border: 'rgba(65,210,139,.22)',  name: '#a7f3d0' },
                        sell:    { bg: 'rgba(248,113,113,.07)', border: 'rgba(248,113,113,.22)', name: '#fca5a5' },
                        neutral: { bg: 'rgba(91,140,255,.06)',  border: 'rgba(91,140,255,.13)',  name: '#c4d2ff' },
                      }
                      const cs_ = CARD_STYLE[statusKey]

                      const liveVals = liveSignal?.signal_values ?? cs.state?.signal_values
                      const liveVal = liveVals?.[sc.name]
                      const paramBadge = liveVal != null
                        ? String(liveVal)
                        : ((sc.params?.dir ?? sc.params?.kind) as string | undefined)

                      async function handleDeleteSignal(e: React.MouseEvent) {
                        e.stopPropagation()
                        const newConfigs = s.signal_configs.filter((_, i) => i !== idx)
                        await updateStrategy(s.id, {
                          ...s,
                          steps: s.steps ?? [],
                          signal_configs: newConfigs,
                          signal_filter: newConfigs.length > 0,
                          grid_levels: s.steps?.length || s.grid_levels || 1,
                          grid_active: s.grid_active || s.steps?.length || 1,
                          grid_step_pct: s.steps?.[0]?.price_move_pct ?? s.grid_step_pct ?? 0,
                          trailing_activation_pct: s.trailing_activation_pct ?? 0,
                          trailing_callback_pct: s.trailing_callback_pct ?? 0,
                        } as any)
                        onChanged()
                      }

                      return (
                        <div key={sc.name + idx} className="flex items-center gap-2 min-w-0">
                          {/* card */}
                          <div
                            className="flex-1 flex items-center gap-3 px-3 py-2.5 rounded-[9px] min-w-0"
                            style={{ background: cs_.bg, border: `1px solid ${cs_.border}` }}
                          >
                            <span className="shrink-0 text-[15px] font-semibold" style={{ color: cs_.name }}>
                              {sig?.name ?? sc.name}
                            </span>
                            {sig?.desc && (
                              <span className="flex-1 min-w-0 text-[12px] text-[#8b9ab8] truncate">
                                {sig.desc}
                              </span>
                            )}
                            {!sig?.desc && <span className="flex-1" />}
                            {tf && (
                              <span className="shrink-0 inline-flex items-center gap-[3px] text-[12px] font-mono">
                                <span className="text-[#5b6479]">TF</span>
                                <span className="text-[#8babff] font-semibold">{tf}</span>
                              </span>
                            )}
                            {paramBadge && (
                              <span className="shrink-0 px-2 py-[3px] rounded-[5px] text-[10px] font-mono text-[#8b9ab8] bg-black/20 border border-white/[.07]">
                                {paramBadge}
                              </span>
                            )}
                          </div>
                          {/* chart icon — outside the card */}
                          <button
                            type="button"
                            onClick={e => {
                              e.stopPropagation()
                              setSignalChartIntent({
                                signalId: sc.name,
                                signalName: sig?.name ?? sc.name,
                                params: (sc.params ?? {}) as Record<string, unknown>,
                                symbol: s.symbol,
                                tf: (sc.params?.tf as string) ?? '1h',
                              })
                              navigate('/signal-chart')
                            }}
                            className="shrink-0 w-7 h-7 inline-flex items-center justify-center rounded-[6px] text-slate-500 hover:text-[#5b8cff] hover:bg-[#5b8cff]/[.10] transition-colors"
                            title="График сигнала"
                          >
                            <IcSpark s={13} w={1.8} />
                          </button>
                          {/* delete — outside the card */}
                          <button
                            type="button"
                            onClick={handleDeleteSignal}
                            className="shrink-0 w-7 h-7 inline-flex items-center justify-center rounded-[6px] text-slate-500 hover:text-rose-300 hover:bg-rose-400/[.10] transition-colors"
                          >
                            <IcTrash s={14} w={1.8} />
                          </button>
                        </div>
                      )
                    })}
                  </div>
                )}

                {/* two-col: levels + TP/SL */}
                <div className="grid grid-cols-1 md:grid-cols-2 gap-2">
                  {/* levels */}
                  <div className="bg-black/[.18] border border-white/[.05] rounded-[10px] p-3">
                    <div className="flex items-center justify-between mb-2">
                      <span className="text-[10px] text-slate-400 uppercase tracking-[1.3px] font-semibold">
                        {s.strategy_type === 'matrix' ? 'Слоты матрицы' : 'Уровни'} · {cs.state?.levels.filter(l => l.status === 'filled' || l.status === 'sl_closed').length ?? 0}/{s.grid_levels}
                      </span>
                      <span className="text-[10px] text-[#5b6479] font-mono font-medium">{s.strategy_type === 'matrix' ? `${s.grid_levels} слотов` : `L1–L${s.grid_levels}`}</span>
                    </div>
                    {s.status === 'active' && s.signal_filter && !!(s.signal_configs?.length) &&
                     !['buy', 'sell'].includes((liveSignal?.signal_state ?? cs.state?.signal_state) ?? '') && (
                      <div className="mb-1.5 px-2 py-[5px] rounded-[5px] bg-amber-500/10 border border-amber-500/15 text-amber-300/80 text-[10px] text-center font-semibold tracking-[.3px]">
                        Ожидание сигнала
                      </div>
                    )}
                    <div
                      ref={levelsScrollRef}
                      className="max-h-[180px] overflow-y-auto flex flex-col gap-px"
                      style={{ scrollbarWidth: 'thin', scrollbarColor: 'rgba(255,255,255,.14) transparent' }}
                    >
                      {cs.state && cs.state.levels.length > 0 ? (
                        (s.strategy_type === 'matrix'
                          ? [...cs.state.levels].sort((a, b) => (b.target_price ?? 0) - (a.target_price ?? 0))
                          : cs.state.levels
                        ).map(l => {
                          const levelStep = s.strategy_type === 'grid' ? s.steps?.[l.level_idx - 1] : undefined
                          const sigState = liveSignal?.signal_state ?? cs.state?.signal_state
                          const tag = s.status === 'active' && s.signal_filter && !!(s.signal_configs?.length) &&
                            !['buy', 'sell'].includes(sigState ?? '') &&
                            l.status === 'pending' && levelStep?.use_signal
                            ? { label: 'сигнал', cls: 'bg-amber-500/20 text-amber-300' }
                            : lvlTag(l.status)
                          const isFilled = l.status === 'filled' || l.status === 'sl_closed'
                          // find matching active order for filled levels to show real executed volume
                          const order = isFilled ? strategyOrders.find(o => o.orderId === l.exchange_order_id) : null
                          const realQty = order ? parseFloat(order.cumExecQty) : 0
                          const usdtVal = isFilled
                            ? (realQty > 0 && l.filled_price > 0
                                ? Math.round(realQty * l.filled_price)
                                : Math.round(l.size_usdt))
                            : Math.round(l.size_usdt)
                          const priceVal = isFilled && l.filled_price > 0 ? l.filled_price : l.target_price
                          const isMatrix = s.strategy_type === 'matrix'
                          return (
                            <div key={l.level_idx} data-lvl-status={l.status}
                              className="grid gap-2 items-center px-1 py-[5px] rounded-[5px] font-mono text-[11px]"
                              style={{ gridTemplateColumns: 'auto 1fr auto' }}
                            >
                              {isMatrix && l.slot != null
                                ? <span className={`text-[9px] px-[4px] py-[1px] rounded-[3px] font-bold ${
                                    l.slot === 0 ? 'bg-yellow-500/25 text-yellow-300' :
                                    l.slot < 0   ? 'bg-blue-500/20 text-blue-300' :
                                    s.direction === 'long' ? 'bg-emerald-500/25 text-emerald-300' :
                                                             'bg-red-500/25 text-red-300'
                                  }`}>L({l.slot})</span>
                                : <span className="text-[10px] text-[#5b6479] w-[18px]">L{l.level_idx}</span>
                              }
                              <span>
                                <span className="text-[#e6ebf5] font-bold">{usdtVal}$</span>
                                {priceVal > 0 && <span className="text-[#5b6479] font-normal"> ({priceVal.toFixed(2)})</span>}
                                {isMatrix && l.sl_price && l.sl_price > 0 && (
                                  <span className="text-rose-400/70 font-normal"> sl:{l.sl_price.toFixed(2)}</span>
                                )}
                              </span>
                              <span className={`text-[9px] px-[5px] py-[2px] rounded-[3px] font-bold uppercase tracking-[.3px] ${tag.cls}`}>{tag.label}</span>
                            </div>
                          )
                        })
                      ) : (
                        <div className="py-4 text-center text-[11px] text-slate-500">Нет активного цикла</div>
                      )}
                    </div>
                  </div>

                  {/* TP + SL */}
                  <div className="flex flex-col gap-2">
                    <div className="relative group/ctip bg-black/[.18] border border-white/[.06] rounded-[10px] p-2.5 flex flex-col gap-1.5">
                      <div className="flex items-center justify-between">
                        <span className="flex items-center gap-1.5 text-[12px] font-semibold text-emerald-300">
                          <IcSpark s={13} c="#5be0a0" />Take Profit
                        </span>
                        <span className="text-[9px] text-slate-500 uppercase tracking-[.6px] font-semibold">auto</span>
                      </div>
                      <div className="font-display text-[18px] font-bold tracking-[-0.4px] text-emerald-300">
                        +{s.tp_pct}%
                      </div>
                      <div className="text-[11px] text-slate-500">
                        {tpPrice() ? tpPrice()!.toFixed(2) + ' от avg' : 'от avg entry'}
                      </div>
                      <CardTip text="Цена закрытия позиции в прибыль. Рассчитывается от средней цены входа по всем взятым уровням." />
                    </div>
                    {s.strategy_type === 'matrix' ? (
                      <div className="relative group/ctip bg-black/[.18] border border-white/[.06] rounded-[10px] p-2.5 flex flex-col gap-1.5">
                        <div className="flex items-center justify-between">
                          <span className="flex items-center gap-1.5 text-[12px] font-semibold text-rose-300">
                            <IcBolt s={13} c="#fca5a5" />Stop Loss
                          </span>
                          <span className="text-[9px] text-slate-500 uppercase tracking-[.6px] font-semibold">per-level</span>
                        </div>
                        <div className="font-display text-[18px] font-bold tracking-[-0.4px] text-rose-300">
                          по уровням
                        </div>
                        <div className="text-[11px] text-slate-500">
                          индивидуальный для каждого уровня
                        </div>
                        <CardTip text="Матрикс-стратегия: SL выставляется индивидуально для каждого заполненного уровня согласно его настройкам stop_pct." />
                      </div>
                    ) : (
                      <div className="relative group/ctip bg-black/[.18] border border-white/[.06] rounded-[10px] p-2.5 flex flex-col gap-1.5">
                        <div className="flex items-center justify-between">
                          <span className="flex items-center gap-1.5 text-[12px] font-semibold text-rose-300">
                            <IcBolt s={13} c="#fca5a5" />Stop Loss
                          </span>
                          <span className="text-[9px] text-slate-500 uppercase tracking-[.6px] font-semibold">{s.sl_type === 'conditional' ? 'cond.' : 'prog.'}</span>
                        </div>
                        <div className="font-display text-[18px] font-bold tracking-[-0.4px] text-rose-300">
                          {s.sl_pct < 0 ? s.sl_pct : `-${s.sl_pct}`}%
                        </div>
                        <div className="text-[11px] text-slate-500">
                          {slPrice() ? slPrice()!.toFixed(2) + ' от start' : 'от start price'}
                        </div>
                        <CardTip text={`Принудительное закрытие при убытке. Тип: ${s.sl_type === 'conditional' ? 'биржевой (срабатывает даже если сервер недоступен)' : 'программный (контролируется сервисом через WS)'}.`} />
                      </div>
                    )}
                  </div>
                </div>

                {/* log card — always visible, collapsed by default */}
                <div className="bg-black/[.18] border border-white/[.05] rounded-[10px] overflow-hidden">
                  <button type="button" onClick={() => setLogOpen(o => !o)}
                    className="w-full flex items-center gap-2 px-3 py-2 border-b border-white/[.04] hover:bg-white/[.03] transition-colors">
                    <IcLog s={12} c="#7b8aa6" />
                    <span className="text-[10px] text-slate-400 uppercase tracking-[1.3px] font-semibold">Лог событий</span>
                    {cs.eventTotal > 0 && (
                      <span className="text-[9px] px-1.5 py-0.5 bg-white/[.06] rounded-[3px] text-[#cfd5e1] font-mono font-semibold ml-0.5">{cs.eventTotal}</span>
                    )}
                    <div className="ml-auto flex items-center gap-0.5">
                      {logOpen && displayEvents.length > 0 && (<>
                        <span onClick={e => { e.stopPropagation(); copyLog() }}
                          className="w-6 h-6 inline-flex items-center justify-center text-slate-500 rounded-[6px] hover:bg-white/[.06] hover:text-[#e6ebf5] transition-colors">
                          <IcCopy s={12} w={1.7} />
                        </span>
                        <span onClick={e => { e.stopPropagation(); setLocalEvents([]) }}
                          className="w-6 h-6 inline-flex items-center justify-center text-slate-500 rounded-[6px] hover:bg-white/[.06] hover:text-rose-300 transition-colors">
                          <IcTrash s={12} w={1.7} />
                        </span>
                      </>)}
                      <svg width={10} height={10} viewBox="0 0 24 24" fill="none" stroke="#5b6479" strokeWidth={2.5} strokeLinecap="round" strokeLinejoin="round"
                        style={{ transform: logOpen ? 'rotate(180deg)' : 'none', transition: 'transform .15s' }}>
                        <path d="M6 9l6 6 6-6"/>
                      </svg>
                    </div>
                  </button>
                  {logOpen && (
                    <>
                      {/* log filters */}
                      <div className="flex flex-wrap items-center gap-2 px-3 py-2 border-b border-white/[.04]">
                        {(['all', 'error', 'warn', 'info'] as const).map(lvl => (
                          <button
                            key={lvl}
                            type="button"
                            onClick={() => setLogFilter(lvl)}
                            className={`text-[10px] font-bold uppercase tracking-wider px-1.5 py-0.5 rounded-[3px] border transition-colors ${
                              logFilter === lvl
                                ? 'bg-white/[.08] border-white/[.12] text-slate-200'
                                : 'bg-transparent border-white/[.04] text-slate-500 hover:text-slate-300'
                            }`}
                          >
                            {lvl === 'all' ? 'Все' : lvl}
                          </button>
                        ))}
                        <div className="relative flex items-center">
                          <Calendar size={12} className="absolute left-1.5 text-slate-500 pointer-events-none" />
                          <input
                            type="date"
                            value={logDate}
                            onChange={e => setLogDate(e.target.value)}
                            className="text-[11px] bg-black/30 border border-white/[.08] rounded pl-6 pr-2 py-0.5 text-slate-300 outline-none"
                          />
                        </div>
                        {logLoading && <span className="text-[10px] text-slate-500 animate-pulse">…</span>}
                      </div>
                      {/* events list */}
                      <div className="flex flex-col max-h-[200px] overflow-y-auto"
                        style={{ scrollbarWidth: 'thin', scrollbarColor: 'rgba(255,255,255,.14) transparent' }}>
                        {displayEvents.length === 0 ? (
                          <div className="py-3 text-center text-[11px] text-slate-600">Событий нет</div>
                        ) : displayEvents.map((e, i) => {
                          const ls = logLvlStyle(e.level)
                          const d = new Date(e.created_at)
                          const copied = copiedLogIdx === i
                          return (
                            <div key={i}
                              onClick={() => copyLogRow(e, i)}
                              className={`grid items-start gap-2.5 px-3 py-1.5 font-mono text-[11px] border-t border-white/[.03] first:border-t-0 cursor-pointer transition-colors duration-150 ${copied ? 'bg-emerald-400/[.08]' : 'hover:bg-white/[.03]'}`}
                              style={{ gridTemplateColumns: 'auto auto 1fr auto' }}
                            >
                              <span className="text-[10px] text-[#5b6479] leading-[1.35] pt-px">
                                <span className="block">{d.toLocaleDateString('ru-RU', { day: '2-digit', month: '2-digit' })}</span>
                                <span className="block">{d.toLocaleTimeString('ru-RU', { hour: '2-digit', minute: '2-digit', second: '2-digit' })}</span>
                              </span>
                              <span className={`w-[14px] h-[14px] rounded-[3px] flex items-center justify-center shrink-0 mt-px ${ls.bg} ${ls.color}`}>
                                {ls.icon}
                              </span>
                              <span className="text-[#cfd5e1] break-words whitespace-pre-wrap">{e.message}</span>
                              <span className={`text-[10px] pt-px transition-colors duration-150 ${copied ? 'text-emerald-400' : 'text-[#5b6479]'}`}>
                                {copied ? '✓' : relTime(e.created_at)}
                              </span>
                            </div>
                          )
                        })}
                        {cs.events.length < cs.eventTotal && (
                          <button
                            type="button"
                            onClick={() => loadLog(false)}
                            disabled={logLoading}
                            className="py-2 text-[11px] font-semibold text-[#5b8cff] hover:text-[#7ba4ff] transition-colors border-t border-white/[.04]"
                          >
                            {logLoading ? 'Загрузка…' : `Загрузить ещё (${cs.eventTotal - cs.events.length})`}
                          </button>
                        )}
                      </div>
                    </>
                  )}
                </div>

              </>
            )}

            {/* bottom action buttons */}
            <div className="flex gap-2 pt-0.5">
              <button
                type="button"
                onClick={e => { e.stopPropagation(); onSelect?.(s); onEdit(s, cs.state?.levels.filter(l => l.status === 'filled').length ?? 0) }}
                className="relative group/ctip flex-1 py-1.5 text-[11px] font-semibold rounded-[8px] bg-white/[.04] border border-white/[.08] text-slate-300 hover:bg-white/[.08] hover:text-white transition-colors"
              >
                Настроить
                <CardTip text="Открыть настройки стратегии — изменить сетку, TP/SL, плечо и параметры входа" />
              </button>
              <button
                type="button"
                disabled={cs.acting || !stratPosition}
                onClick={e => { e.stopPropagation(); handleCloseClick() }}
                className="relative group/ctip flex-1 py-1.5 text-[11px] font-semibold rounded-[8px] border text-amber-300 hover:text-amber-200 transition-colors disabled:opacity-40"
                style={{ background: 'rgba(247,166,0,.07)', borderColor: 'rgba(247,166,0,.25)' }}
              >
                Закрыть
                <CardTip text={stratPosition ? 'Закрыть открытую позицию рыночным ордером прямо сейчас. Все L-ордера отменяются, цикл завершается.' : 'Нет открытой позиции — кнопка недоступна'} />
              </button>
              <button
                type="button"
                disabled={cs.acting || s.status !== 'stopped'}
                onClick={e => { e.stopPropagation(); handleDelete() }}
                className="relative group/ctip flex-1 py-1.5 text-[11px] font-semibold rounded-[8px] border text-rose-300 hover:text-rose-200 transition-colors disabled:opacity-40"
                style={{ background: 'rgba(248,113,113,.07)', borderColor: 'rgba(248,113,113,.25)' }}
              >
                Удалить
                <CardTip text="Полностью удалить стратегию из системы. Доступно только в статусе Стоп." />
              </button>
            </div>

          </div>
        </div>
      </div>

      {closeConfirm && (
        <ClosePositionModal
          confirm={closeConfirm}
          onConfirm={handleConfirmClose}
          onCancel={() => setCloseConfirm(null)}
          closing={closing}
        />
      )}

      {debugOpen && createPortal(
        <DebugCycleWindow strategy={s} orders={strategyOrders} onClose={() => setDebugOpen(false)} liveSignal={liveSignal} />,
        document.body
      )}

      {auditOpen && createPortal(
        <CycleAuditModal strategyId={s.id} strategySymbol={s.symbol} onClose={() => setAuditOpen(false)} />,
        document.body
      )}
    </div>
  )
}
