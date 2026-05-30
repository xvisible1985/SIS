// frontend/src/components/DepositModal.tsx
import { useState, useEffect, useRef } from 'react'
import { QRCodeSVG } from 'qrcode.react'
import {
  createTronDeposit, getTronDeposit, listTronDeposits,
  type TronDeposit,
} from '../api/payments'

/* ── токены ──────────────────────────────────────────────────── */
const T = {
  bg: '#080b12', panel: '#0c1018', border: 'rgba(255,255,255,.07)',
  text: '#f2f5fb', dim: '#7b8aa6', faint: '#5b6479',
  green: '#5be0a0', greenSoft: 'rgba(65,210,139,.12)', greenBd: 'rgba(65,210,139,.25)',
  blue: '#5b8cff', blueSoft: 'rgba(91,140,255,.12)', blueBd: 'rgba(91,140,255,.22)',
  orange: '#f7a600', orangeSoft: 'rgba(247,166,0,.12)', orangeBd: 'rgba(247,166,0,.28)',
  red: '#fca5a5', redSoft: 'rgba(248,113,113,.12)', redBd: 'rgba(248,113,113,.25)',
}
const mono = { fontFamily: "'JetBrains Mono', monospace" } as const
const grotesk = { fontFamily: "'Space Grotesk', sans-serif" } as const

/* ── иконки ──────────────────────────────────────────────────── */
const IcCopy = () => <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round"><rect x="9" y="9" width="11" height="11" rx="2"/><path d="M5 15V6a2 2 0 0 1 2-2h9"/></svg>
const IcCheck = () => <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.2" strokeLinecap="round" strokeLinejoin="round"><path d="M5 13l4 4 10-10"/></svg>
const IcX = () => <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round"><path d="M6 6l12 12M18 6L6 18"/></svg>

/* ── хуки ────────────────────────────────────────────────────── */
function useCountdown(expiresAt: string | null) {
  const [secs, setSecs] = useState(0)
  useEffect(() => {
    if (!expiresAt) return
    const tick = () => setSecs(Math.max(0, Math.floor((new Date(expiresAt).getTime() - Date.now()) / 1000)))
    tick()
    const id = setInterval(tick, 1000)
    return () => clearInterval(id)
  }, [expiresAt])
  const m = String(Math.floor(secs / 60)).padStart(2, '0')
  const s = String(secs % 60).padStart(2, '0')
  return { display: `${m}:${s}`, expired: secs === 0 }
}

function useCopy() {
  const [copied, setCopied] = useState<string | null>(null)
  const copy = (text: string, key: string) => {
    navigator.clipboard.writeText(text)
    setCopied(key)
    setTimeout(() => setCopied(null), 1500)
  }
  return { copied, copy }
}

