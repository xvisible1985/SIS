# Exchange Interface + Bybit Adapter Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Introduce a `trader.Exchange` interface covering every trading operation `pkg/strategy` and `services/api-gateway` actually use today, and a `BybitExchange` implementation that wraps the existing Bybit functions/`TradeStream` with zero behavior change — a pure reorganization that unlocks adding Binance (a later plan) without touching any of the ~11 call sites yet.

**Architecture:** `pkg/trader/exchange.go` defines `Exchange` plus a small unexported `wsOrderClient` interface (the four order-placement/cancel methods `*TradeStream` already has). `BybitExchange` holds `Credentials` + a `wsOrderClient`, delegates the WS-backed methods straight to it, and delegates the REST-backed methods to the existing free functions in `bybit.go`. Nothing in `pkg/strategy`, `services/api-gateway`, `engine.go`, or `trade_ws.go` changes in this plan — this plan is self-contained inside `pkg/trader` and does not wire `BybitExchange` into `AccountRunner` yet (that's the first step of the later call-site-migration plan).

**Tech Stack:** Go, `net/http/httptest` for REST delegation tests (reusing the existing `withMockBybitBase` helper in `pkg/trader/syncer_test.go`), hand-written fakes for the WS-backed methods (no existing WS test harness for `TradeStream` — out of scope to build one here).

**Spec:** `docs/superpowers/specs/2026-09-03-binance-live-trading-design.md`

---

## Before you start

Read these two files in full — every step below assumes you already know their exact shape:
- `pkg/trader/types.go` — `Credentials`, `OrderRequest`, `OrderResult`, `CancelRequest`, `CancelAllRequest`, `BatchPlaceRequest`, `BatchPlaceResult`, `BatchCancelRequest`, `LeverageRequest`, `Position`, `Order`, `ClosedPnl`.
- `pkg/trader/syncer_test.go:1-22` — the `withMockBybitBase(t, srv)` helper this plan's REST tests reuse verbatim (do not redefine it).

All work in this plan lives in two new files: `pkg/trader/exchange.go` and `pkg/trader/exchange_test.go`. No existing file is modified.

---

### Task 1: `Exchange` interface + WS-backed order methods

**Files:**
- Create: `pkg/trader/exchange.go`
- Create: `pkg/trader/exchange_test.go`

- [ ] **Step 1: Write the failing test**

Create `pkg/trader/exchange_test.go`:

```go
package trader

import (
	"context"
	"errors"
	"testing"
)

// fakeWSOrderClient is a hand-written stand-in for *TradeStream, so the WS-backed
// BybitExchange methods can be tested without a real WS connection (TradeStream has no
// existing test harness — building one is out of scope for this plan, which only
// reorganizes existing, already-working code behind an interface).
type fakeWSOrderClient struct {
	placeOrderReq   OrderRequest
	placeOrderResp  OrderResult
	placeOrderErr   error
	placeBatchReq   BatchPlaceRequest
	placeBatchResp  []BatchPlaceResult
	placeBatchErr   error
	cancelOrderReq  CancelRequest
	cancelOrderErr  error
	cancelBatchReq  BatchCancelRequest
	cancelBatchErr  error
}

func (f *fakeWSOrderClient) PlaceOrder(ctx context.Context, req OrderRequest) (OrderResult, error) {
	f.placeOrderReq = req
	return f.placeOrderResp, f.placeOrderErr
}

func (f *fakeWSOrderClient) PlaceOrderBatch(ctx context.Context, req BatchPlaceRequest) ([]BatchPlaceResult, error) {
	f.placeBatchReq = req
	return f.placeBatchResp, f.placeBatchErr
}

func (f *fakeWSOrderClient) CancelOrder(ctx context.Context, req CancelRequest) error {
	f.cancelOrderReq = req
	return f.cancelOrderErr
}

func (f *fakeWSOrderClient) CancelOrderBatch(ctx context.Context, req BatchCancelRequest) error {
	f.cancelBatchReq = req
	return f.cancelBatchErr
}

func TestBybitExchange_PlaceOrder_DelegatesToWSClient(t *testing.T) {
	fake := &fakeWSOrderClient{placeOrderResp: OrderResult{OrderId: "ord-1", OrderLinkId: "link-1"}}
	ex := NewBybitExchange(Credentials{APIKey: "k", SecretKey: "s"}, fake)

	req := OrderRequest{Symbol: "BTCUSDT", Category: "linear", Side: "Buy", OrderType: "Limit", Qty: "1", Price: "50000"}
	got, err := ex.PlaceOrder(context.Background(), req)
	if err != nil {
		t.Fatalf("PlaceOrder: %v", err)
	}
	if got.OrderId != "ord-1" || got.OrderLinkId != "link-1" {
		t.Errorf("PlaceOrder result = %+v, want the fake's canned OrderResult", got)
	}
	if fake.placeOrderReq.Symbol != "BTCUSDT" || fake.placeOrderReq.Price != "50000" {
		t.Errorf("fake received req = %+v, want the exact req passed to PlaceOrder", fake.placeOrderReq)
	}
}

func TestBybitExchange_PlaceOrder_PropagatesError(t *testing.T) {
	wantErr := errors.New("bybit: retCode=110017: current position is zero")
	fake := &fakeWSOrderClient{placeOrderErr: wantErr}
	ex := NewBybitExchange(Credentials{}, fake)

	_, err := ex.PlaceOrder(context.Background(), OrderRequest{Symbol: "BTCUSDT"})
	if !errors.Is(err, wantErr) {
		t.Errorf("PlaceOrder error = %v, want %v", err, wantErr)
	}
}

func TestBybitExchange_PlaceOrderBatch_DelegatesToWSClient(t *testing.T) {
	fake := &fakeWSOrderClient{placeBatchResp: []BatchPlaceResult{{OrderId: "b1"}, {OrderId: "b2"}}}
	ex := NewBybitExchange(Credentials{}, fake)

	req := BatchPlaceRequest{Category: "linear", Request: []BatchOrderItem{{Symbol: "ETHUSDT"}}}
	got, err := ex.PlaceOrderBatch(context.Background(), req)
	if err != nil {
		t.Fatalf("PlaceOrderBatch: %v", err)
	}
	if len(got) != 2 || got[0].OrderId != "b1" || got[1].OrderId != "b2" {
		t.Errorf("PlaceOrderBatch result = %+v, want the fake's canned results", got)
	}
	if fake.placeBatchReq.Category != "linear" {
		t.Errorf("fake received req.Category = %q, want %q", fake.placeBatchReq.Category, "linear")
	}
}

func TestBybitExchange_CancelOrder_DelegatesToWSClient(t *testing.T) {
	fake := &fakeWSOrderClient{}
	ex := NewBybitExchange(Credentials{}, fake)

	req := CancelRequest{Symbol: "BTCUSDT", Category: "linear", OrderId: "ord-1"}
	if err := ex.CancelOrder(context.Background(), req); err != nil {
		t.Fatalf("CancelOrder: %v", err)
	}
	if fake.cancelOrderReq.OrderId != "ord-1" {
		t.Errorf("fake received req.OrderId = %q, want %q", fake.cancelOrderReq.OrderId, "ord-1")
	}
}

func TestBybitExchange_CancelOrderBatch_DelegatesToWSClient(t *testing.T) {
	fake := &fakeWSOrderClient{}
	ex := NewBybitExchange(Credentials{}, fake)

	req := BatchCancelRequest{Category: "linear", Request: []BatchCancelItem{{Symbol: "BTCUSDT", OrderId: "ord-1"}}}
	if err := ex.CancelOrderBatch(context.Background(), req); err != nil {
		t.Fatalf("CancelOrderBatch: %v", err)
	}
	if fake.cancelBatchReq.Category != "linear" {
		t.Errorf("fake received req.Category = %q, want %q", fake.cancelBatchReq.Category, "linear")
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./pkg/trader/... -run TestBybitExchange -v`
Expected: FAIL — `undefined: NewBybitExchange` (or similar; `pkg/trader/exchange.go` does not exist yet, so the package fails to compile).

- [ ] **Step 3: Write the interface and the WS-backed methods**

Create `pkg/trader/exchange.go`:

```go
package trader

import (
	"context"
	"time"
)

// Exchange is every trading operation pkg/strategy and services/api-gateway perform
// against an account, resolved once per account (via account.Exchange) instead of the
// bare free functions in bybit.go being called by name. BybitExchange (this file) is
// the first implementation — a pure reorganization of already-working code, no
// behavior change. A Binance implementation is added in a later plan.
type Exchange interface {
	// WS-backed order placement/cancellation — the live strategy engine's own path
	// (grid/matrix entries, TP/SL, per-level orders). Backed by *TradeStream for Bybit.
	PlaceOrder(ctx context.Context, req OrderRequest) (OrderResult, error)
	PlaceOrderBatch(ctx context.Context, req BatchPlaceRequest) ([]BatchPlaceResult, error)
	CancelOrder(ctx context.Context, req CancelRequest) error
	CancelOrderBatch(ctx context.Context, req BatchCancelRequest) error

	// REST-only order placement/cancellation — used by user-facing/admin handlers
	// (manual order entry, rescue engine, hedge engine's own direct closes) that don't
	// hold a live trade-WS connection for the account.
	PlaceOrderREST(ctx context.Context, req OrderRequest) (OrderResult, error)
	CancelOrderREST(ctx context.Context, req CancelRequest) error
	CancelAllOrders(ctx context.Context, req CancelAllRequest) error

	FetchPositions(ctx context.Context) ([]Position, error)
	FetchOpenOrdersForSymbolAll(ctx context.Context, category, symbol string) ([]Order, error)

	FetchClosedPnlForSymbol(ctx context.Context, category, symbol string, limit int) ([]ClosedPnl, error)
	FetchRecentClosedPnl(ctx context.Context, category string, since time.Time) ([]ClosedPnl, error)

	GetWalletBalance(ctx context.Context) (equity, available float64, err error)
	SetLeverage(ctx context.Context, req LeverageRequest) error
	SwitchPositionMode(ctx context.Context, category, symbol string, mode int) error

	GetMarkPrice(ctx context.Context, category, symbol string) (float64, error)
}

// wsOrderClient is the subset of *TradeStream's methods BybitExchange needs for the
// WS-backed order operations. Exists only so exchange_test.go can substitute a fake —
// *TradeStream (pkg/trader/trade_ws.go) already has this exact method set and needs no
// changes to satisfy it.
type wsOrderClient interface {
	PlaceOrder(ctx context.Context, req OrderRequest) (OrderResult, error)
	PlaceOrderBatch(ctx context.Context, req BatchPlaceRequest) ([]BatchPlaceResult, error)
	CancelOrder(ctx context.Context, req CancelRequest) error
	CancelOrderBatch(ctx context.Context, req BatchCancelRequest) error
}

// BybitExchange implements Exchange by delegating to the existing free functions in
// bybit.go (REST) and a wsOrderClient (WS, normally *TradeStream). No new HTTP/WS
// logic — every method here is a direct pass-through to code that already works in
// production.
type BybitExchange struct {
	creds Credentials
	ws    wsOrderClient
}

// NewBybitExchange builds a BybitExchange. ws is normally the account's own
// *TradeStream (see pkg/trader/trade_ws.go), already constructed with the same creds.
func NewBybitExchange(creds Credentials, ws wsOrderClient) *BybitExchange {
	return &BybitExchange{creds: creds, ws: ws}
}

func (e *BybitExchange) PlaceOrder(ctx context.Context, req OrderRequest) (OrderResult, error) {
	return e.ws.PlaceOrder(ctx, req)
}

func (e *BybitExchange) PlaceOrderBatch(ctx context.Context, req BatchPlaceRequest) ([]BatchPlaceResult, error) {
	return e.ws.PlaceOrderBatch(ctx, req)
}

func (e *BybitExchange) CancelOrder(ctx context.Context, req CancelRequest) error {
	return e.ws.CancelOrder(ctx, req)
}

func (e *BybitExchange) CancelOrderBatch(ctx context.Context, req BatchCancelRequest) error {
	return e.ws.CancelOrderBatch(ctx, req)
}
```

Note: `BybitExchange` does not yet implement the REST-backed methods (`PlaceOrderREST`,
`CancelOrderREST`, `FetchPositions`, etc.) — Tasks 2-5 add those. `Exchange` is not yet
satisfied by `*BybitExchange`; the compile-time assertion proving it is added in Task 6
once every method exists.

- [ ] **Step 4: Run the test to verify it passes**

Run: `go test ./pkg/trader/... -run TestBybitExchange -v`
Expected: `PASS` — all 5 tests from Step 1 (`TestBybitExchange_PlaceOrder_DelegatesToWSClient`, `TestBybitExchange_PlaceOrder_PropagatesError`, `TestBybitExchange_PlaceOrderBatch_DelegatesToWSClient`, `TestBybitExchange_CancelOrder_DelegatesToWSClient`, `TestBybitExchange_CancelOrderBatch_DelegatesToWSClient`).

- [ ] **Step 5: Commit**

```bash
git add pkg/trader/exchange.go pkg/trader/exchange_test.go
git commit -m "$(cat <<'EOF'
feat(trader): add Exchange interface + WS-backed BybitExchange methods

First step of multi-exchange support (see docs/superpowers/specs/2026-09-03-
binance-live-trading-design.md): a trader.Exchange interface covering every
trading operation the strategy engine and API handlers actually use, resolved
once per account instead of bare free-function calls by name. BybitExchange
wraps *TradeStream for the WS-backed order methods with zero behavior change.

REST-backed methods (positions, closed-pnl, wallet, leverage, etc.) land in
follow-up commits on this same branch.

Co-Authored-By: Claude Sonnet 5 <noreply@anthropic.com>
EOF
)"
```

---

### Task 2: Positions and open orders

**Files:**
- Modify: `pkg/trader/exchange.go`
- Modify: `pkg/trader/exchange_test.go`

- [ ] **Step 1: Write the failing tests**

Add `"net/http"` and `"net/http/httptest"` to the existing `import (...)` block at the
top of `pkg/trader/exchange_test.go` (do not add a second `import` block — Go import
declarations must stay in the single block at the top of the file):

```go
import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)
```

Then append to `pkg/trader/exchange_test.go`:

```go
func TestBybitExchange_FetchPositions_DelegatesToREST(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Query().Get("category") {
		case "linear":
			_, _ = w.Write([]byte(`{"retCode":0,"retMsg":"OK","result":{"list":[{"symbol":"BTCUSDT","side":"Buy","size":"0.5"}]}}`))
		default:
			_, _ = w.Write([]byte(`{"retCode":0,"retMsg":"OK","result":{"list":[]}}`))
		}
	}))
	defer srv.Close()
	withMockBybitBase(t, srv)

	ex := NewBybitExchange(Credentials{APIKey: "k", SecretKey: "s"}, &fakeWSOrderClient{})
	got, err := ex.FetchPositions(context.Background())
	if err != nil {
		t.Fatalf("FetchPositions: %v", err)
	}
	found := false
	for _, p := range got {
		if p.Symbol == "BTCUSDT" && p.Category == "linear" {
			found = true
		}
	}
	if !found {
		t.Errorf("FetchPositions = %+v, want a BTCUSDT/linear position", got)
	}
}

func TestBybitExchange_FetchOpenOrdersForSymbolAll_DelegatesToREST(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		filter := r.URL.Query().Get("orderFilter")
		_, _ = w.Write([]byte(`{"retCode":0,"retMsg":"OK","result":{"list":[{"orderId":"ord-` + filter + `","symbol":"BTCUSDT"}],"nextPageCursor":""}}`))
	}))
	defer srv.Close()
	withMockBybitBase(t, srv)

	ex := NewBybitExchange(Credentials{APIKey: "k", SecretKey: "s"}, &fakeWSOrderClient{})
	got, err := ex.FetchOpenOrdersForSymbolAll(context.Background(), "linear", "BTCUSDT")
	if err != nil {
		t.Fatalf("FetchOpenOrdersForSymbolAll: %v", err)
	}
	// bybit.go's FetchOpenOrdersForSymbolAll queries both "Order" and "StopOrder"
	// filters — the mock server echoes the filter into orderId, so two distinct IDs
	// prove both filters were actually requested through this delegation.
	if len(got) != 2 {
		t.Fatalf("FetchOpenOrdersForSymbolAll returned %d orders, want 2 (Order + StopOrder)", len(got))
	}
	seen := map[string]bool{}
	for _, o := range got {
		seen[o.OrderId] = true
	}
	if !seen["ord-Order"] || !seen["ord-StopOrder"] {
		t.Errorf("FetchOpenOrdersForSymbolAll order IDs = %v, want ord-Order and ord-StopOrder", seen)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./pkg/trader/... -run 'TestBybitExchange_FetchPositions|TestBybitExchange_FetchOpenOrdersForSymbolAll' -v`
Expected: FAIL — `ex.FetchPositions undefined` / `ex.FetchOpenOrdersForSymbolAll undefined` (method not yet implemented on `*BybitExchange`).

- [ ] **Step 3: Implement the two methods**

Append to `pkg/trader/exchange.go`:

```go
func (e *BybitExchange) FetchPositions(ctx context.Context) ([]Position, error) {
	return FetchPositions(ctx, e.creds)
}

func (e *BybitExchange) FetchOpenOrdersForSymbolAll(ctx context.Context, category, symbol string) ([]Order, error) {
	return FetchOpenOrdersForSymbolAll(ctx, e.creds, category, symbol)
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./pkg/trader/... -run 'TestBybitExchange_FetchPositions|TestBybitExchange_FetchOpenOrdersForSymbolAll' -v`
Expected: `PASS` for both tests.

- [ ] **Step 5: Commit**

```bash
git add pkg/trader/exchange.go pkg/trader/exchange_test.go
git commit -m "$(cat <<'EOF'
feat(trader): add FetchPositions/FetchOpenOrdersForSymbolAll to BybitExchange

Co-Authored-By: Claude Sonnet 5 <noreply@anthropic.com>
EOF
)"
```

---

### Task 3: Closed PnL

**Files:**
- Modify: `pkg/trader/exchange.go`
- Modify: `pkg/trader/exchange_test.go`

- [ ] **Step 1: Write the failing tests**

Append to `pkg/trader/exchange_test.go`:

```go
func TestBybitExchange_FetchClosedPnlForSymbol_DelegatesToREST(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path != "/v5/position/closed-pnl" {
			// e.g. /v5/market/time — doSignedGET's timestamp sync. Not load-bearing here.
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if r.URL.Query().Get("symbol") != "BTCUSDT" {
			t.Errorf("request symbol = %q, want BTCUSDT", r.URL.Query().Get("symbol"))
		}
		_, _ = w.Write([]byte(`{"retCode":0,"retMsg":"OK","result":{"list":[{"symbol":"BTCUSDT","orderId":"ord-1","closedPnl":"5.5"}]}}`))
	}))
	defer srv.Close()
	withMockBybitBase(t, srv)

	ex := NewBybitExchange(Credentials{APIKey: "k", SecretKey: "s"}, &fakeWSOrderClient{})
	got, err := ex.FetchClosedPnlForSymbol(context.Background(), "linear", "BTCUSDT", 10)
	if err != nil {
		t.Fatalf("FetchClosedPnlForSymbol: %v", err)
	}
	if len(got) != 1 || got[0].OrderId != "ord-1" || got[0].ClosedPnl != "5.5" {
		t.Errorf("FetchClosedPnlForSymbol = %+v, want one ord-1 row with closedPnl=5.5", got)
	}
}

func TestBybitExchange_FetchRecentClosedPnl_DelegatesToREST(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"retCode":0,"retMsg":"OK","result":{"list":[{"symbol":"ETHUSDT","orderId":"ord-2","closedPnl":"1.1"}],"nextPageCursor":""}}`))
	}))
	defer srv.Close()
	withMockBybitBase(t, srv)

	ex := NewBybitExchange(Credentials{APIKey: "k", SecretKey: "s"}, &fakeWSOrderClient{})
	got, err := ex.FetchRecentClosedPnl(context.Background(), "linear", time.Now().Add(-time.Hour))
	if err != nil {
		t.Fatalf("FetchRecentClosedPnl: %v", err)
	}
	if len(got) != 1 || got[0].OrderId != "ord-2" {
		t.Errorf("FetchRecentClosedPnl = %+v, want one ord-2 row", got)
	}
}
```

Add `"time"` to the existing `import (...)` block at the top of
`pkg/trader/exchange_test.go` — it's the first test in this file to need it
(`TestBybitExchange_FetchRecentClosedPnl_DelegatesToREST`'s `since` argument):

```go
import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./pkg/trader/... -run 'TestBybitExchange_FetchClosedPnlForSymbol|TestBybitExchange_FetchRecentClosedPnl' -v`
Expected: FAIL — methods undefined on `*BybitExchange`.

- [ ] **Step 3: Implement the two methods**

Append to `pkg/trader/exchange.go`:

```go
func (e *BybitExchange) FetchClosedPnlForSymbol(ctx context.Context, category, symbol string, limit int) ([]ClosedPnl, error) {
	return FetchClosedPnlForSymbol(ctx, e.creds, category, symbol, limit)
}

