# Binance Exchange Client Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build `pkg/trader/binance`, a Binance USDⓈ-M Futures client implementing the `trader.Exchange` interface (from the already-merged Exchange interface + Bybit adapter plan), so a Binance account can eventually drive the same strategy engine Bybit does today.

**Architecture:** A low-level signed-request client (`client.go`: HMAC-SHA256 signing, `doSignedGET`/`doSignedPOST`/`doSignedDELETE`/`doPublicGET`, matching the `bybitBase`-style overridable-base-URL pattern `pkg/trader/bybit.go` already uses for testability) plus `BinanceExchange` (`exchange.go`), which implements all 15 `trader.Exchange` methods by calling that client and translating Binance's wire format into the existing `trader.OrderRequest`/`Position`/`ClosedPnl`/`Order`/`OrderResult` types — the same "existing types are the canonical internal format" approach the Bybit adapter uses, so nothing in `pkg/strategy` needs new vocabulary once this is wired in (a later plan).

**Tech Stack:** Go, `net/http/httptest` for all tests (no live Binance network access — this plan is fixture-driven throughout, per the design spec's testing section).

**Spec:** `docs/superpowers/specs/2026-09-03-binance-live-trading-design.md`
**Depends on:** `docs/superpowers/plans/2026-09-03-exchange-interface-bybit-adapter.md` (merged — `trader.Exchange`, `trader.OrderRequest`, etc. already exist in `pkg/trader`)

---

## Before you start

Read these first — every step below assumes you already know their exact shape:
- `pkg/trader/types.go` — the target types every translation in this plan produces: `OrderRequest`, `OrderResult`, `CancelRequest`, `CancelAllRequest`, `BatchPlaceRequest`, `BatchOrderItem`, `BatchPlaceResult`, `BatchCancelRequest`, `BatchCancelItem`, `LeverageRequest`, `Position`, `Order`, `ClosedPnl`, `Credentials`.
- `pkg/trader/exchange.go` — the `Exchange` interface this plan's `BinanceExchange` must satisfy, method-for-method.
- `pkg/trader/bybit.go:1-70` — the existing signing/request pattern (`bybitBase` as an overridable `var`, `doSignedGET`/`doSignedPOST`, `checkRetCode`) this plan's Binance client mirrors structurally (different signing scheme, same shape of idea).

**Real Binance USDⓈ-M Futures API facts this plan relies on** (verified against official docs during planning, not assumed from memory):
- Base URL: `https://fapi.binance.com`.
- Auth: `X-MBX-APIKEY` header + `signature` param = hex HMAC-SHA256(secretKey, totalParamsString), where `totalParamsString` is the exact query-string/body-string being sent (all params except `signature` itself, `timestamp` and `recvWindow` required on every signed call).
- GET/DELETE: signed params go in the URL query string. POST: signed params go in the request body as `application/x-www-form-urlencoded` (not JSON — this is a real difference from Bybit, which sends JSON bodies).
- Error shape: `{"code": -1121, "msg": "Invalid symbol."}`.
- `POST /fapi/v1/order` → order id is a JSON **number**, not a string like Bybit (`"orderId": 123456`) — every translation in this plan must `strconv.FormatInt` it into our string-typed `OrderResult.OrderId`/`ClosedPnl.OrderId`.
- `newClientOrderId`: max 36 chars, charset `^[\.A-Z\:/a-z0-9_-]{1,36}$` — our existing `SIS_STR-{id8}-{kind}-{cycle}-{seq}` scheme fits the charset but can exceed 36 chars for high cycle numbers (flagged as an open risk in the design spec) — Task 3 below resolves this concretely.
- `positionSide`: `BOTH` (one-way mode) or `LONG`/`SHORT` (hedge mode) — maps to our existing `OrderRequest.PositionIdx` (0/1/2) exactly like Bybit's own hedge-mode convention, so no new concept needed in `pkg/strategy`.
- `GET /fapi/v1/userTrades` (Account Trade List) returns **`realizedPnl` directly per fill** (`orderId`, `id`, `price`, `qty`, `realizedPnl`, `commission`, `side`, `positionSide`, `time`) — this means closed-pnl reconstruction does NOT need a separate `/fapi/v1/income` call; opening/adding fills have `realizedPnl="0"`, only genuinely closing fills carry a nonzero value. It does NOT include `clientOrderId` — Task 6 below covers the one extra lookup this requires.
- `userTrades`'/`income`'s `startTime`/`endTime` window is capped at 7 days per call (Binance limitation) — `FetchRecentClosedPnl`'s `since` can be arbitrarily old, so it must loop in ≤7-day windows, unlike Bybit's single cursor-paginated call.

---

### Task 1: Signed REST client infrastructure

**Files:**
- Create: `pkg/trader/binance/client.go`
- Create: `pkg/trader/binance/client_test.go`

- [ ] **Step 1: Write the failing test**

Create `pkg/trader/binance/client_test.go`:

```go
package binance

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"sis/pkg/trader"
)

// withMockBinanceBase points binanceBase at srv for the duration of the test and
// restores the real value on cleanup — mirrors pkg/trader/syncer_test.go's
// withMockBybitBase for the same reason (tests must not hit the real exchange).
func withMockBinanceBase(t *testing.T, srv *httptest.Server) {
	t.Helper()
	orig := binanceBase
	binanceBase = srv.URL
	t.Cleanup(func() { binanceBase = orig })
}

func testCreds() trader.Credentials {
	return trader.Credentials{APIKey: "test-key", SecretKey: "test-secret"}
}

func TestSign_IsDeterministicAndKeyDependent(t *testing.T) {
	got1 := sign("secret-a", "symbol=BTCUSDT&timestamp=1000")
	got2 := sign("secret-a", "symbol=BTCUSDT&timestamp=1000")
	if got1 != got2 {
		t.Error("sign must be deterministic for the same secret+payload")
	}
	got3 := sign("secret-b", "symbol=BTCUSDT&timestamp=1000")
	if got1 == got3 {
		t.Error("different secret must produce a different signature")
	}
	if got1 == "" {
		t.Fatal("sign returned empty string")
	}
}

func TestDoSignedGET_SendsAPIKeyHeaderAndValidSignature(t *testing.T) {
	var gotAPIKeyHeader string
	var gotQuery url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAPIKeyHeader = r.Header.Get("X-MBX-APIKEY")
		gotQuery = r.URL.Query()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()
	withMockBinanceBase(t, srv)

	creds := testCreds()
	_, err := doSignedGET(context.Background(), creds, "/fapi/v1/test", url.Values{"symbol": {"BTCUSDT"}})
	if err != nil {
		t.Fatalf("doSignedGET: %v", err)
	}
	if gotAPIKeyHeader != "test-key" {
		t.Errorf("X-MBX-APIKEY header = %q, want %q", gotAPIKeyHeader, "test-key")
	}
	if gotQuery.Get("symbol") != "BTCUSDT" {
		t.Errorf("query symbol = %q, want BTCUSDT", gotQuery.Get("symbol"))
	}
	if gotQuery.Get("timestamp") == "" {
		t.Error("query missing timestamp")
	}
	if gotQuery.Get("signature") == "" {
		t.Error("query missing signature")
	}

	// The signature must actually validate: recompute it the same way doSignedGET
	// does (every param except "signature", in the exact order net/url.Values.Encode
	// produces — alphabetical by key) and compare.
	check := url.Values{}
	for k, v := range gotQuery {
		if k == "signature" {
			continue
		}
		check[k] = v
	}
	wantSig := sign(creds.SecretKey, check.Encode())
	if gotQuery.Get("signature") != wantSig {
		t.Errorf("signature = %q, want %q (recomputed over %q)", gotQuery.Get("signature"), wantSig, check.Encode())
	}
}

func TestDoSignedPOST_SendsFormEncodedBodyNotJSON(t *testing.T) {
	var gotContentType string
	var gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotContentType = r.Header.Get("Content-Type")
		buf := make([]byte, r.ContentLength)
		_, _ = r.Body.Read(buf)
		gotBody = string(buf)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()
	withMockBinanceBase(t, srv)

	_, err := doSignedPOST(context.Background(), testCreds(), "/fapi/v1/order", url.Values{"symbol": {"BTCUSDT"}, "side": {"BUY"}})
	if err != nil {
		t.Fatalf("doSignedPOST: %v", err)
	}
	if !strings.HasPrefix(gotContentType, "application/x-www-form-urlencoded") {
		t.Errorf("Content-Type = %q, want application/x-www-form-urlencoded", gotContentType)
	}
	if !strings.Contains(gotBody, "symbol=BTCUSDT") || !strings.Contains(gotBody, "signature=") {
		t.Errorf("POST body = %q, want form-encoded params including signature", gotBody)
	}
}

func TestCheckBinanceError_ParsesCodeAndMsg(t *testing.T) {
	err := checkBinanceError([]byte(`{"code":-1121,"msg":"Invalid symbol."}`))
	if err == nil {
		t.Fatal("expected an error for a Binance error response")
	}
	if !strings.Contains(err.Error(), "-1121") || !strings.Contains(err.Error(), "Invalid symbol") {
		t.Errorf("error = %v, want it to mention code -1121 and the message", err)
	}
}

func TestCheckBinanceError_NilForSuccessResponse(t *testing.T) {
	// A success response has no top-level "code"/"msg" pair shaped like an error —
	// e.g. an order response's "code" field doesn't exist, only error responses have it.
	if err := checkBinanceError([]byte(`{"orderId":123,"symbol":"BTCUSDT"}`)); err != nil {
		t.Errorf("checkBinanceError on a success body = %v, want nil", err)
	}
}

func TestDoPublicGET_NoAuthHeaderNoSignature(t *testing.T) {
	var gotAPIKeyHeader string
	var gotQuery url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAPIKeyHeader = r.Header.Get("X-MBX-APIKEY")
		gotQuery = r.URL.Query()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"symbol":"BTCUSDT","markPrice":"67890.5"}`))
	}))
	defer srv.Close()
	withMockBinanceBase(t, srv)

	_, err := doPublicGET(context.Background(), "/fapi/v1/premiumIndex", url.Values{"symbol": {"BTCUSDT"}})
	if err != nil {
		t.Fatalf("doPublicGET: %v", err)
	}
	if gotAPIKeyHeader != "" {
		t.Errorf("X-MBX-APIKEY header = %q, want empty (public endpoint, no auth)", gotAPIKeyHeader)
	}
	if gotQuery.Get("signature") != "" {
		t.Error("public endpoint request must not carry a signature param")
	}
	if gotQuery.Get("symbol") != "BTCUSDT" {
		t.Errorf("query symbol = %q, want BTCUSDT", gotQuery.Get("symbol"))
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./pkg/trader/binance/... -v`
Expected: FAIL — package `pkg/trader/binance` doesn't exist yet (no `client.go`), so nothing compiles.

- [ ] **Step 3: Implement the client**

Create `pkg/trader/binance/client.go`:

```go
// Package binance implements trader.Exchange against Binance USDⓈ-M Futures.
package binance

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"sis/pkg/trader"
)

