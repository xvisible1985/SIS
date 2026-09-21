package signal

import (
	"testing"
	"time"
)

func TestTimesfmForecast_MissingKey_ReturnsNotFresh(t *testing.T) {
	pct, fresh := GetTimesfmForecast("TFSTATE_MISSING", "5m", 100, 12, time.Minute)
	if fresh {
		t.Error("fresh = true for a key that was never set, want false")
	}
	if pct != 0 {
		t.Errorf("predictedPct = %v, want 0 for a missing key", pct)
	}
}

func TestTimesfmForecast_SetThenGet_FreshWithinMaxAge(t *testing.T) {
	SetTimesfmForecast("TFSTATE_FRESH", "5m", 100, 12, 1.23)
	pct, fresh := GetTimesfmForecast("TFSTATE_FRESH", "5m", 100, 12, time.Minute)
	if !fresh {
		t.Error("fresh = false immediately after Set, want true")
	}
	if pct != 1.23 {
		t.Errorf("predictedPct = %v, want 1.23", pct)
	}
}

func TestTimesfmForecast_StaleAfterMaxAge(t *testing.T) {
	SetTimesfmForecast("TFSTATE_STALE", "5m", 100, 12, 5.0)
	time.Sleep(5 * time.Millisecond)
	pct, fresh := GetTimesfmForecast("TFSTATE_STALE", "5m", 100, 12, time.Millisecond)
	if fresh {
		t.Error("fresh = true after maxAge elapsed, want false")
	}
	// The last known value is still returned even when stale — callers derive a
	// best-effort state from it while a fresh refresh is (maybe) in flight.
	if pct != 5.0 {
		t.Errorf("predictedPct = %v, want 5.0 (stale but still returned)", pct)
	}
}

func TestTimesfmForecast_DifferentContextOrHorizon_DoesNotCollide(t *testing.T) {
	SetTimesfmForecast("TFSTATE_KEYS", "5m", 100, 12, 1.0)
	SetTimesfmForecast("TFSTATE_KEYS", "5m", 200, 12, 2.0)
	SetTimesfmForecast("TFSTATE_KEYS", "5m", 100, 24, 3.0)

	if pct, _ := GetTimesfmForecast("TFSTATE_KEYS", "5m", 100, 12, time.Minute); pct != 1.0 {
		t.Errorf("context=100/horizon=12 = %v, want 1.0", pct)
	}
	if pct, _ := GetTimesfmForecast("TFSTATE_KEYS", "5m", 200, 12, time.Minute); pct != 2.0 {
		t.Errorf("context=200/horizon=12 = %v, want 2.0", pct)
	}
	if pct, _ := GetTimesfmForecast("TFSTATE_KEYS", "5m", 100, 24, time.Minute); pct != 3.0 {
		t.Errorf("context=100/horizon=24 = %v, want 3.0", pct)
	}
}

func TestTimesfmRefresh_InflightDedup(t *testing.T) {
	// maxAge=0 here: this test is exercising inflight dedup specifically, not the
	// refresh-attempt cooldown (see TestTryStartTimesfmRefresh_DoesNotRetryWithinMaxAgeAfterFinish
	// for that) — a zero maxAge means time.Since(last) < maxAge is never true, so the cooldown
	// gate never blocks the post-finish retry this test asserts on below.
	if !tryStartTimesfmRefresh("TFSTATE_INFLIGHT", "5m", 100, 12, 0) {
		t.Fatal("first tryStartTimesfmRefresh = false, want true")
	}
	if tryStartTimesfmRefresh("TFSTATE_INFLIGHT", "5m", 100, 12, 0) {
		t.Error("second tryStartTimesfmRefresh while first still in flight = true, want false")
	}
	finishTimesfmRefresh("TFSTATE_INFLIGHT", "5m", 100, 12)
	if !tryStartTimesfmRefresh("TFSTATE_INFLIGHT", "5m", 100, 12, 0) {
		t.Error("tryStartTimesfmRefresh after finish = false, want true")
	}
	finishTimesfmRefresh("TFSTATE_INFLIGHT", "5m", 100, 12) // cleanup
}

// TestTryStartTimesfmRefresh_DoesNotRetryWithinMaxAgeAfterFinish is the regression for the
// retry-storm bug found in review: on a FAILED refresh, SetTimesfmForecast is never called, so
// GetTimesfmForecast's freshness check can never throttle the next attempt on its own —
// tryStartTimesfmRefresh must independently remember when the last attempt (successful or
// failed) started, and refuse a new one until maxAge has passed, the same minimum spacing a
// successful refresh already gets for free via the cache's own updatedAt.
func TestTryStartTimesfmRefresh_DoesNotRetryWithinMaxAgeAfterFinish(t *testing.T) {
	const maxAge = 100 * time.Millisecond

	if !tryStartTimesfmRefresh("TFSTATE_COOLDOWN", "5m", 100, 12, maxAge) {
		t.Fatal("first tryStartTimesfmRefresh = false, want true")
	}
	finishTimesfmRefresh("TFSTATE_COOLDOWN", "5m", 100, 12)

	if tryStartTimesfmRefresh("TFSTATE_COOLDOWN", "5m", 100, 12, maxAge) {
		t.Error("tryStartTimesfmRefresh immediately after finish, within maxAge = true, want false")
	}

	time.Sleep(maxAge + 20*time.Millisecond)

	if !tryStartTimesfmRefresh("TFSTATE_COOLDOWN", "5m", 100, 12, maxAge) {
		t.Error("tryStartTimesfmRefresh after maxAge elapsed = false, want true")
	}
	finishTimesfmRefresh("TFSTATE_COOLDOWN", "5m", 100, 12) // cleanup
}
