package gen_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/onscreen/onscreen/internal/domain/media"
)

// TestMediaItemWritesHonourLockedFields fails when a query writes a lockable
// metadata column of media_items (media.LockableFields) without consulting
// locked_fields. An admin's edit locks those fields; a new scan, enrichment
// or import path that writes them unguarded would silently undo it. Guard
// the column with `CASE WHEN '<field>' = ANY(locked_fields) THEN <field>
// ELSE … END`, as UpdateMediaItemMetadata does.
func TestMediaItemWritesHonourLockedFields(t *testing.T) {
	files, err := filepath.Glob(filepath.Join("..", "queries", "*.sql"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no query files found: %v", err)
	}
	writesItems := regexp.MustCompile(`(?is)\bUPDATE\s+media_items\b|\bINSERT\s+INTO\s+media_items\b.*\bON\s+CONFLICT\b`)
	assigns := regexp.MustCompile(`(?im)(?:^\s*|\bSET\s+)(` + strings.Join(media.LockableFields, "|") + `)\s*=[^=]`)
	checked := 0
	for _, f := range files {
		raw, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, q := range strings.Split(string(raw), "-- name: ")[1:] {
			name, _, _ := strings.Cut(q, " ")
			if !writesItems.MatchString(q) {
				continue
			}
			m := assigns.FindStringSubmatch(q)
			if m == nil {
				continue
			}
			checked++
			if !strings.Contains(q, "locked_fields") {
				t.Errorf("%s (%s) writes media_items.%s without checking locked_fields",
					name, filepath.Base(f), m[1])
			}
		}
	}
	// UpdateMediaItemMetadata, UpdateMediaItemTitle, UpdateMediaItemTakenAt
	// and EditMediaItemMetadata at least; fewer means the matcher broke.
	if checked < 4 {
		t.Errorf("only %d lockable-column writes found; the matcher is likely broken", checked)
	}
}
