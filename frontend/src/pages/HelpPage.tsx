import { useState, type CSSProperties } from 'react'
import { BOT_KINDS, BOT_KIND_META } from '../features/bots/botKindMeta'
import type { BotKind } from '../features/bots/types'

/* ─── tokens ─────────────────────────────────────────────────────────────── */
const T = {
  bg: '#0a0d14', panel: '#0c1018', card: 'rgba(255,255,255,.025)',
  border: 'rgba(255,255,255,.07)', borderHi: 'rgba(255,255,255,.12)',
  text: '#f2f5fb', body: '#dde3ef', dim: '#7b8aa6', faint: '#5b6479',
  blue: '#5b8cff', blueBg: 'rgba(91,140,255,.10)', blueBd: 'rgba(91,140,255,.22)',
  green: '#5be0a0', greenBg: 'rgba(65,210,139,.09)', greenBd: 'rgba(65,210,139,.24)',
  orange: '#f7a600', orangeBg: 'rgba(247,166,0,.10)', orangeBd: 'rgba(247,166,0,.28)',
  red: '#fca5a5', redBg: 'rgba(248,113,113,.10)', redBd: 'rgba(248,113,113,.28)',
  yellow: '#facc15', yellowBg: 'rgba(250,204,21,.10)', yellowBd: 'rgba(250,204,21,.28)',
}
const mono: CSSProperties = { fontFamily: "'JetBrains Mono', monospace" }
const grotesk: CSSProperties = { fontFamily: "'Space Grotesk', sans-serif" }

/* ─── icons ──────────────────────────────────────────────────────────────── */
type IP = { s?: number; w?: number; c?: string }
const svg = (p: IP, ch: React.ReactNode) => (
  <svg width={p.s ?? 16} height={p.s ?? 16} viewBox="0 0 24 24" fill="none"
    stroke={p.c ?? 'currentColor'} strokeWidth={p.w ?? 1.8}
    strokeLinecap="round" strokeLinejoin="round" style={{ display: 'block', flexShrink: 0 }}>
    {ch}
  </svg>
)
const IcKey      = (p: IP) => svg(p, <><circle cx="8" cy="15" r="4"/><path d="M10.8 12.2L21 2"/><path d="M16 7l3 3M18 5l3 3"/></>)
const IcBook     = (p: IP) => svg(p, <><path d="M4 19.5A2.5 2.5 0 0 1 6.5 17H20"/><path d="M6.5 2H20v20H6.5A2.5 2.5 0 0 1 4 19.5v-15A2.5 2.5 0 0 1 6.5 2z"/></>)
const IcChev     = (p: IP) => svg(p, <path d="M6 9l6 6 6-6"/>)
const IcChevR    = (p: IP) => svg(p, <path d="M9 6l6 6-6 6"/>)
const IcShield   = (p: IP) => svg(p, <path d="M12 3l8 3v6c0 5-3.5 8.5-8 9-4.5-.5-8-4-8-9V6l8-3z"/>)
const IcAlert    = (p: IP) => svg(p, <><path d="M12 3l10 17H2L12 3z"/><path d="M12 10v4M12 17v.5"/></>)
const IcCheck    = (p: IP) => svg(p, <path d="M5 13l4 4 10-10"/>)
const IcInfo     = (p: IP) => svg(p, <><circle cx="12" cy="12" r="9"/><path d="M12 8h.01M11 12h1v4h1"/></>)
const IcZap      = (p: IP) => svg(p, <path d="M13 3L4 14h7l-1 7 9-11h-7l1-7z"/>)
const IcGlobe    = (p: IP) => svg(p, <><circle cx="12" cy="12" r="9"/><path d="M3 12h18M12 3a14 14 0 0 1 0 18M12 3a14 14 0 0 0 0 18"/></>)
const IcEye      = (p: IP) => svg(p, <><path d="M1 12s4-7 11-7 11 7 11 7-4 7-11 7S1 12 1 12z"/><circle cx="12" cy="12" r="3"/></>)
const IcRefresh  = (p: IP) => svg(p, <><path d="M3 12a9 9 0 1 0 3-6.7"/><path d="M3 4v5h5"/></>)
const IcDot      = (p: IP) => svg({...p, w: 0}, <circle cx="12" cy="12" r="4" fill={p.c ?? 'currentColor'}/>)
const IcLock     = (p: IP) => svg(p, <><rect x="5" y="11" width="14" height="9" rx="2"/><path d="M8 11V8a4 4 0 0 1 8 0v3"/></>)
const IcHash     = (p: IP) => svg(p, <><path d="M4 9h16M4 15h16M10 3l-2 18M16 3l-2 18"/></>)
const IcX        = (p: IP) => svg(p, <path d="M6 6l12 12M18 6L6 18"/>)
const IcCog      = (p: IP) => svg(p, <><circle cx="12" cy="12" r="3"/><path d="M19.4 15a1.65 1.65 0 0 0 .33 1.82l.06.06a2 2 0 0 1-2.83 2.83l-.06-.06a1.65 1.65 0 0 0-1.82-.33 1.65 1.65 0 0 0-1 1.51V21a2 2 0 0 1-4 0v-.09A1.65 1.65 0 0 0 9 19.4a1.65 1.65 0 0 0-1.82.33l-.06.06a2 2 0 0 1-2.83-2.83l.06-.06A1.65 1.65 0 0 0 4.68 15a1.65 1.65 0 0 0-1.51-1H3a2 2 0 0 1 0-4h.09A1.65 1.65 0 0 0 4.6 9a1.65 1.65 0 0 0-.33-1.82l-.06-.06a2 2 0 0 1 2.83-2.83l.06.06A1.65 1.65 0 0 0 9 4.68a1.65 1.65 0 0 0 1-1.51V3a2 2 0 0 1 4 0v.09a1.65 1.65 0 0 0 1 1.51 1.65 1.65 0 0 0 1.82-.33l.06-.06a2 2 0 0 1 2.83 2.83l-.06.06A1.65 1.65 0 0 0 19.4 9a1.65 1.65 0 0 0 1.51 1H21a2 2 0 0 1 0 4h-.09a1.65 1.65 0 0 0-1.51 1z"/></>)
const IcBotHead  = (p: IP) => svg(p, <><rect x="4" y="9" width="16" height="11" rx="3"/><path d="M12 9V5"/><circle cx="12" cy="3.5" r="1.5"/><path d="M9 14.5v1M15 14.5v1"/><path d="M2 13v3M22 13v3"/></>)
const IcTrendUp  = (p: IP) => svg(p, <><path d="M3 17l6-6 4 4 8-8"/><path d="M15 6h6v6"/></>)
const IcRadar    = (p: IP) => svg(p, <><circle cx="11" cy="11" r="7"/><path d="M21 21l-4.3-4.3"/></>)
const IcLayers   = (p: IP) => svg(p, <><path d="M12 3l9 5-9 5-9-5 9-5z"/><path d="M3 13l9 5 9-5"/></>)
const IcMerge    = (p: IP) => svg(p, <><path d="M6 3v9a4 4 0 0 0 4 4h4"/><path d="M18 3v9a4 4 0 0 1-4 4"/><circle cx="6" cy="3" r="0"/><circle cx="18" cy="3" r="2.2"/><circle cx="6" cy="3" r="2.2"/></>)
const IcSend     = (p: IP) => svg(p, <><path d="M22 2L11 13"/><path d="M22 2l-7 20-4-9-9-4 20-7z"/></>)
const IcUsers    = (p: IP) => svg(p, <><circle cx="9" cy="8" r="3.2"/><path d="M2.5 20a6.5 6.5 0 0 1 13 0"/><path d="M16.5 6.2a3.2 3.2 0 0 1 0 6.2"/><path d="M18.5 14.3A6.5 6.5 0 0 1 21.5 20"/></>)
const IcCopy     = (p: IP) => svg(p, <><rect x="8.5" y="8.5" width="12" height="12" rx="2.2"/><path d="M5 15.5H4a1.5 1.5 0 0 1-1.5-1.5V4a1.5 1.5 0 0 1 1.5-1.5h10A1.5 1.5 0 0 1 15.5 4v1"/></>)
const IcClock    = (p: IP) => svg(p, <><circle cx="12" cy="12" r="9"/><path d="M12 7v5l3.2 2"/></>)
const IcPlay     = (p: IP) => svg(p, <path d="M6 4l14 8-14 8V4z"/>)
const IcPause    = (p: IP) => svg(p, <><rect x="6" y="4" width="4" height="16" rx="1"/><rect x="14" y="4" width="4" height="16" rx="1"/></>)

/* ─── sidebar nav items ───────────────────────────────────────────────────── */
type TopicId = 'keys' | 'bots'

const KEY_SECTIONS = [
  { id: 'overview',    label: 'Обзор страницы' },
  { id: 'add-key',     label: 'Добавление ключа' },
  { id: 'key-card',    label: 'Карточка ключа' },
  { id: 'statuses',    label: 'Статусы' },
  { id: 'audit',       label: 'Вкладка Аудит' },
  { id: 'security',    label: 'Безопасность' },
  { id: 'faq',         label: 'Частые вопросы' },
]

const BOT_SECTIONS = [
  { id: 'bots-overview',  label: 'Обзор страницы' },
  { id: 'bots-kinds',     label: 'Типы ботов' },
  { id: 'bots-creating',  label: 'Создание бота' },
  { id: 'bots-library',   label: 'Библиотека' },
  { id: 'bots-manage',    label: 'Управление и лимиты' },
  { id: 'bots-publish',   label: 'Публикация' },
  { id: 'bots-faq',       label: 'Частые вопросы' },
]

const TOPICS: { id: TopicId; label: string; Icon: (p: IP) => React.ReactElement; sections: { id: string; label: string }[] }[] = [
  { id: 'keys', label: 'API ключи', Icon: IcKey,     sections: KEY_SECTIONS },
  { id: 'bots', label: 'Боты',      Icon: IcBotHead, sections: BOT_SECTIONS },
]

/* ─── shared primitives ───────────────────────────────────────────────────── */
function Badge({ color, bg, bd, children }: { color: string; bg: string; bd: string; children: React.ReactNode }) {
  return (
    <span style={{
      display: 'inline-flex', alignItems: 'center', gap: 5,
      padding: '3px 9px', borderRadius: 99, border: `1px solid ${bd}`, background: bg,
      fontSize: 11, fontWeight: 600, color,
    }}>{children}</span>
  )
}

function Section({ id, title, subtitle, children }: {
  id: string; title: string; subtitle?: string; children: React.ReactNode
}) {
  return (
    <section id={id} style={{ marginBottom: 56 }}>
      <div style={{ marginBottom: 20, paddingBottom: 14, borderBottom: `1px solid ${T.border}` }}>
        <h2 style={{ ...grotesk, fontSize: 22, fontWeight: 800, color: T.text, margin: '0 0 4px', letterSpacing: -0.5 }}>
          {title}
        </h2>
        {subtitle && <p style={{ margin: 0, fontSize: 13.5, color: T.dim, lineHeight: 1.5 }}>{subtitle}</p>}
      </div>
      {children}
    </section>
  )
}

