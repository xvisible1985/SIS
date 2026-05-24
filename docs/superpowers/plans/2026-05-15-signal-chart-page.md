# Signal Chart Page Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Добавить иконку графика на карточки сигналов (SignalCard + StrategyCard), при клике открывать полноэкранную страницу `/signal-chart` с candlestick-графиком, маркерами срабатываний (Buy/Sell стрелки), историей в правой панели и интерактивными контролами (сигнал, таймфрейм, монета, параметры).

**Architecture:** Бэкенд-эндпоинт `GET /api/signals/chart-history` забирает свечи с Bybit REST, прогоняет сигнал через каждый срез свечей и возвращает события смены состояния. Параметры навигации передаются через модульный `signalChartStore.ts` (глобальный in-memory объект). Страница рендерится вне Layout — занимает весь экран.

**Tech Stack:** Go (pkg/signal, chi router), React + TypeScript, lightweight-charts, react-router-dom, Tailwind CSS.

---

## File Map

**Create:**
- `services/api-gateway/signal_chart_handler.go` — handler `SignalChartHistory`
- `frontend/src/stores/signalChartStore.ts` — module-level intent store
- `frontend/src/pages/SignalChartPage.tsx` — full-screen chart page

**Modify:**
- `pkg/signal/hub.go` — export `FetchKlineHistory` (uppercase)
- `services/api-gateway/main.go` — add route `r.Get("/signals/chart-history", ...)`
- `frontend/src/App.tsx` — add `/signal-chart` route outside Layout
- `frontend/src/features/indicators/components/SignalCard.tsx` — добавить `onChartClick` prop + иконка графика
- `frontend/src/components/strategies/StrategyCard.tsx` — добавить иконку графика в строки сигналов

---

## Task 1: Экспортировать FetchKlineHistory из pkg/signal/hub.go

**Files:**
- Modify: `pkg/signal/hub.go`

- [ ] **Step 1: Переименовать `fetchKlineHistory` → `FetchKlineHistory`**

В файле `pkg/signal/hub.go` найти строку:
```go
func fetchKlineHistory(symbol, bybitInterval string, limit int) ([]Candle, error) {
```
Заменить на:
```go
func FetchKlineHistory(symbol, bybitInterval string, limit int) ([]Candle, error) {
```

- [ ] **Step 2: Обновить внутренние вызовы**

В том же файле найти все вызовы `fetchKlineHistory` и заменить на `FetchKlineHistory`:
```go
// в prefetchAndConnect (строка ~149):
candles, err := FetchKlineHistory(symbol, bybitIv, candleBufferSize)
```
И в `GetCandles` (строка ~133):
```go
candles, err = FetchKlineHistory(symbol, bybitInterval(interval), candleBufferSize)
```

- [ ] **Step 3: Сборка — проверить что компилируется**

```bash
cd c:/Users/123/Projects/sis
go build ./pkg/signal/...
```
Ожидаемый вывод: без ошибок.

- [ ] **Step 4: Commit**

```bash
git add pkg/signal/hub.go
git commit -m "feat: export FetchKlineHistory from signal hub"
```

---

## Task 2: Бэкенд-handler SignalChartHistory

**Files:**
- Create: `services/api-gateway/signal_chart_handler.go`

- [ ] **Step 1: Создать файл handler**

Создать `services/api-gateway/signal_chart_handler.go`:

