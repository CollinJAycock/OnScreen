//go:build integration

// Music features against a real Postgres, through the production media
// adapter, media service and scanner (migration 00048):
//
//   - featured-artist credits: read from the feat. clause and from
//     multi-valued ARTISTS / MUSICBRAINZ_ARTISTID tags, stored once per
//     artist, kept on rescans, healed into a library scanned before them,
//     and the featured-only artist surviving the empty-container cleanup;
//   - loudness analysis: the work queue, tag values never overwritten,
//     analysed values written as 'analyzed', and the queue picking a file up
//     again when its bytes change — and, with ffmpeg on PATH, the whole path
//     from real audio to ReplayGain values.
//
// Run with: go test -tags=integration ./cmd/server/ -run Integration_Music
package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/onscreen/onscreen/internal/db/gen"
	"github.com/onscreen/onscreen/internal/domain/media"
	"github.com/onscreen/onscreen/internal/loudness"
	"github.com/onscreen/onscreen/internal/scanner"
	"github.com/onscreen/onscreen/internal/scheduler"
	"github.com/onscreen/onscreen/internal/testdb"
)

func musicLibrary(t *testing.T, q *gen.Queries, root string) gen.Library {
	t.Helper()
	lib, err := q.CreateLibrary(context.Background(), gen.CreateLibraryParams{
		Name: "Music-" + uuid.NewString()[:6], Type: "music", ScanPaths: []string{root},
		Agent: "tmdb", Language: "en", ScanInterval: time.Hour, MetadataRefreshInterval: 24 * time.Hour,
	})
	if err != nil {
		t.Fatalf("create library: %v", err)
	}
	return lib
}

