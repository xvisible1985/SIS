import { useEffect, useRef, useState, useCallback } from 'react'
import { createChart, CandlestickSeries, ColorType } from 'lightweight-charts'
import type { IChartApi } from 'lightweight-charts'
import { SIGNALS } from '../indicators/signals'
import { useEnabledSignals } from '../indicators/useEnabledSignals'
import { SignalPickCard } from '../indicators/components/SignalPickCard'
import {
  toBybitTF, loadSignalHistory, SignalLabelsPrimitive,
  type ChartEvent, type SignalLabel,
} from '../indicators/signalChartShared'

// Same exclusion as WebhooksPage's alert catalog — bybit-news has no pkg/signal registry
// entry (fundamental trigger, not candle-based), so /signals/chart-history can't compute it.
const PREVIEWABLE_SIGNALS = SIGNALS.filter(s => s.id !== 'bybit-news')

const TF_OPTIONS = ['1m', '5m', '15m', '30m', '1h', '4h', '1D']
const COINS = [
  'BTCUSDT', 'ETHUSDT', 'BNBUSDT', 'SOLUSDT', 'XRPUSDT',
  'ADAUSDT', 'DOGEUSDT', 'AVAXUSDT', 'DOTUSDT', 'MATICUSDT',
]

type CacheEntry =
  | { status: 'loading' }
  | { status: 'done'; events: ChartEvent[] }
  | { status: 'error'; msg: string }