```go
package main

import (
	"encoding/json"
	"net/http"
	"strconv"

	"sis/pkg/signal"
)

type chartEvent struct {
	Time  int64   `json:"time"`
	State string  `json:"state"`
	Price float64 `json:"price"`
}

// SignalChartHistory fetches klines from Bybit, runs the requested signal
// over each progressive candle slice, and returns state-change events.
//
// Query params:
//   signal   — signal id (e.g. "st-flip")
//   symbol   — e.g. "BTCUSDT"
//   interval — e.g. "1h"
//   limit    — number of candles (default 500, max 1000)
//   params   — JSON object of signal-specific params
func (s *Server) SignalChartHistory(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	sigName := q.Get("signal")
	symbol   := q.Get("symbol")
	interval := q.Get("interval")
	if sigName == "" || symbol == "" || interval == "" {
		writeError(w, http.StatusBadRequest, "signal, symbol, interval required")
		return
	}

	limit := 500
	if l := q.Get("limit"); l != "" {
		if v, err := strconv.Atoi(l); err == nil && v > 0 && v <= 1000 {
			limit = v
		}
	}

	// Parse signal params JSON
	rawParams := q.Get("params")
	var params map[string]interface{}
	if rawParams != "" {
		d := json.NewDecoder(nil)
		_ = d
		if err := json.Unmarshal([]byte(rawParams), &params); err != nil {
			writeError(w, http.StatusBadRequest, "invalid params JSON")
			return
		}
	}

	// Map frontend TF to Bybit interval code
	bybitIv := bybitTF(interval)

	candles, err := signal.FetchKlineHistory(symbol, bybitIv, limit)
	if err != nil {
		writeError(w, http.StatusBadGateway, "failed to fetch klines: "+err.Error())
		return
	}

	cfg := signal.Config{Name: sigName, Params: params}
	sig, err := signal.Build(cfg)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	// Compute signal for each progressive slice and collect state-change events
	var events []chartEvent
	prev := signal.Neutral
	minCandles := 2
	for i := minCandles; i < len(candles); i++ {
		state := sig.Compute(candles[:i+1])
		if state != prev {
			events = append(events, chartEvent{
				Time:  candles[i].Time,
				State: string(state),
				Price: candles[i].Close,
			})
			prev = state
		}
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{"events": events})
}

// bybitTF converts frontend interval names to Bybit REST API interval codes.
func bybitTF(tf string) string {
	m := map[string]string{
		"1m": "1", "3m": "3", "5m": "5", "15m": "15", "30m": "30",
		"1h": "60", "2h": "120", "4h": "240", "6h": "360", "12h": "720",
		"1D": "D", "1W": "W", "1M": "M",
	}
	if v, ok := m[tf]; ok {
		return v
	}
	return tf
}
```

- [ ] **Step 2: Зарегистрировать маршрут в main.go**

В `services/api-gateway/main.go` в блоке маршрутов добавить строку после существующих `/signals` маршрутов:

```go
r.Get("/signals/chart-history", s.SignalChartHistory)
```

Вставить сразу перед:
```go
r.Post("/signals", s.CreateSignal)
```
т.е. до CRUD-маршрутов, чтобы `/signals/chart-history` не перехватывался `/signals/{id}`.

- [ ] **Step 3: Собрать и проверить**

```bash
cd c:/Users/123/Projects/sis
go build ./services/api-gateway/...
```
Ожидаемый вывод: без ошибок.

- [ ] **Step 4: Commit**

```bash
git add services/api-gateway/signal_chart_handler.go services/api-gateway/main.go
git commit -m "feat: add /signals/chart-history endpoint"
```

---

## Task 3: Глобальный стор для передачи параметров

**Files:**
- Create: `frontend/src/stores/signalChartStore.ts`

- [ ] **Step 1: Создать файл стора**

Создать `frontend/src/stores/signalChartStore.ts`:

```ts
export interface SignalChartIntent {
  signalId: string
  signalName: string
  params: Record<string, unknown>
  symbol: string
  tf: string
}

let _intent: SignalChartIntent | null = null

export function setSignalChartIntent(intent: SignalChartIntent): void {
  _intent = intent
}

/** Reads and clears the stored intent. Returns null if none was set. */
export function popSignalChartIntent(): SignalChartIntent | null {
  const v = _intent
  _intent = null
  return v
}
```

- [ ] **Step 2: Commit**

```bash
git add frontend/src/stores/signalChartStore.ts
git commit -m "feat: add signalChartStore for navigation intent"
```

---

## Task 4: Страница SignalChartPage

**Files:**
- Create: `frontend/src/pages/SignalChartPage.tsx`

- [ ] **Step 1: Создать страницу**

Создать `frontend/src/pages/SignalChartPage.tsx`:

