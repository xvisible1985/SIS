# Matrix Slot Repair-Priority Implementation Plan

**Goal:** Stop matrix bot legs from getting permanently orphaned (one direction open, the other never reopening) when the bot's `max_long`/`max_short` capacity is smaller than the number of symbols currently competing for slots.

**Architecture:** Split `ensureMatrixStrategies`' single scan-and-open loop into two ordered passes per tick: a **repair pass** that completes already-half-open pairs first, then the existing **new-pair pass** with whatever capacity remains.

**Tech Stack:** Go (`services/api-gateway/matrix_engine.go`), existing Postgres schema (no new columns/migrations).

---

## Root cause (confirmed via live DB evidence, 2026-07-24)

`ensureMatrixStrategies` (`matrix_engine.go:397`) scans every candidate symbol (the full exchange symbol list when whitelist is empty — hundreds) and independently tries to open long/short for each, gated only by a silent `continue` when that direction's slot count (`activeLong >= maxLong` / `activeShort >= maxShort`) is already at capacity. There is no concept of "this symbol already has one leg open, prioritize completing it over starting a brand-new pair." When more symbols are actively competing than there is long/short capacity for, whichever symbol's leg claims a slot first (pure iteration/tick-timing luck) keeps it indefinitely, while other symbols' already-open legs sit starved with no path back to a complete pair.

Live evidence (MatrixNova bot, `max_long=2, max_short=2`):
- AKEUSDT long: closed via legitimate TP at 03:49, `status='stopped'` (not `'paused'` — genuinely eligible for auto-restart), zero recreation attempts logged in the following 50+ minutes.
- DEXEUSDT short: `status='stopped'` for 2+ hours, zero recreation attempts.
- VELVETUSDT short: cycled through 7 rapid TP closes in under an hour; its long counterpart was never created even once across that entire history.

All three are symptoms of the same starvation pattern — confirmed not a regression from tonight's matrix-cycle-lifecycle-redesign (Tasks 1-8, already merged), since none of those touched `ensureMatrixStrategies`'s slot-allocation code. That redesign likely made the starvation *more visible*: TP/SL closes now correctly end the cycle immediately instead of re-arming in place, so slots free up (and get re-contested) far more often than under the old model.

## Design

Two ordered passes per tick, both still bounded by the existing `maxTotal`/`maxLong`/`maxShort` checks (unchanged):

**Pass 1 — repair.** Query for symbols (for this bot) where exactly one direction is currently `active`/`finishing` and the other direction is **either `stopped` or has no strategy row at all** — both count as repair-eligible. The "no row at all" case matters: VELVETUSDT's long leg was never created even once across 7 short-leg cycles, so it has no `stopped` row to match — only the pairing (one direction live, the other direction missing or stopped) identifies it. `paused` is explicitly excluded either way — a user-initiated stop must stay closed, `directionHasLiveStrategy` already encodes this distinction and is reused as-is for the actual open attempt. For each repair candidate, attempt to open the missing direction using the exact same `createBotStrategy`/position-adopt path the new-pair loop already uses — no new open/adopt logic, just called earlier and against a narrower candidate list.

Unlike the new-pair path, repair does **not** wait for `matrixActivationSignalOK` — mirrors the existing philosophy for adopting an orphan exchange position ("leaving it unmanaged is worse than opening early"): a pair that already has one real leg in the market is worse off staying unbalanced than getting its other leg back promptly.

**Pass 2 — new pairs.** Unchanged existing logic, using whatever `maxLong`/`maxShort`/`maxTotal` headroom pass 1 left.

Self-balancing by construction: when nothing needs repair, pass 1 finds zero candidates and pass 2 gets full capacity, identical to today's behavior. No new config knob, no reserved/wasted capacity.

## Testing

- Go integration test: seed one symbol with long `active` + short `stopped` (repair-eligible), another with long `active` + short `paused` (must NOT be touched), and a third with long `active` + no short row at all (also repair-eligible, the VELVETUSDT case) — assert only the `stopped` and no-row symbols get a recreation attempt.
- Go integration test: confirm repair-pass opens bypass the activation-signal gate (a repair-eligible symbol with no confirming signal still gets its missing leg opened; a brand-new candidate symbol in the same tick, same no-signal condition, does not).
- Go integration test: with capacity for only 1 more long slot and both a repair-eligible symbol (missing long) and a fresh candidate symbol (also wanting long) competing in the same tick, assert the repair-eligible symbol wins the slot.
- Regression: full `pkg/strategy` + `services/api-gateway` unit+integration suites must stay green — in particular the existing `TestEnsureMatrixStrategies_RespectsStrategyLimits` test (already covers `maxTotal`/`maxLong`/`maxShort` enforcement) must keep passing unmodified, proving the repair pass doesn't loosen those caps.

## Deploy note

Same as every backend change this session: needs a full rebuild + `api-gateway` restart before it's live. After restart, worth checking the three currently-orphaned legs (AKEUSDT long, DEXEUSDT short, VELVETUSDT long) actually get recreated on the next tick that has spare capacity, or manually confirming spare capacity exists / freeing some if the bot is still pinned at its limit.
