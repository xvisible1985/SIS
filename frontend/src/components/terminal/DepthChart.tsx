import { useMemo, useRef, useEffect, useState } from 'react'
import type { BookRow } from '../../hooks/terminal/useOrderbook'

interface Props {
  bids: BookRow[]
  asks: BookRow[]
}

const PAD = { top: 24, right: 12, bottom: 32, left: 52 }

function formatVol(v: number): string {
  if (v >= 1_000_000) return (v / 1_000_000).toFixed(2) + 'M'
  if (v >= 1_000) return (v / 1_000).toFixed(1) + 'k'
  return v.toFixed(2)
}

export function DepthChart({ bids, asks }: Props) {
  const containerRef = useRef<HTMLDivElement>(null)
  const [size, setSize] = useState({ w: 0, h: 0 })
  const [isDark, setIsDark] = useState(document.documentElement.classList.contains('dark'))

  useEffect(() => {
    const obs = new MutationObserver(() =>
      setIsDark(document.documentElement.classList.contains('dark'))
    )
    obs.observe(document.documentElement, { attributes: true, attributeFilter: ['class'] })
    return () => obs.disconnect()
  }, [])

  useEffect(() => {
    if (!containerRef.current) return
    const ro = new ResizeObserver(entries => {
      const { width, height } = entries[0].contentRect
      setSize({ w: width, h: height })
    })
    ro.observe(containerRef.current)
    return () => ro.disconnect()
  }, [])

  const chart = useMemo(() => {
    if (!bids.length || !asks.length || size.w < 20 || size.h < 20) return null

    const W = size.w - PAD.left - PAD.right
    const H = size.h - PAD.top - PAD.bottom

    const asksCum: { price: number; vol: number }[] = []
    let cum = 0
    for (const [p, s] of asks) {
      cum += parseFloat(s)
      asksCum.push({ price: parseFloat(p), vol: cum })
    }

    const bidsCum: { price: number; vol: number }[] = []
    cum = 0
    for (const [p, s] of bids) {
      cum += parseFloat(s)
      bidsCum.push({ price: parseFloat(p), vol: cum })
    }

    const minPrice = bidsCum[bidsCum.length - 1].price
    const maxPrice = asksCum[asksCum.length - 1].price
    const priceRange = maxPrice - minPrice || 1

    const maxVol = Math.max(
      bidsCum[bidsCum.length - 1].vol,
      asksCum[asksCum.length - 1].vol,
    )

    const px = (price: number) => PAD.left + ((price - minPrice) / priceRange) * W
    const py = (vol: number) => PAD.top + H - (vol / maxVol) * H
    const baseline = PAD.top + H

    // Asks staircase: right and up
    let asksD = `M ${px(asksCum[0].price)} ${baseline}`
    for (let i = 0; i < asksCum.length; i++) {
      asksD += ` V ${py(asksCum[i].vol)}`
      if (i < asksCum.length - 1) asksD += ` H ${px(asksCum[i + 1].price)}`
    }
    asksD += ` H ${px(maxPrice)} V ${baseline} Z`

    // Bids staircase: left and up
    let bidsD = `M ${px(bidsCum[0].price)} ${baseline}`
    for (let i = 0; i < bidsCum.length; i++) {
      bidsD += ` V ${py(bidsCum[i].vol)}`
      if (i < bidsCum.length - 1) bidsD += ` H ${px(bidsCum[i + 1].price)}`
    }
    bidsD += ` H ${px(minPrice)} V ${baseline} Z`

    const bestBid = bidsCum[0].price
    const bestAsk = asksCum[0].price
    const midX = (px(bestBid) + px(bestAsk)) / 2
    const spread = (bestAsk - bestBid).toFixed(2)

    const volTicks = [0.25, 0.5, 0.75, 1].map(f => ({
      y: py(maxVol * f),
      label: formatVol(maxVol * f),
    }))

    const priceTicks = [
      { x: px(minPrice), label: minPrice.toFixed(0), anchor: 'start' as const },
      { x: px(bestBid), label: bestBid.toFixed(1), anchor: 'end' as const },
      { x: px(bestAsk), label: bestAsk.toFixed(1), anchor: 'start' as const },
      { x: px(maxPrice), label: maxPrice.toFixed(0), anchor: 'end' as const },
    ]

    return { asksD, bidsD, midX, spread, volTicks, priceTicks, baseline }
  }, [bids, asks, size])

  const bg = isDark ? '#0a0a0f' : '#ffffff'
  const textC = isDark ? '#6b7280' : '#9ca3af'
  const gridC = isDark ? '#1a1a2e' : '#f3f4f6'
  const midC = isDark ? '#374151' : '#d1d5db'

  return (
    <div ref={containerRef} className="w-full h-full">
      {size.w > 0 && (
        <svg width={size.w} height={size.h} style={{ display: 'block' }}>
          <rect width={size.w} height={size.h} fill={bg} />
          {chart ? (
            <>
              {chart.volTicks.map((t, i) => (
                <line key={i} x1={PAD.left} y1={t.y} x2={size.w - PAD.right} y2={t.y}
                  stroke={gridC} strokeWidth={1} />
              ))}

              <path d={chart.bidsD} fill="#00DC82" fillOpacity={isDark ? 0.18 : 0.12} stroke="#00DC82" strokeWidth={1.5} />
              <path d={chart.asksD} fill="#ef4444" fillOpacity={isDark ? 0.18 : 0.12} stroke="#ef4444" strokeWidth={1.5} />

              <line x1={chart.midX} y1={PAD.top} x2={chart.midX} y2={chart.baseline}
                stroke={midC} strokeWidth={1} strokeDasharray="4,3" />
              <text x={chart.midX} y={PAD.top - 6} textAnchor="middle" fontSize={10} fill={textC}>
                spread {chart.spread}
              </text>

              {chart.volTicks.map((t, i) => (
                <text key={i} x={PAD.left - 6} y={t.y + 4} textAnchor="end" fontSize={10} fill={textC}>
                  {t.label}
                </text>
              ))}

              {chart.priceTicks.map((t, i) => (
                <text key={i} x={t.x} y={size.h - 8} textAnchor={t.anchor} fontSize={10} fill={textC}>
                  {t.label}
                </text>
              ))}
            </>
          ) : (
            <text x={size.w / 2} y={size.h / 2} textAnchor="middle" fontSize={12} fill={textC}>
              Загрузка…
            </text>
          )}
        </svg>
      )}
    </div>
  )
}
