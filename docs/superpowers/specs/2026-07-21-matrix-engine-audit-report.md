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

## 2. Hedge bot layer (`services/api-gateway/hedge_engine.go`, `pkg/strategy/hedge_support.go`)

### Correction to the audit's own scope assumption

Hedge and matrix bots are **not** run by separate engines. `hedgeEngineTick` (`hedge_engine.go:66-123`) loads every bot with `bot_kind IN ('hedge', 'matrix')` and dispatches by kind in one loop: `case "hedge": processHedgeBot(...)`, `case "matrix": processMatrixBot(...)`. Pure signal bots (ST-Fast, etc.) run on a completely separate loop, `botEngineTick`/`RunBotEngine` (`bot_engine.go`). This matters for what follows.

### Major cross-cutting finding: today's 3.5-hour hang likely isn't a matrix defect at all

`botEngineTick`'s STEP 4 (`bot_engine.go:638-677`, concurrent per-group/per-symbol signal computation) spawns goroutines with **no `defer recoverEngine(...)`** — unlike every other goroutine spawn point in this codebase (`runReactiveProcessor`'s `processBotSymbol` call at `bot_engine.go:1692` does have one; so does every per-bot call inside `hedgeEngineTick`, at `hedge_engine.go:109`). An unrecovered panic in either of these two nested goroutines (lines 642, 655) crashes the entire process — it is *not* caught by `RunBotEngine`'s outer `defer recoverEngine("botEngineTick")`, which only guards the synchronous call, not child goroutines it spawns.

Separately, and more likely given today's symptoms (hang, not crash, until much later): the actual blocking call inside that inner goroutine — `s.signalEngine.ComputeMultiTFState(sym, entry.sigCfgs)` (`bot_engine.go:664`) → `hub.SnapshotOrFetch` (`pkg/signal/hub.go:131`) → `FetchKlineHistory` (`pkg/signal/hub.go:354`) — **takes no `context.Context` at all**, anywhere in that chain. The 90-second `context.WithTimeout` added around the whole tick on 2026-07-07 (commit `814f988`) cannot bound this call; it isn't ctx-aware. `FetchKlineHistory`'s own HTTP client does have a bounded timeout (`proxy.HTTPClient()`, 10–30s), so a single fetch can't hang forever — but nothing prevents a stall elsewhere in that path (e.g. contention on `KlineHub`'s internal mutex from an unrelated stuck goroutine) from blocking every concurrent symbol-goroutine at once, which would block `wg.Wait()` inside the group goroutine, which blocks `groupWg.Wait()` back in `botEngineTick` (`bot_engine.go:677`) — and since `RunBotEngine`'s ticker loop calls `botEngineTick` synchronously, the entire loop stops advancing. The reactive processor is a fully independent goroutine and keeps running off whatever `s.botSnapshot` it last saw before the hang — which is exactly today's observed behavior (ST-Fast kept reacting to signals for hours after being disabled, because the snapshot refresh that would have removed it never ran again).

This was not confirmed with a live goroutine dump (the process was gone by the time pprof was checked — see prior conversation), so it's a strong, evidence-based hypothesis, not a proven root cause. But it locates the *class* of bug in the shared `pkg/signal`/`botEngineTick` plumbing that both `botEngineTick` (signal bots) and, via `processMatrixBot`'s own signal-engine calls, `hedgeEngineTick` depend on — not in matrix's own cycle/level code from Section 1. Today's watchdog (this session) only detects this faster; it doesn't close the gap. The concrete gap is two-fold: (a) missing `defer recoverEngine(...)` on `bot_engine.go:642` and `:655`, (b) no context propagation through `ComputeMultiTFState`/`SnapshotOrFetch`/`FetchKlineHistory`, so the existing 90s-timeout protection doesn't actually cover this path despite looking like it should.

### Self-healing inventory

`hedge_engine.go` and `hedge_support.go` contain zero occurrences of zombie/revive/split-brain/stuck-retry/watchdog-style code — hedge has never needed an equivalent of any of matrix's Section 1 self-healing mechanisms. Consistent with hedge's much lower commit churn (18 vs. 41 commits/60 days) and the fact that hedge's cycles genuinely end when the position closes (no TP-rearm-in-place model), so the generic "cycle ended → no position" assumption elsewhere in the codebase is never violated for it.

### Test coverage

`hedge_engine.go`'s actual decision logic — `checkHedgeActivation`, `checkHedgeDeactivation`, `meetsPairedCloseCriteria`, `resolveHedgeSlotConflict` — has **zero** direct test coverage; grepping for these function names across every `*_test.go` in `services/api-gateway` returns nothing. The one hedge-related test file, `hedge_session_test.go`, covers a read/reporting endpoint (`GetHedgeSession`'s PnL aggregation), not the activation/deactivation decisions themselves.

By contrast, matrix's bot-orchestration layer has four dedicated test files (`matrix_engine_test.go`, `matrix_paired_close_test.go`, `matrix_strategy_limits_test.go`, `matrix_zombie_test.go` — 301 lines total) covering exactly the incidents that produced them. Read charitably, this isn't "matrix is better tested" — it's that matrix has broken in enough distinct, specific ways to have earned incident-driven regression tests for each one, while hedge's core decision logic remains equally untested but simply hasn't been forced to prove it needs coverage yet.
