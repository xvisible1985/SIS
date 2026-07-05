import { useState, useEffect } from 'react'
import { Plus, Trash2, Pencil, Check, X, Loader2, Bot } from 'lucide-react'
import {
  listAdminBotPresets,
  createBotPreset,
  patchBotPreset,
  deleteBotPreset,
  addBotToPreset,
  removeBotFromPreset,
  type BotPreset,
  type PresetBot,
} from '../../api/botPresets'
import { apiClient } from '../../api/client'

const AGG_LABELS: Record<string, string> = {
  conservative: 'Консервативный',
  moderate: 'Умеренный',
  aggressive: 'Агрессивный',
}
const AGG_COLORS: Record<string, string> = {
  conservative: 'border-emerald-500/30 bg-emerald-500/[.12] text-emerald-400',
  moderate:     'border-amber-500/30 bg-amber-500/[.12] text-amber-400',
  aggressive:   'border-rose-500/30 bg-rose-500/[.12] text-rose-400',
}
const COIN_LABELS: Record<string, string> = {
  stable: 'Стабильные (BTC/ETH)',
  meme:   'Мемы (DOGE/PEPE)',
  both:   'Любые',
}
const STYLE_LABELS: Record<string, string> = {
  diversified:  'Много монет',
  concentrated: '1-2 монеты',
  both:         'Любой стиль',
}

interface SimpleBotOption {
  id: string
  name: string
  avatar_url?: string
}

function useCatalogBots() {
  const [bots, setBots] = useState<SimpleBotOption[]>([])
  useEffect(() => {
    apiClient.get<{ catalog: { id: string; name: string; avatar_url?: string }[] }>('/bots')
      .then(r => setBots(r.data.catalog ?? []))
      .catch(() => {})
  }, [])
  return bots
}

// ── Форма создания/редактирования пресета ───────────────────────────────────
interface PresetFormProps {
  initial?: BotPreset
  onSave: (data: Omit<BotPreset, 'id' | 'bots'>) => Promise<void>
  onCancel: () => void
}