// TradingView-style preview: a price chart with a searchable signal catalog below it.
// Clicking a card plots that signal's buy/sell fire history as arrows on the chart.
// Collapsed by default — this is a preview on top of the alert constructor above, not
// the primary flow, and it avoids firing a Bybit candle fetch on every page load.
export function SignalPreviewChart() {
  const [open, setOpen] = useState(false)

  const enabledIds = useEnabledSignals()
  const catalog = enabledIds ? PREVIEWABLE_SIGNALS.filter(s => enabledIds.has(s.id)) : PREVIEWABLE_SIGNALS

  const [symbol, setSymbol] = useState('BTCUSDT')
  const [tf, setTf]         = useState('15m')
  const [query, setQuery]   = useState('')
  const [activeId, setActiveId] = useState<string | null>(null)
  const [cache, setCache]       = useState<Record<string, CacheEntry>>({})

  const pendingRef    = useRef<Set<string>>(new Set())
  const generationRef = useRef(0)

  // Clear cache when symbol/tf changes — stale events from a different pair must not
  // linger and get plotted against the new candle set.
  useEffect(() => {
    generationRef.current++
    pendingRef.current.clear()
    setCache({})
  }, [symbol, tf])

  const fetchEvents = useCallback(async (signalId: string) => {
    if (pendingRef.current.has(signalId)) return
    const sig = SIGNALS.find(s => s.id === signalId)
    if (!sig) return
    pendingRef.current.add(signalId)
    setCache(c => ({ ...c, [signalId]: { status: 'loading' } }))
    const gen = generationRef.current
    try {
      const events = await loadSignalHistory(signalId, symbol, tf, sig.defaults as Record<string, unknown>)
      if (generationRef.current === gen) setCache(c => ({ ...c, [signalId]: { status: 'done', events } }))
    } catch (e: unknown) {
      if (generationRef.current === gen)
        setCache(c => ({ ...c, [signalId]: { status: 'error', msg: e instanceof Error ? e.message : 'Ошибка' } }))
    } finally {
      pendingRef.current.delete(signalId)
    }
  }, [symbol, tf])

  useEffect(() => {
    if (!activeId || cache[activeId]) return
    fetchEvents(activeId)
  }, [activeId, cache, fetchEvents])

  function toggleSignal(id: string) {
    setActiveId(prev => prev === id ? null : id)
  }

  const activeEvents: ChartEvent[] =
    activeId && cache[activeId]?.status === 'done'
      ? (cache[activeId] as { status: 'done'; events: ChartEvent[] }).events
      : []

  // ── chart ────────────────────────────────────────────────────────────────
  const containerRef    = useRef<HTMLDivElement>(null)
  const chartRef        = useRef<IChartApi | null>(null)
  const labelsPluginRef = useRef<SignalLabelsPrimitive | null>(null)
  const candlesMapRef   = useRef<Map<number, { low: number; high: number }>>(new Map())
  const activeEventsRef = useRef<ChartEvent[]>([])
  activeEventsRef.current = activeEvents

  function applyLabels() {
    const plugin = labelsPluginRef.current
    if (!plugin) return
    const labels: SignalLabel[] = activeEventsRef.current
      .filter(ev => ev.state !== 'neutral')
      .map(ev => {
        const timeSec = Math.floor(ev.time / 1000)
        const candle  = candlesMapRef.current.get(timeSec)
        return {
          time:  timeSec,
          price: candle ? (ev.state === 'buy' ? candle.low : candle.high) : ev.price,
          type:  ev.state as 'buy' | 'sell',
        }
      })
      .filter(l => candlesMapRef.current.has(l.time))
    plugin.setLabels(labels)
  }

  // Create/recreate the chart when opened or when symbol/tf changes. Skipped while
  // collapsed — no point fetching candles for a panel the user hasn't opened.
  useEffect(() => {
    if (!open) return
    const el = containerRef.current
    if (!el) return
    let cancelled = false

    async function init() {
      const bybitIv = toBybitTF(tf)
      const res  = await fetch(
        `https://api.bybit.com/v5/market/kline?category=linear&symbol=${symbol}&interval=${bybitIv}&limit=500`
      )
      const data = await res.json()
      if (cancelled || !containerRef.current) return

      const list = ((data?.result?.list ?? []) as string[][]).slice().reverse()
      const candles = list.map(k => ({
        time:  Math.floor(Number(k[0]) / 1000) as unknown as import('lightweight-charts').Time,
        open:  parseFloat(k[1]!),
        high:  parseFloat(k[2]!),
        low:   parseFloat(k[3]!),
        close: parseFloat(k[4]!),
      }))

      const cm = new Map<number, { low: number; high: number }>()
      candles.forEach(c => cm.set(Number(c.time), { low: c.low, high: c.high }))
      candlesMapRef.current = cm

      chartRef.current?.remove()
      chartRef.current = null
      const chartEl = containerRef.current
      if (!chartEl) return

      const chart = createChart(chartEl, {
        width:  chartEl.clientWidth,
        height: chartEl.clientHeight,
        layout: {
          background: { type: ColorType.Solid, color: '#0a0d14' },
          textColor: '#9ca3af',
        },
        grid: {
          vertLines: { color: '#131722' },
          horzLines: { color: '#131722' },
        },
        rightPriceScale: { borderColor: '#1e2535' },
        timeScale: { borderColor: '#1e2535', timeVisible: true },
        crosshair: { mode: 1 },
      })
      chartRef.current = chart

      const series = chart.addSeries(CandlestickSeries, {
        upColor:         '#00DC82',
        downColor:       '#ef4444',
        borderUpColor:   '#00DC82',
        borderDownColor: '#ef4444',
        wickUpColor:     '#00DC82',
        wickDownColor:   '#ef4444',
      })
      series.setData(candles)

      const plugin = new SignalLabelsPrimitive()
      series.attachPrimitive(plugin)
      labelsPluginRef.current = plugin

      applyLabels()
      chart.timeScale().fitContent()
    }

    init().catch(console.error)

    const onResize = () => {
      if (containerRef.current && chartRef.current) {
        chartRef.current.applyOptions({
          width:  containerRef.current.clientWidth,
          height: containerRef.current.clientHeight,
        })
      }
    }
    window.addEventListener('resize', onResize)

    return () => {
      cancelled = true
      window.removeEventListener('resize', onResize)
      chartRef.current?.remove()
      chartRef.current        = null
      labelsPluginRef.current = null
    }
  }, [open, symbol, tf]) // eslint-disable-line react-hooks/exhaustive-deps

  useEffect(() => { applyLabels() }, [activeEvents]) // eslint-disable-line react-hooks/exhaustive-deps

  const filtered = query.trim()
    ? catalog.filter(s =>
        s.name.toLowerCase().includes(query.toLowerCase()) ||
        s.abbr.toLowerCase().includes(query.toLowerCase())
      )
    : catalog

  return (
    <div className="flex flex-col overflow-hidden rounded-xl border border-white/[.07] bg-[#0d1018]">
      <button
        type="button"
        onClick={() => setOpen(v => !v)}
        className="flex w-full items-center gap-2 border-b border-white/[.06] bg-sky-500/[.07] px-4 py-2.5 text-left"
      >
        <span className="text-xs font-semibold uppercase tracking-wider text-sky-300/70">
          Просмотр на графике
        </span>
        <span className="text-[10px] text-slate-500">свечи + точки срабатывания сигнала</span>
        <svg
          width={11} height={11} viewBox="0 0 24 24" fill="none" stroke="#5b6479" strokeWidth={2.5}
          strokeLinecap="round" strokeLinejoin="round"
          className="ml-auto transition-transform"
          style={{ transform: open ? 'rotate(180deg)' : 'none' }}
        >
          <path d="M6 9l6 6 6-6" />
        </svg>
      </button>

      {open && (
        <>
          {/* toolbar */}
          <div className="flex flex-shrink-0 items-center gap-2 border-b border-white/[.06] px-4 py-2">
            <input
              list="preview-coins"
              value={symbol}
              onChange={e => setSymbol(e.target.value.toUpperCase())}
              className="w-28 rounded-lg border border-white/[.08] bg-black/25 px-2.5 py-1 text-[12px] text-slate-200 outline-none focus:border-[#5b8cff]/50"
            />
            <datalist id="preview-coins">
              {COINS.map(c => <option key={c} value={c} />)}
            </datalist>
            <div className="flex items-center gap-px rounded-lg border border-white/[.08] bg-black/25 p-px">
              {TF_OPTIONS.map(t => (
                <button
                  key={t}
                  type="button"
                  onClick={() => setTf(t)}
                  className={`rounded-[5px] px-2 py-1 text-[11px] font-semibold transition-colors ${
                    tf === t ? 'bg-[#5b8cff] text-white' : 'text-slate-400 hover:text-white'
                  }`}
                >
                  {t}
                </button>
              ))}
            </div>
            {activeId && (
              <span className="ml-auto text-[11px] text-slate-500">
                {SIGNALS.find(s => s.id === activeId)?.name}
                {cache[activeId]?.status === 'loading' && ' — загрузка…'}
                {cache[activeId]?.status === 'error' && (
                  <span className="text-rose-400"> — {(cache[activeId] as { status: 'error'; msg: string }).msg}</span>
                )}
              </span>
            )}
          </div>

          {/* chart */}
          <div className="h-[360px] flex-shrink-0 p-2.5">
            <div ref={containerRef} className="h-full w-full overflow-hidden rounded-[10px] border border-white/[.07]" />
          </div>

          {/* search */}
          <div className="flex-shrink-0 border-t border-white/[.06] px-4 py-2.5">
            <input
              value={query}
              onChange={e => setQuery(e.target.value)}
              placeholder="Поиск сигнала…"
              className="w-full rounded-lg border border-white/[.08] bg-black/25 px-3 py-1.5 text-[12px] text-slate-200 outline-none placeholder:text-slate-600 focus:border-[#5b8cff]/50"
            />
          </div>

          {/* card grid */}
          <div className="max-h-[280px] overflow-auto p-3">
            {filtered.length === 0 ? (
              <p className="py-6 text-center text-[12px] text-slate-500">Ничего не найдено</p>
            ) : (
              <div className="grid grid-cols-[repeat(auto-fill,minmax(200px,1fr))] gap-2.5">
                {filtered.map(def => (
                  <SignalPickCard
                    key={def.id}
                    def={def}
                    selected={activeId === def.id}
                    onClick={() => toggleSignal(def.id)}
                  />
                ))}
              </div>
            )}
          </div>
        </>
      )}
    </div>
  )
}
