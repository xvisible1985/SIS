import { useState, useEffect } from 'react'
import { Plus, X } from 'lucide-react'
import {
  listAccounts, createAccount, deleteAccount,
  verifyAccount, getAccountBalance, toggleAccountActive,
  type VerifyResult, type BalanceResult,
} from '../api/accounts'
import { listStrategies } from '../api/strategies'
import type { ExchangeAccount, Strategy } from '../types'

// ── Time formatting ───────────────────────────────────────────────────────────
function formatAge(iso: string): string {
  const diff = Date.now() - new Date(iso).getTime()
  const sec = Math.floor(diff / 1000)
  const min = Math.floor(sec / 60)
  const hr = Math.floor(min / 60)
  const day = Math.floor(hr / 24)
  const month = Math.floor(day / 30)
  const year = Math.floor(day / 365)
  if (year > 0) return `${year} ${plural(year, 'год', 'года', 'лет')}`
  if (month > 0) return `${month} ${plural(month, 'месяц', 'месяца', 'месяцев')}`
  if (day > 0) return `${day} ${plural(day, 'день', 'дня', 'дней')}`
  if (hr > 0) return `${hr} ${plural(hr, 'час', 'часа', 'часов')}`
  if (min > 0) return `${min} ${plural(min, 'минута', 'минуты', 'минут')}`
  return `${sec} ${plural(sec, 'секунда', 'секунды', 'секунд')}`
}
function plural(n: number, one: string, few: string, many: string): string {
  const mod10 = n % 10
  const mod100 = n % 100
  if (mod10 === 1 && mod100 !== 11) return one
  if (mod10 >= 2 && mod10 <= 4 && (mod100 < 10 || mod100 >= 20)) return few
  return many
}

function formatRemaining(iso?: string): string {
  if (!iso) return 'Постоянный'
  const diff = new Date(iso).getTime() - Date.now()
  if (diff <= 0) return 'Истёк'
  const sec = Math.floor(diff / 1000)
  const min = Math.floor(sec / 60)
  const hr = Math.floor(min / 60)
  const day = Math.floor(hr / 24)
  const month = Math.floor(day / 30)
  const year = Math.floor(day / 365)
  if (year > 0) return `~${year} ${plural(year, 'год', 'года', 'лет')}`
  if (month > 0) return `~${month} ${plural(month, 'месяц', 'месяца', 'месяцев')}`
  if (day > 0) return `~${day} ${plural(day, 'день', 'дня', 'дней')}`
  if (hr > 0) return `~${hr} ${plural(hr, 'час', 'часа', 'часов')}`
  if (min > 0) return `~${min} ${plural(min, 'минута', 'минуты', 'минут')}`
  return `< 1 мин`
}

// ── Permission label map ──────────────────────────────────────────────────────
const PERM_LABELS: Record<string, string> = {
  ContractTrade: 'Фьючерсы',
  Spot: 'Спот',
  Wallet: 'Кошелёк',
  Options: 'Опционы',
  Derivatives: 'Деривативы',
  CopyTrading: 'Копитрейдинг',
  BlockTrade: 'Блочные сделки',
  Exchange: 'Обмен',
}

// ── AccountCard ───────────────────────────────────────────────────────────────
interface AccountCardProps {
  acc: ExchangeAccount
  balance: BalanceResult | null
  strategyTotal: number
  strategyActive: number
  botTotal: number
  botActive: number
  onDelete: (id: string) => void
  onToggled: (updated: ExchangeAccount) => void
}

function StatBadge({ label, total, active }: { label: string; total: number; active: number }) {
  return (
    <div className="flex flex-col items-center gap-0.5 px-3 py-2 rounded-lg bg-white/[.07] border border-white/[.10] min-w-[58px]">
      <span className="text-[10px] font-semibold uppercase tracking-[1px] text-slate-400">{label}</span>
      <div className="flex items-baseline gap-0.5">
        <span className="text-[16px] font-bold leading-none text-white">{active}</span>
        <span className="text-[11px] text-slate-400">/{total}</span>
      </div>
    </div>
  )
}

