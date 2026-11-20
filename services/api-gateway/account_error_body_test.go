package main

import (
	"errors"
	"fmt"
	"testing"

	"sis/pkg/proxy"
)

func TestExchangeErrorBody_MarksProxyFailuresAsServerUnavailable(t *testing.T) {
	flattened := errors.New(`bybit: Get "https://api.bybit.com/v5/account/wallet-balance": proxy: no healthy proxy available (proxies configured but all unhealthy)`)
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"sentinel", proxy.ErrNoHealthyProxy, true},
		{"wrapped sentinel", fmt.Errorf("bybit: %w", proxy.ErrNoHealthyProxy), true},
		{"whitelist sentinel", proxy.ErrNoWhitelistedProxy, true},
		{"flattened with %v", flattened, true},
		{"proxy connect failure", errors.New("proxyconnect tcp: dial tcp 1.2.3.4:3128: i/o timeout"), true},
		{"exchange rejection is not an outage", errors.New("bybit: retCode=10003: API key is invalid"), false},
	}
	for _, c := range cases {
		body := exchangeErrorBody(c.err)
		if body["ok"] != false || body["message"] != c.err.Error() {
			t.Errorf("%s: body = %v", c.name, body)
		}
		if got := body["code"] == "server_unavailable"; got != c.want {
			t.Errorf("%s: server_unavailable = %v, want %v", c.name, got, c.want)
		}
	}
}
