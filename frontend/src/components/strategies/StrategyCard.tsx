import { useState, useEffect, useRef, useMemo, useCallback } from 'react'
import { getStrategyState, getStrategyEvents, setStrategyStatus, deleteStrategy } from '../../api/strategies'
import { placeOrder } from '../../api/trader'
import { ClosePositionModal, makeCloseConfirm, type CloseConfirm } from '../common/ClosePositionModal'
import { useStrategyEventsWs } from '../../hooks/useStrategyEventsWs'
import { DebugCycleWindow } from './DebugCycleWindow'

import type { Strategy, StrategyState, StrategyEvent, ExchangeAccount, ActiveOrder, Position } from '../../types'

interface Props {
  strategy: Strategy
  accounts: ExchangeAccount[]
  orders: ActiveOrder[]
  positions?: Position[]
  onEdit: (s: Strategy, filledCount: number) => void
  onChanged: () => void
  selected?: boolean
  onSelect?: (s: Strategy) => void
  isOpen?: boolean
  onToggleOpen?: () => void
}

interface CardState {
  state: StrategyState | null
  events: StrategyEvent[]
  loading: boolean
  acting: boolean
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
const IcSignal = (p: IcProps) => <Ic {...p}><path d="M3 12l4-4 3 3 5-5 6 6"/><circle cx="20" cy="12" r="1.4" fill="currentColor"/></Ic>
const IcLog    = (p: IcProps) => <Ic {...p}><path d="M4 6h16M4 12h16M4 18h10"/></Ic>
const IcCheck  = (p: IcProps) => <Ic {...p}><circle cx="12" cy="12" r="9"/><path d="M9 12l2 2 4-4"/></Ic>
const IcCopy   = (p: IcProps) => <Ic {...p}><rect x="8" y="3" width="12" height="14" rx="2"/><path d="M16 21H6a2 2 0 0 1-2-2V8"/></Ic>
const IcTrash  = (p: IcProps) => <Ic {...p}><path d="M4 7h16M9 7V5a1 1 0 0 1 1-1h4a1 1 0 0 1 1 1v2M6 7l1 12a2 2 0 0 0 2 2h6a2 2 0 0 0 2-2l1-12"/></Ic>
const IcBug    = (p: IcProps) => <Ic {...p}><path d="M8 2l1.5 1.5M16 2l-1.5 1.5M9 9h6M9 13h6M3 9l3 1M21 9l-3 1M3 17l3-1M21 17l-3-1"/><path d="M6.5 6.5A5.5 5.5 0 0 1 17.5 6.5V17a5.5 5.5 0 0 1-11 0V6.5z"/></Ic>
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
  const ref = useRef<HTMLDivElement>(null)
  const cur = STATUS_ITEMS.find(i => i.id === value) ?? STATUS_ITEMS[2]

  useEffect(() => {
    if (!open) return
    const handler = (e: MouseEvent) => { if (ref.current && !ref.current.contains(e.target as Node)) setOpen(false) }
    document.addEventListener('mousedown', handler)
    return () => document.removeEventListener('mousedown', handler)
  }, [open])

  return (
    <div ref={ref} className="relative flex-shrink-0" onClick={e => { onInteract?.(); e.stopPropagation() }}>
      <button
        type="button"
        disabled={acting}
        onClick={() => setOpen(o => !o)}
        className="h-7 inline-flex items-center gap-1.5 px-2.5 text-[10px] font-bold uppercase tracking-[.5px] rounded-[7px] cursor-pointer leading-none font-sans select-none disabled:opacity-40 transition-colors"
        style={cur.btn}
      >
        <span className={`w-[5px] h-[5px] rounded-full shrink-0 ${cur.dotCls}`} style={cur.dot} />
        {cur.label}
        <IcChev open={open} />
      </button>
      {open && (
        <div className="absolute top-[calc(100%+4px)] right-0 z-20 min-w-[156px] bg-[#141a28] border border-white/10 rounded-[9px] p-1 shadow-[0_14px_36px_-10px_rgba(0,0,0,.7),inset_0_0_0_1px_rgba(255,255,255,.02)] flex flex-col gap-px">
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
        </div>
      )}
    </div>
  )
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
  if (status === 'filled')    return { cls: 'bg-emerald-400/[.14] border border-emerald-400/20 text-emerald-300',  label: 'filled'  }
  if (status === 'placed')    return { cls: 'bg-amber-400/[.14] text-amber-300',                                   label: 'placed'  }
  if (status === 'cancelled') return { cls: 'bg-white/[.04] border border-white/[.06] text-slate-500',             label: 'cancel'  }
  return                             { cls: 'bg-white/[.04] border border-white/[.06] text-slate-500',             label: 'wait'    }
}

