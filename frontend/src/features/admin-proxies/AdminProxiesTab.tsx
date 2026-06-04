import { useState } from 'react'
import { Plus, Trash2, RefreshCw, Activity, Server, ShieldAlert, ShieldCheck, Pencil } from 'lucide-react'
import { useProxies, createProxy, updateProxy, deleteProxy } from './api'
import type { Proxy, ProxyMetrics } from './types'

function StatusBadge({ status }: { status: string }) {
  if (status === 'healthy')
    return (
      <span className="inline-flex items-center gap-1 rounded-full bg-emerald-500/10 px-2 py-0.5 text-xs font-medium text-emerald-400">
        <ShieldCheck size={12} /> Healthy
      </span>
    )
  if (status === 'unhealthy')
    return (
      <span className="inline-flex items-center gap-1 rounded-full bg-red-500/10 px-2 py-0.5 text-xs font-medium text-red-400">
        <ShieldAlert size={12} /> Unhealthy
      </span>
    )
  return (
    <span className="inline-flex items-center gap-1 rounded-full bg-slate-500/10 px-2 py-0.5 text-xs font-medium text-slate-400">
      <Activity size={12} /> Unknown
    </span>
  )
}

function LoadBar({ active, total, weight }: { active: number; total: number; weight: number }) {
  const pct = total > 0 ? (active / (weight * 10)) * 100 : 0
  const clamped = Math.min(pct, 100)
  return (
    <div className="w-full">
      <div className="h-1.5 w-full overflow-hidden rounded-full bg-slate-700">
        <div
          className="h-full rounded-full bg-indigo-500 transition-all duration-500"
          style={{ width: `${clamped}%` }}
        />
      </div>
      <div className="mt-0.5 flex justify-between text-[10px] text-slate-400">
        <span>active {active}</span>
        <span>total {total}</span>
      </div>
    </div>
  )
}