function Callout({ kind, title, children }: { kind: 'info' | 'warn' | 'tip' | 'danger'; title?: string; children: React.ReactNode }) {
  const map = {
    info:   { color: T.blue,   bg: T.blueBg,   bd: T.blueBd,   Icon: IcInfo },
    warn:   { color: T.orange, bg: T.orangeBg,  bd: T.orangeBd, Icon: IcAlert },
    tip:    { color: T.green,  bg: T.greenBg,   bd: T.greenBd,  Icon: IcCheck },
    danger: { color: T.red,    bg: T.redBg,     bd: T.redBd,    Icon: IcAlert },
  }[kind]
  const { Icon } = map
  return (
    <div style={{
      display: 'flex', gap: 12, padding: '12px 16px',
      background: map.bg, border: `1px solid ${map.bd}`, borderRadius: 10,
      marginBottom: 14,
    }}>
      <div style={{ flexShrink: 0, marginTop: 1 }}><Icon s={15} c={map.color} w={2}/></div>
      <div>
        {title && <div style={{ fontSize: 13, fontWeight: 700, color: map.color, marginBottom: 3 }}>{title}</div>}
        <div style={{ fontSize: 13, color: T.body, lineHeight: 1.6 }}>{children}</div>
      </div>
    </div>
  )
}

function StepList({ steps }: { steps: { title: string; desc: string }[] }) {
  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 0, background: T.panel, border: `1px solid ${T.border}`, borderRadius: 12, overflow: 'hidden' }}>
      {steps.map((s, i) => (
        <div key={i} style={{
          display: 'flex', gap: 14, padding: '14px 16px',
          borderBottom: i < steps.length - 1 ? `1px solid ${T.border}` : 'none',
        }}>
          <div style={{
            flexShrink: 0, width: 22, height: 22, borderRadius: 6, marginTop: 1,
            background: 'linear-gradient(135deg,rgba(91,140,255,.3),rgba(193,77,255,.2))',
            border: '1px solid rgba(123,140,255,.3)', color: '#b8c8ff',
            fontSize: 11, fontWeight: 700, ...mono,
            display: 'flex', alignItems: 'center', justifyContent: 'center',
          }}>{i + 1}</div>
          <div>
            <div style={{ fontSize: 13.5, fontWeight: 600, color: T.text }}>{s.title}</div>
            <div style={{ fontSize: 12.5, color: T.dim, marginTop: 2, lineHeight: 1.5 }}>{s.desc}</div>
          </div>
        </div>
      ))}
    </div>
  )
}

/* ─── Mockup components (visual diagrams) ────────────────────────────────── */

// Top-level layout diagram
function MockupPageLayout() {
  return (
    <div style={{
      background: '#080b12', border: `1px solid ${T.border}`, borderRadius: 14,
      padding: 16, fontFamily: 'inherit', overflow: 'hidden',
    }}>
      {/* header */}
      <div style={{
        display: 'flex', alignItems: 'center', gap: 12, marginBottom: 12,
        padding: '10px 14px', background: T.panel, borderRadius: 10, border: `1px solid ${T.border}`,
      }}>
        <div style={{ width: 24, height: 24, borderRadius: 6, background: 'rgba(91,140,255,.2)', border: '1px solid rgba(91,140,255,.3)', display: 'flex', alignItems: 'center', justifyContent: 'center', color: '#b8c8ff' }}>
          <IcKey s={12} w={2}/>
        </div>
        <div>
          <div style={{ ...grotesk, fontSize: 14, fontWeight: 700, color: T.text }}>API ключи</div>
          <div style={{ fontSize: 10, color: T.dim }}>Подключите ключ, созданный на бирже</div>
        </div>
        <div style={{ marginLeft: 'auto', display: 'flex', alignItems: 'center', gap: 8 }}>
          <IcShield s={13} c={T.green}/><span style={{ fontSize: 10, color: T.green, fontWeight: 600 }}>AES-256 · IP whitelist</span>
        </div>
      </div>
      {/* stats strip */}
      <div style={{ display: 'grid', gridTemplateColumns: 'repeat(4, 1fr)', gap: 8, marginBottom: 12 }}>
        {[
          { label: 'Всего ключей', val: '3', sub: '2 биржи' },
          { label: 'Активных', val: '2', sub: 'торгуют сейчас', c: T.green },
          { label: 'Требуют внимания', val: '1', sub: 'истекает', c: T.orange },
          { label: 'Совокупный equity', val: '$4 200', sub: 'по счетам' },
        ].map(s => (
          <div key={s.label} style={{ background: T.card, border: `1px solid ${T.border}`, borderRadius: 8, padding: '8px 10px' }}>
            <div style={{ fontSize: 9, color: T.dim, textTransform: 'uppercase', letterSpacing: 1 }}>{s.label}</div>
            <div style={{ fontSize: 14, fontWeight: 700, color: (s as any).c ?? T.text, marginTop: 2 }}>{s.val}</div>
            <div style={{ fontSize: 9, color: T.faint }}>{s.sub}</div>
          </div>
        ))}
      </div>
      {/* 2-column: list + add panel */}
      <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 10 }}>
        {/* key list */}
        <div style={{ display: 'flex', flexDirection: 'column', gap: 8 }}>
          <div style={{ fontSize: 9, color: T.dim, textTransform: 'uppercase', letterSpacing: 1, paddingLeft: 2 }}>Список ключей</div>
          {[
            { ex: '#f7a600', name: 'main · Bybit', status: 'активен', sc: T.green, eq: '$2 800' },
            { ex: '#f7a600', name: 'test · Bybit', status: 'истекает', sc: T.orange, eq: '$1 400' },
            { ex: '#3fa6ff', name: 'mexc-1', status: 'ошибка', sc: T.red, eq: '—' },
          ].map(k => (
            <div key={k.name} style={{
              display: 'flex', alignItems: 'center', gap: 8, padding: '8px 10px',
              background: T.panel, border: `1px solid ${T.border}`, borderRadius: 8,
            }}>
              <div style={{ width: 22, height: 22, borderRadius: 5, background: k.ex, flexShrink: 0 }}/>
              <div style={{ flex: 1, minWidth: 0 }}>
                <div style={{ fontSize: 11, fontWeight: 600, color: T.text }}>{k.name}</div>
                <div style={{ fontSize: 9, color: k.sc, marginTop: 1 }}>● {k.status}</div>
              </div>
              <div style={{ fontSize: 11, fontWeight: 700, color: T.text }}>{k.eq}</div>
              <div style={{ color: T.faint }}><IcChev s={12}/></div>
            </div>
          ))}
        </div>
        {/* add panel */}
        <div style={{ background: T.panel, border: `1px solid ${T.border}`, borderRadius: 8, padding: '10px 12px' }}>
          <div style={{ fontSize: 9, color: T.dim, textTransform: 'uppercase', letterSpacing: 1, marginBottom: 8 }}>Подключить новый ключ</div>
          {['1 Биржа', '2 Название', '3 Ключ и секрет', '4 IP-whitelist'].map(s => (
            <div key={s} style={{ display: 'flex', alignItems: 'center', gap: 8, marginBottom: 6 }}>
              <div style={{ width: 14, height: 14, borderRadius: 4, background: 'rgba(91,140,255,.2)', border: '1px solid rgba(91,140,255,.3)', fontSize: 8, color: '#b8c8ff', display: 'flex', alignItems: 'center', justifyContent: 'center', fontWeight: 700, flexShrink: 0 }}>{s[0]}</div>
              <div style={{ fontSize: 10, color: T.dim }}>{s.slice(2)}</div>
            </div>
          ))}
          <div style={{ marginTop: 10, display: 'flex', gap: 6 }}>
            <div style={{ flex: 1, height: 26, borderRadius: 6, background: 'rgba(255,255,255,.04)', border: `1px solid ${T.border}`, display: 'flex', alignItems: 'center', justifyContent: 'center', fontSize: 9, color: T.dim }}>Проверить ключ</div>
            <div style={{ flex: 1, height: 26, borderRadius: 6, background: 'rgba(91,140,255,.15)', border: '1px solid rgba(91,140,255,.3)', display: 'flex', alignItems: 'center', justifyContent: 'center', fontSize: 9, color: '#b8c8ff', fontWeight: 600 }}>Сохранить ключ</div>
          </div>
        </div>
      </div>
    </div>
  )
}

