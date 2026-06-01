// frontend/src/features/log-visualizer/utils.ts

import type { LVEvent, LVLevel, MergedEvent, LayerSettings } from './types'

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

/** Filter merged events according to current layer settings. */
export function filterEvents(events: MergedEvent[], settings: LayerSettings): MergedEvent[] {
  return events.filter(ev => {
    if (ev.kind === 'level') return settings.showOrderMarkers
    if (ev.kind === 'log') {
      if (!settings.showLogMarkers) return false
      const lvl = ev.log?.level
      if (lvl === 'info'  && !settings.showInfo)  return false
      if (lvl === 'warn'  && !settings.showWarn)  return false
      if (lvl === 'error' && !settings.showError) return false
      return true
    }
    return true
  })
}

/** Compute filled-order statistics from the currently visible events. */
export function computeCardStats(visibleEvents: MergedEvent[]): {
  filledCount: number
  volumeUsdt:  number
} {
  const filledLevels = visibleEvents.filter(ev => ev.kind === 'level')
  return {
    filledCount: filledLevels.length,
    volumeUsdt:  filledLevels.reduce((sum, ev) => sum + (ev.level?.sizeUsdt ?? 0), 0),
  }
}

/** Format a PnL number with sign and 2 decimal places, e.g. "+124.50 $" */
export function formatPnl(pnl: number): string {
  return (pnl >= 0 ? '+' : '') + pnl.toFixed(2) + ' $'
}
