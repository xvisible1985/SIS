package trader

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// Regression for a live incident (2026-08-12): an expired Bybit API key kept every
// retry loop (order placement, matrix reconcile, WS reconnect) hammering the same
// dead key every few seconds forever, and deactivating the exchange_accounts row did
// nothing to stop it. IsPermanentAuthError is what AccountRunner now checks to decide
// whether to auto-stop instead of retrying — pin it against both the REST and the
// WS-auth-failure error shapes, and make sure transient errors (rate limit, the
// proxy-pool IP-mismatch gap) are NOT misclassified as permanent.
func TestIsPermanentAuthError(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"nil error", nil, false},
		{"REST retCode=33004 expired key", fmt.Errorf("bybit: retCode=33004: %s", "Your api key has expired."), true},
		{"WS auth failure, no retCode", errors.New("auth failed: Your api key has expired."), true},
		{"transient: IP mismatch (proxy-pool gap, self-heals)", fmt.Errorf("bybit: retCode=10010: %s", "Unmatched IP, please check your API key's bound IP addresses."), false},
		{"transient: rate limited", fmt.Errorf("bybit: retCode=10006: %s", "Too many visits!"), false},
		{"transient: network timeout", errors.New("context deadline exceeded"), false},
		{"unrelated order error", fmt.Errorf("bybit: retCode=110017: %s", "current position is zero"), false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := IsPermanentAuthError(c.err); got != c.want {
				t.Errorf("IsPermanentAuthError(%v) = %v, want %v", c.err, got, c.want)
			}
		})
	}
}

// Regression (2026-08-13): Bybit's GET /v5/user/query-api returns ips=["*"] (not [])
// for a key with no IP restriction. Storing that verbatim in whitelisted_ips made
// PickForIPs search for a proxy host literally equal to "*" — never found, so the
// account could never route anything and VerifyAccount/Syncer could not self-correct.
func TestNormalizeWhitelistedIPs(t *testing.T) {
	cases := []struct {
		name string
		in   []string
		want []string
	}{
		{"nil — already unrestricted", nil, nil},
		{"empty — unrestricted", []string{}, nil},
		{"wildcard — unrestricted", []string{"*"}, nil},
		{"real IPs — pass through", []string{"1.2.3.4", "5.6.7.8"}, []string{"1.2.3.4", "5.6.7.8"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := NormalizeWhitelistedIPs(c.in)
			if len(got) != len(c.want) {
				t.Fatalf("NormalizeWhitelistedIPs(%v) = %v, want %v", c.in, got, c.want)
			}
			for i := range got {
				if got[i] != c.want[i] {
					t.Fatalf("NormalizeWhitelistedIPs(%v) = %v, want %v", c.in, got, c.want)
				}
			}
		})
	}
}

func TestSign(t *testing.T) {
	got := sign("1000", "APIKEY", "SECRET", "10000", "symbol=BTCUSDT")
	if got == "" {
		t.Fatal("sign returned empty string")
	}
	got2 := sign("1000", "APIKEY", "SECRET", "10000", "symbol=BTCUSDT")
	if got != got2 {
		t.Error("sign must be deterministic")
	}
	got3 := sign("1000", "APIKEY", "OTHER", "10000", "symbol=BTCUSDT")
	if got == got3 {
		t.Error("different secret must produce different signature")
	}
}

