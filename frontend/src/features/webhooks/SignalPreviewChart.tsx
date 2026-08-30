import { useEffect, useRef, useState, useCallback } from 'react'
import { createChart, CandlestickSeries, ColorType } from 'lightweight-charts'
import type { IChartApi } from 'lightweight-charts'
import { SIGNALS } from '../indicators/signals'
import {
  toBybitTF, loadSignalHistory, SignalLabelsPrimitive,
  type ChartEvent, type SignalLabel,
} from '../indicators/signalChartShared'
import type { SignalDef } from '../indicators/types'

type CacheEntry =
  | { status: 'loading' }
  | { status: 'done'; events: ChartEvent[] }
  | { status: 'error'; msg: string }

// Combo mode: the parent (WebhooksPage) already fetched combined AND-logic events via
// /signals/combo-preview (multiple legs) — pass them in directly instead of this component
// doing its own single-signal fetch/cache.
interface ComboPreview {
  events: ChartEvent[]
  loading: boolean
  error: string | null
}

interface Props {
  symbol: string
  tf: string
  activeSignal: SignalDef | null
  combo?: ComboPreview | null
}

// TradingView-style preview chart: candles for `symbol`/`tf`, with either a single
// `activeSignal`'s fire history (fetched here, from /signals/chart-history) or a
// parent-supplied `combo` preview plotted as buy/sell arrows. Symbol/timeframe and the
// selected signal are owned by the parent — shared with the alert constructor, same as the
// terminal shares one `symbol` between its chart and side panel.
export function SignalPreviewChart({ symbol, tf, activeSignal, combo }: Props) {
  const [cache, setCache] = useState<Record<string, CacheEntry>>({})
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
    if (combo || !activeSignal || cache[activeSignal.id]) return
    fetchEvents(activeSignal.id)
  }, [combo, activeSignal, cache, fetchEvents])

  const activeEntry = activeSignal ? cache[activeSignal.id] : undefined
  const activeEvents: ChartEvent[] = combo
    ? combo.events
    : activeEntry?.status === 'done' ? activeEntry.events : []

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

  useEffect(() => {
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
  }, [symbol, tf]) // eslint-disable-line react-hooks/exhaustive-deps

  useEffect(() => { applyLabels() }, [activeEvents]) // eslint-disable-line react-hooks/exhaustive-deps

  return (
    <div className="relative h-full w-full">
      <div ref={containerRef} className="h-full w-full" />
      {combo?.loading && (
        <div className="absolute left-2 top-2 rounded-md bg-black/50 px-2 py-1 text-[10px] text-slate-400">
          Загрузка комбо…
        </div>
      )}
      {combo?.error && (
        <div className="absolute left-2 top-2 rounded-md bg-rose-500/10 px-2 py-1 text-[10px] text-rose-400">
          {combo.error}
        </div>
      )}
      {!combo && activeSignal && activeEntry?.status === 'loading' && (
        <div className="absolute left-2 top-2 rounded-md bg-black/50 px-2 py-1 text-[10px] text-slate-400">
          Загрузка сигнала…
        </div>
      )}
      {!combo && activeSignal && activeEntry?.status === 'error' && (
        <div className="absolute left-2 top-2 rounded-md bg-rose-500/10 px-2 py-1 text-[10px] text-rose-400">
          {activeEntry.msg}
        </div>
      )}
    </div>
  )
}
