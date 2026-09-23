import { useState, useEffect, type CSSProperties, type ReactNode } from 'react'

// Shared visual primitives for the dashboard's "Последние сделки" widget (DashboardPage.tsx)
// and its expanded full-page view (RecentTradesPage.tsx) — split out so the two stay visually
// identical without duplicating the color palette / formatters.

// ─── Colour tokens ────────────────────────────────────────────────────────────
export const T = {
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

export const mono: CSSProperties = { fontFamily: "'JetBrains Mono', monospace" }
export const grotesk: CSSProperties = { fontFamily: "'Space Grotesk', sans-serif" }

// ─── Helpers ──────────────────────────────────────────────────────────────────
export function fmt$(n: number | null | undefined, d = 2): string {
  if (n == null) return '—'
  return (n < 0 ? '-' : '') + '$' + Math.abs(n).toLocaleString('en-US', {
    minimumFractionDigits: d, maximumFractionDigits: d,
  })
}
export function fmtPct(n: number, d = 1): string {
  return (n >= 0 ? '+' : '') + n.toFixed(d) + '%'
}

// ─── Primitives ───────────────────────────────────────────────────────────────
export function Card({ children, pad = '16px 18px', style }: { children: ReactNode; pad?: string; style?: CSSProperties }) {
  return (
    <div style={{ background: T.panel, border: `1px solid ${T.border}`, borderRadius: 14, padding: pad, ...style }}>
      {children}
    </div>
  )
}

export function NoData({ text = 'Нет данных', height = 200 }: { text?: string; height?: number }) {
  return (
    <div style={{ height, display: 'flex', alignItems: 'center', justifyContent: 'center', color: T.dim, fontSize: 13, fontFamily: 'Inter, sans-serif' }}>
      {text}
    </div>
  )
}

/** True below the phone breakpoint (767px), reactive to viewport/orientation changes. */
export function useIsMobile(): boolean {
  const [val, setVal] = useState(() =>
    typeof window !== 'undefined' ? window.matchMedia('(max-width: 767px)').matches : false
  )
  useEffect(() => {
    const mq = window.matchMedia('(max-width: 767px)')
    const h = (e: MediaQueryListEvent) => setVal(e.matches)
    mq.addEventListener('change', h)
    return () => mq.removeEventListener('change', h)
  }, [])
  return val
}