function PresetForm({ initial, onSave, onCancel }: PresetFormProps) {
  const [name, setName] = useState(initial?.name ?? '')
  const [description, setDescription] = useState(initial?.description ?? '')
  const [aggressiveness, setAggressiveness] = useState<BotPreset['aggressiveness']>(
    initial?.aggressiveness ?? 'moderate'
  )
  const [coinType, setCoinType] = useState<BotPreset['coin_type']>(initial?.coin_type ?? 'both')
  const [tradingStyle, setTradingStyle] = useState<BotPreset['trading_style']>(
    initial?.trading_style ?? 'both'
  )
  const [tags, setTags] = useState<string[]>(initial?.tags ?? [])
  const [tagInput, setTagInput] = useState('')
  const [isActive, setIsActive] = useState(initial?.is_active ?? true)
  const [sortOrder, setSortOrder] = useState(initial?.sort_order ?? 0)
  const [saving, setSaving] = useState(false)

  function addTag() {
    const t = tagInput.trim().toLowerCase()
    if (t && !tags.includes(t)) setTags(prev => [...prev, t])
    setTagInput('')
  }

  async function handleSubmit(e: React.FormEvent) {
    e.preventDefault()
    if (!name.trim()) return
    setSaving(true)
    try {
      await onSave({ name, description, aggressiveness, coin_type: coinType, trading_style: tradingStyle, tags, is_active: isActive, sort_order: sortOrder })
    } finally {
      setSaving(false)
    }
  }

  const btnBase = 'px-3 py-1.5 rounded-lg border text-[12px] font-semibold transition-colors'
  const btnActive = 'border-[#4a7dff]/50 bg-[#4a7dff]/20 text-[#7b9fff]'
  const btnInactive = 'border-white/10 bg-white/[.04] text-slate-400 hover:border-white/20'

  function OptionGroup<T extends string>({
    label, options, value, onChange,
  }: { label: string; options: { value: T; label: string }[]; value: T; onChange: (v: T) => void }) {
    return (
      <div>
        <label className="mb-1.5 block text-[11px] font-semibold uppercase tracking-[1px] text-slate-500">{label}</label>
        <div className="flex flex-wrap gap-2">
          {options.map(o => (
            <button key={o.value} type="button" onClick={() => onChange(o.value)}
              className={`${btnBase} ${value === o.value ? btnActive : btnInactive}`}>
              {o.label}
            </button>
          ))}
        </div>
      </div>
    )
  }

  return (
    <form onSubmit={handleSubmit} className="flex flex-col gap-4">
      <div>
        <label className="mb-1 block text-[11px] font-semibold uppercase tracking-[1px] text-slate-500">Название</label>
        <input
          value={name} onChange={e => setName(e.target.value)} required
          className="w-full rounded-lg border border-white/10 bg-white/[.04] px-3 py-2 text-[13px] text-slate-100 outline-none focus:border-[#4a7dff]/50"
          placeholder="Пресет для начинающих"
        />
      </div>

      <div>
        <label className="mb-1 block text-[11px] font-semibold uppercase tracking-[1px] text-slate-500">Описание</label>
        <textarea
          value={description} onChange={e => setDescription(e.target.value)}
          rows={2}
          className="w-full resize-none rounded-lg border border-white/10 bg-white/[.04] px-3 py-2 text-[13px] text-slate-100 outline-none focus:border-[#4a7dff]/50"
          placeholder="Краткое описание стратегии"
        />
      </div>

      <OptionGroup
        label="Агрессивность"
        value={aggressiveness}
        onChange={setAggressiveness}
        options={[
          { value: 'conservative', label: 'Консервативный' },
          { value: 'moderate', label: 'Умеренный' },
          { value: 'aggressive', label: 'Агрессивный' },
        ]}
      />

      <OptionGroup
        label="Монеты"
        value={coinType}
        onChange={setCoinType}
        options={[
          { value: 'stable', label: 'Стабильные (BTC/ETH)' },
          { value: 'meme', label: 'Мемы' },
          { value: 'both', label: 'Любые' },
        ]}
      />

      <OptionGroup
        label="Стиль торговли"
        value={tradingStyle}
        onChange={setTradingStyle}
        options={[
          { value: 'diversified', label: 'Много монет' },
          { value: 'concentrated', label: '1-2 монеты' },
          { value: 'both', label: 'Любой' },
        ]}
      />

      <div>
        <label className="mb-1 block text-[11px] font-semibold uppercase tracking-[1px] text-slate-500">Теги</label>
        <div className="flex flex-wrap gap-1.5 mb-2">
          {tags.map(t => (
            <span key={t} className="flex items-center gap-1 rounded-full border border-white/10 bg-white/[.06] px-2 py-0.5 text-[11px] text-slate-300">
              {t}
              <button type="button" onClick={() => setTags(prev => prev.filter(x => x !== t))} className="text-slate-500 hover:text-slate-200">
                <X size={10} />
              </button>
            </span>
          ))}
        </div>
        <div className="flex gap-2">
          <input
            value={tagInput} onChange={e => setTagInput(e.target.value)}
            onKeyDown={e => { if (e.key === 'Enter') { e.preventDefault(); addTag() } }}
            className="flex-1 rounded-lg border border-white/10 bg-white/[.04] px-3 py-1.5 text-[12px] text-slate-100 outline-none focus:border-[#4a7dff]/50"
            placeholder="recovery, ..."
          />
          <button type="button" onClick={addTag}
            className="rounded-lg border border-white/10 bg-white/[.04] px-3 py-1.5 text-[12px] text-slate-300 hover:bg-white/[.08]">
            + Добавить
          </button>
        </div>
      </div>

      <div className="flex items-center gap-4">
        <div>
          <label className="mb-1 block text-[11px] font-semibold uppercase tracking-[1px] text-slate-500">Порядок</label>
          <input type="number" value={sortOrder} onChange={e => setSortOrder(Number(e.target.value))}
            className="w-20 rounded-lg border border-white/10 bg-white/[.04] px-3 py-1.5 text-[13px] text-slate-100 outline-none focus:border-[#4a7dff]/50" />
        </div>
        <label className="flex cursor-pointer items-center gap-2 mt-4">
          <div onClick={() => setIsActive(v => !v)}
            className={`relative h-5 w-9 rounded-full border transition-colors ${isActive ? 'border-emerald-500/50 bg-emerald-500/30' : 'border-white/10 bg-white/[.06]'}`}>
            <div className={`absolute top-0.5 h-4 w-4 rounded-full transition-all ${isActive ? 'left-4 bg-emerald-400' : 'left-0.5 bg-slate-500'}`} />
          </div>
          <span className="text-[12px] text-slate-400">Активен</span>
        </label>
      </div>

      <div className="flex justify-end gap-2 border-t border-white/[.06] pt-3">
        <button type="button" onClick={onCancel}
          className="rounded-lg border border-white/10 px-4 py-2 text-[12px] text-slate-400 hover:bg-white/[.04]">
          Отмена
        </button>
        <button type="submit" disabled={saving}
          className="flex items-center gap-1.5 rounded-lg bg-[#4a7dff]/20 border border-[#4a7dff]/40 px-4 py-2 text-[12px] font-semibold text-[#7b9fff] hover:bg-[#4a7dff]/30 disabled:opacity-50">
          {saving ? <Loader2 size={13} className="animate-spin" /> : <Check size={13} />}
          {initial ? 'Сохранить' : 'Создать'}
        </button>
      </div>
    </form>
  )
}

