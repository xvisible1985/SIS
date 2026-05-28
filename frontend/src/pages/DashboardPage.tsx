import { useState, useEffect, useMemo, useCallback, type CSSProperties } from 'react'
import { getDashboard, type DashboardData, type DailyPnL } from '../api/dashboard'
import { getAccountBalance, getAccountPositions, listAccounts } from '../api/accounts'
import { useSelectedAccount } from '../contexts/AccountContext'
import type { Position, ExchangeAccount } from '../types'

// ─── Colour tokens ────────────────────────────────────────────────────────────
const T = {
  panel: '#0c1018',
  border: 'rgba(255,255,255,.06)',
  borderHi: 'rgba(255,255,255,.10)',
  text: '#f2f5fb',
  body: '#dde3ef',
  dim: '#7b8aa6',
  faint: '#5b6479',
  blue: '#5b8cff',
  green: '#5be0a0',
  greenSoft: 'rgba(65,210,139,.14)',
  greenBd: 'rgba(65,210,139,.28)',
  orange: '#f7a600',
  red: '#fca5a5',
  redSoft: 'rgba(248,113,113,.14)',
  redBd: 'rgba(248,113,113,.30)',
}

const mono: CSSProperties = { fontFamily: "'JetBrains Mono', monospace" }
const grotesk: CSSProperties = { fontFamily: "'Space Grotesk', sans-serif" }

// ─── Helpers ──────────────────────────────────────────────────────────────────
function fmt$(n: number | null | undefined, d = 2): string {
  if (n == null) return '—'
  return (n < 0 ? '-' : '') + '$' + Math.abs(n).toLocaleString('en-US', {
    minimumFractionDigits: d, maximumFractionDigits: d,
  })
}
function fmtPct(n: number, d = 1): string {
  return (n >= 0 ? '+' : '') + n.toFixed(d) + '%'
}
function fmtPrice(v: number): string {
  return v < 10 ? v.toFixed(4) : v < 1000 ? v.toFixed(2) : v.toLocaleString('en-US')
}

// ─── Catmull-Rom → cubic Bezier ───────────────────────────────────────────────
function smoothPath(pts: [number, number][]): string {
  if (pts.length < 2) return ''
  let d = `M${pts[0][0].toFixed(2)},${pts[0][1].toFixed(2)}`
  for (let i = 0; i < pts.length - 1; i++) {
    const p0 = pts[i - 1] ?? pts[i]
    const p1 = pts[i]
    const p2 = pts[i + 1]
    const p3 = pts[i + 2] ?? p2
    const c1x = p1[0] + (p2[0] - p0[0]) / 6
    const c1y = p1[1] + (p2[1] - p0[1]) / 6
    const c2x = p2[0] - (p3[0] - p1[0]) / 6
    const c2y = p2[1] - (p3[1] - p1[1]) / 6
    d += ` C${c1x.toFixed(2)},${c1y.toFixed(2)} ${c2x.toFixed(2)},${c2y.toFixed(2)} ${p2[0].toFixed(2)},${p2[1].toFixed(2)}`
  }
  return d
}

// ─── Chart: Sparkline ─────────────────────────────────────────────────────────
function Sparkline({ data, width = 80, height = 24, color = T.green, strokeW = 1.4 }: {
  data: number[]; width?: number; height?: number; color?: string; strokeW?: number
}) {
  const id = useMemo(() => 'sp' + Math.random().toString(36).slice(2, 7), [])
  if (!data || data.length < 2) return <svg width={width} height={height} />
  const min = Math.min(...data), max = Math.max(...data), range = (max - min) || 1
  const step = width / (data.length - 1)
  const pts: [number, number][] = data.map((v, i) => [i * step, height - ((v - min) / range) * (height - 4) - 2])
  const line = smoothPath(pts)
  return (
    <svg width={width} height={height} style={{ display: 'block' }}>
      <defs>
        <linearGradient id={id} x1="0" y1="0" x2="0" y2="1">
          <stop offset="0%" stopColor={color} stopOpacity=".28" />
          <stop offset="100%" stopColor={color} stopOpacity="0" />
        </linearGradient>
      </defs>
      <path d={`${line} L${width},${height} L0,${height} Z`} fill={`url(#${id})`} />
      <path d={line} fill="none" stroke={color} strokeWidth={strokeW} strokeLinecap="round" strokeLinejoin="round" />
    </svg>
  )
}

// ─── Chart: Area (for hero) ───────────────────────────────────────────────────
function AreaChart({ data, width = 540, height = 170, color = '#b8c8ff', fullHeight = false }: {
  data: number[]; width?: number; height?: number; color?: string; fullHeight?: boolean
}) {
  const id = useMemo(() => 'ac' + Math.random().toString(36).slice(2, 7), [])
  if (!data || data.length < 2) return <div style={{ flex: fullHeight ? 1 : undefined, height: fullHeight ? undefined : height }} />
  const min = Math.min(...data) * 0.992, max = Math.max(...data) * 1.005
  const range = max - min || 1
  const step = width / (data.length - 1)
  const pts: [number, number][] = data.map((v, i) => [i * step, height - ((v - min) / range) * (height - 12) - 6])
  const line = smoothPath(pts)
  const last = pts[pts.length - 1]
  const clipId = id + 'c'
  return (
    <svg viewBox={`0 0 ${width} ${height}`} width="100%" preserveAspectRatio="none"
      style={{ display: 'block', ...(fullHeight ? { height: '100%', flex: 1 } : { height }) }}>
      <defs>
        <linearGradient id={id} x1="0" y1="0" x2="0" y2="1">
          <stop offset="0%" stopColor={color} stopOpacity=".4" />
          <stop offset="100%" stopColor={color} stopOpacity="0" />
        </linearGradient>
        <clipPath id={clipId}>
          <rect x="0" y="0" width={0} height={height}>
            <animate attributeName="width" from={0} to={width} dur="1.1s"
              calcMode="spline" keySplines="0.25 0.46 0.45 0.94" fill="freeze" />
          </rect>
        </clipPath>
      </defs>
      <g clipPath={`url(#${clipId})`}>
        <path d={`${line} L${width},${height} L0,${height} Z`} fill={`url(#${id})`} />
        <path d={line} fill="none" stroke={color} strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" />
      </g>
      <circle cx={last[0]} cy={last[1]} r="6" fill={color} opacity="0">
        <animate attributeName="opacity" from="0" to=".2" begin="0.95s" dur="0.25s" fill="freeze" />
      </circle>
      <circle cx={last[0]} cy={last[1]} r="3.5" fill={color} opacity="0">
        <animate attributeName="opacity" from="0" to="1" begin="0.95s" dur="0.25s" fill="freeze" />
      </circle>
      <circle cx={last[0]} cy={last[1]} r="1.8" fill="#0c1018" opacity="0">
        <animate attributeName="opacity" from="0" to="1" begin="0.95s" dur="0.25s" fill="freeze" />
      </circle>
    </svg>
  )
}