/* ── активный инвойс ──────────────────────────────────────────── */
function ActiveInvoice({
  deposit, onConfirmed, onBack,
}: {
  deposit: TronDeposit
  onConfirmed: (dep: TronDeposit) => void
  onBack: () => void
}) {
  const { display, expired } = useCountdown(deposit.expires_at)
  const { copied, copy } = useCopy()
  const pollRef = useRef<ReturnType<typeof setInterval> | null>(null)
  const [status, setStatus] = useState(deposit.status)
  const [pollError, setPollError] = useState<string | null>(null)

  useEffect(() => {
    pollRef.current = setInterval(async () => {
      try {
        const updated = await getTronDeposit(deposit.id)
        setPollError(null)
        setStatus(updated.status)
        if (updated.status === 'confirmed') {
          clearInterval(pollRef.current!)
          onConfirmed(updated)
        }
        if (updated.status === 'expired') {
          clearInterval(pollRef.current!)
        }
      } catch (e: unknown) {
        setPollError(e instanceof Error ? e.message : 'Ошибка соединения с сервером')
      }
    }, 10_000)
    return () => clearInterval(pollRef.current!)
  }, [deposit.id])

  const timerColor = expired ? T.red : display < '05:00' ? T.orange : T.green

  return (
    <div>
      {/* header row */}
      <div style={{
        padding: '4px 0 16px', borderBottom: `1px solid ${T.border}`,
        display: 'flex', alignItems: 'center', justifyContent: 'space-between', marginBottom: 18,
      }}>
        <div>
          <div style={{ ...grotesk, fontSize: 15, fontWeight: 700, color: T.text }}>
            Пополнение баланса
          </div>
          <div style={{ fontSize: 12, color: T.dim, marginTop: 2 }}>
            Сеть: <span style={{ color: T.text, fontWeight: 600 }}>USDT TRC20 (Tron)</span>
          </div>
        </div>
        <div style={{ textAlign: 'right' }}>
          <div style={{ fontSize: 11, color: T.dim }}>Осталось</div>
          <div style={{ ...mono, fontSize: 20, fontWeight: 700, color: timerColor }}>{display}</div>
        </div>
      </div>

      {/* сумма */}
      <div style={{
        padding: '14px 18px', borderRadius: 12, marginBottom: 16,
        background: 'rgba(91,140,255,.08)', border: `1px solid ${T.blueBd}`,
        display: 'flex', alignItems: 'center', justifyContent: 'space-between',
      }}>
        <div>
          <div style={{ fontSize: 11, color: T.dim, marginBottom: 4 }}>ОТПРАВЬТЕ РОВНО</div>
          <div style={{ ...mono, fontSize: 24, fontWeight: 700, color: T.text, letterSpacing: -0.5 }}>
            {deposit.amount_exact.toFixed(2)} <span style={{ fontSize: 13, color: T.dim }}>USDT</span>
          </div>
        </div>
        <button
          onClick={() => copy(String(deposit.amount_exact.toFixed(2)), 'amount')}
          style={{
            display: 'flex', alignItems: 'center', gap: 6,
            padding: '8px 14px', borderRadius: 8,
            background: copied === 'amount' ? T.greenSoft : 'rgba(255,255,255,.06)',
            border: `1px solid ${copied === 'amount' ? T.greenBd : T.border}`,
            color: copied === 'amount' ? T.green : T.dim,
            fontSize: 12, fontWeight: 600, cursor: 'pointer',
          }}
        >
          {copied === 'amount' ? <IcCheck /> : <IcCopy />}
          {copied === 'amount' ? 'Скопировано' : 'Копировать'}
        </button>
      </div>

      {/* адрес + QR */}
      <div style={{ display: 'flex', gap: 14, alignItems: 'flex-start', flexWrap: 'wrap', marginBottom: 14 }}>
        <div style={{ padding: 10, borderRadius: 10, background: '#fff', flexShrink: 0 }}>
          <QRCodeSVG value={deposit.address} size={108} />
        </div>
        <div style={{ flex: 1, minWidth: 180 }}>
          <div style={{ fontSize: 11, color: T.dim, marginBottom: 6, fontWeight: 600 }}>АДРЕС КОШЕЛЬКА</div>
          <div style={{
            padding: '10px 12px', borderRadius: 10,
            background: 'rgba(0,0,0,.25)', border: `1px solid ${T.border}`,
            display: 'flex', alignItems: 'center', justifyContent: 'space-between', gap: 8,
          }}>
            <span style={{ ...mono, fontSize: 11, color: T.text, wordBreak: 'break-all' }}>
              {deposit.address}
            </span>
            <button
              onClick={() => copy(deposit.address, 'addr')}
              style={{
                flexShrink: 0, display: 'flex', alignItems: 'center', gap: 5,
                padding: '6px 10px', borderRadius: 7,
                background: copied === 'addr' ? T.greenSoft : 'rgba(255,255,255,.05)',
                border: `1px solid ${copied === 'addr' ? T.greenBd : T.border}`,
                color: copied === 'addr' ? T.green : T.dim,
                fontSize: 11, fontWeight: 600, cursor: 'pointer',
              }}
            >
              {copied === 'addr' ? <IcCheck /> : <IcCopy />}
            </button>
          </div>

          <div style={{
            marginTop: 10, padding: '10px 12px', borderRadius: 10,
            background: T.orangeSoft, border: `1px solid ${T.orangeBd}`,
            fontSize: 12, color: T.orange, lineHeight: 1.5,
          }}>
            ⚠️ Только <strong>USDT TRC20</strong>. Другая сеть = потеря средств.
          </div>
        </div>
      </div>

      {pollError && (
        <div style={{
          padding: '10px 14px', borderRadius: 10, marginBottom: 12,
          background: T.redSoft, border: `1px solid ${T.redBd}`,
          fontSize: 12, color: T.red, display: 'flex', alignItems: 'center', gap: 8,
        }}>
          <span>⚠️</span><span>{pollError}</span>
        </div>
      )}

      {status === 'confirmed' && (
        <div style={{
          padding: '14px 16px', borderRadius: 12,
          background: T.greenSoft, border: `1px solid ${T.greenBd}`,
          fontSize: 14, fontWeight: 600, color: T.green, textAlign: 'center',
        }}>
          ✅ Баланс пополнен на {deposit.amount_exact.toFixed(2)} USDT
        </div>
      )}
      {(expired || status === 'expired') && (
        <div style={{ display: 'flex', flexDirection: 'column', gap: 10 }}>
          <div style={{
            padding: '14px 16px', borderRadius: 12,
            background: T.redSoft, border: `1px solid ${T.redBd}`,
            fontSize: 13, color: T.red, textAlign: 'center',
          }}>
            Время истекло. Создайте новый запрос.
          </div>
          <button onClick={onBack} style={{
            padding: '10px', borderRadius: 10, border: `1px solid ${T.border}`,
            background: 'rgba(255,255,255,.04)', color: T.dim,
            fontSize: 13, fontWeight: 600, cursor: 'pointer',
          }}>
            ← Создать новый
          </button>
        </div>
      )}
    </div>
  )
}

