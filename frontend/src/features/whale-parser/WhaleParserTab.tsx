import { useState, useEffect, useCallback } from 'react'
import {
  listWhaleAddresses, createWhaleAddress, patchWhaleAddress,
  deleteWhaleAddress, listWhaleEvents, getWhaleState, simulateWhale
} from './api'
import type { WhaleAddress, WhaleEvent, WhaleStateRow, SimulateResult, WhaleDirection } from './types'

function DirectionBadge({ dir }: { dir: WhaleDirection }) {
  if (dir === 'buy')  return <span className="text-emerald-400 font-semibold">BUY</span>
  if (dir === 'sell') return <span className="text-red-400 font-semibold">SELL</span>
  return <span className="text-slate-500">—</span>
}

function fmt(n: number) {
  if (n >= 1_000_000) return `$${(n / 1_000_000).toFixed(1)}M`
  if (n >= 1_000)     return `$${(n / 1_000).toFixed(0)}K`
  return `$${n.toFixed(0)}`
}

function timeAgo(iso: string) {
  const diff = Date.now() - new Date(iso).getTime()
  const m = Math.floor(diff / 60000)
  if (m < 1)  return 'только что'
  if (m < 60) return `${m} мин назад`
  const h = Math.floor(m / 60)
  if (h < 24) return `${h}ч назад`
  return `${Math.floor(h / 24)}д назад`
}

function explorerLink(txHash: string, chain: string) {
  if (chain === 'eth')  return `https://etherscan.io/tx/${txHash}`
  if (chain === 'tron') return `https://tronscan.org/#/transaction/${txHash}`
  return '#'
}

