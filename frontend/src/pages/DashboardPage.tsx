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

  if (loading) return <p className="text-gray-500">Loading…</p>
  if (error) return <p className="text-red-600">{error}</p>

  return (
    <div>
      <div className="flex items-center justify-between mb-6">
        <h1 className="text-xl font-semibold">Signals</h1>
        <Link
          to="/signals/new"
          className="bg-blue-600 text-white rounded px-4 py-2 text-sm font-medium hover:bg-blue-700"
        >
          Create signal
        </Link>
      </div>

      {signals.length === 0 ? (
        <div className="text-center py-16 text-gray-500">
          <p className="mb-4">No signals yet.</p>
          <Link to="/signals/new" className="text-blue-600 hover:underline">
            Create signal
          </Link>
        </div>
      ) : (
        <div className="bg-white rounded-xl shadow overflow-hidden">
          <table className="w-full text-sm">
            <thead className="bg-gray-50 text-gray-600 uppercase text-xs">
              <tr>
                <th className="px-4 py-3 text-left">Name</th>
                <th className="px-4 py-3 text-left">Symbol</th>
                <th className="px-4 py-3 text-left">Timeframe</th>
                <th className="px-4 py-3 text-left">Direction</th>
                <th className="px-4 py-3 text-left">Status</th>
                <th className="px-4 py-3 text-left">Actions</th>
              </tr>
            </thead>
            <tbody className="divide-y divide-gray-100">
              {signals.map((sig) => (
                <tr key={sig.id} className="hover:bg-gray-50">
                  <td className="px-4 py-3 font-medium">{sig.name}</td>
                  <td className="px-4 py-3 text-gray-600">{sig.symbol}</td>
                  <td className="px-4 py-3 text-gray-600">{sig.timeframe}</td>
                  <td className="px-4 py-3 text-gray-600">{sig.direction}</td>
                  <td className="px-4 py-3">
                    <span
                      className={`px-2 py-0.5 rounded-full text-xs font-medium ${
                        sig.is_active
                          ? 'bg-green-100 text-green-700'
                          : 'bg-gray-100 text-gray-600'
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
      )}
    </div>
  )
}
