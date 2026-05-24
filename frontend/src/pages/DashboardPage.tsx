import { useEffect, useState } from 'react'
import { Link } from 'react-router-dom'
import { listSignals } from '../api/signals'
import type { Signal } from '../types'

export function DashboardPage() {
  const [signals, setSignals] = useState<Signal[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')

  useEffect(() => {
    listSignals()
      .then(setSignals)
      .catch((e: unknown) => setError(e instanceof Error ? e.message : 'Failed to load'))
      .finally(() => setLoading(false))
  }, [])

  if (loading) return <p className="text-gray-500 dark:text-gray-400">Loading…</p>
  if (error) return <p className="text-red-600 dark:text-red-400">{error}</p>

  return (
    <div>
      <div className="flex flex-col sm:flex-row sm:items-center justify-between mb-6 gap-3">
        <h1 className="text-xl font-semibold dark:text-white">Signals</h1>
        <Link
          to="/signals/new"
          className="bg-blue-600 text-white rounded px-4 py-2 text-sm font-medium hover:bg-blue-700 text-center"
        >
          Create signal
        </Link>
      </div>

      {signals.length === 0 ? (
        <div className="text-center py-16 text-gray-500 dark:text-gray-400">
          <p className="mb-4">No signals yet.</p>
          <Link to="/signals/new" className="text-blue-600 hover:underline">
            Create signal
          </Link>
        </div>
      ) : (
        <div className="bg-white dark:bg-gray-800 rounded-xl shadow overflow-hidden">
          <div className="overflow-x-auto">
            <table className="w-full text-sm min-w-[640px]">
            <thead className="bg-gray-50 dark:bg-gray-700 text-gray-600 dark:text-gray-300 uppercase text-xs">
              <tr>
                <th className="px-4 py-3 text-left">Name</th>
                <th className="px-4 py-3 text-left">Symbol</th>
                <th className="px-4 py-3 text-left">Timeframe</th>
                <th className="px-4 py-3 text-left">Direction</th>
                <th className="px-4 py-3 text-left">Status</th>
                <th className="px-4 py-3 text-left">Actions</th>
              </tr>
            </thead>
            <tbody className="divide-y divide-gray-100 dark:divide-gray-700">
              {signals.map((sig) => (
                <tr key={sig.id} className="hover:bg-gray-50 dark:hover:bg-gray-700">
                  <td className="px-4 py-3 font-medium">{sig.name}</td>
                  <td className="px-4 py-3 text-gray-600 dark:text-gray-400">{sig.symbol}</td>
                  <td className="px-4 py-3 text-gray-600 dark:text-gray-400">{sig.timeframe}</td>
                  <td className="px-4 py-3 text-gray-600 dark:text-gray-400">{sig.direction}</td>
                  <td className="px-4 py-3">
                    <span
                      className={`px-2 py-0.5 rounded-full text-xs font-medium ${
                        sig.is_active
                          ? 'bg-green-100 text-green-700'
                          : 'bg-gray-100 text-gray-600 dark:bg-gray-700 dark:text-gray-400'
                      }`}
                    >
                      {sig.is_active ? 'Active' : 'Inactive'}
                    </span>
                  </td>
                  <td className="px-4 py-3 space-x-2">
                    <Link
                      to={`/signals/${sig.id}/edit`}
                      className="text-blue-600 hover:underline"
                    >
                      Edit
                    </Link>
                    <Link
                      to={`/signals/${sig.id}/backtest`}
                      className="text-gray-600 hover:underline"
                    >
                      Backtest
                    </Link>
                    <Link
                      to={`/signals/${sig.id}/optimize`}
                      className="text-gray-600 hover:underline"
                    >
                      Optimize
                    </Link>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
          </div>
        </div>
      )}
    </div>
  )
}
