# Hedge Flip on Main TP — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** When a main strategy closes at TP while a hedge has an open position and the bot is configured with `hedge_tp_main_mode = 'flip'`, immediately promote the hedge to a standalone simple-grid strategy (TP=0.5%, no SL) via the existing `releaseHedgeToGrid` function.

**Architecture:** `closeCycle("tp")` fires an injected callback `OnMainTpClosed` → hedge engine receives the main strategy ID on a buffered channel → `handleMainTpFlip` queries for the active hedge, checks the bot config, and calls the already-existing `releaseHedgeToGrid`. The channel is wired at server startup.

**Tech Stack:** Go 1.22, PostgreSQL/pgx, React 18 + TypeScript, Tailwind CSS

---

## File Map

| File | Change |
|------|--------|
| `pkg/strategy/engine.go` | Add `OnMainTpClosed func(ctx, id string)` field to `Engine` |
| `pkg/strategy/cycle.go` | Call `OnMainTpClosed` at the end of `closeCycle` when `result=="tp"` |
| `services/api-gateway/server.go` | Add `flipChan chan string` to `Server` struct |
| `services/api-gateway/hedge_engine.go` | Listen on `flipChan` in `runHedgeEngine`; add `handleMainTpFlip`; update `applyHedgeMainControls` to skip TP cancellation when `HedgeTpMainMode=="flip"` |
| `services/api-gateway/bot_engine.go` | Add `HedgeTpMainMode string` field to `StrategyConfig` |
| `services/api-gateway/main.go` | Init `flipChan`; call `wireHedgeFlipCallback()` after engine created |
| `frontend/src/features/bots/types.ts` | Add `hedge_tp_main_mode?: 'cancel' \| 'flip'` to `StrategyConfig` |
| `frontend/src/features/bots/components/HedgeBotForm.tsx` | Replace «Отменять ТП на Main» toggle with 2-state «ТП на Main» toggle bound to `hedge_tp_main_mode`; tooltip only on hover |

---

## Task 1: Add `HedgeTpMainMode` to backend StrategyConfig

**Files:**
- Modify: `services/api-gateway/bot_engine.go` (around line 812)

- [ ] **Step 1.1: Add the new field**

Open `services/api-gateway/bot_engine.go`. After line 814 (`HedgeStopMain bool`), add:

```go
// HedgeTpMainMode controls what happens when the main position closes at TP
// while a hedge is active. "cancel" (default) = existing behaviour: TP on main
// is suppressed. "flip" = TP fires normally; on hit the hedge is promoted to main.
HedgeTpMainMode string `json:"hedge_tp_main_mode"` // "" | "cancel" | "flip"
```

- [ ] **Step 1.2: Update `applyHedgeMainControls` to respect the new mode**

In `services/api-gateway/hedge_engine.go`, find `applyHedgeMainControls` (around line 798). It currently gates on `cfg.HedgeCancelMainTp`. Update the derivation at the top of the function so that `flip` mode means TP is NOT suppressed:

```go
// Derive effective cancel flags from HedgeTpMainMode (new field) + legacy booleans.
// "flip" mode: TP on main fires normally — hedge monitors and handles promotion.
hedgeCancelTp := cfg.HedgeCancelMainTp
if cfg.HedgeTpMainMode == "flip" {
    hedgeCancelTp = false
} else if cfg.HedgeTpMainMode == "cancel" {
    hedgeCancelTp = true
}
// Replace all further references to cfg.HedgeCancelMainTp with hedgeCancelTp.
```

Then replace `cfg.HedgeCancelMainTp` with `hedgeCancelTp` in the two lines that use it:
```go
// line ~809
tpSuppressed := hedgeCancelTp || cfg.HedgeStopMain
```

- [ ] **Step 1.3: Build to verify no errors**

```bash
cd c:/Users/123/Projects/sis && go build ./services/api-gateway/...
```

Expected: no output (clean build).

- [ ] **Step 1.4: Commit**

```bash
git add services/api-gateway/bot_engine.go services/api-gateway/hedge_engine.go
git commit -m "feat: add HedgeTpMainMode to StrategyConfig, skip TP suppression in flip mode"
```

---

## Task 2: Add `OnMainTpClosed` callback to strategy Engine

**Files:**
- Modify: `pkg/strategy/engine.go` (Engine struct, line 28)
- Modify: `pkg/strategy/cycle.go` (closeCycle, line ~2900 — end of function)

- [ ] **Step 2.1: Add callback field to Engine**

In `pkg/strategy/engine.go`, inside `type Engine struct { ... }` (line 28), add after `signalEngine`:

