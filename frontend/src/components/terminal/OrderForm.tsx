import { useState, useEffect, useCallback } from 'react'
import { placeOrder, cancelOrder, setLeverage } from '../../api/trader'
import type { ActiveOrder } from '../../types'

interface Props {
  accountId: string
  symbol: string
  lastPrice: string | null
  orders: ActiveOrder[]
  hedgeMode: boolean
}

type Tab = 'Limit' | 'Market' | 'Conditional'
type LogEntry = { text: string; status: 'pending' | 'ok' | 'error' }

export function OrderForm({ accountId, symbol, lastPrice, orders, hedgeMode }: Props) {
  const category = symbol.endsWith('USDT') || symbol.endsWith('USDC') ? 'linear' : 'inverse'
  const baseCoin = symbol.replace(/USDT|USDC|USD$/, '')
  const quoteCoin = symbol.endsWith('USDC') ? 'USDC' : 'USDT'

  const [open, setOpen] = useState(true)
  const [tab, setTab] = useState<Tab>('Limit')
  const [side, setSide] = useState<'Buy' | 'Sell'>('Buy')
  const [price, setPrice] = useState('')
  const [qty, setQty] = useState('')
  const [qtyUsdt, setQtyUsdt] = useState('')
  const [triggerPrice, setTriggerPrice] = useState('')
  const [triggerBy, setTriggerBy] = useState('MarkPrice')
  const [triggerDir, setTriggerDir] = useState<1 | 2>(1)
  const [condType, setCondType] = useState<'Market' | 'Limit'>('Market')
  const [tif, setTif] = useState('GTC')
  const [leverage, setLev] = useState('')
  const [reduceOnly, setReduceOnly] = useState(false)
  const [loading, setLoading] = useState(false)
  const [log, setLog] = useState<LogEntry[]>([])
  const [qtyStep, setQtyStep] = useState('1')

  const effectivePrice = useCallback(() => {
    if (tab === 'Limit' || (tab === 'Conditional' && condType === 'Limit'))
      return parseFloat(price) || parseFloat(lastPrice ?? '0') || 0
    return parseFloat(lastPrice ?? '0') || 0
  }, [tab, condType, price, lastPrice])

  useEffect(() => {
    fetch(`https://api.bybit.com/v5/market/instruments-info?category=${category}&symbol=${symbol}`)
      .then(r => r.json())
      .then(j => {
        const step = j.result?.list?.[0]?.lotSizeFilter?.qtyStep
        if (step) setQtyStep(step)
      })
      .catch(() => {})
  }, [symbol, category])

  function applyStep(val: number): string {
    const step = parseFloat(qtyStep)
    if (!step || !val) return ''
    const decimals = qtyStep.includes('.') ? qtyStep.split('.')[1].length : 0
    return (Math.round(val / step) * step).toFixed(decimals)
  }

  function onQtyChange(v: string) {
    setQty(v)
    const p = effectivePrice()
    setQtyUsdt(p > 0 && v ? (parseFloat(v) * p).toFixed(2) : '')
  }

  function onQtyUsdtChange(v: string) {
    setQtyUsdt(v)
    const p = effectivePrice()
    const decimals = qtyStep.includes('.') ? qtyStep.split('.')[1].length : 4
    setQty(p > 0 && v ? (parseFloat(v) / p).toFixed(Math.max(decimals, 4)) : '')
  }

  function addLog(text: string, status: LogEntry['status'] = 'pending') {
    setLog(prev => [...prev, { text, status }])
  }
  function resolveLog(status: 'ok' | 'error', text?: string) {
    setLog(prev => {
      const next = [...prev]
      const last = next[next.length - 1]
      if (last) { last.status = status; if (text) last.text = text }
      return next
    })
  }

  useEffect(() => {
    setPrice(''); setQty(''); setQtyUsdt(''); setTriggerPrice(''); setLog([])
  }, [symbol])

  async function handleSubmit() {
    const finalQty = qty || (qtyUsdt ? applyStep(parseFloat(qtyUsdt) / (effectivePrice() || 1)) : '')
    if (!finalQty) { addLog('Укажите количество или объём в USDT', 'error'); return }
    if (tab === 'Limit' && !price) { addLog('Укажите цену', 'error'); return }
    if (tab === 'Conditional' && !triggerPrice) { addLog('Укажите цену триггера', 'error'); return }
    setLoading(true)
    const sideLabel = side === 'Buy' ? 'Buy / Long' : 'Sell / Short'
    addLog(`Размещение ${sideLabel}...`)

    if (leverage) {
      const levRes = await setLeverage({ account_id: accountId, symbol, category, leverage })
      if (!levRes.ok) { resolveLog('error', `Плечо: ${levRes.message}`); setLoading(false); return }
    }

    const res = await placeOrder({
      account_id: accountId,
      symbol,
      category,
      side,
      order_type: tab === 'Market' ? 'Market' : tab === 'Conditional' ? condType : 'Limit',
      qty: finalQty,
      price: (tab === 'Limit' || (tab === 'Conditional' && condType === 'Limit')) ? price : undefined,
      trigger_price: tab === 'Conditional' ? triggerPrice : undefined,
      trigger_by: tab === 'Conditional' ? triggerBy : undefined,
      trigger_direction: tab === 'Conditional' ? triggerDir : undefined,
      order_filter: tab === 'Conditional' ? 'StopOrder' : 'Order',
      time_in_force: tab === 'Limit' ? tif : undefined,
      reduce_only: reduceOnly,
      position_idx: hedgeMode ? (side === 'Buy' ? 1 : 2) : 0,
    })

    if (res.ok) {
      resolveLog('ok', `Принят: ${symbol} ${sideLabel}`)
      setQty(''); setQtyUsdt(''); setPrice(''); setTriggerPrice('')
    } else {
      resolveLog('error', `Отклонено: ${res.message}`)
    }
    setLoading(false)
  }

  async function handleCancel(ord: ActiveOrder) {
    await cancelOrder({
      account_id: accountId,
      symbol: ord.symbol,
      category: ord.category,
      order_id: ord.orderId,
      order_filter: ord.orderFilter,
    })
  }

  const symbolOrders = orders.filter(o => o.symbol === symbol)

  return (
    <div className="border-b border-gray-200 dark:border-gray-700 text-xs text-gray-900 dark:text-white">
      <button
        onClick={() => setOpen(v => !v)}
        className="w-full flex items-center justify-between px-3 py-2 text-xs font-medium text-gray-600 dark:text-gray-400 hover:text-gray-900 dark:hover:text-white transition-colors"
      >
        <span className="flex items-center gap-1.5">
          <span className="text-[10px] opacity-60">⠿</span>
          Ручная торговля
          {symbolOrders.length > 0 && !open && (
            <span className="bg-blue-500/20 text-blue-400 text-[10px] px-1.5 rounded-full">{symbolOrders.length}</span>
          )}
        </span>
        <span className={`text-[10px] transition-transform duration-200 ${open ? 'rotate-180' : ''}`}>▾</span>
      </button>

      {open && (
        <>
          <div className="flex border-y border-gray-200 dark:border-gray-700">
            {(['Limit', 'Market', 'Conditional'] as Tab[]).map(t => (
              <button
                key={t}
                onClick={() => setTab(t)}
                className={`flex-1 py-1.5 text-xs font-medium border-b-2 transition-colors ${tab === t ? 'border-blue-500 text-gray-900 dark:text-white' : 'border-transparent text-gray-500 dark:text-gray-400 hover:text-gray-900 dark:hover:text-white'}`}
              >
                {t === 'Limit' ? 'Лимит' : t === 'Market' ? 'Рынок' : 'Условный'}
              </button>
            ))}
          </div>

          <div className="px-3 py-2 space-y-2">
            <div className="grid grid-cols-2 gap-1">
              <button onClick={() => setSide('Buy')}
                className={`py-1.5 rounded font-semibold transition-colors ${side === 'Buy' ? 'bg-green-500 text-white' : 'bg-green-500/10 text-green-500 hover:bg-green-500/20'}`}>
                Купить / Long
              </button>
              <button onClick={() => setSide('Sell')}
                className={`py-1.5 rounded font-semibold transition-colors ${side === 'Sell' ? 'bg-red-500 text-white' : 'bg-red-500/10 text-red-500 hover:bg-red-500/20'}`}>
                Продать / Short
              </button>
            </div>

            {tab === 'Conditional' && (
              <>
                <div className="grid grid-cols-2 gap-1">
                  {(['Market', 'Limit'] as const).map(t => (
                    <button key={t} onClick={() => setCondType(t)}
                      className={`py-1 rounded text-xs transition-colors ${condType === t ? 'bg-gray-200 dark:bg-gray-700 text-gray-900 dark:text-white' : 'text-gray-500 dark:text-gray-400 hover:text-gray-900 dark:hover:text-white'}`}>
                      {t === 'Market' ? 'По рынку' : 'Лимит'}
                    </button>
                  ))}
                </div>
                <div className="space-y-1">
                  <div className="flex items-center justify-between">
                    <label className="text-gray-500 dark:text-gray-400">Триггер</label>
                    <div className="flex gap-1">
                      {['MarkPrice', 'LastPrice', 'IndexPrice'].map(opt => (
                        <button key={opt} onClick={() => setTriggerBy(opt)}
                          className={`px-1.5 py-0.5 rounded text-xs ${triggerBy === opt ? 'bg-blue-500/20 text-blue-400' : 'text-gray-500 dark:text-gray-400 hover:text-gray-900 dark:hover:text-white'}`}>
                          {opt === 'MarkPrice' ? 'Марк.' : opt === 'LastPrice' ? 'Посл.' : 'Индекс'}
                        </button>
                      ))}
                    </div>
                  </div>
                  <div className="flex items-center gap-1">
                    <input value={triggerPrice} onChange={e => setTriggerPrice(e.target.value)} type="number" placeholder="0.00"
                      className="flex-1 bg-gray-100 dark:bg-gray-800 border border-gray-300 dark:border-gray-600 rounded px-2 py-1 font-mono text-xs focus:outline-none focus:border-blue-500" />
                    <div className="flex flex-col gap-0.5">
                      {([1, 2] as const).map(d => (
                        <button key={d} onClick={() => setTriggerDir(d)}
                          className={`px-1.5 py-0.5 rounded text-xs ${triggerDir === d ? (d === 1 ? 'bg-green-500/20 text-green-500' : 'bg-red-500/20 text-red-500') : 'text-gray-500 dark:text-gray-400'}`}>
                          {d === 1 ? '↑' : '↓'}
                        </button>
                      ))}
                    </div>
                  </div>
                </div>
              </>
            )}

            {(tab === 'Limit' || (tab === 'Conditional' && condType === 'Limit')) && (
              <div>
                <label className="text-gray-500 dark:text-gray-400 block mb-1">Цена ({quoteCoin})</label>
                <input value={price} onChange={e => setPrice(e.target.value)} type="number" placeholder="0.00"
                  className="w-full bg-gray-100 dark:bg-gray-800 border border-gray-300 dark:border-gray-600 rounded px-2 py-1 font-mono text-xs focus:outline-none focus:border-blue-500" />
              </div>
            )}

            <div className="grid grid-cols-2 gap-1">
              <div>
                <label className="text-gray-500 dark:text-gray-400 block mb-1">Кол-во ({baseCoin})</label>
                <input value={qty} onChange={e => onQtyChange(e.target.value)} type="number" placeholder="0.00"
                  className="w-full bg-gray-100 dark:bg-gray-800 border border-gray-300 dark:border-gray-600 rounded px-2 py-1 font-mono text-xs focus:outline-none focus:border-blue-500" />
              </div>
              <div>
                <label className="text-gray-500 dark:text-gray-400 block mb-1">Объём (USDT)</label>
                <input value={qtyUsdt} onChange={e => onQtyUsdtChange(e.target.value)} type="number" placeholder="0.00"
                  className="w-full bg-gray-100 dark:bg-gray-800 border border-gray-300 dark:border-gray-600 rounded px-2 py-1 font-mono text-xs focus:outline-none focus:border-blue-500" />
              </div>
            </div>

            {tab === 'Limit' && (
              <div className="flex items-center gap-1">
                <span className="text-gray-500 dark:text-gray-400">TIF:</span>
                {['GTC', 'IOC', 'FOK'].map(t => (
                  <button key={t} onClick={() => setTif(t)}
                    className={`px-1.5 py-0.5 rounded text-xs ${tif === t ? 'bg-blue-500/20 text-blue-400' : 'text-gray-500 dark:text-gray-400 hover:text-gray-900 dark:hover:text-white'}`}>
                    {t}
                  </button>
                ))}
              </div>
            )}

            <div className="flex items-center gap-2">
              <label className="text-gray-500 dark:text-gray-400 shrink-0">Плечо (x)</label>
              <input value={leverage} onChange={e => setLev(e.target.value)} type="number" min="1" max="200" placeholder="авто"
                className="w-20 bg-gray-100 dark:bg-gray-800 border border-gray-300 dark:border-gray-600 rounded px-2 py-1 font-mono text-xs focus:outline-none focus:border-blue-500" />
              <span className="text-gray-400 dark:text-gray-500 text-[10px]">Пусто = не менять</span>
            </div>

            <label className="flex items-center gap-2 cursor-pointer">
              <input type="checkbox" checked={reduceOnly} onChange={e => setReduceOnly(e.target.checked)} className="rounded" />
              <span className="text-gray-500 dark:text-gray-400">Только закрытие (Reduce Only)</span>
            </label>

            <button onClick={handleSubmit} disabled={loading}
              className={`w-full py-2 rounded font-semibold transition-all ${side === 'Buy' ? 'bg-green-500 hover:bg-green-600' : 'bg-red-500 hover:bg-red-600'} text-white ${loading ? 'opacity-50 cursor-not-allowed' : ''}`}>
              {side === 'Buy' ? 'Купить / Long' : 'Продать / Short'}
            </button>

            {log.length > 0 && (
              <div className="rounded border border-gray-200 dark:border-gray-700 overflow-hidden">
                {log.map((e, i) => (
                  <div key={i} className={`flex items-center gap-2 px-2 py-1 text-xs border-b border-gray-200 dark:border-gray-700/30 last:border-0 ${e.status === 'ok' ? 'bg-green-500/5' : e.status === 'error' ? 'bg-red-500/5' : ''}`}>
                    <span className="shrink-0">
                      {e.status === 'pending' && <span className="inline-block w-2 h-2 rounded-full bg-yellow-400 animate-pulse" />}
                      {e.status === 'ok' && <span className="text-green-500">✓</span>}
                      {e.status === 'error' && <span className="text-red-400">✗</span>}
                    </span>
                    <span className={e.status === 'error' ? 'text-red-400' : e.status === 'ok' ? 'text-gray-900 dark:text-white' : 'text-gray-500 dark:text-gray-400'}>{e.text}</span>
                  </div>
                ))}
              </div>
            )}
          </div>

          {symbolOrders.length > 0 && (
            <div className="border-t border-gray-200 dark:border-gray-700">
              <div className="px-3 py-1.5 text-gray-500 dark:text-gray-400 flex items-center justify-between text-xs">
                <span>Активные ордера</span>
                <span className="bg-gray-200 dark:bg-gray-700 text-xs px-1.5 py-0.5 rounded-full">{symbolOrders.length}</span>
              </div>
              <div className="overflow-y-auto max-h-32">
                {symbolOrders.map(ord => (
                  <div key={ord.orderId} className="flex items-center justify-between px-3 py-1.5 border-t border-gray-200 dark:border-gray-700/30 hover:bg-gray-100 dark:hover:bg-gray-800/50">
                    <div className="flex items-center gap-1.5 min-w-0">
                      <span className={`text-[10px] px-1 rounded ${ord.side === 'Buy' ? 'bg-green-500/20 text-green-400' : 'bg-red-500/20 text-red-400'}`}>
                        {ord.side === 'Buy' ? 'Long' : 'Short'}
                      </span>
                      <span className="text-gray-500 dark:text-gray-400">{ord.orderType}</span>
                      <span className="font-mono">
                        {ord.triggerPrice ? `~${parseFloat(ord.triggerPrice).toFixed(4)}` : parseFloat(ord.price) > 0 ? parseFloat(ord.price).toFixed(4) : '—'}
                      </span>
                      <span className="text-gray-500 dark:text-gray-400">× {ord.qty}</span>
                    </div>
                    <button onClick={() => handleCancel(ord)} className="text-gray-500 dark:text-gray-400 hover:text-red-400 transition-colors ml-1 shrink-0">✕</button>
                  </div>
                ))}
              </div>
            </div>
          )}
        </>
      )}
    </div>
  )
}
