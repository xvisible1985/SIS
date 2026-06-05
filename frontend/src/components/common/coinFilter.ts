// frontend/src/components/common/coinFilter.ts
import { getCoinFilter } from '../../features/admin-defaults/api'
import type { CoinFilterSettings } from '../../features/admin-defaults/types'

export { getCoinFilter }
export type { CoinFilterSettings }

function fmtVol(v: number): string {
  if (v >= 1e9) return (v / 1e9).toFixed(1) + 'B'
  if (v >= 1e6) return (v / 1e6).toFixed(0) + 'M'
  if (v >= 1e3) return (v / 1e3).toFixed(0) + 'K'
  return v.toFixed(0)
}

/**
 * Given a symbol, its 24-hour turnover in USDT, and the current filter settings,
 * returns whether the coin is flagged and a human-readable reason.
 *
 * @param symbol   e.g. "PEPEUSDT"
 * @param turnover 24h USDT volume
 * @param settings loaded from getCoinFilter()
 */
export function checkCoinFlagged(
  symbol: string,
  turnover: number,
  settings: CoinFilterSettings,
): { flagged: boolean; reason: string } {
  if ((settings.blacklist ?? []).includes(symbol)) {
    return { flagged: true, reason: 'В чёрном списке' }
  }
  if (settings.min_turnover_usdt > 0 && turnover < settings.min_turnover_usdt) {
    return {
      flagged: true,
      reason: `Объём ${fmtVol(turnover)} < ${fmtVol(settings.min_turnover_usdt)}`,
    }
  }
  return { flagged: false, reason: '' }
}