```tsx
import { useState, useEffect, useRef, useCallback } from 'react'
import { useNavigate } from 'react-router-dom'
import { createChart, CandlestickSeries, createSeriesMarkers, ColorType } from 'lightweight-charts'
import type { IChartApi } from 'lightweight-charts'
import { popSignalChartIntent } from '../stores/signalChartStore'
import { SIGNALS } from '../features/indicators/signals'
import { CoinPicker } from '../components/common/CoinPicker'
import { ParamNumber, ParamSegmented } from '../features/indicators/components/Params'
import type { SignalDef } from '../features/indicators/types'

const TF_OPTIONS = ['1m','5m','15m','1h','4h','1D']

interface ChartEvent {
  time: number
  state: 'buy' | 'sell' | 'neutral'
  price: number
}

function toBybitTF(tf: string): string {
  const m: Record<string,string> = {
    '1m':'1','5m':'5','15m':'15','30m':'30',
    '1h':'60','4h':'240','1D':'D'
  }
  return m[tf] ?? tf
}

export function SignalChartPage() {
  const navigate = useNavigate()
  const intent = popSignalChartIntent()

  const [signalId, setSignalId] = useState(intent?.signalId ?? SIGNALS[0]!.id)
  const [symbol,   setSymbol]   = useState(intent?.symbol   ?? 'BTCUSDT')
  const [tf,       setTf]       = useState(intent?.tf       ?? '1h')
  const [params,   setParams]   = useState<Record<string,unknown>>(
    intent?.params ?? {}
  )

  const sig = SIGNALS.find(s => s.id === signalId) ?? SIGNALS[0]!

  // Sync params when signal changes
  useEffect(() => {
    const newSig = SIGNALS.find(s => s.id === signalId) ?? SIGNALS[0]!
    setParams(newSig.defaults as Record<string,unknown>)
  }, [signalId])

  const [events,  setEvents]  = useState<ChartEvent[]>([])
  const [loading, setLoading] = useState(false)
  const [error,   setError]   = useState<string | null>(null)

  const containerRef = useRef<HTMLDivElement>(null)
  const chartRef     = useRef<IChartApi | null>(null)

  // Fetch chart events from backend
  const fetchEvents = useCallback(async () => {
    setLoading(true)
    setError(null)
    try {
      const url = new URL('/api/signals/chart-history', window.location.origin)
      url.searchParams.set('signal',   signalId)
      url.searchParams.set('symbol',   symbol)
      url.searchParams.set('interval', tf)
      url.searchParams.set('limit',    '500')
      url.searchParams.set('params',   JSON.stringify(params))

      const res = await fetch(url.toString())
      if (!res.ok) throw new Error(await res.text())
      const json = await res.json()
      setEvents(json.events ?? [])
    } catch (e: any) {
      setError(e.message ?? 'Ошибка загрузки')
    } finally {
      setLoading(false)
    }
  }, [signalId, symbol, tf, params])

  useEffect(() => { fetchEvents() }, [fetchEvents])

  // Build chart from candle data (fetched directly from Bybit)
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
      if (cancelled || !el) return

      const list: string[][] = (data?.result?.list ?? []).slice().reverse()
      const candles = list.map(k => ({
        time: Math.floor(Number(k[0]) / 1000) as unknown as number,
        open:  parseFloat(k[1]!),
        high:  parseFloat(k[2]!),
        low:   parseFloat(k[3]!),
        close: parseFloat(k[4]!),
      }))

      chartRef.current?.remove()
      const chart = createChart(el, {
        width: el.clientWidth,
        height: el.clientHeight,
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
        upColor: '#00DC82', downColor: '#ef4444',
        borderUpColor: '#00DC82', borderDownColor: '#ef4444',
        wickUpColor: '#00DC82', wickDownColor: '#ef4444',
      })
      series.setData(candles as any)

      // Add signal markers from events
      if (events.length > 0) {
        const firstTime = candles[0]?.time ?? 0
        const lastTime  = candles[candles.length - 1]?.time ?? Infinity
        const markers = events
          .map(ev => ({ ...ev, timeSec: Math.floor(ev.time / 1000) }))
          .filter(ev => ev.state !== 'neutral' && ev.timeSec >= (firstTime as number) && ev.timeSec <= (lastTime as number))
          .sort((a, b) => a.timeSec - b.timeSec)
          .map(ev => ({
            time: ev.timeSec as unknown as number,
            position: ev.state === 'buy' ? 'belowBar' as const : 'aboveBar' as const,
            color: ev.state === 'buy' ? '#00DC82' : '#ef4444',
            shape: ev.state === 'buy' ? 'arrowUp' as const : 'arrowDown' as const,
            text: ev.state === 'buy' ? 'Long' : 'Short',
            size: 1,
          }))
        if (markers.length) createSeriesMarkers(series, markers as any)
      }

      chart.timeScale().fitContent()
    }

    init().catch(console.error)

    const onResize = () => {
      if (el && chartRef.current) {
        chartRef.current.applyOptions({ width: el.clientWidth, height: el.clientHeight })
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
    <div className="fixed inset-0 z-50 flex flex-col bg-[#0a0d14] text-white font-sans">
      {/* ── Top bar ── */}
      <div className="flex items-center gap-3 px-4 py-2 border-b border-white/[.07] bg-[#0d1117] flex-wrap flex-shrink-0">
        {/* Back */}
        <button
          onClick={() => navigate(-1)}
          className="flex items-center gap-1.5 text-[12px] text-slate-400 hover:text-white transition-colors"
        >
          <svg width={14} height={14} viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth={2.2} strokeLinecap="round" strokeLinejoin="round">
            <path d="M19 12H5M12 5l-7 7 7 7"/>
          </svg>
          Назад
        </button>

        <div className="w-px h-5 bg-white/[.08]" />

        {/* Signal selector */}
        <select
          value={signalId}
          onChange={e => setSignalId(e.target.value)}
          className="bg-[#161b27] border border-white/[.10] rounded-[7px] text-[12px] text-[#e6ebf5] px-2 py-1.5 outline-none cursor-pointer"
        >
          {SIGNALS.map(s => (
            <option key={s.id} value={s.id}>{s.name}</option>
          ))}
        </select>

        {/* Timeframe */}
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

        {/* Coin picker */}
        <CoinPicker value={symbol} onChange={setSymbol} size="sm" />

        <div className="w-px h-5 bg-white/[.08]" />

        {/* Signal params — rendered inline */}
        {(sig as SignalDef<any>).params.map(p => {
          if (p.kind === 'number') {
            return (
              <div key={p.key} className="flex items-center gap-1.5">
                <span className="text-[10px] text-slate-500 uppercase tracking-[.6px]">{p.label}</span>
                <ParamNumber
                  label=""
                  value={params[p.key] as number ?? (sig.defaults as any)[p.key]}
                  step={p.step} min={p.min} max={p.max} suffix={p.suffix} decimals={p.decimals}
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
                value={params[p.key] as string ?? (sig.defaults as any)[p.key]}
                options={p.options}
                onChange={v => setParam(p.key, v)}
              />
            </div>
          )
        })}

        {loading && <span className="text-[11px] text-slate-500 ml-auto animate-pulse">Загрузка…</span>}
        {error   && <span className="text-[11px] text-rose-400 ml-auto">{error}</span>}
      </div>

      {/* ── Body: chart + right panel ── */}
      <div className="flex flex-1 min-h-0">
        {/* Chart */}
        <div ref={containerRef} className="flex-1 min-w-0" />

        {/* Right panel — history */}
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
                  className="flex items-center gap-2.5 px-3 py-2 border-b border-white/[.04] hover:bg-white/[.03] transition-colors cursor-pointer"
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
                      {d.toLocaleDateString('ru-RU', { day:'2-digit', month:'2-digit' })}
                      {' '}
                      {d.toLocaleTimeString('ru-RU', { hour:'2-digit', minute:'2-digit' })}
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
```