export function WhaleParserTab() {
  const [addresses, setAddresses] = useState<WhaleAddress[]>([])
  const [events, setEvents]       = useState<WhaleEvent[]>([])
  const [state, setState]         = useState<WhaleStateRow[]>([])
  const [loading, setLoading]     = useState(true)

  // Simulate panel
  const [threshold, setThreshold]   = useState(50000)
  const [windowH, setWindowH]       = useState(2)
  const [simResult, setSimResult]   = useState<SimulateResult | null>(null)
  const [simLoading, setSimLoading] = useState(false)

  // Add address modal
  const [showAdd, setShowAdd]     = useState(false)
  const [newAddr, setNewAddr]     = useState('')
  const [newChain, setNewChain]   = useState<'eth' | 'tron'>('eth')
  const [newLabel, setNewLabel]   = useState('')
  const [addLoading, setAddLoading] = useState(false)

  const load = useCallback(async () => {
    try {
      const [a, e, s] = await Promise.all([
        listWhaleAddresses(),
        listWhaleEvents(50),
        getWhaleState(),
      ])
      setAddresses(a)
      setEvents(e)
      setState(s)
    } finally {
      setLoading(false)
    }
  }, [])

  useEffect(() => { load() }, [load])

  async function handleSimulate() {
    setSimLoading(true)
    try {
      const r = await simulateWhale({ threshold_usdt: threshold, window_hours: windowH })
      setSimResult(r)
    } finally {
      setSimLoading(false)
    }
  }

  async function handleAddAddress() {
    if (!newAddr.trim()) return
    setAddLoading(true)
    try {
      await createWhaleAddress({ address: newAddr.trim(), chain: newChain, label: newLabel || undefined })
      setShowAdd(false)
      setNewAddr('')
      setNewLabel('')
      load()
    } finally {
      setAddLoading(false)
    }
  }

  async function handleToggleActive(addr: WhaleAddress) {
    await patchWhaleAddress(addr.id, { isActive: !addr.isActive })
    load()
  }

  async function handleDelete(addr: WhaleAddress) {
    await deleteWhaleAddress(addr.id)
    load()
  }

  if (loading) return <div className="p-6 text-slate-400 text-sm">Загрузка…</div>

  return (
    <div className="p-4 space-y-5 text-sm">

      {/* ── Текущее состояние ── */}
      <section>
        <h3 className="text-xs font-semibold text-slate-400 uppercase tracking-wider mb-2">Текущее состояние китов</h3>
        {state.length === 0 ? (
          <p className="text-slate-500 text-xs">Нет активных whale-сигналов за последние 4ч</p>
        ) : (
          <div className="space-y-1">
            {state.map(row => (
              <div key={row.symbol} className="flex items-center gap-3 bg-white/[.03] rounded-lg px-3 py-2">
                <span className="font-mono text-slate-200 w-24">{row.symbol}</span>
                <DirectionBadge dir={row.direction} />
                <span className="text-slate-500 text-xs ml-auto">{timeAgo(row.detectedAt)}</span>
                <span className="text-[10px] text-slate-600 uppercase">{row.source}</span>
              </div>
            ))}
          </div>
        )}
      </section>

      {/* ── Симуляция ── */}
      <section className="bg-white/[.03] rounded-xl p-4 space-y-3">
        <h3 className="text-xs font-semibold text-slate-400 uppercase tracking-wider">Симуляция</h3>
        <div className="flex items-center gap-4 flex-wrap">
          <label className="flex items-center gap-2 text-slate-300">
            Порог USDT:
            <input
              type="number"
              className="w-28 bg-white/[.06] border border-white/[.08] rounded px-2 py-1 text-slate-200 text-xs"
              value={threshold}
              onChange={e => setThreshold(Number(e.target.value))}
              min={1000}
              step={1000}
            />
          </label>
          <label className="flex items-center gap-2 text-slate-300">
            Окно (ч):
            <input
              type="number"
              className="w-16 bg-white/[.06] border border-white/[.08] rounded px-2 py-1 text-slate-200 text-xs"
              value={windowH}
              onChange={e => setWindowH(Number(e.target.value))}
              min={1}
              max={168}
            />
          </label>
          <button
            onClick={handleSimulate}
            disabled={simLoading}
            className="px-3 py-1 rounded-lg bg-indigo-500/20 text-indigo-300 border border-indigo-500/30 hover:bg-indigo-500/30 transition-colors text-xs disabled:opacity-50"
          >
            {simLoading ? 'Считаем…' : 'Пересчитать'}
          </button>
        </div>
        {simResult && (
          <div className="space-y-2">
            <p className="text-slate-400 text-xs">Событий: <span className="text-slate-200 font-semibold">{simResult.events_count}</span></p>
            <div className="flex gap-3 flex-wrap">
              {simResult.buckets.map(b => (
                <div key={b.label} className="bg-white/[.04] rounded px-2 py-1 text-xs">
                  <span className="text-slate-400">{b.label}:</span>{' '}
                  <span className="text-slate-200 font-semibold">{b.count}</span>
                </div>
              ))}
            </div>
            {simResult.by_symbol.length > 0 && (
              <div className="flex gap-2 flex-wrap">
                {simResult.by_symbol.map(s => (
                  <span key={s.symbol} className={`text-xs px-2 py-0.5 rounded ${s.direction === 'buy' ? 'bg-emerald-500/15 text-emerald-400' : 'bg-red-500/15 text-red-400'}`}>
                    {s.symbol} {s.direction.toUpperCase()}
                  </span>
                ))}
              </div>
            )}
          </div>
        )}
      </section>

      {/* ── Адреса ── */}
      <section>
        <div className="flex items-center justify-between mb-2">
          <h3 className="text-xs font-semibold text-slate-400 uppercase tracking-wider">Адреса китов</h3>
          <button
            onClick={() => setShowAdd(true)}
            className="text-xs px-2 py-1 rounded bg-white/[.06] text-slate-300 hover:bg-white/[.09] border border-white/[.08]"
          >
            + Добавить
          </button>
        </div>

        {showAdd && (
          <div className="mb-3 bg-white/[.04] rounded-xl p-3 space-y-2 border border-white/[.07]">
            <div className="flex gap-2">
              <input
                placeholder="Адрес"
                className="flex-1 bg-white/[.06] border border-white/[.08] rounded px-2 py-1 text-slate-200 text-xs"
                value={newAddr}
                onChange={e => setNewAddr(e.target.value)}
              />
              <select
                className="bg-white/[.06] border border-white/[.08] rounded px-2 py-1 text-slate-200 text-xs"
                value={newChain}
                onChange={e => setNewChain(e.target.value as 'eth' | 'tron')}
              >
                <option value="eth">ETH</option>
                <option value="tron">Tron</option>
              </select>
            </div>
            <input
              placeholder="Лейбл (опционально)"
              className="w-full bg-white/[.06] border border-white/[.08] rounded px-2 py-1 text-slate-200 text-xs"
              value={newLabel}
              onChange={e => setNewLabel(e.target.value)}
            />
            <div className="flex gap-2">
              <button
                onClick={handleAddAddress}
                disabled={addLoading || !newAddr}
                className="px-3 py-1 rounded bg-indigo-500/20 text-indigo-300 border border-indigo-500/30 text-xs disabled:opacity-50"
              >
                {addLoading ? 'Сохраняем…' : 'Сохранить'}
              </button>
              <button
                onClick={() => { setShowAdd(false); setNewAddr(''); setNewLabel('') }}
                className="px-3 py-1 rounded bg-white/[.04] text-slate-400 text-xs"
              >
                Отмена
              </button>
            </div>
          </div>
        )}

        <div className="space-y-1">
          {addresses.length === 0 && <p className="text-slate-500 text-xs">Адресов нет</p>}
          {addresses.map(addr => (
            <div key={addr.id} className={`flex items-center gap-2 px-3 py-2 rounded-lg ${addr.isActive ? 'bg-white/[.03]' : 'bg-white/[.01] opacity-50'}`}>
              <span className={`w-8 text-[10px] font-semibold uppercase ${addr.chain === 'eth' ? 'text-blue-400' : 'text-purple-400'}`}>{addr.chain}</span>
              <span className="font-mono text-slate-300 text-xs truncate max-w-[140px]" title={addr.address}>{addr.address.slice(0, 8)}…{addr.address.slice(-6)}</span>
              <span className="text-slate-400 text-xs truncate flex-1">{addr.label ?? ''}</span>
              <span className="text-slate-500 text-xs">{fmt(addr.volume30d)}</span>
              {addr.lastSeenAt && <span className="text-slate-600 text-xs">{timeAgo(addr.lastSeenAt)}</span>}
              <span className={`w-2 h-2 rounded-full ${addr.isActive ? 'bg-emerald-500' : 'bg-slate-600'}`} />
              <button onClick={() => handleToggleActive(addr)} className="text-[10px] text-slate-500 hover:text-slate-300 px-1">
                {addr.isActive ? 'Откл' : 'Вкл'}
              </button>
              <button onClick={() => handleDelete(addr)} className="text-[10px] text-slate-600 hover:text-red-400 px-1">✕</button>
            </div>
          ))}
        </div>
      </section>

      {/* ── Лента событий ── */}
      <section>
        <h3 className="text-xs font-semibold text-slate-400 uppercase tracking-wider mb-2">Лента событий</h3>
        {events.length === 0 ? (
          <p className="text-slate-500 text-xs">Событий ещё не было</p>
        ) : (
          <div className="space-y-1">
            {events.map(ev => (
              <div key={ev.id} className="flex items-center gap-2 px-3 py-2 rounded-lg bg-white/[.02] text-xs">
                <span className="text-slate-500 w-20 flex-shrink-0">{timeAgo(ev.detectedAt)}</span>
                <span className="text-slate-300 font-medium w-24 truncate flex-shrink-0">{ev.label || `${ev.address.slice(0, 6)}…`}</span>
                <span className="font-mono text-slate-200 w-20 flex-shrink-0">{ev.symbol}</span>
                <DirectionBadge dir={ev.direction} />
                <span className="text-slate-400 ml-auto">{fmt(ev.amountUsd)}</span>
                <a
                  href={explorerLink(ev.txHash, ev.chain)}
                  target="_blank"
                  rel="noreferrer"
                  className="text-indigo-400 hover:text-indigo-300 text-[10px]"
                >
                  tx↗
                </a>
              </div>
            ))}
          </div>
        )}
      </section>
    </div>
  )
}
