package main

import (
	"testing"
	"time"

	traderPkg "sis/pkg/trader"
)

// Regression (2026-10-17): the P&L tab fully unmounts/remounts on every tab switch
// (TerminalPage.tsx's conditional render), re-issuing a full REST fetch every single
// time with no caching anywhere in the path — these tests pin closedPnlCache's basic
// get/set/expiry/key-scoping behavior in isolation, without needing a live server or DB.

func TestClosedPnlCache_SetThenGet_ReturnsWithinTTL(t *testing.T) {
	key := closedPnlCacheKey("acct-1", "linear", "")
	want := []traderPkg.ClosedPnl{{Symbol: "BTCUSDT", OrderId: "1"}}
	setClosedPnlCache(key, want)
	t.Cleanup(func() {
		closedPnlCacheMu.Lock()
		delete(closedPnlCache, key)
		closedPnlCacheMu.Unlock()
	})

	got, ok := getClosedPnlCache(key)
	if !ok {
		t.Fatal("expected a cache hit immediately after set")
	}
	if len(got) != 1 || got[0].Symbol != "BTCUSDT" {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

func TestClosedPnlCache_MissForUnknownKey(t *testing.T) {
	_, ok := getClosedPnlCache(closedPnlCacheKey("never-set", "linear", ""))
	if ok {
		t.Error("expected a cache miss for a key that was never set")
	}
}

func TestClosedPnlCache_ExpiresAfterTTL(t *testing.T) {
	key := closedPnlCacheKey("acct-2", "linear", "")
	closedPnlCacheMu.Lock()
	closedPnlCache[key] = closedPnlCacheEntry{
		list:      []traderPkg.ClosedPnl{{Symbol: "ETHUSDT"}},
		expiresAt: time.Now().Add(-time.Second), // already expired
	}
	closedPnlCacheMu.Unlock()
	t.Cleanup(func() {
		closedPnlCacheMu.Lock()
		delete(closedPnlCache, key)
		closedPnlCacheMu.Unlock()
	})

	_, ok := getClosedPnlCache(key)
	if ok {
		t.Error("expected a cache miss for an expired entry")
	}
}

func TestClosedPnlCacheKey_ScopesByAccountCategoryAndCursor(t *testing.T) {
	a := closedPnlCacheKey("acct-1", "linear", "")
	b := closedPnlCacheKey("acct-1", "linear", "page2")
	c := closedPnlCacheKey("acct-1", "spot", "")
	d := closedPnlCacheKey("acct-2", "linear", "")
	keys := []string{a, b, c, d}
	for i := range keys {
		for j := range keys {
			if i != j && keys[i] == keys[j] {
				t.Errorf("keys[%d]=%q collides with keys[%d]=%q, want distinct keys for distinct (account,category,cursor)", i, keys[i], j, keys[j])
			}
		}
	}
}
