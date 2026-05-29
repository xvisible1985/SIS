import { useState, useEffect, useMemo, type CSSProperties } from 'react'
import { useNavigate } from 'react-router-dom'
import { getNovabotBalance, type NovabotBalance, type NovabotTransaction } from '../api/account'

/* ─── tokens ─────────────────────────────────────────────────────────────── */
const C = {
  green:    '#5be0a0', greenDeep: '#41d28b',
  greenBg:  'rgba(65,210,139,.10)', greenBd: 'rgba(65,210,139,.26)',
  greenGrad:'linear-gradient(180deg,#3fce8b,#2fb778)',
  blue:     '#7ba6ff', blueDeep: '#4a7dff',
  blueBg:   'rgba(91,140,255,.10)', blueBd: 'rgba(91,140,255,.28)',
  blueGrad: 'linear-gradient(180deg,#4a7dff,#3a67e6)',
  amber:    '#f7a600', red: '#fca5a5',
  ink:      '#f2f5fb', sub: '#9aa6c8', dim: '#7b8aa6', faint: '#5b6479',
  panel:    '#0c1018', border: 'rgba(255,255,255,.06)',
}
const mono: CSSProperties  = { fontFamily: "'JetBrains Mono', monospace" }
const grotesk: CSSProperties = { fontFamily: "'Space Grotesk', sans-serif" }

const fmt = (n: number) => n.toLocaleString('ru-RU', { minimumFractionDigits: 2, maximumFractionDigits: 2 })

/* ─── icons ──────────────────────────────────────────────────────────────── */
type IP = { s?: number; w?: number; c?: string }
const svg = (p: IP, children: React.ReactNode) => (
  <svg width={p.s ?? 16} height={p.s ?? 16} viewBox="0 0 24 24" fill="none"
    stroke={p.c ?? 'currentColor'} strokeWidth={p.w ?? 1.8}
    strokeLinecap="round" strokeLinejoin="round" style={{ display: 'block', flexShrink: 0 }}>
    {children}
  </svg>
)
const IcWallet  = (p: IP) => svg(p, <><rect x="3" y="6" width="18" height="14" rx="2.5"/><path d="M3 10h18"/><circle cx="17" cy="15" r="1.2" fill="currentColor"/></>)
const IcGift    = (p: IP) => svg(p, <><rect x="4" y="8" width="16" height="13" rx="2"/><path d="M4 12h16M12 8v13M9 8c-2 0-3-1-3-2.5S7 3 8.5 3 12 5 12 8M15 8c2 0 3-1 3-2.5S17 3 15.5 3 12 5 12 8"/></>)
const IcLayers  = (p: IP) => svg(p, <><path d="M12 3l9 5-9 5-9-5 9-5z"/><path d="M3 13l9 5 9-5"/></>)
const IcPlus    = (p: IP) => svg(p, <path d="M12 5v14M5 12h14"/>)
const IcMinus   = (p: IP) => svg(p, <path d="M5 12h14"/>)
const IcArrDn   = (p: IP) => svg(p, <><path d="M19 12l-7 7-7-7M12 19V4"/></>)
const IcArrUp   = (p: IP) => svg(p, <><path d="M5 12l7-7 7 7M12 5v15"/></>)
const IcShield  = (p: IP) => svg(p, <path d="M12 3l8 3v6c0 5-3.5 8.5-8 9-4.5-.5-8-4-8-9V6l8-3z"/>)
const IcLock    = (p: IP) => svg(p, <><rect x="4" y="11" width="16" height="9" rx="2"/><path d="M8 11V8a4 4 0 1 1 8 0v3"/></>)
const IcInfo    = (p: IP) => svg(p, <><circle cx="12" cy="12" r="9"/><path d="M12 11v5M12 8h.01"/></>)
const IcDot     = (p: IP) => svg({ ...p, w: 0 }, <circle cx="12" cy="12" r="4" fill={p.c ?? 'currentColor'}/>)
const IcRefresh = (p: IP) => svg(p, <><path d="M3 12a9 9 0 1 0 3-6.7"/><path d="M3 4v5h5"/></>)
const IcUserCog = (p: IP) => svg(p, <><circle cx="9" cy="8" r="3.4"/><path d="M3.5 20a5.5 5.5 0 0 1 9.2-3.6"/><circle cx="18" cy="17" r="2.4"/><path d="M18 13.6v-1M18 21.4v-1M21 17h-1M16 17h-1"/></>)

