# Matrix relative slots (Novabot-style renumbering) — design

## Problem

Matrix (DCA) hedge legs currently use **absolute slots**: slot `-1`, `-2`, … are fixed
positions tied to a specific config index and a price computed from `cycle.StartPrice`.
When a per-slot SL fires, the existing modes (`rebuild_on_sl`, `rebuild_from_entry`)
re-enter the *same* absolute slot number/price.

We want the **Novabot** behaviour: slots are numbered by **relative depth from entry**,
and after an inner slot is stopped out the remaining slots **renumber toward entry**, so
the next new slot is opened with the config of the freed relative depth.

### Worked example (from the user)

- Slots `-1` and `-2` are filled. Each has its own per-slot SL (per its config).
- Price retraces toward entry and **SL(-1) fires** → slot `-1` closes (unloads exactly
  its own volume).
- The remaining slot `-2` **renumbers to `-1`**. Its own SL keeps its original settings —
  renumbering only changes the label and what the *next new* slot uses.
- If price continues in the hedge direction, the next new slot is opened as `-2`
  (using config `-2`'s parameters), priced relative to the current innermost open slot.

## Scope

- New **opt-in mode** via a `relative_slots` flag on the matrix strategy config.
- Applies to matrix strategies started by **both** the matrix bot and the hedge bot
  (the flag lives on the strategy config; no separate engine path needed).
- The existing absolute modes (`rebuild_on_sl`, `rebuild_from_entry`) remain unchanged
  and are **mutually exclusive** with `relative_slots` (ignored when it is on).
- Frontend toggle added to the matrix strategy form.

Non-goals: changing grid strategies; changing the absolute matrix behaviour.

## Approach

**Emergent relative numbering (no DB slot mutation).**

The relative number of an open slot is its rank by depth among currently-open slots;
"renumbering after SL" is an *emergent* consequence of counting open slots, not an
explicit mutation of stored slot numbers. This avoids mutating slot identity / linkId
formats (`-msl-{slot}-{seq}`) and keeps rollback simple.

Rejected alternative — **explicit renumber**: on SL close, decrement the `slot` field of
deeper open levels in the DB. More literal but mutates identity, risks linkId collisions,
and is harder to reason about / roll back.

## Design decisions (approved)

1. **Next slot price** = `deepest_open_slot_price × (1 + price_step_pct[next_rank])`.
   If no slots are open, the base is the L(0) entry price. (Generalises the user's
   "from the current innermost slot" to the multi-slot case: the base is the deepest
   currently-open slot.)
2. **Concurrency cap** = number of configured levels on that side (e.g. 5 `below`).
   Renumbering *recycles* deeper configs as inner slots close, so a cycle may open more
   than N slots in total, but never more than N **concurrently**.
3. **Per-slot SL** stays physically attached to the slot it opened with, with its original
   settings; firing it unloads exactly that slot's volume.
4. **Global TP** is computed from the leg's average entry (recomputed on every fill /
   SL-close via existing `matrixUpdateTP`) and closes the whole cycle.
5. **New flag** `relative_slots` on the matrix strategy config; works for matrix bot and
   hedge bot; mutually exclusive with `rebuild_on_sl` / `rebuild_from_entry`.

## Components

### 1. Config & storage
- DB: add column `strategies.relative_slots BOOLEAN NOT NULL DEFAULT false` (migration).
- `strategy_config` JSON (`botCfgJSON`): add `relative_slots` bool.
- Parse in `Engine.Start` (strategy load) and in the bot→strategy creation path.
- Validation: when `relative_slots=true`, ignore `rebuild_on_sl` / `rebuild_from_entry`.

### 2. Slot representation & relative rank
- `strategy_levels.slot` keeps the **absolute config index the slot opened with** (its
  baked params — size, step, tp, sl — are already applied to the placed orders and SL).
- **Relative rank** (helper, computed on the fly): open `filled` levels of a side sorted
  by distance from entry; closest = 1 → `L(-1)`, next → `L(-2)`, …
- **Next config index** for a new slot = `count(open slots on side) + 1`, capped at N.

### 3. Expansion logic (placing the next slot)
- Progressive placement (not all N pre-placed): maintain the target for the next relative
  slot. When price reaches `deepest_open_price × (1 + step[next_rank])`, place the slot
  with `config[next_rank]` (respecting existing virtual/limit/market handling).
- No open slots → base is L(0) entry.
- `next_rank > N` → stop expanding deeper.

### 4. SL-close handling (core renumbering)
- In `handleMatrixSLFill`, when `relative_slots` is on:
  - Mark `sl_closed` + `realized_pnl` (unchanged).
  - **Skip** the absolute re-entry paths (`matrixWaitingSlots`, `matrixRebuildFromSZLow`,
    `RebuildFromEntry`).
  - Recompute the next relative-slot target (open count dropped → `next_rank` decreases);
    cancel/re-place the pending next-slot order at the new relative level/config if one
    was resting. Existing slots' SLs are untouched.
  - Recompute global TP; cycle continues.

### 5. Global TP
- Unchanged mechanism (`matrixUpdateTP` from average entry); closes the cycle on hit.

### 6. Display / frontend
- Level-state payload gains `relative_slot` for open levels.
- `Chart.tsx`: in relative mode render `L(-1)`, `L(-2)`… from `relative_slot`; otherwise
  from the absolute `slot` as today.
- Matrix strategy form: add `relative_slots` toggle.

### 7. Hedge bot applicability
- No engine changes: the hedge leg is a matrix strategy; a matrix strategy with
  `relative_slots=true` gets the behaviour automatically.

## Edge cases

- **L(0) anchor**: relative renumbering applies to accumulation slots (`-1, -2, …`) only.
  An SL on L(0) (if configured) is handled as today (anchor lost / position gone).
- **Both sides**: the relative logic applies independently to each configured side
  (`above` / `below`), symmetrically.
- **Restart / reconcile**: relative state is rebuilt from open levels in the DB (sort by
  depth → ranks); the next-slot target is recomputed from the deepest open slot. Works
  with the matrix reconcile self-healing (fills recovered on restart).
- **Cap reached**: never more than N concurrently open; stop expanding at `next_rank > N`.

## Testing

- Unit: relative-rank helper (ordering by depth, both sides, empty), next-config-index
  (cap, recycle after close), next-slot price base (deepest open vs entry).
- Integration (matrix engine, real DB): fill `-1` and `-2`; fire SL(-1); assert the
  remaining slot reports relative rank `-1`, the next new slot uses config `-2` and is
  priced from the current deepest open; global TP recomputes; cycle stays open.

## Rollout

- Migration for `relative_slots` column.
- Backend build + unit/integration tests.
- Frontend toggle + Chart label wiring; typecheck.
- Rebuild + restart api-gateway; run migration on prod via `deploy.sh`.
- Default `false` — existing strategies unaffected.
