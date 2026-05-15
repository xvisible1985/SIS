import { useState, useEffect, useRef, useCallback } from 'react'
import { createChart, CandlestickSeries, ColorType } from 'lightweight-charts'
import type { IChartApi } from 'lightweight-charts'
import { popSignalChartIntent } from '../stores/signalChartStore'
import { SIGNALS } from '../features/indicators/signals'
import { CoinPicker } from '../components/common/CoinPicker'

const TF_OPTIONS = ['1m', '5m', '15m', '1h', '4h', '1D']

interface ChartEvent {
  time: number
  state: 'buy' | 'sell' | 'neutral'
  price: number
}

type CacheEntry =
  | { status: 'loading' }
  | { status: 'done'; events: ChartEvent[] }
  | { status: 'error'; msg: string }

function toBybitTF(tf: string): string {
  const m: Record<string, string> = {
    '1m': '1', '5m': '5', '15m': '15', '30m': '30',
    '1h': '60', '4h': '240', '1D': 'D',
  }
  return m[tf] ?? tf
}

async function loadSignalHistory(
  signalId: string, symbol: string, tf: string, defaults: Record<string, unknown>
): Promise<ChartEvent[]> {
  const url = new URL('/signals/chart-history', window.location.origin)
  url.searchParams.set('signal',   signalId)
  url.searchParams.set('symbol',   symbol)
  url.searchParams.set('interval', tf)
  url.searchParams.set('limit',    '500')
  url.searchParams.set('params',   JSON.stringify(defaults))
  const token = localStorage.getItem('token') ?? sessionStorage.getItem('token') ?? ''
  const res = await fetch(url.toString(), {
    headers: token ? { Authorization: `Bearer ${token}` } : undefined,
  })
  if (!res.ok) throw new Error(await res.text())
  const json = await res.json()
  return json.events ?? []
}

// ── TradingView-style Buy/Sell label primitive ─────────────────────────────

interface SignalLabel {
  time: number   // unix seconds
  price: number  // anchor price (candle low for buy, high for sell)
  type: 'buy' | 'sell'
}

function rrect(
  ctx: CanvasRenderingContext2D,
  x: number, y: number, w: number, h: number, r: number
) {
  ctx.beginPath()
  ctx.moveTo(x + r, y)
  ctx.lineTo(x + w - r, y)
  ctx.quadraticCurveTo(x + w, y,     x + w, y + r)
  ctx.lineTo(x + w, y + h - r)
  ctx.quadraticCurveTo(x + w, y + h, x + w - r, y + h)
  ctx.lineTo(x + r, y + h)
  ctx.quadraticCurveTo(x, y + h,     x, y + h - r)
  ctx.lineTo(x, y + r)
  ctx.quadraticCurveTo(x, y,         x + r, y)
  ctx.closePath()
}

class SignalLabelsPrimitive {
  private _series: any  = null
  private _chart: any   = null
  private _reqUpdate: (() => void) | null = null
  private _labels: SignalLabel[] = []

  attached({ series, chart, requestUpdate }: any) {
    this._series    = series
    this._chart     = chart
    this._reqUpdate = requestUpdate
  }
  detached() {
    this._series = null
    this._chart  = null
  }
  setLabels(labels: SignalLabel[]) {
    this._labels = labels
    this._reqUpdate?.()
  }
  updateAllViews() {}

