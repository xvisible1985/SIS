// frontend/src/pages/PaymentsPage.tsx
import { useState, useEffect, useRef } from 'react'
import { QRCodeSVG } from 'qrcode.react'
import {
  createTronDeposit, getTronDeposit, listTronDeposits,
  type TronDeposit,
} from '../api/payments'

/* ── дизайн-токены ────────────────────────────────────────────── */
const T = {
  bg: '#080b12', panel: '#0c1018', border: 'rgba(255,255,255,.07)',
  text: '#f2f5fb', dim: '#7b8aa6', faint: '#5b6479',
  green: '#5be0a0', greenSoft: 'rgba(65,210,139,.12)', greenBd: 'rgba(65,210,139,.25)',
  blue: '#5b8cff', blueSoft: 'rgba(91,140,255,.12)', blueBd: 'rgba(91,140,255,.22)',
  orange: '#f7a600', orangeSoft: 'rgba(247,166,0,.12)', orangeBd: 'rgba(247,166,0,.28)',
  red: '#fca5a5', redSoft: 'rgba(248,113,113,.12)', redBd: 'rgba(248,113,113,.25)',
}
const mono = { fontFamily: "'JetBrains Mono', monospace" }
const grotesk = { fontFamily: "'Space Grotesk', sans-serif" }

/* ── таймер ───────────────────────────────────────────────────── */
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

/* ── копирование ─────────────────────────────────────────────── */
function useCopy() {
  const [copied, setCopied] = useState<string | null>(null)
  const copy = (text: string, key: string) => {
    navigator.clipboard.writeText(text)
    setCopied(key)
    setTimeout(() => setCopied(null), 1500)
  }
  return { copied, copy }
}

/* ── иконки ──────────────────────────────────────────────────── */
const IcCopy = () => <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round"><rect x="9" y="9" width="11" height="11" rx="2"/><path d="M5 15V6a2 2 0 0 1 2-2h9"/></svg>
const IcCheck = () => <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.2" strokeLinecap="round" strokeLinejoin="round"><path d="M5 13l4 4 10-10"/></svg>
const IcChevDown = () => <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round"><path d="M6 9l6 6 6-6"/></svg>

/* ── статус-пилюля ────────────────────────────────────────────── */
function StatusPill({ status }: { status: TronDeposit['status'] }) {
  const map = {
    pending:   { label: 'Ожидание',   c: T.orange, bg: T.orangeSoft, bd: T.orangeBd },
    confirmed: { label: 'Зачислено',  c: T.green,  bg: T.greenSoft,  bd: T.greenBd },
    expired:   { label: 'Истёк',      c: T.dim,    bg: 'rgba(255,255,255,.05)', bd: T.border },
  }
  const s = map[status]
  return (
    <span style={{
      display: 'inline-flex', alignItems: 'center', gap: 5,
      padding: '3px 9px', borderRadius: 999, fontSize: 11, fontWeight: 600,
      color: s.c, background: s.bg, border: `1px solid ${s.bd}`,
    }}>{s.label}</span>
  )
}