/* ── форма создания ───────────────────────────────────────────── */
const PRESETS = [10, 20, 50, 100, 200, 500]

function CreateDepositForm({ onCreated }: { onCreated: (dep: TronDeposit) => void }) {
  const [amount, setAmount] = useState('')
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState('')

  async function handleSubmit() {
    const num = parseFloat(amount)
    if (!num || num < 1) { setError('Минимум 1 USDT'); return }
    setError('')
    setLoading(true)
    try {
      const dep = await createTronDeposit(num)
      onCreated(dep)
    } catch (e: unknown) {
      setError(e instanceof Error ? e.message : 'Ошибка создания депозита')
    } finally {
      setLoading(false)
    }
  }

  return (
    <div>
      <div style={{ ...grotesk, fontSize: 15, fontWeight: 700, color: T.text, marginBottom: 16 }}>
        Пополнить баланс
      </div>

      {/* пресеты */}
      <div style={{ display: 'flex', gap: 8, flexWrap: 'wrap', marginBottom: 12 }}>
        {PRESETS.map(p => (
          <button key={p} onClick={() => setAmount(String(p))} style={{
            padding: '7px 14px', borderRadius: 8,
            background: amount === String(p) ? T.blueSoft : 'rgba(255,255,255,.04)',
            border: `1px solid ${amount === String(p) ? T.blueBd : T.border}`,
            color: amount === String(p) ? T.blue : T.dim,
            fontSize: 13, fontWeight: 600, cursor: 'pointer',
          }}>
            ${p}
          </button>
        ))}
      </div>

      {/* ввод */}
      <div style={{ display: 'flex', gap: 8 }}>
        <div style={{ flex: 1, position: 'relative' }}>
          <input
            type="number" min="1" step="1"
            value={amount}
            onChange={e => setAmount(e.target.value)}
            placeholder="Введите сумму"
            style={{
              width: '100%', ...mono, fontSize: 16, fontWeight: 600,
              background: 'rgba(0,0,0,.3)', color: T.text,
              border: `1px solid ${T.border}`, borderRadius: 10,
              padding: '12px 48px 12px 14px', outline: 'none', boxSizing: 'border-box',
            }}
          />
          <span style={{
            position: 'absolute', right: 14, top: '50%', transform: 'translateY(-50%)',
            fontSize: 13, fontWeight: 600, color: T.dim,
          }}>USDT</span>
        </div>
        <button
          onClick={handleSubmit}
          disabled={loading || !amount}
          style={{
            padding: '12px 22px', borderRadius: 10, border: 0,
            background: loading || !amount
              ? 'rgba(91,140,255,.3)'
              : 'linear-gradient(135deg, #5b8cff, #7b5bff)',
            color: '#fff', fontSize: 14, fontWeight: 700,
            cursor: loading || !amount ? 'not-allowed' : 'pointer', whiteSpace: 'nowrap',
          }}
        >
          {loading ? 'Создаём…' : 'Пополнить →'}
        </button>
      </div>

      {error && (
        <div style={{
          marginTop: 10, padding: '10px 12px', borderRadius: 8,
          background: T.redSoft, border: `1px solid ${T.redBd}`,
          fontSize: 12, color: T.red,
        }}>{error}</div>
      )}

      <div style={{ marginTop: 12, fontSize: 11, color: T.faint, lineHeight: 1.6 }}>
        💡 После создания запроса отправьте <strong style={{ color: T.dim }}>точную сумму</strong> на указанный адрес.
        Зачисление — автоматически в течение 1–3 минут после подтверждения в сети Tron.
      </div>
    </div>
  )
}