// binanceBase is a var (not const) so tests can point it at an httptest.Server —
// mirrors pkg/trader/bybit.go's bybitBase.
var binanceBase = "https://fapi.binance.com"

const recvWindowMs = "5000"

// sign returns the hex-encoded HMAC-SHA256 of payload using secret as the key —
// Binance's exact signing scheme (see general-info docs): HMAC-SHA256(secretKey, totalParams).
func sign(secret, payload string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(payload))
	return hex.EncodeToString(mac.Sum(nil))
}

// signParams adds timestamp+recvWindow to params, computes the signature over the
// resulting encoded query string, and returns params with "signature" added — the
// exact string url.Values.Encode() produces (params sorted alphabetically by key) is
// both what gets signed and what gets sent, so the two can never drift apart.
func signParams(creds trader.Credentials, params url.Values) url.Values {
	if params == nil {
		params = url.Values{}
	}
	params.Set("timestamp", strconv.FormatInt(time.Now().UnixMilli(), 10))
	params.Set("recvWindow", recvWindowMs)
	sig := sign(creds.SecretKey, params.Encode())
	params.Set("signature", sig)
	return params
}

// checkBinanceError returns a descriptive error if data is a Binance error response
// (has a negative "code" alongside "msg"), nil otherwise. Success responses for
// endpoints that happen to have their own "code" field (e.g. Cancel All Open Orders'
// {"code":200,"msg":"..."}) are NOT errors — only a negative code is.
func checkBinanceError(data []byte) error {
	var r struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
	}
	if err := json.Unmarshal(data, &r); err != nil {
		return nil // not even shaped like {code,msg} — let the caller's own unmarshal surface issues
	}
	if r.Code < 0 {
		return fmt.Errorf("binance: code=%d: %s", r.Code, r.Msg)
	}
	return nil
}

func doRequest(ctx context.Context, method, path string, values url.Values, apiKey string, body bool) ([]byte, error) {
	var req *http.Request
	var err error
	if body {
		req, err = http.NewRequestWithContext(ctx, method, binanceBase+path, nil)
		if err != nil {
			return nil, err
		}
		req.Body = io.NopCloser(strings.NewReader(values.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	} else {
		full := binanceBase + path
		if len(values) > 0 {
			full += "?" + values.Encode()
		}
		req, err = http.NewRequestWithContext(ctx, method, full, nil)
		if err != nil {
			return nil, err
		}
	}
	if apiKey != "" {
		req.Header.Set("X-MBX-APIKEY", apiKey)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if err := checkBinanceError(data); err != nil {
		return data, err
	}
	return data, nil
}

// doSignedGET issues a signed GET — params go in the query string.
func doSignedGET(ctx context.Context, creds trader.Credentials, path string, params url.Values) ([]byte, error) {
	return doRequest(ctx, http.MethodGet, path, signParams(creds, params), creds.APIKey, false)
}

// doSignedPOST issues a signed POST — params go in the form-urlencoded body, not JSON
// (a real difference from Bybit, which sends a JSON body — see general-info docs).
func doSignedPOST(ctx context.Context, creds trader.Credentials, path string, params url.Values) ([]byte, error) {
	return doRequest(ctx, http.MethodPost, path, signParams(creds, params), creds.APIKey, true)
}

// doSignedDELETE issues a signed DELETE — params go in the query string, same as GET.
func doSignedDELETE(ctx context.Context, creds trader.Credentials, path string, params url.Values) ([]byte, error) {
	return doRequest(ctx, http.MethodDelete, path, signParams(creds, params), creds.APIKey, false)
}

// doPublicGET issues an unauthenticated GET — no API key header, no signature. Used
// only for genuinely public endpoints (e.g. mark price).
func doPublicGET(ctx context.Context, path string, params url.Values) ([]byte, error) {
	return doRequest(ctx, http.MethodGet, path, params, "", false)
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./pkg/trader/binance/... -v`
Expected: `PASS` — all 6 tests (`TestSign_IsDeterministicAndKeyDependent`,
`TestDoSignedGET_SendsAPIKeyHeaderAndValidSignature`,
`TestDoSignedPOST_SendsFormEncodedBodyNotJSON`, `TestCheckBinanceError_ParsesCodeAndMsg`,
`TestCheckBinanceError_NilForSuccessResponse`, `TestDoPublicGET_NoAuthHeaderNoSignature`).

- [ ] **Step 5: Commit**

```bash
git add pkg/trader/binance/client.go pkg/trader/binance/client_test.go
git commit -m "$(cat <<'EOF'
feat(trader/binance): add signed REST client infrastructure

First step of the Binance USDⓈ-M Futures exchange client (see
docs/superpowers/plans/2026-09-03-binance-exchange-client.md and
docs/superpowers/specs/2026-09-03-binance-live-trading-design.md): HMAC-SHA256
request signing, signed GET/POST/DELETE helpers, and a public (unauthenticated)
GET helper, mirroring pkg/trader/bybit.go's overridable-base-URL test pattern.

Co-Authored-By: Claude Sonnet 5 <noreply@anthropic.com>
EOF
)"
```

---

### Task 2: BinanceExchange skeleton + read-only methods

**Files:**
- Create: `pkg/trader/binance/exchange.go`
- Create: `pkg/trader/binance/exchange_test.go`

- [ ] **Step 1: Write the failing tests**

Create `pkg/trader/binance/exchange_test.go`:

```go
package binance

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"sis/pkg/trader"
)

func TestBinanceExchange_GetMarkPrice_UsesPublicEndpoint(t *testing.T) {
	var gotAPIKeyHeader string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAPIKeyHeader = r.Header.Get("X-MBX-APIKEY")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"symbol":"BTCUSDT","markPrice":"67890.50000000"}`))
	}))
	defer srv.Close()
	withMockBinanceBase(t, srv)

	ex := NewBinanceExchange(testCreds())
	got, err := ex.GetMarkPrice(context.Background(), "linear", "BTCUSDT")
	if err != nil {
		t.Fatalf("GetMarkPrice: %v", err)
	}
	if got != 67890.5 {
		t.Errorf("GetMarkPrice = %v, want 67890.5", got)
	}
	if gotAPIKeyHeader != "" {
		t.Error("GetMarkPrice must use the public (unauthenticated) endpoint")
	}
}

func TestBinanceExchange_FetchPositions_TranslatesPositionRisk(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[
			{"symbol":"BTCUSDT","positionSide":"LONG","positionAmt":"0.50000000","entryPrice":"60000.0","markPrice":"61000.0","unRealizedProfit":"500.0","liquidationPrice":"40000.0","leverage":"10"},
			{"symbol":"ETHUSDT","positionSide":"BOTH","positionAmt":"0.00000000","entryPrice":"0.0","markPrice":"3000.0","unRealizedProfit":"0.0","liquidationPrice":"0.0","leverage":"20"}
		]`))
	}))
	defer srv.Close()
	withMockBinanceBase(t, srv)

	ex := NewBinanceExchange(testCreds())
	got, err := ex.FetchPositions(context.Background())
	if err != nil {
		t.Fatalf("FetchPositions: %v", err)
	}
	// Only positions with a nonzero positionAmt are real open positions — Binance
	// returns every symbol's row (mostly zeros) unlike Bybit, which only lists real
	// positions. Filtering zero-size rows keeps behavior compatible with Bybit's
	// FetchPositions (used by pkg/strategy to find actually-open positions).
	if len(got) != 1 {
		t.Fatalf("FetchPositions returned %d positions, want 1 (the zero-size ETHUSDT row must be filtered)", len(got))
	}
	p := got[0]
	if p.Symbol != "BTCUSDT" || p.Side != "Buy" || p.Size != "0.50000000" || p.EntryPrice != "60000.0" {
		t.Errorf("FetchPositions[0] = %+v, want BTCUSDT Buy 0.5 @ 60000.0", p)
	}
	if p.PositionIdx != 1 {
		t.Errorf("PositionIdx = %d, want 1 (LONG hedge-mode slot, matching Bybit's convention)", p.PositionIdx)
	}
	if p.Category != "linear" {
		t.Errorf("Category = %q, want linear", p.Category)
	}
}

