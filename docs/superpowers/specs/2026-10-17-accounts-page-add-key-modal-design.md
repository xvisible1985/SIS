# API keys page: add-key form as a modal

## Context

The `/accounts` page (`AccountsPage.tsx`) is a mature, already well-designed component: stats strip, toolbar, a list of expandable `KeyCard`s with permissions/risk settings/activity tabs, and an "add key" wizard (`AddKeyPanel`). The wizard is currently always rendered in a sticky 420px-wide left column, taking up roughly half the page width even when the user isn't adding a key.

This is a scoped redesign driven by user feedback, not a ground-up visual overhaul: the `KeyCard` and every other section stay as they are.

## Changes

1. **Layout** — drop the `420px minmax(0, 1fr)` two-column grid. Stats strip, toolbar, and the key list become full width.
2. **Toolbar** — add a primary **"+ Добавить ключ"** button that opens the add-key modal. Remove the **"Биржа: все"** filter button — it currently has no `onClick` handler (dead UI) and is redundant once the user is explicitly picking an account.
3. **AddKeyPanel → modal** — the existing `AddKeyPanel` component (all 4 steps, test/save flow, collapsible "how to" guide) moves unchanged in content/behavior into a modal, triggered by the new toolbar button. `AccountsPage` gets a new `addKeyOpen` boolean state.
   - The existing `Modal` component hardcodes `maxWidth: 460`. Add a `width` prop (default 460, so the delete/rotate modals are untouched) and pass `560` (or similar, 560–600px range) for the add-key modal specifically.
   - On successful submit (`handleAddKey`), close the modal in addition to the existing behavior (reset form, expand new card, toast).
4. **Add-key modal color treatment** — within the exchange-selection step only, the selected exchange chip is highlighted using that exchange's own brand color (`EXCHANGES[id].color`) instead of the generic blue highlight. Everything else in the modal (header icon, CTA buttons, field borders) keeps the current blue/dark palette unchanged.

## Out of scope

- `KeyCard` (collapsed or expanded) — no changes.
- `StatsStrip`, `PageHead`, `EmptyState`, delete/rotate modals, `Toast` — no changes.
- Mobile layout — handled in an earlier, separate change (Signals page); this page's mobile behavior is untouched here.

## Affected mechanics

Frontend only, single file: `frontend/src/pages/AccountsPage.tsx`. No backend, signal engine, strategy engine, or DB changes. No existing dedicated test file for this page (`__tests__/AccountsPage.test.tsx` does not exist); verification is via `tsc --noEmit`, the full `vitest run` suite (regression check), and `vite build`.

## Testing

- `tsc --noEmit` — no type errors.
- `vitest run` — full suite passes (no AccountsPage-specific tests exist today; this confirms no unrelated regression).
- `vite build` — production build succeeds.
- Manual check recommended by the user once the backend is running (login required to reach `/accounts`).
