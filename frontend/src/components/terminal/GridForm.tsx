import { useState, useEffect } from 'react'
import { placeOrder } from '../../api/trader'

interface Props {
  accountId: string
  symbol: string
  lastPrice: string | null
}

type Direction = 'buy' | 'sell' | 'both'

export function GridForm({ accountId, symbol, lastPrice }: Props) {
  const [open, setOpen] = useState(false)
  const [basePrice, setBasePrice] = useState('')
  const [step, setStep] = useState('')
  const [levels, setLevels] = useState('5')
  const [sizeUsdt, setSizeUsdt] = useState('')
  const [direction, setDirection] = useState<Direction>('both')
  const [placing, setPlacing] = useState(false)
  const [result, setResult] = useState<string | null>(null)
  const [errors, setErrors] = useState<string[]>([])
  const [qtyStep, setQtyStep] = useState('1')

  const category = symbol.endsWith('USDT') || symbol.endsWith('USDC') ? 'linear' : 'inverse'

  const hedgeMode = accountId
    ? localStorage.getItem(`sis_hedge_${accountId}_${symbol}`) === 'true'
    : false

  useEffect(() => {
    fetch(`https://api.bybit.com/v5/market/instruments-info?category=${category}&symbol=${symbol}`)
      .then(r => r.json())
      .then(j => {
        const s = j.result?.list?.[0]?.lotSizeFilter?.qtyStep
        if (s) setQtyStep(s)
      })
      .catch(() => {})
  }, [symbol, category])

  function roundQty(val: number): string {
    const s = parseFloat(qtyStep)
    if (!s) return val.toFixed(4)
    const dec = qtyStep.includes('.') ? qtyStep.split('.')[1].length : 0
    return (Math.round(val / s) * s).toFixed(dec)
  }

  const base = parseFloat(basePrice) || parseFloat(lastPrice ?? '0') || 0
  const stepVal = parseFloat(step) || 0
  const lvl = Math.max(1, Math.min(20, parseInt(levels) || 5))

  const stepPct = stepVal / 100
  const buyPrices = direction !== 'sell' && base > 0 && stepVal > 0
    ? Array.from({ length: lvl }, (_, i) => +(base * Math.pow(1 - stepPct, i + 1)).toFixed(8))
    : []
  const sellPrices = direction !== 'buy' && base > 0 && stepVal > 0
    ? Array.from({ length: lvl }, (_, i) => +(base * Math.pow(1 + stepPct, i + 1)).toFixed(8))
    : []
  const totalOrders = buyPrices.length + sellPrices.length

  async function handlePlace() {
    if (!accountId || !stepVal || !sizeUsdt || totalOrders === 0) return
    setPlacing(true)
    setResult(null)
    setErrors([])

    const allOrders = [
      ...buyPrices.map(p => ({ side: 'Buy' as const, price: p, triggerDir: 2 as const })),
      ...sellPrices.map(p => ({ side: 'Sell' as const, price: p, triggerDir: 1 as const })),
    ]

    let ok = 0
    const errs: string[] = []
    for (const o of allOrders) {
      const qty = roundQty(parseFloat(sizeUsdt) / o.price)
      try {
        const res = await placeOrder({
          account_id: accountId,
          symbol,
          category,
          side: o.side,
          order_type: 'Market',
          qty,
          trigger_price: o.price.toFixed(2),
          trigger_by: 'MarkPrice',
          trigger_direction: o.triggerDir,
          order_filter: 'StopOrder',
          position_idx: hedgeMode ? (o.side === 'Buy' ? 1 : 2) : 0,
        })
        if (res.ok) ok++
        else errs.push(`${o.side} @ ${o.price.toFixed(2)}: ${res.message ?? 'ошибка'}`)
      } catch (e: any) {
        errs.push(`${o.side} @ ${o.price.toFixed(2)}: ${e?.message ?? 'network error'}`)
      }
    }
    setResult(`✓ ${ok} размещено${errs.length ? `, ✗ ${errs.length} ошибок` : ''}`)
    setErrors(errs)
    setPlacing(false)
  }

  return (
    <div className="bg-white dark:bg-gray-900/50 border border-gray-200 dark:border-gray-700/50 rounded-xl flex-shrink-0 overflow-hidden">
      <button
        onClick={() => setOpen(v => !v)}
        className="w-full flex items-center justify-between px-3 py-2 text-xs font-medium text-gray-600 dark:text-gray-400 hover:text-gray-900 dark:hover:text-white transition-colors"
      >
        <span className="flex items-center gap-1.5">
          <span className="text-[10px] opacity-60">⠿</span>
          Сетка ордеров
          {totalOrders > 0 && !open && (
            <span className="bg-blue-500/20 text-blue-400 text-[10px] px-1.5 rounded-full">{totalOrders}</span>
          )}
        </span>
        <span className={`text-[10px] transition-transform duration-200 ${open ? 'rotate-180' : ''}`}>▾</span>
      </button>

      {open && (
        <div className="border-t border-gray-200 dark:border-gray-700 px-3 py-2 space-y-2 text-xs">

          {/* Направление */}
          <div className="grid grid-cols-3 gap-1">
            {(['buy', 'sell', 'both'] as Direction[]).map(d => (
              <button key={d} onClick={() => setDirection(d)}
                className={`py-1 rounded text-center font-medium transition-colors ${direction === d
                  ? d === 'buy' ? 'bg-green-500 text-white'
                    : d === 'sell' ? 'bg-red-500 text-white'
                    : 'bg-blue-500 text-white'
                  : 'bg-gray-100 dark:bg-gray-800 text-gray-500 dark:text-gray-400 hover:text-gray-800 dark:hover:text-gray-200'
                }`}>
                {d === 'buy' ? 'Buy' : d === 'sell' ? 'Sell' : 'Оба'}
              </button>
            ))}
          </div>

          {/* База */}
          <div>
            <label className="text-gray-500 dark:text-gray-400 block mb-0.5">
              База <span className="text-gray-400 dark:text-gray-500">(пусто = {lastPrice ?? '—'})</span>
            </label>
            <input
              value={basePrice} onChange={e => setBasePrice(e.target.value)}
              type="number" placeholder={lastPrice ?? '0'}
              className="w-full bg-gray-100 dark:bg-gray-800 border border-gray-300 dark:border-gray-600 rounded px-2 py-1 font-mono text-xs focus:outline-none focus:border-blue-500"
            />
          </div>

          {/* Шаг + Уровни */}
          <div className="grid grid-cols-2 gap-1">
            <div>
              <label className="text-gray-500 dark:text-gray-400 block mb-0.5">Шаг (%)</label>
              <input
                value={step} onChange={e => setStep(e.target.value)}
                type="number" placeholder="1.0" step="0.1" min="0.01"
                className="w-full bg-gray-100 dark:bg-gray-800 border border-gray-300 dark:border-gray-600 rounded px-2 py-1 font-mono text-xs focus:outline-none focus:border-blue-500"
              />
            </div>
            <div>
              <label className="text-gray-500 dark:text-gray-400 block mb-0.5">Уровней</label>
              <input
                value={levels} onChange={e => setLevels(e.target.value)}
                type="number" min="1" max="20" placeholder="5"
                className="w-full bg-gray-100 dark:bg-gray-800 border border-gray-300 dark:border-gray-600 rounded px-2 py-1 font-mono text-xs focus:outline-none focus:border-blue-500"
              />
            </div>
          </div>

          {/* Объём */}
          <div>
            <label className="text-gray-500 dark:text-gray-400 block mb-0.5">Объём на уровень (USDT)</label>
            <input
              value={sizeUsdt} onChange={e => setSizeUsdt(e.target.value)}
              type="number" placeholder="0.00"
              className="w-full bg-gray-100 dark:bg-gray-800 border border-gray-300 dark:border-gray-600 rounded px-2 py-1 font-mono text-xs focus:outline-none focus:border-blue-500"
            />
          </div>

          {/* Превью */}
          {totalOrders > 0 && (
            <div className="rounded border border-gray-200 dark:border-gray-700 overflow-hidden max-h-36 overflow-y-auto">
              {sellPrices.slice().reverse().map((p, i) => (
                <div key={`s${i}`} className="flex justify-between items-center px-2 py-[3px] border-b border-gray-100 dark:border-gray-800">
                  <span className="text-[10px] text-red-400 font-medium">Sell ↑</span>
                  <span className="text-[10px] font-mono text-gray-900 dark:text-white">{p.toFixed(2)}</span>
                  {sizeUsdt && <span className="text-[10px] text-gray-400">{roundQty(parseFloat(sizeUsdt) / p)}</span>}
                </div>
              ))}
              <div className="flex justify-between items-center px-2 py-[3px] bg-gray-50 dark:bg-gray-800/60 border-b border-gray-200 dark:border-gray-700">
                <span className="text-[10px] text-gray-500">◆ База</span>
                <span className="text-[10px] font-mono text-gray-900 dark:text-white">{base.toFixed(2)}</span>
              </div>
              {buyPrices.map((p, i) => (
                <div key={`b${i}`} className="flex justify-between items-center px-2 py-[3px] border-b border-gray-100 dark:border-gray-800 last:border-0">
                  <span className="text-[10px] text-green-400 font-medium">Buy ↓</span>
                  <span className="text-[10px] font-mono text-gray-900 dark:text-white">{p.toFixed(2)}</span>
                  {sizeUsdt && <span className="text-[10px] text-gray-400">{roundQty(parseFloat(sizeUsdt) / p)}</span>}
                </div>
              ))}
            </div>
          )}

          {/* Кнопка */}
          <button
            onClick={handlePlace}
            disabled={placing || !stepVal || !sizeUsdt || totalOrders === 0}
            className="w-full py-1.5 rounded bg-blue-500 hover:bg-blue-600 disabled:opacity-40 text-white text-xs font-semibold transition-colors"
          >
            {placing ? 'Размещение...' : `Разместить ${totalOrders} ордеров`}
          </button>

          {result && (
            <div className={`text-center text-[11px] ${errors.length ? 'text-red-400' : 'text-green-400'}`}>{result}</div>
          )}
          {errors.map((e, i) => (
            <div key={i} className="text-[10px] text-red-400 font-mono break-all">{e}</div>
          ))}
        </div>
      )}
    </div>
  )
}
