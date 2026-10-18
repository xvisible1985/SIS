# API keys page: add-key form as a modal — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Move the always-visible "add API key" form on `/accounts` out of a sticky left column and into a modal opened by a button, so the key list uses the full page width.

**Architecture:** Single-file change to `frontend/src/pages/AccountsPage.tsx`. No new files, no backend/API changes. The existing `Modal`, `Toolbar`, `EmptyState`, and `AddKeyPanel` components are extended with small, additive props; `AccountsPage` gains one new boolean state (`addKeyOpen`) and a reworked (non-grid) render.

**Tech Stack:** React + TypeScript (Vite), inline `style={{...}}` objects (no CSS modules/Tailwind in this file), Vitest for the test suite, `tsc --noEmit` for type-checking.

**Spec:** `docs/superpowers/specs/2026-10-17-accounts-page-add-key-modal-design.md`

**Note on testing approach:** This file has no existing dedicated test (`frontend/src/__tests__/AccountsPage.test.tsx` does not exist), and none of the sibling page components in this codebase have component-render tests for presentational pages like this one. Per the project's own CLAUDE.md, regression tests aren't mandatory for a change this small and scoped. Each task below is verified with `npx tsc --noEmit` (catches prop/type mistakes immediately) and the plan ends with a full `npx vitest run` + `npx vite build` pass to confirm no regression elsewhere — the same verification loop already used for the Signals page mobile changes earlier in this project.

---

### Task 1: Widen the `Modal` component with an optional `width` prop

**Files:**
- Modify: `frontend/src/pages/AccountsPage.tsx` (the `Modal` function, currently ~line 1281)

- [ ] **Step 1: Find and replace the `Modal` function**

Find this exact block:

```tsx
function Modal({ open, onClose, children }: { open: boolean; onClose: () => void; children: React.ReactNode }) {
  if (!open) return null
  return (
    <div style={{
      position: 'fixed', inset: 0, zIndex: 100, display: 'flex', alignItems: 'center', justifyContent: 'center',
      background: 'rgba(8,10,16,.65)', backdropFilter: 'blur(6px)', padding: 20,
    }} onClick={onClose}>
      <div onClick={e => e.stopPropagation()} style={{
        width: '100%', maxWidth: 460,
        background: T.panel, border: `1px solid ${T.borderHi}`, borderRadius: 16,
        boxShadow: '0 40px 80px -20px rgba(0,0,0,.7)',
        animation: 'modalIn .18s ease-out', overflow: 'hidden',
      }}>
        {children}
      </div>
    </div>
  )
}
```

Replace it with:

```tsx
function Modal({ open, onClose, children, width = 460 }: { open: boolean; onClose: () => void; children: React.ReactNode; width?: number }) {
  if (!open) return null
  return (
    <div style={{
      position: 'fixed', inset: 0, zIndex: 100, display: 'flex', alignItems: 'center', justifyContent: 'center',
      background: 'rgba(8,10,16,.65)', backdropFilter: 'blur(6px)', padding: 20,
    }} onClick={onClose}>
      <div onClick={e => e.stopPropagation()} style={{
        width: '100%', maxWidth: width,
        background: T.panel, border: `1px solid ${T.borderHi}`, borderRadius: 16,
        boxShadow: '0 40px 80px -20px rgba(0,0,0,.7)',
        animation: 'modalIn .18s ease-out', overflow: 'hidden',
      }}>
        {children}
      </div>
    </div>
  )
}
```

This is purely additive (default value matches the old hardcoded constant), so the two existing callers (`ConfirmDelete`/`ConfirmRotate` modal) keep their current 460px width unchanged.

- [ ] **Step 2: Type-check**

Run: `cd frontend && npx tsc --noEmit -p tsconfig.json`
Expected: no errors.

- [ ] **Step 3: Commit**

```bash
git add frontend/src/pages/AccountsPage.tsx
git commit -m "Allow Modal component to take a custom width"
```