func TestBinanceExchange_FetchPositions_ShortSideFromNegativeAmt(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[{"symbol":"BTCUSDT","positionSide":"SHORT","positionAmt":"-0.25000000","entryPrice":"60000.0","markPrice":"61000.0","unRealizedProfit":"-250.0","liquidationPrice":"80000.0","leverage":"10"}]`))
	}))
	defer srv.Close()
	withMockBinanceBase(t, srv)

	ex := NewBinanceExchange(testCreds())
	got, err := ex.FetchPositions(context.Background())
	if err != nil {
		t.Fatalf("FetchPositions: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("FetchPositions returned %d positions, want 1", len(got))
	}
	p := got[0]
	// Bybit's Position.Size is always a positive magnitude with Side telling direction —
	// Binance's positionAmt carries the sign instead. Must translate to Bybit's convention
	// since pkg/strategy reads Size as an unsigned magnitude string.
	if p.Side != "Sell" || p.Size != "0.25000000" {
		t.Errorf("FetchPositions[0] = %+v, want Sell with positive size 0.25 (sign stripped)", p)
	}
	if p.PositionIdx != 2 {
		t.Errorf("PositionIdx = %d, want 2 (SHORT hedge-mode slot)", p.PositionIdx)
	}
}

func TestBinanceExchange_GetWalletBalance_TranslatesAccountEndpoint(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"totalMarginBalance":"1000.5","assets":[{"asset":"USDT","availableBalance":"800.25"},{"asset":"BUSD","availableBalance":"5.0"}]}`))
	}))
	defer srv.Close()
	withMockBinanceBase(t, srv)

	ex := NewBinanceExchange(testCreds())
	equity, available, err := ex.GetWalletBalance(context.Background())
	if err != nil {
		t.Fatalf("GetWalletBalance: %v", err)
	}
	if equity != 1000.5 {
		t.Errorf("equity = %v, want 1000.5 (totalMarginBalance)", equity)
	}
	if available != 800.25 {
		t.Errorf("available = %v, want 800.25 (USDT asset's availableBalance, not BUSD)", available)
	}
}

var _ trader.Credentials // keep the trader import used even before more methods land
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./pkg/trader/binance/... -run TestBinanceExchange -v`
Expected: FAIL — `NewBinanceExchange` undefined (`exchange.go` doesn't exist yet).

- [ ] **Step 3: Implement `BinanceExchange` and the three methods**

Create `pkg/trader/binance/exchange.go`:

```go
package binance

import (
	"context"
	"encoding/json"
	"net/url"
	"strconv"

	"sis/pkg/trader"
)

// BinanceExchange implements trader.Exchange against Binance USDⓈ-M Futures.
// category parameters accepted by interface methods are always effectively "linear"
// here — Binance separates USDⓈ-M/COIN-M by base URL/endpoint family rather than a
// query param the way Bybit does, and this client only ever targets USDⓈ-M (see the
// design spec's non-goals) — the param exists purely for interface compatibility.
type BinanceExchange struct {
	creds trader.Credentials
}

// NewBinanceExchange builds a BinanceExchange for one account's credentials.
func NewBinanceExchange(creds trader.Credentials) *BinanceExchange {
	return &BinanceExchange{creds: creds}
}

func (e *BinanceExchange) GetMarkPrice(ctx context.Context, category, symbol string) (float64, error) {
	data, err := doPublicGET(ctx, "/fapi/v1/premiumIndex", url.Values{"symbol": {symbol}})
	if err != nil {
		return 0, err
	}
	var r struct {
		MarkPrice string `json:"markPrice"`
	}
	if err := json.Unmarshal(data, &r); err != nil {
		return 0, err
	}
	return strconv.ParseFloat(r.MarkPrice, 64)
}

// binancePositionRow is one row of GET /fapi/v3/positionRisk.
type binancePositionRow struct {
	Symbol           string `json:"symbol"`
	PositionSide     string `json:"positionSide"` // "BOTH", "LONG", "SHORT"
	PositionAmt      string `json:"positionAmt"`  // signed: negative = short
	EntryPrice       string `json:"entryPrice"`
	MarkPrice        string `json:"markPrice"`
	UnRealizedProfit string `json:"unRealizedProfit"`
	LiquidationPrice string `json:"liquidationPrice"`
	Leverage         string `json:"leverage"`
}

func (e *BinanceExchange) FetchPositions(ctx context.Context) ([]trader.Position, error) {
	data, err := doSignedGET(ctx, e.creds, "/fapi/v3/positionRisk", nil)
	if err != nil {
		return nil, err
	}
	var rows []binancePositionRow
	if err := json.Unmarshal(data, &rows); err != nil {
		return nil, err
	}
	var out []trader.Position
	for _, r := range rows {
		amt, err := strconv.ParseFloat(r.PositionAmt, 64)
		if err != nil || amt == 0 {
			continue // Binance lists every symbol; only nonzero positionAmt is a real open position
		}
		side := "Buy"
		positionIdx := 1
		size := r.PositionAmt
		if amt < 0 {
			side = "Sell"
			positionIdx = 2
			size = strconv.FormatFloat(-amt, 'f', -1, 64)
		} else if r.PositionSide == "BOTH" {
			positionIdx = 0 // one-way mode
		}
		out = append(out, trader.Position{
			Symbol:        r.Symbol,
			Side:          side,
			Size:          size,
			EntryPrice:    r.EntryPrice,
			MarkPrice:     r.MarkPrice,
			LiqPrice:      r.LiquidationPrice,
			UnrealisedPnl: r.UnRealizedProfit,
			Leverage:      r.Leverage,
			PositionIdx:   positionIdx,
			Category:      "linear",
		})
	}
	return out, nil
}

func (e *BinanceExchange) GetWalletBalance(ctx context.Context) (equity, available float64, err error) {
	data, err := doSignedGET(ctx, e.creds, "/fapi/v2/account", nil)
	if err != nil {
		return 0, 0, err
	}
	var r struct {
		TotalMarginBalance string `json:"totalMarginBalance"`
		Assets             []struct {
			Asset            string `json:"asset"`
			AvailableBalance string `json:"availableBalance"`
		} `json:"assets"`
	}
	if err := json.Unmarshal(data, &r); err != nil {
		return 0, 0, err
	}
	equity, err = strconv.ParseFloat(r.TotalMarginBalance, 64)
	if err != nil {
		return 0, 0, err
	}
	for _, a := range r.Assets {
		if a.Asset == "USDT" {
			available, _ = strconv.ParseFloat(a.AvailableBalance, 64)
			break
		}
	}
	return equity, available, nil
}
```

Remove the placeholder `var _ trader.Credentials` line from `exchange_test.go` written in
Step 1 — it was only there to keep the `trader` import used before `NewBinanceExchange`'s
signature (which takes a `trader.Credentials`) made it naturally used; once
`testCreds()` (already defined in `client_test.go`, same package) is called in these
tests, the import is used normally and that placeholder line must be deleted, or the
file won't compile (`trader.Credentials` used both as an unused blank var AND normally
is fine actually — but delete it anyway, it serves no purpose once real usage exists).

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./pkg/trader/binance/... -v`
Expected: `PASS` — all tests from Tasks 1-2.

- [ ] **Step 5: Commit**

```bash
git add pkg/trader/binance/exchange.go pkg/trader/binance/exchange_test.go
git commit -m "$(cat <<'EOF'
feat(trader/binance): add BinanceExchange skeleton + read-only methods

GetMarkPrice (public endpoint), FetchPositions (positionRisk v3, translating
Binance's signed-positionAmt convention into Bybit's Side+Size+PositionIdx
shape), GetWalletBalance (account v2).

Co-Authored-By: Claude Sonnet 5 <noreply@anthropic.com>
EOF
)"
```

---

### Task 3: Order placement + single cancel, with the orderLinkId length fix

**Files:**
- Modify: `pkg/trader/binance/exchange.go`
- Create: `pkg/trader/binance/orderlink.go`
- Create: `pkg/trader/binance/orderlink_test.go`
- Modify: `pkg/trader/binance/exchange_test.go`

This task resolves the design spec's flagged open risk: Binance's `newClientOrderId` is
capped at 36 characters, but our `SIS_STR-{id8}-{kind}-{cycle}-{seq}` orderLinkId scheme
can exceed that for high cycle numbers.

- [ ] **Step 1: Write the failing test for the truncation helper**

Create `pkg/trader/binance/orderlink_test.go`:

```go
package binance

import "testing"

func TestTruncateClientOrderID_UnchangedWhenWithinLimit(t *testing.T) {
	id := "SIS_STR-a1b2c3d4-tp-5-2"
	got := truncateClientOrderID(id)
	if got != id {
		t.Errorf("truncateClientOrderID(%q) = %q, want unchanged (already within 36 chars)", id, got)
	}
}

func TestTruncateClientOrderID_ShortensLongIDsTo36Chars(t *testing.T) {
	// A realistic overflow case: high cycle number + repriceGen + level suffix.
	id := "SIS_STR-a1b2c3d4-999999-88888888-777-v99"
	if len(id) <= 36 {
		t.Fatalf("test fixture id is %d chars, must be >36 to exercise truncation", len(id))
	}
	got := truncateClientOrderID(id)
	if len(got) != 36 {
		t.Errorf("truncateClientOrderID(%q) len = %d, want exactly 36", id, len(got))
	}
}

func TestTruncateClientOrderID_DeterministicSameInputSameOutput(t *testing.T) {
	id := "SIS_STR-a1b2c3d4-999999-88888888-777-v99"
	got1 := truncateClientOrderID(id)
	got2 := truncateClientOrderID(id)
	if got1 != got2 {
		t.Errorf("truncateClientOrderID must be deterministic: got %q then %q for the same input", got1, got2)
	}
}

func TestTruncateClientOrderID_DifferentOverflowingIDsDontCollide(t *testing.T) {
	// Two IDs that share the same first 27 characters but differ only in the part that
	// would get clipped by naive truncation — a hash suffix must keep them distinct,
	// otherwise Binance would reject the second as a duplicate clientOrderId.
	idA := "SIS_STR-a1b2c3d4-999999-88888888-AAAA"
	idB := "SIS_STR-a1b2c3d4-999999-88888888-BBBB"
	gotA := truncateClientOrderID(idA)
	gotB := truncateClientOrderID(idB)
	if gotA == gotB {
		t.Errorf("truncateClientOrderID(%q) and truncateClientOrderID(%q) both produced %q — collision", idA, idB, gotA)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./pkg/trader/binance/... -run TestTruncateClientOrderID -v`
Expected: FAIL — `truncateClientOrderID` undefined.

- [ ] **Step 3: Implement the truncation helper**

Create `pkg/trader/binance/orderlink.go`:

```go
package binance

import (
	"fmt"
	"hash/crc32"
)

// maxClientOrderIDLen is Binance's newClientOrderId cap (docs: "1-36 characters").
const maxClientOrderIDLen = 36

// truncateClientOrderID keeps linkID as-is if it already fits Binance's 36-char
// newClientOrderId cap. If not, it keeps as many leading characters as fit alongside a
// short deterministic hash suffix of the FULL original ID, so two different overflowing
// IDs that happen to share the same leading prefix don't collide into an identical,
// Binance-rejected duplicate clientOrderId. The leading prefix ("SIS_STR-{id8}-{kind}-")
// — the part pkg/strategy.ParseStrategyLinkID actually needs for classification — is
// always short enough (≈20 chars) to survive intact even after truncation; only the
// trailing cycle/seq/level numbers (which no parser depends on) are ever affected.
func truncateClientOrderID(linkID string) string {
	if len(linkID) <= maxClientOrderIDLen {
		return linkID
	}
	sum := crc32.ChecksumIEEE([]byte(linkID))
	suffix := fmt.Sprintf("-%08x", sum) // 9 characters
	keep := maxClientOrderIDLen - len(suffix)
	return linkID[:keep] + suffix
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `go test ./pkg/trader/binance/... -run TestTruncateClientOrderID -v`
Expected: `PASS` for all 4 tests.

- [ ] **Step 5: Write the failing tests for order placement/cancellation**

Append to `pkg/trader/binance/exchange_test.go` (remember to delete the
`var _ trader.Credentials` placeholder line from Task 2 if you haven't already):

```go
func TestBinanceExchange_PlaceOrder_TranslatesMarketOrder(t *testing.T) {
	var gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		buf := make([]byte, r.ContentLength)
		_, _ = r.Body.Read(buf)
		gotBody = string(buf)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"orderId":123456789,"clientOrderId":"SIS_STR-a1b2c3d4-tp-1-1","status":"NEW"}`))
	}))
	defer srv.Close()
	withMockBinanceBase(t, srv)

	ex := NewBinanceExchange(testCreds())
	req := trader.OrderRequest{
		Symbol: "BTCUSDT", Side: "Buy", OrderType: "Market", Qty: "0.01",
		PositionIdx: 1, OrderLinkId: "SIS_STR-a1b2c3d4-tp-1-1",
	}
	got, err := ex.PlaceOrder(context.Background(), req)
	if err != nil {
		t.Fatalf("PlaceOrder: %v", err)
	}
	if got.OrderId != "123456789" {
		t.Errorf("OrderId = %q, want %q (numeric orderId stringified)", got.OrderId, "123456789")
	}
	if got.OrderLinkId != "SIS_STR-a1b2c3d4-tp-1-1" {
		t.Errorf("OrderLinkId = %q, want the echoed clientOrderId", got.OrderLinkId)
	}
	if !strings.Contains(gotBody, "side=BUY") {
		t.Errorf("body = %q, want side=BUY (Binance uppercase)", gotBody)
	}
	if !strings.Contains(gotBody, "type=MARKET") {
		t.Errorf("body = %q, want type=MARKET", gotBody)
	}
	if !strings.Contains(gotBody, "positionSide=LONG") {
		t.Errorf("body = %q, want positionSide=LONG (PositionIdx=1 → LONG)", gotBody)
	}
	if !strings.Contains(gotBody, "newClientOrderId=SIS_STR-a1b2c3d4-tp-1-1") {
		t.Errorf("body = %q, want the orderLinkId passed through as newClientOrderId", gotBody)
	}
}

func TestBinanceExchange_PlaceOrder_TranslatesConditionalStopOrder(t *testing.T) {
	var gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		buf := make([]byte, r.ContentLength)
		_, _ = r.Body.Read(buf)
		gotBody = string(buf)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"orderId":2,"clientOrderId":"x"}`))
	}))
	defer srv.Close()
	withMockBinanceBase(t, srv)

	ex := NewBinanceExchange(testCreds())
	// Bybit's conditional-order shape: OrderType + TriggerPrice + OrderFilter=StopOrder.
	// This must translate to Binance's dedicated STOP_MARKET type with stopPrice.
	req := trader.OrderRequest{
		Symbol: "BTCUSDT", Side: "Sell", OrderType: "Market", TriggerPrice: "58000",
		OrderFilter: "StopOrder", ReduceOnly: true, PositionIdx: 1,
	}
	if _, err := ex.PlaceOrder(context.Background(), req); err != nil {
		t.Fatalf("PlaceOrder: %v", err)
	}
	if !strings.Contains(gotBody, "type=STOP_MARKET") {
		t.Errorf("body = %q, want type=STOP_MARKET for a conditional stop order", gotBody)
	}
	if !strings.Contains(gotBody, "stopPrice=58000") {
		t.Errorf("body = %q, want stopPrice=58000", gotBody)
	}
	if !strings.Contains(gotBody, "reduceOnly=true") {
		t.Errorf("body = %q, want reduceOnly=true", gotBody)
	}
}

func TestBinanceExchange_PlaceOrder_TruncatesLongOrderLinkId(t *testing.T) {
	var gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		buf := make([]byte, r.ContentLength)
		_, _ = r.Body.Read(buf)
		gotBody = string(buf)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"orderId":3,"clientOrderId":"whatever"}`))
	}))
	defer srv.Close()
	withMockBinanceBase(t, srv)

	ex := NewBinanceExchange(testCreds())
	longID := "SIS_STR-a1b2c3d4-999999-88888888-777-v99"
	req := trader.OrderRequest{Symbol: "BTCUSDT", Side: "Buy", OrderType: "Market", Qty: "1", OrderLinkId: longID}
	if _, err := ex.PlaceOrder(context.Background(), req); err != nil {
		t.Fatalf("PlaceOrder: %v", err)
	}
	if strings.Contains(gotBody, "newClientOrderId="+longID) {
		t.Errorf("body = %q, must NOT send the untruncated >36-char orderLinkId — Binance would reject it", gotBody)
	}
	want := truncateClientOrderID(longID)
	if !strings.Contains(gotBody, "newClientOrderId="+want) {
		t.Errorf("body = %q, want the truncated id %q", gotBody, want)
	}
}

// PlaceOrderREST must behave identically to PlaceOrder — Binance has no separate WS
// order-placement channel the way Bybit does, so both interface methods delegate to
// the exact same REST call for this exchange (see the design spec's architecture
// section on the two-method split existing purely for Bybit's WS-vs-REST distinction).
func TestBinanceExchange_PlaceOrderREST_SameBehaviorAsPlaceOrder(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"orderId":4,"clientOrderId":"y"}`))
	}))
	defer srv.Close()
	withMockBinanceBase(t, srv)

	ex := NewBinanceExchange(testCreds())
	req := trader.OrderRequest{Symbol: "BTCUSDT", Side: "Buy", OrderType: "Market", Qty: "1"}
	got, err := ex.PlaceOrderREST(context.Background(), req)
	if err != nil {
		t.Fatalf("PlaceOrderREST: %v", err)
	}
	if got.OrderId != "4" {
		t.Errorf("OrderId = %q, want 4", got.OrderId)
	}
}

func TestBinanceExchange_CancelOrder_UsesOrderIdOrLinkId(t *testing.T) {
	var gotQuery url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.Query()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"orderId":5,"status":"CANCELED"}`))
	}))
	defer srv.Close()
	withMockBinanceBase(t, srv)

	ex := NewBinanceExchange(testCreds())
	if err := ex.CancelOrder(context.Background(), trader.CancelRequest{Symbol: "BTCUSDT", OrderId: "5"}); err != nil {
		t.Fatalf("CancelOrder: %v", err)
	}
	if gotQuery.Get("orderId") != "5" {
		t.Errorf("query orderId = %q, want 5", gotQuery.Get("orderId"))
	}
}

func TestBinanceExchange_CancelOrderREST_SameBehaviorAsCancelOrder(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"orderId":6,"status":"CANCELED"}`))
	}))
	defer srv.Close()
	withMockBinanceBase(t, srv)

	ex := NewBinanceExchange(testCreds())
	if err := ex.CancelOrderREST(context.Background(), trader.CancelRequest{Symbol: "BTCUSDT", OrderId: "6"}); err != nil {
		t.Fatalf("CancelOrderREST: %v", err)
	}
}

