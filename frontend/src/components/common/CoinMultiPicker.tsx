import { useState, useEffect, useRef, useMemo } from 'react'
import { X, Search, Save, Trash2, Asterisk, Plus, ChevronRight } from 'lucide-react'
import { CoinIcon } from './CoinIcon'

// ─── Ticker cache ─────────────────────────────────────────────────────────────

interface TickerRow { symbol: string; price: number; change: number; turnover: number }
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

export async function getAllSymbols(): Promise<string[]> {
  await loadTickers()
  return _tickers.map(t => t.symbol)
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

// ─── Static presets ───────────────────────────────────────────────────────────

const STATIC_PRESETS: SavedList[] = [
  { name: 'Major',  symbols: ['BTCUSDT', 'ETHUSDT', 'SOLUSDT', 'BNBUSDT'] },
  { name: 'DeFi',   symbols: ['UNIUSDT', 'AAVEUSDT', 'MKRUSDT', 'COMPUSDT', 'CRVUSDT', 'SUSHIUSDT'] },
  { name: 'Layer1', symbols: ['BTCUSDT', 'ETHUSDT', 'SOLUSDT', 'AVAXUSDT', 'NEARUSDT', 'DOTUSDT', 'ATOMUSDT'] },
]

// ─── Tabs ─────────────────────────────────────────────────────────────────────

type TabId = 'all' | 'top' | 'up' | 'down' | 'lists'

const TABS: { id: TabId; label: string }[] = [
  { id: 'all',   label: 'Все' },
  { id: 'top',   label: '🔥 Топ-30' },
  { id: 'up',    label: '▲ Рост' },
  { id: 'down',  label: '▼ Падение' },
  { id: 'lists', label: '📋 Списки' },
]

// ─── PatternTagInput ──────────────────────────────────────────────────────────

function PatternTagInput({ tags, onChange, placeholder, accent }: {
  tags: string[]
  onChange: (t: string[]) => void
  placeholder: string
  accent: string
}) {
  const [input, setInput] = useState('')
  const add = () => {
    const val = input.trim().toUpperCase()
    if (val && !tags.includes(val)) onChange([...tags, val])
    setInput('')
  }
  return (
    <div className="flex flex-wrap gap-1 rounded-md border border-white/[.07] bg-black/[.25] px-2 py-1.5 min-h-[32px]">
      {tags.map(t => (
        <span key={t} className={`inline-flex items-center gap-0.5 rounded px-1.5 py-0.5 text-[10px] font-mono font-semibold ${accent}`}>
          {t}
          <button type="button" onClick={() => onChange(tags.filter(x => x !== t))} className="opacity-60 hover:opacity-100">
            <X size={8} />
          </button>
        </span>
      ))}
      <input
        type="text" value={input}
        onChange={e => setInput(e.target.value)}
        onKeyDown={e => { if (e.key === 'Enter' || e.key === ',') { e.preventDefault(); add() } }}
        placeholder={tags.length === 0 ? placeholder : ''}
        className="flex-1 min-w-[80px] bg-transparent text-[11px] text-slate-200 outline-none placeholder:text-slate-500"
      />
    </div>
  )
}

// ─── Component ────────────────────────────────────────────────────────────────

interface Props {
  values: string[]
  onChange: (v: string[]) => void
  color?: 'blue' | 'red'
  placeholder?: string
}

export function CoinMultiPicker({ values, onChange, color = 'blue', placeholder = 'Добавить монету...' }: Props) {
  const [open, setOpen]             = useState(false)
  const [rows, setRows]             = useState<TickerRow[]>([])
  const [query, setQuery]           = useState('')
  const [chipFilter, setChipFilter] = useState('')
  const [savedLists, setSavedLists] = useState<SavedList[]>([])
  const [activeTab, setActiveTab]   = useState<TabId>('all')

  // Save current selection as list
  const [saveMode, setSaveMode]   = useState(false)
  const [saveName, setSaveName]   = useState('')

  // Create new list with whitelist/blacklist
  const [createMode, setCreateMode]       = useState(false)
  const [createName, setCreateName]       = useState('')
  const [createInclude, setCreateInclude] = useState<string[]>([])
  const [createExclude, setCreateExclude] = useState<string[]>([])

  // Flash message after bulk-add
  const [addedMsg, setAddedMsg] = useState('')

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
        setOpen(false); setQuery(''); setSaveMode(false)
        setActiveTab('all'); setCreateMode(false)
      }
    }
    document.addEventListener('mousedown', h)
    return () => document.removeEventListener('mousedown', h)
  }, [open])

  // Dynamic top-N presets (computed from loaded tickers by volume)
  const dynamicPresets: SavedList[] = useMemo(() => {
    if (rows.length === 0) return []
    return [
      { name: 'Топ-20', symbols: rows.slice(0, 20).map(r => r.symbol) },
      { name: 'Топ-30', symbols: rows.slice(0, 30).map(r => r.symbol) },
      { name: 'Топ-50', symbols: rows.slice(0, 50).map(r => r.symbol) },
    ]
  }, [rows])

  // Resolve list from include/exclude patterns for the create form
  const resolvedCreate = useMemo(() => {
    if (rows.length === 0) return []
    let result = rows.map(r => r.symbol)
    if (createInclude.length > 0)
      result = result.filter(sym => createInclude.some(rule => matchesPattern(sym, rule)))
    result = result.filter(sym => !createExclude.some(rule => matchesPattern(sym, rule)))
    return result
  }, [rows, createInclude, createExclude])

  // ─── Tab logic ─────────────────────────────────────────────────────────────

  function getTabRows(tab: TabId): TickerRow[] {
    switch (tab) {
      case 'top':  return rows.slice(0, 30)
      case 'up':   return [...rows].filter(r => r.change > 0).sort((a, b) => b.change - a.change).slice(0, 50)
      case 'down': return [...rows].filter(r => r.change < 0).sort((a, b) => a.change - b.change).slice(0, 50)
      default:     return rows
    }
  }

  function handleTabClick(tab: TabId) {
    setActiveTab(tab)
    setSaveMode(false)
    setCreateMode(false)
    setQuery('')

    if (tab !== 'all' && tab !== 'lists' && rows.length > 0) {
      const coins = getTabRows(tab).map(r => r.symbol)
      const newSet = [...new Set([...values, ...coins])]
      const added = newSet.length - values.length
      onChange(newSet)
      if (added > 0) {
        setAddedMsg(`+${added} монет`)
        setTimeout(() => setAddedMsg(''), 2000)
      }
    }
  }

  // ─── Actions ───────────────────────────────────────────────────────────────

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

  function remove(sym: string) { onChange(values.filter(s => s !== sym)) }

  function loadList(list: SavedList) {
    onChange([...new Set([...values, ...list.symbols])])
    setActiveTab('all')
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

  function handleCreateSave() {
    if (!createName.trim() || resolvedCreate.length === 0) return
    persistList(createName.trim(), resolvedCreate)
    setSavedLists(getSavedLists())
    setCreateMode(false); setCreateName('')
    setCreateInclude([]); setCreateExclude([])
  }

  // ─── Filtering ─────────────────────────────────────────────────────────────

  const q               = query.trim()
  const isWildcard      = q.includes('*')
  const wildcardMatches = isWildcard ? patternMatches(q, rows) : []

  // When query is active — search across all; otherwise show current tab's rows
  const tabRows  = (activeTab !== 'all' && activeTab !== 'lists') ? getTabRows(activeTab) : rows
  const filtered = isWildcard
    ? []
    : q
      ? rows.filter(r => r.symbol.toLowerCase().includes(q.toLowerCase()))
      : tabRows

  // ─── Styling ───────────────────────────────────────────────────────────────

  const coinTagCls    = color === 'blue'
    ? 'border-[#5b8cff]/25 bg-[#5b8cff]/[.12] text-[#a0b8ff]'
    : 'border-rose-500/25 bg-rose-500/[.12] text-rose-300'
  const patternTagCls = 'border-amber-500/30 bg-amber-500/[.12] text-amber-300'

  const visibleValues = chipFilter.trim()
    ? values.filter(v => v.toLowerCase().includes(chipFilter.trim().toLowerCase()))
    : values

  const showLists = activeTab === 'lists'

  return (
    <div ref={containerRef} className="relative">
      {/* chip filter bar */}
      {values.length > 5 && (
        <div className="mb-1.5 flex items-center gap-1.5 rounded-lg border border-white/[.06] bg-black/[.15] px-2.5 py-1">
          <Search size={11} className="shrink-0 text-slate-500" />
          <input type="text" value={chipFilter} onChange={e => setChipFilter(e.target.value)}
            placeholder={`Фильтр по выбранным (${values.length})...`}
            className="flex-1 bg-transparent text-[11px] text-slate-200 outline-none placeholder:text-slate-500" />
          {chipFilter && (
            <button type="button" onClick={() => setChipFilter('')} className="text-slate-500 hover:text-slate-300"><X size={10} /></button>
          )}
        </div>
      )}

      {/* chips + trigger */}
      <div onClick={toggleOpen}
        className="min-h-[42px] cursor-pointer rounded-lg border border-white/[.08] bg-black/[.2] px-3 py-2 transition-colors hover:border-white/[.14]">
        {values.length === 0 ? (
          <span className="text-[12px] text-slate-500">{placeholder}</span>
        ) : (
          <div className="flex flex-wrap gap-1.5">
            {visibleValues.map(val => (
              <span key={val}
                className={`inline-flex items-center gap-1 rounded-md border px-2 py-0.5 font-mono text-[11px] font-semibold ${isPattern(val) ? patternTagCls : coinTagCls}`}
                onClick={e => e.stopPropagation()}>
                {isPattern(val) ? <Asterisk size={9} className="opacity-70" /> : <CoinIcon symbol={val} className="w-3 h-3" />}
                {val.replace(/USDT$/i, '')}
                <button type="button" onClick={() => remove(val)} className="opacity-60 hover:opacity-100"><X size={9} /></button>
              </span>
            ))}
            {chipFilter && visibleValues.length < values.length && (
              <span className="self-center text-[10px] text-slate-500 italic">+{values.length - visibleValues.length} скрыто</span>
            )}
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
              : <Search size={13} className="shrink-0 text-slate-500" />}
            <input ref={inputRef} type="text" value={query} onChange={e => setQuery(e.target.value)}
              onKeyDown={e => { if (e.key === 'Enter' && isWildcard) { e.preventDefault(); addPattern(q) } }}
              placeholder="Монета или маска (1000*, *MEME*)"
              className="flex-1 bg-transparent text-[12px] text-slate-200 outline-none placeholder:text-slate-500" />
            {addedMsg && (
              <span className="text-[10px] font-semibold text-emerald-400 animate-pulse">{addedMsg}</span>
            )}
            {values.length > 0 && (
              <button type="button"
                onClick={() => { setSaveMode(v => !v); setCreateMode(false) }}
                className="inline-flex items-center gap-1 rounded-md border border-[#5b8cff]/25 bg-[#5b8cff]/[.10] px-2 py-1 text-[10px] font-semibold text-[#a0b8ff] hover:bg-[#5b8cff]/[.18]">
                <Save size={10} /> Сохранить
              </button>
            )}
          </div>

          {/* tab bar */}
          <div className="flex items-center gap-0.5 overflow-x-auto border-b border-white/[.06] px-2 py-1.5 scrollbar-none">
            {TABS.map(t => (
              <button
                key={t.id}
                type="button"
                onClick={() => handleTabClick(t.id)}
                className={`shrink-0 whitespace-nowrap rounded-md px-2.5 py-1 text-[10px] font-semibold transition-colors
                  ${activeTab === t.id
                    ? 'bg-white/[.08] text-slate-100'
                    : 'text-slate-500 hover:bg-white/[.04] hover:text-slate-300'}`}
              >
                {t.label}
              </button>
            ))}
          </div>

          {/* save current selection */}
          {saveMode && (
            <div className="flex items-center gap-1.5 border-b border-white/[.06] px-3 py-2">
              <input autoFocus type="text" value={saveName}
                onChange={e => setSaveName(e.target.value)}
                onKeyDown={e => e.key === 'Enter' && handleSave()}
                placeholder="Название списка..."
                className="flex-1 rounded-md border border-white/[.08] bg-black/[.3] px-2 py-1 text-[12px] text-slate-200 outline-none" />
              <button type="button" onClick={handleSave} disabled={!saveName.trim()}
                className="rounded-md bg-[#5b8cff]/[.20] px-3 py-1 text-[11px] font-semibold text-[#a0b8ff] hover:bg-[#5b8cff]/[.30] disabled:opacity-40">
                Сохранить
              </button>
            </div>
          )}

          {/* lists panel */}
          {showLists && !createMode && (
            <div className="border-b border-white/[.06]">
              {/* header */}
              <div className="flex items-center justify-between px-3 pt-2 pb-1">
                <span className="text-[9px] font-bold uppercase tracking-wider text-slate-500">Списки монет</span>
                <button type="button"
                  onClick={() => setCreateMode(true)}
                  className="inline-flex items-center gap-1 rounded-md border border-emerald-500/25 bg-emerald-500/[.08] px-2 py-0.5 text-[10px] font-semibold text-emerald-400 hover:bg-emerald-500/[.14]">
                  <Plus size={10} /> Создать
                </button>
              </div>

              {/* static presets */}
              <div className="px-2 pb-1">
                <div className="mb-0.5 px-1 text-[9px] font-semibold uppercase tracking-wider text-slate-600">Пресеты</div>
                {STATIC_PRESETS.map(list => (
                  <button key={list.name} type="button" onClick={() => loadList(list)}
                    className="flex w-full items-center gap-2 rounded-lg px-2 py-1.5 text-left hover:bg-white/[.04]">
                    <span className="flex-1 text-[12px] font-semibold text-slate-200">{list.name}</span>
                    <span className="text-[10px] text-slate-500">{list.symbols.length} монет</span>
                    <ChevronRight size={11} className="text-slate-600" />
                  </button>
                ))}
              </div>

              {/* dynamic top-N */}
              {dynamicPresets.length > 0 && (
                <div className="px-2 pb-1">
                  <div className="mb-0.5 px-1 text-[9px] font-semibold uppercase tracking-wider text-slate-600">По объёму 24ч</div>
                  {dynamicPresets.map(list => (
                    <button key={list.name} type="button" onClick={() => loadList(list)}
                      className="flex w-full items-center gap-2 rounded-lg px-2 py-1.5 text-left hover:bg-white/[.04]">
                      <span className="flex-1 text-[12px] font-semibold text-slate-200">{list.name}</span>
                      <span className="text-[10px] text-slate-500">{list.symbols.length} монет</span>
                      <ChevronRight size={11} className="text-slate-600" />
                    </button>
                  ))}
                </div>
              )}

              {/* user-saved lists */}
              <div className="px-2 pb-2">
                {savedLists.length > 0 && (
                  <div className="mb-0.5 px-1 text-[9px] font-semibold uppercase tracking-wider text-slate-600">Мои списки</div>
                )}
                {savedLists.length === 0 && (
                  <div className="py-2 text-center text-[11px] text-slate-600">Нет сохранённых списков</div>
                )}
                {savedLists.map(list => (
                  <button key={list.name} type="button" onClick={() => loadList(list)}
                    className="flex w-full items-center gap-2 rounded-lg px-2 py-1.5 text-left hover:bg-white/[.04]">
                    <span className="flex-1 text-[12px] font-semibold text-slate-200">{list.name}</span>
                    <span className="text-[10px] text-slate-500">{list.symbols.length} монет</span>
                    <button type="button" onClick={e => handleDeleteList(list.name, e)}
                      className="text-slate-600 hover:text-rose-400 transition-colors">
                      <Trash2 size={11} />
                    </button>
                  </button>
                ))}
              </div>
            </div>
          )}

          {/* create list form */}
          {showLists && createMode && (
            <div className="border-b border-white/[.06] p-3 flex flex-col gap-2.5">
              {/* header */}
              <div className="flex items-center gap-2">
                <button type="button" onClick={() => setCreateMode(false)} className="text-slate-500 hover:text-slate-200">
                  <ChevronRight size={13} className="rotate-180" />
                </button>
                <span className="text-[11px] font-bold text-slate-300">Новый список</span>
              </div>

              {/* name */}
              <input type="text" value={createName} onChange={e => setCreateName(e.target.value)}
                placeholder="Название списка..."
                className="w-full rounded-md border border-white/[.08] bg-black/[.3] px-2.5 py-1.5 text-[12px] text-slate-200 outline-none placeholder:text-slate-500" />

              {/* include */}
              <div>
                <div className="mb-1 text-[10px] font-semibold text-emerald-400">Включить (whitelist)</div>
                <PatternTagInput
                  tags={createInclude} onChange={setCreateInclude}
                  placeholder="BTC*, ETH*, SOL* — Enter"
                  accent="border-emerald-500/30 bg-emerald-500/[.12] text-emerald-300" />
                <div className="mt-0.5 text-[9px] text-slate-600">Пусто = все монеты</div>
              </div>

              {/* exclude */}
              <div>
                <div className="mb-1 text-[10px] font-semibold text-rose-400">Исключить (blacklist)</div>
                <PatternTagInput
                  tags={createExclude} onChange={setCreateExclude}
                  placeholder="BTC*, DOGE* — Enter"
                  accent="border-rose-500/30 bg-rose-500/[.12] text-rose-300" />
              </div>

              {/* preview */}
              <div className="flex items-center justify-between rounded-md border border-white/[.06] bg-black/[.2] px-2.5 py-1.5">
                <span className="text-[11px] text-slate-400">
                  {rows.length === 0 ? 'Загрузка тикеров...' : `Результат: ${resolvedCreate.length} монет`}
                </span>
                {resolvedCreate.length > 0 && (
                  <div className="flex gap-0.5 ml-2 overflow-hidden max-w-[140px]">
                    {resolvedCreate.slice(0, 4).map(s => (
                      <CoinIcon key={s} symbol={s} className="w-4 h-4 rounded-sm" />
                    ))}
                    {resolvedCreate.length > 4 && (
                      <span className="text-[9px] text-slate-500 self-center ml-0.5">+{resolvedCreate.length - 4}</span>
                    )}
                  </div>
                )}
              </div>

              {/* save */}
              <button type="button" onClick={handleCreateSave}
                disabled={!createName.trim() || resolvedCreate.length === 0}
                className="w-full rounded-md bg-emerald-500/[.15] py-1.5 text-[12px] font-semibold text-emerald-300
                  hover:bg-emerald-500/[.25] disabled:opacity-40 disabled:cursor-default transition-colors">
                Создать список ({resolvedCreate.length} монет)
              </button>
            </div>
          )}

          {/* wildcard mode */}
          {!showLists && isWildcard && (
            <div className="p-2">
              <button type="button" onClick={() => addPattern(q)} disabled={values.includes(q.toUpperCase())}
                className="flex w-full items-center gap-2.5 rounded-lg border border-amber-500/25 bg-amber-500/[.08] px-3 py-2 text-left transition-colors hover:bg-amber-500/[.14] disabled:opacity-40">
                <span className="flex h-6 w-6 shrink-0 items-center justify-center rounded-md border border-amber-500/30 bg-amber-500/[.15] text-amber-300">
                  <Asterisk size={11} />
                </span>
                <div className="min-w-0 flex-1">
                  <div className="font-mono text-[12px] font-semibold text-amber-200">{q.toUpperCase()}</div>
                  <div className="text-[10px] text-slate-400">
                    {wildcardMatches.length > 0
                      ? `Совпадает ${wildcardMatches.length} монет — нажмите Enter или кнопку`
                      : 'Нет совпадений в текущем списке тикеров'}
                  </div>
                </div>
                <span className="shrink-0 rounded-md bg-amber-500/[.20] px-2 py-0.5 text-[10px] font-bold text-amber-300">+ добавить маску</span>
              </button>

              {wildcardMatches.length > 0 && (
                <div className="mt-2">
                  <div className="mb-1 px-1 text-[9px] font-bold uppercase tracking-wider text-slate-500">Примеры совпадений</div>
                  <div className="flex flex-wrap gap-1.5 px-1">
                    {wildcardMatches.slice(0, 8).map(r => (
                      <span key={r.symbol}
                        className="inline-flex items-center gap-1 rounded-md border border-white/[.07] bg-white/[.03] px-2 py-0.5 font-mono text-[10px] text-slate-400">
                        <CoinIcon symbol={r.symbol} className="w-3 h-3" />
                        {r.symbol.replace('USDT', '')}
                      </span>
                    ))}
                    {wildcardMatches.length > 8 && (
                      <span className="px-1 py-0.5 text-[10px] text-slate-500">+{wildcardMatches.length - 8} ещё</span>
                    )}
                  </div>
                </div>
              )}
            </div>
          )}

          {/* regular coin list */}
          {!showLists && !isWildcard && (
            <>
              {/* active filter banner */}
              {activeTab !== 'all' && !q && (
                <div className="flex items-center justify-between border-b border-white/[.04] px-3 py-1.5">
                  <span className="text-[10px] text-slate-400">
                    {TABS.find(t => t.id === activeTab)?.label} — добавлено {getTabRows(activeTab).length} монет
                  </span>
                  <button
                    type="button"
                    onClick={() => setActiveTab('all')}
                    className="text-[10px] text-slate-500 hover:text-slate-300 transition-colors"
                  >
                    Сбросить фильтр
                  </button>
                </div>
              )}

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
                    <button key={row.symbol} type="button" onClick={() => toggle(row.symbol)}
                      className={`flex w-full items-center gap-2.5 rounded-lg px-2.5 py-1.5 transition-colors hover:bg-white/[.04] ${selected ? 'bg-white/[.035]' : ''}`}>
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
            </>
          )}

          {values.length > 0 && (
            <div className="flex items-center justify-between border-t border-white/[.06] px-3 py-2">
              <span className="text-[11px] text-slate-400">{values.length} записей</span>
              <button type="button" onClick={() => onChange([])} className="text-[11px] text-rose-400 hover:text-rose-300">Очистить</button>
            </div>
          )}
        </div>
      )}
    </div>
  )
}
