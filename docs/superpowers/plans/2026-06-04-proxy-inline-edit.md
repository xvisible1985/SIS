# Proxy Inline Edit Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add an inline edit form to each proxy row in the admin panel so admins can change host, port, protocol, username, password, and weight without leaving the page.

**Architecture:** Only `AdminProxiesTab.tsx` changes — add `editing`/`editForm`/`saving` state to `ProxyRow`, render an inline form row when editing, and call the existing `updateProxy()` API on save. No backend or API-layer changes needed.

**Tech Stack:** React 18, TypeScript, Vitest + Testing Library (jsdom), Tailwind CSS, lucide-react

---

## File Map

| File | Action |
|---|---|
| `frontend/src/__tests__/AdminProxiesTab.test.tsx` | **Create** — component tests |
| `frontend/src/features/admin-proxies/AdminProxiesTab.tsx` | **Modify** — add edit state + inline form to `ProxyRow` |

---

### Task 1: Write failing tests for inline edit

**Files:**
- Create: `frontend/src/__tests__/AdminProxiesTab.test.tsx`

- [ ] **Step 1: Create the test file**

```typescript
// frontend/src/__tests__/AdminProxiesTab.test.tsx
import { render, screen, fireEvent, waitFor } from '@testing-library/react'
import { vi } from 'vitest'
import { AdminProxiesTab } from '../features/admin-proxies/AdminProxiesTab'
import type { Proxy } from '../features/admin-proxies/types'

const mockRefresh = vi.fn()
const mockUpdateProxy = vi.fn()

const fakeProxy: Proxy = {
  id: 1,
  protocol: 'http',
  host: '1.2.3.4',
  port: 8080,
  username: 'user1',
  weight: 2,
  is_active: true,
  health_status: 'healthy',
  fail_count: 0,
  total_reqs: 10,
  active_reqs: 0,
}

vi.mock('../features/admin-proxies/api', () => ({
  useProxies: () => ({
    proxies: [fakeProxy],
    metrics: [],
    loading: false,
    error: null,
    refresh: mockRefresh,
  }),
  createProxy: vi.fn(),
  updateProxy: mockUpdateProxy,
  deleteProxy: vi.fn(),
}))

beforeEach(() => {
  vi.clearAllMocks()
})

test('shows edit button in proxy row', () => {
  render(<AdminProxiesTab />)
  expect(screen.getByRole('button', { name: /изменить/i })).toBeInTheDocument()
})

test('clicking edit opens inline form pre-filled with proxy data', () => {
  render(<AdminProxiesTab />)
  fireEvent.click(screen.getByRole('button', { name: /изменить/i }))
  expect(screen.getByDisplayValue('1.2.3.4')).toBeInTheDocument()
  expect(screen.getByDisplayValue('8080')).toBeInTheDocument()
  expect(screen.getByDisplayValue('user1')).toBeInTheDocument()
  expect(screen.getByDisplayValue('2')).toBeInTheDocument()
  expect(screen.getByRole('button', { name: /сохранить/i })).toBeInTheDocument()
  expect(screen.getByRole('button', { name: /отмена/i })).toBeInTheDocument()
})

test('cancel button closes edit form', () => {
  render(<AdminProxiesTab />)
  fireEvent.click(screen.getByRole('button', { name: /изменить/i }))
  fireEvent.click(screen.getByRole('button', { name: /отмена/i }))
  expect(screen.queryByRole('button', { name: /сохранить/i })).not.toBeInTheDocument()
  // edit button reappears
  expect(screen.getByRole('button', { name: /изменить/i })).toBeInTheDocument()
})

test('save calls updateProxy with edited values and refreshes', async () => {
  mockUpdateProxy.mockResolvedValue(undefined)
  render(<AdminProxiesTab />)
  fireEvent.click(screen.getByRole('button', { name: /изменить/i }))

  const hostInput = screen.getByDisplayValue('1.2.3.4')
  fireEvent.change(hostInput, { target: { value: '9.9.9.9' } })

  fireEvent.click(screen.getByRole('button', { name: /сохранить/i }))

  await waitFor(() =>
    expect(mockUpdateProxy).toHaveBeenCalledWith(
      1,
      expect.objectContaining({ host: '9.9.9.9', protocol: 'http', port: 8080, weight: 2 }),
    ),
  )
  await waitFor(() => expect(mockRefresh).toHaveBeenCalled())
})

test('empty password field is NOT included in updateProxy call', async () => {
  mockUpdateProxy.mockResolvedValue(undefined)
  render(<AdminProxiesTab />)
  fireEvent.click(screen.getByRole('button', { name: /изменить/i }))
  // password field is left blank
  fireEvent.click(screen.getByRole('button', { name: /сохранить/i }))

  await waitFor(() => expect(mockUpdateProxy).toHaveBeenCalled())
  const callBody = mockUpdateProxy.mock.calls[0][1] as Record<string, unknown>
  expect(callBody).not.toHaveProperty('password')
})

test('filled password field IS included in updateProxy call', async () => {
  mockUpdateProxy.mockResolvedValue(undefined)
  render(<AdminProxiesTab />)
  fireEvent.click(screen.getByRole('button', { name: /изменить/i }))

  const passInput = screen.getByPlaceholderText(/без изменений/i)
  fireEvent.change(passInput, { target: { value: 'newpass123' } })

  fireEvent.click(screen.getByRole('button', { name: /сохранить/i }))

  await waitFor(() =>
    expect(mockUpdateProxy).toHaveBeenCalledWith(
      1,
      expect.objectContaining({ password: 'newpass123' }),
    ),
  )
})
```

