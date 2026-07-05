// frontend/src/features/admin-services/ServicesTab.tsx

import { useEffect, useState } from 'react'
import { apiClient } from '../../api/client'

interface ServiceInfo {
  name:        string
  binary:      string
  description: string
  startCond:   string
  online:      boolean
  lastSeenMs:  number | null
}

function useServices() {
  const [services, setServices] = useState<ServiceInfo[]>([])
  const [error, setError]       = useState<string | null>(null)

  useEffect(() => {
    let cancelled = false

    async function load() {
      try {
        const res = await apiClient.get<ServiceInfo[]>('/admin/services')
        if (!cancelled) { setServices(res.data); setError(null) }
      } catch {
        if (!cancelled) setError('Не удалось загрузить статус сервисов')
      }
    }

    load()
    const iv = setInterval(load, 15_000)
    return () => { cancelled = true; clearInterval(iv) }
  }, [])

  return { services, error }
}

const SOURCE_COLORS: Record<string, string> = {
  'strategy-runner': 'bg-[#5b8cff]/10 text-[#a0b8ff] border-[#5b8cff]/20',
  'hedge-engine':    'bg-violet-500/10 text-violet-300 border-violet-400/20',
  'api':             'bg-slate-500/10 text-slate-400 border-slate-500/20',
  'tg-bot':          'bg-sky-500/10 text-sky-300 border-sky-400/20',
  'webhook':         'bg-amber-500/10 text-amber-300 border-amber-400/20',
  'ingester':        'bg-emerald-500/10 text-emerald-300 border-emerald-400/20',
  'signal-engine':   'bg-fuchsia-500/10 text-fuchsia-300 border-fuchsia-400/20',
  'migrate':         'bg-slate-500/10 text-slate-500 border-slate-600/20',
}

export function ServicesTab() {
  const { services, error } = useServices()

  return (
    <div className="p-5 space-y-4">
      <div className="flex items-center justify-between">
        <h2 className="text-sm font-semibold text-slate-200">Go-сервисы</h2>
        <span className="text-[10px] text-slate-500">обновляется каждые 15 с · heartbeat TTL 30 с</span>
      </div>

      {error && (
        <div className="rounded-lg border border-rose-500/20 bg-rose-500/10 px-4 py-2 text-xs text-rose-400">
          {error}
        </div>
      )}

      <div className="overflow-hidden rounded-xl border border-white/[.06]">
        <table className="w-full text-[12px]">
          <thead>
            <tr className="border-b border-white/[.06] bg-white/[.02]">
              <th className="px-4 py-2.5 text-left text-[10px] font-semibold uppercase tracking-wider text-slate-500">Сервис</th>
              <th className="px-4 py-2.5 text-left text-[10px] font-semibold uppercase tracking-wider text-slate-500">Описание</th>
              <th className="px-4 py-2.5 text-left text-[10px] font-semibold uppercase tracking-wider text-slate-500">Условие запуска</th>
              <th className="px-4 py-2.5 text-center text-[10px] font-semibold uppercase tracking-wider text-slate-500">Статус</th>
            </tr>
          </thead>
          <tbody>
            {services.map((svc, i) => {
              const colorCls = SOURCE_COLORS[svc.name] ?? 'bg-slate-500/10 text-slate-400 border-slate-500/20'
              const isMigrate = svc.name === 'migrate'
              return (
                <tr
                  key={svc.name}
                  className={`border-b border-white/[.04] ${i % 2 === 0 ? '' : 'bg-white/[.01]'}`}
                >
                  {/* Name */}
                  <td className="px-4 py-3">
                    <span className={`inline-block rounded border px-2 py-0.5 font-mono text-[11px] font-medium ${colorCls}`}>
                      {svc.binary}
                    </span>
                  </td>

                  {/* Description */}
                  <td className="px-4 py-3 text-slate-300 leading-relaxed max-w-xs">
                    {svc.description}
                  </td>

                  {/* Start condition */}
                  <td className="px-4 py-3 text-slate-500 italic">
                    {svc.startCond}
                  </td>

                  {/* Status */}
                  <td className="px-4 py-3 text-center">
                    {isMigrate ? (
                      <span className="text-[10px] text-slate-600">—</span>
                    ) : svc.online ? (
                      <span className="inline-flex items-center gap-1.5 text-emerald-400 text-[11px] font-medium">
                        <span className="h-1.5 w-1.5 rounded-full bg-emerald-400 animate-pulse" />
                        online
                      </span>
                    ) : (
                      <span className="inline-flex items-center gap-1.5 text-slate-600 text-[11px]">
                        <span className="h-1.5 w-1.5 rounded-full bg-slate-700" />
                        offline
                      </span>
                    )}
                  </td>
                </tr>
              )
            })}
          </tbody>
        </table>

        {services.length === 0 && !error && (
          <div className="flex h-20 items-center justify-center text-[11px] text-slate-600">
            Загрузка…
          </div>
        )}
      </div>

      {/* Legend */}
      <div className="text-[10px] text-slate-600 space-y-0.5">
        <p><span className="text-slate-500">online</span> — heartbeat в Redis получен в последние 30 секунд</p>
        <p><span className="text-slate-500">offline</span> — сервис не отправлял heartbeat дольше 30 с или не запущен</p>
        <p><span className="text-slate-500">migrate</span> — однократный запуск при деплое, heartbeat не отправляет</p>
      </div>
    </div>
  )
}
