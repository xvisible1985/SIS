import type { Position, Strategy } from '../../types'
import { placeOrder } from '../../api/trader'
import { createStrategy, listStrategies } from '../../api/strategies'
import { useState, useEffect, useRef } from 'react'
import { ClosePositionModal, makeCloseConfirm, type CloseConfirm } from '../common/ClosePositionModal'
import { getBotKindMeta } from '../../features/bots/botKindMeta'

interface Props {
  accountId: string
  positions: Position[]
  onSelect: (symbol: string) => void
  loading: boolean
  tickerPrices?: Map<string, number>
}

function coinIcon(s: string) {
  return `https://cdn.jsdelivr.net/gh/spothq/cryptocurrency-icons/32/color/${s.replace(/USDT|USDC|USD$/, '').toLowerCase()}.png`
}

interface PositionOwner {
  name: string
  botKind: string | null
}

function getPositionOwner(pos: Position, strategies: Strategy[], accountId: string): PositionOwner | null {
  const posDir = pos.side === 'Buy' ? 'long' : 'short'
  const match = strategies.find(s =>
    s.account_id === accountId &&
    s.symbol === pos.symbol &&
    (s.direction === posDir || s.direction === 'both')
  )
  if (!match) return null
  if (!match.bot_id) return { name: 'Manual', botKind: null }
  return { name: match.bot_name ?? match.bot_id, botKind: match.bot_kind ?? null }
}

