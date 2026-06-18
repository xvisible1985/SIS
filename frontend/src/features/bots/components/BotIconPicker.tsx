import { useState } from 'react'

// ── Preset bot icons ──────────────────────────────────────────────────────────

export const BOT_ICONS: { id: string; label: string; url: string }[] = [
  { id: 'robot',    label: 'Робот',    url: '/bot-icons/robot.svg'    },
  { id: 'rocket',   label: 'Ракета',   url: '/bot-icons/rocket.svg'   },
  { id: 'lightning',label: 'Молния',   url: '/bot-icons/lightning.svg'},
  { id: 'grid',     label: 'Сетка',    url: '/bot-icons/grid.svg'     },
  { id: 'diamond',  label: 'Алмаз',    url: '/bot-icons/diamond.svg'  },
  { id: 'bull',     label: 'Бык',      url: '/bot-icons/bull.svg'     },
  { id: 'bear',     label: 'Медведь',  url: '/bot-icons/bear.svg'     },
  { id: 'shield',   label: 'Щит',      url: '/bot-icons/shield.svg'   },
  { id: 'target',   label: 'Прицел',   url: '/bot-icons/target.svg'   },
  { id: 'atom',     label: 'Атом',     url: '/bot-icons/atom.svg'     },
  { id: 'flame',    label: 'Огонь',    url: '/bot-icons/flame.svg'    },
  { id: 'eye',      label: 'Глаз',     url: '/bot-icons/eye.svg'      },
  { id: 'crystal',  label: 'Шар',      url: '/bot-icons/crystal.svg'  },
  { id: 'compass',  label: 'Компас',   url: '/bot-icons/compass.svg'  },
  { id: 'gear',     label: 'Шестерня', url: '/bot-icons/gear.svg'     },
  { id: 'dragon',   label: 'Дракон',   url: '/bot-icons/dragon.svg'   },
  { id: 'phoenix',  label: 'Феникс',   url: '/bot-icons/phoenix.svg'  },
  { id: 'wave',     label: 'Волна',    url: '/bot-icons/wave.svg'     },
  { id: 'fox',      label: 'Лиса',     url: '/bot-icons/fox.svg'      },
  { id: 'snake',    label: 'Змея',     url: '/bot-icons/snake.svg'    },
  { id: 'owl',      label: 'Сова',     url: '/bot-icons/owl.svg'      },
  { id: 'shark',    label: 'Акула',    url: '/bot-icons/shark.svg'    },
  { id: 'wolf',     label: 'Волк',     url: '/bot-icons/wolf.svg'     },
  { id: 'eagle',    label: 'Орёл',     url: '/bot-icons/eagle.svg'    },
]

// ── Component ─────────────────────────────────────────────────────────────────

interface Props {
  value: string    // current avatarUrl
  onChange: (url: string) => void
  onClose: () => void
}

export function BotIconPicker({ value, onChange, onClose }: Props) {
  const [hovered, setHovered] = useState<string | null>(null)

  function handleSelect(url: string) {
    onChange(url)
    onClose()
  }

  return (
    <div className="absolute top-full left-0 z-50 mt-2 w-[280px] rounded-[14px] border border-white/[.08] bg-[#101828] shadow-2xl p-3">
      {/* header */}
      <div className="flex items-center justify-between mb-3">
        <span className="text-[11px] font-semibold text-slate-400 uppercase tracking-wider">Выберите иконку</span>
        <button
          type="button"
          onClick={onClose}
          className="text-slate-500 hover:text-slate-300 text-lg leading-none"
        >×</button>
      </div>

      {/* grid */}
      <div className="grid grid-cols-6 gap-1.5">
        {BOT_ICONS.map(icon => {
          const isActive = value === icon.url
          return (
            <button
              key={icon.id}
              type="button"
              title={icon.label}
              onClick={() => handleSelect(icon.url)}
              onMouseEnter={() => setHovered(icon.id)}
              onMouseLeave={() => setHovered(null)}
              className={`
                relative h-10 w-10 rounded-[8px] overflow-hidden border transition-all
                ${isActive
                  ? 'border-[#5b8cff] ring-1 ring-[#5b8cff]/50 scale-105'
                  : 'border-white/[.06] hover:border-white/25 hover:scale-105'
                }
              `}
            >
              <img
                src={icon.url}
                alt={icon.label}
                className="h-full w-full object-cover"
                draggable={false}
              />
              {/* tooltip on hover */}
              {hovered === icon.id && (
                <div className="pointer-events-none absolute -top-7 left-1/2 -translate-x-1/2 whitespace-nowrap rounded-md bg-gray-900 px-2 py-0.5 text-[10px] text-slate-200 border border-white/[.08]">
                  {icon.label}
                </div>
              )}
            </button>
          )
        })}
      </div>

      {/* clear button */}
      {value && BOT_ICONS.some(i => i.url === value) && (
        <button
          type="button"
          onClick={() => { onChange(''); onClose() }}
          className="mt-3 w-full text-center text-[11px] text-rose-400 hover:text-rose-300"
        >
          Убрать иконку
        </button>
      )}
    </div>
  )
}