function AccountCard({ acc, balance, strategyTotal, strategyActive, botTotal, botActive, onDelete, onToggled }: AccountCardProps) {
  const [open, setOpen] = useState(false)
  const [verify, setVerify] = useState<VerifyResult | null>(null)
  const [verifying, setVerifying] = useState(false)
  const [toggling, setToggling] = useState(false)

  async function handleVerify() {
    setVerifying(true)
    try {
      setVerify(await verifyAccount(acc.id))
    } finally {
      setVerifying(false)
    }
  }

  async function handleToggle() {
    setToggling(true)
    try {
      const updated = await toggleAccountActive(acc.id)
      onToggled(updated)
      window.dispatchEvent(new Event('accounts-changed'))
    } finally {
      setToggling(false)
    }
  }

  async function handleDelete() {
    if (!window.confirm('Удалить аккаунт?')) return
    await deleteAccount(acc.id)
    onDelete(acc.id)
    window.dispatchEvent(new Event('accounts-changed'))
  }

  const equityStr = balance?.ok && balance.equity != null
    ? balance.equity.toLocaleString('en-US', { minimumFractionDigits: 2, maximumFractionDigits: 2 })
    : null

  return (
    <li className="border border-white/[.10] rounded-xl overflow-hidden bg-white/[.05]">
      {/* Header */}
      <button
        onClick={() => setOpen(v => !v)}
        className="w-full flex flex-col sm:flex-row sm:items-center gap-3 px-4 py-4 hover:bg-white/[.04] transition-colors text-left"
      >
        <div className="flex items-center gap-3 w-full">
          {/* Exchange logo */}
          <ExchangeLogo exchange={acc.exchange} size={40} />

          {/* Name + status */}
          <div className="min-w-0 flex-1">
            <div className="flex items-center gap-2">
              <span className="font-semibold text-[14px] text-white capitalize">{acc.exchange}</span>
              <span className="text-slate-500">·</span>
              <span className="text-[14px] text-slate-200 truncate">{acc.label}</span>
            </div>
            <div className="flex items-center gap-1.5 mt-0.5">
              <span className={`h-1.5 w-1.5 rounded-full ${acc.is_active ? 'bg-emerald-400 shadow-[0_0_5px_#41d28b]' : 'bg-slate-500'}`} />
              <span className="text-[11px] text-slate-400">{acc.is_active ? 'Активен' : 'Остановлен'}</span>
            </div>
          </div>

          {/* Chevron — mobile only */}
          <svg
            className={`w-4 h-4 text-slate-400 transition-transform duration-200 shrink-0 sm:hidden ${open ? 'rotate-180' : ''}`}
            fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2}
          >
            <path strokeLinecap="round" strokeLinejoin="round" d="M19 9l-7 7-7-7" />
          </svg>
        </div>

        {/* Equity + Stats row on mobile, inline on desktop */}
        <div className="flex items-center gap-3 w-full sm:w-auto justify-between sm:justify-end">
          {/* Equity */}
          <div className="shrink-0 text-left sm:text-right sm:mr-2">
            <div className="flex items-baseline gap-1 sm:justify-end">
              {equityStr
                ? <span className="text-[20px] sm:text-[22px] font-bold tracking-[-0.5px] text-white">${equityStr}</span>
                : <span className="text-[16px] font-semibold text-slate-500">—</span>
              }
              {equityStr && <span className="text-[11px] font-medium text-slate-400">USDT</span>}
            </div>
            {balance?.ok && balance.available != null && (
              <div className="text-[11px] text-slate-400 mt-0.5">
                доступно&nbsp;${balance.available.toLocaleString('en-US', { maximumFractionDigits: 0 })}
              </div>
            )}
          </div>

          {/* Stat badges */}
          <div className="flex items-center gap-1.5 shrink-0">
            <StatBadge label="Страт" total={strategyTotal} active={strategyActive} />
            <StatBadge label="Боты" total={botTotal} active={botActive} />
          </div>

          {/* Chevron — desktop only */}
          <svg
            className={`w-4 h-4 text-slate-400 transition-transform duration-200 shrink-0 hidden sm:block ${open ? 'rotate-180' : ''}`}
            fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2}
          >
            <path strokeLinecap="round" strokeLinejoin="round" d="M19 9l-7 7-7-7" />
          </svg>
        </div>
      </button>

      {/* Expanded body */}
      {open && (
        <div className="border-t border-white/[.08] px-4 py-4 space-y-4 bg-white/[.02]">
          {/* Key lifetime */}
          <div className="flex items-center gap-2">
            <span className="text-[11px] font-semibold uppercase tracking-[1.2px] text-slate-400">Время жизни</span>
            <span className="text-sm text-slate-200">{formatRemaining(acc.expires_at)}</span>
          </div>

          {/* Permissions */}
          <div>
            <div className="text-[11px] font-semibold uppercase tracking-[1.2px] text-slate-400 mb-2">Разрешения</div>
            {verify === null ? (
              <div className="text-sm text-slate-400">Нажмите «Проверить» для загрузки</div>
            ) : !verify.ok ? (
              <div className="text-sm text-red-400">{verify.message ?? 'Ошибка'}</div>
            ) : (
              <div className="space-y-2">
                <div className="flex flex-wrap gap-1.5">
                  <span className={`text-xs px-2.5 py-0.5 rounded-full font-medium ${
                    verify.read_only
                      ? 'bg-yellow-400/10 text-yellow-300 border border-yellow-400/20'
                      : 'bg-emerald-400/10 text-emerald-300 border border-emerald-400/20'
                  }`}>
                    {verify.read_only ? 'Только чтение' : 'Чтение и запись'}
                  </span>
                  {verify.ips && verify.ips.length > 0 && (
                    <span className="text-xs px-2.5 py-0.5 rounded-full font-medium bg-white/[.06] text-slate-300 border border-white/[.10] font-mono">
                      IP: {verify.ips.join(', ')}
                    </span>
                  )}
                </div>
                <div className="flex flex-wrap gap-1.5">
                  {Object.entries(verify.permissions ?? {}).map(([key, vals]) => {
                    const active = (vals as string[]).length > 0
                    return (
                      <span key={key} className={`text-xs px-2.5 py-0.5 rounded-full font-medium ${
                        active
                          ? 'bg-[#4a7dff]/10 text-[#7ba4ff] border border-[#4a7dff]/20'
                          : 'bg-white/[.04] text-slate-500 border border-white/[.07] line-through'
                      }`}>
                        {PERM_LABELS[key] ?? key}
                      </span>
                    )
                  })}
                </div>
              </div>
            )}
          </div>

          {/* Actions */}
          <div className="flex flex-wrap items-center gap-2">
            <button
              onClick={handleVerify}
              disabled={verifying}
              className="px-3.5 py-1.5 text-sm font-medium bg-[#4a7dff]/15 text-[#7ba4ff] border border-[#4a7dff]/20 rounded-lg hover:bg-[#4a7dff]/25 disabled:opacity-50 transition-colors"
            >
              {verifying ? 'Проверка…' : 'Проверить ключ'}
            </button>
            <button
              onClick={handleToggle}
              disabled={toggling}
              className={`px-3.5 py-1.5 text-sm font-medium rounded-lg border transition-colors disabled:opacity-50 ${
                acc.is_active
                  ? 'border-white/[.12] text-slate-300 hover:bg-white/[.05]'
                  : 'border-emerald-400/25 text-emerald-400 hover:bg-emerald-400/10'
              }`}
            >
              {toggling ? '…' : acc.is_active ? 'Остановить' : 'Запустить'}
            </button>
            <button
              onClick={handleDelete}
              className="ml-auto px-3.5 py-1.5 text-sm font-medium text-red-400/80 hover:text-red-400 hover:bg-red-400/10 border border-transparent hover:border-red-400/15 rounded-lg transition-colors"
            >
              Удалить
            </button>
          </div>
        </div>
      )}
    </li>
  )
}

