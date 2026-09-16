package binance

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// withMockBinanceMainBase points binanceMainBase at srv for the duration of the test and
// restores the real value on cleanup — mirrors client_test.go's withMockBinanceBase, but
// for the general/spot API host that QueryAPIRestrictions uses instead of binanceBase.
func withMockBinanceMainBase(t *testing.T, srv *httptest.Server) {
	t.Helper()
	orig := binanceMainBase
	binanceMainBase = srv.URL
	t.Cleanup(func() { binanceMainBase = orig })
}

func TestQueryAPIRestrictions_SendsAPIKeyHeaderAndValidSignature(t *testing.T) {
	var gotPath string
	var gotAPIKeyHeader string
	var gotQuery url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAPIKeyHeader = r.Header.Get("X-MBX-APIKEY")
		gotQuery = r.URL.Query()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ipRestrict":true,"createTime":1623840271000,"enableWithdrawals":false,"enableInternalTransfer":true,"enableFutures":true,"enableReading":true,"enableSpotAndMarginTrading":false,"tradingAuthorityExpirationTime":0}`))
	}))
	defer srv.Close()
	withMockBinanceMainBase(t, srv)

	creds := testCreds()
	_, err := QueryAPIRestrictions(context.Background(), creds)
	if err != nil {
		t.Fatalf("QueryAPIRestrictions: %v", err)
	}
	if gotPath != "/sapi/v1/account/apiRestrictions" {
		t.Errorf("path = %q, want /sapi/v1/account/apiRestrictions", gotPath)
	}
	if gotAPIKeyHeader != "test-key" {
		t.Errorf("X-MBX-APIKEY header = %q, want %q", gotAPIKeyHeader, "test-key")
	}
	if gotQuery.Get("timestamp") == "" {
		t.Error("query missing timestamp")
	}
	if gotQuery.Get("signature") == "" {
		t.Error("query missing signature")
	}

	// The signature must actually validate: recompute it the same way signParams
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

func TestQueryAPIRestrictions_UsesMainBaseNotFuturesBase(t *testing.T) {
	var mainBaseHit bool
	mainSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mainBaseHit = true
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ipRestrict":false,"createTime":0,"enableWithdrawals":false,"enableInternalTransfer":false,"enableFutures":true,"enableReading":true,"enableSpotAndMarginTrading":false,"tradingAuthorityExpirationTime":0}`))
	}))
	defer mainSrv.Close()
	withMockBinanceMainBase(t, mainSrv)

	futuresSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("QueryAPIRestrictions must not hit binanceBase (the futures host)")
	}))
	defer futuresSrv.Close()
	withMockBinanceBase(t, futuresSrv)

	_, err := QueryAPIRestrictions(context.Background(), testCreds())
	if err != nil {
		t.Fatalf("QueryAPIRestrictions: %v", err)
	}
	if !mainBaseHit {
		t.Error("expected QueryAPIRestrictions to hit binanceMainBase")
	}
}

func TestQueryAPIRestrictions_ParsesResponseFields(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ipRestrict":true,"createTime":1623840271000,"enableWithdrawals":false,"enableInternalTransfer":true,"enableFutures":true,"enableReading":true,"enableSpotAndMarginTrading":false,"tradingAuthorityExpirationTime":0}`))
	}))
	defer srv.Close()
	withMockBinanceMainBase(t, srv)

	got, err := QueryAPIRestrictions(context.Background(), testCreds())
	if err != nil {
		t.Fatalf("QueryAPIRestrictions: %v", err)
	}
	want := APIRestrictions{
		IPRestrict:                     true,
		CreateTime:                     1623840271000,
		EnableWithdrawals:              false,
		EnableInternalTransfer:         true,
		EnableFutures:                  true,
		EnableReading:                  true,
		EnableSpotAndMarginTrading:     false,
		TradingAuthorityExpirationTime: 0,
	}
	if got != want {
		t.Errorf("QueryAPIRestrictions = %+v, want %+v", got, want)
	}
}

func TestQueryAPIRestrictions_ErrorResponseSurfacesAsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"code":-2015,"msg":"Invalid API-key, IP, or permissions for action."}`))
	}))
	defer srv.Close()
	withMockBinanceMainBase(t, srv)

	_, err := QueryAPIRestrictions(context.Background(), testCreds())
	if err == nil {
		t.Fatal("expected an error for a Binance error response")
	}
	if !strings.Contains(err.Error(), "-2015") || !strings.Contains(err.Error(), "Invalid API-key") {
		t.Errorf("error = %v, want it to mention code -2015 and the message", err)
	}
}
