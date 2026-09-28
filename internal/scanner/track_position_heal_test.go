package scanner

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/domain/media"
	"github.com/onscreen/onscreen/internal/mediastore"
)

// countingStore reads the local filesystem and counts opens per path, so a
// test can tell a heal that only parsed the path from one that read the file.
type countingStore struct {
	mediastore.Local
	opens map[string]int
}

func (c *countingStore) Open(ctx context.Context, key string) (io.ReadSeekCloser, error) {
	c.opens[key]++
	return c.Local.Open(ctx, key)
}

func ip(v int) *int { return &v }

func fmtPos(disc, index *int) string {
	f := func(p *int) string {
		if p == nil {
			return "nil"
		}
		return string(rune('0' + *p))
	}
	return "disc " + f(disc) + " track " + f(index)
}

// TestProcessFile_UnchangedTrackGetsPosition covers tracks already in a
// library without a track number — imported before the scanner could read
// Vorbis "02/12" numbers or fall back to the filename's. Their files are
// unchanged, so processFile's mtime fast skip returns before any tag is read
// again; without the heal in resolveUnchangedFile they never get a number.
//
// The heal must keep the file skipped (no hash, no ffprobe, no upsert) and
// read the file only when the path can't place the track: no number in the
// name, a multi-disc album and no disc in the path, or a position another
// track already holds. A file the tags can't place either is read once per
// process, not once per scan.
func TestProcessFile_UnchangedTrackGetsPosition(t *testing.T) {
	// taken refuses a fill of the given position, as the unique
	// (parent, disc, track) index does when another track holds it.
	taken := func(disc, track int) func(index, d *int) error {
		return func(index, d *int) error {
			if index != nil && *index == track && (d == nil && disc == 1 || d != nil && *d == disc) {
				return errors.New("duplicate key value violates unique constraint")
			}
			return nil
		}
	}
	tests := []struct {
		name     string
		rel      string   // path under the library root
		comments []string // Vorbis comments besides TITLE/ARTIST/ALBUM
		discs    *int     // album's disc_total
		index    *int     // track's stored index (nil = the heal case)
		itemType string   // "track" when empty
		libType  string   // "music" when empty
		fillErr  func(index, disc *int) error
		want     *int // index after the scan
		wantDisc *int
		opens    int // file reads the heal may make, over two scans
		fills    int // FillTrackPosition calls, over two scans
	}{
		{name: "number in filename", rel: "A/Album/07 - Song.flac",
			want: ip(7), opens: 0, fills: 1},
		{name: "disc-track filename on a multi-disc album", rel: "A/Box/2-05 Song.flac", discs: ip(2),
			want: ip(5), wantDisc: ip(2), opens: 0, fills: 1},
		{name: "disc folder on a multi-disc album", rel: "A/Box/CD2/05 - Song.flac", discs: ip(2),
			want: ip(5), wantDisc: ip(2), opens: 0, fills: 1},
		{name: "multi-disc album, no disc in path: read tags", rel: "A/Box/05 - Song.flac", discs: ip(2),
			comments: []string{"TRACKNUMBER=05/12", "DISCNUMBER=2/2"}, opens: 1, fills: 1,
			want: ip(5), wantDisc: ip(2)},
		{name: "no number in filename: read Vorbis N/M tags", rel: "A/Album/Song.flac",
			comments: []string{"TRACKNUMBER=02/12", "DISCNUMBER=1/1"}, opens: 1, fills: 1,
			want: ip(2), wantDisc: ip(1)},
		{name: "path's position taken: tags settle it", rel: "A/Album/03 - Song.flac",
			comments: []string{"TRACKNUMBER=03/12", "DISCNUMBER=2/2"}, fillErr: taken(1, 3),
			want: ip(3), wantDisc: ip(2), opens: 1, fills: 2},
		{name: "tags' position taken too: give up once", rel: "A/Album/03 - Song.flac",
			comments: []string{"TRACKNUMBER=3"}, fillErr: taken(1, 3),
			want: nil, opens: 1, fills: 2},
		{name: "no number anywhere: read once", rel: "A/Album/Song.flac",
			want: nil, opens: 1, fills: 0},
		{name: "already numbered: untouched", rel: "A/Album/07 - Song.flac", index: ip(4),
			want: ip(4), opens: 0, fills: 0},
		{name: "not a track", rel: "A/Album/07 - Song.flac", itemType: "music_video",
			want: nil, opens: 0, fills: 0},
		{name: "not a music library", rel: "A/Album/07 - Song.flac", libType: "audiobook",
			want: nil, opens: 0, fills: 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			path := filepath.Join(root, filepath.FromSlash(tc.rel))
			writeTaggedFLAC(t, path, append([]string{"TITLE=Song", "ARTIST=A", "ALBUM=Album"}, tc.comments...)...)
			past := time.Now().Add(-time.Hour)
			if err := os.Chtimes(path, past, past); err != nil {
				t.Fatal(err)
			}
			info, err := os.Stat(path)
			if err != nil {
				t.Fatal(err)
			}

			svc := newMockMediaService()
			svc.fillErr = tc.fillErr
			poster := "poster.jpg" // enriched: nothing else surfaces the item
			album := &media.Item{ID: uuid.New(), Type: "album", Title: "Album", PosterPath: &poster, DiscTotal: tc.discs}
			itemType := tc.itemType
			if itemType == "" {
				itemType = "track"
			}
			track := &media.Item{ID: uuid.New(), Type: itemType, Title: "Song", ParentID: &album.ID,
				Index: tc.index, PosterPath: &poster}
			svc.items[album.ID] = album
			svc.items[track.ID] = track
			hash := "unchanged"
			fileID := uuid.New()
			svc.fileByPath[path] = &media.File{ID: fileID, MediaItemID: track.ID, FilePath: path,
				FileSize: info.Size(), FileHash: &hash, Status: "active", ScannedAt: time.Now()}

			store := &countingStore{opens: map[string]int{}}
			s := newTestScanner(svc).WithMediaStore(store)
			libType := tc.libType
			if libType == "" {
				libType = "music"
			}

			// Twice: the second scan must neither re-read nor re-write.
			for scan := 1; scan <= 2; scan++ {
				if _, _, isNew, err := s.processFile(context.Background(), uuid.New(), libType, path, []string{root}); err != nil || isNew {
					t.Fatalf("scan %d: processFile = isNew %v, err %v", scan, isNew, err)
				}
			}
			if len(svc.fileCalls) != 0 || len(svc.hierarchyCalls) != 0 {
				t.Errorf("file left the fast skip: %d upserts, %d hierarchy calls", len(svc.fileCalls), len(svc.hierarchyCalls))
			}
			if got := store.opens[path]; got != tc.opens {
				t.Errorf("file opened %d times over two scans, want %d", got, tc.opens)
			}
			if !samePos(track.Index, tc.want) || !samePos(track.DiscNumber, tc.wantDisc) {
				t.Errorf("track position = %s, want %s", fmtPos(track.DiscNumber, track.Index), fmtPos(tc.wantDisc, tc.want))
			}
			if len(svc.fillCalls) != tc.fills {
				t.Errorf("FillTrackPosition called %d times over two scans, want %d", len(svc.fillCalls), tc.fills)
			}
		})
	}
}

