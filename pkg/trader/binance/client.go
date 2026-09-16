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

	"sis/pkg/proxy"
	"sis/pkg/trader"
)

// binanceBase is a var (not const) so tests can point it at an httptest.Server —
// mirrors pkg/trader/bybit.go's bybitBase.
var binanceBase = "https://fapi.binance.com"

// binanceMainBase is Binance's general/spot API host — a var (not const), same reason as
// binanceBase, so account_restrictions_test.go can point it at an httptest.Server.
// /sapi/v1/account/apiRestrictions (used by QueryAPIRestrictions) lives here, not on the
// futures host every other call in this package uses.
var binanceMainBase = "https://api.binance.com"

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
//
// Note: this mutates and returns the same params map passed in (params.Set calls
// below) rather than copying it — fine for every current call site, which always
// passes a freshly built url.Values literal, but worth knowing if a caller ever
// starts reusing/sharing a url.Values across calls.
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

// doRequest issues the actual HTTP call. creds is used for two independent things:
// apiKey (usually creds.APIKey for signed calls, "" for public calls with no header)
// controls the X-MBX-APIKEY header, while creds.WhitelistedIPs always picks the
// account's proxy via proxy.HTTPClientFor — even for public/unauthenticated endpoints,
// so every account's traffic (signed or not) consistently routes through its own
// proxy/IP, matching pkg/trader/bybit.go's doSignedGET/doSignedPOST pattern.
// proxy.HTTPClientFor also guarantees a request timeout, unlike http.DefaultClient.
func doRequest(ctx context.Context, method, path string, values url.Values, creds trader.Credentials, apiKey string, body bool) ([]byte, error) {
	return doRequestToBase(ctx, binanceBase, method, path, values, creds, apiKey, body)
}

// doRequestToBase is doRequest's actual implementation, parameterized by host — added so
// account_restrictions.go can call Binance's general/spot API host (api.binance.com)
// instead of the futures host (fapi.binance.com) every other call in this package uses,
// while sharing the exact same signing/proxy/error-handling logic. doRequest itself
// remains the entry point for every existing (futures-host) call site, unchanged.
func doRequestToBase(ctx context.Context, base, method, path string, values url.Values, creds trader.Credentials, apiKey string, body bool) ([]byte, error) {
	var req *http.Request
	var err error
	if body {
		// Passing the *strings.Reader directly (rather than assigning req.Body after
		// constructing the request with a nil body) lets net/http set ContentLength and
		// GetBody automatically, so the request is sent with a known Content-Length
		// instead of chunked transfer encoding.
		req, err = http.NewRequestWithContext(ctx, method, base+path, strings.NewReader(values.Encode()))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	} else {
		full := base + path
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
	resp, err := proxy.HTTPClientFor(creds.WhitelistedIPs).Do(req)
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
	return doRequest(ctx, http.MethodGet, path, signParams(creds, params), creds, creds.APIKey, false)
}

// doSignedPOST issues a signed POST — params go in the form-urlencoded body, not JSON
// (a real difference from Bybit, which sends a JSON body — see general-info docs).
func doSignedPOST(ctx context.Context, creds trader.Credentials, path string, params url.Values) ([]byte, error) {
	return doRequest(ctx, http.MethodPost, path, signParams(creds, params), creds, creds.APIKey, true)
}

// doSignedDELETE issues a signed DELETE — params go in the query string, same as GET.
func doSignedDELETE(ctx context.Context, creds trader.Credentials, path string, params url.Values) ([]byte, error) {
	return doRequest(ctx, http.MethodDelete, path, signParams(creds, params), creds, creds.APIKey, false)
}

// doPublicGET issues an unauthenticated GET — no API key header, no signature — but
// still routes through creds' proxy/whitelisted-IP, same as every signed call, so a
// given account's public traffic (e.g. mark price) originates from the same IP as its
// signed traffic. Used only for genuinely public endpoints.
func doPublicGET(ctx context.Context, creds trader.Credentials, path string, params url.Values) ([]byte, error) {
	return doRequest(ctx, http.MethodGet, path, params, creds, "", false)
}
