// frontend/src/features/log-visualizer/LogVisualizerTab.tsx

import { useState, useEffect, useRef, useCallback } from 'react'
import { LogVisualizerChart }      from './LogVisualizerChart'
import { LogVisualizerEventsList } from './LogVisualizerEventsList'
import { LogVisualizerControls }   from './LogVisualizerControls'
import { lvGetAccounts, lvGetStrategies, lvGetEvents, lvGetLevels, lvGetKlines } from './api'
import { makeMergedEventLabel } from './utils'
import type { LVAccount, LVStrategy, LVCandle, MergedEvent, Interval, LayerSettings } from './types'
import { INTERVALS, DEFAULT_LAYER_SETTINGS } from './types'

// Speed: candles per second = speed * CANDLES_PER_SEC_BASE
const CANDLES_PER_SEC_BASE = 20

export function LogVisualizerTab() {
  // ── Picker state ──────────────────────────────────────────────────────
  const [accounts,   setAccounts]   = useState<LVAccount[]>([])
  const [strategies, setStrategies] = useState<LVStrategy[]>([])
  const [accountId,  setAccountId]  = useState('')
  const [strategyId, setStrategyId] = useState('')
  const [fromDate,   setFromDate]   = useState(() => {
    const d = new Date(); d.setDate(d.getDate() - 7); return d.toISOString().slice(0, 10)
  })
  const [toDate,     setToDate]     = useState(() => new Date().toISOString().slice(0, 10))
  const [interval,   setIntervalVal] = useState<Interval>('1m')
  const [speed,      setSpeed]      = useState(8)
  const [isMax,      setIsMax]      = useState(false)
  const [layerSettings, setLayerSettings] = useState<LayerSettings>(DEFAULT_LAYER_SETTINGS)

  // ── Loaded data ───────────────────────────────────────────────────────
  const [candles,   setCandles]   = useState<LVCandle[]>([])
  const [events,    setEvents]    = useState<MergedEvent[]>([])
  const [loading,   setLoading]   = useState(false)
  const [loadError, setLoadError] = useState<string | null>(null)

  // ── Animation state ───────────────────────────────────────────────────
  const [candleIdx,  setCandleIdx]  = useState(-1)
  const [eventIdx,   setEventIdx]   = useState(-1)
  const [isPlaying,  setIsPlaying]  = useState(false)

  // Stable refs for animation loop (avoid stale closures)
  const candleIdxRef = useRef(candleIdx)
  const eventIdxRef  = useRef(eventIdx)
  const candlesRef   = useRef(candles)
  const eventsRef    = useRef(events)
  // Generation counter: prevents a slow first load from overwriting a faster second load
  const loadGenRef   = useRef(0)
  useEffect(() => { candleIdxRef.current = candleIdx }, [candleIdx])
  useEffect(() => { eventIdxRef.current  = eventIdx  }, [eventIdx])
  useEffect(() => { candlesRef.current   = candles   }, [candles])
  useEffect(() => { eventsRef.current    = events    }, [events])

  // ── Load accounts on mount ────────────────────────────────────────────
  useEffect(() => {
    lvGetAccounts()
      .then(setAccounts)
      .catch(e => console.error('LV accounts:', e))
  }, [])

  // ── Load strategies when account changes ──────────────────────────────
  useEffect(() => {
    if (!accountId) { setStrategies([]); setStrategyId(''); return }
    lvGetStrategies(accountId)
      .then(list => { setStrategies(list); setStrategyId(list[0]?.id ?? '') })
      .catch(e => console.error('LV strategies:', e))
  }, [accountId])

  // ── Load data ─────────────────────────────────────────────────────────
  const handleLoad = useCallback(async () => {
    if (!strategyId || !fromDate || !toDate) return
    const gen = ++loadGenRef.current  // increment generation; ignore results from older loads
    setLoading(true)
    setLoadError(null)
    setCandles([]); setEvents([])
    setCandleIdx(-1); setEventIdx(-1); setIsPlaying(false)

    try {
      const fromMs = new Date(fromDate).getTime()
      const toMs   = new Date(toDate).getTime() + 86_400_000 // include end of day

      const strat  = strategies.find(s => s.id === strategyId)
      const symbol = strat?.symbol ?? ''

      const [eventsRaw, levelsRaw, candlesRaw] = await Promise.all([
        lvGetEvents(strategyId, fromMs, toMs),
        lvGetLevels(strategyId, fromMs, toMs),
        lvGetKlines(symbol, interval, fromMs, toMs),
      ])

      if (gen !== loadGenRef.current) return  // superseded by a newer load

      // Merge and sort by timestamp
      const merged: MergedEvent[] = [
        ...eventsRaw.map(e => ({
          tsMs:  e.tsMs,
          kind:  'log' as const,
          log:   e,
          label: makeMergedEventLabel('log', e, undefined),
        })),
        ...levelsRaw.map(l => ({
          tsMs:   l.tsMs,
          kind:   'level' as const,
          level:  l,
          label:  makeMergedEventLabel('level', undefined, l),
        })),
      ].sort((a, b) => a.tsMs - b.tsMs)

      setCandles(candlesRaw)
      setEvents(merged)
      setCandleIdx(candlesRaw.length > 0 ? 0 : -1)
    } catch (e) {
      if (gen !== loadGenRef.current) return
      setLoadError(e instanceof Error ? e.message : 'Ошибка загрузки')
    } finally {
      if (gen === loadGenRef.current) setLoading(false)
    }
  }, [strategyId, fromDate, toDate, interval, strategies])

  // ── Animation tick ────────────────────────────────────────────────────
  useEffect(() => {
    if (!isPlaying) return

    if (isMax) {
      // MAX: instant jump to next event, no animation
      const nextEvIdx = eventIdxRef.current + 1
      if (nextEvIdx >= eventsRef.current.length) {
        setCandleIdx(candlesRef.current.length - 1)
        setIsPlaying(false)
        return
      }
      const targetTs = eventsRef.current[nextEvIdx].tsMs
      const ci = candlesRef.current.findIndex(c => c.t >= targetTs)
      setCandleIdx(ci >= 0 ? ci : candlesRef.current.length - 1)
      setEventIdx(nextEvIdx)
      setIsPlaying(false)
      return
    }

    const intervalMs = Math.max(16, Math.round(1000 / (speed * CANDLES_PER_SEC_BASE)))
    const id = setInterval(() => {
      const ci     = candleIdxRef.current
      const ei     = eventIdxRef.current
      const cs     = candlesRef.current
      const evs    = eventsRef.current
      const nextCi = ci + 1

      if (nextCi >= cs.length) {
        setIsPlaying(false)
        return
      }

      const nextCandle = cs[nextCi]
      const nextEvent  = evs[ei + 1]

      if (nextEvent && nextCandle.t >= nextEvent.tsMs) {
        setCandleIdx(nextCi)
        setEventIdx(ei + 1)
        setIsPlaying(false)
      } else {
        setCandleIdx(nextCi)
      }
    }, intervalMs)

    return () => clearInterval(id)
  }, [isPlaying, speed, isMax])

  // ── Navigation helpers ────────────────────────────────────────────────
  const jumpToEvent = useCallback((idx: number) => {
    setIsPlaying(false)
    if (idx < 0 || idx >= events.length) return
    const targetTs = events[idx].tsMs
    const ci = candles.findIndex(c => c.t >= targetTs)
    setCandleIdx(ci >= 0 ? ci : candles.length - 1)
    setEventIdx(idx)
  }, [candles, events])

  const handlePrev  = useCallback(() => jumpToEvent(eventIdx - 1),      [jumpToEvent, eventIdx])
  const handleNext  = useCallback(() => jumpToEvent(eventIdx + 1),      [jumpToEvent, eventIdx])
  const handleFirst = useCallback(() => jumpToEvent(0),                 [jumpToEvent])
  const handleLast  = useCallback(() => jumpToEvent(events.length - 1), [jumpToEvent, events.length])

  // ── Derived data ──────────────────────────────────────────────────────
  const visibleCandles = candleIdx >= 0 ? candles.slice(0, candleIdx + 1) : []
  const visibleEvents  = eventIdx  >= 0 ? events.slice(0, eventIdx + 1)  : []
  const currentEvent   = eventIdx >= 0 ? events[eventIdx] : null
  const hasData        = candles.length > 0

  function stratLabel(s: LVStrategy) {
    return `${s.symbol} · ${s.direction} · ${s.strategyType}`
  }

  // ── Render ────────────────────────────────────────────────────────────
  return (
    <div className="flex h-full flex-col overflow-hidden bg-[#0a0d14] text-slate-200">

      {/* Toolbar */}
      <div className="flex-shrink-0 flex flex-wrap items-center gap-2 px-4 py-2 border-b border-white/[.06]">
        {/* Account */}
        <select
          value={accountId}
          onChange={e => setAccountId(e.target.value)}
          className="rounded border border-white/[.08] bg-white/[.04] px-2 py-1 text-xs text-slate-200 focus:outline-none"
        >
          <option value="">— Аккаунт —</option>
          {accounts.map(a => (
            <option key={a.id} value={a.id}>{a.ownerUsername} / {a.label}</option>
          ))}
        </select>

        {/* Strategy */}
        <select
          value={strategyId}
          onChange={e => setStrategyId(e.target.value)}
          disabled={strategies.length === 0}
          className="rounded border border-white/[.08] bg-white/[.04] px-2 py-1 text-xs text-slate-200 focus:outline-none disabled:opacity-40"
        >
          <option value="">— Стратегия —</option>
          {strategies.map(s => (
            <option key={s.id} value={s.id}>{stratLabel(s)}</option>
          ))}
        </select>

        {/* Date range */}
        <input
          type="date" value={fromDate} onChange={e => setFromDate(e.target.value)}
          className="rounded border border-white/[.08] bg-white/[.04] px-2 py-1 text-xs text-slate-200 focus:outline-none"
        />
        <span className="text-slate-600 text-xs">→</span>
        <input
          type="date" value={toDate} onChange={e => setToDate(e.target.value)}
          className="rounded border border-white/[.08] bg-white/[.04] px-2 py-1 text-xs text-slate-200 focus:outline-none"
        />

        {/* Interval */}
        <select
          value={interval}
          onChange={e => setIntervalVal(e.target.value as Interval)}
          className="rounded border border-white/[.08] bg-white/[.04] px-2 py-1 text-xs text-slate-200 focus:outline-none"
        >
          {INTERVALS.map(iv => <option key={iv} value={iv}>{iv}</option>)}
        </select>

        {/* Load button */}
        <button
          onClick={handleLoad}
          disabled={!strategyId || loading}
          className="ml-auto rounded px-3 py-1 text-xs font-semibold bg-[#5b8cff]/20 text-[#b8c8ff] hover:bg-[#5b8cff]/30 disabled:opacity-40 disabled:cursor-not-allowed transition-colors"
        >
          {loading ? 'Загрузка…' : '▶ Загрузить'}
        </button>
      </div>

      {/* Error */}
      {loadError && (
        <div className="flex-shrink-0 mx-4 mt-2 rounded border border-rose-400/20 bg-rose-400/[.06] px-3 py-2 text-xs text-rose-300">
          {loadError}
        </div>
      )}

      {/* Large range warning */}
      {!loading && candles.length > 30_000 && (
        <div className="flex-shrink-0 mx-4 mt-1 text-[10px] text-amber-400/70">
          ⚠ Загружено {candles.length.toLocaleString('ru-RU')} свечей — большой диапазон, анимация может быть медленной
        </div>
      )}

      {/* Main area: chart + sidebar */}
      <div className="flex-1 flex overflow-hidden min-h-0">
        <div className="flex-1 min-w-0">
          <LogVisualizerChart candles={visibleCandles} events={visibleEvents} layerSettings={layerSettings} />
        </div>
        <div className="w-[220px] flex-shrink-0">
          <LogVisualizerEventsList
            events={events}
            currentIndex={eventIdx}
            onJump={jumpToEvent}
          />
        </div>
      </div>

      {/* Controls */}
      <LogVisualizerControls
        isPlaying={isPlaying}
        speed={speed}
        isMax={isMax}
        currentEvent={currentEvent}
        hasData={hasData}
        canGoPrev={eventIdx > 0}
        canGoNext={eventIdx < events.length - 1}
        onPlay={() => setIsPlaying(true)}
        onPause={() => setIsPlaying(false)}
        onPrev={handlePrev}
        onNext={handleNext}
        onFirst={handleFirst}
        onLast={handleLast}
        onSpeedChange={setSpeed}
        onMaxChange={setIsMax}
      />
    </div>
  )
}
