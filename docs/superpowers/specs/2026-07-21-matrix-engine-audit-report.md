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

## 3. Matrix bot layer (`services/api-gateway/matrix_engine.go`)

### Where the hypothesis actually holds: paired-close is genuinely shared

`checkMatrixPairedClose` (`matrix_engine.go:190-262`) calls `meetsPairedCloseCriteria` directly (`matrix_engine.go:251`) — the exact same pure predicate `resolveHedgeSlotConflict`'s sibling function in `hedge_engine.go` uses. This is real, working code sharing, not a coincidence of naming. It's evidence the two bot types *can* converge cleanly where the underlying decision (combined PnL/ROI crosses a threshold) doesn't depend on the strategy-layer divergence from Section 1. This is the one place in the whole audit where the user's hypothesis is directly confirmed by the code, not just plausible in theory.

### Where it breaks down: matrix's own conflict handling is markedly less mature than hedge's

`resolveHedgeSlotConflict` (`hedge_engine.go:1065-1147`) is a real conflict-*resolution* system: three configurable modes — wait for the other side's cycle to end, force-close the conflicting position once its loss crosses a threshold, or suspend-and-restore it later. `directionHasLiveStrategy` (`matrix_engine.go:366-386`, fixed earlier today) is, even after today's fix, a bare boolean gate: found a conflict → refuse to open. No resolution modes, no configurability, nothing beyond "don't." Before today's fix it was also *incorrectly scoped* (only checked the calling bot's own strategies, not other bots' — the direct cause of the ARKMUSDT collision between MatrixNova and ST-Fast). Hedge solved this problem properly weeks before matrix needed to; matrix's version was strictly weaker until patched today, and is still strictly less capable than hedge's.

### Self-healing lives here too, and for the same reason as Section 1

`checkMatrixZombieStrategies`/`reviveMatrixSplitBrain` (`matrix_engine.go:53-163`) exist at this layer specifically because generic code elsewhere (`checkPositionGone`, ghost-close handling) assumes a cycle's `ended_at` reflects reality, which Section 1's TP-rearm-in-place model violates. This isn't a second, independent problem — it's the same root cause from Section 1 surfacing again at the orchestration layer, because the orchestration layer has to reconcile DB state written by strategy-layer code that plays by different rules than hedge/grid do.

### Unresolved: the strategy-limit-overshoot diagnostic is still live

`matrix-limit-debug` category logging (`matrix_engine.go:429-431, 558-563`, "TEMP DIAGNOSTIC") is still in the code, still firing — the live incident it was added to catch (a bot exceeding `max_long_strategies`/`max_short_strategies`) was never root-caused this session. `TestEnsureMatrixStrategies_RespectsStrategyLimits` covers the straightforward case but doesn't reproduce whatever race or edge condition caused the actual overshoot. Worth noting this is a separate, still-open thread from everything else in this report.

### Test coverage

`matrix_zombie_test.go` (4 tests) is genuinely solid coverage for the zombie/revive paths. `matrix_strategy_limits_test.go` covers the expected-case limit enforcement but not the still-unexplained overshoot. `matrix_paired_close_test.go` only tests the order-building helper (`matrixLegCloseRequest`), not the close-trigger decision — and the shared `meetsPairedCloseCriteria` predicate it depends on has no test anywhere in the codebase, on either the hedge or matrix side. Today added two new tests for `directionHasLiveStrategy` and `loadOpenDirections`'s cross-bot conflict blocking.

## Synthesis

**The working hypothesis is half right.** At the bot-orchestration layer, matrix and hedge genuinely can and do share logic where the decision doesn't depend on cycle semantics (paired-close condition) — the "matrix is just hedge running both directions" intuition holds there. But at the strategy layer, matrix is not hedge's mechanics reused twice; it's an independently-maintained reimplementation built around a fundamentally different cycle model (TP re-arms the position in place, `ended_at` stays `NULL` indefinitely) versus hedge/grid's (TP closes the cycle, a new one opens explicitly). That single divergence is the direct or indirect cause of most matrix-specific defensive code this audit found: zombie detection, split-brain revival, the weaker conflict-gate that caused today's cross-bot collision, and the attribution fragility fixed earlier this session. None of it is present on the hedge side because hedge's simpler cycle model never creates the conditions that require it.

**Today's largest single loss driver is probably not this divergence at all.** The 3.5-hour tick-loop hang (Section 2) traces to a concrete, non-matrix-specific gap in `botEngineTick`'s concurrent signal computation: two goroutine spawn points with no panic recovery, and a blocking call chain (`ComputeMultiTFState` → `SnapshotOrFetch` → `FetchKlineHistory`) that ignores context entirely, so the 2026-07-07 "protect from hanging" fix doesn't actually cover it. This affects every bot type equally and is unrelated to the matrix-vs-hedge architecture question.

**Recommendation:**
1. **Fix the concurrency gap first, independent of everything else.** Add `defer recoverEngine(...)` to `bot_engine.go:642` and `:655`, and propagate `ctx` through `ComputeMultiTFState`/`SnapshotOrFetch`/`FetchKlineHistory` (or add an explicit timeout at the call site) so the existing 90s protection actually covers this path. This is low-risk, well-understood, not matrix-specific, and very likely the single highest-value fix available given today's loss.
2. **Don't add more self-healing patches to matrix.go before deciding what to do about the cycle-persistence divergence.** Every new zombie/revive-style mechanism is a bet that the current model is permanent; if a future decision changes it, that work is wasted. This decision — keep the in-place TP-rearm model and make the rest of the codebase correctly aware of it everywhere it currently assumes otherwise, versus change matrix's TP fill to close-and-reopen like hedge/grid — is genuinely consequential (the "Накоплено" cumulative-PnL tracking and close-attribution logic were both built specifically to work around the current model, so reversing it is not a small change) and deserves its own dedicated brainstorm with this report as input, not a decision made as a footnote here.
3. **Test the untested core decision logic on both sides before touching either.** `meetsPairedCloseCriteria`, `checkHedgeActivation`/`checkHedgeDeactivation`, and matrix's TP-rearm/reentry state machines are all currently unprotected by any test. Whichever direction the Section-2 decision goes, these need coverage first so a redesign (or further patching) has a regression net under it.