- [ ] **Step 2: Commit**

```bash
git add frontend/src/pages/SignalChartPage.tsx frontend/src/stores/signalChartStore.ts
git commit -m "feat: add SignalChartPage full-screen chart"
```

---

## Task 5: Добавить маршрут /signal-chart в App.tsx

**Files:**
- Modify: `frontend/src/App.tsx`

- [ ] **Step 1: Добавить импорт и маршрут**

В `frontend/src/App.tsx` добавить импорт:
```tsx
import { SignalChartPage } from './pages/SignalChartPage'
```

Добавить маршрут **внутри `<Routes>` но вне `<Layout>`**, рядом с `/login`:
```tsx
<Route path="/signal-chart" element={<SignalChartPage />} />
```

Итоговый `App.tsx`:
```tsx
import { BrowserRouter, Routes, Route, Navigate } from 'react-router-dom'
import { AuthProvider } from './hooks/useAuth'
import { ProtectedRoute } from './components/ProtectedRoute'
import { Layout } from './components/Layout'
import { AuthPage } from './pages/AuthPage'
import { DashboardPage } from './pages/DashboardPage'
import { SignalBuilderPage } from './pages/SignalBuilderPage'
import { BacktestPage } from './pages/BacktestPage'
import { OptimizerPage } from './pages/OptimizerPage'
import { WebhooksPage } from './pages/WebhooksPage'
import { AccountsPage } from './pages/AccountsPage'
import { TerminalPage } from './pages/TerminalPage'
import { SignalsPage } from './pages/SignalsPage'
import { AdminPage }   from './pages/AdminPage'
import { SignalChartPage } from './pages/SignalChartPage'

export default function App() {
  return (
    <AuthProvider>
      <BrowserRouter>
        <Routes>
          <Route path="/login"        element={<AuthPage defaultTab="login" />} />
          <Route path="/register"     element={<AuthPage defaultTab="register" />} />
          <Route path="/signal-chart" element={<SignalChartPage />} />
          <Route
            path="/*"
            element={
              <ProtectedRoute>
                <Layout>
                  <Routes>
                    <Route path=""                      element={<DashboardPage />} />
                    <Route path="signals/new"           element={<SignalBuilderPage />} />
                    <Route path="signals/:id/edit"      element={<SignalBuilderPage />} />
                    <Route path="signals/:id/backtest"  element={<BacktestPage />} />
                    <Route path="signals/:id/optimize"  element={<OptimizerPage />} />
                    <Route path="terminal"              element={<TerminalPage />} />
                    <Route path="signals"               element={<SignalsPage />} />
                    <Route path="webhooks"              element={<WebhooksPage />} />
                    <Route path="accounts"              element={<AccountsPage />} />
                    <Route path="admin"                 element={<AdminPage />} />
                    <Route path="*"                     element={<Navigate to="/" replace />} />
                  </Routes>
                </Layout>
              </ProtectedRoute>
            }
          />
        </Routes>
      </BrowserRouter>
    </AuthProvider>
  )
}
```

