package main

import "testing"

func TestBotFilterVerdict(t *testing.T) {
	const signal, hedge, other, linked = "sig-bot", "hedge-bot", "other-bot", "main-strategy"
	wl := []string{signal}
	cases := []struct {
		name                           string
		bot, origin, flip, hedged, own string
		wl, bl                         []string
		want                           bool
	}{
		{"owned by whitelisted bot", signal, "", "", "", hedge, wl, nil, true},
		{"owned by another bot", other, "", "", "", hedge, wl, nil, false},
		{"orphan main promoted from this hedge bot (flip origin)", "", hedge, hedge, "", hedge, wl, nil, true},
		{"orphan main, origin only", "", hedge, "", "", hedge, wl, nil, true},
		{"orphan main of a different hedge bot", "", other, other, "", hedge, wl, nil, false},
		{"orphan main with no origin (manual)", "", "", "", "", hedge, wl, nil, false},
		{"orphan without own bot id (legacy callers)", "", hedge, hedge, "", "", wl, nil, false},
		{"bot-owned main is not rescued by origin", other, hedge, hedge, "", hedge, wl, nil, false},
		{"blacklisted bot always blocked", signal, "", "", "", hedge, wl, []string{signal}, false},
		{"blacklist only, orphan allowed", "", "", "", "", hedge, nil, []string{other}, true},
		// hedge leg promoted to main in place by a flip: still bot-owned, hedges nothing any more
		{"promoted own leg (bot-owned, not a hedge)", hedge, hedge, "", "", hedge, wl, nil, true},
		{"own leg that still hedges something is not a main", hedge, "", "", linked, hedge, wl, nil, false},
		{"own leg without own bot id (force activation)", hedge, "", "", "", "", wl, nil, false},
		{"another hedge bot's leg is not ours", other, "", "", "", hedge, wl, nil, false},
		{"own leg blacklisted stays blocked", hedge, "", "", "", hedge, wl, []string{hedge}, false},
	}
	for _, c := range cases {
		if got := botFilterVerdict(c.bot, c.origin, c.flip, c.hedged, c.own, c.wl, c.bl); got != c.want {
			t.Errorf("%s: got %v want %v", c.name, got, c.want)
		}
	}
}