func (e *BybitExchange) FetchRecentClosedPnl(ctx context.Context, category string, since time.Time) ([]ClosedPnl, error) {
	return FetchRecentClosedPnl(ctx, e.creds, category, since)
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./pkg/trader/... -run 'TestBybitExchange_FetchClosedPnlForSymbol|TestBybitExchange_FetchRecentClosedPnl' -v`
Expected: `PASS` for both tests.

- [ ] **Step 5: Commit**

```bash
git add pkg/trader/exchange.go pkg/trader/exchange_test.go
git commit -m "$(cat <<'EOF'
feat(trader): add FetchClosedPnlForSymbol/FetchRecentClosedPnl to BybitExchange

Co-Authored-By: Claude Sonnet 5 <noreply@anthropic.com>
EOF
)"
```

---

### Task 4: Wallet balance, leverage, position mode

**Files:**
- Modify: `pkg/trader/exchange.go`
- Modify: `pkg/trader/exchange_test.go`

- [ ] **Step 1: Write the failing tests**

Append to `pkg/trader/exchange_test.go`:

```go
func TestBybitExchange_GetWalletBalance_DelegatesToREST(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"retCode":0,"retMsg":"OK","result":{"list":[{"totalEquity":"1000.5","totalAvailableBalance":"800.25"}]}}`))
	}))
	defer srv.Close()
	withMockBybitBase(t, srv)

	ex := NewBybitExchange(Credentials{APIKey: "k", SecretKey: "s"}, &fakeWSOrderClient{})
	equity, available, err := ex.GetWalletBalance(context.Background())
	if err != nil {
		t.Fatalf("GetWalletBalance: %v", err)
	}
	if equity != 1000.5 || available != 800.25 {
		t.Errorf("GetWalletBalance = (%v, %v), want (1000.5, 800.25)", equity, available)
	}
}

func TestBybitExchange_SetLeverage_DelegatesToREST(t *testing.T) {
	var gotBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotBody, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"retCode":0,"retMsg":"OK","result":{}}`))
	}))
	defer srv.Close()
	withMockBybitBase(t, srv)

	ex := NewBybitExchange(Credentials{APIKey: "k", SecretKey: "s"}, &fakeWSOrderClient{})
	req := LeverageRequest{Symbol: "BTCUSDT", Category: "linear", BuyLeverage: "10", SellLeverage: "10"}
	if err := ex.SetLeverage(context.Background(), req); err != nil {
		t.Fatalf("SetLeverage: %v", err)
	}
	if !bytes.Contains(gotBody, []byte(`"buyLeverage":"10"`)) {
		t.Errorf("SetLeverage request body = %s, want buyLeverage=10", gotBody)
	}
}