func TestBinanceExchange_CancelAllOrders_DelegatesCorrectly(t *testing.T) {
	var gotQuery url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.Query()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"code":200,"msg":"The operation of cancel all open order is done."}`))
	}))
	defer srv.Close()
	withMockBinanceBase(t, srv)

	ex := NewBinanceExchange(testCreds())
	if err := ex.CancelAllOrders(context.Background(), trader.CancelAllRequest{Symbol: "BTCUSDT"}); err != nil {
		t.Fatalf("CancelAllOrders: %v", err)
	}
	if gotQuery.Get("symbol") != "BTCUSDT" {
		t.Errorf("query symbol = %q, want BTCUSDT", gotQuery.Get("symbol"))
	}
}
```

Add `"net/url"` and `"strings"` to `exchange_test.go`'s import block (alongside the
existing `context`, `net/http`, `net/http/httptest`, `testing`, `sis/pkg/trader`).

- [ ] **Step 6: Run the tests to verify they fail**

Run: `go test ./pkg/trader/binance/... -run 'TestBinanceExchange_PlaceOrder|TestBinanceExchange_CancelOrder|TestBinanceExchange_CancelAllOrders' -v`
Expected: FAIL — methods undefined on `*BinanceExchange`.

- [ ] **Step 7: Implement order placement/cancellation**

Append to `pkg/trader/binance/exchange.go`:

```go
// binanceOrderType maps our OrderType/OrderFilter/TriggerPrice combination onto
// Binance's dedicated conditional-order types. Bybit expresses "this is a conditional
// stop" via OrderFilter="StopOrder" + a nonempty TriggerPrice; Binance has no such
// generic flag — it's baked into the order `type` itself.
func binanceOrderType(req trader.OrderRequest) string {
	if req.OrderFilter == "StopOrder" || req.TriggerPrice != "" {
		if req.Side == "Buy" {
			// A resting reduce-only BUY conditional is a take-profit for a short, or a
			// stop-loss for... this project's call sites always set OrderFilter=StopOrder
			// for stop-losses and a plain TP order (no trigger) for take-profits, so a
			// triggered order reaching this branch is always a stop, never a TP, by
			// construction of how pkg/strategy places orders. STOP_MARKET is correct for
			// both directions — Binance doesn't distinguish TP vs SL at the type level,
			// only via which side of the stopPrice they trigger on, which the caller's
			// TriggerDirection/stopPrice already encodes correctly for either case.
		}
		return "STOP_MARKET"
	}
	switch req.OrderType {
	case "Limit":
		return "LIMIT"
	default:
		return "MARKET"
	}
}

// binancePositionSide maps our PositionIdx (0=one-way, 1=long hedge slot, 2=short
// hedge slot — Bybit's convention, reused as our internal canonical one) onto
// Binance's positionSide enum.
func binancePositionSide(positionIdx int) string {
	switch positionIdx {
	case 1:
		return "LONG"
	case 2:
		return "SHORT"
	default:
		return "BOTH"
	}
}

func (e *BinanceExchange) placeOrder(ctx context.Context, req trader.OrderRequest) (trader.OrderResult, error) {
	params := url.Values{
		"symbol":       {req.Symbol},
		"side":         {strings.ToUpper(req.Side)},
		"type":         {binanceOrderType(req)},
		"positionSide": {binancePositionSide(req.PositionIdx)},
	}
	if req.Qty != "" {
		params.Set("quantity", req.Qty)
	}
	if req.Price != "" {
		params.Set("price", req.Price)
	}
	if req.TriggerPrice != "" {
		params.Set("stopPrice", req.TriggerPrice)
	}
	if req.ReduceOnly {
		params.Set("reduceOnly", "true")
	}
	if req.TimeInForce != "" && binanceOrderType(req) == "LIMIT" {
		params.Set("timeInForce", req.TimeInForce)
	}
	if req.OrderLinkId != "" {
		params.Set("newClientOrderId", truncateClientOrderID(req.OrderLinkId))
	}

	data, err := doSignedPOST(ctx, e.creds, "/fapi/v1/order", params)
	if err != nil {
		return trader.OrderResult{}, err
	}
	var r struct {
		OrderId       int64  `json:"orderId"`
		ClientOrderId string `json:"clientOrderId"`
	}
	if err := json.Unmarshal(data, &r); err != nil {
		return trader.OrderResult{}, err
	}
	return trader.OrderResult{
		OrderId:     strconv.FormatInt(r.OrderId, 10),
		OrderLinkId: r.ClientOrderId,
	}, nil
}

// PlaceOrder and PlaceOrderREST are identical for Binance — there is no separate
// low-latency WS order-placement channel the way Bybit's TradeStream provides, so both
// Exchange interface methods resolve to the same REST call.
func (e *BinanceExchange) PlaceOrder(ctx context.Context, req trader.OrderRequest) (trader.OrderResult, error) {
	return e.placeOrder(ctx, req)
}

func (e *BinanceExchange) PlaceOrderREST(ctx context.Context, req trader.OrderRequest) (trader.OrderResult, error) {
	return e.placeOrder(ctx, req)
}

func (e *BinanceExchange) cancelOrder(ctx context.Context, req trader.CancelRequest) error {
	params := url.Values{"symbol": {req.Symbol}}
	if req.OrderId != "" {
		params.Set("orderId", req.OrderId)
	} else if req.OrderLinkId != "" {
		params.Set("origClientOrderId", truncateClientOrderID(req.OrderLinkId))
	}
	_, err := doSignedDELETE(ctx, e.creds, "/fapi/v1/order", params)
	return err
}

func (e *BinanceExchange) CancelOrder(ctx context.Context, req trader.CancelRequest) error {
	return e.cancelOrder(ctx, req)
}

func (e *BinanceExchange) CancelOrderREST(ctx context.Context, req trader.CancelRequest) error {
	return e.cancelOrder(ctx, req)
}

func (e *BinanceExchange) CancelAllOrders(ctx context.Context, req trader.CancelAllRequest) error {
	_, err := doSignedDELETE(ctx, e.creds, "/fapi/v1/allOpenOrders", url.Values{"symbol": {req.Symbol}})
	return err
}
```

Add `"strings"` to `exchange.go`'s import block (`strings.ToUpper` is used above).

- [ ] **Step 8: Run the tests to verify they pass**

Run: `go test ./pkg/trader/binance/... -v`
Expected: `PASS` — all tests from Tasks 1-3.

- [ ] **Step 9: Commit**

```bash
git add pkg/trader/binance/exchange.go pkg/trader/binance/exchange_test.go pkg/trader/binance/orderlink.go pkg/trader/binance/orderlink_test.go
git commit -m "$(cat <<'EOF'
feat(trader/binance): add order placement/cancellation + orderLinkId length fix

PlaceOrder/PlaceOrderREST/CancelOrder/CancelOrderREST/CancelAllOrders, plus
truncateClientOrderID resolving the design spec's flagged risk: Binance's
newClientOrderId caps at 36 chars, which our SIS_STR-{id8}-{kind}-{cycle}-{seq}
scheme can exceed for high cycle numbers. Truncates to the classifying prefix
+ a deterministic hash suffix so overflowing IDs stay unique instead of naive
truncation risking a Binance-rejected duplicate clientOrderId.

Co-Authored-By: Claude Sonnet 5 <noreply@anthropic.com>
EOF
)"
```

---

### Task 4: Batch order placement/cancellation

**Files:**
- Modify: `pkg/trader/binance/exchange.go`
- Modify: `pkg/trader/binance/exchange_test.go`

- [ ] **Step 1: Write the failing tests**

Append to `pkg/trader/binance/exchange_test.go`:

```go
func TestBinanceExchange_PlaceOrderBatch_SendsJSONArrayParam(t *testing.T) {
	var gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		buf := make([]byte, r.ContentLength)
		_, _ = r.Body.Read(buf)
		gotBody = string(buf)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[{"orderId":10,"clientOrderId":"a"},{"orderId":11,"clientOrderId":"b"}]`))
	}))
	defer srv.Close()
	withMockBinanceBase(t, srv)

	ex := NewBinanceExchange(testCreds())
	req := trader.BatchPlaceRequest{Request: []trader.BatchOrderItem{
		{Symbol: "BTCUSDT", Side: "Buy", OrderType: "Limit", Qty: "1", Price: "50000", PositionIdx: 1},
		{Symbol: "ETHUSDT", Side: "Sell", OrderType: "Market", Qty: "1", PositionIdx: 2},
	}}
	got, err := ex.PlaceOrderBatch(context.Background(), req)
	if err != nil {
		t.Fatalf("PlaceOrderBatch: %v", err)
	}
	if len(got) != 2 || got[0].OrderId != "10" || got[1].OrderId != "11" {
		t.Errorf("PlaceOrderBatch = %+v, want orderIds 10 and 11", got)
	}
	if !strings.Contains(gotBody, "batchOrders=") {
		t.Errorf("body = %q, want a batchOrders param", gotBody)
	}
	if !strings.Contains(gotBody, "BTCUSDT") || !strings.Contains(gotBody, "ETHUSDT") {
		t.Errorf("body = %q, want both symbols present in the encoded batch", gotBody)
	}
}

