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
		// Passing the *strings.Reader directly (rather than assigning req.Body after
		// constructing the request with a nil body) lets net/http set ContentLength and
		// GetBody automatically, so the request is sent with a known Content-Length
		// instead of chunked transfer encoding.
		req, err = http.NewRequestWithContext(ctx, method, binanceBase+path, strings.NewReader(values.Encode()))
		if err != nil {
			return nil, err
		}
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
