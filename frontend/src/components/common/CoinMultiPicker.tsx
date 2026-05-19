import { useState, useEffect, useRef } from 'react'
import { X, Search, Save, ChevronDown, Trash2, Asterisk } from 'lucide-react'
import { CoinIcon } from './CoinIcon'

// ─── Ticker cache ─────────────────────────────────────────────────────────────

interface TickerRow { symbol: string; price: number; change: number; turnover: number }
let _tickers: TickerRow[] = []
let _cacheAt = 0

async function loadTickers() {
  if (Date.now() - _cacheAt < 30_000 && _tickers.length > 0) return
  try {
    const r = await fetch('https://api.bybit.com/v5/market/tickers?category=linear')
    const j = await r.json()
    _tickers = ((j.result?.list ?? []) as any[])
      .filter((t: any) => t.symbol.endsWith('USDT'))
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

// ─── Pattern helpers ──────────────────────────────────────────────────────────

export function isPattern(s: string): boolean {
  return s.includes('*')
}

export function matchesPattern(symbol: string, pattern: string): boolean {
  const escaped = pattern.replace(/[.+?^${}()|[\]\\]/g, '\\$&').replace(/\*/g, '.*')
  return new RegExp(`^${escaped}$`, 'i').test(symbol)
}

function patternMatches(pattern: string, rows: TickerRow[]): TickerRow[] {
  return rows.filter(r => matchesPattern(r.symbol, pattern))
}

// ─── Saved lists ──────────────────────────────────────────────────────────────

const LS_KEY = 'bot-symbol-lists'
export type SavedList = { name: string; symbols: string[] }

function getSavedLists(): SavedList[] {
  try { return JSON.parse(localStorage.getItem(LS_KEY) ?? '[]') } catch { return [] }
}
function persistList(name: string, symbols: string[]) {
  const lists = getSavedLists().filter(l => l.name !== name)
  localStorage.setItem(LS_KEY, JSON.stringify([...lists, { name, symbols }]))
}
function deleteList(name: string) {
  localStorage.setItem(LS_KEY, JSON.stringify(getSavedLists().filter(l => l.name !== name)))
}

// ─── Presets ──────────────────────────────────────────────────────────────────

const PRESETS: SavedList[] = [
  { name: 'Major',  symbols: ['BTCUSDT', 'ETHUSDT', 'SOLUSDT', 'BNBUSDT'] },
  { name: 'Top-10', symbols: ['BTCUSDT', 'ETHUSDT', 'BNBUSDT', 'SOLUSDT', 'XRPUSDT', 'DOGEUSDT', 'ADAUSDT', 'AVAXUSDT', 'DOTUSDT', 'LINKUSDT'] },
  { name: 'DeFi',   symbols: ['UNIUSDT', 'AAVEUSDT', 'MKRUSDT', 'COMPUSDT', 'CRVUSDT', 'SUSHIUSDT'] },
  { name: 'Layer1', symbols: ['BTCUSDT', 'ETHUSDT', 'SOLUSDT', 'AVAXUSDT', 'NEARUSDT', 'DOTUSDT', 'ATOMUSDT'] },
]

// ─── Component ────────────────────────────────────────────────────────────────

interface Props {
  values: string[]
  onChange: (v: string[]) => void
  color?: 'blue' | 'red'
  placeholder?: string
}

export function CoinMultiPicker({ values, onChange, color = 'blue', placeholder = 'Добавить монету...' }: Props) {
  const [open, setOpen]           = useState(false)
  const [rows, setRows]           = useState<TickerRow[]>([])
  const [query, setQuery]         = useState('')
  const [savedLists, setSavedLists] = useState<SavedList[]>([])
  const [saveMode, setSaveMode]   = useState(false)
  const [saveName, setSaveName]   = useState('')
  const [showLists, setShowLists] = useState(false)
  const containerRef = useRef<HTMLDivElement>(null)
  const inputRef     = useRef<HTMLInputElement>(null)

  useEffect(() => {
    loadTickers().then(() => setRows([..._tickers]))
    setSavedLists(getSavedLists())
  }, [])

  useEffect(() => {
    if (!open) return
    const h = (e: MouseEvent) => {
      if (containerRef.current && !containerRef.current.contains(e.target as Node)) {
        setOpen(false); setQuery(''); setSaveMode(false); setShowLists(false)
      }
    }
    document.addEventListener('mousedown', h)
    return () => document.removeEventListener('mousedown', h)
  }, [open])

  function toggleOpen() {
    setOpen(v => !v)
    if (!open) setTimeout(() => inputRef.current?.focus(), 50)
  }

  function toggle(sym: string) {
    onChange(values.includes(sym) ? values.filter(s => s !== sym) : [...values, sym])
  }

  function addPattern(pattern: string) {
    const p = pattern.trim().toUpperCase()
    if (p && !values.includes(p)) onChange([...values, p])
    setQuery('')
  }

  function remove(sym: string) {
    onChange(values.filter(s => s !== sym))
  }

  function loadList(list: SavedList) {
    onChange([...new Set([...values, ...list.symbols])])
    setShowLists(false)
  }

  function handleSave() {
    if (!saveName.trim()) return
    persistList(saveName.trim(), values)
    setSavedLists(getSavedLists())
    setSaveMode(false); setSaveName('')
  }

  function handleDeleteList(name: string, e: React.MouseEvent) {
    e.stopPropagation()
    deleteList(name)
    setSavedLists(getSavedLists())
  }

  const q = query.trim()
  const isWildcard = q.includes('*')

  // For wildcard queries show pattern UI; for plain queries filter the list
  const wildcardMatches = isWildcard ? patternMatches(q, rows) : []
  const filtered = isWildcard
    ? []
    : q ? rows.filter(r => r.symbol.toLowerCase().includes(q.toLowerCase())) : rows

  const coinTagCls = color === 'blue'
    ? 'border-[#5b8cff]/25 bg-[#5b8cff]/[.12] text-[#a0b8ff]'
    : 'border-rose-500/25 bg-rose-500/[.12] text-rose-300'

  const patternTagCls = 'border-amber-500/30 bg-amber-500/[.12] text-amber-300'

  const allLists = [...PRESETS, ...savedLists]

  return (
    <div ref={containerRef} className="relative">
      {/* chips + trigger */}
      <div
        onClick={toggleOpen}
        className="min-h-[42px] cursor-pointer rounded-lg border border-white/[.08] bg-black/[.2] px-3 py-2 transition-colors hover:border-white/[.14]"
      >
        {values.length === 0 ? (
          <span className="text-[12px] text-slate-500">{placeholder}</span>
        ) : (
          <div className="flex flex-wrap gap-1.5">
            {values.map(val => (
              <span
                key={val}
                className={`inline-flex items-center gap-1 rounded-md border px-2 py-0.5 font-mono text-[11px] font-semibold ${isPattern(val) ? patternTagCls : coinTagCls}`}
                onClick={e => e.stopPropagation()}
              >
                {isPattern(val)
                  ? <Asterisk size={9} className="opacity-70" />
                  : <CoinIcon symbol={val} className="w-3 h-3" />
                }
                {val.replace(/USDT$/i, '') + (isPattern(val) ? '' : '')}
                <button type="button" onClick={() => remove(val)} className="opacity-60 hover:opacity-100">
                  <X size={9} />
                </button>
              </span>
            ))}
          </div>
        )}
      </div>

      {/* dropdown */}
      {open && (
        <div className="absolute z-30 mt-1 w-full rounded-xl border border-white/[.10] bg-[#0d1628] shadow-2xl">

          {/* search bar */}
          <div className="flex items-center gap-1.5 border-b border-white/[.06] px-3 py-2">
            {isWildcard
              ? <Asterisk size={13} className="shrink-0 text-amber-400" />
              : <Search size={13} className="shrink-0 text-slate-500" />
            }
            <input
              ref={inputRef}
              type="text"
              value={query}
              onChange={e => setQuery(e.target.value)}
              onKeyDown={e => {
                if (e.key === 'Enter' && isWildcard) { e.preventDefault(); addPattern(q) }
              }}
              placeholder="Монета или маска (1000*, *MEME*)"
              className="flex-1 bg-transparent text-[12px] text-slate-200 outline-none placeholder:text-slate-500"
            />
            <button
              type="button"
              onClick={() => { setShowLists(v => !v); setSaveMode(false) }}
              className="inline-flex items-center gap-1 rounded-md border border-white/[.08] px-2 py-1 text-[10px] font-semibold text-slate-400 hover:text-slate-200"
            >
              Списки <ChevronDown size={10} />
            </button>
            {values.length > 0 && (
              <button
                type="button"
                onClick={() => { setSaveMode(v => !v); setShowLists(false) }}
                className="inline-flex items-center gap-1 rounded-md border border-[#5b8cff]/25 bg-[#5b8cff]/[.10] px-2 py-1 text-[10px] font-semibold text-[#a0b8ff] hover:bg-[#5b8cff]/[.18]"
              >
                <Save size={10} /> Сохранить
              </button>
            )}
          </div>

          {/* save input */}
          {saveMode && (
            <div className="flex items-center gap-1.5 border-b border-white/[.06] px-3 py-2">
              <input
                autoFocus type="text" value={saveName}
                onChange={e => setSaveName(e.target.value)}
                onKeyDown={e => e.key === 'Enter' && handleSave()}
                placeholder="Название списка..."
                className="flex-1 rounded-md border border-white/[.08] bg-black/[.3] px-2 py-1 text-[12px] text-slate-200 outline-none"
              />
              <button
                type="button" onClick={handleSave} disabled={!saveName.trim()}
                className="rounded-md bg-[#5b8cff]/[.20] px-3 py-1 text-[11px] font-semibold text-[#a0b8ff] hover:bg-[#5b8cff]/[.30] disabled:opacity-40"
              >
                Сохранить
              </button>
            </div>
          )}

          {/* lists panel */}
          {showLists && (
            <div className="border-b border-white/[.06] p-2">
              <div className="mb-1.5 px-1 text-[9px] font-bold uppercase tracking-wider text-slate-500">Пресеты и сохранённые</div>
              {allLists.length === 0 ? (
                <div className="py-2 text-center text-[11px] text-slate-500">Нет сохранённых списков</div>
              ) : (
                <div className="flex flex-col gap-0.5">
                  {allLists.map(list => {
                    const isPreset = PRESETS.some(p => p.name === list.name)
                    return (
                      <button
                        key={list.name} type="button" onClick={() => loadList(list)}
                        className="flex items-center gap-2 rounded-lg px-2 py-1.5 text-left hover:bg-white/[.04]"
                      >
                        <span className="flex-1 text-[12px] font-semibold text-slate-200">{list.name}</span>
                        <span className="text-[10px] text-slate-500">{list.symbols.length} записей</span>
                        {!isPreset && (
                          <button type="button" onClick={e => handleDeleteList(list.name, e)} className="text-slate-600 hover:text-rose-400">
                            <Trash2 size={11} />
                          </button>
                        )}
                      </button>
                    )
                  })}
                </div>
              )}
            </div>
          )}

          {/* wildcard mode */}
          {isWildcard && (
            <div className="p-2">
              {/* add pattern button */}
              <button
                type="button"
                onClick={() => addPattern(q)}
                disabled={values.includes(q.toUpperCase())}
                className="flex w-full items-center gap-2.5 rounded-lg border border-amber-500/25 bg-amber-500/[.08] px-3 py-2 text-left transition-colors hover:bg-amber-500/[.14] disabled:opacity-40"
              >
                <span className="flex h-6 w-6 shrink-0 items-center justify-center rounded-md border border-amber-500/30 bg-amber-500/[.15] text-amber-300">
                  <Asterisk size={11} />
                </span>
                <div className="min-w-0 flex-1">
                  <div className="font-mono text-[12px] font-semibold text-amber-200">
                    {q.toUpperCase()}
                  </div>
                  <div className="text-[10px] text-slate-400">
                    {wildcardMatches.length > 0
                      ? `Совпадает ${wildcardMatches.length} монет — нажмите Enter или кнопку`
                      : 'Нет совпадений в текущем списке тикеров'}
                  </div>
                </div>
                <span className="shrink-0 rounded-md bg-amber-500/[.20] px-2 py-0.5 text-[10px] font-bold text-amber-300">
                  + добавить маску
                </span>
              </button>

              {/* preview of matching coins */}
              {wildcardMatches.length > 0 && (
                <div className="mt-2">
                  <div className="mb-1 px-1 text-[9px] font-bold uppercase tracking-wider text-slate-500">
                    Примеры совпадений
                  </div>
                  <div className="flex flex-wrap gap-1.5 px-1">
                    {wildcardMatches.slice(0, 8).map(r => (
                      <span
                        key={r.symbol}
                        className="inline-flex items-center gap-1 rounded-md border border-white/[.07] bg-white/[.03] px-2 py-0.5 font-mono text-[10px] text-slate-400"
                      >
                        <CoinIcon symbol={r.symbol} className="w-3 h-3" />
                        {r.symbol.replace('USDT', '')}
                      </span>
                    ))}
                    {wildcardMatches.length > 8 && (
                      <span className="px-1 py-0.5 text-[10px] text-slate-500">
                        +{wildcardMatches.length - 8} ещё
                      </span>
                    )}
                  </div>
                </div>
              )}
            </div>
          )}

          {/* regular coin list */}
          {!isWildcard && (
            <div className="max-h-56 overflow-y-auto p-1">
              {filtered.length === 0 ? (
                <div className="py-4 text-center text-[11px] text-slate-500">
                  {q ? 'Ничего не найдено — попробуйте маску, например ' : 'Загрузка...'}
                  {q && <code className="text-amber-400">{q}*</code>}
                </div>
              ) : filtered.slice(0, 100).map(row => {
                const selected = values.includes(row.symbol)
                const base = row.symbol.replace('USDT', '')
                const pos = row.change >= 0
                return (
                  <button
                    key={row.symbol} type="button" onClick={() => toggle(row.symbol)}
                    className={`flex w-full items-center gap-2.5 rounded-lg px-2.5 py-1.5 transition-colors hover:bg-white/[.04] ${selected ? 'bg-white/[.035]' : ''}`}
                  >
                    <CoinIcon symbol={row.symbol} className="w-[18px] h-[18px] shrink-0" />
                    <span className="flex-1 text-left">
                      <span className="text-[12px] font-semibold text-slate-200">{base}</span>
                      <span className="ml-1 text-[10px] text-slate-500">USDT</span>
                    </span>
                    <span className={`text-[10px] font-mono ${pos ? 'text-emerald-400' : 'text-rose-400'}`}>
                      {pos ? '+' : ''}{row.change.toFixed(2)}%
                    </span>
                    {selected && (
                      <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="#5b8cff" strokeWidth="2.5" strokeLinecap="round" strokeLinejoin="round">
                        <polyline points="20 6 9 17 4 12" />
                      </svg>
                    )}
                  </button>
                )
              })}
            </div>
          )}

          {values.length > 0 && (
            <div className="flex items-center justify-between border-t border-white/[.06] px-3 py-2">
              <span className="text-[11px] text-slate-400">{values.length} записей</span>
              <button type="button" onClick={() => onChange([])} className="text-[11px] text-rose-400 hover:text-rose-300">
                Очистить
              </button>
            </div>
          )}
        </div>
      )}
    </div>
  )
}
