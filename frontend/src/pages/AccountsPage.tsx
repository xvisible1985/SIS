import { useState, useEffect, useMemo, useCallback, type CSSProperties } from 'react'
import {
  listAccounts, createAccount, deleteAccount,
  verifyAccount, getAccountBalance, toggleAccountActive,
  type VerifyResult, type BalanceResult,
} from '../api/accounts'
import type { ExchangeAccount } from '../types'

/* ─── tokens ─────────────────────────────────────────────────────────────── */
const T = {
  bg: '#0a0d14', panel: '#0c1018', panelHi: '#0e1320',
  card: 'rgba(255,255,255,.02)', border: 'rgba(255,255,255,.06)',
  borderHi: 'rgba(255,255,255,.10)', borderActv: 'rgba(123,140,255,.30)',
  text: '#f2f5fb', body: '#dde3ef', dim: '#7b8aa6', faint: '#5b6479',
  blue: '#5b8cff', green: '#5be0a0',
  greenSoft: 'rgba(65,210,139,.14)', greenBd: 'rgba(65,210,139,.28)',
  orange: '#f7a600',
  orangeSoft: 'rgba(247,166,0,.14)', orangeBd: 'rgba(247,166,0,.30)',
  red: '#fca5a5',
  redSoft: 'rgba(248,113,113,.14)', redBd: 'rgba(248,113,113,.30)',
}
const mono: CSSProperties = { fontFamily: "'JetBrains Mono', monospace" }
const grotesk: CSSProperties = { fontFamily: "'Space Grotesk', sans-serif" }

/* ─── icons ──────────────────────────────────────────────────────────────── */
type IP = { s?: number; w?: number; c?: string }
const svg = (p: IP, children: React.ReactNode) => (
  <svg width={p.s ?? 14} height={p.s ?? 14} viewBox="0 0 24 24" fill="none"
    stroke={p.c ?? 'currentColor'} strokeWidth={p.w ?? 1.7}
    strokeLinecap="round" strokeLinejoin="round">{children}</svg>
)
const IcKey      = (p: IP) => svg(p, <><circle cx="8" cy="15" r="4"/><path d="M10.8 12.2L21 2"/><path d="M16 7l3 3"/><path d="M18 5l3 3"/></>)
const IcPlus     = (p: IP) => svg(p, <path d="M12 5v14M5 12h14"/>)
const IcSearch   = (p: IP) => svg(p, <><circle cx="11" cy="11" r="7"/><path d="M21 21l-4.3-4.3"/></>)
const IcChev     = (p: IP) => svg(p, <path d="M6 9l6 6 6-6"/>)
const IcDot      = (p: IP) => svg({...p, w:0}, <circle cx="12" cy="12" r="3" fill={p.c ?? 'currentColor'}/>)
const IcCheck    = (p: IP) => svg(p, <><circle cx="12" cy="12" r="9"/><path d="M9 12l2 2 4-4"/></>)
const IcCheckMini= (p: IP) => svg(p, <path d="M5 13l4 4 10-10"/>)
const IcAlert    = (p: IP) => svg(p, <><path d="M12 3l10 17H2L12 3z"/><path d="M12 10v4M12 17v.5"/></>)
const IcPause    = (p: IP) => svg(p, <><rect x="6" y="5" width="4" height="14"/><rect x="14" y="5" width="4" height="14"/></>)
const IcPlay     = (p: IP) => svg(p, <path d="M8 5l12 7-12 7V5z"/>)
const IcEye      = (p: IP) => svg(p, <><path d="M1 12s4-7 11-7 11 7 11 7-4 7-11 7S1 12 1 12z"/><circle cx="12" cy="12" r="3"/></>)
const IcEyeOff   = (p: IP) => svg(p, <><path d="M3 3l18 18"/><path d="M10.6 6.2A11 11 0 0 1 12 6c7 0 11 6 11 6a17 17 0 0 1-3.2 3.8"/><path d="M6.6 6.6C3 8.7 1 12 1 12s4 7 11 7c1.8 0 3.4-.4 4.9-1"/><path d="M9.9 9.9a3 3 0 0 0 4.2 4.2"/></>)
const IcCopy     = (p: IP) => svg(p, <><rect x="9" y="9" width="11" height="11" rx="2"/><path d="M5 15V6a2 2 0 0 1 2-2h9"/></>)
const IcEdit     = (p: IP) => svg(p, <><path d="M11 4H4v16h16v-7"/><path d="M18 2l4 4-10 10H8v-4L18 2z"/></>)
const IcTrash    = (p: IP) => svg(p, <><path d="M4 7h16M9 7V4h6v3M6 7l1 13h10l1-13"/></>)
const IcRefresh  = (p: IP) => svg(p, <><path d="M3 12a9 9 0 1 0 3-6.7"/><path d="M3 4v5h5"/></>)
const IcShield   = (p: IP) => svg(p, <path d="M12 3l8 3v6c0 5-3.5 8.5-8 9-4.5-.5-8-4-8-9V6l8-3z"/>)
const IcShieldOk = (p: IP) => svg(p, <><path d="M12 3l8 3v6c0 5-3.5 8.5-8 9-4.5-.5-8-4-8-9V6l8-3z"/><path d="M9 12l2 2 4-4"/></>)
const IcLock     = (p: IP) => svg(p, <><rect x="5" y="11" width="14" height="9" rx="2"/><path d="M8 11V8a4 4 0 0 1 8 0v3"/></>)
const IcGlobe    = (p: IP) => svg(p, <><circle cx="12" cy="12" r="9"/><path d="M3 12h18M12 3a14 14 0 0 1 0 18M12 3a14 14 0 0 0 0 18"/></>)
const IcClock    = (p: IP) => svg(p, <><circle cx="12" cy="12" r="9"/><path d="M12 7v5l3 2"/></>)
const IcZap      = (p: IP) => svg(p, <path d="M13 3L4 14h7l-1 7 9-11h-7l1-7z"/>)
const IcExt      = (p: IP) => svg(p, <><path d="M14 4h6v6"/><path d="M20 4L10 14"/><path d="M19 13v6a1 1 0 0 1-1 1H5a1 1 0 0 1-1-1V6a1 1 0 0 1 1-1h6"/></>)
const IcInfo     = (p: IP) => svg(p, <><circle cx="12" cy="12" r="9"/><path d="M12 8h.01M11 12h1v4h1"/></>)
const IcX        = (p: IP) => svg(p, <path d="M6 6l12 12M18 6L6 18"/>)
const IcFilter   = (p: IP) => svg(p, <path d="M3 5h18M6 12h12M10 19h4"/>)
const IcWallet   = (p: IP) => svg(p, <><rect x="3" y="6" width="18" height="13" rx="2"/><path d="M16 13h2"/><path d="M3 9h15a3 3 0 0 1 0 6H3"/></>)
const IcDownload = (p: IP) => svg(p, <><path d="M12 4v12M6 12l6 6 6-6M5 20h14"/></>)

/* ─── exchange marks ────────────────────────────────────────────────────── */
const Mk = ({ children }: { children: React.ReactNode }) => (
  <svg width="100%" height="100%" viewBox="0 0 24 24" fill="none" stroke="currentColor"
    strokeWidth="2.4" strokeLinecap="round" strokeLinejoin="round">{children}</svg>
)
const MarkBybit   = () => <Mk><path d="M5 17l3-5 3 3 3-6 5 8"/></Mk>
const MarkBinance = () => <Mk><path d="M12 4l4 4-4 4-4-4 4-4z"/><path d="M5 11l3 3-3 3-3-3 3-3z"/><path d="M15 11l3 3-3 3-3-3 3-3z"/><path d="M12 18l4-4-4 4-4-4 4 4z"/></Mk>
const MarkOkx     = () => <Mk><rect x="3" y="3" width="7" height="7" rx="1.4"/><rect x="14" y="3" width="7" height="7" rx="1.4"/><rect x="3" y="14" width="7" height="7" rx="1.4"/><rect x="14" y="14" width="7" height="7" rx="1.4"/></Mk>
const MarkBingx   = () => <Mk><circle cx="12" cy="12" r="8"/><path d="M7 17L17 7"/><path d="M12 4v4M12 16v4M4 12h4M16 12h4"/></Mk>
const MarkBitget  = () => <Mk><path d="M12 3l9 9-9 9-9-9 9-9z"/><path d="M12 8l4 4-4 4-4-4 4-4z"/></Mk>
const MarkMexc    = () => <Mk><path d="M4 18V8l4 5 4-5 4 5 4-5v10"/></Mk>
const MarkKucoin  = () => <Mk><path d="M7 4v16"/><path d="M7 12l7-7"/><path d="M7 12l8 8"/></Mk>
const MarkHtx     = () => <Mk><path d="M5 19V8"/><path d="M11 19v-8"/><path d="M17 19V4"/></Mk>

const EXCHANGES: Record<string, { name: string; color: string; Mark: React.FC; supported?: boolean }> = {
  bybit:   { name: 'Bybit',   color: '#f7a600', Mark: MarkBybit,   supported: true },
  binance: { name: 'Binance', color: '#f0b90b', Mark: MarkBinance, supported: true },
  okx:     { name: 'OKX',     color: '#e8edf5', Mark: MarkOkx },
  bingx:   { name: 'BingX',   color: '#5b8cff', Mark: MarkBingx },
  bitget:  { name: 'Bitget',  color: '#00d4d4', Mark: MarkBitget },
  mexc:    { name: 'MEXC',    color: '#3fa6ff', Mark: MarkMexc },
  kucoin:  { name: 'KuCoin',  color: '#26d391', Mark: MarkKucoin },
  htx:     { name: 'HTX',     color: '#5dc6ff', Mark: MarkHtx },
}

