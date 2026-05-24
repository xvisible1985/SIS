import { useState, useEffect, useRef } from 'react'
import ReactDOM from 'react-dom'
import { CoinIcon } from './CoinIcon'

interface TickerRow {
  symbol: string
  price: number
  change: number
  turnover: number
}

let _tickers: TickerRow[] = []
let _cacheAt = 0
let _delistings: string[] = []
let _delistingsAt = 0

async function loadDelistings() {
  if (Date.now() - _delistingsAt < 60_000 && _delistings.length >= 0) return
  try {
    const r = await fetch('/bybit-news/delistings')
    const j = await r.json()
    _delistings = (j.symbols ?? []) as string[]
    _delistingsAt = Date.now()
  } catch {
    _delistings = []
  }
}

function isDelisted(sym: string): boolean {
  return _delistings.includes(sym)
}

async function loadTickers() {
  if (Date.now() - _cacheAt < 30_000 && _tickers.length > 0) return
  try {
    const [r] = await Promise.all([
      fetch('https://api.bybit.com/v5/market/tickers?category=linear'),
      loadDelistings(),
    ])
    const j = await r.json()
    _tickers = ((j.result?.list ?? []) as any[])
      .filter((t: any) => t.symbol.endsWith('USDT') && !isDelisted(t.symbol))
      .map((t: any) => ({
        symbol: t.symbol,
        price: parseFloat(t.lastPrice),
        change: parseFloat(t.price24hPcnt) * 100,
        turnover: parseFloat(t.turnover24h),
      }))
      .sort((a, b) => b.turnover - a.turnover)
    _cacheAt = Date.now()
  } catch {}
}

function fmtPrice(p: number) {
  if (p >= 1000) return p.toLocaleString('en-US', { minimumFractionDigits: 2, maximumFractionDigits: 2 })
  if (p >= 1) return p.toFixed(4)
  return p.toFixed(6)
}

function fmtVol(v: number) {
  if (v >= 1e9) return (v / 1e9).toFixed(1) + 'B'
  if (v >= 1e6) return (v / 1e6).toFixed(0) + 'M'
  if (v >= 1e3) return (v / 1e3).toFixed(0) + 'K'
  return v.toFixed(0)
}

interface Props {
  value: string
  onChange: (sym: string) => void
  size?: 'sm' | 'md'
  triggerClassName?: string
  disabled?: boolean
  disabledTooltip?: string
}