func TestBybitExchange_SwitchPositionMode_DelegatesToREST(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"retCode":0,"retMsg":"OK"}`))
	}))
	defer srv.Close()
	withMockBybitBase(t, srv)

	ex := NewBybitExchange(Credentials{APIKey: "k", SecretKey: "s"}, &fakeWSOrderClient{})
	if err := ex.SwitchPositionMode(context.Background(), "linear", "BTCUSDT", 3); err != nil {
		t.Fatalf("SwitchPositionMode: %v", err)
	}
}
```

Add `"bytes"` and `"io"` to the existing `import (...)` block at the top of
`pkg/trader/exchange_test.go`:

```go
import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./pkg/trader/... -run 'TestBybitExchange_GetWalletBalance|TestBybitExchange_SetLeverage|TestBybitExchange_SwitchPositionMode' -v`
Expected: FAIL — methods undefined on `*BybitExchange`.

- [ ] **Step 3: Implement the three methods**

Append to `pkg/trader/exchange.go`:

```go
func (e *BybitExchange) GetWalletBalance(ctx context.Context) (equity, available float64, err error) {
	return GetWalletBalance(ctx, e.creds)
}

func (e *BybitExchange) SetLeverage(ctx context.Context, req LeverageRequest) error {
	return SetLeverage(ctx, e.creds, req)
}

func (e *BybitExchange) SwitchPositionMode(ctx context.Context, category, symbol string, mode int) error {
	return SwitchPositionMode(ctx, e.creds, category, symbol, mode)
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./pkg/trader/... -run 'TestBybitExchange_GetWalletBalance|TestBybitExchange_SetLeverage|TestBybitExchange_SwitchPositionMode' -v`
Expected: `PASS` for all three tests.

