package scanner

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/domain/media"
)

func TestDedupeLibrary_LibraryTypeGating(t *testing.T) {
	tests := []struct {
		libraryType  string
		wantCalled   bool
		wantItemType string
	}{
		{"show", true, "show"},
		{"movie", true, "movie"},
		{"music", false, ""},
		{"photo", false, ""},
		{"", false, ""},
		{"unknown", false, ""},
	}
	for _, tt := range tests {
		t.Run(tt.libraryType, func(t *testing.T) {
			svc := newMockMediaService()
			s := newTestScanner(svc)
			libID := uuid.New()

			s.dedupeLibrary(context.Background(), libID, tt.libraryType)

			if tt.wantCalled {
				if len(svc.dedupeCalls) != 1 {
					t.Fatalf("want 1 dedupe call, got %d", len(svc.dedupeCalls))
				}
				c := svc.dedupeCalls[0]
				if c.itemType != tt.wantItemType {
					t.Errorf("itemType: got %q, want %q", c.itemType, tt.wantItemType)
				}
				if c.libraryID != libID {
					t.Errorf("libraryID: got %s, want %s", c.libraryID, libID)
				}
			} else if len(svc.dedupeCalls) != 0 {
				t.Fatalf("want 0 dedupe calls for %q, got %d", tt.libraryType, len(svc.dedupeCalls))
			}
		})
	}
}

// An error from DedupeTopLevelItems must be swallowed — a dedupe failure
// should never fail the scan.
func TestDedupeLibrary_ErrorSwallowed(t *testing.T) {
	svc := newMockMediaService()
	svc.dedupeErr = errors.New("db down")
	s := newTestScanner(svc)

	// Should not panic and should return normally.
	s.dedupeLibrary(context.Background(), uuid.New(), "show")

	if len(svc.dedupeCalls) != 1 {
		t.Fatalf("want 1 dedupe call, got %d", len(svc.dedupeCalls))
	}
}

// When dedupe reports merges, the call still succeeds — the function has no
// return value; this test exists to pin the happy-path and protect against a
// future regression where a non-zero result crashes or re-fires dedupe.
func TestDedupeLibrary_LogsResultsAndReturns(t *testing.T) {
	svc := newMockMediaService()
	svc.dedupeResult = media.DedupeResult{
		MergedItems:    3,
		MergedSeasons:  5,
		MergedEpisodes: 12,
		ReparentedRows: 40,
	}
	s := newTestScanner(svc)

	s.dedupeLibrary(context.Background(), uuid.New(), "movie")

	if len(svc.dedupeCalls) != 1 {
		t.Fatalf("want 1 dedupe call, got %d", len(svc.dedupeCalls))
	}
	if got := svc.dedupeCalls[0].itemType; got != "movie" {
		t.Errorf("itemType: got %q, want %q", got, "movie")
	}
}