// ── Диалог добавления бота в пресет ────────────────────────────────────────
function AddBotDialog({
  presetId,
  catalogBots,
  onAdded,
  onClose,
}: {
  presetId: string
  catalogBots: SimpleBotOption[]
  onAdded: (bot: PresetBot) => void
  onClose: () => void
}) {
  const [botId, setBotId] = useState('')
  const [role, setRole] = useState<'trading' | 'hedging'>('trading')
  const [saving, setSaving] = useState(false)
  const [search, setSearch] = useState('')

  const filtered = catalogBots.filter(b =>
    b.name.toLowerCase().includes(search.toLowerCase())
  )

  async function handleAdd() {
    if (!botId) return
    setSaving(true)
    try {
      const entry = await addBotToPreset(presetId, { bot_id: botId, role })
      onAdded(entry)
      onClose()
    } catch {
      setSaving(false)
    }
  }

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/60 backdrop-blur-sm">
      <div className="w-[420px] rounded-2xl border border-white/[.08] bg-[#111827] shadow-2xl">
        <div className="flex items-center justify-between border-b border-white/[.06] px-5 py-3">
          <h3 className="text-[13px] font-semibold text-slate-100">Добавить бота в пресет</h3>
          <button onClick={onClose} className="text-slate-500 hover:text-slate-200"><X size={16} /></button>
        </div>
        <div className="p-4 flex flex-col gap-3">
          <input value={search} onChange={e => setSearch(e.target.value)}
            className="w-full rounded-lg border border-white/10 bg-white/[.04] px-3 py-2 text-[13px] text-slate-100 outline-none focus:border-[#4a7dff]/50"
            placeholder="Поиск бота..." />
          <div className="max-h-[200px] overflow-y-auto flex flex-col gap-1">
            {filtered.map(b => (
              <button key={b.id} type="button" onClick={() => setBotId(b.id)}
                className={`flex items-center gap-2 rounded-lg px-3 py-2 text-left text-[13px] transition-colors ${
                  botId === b.id ? 'bg-[#4a7dff]/20 border border-[#4a7dff]/40 text-[#7b9fff]' : 'border border-transparent hover:bg-white/[.04] text-slate-300'
                }`}>
                <Bot size={14} className="shrink-0" />
                {b.name}
              </button>
            ))}
            {filtered.length === 0 && (
              <p className="px-3 py-2 text-[12px] text-slate-500">Ботов не найдено</p>
            )}
          </div>

          <div>
            <label className="mb-1.5 block text-[11px] font-semibold uppercase tracking-[1px] text-slate-500">Роль</label>
            <div className="flex gap-2">
              {(['trading', 'hedging'] as const).map(r => (
                <button key={r} type="button" onClick={() => setRole(r)}
                  className={`flex-1 rounded-lg border py-2 text-[12px] font-semibold transition-colors ${
                    role === r ? 'border-[#4a7dff]/50 bg-[#4a7dff]/20 text-[#7b9fff]' : 'border-white/10 bg-white/[.04] text-slate-400 hover:border-white/20'
                  }`}>
                  {r === 'trading' ? 'Торговый' : 'Хеджирующий'}
                </button>
              ))}
            </div>
          </div>
        </div>
        <div className="flex justify-end gap-2 border-t border-white/[.06] px-4 py-3">
          <button onClick={onClose} className="rounded-lg border border-white/10 px-4 py-2 text-[12px] text-slate-400 hover:bg-white/[.04]">
            Отмена
          </button>
          <button onClick={handleAdd} disabled={!botId || saving}
            className="flex items-center gap-1.5 rounded-lg bg-[#4a7dff]/20 border border-[#4a7dff]/40 px-4 py-2 text-[12px] font-semibold text-[#7b9fff] hover:bg-[#4a7dff]/30 disabled:opacity-50">
            {saving ? <Loader2 size={13} className="animate-spin" /> : <Plus size={13} />}
            Добавить
          </button>
        </div>
      </div>
    </div>
  )
}

