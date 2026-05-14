import { useState, useRef, useCallback, useEffect } from 'react'
import { useMarketIndicators } from '../hooks/useMarketIndicators'
import { SignalChartModal } from '../components/signals/SignalChartModal'
import type { IndicatorMarker } from '../hooks/useMarketIndicators'
import { IndicatorCard } from '../features/indicators/components/IndicatorCard'
import { SignalCard }    from '../features/indicators/components/SignalCard'
import { FlyingCardOverlay } from '../features/indicators/components/FlyingCardOverlay'
import { INDICATORS }   from '../features/indicators/indicators'
import { SIGNALS }      from '../features/indicators/signals'
import type { IndicatorDef, BaseParams, SignalDef } from '../features/indicators/types'
import { X, Wrench } from 'lucide-react'
import { apiClient } from '../api/client'
import { useAuth } from '../hooks/useAuth'

interface EnabledItem { id: string; panel: string }

function useEnabledContent(userEndpoint: string, adminEndpoint: string) {
  const [items, setItems] = useState<EnabledItem[] | null>(null)
  useEffect(() => {
    // Always try admin endpoint first; if 403 (not admin) fall back to user endpoint.
    // This ensures correct data regardless of stale localStorage isAdmin flag.
    apiClient.get<{ id: string; status: string; panel: string }[]>(adminEndpoint)
      .then(res => setItems(res.data.filter(x => x.status !== 'disabled')))
      .catch(() =>
        apiClient.get<EnabledItem[]>(userEndpoint)
          .then(res => setItems(res.data))
          .catch(() => setItems(null))
      )
  }, [userEndpoint, adminEndpoint])
  return items
}

function WipPlaceholder({ title }: { title: string }) {
  return (
    <div className="flex h-full flex-col items-center justify-center gap-3 text-center p-6">
      <Wrench size={28} className="text-slate-600" />
      <div>
        <div className="text-[13px] font-semibold text-slate-400">{title}</div>
        <div className="mt-1 text-[11px] text-slate-600">В разработке</div>
      </div>
    </div>
  )
}

const COINS = [
  'BTCUSDT', 'ETHUSDT', 'BNBUSDT', 'SOLUSDT', 'XRPUSDT',
  'ADAUSDT', 'DOGEUSDT', 'AVAXUSDT', 'DOTUSDT', 'MATICUSDT',
  'LTCUSDT', 'LINKUSDT', 'TRXUSDT', 'TONUSDT', 'SUIUSDT',
]

const TIMEFRAMES = [
  { label: '1м', value: '1m' }, { label: '5м', value: '5m' },
  { label: '15м', value: '15m' }, { label: '30м', value: '30m' },
  { label: '1ч', value: '1h' }, { label: '4ч', value: '4h' },
  { label: '1д', value: '1d' },
]

const MA_INDICATORS = new Set(['EMA', 'SMA', 'MACD', 'BB', 'Ichimoku', 'Supertrend'])

interface ChartModal {
  title: string
  badgeColor: 'green' | 'red' | 'neutral'
  badgeLabel: string
  markers: IndicatorMarker[]
  showMA: boolean
}

interface ConstructorItem { type: 'indicator' | 'signal'; id: string }

interface FlyingCard {
  item: { type: 'indicator'; def: IndicatorDef<BaseParams> } | { type: 'signal'; def: SignalDef<Record<string, unknown>> };
  sourceRect: DOMRect;
}

const DRAG_KEY = 'application/x-sis-item'

/** Vertical resizer (splits columns) */
function VSplit({ onMouseDown }: { onMouseDown: (e: React.MouseEvent) => void }) {
  return (
    <div
      onMouseDown={onMouseDown}
      className="group relative z-10 flex w-3 flex-shrink-0 cursor-col-resize items-center justify-center select-none"
    >
      <div className="flex flex-col gap-[3px] opacity-0 transition-opacity group-hover:opacity-100">
        <span className="h-[3px] w-[3px] rounded-full bg-white/30" />
        <span className="h-[3px] w-[3px] rounded-full bg-white/30" />
        <span className="h-[3px] w-[3px] rounded-full bg-white/30" />
      </div>
    </div>
  )
}