- [ ] **Step 2: Run tests to verify they fail**

```
cd frontend
npx vitest run src/__tests__/AdminProxiesTab.test.tsx
```

Expected: all 6 tests FAIL — either "button with name /изменить/i not found" or import errors.

---

### Task 2: Implement inline edit in ProxyRow

**Files:**
- Modify: `frontend/src/features/admin-proxies/AdminProxiesTab.tsx:1-122`

- [ ] **Step 1: Add `Pencil` to the lucide-react import**

Find line 1:
```typescript
import { Plus, Trash2, RefreshCw, Activity, Server, ShieldAlert, ShieldCheck } from 'lucide-react'
```

Replace with:
```typescript
import { Plus, Trash2, RefreshCw, Activity, Server, ShieldAlert, ShieldCheck, Pencil } from 'lucide-react'
```

- [ ] **Step 2: Replace the entire `ProxyRow` function (lines 45–122) with the version below**

Find and replace the whole `ProxyRow` function:

```typescript
function ProxyRow({
  proxy,
  metric,
  onRefresh,
}: {
  proxy: Proxy
  metric?: ProxyMetrics
  onRefresh: () => void
}) {
  const [toggling, setToggling]   = useState(false)
  const [deleting, setDeleting]   = useState(false)
  const [editing, setEditing]     = useState(false)
  const [saving, setSaving]       = useState(false)
  const [editForm, setEditForm]   = useState({
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
```

- [ ] **Step 3: Run tests — expect all 6 to pass**

```
cd frontend
npx vitest run src/__tests__/AdminProxiesTab.test.tsx
```

Expected output:
```
✓ shows edit button in proxy row
✓ clicking edit opens inline form pre-filled with proxy data
✓ cancel button closes edit form
✓ save calls updateProxy with edited values and refreshes
✓ empty password field is NOT included in updateProxy call
✓ filled password field IS included in updateProxy call

Test Files  1 passed (1)
Tests       6 passed (6)
```

- [ ] **Step 4: Run the full test suite**

```
cd frontend
npx vitest run
```

Expected: all tests pass, no regressions.

- [ ] **Step 5: TypeScript + build check**

```
cd frontend
npm run build
```

Expected: exits 0, no TypeScript errors.

- [ ] **Step 6: Commit**

```bash
git add frontend/src/__tests__/AdminProxiesTab.test.tsx \
        frontend/src/features/admin-proxies/AdminProxiesTab.tsx
git commit -m "feat: inline edit for proxy rows in admin panel

- add editing/editForm/saving state to ProxyRow
- render inline form with protocol/host/port/username/password/weight
- password blank = not sent (keep existing); filled = updated
- username empty string sends null (clears existing)
- add Pencil icon edit button with aria-label='Изменить'
- 6 new tests covering open/cancel/save/password logic"
```

---

### Task 3: Smoke check

**Files:** none — verification only

- [ ] **Step 1: Confirm build output is clean**

```
cd frontend
npm run build 2>&1 | tail -5
```

Expected: last lines show `✓ built in ...` with no errors or warnings about the modified file.

- [ ] **Step 2: Self-review the rendered output**

Open the dev server (`npm run dev`) and navigate to the admin proxy tab. Verify:
1. Each proxy row shows a small pencil icon button
2. Clicking it expands an inline form with pre-filled values
3. Changing a field and clicking Сохранить updates the proxy (network tab shows PATCH)
4. Clicking Отмена dismisses the form without a network call
5. Leaving password blank and saving: PATCH body has no `password` key
6. Entering a new password and saving: PATCH body includes `password`
