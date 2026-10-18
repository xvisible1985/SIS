# ALS (Anti-Liquidation System) Hedge Strategy — Design

## Problem

The platform's hedge-bot config (`HedgeBotForm.tsx`, "Хедж позиция" tab) already has a
"Тип стратегии" selector with two buttons: **Matrix** (implemented, the only working
option today) and **ALS** — a disabled placeholder ("ALS скоро") with zero backend
wiring. The user wants a real third-party risk-management system (ALS, "Anti-Liquidation
System" — a hedge-automation service documented at `docs-als.notion.site`) ported in as
the second real strategy type behind that button.

ALS's own architecture is a standalone service watching arbitrary exchange positions
(manual or bot-opened) and reacting with hedges. Ours is scoped differently and more
narrowly on purpose (confirmed with the user): **ALS here only manages positions opened
by the bot/strategy it's configured on** — not account-wide — matching how the config
already lives inside a specific bot's edit screen, not a separate account-level page.

## Source material

All 12 sub-pages of `docs-als.notion.site`'s public docs were fetched and read in full
(2026-10-18): principle of operation, main/hedge position determination, TP management
coupling, Unsafe hedge percent, virtual stop-orders, DCA-grid integration, settings
reference, Safe Zone, glossary, Telegram error messages, connection procedure, API key
setup. The mechanics referenced throughout this spec (Hedge thresholds, Safe Zone,
breakeven stop, Unsafe hedge percent, virtual stop memory-bank, TP pause/resume,
pair-close by combined ROI) are drawn directly from that material.

## What already exists and gets reused as-is

Cross-referencing ALS's documented mechanics against the current codebase surfaced that
most of the underlying engine already exists, built for the Matrix hedge strategy
(`pkg/strategy/matrix.go`), and needs no changes:

- **Safe Zone** — `Strategy.SafeZonePct` + `matrixCheckWaitingReentry` (`matrix.go`)
  already blocks hedge re-entry until price recovers `SafeZonePct`% beyond the SL trigger
  that closed the level, in the direction of the main position — exactly ALS's documented
  Safe Zone behavior (zone cleared only on exit toward the main position).
- **Breakeven stop with delay** — `Strategy.StopCondPct`/`StopReplacePct` +
  `matrixApplyStopCondSLs` already implement ALS's "Min ROI for hedge stop" (the price
  move required before the stop is placed/replaced) and "Stop Loss Break Even Hedge" (the
  resulting stop trigger, trailing behind price on the favorable side).
- **Close both positions on combined result** — the existing "Завершение" outer tab
  (`deact_close_type`/`deact_close_value`/`breakeven_profit`) already closes both legs
  together on a combined PnL/ROI/breakeven condition. This is ALS's documented pair-close
  feature, already fully built and shared as-is.
- **Main/hedge role assignment by volume** — `bot_engine.go`'s hedge-pairing logic
  ("bigger position = main") already matches ALS's documented rule for the two-positions-
  at-startup case.
- **Hedge volume sized off the live main position** — `Strategy.SizeAsMain` +
  `effectiveDeposit()` (`cycle.go`) already compute slot size as a percentage of the
  *current* opposite-direction position's USDT value, recomputed live on every tick. This
  is the base ALS's "% от объёма основной позиции" needs — already wired and working.
- **Activation gate** — the existing "Активация" outer tab (force-activation toggle,
  activation signals, "если занят слот" conflict handling) is reused unchanged as the
  gate for *when ALS starts watching* the main position. Its own single `act_type`/
  `act_value` threshold becomes redundant for ALS (threshold row 1 in the new table takes
  over that role) and is hidden in the UI when ALS is selected — the tab's other controls
  stay active and apply the same way as for Matrix.

## What's genuinely new

Three things ALS needs that don't exist anywhere today, confirmed with the user as
deliberately scoped as follows:

### 1. Price-distance thresholds, not ROI (deviation from the original ALS)

The source ALS computes hedge thresholds in **ROI%** of the main position. Per explicit
user direction, this implementation uses the **same price-distance units already in
`act_type`** (0 = % from the last filled order, 1 = % from average entry price) instead
of introducing ROI-based math — consistency with the existing codebase idiom was chosen
over a literal port.

### 2. Target-based hedge sizing with delta top-up (the actual new mechanic)

Today, when a matrix level fires, it places a flat, fixed-size order. ALS's levels are
different: each threshold row defines a **cumulative target** — "when price reaches this
distance from main's entry, the hedge's *total* volume should be `target_hedge_pct`% of
main's *current* live volume." This is genuinely new logic that must be built:

```
target_usdt = target_hedge_pct / 100 * effectiveDeposit(currentPrice)   // reuses existing helper
already_open_usdt = <sum of currently open hedge position's USDT value>
top_up_usdt = max(0, target_usdt - already_open_usdt)
```

