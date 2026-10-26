# Hedge-bot flip: promote in place instead of detach — Design

## Problem

Found live (2026-10-25/26, account "SIS", bot "ALS-Bot" — `bot_kind='hedge'`, same
`hedge_engine.go` code path used by every hedge-kind bot, e.g. "Gonchar 2.0" hedge): when
a hedge bot's main leg closes via TP with `hedge_tp_main_mode='flip'` enabled,
`handleMainTpFlip` → `releaseHedgeToGrid` **detaches** the surviving hedge leg from the
bot entirely — `bot_id=NULL`, `strategy_type` forced to `'grid'`, `sl_pct=0` (no stop
loss at all), `tp_pct=0.5` (a thin 0.5% take-profit is the only protection left). A fresh
hedge is only opened against this detached leg once its drawdown crosses the bot's normal
activation threshold (`hedge_act_value`, e.g. -2%) — not immediately.

Two consecutive flips on APRUSDT under ALS-Bot (2026-10-25 12:26 and 2026-10-26 01:31)
produced two such detached, unprotected legs sitting in parallel:

- `92eacd7c` (short) — still open, no SL, waiting on its 0.5% TP.
- `ca194446` (long) — still open, no SL, down -29.4% ($-12.60).

A third strategy (`0842f299`, the hedge meant to pair against the newest flip) could not
even activate: `resolveHedgeSlotConflict` (with the bot's `hedge_close_type=0`, "wait for
cycle end") found the (symbol, direction) slot already occupied by the older detached leg
and skipped activation — logged repeatedly as "слот занят (92eacd7c), ожидание
завершения цикла". Each flip leaves behind a parallel orphaned row in the same direction
slot, so a second flip before the first orphan closes blocks the bot from re-hedging at
all.

Confirmed this has no matrix-bot equivalent to port from: `matrix_engine.go` has no
main/hedge pairing and no flip concept at all — both matrix legs are opened
independently by the bot's own per-tick loop, with the same `bot_id`, whenever their slot
is empty. The entire "flip" feature in this codebase is `handleMainTpFlip` +
`releaseHedgeToGrid` in `hedge_engine.go`, shared unconditionally by every `bot_kind='hedge'`
bot (ALS-Bot and Gonchar 2.0's hedge variant run the identical code).

## Goal

When a hedge bot's main closes via TP and `hedge_tp_main_mode='flip'`: the hedge leg
should become the new main **without ever leaving the bot** — keeping its existing
`strategy_type`, SL/TP config, and `bot_id` unchanged — and a new hedge should be opened
against it immediately, with no window where the position is unprotected or where the bot
can get stuck unable to re-hedge.

## Scope

Three call sites invoke `releaseHedgeToGrid` today; this redesign touches only the two
that are genuinely "flip":

1. **`handleMainTpFlip`** (main closed via TP, `hedge_tp_main_mode='flip'`) — **in scope**.
2. **Missed-flip recovery** inside `checkHedgeDeactivation` (main already `stopped`, its
   last cycle ended with `result='tp'`, but the flip never fired in time) — **in scope**,
   same new behavior, since it's explicitly the recovery path for case 1.
3. **Ordinary deactivation** inside `checkHedgeDeactivation` (main is still alive/active,
   simply recovered into profit past `hedge_deact_*`) — **out of scope, unchanged**. There
   is no "main closed" here; detaching the hedge into a standalone grid remains the
   correct behavior, confirmed with the user.

## Design

### New function: `promoteHedgeToMain`

Replaces `releaseHedgeToGrid` at the two in-scope call sites (signature mirrors it:
`(ctx, botID, strategyID, symbol string, hedgePos hedgePosInfo, reason string)`).

1. Read the promoted strategy's current `hedged_strategy_id` (the now-closed old main) —
   needed below to delete it, same as today.
2. **Do not touch** `bot_id`, `strategy_type`, `sl_pct`, `sl_type`, `tp_pct`, `tp_mode`,
   `grid_levels`/`matrix_levels`/`als_thresholds`/steps, or any other trading config on
   the promoted strategy. It keeps running exactly as it was — same SL, same TP
   mechanics, still owned by the bot. The only field this function changes on the
   promoted row is clearing `hedged_strategy_id` (it was pointing at the old main; it's
   now NULL since this strategy is no longer anyone's hedge — it *is* the main).
3. Delete the old (closed, `status='stopped'`) main row — unchanged from today's
   behavior (`DELETE FROM strategies WHERE id=$1 AND status='stopped'`, cascades to its
   cycles/levels/events via `ON DELETE CASCADE`), and `ForceRemoveStrategy` to drop it
   from the live engine.
4. Immediately call `createBotStrategy` for a new hedge in the opposite direction, with
   `hedgedStrategyID = <promoted strategy's id>` — **no threshold wait**. This is the
   behavioral change from "ordinary activation" (which waits for `hedge_act_value`
   drawdown on the main before opening a hedge): right after a flip we already know a
   hedge is wanted, so open it now.
5. Keep the existing `hedge_sessions` bookkeeping (`ended_at=NOW(), end_reason='deactivation'`
   on the old session) and `restoreHedgeMainControls` call, logging, and
   `s.engine.Notify` — unchanged from `releaseHedgeToGrid`.
6. Do **not** set `flip_origin_bot_id` — that field exists solely to let
   `positionPassesBotFilter`'s whitelist check treat a *detached* (`bot_id=NULL`)
   promoted leg as still belonging to the bot. Since the promoted leg's `bot_id` never
   changes here, the whitelist already sees it as the bot's own strategy; the bypass
   mechanism is unnecessary for this path and is left untouched for scenario 2 (which
   still uses `releaseHedgeToGrid` as-is).

### `releaseHedgeToGrid` itself

Stays exactly as it is today, still used by the one remaining call site (ordinary
deactivation, scenario 2 above).

### Side effects this fixes without extra code

- **No more orphan accumulation**: a (account, symbol, direction) slot is now always
  held by at most one live strategy row, continuously reused across however many flips
  happen in a chain — never a second parallel row.
- **`resolveHedgeSlotConflict` / "слот занят" no longer applies to flip chains**: there is
  nothing left in the old direction's slot once promotion flips its role in place, so the
  very next flip (or a fresh activation) never collides with a stale detached leg from a
  previous flip.
- **No unprotected window**: the promoted leg's SL (whatever the bot's own config already
  had — ALS per-level stops, grid conditional SL, etc.) is never cleared, so there is no
  point in time where the position has zero downside protection.

## Testing

- Regression test reproducing the exact live scenario: a hedge with filled levels whose
  paired main closes via TP → assert the hedge row's `bot_id`, `strategy_type`, `sl_pct`
  (or the strategy-type-appropriate SL config) are **unchanged** after the flip, its
  `hedged_strategy_id` is cleared, the old main row is deleted, and a new hedge row exists
  immediately (not gated on a drawdown check) with `hedged_strategy_id` pointing at the
  promoted strategy.
- Chained-flip test: two flips in a row on the same symbol never produce two live rows in
  the same (account, symbol, direction) slot.
- Existing `releaseHedgeToGrid` tests (scenario 2, ordinary deactivation) must still pass
  unchanged — confirms the split didn't regress the path that's intentionally left alone.
- Full `services/api-gateway` test suite run for regressions in adjacent hedge-engine
  mechanics (hedge_sessions accumulation, paired-close, `positionPassesBotFilter`
  whitelist behavior for scenario 2's still-detached legs).
