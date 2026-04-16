import { useEffect, useState } from 'react'
import {
  listWebhooks,
  createWebhook,
  deleteWebhook,
} from '../api/webhooks'
import { listSignals } from '../api/signals'
import type { Webhook, Signal } from '../types'

const PLATFORMS = ['custom', 'tradingview', '3commas', 'alertatron']

export function WebhooksPage() {
  const [webhooks, setWebhooks] = useState<Webhook[]>([])
  const [signals, setSignals] = useState<Signal[]>([])
  const [loading, setLoading] = useState(true)
  const [showForm, setShowForm] = useState(false)
  const [formSignalId, setFormSignalId] = useState('')
  const [formUrl, setFormUrl] = useState('')
  const [formPlatform, setFormPlatform] = useState('custom')
  const [formError, setFormError] = useState('')
  const [creating, setCreating] = useState(false)
  const [error, setError] = useState('')

  useEffect(() => {
    Promise.all([listWebhooks(), listSignals()])
      .then(([whs, sigs]) => {
        setWebhooks(whs)
        setSignals(sigs)
        if (sigs.length > 0) setFormSignalId(sigs[0].id)
      })
      .catch((e: unknown) => setError(e instanceof Error ? e.message : 'Failed to load'))
      .finally(() => setLoading(false))
  }, [])

  async function handleCreate() {
    if (!formSignalId || !formUrl) return
    setFormError('')
    setCreating(true)
    try {
      const wh = await createWebhook({
        signal_id: formSignalId,
        url: formUrl,
        platform: formPlatform,
      })
      setWebhooks((prev) => [wh, ...prev])
      setShowForm(false)
      setFormUrl('')
    } catch (err: unknown) {
      setFormError(err instanceof Error ? err.message : 'Failed to create')
    } finally {
      setCreating(false)
    }
  }

  async function handleDelete(id: string) {
    try {
      await deleteWebhook(id)
      setWebhooks((prev) => prev.filter((w) => w.id !== id))
    } catch (e: unknown) {
      setError(e instanceof Error ? e.message : 'Failed to delete')
    }
  }

  function signalName(id: string) {
    return signals.find((s) => s.id === id)?.name ?? id
  }

  if (loading) return <p className="text-gray-500">Loading…</p>

  return (
    <div className="max-w-3xl space-y-5">
      <div className="flex items-center justify-between">
        <h1 className="text-xl font-semibold">Webhooks</h1>
        <button
          onClick={() => setShowForm((v) => !v)}
          className="bg-blue-600 text-white rounded px-4 py-2 text-sm font-medium"
        >
          {showForm ? 'Cancel' : 'Add webhook'}
        </button>
      </div>

      {showForm && (
        <div className="bg-white rounded-xl shadow p-5 space-y-3">
          <div>
            <label className="block text-xs text-gray-600 mb-1">Signal</label>
            <select
              value={formSignalId}
              onChange={(e) => setFormSignalId(e.target.value)}
              className="w-full border rounded px-2 py-1.5 text-sm"
            >
              {signals.map((s) => (
                <option key={s.id} value={s.id}>
                  {s.name}
                </option>
              ))}
            </select>
          </div>
          <div>
            <label className="block text-xs text-gray-600 mb-1">Platform</label>
            <select
              value={formPlatform}
              onChange={(e) => setFormPlatform(e.target.value)}
              className="w-full border rounded px-2 py-1.5 text-sm"
            >
              {PLATFORMS.map((p) => (
                <option key={p}>{p}</option>
              ))}
            </select>
          </div>
          <div>
            <label className="block text-xs text-gray-600 mb-1">URL</label>
            <input
              type="url"
              placeholder="https://…"
              value={formUrl}
              onChange={(e) => setFormUrl(e.target.value)}
              className="w-full border rounded px-2 py-1.5 text-sm"
            />
          </div>
          {formError && <p className="text-red-600 text-sm">{formError}</p>}
          <button
            onClick={handleCreate}
            disabled={creating}
            className="bg-blue-600 text-white rounded px-4 py-2 text-sm font-medium disabled:opacity-50"
          >
            {creating ? 'Creating…' : 'Create'}
          </button>
        </div>
      )}

      {error && <p className="text-red-600 text-sm">{error}</p>}

      {webhooks.length === 0 ? (
        <p className="text-gray-500 py-10 text-center">No webhooks yet.</p>
      ) : (
        <div className="bg-white rounded-xl shadow overflow-hidden">
          <table className="w-full text-sm">
            <thead className="bg-gray-50 text-xs text-gray-600 uppercase">
              <tr>
                <th className="px-4 py-2 text-left">Signal</th>
                <th className="px-4 py-2 text-left">URL</th>
                <th className="px-4 py-2 text-left">Platform</th>
                <th className="px-4 py-2 text-left">Status</th>
                <th className="px-4 py-2 text-left">Actions</th>
              </tr>
            </thead>
            <tbody className="divide-y divide-gray-100">
              {webhooks.map((wh) => (
                <tr key={wh.id}>
                  <td className="px-4 py-2 font-medium">{signalName(wh.signal_id)}</td>
                  <td className="px-4 py-2 text-gray-600 truncate max-w-xs">
                    {wh.url}
                  </td>
                  <td className="px-4 py-2 text-gray-600">{wh.platform}</td>
                  <td className="px-4 py-2">
                    <span
                      className={`px-2 py-0.5 rounded-full text-xs font-medium ${
                        wh.is_active
                          ? 'bg-green-100 text-green-700'
                          : 'bg-gray-100 text-gray-600'
                      }`}
                    >
                      {wh.is_active ? 'Active' : 'Inactive'}
                    </span>
                  </td>
                  <td className="px-4 py-2">
                    <button
                      onClick={() => handleDelete(wh.id)}
                      className="text-red-600 hover:underline text-xs"
                    >
                      Delete
                    </button>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </div>
  )
}
