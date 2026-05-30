import { useState, type CSSProperties } from 'react'

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

/* ─── sidebar nav items ───────────────────────────────────────────────────── */
const SECTIONS = [
  { id: 'overview',    label: 'Обзор страницы' },
  { id: 'add-key',     label: 'Добавление ключа' },
  { id: 'key-card',    label: 'Карточка ключа' },
  { id: 'statuses',    label: 'Статусы' },
  { id: 'audit',       label: 'Вкладка Аудит' },
  { id: 'security',    label: 'Безопасность' },
  { id: 'faq',         label: 'Частые вопросы' },
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
  const [activeSection, setActiveSection] = useState('overview')

  function scrollTo(id: string) {
    setActiveSection(id)
    document.getElementById(id)?.scrollIntoView({ behavior: 'smooth', block: 'start' })
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
          {/* section label */}
          <div style={{
            display: 'inline-flex', alignItems: 'center', gap: 7,
            padding: '5px 10px', marginBottom: 12,
            background: T.blueBg, border: `1px solid ${T.blueBd}`, borderRadius: 8,
            fontSize: 11, fontWeight: 700, color: T.blue,
          }}>
            <IcKey s={11} w={2.2}/> API ключи
          </div>
          <div style={{ display: 'flex', flexDirection: 'column', gap: 1 }}>
            {SECTIONS.map(s => (
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
            {['Стратегии', 'Терминал', 'Боты', 'Балансы', 'Сигналы'].map(s => (
              <div key={s} style={{ fontSize: 11.5, color: T.faint, padding: '4px 0', borderBottom: `1px solid rgba(255,255,255,.03)` }}>{s}</div>
            ))}
          </div>
        </div>

        {/* ─── content ────────────────────────────────────────────────── */}
        <div style={{ minWidth: 0 }}>

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

        </div>
      </div>

      <style>{`
        .fadein { animation: fadein .15s ease-out }
        @keyframes fadein { from { opacity: 0; transform: translateY(4px) } to { opacity: 1; transform: none } }
      `}</style>
    </div>
  )
}