function ExBadge({ id, size = 28 }: { id: string; size?: number }) {
  const e = EXCHANGES[id.toLowerCase()] ?? EXCHANGES.bybit
  const Mk = e.Mark
  const pad = Math.round(size * 0.24)
  return (
    <div style={{
      width: size, height: size, flexShrink: 0, borderRadius: size > 30 ? 10 : 8,
      background: `linear-gradient(135deg, ${e.color}, ${e.color}cc)`,
      color: '#13161e', display: 'flex', alignItems: 'center', justifyContent: 'center',
      boxShadow: `0 2px 8px -3px ${e.color}aa, inset 0 1px 0 rgba(255,255,255,.35)`,
      position: 'relative', overflow: 'hidden',
    }}>
      <div style={{ width: size - pad * 2, height: size - pad * 2, lineHeight: 0 }}><Mk /></div>
    </div>
  )
}

/* ─── status ─────────────────────────────────────────────────────────────── */
type KeyStatus = 'active' | 'error' | 'paused' | 'expiring' | 'pending'
const STATUS: Record<KeyStatus, { label: string; c: string; bg: string; bd: string; pulse?: boolean; spin?: boolean }> = {
  active:   { label: 'активен',    c: T.green,  bg: T.greenSoft,  bd: T.greenBd,  pulse: true },
  error:    { label: 'ошибка',     c: T.red,    bg: T.redSoft,    bd: T.redBd },
  pending:  { label: 'проверка',   c: T.orange, bg: T.orangeSoft, bd: T.orangeBd, spin: true },
  paused:   { label: 'остановлен', c: T.dim,    bg: 'rgba(255,255,255,.05)', bd: T.border },
  expiring: { label: 'истекает',   c: T.orange, bg: T.orangeSoft, bd: T.orangeBd },
}

function StatusPill({ status }: { status: KeyStatus }) {
  const s = STATUS[status]
  const Ic = status === 'error' ? IcAlert : status === 'paused' ? IcPause : status === 'expiring' ? IcClock : status === 'pending' ? IcRefresh : IcDot
  return (
    <div style={{
      display: 'inline-flex', alignItems: 'center', gap: 5,
      padding: '3px 9px 3px 7px', borderRadius: 999,
      background: s.bg, border: `1px solid ${s.bd}`, color: s.c,
      fontSize: 11, fontWeight: 600,
    }}>
      <span style={{ display: 'inline-flex', animation: s.pulse ? 'pulse 1.8s ease-in-out infinite' : s.spin ? 'spin 1s linear infinite' : 'none' }}>
        <Ic s={10} c={s.c} w={2.4} />
      </span>
      {s.label}
    </div>
  )
}

/* ─── perm chips ─────────────────────────────────────────────────────────── */
const PERM_META: Record<string, { label: string; Ic: React.FC<IP>; on: string; warn?: boolean }> = {
  read:     { label: 'Чтение',   Ic: IcEye,      on: T.green },
  trade:    { label: 'Торговля', Ic: IcZap,      on: T.green },
  futures:  { label: 'Futures',  Ic: IcWallet,   on: T.blue },
  withdraw: { label: 'Вывод',    Ic: IcDownload, on: T.red, warn: true },
}

function PermChip({ k, on }: { k: string; on: boolean }) {
  const m = PERM_META[k]; if (!m) return null
  const { Ic } = m
  const color = on ? (m.warn ? T.red : m.on) : T.dim
  const bg  = on ? (m.warn ? T.redSoft : 'rgba(91,140,255,.10)') : 'rgba(255,255,255,.03)'
  const bd  = on ? (m.warn ? T.redBd  : 'rgba(91,140,255,.20)') : T.border
  return (
    <div style={{
      display: 'inline-flex', alignItems: 'center', gap: 5,
      padding: '4px 9px 4px 7px', background: bg, border: `1px solid ${bd}`,
      borderRadius: 999, fontSize: 11, fontWeight: 600, color, opacity: on ? 1 : 0.55,
    }}>
      <Ic s={11} w={2.2} c={color} />
      {m.label}
      {on && !m.warn && <IcCheckMini s={10} w={2.6} c={color} />}
    </div>
  )
}

/* ─── helpers ────────────────────────────────────────────────────────────── */
const fmt$ = (n: number | null | undefined) =>
  n == null ? '—' : '$' + n.toLocaleString('en-US', { minimumFractionDigits: 2, maximumFractionDigits: 2 })

function daysLeft(iso?: string): number | null {
  if (!iso) return null
  return Math.floor((new Date(iso).getTime() - Date.now()) / 86_400_000)
}
function fmtDate(iso?: string): string {
  if (!iso) return '—'
  return new Date(iso).toLocaleDateString('ru-RU', { day: '2-digit', month: 'short', year: 'numeric' })
}

function getStatus(acc: ExchangeAccount, verify?: VerifyResult): KeyStatus {
  if (!acc.is_active) return 'paused'
  if (verify?.ok === false) return 'error'
  if (acc.expires_at) {
    const d = daysLeft(acc.expires_at)
    if (d !== null && d < 21) return 'expiring'
  }
  return 'active'
}

/* ─── sub-components ─────────────────────────────────────────────────────── */
function KeyRowAction({ Ic, label, danger = false, onClick, disabled = false }: {
  Ic: React.FC<IP>; label: string; danger?: boolean; onClick?: () => void; disabled?: boolean
}) {
  return (
    <button onClick={onClick} disabled={disabled} style={{
      display: 'inline-flex', alignItems: 'center', gap: 6, padding: '7px 11px 7px 9px',
      background: danger ? 'rgba(248,113,113,.08)' : 'rgba(255,255,255,.04)',
      border: `1px solid ${danger ? 'rgba(248,113,113,.20)' : T.border}`,
      color: danger ? T.red : T.body, borderRadius: 8, fontSize: 12, fontWeight: 600,
      cursor: disabled ? 'not-allowed' : 'pointer', fontFamily: 'inherit',
      opacity: disabled ? 0.5 : 1,
    }}>
      <Ic s={12} w={2} c={danger ? T.red : T.body} />
      {label}
    </button>
  )
}

