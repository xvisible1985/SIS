package strategy

import (
	"testing"
	"time"
)

// TestResolveCloseResult: an expected-close reason set recently is used verbatim; one
// set too long ago (past the TTL) or never set at all falls back to the default.
func TestResolveCloseResult(t *testing.T) {
	cases := []struct {
		name   string
		reason string
		setAt  time.Time
		want   string
	}{
		{"no reason set", "", time.Time{}, "manual_close"},
		{"reason set recently", "paired_close", time.Now().Add(-30 * time.Second), "paired_close"},
		{"reason set at the TTL edge (within)", "paired_close", time.Now().Add(-119 * time.Second), "paired_close"},
		{"reason set too long ago", "paired_close", time.Now().Add(-3 * time.Minute), "manual_close"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := resolveCloseResult(c.reason, c.setAt)
			if got != c.want {
				t.Errorf("resolveCloseResult(%q, %v) = %q, want %q", c.reason, c.setAt, got, c.want)
			}
		})
	}
}