function ProxyRow({
  proxy,
  metric,
  onRefresh,
}: {
  proxy: Proxy
  metric?: ProxyMetrics
  onRefresh: () => void
}) {
  const [toggling, setToggling] = useState(false)
  const [deleting, setDeleting] = useState(false)
  const [editing, setEditing]   = useState(false)
  const [saving, setSaving]     = useState(false)
  const [editForm, setEditForm] = useState({
    protocol: proxy.protocol,
    host:     proxy.host,
    port:     String(proxy.port),
    username: proxy.username ?? '',
    password: '',
    weight:   String(proxy.weight),
  })

  const openEdit = () => {
    setEditForm({
      protocol: proxy.protocol,
      host:     proxy.host,
      port:     String(proxy.port),
      username: proxy.username ?? '',
      password: '',
      weight:   String(proxy.weight),
    })
    setEditing(true)
  }

  const handleSave = async (e: React.FormEvent) => {
    e.preventDefault()
    setSaving(true)
    try {
      // eslint-disable-next-line @typescript-eslint/no-explicit-any
      const body: Record<string, any> = {
        protocol: editForm.protocol,
        host:     editForm.host,
        port:     parseInt(editForm.port, 10),
        username: editForm.username !== '' ? editForm.username : null,
        weight:   parseInt(editForm.weight, 10) || 1,
      }
      if (editForm.password !== '') {
        body.password = editForm.password
      }
      await updateProxy(proxy.id, body)
      setEditing(false)
      onRefresh()
    } finally {
      setSaving(false)
    }
  }

  const handleToggle = async () => {
    setToggling(true)
    try {
      await updateProxy(proxy.id, { is_active: !proxy.is_active })
      onRefresh()
    } finally {
      setToggling(false)
    }
  }

  const handleDelete = async () => {
    if (!confirm(`Удалить прокси ${proxy.host}:${proxy.port}?`)) return
    setDeleting(true)
    try {
      await deleteProxy(proxy.id)
      onRefresh()
    } finally {
      setDeleting(false)
    }
  }

  // ── Edit mode ──────────────────────────────────────────────────────────────
  if (editing) {
    return (
      <tr className="border-b border-slate-800 bg-slate-800/60">
        <td colSpan={8} className="px-3 py-3">
          <form onSubmit={handleSave} className="flex flex-wrap items-end gap-3">
            <div>
              <label className="mb-1 block text-xs text-slate-400">Protocol</label>
              <select
                value={editForm.protocol}
                onChange={(e) => setEditForm({ ...editForm, protocol: e.target.value })}
                disabled={saving}
                className="rounded border border-slate-700 bg-slate-800 px-2 py-1.5 text-sm text-slate-200 disabled:opacity-50"
              >
                <option value="http">http</option>
                <option value="https">https</option>
              </select>
            </div>
            <div>
              <label className="mb-1 block text-xs text-slate-400">Host</label>
              <input
                required
                type="text"
                value={editForm.host}
                onChange={(e) => setEditForm({ ...editForm, host: e.target.value })}
                disabled={saving}
                className="rounded border border-slate-700 bg-slate-800 px-2 py-1.5 text-sm text-slate-200 placeholder-slate-600 disabled:opacity-50"
              />
            </div>
            <div>
              <label className="mb-1 block text-xs text-slate-400">Port</label>
              <input
                required
                type="number"
                min={1}
                max={65535}
                value={editForm.port}
                onChange={(e) => setEditForm({ ...editForm, port: e.target.value })}
                disabled={saving}
                className="w-20 rounded border border-slate-700 bg-slate-800 px-2 py-1.5 text-sm text-slate-200 disabled:opacity-50"
              />
            </div>
            <div>
              <label className="mb-1 block text-xs text-slate-400">User</label>
              <input
                type="text"
                value={editForm.username}
                onChange={(e) => setEditForm({ ...editForm, username: e.target.value })}
                disabled={saving}
                placeholder="opt"
                className="rounded border border-slate-700 bg-slate-800 px-2 py-1.5 text-sm text-slate-200 placeholder-slate-600 disabled:opacity-50"
              />
            </div>
            <div>
              <label className="mb-1 block text-xs text-slate-400">Pass</label>
              <input
                type="password"
                value={editForm.password}
                onChange={(e) => setEditForm({ ...editForm, password: e.target.value })}
                disabled={saving}
                placeholder="без изменений"
                className="rounded border border-slate-700 bg-slate-800 px-2 py-1.5 text-sm text-slate-200 placeholder-slate-600 disabled:opacity-50"
              />
            </div>
            <div>
              <label className="mb-1 block text-xs text-slate-400">Weight</label>
              <input
                type="number"
                min={1}
                value={editForm.weight}
                onChange={(e) => setEditForm({ ...editForm, weight: e.target.value })}
                disabled={saving}
                className="w-16 rounded border border-slate-700 bg-slate-800 px-2 py-1.5 text-sm text-slate-200 disabled:opacity-50"
              />
            </div>
            <div className="flex gap-2">
              <button
                type="submit"
                disabled={saving}
                className="rounded bg-indigo-600 px-3 py-1.5 text-sm font-medium text-white hover:bg-indigo-500 disabled:opacity-50"
              >
                {saving ? '…' : 'Сохранить'}
              </button>
              <button
                type="button"
                disabled={saving}
                onClick={() => setEditing(false)}
                className="rounded border border-slate-700 bg-slate-800 px-3 py-1.5 text-sm font-medium text-slate-300 hover:bg-slate-700 disabled:opacity-50"
              >
                Отмена
              </button>
            </div>
          </form>
        </td>
      </tr>
    )
  }

  // ── View mode ──────────────────────────────────────────────────────────────
  return (
    <tr className="border-b border-slate-800 hover:bg-slate-800/40">
      <td className="px-3 py-2 text-sm text-slate-200">
        <div className="flex items-center gap-2">
          <Server size={14} className="text-slate-500" />
          {proxy.host}:{proxy.port}
        </div>
      </td>
      <td className="px-3 py-2 text-sm text-slate-400">{proxy.protocol}</td>
      <td className="px-3 py-2 text-sm text-slate-400">{proxy.weight}</td>
      <td className="px-3 py-2">
        <StatusBadge status={metric?.health_status || proxy.health_status} />
      </td>
      <td className="px-3 py-2 text-sm text-slate-400">
        {proxy.is_active ? (
          <span className="text-emerald-400">Активен</span>
        ) : (
          <span className="text-slate-500">Неактивен</span>
        )}
      </td>
      <td className="px-3 py-2">
        <LoadBar active={metric?.pending || 0} total={metric?.total || 0} weight={proxy.weight} />
      </td>
      <td className="px-3 py-2 text-sm text-slate-400">{metric?.failures ?? proxy.fail_count}</td>
      <td className="px-3 py-2">
        <div className="flex items-center gap-2">
          <button
            aria-label="Изменить"
            onClick={openEdit}
            className="rounded p-1 text-slate-400 hover:bg-slate-700 hover:text-slate-200"
          >
            <Pencil size={14} />
          </button>
          <button
            onClick={handleToggle}
            disabled={toggling}
            className="rounded px-2 py-1 text-xs font-medium text-slate-300 hover:bg-slate-700 disabled:opacity-50"
          >
            {proxy.is_active ? 'Выключить' : 'Включить'}
          </button>
          <button
            onClick={handleDelete}
            disabled={deleting}
            className="rounded p-1 text-slate-400 hover:bg-red-500/10 hover:text-red-400 disabled:opacity-50"
          >
            <Trash2 size={14} />
          </button>
        </div>
      </td>
    </tr>
  )
}

