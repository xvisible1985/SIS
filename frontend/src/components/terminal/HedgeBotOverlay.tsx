import { useMemo } from 'react'
import { Shield } from 'lucide-react'
import type { Bot } from '../../features/bots/types'
import type { Position, Strategy } from '../../types'

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
 * For type 0 (last_order%), uses lastFilledPrice when provided (matches backend logic).
 */
function currentMetric(pos: Position, actType: number, tickerPrice?: number, lastFilledPrice?: number): number {
  // type 0: backend measures from last filled grid level, not avg entry
  const entry = (actType === 0 && lastFilledPrice && lastFilledPrice > 0)
    ? lastFilledPrice
    : parseFloat(pos.entryPrice)
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

// ── Component ─────────────────────────────────────────────────────────────────

interface Props {
  symbol:       string
  positions:    Position[]
  bots:         Bot[]
  accountId:    string | null
  tickerPrices: Map<string, number>
  strategies:   Strategy[]
}

export function HedgeBotOverlay({ symbol, positions, bots, accountId, tickerPrices, strategies }: Props) {
  const items = useMemo(() => {
    if (!accountId) return []

    // Find bot_id of the strategy currently managing this symbol on this account
    const currentStrategy = strategies.find(s =>
      s.symbol === symbol && s.account_id === accountId,
    )
    const stratBotId = currentStrategy?.bot_id ?? null

    // Active hedge bots linked to the current account that are allowed to monitor this strategy.
    // Blacklists always block; whitelists use OR when both are non-empty.
    const hedgeBots = bots.filter(b => {
      if (b.status !== 'active' || b.accountId !== accountId || b.strategyConfig.bot_kind !== 'hedge') return false
      const symBL = b.symbolBlacklist
      const symWL = b.symbolWhitelist
      const botBL = b.strategyConfig.hedge_bot_blacklist ?? []
      const botWL = b.strategyConfig.hedge_bot_whitelist ?? []

      // Blacklists always block
      if (symBL.includes(symbol)) return false
      if (stratBotId && botBL.includes(stratBotId)) return false

      // Whitelist OR logic
      const hasSymWL = symWL.length > 0
      const hasBotWL = botWL.length > 0
      if (hasSymWL && hasBotWL) {
        const symOK = symWL.includes(symbol)
        const botOK = stratBotId != null && botWL.includes(stratBotId)
        return symOK || botOK
      }
      if (hasSymWL) return symWL.includes(symbol)
      if (hasBotWL) return stratBotId != null && botWL.includes(stratBotId)
      return true
    })
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

      const lastFilledPrice = currentStrategy?.last_filled_price

      const posRows = symPositions
        .filter(p => {
          const dir = p.side === 'Buy' ? 'long' : 'short'
          const botDir = cfg.direction ?? 'both'
          return botDir === 'both' || botDir === dir
        })
        .map(p => {
          const value = currentMetric(p, actType, tickerPrice, lastFilledPrice)

          // Sign convention: negative threshold = drawdown trigger, positive = profit/buffer trigger.
          //
          // type 0 (last_order%): value negative when losing, positive when profitable.
          //   threshold < 0 → trigger when value ≤ threshold (lost enough against position)
          //   threshold > 0 → trigger when value ≥ threshold (gained enough)
          //
          // type 1 (drawdown%): value positive when losing, negative when profitable.
          //   threshold < 0 → trigger when value ≥ -threshold (drawdown ≥ |threshold|)
          //   threshold > 0 → trigger when value ≤ -threshold (profitable by threshold%)
          //
          // type 2/3 (pnl$/roi%): raw value.
          //   threshold < 0 → trigger when value ≤ threshold (loss)
          //   threshold > 0 → trigger when value ≥ threshold (gain)
          let triggered: boolean
          if (actType === 0) {
            triggered = actValue < 0 ? value <= actValue : value >= actValue
          } else if (actType === 1) {
            triggered = actValue < 0 ? value >= -actValue : value <= -actValue
          } else {
            triggered = actValue < 0 ? value <= actValue : value >= actValue
          }

          return { pos: p, value, triggered }
        })

      return { bot, actType, actValue, posRows }
    }).filter(item => item.posRows.length > 0)
  }, [symbol, positions, bots, accountId, tickerPrices])

  if (items.length === 0) return null

  return (
    <div className="absolute top-0 right-[68px] z-20 flex flex-col pointer-events-none select-none">
      {items.map(({ bot, actType, actValue, posRows }) => {
        const anyTriggered = posRows.some(r => r.triggered)
        return (
          <div
            key={bot.id}
            className="border px-2.5 py-2 min-w-[180px] rounded-[6px]"
            style={{
              background:  anyTriggered ? 'rgba(90,38,8,0.88)' : 'rgba(6,6,12,0.88)',
              borderColor: anyTriggered ? 'rgba(251,146,60,0.45)' : 'rgba(245,158,11,0.22)',
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
                  <span className="shrink-0 text-[10px] text-slate-400">
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