// ── Exchange icons ────────────────────────────────────────────────────────────
const EXCHANGE_META: Record<string, { bg: string; iconColor: string; slug: string }> = {
  bybit:   { bg: '#F7A600', iconColor: '1a1100', slug: 'bybit' },
  binance: { bg: '#F3BA2F', iconColor: '1a1100', slug: 'binance' },
}

function ExchangeLogo({ exchange, size = 32 }: { exchange: string; size?: number }) {
  const meta = EXCHANGE_META[exchange.toLowerCase()]
  const letter = exchange[0]?.toUpperCase() ?? '?'
  if (!meta) {
    return (
      <div
        style={{ width: size, height: size, background: '#374151' }}
        className="rounded-xl flex items-center justify-center text-white font-extrabold"
        >
        {letter}
      </div>
    )
  }
  return (
    <div
      style={{ width: size, height: size, background: meta.bg }}
      className="rounded-xl flex items-center justify-center overflow-hidden shrink-0"
    >
      <img
        src={`https://cdn.simpleicons.org/${meta.slug}/${meta.iconColor}`}
        style={{ width: '62%', height: '62%', objectFit: 'contain' }}
        alt={exchange}
        onError={e => {
          const el = e.currentTarget
          el.style.display = 'none'
          if (el.parentElement) el.parentElement.textContent = letter
        }}
      />
    </div>
  )
}