// Key card expanded
function MockupKeyCard() {
  return (
    <div style={{
      background: 'linear-gradient(180deg, rgba(91,140,255,.05), rgba(255,255,255,.02))',
      border: '1px solid rgba(91,140,255,.25)', borderRadius: 12, overflow: 'hidden',
    }}>
      {/* card header */}
      <div style={{ display: 'flex', alignItems: 'center', gap: 12, padding: '12px 14px' }}>
        <div style={{ width: 32, height: 32, borderRadius: 8, background: '#f7a600', display: 'flex', alignItems: 'center', justifyContent: 'center', flexShrink: 0 }}>
          <span style={{ fontSize: 10, fontWeight: 700, color: '#1a0900' }}>BY</span>
        </div>
        <div style={{ flex: 1 }}>
          <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
            <span style={{ fontSize: 13.5, fontWeight: 700, color: T.text }}>main · Bybit</span>
            <Badge color={T.green} bg={T.greenBg} bd={T.greenBd}><IcDot s={8} c={T.green}/>активен</Badge>
          </div>
          <div style={{ marginTop: 4, display: 'flex', gap: 10, fontSize: 10, color: T.dim }}>
            <span style={mono}>•••• •••• •••• ••••</span>
            <span style={{ display: 'flex', alignItems: 'center', gap: 4 }}><IcGlobe s={9} c={T.faint}/> 185.94.32.1</span>
          </div>
        </div>
        <div style={{ textAlign: 'right' }}>
          <div style={{ fontSize: 9, color: T.dim, textTransform: 'uppercase', letterSpacing: 1 }}>Equity</div>
          <div style={{ fontSize: 16, fontWeight: 700, color: T.text }}>$2 800</div>
        </div>
      </div>
      {/* expanded body */}
      <div style={{ borderTop: `1px solid ${T.border}`, padding: '10px 14px 14px' }}>
        {/* perms */}
        <div style={{ display: 'flex', gap: 8, marginBottom: 12, flexWrap: 'wrap' }}>
          {[
            { label: 'Чтение', on: true, warn: false },
            { label: 'Торговля', on: true, warn: false },
            { label: 'Деривативы', on: true, warn: false },
            { label: 'Вывод', on: false, warn: true },
          ].map(p => (
            <span key={p.label} style={{
              display: 'inline-flex', alignItems: 'center', gap: 4, padding: '3px 8px',
              borderRadius: 99, fontSize: 10, fontWeight: 600,
              color: p.on ? (p.warn ? T.red : T.green) : T.faint,
              background: p.on ? (p.warn ? T.redBg : T.greenBg) : 'rgba(255,255,255,.03)',
              border: `1px solid ${p.on ? (p.warn ? T.redBd : T.greenBd) : T.border}`,
              opacity: p.on ? 1 : 0.5,
            }}>
              {p.on ? <IcCheck s={9} w={2.6} c={p.warn ? T.red : T.green}/> : <IcX s={9} w={2.2} c={T.faint}/>}
              {p.label}
            </span>
          ))}
        </div>
        {/* details grid */}
        <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr 1fr', gap: 1, background: T.border, borderRadius: 8, overflow: 'hidden', marginBottom: 12 }}>
          {[
            { label: 'API key', val: '•••• ••••' },
            { label: 'Создан', val: '12 янв. 2025' },
            { label: 'Латентность', val: '84 ms' },
          ].map(d => (
            <div key={d.label} style={{ background: T.panel, padding: '8px 10px' }}>
              <div style={{ fontSize: 9, color: T.dim, textTransform: 'uppercase', letterSpacing: 1 }}>{d.label}</div>
              <div style={{ fontSize: 11, color: T.body, marginTop: 2, ...mono }}>{d.val}</div>
            </div>
          ))}
        </div>
        {/* tabs */}
        <div style={{ display: 'flex', gap: 2, padding: 3, background: 'rgba(0,0,0,.25)', border: `1px solid ${T.border}`, borderRadius: 7, width: 'fit-content', marginBottom: 10 }}>
          {['Активность', 'Аудит ключа', 'Боты · 0'].map((t, i) => (
            <div key={t} style={{
              padding: '5px 10px', borderRadius: 5, fontSize: 10, fontWeight: 600,
              background: i === 1 ? 'rgba(123,140,255,.18)' : 'transparent',
              color: i === 1 ? T.text : T.dim,
            }}>{t}</div>
          ))}
        </div>
        {/* audit tab content preview */}
        <div style={{ display: 'flex', flexDirection: 'column', gap: 6 }}>
          {[
            { icon: IcEye, title: 'Чтение', desc: 'Баланс, ордера, история', on: true, warn: false },
            { icon: IcZap, title: 'Торговля', desc: 'Спот, маржа — открытие ордеров', on: true, warn: false },
            { icon: IcShield, title: 'Вывод средств', desc: 'Перевод на внешний адрес', on: false, warn: true },
          ].map(p => {
            const Ic = p.icon
            const color = p.on ? (p.warn ? T.red : T.green) : T.dim
            return (
              <div key={p.title} style={{
                display: 'flex', alignItems: 'center', gap: 10, padding: '8px 10px',
                background: p.on ? (p.warn ? T.redBg : T.greenBg) : T.card,
                border: `1px solid ${p.on ? (p.warn ? T.redBd : T.greenBd) : T.border}`, borderRadius: 8,
              }}>
                <div style={{ width: 24, height: 24, borderRadius: 6, display: 'flex', alignItems: 'center', justifyContent: 'center', background: p.on ? (p.warn ? T.redBg : T.greenBg) : 'rgba(255,255,255,.04)', border: `1px solid ${p.on ? (p.warn ? T.redBd : T.greenBd) : T.border}`, color }}>
                  <Ic s={12} w={2}/>
                </div>
                <div style={{ flex: 1 }}>
                  <div style={{ fontSize: 11, fontWeight: 600, color: p.on ? T.text : T.dim }}>{p.title}</div>
                  <div style={{ fontSize: 9, color: T.faint }}>{p.desc}</div>
                </div>
                <span style={{ fontSize: 10, fontWeight: 700, color, padding: '2px 7px', borderRadius: 99, background: p.on ? (p.warn ? T.redBg : T.greenBg) : 'rgba(255,255,255,.04)', border: `1px solid ${p.on ? (p.warn ? T.redBd : T.greenBd) : T.border}` }}>
                  {p.on ? 'Есть' : 'Нет'}
                </span>
              </div>
            )
          })}
        </div>
        {/* action buttons */}
        <div style={{ display: 'flex', gap: 8, marginTop: 12 }}>
          <div style={{ display: 'flex', alignItems: 'center', gap: 5, padding: '7px 12px', background: 'linear-gradient(180deg,#4a7dff,#3a67e6)', borderRadius: 7, fontSize: 10, fontWeight: 600, color: '#fff' }}>
            <IcRefresh s={10}/> Тест подключения
          </div>
          <div style={{ display: 'flex', alignItems: 'center', gap: 5, padding: '7px 10px', background: 'rgba(255,255,255,.04)', border: `1px solid ${T.border}`, borderRadius: 7, fontSize: 10, color: T.body }}>
            <IcGlobe s={10}/> Изменить IP
          </div>
          <div style={{ marginLeft: 'auto', display: 'flex', alignItems: 'center', gap: 5, padding: '7px 10px', background: T.redBg, border: `1px solid ${T.redBd}`, borderRadius: 7, fontSize: 10, color: T.red }}>
            Удалить
          </div>
        </div>
      </div>
    </div>
  )
}

// Add key form mockup
function MockupAddKeyForm() {
  return (
    <div style={{ background: T.panel, border: `1px solid ${T.border}`, borderRadius: 12, overflow: 'hidden' }}>
      <div style={{ padding: '10px 14px', borderBottom: `1px solid ${T.border}`, display: 'flex', alignItems: 'center', gap: 8, background: 'rgba(91,140,255,.04)' }}>
        <div style={{ width: 22, height: 22, borderRadius: 6, background: 'rgba(91,140,255,.25)', border: '1px solid rgba(91,140,255,.3)', display: 'flex', alignItems: 'center', justifyContent: 'center', color: '#b8c8ff' }}><IcHash s={11} w={2}/></div>
        <div style={{ ...grotesk, fontSize: 12, fontWeight: 700, color: T.text }}>Подключить новый ключ</div>
      </div>
      <div style={{ padding: '12px 14px', display: 'flex', flexDirection: 'column', gap: 12 }}>
        {/* step 1 — exchange */}
        <div>
          <div style={{ display: 'flex', alignItems: 'center', gap: 6, marginBottom: 6 }}>
            <div style={{ width: 16, height: 16, borderRadius: 4, background: 'rgba(91,140,255,.2)', border: '1px solid rgba(91,140,255,.3)', fontSize: 9, fontWeight: 700, color: '#b8c8ff', display: 'flex', alignItems: 'center', justifyContent: 'center' }}>1</div>
            <span style={{ fontSize: 11, fontWeight: 600, color: T.text }}>Биржа</span>
          </div>
          <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 6 }}>
            {[
              { name: 'Bybit', color: '#f7a600', sel: true },
              { name: 'MEXC', color: '#3fa6ff', sel: false },
            ].map(e => (
              <div key={e.name} style={{
                display: 'flex', flexDirection: 'column', alignItems: 'center', gap: 4, padding: '8px',
                background: e.sel ? 'rgba(91,140,255,.15)' : 'rgba(255,255,255,.02)',
                border: `1px solid ${e.sel ? 'rgba(91,140,255,.35)' : T.border}`, borderRadius: 8,
              }}>
                <div style={{ width: 18, height: 18, borderRadius: 4, background: e.color }}/>
                <span style={{ fontSize: 9, fontWeight: 600, color: e.sel ? T.text : T.dim }}>{e.name}</span>
              </div>
            ))}
          </div>
        </div>
        {/* step 3 — keys */}
        <div>
          <div style={{ display: 'flex', alignItems: 'center', gap: 6, marginBottom: 6 }}>
            <div style={{ width: 16, height: 16, borderRadius: 4, background: 'rgba(91,140,255,.2)', border: '1px solid rgba(91,140,255,.3)', fontSize: 9, fontWeight: 700, color: '#b8c8ff', display: 'flex', alignItems: 'center', justifyContent: 'center' }}>3</div>
            <span style={{ fontSize: 11, fontWeight: 600, color: T.text }}>Ключ и секрет</span>
          </div>
          <div style={{ display: 'flex', flexDirection: 'column', gap: 6 }}>
            <div style={{ fontSize: 9, color: T.dim, fontWeight: 600 }}>API key</div>
            <div style={{ padding: '7px 10px', background: 'rgba(0,0,0,.3)', border: `1px solid ${T.border}`, borderRadius: 6, fontSize: 10, ...mono, color: T.faint }}>вставьте публичный ключ</div>
            <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between' }}>
              <span style={{ fontSize: 9, color: T.dim, fontWeight: 600 }}>API secret</span>
              <span style={{ fontSize: 9, color: T.faint, display: 'flex', alignItems: 'center', gap: 3 }}><IcLock s={9}/>хранится в AES-256</span>
            </div>
            <div style={{ padding: '7px 10px', background: 'rgba(0,0,0,.3)', border: `1px solid ${T.border}`, borderRadius: 6, fontSize: 10, ...mono, color: T.faint }}>••••••••••••••••••••</div>
          </div>
        </div>
        {/* test + save buttons */}
        <div style={{ display: 'flex', gap: 6 }}>
          <div style={{ flex: 1, padding: '8px', background: T.greenBg, border: `1px solid ${T.greenBd}`, borderRadius: 7, fontSize: 10, fontWeight: 600, color: T.green, textAlign: 'center', display: 'flex', alignItems: 'center', justifyContent: 'center', gap: 4 }}>
            <IcCheck s={10} w={2.4}/> Подключение успешно
          </div>
          <div style={{ flex: 1, padding: '8px', background: 'linear-gradient(180deg,#4a7dff,#3a67e6)', borderRadius: 7, fontSize: 10, fontWeight: 700, color: '#fff', textAlign: 'center' }}>
            Сохранить ключ
          </div>
        </div>
      </div>
    </div>
  )
}

/* ─── Bot kind icons ─────────────────────────────────────────────────────── */
const BOT_KIND_ICONS: Record<BotKind, (p: IP) => React.ReactElement> = {
  signal: IcTrendUp,
  parser: IcRadar,
  hedge:  IcShield,
  matrix: IcLayers,
  multi:  IcMerge,
}

/* ─── Bots mockups (visual diagrams) ─────────────────────────────────────── */

function MiniBotCard({ kind, name, status, sub }: { kind: BotKind; name: string; status: 'running' | 'paused' | 'stopped'; sub: string }) {
  const km = BOT_KIND_META[kind]
  const Ic = BOT_KIND_ICONS[kind]
  const st = {
    running: { c: T.green,  bg: T.greenBg,  bd: T.greenBd,  label: 'Запущен' },
    paused:  { c: T.orange, bg: T.orangeBg, bd: T.orangeBd, label: 'Пауза' },
    stopped: { c: T.dim,    bg: 'rgba(255,255,255,.05)', bd: T.border, label: 'Остановлен' },
  }[status]
  return (
    <div style={{ background: T.panel, border: `1px solid ${T.border}`, borderRadius: 9, overflow: 'hidden' }}>
      <div style={{ padding: '8px 10px', background: km.bgHeader, display: 'flex', alignItems: 'center', gap: 8 }}>
        <div style={{ width: 22, height: 22, borderRadius: 6, background: km.iconBg, border: `1px solid ${km.border}`, display: 'flex', alignItems: 'center', justifyContent: 'center', color: km.color, flexShrink: 0 }}>
          <Ic s={11} w={2}/>
        </div>
        <div style={{ flex: 1, minWidth: 0 }}>
          <div style={{ fontSize: 11, fontWeight: 700, color: T.text }}>{name}</div>
          <div style={{ fontSize: 9, color: km.color }}>{km.label}</div>
        </div>
        <Badge color={st.c} bg={st.bg} bd={st.bd}><IcDot s={7} c={st.c}/>{st.label}</Badge>
      </div>
      <div style={{ padding: '7px 10px', fontSize: 9.5, color: T.dim }}>{sub}</div>
    </div>
  )
}

