# Binance live trading — design

## Context

`sis` currently trades live on Bybit only. The exchange-agnostic pieces that exist
today — `pkg/exchange.Client` (candle/backtest data) — already have a working Binance
implementation, but that interface only covers historical/streaming OHLCV for the
signal-engine backtester. It is never referenced from `pkg/strategy` or
`services/api-gateway`.

Live trading (order placement, positions, closed-pnl, live price monitoring for
TP/SL/trailing/matrix) has no abstraction at all: `pkg/trader` is a de facto Bybit SDK
called by name (`trader.PlaceOrder`, `trader.FetchPositions`, ...) from ~11 files across
`pkg/strategy` and `services/api-gateway`, and its types (`OrderRequest`, `Position`,
`ClosedPnl`) mirror Bybit's exact wire format (`positionIdx`, `category`,
`orderLinkId`). Live price monitoring (`pkg/signal.TickerHub`) is a second, entirely
separate Bybit-only dependency: it dials `wss://stream.bybit.com` directly and is keyed
by bare symbol, shared market-wide across every strategy that needs a price tick
(matrix TP/SL monitoring, trailing stops). `exchange_accounts.exchange` already permits
`'binance'` in its DB CHECK constraint, and the frontend account-creation UI already
has Binance's name/color/icon wired up — gated behind `supported: false` — but neither
side does anything with a Binance account today; connecting one would produce a
strategy that can never place an order or see a price tick.

This spec covers the first real exchange added to the live-trading path: **Binance**,
alongside the already-working Bybit integration. OKX and Bitget are explicitly
out of scope — decomposed into their own future spec/plan cycles once this phase
proves the abstraction holds up against a second exchange with a genuinely
different API shape (Binance has no WS order-placement channel, unlike Bybit).

## Goals

- Grid, Matrix, and Hedge/Мультибот strategies all work on a Binance account exactly as
  they do on a Bybit account today — same UI, same bot lifecycle, same trade_history/
  attribution guarantees — with no `pkg/strategy` business logic aware of which
  exchange it's running against.
- Binance USDⓈ-M Futures (perpetuals) — the direct analog of the `category=linear`
  Bybit trading the platform already does. Spot and COIN-M are out of scope.
- The existing Bybit trading path is not functionally changed (only reorganized behind
  the new interface) — zero regression risk to the currently-live, real-money Bybit
  path is a harder constraint than shipping Binance fast.

## Non-goals

- OKX, Bitget, or any exchange beyond Binance (future, separate specs).
- Binance Spot or COIN-M Futures.
- A Binance testnet integration — validation happens directly on the user's own live
  Binance account with small capital (explicit user decision; see Testing below).
- Per-user gating/feature-flagging of Binance account creation — once the user's own
  live validation passes, it's available to every platform user with no rollout gate
  (explicit user decision).
- Rewriting Bybit's wire-format-shaped types into a "purer" exchange-neutral vocabulary
  (Approach 2, rejected — see Approaches Considered). The existing `OrderRequest`/
  `Position`/`ClosedPnl` shapes remain the canonical internal representation; only
  Binance translates into them.

## Approaches considered

1. **(Chosen) Unify around the existing Bybit-shaped internal types; introduce a
   `trader.Exchange` interface; Binance gets one implementation that translates into
   the existing types.** `pkg/strategy` business logic is untouched — it keeps reading
   `OrderRequest.PositionIdx`, `Position.Category`, etc., unaware of which exchange
   produced them. Cost: the canonical types keep Bybit-flavored field names even though
   they're now genuinely exchange-agnostic in effect (cosmetic only).
2. **Fully exchange-neutral type system from scratch, migrate `pkg/strategy` onto it.**
   Architecturally cleaner long-term, but touches the currently-live, real-money Bybit
   trading path for a purely cosmetic win, at the same time as introducing a brand-new,
   testnet-unvalidated second exchange. Rejected: unacceptable risk stacking given the
   "validate live, no testnet" testing constraint below.