  paneViews() {
    const self = this
    return [{
      zOrder: () => 'top' as const,
      renderer: () => ({
        draw(target: any) {
          target.useBitmapCoordinateSpace((scope: any) => {
            const ctx  = scope.context as CanvasRenderingContext2D
            const pr   = scope.horizontalPixelRatio
            const vr   = scope.verticalPixelRatio

            for (const label of self._labels) {
              const x = self._chart?.timeScale().timeToCoordinate(label.time)
              const y = self._series?.priceToCoordinate(label.price)
              if (x == null || y == null) continue

              const isBuy    = label.type === 'buy'
              const text     = isBuy ? 'Buy' : 'Sell'
              const bgColor  = isBuy ? '#089981' : '#F23645'
              // offset from anchor: buy labels below, sell labels above
              const offsetPx = isBuy ? 22 : -22
              const px = x * pr
              const py = (y + offsetPx) * vr

              const fontSize = Math.round(11 * Math.min(pr, vr))
              ctx.font = `bold ${fontSize}px -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif`
              const textW = ctx.measureText(text).width
              const padX  = 7 * pr
              const padY  = 3.5 * vr
              const rectW = textW + padX * 2
              const rectH = fontSize + padY * 2
              const rad   = 3 * Math.min(pr, vr)

              // shadow
              ctx.shadowColor   = isBuy ? 'rgba(8,153,129,.55)' : 'rgba(242,54,69,.55)'
              ctx.shadowBlur    = 6 * Math.min(pr, vr)
              ctx.shadowOffsetX = 0
              ctx.shadowOffsetY = 0

              // rectangle
              ctx.fillStyle = bgColor
              rrect(ctx, px - rectW / 2, py - rectH / 2, rectW, rectH, rad)
              ctx.fill()

              // triangle pointer
              ctx.shadowBlur = 0
              const triH = 5 * vr
              const triW = 4 * pr
              ctx.beginPath()
              if (isBuy) {
                // points up toward the candle
                ctx.moveTo(px,          py - rectH / 2 - triH)
                ctx.lineTo(px - triW,   py - rectH / 2)
                ctx.lineTo(px + triW,   py - rectH / 2)
              } else {
                // points down toward the candle
                ctx.moveTo(px,          py + rectH / 2 + triH)
                ctx.lineTo(px - triW,   py + rectH / 2)
                ctx.lineTo(px + triW,   py + rectH / 2)
              }
              ctx.closePath()
              ctx.fillStyle = bgColor
              ctx.fill()

              // text
              ctx.fillStyle     = '#ffffff'
              ctx.textAlign     = 'center'
              ctx.textBaseline  = 'middle'
              ctx.fillText(text, px, py)
            }
          })
        },
      }),
    }]
  }
}

// ── Component ──────────────────────────────────────────────────────────────