function logLvlStyle(lvl: string) {
  if (lvl === 'error') return { bg: 'bg-rose-400/15',   icon: <IcBolt  s={10} w={2.4} c="#fca5a5" />, color: 'text-rose-300'   }
  if (lvl === 'warn')  return { bg: 'bg-amber-400/18',  icon: <IcBolt  s={10} w={2.4} c="#f7a600"  />, color: 'text-amber-300'  }
  return                      { bg: 'bg-[#5b8cff]/18',  icon: <IcCheck s={10} w={2.4} c="#5b8cff"  />, color: 'text-[#5b8cff]'  }
}

// ── main component ─────────────────────────────────────────────────────────────
export function StrategyCard({ strategy: s, accounts, orders, positions, onEdit, onChanged, selected, onSelect, isOpen, onToggleOpen }: Props) {
  const [cs, setCs] = useState<CardState>({ state: null, events: [], loading: false, acting: false })
  const [localEvents, setLocalEvents] = useState<StrategyEvent[] | null>(null)
  const [copiedLogIdx, setCopiedLogIdx] = useState<number | null>(null)
  const [logOpen, setLogOpen] = useState(false)
  const [closeConfirm, setCloseConfirm] = useState<CloseConfirm | null>(null)
  const [closing, setClosing] = useState(false)
  const [debugOpen, setDebugOpen] = useState(false)

  const accountName = accounts.find(a => a.id === s.account_id)?.label ?? s.account_id.slice(0, 8)
  const isLong = s.direction === 'long'
  const isRunning = s.status === 'active' || s.status === 'finishing'

  // ── data loading ─────────────────────────────────────────────────────────────
  useEffect(() => {
    if (isOpen && !cs.state && !cs.loading) {
      setCs(p => ({ ...p, loading: true }))
      Promise.all([getStrategyState(s.id), getStrategyEvents(s.id)])
        .then(([state, events]) => {
          setCs(p => ({ ...p, state, events, loading: false }))
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

  const pnlUsdt = stratPosition ? parseFloat(stratPosition.unrealisedPnl) : null
  const pnlPct  = pnlUsdt !== null && s.volume_usdt > 0 ? pnlUsdt / s.volume_usdt * 100 : null

  useEffect(() => {
    if (!isOpen || cs.acting) return
    Promise.all([getStrategyState(s.id), getStrategyEvents(s.id)])
      .then(([state, events]) => setCs(p => ({ ...p, state, events })))
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
        .then(([state, events]) => setCs(p => ({ ...p, state, events })))
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
      return { ...p, events: [...fresh, ...p.events].slice(0, 200) }
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
    setCs(p => ({ ...p, acting: true }))
    try {
      await setStrategyStatus(s.id, status)
      onChanged()
      await new Promise(r => setTimeout(r, 300))
      const [state, events] = await Promise.all([
        getStrategyState(s.id).catch(() => null),
        getStrategyEvents(s.id).catch(() => [] as StrategyEvent[]),
      ])
      setCs(p => ({ ...p, state, events, acting: false }))
    } catch {
      setCs(p => ({ ...p, acting: false }))
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

  function signalLabel(sc: { name: string; params: Record<string, number> }) {
    const pv = Object.values(sc.params ?? {})
    return pv.length ? `${sc.name}(${pv.join(',')})` : sc.name
  }

  const displayEvents = localEvents ?? cs.events

  async function copyLog() {
    const text = displayEvents.map(e =>
      `${new Date(e.created_at).toLocaleString('ru-RU')} [${e.level}] ${e.message}`
    ).join('\n')
    await navigator.clipboard.writeText(text).catch(() => {})
  }

  async function copyLogRow(ev: StrategyEvent, idx: number) {
    const d = new Date(ev.created_at)
    const dateStr = d.toLocaleDateString('ru-RU', { day: '2-digit', month: '2-digit' })
    const timeStr = d.toLocaleTimeString('ru-RU', { hour: '2-digit', minute: '2-digit', second: '2-digit' })
    const meta = `acc:${accountName} · accid:${s.account_id.slice(0, 8)} · id:${s.id.slice(0, 8)}`
    const text = `${dateStr} ${timeStr}  [${ev.level}]  ${ev.message}  [${meta}]`
    await navigator.clipboard.writeText(text).catch(() => {})
    setCopiedLogIdx(idx)
    setTimeout(() => setCopiedLogIdx(null), 1400)
  }

  // ── card border/bg — colour matches strategy status ──────────────────────────
  const isActive = s.status === 'active'
  const baseBg = isActive && isLong  ? 'rgba(65,210,139,.055)'
               : isActive && !isLong ? 'rgba(248,113,113,.055)'
               :                       'rgba(255,255,255,.025)'
  const baseBorder = isActive && isLong  ? 'rgba(65,210,139,.18)'
                   : isActive && !isLong ? 'rgba(248,113,113,.18)'
                   :                       'rgba(255,255,255,.07)'
  const cardStyle: React.CSSProperties = {
    background: selected ? 'rgba(255,255,255,.07)' : baseBg,
    border: `1px solid ${selected ? 'rgba(255,255,255,.08)' : baseBorder}`,
    ...(selected ? { boxShadow: '0 0 0 1.5px rgba(91,140,255,.45)' } : {}),
  }

  const firstSignal = s.signal_configs?.[0]

  // ────────────────────────────────────────────────────────────────────────────
  return (
    <li className="rounded-xl overflow-visible transition-all font-sans" style={cardStyle}>

      {/* ── collapsed header ── */}
      <div
        className="flex flex-col gap-2 px-3 py-2.5 cursor-pointer"
        onClick={handleHeaderClick}
      >
        {/* row 1: sym · side tag · account · gear · status */}
        <div className="flex items-center gap-2 flex-wrap">
          {/* left: symbol + badges */}
          <div className="min-w-0 flex-1 flex items-center gap-2 flex-wrap">
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
            <span className="inline-flex items-center gap-1.5 text-[11px] text-slate-400 leading-none whitespace-nowrap">
              <span className="w-3.5 h-3.5 rounded-[3px] bg-[linear-gradient(135deg,#f7a600,#e88f00)] text-[#1a1100] font-extrabold text-[8px] flex items-center justify-center shrink-0">B</span>
              {accountName}
            </span>
          </div>

          {/* right: status · gear */}
          <StatusPicker value={s.status} acting={cs.acting} onChange={handleStatus} onInteract={() => onSelect?.(s)} />
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
        <div className="flex items-center gap-3.5 flex-wrap">
          <div className="relative group/ctip flex items-baseline gap-1.5">
            <span className="text-[10px] text-[#5b6479] uppercase tracking-[.8px] font-semibold">Grid</span>
            <span className={`font-mono text-[12px] font-semibold ${s.active_levels > 0 ? 'text-[#e6ebf5]' : 'text-[#5b6479]'}`}>
              {s.active_levels}/{s.grid_levels}
            </span>
            <CardTip text="Исполненных уровней сетки / всего уровней в текущем цикле" />
          </div>
          <div className="w-px h-2.5 bg-white/[.08]" />
          <div className="relative group/ctip flex items-baseline gap-1.5">
            <span className="text-[10px] text-[#5b6479] uppercase tracking-[.8px] font-semibold">Объём</span>
            <span className={`font-mono text-[12px] font-semibold ${s.volume_usdt > 0 ? 'text-[#e6ebf5]' : 'text-[#5b6479]'}`}>
              {s.volume_usdt > 0 ? s.volume_usdt.toFixed(0) : '0'}
              <span className="text-[10px] text-[#5b6479] ml-0.5">USDT</span>
            </span>
            <CardTip text="Суммарный объём открытой позиции — сумма всех взятых уровней в USDT" />
          </div>
          <div className="w-px h-2.5 bg-white/[.08]" />
          <div className="relative group/ctip flex items-baseline gap-1.5">
            <span className="text-[10px] text-[#5b6479] uppercase tracking-[.8px] font-semibold">P&L</span>
            <span className={`font-mono text-[12px] font-semibold ${
              pnlUsdt === null ? 'text-[#5b6479]' : pnlUsdt > 0 ? 'text-emerald-300' : pnlUsdt < 0 ? 'text-rose-300' : 'text-[#5b6479]'
            }`}>
              {pnlUsdt === null
                ? '—'
                : `${pnlUsdt >= 0 ? '+' : ''}${pnlUsdt.toFixed(2)}$ (${pnlPct !== null ? (pnlPct >= 0 ? '+' : '') + pnlPct.toFixed(1) + '%' : '—'})`
              }
            </span>
            <CardTip text="Нереализованный P&L по открытой позиции относительно вложенного капитала" />
          </div>
          <button type="button" onClick={e => { e.stopPropagation(); onToggleOpen?.() }} className="ml-auto w-7 h-7 inline-flex items-center justify-center text-slate-400 rounded-[7px] bg-white/[.04] border border-white/[.08] hover:bg-white/[.08] transition-colors shrink-0">
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
                {/* signal card */}
                {firstSignal && (
                  <div className="bg-[linear-gradient(180deg,rgba(91,140,255,.10),rgba(123,91,255,.06))] border border-[rgba(91,140,255,.25)] rounded-[10px] px-3 py-2.5">
                    <div className="flex items-center gap-2 mb-1.5">
                      <IcSignal s={14} c="#b8c8ff" />
                      <span className="text-[11px] text-[#b8c8ff] font-bold uppercase tracking-[1px]">Сигнал входа</span>
                      <span className={`ml-auto inline-flex items-center gap-1 px-1.5 py-0.5 rounded-full text-[9px] font-bold uppercase tracking-[.4px] border ${
                        isRunning
                          ? 'bg-emerald-400/[.14] border-emerald-400/28 text-emerald-300'
                          : 'bg-white/[.06] border-white/10 text-slate-400'
                      }`}>
                        <span className={`w-1 h-1 rounded-full ${isRunning ? 'bg-emerald-400 animate-pulse' : 'bg-slate-400'}`} />
                        {isRunning ? 'сработал' : 'неактивен'}
                      </span>
                    </div>
                    <div className="flex items-baseline gap-2 flex-wrap">
                      <span className="font-display text-[16px] font-bold text-white tracking-[-0.3px]">{signalLabel(firstSignal)}</span>
                      {s.signal_configs.slice(1).map(sc => (
                        <span key={sc.name} className="font-mono text-[13px] text-[#cfd5e1] font-semibold">{signalLabel(sc)}</span>
                      ))}
                    </div>
                    <div className="flex items-center gap-2.5 mt-2 text-[10px] text-slate-500 font-mono flex-wrap">
                      <span className="inline-flex items-center gap-1">
                        <span className="w-1.5 h-1.5 rounded-full bg-[#5b8cff]" />{s.symbol}
                      </span>
                      <span>{s.direction === 'long' ? 'Long grid' : 'Short grid'}</span>
                      <span>{s.grid_levels} уровней</span>
                    </div>
                  </div>
                )}

                {/* two-col: levels + TP/SL */}
                <div className="grid grid-cols-2 gap-2">
                  {/* levels */}
                  <div className="bg-black/[.18] border border-white/[.05] rounded-[10px] p-3">
                    <div className="flex items-center justify-between mb-2">
                      <span className="text-[10px] text-slate-400 uppercase tracking-[1.3px] font-semibold">
                        Уровни · {cs.state?.levels.filter(l => l.status === 'filled').length ?? 0}/{s.grid_levels}
                      </span>
                      <span className="text-[10px] text-[#5b6479] font-mono font-medium">L1–L{s.grid_levels}</span>
                    </div>
                    <div
                      ref={levelsScrollRef}
                      className="max-h-[180px] overflow-y-auto flex flex-col gap-px"
                      style={{ scrollbarWidth: 'thin', scrollbarColor: 'rgba(255,255,255,.14) transparent' }}
                    >
                      {cs.state && cs.state.levels.length > 0 ? (
                        cs.state.levels.map(l => {
                          const tag = lvlTag(l.status)
                          const isFilled = l.status === 'filled'
                          const usdtVal = isFilled && l.filled_price > 0
                            ? Math.round(l.target_price > 0 ? (l.filled_price / l.target_price) * l.size_usdt : l.size_usdt)
                            : Math.round(l.size_usdt)
                          const priceVal = isFilled && l.filled_price > 0 ? l.filled_price : l.target_price
                          return (
                            <div key={l.level_idx} data-lvl-status={l.status}
                              className="grid gap-2 items-center px-1 py-[5px] rounded-[5px] font-mono text-[11px]"
                              style={{ gridTemplateColumns: 'auto 1fr auto' }}
                            >
                              <span className="text-[10px] text-[#5b6479] w-[18px]">L{l.level_idx}</span>
                              <span>
                                <span className="text-[#e6ebf5] font-bold">{usdtVal}$</span>
                                {priceVal > 0 && <span className="text-[#5b6479] font-normal"> ({priceVal.toFixed(2)})</span>}
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
                    <div className="relative group/ctip bg-black/[.18] border border-white/[.06] rounded-[10px] p-2.5 flex flex-col gap-1.5">
                      <div className="flex items-center justify-between">
                        <span className="flex items-center gap-1.5 text-[12px] font-semibold text-rose-300">
                          <IcBolt s={13} c="#fca5a5" />Stop Loss
                        </span>
                        <span className="text-[9px] text-slate-500 uppercase tracking-[.6px] font-semibold">{s.sl_type === 'conditional' ? 'cond.' : 'prog.'}</span>
                      </div>
                      <div className="font-display text-[18px] font-bold tracking-[-0.4px] text-rose-300">
                        −{s.sl_pct}%
                      </div>
                      <div className="text-[11px] text-slate-500">
                        {slPrice() ? slPrice()!.toFixed(2) + ' от start' : 'от start price'}
                      </div>
                      <CardTip text={`Принудительное закрытие при убытке. Тип: ${s.sl_type === 'conditional' ? 'биржевой (срабатывает даже если сервер недоступен)' : 'программный (контролируется сервисом через WS)'}.`} />
                    </div>
                  </div>
                </div>

                {/* log card — always visible, collapsed by default */}
                <div className="bg-black/[.18] border border-white/[.05] rounded-[10px] overflow-hidden">
                  <button type="button" onClick={() => setLogOpen(o => !o)}
                    className="w-full flex items-center gap-2 px-3 py-2 border-b border-white/[.04] hover:bg-white/[.03] transition-colors">
                    <IcLog s={12} c="#7b8aa6" />
                    <span className="text-[10px] text-slate-400 uppercase tracking-[1.3px] font-semibold">Лог событий</span>
                    {displayEvents.length > 0 && (
                      <span className="text-[9px] px-1.5 py-0.5 bg-white/[.06] rounded-[3px] text-[#cfd5e1] font-mono font-semibold ml-0.5">{displayEvents.length}</span>
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
                    <div className="flex flex-col max-h-[160px] overflow-y-auto"
                      style={{ scrollbarWidth: 'thin', scrollbarColor: 'rgba(255,255,255,.14) transparent' }}>
                      {displayEvents.length === 0 ? (
                        <div className="py-3 text-center text-[11px] text-slate-600">Событий нет</div>
                      ) : displayEvents.slice(0, 30).map((e, i) => {
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
                    </div>
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

      {debugOpen && (
        <DebugCycleWindow strategy={s} orders={strategyOrders} onClose={() => setDebugOpen(false)} />
      )}
    </li>
  )
}
