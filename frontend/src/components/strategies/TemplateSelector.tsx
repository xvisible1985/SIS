import { useState, useEffect, useRef } from 'react'
import { listTemplates, createTemplate, deleteTemplate } from '../../api/strategyTemplates'
import type { StrategyTemplate, StrategyFormData } from '../../types'

interface Props {
  formData: StrategyFormData
  onLoad: (config: Partial<StrategyFormData>) => void
  strategyType: 'grid' | 'matrix' | 'manual'
  onStrategyTypeChange: (t: 'grid' | 'matrix') => void
}

export function TemplateSelector({ formData, onLoad, strategyType, onStrategyTypeChange }: Props) {
  const [templates, setTemplates] = useState<StrategyTemplate[]>([])
  const [open, setOpen] = useState(false)
  const [selected, setSelected] = useState<string | null>(null)
  const [saving, setSaving] = useState(false)
  const [saveName, setSaveName] = useState('')
  const [showSaveRow, setShowSaveRow] = useState(false)
  const ref = useRef<HTMLDivElement>(null)

  useEffect(() => {
    listTemplates().then(setTemplates).catch(() => {})
  }, [])

  useEffect(() => {
    function handleClick(e: MouseEvent) {
      if (ref.current && !ref.current.contains(e.target as Node)) setOpen(false)
    }
    document.addEventListener('mousedown', handleClick)
    return () => document.removeEventListener('mousedown', handleClick)
  }, [])

  function handleLoad(tpl: StrategyTemplate) {
    setSelected(tpl.id)
    setOpen(false)
    onLoad(tpl.config as Partial<StrategyFormData>)
  }

  async function handleDelete(id: string, e: React.MouseEvent) {
    e.stopPropagation()
    await deleteTemplate(id)
    setTemplates(prev => prev.filter(t => t.id !== id))
    if (selected === id) setSelected(null)
  }

  async function handleSave() {
    if (!saveName.trim()) return
    setSaving(true)
    try {
      await createTemplate(saveName.trim(), { ...formData, symbol: '' })
      const fresh = await listTemplates()
      setTemplates(fresh)
      setShowSaveRow(false)
      setSaveName('')
    } finally {
      setSaving(false)
    }
  }

  const filteredTemplates = templates.filter(t => {
    const cfg = t.config as Partial<StrategyFormData>
    // Only show templates whose strategy_type matches current type.
    // Exclude old templates without strategy_type — they may contain
    // incompatible fields (e.g. grid steps inside a DCA strategy).
    return cfg.strategy_type === strategyType
  })

  const selectedName = filteredTemplates.find(t => t.id === selected)?.name
    ?? (selected && templates.find(t => t.id === selected)?.name)

  return (
    <div className="flex items-center gap-2 px-4 py-2 bg-gray-900/60 border-b border-gray-700">
      {/* Strategy type switcher */}
      <div className="flex gap-1 shrink-0">
        {(['grid', 'matrix'] as const).map(t => (
          <button
            key={t}
            type="button"
            onClick={() => onStrategyTypeChange(t)}
            className={`rounded-lg border px-3 py-1 text-[11px] font-semibold transition-colors ${
              strategyType === t
                ? 'border-[#5b8cff]/40 bg-[#5b8cff]/[.18] text-[#a0b8ff]'
                : 'border-white/[.07] bg-white/[.02] text-slate-400 hover:bg-white/[.05]'
            }`}
          >
            {t === 'grid' ? 'Grid' : 'Matrix'}
          </button>
        ))}
      </div>

      <div className="w-px h-4 bg-gray-700 shrink-0" />

      {/* Template selector */}
      <span className="text-gray-500 text-xs shrink-0">Шаблон:</span>
      <div className="relative flex-1" ref={ref}>
        <button
          onClick={() => setOpen(v => !v)}
          className="w-full flex items-center justify-between bg-gray-800 border border-gray-700 rounded px-3 py-1.5 text-[11px] text-left"
        >
          <span className={selectedName ? 'text-gray-100' : 'text-gray-500'}>
            {selectedName ?? 'Без шаблона'}
          </span>
          <span className="text-gray-500 ml-2">▾</span>
        </button>
        {open && (
          <div className="absolute top-full mt-1 left-0 right-0 bg-gray-800 border border-gray-600 rounded-lg z-50 overflow-hidden shadow-xl">
            {filteredTemplates.length === 0 ? (
              <div className="px-3 py-2 text-[11px] text-gray-500">Нет сохранённых шаблонов</div>
            ) : filteredTemplates.map(tpl => (
              <div
                key={tpl.id}
                onClick={() => handleLoad(tpl)}
                className="flex items-center justify-between px-3 py-2 hover:bg-gray-700 cursor-pointer border-b border-gray-700/50 last:border-0"
              >
                <div className="text-[11px] text-gray-100">{tpl.name}</div>
                <button
                  onClick={e => handleDelete(tpl.id, e)}
                  className="text-red-400 text-[10px] px-1.5 hover:text-red-300 opacity-60 hover:opacity-100"
                >
                  🗑
                </button>
              </div>
            ))}
          </div>
        )}
      </div>

      {showSaveRow ? (
        <div className="flex items-center gap-2">
          <input
            value={saveName}
            onChange={e => setSaveName(e.target.value)}
            placeholder="Имя шаблона"
            className="bg-gray-800 border border-gray-600 rounded px-2 py-1 text-[11px] text-gray-100 w-36"
            onKeyDown={e => e.key === 'Enter' && handleSave()}
          />
          <button
            onClick={handleSave}
            disabled={saving}
            className="bg-green-900/50 text-green-400 border border-green-800 rounded px-3 py-1.5 text-[11px] shrink-0 disabled:opacity-50"
          >
            {saving ? '…' : 'Сохранить'}
          </button>
          <button onClick={() => setShowSaveRow(false)} className="text-gray-500 text-lg leading-none">✕</button>
        </div>
      ) : (
        <button
          onClick={() => setShowSaveRow(true)}
          className="shrink-0 bg-blue-900/40 text-blue-400 border border-blue-800 rounded px-3 py-1.5 text-[11px]"
        >
          💾 Сохранить
        </button>
      )}
    </div>
  )
}
