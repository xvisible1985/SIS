import { useState, useEffect, useRef, useCallback } from 'react'
import { createChart, CandlestickSeries, createSeriesMarkers, ColorType } from 'lightweight-charts'
import type { IChartApi } from 'lightweight-charts'
import { popSignalChartIntent } from '../stores/signalChartStore'
import { SIGNALS } from '../features/indicators/signals'
import { CoinPicker } from '../components/common/CoinPicker'
import { ParamNumber, ParamSegmented } from '../features/indicators/components/Params'

const TF_OPTIONS = ['1m', '5m', '15m', '1h', '4h', '1D']

interface ChartEvent {
  time: number
  state: 'buy' | 'sell' | 'neutral'
  price: number
}

function toBybitTF(tf: string): string {
  const m: Record<string, string> = {
    '1m': '1', '5m': '5', '15m': '15', '30m': '30',
    '1h': '60', '4h': '240', '1D': 'D',
  }
  return m[tf] ?? tf
}

export function SignalChartPage() {
  const intent = popSignalChartIntent()

  const [signalId, setSignalId] = useState(intent?.signalId ?? SIGNALS[0]!.id)
  const [symbol,   setSymbol]   = useState(intent?.symbol   ?? 'BTCUSDT')
  const [tf,       setTf]       = useState(intent?.tf       ?? '1h')
  const [params,   setParams]   = useState<Record<string, unknown>>(
    intent?.params ?? (SIGNALS[0]!.defaults as Record<string, unknown>)
  )

  const sig = SIGNALS.find(s => s.id === signalId) ?? SIGNALS[0]!

  useEffect(() => {
    const newSig = SIGNALS.find(s => s.id === signalId) ?? SIGNALS[0]!
    setParams(newSig.defaults as Record<string, unknown>)
  }, [signalId])

  const [events,  setEvents]  = useState<ChartEvent[]>([])
  const [loading, setLoading] = useState(false)
  const [error,   setError]   = useState<string | null>(null)

  const containerRef = useRef<HTMLDivElement>(null)
  const chartRef     = useRef<IChartApi | null>(null)

  const fetchEvents = useCallback(async () => {
    setLoading(true)
    setError(null)
    try {
      const url = new URL('/signals/chart-history', window.location.origin)
      url.searchParams.set('signal',   signalId)
      url.searchParams.set('symbol',   symbol)
      url.searchParams.set('interval', tf)
      url.searchParams.set('limit',    '500')
      url.searchParams.set('params',   JSON.stringify(params))

      const token = localStorage.getItem('token') ?? sessionStorage.getItem('token') ?? ''
      const res = await fetch(url.toString(), {
        headers: token ? { Authorization: `Bearer ${token}` } : undefined,
      })
      if (!res.ok) throw new Error(await res.text())
      const json = await res.json()
      setEvents(json.events ?? [])
    } catch (e: unknown) {
      setError((e as Error).message ?? 'Ошибка загрузки')
    } finally {
      setLoading(false)
    }
  }, [signalId, symbol, tf, params])

  useEffect(() => { fetchEvents() }, [fetchEvents])

  useEffect(() => {
    const el = containerRef.current
    if (!el) return
    let cancelled = false

    async function init() {
      const bybitIv = toBybitTF(tf)
      const res = await fetch(
        `https://api.bybit.com/v5/market/kline?category=linear&symbol=${symbol}&interval=${bybitIv}&limit=500`
      )
      const data = await res.json()
      if (cancelled || !containerRef.current) return

      const list: string[][] = ((data?.result?.list ?? []) as string[][]).slice().reverse()
      const candles = list.map(k => ({
        time: Math.floor(Number(k[0]) / 1000) as unknown as import('lightweight-charts').Time,
        open:  parseFloat(k[1]!),
        high:  parseFloat(k[2]!),
        low:   parseFloat(k[3]!),
        close: parseFloat(k[4]!),
      }))

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
        upColor:        '#00DC82',
        downColor:      '#ef4444',
        borderUpColor:  '#00DC82',
        borderDownColor:'#ef4444',
        wickUpColor:    '#00DC82',
        wickDownColor:  '#ef4444',
      })
      series.setData(candles)

      if (events.length > 0) {
        const firstTime = Number(candles[0]?.time ?? 0)
        const lastTime  = Number(candles[candles.length - 1]?.time ?? Infinity)
        const markers = events
          .map(ev => ({ ...ev, timeSec: Math.floor(ev.time / 1000) }))
          .filter(ev => ev.state !== 'neutral' && ev.timeSec >= firstTime && ev.timeSec <= lastTime)
          .sort((a, b) => a.timeSec - b.timeSec)
          .map(ev => ({
            time: ev.timeSec as unknown as import('lightweight-charts').Time,
            position: ev.state === 'buy' ? 'belowBar' as const : 'aboveBar' as const,
            color:    ev.state === 'buy' ? '#00DC82' : '#ef4444',
            shape:    ev.state === 'buy' ? 'arrowUp'  as const : 'arrowDown' as const,
            text:     ev.state === 'buy' ? 'Long' : 'Short',
            size: 1,
          }))
        if (markers.length) createSeriesMarkers(series, markers)
      }

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
      chartRef.current = null
    }
  }, [symbol, tf, events])

  const setParam = (key: string, val: unknown) => setParams(p => ({ ...p, [key]: val }))

  const buyEvents  = events.filter(e => e.state === 'buy')
  const sellEvents = events.filter(e => e.state === 'sell')

  return (
    <div className="h-full flex flex-col bg-[#0a0d14] text-white font-sans">
      {/* Top bar */}
      <div className="flex items-center gap-3 px-4 py-2 border-b border-white/[.07] bg-[#0d1117] flex-wrap flex-shrink-0">
        <select
          value={signalId}
          onChange={e => setSignalId(e.target.value)}
          className="bg-[#161b27] border border-white/[.10] rounded-[7px] text-[12px] text-[#e6ebf5] px-2 py-1.5 outline-none cursor-pointer"
        >
          {SIGNALS.map(s => (
            <option key={s.id} value={s.id}>{s.name}</option>
          ))}
        </select>

        <div className="flex items-center gap-px bg-[#161b27] border border-white/[.10] rounded-[7px] p-px">
          {TF_OPTIONS.map(t => (
            <button
              key={t}
              onClick={() => setTf(t)}
              className={`px-2.5 py-1 text-[11px] font-semibold rounded-[5px] transition-colors ${
                tf === t ? 'bg-[#5b8cff] text-white' : 'text-slate-400 hover:text-white'
              }`}
            >
              {t}
            </button>
          ))}
        </div>

        <CoinPicker value={symbol} onChange={setSymbol} size="sm" />

        <div className="w-px h-5 bg-white/[.08]" />

        {sig.params.map(p => {
          if (p.kind === 'number') {
            return (
              <div key={p.key} className="flex items-center gap-1.5">
                <span className="text-[10px] text-slate-500 uppercase tracking-[.6px]">{p.label}</span>
                <ParamNumber
                  label=""
                  value={(params[p.key] as number) ?? (sig.defaults as Record<string, unknown>)[p.key] as number}
                  step={p.step}
                  min={p.min}
                  max={p.max}
                  suffix={p.suffix}
                  decimals={p.decimals}
                  onChange={v => setParam(p.key, v)}
                />
              </div>
            )
          }
          return (
            <div key={p.key} className="flex items-center gap-1.5">
              <span className="text-[10px] text-slate-500 uppercase tracking-[.6px]">{p.label}</span>
              <ParamSegmented
                label=""
                value={(params[p.key] as string) ?? (sig.defaults as Record<string, unknown>)[p.key] as string}
                options={p.options}
                onChange={v => setParam(p.key, v)}
              />
            </div>
          )
        })}

        {loading && <span className="text-[11px] text-slate-500 ml-auto animate-pulse">Загрузка…</span>}
        {error   && <span className="text-[11px] text-rose-400 ml-auto">{error}</span>}
      </div>

      {/* Body */}
      <div className="flex flex-1 min-h-0">
        <div ref={containerRef} className="flex-1 min-w-0" />

        {/* Right panel */}
        <div className="w-[280px] flex-shrink-0 flex flex-col border-l border-white/[.07] bg-[#0d1117]">
          <div className="px-3 py-2.5 border-b border-white/[.07] flex items-center justify-between">
            <span className="text-[11px] font-semibold uppercase tracking-[1px] text-slate-400">История</span>
            <div className="flex items-center gap-2 text-[10px]">
              <span className="text-emerald-400 font-semibold">{buyEvents.length} Long</span>
              <span className="text-rose-400 font-semibold">{sellEvents.length} Short</span>
            </div>
          </div>
          <div className="flex-1 overflow-y-auto" style={{ scrollbarWidth: 'thin', scrollbarColor: 'rgba(255,255,255,.1) transparent' }}>
            {events.filter(e => e.state !== 'neutral').length === 0 && !loading && (
              <div className="py-8 text-center text-[12px] text-slate-600">Нет событий</div>
            )}
            {[...events].reverse().filter(e => e.state !== 'neutral').map((ev, i) => {
              const d = new Date(ev.time)
              const isLong = ev.state === 'buy'
              return (
                <div
                  key={i}
                  className="flex items-center gap-2.5 px-3 py-2 border-b border-white/[.04] hover:bg-white/[.03] transition-colors"
                >
                  <span
                    className={`text-[9px] font-bold uppercase px-1.5 py-0.5 rounded-[4px] leading-none ${
                      isLong ? 'bg-emerald-400/[.15] text-emerald-300' : 'bg-rose-400/[.15] text-rose-300'
                    }`}
                  >
                    {isLong ? 'Long' : 'Short'}
                  </span>
                  <div className="flex-1 min-w-0">
                    <div className="text-[11px] font-mono text-[#e6ebf5]">{ev.price.toFixed(2)}</div>
                    <div className="text-[10px] text-slate-500">
                      {d.toLocaleDateString('ru-RU', { day: '2-digit', month: '2-digit' })}
                      {' '}
                      {d.toLocaleTimeString('ru-RU', { hour: '2-digit', minute: '2-digit' })}
                    </div>
                  </div>
                </div>
              )
            })}
          </div>
        </div>
      </div>
    </div>
  )
}