const EXCHANGES: { value: string; label: string; sub: string }[] = [
  { value: 'bybit',   label: 'Bybit',   sub: 'Futures · Spot' },
  { value: 'binance', label: 'Binance', sub: 'Futures · Spot' },
]

// ── AddKeyModal ───────────────────────────────────────────────────────────────
function AddKeyModal({
  onClose,
  onAdded,
}: {
  onClose: () => void
  onAdded: () => void
}) {
  const [exchange, setExchange] = useState('bybit')
  const [label, setLabel] = useState('')
  const [apiKey, setApiKey] = useState('')
  const [secret, setSecret] = useState('')
  const [adding, setAdding] = useState(false)
  const [addMsg, setAddMsg] = useState<{ ok: boolean; text: string } | null>(null)

  async function handleAdd(e: React.FormEvent) {
    e.preventDefault()
    setAdding(true)
    setAddMsg(null)
    try {
      await createAccount({ exchange, label, api_key: apiKey, secret })
      setAddMsg({ ok: true, text: 'Аккаунт добавлен' })
      setTimeout(() => {
        onAdded()
        onClose()
      }, 800)
    } catch (err: any) {
      setAddMsg({ ok: false, text: err?.response?.data?.error ?? 'Ошибка' })
    } finally {
      setAdding(false)
    }
  }

  return (
    <div
      className="fixed inset-0 z-50 flex items-center justify-center bg-black/60 backdrop-blur-sm"
      onClick={e => { if (e.target === e.currentTarget) onClose() }}
    >
      <div className="w-full max-w-md mx-4 bg-[#111827] border border-white/[.08] rounded-2xl shadow-2xl">
        {/* Modal header */}
        <div className="flex items-center justify-between px-6 py-4 border-b border-white/[.06]">
          <h2 className="text-base font-semibold text-white">Добавить API key</h2>
          <button
            type="button"
            onClick={onClose}
            className="text-slate-400 hover:text-slate-200 transition-colors"
          >
            <X size={18} />
          </button>
        </div>

        {/* Modal body */}
        <form onSubmit={handleAdd} className="px-6 py-5 space-y-4">
          {/* Exchange picker */}
          <div>
            <label className="block text-xs font-medium text-slate-400 mb-2">Биржа</label>
            <div className="grid grid-cols-2 gap-2">
              {EXCHANGES.map(ex => {
                const active = exchange === ex.value
                return (
                  <button
                    key={ex.value}
                    type="button"
                    onClick={() => setExchange(ex.value)}
                    className={`flex items-center gap-3 px-3.5 py-3 rounded-xl border transition-all text-left ${
                      active
                        ? 'border-[#4a7dff]/50 bg-[#4a7dff]/[.10] shadow-[0_0_0_1px_rgba(74,125,255,.2)]'
                        : 'border-white/[.07] bg-white/[.03] hover:border-white/[.14] hover:bg-white/[.05]'
                    }`}
                  >
                    <ExchangeLogo exchange={ex.value} size={32} />
                    <div className="min-w-0">
                      <div className={`text-sm font-semibold leading-none ${active ? 'text-white' : 'text-slate-300'}`}>
                        {ex.label}
                      </div>
                      <div className="text-[10px] text-slate-500 mt-0.5">{ex.sub}</div>
                    </div>
                    {active && (
                      <div className="ml-auto shrink-0 w-2 h-2 rounded-full bg-[#4a7dff] shadow-[0_0_6px_rgba(74,125,255,.8)]" />
                    )}
                  </button>
                )
              })}
            </div>
          </div>

          {/* Label */}
          <div>
            <label className="block text-xs font-medium text-slate-400 mb-1.5">Название аккаунта</label>
            <input
              value={label}
              onChange={e => setLabel(e.target.value)}
              required
              placeholder="Например: Основной"
              autoComplete="off"
              className="w-full bg-white/[.04] border border-white/[.08] rounded-lg px-3 py-2.5 text-sm font-sans text-slate-200 placeholder:text-slate-600 focus:outline-none focus:border-[#4a7dff]/50 transition-colors"
            />
          </div>

          {/* API Key */}
          <div>
            <label className="block text-xs font-medium text-slate-400 mb-1.5">API Key</label>
            <input
              value={apiKey}
              onChange={e => setApiKey(e.target.value)}
              required
              type="password"
              placeholder="Вставьте API Key"
              autoComplete="new-password"
              className="w-full bg-white/[.04] border border-white/[.08] rounded-lg px-3 py-2.5 text-sm font-sans text-slate-200 placeholder:text-slate-600 focus:outline-none focus:border-[#4a7dff]/50 transition-colors"
            />
          </div>

          {/* Secret */}
          <div>
            <label className="block text-xs font-medium text-slate-400 mb-1.5">API Secret</label>
            <input
              value={secret}
              onChange={e => setSecret(e.target.value)}
              required
              type="password"
              placeholder="Вставьте Secret"
              autoComplete="new-password"
              className="w-full bg-white/[.04] border border-white/[.08] rounded-lg px-3 py-2.5 text-sm font-sans text-slate-200 placeholder:text-slate-600 focus:outline-none focus:border-[#4a7dff]/50 transition-colors"
            />
          </div>

          {addMsg && (
            <div className={`text-sm px-3 py-2 rounded-lg ${
              addMsg.ok
                ? 'bg-emerald-400/10 text-emerald-300 border border-emerald-400/20'
                : 'bg-red-400/10 text-red-300 border border-red-400/20'
            }`}>
              {addMsg.text}
            </div>
          )}

          <div className="flex items-center gap-3 pt-1">
            <button
              type="submit"
              disabled={adding}
              className="flex-1 py-2.5 bg-[linear-gradient(180deg,#4a7dff_0%,#3a67e6_100%)] text-white text-sm font-semibold rounded-lg hover:brightness-110 active:brightness-95 disabled:opacity-50 transition-all shadow-[0_4px_12px_-4px_rgba(74,125,255,.5)]"
            >
              {adding ? 'Добавление…' : 'Добавить'}
            </button>
            <button
              type="button"
              onClick={onClose}
              className="px-4 py-2.5 text-sm text-slate-400 hover:text-slate-200 border border-white/[.08] rounded-lg hover:bg-white/[.04] transition-colors"
            >
              Отмена
            </button>
          </div>
        </form>
      </div>
    </div>
  )
}

