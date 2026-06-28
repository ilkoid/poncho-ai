package main

import "testing"

// TestConfirmRealWrite covers the fail-closed gate for real WB writes. The guard exists
// because a dropped/mistyped --dry-run in a wrapper script once silently overwrote a
// card: without explicit opt-in, --apply/--auto must REFUSE rather than write.
func TestConfirmRealWrite(t *testing.T) {
	cases := []struct {
		name    string
		dryRun  bool
		yes     bool
		env     string
		wantErr bool
	}{
		{"dry-run only → no write, allowed", true, false, "", false},
		{"--yes confirms real write", false, true, "", false},
		{"env PENALTIES_DIMS_ALLOW_WRITE=1 confirms", false, false, "1", false},
		{"no opt-in → REFUSED (fail-closed)", false, false, "", true},
		{"dry-run takes precedence over --yes", true, true, "", false},
		{"env value other than \"1\" does NOT confirm", false, false, "true", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := confirmRealWrite(c.dryRun, c.yes, c.env)
			switch {
			case c.wantErr && err == nil:
				t.Fatalf("confirmRealWrite(dryRun=%v, yes=%v, env=%q) = nil, want error (refused)", c.dryRun, c.yes, c.env)
			case !c.wantErr && err != nil:
				t.Fatalf("confirmRealWrite(dryRun=%v, yes=%v, env=%q) = %v, want nil", c.dryRun, c.yes, c.env, err)
			}
		})
	}
}