- [ ] **Step 5: Commit**

```bash
git add pkg/trader/exchange.go pkg/trader/exchange_test.go
git commit -m "$(cat <<'EOF'
feat(trader): add GetWalletBalance/SetLeverage/SwitchPositionMode to BybitExchange

Co-Authored-By: Claude Sonnet 5 <noreply@anthropic.com>
EOF
)"
```

---

### Task 5: Mark price, REST order placement/cancel, cancel-all

**Files:**
- Modify: `pkg/trader/exchange.go`
- Modify: `pkg/trader/exchange_test.go`

- [ ] **Step 1: Write the failing tests**

Append to `pkg/trader/exchange_test.go`:

```go
func TestBybitExchange_GetMarkPrice_DelegatesToREST(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"retCode":0,"retMsg":"OK","result":{"list":[{"markPrice":"67890.5"}]}}`))
	}))
	defer srv.Close()
	withMockBybitBase(t, srv)

	ex := NewBybitExchange(Credentials{APIKey: "k", SecretKey: "s"}, &fakeWSOrderClient{})
	got, err := ex.GetMarkPrice(context.Background(), "linear", "BTCUSDT")
	if err != nil {
		t.Fatalf("GetMarkPrice: %v", err)
	}
	if got != 67890.5 {
		t.Errorf("GetMarkPrice = %v, want 67890.5", got)
	}
}

