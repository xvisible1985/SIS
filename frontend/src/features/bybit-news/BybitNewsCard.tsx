import { useState } from 'react'
import { RefreshCw, Sparkles, Trash2, ExternalLink, Newspaper } from 'lucide-react'
import { useLatestBybitNews } from './useLatestNews'

function formatDate(ts?: number) {
  if (!ts) return '—'
  const d = new Date(ts)
  return d.toLocaleString('ru-RU', { day: '2-digit', month: '2-digit', hour: '2-digit', minute: '2-digit' })
}

export function BybitNewsCard() {
  const { items, loading, refreshing, forceRefresh } = useLatestBybitNews()
  const [expanded, setExpanded] = useState(false)

  const latest = items[0]

  return (
    <div className="flex flex-col overflow-hidden rounded-[12px] border border-white/[.06] bg-white/[.08]">
      {/* Header */}
      <div className="flex items-start gap-2.5 p-3 pb-2">
        <div className="min-w-0 flex-1">
          <div className="flex items-center gap-2">
            <Newspaper size={14} className="text-violet-300/70 shrink-0" />
            <div className="truncate text-[14px] font-bold leading-tight tracking-tight text-slate-50">
              Bybit News
            </div>
          </div>
          <div className="mt-1 text-[11px] leading-snug text-slate-400 line-clamp-2">
            Листинги и делистинги с биржи Bybit. Обновление каждые 10 мин.
          </div>
        </div>
        <div className="shrink-0 flex items-center gap-1">
          <button
            type="button"
            onClick={() => forceRefresh()}
            disabled={refreshing}
            className="w-7 h-7 inline-flex items-center justify-center rounded-[7px] bg-white/[.04] border border-white/[.08] text-slate-400 hover:text-slate-200 transition-colors disabled:opacity-50"
            title="Обновить"
          >
            <RefreshCw size={13} className={refreshing ? 'animate-spin' : ''} />
          </button>
          <button
            type="button"
            onClick={() => setExpanded(e => !e)}
            className="w-7 h-7 inline-flex items-center justify-center rounded-[7px] bg-white/[.04] border border-white/[.08] text-slate-400 hover:text-slate-200 transition-colors"
          >
            <svg width={13} height={13} viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth={2.2} strokeLinecap="round" strokeLinejoin="round"
              className={`transition-transform duration-200 ${expanded ? 'rotate-180' : ''}`}>
              <path d="M6 9l6 6 6-6" />
            </svg>
          </button>
        </div>
      </div>

      {/* Latest item preview (always visible) */}
      <div className="px-3 pb-2">
        {loading && !latest && (
          <div className="text-[11px] text-slate-500 py-1">Загрузка...</div>
        )}
        {latest && (
          <div className="flex items-start gap-2 rounded-[8px] bg-white/[.04] border border-white/[.05] px-2.5 py-2">
            <div className="mt-0.5 shrink-0">
              {latest.is_new_listing && <Sparkles size={12} className="text-emerald-400" />}
              {latest.is_delisting && <Trash2 size={12} className="text-rose-400" />}
            </div>
            <div className="min-w-0 flex-1">
              <div className="text-[12px] font-medium text-slate-200 leading-snug truncate">
                {latest.title}
              </div>
              <div className="mt-0.5 flex items-center gap-2">
                <span className="text-[10px] text-slate-500">{formatDate(latest.date_ts)}</span>
                {latest.url && (
                  <a
                    href={latest.url}
                    target="_blank"
                    rel="noopener noreferrer"
                    className="inline-flex items-center gap-0.5 text-[10px] text-[#5b8cff] hover:text-[#7ba4ff]"
                    onClick={e => e.stopPropagation()}
                  >
                    <ExternalLink size={9} />
                    Открыть
                  </a>
                )}
              </div>
              <div className="mt-1 flex flex-wrap items-center gap-1">
                {latest.symbols?.map(s => (
                  <span key={s} className="rounded-[3px] bg-emerald-400/10 border border-emerald-400/20 px-1 py-px text-[9px] font-bold text-emerald-300">
                    {s}
                  </span>
                ))}
                {latest.markets?.map(m => (
                  <span key={m} className="rounded-[3px] bg-[#5b8cff]/10 border border-[#5b8cff]/20 px-1 py-px text-[9px] font-bold text-[#b8c8ff]">
                    {m}
                  </span>
                ))}
                {latest.max_leverage && (
                  <span className="rounded-[3px] bg-amber-400/10 border border-amber-400/20 px-1 py-px text-[9px] font-bold text-amber-300">
                    {latest.max_leverage}
                  </span>
                )}
                {latest.is_pre_market && (
                  <span className="rounded-[3px] bg-violet-400/10 border border-violet-400/20 px-1 py-px text-[9px] font-bold text-violet-300">
                    Pre-Market
                  </span>
                )}
                {latest.launch_at && (
                  <span className="rounded-[3px] bg-slate-400/10 border border-slate-400/20 px-1 py-px text-[9px] font-bold text-slate-300">
                    {formatDate(latest.launch_at)}
                  </span>
                )}
              </div>
            </div>
          </div>
        )}
        {!latest && !loading && (
          <div className="text-[11px] text-slate-500 py-1">Нет данных</div>
        )}
      </div>

      {/* Expanded list */}
      {expanded && (
        <div className="flex flex-col gap-1.5 px-3 pb-3">
          {items.slice(1).map(item => (
            <div
              key={item.announcement_id}
              className="flex items-start gap-2 rounded-[8px] bg-white/[.03] border border-white/[.04] px-2.5 py-2"
            >
              <div className="mt-0.5 shrink-0">
                {item.is_new_listing && <Sparkles size={12} className="text-emerald-400" />}
                {item.is_delisting && <Trash2 size={12} className="text-rose-400" />}
              </div>
              <div className="min-w-0 flex-1">
                <div className="text-[12px] font-medium text-slate-200 leading-snug truncate">
                  {item.title}
                </div>
                <div className="mt-0.5 flex items-center gap-2">
                  <span className="text-[10px] text-slate-500">{formatDate(item.date_ts)}</span>
                  {item.url && (
                    <a
                      href={item.url}
                      target="_blank"
                      rel="noopener noreferrer"
                      className="inline-flex items-center gap-0.5 text-[10px] text-[#5b8cff] hover:text-[#7ba4ff]"
                      onClick={e => e.stopPropagation()}
                    >
                      <ExternalLink size={9} />
                      Открыть
                    </a>
                  )}
                </div>
                <div className="mt-1 flex flex-wrap items-center gap-1">
                  {item.symbols?.map(s => (
                    <span key={s} className="rounded-[3px] bg-emerald-400/10 border border-emerald-400/20 px-1 py-px text-[9px] font-bold text-emerald-300">
                      {s}
                    </span>
                  ))}
                  {item.markets?.map(m => (
                    <span key={m} className="rounded-[3px] bg-[#5b8cff]/10 border border-[#5b8cff]/20 px-1 py-px text-[9px] font-bold text-[#b8c8ff]">
                      {m}
                    </span>
                  ))}
                  {item.max_leverage && (
                    <span className="rounded-[3px] bg-amber-400/10 border border-amber-400/20 px-1 py-px text-[9px] font-bold text-amber-300">
                      {item.max_leverage}
                    </span>
                  )}
                  {item.is_pre_market && (
                    <span className="rounded-[3px] bg-violet-400/10 border border-violet-400/20 px-1 py-px text-[9px] font-bold text-violet-300">
                      Pre-Market
                    </span>
                  )}
                  {item.launch_at && (
                    <span className="rounded-[3px] bg-slate-400/10 border border-slate-400/20 px-1 py-px text-[9px] font-bold text-slate-300">
                      {formatDate(item.launch_at)}
                    </span>
                  )}
                </div>
              </div>
            </div>
          ))}
          {items.length <= 1 && (
            <div className="text-[11px] text-slate-500 text-center py-1">Больше нет анонсов</div>
          )}
        </div>
      )}
    </div>
  )
}