Only `top_up_usdt` gets placed — not the full `target_usdt` — so a threshold table reading
`-10% → 30%`, `-30% → 50%`, `-60% → 80%` behaves exactly like the source ALS's own worked
example: the second threshold adds 20 more percentage points of volume, not another flat
50%. Because `effectiveDeposit()` is recomputed live, a main position that grew via its
own DCA between two hedge threshold triggers is handled correctly — the target is always
relative to main's volume *at the moment the threshold fires*, not frozen at config time.

This lives entirely in the new engine file; it does not touch `matrix.go`'s own (simpler,
flat-add) sizing.

### 3. Virtual stop-order memory-bank (ALS-only, not shared with Matrix)

`Strategy.MaxStopActive` already exists and is already checked once, in
`matrixPlacePerLevelSL` — but today it only **skips** placing a new stop past the limit
(logs a warning, leaves the level without a stop). The source ALS instead *evicts and
remembers*: cancel the stop furthest from current price, persist its parameters, and
restore it from storage the moment any stop closes and frees a slot.

**Explicitly confirmed with the user: this upgrade applies to the new ALS engine only.**
`matrix.go`'s existing `MaxStopActive` check is left exactly as it is today (skip +
warn) — no shared helper, no change to Matrix's behavior for any other live strategy.

## Architecture

- **New strategy type**: `strategy_type = 'als'`, dispatched in `pkg/strategy` alongside
  the existing `'grid'`/`'matrix'` values (same dispatch pattern already used for those
  two).
- **New engine file**: `pkg/strategy/als.go` — a separate, self-contained engine (not an
  extension of `matrix.go`), per explicit user decision. It may structurally mirror
  proven patterns from `matrix.go` (the safe-zone and stop-cond-replace math in
  particular) but does not call into or share code with it, beyond the pieces listed
  above that are reused as-is through `Strategy`'s existing fields and helpers.
- **Shared data table**: ALS levels are persisted as ordinary rows in the existing
  `strategy_levels` table (`slot`, `target_price`, `status`, `filled_price`,
  `exchange_order_id`, `exchange_link_id`, …) — the same schema Matrix levels already
  use. `target_price` is recomputed from the live main-position entry price on every
  tick rather than fixed once, but the column and its consumers (the terminal chart,
  `OrdersTable`, reconcile logic) need no changes — they already just read whatever's in
  the row.

## Data model changes

**New table**, ALS-only:

```sql
CREATE TABLE als_stop_memory (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    strategy_id   UUID NOT NULL REFERENCES strategies(id) ON DELETE CASCADE,
    level_id      UUID NOT NULL REFERENCES strategy_levels(id) ON DELETE CASCADE,
    symbol        TEXT NOT NULL,
    side          TEXT NOT NULL,
    trigger_price NUMERIC(18,8) NOT NULL,
    qty           TEXT NOT NULL,
    evicted_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX idx_als_stop_memory_strategy ON als_stop_memory (strategy_id);
```

**New columns on `strategies`** (read/written only when `strategy_type='als'`):

| Column | Type | Meaning |
|---|---|---|
| `als_thresholds` | `JSONB` | Array of `{trigger_type (0/1, mirrors act_type), trigger_pct, target_hedge_pct}` — the "2. Пороги" table rows. |
| `unsafe_hedge_min_pct` | `NUMERIC` | Minimum allowed top-up order size, % of main's current volume. |
| `unsafe_hedge_max_pct` | `NUMERIC` | Maximum allowed top-up order size, same units. |
| `stop_market_size_k` | `NUMERIC` | Multiplier applied to a hedge's own stop-order size relative to the hedge size it protects. |
| `tp_control_enabled` | `BOOLEAN` | Enables TP pause/resume coupling (see below). |
| `initial_stop_be_pct` | `NUMERIC`, nullable | Optional: stop placed immediately at hedge open, before `stop_cond_pct` fires and replaces it with `stop_replace_pct` (reuses those two existing fields). |

**New columns on `strategy_cycles`** (ALS-only use):

| Column | Type | Meaning |
|---|---|---|
| `paused_tp_price` | `NUMERIC`, nullable | Main position's TP price, saved when a hedge opens and the TP is cancelled. |
| `paused_tp_qty` | `NUMERIC`, nullable | Main position's TP quantity, saved alongside. |

## Unsafe hedge percent — the volume cap

Before placing any top-up order, clamp it against the currently-unprotected (no stop
placed yet) hedge volume, per the source ALS's own formula:

```
Vh = Vmax − Vopen + Vstop / stopMarketSizeK
```

where `Vmax` is the strategy's configured max single top-up (`unsafe_hedge_max_pct`% of
main's current volume), `Vopen` is the hedge volume currently open without a stop, and
`Vstop` is volume already covered by a placed stop. If the computed top-up exceeds `Vh`,
clamp it down to `Vh` (never silently skip the level — place whatever's safely allowed,
log the clamp).

## TP pause/resume (main position)