func TestBybitExchange_PlaceOrderREST_DelegatesToREST(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"retCode":0,"retMsg":"OK","result":{"orderId":"rest-ord-1","orderLinkId":"rest-link-1"}}`))
	}))
	defer srv.Close()
	withMockBybitBase(t, srv)

	ex := NewBybitExchange(Credentials{APIKey: "k", SecretKey: "s"}, &fakeWSOrderClient{})
	got, err := ex.PlaceOrderREST(context.Background(), OrderRequest{Symbol: "BTCUSDT", Category: "linear", Side: "Buy", OrderType: "Market", Qty: "1"})
	if err != nil {
		t.Fatalf("PlaceOrderREST: %v", err)
	}
	if got.OrderId != "rest-ord-1" {
		t.Errorf("PlaceOrderREST result = %+v, want orderId=rest-ord-1", got)
	}
}

func TestBybitExchange_CancelOrderREST_DelegatesToREST(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"retCode":0,"retMsg":"OK","result":{}}`))
	}))
	defer srv.Close()
	withMockBybitBase(t, srv)

	ex := NewBybitExchange(Credentials{APIKey: "k", SecretKey: "s"}, &fakeWSOrderClient{})
	if err := ex.CancelOrderREST(context.Background(), CancelRequest{Symbol: "BTCUSDT", Category: "linear", OrderId: "ord-1"}); err != nil {
		t.Fatalf("CancelOrderREST: %v", err)
	}
}

