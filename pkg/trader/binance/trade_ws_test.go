package binance

import (
	"net/url"
	"testing"
)

// Regression target: Binance's WS API rejects (or silently misbehaves on) INT-typed
// params sent as quoted JSON strings — timestamp/recvWindow must be raw JSON numbers;
// everything else (including numeric-looking decimal fields like quantity/price) must
// stay a JSON string, matching Binance's own documented request examples exactly.
func TestWsParamsToJSON_IntFieldsBecomeNumbers_OthersStayStrings(t *testing.T) {
	params := url.Values{
		"symbol":     {"BTCUSDT"},
		"quantity":   {"0.01000000"},
		"price":      {"43187.00"},
		"timestamp":  {"1645423376532"},
		"recvWindow": {"5000"},
		"apiKey":     {"abc123"},
		"signature":  {"deadbeef"},
	}
	got := wsParamsToJSON(params)

	if v, ok := got["timestamp"].(int64); !ok || v != 1645423376532 {
		t.Errorf("timestamp = %#v (%T), want int64(1645423376532)", got["timestamp"], got["timestamp"])
	}
	if v, ok := got["recvWindow"].(int64); !ok || v != 5000 {
		t.Errorf("recvWindow = %#v (%T), want int64(5000)", got["recvWindow"], got["recvWindow"])
	}
	for _, key := range []string{"symbol", "quantity", "price", "apiKey", "signature"} {
		v, ok := got[key].(string)
		if !ok {
			t.Errorf("%s = %#v (%T), want a string", key, got[key], got[key])
			continue
		}
		if v != params.Get(key) {
			t.Errorf("%s = %q, want %q", key, v, params.Get(key))
		}
	}
}

func TestWsParamsToJSON_ReduceOnlyBecomesBool(t *testing.T) {
	params := url.Values{"reduceOnly": {"true"}}
	got := wsParamsToJSON(params)
	v, ok := got["reduceOnly"].(bool)
	if !ok || v != true {
		t.Errorf("reduceOnly = %#v (%T), want bool(true)", got["reduceOnly"], got["reduceOnly"])
	}
}