When `tp_control_enabled` and a hedge opens on a previously single-sided position: save
the main position's current TP price/qty into `strategy_cycles.paused_tp_price`/
`paused_tp_qty`, cancel it, and log an info event (mirrors ALS's own "TP was cancelled"
Telegram message format). When the hedge fully closes and only the main position
remains: read the saved values back, re-place the TP, clear the saved columns, log an
info event ("TP was re-created"). If the TP gets re-created by someone/something else
before ALS does it (same detection the existing TP-replace-race handling in `cycle.go`
already watches for): warn, and disable `tp_control_enabled` for this one strategy going
forward (not platform-wide) — mirrors the source ALS's own documented behavior exactly.

## UI changes

- **Strategy type selector** (`HedgeBotForm.tsx`, "Хедж позиция → Стратегия" tab): ALS
  button becomes clickable, loses the "скоро" badge.
- **New "2. Пороги" sub-tab** (replaces "2. Матрица" when ALS is selected): editable
  table of threshold rows — trigger type (dropdown: "От последнего ордера" / "От ТВХ
  Main"), trigger %, target hedge % of Main. Add/remove row. Mockup approved 2026-10-18
  (see chat; not persisted as a separate file — the fields above are the source of
  truth).
- **"3. Параметры" sub-tab content for ALS** (same outer tab, different fields than
  Matrix's own "3. Параметры"): Min ROI for hedge stop (reuses `stop_cond_pct` field/UI),
  Stop Loss Break Even Hedge (reuses `stop_replace_pct`), Safe Zone % (reuses
  `SafeZonePct`), Stop Market Size K (new), Unsafe hedge % min/max (new, two fields), TP
  Control Enabled toggle (new), Initial Stop Loss Break Even Hedge toggle + value (new,
  optional).
- **"1. Базовые" sub-tab**: unchanged, shared verbatim with Matrix (direction, entry
  order type, margin type, hedge exchange mode, active slots, max conditional stops —
  the last of these, `MaxStopActive`, now actually gets ALS's memory-bank behavior
  applied to it when `strategy_type='als'`, vs. the existing skip-and-warn behavior that
  stays for Matrix).
- **"Активация" outer tab**: unchanged controls, but the single `act_type`/`act_value`
  fields are hidden when ALS is selected (threshold row 1 replaces that role).
  Force-activation, activation signals, and the slot-conflict dropdown stay visible and
  behave identically to Matrix.
- **"Завершение" outer tab**: fully unchanged, shared as-is (already covers ALS's
  pair-close-by-combined-result feature).

## Notifications

Reuse the existing `tg_notifier.go` infrastructure. New message templates for ALS-
specific events (stop → memory, memory → stop restored, TP cancelled, TP re-created, TP
control disabled due to external interference), formatted in the same style as the
platform's existing strategy Telegram notifications. Reuse whatever repeat-suppression
window pattern `tg_notifier.go` already has for existing message types; if none exists,
this spec doesn't introduce a new one — notification throttling is not in scope beyond
matching current behavior.

## Error handling

- A `GetMarkPrice`/position-fetch failure during a threshold check: skip this tick, retry
  next tick — same pattern already used throughout `cycle.go`/`matrix.go` for live-price-
  dependent logic.
- An exchange rejection placing a top-up or stop order: log via `sr.errlog`, leave the
  level `pending` for retry on the next tick — same pattern as existing grid/matrix level
  placement failures.
- `als_stop_memory` restore failure (exchange rejects the restored stop): leave the row
  in the table, retry on the next eviction/restore check — never drop a remembered stop
  silently.

## Testing

TDD throughout, per the project's established practice:

- Unit tests on `als.go`'s pure calculations (target/delta sizing, Unsafe hedge percent
  clamping, trigger-distance evaluation) with no exchange dependency.
- Integration-style tests using the existing `fakeExchange` harness (`pkg/strategy`) for
  the stateful flows: threshold firing → top-up placement, stop eviction → memory-bank
  write, slot-free → memory-bank restore, TP pause/resume, Safe Zone re-entry blocking
  reused from Matrix (confirm it behaves identically when driven from the ALS engine).
- `services/api-gateway` integration tests for the new `strategies` columns (create/
  update round-trip) and the new `als_stop_memory` table's basic CRUD path.
- Full regression run (`pkg/strategy`, `services/api-gateway`, both `-tags integration`)
  before each deploy, comparing against the established baseline failure counts, as done
  for every fix this session.

## Out of scope for this iteration

- Literal ROI-based thresholds (superseded by the price-distance-% decision above).
- Any account-wide / cross-bot ALS mode (confirmed single-strategy scope only).
- Upgrading Matrix's own `MaxStopActive` behavior to the memory-bank mechanic (confirmed
  ALS-only).
- The source ALS's "inside/outside DCA grid" and "до замка / сверх замка" framing as
  distinct configurable *modes* — both are naturally expressible just by what values the
  user enters in the thresholds table (targets ≤100% vs >100% of main), so no separate
  toggle is needed.
