# Matrix Engine Audit — Design

**Goal:** Determine whether the matrix bot's recurring problems (2+ weeks of live incidents, 30% of deposit lost 2026-07-21) come from a fundamentally fragile design, or from a series of independent bugs — before deciding whether to keep patching or to redesign. This is an investigation, not a feature build: the deliverable is a written audit report, not code.

## Why this instead of another patch

This session alone added five defensive/self-healing mechanisms to the matrix engine (zombie-strategy detection, split-brain cycle revival, stuck-relative-slot retry, forced-market-order fallback, a tick-loop watchdog) on top of an already-existing "protect engines from hanging" fix from 2026-07-07 (`recoverEngine` + 90s per-tick context timeout). Despite that prior protection, today's tick loop still hung for 3.5 hours with zero recovery, and the watchdog added today only detects that class of failure faster — it does not explain or fix why the 2026-07-07 protection didn't catch it.

Quantitative signal: over the last 60 days, matrix-related files received 41 commits vs. 18 for hedge-related files, despite hedge existing for roughly the same amount of time (hedge's core dates to 2026-05-07, matrix.go to 2026-05-22). Matrix-specific code also carries far more of its own bespoke logic — `matrix.go` + `matrix_relative_engine.go` + `matrix_relative.go` total ~3,168 lines, versus `hedge_support.go`'s 120 lines (hedge leans on the shared `cycle.go` core for most of its behavior; matrix has grown a large parallel implementation instead).

The user's working hypothesis: matrix strategy mechanics are inherited from — and should behave like — the hedge bot's (which is comparatively stable), with the only real difference being that a matrix bot runs both directions (long and short) in parallel through paired matrix strategies, using bot-level controls (paired close, etc.) similar to hedge's. This audit exists to verify or falsify that hypothesis with evidence, not assumption.

## Scope

Three layers, audited in this order:

1. **Strategy layer** (`pkg/strategy/matrix.go`, `matrix_relative_engine.go`, `matrix_relative.go`, plus the shared `cycle.go`/`engine.go` code both matrix and hedge strategies run through) — a single matrix leg's lifecycle in isolation: cycle open, level fill, TP/SL, re-arm, close. Not paired-close or bot-level orchestration.
2. **Hedge bot layer** (`services/api-gateway/hedge_engine.go`, `pkg/strategy/hedge_support.go`) — the comparatively mature, lower-churn bot orchestration this hypothesis says matrix should resemble.
3. **Matrix bot layer** (`services/api-gateway/matrix_engine.go`) — bot-level orchestration across both directions: paired-close, per-bot strategy limits, zombie/revival logic, cross-bot conflict checks.

Out of scope: deciding the fix (patch vs. redesign) — that is a follow-up brainstorm once the audit's findings are in. Out of scope: pausing live trading — the user has decided matrix bots keep trading in parallel while this audit runs.

## Method

No subagents — the auditor (me) carries this session's live-incident context (ARKM cross-bot collision, HEMI stuck limit, strategy-limit overshoot, the 3.5h tick-loop hang) and the exact rationale behind every patch made this session; delegating would mean reconstructing that from git history alone and losing the "why."

For each of the three layers, evaluate against three criteria:

1. **Duplicated/divergent logic** — does matrix implement its own version of something `cycle.go` already does for hedge (closing a cycle, handling a TP/SL fill, re-arming), and if so, does it behave the same way? Divergent reimplementations of the same concept are a structural risk independent of any single bug.
2. **This session's patches as a checklist** — for each defensive/self-healing mechanism added recently (zombie detection, split-brain revival, stuck-slot retry, forced-market-order, diagnostic logging, tick watchdog): does it compensate for a design gap specific to matrix, or is it a generic resilience feature hedge also needs but lacks? If hedge doesn't need an equivalent and isn't missing one, that's evidence matrix's own design is what's generating the failure modes.
3. **Test coverage** — for the core invariants (cycle lifecycle, paired-close conditions, strategy limits, close attribution), is there an automated test pinning today's actual behavior, separately at the strategy level (one leg) and the bot level (orchestration)? Note explicitly which of today's live incidents had zero test coverage.

## Deliverable

Written report at `docs/superpowers/specs/2026-07-21-matrix-engine-audit-report.md` (a second file, since this document is the audit's own design — the report is its output), structured as:

- **Strategy layer** — findings against the three criteria, with evidence (file:line, commit hash, date).
- **Hedge bot layer** — same, as the comparison baseline.
- **Matrix bot layer** — same.
- **Synthesis** — does the hypothesis hold (matrix ≈ hedge + parallel directions)? Where does it break down? A recommendation (continue patching vs. architectural rework) with the reasoning that supports it — not a decision made here, but the evidence a follow-up brainstorm will decide from.

## After this audit

The synthesis section's recommendation becomes the input to a *separate* brainstorm about what to actually change. This spec's job ends at "here is what we found and why it matters" — it does not itself propose or implement a fix.