func TestBybitExchange_CancelAllOrders_DelegatesToREST(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"retCode":0,"retMsg":"OK","result":{}}`))
	}))
	defer srv.Close()
	withMockBybitBase(t, srv)

	ex := NewBybitExchange(Credentials{APIKey: "k", SecretKey: "s"}, &fakeWSOrderClient{})
	if err := ex.CancelAllOrders(context.Background(), CancelAllRequest{Category: "linear", Symbol: "BTCUSDT"}); err != nil {
		t.Fatalf("CancelAllOrders: %v", err)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./pkg/trader/... -run 'TestBybitExchange_GetMarkPrice|TestBybitExchange_PlaceOrderREST|TestBybitExchange_CancelOrderREST|TestBybitExchange_CancelAllOrders' -v`
Expected: FAIL — methods undefined on `*BybitExchange`.

- [ ] **Step 3: Implement the four methods**

Append to `pkg/trader/exchange.go`:

```go
func (e *BybitExchange) GetMarkPrice(ctx context.Context, category, symbol string) (float64, error) {
	return FetchMarkPrice(ctx, e.creds, category, symbol)
}

func (e *BybitExchange) PlaceOrderREST(ctx context.Context, req OrderRequest) (OrderResult, error) {
	return PlaceOrder(ctx, e.creds, req)
}

func (e *BybitExchange) CancelOrderREST(ctx context.Context, req CancelRequest) error {
	return CancelOrder(ctx, e.creds, req)
}

func (e *BybitExchange) CancelAllOrders(ctx context.Context, req CancelAllRequest) error {
	return CancelAllOrders(ctx, e.creds, req)
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./pkg/trader/... -run 'TestBybitExchange_GetMarkPrice|TestBybitExchange_PlaceOrderREST|TestBybitExchange_CancelOrderREST|TestBybitExchange_CancelAllOrders' -v`
Expected: `PASS` for all four tests.

