import { useState, useEffect, useCallback, useMemo } from 'react'
import { listStrategies } from '../api/strategies'
import { listAccounts } from '../api/accounts'
import { StrategyCard } from '../components/strategies/StrategyCard'
import { HedgePairCard } from '../components/strategies/HedgePairCard'
import { StrategyModal } from '../components/strategies/StrategyModal'
import { MatrixDebugOverlay } from '../components/strategies/MatrixDebugOverlay'
import { useSelectedAccount } from '../contexts/AccountContext'
import { usePositionsWs } from '../hooks/terminal/usePositionsWs'
import { useBots } from '../features/bots/api'
import type { Strategy, ExchangeAccount } from '../types'

const STATUS_ORDER: Record<string, number> = { active: 0, finishing: 1, stopped: 2 }
function sortStrategies(list: Strategy[]): Strategy[] {
  return [...list].sort((a, b) => {
    const sd = (STATUS_ORDER[a.status] ?? 9) - (STATUS_ORDER[b.status] ?? 9)
    return sd !== 0 ? sd : a.symbol.localeCompare(b.symbol)
  })
}

export function StrategiesPage() {
  const { selectedAccountId } = useSelectedAccount()
  const { positions, orders } = usePositionsWs(selectedAccountId || null)
  const { mine: hedgeBots } = useBots()

  const [strategies, setStrategies] = useState<Strategy[]>([])
  const [accounts, setAccounts] = useState<ExchangeAccount[]>([])
  const [loading, setLoading] = useState(true)
  const [loadError, setLoadError] = useState<string | null>(null)
  const [modalOpen, setModalOpen] = useState(false)
  const [editTarget, setEditTarget] = useState<Strategy | undefined>()
  const [editFilledCount, setEditFilledCount] = useState(0)
  const [expandedId, setExpandedId] = useState<string | null>(null)
  const [selectedId, setSelectedId] = useState<string | null>(null)
  const [matrixDebugOpen, setMatrixDebugOpen] = useState(false)

  const load = useCallback(async () => {
    setLoading(true)
    setLoadError(null)
    try {
      const [strats, accs] = await Promise.all([listStrategies(), listAccounts()])
      setStrategies(strats)
      setAccounts(accs)
    } catch (e: unknown) {
      setStrategies([])
      setLoadError(e instanceof Error ? e.message : 'Ошибка загрузки стратегий')
    } finally {
      setLoading(false)
    }
  }, [])

  useEffect(() => { load() }, [load])

  // Real-time strategy status updates via WebSocket.
  // Backend pushes [{id, status}] whenever any strategy status changes (2s poll on server).
  useEffect(() => {
    let ws: WebSocket | null = null
    let reconnectTimer: ReturnType<typeof setTimeout> | null = null

    function connect() {
      const token = localStorage.getItem('token') ?? ''
      const proto = window.location.protocol === 'https:' ? 'wss:' : 'ws:'
      ws = new WebSocket(`${proto}//${window.location.host}/ws/strategies/updates?token=${encodeURIComponent(token)}`)

      ws.onmessage = (evt) => {
        try {
          const updates: { id: string; status: string; manual_alert?: string }[] = JSON.parse(evt.data as string)
          if (updates.length > 0) {
            const deletedIds = new Set(updates.filter(u => u.status === 'deleted').map(u => u.id))
            setStrategies(prev => {
              let next = prev.filter(s => !deletedIds.has(s.id))
              next = next.map(s => {
                const upd = updates.find(u => u.id === s.id && u.status !== 'deleted')
                return upd ? { ...s, status: upd.status as Strategy['status'], manual_alert: upd.manual_alert } : s
              })
              return next
            })
          }
        } catch { /* ignore malformed */ }
      }

      ws.onclose = () => {
        reconnectTimer = setTimeout(connect, 5000)
      }
      ws.onerror = () => ws?.close()
    }

    connect()
    return () => {
      if (reconnectTimer) clearTimeout(reconnectTimer)
      if (ws) { ws.onclose = null; ws.close() }
    }
  }, [])

  const hedgeBotIds = useMemo(
    () => new Set(hedgeBots.filter(b => b.strategyConfig.bot_kind === 'hedge').map(b => b.id)),
    [hedgeBots],
  )

  const hedgeInfoMap = useMemo(() => {
    const map = new Map<string, { hasActiveHedge: boolean; isHedgeItself: boolean }>()
    for (const s of strategies) {
      // A strategy is "hedge itself" if:
      //  - it was created by a hedge bot (bot_id present and in hedgeBotIds), OR
      //  - it is stopped and has hedged_strategy_id set (old hedge activations created
      //    before bot_id was reliably populated — they must not appear as orphan cards).
      const isHedgeItself =
        (!!s.bot_id && hedgeBotIds.has(s.bot_id)) ||
        (s.status === 'stopped' && !!s.hedged_strategy_id)
      let hasActiveHedge = false
      // Only active/finishing strategies can be mains; stopped strategies should
      // not be treated as "has active hedge" to avoid spurious pair cards.
      if (!isHedgeItself && (s.status === 'active' || s.status === 'finishing')) {
        const oppDir = s.direction === 'long' ? 'short' : 'long'
        hasActiveHedge = strategies.some(h =>
          !!h.bot_id &&
          hedgeBotIds.has(h.bot_id) &&
          h.symbol === s.symbol &&
          h.account_id === s.account_id &&
          h.direction === oppDir &&
          (h.status === 'active' || h.status === 'finishing'),
        )
      }
      map.set(s.id, { hasActiveHedge, isHedgeItself })
    }
    return map
  }, [strategies, hedgeBotIds])

  const renderItems = useMemo(() => {
    const sorted = sortStrategies(strategies)
    const usedIds = new Set<string>()
    const items: Array<
      | { type: 'single'; strategy: Strategy }
      | { type: 'pair'; main: Strategy; hedge: Strategy }
    > = []
    for (const s of sorted) {
      if (usedIds.has(s.id)) continue

      // Method 1: direct link via hedged_strategy_id
      if (s.hedged_strategy_id) {
        const partner = sorted.find(h => !usedIds.has(h.id) && h.id === s.hedged_strategy_id)
        // Skip stopped-stopped pairs — they are stale historical records from old hedge
        // activation cycles. At least one side must be active/finishing to render a pair card.
        if (partner && !(s.status === 'stopped' && partner.status === 'stopped')) {
          usedIds.add(s.id)
          usedIds.add(partner.id)
          const sIsHedge = !!hedgeInfoMap.get(s.id)?.isHedgeItself
          const [main, hedge] = sIsHedge ? [partner, s] : [s, partner]
          items.push({ type: 'pair', main, hedge })
          continue
        }
      }

      // Method 2: hedgeInfoMap detection
      const info = hedgeInfoMap.get(s.id)
      if (info?.hasActiveHedge || info?.isHedgeItself) {
        const isMain = !!info.hasActiveHedge
        const partner = sorted.find(h =>
          !usedIds.has(h.id) &&
          h.symbol === s.symbol &&
          h.account_id === s.account_id &&
          h.direction !== s.direction &&
          (isMain
            ? hedgeInfoMap.get(h.id)?.isHedgeItself
            : hedgeInfoMap.get(h.id)?.hasActiveHedge),
        )
        if (partner) {
          usedIds.add(s.id)
          usedIds.add(partner.id)
          const [main, hedge] = isMain ? [s, partner] : [partner, s]
          items.push({ type: 'pair', main, hedge })
          continue
        }
      }

      usedIds.add(s.id)
      // Hedge-bot strategies (isHedgeItself) that couldn't pair are suppressed —
      // they're managed from HedgePairCard. When bot is detached, bot_id becomes
      // null → isHedgeItself = false → strategy reappears as a standalone card.
      if (hedgeInfoMap.get(s.id)?.isHedgeItself) continue
      items.push({ type: 'single', strategy: s })
    }

    // Post-filter: suppress stopped standalone strategies whose symbol+account+direction
    // matches the hedge side of an active pair — these are "ghost" hedges left after a
    // detach → bot-creates-new-hedge cycle. Active/finishing singles are never suppressed.
    const hedgeCoveredCombos = new Set<string>()
    for (const item of items) {
      if (item.type === 'pair') {
        hedgeCoveredCombos.add(`${item.hedge.symbol}:${item.hedge.account_id}:${item.hedge.direction}`)
      }
    }
    return items.filter(item => {
      if (item.type !== 'single') return true
      const s = item.strategy
      if (s.status !== 'stopped') return true
      return !hedgeCoveredCombos.has(`${s.symbol}:${s.account_id}:${s.direction}`)
    })
  }, [strategies, hedgeInfoMap])

  function handleSelect(s: Strategy) {
    setSelectedId(s.id)
    setExpandedId(prev => (prev !== null && prev !== s.id ? null : prev))
  }

  function openCreate() {
    setEditTarget(undefined)
    setModalOpen(true)
  }

  function openEdit(s: Strategy, filledCount = 0) {
    setEditTarget(s)
    setEditFilledCount(filledCount)
    setModalOpen(true)
  }

  function handleSaved() {
    setModalOpen(false)
    load()
  }

  return (
    <div className="max-w-[739px] mx-auto space-y-6">
      <div className="flex items-center justify-between">
        <h1 className="text-xl font-semibold text-gray-900 dark:text-white">Стратегии</h1>
        <div className="flex items-center gap-2">
          {strategies.some(s => s.strategy_type === 'matrix') && (
            <button
              onClick={() => setMatrixDebugOpen(true)}
              className="px-3 py-2 bg-gray-700 text-gray-200 text-sm rounded-lg hover:bg-gray-600 transition-colors border border-gray-600"
              title="Matrix Debug Overlay"
            >
              ⊞ Matrix Debug
            </button>
          )}
          <button
            onClick={openCreate}
            className="px-4 py-2 bg-blue-600 text-white text-sm rounded-lg hover:bg-blue-700 transition-colors"
          >
            + Новая стратегия
          </button>
        </div>
      </div>

      <div className="bg-white dark:bg-gray-800 rounded-xl shadow overflow-hidden">
        {loadError ? (
          <div className="p-6 m-4 rounded-xl border border-red-500/30 bg-red-500/10 text-red-400 text-sm flex items-start gap-3">
            <span className="text-base shrink-0">⚠️</span>
            <div className="flex-1">
              <div className="font-semibold mb-1">Ошибка загрузки стратегий</div>
              <div className="font-mono text-xs opacity-80">{loadError}</div>
              <button onClick={load} className="mt-2 text-xs font-semibold px-3 py-1 rounded-md bg-red-500/20 border border-red-500/30 hover:bg-red-500/30 cursor-pointer">
                Повторить
              </button>
            </div>
          </div>
        ) : loading && strategies.length === 0 ? (
          <div className="p-10 text-center text-gray-400">Загрузка…</div>
        ) : strategies.length === 0 ? (
          <div className="p-10 text-center text-gray-400">
            Нет стратегий. Нажмите «+ Новая стратегия».
          </div>
        ) : (
          <ul className="divide-y divide-gray-100 dark:divide-gray-700">
            {renderItems.map(item => {
              if (item.type === 'pair') {
                return (
                  <li key={`pair-${item.main.id}`} className="p-2">
                    <HedgePairCard
                      main={item.main}
                      hedge={item.hedge}
                      accounts={accounts}
                      orders={orders}
                      positions={positions}
                      selectedStrategyId={selectedId}
                      hedgeBot={hedgeBots.find(b => b.id === item.hedge.bot_id) ?? null}
                      onEdit={openEdit}
                      onChanged={load}
                      onSelect={handleSelect}
                    />
                  </li>
                )
              }
              const s = item.strategy
              return (
                <StrategyCard
                  key={s.id}
                  strategy={s}
                  accounts={accounts}
                  orders={orders}
                  positions={positions}
                  onEdit={openEdit}
                  onChanged={load}
                  selected={s.id === selectedId}
                  onSelect={handleSelect}
                  isOpen={s.id === expandedId}
                  onToggleOpen={() => {
                    const isExpanding = expandedId !== s.id
                    setExpandedId(isExpanding ? s.id : null)
                    if (isExpanding) setSelectedId(s.id)
                  }}
                />
              )
            })}
          </ul>
        )}
      </div>

      {modalOpen && (
        <StrategyModal
          strategy={editTarget}
          filledLevels={editFilledCount}
          onClose={() => setModalOpen(false)}
          onSaved={handleSaved}
        />
      )}

      {matrixDebugOpen && (
        <MatrixDebugOverlay
          strategies={strategies}
          onClose={() => setMatrixDebugOpen(false)}
        />
      )}
    </div>
  )
}
