package scanner

import (
	"context"
	"sync"

	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/domain/media"
)

// scanState is what one scan run reads once instead of once per file. It
// lives on the scan's context (withScanState) so it is scoped to that run:
// two libraries scanning at once, or a watcher's directory scan beside a
// full scan of the same library, each see only their own.
//
// Without it a rescan of an unchanged library paid four or five queries per
// file — the file row by path, the owning item, and for an episode or a
// track the two parents parentNeedsEnrich reads (plus, after a restart, the
// track's credits-read flag) — about a million round trips for a 200k-track
// library whose files had not changed at all.
type scanState struct {
	// files holds the library's file rows, by path, as they were when the
	// scan started (full scans only; nil otherwise, and once the file pass
	// is over). A path that isn't here is looked up as before, so a file
	// indexed after the snapshot, or under another library, is still found.
	files map[string]media.FileScanSnapshot

	// parents caches the parent items parentNeedsEnrich reads (a season and
	// its show, an album and its artist), by ID. Every episode of a show
	// asks about the same two rows, so each is read once per scan.
	//
	// Only a parent that HAS a poster is kept. That answer can't go stale in
	// a way that matters: the walk never clears a poster, and enrichment,
	// which is what gives a parent one, runs after the walk. A parent without
	// a poster is read again each time — those are the files that queue
	// enrichment anyway, so the extra read is noise next to the lookup that
	// follows. Values are *media.Item and are read-only.
	parents sync.Map
}

type scanStateKey struct{}

// withScanState returns ctx carrying st.
func withScanState(ctx context.Context, st *scanState) context.Context {
	return context.WithValue(ctx, scanStateKey{}, st)
}

// scanStateFrom returns the scan run's state, or nil outside a scan (a test
// driving processFile directly): every lookup then goes to the database, as
// it did before the state existed.
func scanStateFrom(ctx context.Context) *scanState {
	st, _ := ctx.Value(scanStateKey{}).(*scanState)
	return st
}

// loadFileSnapshots reads the library's file rows for st.files in one
// query. On failure the scan goes on without them (one lookup per file, as
// before) — a slower scan, never a wrong one.
func (s *Scanner) loadFileSnapshots(ctx context.Context, libraryID uuid.UUID) map[string]media.FileScanSnapshot {
	snaps, err := s.media.ListFileScanSnapshots(ctx, libraryID)
	if err != nil {
		s.logger.WarnContext(ctx, "file snapshot read failed; looking files up one by one",
			"library_id", libraryID, "err", err)
		return nil
	}
	files := make(map[string]media.FileScanSnapshot, len(snaps))
	for _, f := range snaps {
		files[f.FilePath] = f
	}
	return files
}

// existingFile returns the indexed row for path: from the scan's snapshot
// when it has one (fromSnapshot=true; the File carries only the snapshot's
// fields, see media.FileScanSnapshot), otherwise from the database.
func (s *Scanner) existingFile(ctx context.Context, path string) (file *media.File, fromSnapshot bool, err error) {
	if st := scanStateFrom(ctx); st != nil && st.files != nil {
		if snap, ok := st.files[path]; ok {
			return snap.AsFile(), true, nil
		}
	}
	f, err := s.media.GetFileByPath(ctx, path)
	return f, false, err
}

// enrichFile is the file processFile hands back with an item to enrich. A
// snapshot-built File carries only the snapshot's fields, so the full row is
// read for the enricher; that happens only for the items still needing
// enrichment, a small share of an unchanged library. If the read fails the
// snapshot File stands in: it carries the path and duration, which are what
// the enricher reads.
func (s *Scanner) enrichFile(ctx context.Context, path string, file *media.File, fromSnapshot bool) *media.File {
	if file == nil || !fromSnapshot {
		return file
	}
	if full, err := s.media.GetFileByPath(ctx, path); err == nil && full != nil && full.ID == file.ID {
		return full
	}
	return file
}

// snapshotCreditsScanned reports whether the scan's snapshot already says
// fileID's credits were read (known=false when there is no snapshot row for
// it, and the caller asks the database).
func snapshotCreditsScanned(ctx context.Context, path string, fileID uuid.UUID) (done, known bool) {
	st := scanStateFrom(ctx)
	if st == nil || st.files == nil {
		return false, false
	}
	snap, ok := st.files[path]
	if !ok || snap.ID != fileID {
		return false, false
	}
	return snap.CreditsScanned, true
}

// parentItem reads a parent item for parentNeedsEnrich through the scan's
// parent cache (see scanState.parents).
func (s *Scanner) parentItem(ctx context.Context, id uuid.UUID) (*media.Item, error) {
	st := scanStateFrom(ctx)
	if st != nil {
		if v, ok := st.parents.Load(id); ok {
			return v.(*media.Item), nil
		}
	}
	it, err := s.media.GetItem(ctx, id)
	if err != nil || it == nil {
		return it, err
	}
	if st != nil && it.PosterPath != nil {
		st.parents.Store(id, it)
	}
	return it, nil
}