function MiniLibraryCard({ kind, name, author, price, users }: { kind: BotKind; name: string; author: string; price: string; users: string }) {
  const km = BOT_KIND_META[kind]
  const Ic = BOT_KIND_ICONS[kind]
  return (
    <div style={{ background: T.panel, border: `1px solid ${km.border}`, borderRadius: 9, overflow: 'hidden' }}>
      <div style={{ padding: '8px 10px', background: km.bgHeader }}>
        <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
          <div style={{ width: 22, height: 22, borderRadius: 6, background: km.iconBg, border: `1px solid ${km.border}`, display: 'flex', alignItems: 'center', justifyContent: 'center', color: km.color, flexShrink: 0 }}>
            <Ic s={11} w={2}/>
          </div>
          <div style={{ flex: 1, minWidth: 0 }}>
            <div style={{ fontSize: 11, fontWeight: 700, color: T.text }}>{name}</div>
            <div style={{ fontSize: 9, color: T.dim }}>от {author}</div>
          </div>
          <span style={{ fontSize: 9, fontWeight: 700, color: price === 'Free' ? T.green : T.orange, padding: '2px 6px', borderRadius: 99, background: price === 'Free' ? T.greenBg : T.orangeBg, border: `1px solid ${price === 'Free' ? T.greenBd : T.orangeBd}` }}>{price}</span>
        </div>
      </div>
      <div style={{ padding: '7px 10px', display: 'flex', alignItems: 'center', gap: 6 }}>
        <span style={{ fontSize: 9.5, color: T.dim, display: 'flex', alignItems: 'center', gap: 3 }}><IcUsers s={10} c={T.faint}/>{users} активных</span>
        <div style={{ flex: 1 }} />
        <span style={{ fontSize: 9, fontWeight: 600, color: '#a0b8ff', background: 'rgba(91,140,255,.12)', border: '1px solid rgba(91,140,255,.3)', borderRadius: 6, padding: '3px 7px', display: 'flex', alignItems: 'center', gap: 3 }}><IcCopy s={9} w={2}/>В свои боты</span>
      </div>
    </div>
  )
}

// Top-level "Боты" page layout diagram
function MockupBotsPageLayout() {
  return (
    <div style={{ background: '#080b12', border: `1px solid ${T.border}`, borderRadius: 14, padding: 16, overflow: 'hidden' }}>
      {/* header */}
      <div style={{ display: 'flex', alignItems: 'center', gap: 12, marginBottom: 14, padding: '10px 14px', background: T.panel, borderRadius: 10, border: `1px solid ${T.border}` }}>
        <div style={{ width: 24, height: 24, borderRadius: 6, background: 'rgba(91,140,255,.2)', border: '1px solid rgba(91,140,255,.3)', display: 'flex', alignItems: 'center', justifyContent: 'center', color: '#b8c8ff' }}>
          <IcBotHead s={13} w={2}/>
        </div>
        <div>
          <div style={{ ...grotesk, fontSize: 14, fontWeight: 700, color: T.text }}>Боты</div>
          <div style={{ fontSize: 10, color: T.dim }}>Запускай готовые стратегии или создавай свои</div>
        </div>
        <div style={{ marginLeft: 'auto' }}>
          <Badge color={T.green} bg={T.greenBg} bd={T.greenBd}><IcDot s={7} c={T.green}/>2 активных из 3</Badge>
        </div>
      </div>

      {/* Мои боты */}
      <div style={{ marginBottom: 14 }}>
        <div style={{ display: 'flex', alignItems: 'center', gap: 8, marginBottom: 8 }}>
          <span style={{ fontSize: 11, fontWeight: 700, color: T.text }}>Мои боты</span>
          <span style={{ fontSize: 10, color: T.faint, ...mono }}>3</span>
          <div style={{ flex: 1 }} />
          <span style={{ fontSize: 9, color: T.dim, border: `1px solid ${T.border}`, borderRadius: 6, padding: '3px 7px' }}>Экспорт</span>
          <span style={{ fontSize: 9, fontWeight: 700, color: '#fff', background: 'linear-gradient(180deg,#4a7dff,#3a67e6)', borderRadius: 6, padding: '3px 8px' }}>+ Создать своего бота</span>
        </div>
        <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr 1fr', gap: 8 }}>
          <MiniBotCard kind="signal" name="BTC Grid Pro" status="running" sub="12 монет · 4 сделки сегодня"/>
          <MiniBotCard kind="hedge"  name="ETH Hedge"    status="running" sub="Хеджирует 3 стратегии"/>
          <MiniBotCard kind="matrix" name="SOL Matrix"   status="paused"  sub="Custom · на паузе"/>
        </div>
      </div>

      {/* Библиотека */}
      <div>
        <div style={{ display: 'flex', alignItems: 'center', gap: 8, marginBottom: 8 }}>
          <span style={{ fontSize: 11, fontWeight: 700, color: T.text }}>Библиотека ботов</span>
          <span style={{ fontSize: 10, color: T.faint }}>Готовые стратегии от NovaBot и сообщества</span>
        </div>
        <div style={{ display: 'flex', gap: 6, marginBottom: 8, flexWrap: 'wrap' }}>
          {['Бот', 'Стратегия', 'Риск', 'Режим', 'Цена'].map(f => (
            <span key={f} style={{ fontSize: 9, color: T.faint, border: `1px solid ${T.border}`, borderRadius: 6, padding: '3px 8px' }}>{f} ▾</span>
          ))}
        </div>
        <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 8 }}>
          <MiniLibraryCard kind="signal" name="Trend Rider" author="NovaBot" price="Free" users="1 240"/>
          <MiniLibraryCard kind="multi"  name="Hedge Combo" author="community" price="$9/мес" users="86"/>
        </div>
      </div>
    </div>
  )
}

// Bot type picker mockup
function MockupBotTypePicker() {
  return (
    <div style={{ background: T.panel, border: `1px solid ${T.border}`, borderRadius: 12, overflow: 'hidden' }}>
      <div style={{ padding: '10px 14px', borderBottom: `1px solid ${T.border}` }}>
        <div style={{ ...grotesk, fontSize: 12, fontWeight: 700, color: T.text }}>Выберите тип бота</div>
        <div style={{ fontSize: 10, color: T.dim, marginTop: 2 }}>Тип определяет логику работы. Настройки можно изменить позже</div>
      </div>
      <div style={{ padding: 12, display: 'grid', gridTemplateColumns: 'repeat(4, 1fr)', gap: 8 }}>
        {BOT_KINDS.map(kind => {
          const km = BOT_KIND_META[kind]
          const Ic = BOT_KIND_ICONS[kind]
          return (
            <div key={kind} style={{
              borderRadius: 9, border: `1px solid ${km.border}`, overflow: 'hidden',
              opacity: km.disabled ? 0.45 : 1,
            }}>
              <div style={{ padding: '8px 8px 7px', background: km.bgHeader, position: 'relative' }}>
                {km.disabled && (
                  <span style={{ position: 'absolute', right: 6, top: 6, fontSize: 7, fontWeight: 700, color: km.color, background: km.iconBg, border: `1px solid ${km.border}`, borderRadius: 4, padding: '1px 4px', textTransform: 'uppercase' }}>Скоро</span>
                )}
                <div style={{ width: 22, height: 22, borderRadius: 7, background: km.iconBg, border: `1px solid ${km.border}`, display: 'flex', alignItems: 'center', justifyContent: 'center', color: km.color, marginBottom: 6 }}>
                  <Ic s={11} w={2}/>
                </div>
                <div style={{ fontSize: 10.5, fontWeight: 700, color: km.disabled ? T.faint : T.text }}>{km.label}</div>
                <div style={{ fontSize: 8.5, color: T.faint, marginTop: 1 }}>{km.tagline}</div>
              </div>
            </div>
          )
        })}
      </div>
    </div>
  )
}

// Bot creation form tabs mockup
function MockupBotFormTabs() {
  return (
    <div style={{ background: T.panel, border: `1px solid ${T.border}`, borderRadius: 12, overflow: 'hidden' }}>
      <div style={{ padding: '10px 14px', borderBottom: `1px solid ${T.border}`, display: 'flex', alignItems: 'center', gap: 8, background: 'rgba(91,140,255,.04)' }}>
        <div style={{ width: 22, height: 22, borderRadius: 6, background: 'rgba(91,140,255,.25)', border: '1px solid rgba(91,140,255,.3)', display: 'flex', alignItems: 'center', justifyContent: 'center', color: '#b8c8ff' }}><IcBotHead s={11} w={2}/></div>
        <div style={{ ...grotesk, fontSize: 12, fontWeight: 700, color: T.text }}>Создать бота</div>
      </div>
      {/* outer tabs */}
      <div style={{ display: 'flex', gap: 14, padding: '0 14px', borderBottom: `1px solid ${T.border}` }}>
        {['Основное', 'Активация', 'Стратегия'].map((t, i) => (
          <div key={t} style={{ padding: '8px 0', fontSize: 10.5, fontWeight: 600, color: i === 2 ? T.text : T.dim, borderBottom: i === 2 ? '2px solid #5b8cff' : '2px solid transparent' }}>{t}</div>
        ))}
      </div>
      <div style={{ padding: 14, display: 'flex', flexDirection: 'column', gap: 10 }}>
        {/* strategy type toggle */}
        <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
          <span style={{ fontSize: 9, color: T.dim, textTransform: 'uppercase', letterSpacing: 1 }}>Тип стратегии</span>
          <div style={{ display: 'flex', gap: 4 }}>
            <span style={{ fontSize: 10, fontWeight: 700, color: '#a0b8ff', background: 'rgba(91,140,255,.18)', border: '1px solid rgba(91,140,255,.4)', borderRadius: 6, padding: '3px 10px' }}>Grid</span>
            <span style={{ fontSize: 10, color: T.dim, border: `1px solid ${T.border}`, borderRadius: 6, padding: '3px 10px' }}>Matrix</span>
          </div>
        </div>
        {/* direction toggle */}
        <div>
          <div style={{ fontSize: 9, color: T.dim, marginBottom: 4 }}>Направление</div>
          <div style={{ display: 'flex', gap: 4 }}>
            {['Long', 'Short', 'Both'].map((d, i) => (
              <span key={d} style={{ fontSize: 9.5, fontWeight: 700, color: '#fff', background: i === 0 ? 'rgba(5,150,105,.7)' : 'rgba(255,255,255,.04)', borderRadius: 6, padding: '4px 10px' }}>{d}</span>
            ))}
          </div>
        </div>
        {/* deposit + leverage */}
        <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 8 }}>
          <div>
            <div style={{ fontSize: 9, color: T.dim, marginBottom: 4 }}>Депозит (USDT)</div>
            <div style={{ padding: '6px 9px', background: 'rgba(0,0,0,.3)', border: `1px solid ${T.border}`, borderRadius: 6, fontSize: 10, ...mono, color: T.body }}>100</div>
          </div>
          <div>
            <div style={{ fontSize: 9, color: T.dim, marginBottom: 4 }}>Плечо ×5</div>
            <div style={{ height: 6, borderRadius: 99, background: 'rgba(255,255,255,.06)', position: 'relative' }}>
              <div style={{ position: 'absolute', left: 0, top: 0, bottom: 0, width: '20%', borderRadius: 99, background: 'linear-gradient(90deg,#5b8cff,#7ba4ff)' }}/>
            </div>
          </div>
        </div>
        {/* steps mini table */}
        <div>
          <div style={{ fontSize: 9, color: T.dim, marginBottom: 4 }}>Шаги усреднения</div>
          <div style={{ display: 'flex', flexDirection: 'column', gap: 3 }}>
            {[{ m: '0%', s: '50%' }, { m: '1.5%', s: '100%' }, { m: '2.0%', s: '150%' }].map((r, i) => (
              <div key={i} style={{ display: 'flex', gap: 6, fontSize: 9.5, color: T.body, ...mono }}>
                <span style={{ color: T.faint, width: 12 }}>{i + 1}</span>
                <span style={{ flex: 1 }}>движение {r.m}</span>
                <span>{r.s} депозита</span>
              </div>
            ))}
          </div>
        </div>
      </div>
    </div>
  )
}

