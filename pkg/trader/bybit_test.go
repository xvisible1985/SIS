package trader

import (
	"errors"
	"fmt"
	"testing"
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