/* ─── Ring chart ─────────────────────────────────────────────────────────── */
function Ring({ realPct }: { realPct: number }) {
  const R = 38, CIRC = 2 * Math.PI * R, gap = 6
  const realLen = Math.max(0, CIRC * realPct / 100 - gap)
  const virtLen = Math.max(0, CIRC * (100 - realPct) / 100 - gap)
  return (
    <svg width="104" height="104" viewBox="0 0 104 104" style={{ flexShrink: 0, transform: 'rotate(-90deg)' }}>
      <circle cx="52" cy="52" r={R} fill="none" stroke="rgba(255,255,255,.05)" strokeWidth="11"/>
      <circle cx="52" cy="52" r={R} fill="none" stroke={C.green} strokeWidth="11" strokeLinecap="round"
        strokeDasharray={`${realLen} ${CIRC - realLen}`} strokeDashoffset="0"
        style={{ filter: 'drop-shadow(0 0 4px rgba(65,210,139,.5))' }}/>
      <circle cx="52" cy="52" r={R} fill="none" stroke={C.blueDeep} strokeWidth="11" strokeLinecap="round"
        strokeDasharray={`${virtLen} ${CIRC - virtLen}`} strokeDashoffset={`${-(realLen + gap)}`}
        style={{ filter: 'drop-shadow(0 0 4px rgba(74,125,255,.5))' }}/>
    </svg>
  )
}

/* ─── Hero card ──────────────────────────────────────────────────────────── */
function Hero({ data, view }: { data: NovabotBalance; view: 'bar' | 'ring' }) {
  const realPct = data.total > 0 ? (data.real / data.total) * 100 : 0
  const virtPct = 100 - realPct

  const Legend = (
    <div style={view === 'ring'
      ? { display: 'flex', flexDirection: 'column', gap: 12, flex: 1 }
      : { display: 'flex', gap: 0, justifyContent: 'space-between' }
    }>
      {[
        { label: 'Реальный',    val: data.real,    pct: realPct, color: C.green,    dot: C.green },
        { label: 'Виртуальный', val: data.virtual, pct: virtPct, color: C.blue,     dot: C.blueDeep },
      ].map(({ label, val, pct, color, dot }) => (
        <div key={label} style={{ display: 'flex', flexDirection: 'column', gap: 4, flex: 1 }}>
          <div style={{ display: 'flex', alignItems: 'center', gap: 7 }}>
            <span style={{ width: 9, height: 9, borderRadius: 3, background: dot, flexShrink: 0 }}/>
            <span style={{ fontSize: 11.5, color: C.sub, fontWeight: 600 }}>{label}</span>
          </div>
          <div style={{ ...mono, fontSize: 15, fontWeight: 700, color, paddingLeft: 16 }}>{fmt(val)}</div>
          <div style={{ ...mono, fontSize: 10.5, color: C.faint, paddingLeft: 16, marginTop: -2 }}>
            {pct.toFixed(1)}% · USDT
          </div>
        </div>
      ))}
    </div>
  )

  return (
    <div style={{
      background: 'linear-gradient(150deg, rgba(91,140,255,.10), rgba(65,210,139,.06) 70%, rgba(0,0,0,0))',
      border: '1px solid rgba(91,140,255,.18)', borderRadius: 18,
      padding: '22px 20px', display: 'flex', flexDirection: 'column', gap: 18,
      position: 'relative', overflow: 'hidden',
    }}>
      {/* glows */}
      <div style={{ position: 'absolute', top: -110, right: -70, width: 240, height: 240, borderRadius: '50%', background: 'radial-gradient(circle, rgba(91,140,255,.28), transparent 62%)', filter: 'blur(46px)', pointerEvents: 'none' }}/>
      <div style={{ position: 'absolute', bottom: -120, left: -70, width: 220, height: 220, borderRadius: '50%', background: 'radial-gradient(circle, rgba(65,210,139,.20), transparent 62%)', filter: 'blur(46px)', pointerEvents: 'none' }}/>

      {/* label */}
      <div style={{ display: 'flex', alignItems: 'center', gap: 9, position: 'relative', zIndex: 1 }}>
        <div style={{ width: 26, height: 26, borderRadius: 7, background: 'rgba(255,255,255,.06)', border: '1px solid rgba(255,255,255,.10)', display: 'flex', alignItems: 'center', justifyContent: 'center', color: C.sub }}>
          <IcLayers s={15} w={1.9}/>
        </div>
        <span style={{ fontSize: 10.5, color: C.sub, textTransform: 'uppercase', letterSpacing: 1.4, fontWeight: 700 }}>Общий баланс</span>
      </div>

      {/* total value */}
      <div style={{ position: 'relative', zIndex: 1 }}>
        <div style={{ ...grotesk, fontSize: 48, fontWeight: 800, color: C.ink, letterSpacing: -1.6, lineHeight: 0.95, display: 'flex', alignItems: 'baseline', gap: 8 }}>
          {fmt(data.total)}
          <span style={{ fontSize: 18, fontWeight: 700, color: C.sub, letterSpacing: 0 }}>USDT</span>
        </div>
      </div>

      {/* breakdown */}
      <div style={{ position: 'relative', zIndex: 1 }}>
        {view === 'ring' ? (
          <div style={{ display: 'flex', alignItems: 'center', gap: 20 }}>
            <Ring realPct={realPct}/>
            {Legend}
          </div>
        ) : (
          <div style={{ display: 'flex', flexDirection: 'column', gap: 12 }}>
            <div style={{ display: 'flex', height: 12, borderRadius: 99, overflow: 'hidden', background: 'rgba(0,0,0,.35)', border: '1px solid rgba(255,255,255,.06)' }}>
              <div style={{ height: '100%', width: `${realPct}%`, background: C.greenGrad }}/>
              <div style={{ height: '100%', width: `${virtPct}%`, background: C.blueGrad }}/>
            </div>
            {Legend}
          </div>
        )}
      </div>
    </div>
  )
}