// artistIDs maps live artist titles to ids; a duplicate title fails.
func artistIDs(t *testing.T, pool *pgxpool.Pool, libID uuid.UUID) map[string]uuid.UUID {
	t.Helper()
	rows, err := pool.Query(context.Background(), `
		SELECT title, id FROM media_items
		WHERE library_id = $1 AND type = 'artist' AND deleted_at IS NULL`, libID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	out := map[string]uuid.UUID{}
	for rows.Next() {
		var title string
		var id uuid.UUID
		if err := rows.Scan(&title, &id); err != nil {
			t.Fatal(err)
		}
		if _, dup := out[title]; dup {
			t.Fatalf("artist %q exists twice", title)
		}
		out[title] = id
	}
	return out
}

type credit struct {
	track, artist, role string
}

func trackCredits(t *testing.T, pool *pgxpool.Pool, libID uuid.UUID) map[credit]bool {
	t.Helper()
	rows, err := pool.Query(context.Background(), `
		SELECT t.title, a.title, ta.role
		FROM track_artists ta
		JOIN media_items t ON t.id = ta.track_id
		JOIN media_items a ON a.id = ta.artist_id
		WHERE t.library_id = $1`, libID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	out := map[credit]bool{}
	for rows.Next() {
		var c credit
		if err := rows.Scan(&c.track, &c.artist, &c.role); err != nil {
			t.Fatal(err)
		}
		if out[c] {
			t.Fatalf("credit %+v stored twice", c)
		}
		out[c] = true
	}
	return out
}

func TestIntegration_Music_FeaturedArtistCredits(t *testing.T) {
	pool := testdb.New(t)
	q := gen.New(pool)
	ctx := context.Background()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	adapter := &mediaAdapter{q: q}
	newScanner := func() *scanner.Scanner {
		return scanner.New(media.NewService(adapter, adapter, logger), nil, trackTestConc{}, logger).
			WithArtistCredits(musicCreditStore{q: q})
	}
	sc := newScanner()

	root := t.TempDir()
	file := func(rel string) string { return filepath.Join(root, filepath.FromSlash(rel)) }
	jay, riri, kanye := uuid.New(), uuid.New(), uuid.New()
	// Picard-style: a joined ARTIST plus the multi-valued ARTISTS and MBIDs.
	writeVorbisFLAC(t, file("Jay-Z/The Blueprint 3/01 - Run This Town.flac"),
		"TITLE=Run This Town", "ARTIST=Jay-Z feat. Rihanna & Kanye West", "ALBUMARTIST=Jay-Z",
		"ALBUM=The Blueprint 3", "TRACKNUMBER=1",
		"ARTISTS=Jay-Z", "ARTISTS=Rihanna", "ARTISTS=Kanye West",
		"MUSICBRAINZ_ARTISTID="+jay.String(), "MUSICBRAINZ_ARTISTID="+riri.String(), "MUSICBRAINZ_ARTISTID="+kanye.String(),
		"MUSICBRAINZ_ALBUMARTISTID="+jay.String())
	// Only a feat. clause, and no album artist tag: the album still hangs
	// under Rihanna, and Jay-Z is a credit.
	writeVorbisFLAC(t, file("Rihanna/Good Girl Gone Bad/01 - Umbrella.flac"),
		"TITLE=Umbrella", "ARTIST=Rihanna feat. Jay-Z", "ALBUM=Good Girl Gone Bad", "TRACKNUMBER=1")
	// A band whose name has a comma and an ampersand: one artist, no credits.
	writeVorbisFLAC(t, file("Earth, Wind & Fire/I Am/01 - Boogie Wonderland.flac"),
		"TITLE=Boogie Wonderland", "ARTIST=Earth, Wind & Fire", "ALBUM=I Am", "TRACKNUMBER=1")

	lib := musicLibrary(t, q, root)
	scan := func(label string, s *scanner.Scanner) {
		t.Helper()
		if _, err := s.ScanLibrary(ctx, lib.ID, "music", []string{root}); err != nil {
			t.Fatalf("%s: scan: %v", label, err)
		}
	}
	want := map[credit]bool{
		{"Run This Town", "Rihanna", "featured"}:    true,
		{"Run This Town", "Kanye West", "featured"}: true,
		{"Umbrella", "Jay-Z", "featured"}:           true,
	}
	check := func(label string) {
		t.Helper()
		artists := artistIDs(t, pool, lib.ID)
		for _, name := range []string{"Jay-Z", "Rihanna", "Kanye West", "Earth, Wind & Fire"} {
			if _, ok := artists[name]; !ok {
				t.Errorf("%s: artist %q missing (have %v)", label, name, artists)
			}
		}
		if len(artists) != 4 {
			t.Errorf("%s: %d artists, want 4: %v", label, len(artists), artists)
		}
		got := trackCredits(t, pool, lib.ID)
		if len(got) != len(want) {
			t.Errorf("%s: credits = %v, want %v", label, got, want)
		}
		for c := range want {
			if !got[c] {
				t.Errorf("%s: missing credit %+v (have %v)", label, c, got)
			}
		}
	}

	scan("import", sc)
	check("import")
	// The featured-only artist (no album of its own) carries its MBID, and
	// the featured artist found by MBID is the album artist row.
	var kanyeMBID pgtype.UUID
	if err := pool.QueryRow(ctx, `SELECT musicbrainz_artist_id FROM media_items
		WHERE library_id = $1 AND type = 'artist' AND title = 'Kanye West'`, lib.ID).Scan(&kanyeMBID); err != nil {
		t.Fatal(err)
	}
	if !kanyeMBID.Valid || uuid.UUID(kanyeMBID.Bytes) != kanye {
		t.Errorf("featured artist created without its MBID: %v", kanyeMBID)
	}

	// Kanye West has no albums: the scan's empty-container cleanup must
	// keep him, since he is credited on a live track.
	if err := media.NewService(adapter, adapter, logger).CleanupEmptyItems(ctx, lib.ID); err != nil {
		t.Fatal(err)
	}
	check("after cleanup")

	// Appears on: Kanye West's page lists the Jay-Z track.
	kanyeID := artistIDs(t, pool, lib.ID)["Kanye West"]
	apps, err := q.ListArtistAppearances(ctx, gen.ListArtistAppearancesParams{ArtistID: kanyeID, ResultLimit: 50})
	if err != nil {
		t.Fatal(err)
	}
	if len(apps) != 1 || apps[0].Title != "Run This Town" || apps[0].AlbumTitle != "The Blueprint 3" ||
		apps[0].AlbumArtistTitle == nil || *apps[0].AlbumArtistTitle != "Jay-Z" {
		t.Errorf("appears on = %+v", apps)
	}

	// Rescanning the unchanged files changes nothing and duplicates nothing.
	scan("rescan", sc)
	check("rescan")

	// A library scanned before credits existed: no credits, no read marks.
	// The next scan (a fresh process) heals it without the files changing.
	if _, err := pool.Exec(ctx, `DELETE FROM track_artists; DELETE FROM track_credit_scans`); err != nil {
		t.Fatal(err)
	}
	var scannedBefore time.Time
	if err := pool.QueryRow(ctx, `SELECT max(scanned_at) FROM media_files`).Scan(&scannedBefore); err != nil {
		t.Fatal(err)
	}
	scan("heal", newScanner())
	check("heal")
	var scannedAfter time.Time
	if err := pool.QueryRow(ctx, `SELECT max(scanned_at) FROM media_files`).Scan(&scannedAfter); err != nil {
		t.Fatal(err)
	}
	if !scannedAfter.Equal(scannedBefore) {
		t.Error("healing credits must not re-process the unchanged files")
	}
	var marks int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM track_credit_scans`).Scan(&marks); err != nil {
		t.Fatal(err)
	}
	if marks != 3 {
		t.Errorf("credit read marks = %d, want 3", marks)
	}
}

// musicTrack creates artist → album → track → file rows directly.
func musicTrack(t *testing.T, q *gen.Queries, libID, albumID uuid.UUID, title string, n int32, size int64, durMS int64) (uuid.UUID, uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	tr, err := q.CreateMediaItem(ctx, gen.CreateMediaItemParams{
		LibraryID: libID, Type: "track", Title: title, SortTitle: title, Genres: []string{}, Tags: []string{},
		ParentID: pgtype.UUID{Bytes: albumID, Valid: true}, Index: &n,
	})
	if err != nil {
		t.Fatalf("create track: %v", err)
	}
	hash := "h-" + title
	f, err := q.CreateMediaFile(ctx, gen.CreateMediaFileParams{
		MediaItemID: tr.ID, FilePath: "/music/" + title + ".flac", FileSize: size, FileHash: &hash, DurationMs: &durMS,
	})
	if err != nil {
		t.Fatalf("create file: %v", err)
	}
	return tr.ID, f.ID
}

func TestIntegration_Music_LoudnessQueueNeverOverwritesTags(t *testing.T) {
	pool := testdb.New(t)
	q := gen.New(pool)
	ctx := context.Background()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	lib := musicLibrary(t, q, t.TempDir())
	artist, err := q.CreateMediaItem(ctx, gen.CreateMediaItemParams{LibraryID: lib.ID, Type: "artist", Title: "A", SortTitle: "a", Genres: []string{}, Tags: []string{}})
	if err != nil {
		t.Fatal(err)
	}
	album, err := q.CreateMediaItem(ctx, gen.CreateMediaItemParams{LibraryID: lib.ID, Type: "album", Title: "Al", SortTitle: "al", Genres: []string{}, Tags: []string{},
		ParentID: pgtype.UUID{Bytes: artist.ID, Valid: true}})
	if err != nil {
		t.Fatal(err)
	}
	_, taggedFile := musicTrack(t, q, lib.ID, album.ID, "tagged", 1, 100, 200000)
	_, plainA := musicTrack(t, q, lib.ID, album.ID, "plainA", 2, 100, 200000)
	_, plainB := musicTrack(t, q, lib.ID, album.ID, "plainB", 3, 100, 200000)
	// A file whose ReplayGain came from its tags.
	if _, err := pool.Exec(ctx, `UPDATE media_files SET replaygain_track_gain = -3.21, replaygain_source = 'tags' WHERE id = $1`, taggedFile); err != nil {
		t.Fatal(err)
	}

	store := loudnessStore{q: q}
	queued, err := store.ListAlbumsNeedingLoudness(ctx, nil, 10)
	if err != nil || len(queued) != 1 || queued[0] != album.ID {
		t.Fatalf("queue = %v %v, want the album", queued, err)
	}
	lufs := map[string]float64{"/music/tagged.flac": -12, "/music/plainA.flac": -14, "/music/plainB.flac": -20}
	measure := func(_ context.Context, path string) (loudness.Measurement, error) {
		v := lufs[path]
		p := 0.95
		return loudness.Measurement{IntegratedLUFS: &v, TruePeak: &p}, nil
	}
	h := scheduler.NewLoudnessAnalysisHandler(store, measure, logger)
	if _, err := h.Run(ctx, nil); err != nil {
		t.Fatal(err)
	}

	type rg struct {
		track, album *float64
		source       *string
	}
	read := func(id uuid.UUID) rg {
		t.Helper()
		f, err := q.GetMediaFile(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		num := func(n pgtype.Numeric) *float64 {
			if !n.Valid {
				return nil
			}
			v, _ := n.Float64Value()
			return &v.Float64
		}
		return rg{num(f.ReplaygainTrackGain), num(f.ReplaygainAlbumGain), f.ReplaygainSource}
	}
	tg := read(taggedFile)
	if tg.track == nil || *tg.track != -3.21 || *tg.source != "tags" || tg.album != nil {
		t.Errorf("tag values touched: %+v", tg)
	}
	a := read(plainA)
	if a.source == nil || *a.source != "analyzed" || a.track == nil || *a.track != -4 {
		t.Errorf("plainA = %+v, want analyzed -4 dB", a)
	}
	// The album includes the tagged track's measured loudness.
	wantAlbum, _ := loudness.AlbumLoudness([]loudness.Track{
		{IntegratedLUFS: -12, DurationMS: 200000}, {IntegratedLUFS: -14, DurationMS: 200000}, {IntegratedLUFS: -20, DurationMS: 200000}})
	if a.album == nil || math.Abs(*a.album-loudness.TrackGain(wantAlbum)) > 0.005 {
		t.Errorf("album gain = %v, want %v", a.album, loudness.TrackGain(wantAlbum))
	}
	if b := read(plainB); b.track == nil || *b.track != 2 {
		t.Errorf("plainB = %+v", b)
	}
	if n, err := store.SetAnalyzedReplayGain(ctx, taggedFile, loudness.ReplayGain{}); err != nil || n {
		t.Errorf("SetAnalyzedReplayGain over tags: written=%v err=%v, want refused", n, err)
	}

	// Done: the queue is empty, and a second run measures nothing.
	if queued, _ := store.ListAlbumsNeedingLoudness(ctx, nil, 10); len(queued) != 0 {
		t.Errorf("queue after the run = %v", queued)
	}

	// The scanner re-reads plainB with new bytes: the media service drops the
	// stale measurement, and the album is queued again.
	svc := media.NewService(&mediaAdapter{q: q}, &mediaAdapter{q: q}, logger)
	itemB, _ := q.GetMediaFile(ctx, plainB)
	newHash := "h-plainB-v2"
	if _, _, err := svc.CreateOrUpdateFile(ctx, media.CreateFileParams{
		MediaItemID: itemB.MediaItemID, FilePath: itemB.FilePath, FileSize: 100, FileHash: &newHash, ReplayGainKnown: true,
	}); err != nil {
		t.Fatal(err)
	}
	if b := read(plainB); b.track != nil || b.source != nil {
		t.Errorf("changed file kept a stale measurement: %+v", b)
	}
	if queued, _ := store.ListAlbumsNeedingLoudness(ctx, nil, 10); len(queued) != 1 {
		t.Errorf("changed file not queued again: %v", queued)
	}

	// And a rescan whose tags now carry ReplayGain switches the file to tags.
	gain := -1.5
	if _, _, err := svc.CreateOrUpdateFile(ctx, media.CreateFileParams{
		MediaItemID: itemB.MediaItemID, FilePath: itemB.FilePath, FileSize: 100, FileHash: &newHash,
		ReplayGainKnown: true, ReplayGainTrackGain: &gain,
	}); err != nil {
		t.Fatal(err)
	}
	if b := read(plainB); b.track == nil || *b.track != -1.5 || *b.source != "tags" {
		t.Errorf("retagged file = %+v", b)
	}
}

// TestIntegration_Music_LoudnessEndToEnd scans real audio (generated with
// ffmpeg) and runs the task with the real measurement. Skipped without
// ffmpeg and ffprobe.
func TestIntegration_Music_LoudnessEndToEnd(t *testing.T) {
	for _, tool := range []string{"ffmpeg", "ffprobe"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s not on PATH", tool)
		}
	}
	pool := testdb.New(t)
	q := gen.New(pool)
	ctx := context.Background()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	adapter := &mediaAdapter{q: q}
	sc := scanner.New(media.NewService(adapter, adapter, logger), nil, trackTestConc{}, logger)

	root := t.TempDir()
	gen1 := func(rel string, volumeDB int, extra ...string) {
		t.Helper()
		out := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
			t.Fatal(err)
		}
		args := []string{"-hide_banner", "-loglevel", "error", "-y", "-f", "lavfi",
			"-i", "sine=frequency=440:duration=4", "-af", fmt.Sprintf("volume=%ddB", volumeDB),
			"-metadata", "ARTIST=Tone", "-metadata", "ALBUM=Tones"}
		args = append(args, extra...)
		args = append(args, "-c:a", "flac", out)
		if b, err := exec.Command("ffmpeg", args...).CombinedOutput(); err != nil {
			t.Fatalf("ffmpeg %s: %v %s", rel, err, b)
		}
		past := time.Now().Add(-time.Hour)
		_ = os.Chtimes(out, past, past)
	}
	gen1("Tone/Tones/01 - Loud.flac", 6, "-metadata", "TITLE=Loud", "-metadata", "TRACKNUMBER=1")
	gen1("Tone/Tones/02 - Quiet.flac", -6, "-metadata", "TITLE=Quiet", "-metadata", "TRACKNUMBER=2")
	gen1("Tone/Tones/03 - Tagged.flac", 0, "-metadata", "TITLE=Tagged", "-metadata", "TRACKNUMBER=3",
		"-metadata", "REPLAYGAIN_TRACK_GAIN=-7.00 dB", "-metadata", "REPLAYGAIN_TRACK_PEAK=0.500000")

	lib := musicLibrary(t, q, root)
	if _, err := sc.ScanLibrary(ctx, lib.ID, "music", []string{root}); err != nil {
		t.Fatal(err)
	}
	h := scheduler.NewLoudnessAnalysisHandler(loudnessStore{q: q}, loudness.Measure, logger)
	if summary, err := h.Run(ctx, nil); err != nil {
		t.Fatal(err)
	} else {
		t.Log(summary)
	}

	rows, err := pool.Query(ctx, `
		SELECT mi.title, mf.replaygain_track_gain::float8, mf.replaygain_album_gain::float8, mf.replaygain_source
		FROM media_files mf JOIN media_items mi ON mi.id = mf.media_item_id
		WHERE mi.library_id = $1`, lib.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	type v struct {
		track, album *float64
		source       *string
	}
	got := map[string]v{}
	for rows.Next() {
		var title string
		var r v
		if err := rows.Scan(&title, &r.track, &r.album, &r.source); err != nil {
			t.Fatal(err)
		}
		got[title] = r
	}
	tagged := got["Tagged"]
	if tagged.track == nil || *tagged.track != -7 || tagged.source == nil || *tagged.source != "tags" || tagged.album != nil {
		t.Errorf("tagged file = %+v, want its own -7 dB from tags and nothing else", tagged)
	}
	loud, quiet := got["Loud"], got["Quiet"]
	if loud.source == nil || *loud.source != "analyzed" || quiet.source == nil || *quiet.source != "analyzed" {
		t.Fatalf("untagged files not analysed: loud %+v quiet %+v", loud, quiet)
	}
	// 12 dB apart in level → 12 dB apart in gain.
	if d := *quiet.track - *loud.track; math.Abs(d-12) > 0.2 {
		t.Errorf("gain difference = %.2f dB, want ≈ 12", d)
	}
	if loud.album == nil || quiet.album == nil || *loud.album != *quiet.album {
		t.Errorf("album gain must be shared: %v %v", loud.album, quiet.album)
	}
	if !(*loud.album > *loud.track && *loud.album < *quiet.track) {
		t.Errorf("album gain %.2f should sit between the tracks' %.2f and %.2f", *loud.album, *loud.track, *quiet.track)
	}
}