/* ── модалка ─────────────────────────────────────────────────── */
export function DepositModal({ open, onClose }: { open: boolean; onClose: () => void }) {
  const [activeDeposit, setActiveDeposit] = useState<TronDeposit | null>(null)
  const [loading, setLoading] = useState(false)

  // при открытии — проверяем, есть ли уже pending инвойс
  useEffect(() => {
    if (!open) return
    setLoading(true)
    listTronDeposits()
      .then(list => {
        if (Array.isArray(list)) {
          const pending = list.find(d => d.status === 'pending')
          setActiveDeposit(pending ?? null)
        }
      })
      .catch(() => {})
      .finally(() => setLoading(false))
  }, [open])

  // закрытие по Escape
  useEffect(() => {
    if (!open) return
    const handle = (e: KeyboardEvent) => { if (e.key === 'Escape') onClose() }
    window.addEventListener('keydown', handle)
    return () => window.removeEventListener('keydown', handle)
  }, [open, onClose])

  if (!open) return null

  return (
    <div
      onClick={onClose}
      style={{
        position: 'fixed', inset: 0, zIndex: 200,
        display: 'flex', alignItems: 'center', justifyContent: 'center',
        background: 'rgba(8,10,16,.72)', backdropFilter: 'blur(6px)', padding: 20,
      }}
    >
      <div
        onClick={e => e.stopPropagation()}
        style={{
          width: '100%', maxWidth: 520,
          background: T.panel, border: `1px solid rgba(255,255,255,.10)`,
          borderRadius: 18, overflow: 'hidden',
          boxShadow: '0 40px 80px -20px rgba(0,0,0,.8)',
          animation: 'modalIn .18s ease-out',
        }}
      >
        {/* шапка модалки */}
        <div style={{
          padding: '16px 20px', borderBottom: `1px solid ${T.border}`,
          background: 'linear-gradient(180deg, rgba(91,140,255,.07) 0%, transparent 100%)',
          display: 'flex', alignItems: 'center', justifyContent: 'space-between',
        }}>
          <div>
            <div style={{ ...grotesk, fontSize: 14, fontWeight: 700, color: T.text }}>
              💰 Пополнение NovaBot
            </div>
            <div style={{ fontSize: 11, color: T.dim, marginTop: 2 }}>
              USDT TRC20 · сеть Tron
            </div>
          </div>
          <button
            onClick={onClose}
            style={{
              display: 'flex', alignItems: 'center', justifyContent: 'center',
              width: 30, height: 30, borderRadius: 8,
              background: 'rgba(255,255,255,.05)', border: `1px solid ${T.border}`,
              color: T.dim, cursor: 'pointer',
            }}
          >
            <IcX />
          </button>
        </div>

        {/* тело */}
        <div style={{ padding: '20px' }}>
          {loading ? (
            <div style={{ padding: '32px 0', textAlign: 'center', fontSize: 13, color: T.dim }}>
              Загрузка…
            </div>
          ) : activeDeposit && activeDeposit.status === 'pending' ? (
            <ActiveInvoice
              deposit={activeDeposit}
              onConfirmed={dep => setActiveDeposit(dep)}
              onBack={() => setActiveDeposit(null)}
            />
          ) : (
            <CreateDepositForm
              onCreated={dep => setActiveDeposit(dep)}
            />
          )}
        </div>
      </div>
    </div>
  )
}
