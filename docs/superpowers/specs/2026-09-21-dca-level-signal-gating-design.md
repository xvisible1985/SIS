# DCA/Matrix Level Signal Gating — Design

## Problem

`GridStep.UseSignal`, `MatrixLevel.UseSignal`, and `MatrixEntryLevel.UseSignal` are boolean
fields that already exist in `pkg/strategy/types.go`, are stored in the DB (`steps`/
`matrix_levels`/`matrix_entry_level` JSONB columns), and already have working toggle UI in
`BotForm.tsx`, `HedgeBotForm.tsx`, and `MatrixBotForm.tsx` (a "Signal"/"No" button per level).
The comment on each field says "gate this level on signal before placing" — but nothing in
`pkg/strategy`'s placement logic ever reads `.UseSignal`. The toggle is fully wired on the
frontend and in the data model, and completely inert on the backend: every level, regardless
of this flag, gets placed exactly the same way it always has.

Found live (2026-09-21, PTBUSDT/Semera account, grid strategy): the strategy has a real
signal configured (`signal_configs: [{"name": "st-flip", ...}]`) and `use_signal=true` set on
grid steps in the UI, but every level's order was placed on the exchange immediately at
cycle start, exactly as if no signal gating existed at all.

## Goal

A DCA/averaging level flagged `UseSignal=true` must not sit as a live resting order on the
exchange unless the strategy's configured signal currently agrees with the strategy's
direction. Concretely:

- **Before placing**: if price has reached the level's trigger but the signal doesn't (yet)
  match, the order stays unplaced — pending, not on the exchange.
- **After placing**: if the signal later stops matching while the order is still resting
  (unfilled) on the exchange, cancel it and revert the level to the same unplaced/pending
  state, so it can re-place automatically once the signal returns (and the price condition
  still holds).
- **Already-filled levels are never touched.** A position that already exists is real money
  at risk — signal loss only withholds/removes *pending* orders, never closes an open
  position. TP/SL remain the only mechanisms that touch a filled level.

This applies to **both** grid and matrix strategies (`UseSignal` exists on `GridStep`,
`MatrixLevel`, and `MatrixEntryLevel` alike), using the strategy's **own existing**
`SignalConfigs`/timeframe — the same signal already used to gate a cycle's first entry. No
new per-level signal picker is introduced; the per-level control is purely the existing
on/off `UseSignal` toggle.

## Architecture

Both strategy types already have a continuous "virtual level" monitoring loop that
re-evaluates pending, not-yet-placed levels against live price on every tick and calls
`PlaceOrder` once a level's price trigger is reached:

- Grid: `gridVirtualPriceTick(ctx, price)` (`pkg/strategy/cycle.go:5706`), driven by
  `ForceVirtual` levels — currently `ForceVirtual := step.OrderType == "virtual" ||
  step.PriceMovePct > 0`.
- Matrix: the equivalent price-tick path (`matrixPriceTick`/`matrixIsVirtual`,
  `pkg/strategy/matrix.go`) driven by an analogous virtual/real distinction.

This is the natural, already-tested hook point — no new ticker, no new subscription
lifecycle, no new goroutine. Two changes land in this existing machinery:

1. **Force virtual for signal-gated levels.** Extend both types' "is this level virtual"
   computation to also be true whenever `UseSignal == true`, regardless of `OrderType` /
   `PriceMovePct`. A signal-gated level is *always* software-monitored — it can never be a
   blind resting order placed unconditionally at cycle start, which is exactly the bug this
   feature fixes.

2. **Check the signal at the decision point.** Where the existing tick handler currently
   decides "price condition met → call PlaceOrder", add: if the level's `UseSignal` is true,
   also query `signal.Engine.QueryState(symbol, tf, configs)` (a synchronous, already-cached
   lookup — no new subscription; see `pkg/signal/engine.go:468`) and require it to match the
   strategy's direction (`Buy` for long, `Sell` for short) before actually placing. If the
   signal doesn't match, the level stays `Pending` — the next tick re-evaluates from scratch.

3. **New: cancel-on-signal-loss.** The same tick handler, for a level that is `UseSignal ==
   true` AND currently has a live `ExchangeOrderID` (placed, unfilled), additionally checks
   whether the signal still matches. If it no longer does, cancel the resting order
   (`Exchange().CancelOrder`, same pattern `closeCycle`/`matrixHandleSLFlattenOrContinue`
   already use elsewhere for order cancellation) and revert the level to `Pending` with
   `ExchangeOrderID` cleared — identical shape to a level that was never placed. It resumes
   silent monitoring and re-places automatically once the signal returns, with no special
   "was previously placed" state to track.