/* ── активный инвойс ──────────────────────────────────────────── */
function ActiveInvoice({
  deposit, onConfirmed,
}: {
  deposit: TronDeposit
  onConfirmed: (dep: TronDeposit) => void
}) {
  const { display, expired } = useCountdown(deposit.expires_at)
  const { copied, copy } = useCopy()
  const pollRef = useRef<ReturnType<typeof setInterval> | null>(null)
  const [status, setStatus] = useState(deposit.status)

  useEffect(() => {
    pollRef.current = setInterval(async () => {
      try {
        const updated = await getTronDeposit(deposit.id)
        setStatus(updated.status)
        if (updated.status === 'confirmed') {
          clearInterval(pollRef.current!)
          onConfirmed(updated)
        }
        if (updated.status === 'expired') {
          clearInterval(pollRef.current!)
        }
      } catch {}
    }, 10_000)
    return () => clearInterval(pollRef.current!)
  }, [deposit.id])

  const addrShort = deposit.address.slice(0, 8) + '...' + deposit.address.slice(-6)
  const timerColor = expired ? T.red : display < '05:00' ? T.orange : T.green

  return (
    <div style={{
      background: T.panel, border: `1px solid ${T.blueBd}`,
      borderRadius: 16, overflow: 'hidden',
      boxShadow: '0 12px 40px -20px rgba(91,140,255,.3)',
    }}>
      {/* header */}
      <div style={{
        padding: '16px 20px', borderBottom: `1px solid ${T.border}`,
        background: 'linear-gradient(180deg, rgba(91,140,255,.06) 0%, transparent 100%)',
        display: 'flex', alignItems: 'center', justifyContent: 'space-between',
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

      {/* body */}
      <div style={{ padding: '20px', display: 'flex', flexDirection: 'column', gap: 18 }}>
        {/* сумма */}
        <div style={{
          padding: '16px 20px', borderRadius: 12,
          background: 'rgba(91,140,255,.08)', border: `1px solid ${T.blueBd}`,
          display: 'flex', alignItems: 'center', justifyContent: 'space-between',
        }}>
          <div>
            <div style={{ fontSize: 11, color: T.dim, marginBottom: 4 }}>ОТПРАВЬТЕ РОВНО</div>
            <div style={{ ...mono, fontSize: 26, fontWeight: 700, color: T.text, letterSpacing: -0.5 }}>
              {deposit.amount_exact.toFixed(2)} <span style={{ fontSize: 14, color: T.dim }}>USDT</span>
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
        <div style={{ display: 'flex', gap: 16, alignItems: 'flex-start', flexWrap: 'wrap' }}>
          <div style={{
            padding: 12, borderRadius: 12,
            background: '#fff', flexShrink: 0,
          }}>
            <QRCodeSVG value={deposit.address} size={120} />
          </div>
          <div style={{ flex: 1, minWidth: 200 }}>
            <div style={{ fontSize: 11, color: T.dim, marginBottom: 6, fontWeight: 600 }}>АДРЕС КОШЕЛЬКА</div>
            <div style={{
              padding: '10px 12px', borderRadius: 10,
              background: 'rgba(0,0,0,.25)', border: `1px solid ${T.border}`,
              display: 'flex', alignItems: 'center', justifyContent: 'space-between', gap: 8,
            }}>
              <span style={{ ...mono, fontSize: 12, color: T.text, wordBreak: 'break-all' }}>
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

            {/* предупреждение */}
            <div style={{
              marginTop: 10, padding: '10px 12px', borderRadius: 10,
              background: T.orangeSoft, border: `1px solid ${T.orangeBd}`,
              fontSize: 12, color: T.orange, lineHeight: 1.5,
            }}>
              ⚠️ Отправляйте <strong>только USDT через сеть TRC20</strong>.
              Отправка через другую сеть приведёт к потере средств.
            </div>
          </div>
        </div>

        {/* статус */}
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
          <div style={{
            padding: '14px 16px', borderRadius: 12,
            background: T.redSoft, border: `1px solid ${T.redBd}`,
            fontSize: 13, color: T.red, textAlign: 'center',
          }}>
            Время истекло. Создайте новый запрос на пополнение.
          </div>
        )}
      </div>
    </div>
  )
}

/* ── форма создания депозита ──────────────────────────────────── */
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
    <div style={{
      background: T.panel, border: `1px solid ${T.border}`,
      borderRadius: 16, padding: '20px',
    }}>
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

      {/* поле ввода */}
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
              padding: '12px 48px 12px 14px', outline: 'none',
              boxSizing: 'border-box',
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
            padding: '12px 24px', borderRadius: 10, border: 0,
            background: loading || !amount
              ? 'rgba(91,140,255,.3)'
              : 'linear-gradient(135deg, #5b8cff, #7b5bff)',
            color: '#fff', fontSize: 14, fontWeight: 700,
            cursor: loading || !amount ? 'not-allowed' : 'pointer',
            whiteSpace: 'nowrap',
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
        Зачисление происходит автоматически в течение 1–3 минут после подтверждения в сети Tron.
      </div>
    </div>
  )
}

