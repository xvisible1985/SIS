package trader

import (
	"context"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// fakeSyncExchangeCall records one FetchAccountExecutions/FetchAccountOrderHistory call.
type fakeSyncExchangeCall struct {
	category string
	since    time.Time
}

// fakeSyncExchange is a minimal, call-recording Exchange for syncer.go's dispatch tests
// below — every method besides the ones under test is a trivial stub, mirroring
// services/api-gateway's fakeReconcileExchange convention.
type fakeSyncExchange struct {
	execCalls []fakeSyncExchangeCall
	histCalls []fakeSyncExchangeCall

	whitelistIPs []string
	whitelistErr error
}

func (f *fakeSyncExchange) PlaceOrder(ctx context.Context, req OrderRequest) (OrderResult, error) {
	return OrderResult{}, nil
}
func (f *fakeSyncExchange) PlaceOrderBatch(ctx context.Context, req BatchPlaceRequest) ([]BatchPlaceResult, error) {
	return nil, nil
}
func (f *fakeSyncExchange) CancelOrder(ctx context.Context, req CancelRequest) error { return nil }
func (f *fakeSyncExchange) CancelOrderBatch(ctx context.Context, req BatchCancelRequest) error {
	return nil
}
func (f *fakeSyncExchange) PlaceOrderREST(ctx context.Context, req OrderRequest) (OrderResult, error) {
	return OrderResult{}, nil
}
func (f *fakeSyncExchange) CancelOrderREST(ctx context.Context, req CancelRequest) error { return nil }
func (f *fakeSyncExchange) CancelAllOrders(ctx context.Context, req CancelAllRequest) error {
	return nil
}
func (f *fakeSyncExchange) FetchPositions(ctx context.Context) ([]Position, error) { return nil, nil }
func (f *fakeSyncExchange) FetchOpenOrdersForSymbolAll(ctx context.Context, category, symbol string) ([]Order, error) {
	return nil, nil
}
func (f *fakeSyncExchange) FetchClosedPnlForSymbol(ctx context.Context, category, symbol string, limit int) ([]ClosedPnl, error) {
	return nil, nil
}
func (f *fakeSyncExchange) FetchRecentClosedPnl(ctx context.Context, category string, since time.Time) ([]ClosedPnl, error) {
	return nil, nil
}
func (f *fakeSyncExchange) GetWalletBalance(ctx context.Context) (float64, float64, error) {
	return 0, 0, nil
}
func (f *fakeSyncExchange) SetLeverage(ctx context.Context, req LeverageRequest) error { return nil }
func (f *fakeSyncExchange) SwitchPositionMode(ctx context.Context, category, symbol string, mode int) error {
	return nil
}
func (f *fakeSyncExchange) GetMarkPrice(ctx context.Context, category, symbol string) (float64, error) {
	return 0, nil
}
func (f *fakeSyncExchange) GetInstrumentInfo(ctx context.Context, category, symbol string) (InstrumentInfo, error) {
	return InstrumentInfo{}, nil
}
func (f *fakeSyncExchange) FetchOrderByLinkId(ctx context.Context, category, symbol, orderLinkId string) (Order, bool, error) {
	return Order{}, false, nil
}
func (f *fakeSyncExchange) FetchOrderById(ctx context.Context, category, symbol, orderId string) (Order, bool, error) {
	return Order{}, false, nil
}
func (f *fakeSyncExchange) FetchOpenOrders(ctx context.Context) ([]Order, error) { return nil, nil }
func (f *fakeSyncExchange) GetAccountMarginInfo(ctx context.Context) (float64, float64, float64, error) {
	return 0, 0, 0, nil
}
func (f *fakeSyncExchange) SetTrailingStop(ctx context.Context, req TrailingStopRequest) error {
	return nil
}

func (f *fakeSyncExchange) FetchAccountExecutions(ctx context.Context, category string, since time.Time) ([]Execution, error) {
	f.execCalls = append(f.execCalls, fakeSyncExchangeCall{category: category, since: since})
	return nil, nil
}

func (f *fakeSyncExchange) FetchAccountOrderHistory(ctx context.Context, category string, since time.Time) ([]Order, error) {
	f.histCalls = append(f.histCalls, fakeSyncExchangeCall{category: category, since: since})
	return nil, nil
}

func (f *fakeSyncExchange) FetchWhitelistedIPs(ctx context.Context) ([]string, error) {
	return f.whitelistIPs, f.whitelistErr
}

var _ Exchange = (*fakeSyncExchange)(nil)

// Regression (2026-10-17): account fd7d407b ("N2 Binance")'s logs showed
// "syncer: fetch executions account=fd7d407b... category=linear/inverse/spot: bybit:
// parse response: unexpected end of JSON input" / "retCode=10003: You are not
// authorized" — syncExecutions used to call Bybit-only free functions directly for every
// account regardless of exchange. These tests pin the fixed dispatch: Bybit still
// queries all 3 categories (unchanged), Binance (USDⓈ-M futures only) queries exactly
// one, and the account's own `since` cutoff (derived from syncDays) is what's passed to
// the Exchange, not re-derived or ignored.
func TestSyncExecutions_BybitQueriesAllThreeCategories(t *testing.T) {
	fake := &fakeSyncExchange{}
	s := &Syncer{syncDays: 7}
	s.syncExecutions(context.Background(), accountRow{id: "a1", exchange: "bybit"}, fake)

	if len(fake.execCalls) != 3 {
		t.Fatalf("got %d FetchAccountExecutions calls, want 3 (linear/inverse/spot)", len(fake.execCalls))
	}
	got := []string{fake.execCalls[0].category, fake.execCalls[1].category, fake.execCalls[2].category}
	want := []string{"linear", "inverse", "spot"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("categories = %v, want %v", got, want)
	}
}

func TestSyncExecutions_BinanceQueriesOnlyOneCategory(t *testing.T) {
	fake := &fakeSyncExchange{}
	s := &Syncer{syncDays: 7}
	s.syncExecutions(context.Background(), accountRow{id: "a1", exchange: "binance"}, fake)

	if len(fake.execCalls) != 1 {
		t.Fatalf("got %d FetchAccountExecutions calls, want 1 (Binance has no inverse/spot split)", len(fake.execCalls))
	}
	if fake.execCalls[0].category != "linear" {
		t.Errorf("category = %q, want %q", fake.execCalls[0].category, "linear")
	}
}

func TestSyncOrderHistory_BinanceQueriesOnlyOneCategory(t *testing.T) {
	fake := &fakeSyncExchange{}
	s := &Syncer{syncDays: 7}
	s.syncOrderHistory(context.Background(), accountRow{id: "a1", exchange: "binance"}, fake)

	if len(fake.histCalls) != 1 {
		t.Fatalf("got %d FetchAccountOrderHistory calls, want 1", len(fake.histCalls))
	}
	if fake.histCalls[0].category != "linear" {
		t.Errorf("category = %q, want %q", fake.histCalls[0].category, "linear")
	}
}

func TestSyncExecutions_SinceReflectsSyncDays(t *testing.T) {
	fake := &fakeSyncExchange{}
	s := &Syncer{syncDays: 5}
	before := time.Now()
	s.syncExecutions(context.Background(), accountRow{id: "a1", exchange: "bybit"}, fake)

	wantSince := before.AddDate(0, 0, -5)
	for _, c := range fake.execCalls {
		if c.since.Before(wantSince.Add(-time.Second)) || c.since.After(wantSince.Add(time.Second)) {
			t.Errorf("since = %v, want ~%v (5 days back)", c.since, wantSince)
		}
	}
}

// Regression (2026-10-17): refreshWhitelistedIPs used to call Bybit's QueryAPI directly
// for every account regardless of exchange — for a Binance account that produced exactly
// the "syncer: refresh whitelist account=... bybit: retCode=10003: You are not
// authorized" warning seen live in production. A Binance account's FetchWhitelistedIPs
// now returns ErrWhitelistNotSupported (Binance's API has no endpoint exposing the
// actual IP list — see that error's doc comment), which must be treated as an expected,
// quiet no-op: no DB write attempted, no error logged, no Bybit endpoint ever touched.
func TestRefreshWhitelistedIPs_BinanceAccount_NoOpsWithoutError(t *testing.T) {
	a := &accountRow{id: "acct-binance", exchange: "binance"}
	creds := &Credentials{APIKey: "key", SecretKey: "secret", AccountID: "acct-binance"}

	s := &Syncer{pool: nil, factory: func(exchangeName string, creds Credentials) Exchange {
		return &fakeSyncExchange{whitelistErr: ErrWhitelistNotSupported}
	}}
	changed := s.refreshWhitelistedIPs(context.Background(), a, creds)
	if changed {
		t.Error("expected no change for an exchange with no whitelist endpoint")
	}
	if a.whitelistedIPs != nil {
		t.Errorf("a.whitelistedIPs = %v, want unchanged (nil)", a.whitelistedIPs)
	}
}

// withMockBybitBase points bybitBase at srv for the duration of the test and restores
// the real value on cleanup. Tests in this package are not run in parallel with each
// other by default (no t.Parallel() calls), so mutating this package-level var is safe.
func withMockBybitBase(t *testing.T, srv *httptest.Server) {
	t.Helper()
	orig := bybitBase
	bybitBase = srv.URL
	t.Cleanup(func() { bybitBase = orig })
}

// bybitExchangeFactory is the ExchangeFactory tests use — equivalent to what
// strategy.ResolveExchange would build for exchangeName=="bybit" (the only exchange this
// package can construct without importing pkg/trader/binance, which would be a cycle).
// ws is nil since none of these tests exercise WS-backed order placement.
func bybitExchangeFactory(exchangeName string, creds Credentials) Exchange {
	return NewBybitExchange(creds, nil)
}

// unreachablePool returns a *pgxpool.Pool that constructs successfully (pgxpool.New is
// lazy: it does not dial on construction) but fails any Exec/Query with a connection
// error, since nothing listens on the target port. Used to exercise refreshWhitelistedIPs
// without depending on a live database.
func unreachablePool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	pool, err := pgxpool.New(context.Background(), "postgres://nouser:nopass@127.0.0.1:1/nodb")
	if err != nil {
		t.Fatalf("pgxpool.New: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// TestRefreshWhitelistedIPs_DoesNotSelfLock is a regression test for the self-lock bug:
// refreshWhitelistedIPs must call QueryAPI with UNRESTRICTED credentials, never with
// creds.WhitelistedIPs as it stood before the refresh. We prove this by pre-populating
// creds.WhitelistedIPs with an IP that cannot possibly be satisfied (no proxy pool is
// configured in this test process at all, i.e. proxy.globalManager is nil/zero), which
// per pkg/proxy's own HTTPClientFor behavior (see pkg/proxy/client_test.go) means any
// REQUEST made with a non-empty allowedIPs list fails client-side with
// ErrNoWhitelistedProxy before ever reaching the network. If refreshWhitelistedIPs used
// *creds directly (the bug), our mock Bybit server would receive zero requests. With the
// fix (unrestricted credentials), the mock server is hit exactly once.
func TestRefreshWhitelistedIPs_DoesNotSelfLock(t *testing.T) {
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v5/user/query-api":
			atomic.AddInt32(&hits, 1)
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"retCode":0,"retMsg":"OK","result":{"ips":["3.3.3.3","4.4.4.4"]}}`))
		default:
			// /v5/market/time (timestamp sync) and anything else: respond so callers
			// don't hang; errors here are swallowed by serverTimestamp() and are not
			// load-bearing for this test.
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()
	withMockBybitBase(t, srv)

	a := &accountRow{id: "acct-1", exchange: "bybit", whitelistedIPs: []string{"9.9.9.9"}}
	creds := &Credentials{
		APIKey:         "key",
		SecretKey:      "secret",
		AccountID:      "acct-1",
		WhitelistedIPs: []string{"9.9.9.9"}, // stale/narrow whitelist from a previous refresh
	}

	s := &Syncer{pool: unreachablePool(t), factory: bybitExchangeFactory}
	s.refreshWhitelistedIPs(context.Background(), a, creds)

	if got := atomic.LoadInt32(&hits); got != 1 {
		t.Fatalf("expected QueryAPI to reach the mock Bybit server exactly once (proving it used unrestricted creds), got %d hits", got)
	}

	// The DB is unreachable in this test, so persistence fails and refreshWhitelistedIPs
	// must leave the in-memory fields untouched (it only mutates them after a successful
	// persist).
	if !reflect.DeepEqual(a.whitelistedIPs, []string{"9.9.9.9"}) {
		t.Errorf("a.whitelistedIPs mutated despite failed persist: %v", a.whitelistedIPs)
	}
	if !reflect.DeepEqual(creds.WhitelistedIPs, []string{"9.9.9.9"}) {
		t.Errorf("creds.WhitelistedIPs mutated despite failed persist: %v", creds.WhitelistedIPs)
	}
}

// TestRefreshWhitelistedIPs_QueryAPIError_LeavesFieldsUnchanged covers the other early-return
// path: when QueryAPI itself fails (non-zero retCode), refreshWhitelistedIPs must return
// before touching a/creds or the database.
func TestRefreshWhitelistedIPs_QueryAPIError_LeavesFieldsUnchanged(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"retCode":10003,"retMsg":"Invalid API key","result":{}}`))
	}))
	defer srv.Close()
	withMockBybitBase(t, srv)

	a := &accountRow{id: "acct-2", exchange: "bybit", whitelistedIPs: []string{"5.5.5.5"}}
	creds := &Credentials{
		APIKey:         "bad-key",
		SecretKey:      "bad-secret",
		AccountID:      "acct-2",
		WhitelistedIPs: []string{"5.5.5.5"},
	}

	// pool intentionally nil: if refreshWhitelistedIPs reached the persist step on this
	// error path, the test would panic on the nil pool dereference, failing loudly.
	s := &Syncer{pool: nil, factory: bybitExchangeFactory}
	s.refreshWhitelistedIPs(context.Background(), a, creds)

	if !reflect.DeepEqual(a.whitelistedIPs, []string{"5.5.5.5"}) {
		t.Errorf("a.whitelistedIPs mutated on QueryAPI error: %v", a.whitelistedIPs)
	}
	if !reflect.DeepEqual(creds.WhitelistedIPs, []string{"5.5.5.5"}) {
		t.Errorf("creds.WhitelistedIPs mutated on QueryAPI error: %v", creds.WhitelistedIPs)
	}
}