function Detail({ label, value, mono: isMono = false, accent, extra }: {
  label: string; value: string; mono?: boolean; accent?: string; extra?: React.ReactNode
}) {
  return (
    <div style={{ padding: '10px 12px', background: T.panel }}>
      <div style={{ fontSize: 10, color: T.dim, textTransform: 'uppercase', letterSpacing: 1.2, fontWeight: 600 }}>{label}</div>
      <div style={{
        marginTop: 4, fontSize: 12, color: accent ?? T.body, fontWeight: 500,
        fontFamily: isMono ? "'JetBrains Mono', monospace" : 'inherit',
        display: 'flex', alignItems: 'center', gap: 6, justifyContent: 'space-between',
      }}>
        <span style={{ overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{value}</span>
        {extra}
      </div>
    </div>
  )
}

function LogLine({ t, m, c, code }: { t: string; m: string; c: string; code: string }) {
  return (
    <div style={{ display: 'flex', alignItems: 'center', gap: 10, color: T.body }}>
      <span style={{ color: T.faint }}>{t}</span>
      <span style={{ color: c, fontWeight: 600 }}>●</span>
      <span style={{ flex: 1, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap', color: T.body }}>{m}</span>
      <span style={{ color: T.dim, flexShrink: 0 }}>{code}</span>
    </div>
  )
}

/* ─── KeyCard ────────────────────────────────────────────────────────────── */
interface KeyCardProps {
  acc: ExchangeAccount
  balance: BalanceResult | null
  verify: VerifyResult | null
  latency: number | null
  expanded: boolean
  testing: boolean
  onToggle: () => void
  onTest: () => void
  onDelete: () => void
  onRotate: () => void
  onPause: () => void
}

function KeyCard({ acc, balance, verify, latency, expanded, testing, onToggle, onTest, onDelete, onRotate, onPause }: KeyCardProps) {
  const [tab, setTab] = useState<'activity' | 'audit' | 'bots'>('activity')
  const status = getStatus(acc, verify ?? undefined)
  const isErr  = status === 'error'
  const isExp  = status === 'expiring'
  const equity = balance?.ok ? balance.equity : null
  const ip = verify?.ips?.join(', ') ?? null

  const perms = {
    read:     verify ? !verify.read_only || true : true,
    trade:    verify ? !verify.read_only : true,
    futures:  verify ? !!verify.permissions?.ContractTrade?.length || !!verify.permissions?.Derivatives?.length : false,
    withdraw: verify ? !!verify.permissions?.Wallet?.some(v => v.toLowerCase().includes('withdraw')) : false,
  }

  const expiresStr = fmtDate(acc.expires_at)
  const createdStr = fmtDate(acc.created_at)
  const expiringDays = daysLeft(acc.expires_at)

  return (
    <div style={{
      background: expanded ? 'linear-gradient(180deg, rgba(91,140,255,.05) 0%, rgba(255,255,255,.02) 100%)' : T.card,
      border: `1px solid ${expanded ? T.borderActv : isErr ? T.redBd : T.border}`,
      borderRadius: 14, overflow: 'hidden',
      boxShadow: expanded ? '0 12px 32px -16px rgba(91,140,255,.4)' : 'none',
      transition: 'border-color .15s, box-shadow .15s, background .15s',
    }}>
      {/* header */}
      <div onClick={onToggle} style={{ padding: '14px 16px', display: 'flex', alignItems: 'center', gap: 14, cursor: 'pointer' }}>
        <ExBadge id={acc.exchange} size={34} />
        <div style={{ minWidth: 0, flex: '1 1 auto' }}>
          <div style={{ display: 'flex', alignItems: 'center', gap: 8, flexWrap: 'wrap' }}>
            <span style={{ ...grotesk, fontSize: 15, fontWeight: 700, color: T.text, letterSpacing: -0.2 }}>
              {acc.label}
            </span>
            <span style={{
              fontSize: 11, color: T.dim, padding: '2px 7px',
              background: 'rgba(255,255,255,.04)', border: `1px solid ${T.border}`, borderRadius: 6, fontWeight: 500,
            }}>
              {EXCHANGES[acc.exchange.toLowerCase()]?.name ?? acc.exchange}
            </span>
            <StatusPill status={status} />
          </div>
          <div style={{ marginTop: 6, display: 'flex', alignItems: 'center', gap: 14, color: T.dim, fontSize: 12, flexWrap: 'wrap' }}>
            <span style={{ ...mono, color: T.body }}>•••• •••• •••• ••••</span>
            {ip ? (
              <span style={{ display: 'inline-flex', alignItems: 'center', gap: 5 }}>
                <IcGlobe s={11} c={T.faint} /> {ip}
              </span>
            ) : verify ? (
              <span style={{ display: 'inline-flex', alignItems: 'center', gap: 5, color: T.orange }}>
                <IcAlert s={11} c={T.orange} /> без IP-whitelist
              </span>
            ) : null}
            <span style={{ display: 'inline-flex', alignItems: 'center', gap: 5 }}>
              <IcClock s={11} c={T.faint} /> {createdStr}
            </span>
          </div>
        </div>
        <div style={{ display: 'flex', alignItems: 'center', gap: 14 }}>
          <div style={{ textAlign: 'right' }}>
            <div style={{ fontSize: 10, color: T.dim, textTransform: 'uppercase', letterSpacing: 1, fontWeight: 600 }}>Equity</div>
            <div style={{ ...grotesk, fontSize: 17, fontWeight: 700, color: isErr ? T.dim : T.text, letterSpacing: -0.3 }}>
              {fmt$(equity)}
            </div>
          </div>
          <div style={{
            width: 30, height: 30, borderRadius: 8, background: 'rgba(255,255,255,.04)',
            display: 'flex', alignItems: 'center', justifyContent: 'center', color: T.dim,
            transition: 'transform .15s', transform: expanded ? 'rotate(180deg)' : 'none',
          }}>
            <IcChev s={14} />
          </div>
        </div>
      </div>

      {/* error banner (collapsed) */}
      {isErr && !expanded && (
        <div style={{ padding: '10px 16px', display: 'flex', alignItems: 'center', gap: 10, background: 'rgba(248,113,113,.06)', borderTop: `1px solid ${T.redBd}` }}>
          <IcAlert s={14} c={T.red} w={2} />
          <span style={{ fontSize: 12, color: T.red, fontWeight: 500 }}>Ошибка подключения — нажмите «Тест» для проверки</span>
        </div>
      )}
      {isExp && !expanded && (
        <div style={{ padding: '10px 16px', display: 'flex', alignItems: 'center', gap: 10, background: 'rgba(247,166,0,.06)', borderTop: `1px solid ${T.orangeBd}` }}>
          <IcClock s={14} c={T.orange} w={2} />
          <span style={{ fontSize: 12, color: T.orange, fontWeight: 500 }}>
            Истекает через {expiringDays} дней — обновите ключ заранее
          </span>
        </div>
      )}

      {/* expanded body */}
      {expanded && (
        <div className="fadein" style={{ padding: '4px 16px 16px', borderTop: `1px solid ${T.border}` }}>
          {/* permissions */}
          <div style={{ marginTop: 14, display: 'flex', alignItems: 'center', gap: 12, flexWrap: 'wrap' }}>
            <span style={{ fontSize: 10, color: T.dim, textTransform: 'uppercase', letterSpacing: 1.2, fontWeight: 600 }}>Права</span>
            {verify ? (
              <>
                <PermChip k="read"     on={perms.read} />
                <PermChip k="trade"    on={perms.trade} />
                <PermChip k="futures"  on={perms.futures} />
                <PermChip k="withdraw" on={perms.withdraw} />
                <span style={{ marginLeft: 'auto', fontSize: 11, color: T.dim }}>
                  <span style={{ color: T.green, fontWeight: 600 }}>✓</span> вывод отключён — мы намеренно не запрашиваем это право
                </span>
              </>
            ) : (
              <span style={{ fontSize: 12, color: T.dim }}>Нажмите «Тест подключения» для загрузки прав</span>
            )}
          </div>

          {/* details grid */}
          <div style={{
            marginTop: 14,
            display: 'grid', gridTemplateColumns: 'repeat(auto-fit, minmax(160px, 1fr))',
            gap: 1, background: T.border, border: `1px solid ${T.border}`, borderRadius: 10, overflow: 'hidden',
          }}>
            <Detail label="API key"      mono value="•••• •••• •••• ••••"    extra={<IcCopy s={12} c={T.dim} />} />
            <Detail label="API secret"   mono value="•••• •••• •••• ••••"    extra={<IcEyeOff s={12} c={T.dim} />} />
            <Detail label="Создан"            value={createdStr} />
            <Detail label="Истекает"          value={expiresStr} accent={isExp ? T.orange : T.body} />
            <Detail label="Латентность"  mono  value={latency != null ? `${latency} ms` : '—'} accent={latency && latency > 300 ? T.orange : T.body} />
            <Detail label="IP whitelist" mono  value={ip ?? (verify ? 'не настроен' : '—')} accent={ip ? T.body : (verify ? T.orange : T.dim)} />
          </div>

          {/* error block */}
          {isErr && (
            <div style={{
              marginTop: 14, padding: '12px 14px',
              background: 'rgba(248,113,113,.06)', border: `1px solid ${T.redBd}`, borderRadius: 10,
              display: 'flex', gap: 10,
            }}>
              <IcAlert s={16} c={T.red} w={2} />
              <div style={{ minWidth: 0 }}>
                <div style={{ fontSize: 12, fontWeight: 600, color: T.red, marginBottom: 3 }}>Подключение прервано</div>
                <div style={{ fontSize: 12, color: T.body, lineHeight: 1.5 }}>
                  {verify?.message ?? 'Ошибка авторизации. Проверьте ключ или создайте новый.'}
                </div>
                <div style={{ marginTop: 8, display: 'flex', gap: 6 }}>
                  <button onClick={onRotate} style={{
                    padding: '6px 10px', background: T.red, color: '#1a0a0a', border: 0, borderRadius: 6,
                    fontSize: 11, fontWeight: 700, cursor: 'pointer',
                  }}>Обновить секрет</button>
                  <button style={{
                    padding: '6px 10px', background: 'transparent', color: T.body, border: `1px solid ${T.border}`,
                    borderRadius: 6, fontSize: 11, fontWeight: 600, cursor: 'pointer',
                  }}>Открыть гайд</button>
                </div>
              </div>
            </div>
          )}

          {/* tabs */}
          <div style={{ marginTop: 14 }}>
            <div style={{
              display: 'flex', gap: 2, padding: 3, background: 'rgba(0,0,0,.25)',
              border: `1px solid ${T.border}`, borderRadius: 8, width: 'fit-content',
            }}>
              {(['activity', 'audit', 'bots'] as const).map(t => (
                <button key={t} onClick={() => setTab(t)} style={{
                  padding: '6px 12px',
                  background: tab === t ? 'rgba(123,140,255,.18)' : 'transparent',
                  color: tab === t ? T.text : T.dim,
                  border: 0, borderRadius: 6, fontSize: 12, fontWeight: 600, cursor: 'pointer', fontFamily: 'inherit',
                }}>
                  {t === 'activity' ? 'Активность' : t === 'audit' ? 'Аудит ключа' : 'Боты · 0'}
                </button>
              ))}
            </div>

            {tab === 'activity' && (
              <div className="fadein" style={{
                marginTop: 10, background: 'rgba(0,0,0,.25)', border: `1px solid ${T.border}`, borderRadius: 8,
                padding: '8px 10px', ...mono, fontSize: 11, display: 'flex', flexDirection: 'column', gap: 4,
              }}>
                <LogLine t="14:42:08" m="GET /v5/account/wallet-balance"   c={T.green} code="200 · 84ms" />
                <LogLine t="14:42:03" m="POST /v5/order/create · BNBUSDT"  c={T.green} code="200 · 121ms" />
                <LogLine t="14:41:42" m="GET /v5/position/list"            c={T.green} code="200 · 76ms" />
                <LogLine t="14:41:31" m="GET /v5/market/tickers · BTC"     c={T.green} code="200 · 62ms" />
                <LogLine t="14:40:55" m="GET /v5/account/wallet-balance"   c={T.green} code="200 · 91ms" />
              </div>
            )}

            {tab === 'audit' && (
              <div className="fadein" style={{
                marginTop: 10, background: 'rgba(0,0,0,.25)', border: `1px solid ${T.border}`, borderRadius: 8,
                padding: '4px 0',
              }}>
                {[
                  { t: 'сегодня · 09:12', Ic: IcRefresh, c: T.body,  msg: 'Автоматическая проверка прав — без изменений', actor: 'system' },
                  { t: createdStr + ' · 10:18', Ic: IcKey,  c: T.green, msg: 'Ключ подключён к платформе', actor: 'Вы' },
                ].map((e, i, arr) => {
                  const Ic = e.Ic
                  return (
                    <div key={i} style={{
                      display: 'flex', gap: 12, padding: '10px 12px',
                      borderBottom: i < arr.length - 1 ? `1px dashed ${T.border}` : 'none',
                    }}>
                      <div style={{
                        width: 24, height: 24, borderRadius: 6, flexShrink: 0,
                        background: 'rgba(255,255,255,.04)', border: `1px solid ${T.border}`,
                        display: 'flex', alignItems: 'center', justifyContent: 'center', color: e.c,
                      }}>
                        <Ic s={12} w={2} />
                      </div>
                      <div style={{ flex: 1, minWidth: 0 }}>
                        <div style={{ fontSize: 12, color: T.body, fontWeight: 500 }}>{e.msg}</div>
                        <div style={{ fontSize: 11, color: T.dim, marginTop: 2, display: 'flex', alignItems: 'center', gap: 6 }}>
                          <span style={{ ...mono }}>{e.t}</span>
                          <span>·</span>
                          <span>{e.actor}</span>
                        </div>
                      </div>
                    </div>
                  )
                })}
              </div>
            )}

            {tab === 'bots' && (
              <div className="fadein" style={{ marginTop: 10 }}>
                <div style={{
                  padding: '24px 16px', textAlign: 'center',
                  background: 'rgba(0,0,0,.2)', border: `1px dashed ${T.border}`, borderRadius: 8,
                  color: T.dim, fontSize: 12,
                }}>
                  К ключу не привязан ни один бот.
                </div>
              </div>
            )}
          </div>

          {/* actions */}
          <div style={{ marginTop: 14, display: 'flex', gap: 8, flexWrap: 'wrap' }}>
            <button onClick={onTest} disabled={testing} style={{
              display: 'inline-flex', alignItems: 'center', gap: 6,
              padding: '8px 14px 8px 11px',
              background: testing ? 'rgba(91,140,255,.18)' : 'linear-gradient(180deg, #4a7dff 0%, #3a67e6 100%)',
              color: '#fff', border: 0, borderRadius: 8, fontSize: 12, fontWeight: 600,
              cursor: testing ? 'progress' : 'pointer', fontFamily: 'inherit',
              boxShadow: testing ? 'none' : '0 6px 16px -8px rgba(74,125,255,.6)',
            }}>
              <span style={{ display: 'inline-flex', animation: testing ? 'spin 1s linear infinite' : 'none' }}>
                <IcRefresh s={12} w={2.2} />
              </span>
              {testing ? 'Проверяем…' : 'Тест подключения'}
            </button>
            <KeyRowAction Ic={IcShield}  label="Изменить IP" />
            <KeyRowAction Ic={IcRefresh} label="Ротейт" onClick={onRotate} />
            <div style={{ flex: 1 }} />
            {status === 'paused'
              ? <KeyRowAction Ic={IcPlay}  label="Возобновить" onClick={onPause} />
              : <KeyRowAction Ic={IcPause} label="Приостановить" onClick={onPause} />
            }
            <KeyRowAction Ic={IcTrash} label="Удалить" danger onClick={onDelete} />
          </div>
        </div>
      )}
    </div>
  )
}

/* ─── Toggle ──────────────────────────────────────────────────────────────── */
function Toggle({ on, onChange, color = T.blue }: { on: boolean; onChange: (v: boolean) => void; color?: string }) {
  return (
    <button onClick={() => onChange(!on)} style={{
      width: 34, height: 20, padding: 0, border: 0, cursor: 'pointer',
      background: on ? color : 'rgba(255,255,255,.10)',
      borderRadius: 999, position: 'relative', transition: 'background .15s',
    }}>
      <span style={{
        position: 'absolute', top: 2, left: on ? 16 : 2, width: 16, height: 16, borderRadius: '50%',
        background: '#fff', transition: 'left .15s', boxShadow: '0 1px 3px rgba(0,0,0,.4)',
      }} />
    </button>
  )
}

function PermRow({ Ic, title, desc, on, onChange, danger = false }: {
  Ic: React.FC<IP>; title: string; desc: string; on: boolean; onChange: (v: boolean) => void; danger?: boolean
}) {
  return (
    <div style={{
      display: 'flex', alignItems: 'center', gap: 12, padding: '10px 12px',
      background: 'rgba(255,255,255,.02)', border: `1px solid ${T.border}`, borderRadius: 10,
    }}>
      <div style={{
        width: 28, height: 28, borderRadius: 7, flexShrink: 0,
        display: 'flex', alignItems: 'center', justifyContent: 'center',
        background: on ? (danger ? T.redSoft : 'rgba(91,140,255,.15)') : 'rgba(255,255,255,.04)',
        border: `1px solid ${on ? (danger ? T.redBd : 'rgba(91,140,255,.25)') : T.border}`,
        color: on ? (danger ? T.red : T.blue) : T.dim,
      }}>
        <Ic s={14} w={2} />
      </div>
      <div style={{ flex: 1, minWidth: 0 }}>
        <div style={{ fontSize: 13, color: T.text, fontWeight: 600 }}>{title}</div>
        <div style={{ fontSize: 11, color: T.dim, marginTop: 1 }}>{desc}</div>
      </div>
      <Toggle on={on} onChange={onChange} color={danger ? '#d04545' : T.blue} />
    </div>
  )
}

function Step({ n, title, hint, children }: { n: number; title: string; hint?: string; children: React.ReactNode }) {
  return (
    <div>
      <div style={{ display: 'flex', alignItems: 'baseline', gap: 8, marginBottom: 8 }}>
        <span style={{
          width: 18, height: 18, borderRadius: 5, background: 'rgba(123,140,255,.15)',
          color: '#b8c8ff', fontSize: 10, fontWeight: 700, ...mono,
          display: 'inline-flex', alignItems: 'center', justifyContent: 'center', flexShrink: 0,
        }}>{n}</span>
        <span style={{ fontSize: 13, color: T.text, fontWeight: 600 }}>{title}</span>
        {hint && <span style={{ fontSize: 11, color: T.dim }}>{hint}</span>}
      </div>
      {children}
    </div>
  )
}

const fieldInput: CSSProperties = {
  width: '100%', background: 'rgba(0,0,0,.3)', color: T.text,
  border: `1px solid ${T.border}`, borderRadius: 8, padding: '9px 12px',
  fontSize: 13, fontFamily: 'inherit', outline: 'none',
}

/* ─── AddKeyPanel ─────────────────────────────────────────────────────────── */
function AddKeyPanel({ onSubmit, onExchangeChange }: {
  onSubmit: (data: { exchange: string; label: string; apiKey: string; secret: string; ip: string }) => Promise<void>
  onExchangeChange: (ex: string) => void
}) {
  const [exchange, setExchange]     = useState('bybit')
  const [label, setLabel]           = useState('')
  const [apiKey, setApiKey]         = useState('')
  const [secret, setSecret]         = useState('')
  const [showSecret, setShowSecret] = useState(false)
  const [ip, setIp]                 = useState('')
  const [perms, setPerms]           = useState({ read: true, trade: true, futures: true, withdraw: false })
  const [testState, setTestState]   = useState<'idle' | 'testing' | 'ok' | 'fail'>('idle')
  const [saving, setSaving]         = useState(false)

  const SUPPORTED = Object.entries(EXCHANGES).filter(([, e]) => e.supported)
  const filled = apiKey.length > 4 && secret.length > 4

  function handleExchange(id: string) {
    setExchange(id)
    onExchangeChange(id)
    setTestState('idle')
  }

  function runTest() {
    if (!filled) return
    setTestState('testing')
    setTimeout(() => setTestState('ok'), 1500)
  }

  async function handleSubmit() {
    if (testState !== 'ok') return
    setSaving(true)
    try {
      await onSubmit({ exchange, label, apiKey, secret, ip })
      setLabel(''); setApiKey(''); setSecret(''); setIp(''); setTestState('idle')
    } finally {
      setSaving(false)
    }
  }

  return (
    <div style={{ background: T.panel, border: `1px solid ${T.border}`, borderRadius: 14, overflow: 'hidden' }}>
      {/* header */}
      <div style={{
        padding: '14px 16px', display: 'flex', alignItems: 'center', gap: 10,
        borderBottom: `1px solid ${T.border}`,
        background: 'linear-gradient(180deg, rgba(91,140,255,.06) 0%, transparent 100%)',
      }}>
        <div style={{
          width: 28, height: 28, borderRadius: 8, display: 'flex', alignItems: 'center', justifyContent: 'center',
          background: 'linear-gradient(135deg, rgba(91,140,255,.3), rgba(193,77,255,.2))',
          border: '1px solid rgba(123,140,255,.3)', color: '#b8c8ff',
        }}>
          <IcPlus s={14} w={2.4} />
        </div>
        <div style={{ flex: 1 }}>
          <div style={{ ...grotesk, fontSize: 14, fontWeight: 700, color: T.text }}>Подключить новый ключ</div>
          <div style={{ fontSize: 11, color: T.dim, marginTop: 1 }}>уже создали ключ на бирже? добавьте его сюда</div>
        </div>
      </div>

      {/* form */}
      <div style={{ padding: '14px 16px', display: 'flex', flexDirection: 'column', gap: 14 }}>
        {/* Step 1 — exchange */}
        <Step n={1} title="Биржа">
          <div style={{ display: 'grid', gridTemplateColumns: 'repeat(2, 1fr)', gap: 6 }}>
            {SUPPORTED.map(([id, e]) => {
              const sel = id === exchange
              return (
                <button key={id} onClick={() => handleExchange(id)} style={{
                  display: 'flex', flexDirection: 'column', alignItems: 'center', gap: 6,
                  padding: '10px 4px',
                  background: sel ? 'linear-gradient(180deg, rgba(91,140,255,.18), rgba(91,140,255,.04))' : 'rgba(255,255,255,.02)',
                  border: `1px solid ${sel ? T.borderActv : T.border}`,
                  borderRadius: 10, cursor: 'pointer', color: sel ? T.text : T.body,
                  boxShadow: sel ? 'inset 0 1px 0 rgba(255,255,255,.08), 0 6px 14px -10px rgba(91,140,255,.5)' : 'none',
                  transition: 'all .12s',
                }}>
                  <ExBadge id={id} size={22} />
                  <span style={{ fontSize: 11, fontWeight: 600 }}>{e.name}</span>
                </button>
              )
            })}
          </div>
        </Step>

        {/* Step 2 — label */}
        <Step n={2} title="Название" hint="отобразится в списке">
          <input
            value={label} onChange={e => setLabel(e.target.value)}
            placeholder={`напр. «main · ${EXCHANGES[exchange]?.name ?? exchange}»`}
            style={fieldInput}
          />
        </Step>

        {/* Step 3 — key + secret */}
        <Step n={3} title="Ключ и секрет">
          <div style={{ display: 'flex', flexDirection: 'column', gap: 6 }}>
            <div style={{ fontSize: 11, color: T.dim, fontWeight: 600 }}>API key</div>
            <input
              value={apiKey} onChange={e => { setApiKey(e.target.value); setTestState('idle') }}
              placeholder="вставьте публичный ключ"
              style={{ ...fieldInput, ...mono, fontSize: 12 }}
            />
          </div>
          <div style={{ display: 'flex', flexDirection: 'column', gap: 6, marginTop: 10 }}>
            <div style={{ fontSize: 11, color: T.dim, fontWeight: 600, display: 'flex', alignItems: 'center', gap: 6 }}>
              API secret
              <span style={{ marginLeft: 'auto', display: 'inline-flex', alignItems: 'center', gap: 4, color: T.faint, fontSize: 10 }}>
                <IcLock s={10} /> хранится в AES-256
              </span>
            </div>
            <div style={{ position: 'relative' }}>
              <input
                type={showSecret ? 'text' : 'password'}
                value={secret} onChange={e => { setSecret(e.target.value); setTestState('idle') }}
                placeholder="секрет"
                style={{ ...fieldInput, ...mono, fontSize: 12, paddingRight: 38 }}
              />
              <button onClick={() => setShowSecret(v => !v)} style={{
                position: 'absolute', right: 6, top: '50%', transform: 'translateY(-50%)',
                background: 'transparent', border: 0, color: T.dim, cursor: 'pointer', padding: 6,
              }}>
                {showSecret ? <IcEyeOff s={14} /> : <IcEye s={14} />}
              </button>
            </div>
          </div>
        </Step>

        {/* Step 4 — permissions (educational) */}
        <Step n={4} title="Права" hint="включите на бирже">
          <div style={{ display: 'flex', flexDirection: 'column', gap: 6 }}>
            <PermRow Ic={IcEye}      title="Чтение"       desc="баланс, ордера, история"           on={perms.read}     onChange={v => setPerms(p => ({...p, read: v}))} />
            <PermRow Ic={IcZap}      title="Торговля"      desc="спот / маржа · открытие ордеров"   on={perms.trade}    onChange={v => setPerms(p => ({...p, trade: v}))} />
            <PermRow Ic={IcWallet}   title="Деривативы"    desc="фьючерсы, perp, опционы"           on={perms.futures}  onChange={v => setPerms(p => ({...p, futures: v}))} />
            <PermRow Ic={IcDownload} title="Вывод средств" desc="нам не нужно — оставьте выключен" on={perms.withdraw} onChange={v => setPerms(p => ({...p, withdraw: v}))} danger />
          </div>
        </Step>

        {/* Step 5 — IP */}
        <Step n={5} title="IP-whitelist" hint="рекомендуем">
          <div style={{ position: 'relative' }}>
            <input
              value={ip} onChange={e => setIp(e.target.value)}
              placeholder="185.94.32.0/24 (наши IP)"
              style={{ ...fieldInput, ...mono, fontSize: 12, paddingRight: 90 }}
            />
            <button onClick={() => setIp('185.94.32.0/24')} style={{
              position: 'absolute', right: 5, top: 5, bottom: 5,
              padding: '0 10px', background: 'rgba(91,140,255,.12)',
              border: '1px solid rgba(91,140,255,.25)', color: '#b8c8ff',
              borderRadius: 6, fontSize: 11, fontWeight: 600, cursor: 'pointer', fontFamily: 'inherit',
            }}>наши IP</button>
          </div>
        </Step>

        {/* test + save */}
        <div style={{ display: 'flex', gap: 8, marginTop: 4 }}>
          <button onClick={runTest} disabled={!filled || testState === 'testing'} style={{
            flex: 1, display: 'flex', alignItems: 'center', justifyContent: 'center', gap: 6, padding: '10px 0',
            background: filled ? (testState === 'ok' ? T.greenSoft : 'rgba(255,255,255,.04)') : 'rgba(255,255,255,.02)',
            color: testState === 'ok' ? T.green : (filled ? T.body : T.faint),
            border: `1px solid ${testState === 'ok' ? T.greenBd : T.border}`,
            borderRadius: 8, fontSize: 12, fontWeight: 600,
            cursor: filled ? 'pointer' : 'not-allowed', fontFamily: 'inherit',
          }}>
            {testState === 'idle'    && <><IcRefresh s={12} w={2.2} /> Проверить ключ</>}
            {testState === 'testing' && <><span style={{ display: 'inline-flex', animation: 'spin 1s linear infinite' }}><IcRefresh s={12} w={2.2} /></span> Подключаемся…</>}
            {testState === 'ok'      && <><IcCheck s={12} w={2} /> Подключение успешно</>}
          </button>
          <button disabled={testState !== 'ok' || saving} onClick={handleSubmit} style={{
            flex: 1, display: 'flex', alignItems: 'center', justifyContent: 'center', gap: 6, padding: '10px 0',
            background: testState === 'ok' ? 'linear-gradient(180deg, #4a7dff 0%, #3a67e6 100%)' : 'rgba(255,255,255,.04)',
            color: testState === 'ok' ? '#fff' : T.faint,
            border: testState === 'ok' ? 0 : `1px solid ${T.border}`,
            borderRadius: 8, fontSize: 12, fontWeight: 700,
            cursor: testState === 'ok' ? 'pointer' : 'not-allowed', fontFamily: 'inherit',
            boxShadow: testState === 'ok' ? '0 6px 16px -8px rgba(74,125,255,.6)' : 'none',
          }}>
            {saving ? 'Сохранение…' : 'Сохранить ключ'}
          </button>
        </div>
      </div>
    </div>
  )
}

/* ─── HowTo ───────────────────────────────────────────────────────────────── */
function HowTo({ exchange }: { exchange: string }) {
  const ex = EXCHANGES[exchange] ?? EXCHANGES.bybit
  const steps = [
    { t: 'Зайдите в раздел API', d: `Профиль → API · ${ex.name}` },
    { t: 'Создайте новый ключ', d: 'Системный, без HMAC-привязки к устройству' },
    { t: 'Включите права: чтение и торговля', d: 'Spot, Derivatives — по необходимости' },
    { t: 'Запретите вывод средств', d: 'Мы не запрашиваем этого права' },
    { t: 'Привяжите наши IP', d: '185.94.32.0/24 — повышает безопасность' },
    { t: 'Скопируйте key + secret сюда', d: 'Secret показывается только при создании' },
  ]
  return (
    <div style={{ background: T.panel, border: `1px solid ${T.border}`, borderRadius: 14, padding: '14px 16px' }}>
      <div style={{ display: 'flex', alignItems: 'center', gap: 10, marginBottom: 12 }}>
        <ExBadge id={exchange} size={22} />
        <div style={{ flex: 1 }}>
          <div style={{ ...grotesk, fontSize: 13, fontWeight: 700, color: T.text }}>Как создать ключ на {ex.name}</div>
          <div style={{ fontSize: 11, color: T.dim, marginTop: 1 }}>~ 90 секунд · не отправляйте секрет в чат</div>
        </div>
        <a href="#" style={{
          display: 'inline-flex', alignItems: 'center', gap: 5, fontSize: 11, color: '#b8c8ff', textDecoration: 'none',
          padding: '4px 8px', background: 'rgba(91,140,255,.10)', border: '1px solid rgba(91,140,255,.20)', borderRadius: 6, fontWeight: 600,
        }}>
          гайд <IcExt s={10} />
        </a>
      </div>
      <div style={{ display: 'flex', flexDirection: 'column', gap: 0 }}>
        {steps.map((s, i) => (
          <div key={i} style={{ display: 'flex', gap: 12, padding: '8px 0', borderTop: i ? `1px dashed ${T.border}` : 'none' }}>
            <div style={{
              flexShrink: 0, width: 20, height: 20, borderRadius: 6,
              background: 'rgba(255,255,255,.04)', border: `1px solid ${T.border}`,
              color: T.body, fontSize: 11, fontWeight: 700, ...mono,
              display: 'inline-flex', alignItems: 'center', justifyContent: 'center',
            }}>{i + 1}</div>
            <div style={{ flex: 1, minWidth: 0 }}>
              <div style={{ fontSize: 12, color: T.text, fontWeight: 600 }}>{s.t}</div>
              <div style={{ fontSize: 11, color: T.dim, marginTop: 1 }}>{s.d}</div>
            </div>
          </div>
        ))}
      </div>
      <div style={{
        marginTop: 12, padding: '10px 12px',
        background: 'rgba(65,210,139,.05)', border: `1px solid ${T.greenBd}`, borderRadius: 10,
        display: 'flex', gap: 10, alignItems: 'flex-start',
      }}>
        <IcShieldOk s={14} c={T.green} w={2} />
        <div style={{ fontSize: 11, color: T.body, lineHeight: 1.5 }}>
          <b style={{ color: T.green }}>Что мы никогда не делаем:</b> не запрашиваем «Withdraw», не отправляем ключ на третьи серверы,
          не показываем секрет после сохранения.
        </div>
      </div>
    </div>
  )
}

/* ─── EmptyState ─────────────────────────────────────────────────────────── */
function EmptyState() {
  return (
    <div style={{
      background: 'linear-gradient(180deg, rgba(91,140,255,.04) 0%, rgba(255,255,255,.01) 100%)',
      border: `1px dashed ${T.borderHi}`, borderRadius: 16,
      padding: '48px 32px', display: 'flex', flexDirection: 'column', alignItems: 'center',
      textAlign: 'center', gap: 14,
    }}>
      <div style={{
        width: 64, height: 64, borderRadius: 18,
        background: 'linear-gradient(135deg, rgba(91,140,255,.25), rgba(193,77,255,.15))',
        border: '1px solid rgba(123,140,255,.30)',
        display: 'flex', alignItems: 'center', justifyContent: 'center', color: '#b8c8ff',
        boxShadow: '0 20px 40px -20px rgba(91,140,255,.6)',
      }}>
        <IcKey s={28} w={1.8} />
      </div>
      <div>
        <h2 style={{ margin: 0, ...grotesk, fontSize: 20, fontWeight: 700, color: T.text, letterSpacing: -0.3 }}>
          Подключите первый API-ключ
        </h2>
        <p style={{ margin: '8px auto 0', maxWidth: 440, fontSize: 13, color: T.dim, lineHeight: 1.5 }}>
          После подключения платформа сможет читать ваш баланс и выставлять ордера. Все секреты шифруются на нашей стороне.
        </p>
      </div>
      <div style={{ display: 'flex', alignItems: 'center', gap: 8, flexWrap: 'wrap', justifyContent: 'center' }}>
        {Object.entries(EXCHANGES).slice(0, 6).map(([id, e]) => (
          <div key={id} style={{
            display: 'inline-flex', alignItems: 'center', gap: 6,
            padding: '5px 10px 5px 5px', background: 'rgba(255,255,255,.03)',
            border: `1px solid ${T.border}`, borderRadius: 999, fontSize: 11, color: T.body, fontWeight: 500,
          }}>
            <ExBadge id={id} size={16} />
            {e.name}
          </div>
        ))}
        <span style={{ fontSize: 11, color: T.dim }}>+ ещё 2</span>
      </div>
    </div>
  )
}

/* ─── Toolbar ────────────────────────────────────────────────────────────── */
function Toolbar({ search, setSearch, filter, setFilter, counts }: {
  search: string; setSearch: (v: string) => void
  filter: string; setFilter: (v: string) => void
  counts: Record<string, number>
}) {
  const filters = [
    { id: 'all',    label: 'Все',               c: undefined },
    { id: 'active', label: 'Активные',           c: T.green },
    { id: 'issues', label: 'Требуют внимания',   c: T.orange },
    { id: 'paused', label: 'Остановленные',      c: T.dim },
  ]
  return (
    <div style={{ display: 'flex', alignItems: 'center', gap: 10, marginBottom: 14, flexWrap: 'wrap' }}>
      <div style={{ position: 'relative', flex: '1 1 240px', maxWidth: 340 }}>
        <input
          value={search} onChange={e => setSearch(e.target.value)}
          placeholder="Поиск по названию или бирже…"
          style={{
            width: '100%', background: 'rgba(0,0,0,.25)', color: T.text,
            border: `1px solid ${T.border}`, borderRadius: 8, padding: '8px 12px 8px 34px',
            fontSize: 12, fontFamily: 'inherit', outline: 'none',
          }}
        />
        <span style={{ position: 'absolute', left: 11, top: '50%', transform: 'translateY(-50%)', pointerEvents: 'none' }}>
          <IcSearch s={13} c={T.dim} />
        </span>
      </div>
      <div style={{ display: 'flex', gap: 4, padding: 3, background: 'rgba(0,0,0,.25)', border: `1px solid ${T.border}`, borderRadius: 8 }}>
        {filters.map(f => (
          <button key={f.id} onClick={() => setFilter(f.id)} style={{
            display: 'inline-flex', alignItems: 'center', gap: 6,
            padding: '6px 10px',
            background: filter === f.id ? 'rgba(123,140,255,.18)' : 'transparent',
            color: filter === f.id ? T.text : T.dim,
            border: 0, borderRadius: 6, fontSize: 12, fontWeight: 600, cursor: 'pointer', fontFamily: 'inherit',
          }}>
            {f.label}
            <span style={{
              fontSize: 10, ...mono,
              color: filter === f.id ? (f.c ?? '#b8c8ff') : T.faint, fontWeight: 700,
            }}>{counts[f.id] ?? 0}</span>
          </button>
        ))}
      </div>
      <div style={{ flex: 1 }} />
      <button style={{
        display: 'inline-flex', alignItems: 'center', gap: 6, padding: '8px 11px',
        background: 'rgba(255,255,255,.04)', border: `1px solid ${T.border}`,
        color: T.body, borderRadius: 8, fontSize: 12, fontWeight: 600, cursor: 'pointer', fontFamily: 'inherit',
      }}>
        <IcFilter s={12} c={T.dim} /> Биржа: все
      </button>
    </div>
  )
}

/* ─── StatsStrip ─────────────────────────────────────────────────────────── */
function StatCard({ label, value, sub, accent, isMono = false }: {
  label: string; value: string | number; sub: string; accent?: string; isMono?: boolean
}) {
  return (
    <div style={{
      flex: '1 1 180px', background: T.panel, border: `1px solid ${T.border}`, borderRadius: 12,
      padding: '14px 16px', display: 'flex', flexDirection: 'column', gap: 4,
    }}>
      <div style={{ fontSize: 10, color: T.dim, textTransform: 'uppercase', letterSpacing: 1.2, fontWeight: 600 }}>{label}</div>
      <div style={{
        fontFamily: isMono ? "'JetBrains Mono', monospace" : "'Space Grotesk', sans-serif",
        fontSize: isMono ? 18 : 22, fontWeight: 700, color: accent ?? T.text, letterSpacing: -0.4, lineHeight: 1.1,
      }}>{value}</div>
      <div style={{ fontSize: 11, color: T.dim }}>{sub}</div>
    </div>
  )
}

function StatsStrip({ accounts, balances, verifyData }: {
  accounts: ExchangeAccount[]
  balances: Record<string, BalanceResult>
  verifyData: Record<string, VerifyResult>
}) {
  const active   = accounts.filter(a => a.is_active).length
  const issues   = accounts.filter(a => {
    const v = verifyData[a.id]
    return (v && !v.ok) || (a.expires_at && (daysLeft(a.expires_at) ?? 999) < 21)
  }).length
  const equity   = Object.values(balances).reduce((s, b) => s + (b.ok && b.equity ? b.equity : 0), 0)
  const exchanges = new Set(accounts.map(a => a.exchange.toLowerCase())).size

  return (
    <div style={{ display: 'flex', gap: 14, flexWrap: 'wrap', marginBottom: 22 }}>
      <StatCard label="Всего ключей"           value={accounts.length} sub={`${exchanges} бирж`} />
      <StatCard label="Активных"               value={active}          sub="торгуют сейчас"       accent={T.green} />
      <StatCard label="Требуют внимания"        value={issues}          sub={issues ? 'ошибка / истекает' : 'всё в порядке'} accent={issues ? T.orange : T.body} />
      <StatCard label="Совокупный equity"       value={fmt$(equity)}    sub="по подключённым счетам" />
      <StatCard label="Последняя синхронизация" value="авто"            sub="каждые 30s"            isMono />
    </div>
  )
}

/* ─── PageHead ────────────────────────────────────────────────────────────── */
function PageHead({ count }: { count: number }) {
  return (
    <div style={{ display: 'flex', alignItems: 'flex-end', gap: 24, flexWrap: 'wrap', marginBottom: 22 }}>
      <div style={{ flex: '1 1 320px' }}>
        <div style={{ display: 'flex', alignItems: 'center', gap: 10, marginBottom: 8 }}>
          <div style={{
            width: 28, height: 28, borderRadius: 8, display: 'flex', alignItems: 'center', justifyContent: 'center',
            background: 'linear-gradient(135deg, rgba(91,140,255,.25), rgba(193,77,255,.18))',
            border: '1px solid rgba(123,140,255,.3)', color: '#b8c8ff',
          }}>
            <IcKey s={15} w={2} />
          </div>
          <h1 style={{ ...grotesk, fontSize: 24, fontWeight: 700, letterSpacing: -0.5, margin: 0, color: T.text }}>
            API ключи
          </h1>
          <span style={{
            ...mono, fontSize: 11, color: T.dim,
            padding: '3px 8px', background: 'rgba(255,255,255,.04)', border: `1px solid ${T.border}`, borderRadius: 6,
          }}>{count} подключено</span>
        </div>
        <p style={{ margin: 0, fontSize: 13, color: T.dim, maxWidth: 680, lineHeight: 1.5 }}>
          Подключите ключ, который вы создали в личном кабинете биржи — платформа будет использовать его для торговли и чтения баланса.
          Мы никогда не запрашиваем право на вывод и храним секрет в зашифрованном виде.
        </p>
      </div>
      <div style={{
        display: 'flex', alignItems: 'center', gap: 10, padding: '10px 12px',
        background: T.panel, border: `1px solid ${T.border}`, borderRadius: 12,
      }}>
        <IcShieldOk s={16} c={T.green} w={1.8} />
        <div style={{ fontSize: 12, color: T.body, lineHeight: 1.3 }}>
          <div style={{ fontWeight: 600, color: T.text }}>AES-256 · IP whitelist</div>
          <div style={{ color: T.dim, fontSize: 11 }}>данные зашифрованы на сервере</div>
        </div>
      </div>
    </div>
  )
}

/* ─── Modals ─────────────────────────────────────────────────────────────── */
function Modal({ open, onClose, children }: { open: boolean; onClose: () => void; children: React.ReactNode }) {
  if (!open) return null
  return (
    <div style={{
      position: 'fixed', inset: 0, zIndex: 100, display: 'flex', alignItems: 'center', justifyContent: 'center',
      background: 'rgba(8,10,16,.65)', backdropFilter: 'blur(6px)', padding: 20,
    }} onClick={onClose}>
      <div onClick={e => e.stopPropagation()} style={{
        width: '100%', maxWidth: 460,
        background: T.panel, border: `1px solid ${T.borderHi}`, borderRadius: 16,
        boxShadow: '0 40px 80px -20px rgba(0,0,0,.7)',
        animation: 'modalIn .18s ease-out', overflow: 'hidden',
      }}>
        {children}
      </div>
    </div>
  )
}

function ConfirmDelete({ acc, onCancel, onConfirm }: {
  acc: ExchangeAccount; onCancel: () => void; onConfirm: () => void
}) {
  const [text, setText] = useState('')
  const phrase = 'УДАЛИТЬ'
  const can = text.trim().toUpperCase() === phrase
  return (
    <>
      <div style={{ padding: '18px 20px 14px', borderBottom: `1px solid ${T.border}`, display: 'flex', alignItems: 'center', gap: 12 }}>
        <div style={{
          width: 36, height: 36, borderRadius: 10, background: T.redSoft, border: `1px solid ${T.redBd}`,
          display: 'flex', alignItems: 'center', justifyContent: 'center', color: T.red, flexShrink: 0,
        }}>
          <IcTrash s={18} w={2} />
        </div>
        <div style={{ flex: 1 }}>
          <div style={{ ...grotesk, fontSize: 15, fontWeight: 700, color: T.text }}>Удалить API ключ</div>
          <div style={{ fontSize: 12, color: T.dim, marginTop: 2 }}>«{acc.label}» · {EXCHANGES[acc.exchange.toLowerCase()]?.name ?? acc.exchange}</div>
        </div>
        <button onClick={onCancel} style={{ background: 'transparent', border: 0, color: T.dim, cursor: 'pointer', padding: 6 }}>
          <IcX s={16} />
        </button>
      </div>
      <div style={{ padding: '16px 20px' }}>
        <p style={{ margin: '0 0 10px', fontSize: 13, color: T.body, lineHeight: 1.55 }}>
          Ключ будет удалён из платформы. Все стратегии и боты, использующие его, остановятся.
          На стороне биржи ключ <b style={{ color: T.text }}>останется активным</b> — удалите его в личном кабинете биржи.
        </p>
        <div style={{ fontSize: 11, color: T.dim, marginBottom: 6 }}>
          Введите <span style={{ ...mono, color: T.text, fontWeight: 600 }}>{phrase}</span> для подтверждения:
        </div>
        <input
          value={text} onChange={e => setText(e.target.value)} autoFocus
          style={{
            width: '100%', background: 'rgba(0,0,0,.3)', color: T.text,
            border: `1px solid ${can ? T.redBd : T.border}`, borderRadius: 8, padding: '9px 12px',
            fontSize: 13, ...mono, outline: 'none',
          }}
        />
      </div>
      <div style={{ padding: '12px 20px 18px', display: 'flex', gap: 8, justifyContent: 'flex-end', background: 'rgba(255,255,255,.01)' }}>
        <button onClick={onCancel} style={{
          padding: '9px 14px', background: 'rgba(255,255,255,.04)', color: T.body,
          border: `1px solid ${T.border}`, borderRadius: 8, fontSize: 12, fontWeight: 600, cursor: 'pointer', fontFamily: 'inherit',
        }}>Отмена</button>
        <button disabled={!can} onClick={onConfirm} style={{
          padding: '9px 14px',
          background: can ? '#d04545' : 'rgba(208,69,69,.20)',
          color: can ? '#fff' : 'rgba(252,165,165,.4)',
          border: 0, borderRadius: 8, fontSize: 12, fontWeight: 700,
          cursor: can ? 'pointer' : 'not-allowed', fontFamily: 'inherit',
          boxShadow: can ? '0 6px 16px -8px rgba(208,69,69,.7)' : 'none',
        }}>Удалить ключ</button>
      </div>
    </>
  )
}

function ConfirmRotate({ acc, onCancel, onConfirm }: {
  acc: ExchangeAccount; onCancel: () => void; onConfirm: (key: string, secret: string) => void
}) {
  const [newKey, setNewKey]     = useState('')
  const [newSecret, setNewSecret] = useState('')
  const [show, setShow]         = useState(false)
  const filled = newKey.length > 4 && newSecret.length > 4
  return (
    <>
      <div style={{ padding: '18px 20px 14px', borderBottom: `1px solid ${T.border}`, display: 'flex', alignItems: 'center', gap: 12 }}>
        <div style={{
          width: 36, height: 36, borderRadius: 10,
          background: 'rgba(91,140,255,.15)', border: '1px solid rgba(91,140,255,.30)',
          display: 'flex', alignItems: 'center', justifyContent: 'center', color: '#b8c8ff', flexShrink: 0,
        }}>
          <IcRefresh s={18} w={2} />
        </div>
        <div style={{ flex: 1 }}>
          <div style={{ ...grotesk, fontSize: 15, fontWeight: 700, color: T.text }}>Ротейт ключа</div>
          <div style={{ fontSize: 12, color: T.dim, marginTop: 2 }}>«{acc.label}» · {EXCHANGES[acc.exchange.toLowerCase()]?.name ?? acc.exchange}</div>
        </div>
        <button onClick={onCancel} style={{ background: 'transparent', border: 0, color: T.dim, cursor: 'pointer', padding: 6 }}>
          <IcX s={16} />
        </button>
      </div>
      <div style={{ padding: '16px 20px' }}>
        <p style={{ margin: '0 0 14px', fontSize: 13, color: T.body, lineHeight: 1.55 }}>
          Создайте новую пару key/secret в личном кабинете {EXCHANGES[acc.exchange.toLowerCase()]?.name ?? acc.exchange} и вставьте сюда.
          Старая пара будет отозвана, стратегии переключатся на новую.
        </p>
        <div style={{ display: 'flex', flexDirection: 'column', gap: 10 }}>
          <div style={{ display: 'flex', flexDirection: 'column', gap: 6 }}>
            <div style={{ fontSize: 11, color: T.dim, fontWeight: 600 }}>Новый API key</div>
            <input value={newKey} onChange={e => setNewKey(e.target.value)}
              placeholder="вставьте новый публичный ключ"
              style={{ ...fieldInput, ...mono, fontSize: 12 }} />
          </div>
          <div style={{ display: 'flex', flexDirection: 'column', gap: 6 }}>
            <div style={{ fontSize: 11, color: T.dim, fontWeight: 600 }}>Новый API secret</div>
            <div style={{ position: 'relative' }}>
              <input type={show ? 'text' : 'password'} value={newSecret} onChange={e => setNewSecret(e.target.value)}
                placeholder="новый секрет"
                style={{ ...fieldInput, ...mono, fontSize: 12, paddingRight: 38 }} />
              <button onClick={() => setShow(v => !v)} style={{
                position: 'absolute', right: 6, top: '50%', transform: 'translateY(-50%)',
                background: 'transparent', border: 0, color: T.dim, cursor: 'pointer', padding: 6,
              }}>{show ? <IcEyeOff s={14} /> : <IcEye s={14} />}</button>
            </div>
          </div>
        </div>
        <div style={{
          marginTop: 14, padding: '10px 12px',
          background: 'rgba(65,210,139,.05)', border: `1px solid ${T.greenBd}`, borderRadius: 8,
          display: 'flex', gap: 8,
        }}>
          <IcShieldOk s={14} c={T.green} w={2} />
          <div style={{ fontSize: 11, color: T.body, lineHeight: 1.5 }}>
            Права, IP-whitelist и привязки ботов сохранятся. После сохранения старая пара перестанет работать.
          </div>
        </div>
      </div>
      <div style={{ padding: '12px 20px 18px', display: 'flex', gap: 8, justifyContent: 'flex-end', background: 'rgba(255,255,255,.01)' }}>
        <button onClick={onCancel} style={{
          padding: '9px 14px', background: 'rgba(255,255,255,.04)', color: T.body,
          border: `1px solid ${T.border}`, borderRadius: 8, fontSize: 12, fontWeight: 600, cursor: 'pointer', fontFamily: 'inherit',
        }}>Отмена</button>
        <button disabled={!filled} onClick={() => onConfirm(newKey, newSecret)} style={{
          padding: '9px 14px',
          background: filled ? 'linear-gradient(180deg, #4a7dff 0%, #3a67e6 100%)' : 'rgba(91,140,255,.20)',
          color: filled ? '#fff' : 'rgba(184,200,255,.5)',
          border: 0, borderRadius: 8, fontSize: 12, fontWeight: 700,
          cursor: filled ? 'pointer' : 'not-allowed', fontFamily: 'inherit',
          boxShadow: filled ? '0 6px 16px -8px rgba(74,125,255,.6)' : 'none',
        }}>Заменить ключ</button>
      </div>
    </>
  )
}

/* ─── Toast ──────────────────────────────────────────────────────────────── */
type ToastData = { kind: 'success' | 'info' | 'error'; title: string; desc?: string }

function Toast({ toast, onClose }: { toast: ToastData | null; onClose: () => void }) {
  useEffect(() => {
    if (!toast) return
    const t = setTimeout(onClose, 4200)
    return () => clearTimeout(t)
  }, [toast, onClose])
  if (!toast) return null
  const map = {
    success: { c: T.green, bg: T.greenSoft, bd: T.greenBd, Ic: IcCheck },
    info:    { c: '#b8c8ff', bg: 'rgba(91,140,255,.12)', bd: 'rgba(91,140,255,.25)', Ic: IcInfo },
    error:   { c: T.red,   bg: T.redSoft,   bd: T.redBd,   Ic: IcAlert },
  }
  const m = map[toast.kind]
  const Ic = m.Ic
  return (
    <div style={{
      position: 'fixed', bottom: 24, left: '50%', transform: 'translateX(-50%)', zIndex: 200,
      display: 'flex', alignItems: 'center', gap: 10,
      padding: '12px 16px 12px 13px',
      background: T.panelHi, border: `1px solid ${m.bd}`, borderRadius: 12,
      boxShadow: '0 20px 40px -10px rgba(0,0,0,.6)',
      animation: 'slideUp .22s ease-out', maxWidth: 480,
    }}>
      <div style={{
        width: 28, height: 28, borderRadius: 8, background: m.bg, border: `1px solid ${m.bd}`,
        display: 'flex', alignItems: 'center', justifyContent: 'center', color: m.c, flexShrink: 0,
      }}>
        <Ic s={14} w={2} />
      </div>
      <div style={{ minWidth: 0, flex: 1 }}>
        <div style={{ fontSize: 13, color: T.text, fontWeight: 600 }}>{toast.title}</div>
        {toast.desc && <div style={{ fontSize: 11, color: T.dim, marginTop: 1 }}>{toast.desc}</div>}
      </div>
      <button onClick={onClose} style={{ background: 'transparent', border: 0, color: T.dim, cursor: 'pointer', padding: 6 }}>
        <IcX s={14} />
      </button>
    </div>
  )
}

/* ─── AccountsPage ────────────────────────────────────────────────────────── */
export function AccountsPage() {
  const [accounts, setAccounts]     = useState<ExchangeAccount[]>([])
  const [balances, setBalances]     = useState<Record<string, BalanceResult>>({})
  const [verifyData, setVerifyData] = useState<Record<string, VerifyResult>>({})
  const [latencies, setLatencies]   = useState<Record<string, number>>({})
  const [loading, setLoading]       = useState(true)
  const [expandedId, setExpandedId] = useState<string | null>(null)
  const [search, setSearch]         = useState('')
  const [filter, setFilter]         = useState('all')
  const [testing, setTesting]       = useState<string | null>(null)
  const [modal, setModal]           = useState<null | { type: 'delete' | 'rotate'; acc: ExchangeAccount }>(null)
  const [toast, setToast]           = useState<ToastData | null>(null)
  const [howToEx, setHowToEx]       = useState('bybit')

  const showToast = useCallback((t: ToastData) => { setToast(t) }, [])

  const load = useCallback(async () => {
    setLoading(true)
    try {
      const accs = await listAccounts()
      setAccounts(accs)
      const bals: Record<string, BalanceResult> = {}
      await Promise.allSettled(accs.map(async a => {
        try { bals[a.id] = await getAccountBalance(a.id) } catch { /* skip */ }
      }))
      setBalances(bals)
    } catch {
      setAccounts([])
    } finally {
      setLoading(false)
    }
  }, [])

  useEffect(() => { load() }, [load])

  async function handleTest(acc: ExchangeAccount) {
    setTesting(acc.id)
    const t0 = Date.now()
    try {
      const result = await verifyAccount(acc.id)
      const lat = Date.now() - t0
      setVerifyData(v => ({ ...v, [acc.id]: result }))
      setLatencies(l => ({ ...l, [acc.id]: lat }))
      if (result.ok) {
        showToast({ kind: 'success', title: 'Подключение успешно', desc: `${lat} ms · ключ активен` })
      } else {
        showToast({ kind: 'error', title: 'Ошибка подключения', desc: result.message ?? 'Проверьте ключ' })
      }
    } catch {
      showToast({ kind: 'error', title: 'Ошибка проверки', desc: 'Не удалось связаться с биржей' })
    } finally {
      setTesting(null)
    }
  }

  async function handleAddKey(data: { exchange: string; label: string; apiKey: string; secret: string; ip: string }) {
    try {
      const newAcc = await createAccount({
        exchange: data.exchange,
        label: data.label || `${EXCHANGES[data.exchange]?.name ?? data.exchange} · ${new Date().toLocaleDateString('ru-RU')}`,
        api_key: data.apiKey,
        secret:  data.secret,
      })
      setAccounts(prev => [newAcc, ...prev])
      setExpandedId(newAcc.id)
      window.dispatchEvent(new Event('accounts-changed'))
      showToast({
        kind: 'success',
        title: `${EXCHANGES[data.exchange]?.name ?? data.exchange} подключён`,
        desc: `«${newAcc.label}» · ключ активен, баланс синхронизируется`,
      })
      // load balance for new account
      getAccountBalance(newAcc.id).then(b => setBalances(prev => ({ ...prev, [newAcc.id]: b }))).catch(() => {})
    } catch (err: unknown) {
      const msg = (err as { response?: { data?: { error?: string } } })?.response?.data?.error
      showToast({ kind: 'error', title: 'Ошибка добавления', desc: msg ?? 'Проверьте данные' })
      throw err
    }
  }

  async function handleToggle(acc: ExchangeAccount) {
    try {
      const updated = await toggleAccountActive(acc.id)
      setAccounts(prev => prev.map(a => a.id === updated.id ? updated : a))
      window.dispatchEvent(new Event('accounts-changed'))
      showToast({ kind: 'info', title: updated.is_active ? 'Ключ активирован' : 'Ключ приостановлен', desc: `«${acc.label}»` })
    } catch {
      showToast({ kind: 'error', title: 'Ошибка', desc: 'Не удалось изменить статус' })
    }
  }

  async function confirmDelete() {
    if (!modal || modal.type !== 'delete') return
    const acc = modal.acc
    try {
      await deleteAccount(acc.id)
      setAccounts(prev => prev.filter(a => a.id !== acc.id))
      if (expandedId === acc.id) setExpandedId(null)
      window.dispatchEvent(new Event('accounts-changed'))
      showToast({ kind: 'info', title: 'Ключ удалён', desc: `«${acc.label}» · ${EXCHANGES[acc.exchange.toLowerCase()]?.name ?? acc.exchange}` })
    } catch {
      showToast({ kind: 'error', title: 'Ошибка удаления' })
    }
    setModal(null)
  }

  async function confirmRotate(newKey: string, newSecret: string) {
    if (!modal || modal.type !== 'rotate') return
    const acc = modal.acc
    try {
      // create new account with same label, delete old
      const newAcc = await createAccount({ exchange: acc.exchange, label: acc.label, api_key: newKey, secret: newSecret })
      await deleteAccount(acc.id)
      setAccounts(prev => [newAcc, ...prev.filter(a => a.id !== acc.id)])
      setExpandedId(newAcc.id)
      window.dispatchEvent(new Event('accounts-changed'))
      showToast({ kind: 'success', title: 'Ключ обновлён', desc: 'Старая пара отозвана, стратегии переключились' })
    } catch {
      showToast({ kind: 'error', title: 'Ошибка ротейта' })
    }
    setModal(null)
  }

  const counts = useMemo(() => ({
    all:    accounts.length,
    active: accounts.filter(a => a.is_active).length,
    issues: accounts.filter(a => !a.is_active || (verifyData[a.id] && !verifyData[a.id].ok)).length,
    paused: accounts.filter(a => !a.is_active).length,
  }), [accounts, verifyData])

  const visible = useMemo(() => {
    const q = search.trim().toLowerCase()
    return accounts.filter(a => {
      if (filter === 'active' && !a.is_active) return false
      if (filter === 'issues' && a.is_active && !(verifyData[a.id] && !verifyData[a.id].ok)) return false
      if (filter === 'paused' && a.is_active) return false
      if (q && !`${a.label} ${a.exchange}`.toLowerCase().includes(q)) return false
      return true
    })
  }, [accounts, verifyData, search, filter])

  return (
    <div style={{ color: T.body, fontFamily: "'Inter', system-ui, sans-serif" }}>
      <PageHead count={accounts.length} />

      {loading ? (
        <div style={{ padding: '60px 0', textAlign: 'center', color: T.dim, fontSize: 13 }}>
          <span style={{ display: 'inline-flex', animation: 'spin 1s linear infinite', marginRight: 8 }}>
            <IcRefresh s={16} c={T.dim} />
          </span>
          Загрузка…
        </div>
      ) : (
        <>
          {accounts.length > 0 && (
            <StatsStrip accounts={accounts} balances={balances} verifyData={verifyData} />
          )}

          <div style={{
            display: 'grid', gridTemplateColumns: '420px minmax(0, 1fr)', gap: 24, alignItems: 'flex-start',
          }}>
            {/* left — add + guide */}
            <div style={{ display: 'flex', flexDirection: 'column', gap: 14, position: 'sticky', top: 24 }}>
              <AddKeyPanel onSubmit={handleAddKey} onExchangeChange={setHowToEx} />
              <HowTo exchange={howToEx} />
            </div>

            {/* right — list */}
            <div>
              {accounts.length === 0 ? (
                <EmptyState />
              ) : (
                <>
                  <Toolbar search={search} setSearch={setSearch} filter={filter} setFilter={setFilter} counts={counts} />
                  <div style={{ display: 'flex', flexDirection: 'column', gap: 10 }}>
                    {visible.map(acc => (
                      <KeyCard
                        key={acc.id}
                        acc={acc}
                        balance={balances[acc.id] ?? null}
                        verify={verifyData[acc.id] ?? null}
                        latency={latencies[acc.id] ?? null}
                        expanded={expandedId === acc.id}
                        testing={testing === acc.id}
                        onToggle={() => setExpandedId(expandedId === acc.id ? null : acc.id)}
                        onTest={() => handleTest(acc)}
                        onDelete={() => setModal({ type: 'delete', acc })}
                        onRotate={() => setModal({ type: 'rotate', acc })}
                        onPause={() => handleToggle(acc)}
                      />
                    ))}
                    {visible.length === 0 && (
                      <div style={{
                        padding: '40px 20px', textAlign: 'center', color: T.dim, fontSize: 13,
                        background: T.card, border: `1px dashed ${T.border}`, borderRadius: 12,
                      }}>
                        По фильтру ничего не найдено.
                      </div>
                    )}
                  </div>
                </>
              )}
            </div>
          </div>
        </>
      )}

      <Modal open={!!modal} onClose={() => setModal(null)}>
        {modal?.type === 'delete' && (
          <ConfirmDelete acc={modal.acc} onCancel={() => setModal(null)} onConfirm={confirmDelete} />
        )}
        {modal?.type === 'rotate' && (
          <ConfirmRotate acc={modal.acc} onCancel={() => setModal(null)} onConfirm={confirmRotate} />
        )}
      </Modal>

      <Toast toast={toast} onClose={() => setToast(null)} />

      <style>{`
        @keyframes pulse   { 0%,100%{opacity:1} 50%{opacity:.35} }
        @keyframes spin    { to{transform:rotate(360deg)} }
        @keyframes fadeIn  { from{opacity:0;transform:translateY(4px)} to{opacity:1;transform:none} }
        @keyframes slideUp { from{opacity:0;transform:translate(-50%,12px)} to{opacity:1;transform:translate(-50%,0)} }
        @keyframes modalIn { from{opacity:0;transform:scale(.96) translateY(8px)} to{opacity:1;transform:none} }
        .fadein { animation: fadeIn .18s ease-out; }
        input::placeholder { color: #5b6479; }
        @media (max-width: 1100px) {
          .ak-layout { grid-template-columns: 1fr !important; }
        }
      `}</style>
    </div>
  )
}
