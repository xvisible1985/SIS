import { useMemo } from 'react'
import { Shield } from 'lucide-react'
import type { Bot } from '../../features/bots/types'
import type { Position } from '../../types'

// ── Label helpers ─────────────────────────────────────────────────────────────

const ACT_LABELS = ['От посл. ордера', 'Просадка', 'PnL', 'ROI']
const ACT_UNITS  = ['%', '%', ' USDT', '%']

function actLabel(type: number) { return ACT_LABELS[type] ?? '—' }
function actUnit(type: number)  { return ACT_UNITS[type]  ?? '' }

/**
 * Compute the current activation metric value for a position.
 *
 * Sign convention:
 *   type 0 (last_order%): NEGATIVE when price moved against the position.
 *     SHORT: price UP → negative (bad). LONG: price DOWN → negative (bad).
 *   type 1 (drawdown%):   POSITIVE when position is losing (drawdown amount).
 *   type 2 (pnl$):        raw pnl (negative = losing).
 *   type 3 (roi%):        raw roi% (negative = losing).
 *
 * Uses tickerPrice (real-time) when available; falls back to pos.markPrice.
 */
function currentMetric(pos: Position, actType: number, tickerPrice?: number): number {
  const entry = parseFloat(pos.entryPrice)
  const mark  = tickerPrice && tickerPrice > 0 ? tickerPrice : parseFloat(pos.markPrice)
  const pnl   = parseFloat(pos.unrealisedPnl)
  const lev   = parseFloat(pos.leverage) || 1
  const size  = parseFloat(pos.size)

  switch (actType) {
    case 0: // last_order% — negative when against position
      // SHORT: losing when mark > entry → (entry - mark) / entry is negative when rising
      // LONG:  losing when mark < entry → (mark - entry) / entry is negative when falling
      return pos.side === 'Buy'
        ? (mark - entry) / entry * 100
        : (entry - mark) / entry * 100
    case 1: // drawdown% — positive when losing
      return pos.side === 'Buy'
        ? (entry - mark) / entry * 100
        : (mark - entry) / entry * 100
    case 2: // pnl$
      return pnl
    case 3: { // roi%
      const margin = entry * size / lev
      return margin > 0 ? pnl / margin * 100 : 0
    }
    default:
      return 0
  }
}

/** Returns true if the symbol passes a hedge bot's whitelist/blacklist. */
function symbolPassesFilter(symbol: string, whitelist: string[], blacklist: string[]): boolean {
  if (blacklist.includes(symbol)) return false
  if (whitelist.length === 0) return true
  return whitelist.includes(symbol)
}

// ── Component ─────────────────────────────────────────────────────────────────

interface Props {
  symbol:       string
  positions:    Position[]
  bots:         Bot[]
  accountId:    string | null
  tickerPrices: Map<string, number>
}