3. **Fully separate, non-shared code paths per exchange (duplicate the strategy
   engine's exchange-touching logic for Binance).** No interface design needed, but
   every future grid/matrix/hedge bugfix has to be applied twice, and the two paths
   will drift. Rejected given the user wants full Grid+Matrix+Hedge parity from day
   one — duplicating that much logic is a larger, worse-shaped effort than building
   the interface once.

## Architecture

### 1. Trading interface (`pkg/trader.Exchange`)

A new interface covering exactly the operations the ~11 existing call sites use today
(exact method list to be finalized against those call sites during planning — this is
the shape, not the final signature set):

```go
type Exchange interface {
    PlaceOrder(ctx, OrderRequest) (OrderResult, error)
    PlaceOrderBatch(ctx, BatchPlaceRequest) ([]BatchPlaceResult, error)
    CancelOrder(ctx, CancelRequest) error
    CancelOrderBatch(ctx, BatchCancelRequest) error
    FetchPositions(ctx, category, symbol string) ([]Position, error)
    FetchClosedPnl(ctx, category, symbol string, ...) ([]ClosedPnl, error)
    GetWalletBalance(ctx) (equity, available float64, err error)
    SwitchPositionMode(ctx, hedgeMode bool) error
    SetLeverage(ctx, symbol string, lev int) error
    GetMarkPrice(ctx, category, symbol string) (float64, error)
    // + instrument constraints (tick size / min notional / lot size) — see §3
}
```

The existing `pkg/trader/bybit.go` functions and `TradeStream` are wrapped into a
`bybit.Exchange{}` implementation — a reorganization, not a rewrite; the actual HTTP/WS
calls and their behavior are unchanged. A new `pkg/trader/binance/` package implements
the same interface against Binance USDⓈ-M Futures: REST for order placement/cancel
(Binance has no WS order-placement channel the way Bybit does — this is hidden inside
the implementation, invisible to callers) and the private user-data-stream WS for
order/position/execution updates (Binance's analog of Bybit's private WS).

`AccountRunner` (already a per-account construct) resolves the right `Exchange`
implementation once, at construction time, from `account.Exchange`. The ~11 call
sites move from free-function calls (`trader.PlaceOrder(ctx, creds, req)`) to the
resolved interface (`sr.runner.exchange.PlaceOrder(ctx, req)`) — mechanical, but
touches every file in that list.

Binance's hedge mode (`dualSidePosition` + `positionSide LONG/SHORT`) maps directly
onto the existing `PositionIdx` 1/2 convention Bybit already uses — no new concept
needed in `pkg/strategy`.

### 2. Price feed dispatch (`pkg/signal.TickerHub`)

Live price monitoring for TP/SL/trailing/matrix does not go through `pkg/exchange` at
all today — it's a separate dependency chain: `pkg/signal.TickerHub` (WS, keyed by bare
symbol, one shared Bybit connection pool) with a `trader.FetchMarkPrice` REST fallback,
plus `AccountRunner.posAvgEntry` (average-entry-price cache, filled reactively from
Bybit's private position-stream WS — the "ТВХ биржи (WS)" logs).

Changes:
- `TickerHub`'s cache/subscription key changes from `symbol` to `(exchange, symbol)` —
  the same symbol string prices differently on each exchange. Internally it manages two
  independent WS connection pools (existing Bybit pool + new Binance
  `wss://fstream.binance.com` pool for `@markPrice`), each started lazily on first
  subscription.
- `Engine.PriceHub().Subscribe(...)` takes an exchange parameter. `matrix.go`'s
  `launchMatrixPriceMonitor` supplies it from the subscribing strategy's own
  `account.Exchange` — already available, nothing new to plumb in.
- The REST fallback (`runMatrixPriceMonitorPolling`) moves from bare
  `trader.FetchMarkPrice` to `Exchange.GetMarkPrice` — same dispatch pattern as
  orders.
- `posAvgEntry` needs no new dispatch layer — it's already populated per-`AccountRunner`,
  and an account trades exactly one exchange. Binance accounts wire the same
  `OnPositionEvent` callback to Binance's private user-data-stream WS instead of
  Bybit's, writing into the same cache structure.

No caller in `pkg/strategy` needs to know which exchange produced a given price tick or
avg-entry value — the dispatch lives entirely at the `AccountRunner`/`TickerHub` layer,
mirroring how order dispatch works.

### 3. Order/orderLinkId mapping and risks

The system embeds a custom `orderLinkId` on every order it places
(`SIS_STR-{id8}-{kind}-{cycle}-{seq}`, sometimes longer — see `pkg/strategy/linkid.go`
and construction sites in `cycle.go`/`matrix.go`) so `ClosedPnlSyncer` can classify a
closed-pnl event's origin (grid TP/SL, matrix TP re-arm, per-level matrix SL,
self-close) without guessing from timing.

**Open risk, resolved during implementation, not here:** Binance Futures'
`newClientOrderId` is capped at 36 characters (letters/digits/`-`/`_` — our existing
charset already fits). Typical observed length is ~25-30 chars, but a strategy with a
high cycle number (live examples exist in the 200s-300s) plus a multi-digit
`repriceGen` can approach or exceed the cap. Needs a concrete fix during
implementation — e.g. a shorter (6-hex) strategy-id prefix for Binance-bound orders, or
a dedicated compact encoding — verified against Binance's real validation behavior, not
assumed from documentation.