// ── Test data ─────────────────────────────────────────────────────────────────
const MOCK_ACCOUNTS: ExchangeAccount[] = [
  { id: 'mock-1', exchange: 'bybit',   label: 'Основной',    is_active: true,  created_at: '2024-09-01T00:00:00Z' },
  { id: 'mock-2', exchange: 'binance', label: 'Скальпинг',   is_active: true,  created_at: '2024-11-15T00:00:00Z' },
  { id: 'mock-3', exchange: 'bybit',   label: 'DCA портфель', is_active: false, created_at: '2025-01-20T00:00:00Z' },
  { id: 'mock-4', exchange: 'binance', label: 'Тест',        is_active: true,  created_at: '2025-03-10T00:00:00Z' },
]
const MOCK_BALANCES: Record<string, BalanceResult> = {
  'mock-1': { ok: true, equity: 24_831.50, available: 18_200.00 },
  'mock-2': { ok: true, equity:  8_450.25, available:  6_100.75 },
  'mock-3': { ok: true, equity:  3_000.00, available:  3_000.00 },
  'mock-4': { ok: true, equity:    512.10, available:    512.10 },
}
const S = (id: string, accId: string, status: Strategy['status'], type: Strategy['strategy_type'], symbol: string): Strategy => ({
  id, account_id: accId, status, strategy_type: type, symbol,
  category: 'linear', direction: 'both', grid_levels: 8, grid_active: 4, max_stop_active: 0,
  grid_step_pct: 1, grid_size_usdt: 300, tp_mode: 'total', tp_pct: 2,
  sl_type: 'conditional', sl_pct: 5, signal_filter: false, leverage: 5,
  margin_type: 'isolated', hedge_mode: false, entry_order_type: 'limit',
  signal_configs: [], steps: null, trailing_stop_enabled: false,
  trailing_activation_pct: null, trailing_callback_pct: null,
  active_levels: 4, last_pnl: 0, created_at: '', updated_at: '', volume_usdt: 0,
})