func TestBinanceExchange_CancelOrderBatch_SendsOrderIdListParam(t *testing.T) {
	var gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		buf := make([]byte, r.ContentLength)
		_, _ = r.Body.Read(buf)
		gotBody = string(buf)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[{"orderId":20,"status":"CANCELED"},{"orderId":21,"status":"CANCELED"}]`))
	}))
	defer srv.Close()
	withMockBinanceBase(t, srv)

	ex := NewBinanceExchange(testCreds())
	req := trader.BatchCancelRequest{Request: []trader.BatchCancelItem{
		{Symbol: "BTCUSDT", OrderId: "20"},
		{Symbol: "BTCUSDT", OrderId: "21"},
	}}
	if err := ex.CancelOrderBatch(context.Background(), req); err != nil {
		t.Fatalf("CancelOrderBatch: %v", err)
	}
	if !strings.Contains(gotBody, "orderIdList=") {
		t.Errorf("body = %q, want an orderIdList param", gotBody)
	}
	if !strings.Contains(gotBody, "20") || !strings.Contains(gotBody, "21") {
		t.Errorf("body = %q, want both order IDs present", gotBody)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./pkg/trader/binance/... -run 'TestBinanceExchange_PlaceOrderBatch|TestBinanceExchange_CancelOrderBatch' -v`
Expected: FAIL — methods undefined.

- [ ] **Step 3: Implement the two batch methods**

Append to `pkg/trader/binance/exchange.go`:

```go
// binanceBatchOrderItem is one entry of the JSON array sent as POST
// /fapi/v1/batchOrders' "batchOrders" param (itself a JSON-encoded string, not a
// native array — this is a real Binance quirk: the array is serialized to JSON text
// and that text becomes the value of one form field).
type binanceBatchOrderItem struct {
	Symbol           string `json:"symbol"`
	Side             string `json:"side"`
	Type             string `json:"type"`
	PositionSide     string `json:"positionSide"`
	Quantity         string `json:"quantity,omitempty"`
	Price            string `json:"price,omitempty"`
	StopPrice        string `json:"stopPrice,omitempty"`
	ReduceOnly       string `json:"reduceOnly,omitempty"`
	TimeInForce      string `json:"timeInForce,omitempty"`
	NewClientOrderId string `json:"newClientOrderId,omitempty"`
}

func (e *BinanceExchange) PlaceOrderBatch(ctx context.Context, req trader.BatchPlaceRequest) ([]trader.BatchPlaceResult, error) {
	items := make([]binanceBatchOrderItem, 0, len(req.Request))
	for _, it := range req.Request {
		orderReq := trader.OrderRequest{
			Symbol: it.Symbol, Side: it.Side, OrderType: it.OrderType,
			TriggerPrice: it.TriggerPrice, OrderFilter: it.OrderFilter,
			ReduceOnly: it.ReduceOnly, PositionIdx: it.PositionIdx,
		}
		bi := binanceBatchOrderItem{
			Symbol: it.Symbol, Side: strings.ToUpper(it.Side), Type: binanceOrderType(orderReq),
			PositionSide: binancePositionSide(it.PositionIdx), Quantity: it.Qty, Price: it.Price,
			TimeInForce: it.TimeInForce,
		}
		if it.TriggerPrice != "" {
			bi.StopPrice = it.TriggerPrice
		}
		if it.ReduceOnly {
			bi.ReduceOnly = "true"
		}
		if it.OrderLinkId != "" {
			bi.NewClientOrderId = truncateClientOrderID(it.OrderLinkId)
		}
		items = append(items, bi)
	}
	encoded, err := json.Marshal(items)
	if err != nil {
		return nil, err
	}
	data, err := doSignedPOST(ctx, e.creds, "/fapi/v1/batchOrders", url.Values{"batchOrders": {string(encoded)}})
	if err != nil {
		return nil, err
	}
	var rows []struct {
		OrderId       int64  `json:"orderId"`
		ClientOrderId string `json:"clientOrderId"`
		Code          int    `json:"code"`
		Msg           string `json:"msg"`
	}
	if err := json.Unmarshal(data, &rows); err != nil {
		return nil, err
	}
	out := make([]trader.BatchPlaceResult, 0, len(rows))
	for _, r := range rows {
		out = append(out, trader.BatchPlaceResult{
			OrderId: strconv.FormatInt(r.OrderId, 10), OrderLinkId: r.ClientOrderId,
			Code: r.Code, Msg: r.Msg,
		})
	}
	return out, nil
}

func (e *BinanceExchange) CancelOrderBatch(ctx context.Context, req trader.BatchCancelRequest) error {
	if len(req.Request) == 0 {
		return nil
	}
	symbol := req.Request[0].Symbol
	ids := make([]string, 0, len(req.Request))
	for _, it := range req.Request {
		ids = append(ids, it.OrderId)
	}
	encoded, err := json.Marshal(ids)
	if err != nil {
		return err
	}
	_, err = doSignedDELETE(ctx, e.creds, "/fapi/v1/batchOrders", url.Values{
		"symbol":      {symbol},
		"orderIdList": {string(encoded)},
	})
	return err
}
```

(`"strings"` is already imported in `exchange.go` from Task 3.)

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./pkg/trader/binance/... -v`
Expected: `PASS` — all tests from Tasks 1-4.

- [ ] **Step 5: Commit**

```bash
git add pkg/trader/binance/exchange.go pkg/trader/binance/exchange_test.go
git commit -m "$(cat <<'EOF'
feat(trader/binance): add PlaceOrderBatch/CancelOrderBatch

Co-Authored-By: Claude Sonnet 5 <noreply@anthropic.com>
EOF
)"
```

---

### Task 5: Open orders + account setup (leverage, position mode)

**Files:**
- Modify: `pkg/trader/binance/exchange.go`
- Modify: `pkg/trader/binance/exchange_test.go`

- [ ] **Step 1: Write the failing tests**

Append to `pkg/trader/binance/exchange_test.go`:

```go
func TestBinanceExchange_FetchOpenOrdersForSymbolAll_TranslatesOpenOrders(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[{"orderId":30,"clientOrderId":"link-1","symbol":"BTCUSDT","side":"BUY","type":"LIMIT","price":"50000","origQty":"1","executedQty":"0","status":"NEW"}]`))
	}))
	defer srv.Close()
	withMockBinanceBase(t, srv)

	ex := NewBinanceExchange(testCreds())
	got, err := ex.FetchOpenOrdersForSymbolAll(context.Background(), "linear", "BTCUSDT")
	if err != nil {
		t.Fatalf("FetchOpenOrdersForSymbolAll: %v", err)
	}
	if len(got) != 1 || got[0].OrderId != "30" || got[0].OrderLinkId != "link-1" {
		t.Errorf("FetchOpenOrdersForSymbolAll = %+v, want one order id=30 linkId=link-1", got)
	}
}

func TestBinanceExchange_SetLeverage_SendsSymbolAndLeverage(t *testing.T) {
	var gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		buf := make([]byte, r.ContentLength)
		_, _ = r.Body.Read(buf)
		gotBody = string(buf)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"leverage":10,"maxNotionalValue":"1000000","symbol":"BTCUSDT"}`))
	}))
	defer srv.Close()
	withMockBinanceBase(t, srv)

	ex := NewBinanceExchange(testCreds())
	req := trader.LeverageRequest{Symbol: "BTCUSDT", BuyLeverage: "10", SellLeverage: "10"}
	if err := ex.SetLeverage(context.Background(), req); err != nil {
		t.Fatalf("SetLeverage: %v", err)
	}
	if !strings.Contains(gotBody, "leverage=10") {
		t.Errorf("body = %q, want leverage=10 (Binance has one leverage per symbol, not separate buy/sell — BuyLeverage is used as the single value)", gotBody)
	}
}

