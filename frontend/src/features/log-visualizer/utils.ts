// frontend/src/features/log-visualizer/utils.ts

import type { LVEvent, LVLevel } from './types'

/** Returns label for display in events list and info panel. */
export function makeMergedEventLabel(
  kind: 'log' | 'level',
  log?: LVEvent,
  level?: LVLevel,
): string {
  if (kind === 'level' && level) {
    const dir = level.side === 'Buy' ? '▲' : '▼'
    const status = level.status === 'sl_closed' ? ' [SL]' : ''
    return `${dir} L${level.levelIdx} ${level.filledPrice.toFixed(4)} · ${level.qty}${status}`
  }
  return log?.message ?? '—'
}
