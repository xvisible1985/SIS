package strategy

import "testing"

func TestParseStrategyLinkID(t *testing.T) {
	cases := []struct {
		name     string
		linkID   string
		wantOK   bool
		wantKind LinkIDKind
		wantID8  string
	}{
		{"matrix TP positive slot", "SIS_STR-286130b1-tpl2-1-83452541", true, LinkIDMatrixTP, "286130b1"},
		{"matrix TP negative slot", "SIS_STR-286130b1-tpln1-1-83452541", true, LinkIDMatrixTP, "286130b1"},
		{"matrix per-level SL positive", "SIS_STR-479024f8-msl-3-7", true, LinkIDMatrixLevelSL, "479024f8"},
		{"matrix per-level SL negative", "SIS_STR-479024f8-msl-n1-7", true, LinkIDMatrixLevelSL, "479024f8"},
		{"grid TP", "SIS_STR-aaa41b1f-tp-1-12345", true, LinkIDGridTP, "aaa41b1f"},
		{"grid SL", "SIS_STR-aaa41b1f-sl-1-12345", true, LinkIDGridSL, "aaa41b1f"},
		{"self-close (ghost/remnant/full-position market close)", "SIS_STR-aaa41b1f-scl-1-12345", true, LinkIDSelfClose, "aaa41b1f"},
		{"level entry fill — recognized prefix, not a close kind", "SIS_STR-d1c35a82-1-3-0", true, LinkIDUnrecognized, "d1c35a82"},
		{"virtual level — recognized prefix, not a close kind", "SIS_STR-d1c35a82-1-3-0-v", true, LinkIDUnrecognized, "d1c35a82"},
		{"paired-close order — no strategy id, not ours in this sense", "SIS_MPC_1_1720000000000", false, LinkIDUnrecognized, ""},
		{"detach close order", "SIS_DTH_1720000000000", false, LinkIDUnrecognized, ""},
		{"empty", "", false, LinkIDUnrecognized, ""},
		{"external/manual exchange order", "abc-123-manual", false, LinkIDUnrecognized, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, ok := ParseStrategyLinkID(c.linkID)
			if ok != c.wantOK {
				t.Fatalf("ok = %v, want %v", ok, c.wantOK)
			}
			if !ok {
				return
			}
			if got.Kind != c.wantKind {
				t.Errorf("Kind = %v, want %v", got.Kind, c.wantKind)
			}
			if got.StrategyID8 != c.wantID8 {
				t.Errorf("StrategyID8 = %q, want %q", got.StrategyID8, c.wantID8)
			}
		})
	}
}