```go
// OnMainTpClosed, if set, is called when any strategy closes its cycle at TP.
// The hedge engine injects this at startup to detect flip opportunities.
// Called with the main strategy's ID. Must be non-blocking (uses a buffered channel).
OnMainTpClosed func(ctx context.Context, strategyID string)
```

- [ ] **Step 2.2: Fire callback in closeCycle**

In `pkg/strategy/cycle.go`, at the end of `closeCycle` (line ~2901, just before the closing `}`), add:

```go
// Notify hedge engine when a TP cycle closes — allows immediate flip detection.
if result == "tp" && sr.engine.OnMainTpClosed != nil {
    sr.engine.OnMainTpClosed(ctx, sr.strategy.ID)
}
```

This goes AFTER `go RecordStrategyTrade(...)` and before the closing `}`.

- [ ] **Step 2.3: Build pkg/strategy**

```bash
cd c:/Users/123/Projects/sis && go build ./pkg/strategy/...
```

Expected: no output.

- [ ] **Step 2.4: Commit**

```bash
git add pkg/strategy/engine.go pkg/strategy/cycle.go
git commit -m "feat: fire OnMainTpClosed callback from closeCycle on TP result"
```

---

## Task 3: Add `flipChan` to Server and wire in run loop

**Files:**
- Modify: `services/api-gateway/server.go` (Server struct)
- Modify: `services/api-gateway/hedge_engine.go` (runHedgeEngine + new handleMainTpFlip)
- Modify: `services/api-gateway/main.go` (startup wiring)

- [ ] **Step 3.1: Add `flipChan` to Server struct**

In `services/api-gateway/server.go`, inside `type Server struct { ... }`, after `hedgeTriggerCh chan struct{}` (line 67), add:

```go
// flipChan receives main strategy IDs when their TP cycle closes while a
// hedge is active. Buffered so closeCycle never blocks.
flipChan chan string
```

- [ ] **Step 3.2: Init the channel in NewServer**

In `services/api-gateway/server.go`, inside `NewServer(...)`, after the line that sets `hedgeTriggerCh: make(chan struct{}, 1)`, add:

```go
flipChan: make(chan string, 16),
```

- [ ] **Step 3.3: Listen on flipChan in runHedgeEngine**

In `services/api-gateway/hedge_engine.go`, `runHedgeEngine` currently has a `select` with three cases (ctx.Done, ticker.C, hedgeTriggerCh). Add a fourth case:

```go
case mainID := <-s.flipChan:
    s.handleMainTpFlip(ctx, mainID)
```

The full select becomes:
```go
for {
    select {
    case <-ctx.Done():
        return
    case <-ticker.C:
        lastTick = time.Now()
        s.hedgeEngineTick(ctx)
    case <-s.hedgeTriggerCh:
        if time.Since(lastTick) < 2*time.Second {
            continue
        }
        lastTick = time.Now()
        log.Printf("hedge engine: WS price trigger → немедленная проверка хеджей")
        s.hedgeEngineTick(ctx)
    case mainID := <-s.flipChan:
        s.handleMainTpFlip(ctx, mainID)
    }
}
```

- [ ] **Step 3.4: Implement `handleMainTpFlip`**

Add this function to `services/api-gateway/hedge_engine.go` (after `releaseHedgeToGrid`):

