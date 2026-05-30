import { useEffect } from 'react'
import { TrendingUp, Search, Shield } from 'lucide-react'
import { BOT_KINDS, BOT_KIND_META } from '../botKindMeta'
import type { BotKind } from '../types'

const KIND_ICONS: Record<BotKind, React.ComponentType<{ size?: number; strokeWidth?: number }>> = {
  signal: TrendingUp,
  parser: Search,
  hedge:  Shield,
}

type Props = {
  onSelect: (kind: BotKind) => void
  onClose:  () => void
}

export function BotTypePickerModal({ onSelect, onClose }: Props) {
  // Закрытие по Escape
  useEffect(() => {
    const handler = (e: KeyboardEvent) => { if (e.key === 'Escape') onClose() }
    window.addEventListener('keydown', handler)
    return () => window.removeEventListener('keydown', handler)
  }, [onClose])

  return (
    <div
      className="fixed inset-0 z-50 flex items-center justify-center"
      style={{ background: 'rgba(0,0,0,0.72)', backdropFilter: 'blur(4px)' }}
      onClick={(e) => { if (e.target === e.currentTarget) onClose() }}
    >
      <div
        className="relative w-full max-w-[760px] rounded-2xl border p-6"
        style={{
          background:   'linear-gradient(180deg,#10141f 0%,#0c1018 100%)',
          borderColor:  'rgba(255,255,255,0.08)',
          boxShadow:    '0 32px 80px -20px rgba(0,0,0,0.8)',
        }}
        onClick={(e) => e.stopPropagation()}
      >
        {/* заголовок */}
        <div className="mb-5">
          <div className="text-[18px] font-bold tracking-tight text-slate-50">Выберите тип бота</div>
          <div className="mt-1 text-[13px] text-slate-400">
            Тип определяет логику работы. Настройки можно изменить позже.
          </div>
        </div>

        {/* карточки */}
        <div className="grid grid-cols-3 gap-3.5">
          {BOT_KINDS.map((kind) => {
            const m    = BOT_KIND_META[kind]
            const Icon = KIND_ICONS[kind]

            return (
              <button
                key={kind}
                type="button"
                onClick={() => onSelect(kind)}
                className="group relative flex flex-col overflow-hidden rounded-[14px] border text-left transition-all duration-150"
                style={{
                  borderColor: m.border,
                  background:  'rgba(255,255,255,0.02)',
                }}
                onMouseEnter={(e) => {
                  ;(e.currentTarget as HTMLElement).style.background = m.bg
                  ;(e.currentTarget as HTMLElement).style.boxShadow = `0 8px 28px -8px ${m.border}`
                }}
                onMouseLeave={(e) => {
                  ;(e.currentTarget as HTMLElement).style.background = 'rgba(255,255,255,0.02)'
                  ;(e.currentTarget as HTMLElement).style.boxShadow = 'none'
                }}
              >
                {/* цветная шапка */}
                <div
                  className="px-4 pt-4 pb-3"
                  style={{ background: m.bgHeader }}
                >
                  {/* иконка */}
                  <div
                    className="mb-3 flex h-10 w-10 items-center justify-center rounded-xl border"
                    style={{
                      background:  m.iconBg,
                      borderColor: m.border,
                      color:       m.color,
                    }}
                  >
                    <Icon size={18} strokeWidth={2} />
                  </div>

                  {/* название */}
                  <div
                    className="font-display text-[17px] font-bold tracking-tight"
                    style={{ color: m.color }}
                  >
                    {m.label}
                  </div>

                  {/* тэглайн */}
                  <div className="mt-0.5 text-[12px] font-medium text-slate-400">
                    {m.tagline}
                  </div>
                </div>

                {/* описание */}
                <div className="px-4 py-3">
                  <p className="text-[12px] leading-relaxed text-slate-400">
                    {m.desc}
                  </p>
                </div>

                {/* кнопка-подсказка */}
                <div
                  className="mx-4 mb-4 flex items-center justify-center rounded-lg border py-1.5 text-[11px] font-semibold transition-colors"
                  style={{
                    borderColor: m.border,
                    color:       m.color,
                    background:  m.iconBg,
                  }}
                >
                  Выбрать {m.label} →
                </div>
              </button>
            )
          })}
        </div>

        {/* закрыть */}
        <button
          type="button"
          onClick={onClose}
          className="absolute right-4 top-4 flex h-7 w-7 items-center justify-center rounded-full text-slate-500 hover:bg-white/[.06] hover:text-slate-300 transition-colors"
        >
          <svg width="13" height="13" viewBox="0 0 13 13" fill="none">
            <path d="M1 1l11 11M12 1L1 12" stroke="currentColor" strokeWidth="1.6" strokeLinecap="round"/>
          </svg>
        </button>
      </div>
    </div>
  )
}