/* ─── Balance card ───────────────────────────────────────────────────────── */
function BalanceCard({ data, kind, onTopUp, onWithdraw }: {
  data: NovabotBalance
  kind: 'real' | 'virtual'
  onTopUp?: () => void
  onWithdraw?: () => void
}) {
  const green = kind === 'real'
  const accent = green ? C.green : C.blue
  const bg = green ? C.greenBg : C.blueBg
  const bd = green ? C.greenBd : C.blueBd
  const grad = green ? C.greenGrad : C.blueGrad
  const val = green ? data.real : data.virtual
  return (
    <div style={{
      background: `linear-gradient(165deg, ${bg}, rgba(12,16,24,.6) 75%)`,
      border: `1px solid ${bd}`, borderRadius: 16,
      padding: '17px 17px 18px', display: 'flex', flexDirection: 'column', gap: 12,
      position: 'relative', overflow: 'hidden',
    }}>
      <div style={{ position: 'absolute', top: -50, right: -40, width: 130, height: 130, borderRadius: '50%', background: `radial-gradient(circle, ${green ? 'rgba(65,210,139,.18)' : 'rgba(91,140,255,.20)'}, transparent 60%)`, filter: 'blur(28px)', pointerEvents: 'none' }}/>

      {/* header */}
      <div style={{ display: 'flex', alignItems: 'center', gap: 10 }}>
        <div style={{ width: 34, height: 34, borderRadius: 9, background: bg, border: `1px solid ${bd}`, color: accent, display: 'flex', alignItems: 'center', justifyContent: 'center', flexShrink: 0 }}>
          {green ? <IcWallet s={17} w={1.9}/> : <IcGift s={17} w={1.9}/>}
        </div>
        <div style={{ flex: 1, minWidth: 0 }}>
          <div style={{ fontSize: 10, fontWeight: 700, textTransform: 'uppercase', letterSpacing: 1, color: accent }}>{green ? 'Реальный' : 'Виртуальный'}</div>
          <div style={{ ...grotesk, fontSize: 14.5, fontWeight: 700, color: '#f2f5fb', letterSpacing: -0.2 }}>{green ? 'Средства на счёте' : 'Бонусный баланс'}</div>
        </div>
        <span style={{ display: 'inline-flex', alignItems: 'center', gap: 5, padding: '3px 8px', borderRadius: 99, background: bg, border: `1px solid ${bd}`, fontSize: 10, fontWeight: 700, color: accent, letterSpacing: 0.3 }}>
          {green ? <><IcShield s={10} w={2.2}/>выводится</> : <><IcLock s={10} w={2.2}/>бонус</>}
        </span>
      </div>

      {/* value */}
      <div style={{ ...mono, fontSize: 28, fontWeight: 700, color: C.ink, letterSpacing: -0.5, lineHeight: 1, display: 'flex', alignItems: 'baseline', gap: 6 }}>
        {fmt(val)}
        <span style={{ ...grotesk, fontSize: 12.5, fontWeight: 600, color: C.sub }}>USDT</span>
      </div>

      {/* note */}
      <div style={{ fontSize: 11.5, color: C.dim, lineHeight: 1.5, display: 'flex', gap: 6, alignItems: 'flex-start' }}>
        <IcInfo s={13} w={1.9} c={C.faint}/>
        <span>{green
          ? 'Реальные USDT, поступившие на счёт. Доступны для торговли и вывода.'
          : 'Начислен администратором. Участвует в торговле, но не выводится.'
        }</span>
      </div>

      {/* actions */}
      <div style={{ display: 'flex', gap: 8, marginTop: 2 }}>
        {green ? (
          <>
            <button onClick={onTopUp} style={{ ...btnBase, background: grad, color: '#04130c', boxShadow: 'inset 0 1px 0 rgba(255,255,255,.28), 0 6px 14px -8px rgba(65,210,139,.7)' }}>
              <IcPlus s={13} w={2.6}/> Пополнить
            </button>
            <button onClick={onWithdraw} style={{ ...btnBase, ...btnGhost }}>
              <IcMinus s={13} w={2.6}/> Вывести
            </button>
          </>
        ) : (
          <button style={{ ...btnBase, ...btnGhost }}>
            <IcUserCog s={13} w={1.9}/> Запросить начисление
          </button>
        )}
      </div>
    </div>
  )
}