/* ── история ─────────────────────────────────────────────────── */
function DepositHistory({ deposits }: { deposits: TronDeposit[] }) {
  const [open, setOpen] = useState(false)
  if (deposits.length === 0) return null
  return (
    <div style={{ background: T.panel, border: `1px solid ${T.border}`, borderRadius: 14 }}>
      <button
        onClick={() => setOpen(v => !v)}
        style={{
          width: '100%', padding: '14px 18px',
          display: 'flex', alignItems: 'center', justifyContent: 'space-between',
          background: 'transparent', border: 0, cursor: 'pointer', color: T.text,
        }}
      >
        <span style={{ fontSize: 13, fontWeight: 600 }}>История пополнений ({deposits.length})</span>
        <span style={{ transform: open ? 'rotate(180deg)' : 'none', transition: 'transform .15s', color: T.dim }}>
          <IcChevDown />
        </span>
      </button>
      {open && (
        <div style={{ borderTop: `1px solid ${T.border}` }}>
          {deposits.map(dep => (
            <div key={dep.id} style={{
              padding: '12px 18px', borderBottom: `1px solid ${T.border}`,
              display: 'flex', alignItems: 'center', gap: 12,
            }}>
              <div style={{ flex: 1 }}>
                <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
                  <span style={{ ...mono, fontSize: 14, fontWeight: 700, color: T.text }}>
                    +{dep.amount_exact.toFixed(2)} USDT
                  </span>
                  <StatusPill status={dep.status} />
                </div>
                {dep.tx_hash && (
                  <div style={{ fontSize: 11, color: T.dim, marginTop: 3, ...mono }}>
                    {dep.tx_hash.slice(0, 20)}…
                  </div>
                )}
              </div>
              <div style={{ fontSize: 11, color: T.faint }}>
                {new Date(dep.confirmed_at ?? dep.expires_at).toLocaleDateString('ru-RU')}
              </div>
            </div>
          ))}
        </div>
      )}
    </div>
  )
}

/* ── главная страница ────────────────────────────────────────── */
export function PaymentsPage() {
  const [activeDeposit, setActiveDeposit] = useState<TronDeposit | null>(null)
  const [history, setHistory] = useState<TronDeposit[]>([])

  useEffect(() => {
    listTronDeposits().then(list => {
      const pending = list.find(d => d.status === 'pending')
      if (pending) setActiveDeposit(pending)
      setHistory(list)
    }).catch(() => {})
  }, [])

  function handleCreated(dep: TronDeposit) {
    setActiveDeposit(dep)
    setHistory(prev => [dep, ...prev])
  }

  function handleConfirmed(dep: TronDeposit) {
    setActiveDeposit(dep)
    setHistory(prev => prev.map(d => d.id === dep.id ? dep : d))
  }

  return (
    <div style={{ background: T.bg, minHeight: '100vh', padding: '28px 24px', maxWidth: 680, margin: '0 auto' }}>
      {/* заголовок */}
      <div style={{ marginBottom: 24 }}>
        <h1 style={{ ...grotesk, fontSize: 24, fontWeight: 700, color: T.text, margin: 0, letterSpacing: -0.5 }}>
          💰 Баланс
        </h1>
        <p style={{ margin: '6px 0 0', fontSize: 13, color: T.dim }}>
          Пополнение через USDT TRC20 — без комиссии платформы
        </p>
      </div>

      <div style={{ display: 'flex', flexDirection: 'column', gap: 16 }}>
        {/* активный инвойс или форма создания */}
        {activeDeposit && activeDeposit.status === 'pending'
          ? <ActiveInvoice deposit={activeDeposit} onConfirmed={handleConfirmed} />
          : <CreateDepositForm onCreated={handleCreated} />
        }

        {/* история */}
        <DepositHistory deposits={history.filter(d => d.status !== 'pending')} />
      </div>
    </div>
  )
}