// ─── Chart: Daily PnL bars ────────────────────────────────────────────────────
function DailyBarsChart({ items, xLabels }: { items: DailyPnL[]; xLabels: string[] }) {
  const vals = items.map(d => d.pnl)
  const W = 780, H = 200
  const pad = { t: 14, r: 10, b: 26, l: 48 }
  const w = W - pad.l - pad.r, h = H - pad.t - pad.b
  const max = Math.max(...vals.map(Math.abs), 0.01)
  const barW = (w / Math.max(vals.length, 1)) * 0.7
  const gap = (w / Math.max(vals.length, 1)) * 0.3
  const zeroY = pad.t + h / 2

  if (vals.length === 0) {
    return (
      <svg viewBox={`0 0 ${W} ${H}`} width="100%" height={H} style={{ display: 'block' }}>
        <text x={W / 2} y={H / 2} textAnchor="middle" fill={T.dim} fontSize="13">нет данных</text>
      </svg>
    )
  }
  return (
    <svg viewBox={`0 0 ${W} ${H}`} width="100%" height={H} preserveAspectRatio="none" style={{ display: 'block' }}>
      <line x1={pad.l} x2={pad.l + w} y1={zeroY} y2={zeroY} stroke={T.borderHi} />
      {[max, max / 2, -max / 2, -max].map((v, i) => (
        <g key={i}>
          <line x1={pad.l} x2={pad.l + w} y1={zeroY - (v / max) * (h / 2)} y2={zeroY - (v / max) * (h / 2)} stroke={T.border} strokeDasharray="2 4" />
          <text x={pad.l - 6} y={zeroY - (v / max) * (h / 2) + 4} fill={T.faint} fontSize="10" fontFamily="'JetBrains Mono',monospace" textAnchor="end">
            {v >= 0 ? '+' : ''}{Math.round(v)}
          </text>
        </g>
      ))}
      {vals.map((v, i) => {
        const x = pad.l + i * (barW + gap) + gap / 2
        const bh = Math.abs(v) / max * (h / 2)
        const y = v >= 0 ? zeroY - bh : zeroY
        const delay = (i * 0.012).toFixed(3)
        return (
          <rect key={i} x={x} y={zeroY} width={barW} height={0} fill={v >= 0 ? T.green : T.red} opacity={0.9} rx="1.5">
            <animate attributeName="height" from={0} to={Math.max(bh, 1.5)}
              dur="0.55s" begin={`${delay}s`} calcMode="spline" keySplines="0.25 0.46 0.45 0.94" fill="freeze" />
            <animate attributeName="y" from={zeroY} to={y}
              dur="0.55s" begin={`${delay}s`} calcMode="spline" keySplines="0.25 0.46 0.45 0.94" fill="freeze" />
          </rect>
        )
      })}
      {xLabels.map((l, i) => (
        <text key={i} x={pad.l + (w * i) / Math.max(xLabels.length - 1, 1)} y={pad.t + h + 18}
          fill={T.faint} fontSize="10" fontFamily="'Inter',sans-serif" textAnchor="middle">{l}</text>
      ))}
    </svg>
  )
}

// ─── Chart: Drawdown area ─────────────────────────────────────────────────────
function DrawdownChart({ daily }: { daily: DailyPnL[] }) {
  const id = useMemo(() => 'dd' + Math.random().toString(36).slice(2, 7), [])

  const W = 480, H = 200
  const pad = { t: 14, r: 10, b: 26, l: 42 }
  const w = W - pad.l - pad.r, h = H - pad.t - pad.b

  const dd = useMemo(() => {
    let running = 0, peak = 0
    return daily.map(d => {
      running += d.pnl
      if (running > peak) peak = running
      return peak > 0 ? ((running - peak) / peak) * 100 : 0
    })
  }, [daily])

  if (daily.length < 2) {
    return (
      <svg viewBox={`0 0 ${W} ${H}`} width="100%" height={H} style={{ display: 'block' }}>
        <text x={W / 2} y={H / 2} textAnchor="middle" fill={T.dim} fontSize="13">нет данных</text>
      </svg>
    )
  }

  const minDD = Math.min(...dd, -0.01)
  const range = 0 - minDD || 1
  const step = (w) / (dd.length - 1)
  const pts: [number, number][] = dd.map((v, i) => [pad.l + i * step, pad.t + ((0 - v) / range) * h])
  const line = smoothPath(pts)
  const grids = [0, -2, -4, -6, -8, -10, -15].filter(v => v >= minDD - 1)

  const clipId = id + 'c'
  return (
    <svg viewBox={`0 0 ${W} ${H}`} width="100%" height={H} preserveAspectRatio="none" style={{ display: 'block' }}>
      <defs>
        <linearGradient id={id} x1="0" y1="0" x2="0" y2="1">
          <stop offset="0%" stopColor={T.red} stopOpacity="0" />
          <stop offset="100%" stopColor={T.red} stopOpacity=".34" />
        </linearGradient>
        <clipPath id={clipId}>
          <rect x="0" y="0" width={0} height={H}>
            <animate attributeName="width" from={0} to={W} dur="1.1s"
              calcMode="spline" keySplines="0.25 0.46 0.45 0.94" fill="freeze" />
          </rect>
        </clipPath>
      </defs>
      {grids.map((v, i) => (
        <g key={i}>
          <line x1={pad.l} x2={pad.l + w} y1={pad.t + ((0 - v) / range) * h} y2={pad.t + ((0 - v) / range) * h} stroke={T.border} strokeDasharray="2 4" />
          <text x={pad.l - 6} y={pad.t + ((0 - v) / range) * h + 4} fill={T.faint} fontSize="10" fontFamily="'JetBrains Mono',monospace" textAnchor="end">{v}%</text>
        </g>
      ))}
      <g clipPath={`url(#${clipId})`}>
        <path d={`${line} L${pad.l + w},${pad.t + h} L${pad.l},${pad.t + h} Z`} fill={`url(#${id})`} />
        <path d={line} fill="none" stroke={T.red} strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round" />
      </g>
    </svg>
  )
}

// ─── Chart: Donut ─────────────────────────────────────────────────────────────
function DonutChart({ segs, size = 130, thick = 15 }: {
  segs: { pct: number; color: string }[]; size?: number; thick?: number
}) {
  const r = size / 2 - thick / 2
  const c = 2 * Math.PI * r
  const total = segs.reduce((s, x) => s + x.pct, 0) || 1
  let offset = 0
  return (
    <svg width={size} height={size} style={{ transform: 'rotate(-90deg)' }}>
      <circle cx={size / 2} cy={size / 2} r={r} stroke={T.border} strokeWidth={thick} fill="none" />
      {segs.map((s, i) => {
        const frac = s.pct / total
        const dash = c * frac - 2.5
        const el = (
          <circle key={i} cx={size / 2} cy={size / 2} r={r}
            stroke={s.color} strokeWidth={thick} fill="none"
            strokeDasharray={`${dash} ${c - dash}`}
            strokeDashoffset={-c * offset}
            strokeLinecap="butt" />
        )
        offset += frac
        return el
      })}
    </svg>
  )
}

// ─── Primitives ───────────────────────────────────────────────────────────────
function Card({ children, pad = '16px 18px', style }: { children: React.ReactNode; pad?: string; style?: CSSProperties }) {
  return (
    <div style={{ background: T.panel, border: `1px solid ${T.border}`, borderRadius: 14, padding: pad, ...style }}>
      {children}
    </div>
  )
}

function Lbl({ children }: { children: React.ReactNode }) {
  return <div style={{ fontSize: 10, color: T.dim, textTransform: 'uppercase', letterSpacing: '1.3px', fontWeight: 600 }}>{children}</div>
}