export function HedgeBotOverlay({ symbol, positions, bots, accountId, tickerPrices }: Props) {
  const items = useMemo(() => {
    if (!accountId) return []

    // Active hedge bots linked to the current account
    const hedgeBots = bots.filter(b =>
      b.status === 'active' &&
      b.accountId === accountId &&
      b.strategyConfig.bot_kind === 'hedge' &&
      symbolPassesFilter(symbol, b.symbolWhitelist, b.symbolBlacklist),
    )
    if (hedgeBots.length === 0) return []

    // Positions for current symbol (exclude zero-size)
    const symPositions = positions.filter(p =>
      p.symbol === symbol && parseFloat(p.size) > 0,
    )
    if (symPositions.length === 0) return []

    return hedgeBots.map(bot => {
      const cfg = bot.strategyConfig
      const actType  = cfg.hedge_act_type  ?? 1
      const actValue = cfg.hedge_act_value ?? 0
      const tickerPrice = tickerPrices.get(symbol)

      const posRows = symPositions
        .filter(p => {
          const dir = p.side === 'Buy' ? 'long' : 'short'
          const botDir = cfg.direction ?? 'both'
          return botDir === 'both' || botDir === dir
        })
        .map(p => {
          const value = currentMetric(p, actType, tickerPrice)

          // Triggered logic per type:
          //   type 0 (last_order%): value is negative when against position;
          //     actValue stored as negative (e.g. -4). Trigger when |value| >= |actValue|.
          //   type 1 (drawdown%):   value is positive when losing;
          //     actValue stored as positive (e.g. 5). Trigger when value >= actValue.
          //   type 2 (pnl$) / type 3 (roi%): trigger when value <= -|actValue|.
          let triggered: boolean
          if (actType === 2 || actType === 3) {
            triggered = value <= -Math.abs(actValue)
          } else if (actType === 0) {
            // Negative convention: both value and threshold are negative when bad.
            // Triggered when value crosses threshold (value <= actValue, both negative).
            triggered = value <= actValue
          } else {
            // type 1: positive convention.
            triggered = value >= Math.abs(actValue)
          }

          return { pos: p, value, triggered }
        })

      return { bot, actType, actValue, posRows }
    }).filter(item => item.posRows.length > 0)
  }, [symbol, positions, bots, accountId, tickerPrices])

  if (items.length === 0) return null

  // right-[68px]: keeps the panel left of the price-scale column (~60 px wide)
  return (
    <div className="absolute top-2 right-[68px] z-20 flex flex-col gap-1.5 pointer-events-none select-none">
      {items.map(({ bot, actType, actValue, posRows }) => {
        const anyTriggered = posRows.some(r => r.triggered)
        return (
          <div
            key={bot.id}
            className="rounded-[10px] border px-2.5 py-2 shadow-[0_4px_16px_-4px_rgba(0,0,0,.6)] backdrop-blur-[4px] min-w-[180px]"
            style={{
              background:   anyTriggered ? 'rgba(90,38,8,0.46)' : 'rgba(6,6,12,0.50)',
              borderColor:  anyTriggered ? 'rgba(251,146,60,0.38)' : 'rgba(245,158,11,0.18)',
            }}
          >
            {/* Header */}
            <div className="flex items-center gap-1.5 mb-1.5">
              <Shield
                size={11}
                className={anyTriggered ? 'text-amber-300' : 'text-amber-500/70'}
                strokeWidth={2.2}
              />
              <span
                className="text-[11px] font-semibold truncate max-w-[160px]"
                style={{ color: anyTriggered ? '#fcd34d' : '#d97706' }}
              >
                {bot.name}
              </span>
              {anyTriggered && (
                <span className="ml-auto shrink-0 rounded-full px-1.5 py-px text-[9px] font-bold uppercase tracking-wider text-amber-900"
                  style={{ background: 'rgba(251,191,36,0.9)' }}>
                  ●&nbsp;актив.
                </span>
              )}
            </div>

            {/* Divider */}
            <div className="mb-1.5 h-px" style={{ background: 'rgba(245,158,11,0.18)' }} />

            {/* Position rows */}
            {posRows.map(({ pos, value, triggered }) => {
              const isLong = pos.side === 'Buy'
              const unit   = actUnit(actType)
              // Format with sign: show + for positive, − for negative (explicit).
              const fmtVal = unit === ' USDT'
                ? `${value >= 0 ? '+' : ''}${value.toFixed(2)} USDT`
                : `${value >= 0 ? '+' : ''}${value.toFixed(2)}%`

              return (
                <div key={pos.side} className="flex items-baseline justify-between gap-2 py-[2px]">
                  {/* Direction badge */}
                  <span
                    className="shrink-0 rounded-[3px] px-1 py-px text-[9px] font-bold uppercase tracking-wider"
                    style={isLong
                      ? { color: '#34d399', background: 'rgba(52,211,153,0.12)' }
                      : { color: '#f87171', background: 'rgba(248,113,113,0.12)' }}
                  >
                    {isLong ? 'LONG' : 'SHORT'}
                  </span>

                  {/* Metric label */}
                  <span className="text-[10px] text-slate-400 flex-1 text-right">
                    {actLabel(actType)}
                  </span>

                  {/* Current value */}
                  <span
                    className="shrink-0 font-mono text-[11px] font-semibold tabular-nums"
                    style={{ color: triggered ? '#fbbf24' : '#94a3b8' }}
                  >
                    {fmtVal}
                  </span>

                  {/* Threshold */}
                  <span className="shrink-0 text-[10px] text-slate-600">
                    /&nbsp;{actValue}{unit}
                  </span>
                </div>
              )
            })}
          </div>
        )
      })}
    </div>
  )
}
