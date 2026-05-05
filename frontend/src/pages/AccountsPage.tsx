import { useState, useEffect } from 'react'
import {
  listAccounts, createAccount, deleteAccount,
  verifyAccount, getAccountBalance, toggleAccountActive,
  type VerifyResult, type BalanceResult,
} from '../api/accounts'
import type { ExchangeAccount } from '../types'

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
interface CardState {
  open: boolean
  verify: VerifyResult | null
  balance: BalanceResult | null
  verifying: boolean
  loadingBalance: boolean
  toggling: boolean
}

function AccountCard({
  acc,
  onDelete,
  onToggled,
}: {
  acc: ExchangeAccount
  onDelete: (id: string) => void
  onToggled: (updated: ExchangeAccount) => void
}) {
  const [s, setS] = useState<CardState>({
    open: false, verify: null, balance: null,
    verifying: false, loadingBalance: false, toggling: false,
  })

  async function fetchBalance() {
    if (s.balance) return
    setS(p => ({ ...p, loadingBalance: true }))
    try {
      const b = await getAccountBalance(acc.id)
      setS(p => ({ ...p, balance: b }))
    } finally {
      setS(p => ({ ...p, loadingBalance: false }))
    }
  }

  function handleOpen() {
    setS(p => {
      if (!p.open) setTimeout(fetchBalance, 0)
      return { ...p, open: !p.open }
    })
  }

  async function handleVerify() {
    setS(p => ({ ...p, verifying: true }))
    try {
      const v = await verifyAccount(acc.id)
      setS(p => ({ ...p, verify: v }))
    } finally {
      setS(p => ({ ...p, verifying: false }))
    }
  }

  async function handleToggle() {
    setS(p => ({ ...p, toggling: true }))
    try {
      const updated = await toggleAccountActive(acc.id)
      onToggled(updated)
    } finally {
      setS(p => ({ ...p, toggling: false }))
    }
  }

  async function handleDelete() {
    if (!window.confirm('Удалить аккаунт?')) return
    await deleteAccount(acc.id)
    onDelete(acc.id)
  }

  return (
    <li className="border border-gray-200 dark:border-gray-700 rounded-xl overflow-hidden">
      {/* Header */}
      <button
        onClick={handleOpen}
        className="w-full flex items-center justify-between px-5 py-4 hover:bg-gray-50 dark:hover:bg-gray-700/40 transition-colors text-left"
      >
        <div className="min-w-0">
          <div className="flex items-center gap-2">
            <span className="font-semibold text-gray-900 dark:text-white capitalize">{acc.exchange}</span>
            <span className="text-gray-400">·</span>
            <span className="text-gray-700 dark:text-gray-300">{acc.label}</span>
          </div>
          <div className="text-xs text-gray-400 mt-0.5">
            Добавлен {new Date(acc.created_at).toLocaleDateString('ru-RU')}
          </div>
        </div>
        <div className="flex items-center gap-3 shrink-0 ml-4">
          <span className={`text-xs px-2 py-0.5 rounded-full font-medium ${
            acc.is_active
              ? 'bg-green-100 text-green-700 dark:bg-green-900/30 dark:text-green-400'
              : 'bg-gray-100 text-gray-500 dark:bg-gray-700 dark:text-gray-400'
          }`}>
            {acc.is_active ? 'Активен' : 'Остановлен'}
          </span>
          <svg
            className={`w-4 h-4 text-gray-400 transition-transform duration-200 ${s.open ? 'rotate-180' : ''}`}
            fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2}
          >
            <path strokeLinecap="round" strokeLinejoin="round" d="M19 9l-7 7-7-7" />
          </svg>
        </div>
      </button>

      {/* Expanded body */}
      {s.open && (
        <div className="border-t border-gray-200 dark:border-gray-700 px-5 py-4 space-y-4 bg-gray-50/50 dark:bg-gray-800/30">

          {/* Balance */}
          <div>
            <div className="text-xs text-gray-500 dark:text-gray-400 mb-1">Депозит (USDT)</div>
            {s.loadingBalance ? (
              <div className="text-sm text-gray-400">Загрузка…</div>
            ) : s.balance ? (
              s.balance.ok ? (
                <div className="flex items-baseline gap-3">
                  <span className="text-lg font-semibold text-gray-900 dark:text-white">
                    {s.balance.available?.toLocaleString('ru-RU', { maximumFractionDigits: 2 })}
                  </span>
                  <span className="text-xs text-gray-400">
                    доступно / всего {s.balance.equity?.toLocaleString('ru-RU', { maximumFractionDigits: 2 })}
                  </span>
                </div>
              ) : (
                <div className="text-sm text-red-500">{s.balance.message ?? 'Ошибка загрузки'}</div>
              )
            ) : (
              <div className="text-sm text-gray-400">—</div>
            )}
          </div>

          {/* Permissions */}
          <div>
            <div className="text-xs text-gray-500 dark:text-gray-400 mb-1.5">Разрешения</div>
            {s.verify === null ? (
              <div className="text-sm text-gray-400">Нажмите «Проверить» для загрузки</div>
            ) : !s.verify.ok ? (
              <div className="text-sm text-red-500">{s.verify.message ?? 'Ошибка'}</div>
            ) : (
              <div className="space-y-2">
                {/* Read/write + IP row */}
                <div className="flex flex-wrap gap-1.5">
                  <span className={`text-xs px-2 py-0.5 rounded-full font-medium ${
                    s.verify.read_only
                      ? 'bg-yellow-100 text-yellow-700 dark:bg-yellow-900/30 dark:text-yellow-300'
                      : 'bg-green-100 text-green-700 dark:bg-green-900/30 dark:text-green-400'
                  }`}>
                    {s.verify.read_only ? 'Только чтение' : 'Чтение и запись'}
                  </span>
                  {s.verify.ips && s.verify.ips.length > 0 && (
                    <span className="text-xs px-2 py-0.5 rounded-full font-medium bg-gray-100 text-gray-600 dark:bg-gray-700 dark:text-gray-300 font-mono">
                      IP: {s.verify.ips.join(', ')}
                    </span>
                  )}
                </div>
                {/* All permission categories */}
                <div className="flex flex-wrap gap-1.5">
                  {Object.entries(s.verify.permissions ?? {}).map(([key, vals]) => {
                    const active = (vals as string[]).length > 0
                    return (
                      <span key={key} className={`text-xs px-2 py-0.5 rounded-full font-medium ${
                        active
                          ? 'bg-blue-100 text-blue-700 dark:bg-blue-900/30 dark:text-blue-300'
                          : 'bg-gray-100 text-gray-400 dark:bg-gray-700/50 dark:text-gray-500 line-through'
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
          <div className="flex items-center gap-2 pt-1">
            <button
              onClick={handleVerify}
              disabled={s.verifying}
              className="px-3 py-1.5 text-sm bg-blue-600 text-white rounded-lg hover:bg-blue-700 disabled:opacity-50 transition-colors"
            >
              {s.verifying ? 'Проверка…' : 'Проверить'}
            </button>
            <button
              onClick={handleToggle}
              disabled={s.toggling}
              className={`px-3 py-1.5 text-sm rounded-lg border transition-colors disabled:opacity-50 ${
                acc.is_active
                  ? 'border-gray-300 dark:border-gray-600 text-gray-700 dark:text-gray-300 hover:bg-gray-100 dark:hover:bg-gray-700'
                  : 'border-green-500 text-green-600 dark:text-green-400 hover:bg-green-50 dark:hover:bg-green-900/20'
              }`}
            >
              {s.toggling ? '…' : acc.is_active ? 'Остановить' : 'Запустить'}
            </button>
            <button
              onClick={handleDelete}
              className="ml-auto px-3 py-1.5 text-sm text-red-500 hover:text-red-700 hover:bg-red-50 dark:hover:bg-red-900/20 rounded-lg transition-colors"
            >
              Удалить
            </button>
          </div>
        </div>
      )}
    </li>
  )
}

// ── AccountsPage ──────────────────────────────────────────────────────────────
export function AccountsPage() {
  const [accounts, setAccounts] = useState<ExchangeAccount[]>([])
  const [loading, setLoading] = useState(true)
  const [exchange, setExchange] = useState('bybit')
  const [label, setLabel] = useState('')
  const [apiKey, setApiKey] = useState('')
  const [secret, setSecret] = useState('')
  const [formOpen, setFormOpen] = useState(false)
  const [adding, setAdding] = useState(false)
  const [addMsg, setAddMsg] = useState<{ ok: boolean; text: string } | null>(null)

  async function load() {
    setLoading(true)
    try {
      setAccounts(await listAccounts())
    } catch {
      setAccounts([])
    } finally {
      setLoading(false)
    }
  }

  useEffect(() => { load() }, [])

  async function handleAdd(e: React.FormEvent) {
    e.preventDefault()
    setAdding(true)
    setAddMsg(null)
    try {
      await createAccount({ exchange, label, api_key: apiKey, secret })
      setAddMsg({ ok: true, text: 'Аккаунт добавлен' })
      setLabel(''); setApiKey(''); setSecret('')
      setFormOpen(false)
      await load()
    } catch (err: any) {
      setAddMsg({ ok: false, text: err?.response?.data?.error ?? 'Ошибка' })
    } finally {
      setAdding(false)
    }
  }

  function handleDelete(id: string) {
    setAccounts(prev => prev.filter(a => a.id !== id))
  }

  function handleToggled(updated: ExchangeAccount) {
    setAccounts(prev => prev.map(a => a.id === updated.id ? updated : a))
  }

  return (
    <div className="max-w-2xl mx-auto space-y-8">
      {/* Add form */}
      <div className="bg-white dark:bg-gray-800 rounded-xl shadow overflow-hidden">
        <button
          onClick={() => { setFormOpen(v => !v); setAddMsg(null) }}
          className="w-full flex items-center justify-between px-6 py-4 hover:bg-gray-50 dark:hover:bg-gray-700/40 transition-colors text-left"
        >
          <span className="text-base font-semibold text-gray-900 dark:text-white">Добавить аккаунт</span>
          <svg
            className={`w-4 h-4 text-gray-400 transition-transform duration-200 ${formOpen ? 'rotate-180' : ''}`}
            fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2}
          >
            <path strokeLinecap="round" strokeLinejoin="round" d="M19 9l-7 7-7-7" />
          </svg>
        </button>

        {formOpen && (
          <div className="border-t border-gray-200 dark:border-gray-700 px-6 py-5">
            <form onSubmit={handleAdd} className="space-y-3">
              <div className="grid grid-cols-2 gap-3">
                <div>
                  <label className="block text-sm text-gray-600 dark:text-gray-400 mb-1">Биржа</label>
                  <select value={exchange} onChange={e => setExchange(e.target.value)}
                    className="w-full border rounded px-3 py-2 text-sm dark:bg-gray-700 dark:border-gray-600 dark:text-white">
                    <option value="bybit">Bybit</option>
                    <option value="binance">Binance</option>
                  </select>
                </div>
                <div>
                  <label className="block text-sm text-gray-600 dark:text-gray-400 mb-1">Название</label>
                  <input value={label} onChange={e => setLabel(e.target.value)} required placeholder="Мой аккаунт"
                    autoComplete="off"
                    className="w-full border rounded px-3 py-2 text-sm dark:bg-gray-700 dark:border-gray-600 dark:text-white" />
                </div>
              </div>
              <div>
                <label className="block text-sm text-gray-600 dark:text-gray-400 mb-1">API Key</label>
                <input value={apiKey} onChange={e => setApiKey(e.target.value)} required type="password" placeholder="API Key"
                  autoComplete="new-password"
                  className="w-full border rounded px-3 py-2 text-sm font-mono dark:bg-gray-700 dark:border-gray-600 dark:text-white" />
              </div>
              <div>
                <label className="block text-sm text-gray-600 dark:text-gray-400 mb-1">Secret</label>
                <input value={secret} onChange={e => setSecret(e.target.value)} required type="password" placeholder="Secret"
                  autoComplete="new-password"
                  className="w-full border rounded px-3 py-2 text-sm font-mono dark:bg-gray-700 dark:border-gray-600 dark:text-white" />
              </div>
              <div className="flex items-center gap-3">
                <button type="submit" disabled={adding}
                  className="px-4 py-2 bg-blue-600 text-white text-sm rounded-lg hover:bg-blue-700 disabled:opacity-50 transition-colors">
                  {adding ? 'Добавление…' : 'Добавить'}
                </button>
                {addMsg && (
                  <span className={`text-sm ${addMsg.ok ? 'text-green-600' : 'text-red-600'}`}>
                    {addMsg.ok ? '✓' : '✗'} {addMsg.text}
                  </span>
                )}
              </div>
            </form>
          </div>
        )}
      </div>

      {/* List */}
      <div className="bg-white dark:bg-gray-800 rounded-xl shadow">
        <div className="px-6 py-4 border-b border-gray-200 dark:border-gray-700">
          <h2 className="text-lg font-semibold text-gray-900 dark:text-white">Мои аккаунты</h2>
        </div>
        {loading ? (
          <div className="p-8 text-center text-gray-400">Загрузка…</div>
        ) : !accounts.length ? (
          <div className="p-8 text-center text-gray-400">Нет аккаунтов. Добавьте первый выше.</div>
        ) : (
          <ul className="divide-y divide-gray-100 dark:divide-gray-700">
            {accounts.map(acc => (
              <AccountCard
                key={acc.id}
                acc={acc}
                onDelete={handleDelete}
                onToggled={handleToggled}
              />
            ))}
          </ul>
        )}
      </div>
    </div>
  )
}
