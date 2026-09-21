import { useEffect, useState } from 'react'
import { listTimesfmPredictions, type TimesfmPredictionsResponse } from '../../api/timesfm'

type Props = { symbol: string; tf: string }

// Replaces SignalPreviewChart for the timesfm signal: that chart recomputes Compute() over
// every past candle, which timesfm's own signal deliberately never supports (Compute()
// always returns Neutral — only ComputeWithSymbol, backed by the live cache, does anything —
// same reason whale/leverage/bybit-news are hidden from the default catalog filter, see
// WebhooksPage.tsx's DEFAULT_CATS comment). This shows the current cached forecast plus a
// running accuracy log instead.
export function TimesfmForecastPanel({ symbol, tf }: Props) {
  const [data, setData] = useState<TimesfmPredictionsResponse | null>(null)
  const [error, setError] = useState<string | null>(null)

  useEffect(() => {
    setData(null)
    setError(null)
    let cancelled = false
    listTimesfmPredictions(symbol, tf)
      .then(res => { if (!cancelled) setData(res) })
      .catch((err: unknown) => {
        if (!cancelled) setError(err instanceof Error ? err.message : 'Не удалось загрузить прогнозы')
      })
    return () => { cancelled = true }
  }, [symbol, tf])

  if (error !== null) {
    return <div className="px-3 py-2 text-[11px] text-rose-400">{error}</div>
  }

  if (data === null) {
    return <div className="px-3 py-2 text-[11px] text-slate-500">Загрузка…</div>
  }

  const latest = data.predictions[0]

  return (
    <div className="flex h-full flex-col overflow-hidden">
      <div className="border-b border-white/[.06] px-3 py-2">
        <div className="text-[11px] text-slate-500">Текущий прогноз ({symbol}, {tf})</div>
        {latest ? (
          <div className="mt-1 flex items-center gap-2 text-[12px]">
            <span
              className={
                latest.predicted_direction === 'buy'
                  ? 'text-emerald-400'
                  : latest.predicted_direction === 'sell'
                  ? 'text-rose-400'
                  : 'text-slate-400'
              }
            >
              {latest.predicted_direction.toUpperCase()}
            </span>
            <span className="text-slate-300">{latest.predicted_pct.toFixed(2)}%</span>
            <span className="text-slate-600">{new Date(latest.predicted_at).toLocaleString('ru-RU')}</span>
          </div>
        ) : (
          <div className="mt-1 text-[12px] text-slate-600">Прогнозов ещё не было</div>
        )}
        <div className="mt-1 text-[11px] text-slate-500">
          Точность: {data.checked > 0 ? `${data.win_rate.toFixed(0)}% (${data.correct}/${data.checked})` : '—'}
        </div>
      </div>
      <div className="flex-1 overflow-auto px-3 py-2">
        {data.predictions.length === 0 ? (
          <div className="text-[11px] text-slate-500">История пуста</div>
        ) : (
          data.predictions.map(p => (
            <div
              key={p.id}
              className="flex items-center justify-between gap-2 border-b border-white/[.04] py-1 text-[11px] last:border-0"
            >
              <span className="text-slate-500">{new Date(p.predicted_at).toLocaleString('ru-RU')}</span>
              <span
                className={
                  p.predicted_direction === 'buy'
                    ? 'text-emerald-400'
                    : p.predicted_direction === 'sell'
                    ? 'text-rose-400'
                    : 'text-slate-400'
                }
              >
                {p.predicted_direction}
              </span>
              <span className="text-slate-600">
                {p.correct === null ? 'ожидание' : p.correct ? 'верно' : 'неверно'}
              </span>
            </div>
          ))
        )}
      </div>
    </div>
  )
}
