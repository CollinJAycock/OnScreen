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
		libraryType   string
		wantItemTypes []string
	}{
		{"show", []string{"show"}},
		{"movie", []string{"movie"}},
		// Music triggers a collab-artist merge, then artist dedupe (and album
		// dedupe via per-artist walk). Album dedupe fires only when artists
		// exist; the mock has none so we only expect the two artist-level
		// calls here.
		{"music", []string{"collab-artist", "artist"}},
		{"photo", nil},
		{"", nil},
		{"unknown", nil},
	}
	for _, tt := range tests {
		t.Run(tt.libraryType, func(t *testing.T) {
			svc := newMockMediaService()
			s := newTestScanner(svc)
			libID := uuid.New()

			s.dedupeLibrary(context.Background(), libID, tt.libraryType)

			if len(svc.dedupeCalls) != len(tt.wantItemTypes) {
				t.Fatalf("want %d dedupe calls, got %d", len(tt.wantItemTypes), len(svc.dedupeCalls))
			}
			for i, want := range tt.wantItemTypes {
				c := svc.dedupeCalls[i]
				if c.itemType != want {
					t.Errorf("call[%d] itemType: got %q, want %q", i, c.itemType, want)
				}
				if c.libraryID != libID {
					t.Errorf("call[%d] libraryID: got %s, want %s", i, c.libraryID, libID)
				}
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