func samePos(a, b *int) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

// A file that changed takes the slow path, where FindOrCreateHierarchyItem
// returns the existing track as stored — without the number the file's tags
// now yield. processMusicHierarchy must fill it in (and the disc), and must
// not rewrite a number the track already has.
func TestProcessMusicHierarchy_FillsExistingTrackPosition(t *testing.T) {
	for _, tc := range []struct {
		name            string
		index, disc     *int
		wantIdx, wantDk int
		wantFill        bool
	}{
		{name: "unnumbered track", wantIdx: 3, wantDk: 2, wantFill: true},
		{name: "numbered, no disc yet", index: ip(3), wantIdx: 3, wantDk: 2, wantFill: true},
		{name: "fully numbered", index: ip(4), disc: ip(1), wantIdx: 4, wantDk: 1, wantFill: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			path := filepath.Join(root, "Artist", "Album", "Third.flac")
			writeTaggedFLAC(t, path, "TITLE=Third", "ARTIST=Artist", "ALBUM=Album",
				"TRACKNUMBER=03/10", "DISCNUMBER=2/2")

			svc := newMockMediaService()
			existing := &media.Item{ID: uuid.New(), Type: "track", Title: "Third", Index: tc.index, DiscNumber: tc.disc}
			svc.items[existing.ID] = existing
			svc.hierarchyFind = func(p media.CreateItemParams) *media.Item {
				if p.Type == "track" {
					return existing
				}
				return nil
			}

			got, _, err := newTestScanner(svc).processMusicHierarchy(context.Background(), uuid.New(), path, []string{root})
			if err != nil {
				t.Fatalf("processMusicHierarchy: %v", err)
			}
			if got.ID != existing.ID {
				t.Fatal("did not return the existing track")
			}
			if got.Index == nil || *got.Index != tc.wantIdx || got.DiscNumber == nil || *got.DiscNumber != tc.wantDk {
				t.Errorf("position = %s, want disc %d track %d", fmtPos(got.DiscNumber, got.Index), tc.wantDk, tc.wantIdx)
			}
			if (len(svc.fillCalls) == 1) != tc.wantFill {
				t.Errorf("FillTrackPosition calls = %d, want fill %v", len(svc.fillCalls), tc.wantFill)
			}
			// The lookup itself carries the position, so a new track is
			// created with it and a disc-2 file can't match disc 1's track.
			last := svc.hierarchyCalls[len(svc.hierarchyCalls)-1]
			if !samePos(last.Index, ip(3)) || !samePos(last.DiscNumber, ip(2)) {
				t.Errorf("track lookup position = %s, want disc 2 track 3", fmtPos(last.DiscNumber, last.Index))
			}
		})
	}
}