---

### Task 2: Exchange chips in `AddKeyPanel` use the exchange's own brand color when selected

**Files:**
- Modify: `frontend/src/pages/AccountsPage.tsx` (inside `AddKeyPanel`, the `SUPPORTED.map(...)` block, currently ~line 898-918)

- [ ] **Step 1: Find and replace the exchange button styling**

Find this exact block (inside `AddKeyPanel`, Step 1 "Биржа"):

```tsx
            {SUPPORTED.map(([id, e]) => {
              const sel = id === exchange
              return (
                <button key={id} onClick={() => handleExchange(id)} style={{
                  display: 'flex', flexDirection: 'column', alignItems: 'center', gap: 6,
                  padding: '10px 4px',
                  background: sel ? 'linear-gradient(180deg, rgba(91,140,255,.18), rgba(91,140,255,.04))' : 'rgba(255,255,255,.02)',
                  border: `1px solid ${sel ? T.borderActv : T.border}`,
                  borderRadius: 10, cursor: 'pointer', color: sel ? T.text : T.body,
                  boxShadow: sel ? 'inset 0 1px 0 rgba(255,255,255,.08), 0 6px 14px -10px rgba(91,140,255,.5)' : 'none',
                  transition: 'all .12s',
                }}>
                  <ExBadge id={id} size={22} />
                  <span style={{ fontSize: 11, fontWeight: 600 }}>{e.name}</span>
                </button>
              )
            })}
```

Replace it with:

```tsx
            {SUPPORTED.map(([id, e]) => {
              const sel = id === exchange
              return (
                <button key={id} onClick={() => handleExchange(id)} style={{
                  display: 'flex', flexDirection: 'column', alignItems: 'center', gap: 6,
                  padding: '10px 4px',
                  background: sel ? `linear-gradient(180deg, ${e.color}2e, ${e.color}0a)` : 'rgba(255,255,255,.02)',
                  border: `1px solid ${sel ? e.color + '4d' : T.border}`,
                  borderRadius: 10, cursor: 'pointer', color: sel ? T.text : T.body,
                  boxShadow: sel ? `inset 0 1px 0 rgba(255,255,255,.08), 0 6px 14px -10px ${e.color}80` : 'none',
                  transition: 'all .12s',
                }}>
                  <ExBadge id={id} size={22} />
                  <span style={{ fontSize: 11, fontWeight: 600 }}>{e.name}</span>
                </button>
              )
            })}
```

`e` here is the `EXCHANGES` record value (`{ name, color, Mark, supported? }`) already destructured by the `.map(([id, e]) => ...)` — `e.color` is each exchange's hex brand color (e.g. Bybit `#f7a600`). The two-character hex suffixes (`2e`, `0a`, `4d`, `80`) reproduce the original opacities (`.18`, `.04`, `.30`, `.50` respectively) as alpha channels on the 6-digit hex color, the same hex-alpha-suffix pattern already used elsewhere in this file (e.g. `` `${T.orange}40` ``).

- [ ] **Step 2: Type-check**

Run: `cd frontend && npx tsc --noEmit -p tsconfig.json`
Expected: no errors.

- [ ] **Step 3: Commit**

```bash
git add frontend/src/pages/AccountsPage.tsx
git commit -m "Highlight selected exchange chip with its own brand color"
```

---

### Task 3: Add a "Добавить ключ" button to `Toolbar`, remove the dead "Биржа: все" button

**Files:**
- Modify: `frontend/src/pages/AccountsPage.tsx` (the `Toolbar` function, currently ~line 1140-1195)

- [ ] **Step 1: Find and replace the `Toolbar` function**

Find this exact block:

```tsx
function Toolbar({ search, setSearch, filter, setFilter, counts }: {
  search: string; setSearch: (v: string) => void
  filter: string; setFilter: (v: string) => void
  counts: Record<string, number>
}) {
  const filters = [
    { id: 'all',    label: 'Все',               c: undefined },
    { id: 'active', label: 'Активные',           c: T.green },
    { id: 'issues', label: 'Требуют внимания',   c: T.orange },
    { id: 'paused', label: 'Остановленные',      c: T.dim },
  ]
  return (
    <div style={{ display: 'flex', alignItems: 'center', gap: 10, marginBottom: 14, flexWrap: 'wrap' }}>
      <div style={{ position: 'relative', flex: '1 1 240px', maxWidth: 340 }}>
        <input
          value={search} onChange={e => setSearch(e.target.value)}
          placeholder="Поиск по названию или бирже…"
          style={{
            width: '100%', background: 'rgba(0,0,0,.25)', color: T.text,
            border: `1px solid ${T.border}`, borderRadius: 8, padding: '8px 12px 8px 34px',
            fontSize: 12, fontFamily: 'inherit', outline: 'none',
          }}
        />
        <span style={{ position: 'absolute', left: 11, top: '50%', transform: 'translateY(-50%)', pointerEvents: 'none' }}>
          <IcSearch s={13} c={T.dim} />
        </span>
      </div>
      <div style={{ display: 'flex', gap: 4, padding: 3, background: 'rgba(0,0,0,.25)', border: `1px solid ${T.border}`, borderRadius: 8 }}>
        {filters.map(f => (
          <button key={f.id} onClick={() => setFilter(f.id)} style={{
            display: 'inline-flex', alignItems: 'center', gap: 6,
            padding: '6px 10px',
            background: filter === f.id ? 'rgba(123,140,255,.18)' : 'transparent',
            color: filter === f.id ? T.text : T.dim,
            border: 0, borderRadius: 6, fontSize: 12, fontWeight: 600, cursor: 'pointer', fontFamily: 'inherit',
          }}>
            {f.label}
            <span style={{
              fontSize: 10, ...mono,
              color: filter === f.id ? (f.c ?? '#b8c8ff') : T.faint, fontWeight: 700,
            }}>{counts[f.id] ?? 0}</span>
          </button>
        ))}
      </div>
      <div style={{ flex: 1 }} />
      <button style={{
        display: 'inline-flex', alignItems: 'center', gap: 6, padding: '8px 11px',
        background: 'rgba(255,255,255,.04)', border: `1px solid ${T.border}`,
        color: T.body, borderRadius: 8, fontSize: 12, fontWeight: 600, cursor: 'pointer', fontFamily: 'inherit',
      }}>
        <IcFilter s={12} c={T.dim} /> Биржа: все
      </button>
    </div>
  )
}
```

Replace it with:

```tsx
function Toolbar({ search, setSearch, filter, setFilter, counts, onAddKey }: {
  search: string; setSearch: (v: string) => void
  filter: string; setFilter: (v: string) => void
  counts: Record<string, number>
  onAddKey: () => void
}) {
  const filters = [
    { id: 'all',    label: 'Все',               c: undefined },
    { id: 'active', label: 'Активные',           c: T.green },
    { id: 'issues', label: 'Требуют внимания',   c: T.orange },
    { id: 'paused', label: 'Остановленные',      c: T.dim },
  ]
  return (
    <div style={{ display: 'flex', alignItems: 'center', gap: 10, marginBottom: 14, flexWrap: 'wrap' }}>
      <div style={{ position: 'relative', flex: '1 1 240px', maxWidth: 340 }}>
        <input
          value={search} onChange={e => setSearch(e.target.value)}
          placeholder="Поиск по названию или бирже…"
          style={{
            width: '100%', background: 'rgba(0,0,0,.25)', color: T.text,
            border: `1px solid ${T.border}`, borderRadius: 8, padding: '8px 12px 8px 34px',
            fontSize: 12, fontFamily: 'inherit', outline: 'none',
          }}
        />
        <span style={{ position: 'absolute', left: 11, top: '50%', transform: 'translateY(-50%)', pointerEvents: 'none' }}>
          <IcSearch s={13} c={T.dim} />
        </span>
      </div>
      <div style={{ display: 'flex', gap: 4, padding: 3, background: 'rgba(0,0,0,.25)', border: `1px solid ${T.border}`, borderRadius: 8 }}>
        {filters.map(f => (
          <button key={f.id} onClick={() => setFilter(f.id)} style={{
            display: 'inline-flex', alignItems: 'center', gap: 6,
            padding: '6px 10px',
            background: filter === f.id ? 'rgba(123,140,255,.18)' : 'transparent',
            color: filter === f.id ? T.text : T.dim,
            border: 0, borderRadius: 6, fontSize: 12, fontWeight: 600, cursor: 'pointer', fontFamily: 'inherit',
          }}>
            {f.label}
            <span style={{
              fontSize: 10, ...mono,
              color: filter === f.id ? (f.c ?? '#b8c8ff') : T.faint, fontWeight: 700,
            }}>{counts[f.id] ?? 0}</span>
          </button>
        ))}
      </div>
      <div style={{ flex: 1 }} />
      <button onClick={onAddKey} style={{
        display: 'inline-flex', alignItems: 'center', gap: 6, padding: '8px 14px 8px 11px',
        background: 'linear-gradient(180deg, #4a7dff 0%, #3a67e6 100%)',
        color: '#fff', border: 0, borderRadius: 8, fontSize: 12, fontWeight: 600, cursor: 'pointer', fontFamily: 'inherit',
        boxShadow: '0 6px 16px -8px rgba(74,125,255,.6)',
      }}>
        <IcPlus s={12} w={2.2} /> Добавить ключ
      </button>
    </div>
  )
}
```

`IcFilter` is no longer used by this function — leave its definition in place since it's a shared icon constant that may still be used elsewhere in the file (if `tsc`/eslint flags it as unused after this change, that's expected and fine — it's a top-level `const`, not a local variable, so it won't fail the build).

- [ ] **Step 2: Type-check**

Run: `cd frontend && npx tsc --noEmit -p tsconfig.json`
Expected: an error that `<Toolbar>`'s caller is missing the required `onAddKey` prop — this is expected, it gets fixed in Task 5. Confirm the error is specifically about the missing prop and nothing else.

- [ ] **Step 3: Commit**

```bash
git add frontend/src/pages/AccountsPage.tsx
git commit -m "Add 'Добавить ключ' button to Toolbar, drop dead exchange filter button"
```

---

### Task 4: Add a "Добавить ключ" button to `EmptyState`

**Files:**
- Modify: `frontend/src/pages/AccountsPage.tsx` (the `EmptyState` function, currently ~line 1097-1138)

- [ ] **Step 1: Find and replace the `EmptyState` function**

Find this exact block:

```tsx
function EmptyState() {
  return (
    <div style={{
      background: 'linear-gradient(180deg, rgba(91,140,255,.04) 0%, rgba(255,255,255,.01) 100%)',
      border: `1px dashed ${T.borderHi}`, borderRadius: 16,
      padding: '48px 32px', display: 'flex', flexDirection: 'column', alignItems: 'center',
      textAlign: 'center', gap: 14,
    }}>
      <div style={{
        width: 64, height: 64, borderRadius: 18,
        background: 'linear-gradient(135deg, rgba(91,140,255,.25), rgba(193,77,255,.15))',
        border: '1px solid rgba(123,140,255,.30)',
        display: 'flex', alignItems: 'center', justifyContent: 'center', color: '#b8c8ff',
        boxShadow: '0 20px 40px -20px rgba(91,140,255,.6)',
      }}>
        <IcKey s={28} w={1.8} />
      </div>
      <div>
        <h2 style={{ margin: 0, ...grotesk, fontSize: 20, fontWeight: 700, color: T.text, letterSpacing: -0.3 }}>
          Подключите первый API-ключ
        </h2>
        <p style={{ margin: '8px auto 0', maxWidth: 440, fontSize: 13, color: T.dim, lineHeight: 1.5 }}>
          После подключения платформа сможет читать ваш баланс и выставлять ордера. Все секреты шифруются на нашей стороне.
        </p>
      </div>
      <div style={{ display: 'flex', alignItems: 'center', gap: 8, flexWrap: 'wrap', justifyContent: 'center' }}>
        {Object.entries(EXCHANGES).slice(0, 6).map(([id, e]) => (
          <div key={id} style={{
            display: 'inline-flex', alignItems: 'center', gap: 6,
            padding: '5px 10px 5px 5px', background: 'rgba(255,255,255,.03)',
            border: `1px solid ${T.border}`, borderRadius: 999, fontSize: 11, color: T.body, fontWeight: 500,
          }}>
            <ExBadge id={id} size={16} />
            {e.name}
          </div>
        ))}
        <span style={{ fontSize: 11, color: T.dim }}>+ ещё 2</span>
      </div>
    </div>
  )
}
```

Replace it with:

```tsx
function EmptyState({ onAddKey }: { onAddKey: () => void }) {
  return (
    <div style={{
      background: 'linear-gradient(180deg, rgba(91,140,255,.04) 0%, rgba(255,255,255,.01) 100%)',
      border: `1px dashed ${T.borderHi}`, borderRadius: 16,
      padding: '48px 32px', display: 'flex', flexDirection: 'column', alignItems: 'center',
      textAlign: 'center', gap: 14,
    }}>
      <div style={{
        width: 64, height: 64, borderRadius: 18,
        background: 'linear-gradient(135deg, rgba(91,140,255,.25), rgba(193,77,255,.15))',
        border: '1px solid rgba(123,140,255,.30)',
        display: 'flex', alignItems: 'center', justifyContent: 'center', color: '#b8c8ff',
        boxShadow: '0 20px 40px -20px rgba(91,140,255,.6)',
      }}>
        <IcKey s={28} w={1.8} />
      </div>
      <div>
        <h2 style={{ margin: 0, ...grotesk, fontSize: 20, fontWeight: 700, color: T.text, letterSpacing: -0.3 }}>
          Подключите первый API-ключ
        </h2>
        <p style={{ margin: '8px auto 0', maxWidth: 440, fontSize: 13, color: T.dim, lineHeight: 1.5 }}>
          После подключения платформа сможет читать ваш баланс и выставлять ордера. Все секреты шифруются на нашей стороне.
        </p>
      </div>
      <button onClick={onAddKey} style={{
        display: 'inline-flex', alignItems: 'center', gap: 7, padding: '10px 18px 10px 14px',
        background: 'linear-gradient(180deg, #4a7dff 0%, #3a67e6 100%)',
        color: '#fff', border: 0, borderRadius: 9, fontSize: 13, fontWeight: 700, cursor: 'pointer', fontFamily: 'inherit',
        boxShadow: '0 10px 24px -10px rgba(74,125,255,.6)',
      }}>
        <IcPlus s={13} w={2.2} /> Добавить ключ
      </button>
      <div style={{ display: 'flex', alignItems: 'center', gap: 8, flexWrap: 'wrap', justifyContent: 'center' }}>
        {Object.entries(EXCHANGES).slice(0, 6).map(([id, e]) => (
          <div key={id} style={{
            display: 'inline-flex', alignItems: 'center', gap: 6,
            padding: '5px 10px 5px 5px', background: 'rgba(255,255,255,.03)',
            border: `1px solid ${T.border}`, borderRadius: 999, fontSize: 11, color: T.body, fontWeight: 500,
          }}>
            <ExBadge id={id} size={16} />
            {e.name}
          </div>
        ))}
        <span style={{ fontSize: 11, color: T.dim }}>+ ещё 2</span>
      </div>
    </div>
  )
}
```

