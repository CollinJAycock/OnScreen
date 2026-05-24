package v1

import "testing"

func TestABRLadderCap(t *testing.T) {
	cases := []struct {
		name                            string
		requested, autoMax, hardCap, want int
	}{
		// Auto (no pick) uses the soft default so it won't build a 4K rung.
		{"auto uses soft default", 0, 1080, 0, 1080},
		// Explicit pick wins over the Auto default.
		{"explicit pick overrides auto default", 720, 1080, 0, 720},
		// Explicit 4K pick honored when no caps constrain it.
		{"explicit 2160 with no caps", 2160, 0, 0, 2160},
		// Hard operator cap clamps an explicit pick.
		{"hard cap clamps explicit pick", 2160, 1080, 1440, 1440},
		// Hard cap below the Auto default wins for Auto.
		{"hard cap below auto default", 0, 1080, 720, 720},
		// Auto default disabled (0) + no hard cap => uncapped (source height).
		{"auto uncapped when both zero", 0, 0, 0, 0},
		// Auto default above hard cap => hard cap wins.
		{"hard cap below auto default again", 0, 2160, 1080, 1080},
		// Explicit pick already under everything is returned unchanged.
		{"explicit pick under all caps", 480, 1080, 2160, 480},
	}
	for _, c := range cases {
		if got := abrLadderCap(c.requested, c.autoMax, c.hardCap); got != c.want {
			t.Errorf("%s: abrLadderCap(%d,%d,%d) = %d, want %d",
				c.name, c.requested, c.autoMax, c.hardCap, got, c.want)
		}
	}
}