export function PositionsTable({ accountId, positions, onSelect, loading, tickerPrices }: Props) {
  const [closing, setClosing] = useState(false)
  const [confirm, setConfirm] = useState<CloseConfirm | null>(null)
  const [contextMenu, setContextMenu] = useState<{ pos: Position; x: number; y: number } | null>(null)
  const [creatingFor, setCreatingFor] = useState<string | null>(null)
  const [flashMsg, setFlashMsg] = useState<{ text: string; ok: boolean } | null>(null)
  const [strategies, setStrategies] = useState<Strategy[]>([])
  const menuRef = useRef<HTMLDivElement>(null)

  useEffect(() => {
    if (!accountId) return
    listStrategies().then(setStrategies).catch(() => {})
  }, [accountId])

  useEffect(() => {
    if (!contextMenu) return
    function onDown(e: MouseEvent) {
      if (menuRef.current && !menuRef.current.contains(e.target as Node)) setContextMenu(null)
    }
    function onKey(e: KeyboardEvent) {
      if (e.key === 'Escape') setContextMenu(null)
    }
    document.addEventListener('mousedown', onDown)
    document.addEventListener('keydown', onKey)
    return () => { document.removeEventListener('mousedown', onDown); document.removeEventListener('keydown', onKey) }
  }, [contextMenu])

  function handleCloseClick(pos: Position) {
    setConfirm(makeCloseConfirm(pos, accountId))
  }

  async function handleConfirm() {
    if (!confirm) return
    setClosing(true)
    try {
      await placeOrder({
        account_id: confirm.accountId,
        symbol: confirm.pos.symbol,
        category: confirm.pos.category,
        side: confirm.pos.side === 'Buy' ? 'Sell' : 'Buy',
        order_type: 'Market',
        qty: confirm.pos.size,
        reduce_only: true,
        position_idx: confirm.pos.positionIdx,
      })
    } catch { /* WS обновит позицию */ }
    setClosing(false)
    setConfirm(null)
  }

  async function handleCreateStrategy(pos: Position) {
    setContextMenu(null)
    const posDir = pos.side === 'Buy' ? 'long' : 'short'

    // Debug: log all strategies for this symbol to console
    const symStrategies = strategies.filter(s => s.symbol === pos.symbol)
    console.log(`[createStrategy] ${pos.symbol} ${posDir} — все стратегии по паре:`, symStrategies.map(s => ({ id: s.id, dir: s.direction, status: s.status, bot: s.bot_id })))

    const existing = strategies.find(s =>
      s.account_id === accountId &&
      s.symbol === pos.symbol &&
      (s.direction === posDir || s.direction === 'both') &&
      (s.status === 'active' || s.status === 'finishing')
    )
    if (existing) {
      const msg = `Стратегия по ${pos.symbol} уже существует (id=${existing.id?.slice(0,8)} status=${existing.status} dir=${existing.direction})`
      console.warn('[createStrategy] blocked by frontend check:', existing)
      setFlashMsg({ text: msg, ok: false })
      setTimeout(() => setFlashMsg(null), 10000)
      return
    }
    const key = `${pos.symbol}-${pos.side}`
    setCreatingFor(key)
    try {
      await createStrategy({
        account_id: accountId,
        symbol: pos.symbol,
        category: pos.category,
        direction: pos.side === 'Buy' ? 'long' : 'short',
        strategy_type: 'grid',
        adopt_position_data: { size: pos.size, entry_price: pos.entryPrice },
        robot_enabled: false,
        virtual_orders: false,
        entry_order_type: 'limit',
        leverage: parseInt(pos.leverage) || 1,
        grid_size_usdt: pos.sizeUsdt,
        margin_type: 'isolated',
        hedge_mode: pos.positionIdx !== 0,
        steps: [{ price_move_pct: 0, size_pct: 100 }],
        grid_active: 0,
        max_stop_active: 10,
        signal_configs: [],
        tp_pct: 1.5,
        tp_mode: 'total',
        sl_pct: -100,
        sl_type: 'conditional',
        trailing_stop_enabled: false,
        trailing_activation_pct: 1.5,
        trailing_callback_pct: 0.5,
        matrix_levels: [],
        safe_zone_pct: 1.5,
        protected_build: false,
        matrix_entry_level: { size_pct: 100, stop_pct: null, stop_cond_pct: null, stop_replace_pct: null, tp_pct: 1.5 },
        matrix_rebuild_on_sl: false,
        matrix_rebuild_from_entry: false,
        size_as_main: false,
      })
      listStrategies().then(setStrategies).catch(() => {})
      window.dispatchEvent(new CustomEvent('strategy-created'))
      setFlashMsg({ text: `Стратегия ${pos.symbol} создана`, ok: true })
      setTimeout(() => setFlashMsg(null), 4000)
    } catch (e: any) {
      const errMsg = e?.message ?? e?.response?.data?.error ?? 'неизвестная ошибка'
      console.error('[createStrategy] API error:', e)
      setFlashMsg({ text: `Ошибка создания ${pos.symbol}: ${errMsg}`, ok: false })
      setTimeout(() => setFlashMsg(null), 15000)
    } finally {
      setCreatingFor(null)
    }
  }

  if (loading) return (
    <div className="flex items-center justify-center h-full text-gray-500 dark:text-gray-400 text-sm">
      <span className="animate-pulse">Загрузка данных...</span>
    </div>
  )

  if (!positions.length) return (
    <div className="flex flex-col items-center justify-center h-full text-gray-500 dark:text-gray-400 gap-2">
      <span className="text-2xl opacity-30">📭</span>
      <p className="text-sm">Открытых позиций нет</p>
    </div>
  )

  return (
    <div className="relative h-full">
      {confirm && (
        <ClosePositionModal
          confirm={confirm}
          onConfirm={handleConfirm}
          onCancel={() => setConfirm(null)}
          closing={closing}
        />
      )}

      {flashMsg && (
        <div className={`absolute top-2 left-1/2 -translate-x-1/2 z-50 px-3 py-2 rounded text-xs font-medium shadow-lg max-w-[90%] break-words text-center ${flashMsg.ok ? 'bg-green-900/90 text-green-300 border border-green-700/50' : 'bg-red-900/90 text-red-300 border border-red-700/50'}`}>
          {flashMsg.text}
        </div>
      )}

      {contextMenu && (() => {
        const cmDir = contextMenu.pos.side === 'Buy' ? 'long' : 'short'
        const relatedStrategies = strategies.filter(s =>
          s.account_id === accountId &&
          s.symbol === contextMenu.pos.symbol
        )
        const strategyExists = relatedStrategies.some(s =>
          (s.direction === cmDir || s.direction === 'both') &&
          (s.status === 'active' || s.status === 'finishing')
        )
        console.log(`[contextMenu] ${contextMenu.pos.symbol} ${cmDir} — strategyExists=${strategyExists}, стратегии:`, relatedStrategies.map(s => ({ id: s.id?.slice(0,8), dir: s.direction, status: s.status })))
        return (
          <div
            ref={menuRef}
            className="fixed z-50 bg-gray-800 border border-gray-600 rounded shadow-xl py-1 min-w-[170px]"
            style={{ top: contextMenu.y, left: contextMenu.x }}
          >
            <button
              onClick={() => handleCreateStrategy(contextMenu.pos)}
              disabled={!!creatingFor || strategyExists}
              title={strategyExists ? 'Стратегия по этой паре уже существует' : undefined}
              className={`w-full px-3 py-1.5 text-left text-xs transition-colors ${strategyExists ? 'text-gray-500 cursor-not-allowed' : 'text-gray-200 hover:bg-gray-700'}`}
            >
              Создать стратегию
              {strategyExists && <span className="ml-1.5 text-[10px] text-gray-600">— заблокировано (см. консоль)</span>}
            </button>
          </div>
        )
      })()}

      <div className="relative overflow-x-auto">
      <table className="w-full text-xs min-w-[700px]">
        <thead className="sticky top-0 z-10 bg-white dark:bg-gray-900">
          <tr className="text-gray-500 dark:text-gray-400 border-b border-gray-200 dark:border-gray-700">
            {['Символ', 'Сторона', 'Размер', 'Цена входа', 'Mark Price', 'PnL', 'Плечо', 'Владелец', ''].map(h => (
              <th key={h} className="px-3 py-2 font-medium text-left">{h}</th>
            ))}
          </tr>
        </thead>
        <tbody>
          {positions.map(pos => {
            const posKey = `${pos.symbol}-${pos.side}`
            const entry = parseFloat(pos.entryPrice)
            const size = parseFloat(pos.size)
            const liveMarkPrice = tickerPrices?.get(pos.symbol)
            const mark = liveMarkPrice ?? parseFloat(pos.markPrice)
            const pnl = liveMarkPrice != null
              ? (pos.side === 'Buy' ? (mark - entry) : (entry - mark)) * size
              : parseFloat(pos.unrealisedPnl)
            const pnlPct = entry > 0 && size > 0 ? (pnl / (size * entry)) * 100 : 0
            const owner = getPositionOwner(pos, strategies, accountId)
            const ownerMeta = owner?.botKind ? getBotKindMeta(owner.botKind) : null
            return (
              <tr
                key={posKey}
                onClick={() => onSelect(pos.symbol)}
                onContextMenu={e => { e.preventDefault(); setContextMenu({ pos, x: e.clientX, y: e.clientY }) }}
                className="border-b border-gray-200 dark:border-gray-700/50 hover:bg-gray-100 dark:hover:bg-gray-800/60 transition-colors cursor-pointer"
              >
                <td className="px-3 py-2 font-mono font-medium">
                  <div className="flex items-center gap-1.5">
                    <img src={coinIcon(pos.symbol)} className="w-4 h-4 rounded-full shrink-0" onError={e => (e.currentTarget.style.display = 'none')} />
                    {pos.symbol}
                  </div>
                </td>
                <td className="px-3 py-2">
                  <span className={`text-[10px] px-1.5 py-0.5 rounded ${pos.side === 'Buy' ? 'bg-green-500/20 text-green-400' : 'bg-red-500/20 text-red-400'}`}>
                    {pos.side === 'Buy' ? 'Long' : 'Short'}
                  </span>
                </td>
                <td className="px-3 py-2 text-left font-mono">{pos.sizeUsdt.toFixed(2)} <span className="text-gray-500 dark:text-gray-400 text-[10px]">USDT</span></td>
                <td className="px-3 py-2 text-left font-mono">{entry.toFixed(2)}</td>
                <td className="px-3 py-2 text-left font-mono">{mark.toFixed(2)}</td>
                <td className={`px-3 py-2 text-left font-mono font-medium ${pnl >= 0 ? 'text-green-500' : 'text-red-500'}`}>
                  {pnl >= 0 ? '+' : ''}{pnl.toFixed(4)} <span className="text-gray-500 dark:text-gray-400 font-normal">({pnlPct.toFixed(2)}%)</span>
                </td>
                <td className="px-3 py-2 text-left font-mono">{pos.leverage}x</td>
                <td className="px-3 py-2 text-left">
                  {creatingFor === posKey ? (
                    <span className="text-[10px] text-blue-400 animate-pulse">создаём...</span>
                  ) : owner ? (
                    ownerMeta ? (
                      <span
                        className="text-[10px] px-1.5 py-0.5 rounded font-medium"
                        style={{
                          color: ownerMeta.color,
                          background: ownerMeta.bg,
                          border: `1px solid ${ownerMeta.border}`,
                        }}
                      >
                        {owner.name}
                      </span>
                    ) : (
                      <span className="text-[10px] px-1.5 py-0.5 rounded bg-gray-500/15 text-gray-400 border border-gray-500/20">
                        {owner.name}
                      </span>
                    )
                  ) : null}
                </td>
                <td className="px-3 py-2 text-right" onClick={e => e.stopPropagation()}>
                  <button
                    onClick={() => handleCloseClick(pos)}
                    className="text-[10px] px-2 py-0.5 rounded bg-red-500/20 text-red-400 hover:bg-red-500/30 transition-colors"
                  >
                    Закрыть
                  </button>
                </td>
              </tr>
            )
          })}
        </tbody>
      </table>
      </div>
    </div>
  )
}