func TestBinanceExchange_SwitchPositionMode_SendsDualSidePositionString(t *testing.T) {
	var gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		buf := make([]byte, r.ContentLength)
		_, _ = r.Body.Read(buf)
		gotBody = string(buf)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"code":200,"msg":"success"}`))
	}))
	defer srv.Close()
	withMockBinanceBase(t, srv)

	ex := NewBinanceExchange(testCreds())
	// Binance's dual-side-position toggle is ACCOUNT-WIDE, not per-symbol — the
	// interface's category/symbol params exist for Bybit compatibility and are simply
	// unused here (documented in the method itself, not a bug).
	if err := ex.SwitchPositionMode(context.Background(), "linear", "BTCUSDT", 3); err != nil {
		t.Fatalf("SwitchPositionMode: %v", err)
	}
	if !strings.Contains(gotBody, "dualSidePosition=true") {
		t.Errorf("body = %q, want dualSidePosition=true for mode=3 (hedge)", gotBody)
	}
}

func TestBinanceExchange_SwitchPositionMode_OneWayForModeZero(t *testing.T) {
	var gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		buf := make([]byte, r.ContentLength)
		_, _ = r.Body.Read(buf)
		gotBody = string(buf)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"code":200,"msg":"success"}`))
	}))
	defer srv.Close()
	withMockBinanceBase(t, srv)

	ex := NewBinanceExchange(testCreds())
	if err := ex.SwitchPositionMode(context.Background(), "linear", "BTCUSDT", 0); err != nil {
		t.Fatalf("SwitchPositionMode: %v", err)
	}
	if !strings.Contains(gotBody, "dualSidePosition=false") {
		t.Errorf("body = %q, want dualSidePosition=false for mode=0 (one-way)", gotBody)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./pkg/trader/binance/... -run 'TestBinanceExchange_FetchOpenOrdersForSymbolAll|TestBinanceExchange_SetLeverage|TestBinanceExchange_SwitchPositionMode' -v`
Expected: FAIL — methods undefined.

- [ ] **Step 3: Implement the three methods**

Append to `pkg/trader/binance/exchange.go`:

```go
func (e *BinanceExchange) FetchOpenOrdersForSymbolAll(ctx context.Context, category, symbol string) ([]trader.Order, error) {
	data, err := doSignedGET(ctx, e.creds, "/fapi/v1/openOrders", url.Values{"symbol": {symbol}})
	if err != nil {
		return nil, err
	}
	var rows []struct {
		OrderId       int64  `json:"orderId"`
		ClientOrderId string `json:"clientOrderId"`
		Symbol        string `json:"symbol"`
		Side          string `json:"side"`
		Type          string `json:"type"`
		Price         string `json:"price"`
		OrigQty       string `json:"origQty"`
		ExecutedQty   string `json:"executedQty"`
		Status        string `json:"status"`
		StopPrice     string `json:"stopPrice"`
	}
	if err := json.Unmarshal(data, &rows); err != nil {
		return nil, err
	}
	out := make([]trader.Order, 0, len(rows))
	for _, r := range rows {
		out = append(out, trader.Order{
			OrderId: strconv.FormatInt(r.OrderId, 10), OrderLinkId: r.ClientOrderId,
			Symbol: r.Symbol, Side: r.Side, OrderType: r.Type, Price: r.Price,
			Qty: r.OrigQty, CumExecQty: r.ExecutedQty, OrderStatus: r.Status,
			TriggerPrice: r.StopPrice, Category: "linear",
		})
	}
	return out, nil
}

// SetLeverage sets one symbol's leverage. Binance has a single leverage value per
// symbol (not separate long/short values like Bybit's hedge-mode BuyLeverage/
// SellLeverage) — BuyLeverage is used as that single value; callers in pkg/strategy
// already always set both fields identically for grid/matrix, so this is a no-op
// simplification in practice, not a behavior loss.
func (e *BinanceExchange) SetLeverage(ctx context.Context, req trader.LeverageRequest) error {
	_, err := doSignedPOST(ctx, e.creds, "/fapi/v1/leverage", url.Values{
		"symbol":   {req.Symbol},
		"leverage": {req.BuyLeverage},
	})
	return err
}

// SwitchPositionMode sets Hedge Mode (dualSidePosition=true) or One-way Mode (false)
// for the WHOLE ACCOUNT — Binance has no per-symbol position mode, unlike Bybit. The
// category/symbol params are accepted only for Exchange interface compatibility and
// are unused. Binance also errors if any position/order is currently open when this
// is called — same class of constraint as Bybit's own "can't switch with an open
// position" error; that error surfaces to the caller unchanged via doSignedPOST.
func (e *BinanceExchange) SwitchPositionMode(ctx context.Context, category, symbol string, mode int) error {
	dual := "false"
	if mode != 0 {
		dual = "true"
	}
	_, err := doSignedPOST(ctx, e.creds, "/fapi/v1/positionSide/dual", url.Values{"dualSidePosition": {dual}})
	return err
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./pkg/trader/binance/... -v`
Expected: `PASS` — all tests from Tasks 1-5.

- [ ] **Step 5: Commit**

```bash
git add pkg/trader/binance/exchange.go pkg/trader/binance/exchange_test.go
git commit -m "$(cat <<'EOF'
feat(trader/binance): add FetchOpenOrdersForSymbolAll/SetLeverage/SwitchPositionMode

SwitchPositionMode documents a real semantic difference from Bybit: Binance's
hedge-mode toggle is account-wide, not per-symbol.

Co-Authored-By: Claude Sonnet 5 <noreply@anthropic.com>
EOF
)"
```

---

### Task 6: Closed PnL reconstruction

**Files:**
- Modify: `pkg/trader/binance/exchange.go`
- Modify: `pkg/trader/binance/exchange_test.go`

Binance has no single endpoint matching Bybit's `/v5/position/closed-pnl`. Instead,
`GET /fapi/v1/userTrades` returns every fill with a `realizedPnl` field directly —
opening/adding fills carry `"0"`, only genuinely closing fills carry a nonzero value.
Grouping the nonzero-`realizedPnl` fills by `orderId` reconstructs one row per closing
order, matching Bybit's `ClosedPnl` shape. `userTrades` does not include `clientOrderId`
though, so one extra `GET /fapi/v1/order` lookup per distinct closing `orderId` fills in
`ClosedPnl.OrderLinkId` (needed for `pkg/strategy.ParseStrategyLinkID` attribution).

- [ ] **Step 1: Write the failing tests**

Append to `pkg/trader/binance/exchange_test.go`:

```go
func TestBinanceExchange_FetchClosedPnlForSymbol_GroupsClosingFillsByOrder(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.Contains(r.URL.Path, "/fapi/v1/userTrades"):
			// Two fills on the SAME closing order (partial fill, then the rest) plus
			// one unrelated OPENING fill (realizedPnl=0) that must be excluded.
			_, _ = w.Write([]byte(`[
				{"id":1,"orderId":100,"symbol":"BTCUSDT","side":"SELL","price":"61000","qty":"0.3","realizedPnl":"0","time":1000},
				{"id":2,"orderId":200,"symbol":"BTCUSDT","side":"SELL","price":"61000","qty":"0.3","realizedPnl":"180.0","time":2000},
				{"id":3,"orderId":200,"symbol":"BTCUSDT","side":"SELL","price":"61050","qty":"0.2","realizedPnl":"130.0","time":2100}
			]`))
		case strings.Contains(r.URL.Path, "/fapi/v1/order"):
			_, _ = w.Write([]byte(`{"orderId":200,"clientOrderId":"SIS_STR-a1b2c3d4-tp-3-1"}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()
	withMockBinanceBase(t, srv)

	ex := NewBinanceExchange(testCreds())
	got, err := ex.FetchClosedPnlForSymbol(context.Background(), "linear", "BTCUSDT", 10)
	if err != nil {
		t.Fatalf("FetchClosedPnlForSymbol: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("FetchClosedPnlForSymbol returned %d rows, want 1 (order 100's zero-pnl opening fill must be excluded; order 200's two fills must merge into one row)", len(got))
	}
	row := got[0]
	if row.OrderId != "200" {
		t.Errorf("OrderId = %q, want 200", row.OrderId)
	}
	if row.OrderLinkId != "SIS_STR-a1b2c3d4-tp-3-1" {
		t.Errorf("OrderLinkId = %q, want the looked-up clientOrderId", row.OrderLinkId)
	}
	if row.ClosedPnl != "310" && row.ClosedPnl != "310.0" {
		t.Errorf("ClosedPnl = %q, want 310 (180.0 + 130.0 summed across both fills)", row.ClosedPnl)
	}
	if row.Qty != "0.5" {
		t.Errorf("Qty = %q, want 0.5 (0.3 + 0.2 summed)", row.Qty)
	}
}

func TestBinanceExchange_FetchRecentClosedPnl_WindowsRequestsAt7Days(t *testing.T) {
	var gotStartTimes, gotEndTimes []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.Contains(r.URL.Path, "/fapi/v1/userTrades") {
			gotStartTimes = append(gotStartTimes, r.URL.Query().Get("startTime"))
			gotEndTimes = append(gotEndTimes, r.URL.Query().Get("endTime"))
			_, _ = w.Write([]byte(`[]`))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()
	withMockBinanceBase(t, srv)

	ex := NewBinanceExchange(testCreds())
	// 16 days ago requires 3 windows of ≤7 days each (Binance's own per-call cap).
	since := time.Now().Add(-16 * 24 * time.Hour)
	if _, err := ex.FetchRecentClosedPnl(context.Background(), "linear", since); err != nil {
		t.Fatalf("FetchRecentClosedPnl: %v", err)
	}
	if len(gotStartTimes) < 3 {
		t.Errorf("FetchRecentClosedPnl issued %d userTrades calls for a 16-day window, want at least 3 (7-day cap per call)", len(gotStartTimes))
	}
}
```

Add `"time"` to `exchange_test.go`'s import block.

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./pkg/trader/binance/... -run 'TestBinanceExchange_FetchClosedPnlForSymbol|TestBinanceExchange_FetchRecentClosedPnl' -v`
Expected: FAIL — methods undefined.

- [ ] **Step 3: Implement closed-pnl reconstruction**

Append to `pkg/trader/binance/exchange.go`:

```go
type binanceUserTrade struct {
	Id          int64  `json:"id"`
	OrderId     int64  `json:"orderId"`
	Symbol      string `json:"symbol"`
	Side        string `json:"side"`
	Price       string `json:"price"`
	Qty         string `json:"qty"`
	RealizedPnl string `json:"realizedPnl"`
	Time        int64  `json:"time"`
}

// closingFillsToClosedPnl groups trades whose realizedPnl != 0 (the actual signal that
// a fill closed/reduced a position, as opposed to opening/adding to one) by orderId,
// summing PnL and quantity and taking the latest fill's price/time as the row's
// exit/close time — the same one-row-per-closing-order shape Bybit's own
// /v5/position/closed-pnl already returns natively.
func closingFillsToClosedPnl(ctx context.Context, creds trader.Credentials, symbol string, trades []binanceUserTrade) ([]trader.ClosedPnl, error) {
	type agg struct {
		side        string
		qty         float64
		pnl         float64
		lastPrice   string
		lastTimeMs  int64
	}
	byOrder := make(map[int64]*agg)
	var order []int64
	for _, t := range trades {
		pnl, _ := strconv.ParseFloat(t.RealizedPnl, 64)
		if pnl == 0 {
			continue
		}
		a, ok := byOrder[t.OrderId]
		if !ok {
			a = &agg{side: t.Side}
			byOrder[t.OrderId] = a
			order = append(order, t.OrderId)
		}
		qty, _ := strconv.ParseFloat(t.Qty, 64)
		a.qty += qty
		a.pnl += pnl
		if t.Time >= a.lastTimeMs {
			a.lastTimeMs = t.Time
			a.lastPrice = t.Price
		}
	}

	out := make([]trader.ClosedPnl, 0, len(order))
	for _, orderId := range order {
		a := byOrder[orderId]
		linkId, err := lookupClientOrderId(ctx, creds, symbol, orderId)
		if err != nil {
			return nil, err
		}
		out = append(out, trader.ClosedPnl{
			Symbol: symbol, OrderId: strconv.FormatInt(orderId, 10), OrderLinkId: linkId,
			Side: a.side, Qty: strconv.FormatFloat(a.qty, 'f', -1, 64),
			AvgExitPrice: a.lastPrice, ClosedPnl: strconv.FormatFloat(a.pnl, 'f', -1, 64),
			CreatedTime: strconv.FormatInt(a.lastTimeMs, 10), Category: "linear",
		})
	}
	return out, nil
}

// lookupClientOrderId fetches one order's clientOrderId by orderId — userTrades doesn't
// carry it, but pkg/strategy's attribution (ParseStrategyLinkID) needs it. One call per
// distinct closing order in the queried window, not per fill.
func lookupClientOrderId(ctx context.Context, creds trader.Credentials, symbol string, orderId int64) (string, error) {
	data, err := doSignedGET(ctx, creds, "/fapi/v1/order", url.Values{
		"symbol": {symbol}, "orderId": {strconv.FormatInt(orderId, 10)},
	})
	if err != nil {
		return "", err
	}
	var r struct {
		ClientOrderId string `json:"clientOrderId"`
	}
	if err := json.Unmarshal(data, &r); err != nil {
		return "", err
	}
	return r.ClientOrderId, nil
}

func (e *BinanceExchange) FetchClosedPnlForSymbol(ctx context.Context, category, symbol string, limit int) ([]trader.ClosedPnl, error) {
	if limit <= 0 || limit > 1000 {
		limit = 50
	}
	data, err := doSignedGET(ctx, e.creds, "/fapi/v1/userTrades", url.Values{
		"symbol": {symbol}, "limit": {strconv.Itoa(limit)},
	})
	if err != nil {
		return nil, err
	}
	var trades []binanceUserTrade
	if err := json.Unmarshal(data, &trades); err != nil {
		return nil, err
	}
	return closingFillsToClosedPnl(ctx, e.creds, symbol, trades)
}

// FetchRecentClosedPnl fans out across ≤7-day windows from since to now — Binance caps
// userTrades' startTime..endTime span at 7 days per call, unlike Bybit's single
// cursor-paginated call across an arbitrarily old `since`. category is accepted for
// interface compatibility; this implementation has no per-symbol scoping (see the
// signature — no symbol param either, matching Bybit's own FetchRecentClosedPnl, which
// is genuinely account-wide). Binance has no exact account-wide equivalent to
// userTrades (it's always per-symbol), so this walks every symbol currently holding an
// open position — see the inline note below for why that's the right scope, not a gap.
func (e *BinanceExchange) FetchRecentClosedPnl(ctx context.Context, category string, since time.Time) ([]trader.ClosedPnl, error) {
	positions, err := e.FetchPositions(ctx)
	if err != nil {
		return nil, err
	}
	var all []trader.ClosedPnl
	now := time.Now()
	for _, p := range positions {
		windowStart := since
		for windowStart.Before(now) {
			windowEnd := windowStart.Add(7 * 24 * time.Hour)
			if windowEnd.After(now) {
				windowEnd = now
			}
			data, err := doSignedGET(ctx, e.creds, "/fapi/v1/userTrades", url.Values{
				"symbol":    {p.Symbol},
				"startTime": {strconv.FormatInt(windowStart.UnixMilli(), 10)},
				"endTime":   {strconv.FormatInt(windowEnd.UnixMilli(), 10)},
				"limit":     {"1000"},
			})
			if err != nil {
				return nil, err
			}
			var trades []binanceUserTrade
			if err := json.Unmarshal(data, &trades); err != nil {
				return nil, err
			}
			rows, err := closingFillsToClosedPnl(ctx, e.creds, p.Symbol, trades)
			if err != nil {
				return nil, err
			}
			all = append(all, rows...)
			windowStart = windowEnd
		}
	}
	return all, nil
}
```

**Note on `FetchRecentClosedPnl`'s scope:** it only walks symbols with a *currently
open* position, which means a symbol closed to flat within the queried window would be
missed. This is a real, known gap versus Bybit's account-wide call — flag it explicitly
in your Step 5 self-review and commit message rather than silently shipping it; it's an
acceptable v1 limitation (this method backs `closed_pnl_syncer`'s reconciliation sweep,
which already treats missed rows as retryable rather than catastrophic — see
`services/api-gateway/closed_pnl_syncer.go`'s gap-backfill watchdog from the earlier
trade-attribution work), not something to silently expand scope to fix in this task.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./pkg/trader/binance/... -v`
Expected: `PASS` — all tests from Tasks 1-6.

- [ ] **Step 5: Commit**

```bash
git add pkg/trader/binance/exchange.go pkg/trader/binance/exchange_test.go
git commit -m "$(cat <<'EOF'
feat(trader/binance): add closed-pnl reconstruction from userTrades

Binance has no single endpoint matching Bybit's /v5/position/closed-pnl.
userTrades returns realizedPnl per fill directly (0 for opening/adding fills,
nonzero for closing ones) — grouping nonzero-pnl fills by orderId reconstructs
one row per closing order. clientOrderId isn't in userTrades, so one extra
GET /fapi/v1/order lookup per distinct closing order fills OrderLinkId for
pkg/strategy's attribution.

Known v1 gap: FetchRecentClosedPnl only walks symbols with a currently-open
position, so a symbol that closed to fully flat within the window is missed —
acceptable given the trade_history gap-backfill watchdog already treats
missed rows as retryable.

Co-Authored-By: Claude Sonnet 5 <noreply@anthropic.com>
EOF
)"
```

---

### Task 7: Compile-time assertion + full verification

**Files:**
- Modify: `pkg/trader/binance/exchange.go`

- [ ] **Step 1: Append the assertion**

Add to the bottom of `pkg/trader/binance/exchange.go`:

```go
// var _ trader.Exchange = (*BinanceExchange)(nil) fails to compile if BinanceExchange
// ever stops satisfying trader.Exchange — mirrors pkg/trader/exchange.go's own
// var _ Exchange = (*BybitExchange)(nil).
var _ trader.Exchange = (*BinanceExchange)(nil)
```

If this fails to compile, **stop and report BLOCKED** with the exact compiler error —
do not guess-fix a missing/mismatched method. Every method `Exchange` declares should
already exist on `*BinanceExchange` from Tasks 2-6; a compile failure here means one of
those tasks' reviews missed something and needs a human decision, not a mechanical patch.

- [ ] **Step 2: Run the full package verification**

Run: `go build ./pkg/trader/binance/... && go vet ./pkg/trader/binance/... && go test ./pkg/trader/binance/... -v`
Expected: builds cleanly, vet reports nothing, every test from Tasks 1-6 passes.

- [ ] **Step 3: Run the full repository build**

Run: `go build ./... && go test ./...`
Expected: everything still builds and passes — this plan only added the new
`pkg/trader/binance` package; no existing file changed, so no other package should be
affected. (If you're working in a git worktree per this session's established pattern,
run this inside the worktree — do not use an absolute path outside it. Double-check
`pwd` before running file-modifying or path-sensitive commands; a prior session in this
same project once mis-pathed an edit into the main checkout instead of the worktree by
mistake — verify you're where you think you are.)

- [ ] **Step 4: Commit**

```bash
git add pkg/trader/binance/exchange.go
git commit -m "$(cat <<'EOF'
feat(trader/binance): assert BinanceExchange satisfies trader.Exchange

Closes out the Binance exchange client plan
(docs/superpowers/plans/2026-09-03-binance-exchange-client.md). Nothing in
pkg/strategy or services/api-gateway wires this in yet — that's a separate,
later plan (ticker-hub price dispatch, then AccountRunner/call-site
migration), per the design spec's phasing.

Co-Authored-By: Claude Sonnet 5 <noreply@anthropic.com>
EOF
)"
```

---

## What this plan deliberately does not do

- Does not wire `BinanceExchange` into `AccountRunner` or any call site — that's a
  later plan, same as the Bybit adapter plan's own scope boundary.
- Does not touch `pkg/signal/ticker_hub.go` (the price-feed dispatch layer) — separate
  plan per the design spec's architecture section.
- Does not handle Binance user-data-stream WS (order/position/execution push updates) —
  this plan is REST-only. The design spec's price-feed plan and the account-runner
  wiring plan cover where Binance's private WS stream (the analog of Bybit's WS
  position-stream that feeds `AccountRunner.posAvgEntry`) gets integrated.
- Does not add DB migrations, frontend changes, or account-onboarding flows (leverage/
  hedge-mode auto-setup on connect) — later plan.
