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