function SHead({ title, sub, right }: { title: string; sub?: string; right?: React.ReactNode }) {
  return (
    <div style={{ display: 'flex', alignItems: 'baseline', gap: 10, marginBottom: 12 }}>
      <h2 style={{ margin: 0, ...grotesk, fontSize: 14, fontWeight: 700, color: T.text, letterSpacing: '-0.2px' }}>{title}</h2>
      {sub && <span style={{ fontSize: 11, color: T.dim }}>{sub}</span>}
      <div style={{ flex: 1 }} />
      {right}
    </div>
  )
}

function Pill({ c = T.body, bg = 'rgba(255,255,255,.04)', bd = T.border, children }: {
  c?: string; bg?: string; bd?: string; children: React.ReactNode
}) {
  return (
    <span style={{ display: 'inline-flex', alignItems: 'center', gap: 5, padding: '3px 8px', background: bg, border: `1px solid ${bd}`, color: c, borderRadius: 999, fontSize: 11, fontWeight: 600 }}>
      {children}
    </span>
  )
}

function Delta({ val, pct, size = 'md' }: { val?: number | null; pct?: number | null; size?: 'sm' | 'md' | 'lg' }) {
  const v = val ?? pct ?? 0
  const up = v >= 0
  const c = up ? T.green : T.red
  const bg = up ? 'rgba(65,210,139,.14)' : 'rgba(248,113,113,.14)'
  const bd = up ? 'rgba(65,210,139,.28)' : 'rgba(248,113,113,.30)'
  const fs = size === 'lg' ? 13 : size === 'sm' ? 11 : 12
  const pd = size === 'lg' ? '4px 11px 4px 8px' : size === 'sm' ? '2px 7px 2px 5px' : '3px 9px 3px 6px'
  return (
    <span style={{ display: 'inline-flex', alignItems: 'center', gap: 4, padding: pd, background: bg, border: `1px solid ${bd}`, color: c, borderRadius: 999, fontSize: fs, fontWeight: 700, ...mono }}>
      <svg width={fs - 1} height={fs - 1} viewBox="0 0 24 24" fill="none" stroke={c} strokeWidth={2.5} strokeLinecap="round" strokeLinejoin="round">
        {up ? <path d="M7 17L17 7M9 7h8v8" /> : <path d="M17 7L7 17M7 9v8h8" />}
      </svg>
      {val != null && <>{up ? '+' : ''}{fmt$(val)}</>}
      {pct != null && <>{val != null ? ' · ' : ''}{fmtPct(pct)}</>}
    </span>
  )
}

function StatBox({ label, value, good, bad, warn }: { label: string; value: string; good?: boolean; bad?: boolean; warn?: boolean }) {
  const c = bad ? T.red : warn ? T.orange : good ? T.green : T.text
  return (
    <div style={{ padding: '9px 11px', background: 'rgba(255,255,255,.03)', border: `1px solid ${T.border}`, borderRadius: 9 }}>
      <div style={{ fontSize: 10, color: T.dim, textTransform: 'uppercase', letterSpacing: '1px', fontWeight: 600 }}>{label}</div>
      <div style={{ ...mono, fontSize: 15, fontWeight: 700, marginTop: 3, color: c }}>{value}</div>
    </div>
  )
}

function SmRow({ label, value, c = T.body }: { label: string; value: string; c?: string }) {
  return (
    <div style={{ display: 'flex', alignItems: 'baseline', justifyContent: 'space-between' }}>
      <span style={{ fontSize: 12, color: T.dim }}>{label}</span>
      <span style={{ ...mono, fontSize: 13, fontWeight: 600, color: c }}>{value}</span>
    </div>
  )
}

// ─── Period tabs ──────────────────────────────────────────────────────────────
type Period = '1d' | '7d' | '30d' | '90d' | '1y' | 'all'
const PERIODS: { id: Period; label: string }[] = [
  { id: '1d',  label: '1 день'  },
  { id: '7d',  label: '7 дней'  },
  { id: '30d', label: '30 дней' },
  { id: '90d', label: '90 дней' },
  { id: '1y',  label: '1 год'   },
  { id: 'all', label: 'Всё'     },
]

function PeriodTabs({ value, onChange }: { value: Period; onChange: (p: Period) => void }) {
  return (
    <div style={{ display: 'flex', gap: 2, padding: 3, background: 'rgba(0,0,0,.25)', border: `1px solid ${T.border}`, borderRadius: 10 }}>
      {PERIODS.map(p => (
        <button key={p.id} onClick={() => onChange(p.id)} style={{
          padding: '7px 14px', border: 0, borderRadius: 7, fontSize: 12, fontWeight: 600, cursor: 'pointer', fontFamily: 'inherit',
          background: value === p.id ? 'rgba(123,140,255,.18)' : 'transparent',
          color: value === p.id ? T.text : T.dim,
        }}>{p.label}</button>
      ))}
    </div>
  )
}

