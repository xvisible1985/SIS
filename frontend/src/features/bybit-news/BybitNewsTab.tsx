import { useState } from 'react'
import { RefreshCw, ExternalLink, Tag, Sparkles, Trash2 } from 'lucide-react'
import { useBybitNews } from './api'

function formatDate(ts?: number) {
  if (!ts) return '—'
  const d = new Date(ts)
  return d.toLocaleString('ru-RU', { day: '2-digit', month: '2-digit', hour: '2-digit', minute: '2-digit' })
}

function Badge({ children, color }: { children: React.ReactNode; color: 'green' | 'red' | 'blue' | 'gray' }) {
  const colors = {
    green: 'bg-emerald-500/10 text-emerald-400 border-emerald-500/20',
    red:   'bg-rose-500/10 text-rose-400 border-rose-500/20',
    blue:  'bg-blue-500/10 text-blue-400 border-blue-500/20',
    gray:  'bg-slate-500/10 text-slate-400 border-slate-500/20',
  }
  return (
    <span className={`inline-flex items-center gap-1 rounded-full border px-2 py-0.5 text-[10px] font-bold uppercase tracking-wider ${colors[color]}`}>
      {children}
    </span>
  )
}

export function BybitNewsTab() {
  const [filter, setFilter] = useState<'all' | 'listings' | 'delistings'>('all')
  const { items, loading, error, refresh } = useBybitNews(
    filter === 'listings',
    filter === 'delistings',
  )

  return (
    <div className="space-y-4 p-4">
      {/* Filters */}
      <div className="flex items-center gap-2">
        {([
          { id: 'all' as const, label: 'Все', icon: Tag },
          { id: 'listings' as const, label: 'Листинги', icon: Sparkles },
          { id: 'delistings' as const, label: 'Делистинги', icon: Trash2 },
        ]).map(f => (
          <button
            key={f.id}
            onClick={() => setFilter(f.id)}
            className={`inline-flex items-center gap-1.5 rounded-lg border px-3 py-1.5 text-xs font-semibold transition-colors ${
              filter === f.id
                ? 'bg-[#5b8cff]/15 border-[#5b8cff]/30 text-[#b8c8ff]'
                : 'bg-white/[.03] border-white/[.06] text-slate-400 hover:text-slate-200'
            }`}
          >
            <f.icon size={12} />
            {f.label}
          </button>
        ))}
        <button
          onClick={refresh}
          className="ml-auto inline-flex items-center gap-1 rounded-lg border border-white/[.06] bg-white/[.03] px-2 py-1.5 text-xs text-slate-400 hover:text-slate-200 transition-colors"
        >
          <RefreshCw size={12} />
          Обновить
        </button>
      </div>

      {/* Table */}
      <div className="overflow-x-auto rounded-lg border border-slate-700">
        <table className="w-full text-left text-sm">
          <thead className="bg-slate-800 text-xs uppercase text-slate-400">
            <tr>
              <th className="px-3 py-2">Тип</th>
              <th className="px-3 py-2">Заголовок</th>
              <th className="px-3 py-2">Пары</th>
              <th className="px-3 py-2">Рынки</th>
              <th className="px-3 py-2">Плечо</th>
              <th className="px-3 py-2">Дата</th>
              <th className="px-3 py-2">Ссылка</th>
            </tr>
          </thead>
          <tbody>
            {loading && items.length === 0 && (
              <tr>
                <td colSpan={7} className="px-3 py-6 text-center text-slate-500">Загрузка...</td>
              </tr>
            )}
            {error && (
              <tr>
                <td colSpan={7} className="px-3 py-6 text-center text-red-400">{error}</td>
              </tr>
            )}
            {items.map(item => (
              <tr key={item.announcement_id} className="border-b border-slate-800 hover:bg-slate-800/40">
                <td className="px-3 py-2 whitespace-nowrap">
                  {item.is_new_listing && <Badge color="green">Листинг</Badge>}
                  {item.is_delisting && <Badge color="red">Делистинг</Badge>}
                  {!item.is_new_listing && !item.is_delisting && <Badge color="gray">{item.type_title || 'News'}</Badge>}
                </td>
                <td className="px-3 py-2 max-w-md">
                  <div className="text-slate-200 font-medium text-[13px] leading-snug">{item.title}</div>
                  {item.description && (
                    <div className="mt-0.5 text-[11px] text-slate-500 line-clamp-2">{item.description}</div>
                  )}
                </td>
                <td className="px-3 py-2">
                  <div className="flex flex-wrap gap-1">
                    {item.symbols?.map(s => (
                      <span key={s} className="rounded px-1.5 py-0.5 text-[10px] bg-emerald-400/10 text-emerald-300 border border-emerald-400/20 font-mono">
                        {s}
                      </span>
                    ))}
                  </div>
                </td>
                <td className="px-3 py-2">
                  <div className="flex flex-wrap gap-1">
                    {item.markets?.map(m => (
                      <span key={m} className="rounded px-1.5 py-0.5 text-[10px] bg-[#5b8cff]/10 text-[#b8c8ff] border border-[#5b8cff]/20">
                        {m}
                      </span>
                    ))}
                  </div>
                </td>
                <td className="px-3 py-2 whitespace-nowrap">
                  {item.max_leverage && (
                    <span className="rounded px-1.5 py-0.5 text-[10px] bg-amber-400/10 text-amber-300 border border-amber-400/20 font-bold">
                      {item.max_leverage}
                    </span>
                  )}
                </td>
                <td className="px-3 py-2 whitespace-nowrap text-[12px] text-slate-400">
                  {formatDate(item.date_ts)}
                </td>
                <td className="px-3 py-2 whitespace-nowrap">
                  {item.url && (
                    <a
                      href={item.url}
                      target="_blank"
                      rel="noopener noreferrer"
                      className="inline-flex items-center gap-1 text-[#5b8cff] hover:text-[#7ba4ff] text-xs font-medium"
                    >
                      <ExternalLink size={12} />
                      Открыть
                    </a>
                  )}
                </td>
              </tr>
            ))}
            {items.length === 0 && !loading && (
              <tr>
                <td colSpan={7} className="px-3 py-6 text-center text-slate-500">
                  Нет анонсов. Скraper опрашивает Bybit каждые 5 минут.
                </td>
              </tr>
            )}
          </tbody>
        </table>
      </div>

      <div className="text-[11px] text-slate-500">
        Источник: <span className="text-slate-400">api.bybit.com/v5/announcements/index</span> · Обновление каждые 5 мин · Локальное хранение в БД
      </div>
    </div>
  )
}