- [ ] **Step 2: Проверить TypeScript**

```bash
cd c:/Users/123/Projects/sis/frontend
npx tsc --noEmit
```
Ожидаемый вывод: без ошибок (или только уже существующие).

- [ ] **Step 3: Commit**

```bash
git add frontend/src/App.tsx
git commit -m "feat: add /signal-chart route"
```

---

## Task 6: Иконка графика в SignalCard

**Files:**
- Modify: `frontend/src/features/indicators/components/SignalCard.tsx`

- [ ] **Step 1: Добавить `onChartClick` prop и кнопку**

В `frontend/src/features/indicators/components/SignalCard.tsx` изменить Props и добавить кнопку-иконку рядом с badge:

```tsx
import { useRef, useState } from 'react';
import { ChevronUp, Settings, LineChart } from 'lucide-react';
import { CAT_STYLES, TFS } from '../categories';
import type { SignalDef, Candle, SignalState } from '../types';
import { SignalPill } from './SignalPill';
import { ParamNumber, ParamSegmented, useParams } from './Params';

type Props<P extends Record<string, unknown>> = {
  signal: SignalDef<P>;
  candles?: Candle[];
  value?: P;
  onChange?: (next: P) => void;
  defaultExpanded?: boolean;
  onSettingsClick?: (rect: DOMRect) => void;
  hideBadge?: boolean;
  onChartClick?: () => void;   // ← новый prop
};
```

