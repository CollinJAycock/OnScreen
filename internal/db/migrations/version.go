package migrations

import (
	"io/fs"
	"strconv"
	"strings"
)

// HighestVersion returns the highest NNNNN_*.sql version found in the given
// migrations FS. Used by the readiness check, the backup/restore version
// gate, and anywhere else that needs to know "what schema version does this
// binary expect."
func HighestVersion(migFS fs.FS) (int64, error) {
	entries, err := fs.ReadDir(migFS, ".")
	if err != nil {
		return 0, err
	}
	var max int64
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".sql") {
			continue
		}
		idx := strings.IndexByte(e.Name(), '_')
		if idx <= 0 {
			continue
		}
		v, err := strconv.ParseInt(e.Name()[:idx], 10, 64)
		if err != nil {
			continue
		}
		if v > max {
			max = v
		}
	}
	return max, nil
}

// Highest returns HighestVersion against the embedded FS. Convenience for
// the common case where the caller is using the package-local migrations.
func Highest() (int64, error) {
	return HighestVersion(FS)
}
