import { useState, useEffect } from 'react'
import { listAccounts, createAccount, deleteAccount, verifyAccount } from '../api/accounts'
import type { ExchangeAccount } from '../types'

export function AccountsPage() {
  const [accounts, setAccounts] = useState<ExchangeAccount[]>([])
  const [loading, setLoading] = useState(true)
  const [exchange, setExchange] = useState('bybit')
  const [label, setLabel] = useState('')
  const [apiKey, setApiKey] = useState('')
  const [secret, setSecret] = useState('')
  const [adding, setAdding] = useState(false)
  const [addMsg, setAddMsg] = useState<{ ok: boolean; text: string } | null>(null)
  const [verifyResults, setVerifyResults] = useState<Record<string, { ok: boolean; text: string }>>({})
  const [verifying, setVerifying] = useState<string | null>(null)

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
      await load()
    } catch (err: any) {
      setAddMsg({ ok: false, text: err?.response?.data?.error ?? 'Ошибка' })
    } finally {
      setAdding(false)
    }
  }

  async function handleVerify(id: string) {
    setVerifying(id)
    try {
      const res = await verifyAccount(id)
      setVerifyResults(prev => ({ ...prev, [id]: { ok: res.ok, text: res.ok ? 'OK' : (res.message ?? 'Ошибка') } }))
    } catch {
      setVerifyResults(prev => ({ ...prev, [id]: { ok: false, text: 'Ошибка запроса' } }))
    } finally {
      setVerifying(null)
    }
  }

  async function handleDelete(id: string) {
    if (!window.confirm('Удалить аккаунт?')) return
    await deleteAccount(id)
    await load()
  }

  return (
    <div className="max-w-2xl mx-auto space-y-8">
      {/* Add form */}
      <div className="bg-white dark:bg-gray-800 rounded-lg shadow p-6">
        <h2 className="text-lg font-semibold text-gray-900 dark:text-white mb-4">Добавить аккаунт</h2>
        <form onSubmit={handleAdd} className="space-y-3">
          <div className="grid grid-cols-2 gap-3">
            <div>
              <label className="block text-sm text-gray-600 dark:text-gray-400 mb-1">Биржа</label>
              <select value={exchange} onChange={e => setExchange(e.target.value)}
                className="w-full border border-gray-300 rounded px-3 py-2 text-sm focus:outline-none focus:ring-2 focus:ring-blue-500 dark:bg-gray-700 dark:border-gray-600 dark:text-white">
                <option value="bybit">Bybit</option>
                <option value="binance">Binance</option>
              </select>
            </div>
            <div>
              <label className="block text-sm text-gray-600 dark:text-gray-400 mb-1">Название</label>
              <input value={label} onChange={e => setLabel(e.target.value)} required placeholder="Мой аккаунт"
                className="w-full border border-gray-300 rounded px-3 py-2 text-sm focus:outline-none focus:ring-2 focus:ring-blue-500 dark:bg-gray-700 dark:border-gray-600 dark:text-white dark:placeholder-gray-400" />
            </div>
          </div>
          <div>
            <label className="block text-sm text-gray-600 dark:text-gray-400 mb-1">API Key</label>
            <input value={apiKey} onChange={e => setApiKey(e.target.value)} required type="password" placeholder="API Key"
              className="w-full border border-gray-300 rounded px-3 py-2 text-sm font-mono focus:outline-none focus:ring-2 focus:ring-blue-500 dark:bg-gray-700 dark:border-gray-600 dark:text-white dark:placeholder-gray-400" />
          </div>
          <div>
            <label className="block text-sm text-gray-600 dark:text-gray-400 mb-1">Secret</label>
            <input value={secret} onChange={e => setSecret(e.target.value)} required type="password" placeholder="Secret"
              className="w-full border border-gray-300 rounded px-3 py-2 text-sm font-mono focus:outline-none focus:ring-2 focus:ring-blue-500 dark:bg-gray-700 dark:border-gray-600 dark:text-white dark:placeholder-gray-400" />
          </div>
          <div className="flex items-center gap-3">
            <button type="submit" disabled={adding}
              className="px-4 py-2 bg-blue-600 text-white text-sm rounded hover:bg-blue-700 disabled:opacity-50">
              {adding ? 'Добавление...' : 'Добавить'}
            </button>
            {addMsg && (
              <span className={`text-sm ${addMsg.ok ? 'text-green-600' : 'text-red-600'}`}>
                {addMsg.ok ? '✓' : '✗'} {addMsg.text}
              </span>
            )}
          </div>
        </form>
      </div>

      {/* List */}
      <div className="bg-white dark:bg-gray-800 rounded-lg shadow">
        <div className="px-6 py-4 border-b border-gray-200 dark:border-gray-700">
          <h2 className="text-lg font-semibold text-gray-900 dark:text-white">Мои аккаунты</h2>
        </div>
        {loading ? (
          <div className="p-8 text-center text-gray-400">Загрузка...</div>
        ) : !accounts.length ? (
          <div className="p-8 text-center text-gray-400">Нет аккаунтов. Добавьте первый выше.</div>
        ) : (
          <ul className="divide-y divide-gray-100 dark:divide-gray-700">
            {accounts.map(acc => (
              <li key={acc.id} className="flex items-center justify-between px-6 py-4 hover:bg-gray-50 dark:hover:bg-gray-700">
                <div className="min-w-0">
                  <div className="flex items-center gap-2">
                    <span className="font-medium text-gray-900 dark:text-white capitalize">{acc.exchange}</span>
                    <span className="text-gray-400">·</span>
                    <span className="text-gray-700 dark:text-gray-300">{acc.label}</span>
                    <span className={`text-xs px-1.5 py-0.5 rounded-full ${acc.is_active ? 'bg-green-100 text-green-700' : 'bg-gray-100 text-gray-500 dark:bg-gray-700 dark:text-gray-400'}`}>
                      {acc.is_active ? 'Активен' : 'Неактивен'}
                    </span>
                  </div>
                  <div className="text-xs text-gray-400 mt-0.5">
                    Добавлен {new Date(acc.created_at).toLocaleDateString('ru-RU')}
                  </div>
                </div>
                <div className="flex items-center gap-3 ml-4 shrink-0">
                  {verifyResults[acc.id] && (
                    <span className={`text-xs ${verifyResults[acc.id].ok ? 'text-green-600' : 'text-red-500'}`}>
                      {verifyResults[acc.id].ok ? '✓' : '✗'} {verifyResults[acc.id].text}
                    </span>
                  )}
                  <button onClick={() => handleVerify(acc.id)} disabled={verifying === acc.id}
                    className="text-sm text-blue-600 hover:text-blue-800 disabled:opacity-50">
                    {verifying === acc.id ? '...' : 'Проверить'}
                  </button>
                  <button onClick={() => handleDelete(acc.id)}
                    className="text-sm text-red-500 hover:text-red-700">
                    Удалить
                  </button>
                </div>
              </li>
            ))}
          </ul>
        )}
      </div>
    </div>
  )
}