В JSX, в блоке рядом с badge (после `{!hideBadge && ...}`), добавить кнопку:
```tsx
{onChartClick && (
  <button
    type="button"
    onClick={e => { e.stopPropagation(); onChartClick() }}
    className="shrink-0 w-7 h-7 inline-flex items-center justify-center rounded-[7px] bg-white/[.04] border border-white/[.08] text-slate-400 hover:text-[#5b8cff] hover:bg-[#5b8cff]/[.10] hover:border-[#5b8cff]/30 transition-colors"
    title="Открыть график"
  >
    <LineChart size={13} />
  </button>
)}
```

Полный блок `items-start gap-2.5 p-3 pb-2.5` после изменений:
```tsx
<div className="flex items-start gap-2.5 p-3 pb-2.5">
  <div className="min-w-0 flex-1">
    <div className="truncate text-[14px] font-bold leading-tight tracking-tight text-slate-50">{signal.name}</div>
    <div className="mt-1 min-h-[30px] text-[11px] leading-snug text-slate-400 line-clamp-2">{signal.desc}</div>
  </div>
  <div className="shrink-0 flex items-center gap-1.5">
    {!hideBadge && liveState && <SignalPill signal={{ state: liveState }} />}
    {onChartClick && (
      <button
        type="button"
        onClick={e => { e.stopPropagation(); onChartClick() }}
        className="shrink-0 w-7 h-7 inline-flex items-center justify-center rounded-[7px] bg-white/[.04] border border-white/[.08] text-slate-400 hover:text-[#5b8cff] hover:bg-[#5b8cff]/[.10] hover:border-[#5b8cff]/30 transition-colors"
        title="Открыть график"
      >
        <LineChart size={13} />
      </button>
    )}
  </div>
</div>
```

- [ ] **Step 2: Подключить в SignalsPage.tsx**

В `frontend/src/pages/SignalsPage.tsx` добавить импорты:
```tsx
import { useNavigate } from 'react-router-dom'
import { setSignalChartIntent } from '../stores/signalChartStore'
```

В компоненте:
```tsx
const navigate = useNavigate()

function openSignalChart(sig: SignalDef<Record<string,unknown>>, params?: Record<string,unknown>) {
  setSignalChartIntent({
    signalId: sig.id,
    signalName: sig.name,
    params: params ?? (sig.defaults as Record<string,unknown>),
    symbol: 'BTCUSDT',
    tf: (sig.defaults as any).tf ?? '1h',
  })
  navigate('/signal-chart')
}
```

Передать `onChartClick` во все `<SignalCard>` рендеры в SignalsPage:
```tsx
onChartClick={() => openSignalChart(item.def as SignalDef<Record<string,unknown>>)}
```

- [ ] **Step 3: Подключить в AdminPage.tsx**

В `frontend/src/pages/AdminPage.tsx` добавить те же импорты и функцию `openSignalChart`, передать `onChartClick` во все `<SignalCard>` в странице.

- [ ] **Step 4: Commit**

```bash
git add frontend/src/features/indicators/components/SignalCard.tsx \
        frontend/src/pages/SignalsPage.tsx \
        frontend/src/pages/AdminPage.tsx
git commit -m "feat: add chart icon to SignalCard"
```

---

## Task 7: Иконка графика в StrategyCard (строки сигналов)

**Files:**
- Modify: `frontend/src/components/strategies/StrategyCard.tsx`

- [ ] **Step 1: Добавить иконку в строки сигналов**

В `StrategyCard.tsx` найти секцию рендера строк сигналов (около строки 571 — `return ( <div key={sc.name + idx}...`).

Добавить импорты в начало файла:
```tsx
import { useNavigate } from 'react-router-dom'
import { setSignalChartIntent } from '../../stores/signalChartStore'
```