export function CoinPicker({ value, onChange, size = 'md', triggerClassName, disabled, disabledTooltip }: Props) {
  const [open, setOpen] = useState(false)
  const [query, setQuery] = useState('')
  const [rows, setRows] = useState<TickerRow[]>([])
  const [dropPos, setDropPos] = useState({ top: 0, left: 0 })
  const [tipPos, setTipPos] = useState({ top: 0, left: 0 })
  const [tipVisible, setTipVisible] = useState(false)
  const triggerRef = useRef<HTMLButtonElement>(null)
  const inputRef = useRef<HTMLInputElement>(null)

  function handleMouseEnter(e: React.MouseEvent<HTMLButtonElement>) {
    if (!disabled || !disabledTooltip) return
    const r = e.currentTarget.getBoundingClientRect()
    setTipPos({ top: r.top, left: r.left + r.width / 2 })
    setTipVisible(true)
  }

  useEffect(() => {
    if (!open) return
    loadTickers().then(() => setRows([..._tickers]))
    setTimeout(() => inputRef.current?.focus(), 30)
  }, [open])

  useEffect(() => {
    if (!open) return
    function onKey(e: KeyboardEvent) {
      if (e.key === 'Escape') close()
    }
    document.addEventListener('keydown', onKey)
    return () => document.removeEventListener('keydown', onKey)
  }, [open])

  function handleOpen() {
    if (disabled) return
    const rect = triggerRef.current?.getBoundingClientRect()
    if (rect) {
      setDropPos({
        top: rect.bottom + 4,
        left: Math.min(rect.left, window.innerWidth - 284),
      })
    }
    setOpen(v => !v)
  }

  function close() {
    setOpen(false)
    setQuery('')
  }

  function select(sym: string) {
    onChange(sym)
    close()
  }

  const filtered = query
    ? rows.filter(r => r.symbol.toLowerCase().includes(query.toLowerCase()))
    : rows

  const isSm = size === 'sm'

  return (
    <>
      <button
        ref={triggerRef}
        onClick={handleOpen}
        onMouseEnter={handleMouseEnter}
        onMouseLeave={() => setTipVisible(false)}
        className={`flex items-center gap-1.5 rounded-lg bg-gray-100 dark:bg-gray-800 border border-gray-300 dark:border-gray-600 transition-colors ${isSm ? 'px-1.5 py-1' : 'px-2 py-1.5'} ${disabled ? 'opacity-50 cursor-not-allowed' : 'hover:border-blue-400 dark:hover:border-blue-500'} ${triggerClassName ?? ''}`}
      >
        <CoinIcon symbol={value} className={isSm ? 'w-3.5 h-3.5' : 'w-4 h-4'} />
        <span className={`font-mono font-semibold text-gray-900 dark:text-white flex-1 text-left ${isSm ? 'text-[11px]' : 'text-base'}`}>{value}</span>
        <svg className="w-3.5 h-3.5 text-gray-400 flex-shrink-0 ml-auto" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2.5}>
          <path strokeLinecap="round" strokeLinejoin="round" d="M19 9l-7 7-7-7" />
        </svg>
      </button>

      {tipVisible && disabledTooltip && ReactDOM.createPortal(
        <span
          className="fixed w-56 rounded-[9px] px-3 py-2.5 text-[11px] leading-relaxed shadow-2xl font-medium pointer-events-none whitespace-normal"
          style={{
            background: '#1a1a2e',
            border: '1px solid rgba(148,163,184,.25)',
            color: '#94a3b8',
            zIndex: 9999,
            top: tipPos.top,
            left: tipPos.left,
            transform: 'translate(-50%, calc(-100% - 8px))',
          }}
        >
          {disabledTooltip}
        </span>,
        document.body
      )}

      {open && <div className="fixed inset-0 z-[9998]" onClick={close} />}

      {open && (
        <div
          style={{ position: 'fixed', top: dropPos.top, left: dropPos.left, width: 280, zIndex: 9999 }}
          className="rounded-xl border border-gray-200 dark:border-gray-700 bg-white dark:bg-gray-900 shadow-2xl overflow-hidden"
        >
          <div className="p-2 border-b border-gray-100 dark:border-gray-800">
            <div className="flex items-center gap-2 px-2.5 py-1.5 rounded-lg bg-gray-100 dark:bg-gray-800">
              <svg className="w-3.5 h-3.5 text-gray-400 flex-shrink-0" fill="none" viewBox="0 0 24 24" stroke="currentColor">
                <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M21 21l-6-6m2-5a7 7 0 11-14 0 7 7 0 0114 0z" />
              </svg>
              <input
                ref={inputRef}
                value={query}
                onChange={e => setQuery(e.target.value)}
                placeholder="Поиск монеты..."
                className="flex-1 bg-transparent text-xs text-gray-900 dark:text-white placeholder-gray-400 dark:placeholder-gray-500 outline-none"
              />
              {query && (
                <button onClick={() => setQuery('')} className="text-gray-400 hover:text-gray-600 dark:hover:text-gray-200 text-xs leading-none">✕</button>
              )}
            </div>
          </div>

          <div className="overflow-y-auto max-h-72">
            {rows.length === 0 && (
              <div className="px-4 py-6 text-center text-xs text-gray-400">Загрузка…</div>
            )}
            {rows.length > 0 && filtered.length === 0 && (
              <div className="px-4 py-6 text-center text-xs text-gray-400">Ничего не найдено</div>
            )}
            {filtered.map(r => {
              const base = r.symbol.replace(/(?:USDT|USDC|USD)$/, '')
              const isUp = r.change >= 0
              return (
                <button
                  key={r.symbol}
                  onClick={() => select(r.symbol)}
                  className={`w-full flex items-center gap-3 px-3 py-2 hover:bg-gray-50 dark:hover:bg-gray-800 transition-colors text-left ${r.symbol === value ? 'bg-blue-50 dark:bg-blue-900/20' : ''}`}
                >
                  <CoinIcon symbol={r.symbol} className="w-7 h-7" />
                  <div className="flex-1 min-w-0">
                    <div className="flex items-baseline gap-0.5">
                      <span className="font-semibold text-sm text-gray-900 dark:text-white">{base}</span>
                      <span className="text-[10px] text-gray-400">/USDT</span>
                    </div>
                    <div className="text-[10px] text-gray-400">{fmtVol(r.turnover)} USDT</div>
                  </div>
                  <div className="text-right flex-shrink-0">
                    <div className="text-xs font-mono text-gray-900 dark:text-white">{fmtPrice(r.price)}</div>
                    <div className={`text-[10px] font-semibold ${isUp ? 'text-green-500' : 'text-red-500'}`}>
                      {isUp ? '▲' : '▼'} {Math.abs(r.change).toFixed(2)}%
                    </div>
                  </div>
                </button>
              )
            })}
          </div>
        </div>
      )}
    </>
  )
}