// ─── Hero card ────────────────────────────────────────────────────────────────
function HeroCard({ data, period, equity, equityChange }: {
  data: DashboardData; period: Period; equity: number | null; equityChange: { usd: number; pct: number } | null
}) {
  const { stats, daily_pnl, recent_trades } = data
  const pLabel = PERIODS.find(p => p.id === period)?.label ?? period

  const cumSeries = useMemo(() => {
    let acc = 0
    const base = Math.max(stats.total_pnl * 0.1, 0)
    return daily_pnl.map(d => { acc += d.pnl; return base + acc })
  }, [daily_pnl, stats.total_pnl])

  // Reward:Risk — avg winner / avg loser (from recent_trades sample)
  const rr = useMemo(() => {
    const wins = recent_trades.filter(t => (t.pnl ?? 0) > 0)
    const losses = recent_trades.filter(t => (t.pnl ?? 0) < 0)
    if (wins.length === 0 || losses.length === 0) return null
    const avgW = wins.reduce((s, t) => s + (t.pnl ?? 0), 0) / wins.length
    const avgL = Math.abs(losses.reduce((s, t) => s + (t.pnl ?? 0), 0) / losses.length)
    return avgL > 0 ? avgW / avgL : null
  }, [recent_trades])

  return (
    <div style={{
      flex: 1,
      background: 'linear-gradient(135deg,#131a30 0%,#16182d 40%,#1f1932 100%)',
      border: '1px solid rgba(123,140,255,.22)', borderRadius: 18,
      boxShadow: '0 28px 70px -32px rgba(91,140,255,.4)',
      position: 'relative', overflow: 'hidden',
      display: 'grid', gridTemplateColumns: 'minmax(0,1fr) minmax(0,2fr) minmax(0,1fr)',
      gridTemplateRows: '1fr',
    }}>
      <div style={{ position: 'absolute', top: -60, right: 120, width: 340, height: 340, pointerEvents: 'none', background: 'radial-gradient(circle,rgba(91,140,255,.35),transparent 60%)', filter: 'blur(20px)' }} />
      <div style={{ position: 'absolute', bottom: -90, left: -80, width: 280, height: 280, pointerEvents: 'none', background: 'radial-gradient(circle,rgba(193,77,255,.22),transparent 60%)', filter: 'blur(24px)' }} />

      {/* LEFT */}
      <div style={{ padding: '22px 24px', position: 'relative', borderRight: '1px solid rgba(255,255,255,.05)' }}>
        <Lbl>{equity != null ? 'Equity' : `P&L · ${pLabel}`}</Lbl>
        <div style={{ ...grotesk, fontSize: 36, fontWeight: 700, color: '#fff', letterSpacing: '-1px', marginTop: 4, lineHeight: 1.05, display: 'flex', alignItems: 'baseline', gap: 8 }}>
          {equity != null ? fmt$(equity) : fmt$(stats.total_pnl)}
          <span style={{ fontSize: 13, color: T.dim, fontFamily: 'Inter,sans-serif', fontWeight: 500 }}>USDT</span>
        </div>
        <div style={{ marginTop: 12, display: 'flex', alignItems: 'center', gap: 8 }}>
          {equityChange != null
            ? <Delta val={equityChange.usd} pct={equityChange.pct} size="lg" />
            : <Delta val={stats.total_pnl} size="lg" />
          }
          <span style={{ fontSize: 11, color: T.dim }}>за {pLabel}</span>
        </div>
        <div style={{ marginTop: 16, display: 'flex', flexDirection: 'column', gap: 6 }}>
          <SmRow label="Реализованный P&L" value={fmt$(stats.total_pnl)} c={stats.total_pnl >= 0 ? T.green : T.red} />
          <SmRow label="Лучшая сделка" value={fmt$(stats.best_trade)} c={T.green} />
          <SmRow label="Худшая сделка" value={fmt$(stats.worst_trade)} c={T.red} />
        </div>
      </div>

      {/* CENTER */}
      <div style={{ padding: '22px 4px 12px 12px', position: 'relative', display: 'flex', flexDirection: 'column' }}>
        <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'baseline', padding: '0 16px 8px', flexShrink: 0 }}>
          <Lbl>Кривая P&L</Lbl>
          {daily_pnl.length > 0 && (
            <span style={{ ...mono, fontSize: 11, color: T.dim }}>
              {daily_pnl[0]?.day.slice(5)} → {daily_pnl[daily_pnl.length - 1]?.day.slice(5)}
            </span>
          )}
        </div>
        {cumSeries.length >= 2
          ? <AreaChart data={cumSeries} width={540} height={170} color="#b8c8ff" fullHeight />
          : <div style={{ flex: 1, minHeight: 140, display: 'flex', alignItems: 'center', justifyContent: 'center', color: T.dim, fontSize: 13 }}>нет данных</div>
        }
      </div>

      {/* RIGHT */}
      <div style={{ padding: '22px 24px', borderLeft: '1px solid rgba(255,255,255,.05)', position: 'relative' }}>
        <Lbl>Статистика</Lbl>
        <div style={{ marginTop: 10, display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 10 }}>
          <StatBox label="Win Rate" value={`${stats.win_rate.toFixed(1)}%`}
            good={stats.win_rate >= 60} warn={stats.win_rate >= 45 && stats.win_rate < 60}
            bad={stats.win_rate < 45 && stats.total > 0} />
          <StatBox label="Profit Factor" value={stats.profit_factor >= 999 ? '∞' : stats.profit_factor.toFixed(2)}
            good={stats.profit_factor >= 1.5} warn={stats.profit_factor >= 1 && stats.profit_factor < 1.5}
            bad={stats.profit_factor < 1 && stats.total > 0} />
          <StatBox label="Сделок" value={String(stats.total)} />
          <StatBox label="Побед" value={`${stats.wins} / ${stats.losses}`} />
          <StatBox label="Ср. P&L" value={fmt$(stats.avg_pnl)} good={stats.avg_pnl > 0} bad={stats.avg_pnl < 0} />
          <StatBox label="R:R" value={rr != null ? rr.toFixed(2) : '—'} good={rr != null && rr >= 1.5} warn={rr != null && rr >= 1 && rr < 1.5} bad={rr != null && rr < 1} />
        </div>
      </div>
    </div>
  )
}

// ─── Daily Stats Strip ────────────────────────────────────────────────────────
function DailyStatsStrip({ data }: { data: DashboardData }) {
  const { daily_pnl } = data

  const s = useMemo(() => {
    if (daily_pnl.length === 0) return null
    const best  = daily_pnl.reduce((a, b) => b.pnl > a.pnl ? b : a)
    const worst = daily_pnl.reduce((a, b) => b.pnl < a.pnl ? b : a)
    const avg   = daily_pnl.reduce((sum, d) => sum + d.pnl, 0) / daily_pnl.length
    const posCount = daily_pnl.filter(d => d.pnl > 0).length
    const posPct   = (posCount / daily_pnl.length) * 100
    // Current streak (backwards from last day)
    let streak = 0, streakUp: boolean | null = null
    for (let i = daily_pnl.length - 1; i >= 0; i--) {
      const up = daily_pnl[i].pnl >= 0
      if (streakUp === null) { streakUp = up; streak = 1 }
      else if (up === streakUp) streak++
      else break
    }
    // Max consecutive green days
    let maxWin = 0, cur = 0
    for (const d of daily_pnl) {
      if (d.pnl >= 0) { cur++; if (cur > maxWin) maxWin = cur } else cur = 0
    }
    return { best, worst, avg, posCount, posPct, streak, streakUp, maxWin }
  }, [daily_pnl])

  const items = !s ? [] : [
    {
      label: 'Лучший день',
      value: fmt$(s.best.pnl),
      accent: T.green,
      sub: s.best.day.slice(5),
    },
    {
      label: 'Худший день',
      value: fmt$(s.worst.pnl),
      accent: T.red,
      sub: s.worst.day.slice(5),
    },
    {
      label: 'Ср. P&L в день',
      value: fmt$(s.avg),
      accent: s.avg >= 0 ? T.green : T.red,
      sub: `за ${daily_pnl.length} дн.`,
    },
    {
      label: 'Зелёных дней',
      value: `${s.posPct.toFixed(0)}%`,
      accent: s.posPct >= 60 ? T.green : s.posPct >= 45 ? T.orange : T.red,
      sub: `${s.posCount} из ${daily_pnl.length}`,
    },
    {
      label: 'Текущий стрик',
      value: s.streak > 0 && s.streakUp !== null ? `${s.streakUp ? '+' : '−'}${s.streak} дн.` : '—',
      accent: s.streakUp === true ? T.green : s.streakUp === false ? T.red : T.dim,
      sub: s.streakUp === true ? 'подряд зелёных' : 'подряд красных',
    },
    {
      label: 'Макс. стрик',
      value: s.maxWin > 0 ? `${s.maxWin} дн.` : '—',
      accent: T.blue,
      sub: 'лучшая серия побед',
    },
  ]

  return (
    <div style={{ display: 'grid', gridTemplateColumns: 'repeat(6,1fr)', gap: 12 }}>
      {daily_pnl.length === 0
        ? Array.from({ length: 6 }).map((_, i) => (
          <Card key={i} pad="14px 16px">
            <Lbl>—</Lbl>
            <div style={{ ...grotesk, fontSize: 22, fontWeight: 700, color: T.dim, marginTop: 8 }}>—</div>
          </Card>
        ))
        : items.map((it, i) => (
          <Card key={i} pad="14px 16px">
            <Lbl>{it.label}</Lbl>
            <div style={{ ...grotesk, fontSize: 22, fontWeight: 700, color: it.accent, letterSpacing: '-0.4px', marginTop: 8, lineHeight: 1.1 }}>
              {it.value}
            </div>
            <div style={{ marginTop: 6, fontSize: 11, color: T.dim }}>{it.sub}</div>
          </Card>
        ))
      }
    </div>
  )
}