export function SignalChartPage() {
  const intent = popSignalChartIntent()

  const [symbol,     setSymbol]     = useState(intent?.symbol ?? 'BTCUSDT')
  const [tf,         setTf]         = useState(intent?.tf ?? '1h')
  const [expandedId, setExpandedId] = useState<string | null>(intent?.signalId ?? null)
  const [cache,      setCache]      = useState<Record<string, CacheEntry>>({})

  const pendingRef    = useRef<Set<string>>(new Set())
  const generationRef = useRef(0)

  // Clear cache when symbol/tf changes
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
      const events = await loadSignalHistory(
        signalId, symbol, tf, sig.defaults as Record<string, unknown>
      )
      if (generationRef.current === gen)
        setCache(c => ({ ...c, [signalId]: { status: 'done', events } }))
    } catch (e: unknown) {
      if (generationRef.current === gen)
        setCache(c => ({ ...c, [signalId]: { status: 'error', msg: (e as Error).message ?? 'Ошибка' } }))
    } finally {
      pendingRef.current.delete(signalId)
    }
  }, [symbol, tf])

  // Auto-fetch when expanded card has no cache entry
  useEffect(() => {
    if (!expandedId || cache[expandedId]) return
    fetchEvents(expandedId)
  }, [expandedId, cache, fetchEvents])

  function toggleCard(signalId: string) {
    setExpandedId(prev => prev === signalId ? null : signalId)
  }

  const activeEvents: ChartEvent[] =
    expandedId && cache[expandedId]?.status === 'done'
      ? (cache[expandedId] as { status: 'done'; events: ChartEvent[] }).events
      : []

  // ── Chart refs ─────────────────────────────────────────────────────────────
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

  // Effect 1: create chart when symbol/tf changes
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

      // Build candle map for label anchoring
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
      chartRef.current      = null
      labelsPluginRef.current = null
    }
  }, [symbol, tf])

  // Effect 2: update labels when active signal changes (no chart recreation)
  useEffect(() => {
    applyLabels()
  }, [activeEvents]) // eslint-disable-line react-hooks/exhaustive-deps

  // ── Render ─────────────────────────────────────────────────────────────────
  return (
    <div className="h-full flex flex-col bg-[#0a0d14] text-white font-sans">

      {/* Top bar */}
      <div className="flex items-center gap-3 px-4 py-2 border-b border-white/[.07] bg-[#0d1117] flex-shrink-0">
        <CoinPicker value={symbol} onChange={setSymbol} size="sm" />
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
      </div>

      {/* Body */}
      <div className="flex flex-1 min-h-0">

        {/* Chart area */}
        <div className="flex-1 min-w-0 p-2.5">
          <div
            ref={containerRef}
            className="h-full rounded-[12px] overflow-hidden border border-white/[.07]"
          />
        </div>

        {/* Right sidebar — all signals as accordion cards */}
        <div className="w-[340px] flex-shrink-0 flex flex-col border-l border-white/[.07] bg-[#0d1117]">
          <div className="px-3 py-2.5 border-b border-white/[.07] flex-shrink-0">
            <span className="text-[11px] font-semibold uppercase tracking-[1px] text-slate-400">Сигналы</span>
          </div>

          <div
            className="flex-1 overflow-y-auto"
            style={{ scrollbarWidth: 'thin', scrollbarColor: 'rgba(255,255,255,.1) transparent' }}
          >
            {SIGNALS.map(sig => {
              const isExpanded = expandedId === sig.id
              const entry      = cache[sig.id]
              const isLoading  = entry?.status === 'loading'
              const isError    = entry?.status === 'error'
              const events     = entry?.status === 'done' ? entry.events : []
              const buyCount   = events.filter(e => e.state === 'buy').length
              const sellCount  = events.filter(e => e.state === 'sell').length

              return (
                <div
                  key={sig.id}
                  className={`border-b border-white/[.04] transition-colors ${isExpanded ? 'bg-white/[.02]' : ''}`}
                >
                  <button
                    type="button"
                    onClick={() => toggleCard(sig.id)}
                    className="w-full flex items-center gap-2 px-3 py-2.5 hover:bg-white/[.03] transition-colors text-left"
                  >
                    <span className="flex-1 min-w-0 text-[12px] font-semibold text-[#e6ebf5] truncate">
                      {sig.name}
                    </span>
                    {entry?.status === 'done' && (
                      <span className="flex items-center gap-1 text-[10px] font-semibold shrink-0">
                        <span className="text-emerald-400">{buyCount}B</span>
                        <span className="text-slate-600">/</span>
                        <span className="text-rose-400">{sellCount}S</span>
                      </span>
                    )}
                    {isLoading && (
                      <span className="text-[10px] text-slate-600 animate-pulse shrink-0">…</span>
                    )}
                    <svg
                      width={10} height={10} viewBox="0 0 24 24"
                      fill="none" stroke="#5b6479" strokeWidth={2.5}
                      strokeLinecap="round" strokeLinejoin="round"
                      style={{
                        flexShrink: 0,
                        transition: 'transform .15s',
                        transform: isExpanded ? 'rotate(180deg)' : 'none',
                      }}
                    >
                      <path d="M6 9l6 6 6-6" />
                    </svg>
                  </button>

                  {isExpanded && (
                    <div className="border-t border-white/[.04]">
                      {isLoading && (
                        <div className="py-4 text-center text-[11px] text-slate-600 animate-pulse">
                          Загрузка…
                        </div>
                      )}
                      {isError && (
                        <div className="px-3 py-3 text-[11px] text-rose-400">
                          {(entry as { status: 'error'; msg: string }).msg}
                        </div>
                      )}
                      {!isLoading && !isError && events.filter(e => e.state !== 'neutral').length === 0 && (
                        <div className="py-4 text-center text-[11px] text-slate-600">Нет событий</div>
                      )}
                      {!isLoading && !isError && (
                        [...events].reverse().filter(e => e.state !== 'neutral').map((ev, i) => {
                          const d     = new Date(ev.time)
                          const isBuy = ev.state === 'buy'
                          return (
                            <div
                              key={i}
                              className="flex items-center gap-2 px-3 py-1.5 border-b border-white/[.03] hover:bg-white/[.02] transition-colors"
                            >
                              <span
                                className={`text-[9px] font-bold uppercase px-1.5 py-0.5 rounded-[4px] leading-none shrink-0 ${
                                  isBuy
                                    ? 'bg-emerald-400/[.15] text-emerald-300'
                                    : 'bg-rose-400/[.15] text-rose-300'
                                }`}
                              >
                                {isBuy ? 'Buy' : 'Sell'}
                              </span>
                              <span className="text-[11px] font-mono text-[#e6ebf5]">
                                {ev.price.toFixed(2)}
                              </span>
                              <span className="ml-auto text-[10px] text-slate-500 shrink-0">
                                {d.toLocaleDateString('ru-RU', { day: '2-digit', month: '2-digit' })}
                                {' '}
                                {d.toLocaleTimeString('ru-RU', { hour: '2-digit', minute: '2-digit' })}
                              </span>
                            </div>
                          )
                        })
                      )}
                    </div>
                  )}
                </div>
              )
            })}
          </div>
        </div>

      </div>
    </div>
  )
}