// TestRefreshWhitelistedIPs_ParseError_LeavesFieldsUnchanged covers the third early-return
// path: retCode==0 but the result body isn't the expected {"ips": [...]} shape.
func TestRefreshWhitelistedIPs_ParseError_LeavesFieldsUnchanged(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"retCode":0,"retMsg":"OK","result":"not-an-object"}`))
	}))
	defer srv.Close()
	withMockBybitBase(t, srv)

	a := &accountRow{id: "acct-3", exchange: "bybit", whitelistedIPs: []string{"6.6.6.6"}}
	creds := &Credentials{
		APIKey:         "key",
		SecretKey:      "secret",
		AccountID:      "acct-3",
		WhitelistedIPs: []string{"6.6.6.6"},
	}

	s := &Syncer{pool: nil, factory: bybitExchangeFactory}
	s.refreshWhitelistedIPs(context.Background(), a, creds)

	if !reflect.DeepEqual(a.whitelistedIPs, []string{"6.6.6.6"}) {
		t.Errorf("a.whitelistedIPs mutated on parse error: %v", a.whitelistedIPs)
	}
	if !reflect.DeepEqual(creds.WhitelistedIPs, []string{"6.6.6.6"}) {
		t.Errorf("creds.WhitelistedIPs mutated on parse error: %v", creds.WhitelistedIPs)
	}
}