// ── Карточка пресета ────────────────────────────────────────────────────────
function PresetCard({
  preset,
  catalogBots,
  onEdit,
  onDelete,
  onToggleActive,
  onBotAdded,
  onBotRemoved,
}: {
  preset: BotPreset
  catalogBots: SimpleBotOption[]
  onEdit: () => void
  onDelete: () => void
  onToggleActive: () => void
  onBotAdded: (bot: PresetBot) => void
  onBotRemoved: (entryId: string) => void
}) {
  const [addBotOpen, setAddBotOpen] = useState(false)

  async function handleRemoveBot(entryId: string) {
    try {
      await removeBotFromPreset(preset.id, entryId)
      onBotRemoved(entryId)
    } catch {}
  }

  return (
    <>
      <div className={`rounded-xl border bg-white/[.025] p-4 transition-colors ${preset.is_active ? 'border-white/[.08]' : 'border-white/[.04] opacity-60'}`}>
        <div className="flex items-start justify-between gap-3 mb-3">
          <div className="flex-1 min-w-0">
            <div className="flex items-center gap-2 flex-wrap">
              <span className="text-[14px] font-semibold text-slate-100">{preset.name}</span>
              <span className={`rounded-[4px] border px-1.5 py-px text-[10px] font-bold ${AGG_COLORS[preset.aggressiveness]}`}>
                {AGG_LABELS[preset.aggressiveness]}
              </span>
              {!preset.is_active && (
                <span className="rounded-[4px] border border-slate-500/30 bg-slate-500/10 px-1.5 py-px text-[10px] font-bold text-slate-500">
                  Неактивен
                </span>
              )}
            </div>
            {preset.description && (
              <p className="mt-0.5 text-[12px] text-slate-500 line-clamp-1">{preset.description}</p>
            )}
            <div className="mt-1.5 flex flex-wrap gap-1.5">
              <span className="rounded-full border border-white/[.07] bg-white/[.04] px-2 py-px text-[10px] text-slate-400">
                {COIN_LABELS[preset.coin_type]}
              </span>
              <span className="rounded-full border border-white/[.07] bg-white/[.04] px-2 py-px text-[10px] text-slate-400">
                {STYLE_LABELS[preset.trading_style]}
              </span>
              {preset.tags.map(t => (
                <span key={t} className="rounded-full border border-violet-500/25 bg-violet-500/[.08] px-2 py-px text-[10px] text-violet-400">
                  #{t}
                </span>
              ))}
            </div>
          </div>
          <div className="flex items-center gap-1 shrink-0">
            <button onClick={onToggleActive} title={preset.is_active ? 'Деактивировать' : 'Активировать'}
              className="rounded-lg border border-white/10 p-1.5 text-slate-500 hover:text-slate-200 hover:bg-white/[.06]">
              {preset.is_active ? <X size={13} /> : <Check size={13} />}
            </button>
            <button onClick={onEdit}
              className="rounded-lg border border-white/10 p-1.5 text-slate-500 hover:text-slate-200 hover:bg-white/[.06]">
              <Pencil size={13} />
            </button>
            <button onClick={onDelete}
              className="rounded-lg border border-white/10 p-1.5 text-slate-500 hover:text-rose-400 hover:bg-rose-500/[.08]">
              <Trash2 size={13} />
            </button>
          </div>
        </div>

        {/* Боты пресета */}
        <div className="flex flex-col gap-1">
          {preset.bots.map(b => (
            <div key={b.entry_id} className="flex items-center gap-2 rounded-lg border border-white/[.05] bg-white/[.02] px-3 py-1.5">
              <Bot size={12} className="text-slate-500 shrink-0" />
              <span className="flex-1 text-[12px] text-slate-300 truncate">{b.name}</span>
              <span className={`rounded-[3px] border px-1.5 py-px text-[9px] font-bold ${
                b.role === 'trading'
                  ? 'border-emerald-500/30 bg-emerald-500/[.1] text-emerald-400'
                  : 'border-violet-500/30 bg-violet-500/[.1] text-violet-400'
              }`}>
                {b.role === 'trading' ? 'торговый' : 'хеджирующий'}
              </span>
              <button onClick={() => handleRemoveBot(b.entry_id)}
                className="text-slate-600 hover:text-rose-400 transition-colors">
                <X size={12} />
              </button>
            </div>
          ))}
          <button onClick={() => setAddBotOpen(true)}
            className="flex items-center gap-1.5 rounded-lg border border-dashed border-white/[.08] px-3 py-1.5 text-[11px] text-slate-500 hover:border-white/20 hover:text-slate-300 transition-colors">
            <Plus size={12} />
            Добавить бота
          </button>
        </div>
      </div>

      {addBotOpen && (
        <AddBotDialog
          presetId={preset.id}
          catalogBots={catalogBots}
          onAdded={onBotAdded}
          onClose={() => setAddBotOpen(false)}
        />
      )}
    </>
  )
}