- [ ] **Step 5: Commit**

```bash
git add pkg/trader/exchange.go pkg/trader/exchange_test.go
git commit -m "$(cat <<'EOF'
feat(trader): add GetMarkPrice/PlaceOrderREST/CancelOrderREST/CancelAllOrders to BybitExchange

Co-Authored-By: Claude Sonnet 5 <noreply@anthropic.com>
EOF
)"
```

---

### Task 6: Compile-time interface assertion + full verification

**Files:**
- Modify: `pkg/trader/exchange.go`

- [ ] **Step 1: Write the failing check**

Append to the bottom of `pkg/trader/exchange.go`:

```go
// var _ Exchange = (*BybitExchange)(nil) fails to compile if BybitExchange ever stops
// satisfying Exchange — the cheapest possible regression guard for this file.
var _ Exchange = (*BybitExchange)(nil)
```

At this point every method Task 1-5 defined should already cover the full `Exchange`
interface. If a method is missing, this line will not compile — that IS the check for
this step; there is no separate "make it fail" step since Tasks 1-5 already built the
implementation incrementally.

- [ ] **Step 2: Run the full package build and test suite**

Run: `go build ./pkg/trader/... && go vet ./pkg/trader/... && go test ./pkg/trader/... -v`
Expected: builds cleanly, `go vet` reports nothing, and every test in the package
passes — including all `TestBybitExchange_*` tests from Tasks 1-5 and the pre-existing
`TestIsPermanentAuthError`, `TestNormalizeWhitelistedIPs`, `TestSign`, and the
`TestRefreshWhitelistedIPs_*` tests in `syncer_test.go` (proving this plan didn't touch
their behavior).

