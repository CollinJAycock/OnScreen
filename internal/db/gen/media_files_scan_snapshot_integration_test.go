//go:build integration

package gen_test

import (
	"context"
	"testing"

	"github.com/onscreen/onscreen/internal/db/gen"
	"github.com/onscreen/onscreen/internal/testdb"
)

// The scanner's per-scan reads: ListFileScanSnapshotsForLibrary is every
// file row of the library (any status, as GetMediaFileByPath would find it)
// with the heal-gate columns and the credits-read flag; ListActiveFileRefs-
// ForLibrary is the active rows only. Neither reaches into another library.
func TestMediaFile_Integration_ScanSnapshotAndActiveRefs(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	q := gen.New(pool)

	libID := seedLibrary(ctx, t, q, "snapshot")
	otherLib := seedLibrary(ctx, t, q, "other")
	itemID := seedMediaItem(ctx, t, q, libID, "Movie")
	otherItem := seedMediaItem(ctx, t, q, otherLib, "Elsewhere")

	hash := "abc123"
	active, err := q.CreateMediaFile(ctx, gen.CreateMediaFileParams{
		MediaItemID: itemID, FilePath: "/media/Movie cd1.mkv", FileSize: 42, FileHash: &hash,
		HdrType: integStrPtr("dolby_vision"), DvProfile: dvPtr(8), Edition: integStrPtr("Extended"), PartIndex: dvPtr(1),
	})
	if err != nil {
		t.Fatalf("CreateMediaFile: %v", err)
	}
	missing, err := q.CreateMediaFile(ctx, gen.CreateMediaFileParams{
		MediaItemID: itemID, FilePath: "/media/Movie cd2.mkv", FileSize: 7,
	})
	if err != nil {
		t.Fatalf("CreateMediaFile: %v", err)
	}
	if err := q.MarkMediaFileMissing(ctx, missing.ID); err != nil {
		t.Fatalf("MarkMediaFileMissing: %v", err)
	}
	if _, err := q.CreateMediaFile(ctx, gen.CreateMediaFileParams{
		MediaItemID: otherItem, FilePath: "/other/Elsewhere.mkv", FileSize: 1,
	}); err != nil {
		t.Fatalf("CreateMediaFile: %v", err)
	}
	if err := q.MarkTrackCreditsScanned(ctx, active.ID); err != nil {
		t.Fatalf("MarkTrackCreditsScanned: %v", err)
	}

	snaps, err := q.ListFileScanSnapshotsForLibrary(ctx, libID)
	if err != nil {
		t.Fatalf("ListFileScanSnapshotsForLibrary: %v", err)
	}
	if len(snaps) != 2 {
		t.Fatalf("snapshot rows = %d, want 2 (active + missing, not the other library's)", len(snaps))
	}
	byPath := map[string]gen.ListFileScanSnapshotsForLibraryRow{}
	for _, s := range snaps {
		byPath[s.FilePath] = s
	}
	a, m := byPath[active.FilePath], byPath[missing.FilePath]
	if a.ID != active.ID || a.MediaItemID != itemID || a.FileSize != 42 || a.FileHash == nil || *a.FileHash != hash ||
		a.Status != "active" || !a.ScannedAt.Valid {
		t.Errorf("active snapshot = %+v", a)
	}
	if a.HdrType == nil || *a.HdrType != "dolby_vision" || a.DvProfile == nil || *a.DvProfile != 8 ||
		a.Edition == nil || *a.Edition != "Extended" || a.PartIndex == nil || *a.PartIndex != 1 {
		t.Errorf("active snapshot heal columns = hdr %v dv %v edition %v part %v", a.HdrType, a.DvProfile, a.Edition, a.PartIndex)
	}
	if !a.CreditsScanned || m.CreditsScanned {
		t.Errorf("credits_scanned = %v / %v, want true / false", a.CreditsScanned, m.CreditsScanned)
	}
	if m.Status != "missing" {
		t.Errorf("missing row status = %q", m.Status)
	}

	refs, err := q.ListActiveFileRefsForLibrary(ctx, libID)
	if err != nil {
		t.Fatalf("ListActiveFileRefsForLibrary: %v", err)
	}
	if len(refs) != 1 || refs[0].ID != active.ID || refs[0].MediaItemID != itemID || refs[0].FilePath != active.FilePath {
		t.Errorf("active refs = %+v, want only %s", refs, active.FilePath)
	}
}
