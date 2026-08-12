package trader

import (
	"context"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sync/atomic"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// withMockBybitBase points bybitBase at srv for the duration of the test and restores
// the real value on cleanup. Tests in this package are not run in parallel with each
// other by default (no t.Parallel() calls), so mutating this package-level var is safe.
func withMockBybitBase(t *testing.T, srv *httptest.Server) {
	t.Helper()
	orig := bybitBase
	bybitBase = srv.URL
	t.Cleanup(func() { bybitBase = orig })
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

	a := &accountRow{id: "acct-1", whitelistedIPs: []string{"9.9.9.9"}}
	creds := &Credentials{
		APIKey:         "key",
		SecretKey:      "secret",
		AccountID:      "acct-1",
		WhitelistedIPs: []string{"9.9.9.9"}, // stale/narrow whitelist from a previous refresh
	}

	s := &Syncer{pool: unreachablePool(t)}
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

	a := &accountRow{id: "acct-2", whitelistedIPs: []string{"5.5.5.5"}}
	creds := &Credentials{
		APIKey:         "bad-key",
		SecretKey:      "bad-secret",
		AccountID:      "acct-2",
		WhitelistedIPs: []string{"5.5.5.5"},
	}

	// pool intentionally nil: if refreshWhitelistedIPs reached the persist step on this
	// error path, the test would panic on the nil pool dereference, failing loudly.
	s := &Syncer{pool: nil}
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

	a := &accountRow{id: "acct-3", whitelistedIPs: []string{"6.6.6.6"}}
	creds := &Credentials{
		APIKey:         "key",
		SecretKey:      "secret",
		AccountID:      "acct-3",
		WhitelistedIPs: []string{"6.6.6.6"},
	}

	s := &Syncer{pool: nil}
	s.refreshWhitelistedIPs(context.Background(), a, creds)

	if !reflect.DeepEqual(a.whitelistedIPs, []string{"6.6.6.6"}) {
		t.Errorf("a.whitelistedIPs mutated on parse error: %v", a.whitelistedIPs)
	}
	if !reflect.DeepEqual(creds.WhitelistedIPs, []string{"6.6.6.6"}) {
		t.Errorf("creds.WhitelistedIPs mutated on parse error: %v", creds.WhitelistedIPs)
	}
}