// ─── Cumulative PnL big chart ─────────────────────────────────────────────────
function PnLCurveCard({ data }: { data: DashboardData }) {
  const { daily_pnl } = data
  const cumSeries = useMemo(() => { let acc = 0; return daily_pnl.map(d => { acc += d.pnl; return acc }) }, [daily_pnl])

  const xLabels = useMemo(() => {
    if (daily_pnl.length === 0) return []
    const n = daily_pnl.length
    const idxs = [0, Math.floor(n * 0.25), Math.floor(n * 0.5), Math.floor(n * 0.75), n - 1]
    return [...new Set(idxs)].map(i => daily_pnl[i]?.day.slice(5) ?? '')
  }, [daily_pnl])

  const W = 1400, H = 260
  const pad = { t: 18, r: 18, b: 30, l: 64 }
  const cw = W - pad.l - pad.r, ch = H - pad.t - pad.b
  const id = useMemo(() => 'eq' + Math.random().toString(36).slice(2, 7), [])

  const allMin = Math.min(...cumSeries, 0), allMax = Math.max(...cumSeries, 0)
  const vRange = Math.max(allMax - allMin, 0.01)
  const min = allMin - vRange * 0.06, max = allMax + vRange * 0.06
  const range = max - min

  const toPts = (arr: number[]): [number, number][] => {
    const step = arr.length > 1 ? cw / (arr.length - 1) : 0
    return arr.map((v, i) => [pad.l + i * step, pad.t + ch - ((v - min) / range) * ch])
  }
  const ePts = toPts(cumSeries)
  const ePath = ePts.length > 1 ? smoothPath(ePts) : ''
  const eLast = ePts[ePts.length - 1]
  const yTicks = 4

  return (
    <Card pad="18px 20px 14px">
      <div style={{ display: 'flex', alignItems: 'center', gap: 12, marginBottom: 8 }}>
        <h2 style={{ margin: 0, ...grotesk, fontSize: 14, fontWeight: 700, color: T.text, letterSpacing: '-0.2px' }}>Кривая P&L</h2>
        <Pill c={T.blue} bg="rgba(91,140,255,.10)" bd="rgba(91,140,255,.25)">
          <span style={{ width: 6, height: 6, borderRadius: '50%', background: T.blue, display: 'inline-block' }} />{' '}
          кумулятивный P&L
        </Pill>
        <div style={{ flex: 1 }} />
        <span style={{ ...mono, fontSize: 11, color: T.dim }}>{daily_pnl.length} дней данных</span>
      </div>
      {cumSeries.length >= 2 ? (
        <svg viewBox={`0 0 ${W} ${H}`} width="100%" height={280} preserveAspectRatio="none" style={{ display: 'block' }}>
          <defs>
            <linearGradient id={id} x1="0" y1="0" x2="0" y2="1">
              <stop offset="0%" stopColor={T.blue} stopOpacity=".28" />
              <stop offset="100%" stopColor={T.blue} stopOpacity="0" />
            </linearGradient>
            <clipPath id={id + 'c'}>
              <rect x="0" y="0" width={0} height={H}>
                <animate attributeName="width" from={0} to={W} dur="1.3s"
                  calcMode="spline" keySplines="0.25 0.46 0.45 0.94" fill="freeze" />
              </rect>
            </clipPath>
          </defs>
          {Array.from({ length: yTicks + 1 }).map((_, i) => {
            const v = min + (range * i) / yTicks
            const y = pad.t + ch - ((v - min) / range) * ch
            return (
              <g key={i}>
                <line x1={pad.l} x2={pad.l + cw} y1={y} y2={y} stroke={T.border} strokeDasharray="2 4" />
                <text x={pad.l - 10} y={y + 4} fill={T.faint} fontSize="11" fontFamily="'JetBrains Mono',monospace" textAnchor="end">
                  {v >= 0 ? '+' : ''}{Math.round(v).toString().replace(/\B(?=(\d{3})+(?!\d))/g, ',')}$
                </text>
              </g>
            )
          })}
          {xLabels.map((l, i) => (
            <text key={i} x={pad.l + (cw * i) / Math.max(xLabels.length - 1, 1)} y={pad.t + ch + 20}
              fill={T.faint} fontSize="11" fontFamily="'Inter',sans-serif" textAnchor="middle">{l}</text>
          ))}
          <g clipPath={`url(#${id + 'c'})`}>
            {allMin < 0 && allMax > 0 && (
              <line x1={pad.l} x2={pad.l + cw}
                y1={pad.t + ch - ((0 - min) / range) * ch}
                y2={pad.t + ch - ((0 - min) / range) * ch}
                stroke="rgba(255,255,255,.12)" strokeDasharray="4 4" />
            )}
            <path d={`${ePath} L${pad.l + cw},${pad.t + ch} L${pad.l},${pad.t + ch} Z`} fill={`url(#${id})`} />
            <path d={ePath} fill="none" stroke={T.blue} strokeWidth="2.4" strokeLinecap="round" strokeLinejoin="round" />
          </g>
          {eLast && (
            <>
              <circle cx={eLast[0]} cy={eLast[1]} r="7" fill={T.blue} opacity="0">
                <animate attributeName="opacity" from="0" to=".22" begin="1.1s" dur="0.3s" fill="freeze" />
              </circle>
              <circle cx={eLast[0]} cy={eLast[1]} r="4" fill={T.blue} opacity="0">
                <animate attributeName="opacity" from="0" to="1" begin="1.1s" dur="0.3s" fill="freeze" />
              </circle>
              <circle cx={eLast[0]} cy={eLast[1]} r="2" fill="#fff" opacity="0">
                <animate attributeName="opacity" from="0" to="1" begin="1.1s" dur="0.3s" fill="freeze" />
              </circle>
            </>
          )}
        </svg>
      ) : (
        <div style={{ height: 280, display: 'flex', alignItems: 'center', justifyContent: 'center', color: T.dim, fontSize: 13 }}>
          {data.stats.total === 0 ? 'Нет закрытых сделок за период' : 'Недостаточно данных'}
        </div>
      )}
    </Card>
  )
}

// ─── Daily PnL card ───────────────────────────────────────────────────────────
function DailyBarsCard({ data }: { data: DashboardData }) {
  const { daily_pnl } = data
  const greenCnt = daily_pnl.filter(d => d.pnl >= 0).length
  const redCnt   = daily_pnl.filter(d => d.pnl < 0).length
  const xLabels  = useMemo(() => {
    if (daily_pnl.length === 0) return []
    const n = daily_pnl.length
    return [0, Math.floor(n * 0.33), Math.floor(n * 0.66), n - 1]
      .map(i => daily_pnl[i]?.day.slice(5) ?? '')
  }, [daily_pnl])
  return (
    <Card pad="16px 18px">
      <SHead title="P&L по дням" sub="по периоду"
        right={<div style={{ display: 'flex', gap: 6 }}>
          <Pill c={T.green} bg={T.greenSoft} bd={T.greenBd}>● {greenCnt}</Pill>
          <Pill c={T.red}   bg={T.redSoft}   bd={T.redBd}>● {redCnt}</Pill>
        </div>}
      />
      <DailyBarsChart items={daily_pnl} xLabels={xLabels} />
    </Card>
  )
}

