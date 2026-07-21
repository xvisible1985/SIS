# Matrix Cycle Lifecycle Redesign — Design

> Follow-up to [2026-07-21-matrix-engine-audit-report.md](2026-07-21-matrix-engine-audit-report.md), recommendation #2: decide whether matrix keeps its in-place TP-rearm cycle model or switches to close-and-reopen like hedge/grid. Decided via dialogue with the user; this document records the decision and its concrete shape.

**Goal:** Matrix strategies close their cycle whenever the position fully flattens (position → 0), exactly like hedge/grid, instead of re-arming the same cycle in place forever. Remove the root cause behind most of this session's matrix-specific defensive code (zombie detection, split-brain revival, close-attribution fragility) rather than continuing to patch its symptoms.

**Architecture:** Matrix's fill handlers stop being a fully separate parallel implementation and route through the same `closeCycle`/`handleTPFill`/`handleSLFill` functions hedge/grid strategies already use, with matrix-specific per-level bookkeeping (per-level SL cancellation, reentry-state handling) running as conditional steps inside those shared functions before the final close decision — not as a wholesale `if StrategyType == "matrix"` delegation to an entirely separate code path.

**Tech Stack:** Go (`pkg/strategy`, `services/api-gateway`), Postgres (new `hedge_sessions.accumulated_pnl` column), React/TypeScript frontend for the paired-close progress indicator.

---

## 1. Strategy-layer cycle lifecycle

### Cycle-end condition

A matrix cycle ends when the position fully flattens to zero — not on every TP/SL event individually:

- **Global TP fill** always flattens the whole position (by construction, it closes every filled level at once) → always ends the cycle.
- **A per-level SL fill** ends the cycle *only if it was the last remaining filled level* (position → 0 after this fill). If other levels are still filled, the cycle stays open and the fired level enters the existing "safe zone" reentry queue (`matrixCheckWaitingReentry`, `matrixReenterRelativeToRef`, etc. — unchanged) exactly as it does today.

### Implementation shape

`handleTPFill` and `handleSLFill` (`cycle.go`) lose their unconditional `if sr.strategy.StrategyType == "matrix" { handleMatrixX(...); return }` delegation. Instead:

- `handleTPFill`: for matrix, run the existing pre-close bookkeeping (persist the fill's realized PnL contribution — see Section 2 — cancel per-level SLs, clear waiting-reentry state) and then fall through to the same `closeCycle(ctx, "tp")` + `maybeRestart(ctx)` hedge/grid already calls. A matrix TP fill is unconditionally a full flatten, so this path always closes.
- `handleSLFill`: for matrix, run the equivalent per-level bookkeeping (persist this level's realized PnL — Section 2 — record the level as `sl_closed`), then check whether the position is now fully flat. If yes, fall through to `closeCycle(ctx, "sl")` + `maybeRestart(ctx)`. If no, do *not* close — hand off to the existing reentry-queue logic exactly as `handleMatrixSLFill` does today, and return without touching the cycle.

`handleMatrixTPFill`/`handleMatrixSLFill` as standalone functions go away; their non-closing bookkeeping steps move inline into the shared handlers, gated on `StrategyType == "matrix"` where genuinely matrix-specific (per-level SL cancellation, reentry state), not gated at all where the logic already generalizes (the final `closeCycle` call).

### Restart timing

After `closeCycle`, `maybeRestart` decides whether/how a new cycle opens — for matrix this happens **immediately**, in the same code path, matching hedge/grid exactly. No bot-tick-driven delay, so a paired leg never sits open alone waiting for its partner to reopen.

### No migration needed

Currently-open matrix cycles (`ended_at IS NULL`, having already re-armed many times under the old model) need no special handling — their next TP or SL fill closes them correctly under the new logic and the account continues trading normally from there.

## 2. Bot-level accumulated PnL (`hedge_sessions.accumulated_pnl`)

### Why not compute it live from `trade_history`

`GetHedgeSession`'s existing cumulative-PnL query (`strategy_handler.go:1509`) reconstructs a leg's historical PnL after the fact by unioning `trade_history`, `strategy_levels.realized_pnl`, and `matrix_tp_profits`. This inherits every close-attribution bug this session has fought (trades still landing as "manual," unreliable bot attribution) — not acceptable as the input to a real-money paired-close trigger.

### Design

New column: `hedge_sessions.accumulated_pnl NUMERIC NOT NULL DEFAULT 0`.

Incremented **at the moment PnL is realized**, inside the same authoritative code path that already knows the exact strategy/bot/session unambiguously — no reconstruction from `trade_history` later:

- On a per-level SL fill for a matrix leg (whether or not it ends the cycle) — add that level's realized PnL.
- On a full cycle close (`closeCycle`, any strategy type, any reason) — add the cycle's realized PnL.

Each increment looks up the currently-open (`ended_at IS NULL`) `hedge_sessions` row for the strategy's `bot_id` (matching on `main_strategy_id` or `hedge_strategy_id`, whichever this leg is) and adds to `accumulated_pnl`.

**Reset:** a new `hedge_sessions` row starts at `accumulated_pnl = 0` by default (`DEFAULT 0`), and a fresh row is created after a `paired_close` ends the previous one (the existing session-creation logic in `checkMatrixPairedClose` already handles this — no new code needed there beyond adding the column).

### `GetHedgeSession` simplification

Replace the 3-way `trade_history`/`strategy_levels`/`matrix_tp_profits` UNION with a direct read of `hedge_sessions.accumulated_pnl` for the requested leg's session. Same reset semantics (scoped to the current open session), much simpler query, and no dependency on attribution-fragile historical tables.

### `matrix_tp_profits` retirement

Stop calling `RecordMatrixTPProfit` — matrix TP fills now land in `trade_history` via the normal `closeCycle` path, same as hedge, making the special-case table redundant for new data. The table and its existing rows stay in the database for historical reference; nothing reads from it going forward.

## 3. Paired-close trigger using accumulated PnL

Three existing modes (`cfg.HedgeDeactCloseType`), unpacked in `meetsPairedCloseCriteria`:

- **0 (combined PnL$)** — unchanged: live `mainPos.UnrealisedPnl + hPos.UnrealisedPnl` vs. `cfg.HedgeDeactCloseValue`.
- **1 (combined ROI%)** — unchanged: live combined PnL over combined margin vs. threshold.
- **2 (breakeven)** — **changes**: compare `hedge_sessions.accumulated_pnl` (this session's realized history for both legs) **plus** the current live combined unrealized PnL, against `cfg.HedgeBreakevenProfit`. This replaces today's live-only comparison, which ignored all historical realized PnL and fees.

## 4. UI: paired-close progress indicator

New element in the matrix-pair card (the currently-empty area next to "Накоплено матрикс" in the stats panel) — a horizontal progress bar plus the two driving numbers, showing the *active* mode's progress toward its threshold:

```
Безубыток · 52% до порога
┌──────────────────────────────┐
│███████████░░░░░░░░░░░░░░░░░░░│
└──────────────────────────────┘
+$5.18                   $10.00
```

- Label: the active mode's name (PnL$ / ROI% / Безубыток) — read from `cfg.HedgeDeactCloseType`.
- Bar fill: `current / threshold` clamped to [0, 100]%, using whichever `current` calculation matches the active mode (mode 2 uses accumulated + live; modes 0/1 use live-only, matching Section 3).
- Numbers: current value and threshold, in the mode's native unit ($ or %).

Backend: extend `GetHedgeSession`'s response with the fields the frontend needs to render this without a second round trip — mode, threshold, and the live-combined component (accumulated_pnl is already in the response). Exact field shape is an implementation-plan detail, not a design decision.

## 5. Explicitly deferred (tracked as debt, not done now)

- **Zombie-strategy detection / split-brain revival** (`checkMatrixZombieStrategies`, `reviveMatrixSplitBrain`) stay in place as a safety net. Their original trigger condition (cycle never ending) goes away with this redesign, but other failure paths (`ghost_close`, WS reconnect races) could still leave a cycle in a bad state. Remove only once confirmed they no longer fire in practice — not part of this change.
- Whether the `orderLinkId` attribution fixes added earlier this session (`-scl-`, `-tplt-` suffixes) are still necessary once matrix closes route through the same path hedge already uses correctly — needs verification during implementation, not a design decision.
- The still-unresolved strategy-limit-overshoot bug (`matrix-limit-debug` diagnostic logging, still live) is a separate, open thread — this redesign may incidentally affect its conditions, but re-investigating it is out of scope here.
