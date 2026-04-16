import { useEffect, useState, FormEvent } from 'react'
import { useNavigate, useParams } from 'react-router-dom'
import { createSignal, getSignal, updateSignal } from '../api/signals'
import { ConditionTree } from '../components/ConditionTree'
import type { ConditionNode, GroupNode } from '../types'

const EXCHANGES = ['binance', 'bybit']
const MARKETS = ['spot', 'futures']
const TIMEFRAMES = ['1m', '5m', '15m', '1h', '4h', '1d']
const DIRECTIONS = ['LONG', 'SHORT', 'BOTH']

const DEFAULT_CONDITIONS: GroupNode = { type: 'AND', children: [] }

export function SignalBuilderPage() {
  const { id } = useParams<{ id: string }>()
  const isEdit = !!id
  const navigate = useNavigate()

  const [name, setName] = useState('')
  const [description, setDescription] = useState('')
  const [exchange, setExchange] = useState('binance')
  const [symbol, setSymbol] = useState('BTCUSDT')
  const [market, setMarket] = useState('spot')
  const [timeframe, setTimeframe] = useState('1h')
  const [direction, setDirection] = useState('LONG')
  const [conditions, setConditions] = useState<ConditionNode>(DEFAULT_CONDITIONS)
  const [error, setError] = useState('')
  const [loading, setLoading] = useState(false)

  useEffect(() => {
    if (!isEdit) return
    getSignal(id).then((sig) => {
      setName(sig.name)
      setDescription(sig.description)
      setExchange(sig.exchange)
      setSymbol(sig.symbol)
      setMarket(sig.market)
      setTimeframe(sig.timeframe)
      setDirection(sig.direction)
      setConditions(sig.conditions)
    })
  }, [id, isEdit])

  async function handleSubmit(e: FormEvent) {
    e.preventDefault()
    setError('')
    setLoading(true)
    try {
      if (isEdit) {
        await updateSignal(id, { name, description, direction, conditions })
      } else {
        await createSignal({ name, description, exchange, symbol, market, timeframe, direction, conditions })
      }
      navigate('/')
    } catch (err: unknown) {
      setError(err instanceof Error ? err.message : 'Save failed')
    } finally {
      setLoading(false)
    }
  }

  return (
    <div className="max-w-2xl">
      <h1 className="text-xl font-semibold mb-6">
        {isEdit ? 'Edit Signal' : 'New Signal'}
      </h1>
      <form onSubmit={handleSubmit} className="space-y-4">
        <input
          type="text"
          placeholder="Signal name"
          value={name}
          onChange={(e) => setName(e.target.value)}
          required
          className="w-full border rounded px-3 py-2 text-sm"
        />
        <input
          type="text"
          placeholder="Description (optional)"
          value={description}
          onChange={(e) => setDescription(e.target.value)}
          className="w-full border rounded px-3 py-2 text-sm"
        />

        <div className="grid grid-cols-2 gap-3">
          <div>
            <label className="block text-xs text-gray-600 mb-1">Exchange</label>
            <select
              value={exchange}
              onChange={(e) => setExchange(e.target.value)}
              disabled={isEdit}
              className="w-full border rounded px-2 py-1.5 text-sm"
            >
              {EXCHANGES.map((x) => <option key={x}>{x}</option>)}
            </select>
          </div>
          <div>
            <label className="block text-xs text-gray-600 mb-1">Market</label>
            <select
              value={market}
              onChange={(e) => setMarket(e.target.value)}
              disabled={isEdit}
              className="w-full border rounded px-2 py-1.5 text-sm"
            >
              {MARKETS.map((m) => <option key={m}>{m}</option>)}
            </select>
          </div>
          <div>
            <label className="block text-xs text-gray-600 mb-1" htmlFor="symbol-input">Symbol</label>
            <input
              id="symbol-input"
              type="text"
              value={symbol}
              onChange={(e) => setSymbol(e.target.value.toUpperCase())}
              disabled={isEdit}
              className="w-full border rounded px-2 py-1.5 text-sm"
            />
          </div>
          <div>
            <label className="block text-xs text-gray-600 mb-1" htmlFor="timeframe-select">Timeframe</label>
            <select
              id="timeframe-select"
              value={timeframe}
              onChange={(e) => setTimeframe(e.target.value)}
              disabled={isEdit}
              className="w-full border rounded px-2 py-1.5 text-sm"
            >
              {TIMEFRAMES.map((t) => <option key={t}>{t}</option>)}
            </select>
          </div>
          <div>
            <label className="block text-xs text-gray-600 mb-1">Direction</label>
            <select
              value={direction}
              onChange={(e) => setDirection(e.target.value)}
              className="w-full border rounded px-2 py-1.5 text-sm"
            >
              {DIRECTIONS.map((d) => <option key={d}>{d}</option>)}
            </select>
          </div>
        </div>

        <div>
          <p className="text-sm font-medium text-gray-700 mb-2">Conditions</p>
          <ConditionTree value={conditions} onChange={setConditions} />
        </div>

        {error && <p className="text-red-600 text-sm">{error}</p>}

        <div className="flex gap-3">
          <button
            type="submit"
            disabled={loading}
            className="bg-blue-600 text-white rounded px-4 py-2 text-sm font-medium disabled:opacity-50"
          >
            {loading ? 'Saving…' : 'Save signal'}
          </button>
          <button
            type="button"
            onClick={() => navigate('/')}
            className="border rounded px-4 py-2 text-sm text-gray-700 hover:bg-gray-50"
          >
            Cancel
          </button>
        </div>
      </form>
    </div>
  )
}