Order-type mapping: Bybit's conditional orders use `orderType` + `triggerPrice` +
`orderFilter=StopOrder`; Binance Futures uses dedicated `STOP_MARKET`/
`TAKE_PROFIT_MARKET` types with `stopPrice` + `closePosition`/`reduceOnly` flags. This
translation is entirely inside the Binance `Exchange.PlaceOrder` implementation.

Account setup on connect: Binance requires an explicit `POST /positionSide/dual` call
(hedge-mode toggle, analog of `SwitchPositionMode`) and per-symbol `POST /leverage` —
both become `Exchange` interface methods already listed above, already needed for
Bybit's own account setup.

Instrument constraints (tick size, min notional, lot size) differ per exchange even for
an identical symbol string. `getInstrumentConstraints` (currently Bybit
`instruments-info`) needs a Binance equivalent (`/fapi/v1/exchangeInfo`) behind the
same interface method.

## Testing

No testnet — the user validates directly on their own live Binance account with small
capital. Given that, the design pushes as much verification as possible into
zero-risk, no-network testing:

- Table-driven unit tests translating recorded/sample Binance REST and WS JSON payloads
  into the existing internal types (`OrderRequest`/`Position`/`ClosedPnl`) — the actual
  correctness-critical surface, fully testable without live API access.
- Table-driven tests specifically covering `orderLinkId` length/format edge cases (high
  cycle numbers, `repriceGen`, matrix level suffixes) against Binance's 36-char cap.
- `Exchange` dispatch tests (`AccountRunner` resolves the right implementation from
  `account.Exchange`) using a fake `Exchange`, no network.
- `reconcileMissingTradeHistory` (the trade_history gap-detection watchdog added
  2026-09-02/03) needs no Binance-specific work — it already operates purely through
  `trade_history`/`trader_executions`/`strategy_cycles`, exchange-agnostic by
  construction; it picks up Binance-originated gaps for free once `Exchange` is wired.
- Live validation on the user's own account, in ascending order of blast radius: Grid
  first (cheapest failure mode — real TP/SL orders on the exchange, no continuous price
  dependency), then Matrix (exercises the new `TickerHub` dispatch), then Hedge/
  Мультибот. All three ship in the same release; the ordering is about how the user
  exercises them live, not a code-shipping sequence.

## Rollout

No per-user gating once live-validated (explicit decision — same behavior as every
other exchange on the platform, no beta flag, no admin-only restriction). The practical
mechanism: the frontend's `EXCHANGES.binance.supported` flag (`AccountsPage.tsx`,
currently `false`) stays `false` — hiding Binance from the account-creation picker —
until the user's own live validation (Grid → Matrix → Hedge, above) passes. Flipping it
to `true` is the actual "launch" action; no other rollout machinery is needed.

## Scope / affected files

New:
- `pkg/trader/exchange.go` — the `Exchange` interface
- `pkg/trader/binance/` — Binance USDⓈ-M Futures REST + user-data-stream WS client,
  implementing `Exchange`

Modified:
- `pkg/trader/bybit.go`, `pkg/trader/trade_ws.go` — reorganized behind
  `bybit.Exchange{}`, behavior unchanged
- `pkg/signal/ticker_hub.go`, `pkg/signal/hub.go` — `(exchange, symbol)` keying, second
  WS pool
- `pkg/strategy/engine.go`, `cycle.go`, `matrix.go`, `startup_reconcile.go` — free
  function calls → `sr.runner.exchange.*`
- `services/api-gateway/{bots_handler,strategy_handler,matrix_engine,hedge_engine,
  bot_engine,trader_handler,rescue_engine}.go` — same swap
- `services/api-gateway/*` instrument-constraints endpoint — Binance branch
- `frontend/src/pages/AccountsPage.tsx` — flip `supported: true` for Binance (launch
  step, done last)

Likely unaffected:
- DB schema/migrations — `exchange_accounts.exchange` already permits `'binance'`
- `closed_pnl_syncer.go`'s `reconcileMissingTradeHistory` — exchange-agnostic already

## Open risks carried into planning

1. `orderLinkId` length against Binance's 36-char cap (§3) — needs a concrete resolved
   encoding scheme, verified against Binance's real API.
2. Binance Futures rate limits are weight-based and structured differently from
   Bybit's — the platform's "global warmer" (742-symbol scan) and grid strategies'
   order cadence need a sanity check against Binance's actual limits during
   implementation; not assessed in this design.
3. Exact `Exchange` method list is illustrative here — finalized by reading the ~11
   call sites during the implementation plan, not guessed in advance.