const MOCK_STRATEGIES: Strategy[] = [
  S('ms-1', 'mock-1', 'active',  'grid', 'BTCUSDT'),
  S('ms-2', 'mock-1', 'stopped', 'matrix',  'ETHUSDT'),
  S('ms-3', 'mock-1', 'active',  'grid', 'SOLUSDT'),
  S('ms-4', 'mock-2', 'active',  'matrix',  'BTCUSDT'),
  S('ms-5', 'mock-4', 'active',  'grid', 'BNBUSDT'),
]

// ── AccountsPage ──────────────────────────────────────────────────────────────
export function AccountsPage() {
  const [accounts, setAccounts] = useState<ExchangeAccount[]>(MOCK_ACCOUNTS)
  const [strategies, setStrategies] = useState<Strategy[]>(MOCK_STRATEGIES)
  const [balances, setBalances] = useState<Record<string, BalanceResult>>(MOCK_BALANCES)
  const [loading, setLoading] = useState(true)
  const [modalOpen, setModalOpen] = useState(false)

  async function load() {
    setLoading(true)
    try {
      const [accs, strats] = await Promise.all([listAccounts(), listStrategies()])
      setAccounts([...MOCK_ACCOUNTS, ...accs])
      setStrategies([...MOCK_STRATEGIES, ...strats])
      const results = await Promise.allSettled(
        accs.map(a => getAccountBalance(a.id).then(b => ({ id: a.id, b })))
      )
      const bals: Record<string, BalanceResult> = { ...MOCK_BALANCES }
      for (const r of results) {
        if (r.status === 'fulfilled') bals[r.value.id] = r.value.b
      }
      setBalances(bals)
    } catch {
      setAccounts(MOCK_ACCOUNTS)
    } finally {
      setLoading(false)
    }
  }

  useEffect(() => { load() }, [])

  function handleDelete(id: string) {
    setAccounts(prev => prev.filter(a => a.id !== id))
  }

  function handleToggled(updated: ExchangeAccount) {
    setAccounts(prev => prev.map(a => a.id === updated.id ? updated : a))
  }

  return (
    <div className="max-w-3xl space-y-6">
      {/* Header row */}
      <div className="flex flex-col sm:flex-row sm:items-center justify-between gap-3">
        <div>
          <h1 className="text-xl font-bold text-white">API Keys</h1>
          <p className="text-sm text-slate-400 mt-0.5">Управление ключами биржевых аккаунтов</p>
        </div>
        <button
          type="button"
          onClick={() => setModalOpen(true)}
          className="flex items-center justify-center gap-1.5 px-4 py-2 bg-[linear-gradient(180deg,#4a7dff_0%,#3a67e6_100%)] text-white text-sm font-semibold rounded-lg hover:brightness-110 active:brightness-95 transition-all shadow-[0_4px_12px_-4px_rgba(74,125,255,.5)]"
        >
          <Plus size={15} strokeWidth={2.5} />
          Добавить API key
        </button>
      </div>

      {/* List */}
      {loading ? (
        <div className="py-12 text-center text-slate-500 text-sm">Загрузка…</div>
      ) : !accounts.length ? (
        <div className="py-12 text-center text-slate-500 text-sm">Нет аккаунтов. Нажмите «Добавить API key».</div>
      ) : (
        <ul className="flex flex-col gap-2">
          {accounts.map(acc => {
            const acctStrats = strategies.filter(s => s.account_id === acc.id)
            const acctBots = acctStrats.filter(s => s.strategy_type !== 'manual')
            return (
              <AccountCard
                key={acc.id}
                acc={acc}
                balance={balances[acc.id] ?? null}
                strategyTotal={acctStrats.length}
                strategyActive={acctStrats.filter(s => s.status === 'active').length}
                botTotal={acctBots.length}
                botActive={acctBots.filter(s => s.status === 'active').length}
                onDelete={handleDelete}
                onToggled={handleToggled}
              />
            )
          })}
        </ul>
      )}

      {modalOpen && (
        <AddKeyModal
          onClose={() => setModalOpen(false)}
          onAdded={load}
        />
      )}
    </div>
  )
}