Внутри компонента `StrategyCard` добавить:
```tsx
const navigate = useNavigate()

function openSignalChart(sc: typeof s.signal_configs[0]) {
  const sigDef = SIGNALS.find(x => x.id === sc.name) ?? (INDICATORS as any[]).find(x => x.id === sc.name)
  setSignalChartIntent({
    signalId: sc.name,
    signalName: sigDef?.name ?? sc.name,
    params: (sc.params ?? {}) as Record<string, unknown>,
    symbol: s.symbol,
    tf: (sc.params?.tf as string) ?? '1h',
  })
  navigate('/signal-chart')
}
```

В JSX-строке сигнала добавить кнопку после кнопки удаления:
```tsx
{/* chart icon — outside the card */}
<button
  type="button"
  onClick={e => { e.stopPropagation(); openSignalChart(sc) }}
  className="shrink-0 w-7 h-7 inline-flex items-center justify-center rounded-[6px] text-slate-500 hover:text-[#5b8cff] hover:bg-[#5b8cff]/[.10] transition-colors"
  title="Открыть график сигнала"
>
  <svg width={13} height={13} viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth={1.8} strokeLinecap="round" strokeLinejoin="round">
    <polyline points="22 12 18 12 15 21 9 3 6 12 2 12"/>
  </svg>
</button>
```

Найти блок `{/* delete — outside the card */}` и вставить кнопку графика сразу после него (или до него, на усмотрение):
```tsx
<div key={sc.name + idx} className="flex items-center gap-2 min-w-0">
  {/* card */}
  <div className="flex-1 flex items-center gap-3 px-3 py-2.5 rounded-[9px] min-w-0" ...>
    ...
  </div>
  {/* chart icon */}
  <button
    type="button"
    onClick={e => { e.stopPropagation(); openSignalChart(sc) }}
    className="shrink-0 w-7 h-7 inline-flex items-center justify-center rounded-[6px] text-slate-500 hover:text-[#5b8cff] hover:bg-[#5b8cff]/[.10] transition-colors"
    title="График сигнала"
  >
    <svg width={13} height={13} viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth={1.8} strokeLinecap="round" strokeLinejoin="round">
      <polyline points="22 12 18 12 15 21 9 3 6 12 2 12"/>
    </svg>
  </button>
  {/* delete */}
  <button
    type="button"
    onClick={handleDeleteSignal}
    className="shrink-0 w-7 h-7 inline-flex items-center justify-center rounded-[6px] text-slate-500 hover:text-rose-300 hover:bg-rose-400/[.10] transition-colors"
  >
    <IcTrash s={14} w={1.8} />
  </button>
</div>
```

- [ ] **Step 2: TypeScript check**

```bash
cd c:/Users/123/Projects/sis/frontend
npx tsc --noEmit
```
Ожидаемый вывод: без новых ошибок.

- [ ] **Step 3: Commit**

```bash
git add frontend/src/components/strategies/StrategyCard.tsx
git commit -m "feat: add chart icon to StrategyCard signal rows"
```

---

## Task 8: Финальная сборка и проверка

- [ ] **Step 1: Собрать бэкенд**

```bash
cd c:/Users/123/Projects/sis
go build ./...
```
Ожидаемый вывод: без ошибок.

- [ ] **Step 2: Собрать фронтенд**

```bash
cd c:/Users/123/Projects/sis/frontend
npm run build
```
Ожидаемый вывод: без ошибок TypeScript/Vite.

- [ ] **Step 3: Проверить endpoint вручную**

Запустить dev-сервер и открыть в браузере:
```
http://localhost:3000/signal-chart
```
Проверить:
- Страница открывается полноэкранно (без сайдбара Layout)
- Кнопка «Назад» работает
- Переключение сигнала / TF / монеты перезагружает маркеры
- Правая панель показывает список Long/Short событий

- [ ] **Step 4: Проверить иконки**

- Открыть SignalsPage — иконка LineChart появляется на карточках сигналов
- Клик открывает `/signal-chart` с заполненными параметрами
- Открыть TerminalPage → развернуть стратегию с сигналом → иконка отображается рядом с Delete

- [ ] **Step 5: Финальный commit**

```bash
git add -A
git commit -m "feat: signal chart page — full-screen chart with signal history"
```
