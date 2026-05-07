import { useState, useEffect, useCallback } from 'react'
import { listStrategies } from '../api/strategies'
import { listAccounts } from '../api/accounts'
import { StrategyCard } from '../components/strategies/StrategyCard'
import { StrategyModal } from '../components/strategies/StrategyModal'
import type { Strategy, ExchangeAccount } from '../types'

export function StrategiesPage() {
  const [strategies, setStrategies] = useState<Strategy[]>([])
  const [accounts, setAccounts] = useState<ExchangeAccount[]>([])
  const [loading, setLoading] = useState(true)
  const [modalOpen, setModalOpen] = useState(false)
  const [editTarget, setEditTarget] = useState<Strategy | undefined>()
  const [expandedId, setExpandedId] = useState<string | null>(null)

  const load = useCallback(async () => {
    setLoading(true)
    try {
      const [strats, accs] = await Promise.all([listStrategies(), listAccounts()])
      setStrategies(strats)
      setAccounts(accs)
    } catch {
      setStrategies([])
    } finally {
      setLoading(false)
    }
  }, [])

  useEffect(() => { load() }, [load])

  function openCreate() {
    setEditTarget(undefined)
    setModalOpen(true)
  }

  function openEdit(s: Strategy) {
    setEditTarget(s)
    setModalOpen(true)
  }

  function handleSaved() {
    setModalOpen(false)
    load()
  }

  return (
    <div className="max-w-2xl mx-auto space-y-6">
      <div className="flex items-center justify-between">
        <h1 className="text-xl font-semibold text-gray-900 dark:text-white">Стратегии</h1>
        <button
          onClick={openCreate}
          className="px-4 py-2 bg-blue-600 text-white text-sm rounded-lg hover:bg-blue-700 transition-colors"
        >
          + Новая стратегия
        </button>
      </div>

      <div className="bg-white dark:bg-gray-800 rounded-xl shadow overflow-hidden">
        {loading ? (
          <div className="p-10 text-center text-gray-400">Загрузка…</div>
        ) : strategies.length === 0 ? (
          <div className="p-10 text-center text-gray-400">
            Нет стратегий. Нажмите «+ Новая стратегия».
          </div>
        ) : (
          <ul className="divide-y divide-gray-100 dark:divide-gray-700">
            {strategies.map(s => (
              <StrategyCard
                key={s.id}
                strategy={s}
                accounts={accounts}
                orders={[]}
                onEdit={openEdit}
                onChanged={load}
                isOpen={s.id === expandedId}
                onToggleOpen={() => setExpandedId(prev => prev === s.id ? null : s.id)}
              />
            ))}
          </ul>
        )}
      </div>

      {modalOpen && (
        <StrategyModal
          strategy={editTarget}
          onClose={() => setModalOpen(false)}
          onSaved={handleSaved}
        />
      )}
    </div>
  )
}