/* ─── FAQ ─────────────────────────────────────────────────────────────────── */
function FAQ({ q, a }: { q: string; a: React.ReactNode }) {
  const [open, setOpen] = useState(false)
  return (
    <div style={{ border: `1px solid ${T.border}`, borderRadius: 10, overflow: 'hidden', marginBottom: 8 }}>
      <button onClick={() => setOpen(v => !v)} style={{
        width: '100%', display: 'flex', alignItems: 'center', gap: 10, padding: '13px 16px',
        background: open ? 'rgba(91,140,255,.06)' : 'transparent',
        border: 0, cursor: 'pointer', color: T.text, fontFamily: 'inherit', textAlign: 'left',
      }}>
        <span style={{ flex: 1, fontSize: 13.5, fontWeight: 600 }}>{q}</span>
        <span style={{ flexShrink: 0, transition: 'transform .15s', transform: open ? 'rotate(180deg)' : 'none', color: T.faint }}>
          <IcChev s={14}/>
        </span>
      </button>
      {open && (
        <div style={{ padding: '0 16px 14px', fontSize: 13, color: T.body, lineHeight: 1.65, borderTop: `1px solid ${T.border}`, paddingTop: 12 }}>
          {a}
        </div>
      )}
    </div>
  )
}

/* ─── HelpPage ────────────────────────────────────────────────────────────── */
export function HelpPage() {
  const [topic, setTopic]           = useState<TopicId>('keys')
  const [activeSection, setActiveSection] = useState(KEY_SECTIONS[0].id)

  const sections = topic === 'keys' ? KEY_SECTIONS : BOT_SECTIONS

  function scrollTo(id: string) {
    setActiveSection(id)
    document.getElementById(id)?.scrollIntoView({ behavior: 'smooth', block: 'start' })
  }

  function switchTopic(t: TopicId) {
    if (t === topic) return
    setTopic(t)
    const first = (t === 'keys' ? KEY_SECTIONS : BOT_SECTIONS)[0].id
    setActiveSection(first)
    window.scrollTo({ top: 0, behavior: 'smooth' })
  }

  return (
    <div style={{ color: '#dde3ef', fontFamily: "'Inter', system-ui, sans-serif" }}>
      {/* page header */}
      <div style={{ marginBottom: 28 }}>
        <div style={{ display: 'flex', alignItems: 'center', gap: 10, marginBottom: 6 }}>
          <div style={{
            width: 28, height: 28, borderRadius: 8, display: 'flex', alignItems: 'center', justifyContent: 'center',
            background: 'linear-gradient(135deg,rgba(91,140,255,.28),rgba(193,77,255,.18))',
            border: '1px solid rgba(123,140,255,.3)', color: '#b8c8ff',
          }}>
            <IcBook s={14} w={2}/>
          </div>
          <h1 style={{ ...grotesk, fontSize: 26, fontWeight: 800, color: T.text, margin: 0, letterSpacing: -0.6 }}>
            Справка
          </h1>
        </div>
        <p style={{ margin: 0, fontSize: 13.5, color: T.dim }}>
          Документация по платформе — как работают ключи, стратегии и всё остальное
        </p>
      </div>

      <div style={{ display: 'grid', gridTemplateColumns: '200px 1fr', gap: 32, alignItems: 'start' }}>
        {/* ─── left nav ───────────────────────────────────────────────── */}
        <div style={{ position: 'sticky', top: 20 }}>
          {/* topic switcher */}
          <div style={{ display: 'flex', gap: 6, marginBottom: 14 }}>
            {TOPICS.map(t => (
              <button key={t.id} onClick={() => switchTopic(t.id)} style={{
                flex: 1, display: 'flex', alignItems: 'center', justifyContent: 'center', gap: 6,
                padding: '7px 8px', borderRadius: 8, cursor: 'pointer', fontFamily: 'inherit',
                background: topic === t.id ? T.blueBg : 'transparent',
                border: `1px solid ${topic === t.id ? T.blueBd : T.border}`,
                fontSize: 11, fontWeight: 700, color: topic === t.id ? T.blue : T.dim,
                transition: 'all .1s',
              }}>
                <t.Icon s={11} w={2.2}/> {t.label}
              </button>
            ))}
          </div>
          <div style={{ display: 'flex', flexDirection: 'column', gap: 1 }}>
            {sections.map(s => (
              <button key={s.id} onClick={() => scrollTo(s.id)} style={{
                display: 'flex', alignItems: 'center', gap: 8,
                padding: '7px 10px', borderRadius: 7,
                background: activeSection === s.id ? 'rgba(91,140,255,.12)' : 'transparent',
                border: `1px solid ${activeSection === s.id ? 'rgba(91,140,255,.25)' : 'transparent'}`,
                color: activeSection === s.id ? '#c8d8ff' : T.dim,
                fontSize: 12.5, fontWeight: activeSection === s.id ? 600 : 400,
                cursor: 'pointer', fontFamily: 'inherit', textAlign: 'left', width: '100%',
                transition: 'all .1s',
              }}>
                {activeSection === s.id && <IcChevR s={10} c="#7b9fff"/>}
                {activeSection !== s.id && <span style={{ width: 10 }}/>}
                {s.label}
              </button>
            ))}
          </div>

          {/* other sections (coming soon) */}
          <div style={{ marginTop: 20, padding: '10px', background: T.card, border: `1px solid ${T.border}`, borderRadius: 8 }}>
            <div style={{ fontSize: 10, color: T.faint, fontWeight: 600, textTransform: 'uppercase', letterSpacing: 1, marginBottom: 6 }}>Скоро</div>
            {['Стратегии', 'Терминал', 'Балансы', 'Сигналы'].map(s => (
              <div key={s} style={{ fontSize: 11.5, color: T.faint, padding: '4px 0', borderBottom: `1px solid rgba(255,255,255,.03)` }}>{s}</div>
            ))}
          </div>
        </div>

        {/* ─── content ────────────────────────────────────────────────── */}
        <div style={{ minWidth: 0 }}>
        {topic === 'keys' && <>

          {/* ── Overview ─────────────────────────────────────────── */}
          <Section id="overview" title="Обзор страницы API ключей"
            subtitle="Центральная точка управления подключениями к биржам. Здесь вы добавляете, проверяете и контролируете ключи.">

            <MockupPageLayout />

            <div style={{ marginTop: 20, display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 12 }}>
              {[
                { title: 'Статистика вверху', desc: 'Общее число ключей, активные, требующие внимания и суммарный equity по всем аккаунтам.', icon: IcCog },
                { title: 'Список ключей', desc: 'Каждый ключ — отдельная карточка. Нажмите на неё, чтобы развернуть детали, вкладки и кнопки управления.', icon: IcKey },
                { title: 'Панель добавления', desc: 'Правая часть страницы — форма из 4 шагов для подключения нового ключа с биржи.', icon: IcHash },
                { title: 'Поиск и фильтр', desc: 'Тулбар между статистикой и списком позволяет фильтровать по статусу: Все / Активные / Требуют внимания / Остановленные.', icon: IcEye },
              ].map(({ title, desc, icon: Ic }) => (
                <div key={title} style={{ display: 'flex', gap: 10, padding: '12px', background: T.panel, border: `1px solid ${T.border}`, borderRadius: 10 }}>
                  <div style={{ flexShrink: 0, width: 28, height: 28, borderRadius: 7, background: T.blueBg, border: `1px solid ${T.blueBd}`, display: 'flex', alignItems: 'center', justifyContent: 'center', color: T.blue }}>
                    <Ic s={13} w={2}/>
                  </div>
                  <div>
                    <div style={{ fontSize: 13, fontWeight: 600, color: T.text, marginBottom: 3 }}>{title}</div>
                    <div style={{ fontSize: 12, color: T.dim, lineHeight: 1.55 }}>{desc}</div>
                  </div>
                </div>
              ))}
            </div>
          </Section>

          {/* ── Add Key ─────────────────────────────────────────── */}
          <Section id="add-key" title="Добавление нового ключа"
            subtitle="Форма состоит из 4 шагов. Ключ не сохраняется до прохождения проверки подключения.">

            <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 20, marginBottom: 20 }}>
              <div>
                <StepList steps={[
                  { title: 'Выберите биржу', desc: 'Сейчас поддерживается Bybit. Другие биржи — в разработке.' },
                  { title: 'Дайте название ключу', desc: 'Любое удобное имя: «main · Bybit» или «test». Отобразится в списке.' },
                  { title: 'Вставьте ключ и секрет', desc: 'API key — публичный. API secret — приватный, хранится в AES-256 и не показывается после сохранения.' },
                  { title: 'Укажите IP-whitelist', desc: 'Рекомендуем указать наши IP: 185.94.32.0/24. Есть кнопка «наши IP» для автозаполнения.' },
                ]} />
                <div style={{ marginTop: 14 }}>
                  <Callout kind="tip" title="Сначала проверьте — потом сохраните">
                    Кнопка «Сохранить ключ» активируется только после успешной проверки. Это защищает от добавления нерабочих ключей.
                  </Callout>
                  <Callout kind="warn" title="Secret виден только один раз">
                    На бирже API secret показывается только в момент создания ключа. Скопируйте его сразу — потом придётся создавать новый.
                  </Callout>
                </div>
              </div>
              <MockupAddKeyForm />
            </div>

            {/* How to create key on Bybit */}
            <div style={{ background: T.panel, border: `1px solid ${T.border}`, borderRadius: 12, overflow: 'hidden' }}>
              <div style={{ padding: '12px 16px', borderBottom: `1px solid ${T.border}`, display: 'flex', alignItems: 'center', gap: 10 }}>
                <div style={{ width: 24, height: 24, borderRadius: 6, background: '#f7a600', display: 'flex', alignItems: 'center', justifyContent: 'center' }}>
                  <span style={{ fontSize: 9, fontWeight: 800, color: '#1a0900' }}>BY</span>
                </div>
                <span style={{ ...grotesk, fontSize: 13.5, fontWeight: 700, color: T.text }}>Как создать ключ на Bybit</span>
                <span style={{ fontSize: 11, color: T.dim }}>~ 2 минуты</span>
              </div>
              <div style={{ padding: '12px 16px', display: 'flex', flexDirection: 'column', gap: 0 }}>
                {[
                  { step: 'Войдите в Bybit', desc: 'Откройте bybit.com → меню аккаунта (иконка человека справа вверху)' },
                  { step: 'Перейдите в раздел API', desc: 'Личный кабинет → API → Управление API-ключами' },
                  { step: 'Нажмите «Создать новый ключ»', desc: 'Выберите тип «System-generated API keys» (Системный)' },
                  { step: 'Настройте права', desc: 'Включите: Чтение (Read), Торговля (Trade). Раздел Unified Trading → включите. Вывод средств (Withdraw) — НЕ включайте' },
                  { step: 'Привяжите наши IP', desc: 'В поле IP restriction введите: 185.94.32.0/24 — это снизит риск при утечке ключа' },
                  { step: 'Скопируйте key + secret', desc: 'Secret будет показан только сейчас — скопируйте сразу в поле формы' },
                ].map((s, i, arr) => (
                  <div key={i} style={{ display: 'flex', gap: 14, padding: '10px 0', borderBottom: i < arr.length - 1 ? `1px dashed rgba(255,255,255,.06)` : 'none' }}>
                    <div style={{ flexShrink: 0, width: 20, height: 20, borderRadius: 5, background: 'rgba(247,166,0,.15)', border: '1px solid rgba(247,166,0,.3)', color: '#f7a600', fontSize: 10, fontWeight: 700, ...mono, display: 'flex', alignItems: 'center', justifyContent: 'center' }}>{i + 1}</div>
                    <div>
                      <div style={{ fontSize: 12.5, fontWeight: 600, color: T.text }}>{s.step}</div>
                      <div style={{ fontSize: 11.5, color: T.dim, marginTop: 2, lineHeight: 1.5 }}>{s.desc}</div>
                    </div>
                  </div>
                ))}
              </div>
            </div>
          </Section>

          {/* ── Key Card ─────────────────────────────────────────── */}
          <Section id="key-card" title="Карточка ключа"
            subtitle="Нажмите на карточку в списке — она развернётся и покажет полную информацию.">

            <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 20, marginBottom: 20 }}>
              <div>
                <div style={{ fontSize: 13, color: T.body, lineHeight: 1.65, marginBottom: 16 }}>
                  В свёрнутом виде карточка показывает: иконку биржи, название, статус и баланс (equity). При развёртывании открывается расширенная информация.
                </div>
                <div style={{ display: 'flex', flexDirection: 'column', gap: 10 }}>
                  {[
                    { label: 'Права доступа', desc: 'Чипы: Чтение / Торговля / Деривативы / Вывод. Заполняются после теста подключения.' },
                    { label: 'Детали', desc: 'Сетка из полей: API key (скрыт), дата создания, истечения, латентность, IP whitelist.' },
                    { label: 'Вкладки', desc: 'Активность — лог запросов. Аудит ключа — детальные права. Боты — привязанные стратегии.' },
                    { label: 'Кнопки', desc: 'Тест подключения, Изменить IP, Ротейт, Приостановить/Возобновить, Удалить.' },
                  ].map(({ label, desc }) => (
                    <div key={label} style={{ padding: '10px 12px', background: T.panel, border: `1px solid ${T.border}`, borderRadius: 9 }}>
                      <div style={{ fontSize: 12.5, fontWeight: 600, color: T.text, marginBottom: 3 }}>{label}</div>
                      <div style={{ fontSize: 12, color: T.dim, lineHeight: 1.5 }}>{desc}</div>
                    </div>
                  ))}
                </div>
              </div>
              <MockupKeyCard />
            </div>
          </Section>

          {/* ── Statuses ─────────────────────────────────────────── */}
          <Section id="statuses" title="Статусы ключа"
            subtitle="Статус показывается на карточке цветным бейджем и влияет на поведение платформы.">

            <div style={{ display: 'flex', flexDirection: 'column', gap: 10 }}>
              {[
                {
                  status: 'активен', color: T.green, bg: T.greenBg, bd: T.greenBd,
                  desc: 'Ключ прошёл проверку и не имеет проблем. Платформа использует его для торговли и мониторинга баланса.',
                  icon: IcDot,
                },
                {
                  status: 'истекает', color: T.orange, bg: T.orangeBg, bd: T.orangeBd,
                  desc: 'До истечения ключа осталось менее 21 дня. Обновите секрет заранее через кнопку «Ротейт» — иначе стратегии остановятся.',
                  icon: IcAlert,
                },
                {
                  status: 'ошибка', color: T.red, bg: T.redBg, bd: T.redBd,
                  desc: 'Биржа отклонила запрос с этим ключом: неверные права, истёк или был удалён. Нажмите «Тест подключения» для диагностики, затем «Обновить секрет».',
                  icon: IcAlert,
                },
                {
                  status: 'остановлен', color: T.dim, bg: 'rgba(255,255,255,.05)', bd: T.border,
                  desc: 'Ключ вручную приостановлен через кнопку «Приостановить». Платформа не использует его. Возобновите через «Возобновить».',
                  icon: IcAlert,
                },
                {
                  status: 'проверка', color: T.orange, bg: T.orangeBg, bd: T.orangeBd,
                  desc: 'Идёт тест подключения к бирже. Через несколько секунд статус изменится на «активен» или «ошибка».',
                  icon: IcRefresh,
                },
              ].map(({ status, color, bg, bd, desc, icon: Ic }) => (
                <div key={status} style={{ display: 'flex', gap: 14, padding: '14px 16px', background: bg, border: `1px solid ${bd}`, borderRadius: 10 }}>
                  <div style={{ flexShrink: 0, marginTop: 2 }}>
                    <Badge color={color} bg={bg} bd={bd}>
                      <Ic s={9} c={color} w={2.4}/>{status}
                    </Badge>
                  </div>
                  <div style={{ fontSize: 13, color: T.body, lineHeight: 1.6 }}>{desc}</div>
                </div>
              ))}
            </div>
          </Section>

          {/* ── Audit tab ─────────────────────────────────────────── */}
          <Section id="audit" title="Вкладка «Аудит ключа»"
            subtitle="Открывается при нажатии «Тест подключения». Показывает права, IP и сырые разрешения с биржи.">

            <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 20, marginBottom: 20 }}>
              <div>
                <div style={{ display: 'flex', flexDirection: 'column', gap: 10 }}>
                  {[
                    { label: 'Чтение', Ic: IcEye, desc: 'Позволяет читать баланс, ордера и историю сделок. Всегда должно быть включено.', required: true },
                    { label: 'Торговля', Ic: IcZap, desc: 'Разрешает создавать и отменять ордера. Нужно для работы стратегий.', required: true },
                    { label: 'Деривативы (Futures)', Ic: IcCog, desc: 'Доступ к фьючерсам и бессрочным контрактам. Включите если торгуете перп.', required: false },
                    { label: 'Вывод средств', Ic: IcAlert, desc: 'МЫ НИКОГДА не запрашиваем это право. Если оно включено — пересоздайте ключ.', required: false, danger: true },
                  ].map(({ label, Ic, desc, required, danger }) => (
                    <div key={label} style={{ display: 'flex', gap: 10, padding: '10px 12px', background: danger ? T.redBg : T.panel, border: `1px solid ${danger ? T.redBd : T.border}`, borderRadius: 9 }}>
                      <div style={{ flexShrink: 0, width: 26, height: 26, borderRadius: 6, background: danger ? T.redBg : (required ? T.greenBg : T.blueBg), border: `1px solid ${danger ? T.redBd : (required ? T.greenBd : T.blueBd)}`, display: 'flex', alignItems: 'center', justifyContent: 'center', color: danger ? T.red : (required ? T.green : T.blue) }}>
                        <Ic s={12} w={2}/>
                      </div>
                      <div>
                        <div style={{ fontSize: 13, fontWeight: 600, color: danger ? T.red : T.text }}>{label}</div>
                        <div style={{ fontSize: 11.5, color: T.dim, marginTop: 2, lineHeight: 1.5 }}>{desc}</div>
                        {required && <span style={{ fontSize: 10, color: T.green, fontWeight: 600 }}>● обязательно</span>}
                        {danger && <span style={{ fontSize: 10, color: T.red, fontWeight: 600 }}>⚠ не включайте</span>}
                      </div>
                    </div>
                  ))}
                </div>
              </div>
              <div>
                <div style={{ fontSize: 13, fontWeight: 600, color: T.text, marginBottom: 10 }}>IP-whitelist в аудите</div>
                <div style={{ fontSize: 13, color: T.body, lineHeight: 1.65, marginBottom: 12 }}>
                  Если IP не привязан, аудит показывает предупреждение. Это не блокирует работу, но снижает безопасность.
                </div>
                <Callout kind="tip" title="IP whitelist — наши адреса">
                  В поле IP-whitelist укажите <span style={{ ...mono, color: T.text }}>185.94.32.0/24</span> — это подсеть наших серверов.
                  Без этого ключ будет принимать запросы с любого IP.
                </Callout>
                <div style={{ fontSize: 13, fontWeight: 600, color: T.text, marginBottom: 10, marginTop: 16 }}>Права биржи (raw)</div>
                <div style={{ fontSize: 13, color: T.body, lineHeight: 1.65 }}>
                  В нижней части вкладки — сырые права в формате биржи (ContractTrade, Derivatives, Wallet…). Это то, что биржа сообщает напрямую, без нашей интерпретации. Полезно при нестандартных правах.
                </div>
              </div>
            </div>
          </Section>

          {/* ── Security ─────────────────────────────────────────── */}
          <Section id="security" title="Безопасность"
            subtitle="Как мы защищаем ваши ключи и что вы должны сделать со своей стороны.">

            <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 12, marginBottom: 20 }}>
              {[
                { icon: IcLock,   title: 'AES-256 шифрование',    desc: 'API secret шифруется на сервере перед сохранением. Никто, включая нас, не может прочитать его в открытом виде.' },
                { icon: IcGlobe,  title: 'IP-whitelist',           desc: 'Даже если кто-то получит ключ — биржа откажет в запросе, если он идёт не с наших IP 185.94.32.0/24.' },
                { icon: IcShield, title: 'Нет права на вывод',     desc: 'Платформа явно проверяет: если у ключа есть «Withdraw» — показывает предупреждение. Мы не торгуем с этим правом.' },
                { icon: IcRefresh,title: 'Ротация ключей',         desc: 'Используйте кнопку «Ротейт» при подозрении на компрометацию или истечении. Секрет можно обновить без удаления ключа.' },
              ].map(({ icon: Ic, title, desc }) => (
                <div key={title} style={{ display: 'flex', gap: 12, padding: '14px', background: T.panel, border: `1px solid ${T.border}`, borderRadius: 10 }}>
                  <div style={{ flexShrink: 0, width: 32, height: 32, borderRadius: 8, background: T.greenBg, border: `1px solid ${T.greenBd}`, display: 'flex', alignItems: 'center', justifyContent: 'center', color: T.green }}>
                    <Ic s={15} w={2}/>
                  </div>
                  <div>
                    <div style={{ fontSize: 13, fontWeight: 600, color: T.text, marginBottom: 4 }}>{title}</div>
                    <div style={{ fontSize: 12, color: T.dim, lineHeight: 1.6 }}>{desc}</div>
                  </div>
                </div>
              ))}
            </div>

            <Callout kind="danger" title="Что мы никогда не делаем">
              <ul style={{ margin: '4px 0 0', paddingLeft: 18, display: 'flex', flexDirection: 'column', gap: 4 }}>
                <li>Не запрашиваем право на вывод средств (Withdraw)</li>
                <li>Не показываем API secret после первого сохранения</li>
                <li>Не передаём ключи третьим сервисам</li>
                <li>Не храним секрет в открытом виде</li>
              </ul>
            </Callout>
          </Section>

          {/* ── FAQ ─────────────────────────────────────────────── */}
          <Section id="faq" title="Частые вопросы">
            <FAQ
              q="Ключ удалён из платформы — его нужно удалять на бирже тоже?"
              a={<>Да. При удалении из нашей платформы ключ <b>остаётся активным на бирже</b>. Зайдите в личный кабинет биржи и удалите его вручную, если он вам больше не нужен.</>}
            />
            <FAQ
              q="Что будет, если я не укажу IP-whitelist?"
              a={<>Ключ будет работать, но с меньшей безопасностью — биржа примет запросы с любого IP. Мы показываем предупреждение в аудите. Рекомендуем указать <span style={{ ...mono, color: T.text }}>185.94.32.0/24</span>.</>}
            />
            <FAQ
              q="Почему кнопка «Сохранить ключ» неактивна?"
              a={<>Кнопка активируется только после успешного теста подключения. Нажмите «Проверить ключ» — если ключ верный, кнопка сохранения станет активной.</>}
            />
            <FAQ
              q="Что такое «Ротейт»?"
              a={<>Замена API secret без изменения API key. Используйте если: истекает срок, вы подозреваете компрометацию, или биржа попросила обновить секрет. Стратегии автоматически подхватят новый секрет.</>}
            />
            <FAQ
              q="Могу ли я подключить несколько аккаунтов одной биржи?"
              a={<>Да. Вы можете добавить любое количество ключей. Каждый отображается как отдельная карточка. Стратегии привязываются к конкретному ключу при создании.</>}
            />
            <FAQ
              q="Что означает «Деривативы» в правах?"
              a={<>Доступ к фьючерсам и бессрочным контрактам (Perpetual). Нужно включать, если вы планируете торговать с плечом (Futures/Perp). Для спотовой торговли не нужно.</>}
            />
          </Section>

        </> }
        {topic === 'bots' && <>

          {/* ── Bots: Overview ─────────────────────────────────────── */}
          <Section id="bots-overview" title="Обзор страницы «Боты»"
            subtitle="Витрина готовых стратегий и место, где живут ваши собственные боты — «Мои боты» и «Библиотека» на одной странице.">

            <MockupBotsPageLayout />

            <div style={{ marginTop: 20, display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 12 }}>
              {[
                { title: 'Мои боты', desc: 'Боты, которые вы создали сами или получили подпиской из библиотеки. У каждого — свой статус, статистика по сделкам и настройки.', icon: IcBotHead },
                { title: 'Библиотека ботов', desc: 'Готовые стратегии от NovaBot (Verified) и сообщества. Фильтруйте по типу, стратегии, риску, режиму и цене, сортируйте по популярности, доходности или win-rate.', icon: IcLayers },
                { title: 'Перетаскивание', desc: 'Карточку из библиотеки можно перетащить прямо в блок «Мои боты» — результат тот же, что и у кнопки «В свои боты».', icon: IcCopy },
                { title: 'Экспорт', desc: 'Кнопка «Экспорт» в шапке «Мои боты» выгружает список ваших ботов и их настроек.', icon: IcHash },
              ].map(({ title, desc, icon: Ic }) => (
                <div key={title} style={{ display: 'flex', gap: 10, padding: '12px', background: T.panel, border: `1px solid ${T.border}`, borderRadius: 10 }}>
                  <div style={{ flexShrink: 0, width: 28, height: 28, borderRadius: 7, background: T.blueBg, border: `1px solid ${T.blueBd}`, display: 'flex', alignItems: 'center', justifyContent: 'center', color: T.blue }}>
                    <Ic s={13} w={2}/>
                  </div>
                  <div>
                    <div style={{ fontSize: 13, fontWeight: 600, color: T.text, marginBottom: 3 }}>{title}</div>
                    <div style={{ fontSize: 12, color: T.dim, lineHeight: 1.55 }}>{desc}</div>
                  </div>
                </div>
              ))}
            </div>
          </Section>

          {/* ── Bots: Kinds ─────────────────────────────────────────── */}
          <Section id="bots-kinds" title="Типы ботов"
            subtitle="Тип выбирается один раз при создании и определяет саму логику работы бота — кто и зачем открывает позиции.">

            <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 12, marginBottom: 16 }}>
              {BOT_KINDS.map(kind => {
                const km = BOT_KIND_META[kind]
                const Ic = BOT_KIND_ICONS[kind]
                return (
                  <div key={kind} style={{ borderRadius: 12, border: `1px solid ${km.border}`, overflow: 'hidden', opacity: km.disabled ? 0.6 : 1 }}>
                    <div style={{ padding: '12px 14px', background: km.bgHeader, display: 'flex', alignItems: 'center', gap: 10 }}>
                      <div style={{ width: 30, height: 30, borderRadius: 8, background: km.iconBg, border: `1px solid ${km.border}`, display: 'flex', alignItems: 'center', justifyContent: 'center', color: km.color, flexShrink: 0 }}>
                        <Ic s={15} w={2}/>
                      </div>
                      <div style={{ flex: 1 }}>
                        <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
                          <span style={{ ...grotesk, fontSize: 14.5, fontWeight: 700, color: T.text }}>{km.label}</span>
                          {km.disabled && <Badge color={km.color} bg={km.iconBg} bd={km.border}>Скоро</Badge>}
                        </div>
                        <div style={{ fontSize: 11.5, color: km.color, fontWeight: 600, marginTop: 1 }}>{km.tagline}</div>
                      </div>
                    </div>
                    <div style={{ padding: '10px 14px 14px', fontSize: 12.5, color: T.body, lineHeight: 1.6 }}>{km.desc}</div>
                  </div>
                )
              })}
            </div>

            <Callout kind="info" title="Тип бота ≠ тип стратегии">
              «Тип бота» (эта страница) — про саму логику: кто и почему открывает позиции. А «Grid / Matrix» на вкладке «Стратегия» при настройке SignalBot или HedgeBot — это способ набора <b>одной</b> позиции: сеткой усреднения по шагам (Grid) или уровнями с индивидуальным TP/SL выше и ниже цены входа (Matrix). MatrixBot как тип бота — отдельная сущность с парными зеркальными лонг+шорт позициями, настраивается через свою форму.
            </Callout>
          </Section>

          {/* ── Bots: Creating ─────────────────────────────────────── */}
          <Section id="bots-creating" title="Создание и настройка бота"
            subtitle="Форма из трёх вкладок: Основное, Активация, Стратегия. Тип бота, выбранный на первом шаге, изменить после создания нельзя.">

            <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 20, marginBottom: 20 }}>
              <div>
                <StepList steps={[
                  { title: 'Создать своего бота', desc: 'Кнопка в шапке «Мои боты» открывает выбор типа.' },
                  { title: 'Выберите тип', desc: 'SignalBot, HedgeBot, MatrixBot или МультиБот. ParserBot — «Скоро», недоступен для выбора.' },
                  { title: 'Заполните «Основное»', desc: 'Название и краткое описание обязательны. Плюс аватар, публичность, авто-режим и лимиты стратегий.' },
                  { title: 'Настройте «Активация»', desc: 'Сигналы, по которым бот сканирует рынок, и список монет (whitelist/blacklist с масками).' },
                  { title: 'Настройте «Стратегия»', desc: 'Направление, тип ордера, маржа, депозит и плечо, шаги усреднения или уровни матрицы.' },
                ]} />
                <div style={{ marginTop: 14 }}>
                  <Callout kind="tip" title="Название и описание обязательны">
                    Без них бот не сохранится — краткое описание отображается прямо на карточке в «Моих ботах» и в библиотеке.
                  </Callout>
                  <Callout kind="warn" title="Изменения применяются немедленно">
                    Правки SL/TP и параметров сетки у уже сохранённого бота сразу применяются ко всем его активным стратегиям. Тип стратегии (Grid/Matrix) нельзя сменить, пока есть открытые позиции.
                  </Callout>
                </div>
              </div>
              <MockupBotTypePicker />
            </div>

            <MockupBotFormTabs />

            <div style={{ marginTop: 16, background: T.panel, border: `1px solid ${T.border}`, borderRadius: 12, overflow: 'hidden' }}>
              <div style={{ padding: '12px 16px', borderBottom: `1px solid ${T.border}` }}>
                <span style={{ ...grotesk, fontSize: 13.5, fontWeight: 700, color: T.text }}>Что настраивается на вкладке «Активация»</span>
              </div>
              <div style={{ padding: '12px 16px', display: 'flex', flexDirection: 'column', gap: 0 }}>
                {[
                  { step: 'Сигналы активации', desc: 'При срабатывании бот сканирует все доступные монеты и открывает стратегию на каждой подходящей под условие (AND-логика для нескольких сигналов).' },
                  { step: 'Whitelist / blacklist монет', desc: 'Поддерживают маски, например BTC*. Белый список сужает выбор, чёрный — исключает конкретные монеты из скана.' },
                  { step: 'Проверка сканированием', desc: 'Кнопка запускает пробный скан по текущим сигналам и списку монет — показывает, какие монеты сейчас buy/sell, ещё до сохранения бота.' },
                ].map((s, i, arr) => (
                  <div key={i} style={{ display: 'flex', gap: 14, padding: '10px 0', borderBottom: i < arr.length - 1 ? `1px dashed rgba(255,255,255,.06)` : 'none' }}>
                    <div style={{ flexShrink: 0, width: 20, height: 20, borderRadius: 5, background: 'rgba(91,140,255,.15)', border: '1px solid rgba(91,140,255,.3)', color: '#7ba4ff', fontSize: 10, fontWeight: 700, ...mono, display: 'flex', alignItems: 'center', justifyContent: 'center' }}>{i + 1}</div>
                    <div>
                      <div style={{ fontSize: 12.5, fontWeight: 600, color: T.text }}>{s.step}</div>
                      <div style={{ fontSize: 11.5, color: T.dim, marginTop: 2, lineHeight: 1.5 }}>{s.desc}</div>
                    </div>
                  </div>
                ))}
              </div>
            </div>
          </Section>

          {/* ── Bots: Library ───────────────────────────────────────── */}
          <Section id="bots-library" title="Библиотека и подписка"
            subtitle="Каталог готовых ботов от NovaBot (Verified) и сообщества. «Подписка» создаёт независимую копию бота в «Моих ботах».">

            <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 20, marginBottom: 20 }}>
              <div style={{ display: 'flex', flexDirection: 'column', gap: 10 }}>
                {[
                  { label: 'Фильтры', desc: 'Бот (тип), Стратегия (grid / matrix / signal / scalp / arbitrage / copy / trend / hold), Риск (низкий / средний / высокий), Режим (spot / futures), Цена (бесплатные / платные).' },
                  { label: 'Сортировка', desc: 'Популярные, Доходные, Win-rate или Новые — переключается рядом с поиском.' },
                  { label: 'Verified', desc: 'Синяя галочка на карточке — бот проверен и опубликован самой платформой NovaBot.' },
                  { label: 'Карточка с флипом', desc: 'Если у бота есть подробное описание, клик по карточке переворачивает её и показывает полный текст с кнопкой подписки.' },
                ].map(({ label, desc }) => (
                  <div key={label} style={{ padding: '10px 12px', background: T.panel, border: `1px solid ${T.border}`, borderRadius: 9 }}>
                    <div style={{ fontSize: 12.5, fontWeight: 600, color: T.text, marginBottom: 3 }}>{label}</div>
                    <div style={{ fontSize: 12, color: T.dim, lineHeight: 1.5 }}>{desc}</div>
                  </div>
                ))}
              </div>
              <div style={{ display: 'flex', flexDirection: 'column', gap: 8 }}>
                <MiniLibraryCard kind="signal" name="Trend Rider"  author="NovaBot"    price="Free"    users="1 240"/>
                <MiniLibraryCard kind="hedge"  name="Safe Hedge"   author="community"  price="$5/мес"  users="312"/>
                <MiniLibraryCard kind="multi"  name="Hedge Combo"  author="community"  price="$9/мес"  users="86"/>
              </div>
            </div>

            <Callout kind="tip" title="Подписка = клонирование">
              Кнопка «В свои боты» создаёт независимую копию бота в «Моих ботах». При подписке можно сразу сузить список монет через свои whitelist/blacklist. Оригинал в библиотеке при этом не меняется — дальнейшие изменения копии никак на него не влияют.
            </Callout>
          </Section>

          {/* ── Bots: Manage ─────────────────────────────────────────── */}
          <Section id="bots-manage" title="Управление и лимиты"
            subtitle="Статус, авто-режим и риск-лимиты определяют, что боту разрешено делать, не трогая его торговую логику.">

            <div style={{ display: 'flex', flexDirection: 'column', gap: 10, marginBottom: 20 }}>
              {[
                { status: 'Запущен',    color: T.green,  bg: T.greenBg,  bd: T.greenBd,  icon: IcPlay,
                  desc: 'Бот активен: реагирует на сигналы и открывает стратегии в рамках заданных лимитов. Переключается кнопкой на карточке.' },
                { status: 'Пауза',      color: T.orange, bg: T.orangeBg, bd: T.orangeBd, icon: IcPause,
                  desc: 'Бот остановлен вручную — не открывает новые стратегии. Уже открытые позиции продолжают жить по своим TP/SL.' },
                { status: 'Остановлен', color: T.dim,    bg: 'rgba(255,255,255,.05)', bd: T.border, icon: IcPause,
                  desc: 'Бот выключен и не реагирует на сигналы. Используется в основном для сохранённых, но не запущенных ботов.' },
              ].map(({ status, color, bg, bd, desc, icon: Ic }) => (
                <div key={status} style={{ display: 'flex', gap: 14, padding: '14px 16px', background: bg, border: `1px solid ${bd}`, borderRadius: 10 }}>
                  <div style={{ flexShrink: 0, marginTop: 2 }}>
                    <Badge color={color} bg={bg} bd={bd}><Ic s={9} c={color} w={2.4}/>{status}</Badge>
                  </div>
                  <div style={{ fontSize: 13, color: T.body, lineHeight: 1.6 }}>{desc}</div>
                </div>
              ))}
            </div>

            <div style={{ marginBottom: 8, fontSize: 11, fontWeight: 700, color: T.dim, textTransform: 'uppercase', letterSpacing: 1 }}>Риск-лимиты (вкладка «Основное»)</div>
            <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 12, marginBottom: 16 }}>
              {[
                { title: 'Всего стратегий',   desc: 'Максимум одновременно открытых стратегий у бота. 0 — без ограничений.' },
                { title: 'Макс. лонг / шорт', desc: 'Отдельные счётчики на каждое направление — можно, например, разрешить только 3 шорта при неограниченных лонгах.' },
                { title: 'Лимит маржи, USDT', desc: 'Суммарная маржа по всем открытым стратегиям бота. 0 — без ограничений.' },
                { title: 'Повторов подряд',   desc: 'После N запусков подряд на одной и той же монете бот пропустит её, пока не отработает другую. 0 — без ограничений.' },
              ].map(({ title, desc }) => (
                <div key={title} style={{ display: 'flex', gap: 10, padding: '12px', background: T.panel, border: `1px solid ${T.border}`, borderRadius: 10 }}>
                  <div style={{ flexShrink: 0, width: 28, height: 28, borderRadius: 7, background: T.blueBg, border: `1px solid ${T.blueBd}`, display: 'flex', alignItems: 'center', justifyContent: 'center', color: T.blue }}>
                    <IcCog s={13} w={2}/>
                  </div>
                  <div>
                    <div style={{ fontSize: 13, fontWeight: 600, color: T.text, marginBottom: 3 }}>{title}</div>
                    <div style={{ fontSize: 12, color: T.dim, lineHeight: 1.55 }}>{desc}</div>
                  </div>
                </div>
              ))}
            </div>

            <Callout kind="info" title="Авто-режим">
              Включённый авто-режим позволяет боту самому открывать стратегии при срабатывании сигналов активации — без подтверждения. По умолчанию выключен.
            </Callout>
            <Callout kind="info" title="После TP/SL: удалять или перезапускать">
              «Удалять» — стратегия закрывается и удаляется, бот сможет открыть новую на той же монете. «Перезапускать» — стратегия продолжает работу дальше без нового сигнала активации.
            </Callout>
            <Callout kind="info" title="Управление МультиБотом — одной карточкой на двоих">
              Внутри это две обычные записи — сигнальная и хедж-нога, связанные между собой. В «Моих ботах» они показываются <b>одной карточкой</b>: хедж-нога скрыта из списка, а её статистика сложена в карточку сигнальной ноги. Запуск, пауза и удаление на этой карточке применяются сразу к обеим ногам — отдельно остановить только одну сторону нельзя.
            </Callout>
          </Section>

          {/* ── Bots: Publish ────────────────────────────────────────── */}
          <Section id="bots-publish" title="Публикация в библиотеку"
            subtitle="Собственного бота можно открыть другим пользователям — через публикацию в общую библиотеку.">

            <Callout kind="warn" title="МультиБота пока нельзя опубликовать">
              Публикация в библиотеку ниже описана для SignalBot, HedgeBot и MatrixBot. Для МультиБота она ещё не реализована: тумблер «Публичный бот» для него недоступен, а на сервере поле isPublic принудительно выставлено в false — конвейер публикации пока не умеет клонировать пару из сигнальной и хедж-ноги целиком.
            </Callout>

            <StepList steps={[
              { title: 'Включите «Публичный бот»', desc: 'Тумблер на вкладке «Основное» (недоступен для форков чужих ботов и для МультиБота) — так другие пользователи увидят бота в библиотеке.' },
              { title: 'Отправьте на проверку', desc: 'На карточке кастомного бота появится кнопка отправки — «Отправить в библиотеку на проверку».' },
              { title: 'Дождитесь решения', desc: 'Пока идёт проверка — на карточке значок часов «Ожидает проверки администратором».' },
              { title: 'Approved или Rejected', desc: 'Одобренный бот появляется в общей библиотеке. Отклонённый можно доработать и отправить на проверку повторно.' },
            ]} />

            <div style={{ marginTop: 14 }}>
              <Callout kind="warn" title="Пока идёт проверка — повторная отправка недоступна">
                Кнопка отправки скрыта, пока approvalStatus = «на проверке» (иконка часов на карточке). Она снова появится только после решения администратора.
              </Callout>
              <Callout kind="info" title="Verified — это не approvalStatus">
                Синяя галочка Verified в библиотеке означает, что бот выпущен самой платформой NovaBot. Approval-статус пользовательского бота (pending / approved / rejected) — отдельный процесс модерации перед появлением в общем каталоге.
              </Callout>
            </div>
          </Section>

          {/* ── Bots: FAQ ───────────────────────────────────────────── */}
          <Section id="bots-faq" title="Частые вопросы">
            <FAQ
              q="В чём разница между типом бота и типом стратегии (Grid/Matrix)?"
              a={<>Тип бота (SignalBot / HedgeBot / MatrixBot / МультиБот) определяет саму логику работы — кто и зачем открывает позиции. А Grid/Matrix на вкладке «Стратегия» при настройке SignalBot или HedgeBot — это способ набора <b>одной</b> позиции: сеткой усреднения по шагам (Grid) или уровнями с индивидуальными TP/SL выше и ниже входа (Matrix). Это разные настройки.</>}
            />
            <FAQ
              q="Что происходит, когда я подписываюсь на бота из библиотеки?"
              a={<>Создаётся независимая копия в «Моих ботах» со своими настройками, включая список монет, который можно сузить прямо при подписке. Изменения оригинала в библиотеке на вашу копию не влияют, и наоборот.</>}
            />
            <FAQ
              q="Почему бот не открывает стратегию, хотя сигнал сработал?"
              a={<>Проверьте риск-лимиты в «Основном» — «Всего стратегий», «Макс. лонг/шорт», «Лимит маржи» или «Повторов подряд» могли быть исчерпаны. Также бот мог быть на паузе, а монета — попасть в чёрный список или не входить в белый.</>}
            />
            <FAQ
              q="Чем МультиБот отличается от отдельных SignalBot и HedgeBot?"
              a={<>МультиБот объединяет сигнальную и хедж-ногу в одну сущность с общим именем, аватаром и лимитами. Технически это две обычные записи, связанные между собой, но в «Моих ботах» они показываются одной карточкой — хедж-нога скрыта, её статистика сложена в карточку сигнальной. Хедж-нога хеджирует именно собственные стратегии сигнальной ноги того же МультиБота, а не весь портфель. Запуск, пауза и удаление карточки применяются сразу к обеим ногам. Опубликовать МультиБота в общую библиотеку пока нельзя — эта функция ещё в разработке.</>}
            />
            <FAQ
              q="Как отправить своего бота на проверку для публикации?"
              a={<>Включите тумблер «Публичный бот» на вкладке «Основное» и сохраните — на карточке бота появится кнопка отправки на проверку (значок самолётика). После отправки статус станет «на проверке».</>}
            />
            <FAQ
              q="Можно ли изменить тип бота после создания?"
              a={<>Нет, тип бота (SignalBot / HedgeBot / MatrixBot / МультиБот) фиксируется при создании и не меняется. Чтобы получить бота другого типа — создайте новый.</>}
            />
          </Section>

        </> }

        </div>
      </div>

      <style>{`
        .fadein { animation: fadein .15s ease-out }
        @keyframes fadein { from { opacity: 0; transform: translateY(4px) } to { opacity: 1; transform: none } }
      `}</style>
    </div>
  )
}
