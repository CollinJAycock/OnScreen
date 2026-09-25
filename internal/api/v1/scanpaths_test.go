package v1

import (
	"runtime"
	"testing"
)

// TestValidateScanPaths pins the scan-path rules shared by library create and
// update (update used to skip them entirely).
func TestValidateScanPaths(t *testing.T) {
	bad := [][]string{nil, {""}, {"  "}, {"/"}, {"/srv/../etc"}, {"/srv/.."}, {"relative/path"}}
	for _, p := range bad {
		if err := validateScanPaths(p); err == nil {
			t.Errorf("expected rejection for %q", p)
		}
	}
	good := [][]string{
		{"/mnt/media/Movies", "/srv/tv"},
		{"/mnt/media/Movies..Archive"}, // ".." inside a name is not a traversal
	}
	if runtime.GOOS == "windows" {
		good = append(good,
			[]string{`\\nas\Movies`}, []string{`\\nas\Movies\`}, // UNC share roots
			[]string{`C:\Media\TV`},
			[]string{`Z:\`}, // a dedicated media drive (never the system drive here)
		)
		bad = append(bad, []string{`C:\`}, []string{`C:\Media\..\Windows`})
	}
	for _, p := range good {
		if err := validateScanPaths(p); err != nil {
			t.Errorf("valid paths %q rejected: %v", p, err)
		}
	}
	for _, p := range bad {
		if err := validateScanPaths(p); err == nil {
			t.Errorf("expected rejection for %q", p)
		}
	}
}