```go
// handleMainTpFlip is called when a main strategy closes at TP and a hedge may
// be eligible for promotion. It finds the active hedge for the given main strategy,
// checks that the bot is configured for flip mode, and calls releaseHedgeToGrid.
func (s *Server) handleMainTpFlip(ctx context.Context, mainStrategyID string) {
    // Find active hedge strategy linked to this main.
    var hedgeID, botID, symbol, direction string
    err := s.pool.QueryRow(ctx,
        `SELECT s.id, s.bot_id, s.symbol, s.direction
         FROM strategies s
         WHERE s.hedged_strategy_id = $1
           AND s.status IN ('active', 'finishing')
         LIMIT 1`,
        mainStrategyID,
    ).Scan(&hedgeID, &botID, &symbol, &direction)
    if err != nil {
        return // no active hedge for this main — nothing to flip
    }

    // Check bot config: flip mode must be explicitly enabled.
    var cfgRaw []byte
    if err := s.pool.QueryRow(ctx,
        `SELECT strategy_config FROM bots WHERE id = $1`, botID,
    ).Scan(&cfgRaw); err != nil {
        return
    }
    var cfg StrategyConfig
    if err := json.Unmarshal(cfgRaw, &cfg); err != nil {
        return
    }
    if cfg.HedgeTpMainMode != "flip" {
        return
    }

    // Guard: hedge must have a filled position in an open cycle.
    if !s.hedgeHasOpenFilledLevels(ctx, hedgeID) {
        return
    }

    // Compute hedge position size and entry price from filled levels in the open cycle.
    // This avoids an extra exchange API call.
    var hedgeSize, hedgeEntry float64
    err = s.pool.QueryRow(ctx,
        `SELECT COALESCE(SUM(sl.filled_qty), 0),
                COALESCE(SUM(sl.filled_qty * sl.fill_price) / NULLIF(SUM(sl.filled_qty), 0), 0)
         FROM strategy_levels sl
         JOIN strategy_cycles sc ON sl.cycle_id = sc.id
         WHERE sc.strategy_id = $1 AND sc.ended_at IS NULL AND sl.status = 'filled'`,
        hedgeID,
    ).Scan(&hedgeSize, &hedgeEntry)
    if err != nil || hedgeSize == 0 {
        log.Printf("handleMainTpFlip: не удалось получить объём хеджа %s: %v", hedgeID[:8], err)
        return
    }

    hedgePos := hedgePosInfo{
        Size:       hedgeSize,
        EntryPrice: hedgeEntry,
        Direction:  direction,
    }

    s.releaseHedgeToGrid(ctx, botID, hedgeID, symbol, hedgePos,
        fmt.Sprintf("переворот: мейн %s закрылся по TP", mainStrategyID[:8]))
}
```

- [ ] **Step 3.5: Wire the callback in main.go**

In `services/api-gateway/main.go`, find the place where the strategy engine is configured (after `s.engine = strategy.New(...)` or where `s.engine.SetSignalEngine(...)` is called). Add:

```go
// Wire hedge flip: when a main strategy closes at TP, notify the hedge engine.
s.engine.OnMainTpClosed = func(ctx context.Context, strategyID string) {
    select {
    case s.flipChan <- strategyID:
    default: // channel full — will be picked up on next tick
    }
}
```

- [ ] **Step 3.6: Build**

```bash
cd c:/Users/123/Projects/sis && go build ./services/api-gateway/...
```

Expected: no output.

- [ ] **Step 3.7: Commit**

```bash
git add services/api-gateway/server.go services/api-gateway/hedge_engine.go services/api-gateway/main.go
git commit -m "feat: handleMainTpFlip — promote hedge to main when main closes at TP (flip mode)"
```

---

## Task 4: Frontend — add `hedge_tp_main_mode` to types.ts

**Files:**
- Modify: `frontend/src/features/bots/types.ts` (around line 83)

- [ ] **Step 4.1: Add the new field**

In `frontend/src/features/bots/types.ts`, after the `hedge_cancel_main_tp` line (~line 83), add:

```ts
hedge_tp_main_mode?: 'cancel' | 'flip'; // "cancel" = suppress TP on main; "flip" = promote hedge to main on TP
```

- [ ] **Step 4.2: Commit**

```bash
git add frontend/src/features/bots/types.ts
git commit -m "feat: add hedge_tp_main_mode to frontend StrategyConfig type"
```

---

## Task 5: Frontend — replace toggle in HedgeBotForm.tsx

**Files:**
- Modify: `frontend/src/features/bots/components/HedgeBotForm.tsx` (around line 1511–1521)

- [ ] **Step 5.1: Replace the «Отменять ТП на Main» control**

Find this block (lines ~1511–1521):

```tsx
<div>
  <label className={labelCls}>
    Отменять ТП на Main
    <Tip text="При активации хеджа — отменяет все ТП-ордера основного бота." />
  </label>
  <Toggle
    options={[{ label: 'Выкл', value: 'false' }, { label: 'Вкл', value: 'true' }]}
    value={String(config.hedge_cancel_main_tp ?? false)}
    onChange={v => patch({ hedge_cancel_main_tp: v === 'true' })}
    optionColors={{ true: 'bg-rose-700 text-white' }}
  />
</div>
```

Replace with:

```tsx
<div>
  <label className={labelCls}>
    ТП на Main
  </label>
  <div className="grid grid-cols-2 gap-1">
    <div className="relative group/tip flex items-center gap-1">
      <button
        type="button"
        onClick={() => patch({ hedge_tp_main_mode: 'cancel', hedge_cancel_main_tp: true })}
        className={`flex-1 py-[7px] rounded-lg border text-[11px] font-semibold transition-colors ${
          (config.hedge_tp_main_mode ?? 'cancel') === 'cancel'
            ? 'border-[#2563eb] bg-[#1d4ed8] text-white'
            : 'border-white/[.08] bg-transparent text-slate-500 hover:text-slate-300'
        }`}
      >
        Отменять
      </button>
      <div className="pointer-events-none absolute bottom-full left-1/2 -translate-x-1/2 mb-2 z-50 w-[200px] hidden group-hover/tip:block">
        <div className="bg-[#1e293b] border border-[#334155] rounded-lg px-2.5 py-2 text-[10px] text-slate-300 leading-relaxed shadow-xl">
          ТП мейна снимается, пока хедж активен. Бот ждёт парного закрытия в направлении хеджа.
        </div>
      </div>
    </div>
    <div className="relative group/tip2 flex items-center gap-1">
      <button
        type="button"
        onClick={() => patch({ hedge_tp_main_mode: 'flip', hedge_cancel_main_tp: false })}
        className={`flex-1 py-[7px] rounded-lg border text-[11px] font-semibold transition-colors ${
          config.hedge_tp_main_mode === 'flip'
            ? 'border-amber-500/60 bg-amber-950/30 text-amber-300'
            : 'border-white/[.08] bg-transparent text-slate-500 hover:text-slate-300'
        }`}
      >
        🔄 Переворот
      </button>
      <div className="pointer-events-none absolute bottom-full right-0 mb-2 z-50 w-[220px] hidden group-hover/tip2:block">
        <div className="bg-[#1e293b] border border-[#334155] rounded-lg px-2.5 py-2 text-[10px] text-slate-300 leading-relaxed shadow-xl">
          ТП мейна не снимается. При срабатывании — хедж-позиция становится новым мейном, выставляется TP +0.5%, без SL. Бот продолжает мониторинг.
        </div>
      </div>
    </div>
  </div>
</div>
```

Note: we also set `hedge_cancel_main_tp` in `patch` so the backend receives both the new `hedge_tp_main_mode` field and the legacy `hedge_cancel_main_tp` correctly.

- [ ] **Step 5.2: TypeScript check**

```bash
cd c:/Users/123/Projects/sis/frontend && npx tsc --noEmit
```

Expected: no errors.

- [ ] **Step 5.3: Commit**

```bash
git add frontend/src/features/bots/components/HedgeBotForm.tsx
git commit -m "feat: replace Отменять ТП toggle with 2-state ТП на Main (Отменять/Переворот)"
```

---

## Task 6: Check `hedgePosInfo` struct has `Direction` field

**Files:**
- Read: `services/api-gateway/hedge_engine.go`

- [ ] **Step 6.1: Verify hedgePosInfo struct**

Search for `hedgePosInfo` in `hedge_engine.go`:

```bash
grep -n "type hedgePosInfo\|hedgePosInfo {" c:/Users/123/Projects/sis/services/api-gateway/hedge_engine.go
```

If `Direction` field doesn't exist, either remove it from the `handleMainTpFlip` struct literal (it's only used in logging, not needed by `releaseHedgeToGrid`), or add it:

`releaseHedgeToGrid` signature is:
```go
func (s *Server) releaseHedgeToGrid(ctx context.Context, botID, strategyID, symbol string, hedgePos hedgePosInfo, reason string)
```

It uses `hedgePos.Size` and `hedgePos.EntryPrice`. If `hedgePosInfo` has no `Direction` field, remove it from the struct literal in `handleMainTpFlip`:

```go
hedgePos := hedgePosInfo{
    Size:       hedgeSize,
    EntryPrice: hedgeEntry,
}
```

- [ ] **Step 6.2: Build to confirm clean**

```bash
cd c:/Users/123/Projects/sis && go build ./services/api-gateway/...
```

Expected: no errors.

- [ ] **Step 6.3: Commit if changed**

```bash
git add services/api-gateway/hedge_engine.go
git commit -m "fix: remove Direction from hedgePosInfo literal in handleMainTpFlip"
```

---

## Task 7: Push and deploy

- [ ] **Step 7.1: Push to remote**

```bash
cd c:/Users/123/Projects/sis && git push origin master
```

- [ ] **Step 7.2: Deploy to server**

On the server:
```bash
cd /opt/sis
git pull
docker compose build api-gateway
docker compose up -d api-gateway
cd frontend && npm run build
```

- [ ] **Step 7.3: Manual smoke test**

1. Create or edit a hedge bot → Стратегия → Управление мейн → select **🔄 Переворот** → Save
2. Start a main strategy, wait for hedge to activate
3. Manually trigger a TP on the main (or set a very close TP)
4. Verify in strategy list: main stops, hedge becomes a standalone simple-grid strategy with TP=0.5%
5. Verify hedge bot still shows in bot monitoring (picks up the promoted strategy as new main on next tick)