// ─── Drawdown card ────────────────────────────────────────────────────────────
function DrawdownCard({ data }: { data: DashboardData }) {
  const { daily_pnl } = data
  const maxDD = useMemo(() => {
    let running = 0, peak = 0, mx = 0
    for (const d of daily_pnl) {
      running += d.pnl
      if (running > peak) peak = running
      if (peak > 0) { const dd = Math.abs((running - peak) / peak * 100); if (dd > mx) mx = dd }
    }
    return mx
  }, [daily_pnl])
  return (
    <Card pad="16px 18px">
      <SHead title="Просадка" sub="от пика P&L"
        right={<Pill c={T.red} bg={T.redSoft} bd={T.redBd}>max: -{maxDD.toFixed(1)}%</Pill>}
      />
      <DrawdownChart daily={daily_pnl} />
    </Card>
  )
}

// ─── Open Positions card ──────────────────────────────────────────────────────
function OpenPositionsCard({ positions, accountLabel }: { positions: Position[]; accountLabel: string }) {
  const totalPnl = positions.reduce((s, p) => s + parseFloat(p.unrealisedPnl || '0'), 0)
  return (
    <Card pad="0" style={{ display: 'flex', flexDirection: 'column', overflow: 'hidden' }}>
      <div style={{ padding: '14px 18px 10px', display: 'flex', alignItems: 'center', gap: 10, flexShrink: 0 }}>
        <h2 style={{ margin: 0, ...grotesk, fontSize: 14, fontWeight: 700, color: T.text }}>Открытые позиции</h2>
        <span style={{ fontSize: 11, color: T.dim }}>{positions.length} активных · {accountLabel}</span>
        <div style={{ flex: 1 }} />
        {positions.length > 0 && (
          <Pill c={totalPnl >= 0 ? T.green : T.red}
            bg={totalPnl >= 0 ? T.greenSoft : T.redSoft}
            bd={totalPnl >= 0 ? T.greenBd : T.redBd}>
            P&L: {totalPnl >= 0 ? '+' : ''}{fmt$(totalPnl)}
          </Pill>
        )}
      </div>
      {positions.length === 0 ? (
        <div style={{ padding: '32px 18px', textAlign: 'center', color: T.dim, fontSize: 13, borderTop: `1px solid ${T.border}` }}>
          Нет открытых позиций
        </div>
      ) : (
        <div style={{ borderTop: `1px solid ${T.border}`, flex: 1, overflowY: 'auto' }}>
          <div style={{
            display: 'grid', gridTemplateColumns: 'minmax(0,1fr) 48px 72px 72px 72px 80px',
            padding: '9px 16px', fontSize: 10, color: T.dim,
            textTransform: 'uppercase', letterSpacing: '1.2px', fontWeight: 600,
            borderBottom: `1px solid ${T.border}`,
            position: 'sticky', top: 0, background: T.panel, zIndex: 1,
          }}>
            <div>Символ</div><div>Side</div>
            <div style={{ textAlign: 'right' }}>Размер</div>
            <div style={{ textAlign: 'right' }}>Вход</div>
            <div style={{ textAlign: 'right' }}>Mark</div>
            <div style={{ textAlign: 'right' }}>P&L</div>
          </div>
          {positions.slice(0, 10).map((p, i) => {
            const isLong = p.side === 'Buy'
            const pnl = parseFloat(p.unrealisedPnl || '0')
            return (
              <div key={i} style={{
                display: 'grid', gridTemplateColumns: 'minmax(0,1fr) 48px 72px 72px 72px 80px',
                padding: '10px 16px', fontSize: 12, alignItems: 'center',
                borderBottom: i === positions.length - 1 ? 'none' : `1px solid ${T.border}`,
              }}>
                <div style={{ display: 'flex', alignItems: 'center', gap: 7, minWidth: 0 }}>
                  <span style={{ ...mono, fontWeight: 700, color: T.text, fontSize: 12 }}>{p.symbol}</span>
                  <span style={{ ...mono, fontSize: 9, color: T.dim, padding: '1px 4px', background: 'rgba(255,255,255,.04)', borderRadius: 3, fontWeight: 600 }}>{p.leverage}x</span>
                </div>
                <div>
                  <span style={{
                    display: 'inline-flex', padding: '2px 6px', borderRadius: 3, fontSize: 9, fontWeight: 700,
                    background: isLong ? T.greenSoft : T.redSoft,
                    border: `1px solid ${isLong ? T.greenBd : T.redBd}`,
                    color: isLong ? T.green : T.red, textTransform: 'uppercase',
                  }}>{isLong ? 'L' : 'S'}</span>
                </div>
                <div style={{ ...mono, fontSize: 11, color: T.body, textAlign: 'right' }}>{fmt$(p.sizeUsdt, 0)}</div>
                <div style={{ ...mono, fontSize: 11, color: T.body, textAlign: 'right' }}>{fmtPrice(parseFloat(p.entryPrice || '0'))}</div>
                <div style={{ ...mono, fontSize: 11, color: T.text, textAlign: 'right', fontWeight: 600 }}>{fmtPrice(parseFloat(p.markPrice || '0'))}</div>
                <div style={{ textAlign: 'right' }}>
                  <div style={{ ...mono, fontSize: 12, fontWeight: 700, color: pnl >= 0 ? T.green : T.red }}>{pnl >= 0 ? '+' : ''}{fmt$(pnl)}</div>
                  <div style={{ ...mono, fontSize: 10, color: pnl >= 0 ? T.green : T.red, marginTop: 1 }}>
                    {p.unrealisedPnlPct ? (parseFloat(p.unrealisedPnlPct) >= 0 ? '+' : '') + parseFloat(p.unrealisedPnlPct).toFixed(2) + '%' : ''}
                  </div>
                </div>
              </div>
            )
          })}
        </div>
      )}
    </Card>
  )
}

// ─── Asset Allocation card ────────────────────────────────────────────────────
function AssetAllocationCard({ positions }: { positions: Position[] }) {
  const totalSize = positions.reduce((s, p) => s + (p.sizeUsdt ?? 0), 0)
  const COLORS = ['#f7a600', '#9aa6ff', '#c14dff', '#f0b90b', '#5be0a0', '#5b8cff', '#fca5a5', '#7b8aa6']
  const assetMap: Record<string, number> = {}
  for (const p of positions) {
    const sym = p.symbol.replace('USDT', '').replace('USDC', '')
    assetMap[sym] = (assetMap[sym] ?? 0) + (p.sizeUsdt ?? 0)
  }
  const assets = Object.entries(assetMap)
    .sort((a, b) => b[1] - a[1]).slice(0, 8)
    .map(([sym, value], i) => ({ sym, value, pct: totalSize > 0 ? (value / totalSize) * 100 : 0, color: COLORS[i % COLORS.length] }))

  return (
    <Card pad="16px 18px">
      <SHead title="Позиции по активам" sub={fmt$(totalSize, 0) + ' в работе'} />
      {assets.length === 0 ? (
        <div style={{ textAlign: 'center', padding: '28px 0', color: T.dim, fontSize: 13 }}>Нет открытых позиций</div>
      ) : (
        <div style={{ display: 'flex', alignItems: 'center', gap: 14 }}>
          <div style={{ position: 'relative', width: 130, height: 130, flexShrink: 0 }}>
            <DonutChart segs={assets} size={130} thick={15} />
            <div style={{ position: 'absolute', inset: 0, display: 'flex', flexDirection: 'column', alignItems: 'center', justifyContent: 'center', pointerEvents: 'none' }}>
              <div style={{ fontSize: 9, color: T.dim, textTransform: 'uppercase', letterSpacing: '1px', fontWeight: 600 }}>Всего</div>
              <div style={{ ...grotesk, fontSize: 13, fontWeight: 700, color: T.text, marginTop: 1 }}>{fmt$(totalSize, 0)}</div>
            </div>
          </div>
          <div style={{ flex: 1, display: 'flex', flexDirection: 'column', gap: 6 }}>
            {assets.map((a, i) => (
              <div key={i} style={{ display: 'flex', alignItems: 'center', gap: 7 }}>
                <div style={{ width: 7, height: 7, borderRadius: 2, background: a.color, flexShrink: 0 }} />
                <div style={{ fontSize: 12, color: T.body, fontWeight: 600, minWidth: 34 }}>{a.sym}</div>
                <div style={{ flex: 1, height: 3, background: 'rgba(255,255,255,.04)', borderRadius: 2, overflow: 'hidden' }}>
                  <div style={{ width: `${Math.min(100, a.pct)}%`, height: '100%', background: a.color, opacity: .7 }} />
                </div>
                <div style={{ ...mono, fontSize: 10, color: T.dim, minWidth: 28, textAlign: 'right' }}>{a.pct.toFixed(0)}%</div>
                <div style={{ ...mono, fontSize: 10, color: T.body, minWidth: 48, textAlign: 'right', fontWeight: 500 }}>{fmt$(a.value, 0)}</div>
              </div>
            ))}
          </div>
        </div>
      )}
    </Card>
  )
}