// Regression (2026-10-17): this cursor-walk-until-since logic used to live inline in
// pkg/trader.Syncer.syncExecutions with no direct test of its own (only exercised
// end-to-end) — moved out behind BybitExchange.FetchAccountExecutions as part of giving
// Binance an equivalent via the same trader.Exchange interface. Pins that the walk stops
// the INSTANT an execution older than `since` is seen, never requesting a further page.
func TestFetchAccountExecutionsSince_StopsAtSinceCutoff(t *testing.T) {
	now := time.Now()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Query().Get("cursor") {
		case "":
			fmt.Fprintf(w, `{"retCode":0,"retMsg":"OK","result":{"list":[{"execId":"e1","execTime":"%d"},{"execId":"e2","execTime":"%d"}],"nextPageCursor":"page2"}}`,
				now.UnixMilli(), now.Add(-2*time.Hour).UnixMilli())
		case "page2":
			// e3 is within `since`, e4 is older — the walk must stop right after e4 and
			// never request a hypothetical page3.
			fmt.Fprintf(w, `{"retCode":0,"retMsg":"OK","result":{"list":[{"execId":"e3","execTime":"%d"},{"execId":"e4","execTime":"%d"}],"nextPageCursor":"page3"}}`,
				now.Add(-4*time.Hour).UnixMilli(), now.Add(-10*time.Hour).UnixMilli())
		default:
			t.Error("requested a page beyond the one containing an execution older than since")
			fmt.Fprint(w, `{"retCode":0,"retMsg":"OK","result":{"list":[],"nextPageCursor":""}}`)
		}
	}))
	defer srv.Close()
	withMockBybitBase(t, srv)

	since := now.Add(-6 * time.Hour)
	got, err := FetchAccountExecutionsSince(context.Background(), Credentials{}, "linear", since)
	if err != nil {
		t.Fatalf("FetchAccountExecutionsSince: %v", err)
	}
	wantIDs := []string{"e1", "e2", "e3"}
	if len(got) != len(wantIDs) {
		t.Fatalf("got %d executions, want %d: %+v", len(got), len(wantIDs), got)
	}
	for i, id := range wantIDs {
		if got[i].ExecId != id {
			t.Errorf("got[%d].ExecId = %q, want %q", i, got[i].ExecId, id)
		}
	}
}

// Same cutoff behavior as FetchAccountExecutionsSince, but for order history — unlike
// the pre-2026-10-17 syncOrderHistory (which walked every page with NO cutoff at all),
// this now bounds the walk to `since` too.
func TestFetchAccountOrderHistorySince_StopsAtSinceCutoff(t *testing.T) {
	now := time.Now()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Query().Get("cursor") {
		case "":
			fmt.Fprintf(w, `{"retCode":0,"retMsg":"OK","result":{"list":[{"orderId":"o1","createdTime":"%d"}],"nextPageCursor":"page2"}}`, now.UnixMilli())
		case "page2":
			fmt.Fprintf(w, `{"retCode":0,"retMsg":"OK","result":{"list":[{"orderId":"o2","createdTime":"%d"}],"nextPageCursor":"page3"}}`, now.Add(-10*time.Hour).UnixMilli())
		default:
			t.Error("requested a page beyond the one containing an order older than since")
			fmt.Fprint(w, `{"retCode":0,"retMsg":"OK","result":{"list":[],"nextPageCursor":""}}`)
		}
	}))
	defer srv.Close()
	withMockBybitBase(t, srv)

	since := now.Add(-6 * time.Hour)
	got, err := FetchAccountOrderHistorySince(context.Background(), Credentials{}, "linear", since)
	if err != nil {
		t.Fatalf("FetchAccountOrderHistorySince: %v", err)
	}
	if len(got) != 1 || got[0].OrderId != "o1" {
		t.Fatalf("got %+v, want exactly one order (o1)", got)
	}
}

// FetchAccountWhitelistedIPs must use unrestricted credentials (not creds.WhitelistedIPs
// — see the self-lock regression this mirrors in syncer_test.go) and normalize Bybit's
// wildcard ["*"] the same way the old inline refreshWhitelistedIPs code did.
func TestFetchAccountWhitelistedIPs_NormalizesWildcard(t *testing.T) {
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path != "/v5/user/query-api" {
			// e.g. /v5/market/time's timestamp-sync ping — not the call under test.
			w.WriteHeader(http.StatusNotFound)
			return
		}
		hits++
		fmt.Fprint(w, `{"retCode":0,"retMsg":"OK","result":{"ips":["*"]}}`)
	}))
	defer srv.Close()
	withMockBybitBase(t, srv)

	got, err := FetchAccountWhitelistedIPs(context.Background(), Credentials{APIKey: "k", SecretKey: "s", WhitelistedIPs: []string{"1.2.3.4"}})
	if err != nil {
		t.Fatalf("FetchAccountWhitelistedIPs: %v", err)
	}
	if got != nil {
		t.Errorf("got %v, want nil (wildcard \"*\" normalized to no-restriction)", got)
	}
	if hits != 1 {
		t.Errorf("hits = %d, want 1", hits)
	}
}
