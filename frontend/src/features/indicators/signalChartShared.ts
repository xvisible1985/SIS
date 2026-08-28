// Shared between SignalChartPage.tsx and features/webhooks/SignalPreviewChart.tsx —
// fetching a catalog signal's fire history and rendering it as TradingView-style Buy/Sell
// labels on a lightweight-charts candlestick series.

export interface ChartEvent {
  time: number
  state: 'buy' | 'sell' | 'neutral'
  price: number
}

export function toBybitTF(tf: string): string {
  const m: Record<string, string> = {
    '1m': '1', '5m': '5', '15m': '15', '30m': '30',
    '1h': '60', '4h': '240', '1D': 'D',
  }
  return m[tf] ?? tf
}

export async function loadSignalHistory(
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

export interface SignalLabel {
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

export class SignalLabelsPrimitive {
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
