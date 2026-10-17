package binance

import (
	"net/url"
	"strconv"
)

// binanceWSIntParams is the fixed set of WS request fields Binance expects as JSON
// integers rather than strings — confirmed via Binance's own documented example
// payloads (INT-typed params like timestamp/recvWindow are unquoted; DECIMAL/STRING
// params like quantity/price/symbol stay quoted, exactly as REST's form-encoded body
// already treats them as strings for signing purposes).
var binanceWSIntParams = map[string]bool{
	"timestamp":  true,
	"recvWindow": true,
}

// binanceWSBoolParams is the (currently singleton) set of WS request fields Binance
// expects as a JSON boolean rather than a string — REST sends "true" as a literal
// string in the form-encoded body; the WS JSON envelope expects a real `true`.
var binanceWSBoolParams = map[string]bool{
	"reduceOnly": true,
}

// wsParamsToJSON converts a signed url.Values (built and signed exactly like a REST
// request — see signParams in client.go) into the map[string]any the WS JSON envelope's
// "params" object needs, converting only the fields Binance expects as non-string JSON
// types. The signature itself is unaffected: it's computed over params.Encode() (the
// all-strings query form) BEFORE this conversion ever runs, so signing math never needs
// to know about WS-specific JSON typing at all.
func wsParamsToJSON(params url.Values) map[string]any {
	out := make(map[string]any, len(params))
	for k := range params {
		v := params.Get(k)
		switch {
		case binanceWSIntParams[k]:
			if n, err := strconv.ParseInt(v, 10, 64); err == nil {
				out[k] = n
				continue
			}
		case binanceWSBoolParams[k]:
			if b, err := strconv.ParseBool(v); err == nil {
				out[k] = b
				continue
			}
		}
		out[k] = v
	}
	return out
}
