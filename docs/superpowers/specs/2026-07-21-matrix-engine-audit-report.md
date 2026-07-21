# Matrix Engine Audit — Report

See [2026-07-21-matrix-engine-audit-design.md](2026-07-21-matrix-engine-audit-design.md) for methodology and criteria.

## 1. Strategy layer (`pkg/strategy`)

### Finding: matrix does not share hedge/grid's fill-handling logic — it only shares the dispatch function name

Every fill-handling entry point (`handleLevelFill`, `handleTPFill`, `handleSLFill` in `cycle.go`) branches at the very top:

```go
// cycle.go:2233-2237 (handleLevelFill)
if sr.strategy.StrategyType == "matrix" {
    sr.handleMatrixLevelFill(ctx, levelID, filledPrice)
    return
}
```

Same pattern at `cycle.go:2754` (`handleTPFill` → `handleMatrixTPFill`) and via `engine.go:1213` (→ `handleMatrixSLFill`). Past that branch, matrix never touches `updateTP`, `updateSL`, `placeNextLevels`, or `closeCycle` — the functions hedge/grid strategies actually run through. Matrix has its own complete parallel implementation (`handleMatrixLevelFill`, `matrixUpdateTP`, `handleMatrixSLFill`, `handleMatrixTPFill` — all in `matrix.go`) that shares only the `StrategyRunner`/`GridLevel`/`Cycle` data structures with hedge, not the behavior.

This directly contradicts the working hypothesis ("matrix ≈ hedge, just both directions") at the structural level: matrix isn't hedge's cycle machinery running twice — it's a separately-maintained reimplementation that happens to store its state in the same tables.

### Root structural divergence: matrix's global TP never ends the cycle

`handleTPFill` (hedge/grid, `cycle.go:2792-2793`):
```go
sr.closeCycle(ctx, "tp") // cancels SL if present
sr.maybeRestart(ctx)
```
One TP = one closed cycle = an explicit decision (`maybeRestart`) about whether to open a new one. `strategy_cycles.ended_at` gets set every time.

`handleMatrixTPFill` (`matrix.go:1884-1985`) does the opposite: an 8-step **in-place reset** — cancel per-level SLs, reset filled/sl_closed levels back to `pending`, clear the TP order ref, re-anchor `sr.cycle.StartPrice` at the fill price, bump `repriceGen`, re-place all levels. `sr.cycle` is never touched via `closeCycle`; `ended_at` stays `NULL` through every re-arm. A single matrix cycle can span dozens of TP fills over its lifetime.

This is almost certainly the taproot of most matrix-specific bugs surfaced this session:
- **Zombie-strategy detection & split-brain revival** (bot layer, `checkMatrixZombieStrategies`/`reviveMatrixSplitBrain`) exist because other generic code (`checkPositionGone`, ghost-close paths) assumes "cycle ended → no position," which matrix's persistent-cycle model violates whenever those generic paths fire on it.
- **Close attribution difficulty** (`ClosedPnlSyncer`'s fuzzy step-3 heuristic matching "the currently open cycle for symbol+direction") is dangerous specifically because a matrix cycle can still be "the current open cycle" many re-arms and hours after its first TP — a plain grid/hedge cycle wouldn't still be open.
- **Strategy-limit-overshoot investigation** (still unresolved, `matrix-limit-debug` diagnostic logging still live) — any counting logic that infers "is this strategy actively cycling" from cycle state is working against a model where the cycle is nominally the same one for a very long time.

### Secondary complexity: a ~600-line reentry system with no hedge equivalent

`matrix.go:1998-2659` — `matrixFindDeepestActiveRef`, `matrixReentryConditionMet`, `matrixCheckWaitingReentry`, `matrixReenterRelativeToRef`, `matrixReenterAtConfigPrice`, `matrixReenterL0AtMarket`, `matrixReenterFromL0`, `matrixRebuildFromSZLow`, `restoreMatrixWaitingSlots` — a "safe zone" system that lets an individual level, once stopped out, wait and re-enter later once price moves favorably again relative to a neighboring filled level. `cycle.go`'s non-matrix functions have no equivalent concept; hedge/grid's `handleSLFill` just closes the cycle. This looks like a genuine feature requirement, not an accidental patch — but it's substantial unique state-machine complexity matrix carries alone, on top of the cycle-persistence divergence above.

### Test coverage

Zero automated coverage for the mechanisms above: `handleMatrixTPFill`, `handleMatrixSLFill`, `matrixCheckWaitingReentry`, `matrixRebuildFromSZLow`, `matrixReenter*` don't appear in any `_test.go` file. Existing matrix-adjacent tests in `pkg/strategy` (`matrix_adopt_test.go`, `mark_level_placed_test.go`, `cycle_reload_test.go`, `matrix_relative_test.go`) cover only peripheral pieces (adopt-branch nil check, a single DB write, cycle-reload self-heal detection, and this session's own relative-slot pure functions) — none of them touch the TP-rearm or reentry state machines that are the actual heart of `matrix.go` (~1,900 of its 2,697 lines).

### This session's strategy-layer patches

Narrower than it felt in the moment — most of this session's defensive additions actually live in the bot layer (see below), not here:
- `matrixRetryStuckRelativeSlot` (2026-07-17 live incident — a stuck relative slot retry)
- Today's forced-Market-order fix for relative slots (compensates for the reactive trigger-then-place design being fragile on price gaps — itself a symptom of relative-slots being bolted onto the fill-based model above, not evidence of a new unrelated bug)
- `orderLinkId` attribution fixes (`-scl-`, `-tplt-` suffixes) — needed specifically because the cycle-persistence divergence above breaks the generic close-attribution heuristic