export function AdminProxiesTab() {
  const { proxies, metrics, loading, error, refresh } = useProxies()
  const [form, setForm] = useState({
    protocol: 'http',
    host: '',
    port: '',
    username: '',
    password: '',
    weight: '1',
  })
  const [creating, setCreating] = useState(false)

  const handleCreate = async (e: React.FormEvent) => {
    e.preventDefault()
    const port = parseInt(form.port, 10)
    if (!form.host || !port) return
    setCreating(true)
    try {
      await createProxy({
        protocol: form.protocol,
        host: form.host,
        port,
        username: form.username || undefined,
        password: form.password || undefined,
        weight: parseInt(form.weight, 10) || 1,
      })
      setForm({ protocol: 'http', host: '', port: '', username: '', password: '', weight: '1' })
      refresh()
    } finally {
      setCreating(false)
    }
  }

  const metricById = new Map<number, ProxyMetrics>()
  for (const m of metrics) metricById.set(m.id, m)

  return (
    <div className="space-y-6 p-4">
      {/* Add form */}
      <form
        onSubmit={handleCreate}
        className="flex flex-wrap items-end gap-3 rounded-lg border border-slate-700 bg-slate-900/50 p-4"
      >
        <div>
          <label className="mb-1 block text-xs text-slate-400">Protocol</label>
          <select
            value={form.protocol}
            onChange={(e) => setForm({ ...form, protocol: e.target.value })}
            className="rounded border border-slate-700 bg-slate-800 px-2 py-1.5 text-sm text-slate-200"
          >
            <option value="http">http</option>
            <option value="https">https</option>
          </select>
        </div>
        <div>
          <label className="mb-1 block text-xs text-slate-400">Host</label>
          <input
            type="text"
            value={form.host}
            onChange={(e) => setForm({ ...form, host: e.target.value })}
            placeholder="proxy.example.com"
            className="rounded border border-slate-700 bg-slate-800 px-2 py-1.5 text-sm text-slate-200 placeholder-slate-600"
          />
        </div>
        <div>
          <label className="mb-1 block text-xs text-slate-400">Port</label>
          <input
            type="number"
            value={form.port}
            onChange={(e) => setForm({ ...form, port: e.target.value })}
            placeholder="8080"
            className="w-20 rounded border border-slate-700 bg-slate-800 px-2 py-1.5 text-sm text-slate-200 placeholder-slate-600"
          />
        </div>
        <div>
          <label className="mb-1 block text-xs text-slate-400">User</label>
          <input
            type="text"
            value={form.username}
            onChange={(e) => setForm({ ...form, username: e.target.value })}
            placeholder="opt"
            className="rounded border border-slate-700 bg-slate-800 px-2 py-1.5 text-sm text-slate-200 placeholder-slate-600"
          />
        </div>
        <div>
          <label className="mb-1 block text-xs text-slate-400">Pass</label>
          <input
            type="password"
            value={form.password}
            onChange={(e) => setForm({ ...form, password: e.target.value })}
            placeholder="opt"
            className="rounded border border-slate-700 bg-slate-800 px-2 py-1.5 text-sm text-slate-200 placeholder-slate-600"
          />
        </div>
        <div>
          <label className="mb-1 block text-xs text-slate-400">Weight</label>
          <input
            type="number"
            value={form.weight}
            onChange={(e) => setForm({ ...form, weight: e.target.value })}
            className="w-16 rounded border border-slate-700 bg-slate-800 px-2 py-1.5 text-sm text-slate-200"
          />
        </div>
        <button
          type="submit"
          disabled={creating}
          className="inline-flex items-center gap-1 rounded bg-indigo-600 px-3 py-1.5 text-sm font-medium text-white hover:bg-indigo-500 disabled:opacity-50"
        >
          <Plus size={14} /> Добавить
        </button>
      </form>

      {/* Table */}
      <div className="overflow-x-auto rounded-lg border border-slate-700">
        <table className="w-full text-left text-sm">
          <thead className="bg-slate-800 text-xs uppercase text-slate-400">
            <tr>
              <th className="px-3 py-2">Адрес</th>
              <th className="px-3 py-2">Proto</th>
              <th className="px-3 py-2">Weight</th>
              <th className="px-3 py-2">Health</th>
              <th className="px-3 py-2">Статус</th>
              <th className="px-3 py-2">Нагрузка</th>
              <th className="px-3 py-2">Fails</th>
              <th className="px-3 py-2">
                <button onClick={refresh} className="hover:text-white">
                  <RefreshCw size={12} />
                </button>
              </th>
            </tr>
          </thead>
          <tbody>
            {loading && proxies.length === 0 && (
              <tr>
                <td colSpan={8} className="px-3 py-6 text-center text-slate-500">
                  Загрузка...
                </td>
              </tr>
            )}
            {error && (
              <tr>
                <td colSpan={8} className="px-3 py-6 text-center text-red-400">
                  {error}
                </td>
              </tr>
            )}
            {proxies.map((p) => (
              <ProxyRow key={p.id} proxy={p} metric={metricById.get(p.id)} onRefresh={refresh} />
            ))}
            {proxies.length === 0 && !loading && (
              <tr>
                <td colSpan={8} className="px-3 py-6 text-center text-slate-500">
                  Нет прокси. Добавьте первый выше.
                </td>
              </tr>
            )}
          </tbody>
        </table>
      </div>
    </div>
  )
}