/** Horizontal resizer (splits rows) */
function HSplit({ onMouseDown }: { onMouseDown: (e: React.MouseEvent) => void }) {
  return (
    <div
      onMouseDown={onMouseDown}
      className="group relative z-10 flex h-3 flex-shrink-0 cursor-row-resize items-center justify-center select-none"
    >
      <div className="flex flex-row gap-[3px] opacity-0 transition-opacity group-hover:opacity-100">
        <span className="h-[3px] w-[3px] rounded-full bg-white/30" />
        <span className="h-[3px] w-[3px] rounded-full bg-white/30" />
        <span className="h-[3px] w-[3px] rounded-full bg-white/30" />
      </div>
    </div>
  )
}

export function SignalsPage() {
  const { isAdmin } = useAuth()
  const enabledSignals    = useEnabledContent('/signal-types',    '/admin/signal-types')
  const enabledIndicators = useEnabledContent('/indicator-types', '/admin/indicator-types')

  // Indicators panel: enabled indicators with panel='indicator' + enabled signals with panel='indicator'
  const indicatorPanelInds = enabledIndicators
    ? INDICATORS.filter(i => enabledIndicators.find(x => x.id === i.id && x.panel === 'indicator'))
    : INDICATORS
  const indicatorPanelSigs = enabledSignals
    ? SIGNALS.filter(s => enabledSignals.find(x => x.id === s.id && x.panel === 'indicator'))
    : []

  // Signals panel: enabled signals with panel='signal' + enabled indicators with panel='signal'
  const signalPanelSigs = enabledSignals
    ? SIGNALS.filter(s => enabledSignals.find(x => x.id === s.id && x.panel === 'signal'))
    : SIGNALS
  const signalPanelInds = enabledIndicators
    ? INDICATORS.filter(i => enabledIndicators.find(x => x.id === i.id && x.panel === 'signal'))
    : []

  const [selectedCoin, setSelectedCoin] = useState('BTCUSDT')
  const [selectedTf, setSelectedTf] = useState('1h')
  const { connected, indicatorMarkers, candles } = useMarketIndicators(selectedCoin, selectedTf)

  const [chartModal, setChartModal] = useState<ChartModal | null>(null)
  const [constructorItems, setConstructorItems] = useState<ConstructorItem[]>([])
  const [isDragOver, setIsDragOver] = useState(false)
  const [flyingCard, setFlyingCard] = useState<FlyingCard | null>(null)
  const [paramsMap, setParamsMap] = useState<Record<string, Record<string, unknown>>>({})

  function getP(id: string, defaults: Record<string, unknown>): Record<string, unknown> {
    return paramsMap[id] ?? defaults
  }
  function setP(id: string, next: Record<string, unknown>) {
    setParamsMap(prev => ({ ...prev, [id]: next }))
  }

  const containerRef = useRef<HTMLDivElement>(null)
  const [topLeftPct,  setTopLeftPct]  = useState(50)
  const [bottomPx,    setBottomPx]    = useState(280)
  const [botLeftPct,  setBotLeftPct]  = useState(66)

  const onTopSplit = useCallback((e: React.MouseEvent) => {
    e.preventDefault()
    const startX = e.clientX; const startPct = topLeftPct
    const onMove = (ev: MouseEvent) => {
      const w = containerRef.current?.clientWidth ?? 1
      setTopLeftPct(Math.min(80, Math.max(20, startPct + ((ev.clientX - startX) / w) * 100)))
    }
    const onUp = () => { window.removeEventListener('mousemove', onMove); window.removeEventListener('mouseup', onUp) }
    window.addEventListener('mousemove', onMove); window.addEventListener('mouseup', onUp)
  }, [topLeftPct])

  const onHSplit = useCallback((e: React.MouseEvent) => {
    e.preventDefault()
    const startY = e.clientY; const startPx = bottomPx
    const onMove = (ev: MouseEvent) => setBottomPx(Math.min(520, Math.max(120, startPx - (ev.clientY - startY))))
    const onUp = () => { window.removeEventListener('mousemove', onMove); window.removeEventListener('mouseup', onUp) }
    window.addEventListener('mousemove', onMove); window.addEventListener('mouseup', onUp)
  }, [bottomPx])

  const onBotSplit = useCallback((e: React.MouseEvent) => {
    e.preventDefault()
    const startX = e.clientX; const startPct = botLeftPct
    const onMove = (ev: MouseEvent) => {
      const w = containerRef.current?.clientWidth ?? 1
      setBotLeftPct(Math.min(85, Math.max(15, startPct + ((ev.clientX - startX) / w) * 100)))
    }
    const onUp = () => { window.removeEventListener('mousemove', onMove); window.removeEventListener('mouseup', onUp) }
    window.addEventListener('mousemove', onMove); window.addEventListener('mouseup', onUp)
  }, [botLeftPct])

  function startDrag(e: React.DragEvent, item: ConstructorItem) {
    e.dataTransfer.setData(DRAG_KEY, JSON.stringify(item))
    e.dataTransfer.effectAllowed = 'copy'
  }
  function onDropZoneOver(e: React.DragEvent) {
    if (!e.dataTransfer.types.includes(DRAG_KEY)) return
    e.preventDefault(); e.dataTransfer.dropEffect = 'copy'; setIsDragOver(true)
  }
  function onDropZoneLeave(e: React.DragEvent) {
    if ((e.currentTarget as HTMLElement).contains(e.relatedTarget as Node)) return
    setIsDragOver(false)
  }
  function onDrop(e: React.DragEvent) {
    e.preventDefault(); setIsDragOver(false)
    const raw = e.dataTransfer.getData(DRAG_KEY)
    if (!raw) return
    try {
      const item: ConstructorItem = JSON.parse(raw)
      setConstructorItems(prev => prev.some(i => i.id === item.id) ? prev : [...prev, item])
    } catch {}
  }
  function removeItem(id: string) {
    setConstructorItems(prev => prev.filter(i => i.id !== id))
  }

  return (
    <div className="flex h-full flex-col overflow-hidden bg-[#0a0d14] text-slate-200">

      {/* Toolbar */}
      <div className="flex h-11 flex-shrink-0 items-center gap-3 border-b border-white/[.06] px-5">
        <span className="text-sm font-semibold text-slate-100">Сигналы</span>
        <select value={selectedCoin} onChange={e => setSelectedCoin(e.target.value)}
          className="rounded border border-white/[.08] bg-white/[.04] px-2 py-0.5 text-xs text-slate-200 focus:outline-none">
          {COINS.map(c => <option key={c} value={c}>{c}</option>)}
        </select>
        <div className="flex gap-0.5">
          {TIMEFRAMES.map(tf => (
            <button key={tf.value} onClick={() => setSelectedTf(tf.value)}
              className={`rounded px-2 py-0.5 text-xs transition-colors ${selectedTf === tf.value ? 'bg-[#5b8cff]/30 text-[#b8c8ff]' : 'text-slate-500 hover:text-slate-300'}`}>
              {tf.label}
            </button>
          ))}
        </div>
        <span className={`ml-auto flex items-center gap-1 rounded-full px-2 py-0.5 text-[10px] font-medium ${connected ? 'bg-emerald-400/15 text-emerald-300' : 'bg-white/[.04] text-slate-500'}`}>
          <span className={`h-1.5 w-1.5 rounded-full ${connected ? 'bg-emerald-400' : 'animate-pulse bg-slate-500'}`} />
          {connected ? 'Live' : 'Подключение...'}
        </span>
      </div>

      {/* Panel layout */}
      <div ref={containerRef} className="flex flex-1 flex-col gap-0 overflow-hidden p-2.5 pb-2.5">

        {/* Top row */}
        <div className="flex flex-1 overflow-hidden" style={{ minHeight: 0 }}>

          {/* Indicators panel */}
          <div className="flex flex-col overflow-hidden rounded-xl border border-white/[.07] bg-[#0d1018]" style={{ width: `${topLeftPct}%` }}>
            <div className="rounded-t-xl border-b border-white/[.06] bg-blue-500/[.07] px-5 py-2.5">
              <span className="text-xs font-semibold uppercase tracking-wider text-blue-300/70">
                Индикаторы <span className="ml-1.5 font-mono text-blue-400/50">{indicatorPanelInds.length + indicatorPanelSigs.length}</span>
              </span>
            </div>
            <div className="flex-1 overflow-auto px-5 py-4">
              <div className="grid grid-cols-[repeat(auto-fill,minmax(260px,1fr))] gap-3">
                {indicatorPanelInds.map(ind => (
                  <div key={ind.id} draggable
                    onDragStart={e => startDrag(e, { type: 'indicator', id: ind.id })}
                    className="cursor-grab active:cursor-grabbing">
                    <IndicatorCard
                      indicator={ind}
                      candles={candles}
                      value={getP(ind.id, ind.defaults as Record<string, unknown>) as BaseParams}
                      onChange={p => setP(ind.id, p)}
                      onSettingsClick={rect => setFlyingCard({ item: { type: 'indicator', def: ind as IndicatorDef<BaseParams> }, sourceRect: rect })}
                    />
                  </div>
                ))}
                {indicatorPanelSigs.map(sig => (
                  <div key={sig.id} draggable
                    onDragStart={e => startDrag(e, { type: 'signal', id: sig.id })}
                    className="cursor-grab active:cursor-grabbing">
                    <SignalCard
                      signal={sig}
                      candles={candles}
                      value={getP(sig.id, sig.defaults)}
                      onChange={p => setP(sig.id, p)}
                      onSettingsClick={rect => setFlyingCard({ item: { type: 'signal', def: sig as SignalDef<Record<string, unknown>> }, sourceRect: rect })}
                    />
                  </div>
                ))}
              </div>
            </div>
          </div>

          <VSplit onMouseDown={onTopSplit} />

          {/* Signals panel */}
          <div className="flex flex-1 flex-col overflow-hidden rounded-xl border border-white/[.07] bg-[#0d1018]">
            <div className="rounded-t-xl border-b border-white/[.06] bg-violet-500/[.07] px-5 py-2.5">
              <span className="text-xs font-semibold uppercase tracking-wider text-violet-300/70">
                Сигналы <span className="ml-1.5 font-mono text-violet-400/50">{signalPanelSigs.length + signalPanelInds.length}</span>
              </span>
            </div>
            <div className="flex-1 overflow-auto px-5 py-4">
              <div className="grid grid-cols-[repeat(auto-fill,minmax(260px,1fr))] gap-3">
                {signalPanelSigs.map(sig => (
                  <div key={sig.id} draggable
                    onDragStart={e => startDrag(e, { type: 'signal', id: sig.id })}
                    className="cursor-grab active:cursor-grabbing">
                    <SignalCard
                      signal={sig}
                      candles={candles}
                      value={getP(sig.id, sig.defaults)}
                      onChange={p => setP(sig.id, p)}
                      onSettingsClick={rect => setFlyingCard({ item: { type: 'signal', def: sig as SignalDef<Record<string, unknown>> }, sourceRect: rect })}
                    />
                  </div>
                ))}
                {signalPanelInds.map(ind => (
                  <div key={ind.id} draggable
                    onDragStart={e => startDrag(e, { type: 'indicator', id: ind.id })}
                    className="cursor-grab active:cursor-grabbing">
                    <IndicatorCard
                      indicator={ind}
                      candles={candles}
                      value={getP(ind.id, ind.defaults as Record<string, unknown>) as BaseParams}
                      onChange={p => setP(ind.id, p)}
                      onSettingsClick={rect => setFlyingCard({ item: { type: 'indicator', def: ind as IndicatorDef<BaseParams> }, sourceRect: rect })}
                    />
                  </div>
                ))}
              </div>
            </div>
          </div>

        </div>

        <HSplit onMouseDown={onHSplit} />

        {/* Bottom row */}
        <div className="flex flex-shrink-0 overflow-hidden" style={{ height: `${bottomPx}px` }}>

          {/* Constructor panel */}
          <div className="flex flex-col overflow-hidden rounded-xl border border-white/[.07] bg-[#0d1018]" style={{ width: `${botLeftPct}%` }}>
            <div className="rounded-t-xl border-b border-white/[.06] bg-emerald-500/[.07] px-5 py-2.5">
              <span className="text-xs font-semibold uppercase tracking-wider text-emerald-300/70">
                Конструктор сигналов
                {isAdmin && constructorItems.length > 0 && (
                  <span className="ml-2 font-mono text-emerald-400/50">{constructorItems.length}</span>
                )}
              </span>
            </div>
            {isAdmin ? (
              <div
                className={`relative flex-1 overflow-auto px-4 py-3 transition-colors ${isDragOver ? 'bg-[#5b8cff]/[.05]' : ''}`}
                onDragOver={onDropZoneOver}
                onDragLeave={onDropZoneLeave}
                onDrop={onDrop}
              >
                {isDragOver && (
                  <div className="pointer-events-none absolute inset-2 rounded-lg border-2 border-dashed border-[#5b8cff]/40" />
                )}
                {constructorItems.length === 0 ? (
                  <div className="flex h-full flex-col items-center justify-center gap-2 text-center">
                    <span className="text-[28px] opacity-20">⊕</span>
                    <span className="text-[11px] text-slate-500">Перетащите индикаторы или сигналы сюда</span>
                  </div>
                ) : (
                  <div className="grid grid-cols-[repeat(auto-fill,minmax(240px,1fr))] gap-3">
                    {constructorItems.map(item => {
                      const ind = item.type === 'indicator' ? INDICATORS.find(i => i.id === item.id) : null
                      const sig = item.type === 'signal'    ? SIGNALS.find(s => s.id === item.id)    : null
                      return (
                        <div key={item.id} className="relative">
                          {ind && (
                            <IndicatorCard
                              indicator={ind}
                              candles={candles}
                              value={getP(ind.id, ind.defaults as Record<string, unknown>) as BaseParams}
                              onChange={p => setP(ind.id, p)}
                              onSettingsClick={rect => setFlyingCard({ item: { type: 'indicator', def: ind as IndicatorDef<BaseParams> }, sourceRect: rect })}
                            />
                          )}
                          {sig && (
                            <SignalCard
                              signal={sig}
                              candles={candles}
                              value={getP(sig.id, sig.defaults)}
                              onChange={p => setP(sig.id, p)}
                              onSettingsClick={rect => setFlyingCard({ item: { type: 'signal', def: sig as SignalDef<Record<string, unknown>> }, sourceRect: rect })}
                            />
                          )}
                          <button type="button" onClick={() => removeItem(item.id)}
                            className="absolute right-2 top-2 z-10 flex h-5 w-5 items-center justify-center rounded-full bg-black/60 text-slate-400 hover:bg-rose-500/80 hover:text-white">
                            <X size={10} />
                          </button>
                        </div>
                      )
                    })}
                  </div>
                )}
              </div>
            ) : (
              <WipPlaceholder title="Конструктор сигналов" />
            )}
          </div>

          <VSplit onMouseDown={onBotSplit} />

          {/* My Signal panel */}
          <div className="flex flex-1 flex-col overflow-hidden rounded-xl border border-white/[.07] bg-[#0d1018]">
            <div className="rounded-t-xl border-b border-white/[.06] bg-sky-500/[.07] px-5 py-2.5">
              <span className="text-xs font-semibold uppercase tracking-wider text-sky-300/70">
                Мой сигнал
              </span>
            </div>
            {isAdmin ? (
              <div className="flex-1 overflow-auto px-4 py-3" />
            ) : (
              <WipPlaceholder title="Мой сигнал" />
            )}
          </div>

        </div>

      </div>

      {/* Flying card settings overlay */}
      {flyingCard && (
        <FlyingCardOverlay
          item={flyingCard.item}
          sourceRect={flyingCard.sourceRect}
          candles={candles}
          value={getP(flyingCard.item.def.id, flyingCard.item.def.defaults as Record<string, unknown>)}
          onChange={p => setP(flyingCard.item.def.id, p)}
          onClose={() => setFlyingCard(null)}
        />
      )}

      {/* Chart Modal */}
      {chartModal && (
        <SignalChartModal
          symbol={selectedCoin}
          interval={selectedTf}
          title={chartModal.title}
          badgeColor={chartModal.badgeColor}
          badgeLabel={chartModal.badgeLabel}
          markers={chartModal.markers}
          showMA={chartModal.showMA}
          onClose={() => setChartModal(null)}
        />
      )}
    </div>
  )
}