- [ ] **Step 3: Run the full repository build**

Run: `go build ./... && go test ./...`
Expected: everything still builds and passes — this plan added two new files inside
`pkg/trader` and touched nothing else, so no other package should be affected.

- [ ] **Step 4: Commit**

```bash
git add pkg/trader/exchange.go
git commit -m "$(cat <<'EOF'
feat(trader): assert BybitExchange satisfies Exchange

Closes out the Exchange interface + Bybit adapter plan (see
docs/superpowers/specs/2026-09-03-binance-live-trading-design.md). Nothing in
pkg/strategy or services/api-gateway uses BybitExchange yet — that migration
is a separate, later plan.

Co-Authored-By: Claude Sonnet 5 <noreply@anthropic.com>
EOF
)"
```

---

## What this plan deliberately does not do

- Does not wire `BybitExchange` into `AccountRunner`/`Engine.Start` — no call site in
  `pkg/strategy` or `services/api-gateway` changes.
- Does not touch `pkg/trader/trade_ws.go` — `*TradeStream` already satisfies
  `wsOrderClient` structurally, zero changes needed.
- Does not add `pkg/trader/binance/` — that's its own plan, testable independently via
  recorded API fixtures once this interface exists to implement.
- Does not resolve the `orderLinkId` 36-character risk noted in the spec — that's
  scoped to the plan that actually builds Binance's order-placement translation.
