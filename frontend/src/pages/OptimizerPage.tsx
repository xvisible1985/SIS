import { useEffect, useState, FormEvent } from 'react'
import { useParams } from 'react-router-dom'
import { submitOptimize, getOptimizationResults } from '../api/signals'
import { useJobProgress } from '../hooks/useJobProgress'
import { ProgressBar } from '../components/ProgressBar'
import type { OptimizationResult } from '../types'

export function OptimizerPage() {
  const { id } = useParams<{ id: string }>()
  const [periodFrom, setPeriodFrom] = useState('2025-01-01')
  const [periodTo, setPeriodTo] = useState('2026-01-01')
  const [mode, setMode] = useState<'fast' | 'walk_forward'>('fast')
  const [takeProfit, setTakeProfit] = useState('1.5,2.0,3.0')
  const [stopLoss, setStopLoss] = useState('0.5,1.0,1.5')
  const [jobId, setJobId] = useState<string | null>(null)
  const [results, setResults] = useState<OptimizationResult[]>([])
  const [error, setError] = useState('')
  const [submitting, setSubmitting] = useState(false)

  const progress = useJobProgress(jobId, 'optimize')

  useEffect(() => {
    if (!id) return
    getOptimizationResults(id).then(setResults).catch((e: unknown) => {
      setError(e instanceof Error ? e.message : 'Failed to load results')
    })
  }, [id])

  useEffect(() => {
    if (progress.status === 'done' && id) {
      getOptimizationResults(id).then(setResults).catch((e: unknown) => {
        setError(e instanceof Error ? e.message : 'Failed to load results')
      })
      setJobId(null)
    }
  }, [progress.status, id])

  function parseList(s: string): number[] {
    return s
      .split(',')
      .map((v) => Number(v.trim()))
      .filter((v) => !isNaN(v) && v > 0)
  }

  async function handleSubmit(e: FormEvent) {
    e.preventDefault()
    if (!id) return
    setError('')
    setSubmitting(true)
    try {
      const res = await submitOptimize(id!, {
        period_from: periodFrom,
        period_to: periodTo,
        mode,
        score_by: 'profit_factor',
        top_n: 10,
        take_profits: parseList(takeProfit),
        stop_losses: parseList(stopLoss),
        param_space: {},
        wf_folds: 5,
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
      <h1 className="text-xl font-semibold">Optimizer</h1>

      <div className="bg-white rounded-xl shadow p-5">
        <form onSubmit={handleSubmit} className="grid grid-cols-2 gap-4">
          <div>
            <label className="block text-xs text-gray-600 mb-1" htmlFor="opt-period-from">
              Period From
            </label>
            <input
              id="opt-period-from"
              type="date"
              value={periodFrom}
              onChange={(e) => setPeriodFrom(e.target.value)}
              required
              className="w-full border rounded px-2 py-1.5 text-sm"
            />
          </div>
          <div>
            <label className="block text-xs text-gray-600 mb-1" htmlFor="opt-period-to">
              Period To
            </label>
            <input
              id="opt-period-to"
              type="date"
              value={periodTo}
              onChange={(e) => setPeriodTo(e.target.value)}
              required
              className="w-full border rounded px-2 py-1.5 text-sm"
            />
          </div>
          <div>
            <label className="block text-xs text-gray-600 mb-1">Mode</label>
            <select
              value={mode}
              onChange={(e) => setMode(e.target.value as 'fast' | 'walk_forward')}
              className="w-full border rounded px-2 py-1.5 text-sm"
            >
              <option value="fast">Fast (grid search)</option>
              <option value="walk_forward">Walk-Forward</option>
            </select>
          </div>
          <div>
            <label className="block text-xs text-gray-600 mb-1">
              Take Profit values (comma-separated %)
            </label>
            <input
              type="text"
              value={takeProfit}
              onChange={(e) => setTakeProfit(e.target.value)}
              placeholder="1.5,2.0,3.0"
              className="w-full border rounded px-2 py-1.5 text-sm"
            />
          </div>
          <div>
            <label className="block text-xs text-gray-600 mb-1">
              Stop Loss values (comma-separated %)
            </label>
            <input
              type="text"
              value={stopLoss}
              onChange={(e) => setStopLoss(e.target.value)}
              placeholder="0.5,1.0,1.5"
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
              {submitting ? 'Submitting…' : 'Run optimizer'}
            </button>
          </div>
        </form>
      </div>

      {jobId && (
        <div className="bg-white rounded-xl shadow p-5">
          <p className="text-sm font-medium mb-3">Optimizing…</p>
          <ProgressBar pct={progress.pct} status={progress.status} />
        </div>
      )}

      {results.map((result) => (
        <div key={result.id} className="bg-white rounded-xl shadow overflow-hidden">
          <div className="px-5 py-3 border-b flex items-center gap-3">
            <h2 className="font-medium text-sm">
              Top Combinations — {result.mode} —{' '}
              {new Date(result.created_at).toLocaleDateString()}
            </h2>
          </div>
          <table className="w-full text-sm">
            <thead className="bg-gray-50 text-xs text-gray-600 uppercase">
              <tr>
                <th className="px-4 py-2 text-right">#</th>
                <th className="px-4 py-2 text-right">Score</th>
                <th className="px-4 py-2 text-right">Win Rate</th>
                <th className="px-4 py-2 text-right">Profit Factor</th>
                <th className="px-4 py-2 text-right">TP %</th>
                <th className="px-4 py-2 text-right">SL %</th>
                <th className="px-4 py-2 text-right">Total</th>
                <th className="px-4 py-2 text-left">Params</th>
              </tr>
            </thead>
            <tbody className="divide-y divide-gray-100">
              {result.top_combinations.map((combo, idx) => (
                <tr key={idx}>
                  <td className="px-4 py-2 text-right text-gray-500">{idx + 1}</td>
                  <td className="px-4 py-2 text-right font-medium">
                    {combo.score.toFixed(2)}
                  </td>
                  <td className="px-4 py-2 text-right">
                    {(combo.win_rate * 100).toFixed(1)}%
                  </td>
                  <td className="px-4 py-2 text-right">
                    {combo.profit_factor.toFixed(2)}
                  </td>
                  <td className="px-4 py-2 text-right">{combo.take_profit}</td>
                  <td className="px-4 py-2 text-right">{combo.stop_loss}</td>
                  <td className="px-4 py-2 text-right">{combo.total_signals}</td>
                  <td className="px-4 py-2 text-left text-xs text-gray-600">
                    {Object.entries(combo.params)
                      .map(([k, v]) => `${k}=${v}`)
                      .join(', ')}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      ))}
    </div>
  )
}
