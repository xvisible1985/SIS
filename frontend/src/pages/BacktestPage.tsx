import { useEffect, useState, FormEvent } from 'react'
import { useParams } from 'react-router-dom'
import { submitBacktest, getBacktestResults } from '../api/signals'
import { useJobProgress } from '../hooks/useJobProgress'
import { ProgressBar } from '../components/ProgressBar'
import type { BacktestResult } from '../types'

export function BacktestPage() {
  const { id } = useParams<{ id: string }>()
  const [periodFrom, setPeriodFrom] = useState('2025-01-01')
  const [periodTo, setPeriodTo] = useState('2026-01-01')
  const [takeProfit, setTakeProfit] = useState(2)
  const [stopLoss, setStopLoss] = useState(1)
  const [jobId, setJobId] = useState<string | null>(null)
  const [results, setResults] = useState<BacktestResult[]>([])
  const [error, setError] = useState('')
  const [submitting, setSubmitting] = useState(false)

  const progress = useJobProgress(jobId, 'backtest')

  // Load previous results
  useEffect(() => {
    if (!id) return
    getBacktestResults(id).then(setResults).catch((e: unknown) => {
      setError(e instanceof Error ? e.message : 'Failed to load results')
    })
  }, [id])

  // Reload results when job completes
  useEffect(() => {
    if (progress.status === 'done' && id) {
      getBacktestResults(id).then(setResults).catch((e: unknown) => {
        setError(e instanceof Error ? e.message : 'Failed to load results')
      })
      setJobId(null)
    }
  }, [progress.status, id])

  async function handleSubmit(e: FormEvent) {
    e.preventDefault()
    if (!id) return
    setError('')
    setSubmitting(true)
    try {
      const res = await submitBacktest(id!, {
        period_from: periodFrom,
        period_to: periodTo,
        take_profit: takeProfit,
        stop_loss: stopLoss,
      })
      setJobId(res.job_id)
    } catch (err: unknown) {
      setError(err instanceof Error ? err.message : 'Failed to submit')
    } finally {
      setSubmitting(false)
    }
  }

  return (
    <div className="max-w-3xl space-y-6">
      <h1 className="text-xl font-semibold">Backtest</h1>

      <div className="bg-white rounded-xl shadow p-5">
        <form onSubmit={handleSubmit} className="grid grid-cols-2 gap-4">
          <div>
            <label className="block text-xs text-gray-600 mb-1" htmlFor="period-from">
              Period From
            </label>
            <input
              id="period-from"
              type="date"
              value={periodFrom}
              onChange={(e) => setPeriodFrom(e.target.value)}
              required
              className="w-full border rounded px-2 py-1.5 text-sm"
            />
          </div>
          <div>
            <label className="block text-xs text-gray-600 mb-1" htmlFor="period-to">
              Period To
            </label>
            <input
              id="period-to"
              type="date"
              value={periodTo}
              onChange={(e) => setPeriodTo(e.target.value)}
              required
              className="w-full border rounded px-2 py-1.5 text-sm"
            />
          </div>
          <div>
            <label className="block text-xs text-gray-600 mb-1" htmlFor="take-profit">
              Take Profit %
            </label>
            <input
              id="take-profit"
              type="number"
              step="0.1"
              min="0.1"
              value={takeProfit}
              onChange={(e) => setTakeProfit(Number(e.target.value))}
              className="w-full border rounded px-2 py-1.5 text-sm"
            />
          </div>
          <div>
            <label className="block text-xs text-gray-600 mb-1" htmlFor="stop-loss">
              Stop Loss %
            </label>
            <input
              id="stop-loss"
              type="number"
              step="0.1"
              min="0.1"
              value={stopLoss}
              onChange={(e) => setStopLoss(Number(e.target.value))}
              className="w-full border rounded px-2 py-1.5 text-sm"
            />
          </div>
          <div className="col-span-2">
            {error && <p className="text-red-600 text-sm mb-2">{error}</p>}
            <button
              type="submit"
              disabled={submitting || !!jobId}
              className="bg-blue-600 text-white rounded px-4 py-2 text-sm font-medium disabled:opacity-50"
            >
              {submitting ? 'Submitting…' : 'Run backtest'}
            </button>
          </div>
        </form>
      </div>

      {jobId && (
        <div className="bg-white rounded-xl shadow p-5">
          <p className="text-sm font-medium mb-3">Running…</p>
          <ProgressBar pct={progress.pct} status={progress.status} />
        </div>
      )}

      {results.length > 0 && (
        <div className="bg-white rounded-xl shadow overflow-hidden">
          <div className="px-5 py-3 border-b">
            <h2 className="font-medium text-sm">Results</h2>
          </div>
          <table className="w-full text-sm">
            <thead className="bg-gray-50 text-xs text-gray-600 uppercase">
              <tr>
                <th className="px-4 py-2 text-left">Date</th>
                <th className="px-4 py-2 text-right">Total</th>
                <th className="px-4 py-2 text-right">Win Rate</th>
                <th className="px-4 py-2 text-right">Avg Gain %</th>
                <th className="px-4 py-2 text-right">Max DD %</th>
                <th className="px-4 py-2 text-right">Profit Factor</th>
              </tr>
            </thead>
            <tbody className="divide-y divide-gray-100">
              {results.map((r) => (
                <tr key={r.id}>
                  <td className="px-4 py-2 text-gray-600">
                    {new Date(r.created_at).toLocaleDateString()}
                  </td>
                  <td className="px-4 py-2 text-right">{r.total_signals}</td>
                  <td className="px-4 py-2 text-right">
                    {(r.win_rate * 100).toFixed(1)}%
                  </td>
                  <td className="px-4 py-2 text-right">{r.avg_gain.toFixed(2)}</td>
                  <td className="px-4 py-2 text-right">{r.max_drawdown.toFixed(2)}</td>
                  <td className="px-4 py-2 text-right">{r.profit_factor.toFixed(2)}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </div>
  )
}
