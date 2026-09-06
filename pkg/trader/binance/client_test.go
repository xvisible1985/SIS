package binance

import (
	"context"
	"io"
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
		buf, _ := io.ReadAll(r.Body)
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

	_, err := doPublicGET(context.Background(), testCreds(), "/fapi/v1/premiumIndex", url.Values{"symbol": {"BTCUSDT"}})
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

func TestDoSignedDELETE_SendsAPIKeyHeaderAndValidSignature(t *testing.T) {
	var gotMethod string
	var gotAPIKeyHeader string
	var gotQuery url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotAPIKeyHeader = r.Header.Get("X-MBX-APIKEY")
		gotQuery = r.URL.Query()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()
	withMockBinanceBase(t, srv)

	_, err := doSignedDELETE(context.Background(), testCreds(), "/fapi/v1/order", url.Values{"symbol": {"BTCUSDT"}, "orderId": {"5"}})
	if err != nil {
		t.Fatalf("doSignedDELETE: %v", err)
	}
	if gotMethod != http.MethodDelete {
		t.Errorf("method = %q, want DELETE", gotMethod)
	}
	if gotAPIKeyHeader != "test-key" {
		t.Errorf("X-MBX-APIKEY header = %q, want %q", gotAPIKeyHeader, "test-key")
	}
	if gotQuery.Get("symbol") != "BTCUSDT" {
		t.Errorf("query symbol = %q, want BTCUSDT", gotQuery.Get("symbol"))
	}
	if gotQuery.Get("signature") == "" {
		t.Error("query missing signature")
	}
}
