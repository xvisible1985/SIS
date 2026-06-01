// frontend/src/features/log-visualizer/LogVisualizerStrategyCard.tsx

import type { LVStrategy, MergedEvent } from './types'
import { computeCardStats, formatPnl } from './utils'

interface Props {
  strategy:      LVStrategy
  visibleEvents: MergedEvent[]
}

export function LogVisualizerStrategyCard({ strategy, visibleEvents }: Props) {
  const { filledCount, volumeUsdt } = computeCardStats(visibleEvents)
  const pnl    = strategy.lastPnl
  const isLong = strategy.direction.toLowerCase() === 'long'

  return (
    <div
      className="absolute bottom-4 right-4 backdrop-blur-sm pointer-events-none select-none"
      style={{
        background:   'rgba(6,6,12,0.85)',
        border:       '1px solid rgba(255,255,255,.10)',
        borderRadius: 10,
        padding:      '10px 12px',
        width:        200,
      }}
    >
      {/* Header row: symbol + direction badge */}
      <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', marginBottom: 3 }}>
        <span style={{ fontSize: 12, fontWeight: 700, color: '#e2e8f0' }}>
          {strategy.symbol}
        </span>
        <span style={{
          fontSize:   9,
          fontWeight: 700,
          padding:    '1px 6px',
          borderRadius: 4,
          color:      isLong ? '#34d399' : '#f87171',
          background: isLong ? 'rgba(52,211,153,.12)' : 'rgba(248,113,113,.12)',
        }}>
          {strategy.direction.toUpperCase()}
        </span>
      </div>

      {/* Subheader: type · status */}
      <div style={{ fontSize: 10, color: '#64748b', marginBottom: 8 }}>
        {strategy.strategyType} · {strategy.status}
      </div>

      {/* Stats grid */}
      <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: '4px 8px', marginBottom: pnl !== null ? 8 : 0 }}>
        <div>
          <div style={{ fontSize: 9, color: '#475569', textTransform: 'uppercase' }}>Взято ордеров</div>
          <div style={{ fontSize: 13, color: '#e2e8f0', fontWeight: 700, fontFamily: 'monospace' }}>
            {filledCount}&nbsp;/&nbsp;{strategy.gridLevels}
          </div>
        </div>
        <div>
          <div style={{ fontSize: 9, color: '#475569', textTransform: 'uppercase' }}>Объём</div>
          <div style={{ fontSize: 13, color: '#e2e8f0', fontWeight: 700, fontFamily: 'monospace' }}>
            {volumeUsdt.toFixed(2)}&nbsp;$
          </div>
        </div>
      </div>

      {/* Last PnL — only shown when available */}
      {pnl !== null && (
        <div style={{ borderTop: '1px solid rgba(255,255,255,.06)', paddingTop: 7 }}>
          <div style={{ fontSize: 9, color: '#475569', textTransform: 'uppercase', marginBottom: 2 }}>
            Last PnL стратегии
          </div>
          <div style={{
            fontSize:   14,
            fontWeight: 700,
            fontFamily: 'monospace',
            color:      pnl > 0 ? '#34d399' : pnl < 0 ? '#f87171' : '#94a3b8',
          }}>
            {formatPnl(pnl)}
          </div>
        </div>
      )}
    </div>
  )
}