This means signal state is read fresh on every price tick rather than through a persistent
subscribe/unsubscribe lifecycle (unlike `awaitSignal`'s one-shot entry-gating pattern) —
simpler lifecycle, and reaction latency is bounded by tick frequency (already sub-second to a
few seconds for both existing monitors), which is fine for this use case.

## Data model

No new columns or JSON fields. `UseSignal` already exists and is already persisted; this
feature is purely backend enforcement of a flag that already round-trips through the API and
UI correctly.

## Frontend

`Chart.tsx` already renders three tiers for a level's price line, using real colors already
in the codebase:

- Placed (real resting order): `lineStyle: 2` (dashed), `#34d399` (buy) / `#f87171` (sell).
- Virtual/pending (waiting on price only): `lineStyle: 3` (dotted), lighter `#6ee7b7` /
  `#fca5a5`.

New: a level with `use_signal === true` and `status === 'pending'` renders with a **muted**
version of its direction color (reduced opacity, not a new gray palette) instead of the
existing lighter virtual-dotted color, plus a short `⏸ signal` text badge appended to the
level's label. This was chosen over two alternatives (plain gray dotted matching the existing
virtual style; plain gray dashed matching the placed style) specifically so the line still
visually reads as "this level's own buy/sell color," just visibly held back — confirmed via
browser mockup comparison.

Distinguishing "still waiting on price" from "price reached, but signal is what's holding it
back" needs no new backend field: the frontend already has both `target_price` and the live
current price for the level, so it can compute locally whether price has already crossed the
trigger and choose which sub-state to hint in the label if desired. Not required for v1 — the
single muted+badge style covers both sub-states adequately, and this is explicitly left open
to refine later.

## Error handling / edge cases

- **No signal engine / no configs**: if `sr.runner.signalEngine == nil` or
  `len(SignalConfigs) == 0`, a `UseSignal=true` level must behave exactly as if `UseSignal`
  were false — place unconditionally once its price condition is met. (Mirrors `awaitSignal`'s
  existing fallback for the entry-gating case — same missing-config fallback, same reasoning:
  a `UseSignal` flag with nothing to evaluate must never block a bot's trading indefinitely.)
- **Cancel races a fill**: if the cancel-on-signal-loss check and a genuine fill happen to
  race (order fills on the exchange in the same window the signal drops), the existing
  fill-handling WS path and the cancel call both hit the same exchange order — `CancelOrder`
  on an already-filled/gone order must be treated as a no-op via the established
  `isOrderGone(err)` helper already used throughout `cycle.go`/`matrix.go`, not as an error.
- **L(0)/entry level specifically**: `MatrixEntryLevel.UseSignal` gates the very first
  (anchor) entry the same way — this is a *different* mechanism from the existing
  `signal_filter`+`awaitSignal` cycle-start gate (which decides whether to start a cycle at
  all) and can coexist with it; `UseSignal` on the entry level gates *this specific level's
  order*, not cycle start.

## Testing

Both `pkg/strategy` monitoring loops (`gridVirtualPriceTick`, matrix's price-tick path) already
have existing unit test coverage exercising the "place once price condition met" path — new
tests extend those with `UseSignal=true` cases:

- Price condition met, signal matching direction → order placed (existing behavior,
  unchanged when `UseSignal=true` and signal agrees).
- Price condition met, signal NOT matching (or Neutral) → order stays `Pending`, no
  `PlaceOrder` call.
- Level already placed (`ExchangeOrderID` set, unfilled), signal flips away → `CancelOrder`
  called, level reverts to `Pending`/no `ExchangeOrderID`.
- Level already `Filled` → untouched regardless of signal state (regression guard for the
  "never touch a filled position" invariant).
- No signal engine / empty `SignalConfigs` with `UseSignal=true` → same as `UseSignal=false`
  (fallback parity with `awaitSignal`).

Frontend: extend `Chart.tsx`'s existing level-rendering tests (if present) or add a focused
test asserting the muted+badge style renders only for `use_signal && status==='pending'`
levels, leaving the existing placed/virtual styles unchanged for every other case.