- [ ] **Step 2: Type-check**

Run: `cd frontend && npx tsc --noEmit -p tsconfig.json`
Expected: an error that `<EmptyState>`'s caller is missing the required `onAddKey` prop (same situation as Task 3) — fixed in Task 5.

- [ ] **Step 3: Commit**

```bash
git add frontend/src/pages/AccountsPage.tsx
git commit -m "Add 'Добавить ключ' button to EmptyState"
```

---

### Task 5: Wire `addKeyOpen` state, drop the two-column grid, move `AddKeyPanel` into the modal

**Files:**
- Modify: `frontend/src/pages/AccountsPage.tsx` (state declarations ~line 1481-1492, `handleAddKey` ~line 1535-1558, and the component's `return` ~line 1627-1715)

- [ ] **Step 1: Add the `addKeyOpen` state**

Find:

```tsx
  const [modal, setModal]           = useState<null | { type: 'delete' | 'rotate'; acc: ExchangeAccount }>(null)
  const [toast, setToast]           = useState<ToastData | null>(null)
```

Replace with:

```tsx
  const [modal, setModal]           = useState<null | { type: 'delete' | 'rotate'; acc: ExchangeAccount }>(null)
  const [toast, setToast]           = useState<ToastData | null>(null)
  const [addKeyOpen, setAddKeyOpen] = useState(false)
```

- [ ] **Step 2: Close the modal after a successful add**

Find (inside `handleAddKey`):

```tsx
      setAccounts(prev => [newAcc, ...prev])
      setExpandedId(newAcc.id)
      window.dispatchEvent(new Event('accounts-changed'))
```

Replace with:

```tsx
      setAccounts(prev => [newAcc, ...prev])
      setExpandedId(newAcc.id)
      setAddKeyOpen(false)
      window.dispatchEvent(new Event('accounts-changed'))
```

- [ ] **Step 3: Replace the two-column grid render with a full-width render and the new modal**

Find this exact block (the component's `return`, from the opening `<div>` through the closing `<style>` tag):

```tsx
  return (
    <div style={{ color: T.body, fontFamily: "'Inter', system-ui, sans-serif" }}>
      <PageHead count={accounts.length} />

      {loading ? (
        <div style={{ padding: '60px 0', textAlign: 'center', color: T.dim, fontSize: 13 }}>
          <span style={{ display: 'inline-flex', animation: 'spin 1s linear infinite', marginRight: 8 }}>
            <IcRefresh s={16} c={T.dim} />
          </span>
          Загрузка…
        </div>
      ) : (
        <>
          <div style={{
            display: 'grid', gridTemplateColumns: '420px minmax(0, 1fr)', gap: 24, alignItems: 'flex-start',
          }}>
            {/* left — add */}
            <div style={{ position: 'sticky', top: 24 }}>
              <AddKeyPanel onSubmit={handleAddKey} />
            </div>

            {/* right — stats + list */}
            <div>
              {accounts.length > 0 && (
                <StatsStrip accounts={accounts} balances={balances} verifyData={verifyData} />
              )}
              {accounts.length === 0 ? (
                <EmptyState />
              ) : (
                <>
                  <Toolbar search={search} setSearch={setSearch} filter={filter} setFilter={setFilter} counts={counts} />
                  <div style={{ display: 'flex', flexDirection: 'column', gap: 10 }}>
                    {visible.map(acc => (
                      <KeyCard
                        key={acc.id}
                        acc={acc}
                        balance={balances[acc.id] ?? null}
                        verify={verifyData[acc.id] ?? null}
                        latency={latencies[acc.id] ?? null}
                        expanded={expandedId === acc.id}
                        testing={testing === acc.id}
                        onToggle={() => setExpandedId(expandedId === acc.id ? null : acc.id)}
                        onTest={() => handleTest(acc)}
                        onDelete={() => setModal({ type: 'delete', acc })}
                        onRotate={() => setModal({ type: 'rotate', acc })}
                        onPause={() => handleToggle(acc)}
                        onSaveRisk={(values) => handleSaveRiskSettings(acc, values)}
                      />
                    ))}
                    {visible.length === 0 && (
                      <div style={{
                        padding: '40px 20px', textAlign: 'center', color: T.dim, fontSize: 13,
                        background: T.card, border: `1px dashed ${T.border}`, borderRadius: 12,
                      }}>
                        По фильтру ничего не найдено.
                      </div>
                    )}
                  </div>
                </>
              )}
            </div>
          </div>
        </>
      )}

      <Modal open={!!modal} onClose={() => setModal(null)}>
        {modal?.type === 'delete' && (
          <ConfirmDelete acc={modal.acc} onCancel={() => setModal(null)} onConfirm={confirmDelete} />
        )}
        {modal?.type === 'rotate' && (
          <ConfirmRotate acc={modal.acc} onCancel={() => setModal(null)} onConfirm={confirmRotate} />
        )}
      </Modal>

      <Toast toast={toast} onClose={() => setToast(null)} />

      <style>{`
        @keyframes pulse   { 0%,100%{opacity:1} 50%{opacity:.35} }
        @keyframes spin    { to{transform:rotate(360deg)} }
        @keyframes fadeIn  { from{opacity:0;transform:translateY(4px)} to{opacity:1;transform:none} }
        @keyframes slideUp { from{opacity:0;transform:translate(-50%,12px)} to{opacity:1;transform:translate(-50%,0)} }
        @keyframes modalIn { from{opacity:0;transform:scale(.96) translateY(8px)} to{opacity:1;transform:none} }
        .fadein { animation: fadeIn .18s ease-out; }
        input::placeholder { color: #5b6479; }
        @media (max-width: 1100px) {
          .ak-layout { grid-template-columns: 1fr !important; }
        }
      `}</style>
    </div>
  )
```

Replace it with:

```tsx
  return (
    <div style={{ color: T.body, fontFamily: "'Inter', system-ui, sans-serif" }}>
      <PageHead count={accounts.length} />

      {loading ? (
        <div style={{ padding: '60px 0', textAlign: 'center', color: T.dim, fontSize: 13 }}>
          <span style={{ display: 'inline-flex', animation: 'spin 1s linear infinite', marginRight: 8 }}>
            <IcRefresh s={16} c={T.dim} />
          </span>
          Загрузка…
        </div>
      ) : (
        <>
          {accounts.length > 0 && (
            <StatsStrip accounts={accounts} balances={balances} verifyData={verifyData} />
          )}
          {accounts.length === 0 ? (
            <EmptyState onAddKey={() => setAddKeyOpen(true)} />
          ) : (
            <>
              <Toolbar search={search} setSearch={setSearch} filter={filter} setFilter={setFilter} counts={counts} onAddKey={() => setAddKeyOpen(true)} />
              <div style={{ display: 'flex', flexDirection: 'column', gap: 10 }}>
                {visible.map(acc => (
                  <KeyCard
                    key={acc.id}
                    acc={acc}
                    balance={balances[acc.id] ?? null}
                    verify={verifyData[acc.id] ?? null}
                    latency={latencies[acc.id] ?? null}
                    expanded={expandedId === acc.id}
                    testing={testing === acc.id}
                    onToggle={() => setExpandedId(expandedId === acc.id ? null : acc.id)}
                    onTest={() => handleTest(acc)}
                    onDelete={() => setModal({ type: 'delete', acc })}
                    onRotate={() => setModal({ type: 'rotate', acc })}
                    onPause={() => handleToggle(acc)}
                    onSaveRisk={(values) => handleSaveRiskSettings(acc, values)}
                  />
                ))}
                {visible.length === 0 && (
                  <div style={{
                    padding: '40px 20px', textAlign: 'center', color: T.dim, fontSize: 13,
                    background: T.card, border: `1px dashed ${T.border}`, borderRadius: 12,
                  }}>
                    По фильтру ничего не найдено.
                  </div>
                )}
              </div>
            </>
          )}
        </>
      )}

      <Modal open={addKeyOpen} onClose={() => setAddKeyOpen(false)} width={600}>
        <AddKeyPanel onSubmit={handleAddKey} />
      </Modal>

      <Modal open={!!modal} onClose={() => setModal(null)}>
        {modal?.type === 'delete' && (
          <ConfirmDelete acc={modal.acc} onCancel={() => setModal(null)} onConfirm={confirmDelete} />
        )}
        {modal?.type === 'rotate' && (
          <ConfirmRotate acc={modal.acc} onCancel={() => setModal(null)} onConfirm={confirmRotate} />
        )}
      </Modal>

      <Toast toast={toast} onClose={() => setToast(null)} />

      <style>{`
        @keyframes pulse   { 0%,100%{opacity:1} 50%{opacity:.35} }
        @keyframes spin    { to{transform:rotate(360deg)} }
        @keyframes fadeIn  { from{opacity:0;transform:translateY(4px)} to{opacity:1;transform:none} }
        @keyframes slideUp { from{opacity:0;transform:translate(-50%,12px)} to{opacity:1;transform:translate(-50%,0)} }
        @keyframes modalIn { from{opacity:0;transform:scale(.96) translateY(8px)} to{opacity:1;transform:none} }
        .fadein { animation: fadeIn .18s ease-out; }
        input::placeholder { color: #5b6479; }
      `}</style>
    </div>
  )
```

Notes for the engineer:
- The `@media (max-width: 1100px) { .ak-layout { ... } }` rule is removed along with the grid — no element in this file ever had `className="ak-layout"`, so that rule was already dead CSS (a pre-existing leftover, not something this plan needs to preserve).
- `AddKeyPanel` is now only mounted while `addKeyOpen` is `true` (it lives inside `<Modal>`, which returns `null` when closed). This means its internal form state (exchange, label, key, secret, test result) resets every time the modal is closed and reopened — this is a deliberate, desirable side effect for a form that holds an API secret, not a bug to fix.

- [ ] **Step 4: Type-check**

Run: `cd frontend && npx tsc --noEmit -p tsconfig.json`
Expected: no errors (this step supplies the `onAddKey` props that Tasks 3 and 4 were missing).

- [ ] **Step 5: Commit**

```bash
git add frontend/src/pages/AccountsPage.tsx
git commit -m "Move add-key form into a modal, drop two-column layout for full-width list"
```

---

### Task 6: Full regression check

**Files:** none (verification only)

- [ ] **Step 1: Type-check the whole frontend**

Run: `cd frontend && npx tsc --noEmit -p tsconfig.json`
Expected: no errors.

- [ ] **Step 2: Run the full test suite**

Run: `cd frontend && npx vitest run`
Expected: all test files pass (143 tests / 28 files as of this plan's writing — no new tests were added, so the count should be unchanged; this confirms no regression in any other page).

- [ ] **Step 3: Production build**

Run: `cd frontend && npx vite build`
Expected: build succeeds with no errors (the existing "chunk larger than 500 kB" warning is pre-existing and unrelated to this change — ignore it).

- [ ] **Step 4: Report to the user**

Tell the user (in Russian, per project convention):
- Что проверено: `tsc --noEmit`, `vitest run` (полный набор, без изменений в количестве тестов), `vite build`.
- Что не проверено: реальный визуальный вид в браузере — нужен запущенный бэкенд (`api-gateway`) для логина и захода на `/accounts`; попросить пользователя проверить вручную.
- Список изменённых файлов: только `frontend/src/pages/AccountsPage.tsx`.
- Что пересобирать не нужно — если запущен `vite dev`, подхватится по HMR.