// ── Главный компонент ───────────────────────────────────────────────────────
export function PresetsAdminTab() {
  const [presets, setPresets] = useState<BotPreset[]>([])
  const [loading, setLoading] = useState(true)
  const [creating, setCreating] = useState(false)
  const [editingPreset, setEditingPreset] = useState<BotPreset | null>(null)
  const catalogBots = useCatalogBots()

  useEffect(() => {
    listAdminBotPresets()
      .then(setPresets)
      .catch(() => {})
      .finally(() => setLoading(false))
  }, [])

  async function handleCreate(data: Omit<BotPreset, 'id' | 'bots'>) {
    const preset = await createBotPreset(data)
    setPresets(prev => [...prev, { ...preset, bots: [] }])
    setCreating(false)
  }

  async function handleEdit(data: Omit<BotPreset, 'id' | 'bots'>) {
    if (!editingPreset) return
    const updated = await patchBotPreset(editingPreset.id, data)
    setPresets(prev => prev.map(p => p.id === editingPreset.id ? { ...updated, bots: editingPreset.bots } : p))
    setEditingPreset(null)
  }

  async function handleDelete(id: string) {
    if (!confirm('Удалить пресет?')) return
    await deleteBotPreset(id)
    setPresets(prev => prev.filter(p => p.id !== id))
  }

  async function handleToggleActive(preset: BotPreset) {
    const updated = await patchBotPreset(preset.id, { is_active: !preset.is_active })
    setPresets(prev => prev.map(p => p.id === preset.id ? { ...p, is_active: updated.is_active } : p))
  }

  function handleBotAdded(presetId: string, bot: PresetBot) {
    setPresets(prev => prev.map(p =>
      p.id === presetId ? { ...p, bots: [...p.bots, bot] } : p
    ))
  }

  function handleBotRemoved(presetId: string, entryId: string) {
    setPresets(prev => prev.map(p =>
      p.id === presetId ? { ...p, bots: p.bots.filter(b => b.entry_id !== entryId) } : p
    ))
  }

  return (
    <div className="flex h-full flex-col overflow-hidden">
      <div className="flex items-center justify-between border-b border-white/[.06] px-5 py-3">
        <div className="flex items-baseline gap-3">
          <h2 className="m-0 text-sm font-semibold text-slate-100">Пресеты</h2>
          <span className="text-[11px] text-slate-400">
            {presets.filter(p => p.is_active).length} активных · {presets.length} всего
          </span>
        </div>
        <button
          onClick={() => { setEditingPreset(null); setCreating(true) }}
          className="flex items-center gap-1.5 rounded-lg border border-[#4a7dff]/35 bg-[#4a7dff]/[.12] px-3 py-1.5 text-[12px] font-semibold text-[#7b9fff] hover:bg-[#4a7dff]/20 transition-colors"
        >
          <Plus size={13} /> Новый пресет
        </button>
      </div>

      <div className="flex-1 overflow-y-auto p-4">
        {loading ? (
          <div className="flex items-center justify-center gap-2 py-12 text-slate-500 text-[13px]">
            <Loader2 size={16} className="animate-spin" /> Загрузка...
          </div>
        ) : (creating || editingPreset) ? (
          <div className="mx-auto max-w-xl rounded-2xl border border-white/[.08] bg-white/[.025] p-5">
            <h3 className="mb-4 text-[14px] font-semibold text-slate-100">
              {editingPreset ? 'Редактировать пресет' : 'Новый пресет'}
            </h3>
            <PresetForm
              initial={editingPreset ?? undefined}
              onSave={editingPreset ? handleEdit : handleCreate}
              onCancel={() => { setCreating(false); setEditingPreset(null) }}
            />
          </div>
        ) : presets.length === 0 ? (
          <div className="flex flex-col items-center justify-center py-16 text-center">
            <p className="text-[13px] text-slate-500 mb-4">Пресетов ещё нет</p>
            <button onClick={() => setCreating(true)}
              className="flex items-center gap-1.5 rounded-lg border border-[#4a7dff]/35 bg-[#4a7dff]/[.12] px-4 py-2 text-[13px] font-semibold text-[#7b9fff]">
              <Plus size={14} /> Создать первый пресет
            </button>
          </div>
        ) : (
          <div className="flex flex-col gap-3 max-w-2xl">
            {presets.map(preset => (
              <PresetCard
                key={preset.id}
                preset={preset}
                catalogBots={catalogBots}
                onEdit={() => { setCreating(false); setEditingPreset(preset) }}
                onDelete={() => handleDelete(preset.id)}
                onToggleActive={() => handleToggleActive(preset)}
                onBotAdded={bot => handleBotAdded(preset.id, bot)}
                onBotRemoved={entryId => handleBotRemoved(preset.id, entryId)}
              />
            ))}
          </div>
        )}
      </div>
    </div>
  )
}