// ─── Recent Trades card (standalone) ─────────────────────────────────────────
function RecentTradesCard({ data }: { data: DashboardData }) {
  const { recent_trades } = data
  return (
    <Card pad="0" style={{ display: 'flex', flexDirection: 'column', overflow: 'hidden' }}>
      <div style={{ padding: '14px 18px 10px', display: 'flex', alignItems: 'center', gap: 10, flexShrink: 0 }}>
        <h2 style={{ margin: 0, ...grotesk, fontSize: 14, fontWeight: 700, color: T.text }}>Последние сделки</h2>
        <span style={{ fontSize: 11, color: T.dim }}>{recent_trades.length} записей</span>
      </div>
      {recent_trades.length === 0 ? (
        <div style={{ padding: '32px 18px', textAlign: 'center', color: T.dim, fontSize: 13, borderTop: `1px solid ${T.border}` }}>
          Нет закрытых сделок
        </div>
      ) : (
        <div style={{ borderTop: `1px solid ${T.border}`, flex: 1, overflowY: 'auto' }}>
          {/* Header row */}
          <div style={{
            display: 'grid', gridTemplateColumns: '78px minmax(0,1.2fr) 40px minmax(0,1fr) 80px 58px',
            padding: '8px 16px', fontSize: 10, color: T.dim, textTransform: 'uppercase',
            letterSpacing: '1.2px', fontWeight: 600, borderBottom: `1px solid ${T.border}`,
            position: 'sticky', top: 0, background: T.panel, zIndex: 1,
          }}>
            <div>Дата</div><div>Символ</div><div>Side</div><div>Бот</div>
            <div style={{ textAlign: 'right' }}>P&L</div>
            <div style={{ textAlign: 'right' }}>%</div>
          </div>
          {recent_trades.map((t, i) => {
            const isLong = t.direction === 'long'
            const isTP = t.result === 'tp'
            const d = new Date(t.closed_at)
            const ds = `${String(d.getDate()).padStart(2, '0')}.${String(d.getMonth() + 1).padStart(2, '0')} ${String(d.getHours()).padStart(2, '0')}:${String(d.getMinutes()).padStart(2, '0')}`
            return (
              <div key={t.id} style={{
                display: 'grid', gridTemplateColumns: '78px minmax(0,1.2fr) 40px minmax(0,1fr) 80px 58px',
                padding: '9px 16px', alignItems: 'center', fontSize: 12,
                borderBottom: i === recent_trades.length - 1 ? 'none' : `1px solid ${T.border}`,
              }}>
                <div style={{ ...mono, color: T.dim, fontSize: 11 }}>{ds}</div>
                <div style={{ display: 'flex', alignItems: 'center', gap: 5, minWidth: 0 }}>
                  <span style={{ ...mono, fontWeight: 700, color: T.text, fontSize: 12 }}>{t.symbol}</span>
                  <span style={{
                    padding: '1px 4px', borderRadius: 3, fontSize: 9, fontWeight: 700,
                    background: isTP ? T.greenSoft : T.redSoft,
                    border: `1px solid ${isTP ? T.greenBd : T.redBd}`,
                    color: isTP ? T.green : T.red, textTransform: 'uppercase', flexShrink: 0,
                  }}>{t.result.toUpperCase()}</span>
                </div>
                <div>
                  <span style={{
                    padding: '1px 5px', borderRadius: 3, fontSize: 10, fontWeight: 700,
                    background: isLong ? T.greenSoft : T.redSoft,
                    border: `1px solid ${isLong ? T.greenBd : T.redBd}`,
                    color: isLong ? T.green : T.red, textTransform: 'uppercase',
                  }}>{isLong ? 'L' : 'S'}</span>
                </div>
                <div style={{ fontSize: 11, color: T.body, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
                  {t.bot_name ?? '—'}
                </div>
                <div style={{ textAlign: 'right', ...mono, fontSize: 13, fontWeight: 700, color: (t.pnl ?? 0) >= 0 ? T.green : T.red }}>
                  {t.pnl != null ? ((t.pnl >= 0 ? '+' : '') + fmt$(t.pnl)) : '—'}
                </div>
                <div style={{ textAlign: 'right', ...mono, fontSize: 11, color: (t.pnl_pct ?? 0) >= 0 ? T.green : T.red }}>
                  {t.pnl_pct != null ? fmtPct(t.pnl_pct) : '—'}
                </div>
              </div>
            )
          })}
        </div>
      )}
    </Card>
  )
}

// ─── Bots Leaderboard card (standalone) ──────────────────────────────────────
function BotsCard({ data, period }: { data: DashboardData; period: Period }) {
  const { bot_stats } = data
  const pLabel = PERIODS.find(p => p.id === period)?.label ?? period
  return (
    <Card pad="16px 18px">
      <SHead title="Лидерборд ботов" sub={`P&L за ${pLabel}`} />
      {bot_stats.length === 0 ? (
        <div style={{ textAlign: 'center', padding: '20px 0', color: T.dim, fontSize: 13 }}>Нет ботов</div>
      ) : (
        <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fill,minmax(220px,1fr))', gap: 8 }}>
          {bot_stats.map((b) => {
            const up = b.pnl >= 0
            const isActive = b.status === 'active' || b.status === 'running'
            const wr = b.trades > 0 ? Math.round(b.wins / b.trades * 100) : 0
            return (
              <div key={b.bot_id} style={{
                display: 'grid', gridTemplateColumns: '22px minmax(0,1fr) 80px', gap: 10, alignItems: 'center',
                padding: '8px 10px', background: 'rgba(255,255,255,.02)',
                border: `1px solid ${T.border}`, borderRadius: 10,
              }}>
                <div style={{
                  width: 22, height: 22, borderRadius: 7,
                  background: isActive ? T.greenSoft : 'rgba(255,255,255,.04)',
                  border: `1px solid ${isActive ? T.greenBd : T.border}`,
                  color: isActive ? T.green : T.dim,
                  display: 'flex', alignItems: 'center', justifyContent: 'center', fontSize: 11,
                }}>
                  {isActive ? '⚡' : '⏸'}
                </div>
                <div style={{ minWidth: 0 }}>
                  <div style={{ fontSize: 13, color: T.text, fontWeight: 600, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{b.name}</div>
                  <div style={{ fontSize: 10, color: T.dim, marginTop: 1 }}>{b.trades} сд · WR {wr}%</div>
                </div>
                <div style={{ textAlign: 'right' }}>
                  <div style={{ ...mono, fontSize: 13, fontWeight: 700, color: up ? T.green : T.red }}>{up ? '+' : ''}{fmt$(b.pnl)}</div>
                </div>
              </div>
            )
          })}
        </div>
      )}
    </Card>
  )
}

// ─── Page ─────────────────────────────────────────────────────────────────────
export function DashboardPage() {
  const { selectedAccountId } = useSelectedAccount()
  const [period, setPeriod] = useState<Period>('30d')
  const [animKey, setAnimKey] = useState(0)
  const [data, setData] = useState<DashboardData | null>(null)
  const [equity, setEquity] = useState<number | null>(null)
  const [equityChange, setEquityChange] = useState<{ usd: number; pct: number } | null>(null)
  const [positions, setPositions] = useState<Position[]>([])
  const [accounts, setAccounts] = useState<ExchangeAccount[]>([])
  const [loading, setLoading] = useState(true)
  const [refreshing, setRefreshing] = useState(false)
  const [error, setError] = useState<string | null>(null)

  const loadDash = useCallback(async (p: Period) => {
    try {
      const d = await getDashboard(p)
      // Guard: if the server returned HTML or an unexpected shape, don't crash
      if (!d || typeof d !== 'object' || !d.stats || !Array.isArray(d.daily_pnl)) {
        setError('Бэкенд вернул неожиданный ответ — убедитесь что api-gateway пересобран')
        return
      }
      setData(d)
      setAnimKey(k => k + 1)
      setError(null)
    } catch (e) {
      setError(e instanceof Error ? e.message : 'Ошибка загрузки')
    }
  }, [])

  const loadAccount = useCallback(async (id: string) => {
    if (!id) return
    try {
      const [balRes, posRes] = await Promise.all([
        getAccountBalance(id),
        getAccountPositions(id),
      ])
      if (balRes.ok && balRes.equity != null) {
        setEquity(balRes.equity)
        if (balRes.equity_change_usd != null && balRes.equity_change_percent != null) {
          setEquityChange({ usd: balRes.equity_change_usd, pct: balRes.equity_change_percent })
        }
      }
      if (posRes.ok && posRes.positions) {
        setPositions(posRes.positions.filter(p => parseFloat(p.size || '0') > 0))
      }
    } catch { /* silent */ }
  }, [])

  useEffect(() => {
    setLoading(true)
    Promise.all([
      loadDash(period),
      listAccounts().then(a => setAccounts(a.filter(x => x.is_active))).catch(() => {}),
    ]).finally(() => setLoading(false))
  }, [])

  useEffect(() => { loadDash(period) }, [period, loadDash])
  useEffect(() => { loadAccount(selectedAccountId) }, [selectedAccountId, loadAccount])

  const handleRefresh = async () => {
    setRefreshing(true)
    await Promise.all([loadDash(period), loadAccount(selectedAccountId)])
    setRefreshing(false)
  }

  const selectedAcc = accounts.find(a => a.id === selectedAccountId)
  const accLabel = selectedAcc ? selectedAcc.label : 'Аккаунт не выбран'

  return (
    <div style={{ color: T.body }}>
      {/* Header */}
      <div style={{ display: 'flex', alignItems: 'center', gap: 14, marginBottom: 20, flexWrap: 'wrap' }}>
        <div>
          <h1 style={{ margin: 0, ...grotesk, fontSize: 22, fontWeight: 700, letterSpacing: '-0.5px', color: T.text }}>Дашборд</h1>
          <div style={{ fontSize: 12, color: T.dim, marginTop: 4, display: 'flex', alignItems: 'center', gap: 7 }}>
            <span style={{ width: 6, height: 6, borderRadius: '50%', background: T.green, boxShadow: `0 0 8px ${T.green}`, display: 'inline-block' }} />
            {accLabel}
          </div>
        </div>
        <div style={{ flex: 1 }} />
        <PeriodTabs value={period} onChange={setPeriod} />
        <button onClick={handleRefresh} disabled={refreshing} style={{
          width: 36, height: 36, background: T.panel, border: `1px solid ${T.border}`,
          borderRadius: 10, color: T.body, cursor: 'pointer', display: 'flex',
          alignItems: 'center', justifyContent: 'center',
        }}>
          <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth={1.8} strokeLinecap="round" strokeLinejoin="round"
            style={{ animation: refreshing ? 'spin 1s linear infinite' : 'none' }}>
            <path d="M3 12a9 9 0 1 0 3-6.7" /><path d="M3 4v5h5" />
          </svg>
        </button>
      </div>

      {error && (
        <div style={{ marginBottom: 16, padding: '12px 16px', background: T.redSoft, border: `1px solid ${T.redBd}`, borderRadius: 10, color: T.red, fontSize: 13 }}>
          {error}
        </div>
      )}

      {loading ? (
        <div style={{ textAlign: 'center', padding: '80px 0', color: T.dim }}>
          <svg width="28" height="28" viewBox="0 0 24 24" fill="none" stroke={T.blue} strokeWidth={2} strokeLinecap="round" style={{ animation: 'spin 1s linear infinite' }}>
            <path d="M3 12a9 9 0 1 0 3-6.7" /><path d="M3 4v5h5" />
          </svg>
          <div style={{ marginTop: 12, fontSize: 14 }}>Загрузка данных…</div>
        </div>
      ) : data ? (
        <div key={animKey} style={{ animation: 'dashFadeIn 0.4s ease-out' }}>
          {/* Hero block: left column [HeroCard + KpiStrip], right column [RecentTrades] */}
          <div style={{ display: 'grid', gridTemplateColumns: '7fr 3fr', gap: 14, marginBottom: 18, alignItems: 'stretch' }}>
            {/* Left: HeroCard + KpiStrip stacked */}
            <div style={{ display: 'flex', flexDirection: 'column', gap: 14 }}>
              <HeroCard data={data} period={period} equity={equity} equityChange={equityChange} />
              <DailyStatsStrip data={data} />
            </div>
            {/* Right: Recent Trades spanning full height */}
            <RecentTradesCard data={data} />
          </div>
          {/* Два ряда по три виджета — одна сетка с фиксированной высотой строки */}
          <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr 1fr', gridAutoRows: '360px', gap: 14, marginBottom: 18 }}>
            <PnLCurveCard data={data} />
            <DailyBarsCard data={data} />
            <DrawdownCard data={data} />
            <OpenPositionsCard positions={positions} accountLabel={accLabel} />
            <AssetAllocationCard positions={positions} />
            <BotsCard data={data} period={period} />
          </div>
        </div>
      ) : null}

      <style>{`
        @keyframes spin { to { transform: rotate(360deg); } }
        @keyframes dashFadeIn {
          from { opacity: 0; transform: translateY(12px); }
          to   { opacity: 1; transform: translateY(0); }
        }
      `}</style>
    </div>
  )
}