const btnBase: CSSProperties = {
  display: 'inline-flex', alignItems: 'center', justifyContent: 'center', gap: 6,
  padding: '9px 13px', borderRadius: 9, fontSize: 12.5, fontWeight: 700, flex: 1,
  border: 0, cursor: 'pointer', fontFamily: 'inherit',
}
const btnGhost: CSSProperties = {
  background: 'rgba(255,255,255,.05)', border: '1px solid rgba(255,255,255,.10)', color: '#cfd5e1',
}

/* ─── History ─────────────────────────────────────────────────────────────── */
const BUCKET_META = {
  real:    { dot: C.green,    bg: C.greenBg, bd: C.greenBd, label: 'реальный',    color: C.green },
  virtual: { dot: C.blueDeep, bg: C.blueBg,  bd: C.blueBd,  label: 'виртуальный', color: C.blue  },
}

function fmtDate(iso: string): string {
  const d = new Date(iso)
  const pad = (n: number) => String(n).padStart(2, '0')
  return `${pad(d.getDate())}.${pad(d.getMonth() + 1)}.${String(d.getFullYear()).slice(2)} ${pad(d.getHours())}:${pad(d.getMinutes())}`
}

function HistoryRow({ tx, last }: { tx: NovabotTransaction; last: boolean }) {
  const b = BUCKET_META[tx.bucket] ?? BUCKET_META.virtual
  const pos = tx.amount >= 0
  const Icon = tx.bucket === 'virtual' ? IcGift : (pos ? IcArrDn : IcArrUp)
  return (
    <div style={{
      display: 'grid', gridTemplateColumns: '36px 1fr auto', gap: 12, alignItems: 'center',
      padding: '12px 0', borderBottom: last ? 'none' : '1px solid rgba(255,255,255,.04)',
    }}>
      <div style={{ width: 36, height: 36, borderRadius: 10, background: b.bg, border: `1px solid ${b.bd}`, color: b.color, display: 'flex', alignItems: 'center', justifyContent: 'center', flexShrink: 0 }}>
        <Icon s={16} w={2}/>
      </div>
      <div style={{ display: 'flex', flexDirection: 'column', gap: 3, minWidth: 0 }}>
        <div style={{ fontSize: 13.5, color: '#e6ebf5', fontWeight: 600, lineHeight: 1.25, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
          {tx.note || (tx.bucket === 'virtual' ? 'Начисление' : pos ? 'Пополнение' : 'Вывод')}
        </div>
        <div style={{ display: 'flex', alignItems: 'center', gap: 7, fontSize: 11, color: C.dim }}>
          <span style={mono}>{fmtDate(tx.created_at)}</span>
          <span style={{ width: 3, height: 3, borderRadius: '50%', background: 'rgba(255,255,255,.18)' }}/>
          <span style={{ display: 'inline-flex', alignItems: 'center', gap: 4, fontSize: 9.5, fontWeight: 700, letterSpacing: 0.3, padding: '1px 6px', borderRadius: 5, background: b.bg, border: `1px solid ${b.bd}`, color: b.color }}>
            <span style={{ width: 5, height: 5, borderRadius: 2, background: b.dot }}/>
            {b.label}
          </span>
        </div>
      </div>
      <div style={{ display: 'flex', flexDirection: 'column', alignItems: 'flex-end', gap: 3 }}>
        <div style={{ ...mono, fontSize: 14, fontWeight: 700, color: pos ? b.color : '#e6ebf5' }}>
          {pos ? '+' : ''}{fmt(tx.amount)}
          <span style={{ color: C.faint, fontSize: 10, marginLeft: 3 }}>USDT</span>
        </div>
      </div>
    </div>
  )
}

function History({ history }: { history: NovabotTransaction[] }) {
  const [filter, setFilter] = useState<'all' | 'real' | 'virtual'>('all')
  const rows = filter === 'all' ? history : history.filter(h => h.bucket === filter)

  const chips: { id: typeof filter; label: string; dot: string | null }[] = [
    { id: 'all',     label: 'Все',          dot: null },
    { id: 'real',    label: 'Реальный',     dot: C.green },
    { id: 'virtual', label: 'Виртуальный',  dot: C.blueDeep },
  ]

  return (
    <div style={{ background: C.panel, border: `1px solid ${C.border}`, borderRadius: 14, overflow: 'hidden' }}>
      {/* head */}
      <div style={{ display: 'flex', alignItems: 'center', gap: 10, padding: '14px 20px 12px', borderBottom: '1px solid rgba(255,255,255,.05)' }}>
        <div style={{ width: 28, height: 28, borderRadius: 7, background: 'rgba(255,255,255,.05)', border: '1px solid rgba(255,255,255,.08)', color: C.sub, display: 'flex', alignItems: 'center', justifyContent: 'center' }}>
          <IcWallet s={15} w={1.9}/>
        </div>
        <div style={{ ...grotesk, fontSize: 14, fontWeight: 700, color: '#f2f5fb', letterSpacing: -0.2 }}>История начислений</div>
      </div>

      {/* filter chips */}
      <div style={{ display: 'flex', gap: 6, padding: '14px 20px 4px', flexWrap: 'wrap' }}>
        {chips.map(c => {
          const on = filter === c.id
          const isReal = c.id === 'real', isVirt = c.id === 'virtual'
          return (
            <div key={c.id} onClick={() => setFilter(c.id)} style={{
              padding: '5px 11px', borderRadius: 99, fontSize: 11.5, fontWeight: 600, cursor: 'pointer',
              display: 'inline-flex', alignItems: 'center', gap: 6,
              background: on ? (isReal ? C.greenBg : isVirt ? C.blueBg : 'rgba(255,255,255,.10)') : 'rgba(255,255,255,.04)',
              border: `1px solid ${on ? (isReal ? C.greenBd : isVirt ? C.blueBd : 'rgba(255,255,255,.16)') : 'rgba(255,255,255,.07)'}`,
              color: on ? (isReal ? C.green : isVirt ? C.blue : '#fff') : C.dim,
            }}>
              {c.dot && <span style={{ width: 7, height: 7, borderRadius: 3, background: c.dot, flexShrink: 0 }}/>}
              {c.label}
            </div>
          )
        })}
      </div>

      {/* rows */}
      <div style={{ padding: '8px 20px 12px' }}>
        {rows.length === 0 ? (
          <div style={{ padding: '28px 0', textAlign: 'center', fontSize: 12.5, color: C.faint }}>
            Нет операций по фильтру
          </div>
        ) : (
          rows.map((h, i) => <HistoryRow key={h.id} tx={h} last={i === rows.length - 1}/>)
        )}
      </div>
    </div>
  )
}

/* ─── Page ───────────────────────────────────────────────────────────────── */
export function BalancesPage() {
  const navigate = useNavigate()
  const [data, setData] = useState<NovabotBalance | null>(null)
  const [loading, setLoading] = useState(true)
  const [view, setView] = useState<'bar' | 'ring'>('bar')
  const [withdrawOpen, setWithdrawOpen] = useState(false)

  useEffect(() => {
    getNovabotBalance()
      .then(setData)
      .catch(() => {})
      .finally(() => setLoading(false))
  }, [])

  const empty: NovabotBalance = useMemo(() => ({ total: 0, real: 0, virtual: 0, history: [] }), [])
  const d = data ?? empty

  return (
    <div style={{ color: '#dde3ef', fontFamily: "'Inter', system-ui, sans-serif", maxWidth: 600 }}>
      {/* Модалка вывода */}
      {withdrawOpen && (
        <div onClick={() => setWithdrawOpen(false)} style={{
          position: 'fixed', inset: 0, background: 'rgba(0,0,0,.6)', backdropFilter: 'blur(4px)',
          display: 'flex', alignItems: 'center', justifyContent: 'center', zIndex: 1000,
        }}>
          <div onClick={e => e.stopPropagation()} style={{
            background: '#0c1018', border: '1px solid rgba(255,255,255,.10)',
            borderRadius: 18, padding: '28px 26px', maxWidth: 380, width: '90%',
            display: 'flex', flexDirection: 'column', gap: 14,
          }}>
            <div style={{ fontSize: 18, fontWeight: 700, color: '#f2f5fb' }}>💸 Вывод средств</div>
            <div style={{ fontSize: 13.5, color: '#9aa6c8', lineHeight: 1.6 }}>
              Вывод USDT пока в разработке. Для вывода средств обратитесь к администратору через Telegram-бот или напишите на почту поддержки.
            </div>
            <button onClick={() => setWithdrawOpen(false)} style={{
              marginTop: 4, padding: '10px 0', borderRadius: 10, border: 0,
              background: 'rgba(255,255,255,.08)', color: '#cfd5e1',
              fontSize: 13, fontWeight: 600, cursor: 'pointer',
            }}>Закрыть</button>
          </div>
        </div>
      )}
      {/* page head */}
      <div style={{ display: 'flex', alignItems: 'flex-end', justifyContent: 'space-between', marginBottom: 22, padding: '2px 4px 0' }}>
        <div>
          <h1 style={{ ...grotesk, fontSize: 24, fontWeight: 800, color: C.ink, letterSpacing: -0.6, margin: 0 }}>Балансы</h1>
          <div style={{ fontSize: 12.5, color: C.dim, marginTop: 4 }}>Реальные средства, бонусы и история начислений</div>
        </div>
        <div style={{ display: 'flex', alignItems: 'center', gap: 4, padding: '3px 4px', background: 'rgba(255,255,255,.04)', border: '1px solid rgba(255,255,255,.07)', borderRadius: 9 }}>
          {(['bar', 'ring'] as const).map(v => (
            <button key={v} onClick={() => setView(v)} style={{
              padding: '5px 10px', borderRadius: 7, border: 0, cursor: 'pointer', fontFamily: 'inherit',
              fontSize: 11.5, fontWeight: 600,
              background: view === v ? 'rgba(255,255,255,.10)' : 'transparent',
              color: view === v ? '#fff' : C.dim,
            }}>
              {v === 'bar' ? 'Полоса' : 'Кольцо'}
            </button>
          ))}
          <span style={{ ...mono, fontSize: 11, color: C.sub, padding: '5px 10px', background: 'rgba(255,255,255,.04)', border: '1px solid rgba(255,255,255,.08)', borderRadius: 99, display: 'inline-flex', alignItems: 'center', gap: 6, marginLeft: 4 }}>
            <IcDot s={7} c={C.green}/> NovaBot
          </span>
        </div>
      </div>

      {loading ? (
        <div style={{ padding: '60px 0', textAlign: 'center', color: C.dim, fontSize: 13 }}>
          <span style={{ display: 'inline-flex', animation: 'spin 1s linear infinite', marginRight: 8 }}>
            <IcRefresh s={16} c={C.dim}/>
          </span>
          Загрузка…
        </div>
      ) : (
        <div style={{ display: 'flex', flexDirection: 'column', gap: 14 }}>
          <Hero data={d} view={view}/>

          <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 14 }}>
            <BalanceCard data={d} kind="real" onTopUp={() => navigate('/payments')} onWithdraw={() => setWithdrawOpen(true)}/>
            <BalanceCard data={d} kind="virtual"/>
          </div>

          <History history={d.history}/>
        </div>
      )}

      <style>{`
        @keyframes spin { to { transform: rotate(360deg) } }
      `}</style>
    </div>
  )
}
