package trader

import "testing"

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
