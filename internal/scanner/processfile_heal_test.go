package scanner

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/domain/media"
)

// TestProcessFile_OrphanEpisodeHeal covers the second half of the
// unspaced-dash fix: a rescan must heal the parentless "episode" rows the
// old parser chain left behind for names like
// "[Moozzi2] Dungeon Meshi-13 [BD 1920x1080 x265-10Bit 2Audio].mkv".
//
// The file on disk hasn't changed, so both of processFile's short-circuits
// (mtime+size fast skip, hash fast path) would normally return before the
// name is ever re-parsed — which is why 22 of 24 Delicious in Dungeon
// episodes stayed orphaned on QA through every rescan. The orphan-heal
// gate (orphanEpisodeNowParses) must make both short-circuits fall through
// to the slow path instead, which rebuilds show → season → episode and
// calls CreateOrUpdateFile pointed at the new episode (the real service
// re-points media_files.media_item_id there; CleanupEmptyItems drops the
// vacated orphan at scan end). Everything else — an orphan whose name
// still doesn't parse, an episode that already has a parent, a parentless
// item that isn't an episode, a non-show library, a GetItem failure — must
// fast-skip exactly as today.
//
// Fixture pattern follows processfile_fastskip_test.go: a temp file with
// its mtime pushed into the past, svc.items / svc.fileByPath seeded to
// satisfy the short-circuit preconditions. On the heal path the slow path
// runs ffprobe against the fake .mkv and logs "ffprobe failed, storing
// minimal metadata" before continuing — expected here.
//
// The gate's contract is "parses through the whole TV parser chain", so
// besides the Moozzi2 name (anime rule) a heal row each goes through the
// S##E## rule and the daily rule — an orphan of either shape heals the
// same way, and a gate that consulted only the anime rule would miss
// them. Two more shapes pin things the other rows cannot: an orphan that
// ALSO still needs enrichment must heal rather than be surfaced for yet
// another doomed enrichment pass (on QA the orphans are exactly the
// items whose enrichment keeps failing — title "Dungeon Meshi-13 [BD",
// year 1920 — so once the enrich cooldown lapses this is the production
// shape; it is why resolveUnchangedFile asks the gate BEFORE
// shouldEnrich), and a tag-only stem inside the show's folder must heal
// through extractShowTitle's folder fallback, which only works because
// the gate hands parseEpisodeIdentity the full path (it is also the one
// heal shape with a non-empty show FolderPath hint).
func TestProcessFile_OrphanEpisodeHeal(t *testing.T) {
	const moozzi = "[Moozzi2] Dungeon Meshi-13 [BD 1920x1080 x265-10Bit 2Audio].mkv"
	const healLog = "orphan episode now parses"

	tests := []struct {
		name        string
		filename    string
		libraryType string
		// wantShow / wantSeason / wantEpisode are the hierarchy a heal
		// must build; zero values mean the Moozzi2 name's ("Dungeon
		// Meshi", 1, 13).
		wantShow    string
		wantSeason  int
		wantEpisode int
		// itemType overrides the seeded item's Type ("episode" when empty).
		itemType string
		// parented gives the seeded episode a ParentID — a healthy row
		// the gate must leave alone.
		parented bool
		// unenriched leaves ThumbPath / Summary off the seeded item so
		// shouldEnrich is true and the short-circuit surfaces it.
		unenriched bool
		// noItem leaves the item out of the mock so GetItem fails.
		noItem bool
		// inShowFolder puts the file under "<root>/Dungeon Meshi/" instead
		// of directly in the root, so a tag-only stem resolves its show
		// from the folder and the show gets a FolderPath hint.
		inShowFolder bool
		// hashPath dates ScannedAt before the file mtime so the mtime
		// skip misses and the hash fast path is the short-circuit that
		// fires; the seeded FileHash is then the temp file's real hash.
		hashPath bool
		// realHash seeds the temp file's real hash while keeping the
		// mtime-skip preconditions, so a heal decided by the mtime skip
		// would ALSO satisfy the hash fast path — the double-gate shape a
		// genuinely unchanged file has in production.
		realHash bool
		wantHeal bool
		// wantEnrich expects the short-circuit to return the seeded item
		// and file for enrichment (today's shouldEnrich behaviour).
		wantEnrich bool
	}{
		{name: "mtime fast skip: orphan now parses → re-parented", filename: moozzi, libraryType: "show", wantHeal: true},
		{name: "mtime fast skip: orphan now parses, anime library → re-parented", filename: moozzi, libraryType: "anime", wantHeal: true},
		{name: "mtime fast skip: orphan now parses, cartoons library → re-parented", filename: moozzi, libraryType: "cartoons", wantHeal: true},
		{name: "mtime fast skip: S##E## orphan → re-parented", filename: "Show.Name.S01E03.mkv", libraryType: "show", wantShow: "Show Name", wantSeason: 1, wantEpisode: 3, wantHeal: true},
		{name: "mtime fast skip: date-based orphan → re-parented", filename: "The Daily Show - 2013-10-30 - Guest.mkv", libraryType: "show", wantShow: "The Daily Show", wantSeason: 2013, wantEpisode: 1030, wantHeal: true},
		{name: "mtime fast skip: orphan now parses AND still needs enrichment → re-parented, not surfaced", filename: moozzi, libraryType: "show", unenriched: true, wantHeal: true},
		{name: "mtime fast skip: tag-only stem in the show folder → re-parented via the folder fallback", filename: "[Moozzi2]-13 [BD 1920x1080 x265-10Bit 2Audio].mkv", libraryType: "show", inShowFolder: true, wantHeal: true},
		{name: "mtime fast skip: orphan still unparseable → fast-skips", filename: "[Coalgirls]_Show_01_(1920x1080_Blu-ray_FLAC).mkv", libraryType: "show"},
		{name: "mtime fast skip: episode already parented → fast-skips", filename: moozzi, libraryType: "show", parented: true},
		{name: "mtime fast skip: parentless non-episode item → fast-skips", filename: moozzi, libraryType: "show", itemType: "movie"},
		{name: "mtime fast skip: movie library → fast-skips", filename: moozzi, libraryType: "movie"},
		{name: "mtime fast skip: GetItem fails → fast-skips", filename: moozzi, libraryType: "show", noItem: true},
		{name: "mtime fast skip: parented episode still needs enrichment → surfaced", filename: moozzi, libraryType: "show", parented: true, unenriched: true, wantEnrich: true},
		{name: "mtime fast skip + matching hash: gate runs once → re-parented", filename: moozzi, libraryType: "show", realHash: true, wantHeal: true},
		{name: "hash fast path: orphan now parses → re-parented", filename: moozzi, libraryType: "show", hashPath: true, wantHeal: true},
		{name: "hash fast path: orphan now parses AND still needs enrichment → re-parented, not surfaced", filename: moozzi, libraryType: "show", hashPath: true, unenriched: true, wantHeal: true},
		{name: "hash fast path: episode already parented → fast-skips", filename: moozzi, libraryType: "show", hashPath: true, parented: true},
		{name: "hash fast path: GetItem fails → fast-skips", filename: moozzi, libraryType: "show", hashPath: true, noItem: true},
		{name: "hash fast path: parented episode still needs enrichment → surfaced", filename: moozzi, libraryType: "show", hashPath: true, parented: true, unenriched: true, wantEnrich: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			wantShow, wantSeason, wantEpisode := tt.wantShow, tt.wantSeason, tt.wantEpisode
			if wantShow == "" {
				wantShow, wantSeason, wantEpisode = "Dungeon Meshi", 1, 13
			}
			dir := t.TempDir()
			fileDir := dir
			if tt.inShowFolder {
				fileDir = filepath.Join(dir, "Dungeon Meshi")
				if err := os.Mkdir(fileDir, 0o755); err != nil {
					t.Fatalf("mkdir show folder: %v", err)
				}
			}
			path := filepath.Join(fileDir, tt.filename)
			if err := os.WriteFile(path, []byte("not a real mkv"), 0o644); err != nil {
				t.Fatalf("write temp file: %v", err)
			}
			pastMtime := time.Now().Add(-1 * time.Hour)
			if err := os.Chtimes(path, pastMtime, pastMtime); err != nil {
				t.Fatalf("chtimes: %v", err)
			}
			info, err := os.Stat(path)
			if err != nil {
				t.Fatalf("stat: %v", err)
			}

			svc := newMockMediaService()
			orphanID := uuid.New()
			if !tt.noItem {
				// The shape the "No parser matched" fallback leaves
				// behind: flat "episode", movie-parser title, year
				// scraped from 1920x1080. Thumb + summary are set so
				// shouldEnrich is false and, absent the heal, today's
				// short-circuit returns the full (nil, nil, false, nil)
				// — mirrors the PosterPath trick in the fast-skip tests
				// (and sets PosterPath too, which is what a non-episode
				// item's enrichment check looks at).
				thumb := "Shows/orphan/thumb.jpg"
				summary := "enriched"
				year := 1920
				item := &media.Item{
					ID:    orphanID,
					Type:  "episode",
					Title: "Dungeon Meshi-13 [BD",
					Year:  &year,
				}
				if tt.itemType != "" {
					item.Type = tt.itemType
				}
				if !tt.unenriched {
					item.ThumbPath = &thumb
					item.PosterPath = &thumb
					item.Summary = &summary
				}
				if tt.parented {
					seasonID := uuid.New()
					item.ParentID = &seasonID
				}
				svc.items[orphanID] = item
			}

			hash := "deadbeefcafefood"
			scannedAt := time.Now()
			if tt.hashPath || tt.realHash {
				h, err := HashFile(context.Background(), path, info)
				if err != nil {
					t.Fatalf("hash: %v", err)
				}
				hash = *h
			}
			if tt.hashPath {
				scannedAt = pastMtime.Add(-1 * time.Hour)
			}
			durationMS := int64(1_440_000)
			seededFile := &media.File{
				ID:          uuid.New(),
				MediaItemID: orphanID,
				FilePath:    path,
				FileSize:    info.Size(),
				FileHash:    &hash,
				DurationMS:  &durationMS,
				Status:      "active",
				ScannedAt:   scannedAt,
			}
			svc.fileByPath[path] = seededFile

			// Capture the log: the heal must announce itself (path + orphan
			// item id) exactly once, and a fast skip must stay silent.
			var logBuf bytes.Buffer
			s := New(svc, nil, nil, slog.New(slog.NewTextHandler(&logBuf, nil)))
			item, file, isNew, err := s.processFile(context.Background(), uuid.New(), tt.libraryType, path, []string{dir})
			if err != nil {
				t.Fatalf("processFile returned error: %v", err)
			}

			if !tt.wantHeal {
				if isNew {
					t.Errorf("short-circuit should report isNew=false")
				}
				if tt.wantEnrich {
					if item == nil || item.ID != orphanID {
						t.Errorf("short-circuit should surface the seeded item for enrichment; got %+v", item)
					}
					if file != seededFile {
						t.Errorf("short-circuit should return the seeded file alongside the item; got %+v", file)
					}
				} else if item != nil || file != nil {
					t.Errorf("fast skip should return (nil, nil); got item=%+v file=%+v", item, file)
				}
				if len(svc.fileCalls) != 0 {
					t.Errorf("CreateOrUpdateFile should not be called on fast skip; got %d calls", len(svc.fileCalls))
				}
				if len(svc.hierarchyCalls) != 0 {
					t.Errorf("no hierarchy should be built on fast skip; got %d calls", len(svc.hierarchyCalls))
				}
				if strings.Contains(logBuf.String(), healLog) {
					t.Errorf("fast skip must not log the heal; log:\n%s", logBuf.String())
				}
				return
			}

			if item == nil {
				t.Fatal("orphan heal must fall through to the slow path; got nil item (fast skip fired)")
			}
			if item.ID == orphanID {
				t.Fatal("slow path returned the orphan itself; want the re-parented episode")
			}
			if item.Type != "episode" {
				t.Errorf("type: got %q, want %q", item.Type, "episode")
			}
			if item.Index == nil || *item.Index != wantEpisode {
				t.Errorf("healed episode index: got %v, want %d", item.Index, wantEpisode)
			}
			if item.ParentID == nil {
				t.Errorf("healed episode has no ParentID; want it under a season")
			}
			if file == nil {
				t.Errorf("slow path should return the upserted file; got nil")
			}
			if len(svc.hierarchyCalls) != 3 {
				t.Fatalf("expected 3 hierarchy calls (show, season, episode), got %d", len(svc.hierarchyCalls))
			}
			if svc.hierarchyCalls[0].Type != "show" || svc.hierarchyCalls[0].Title != wantShow {
				t.Errorf("call[0]: got type %q title %q, want show %q", svc.hierarchyCalls[0].Type, svc.hierarchyCalls[0].Title, wantShow)
			}
			// A file directly in the library root gets no folder hint: a
			// root-as-prefix hint would let FindShowByFolderPrefix attach
			// the whole all-orphan set to whichever show chained first (see
			// showFolderHint). A file under its show folder gets that
			// folder, trailing separator included.
			wantFolder := ""
			if tt.inShowFolder {
				wantFolder = fileDir + string(filepath.Separator)
			}
			if fp := svc.hierarchyCalls[0].FolderPath; fp != wantFolder {
				t.Errorf("show FolderPath: got %q, want %q", fp, wantFolder)
			}
			if svc.hierarchyCalls[1].Type != "season" || svc.hierarchyCalls[1].Index == nil || *svc.hierarchyCalls[1].Index != wantSeason {
				t.Errorf("call[1]: got type %q index %v, want season %d", svc.hierarchyCalls[1].Type, svc.hierarchyCalls[1].Index, wantSeason)
			}
			if svc.hierarchyCalls[2].Type != "episode" || svc.hierarchyCalls[2].Index == nil || *svc.hierarchyCalls[2].Index != wantEpisode {
				t.Errorf("call[2]: got type %q index %v, want episode %d", svc.hierarchyCalls[2].Type, svc.hierarchyCalls[2].Index, wantEpisode)
			}
			if p := svc.hierarchyCalls[2].ParentID; p == nil || item.ParentID == nil || *item.ParentID != *p {
				t.Errorf("healed episode parent %v is not the season the hierarchy created %v", item.ParentID, p)
			}
			if len(svc.fileCalls) != 1 {
				t.Fatalf("expected 1 CreateOrUpdateFile call, got %d", len(svc.fileCalls))
			}
			if got := svc.fileCalls[0].MediaItemID; got == orphanID {
				t.Errorf("CreateOrUpdateFile still points the file at the orphan %s", orphanID)
			} else if got != item.ID {
				t.Errorf("CreateOrUpdateFile media_item_id: got %s, want the healed episode %s", got, item.ID)
			}
			// The gate runs once per file, even when the file would also
			// satisfy the hash fast path (a genuinely unchanged file always
			// does): one Info line, one GetItem.
			if n := strings.Count(logBuf.String(), healLog); n != 1 {
				t.Errorf("heal log lines: got %d, want 1; log:\n%s", n, logBuf.String())
			}
			if !strings.Contains(logBuf.String(), orphanID.String()) {
				t.Errorf("heal log does not name the orphan item %s; log:\n%s", orphanID, logBuf.String())
			}
			if svc.getItemCalls != 1 {
				t.Errorf("GetItem calls during the heal: got %d, want 1", svc.getItemCalls)
			}

			// Second pass: the healed row has a parent now, so the gate is
			// false again and the fast skip resumes — the heal costs one
			// slow-path pass per file. The mock's CreateOrUpdateFile stores
			// a bare File, so seed the fast-skip preconditions on the healed
			// row, and enrich the healed episode AND its season and show
			// (shouldEnrich walks the chain) so enrichment can't be the
			// reason the item comes back.
			healed := svc.fileByPath[path]
			healed.FileHash = &hash
			healed.DurationMS = &durationMS
			healed.ScannedAt = time.Now()
			thumb := "Shows/healed/thumb.jpg"
			summary := "enriched"
			svc.items[item.ID].ThumbPath = &thumb
			svc.items[item.ID].Summary = &summary
			season := svc.items[*item.ParentID]
			season.PosterPath = &thumb
			svc.items[*season.ParentID].PosterPath = &thumb
			svc.fileCalls, svc.hierarchyCalls, svc.getItemCalls = nil, nil, 0
			logBuf.Reset()

			item2, file2, isNew2, err := s.processFile(context.Background(), uuid.New(), tt.libraryType, path, []string{dir})
			if err != nil {
				t.Fatalf("second processFile returned error: %v", err)
			}
			if item2 != nil || file2 != nil || isNew2 {
				t.Errorf("second pass should fast-skip the healed row with (nil, nil, false); got item=%+v file=%+v isNew=%v", item2, file2, isNew2)
			}
			if len(svc.fileCalls) != 0 || len(svc.hierarchyCalls) != 0 {
				t.Errorf("second pass must not touch the DB; got %d file calls, %d hierarchy calls", len(svc.fileCalls), len(svc.hierarchyCalls))
			}
			if strings.Contains(logBuf.String(), healLog) {
				t.Errorf("second pass must not log the heal again; log:\n%s", logBuf.String())
			}
		})
	}
}

// TestIsShowLikeLibrary pins the three library types that hold the
// show → season → episode hierarchy. The processFile routing site and the
// orphan-heal gate both consult it; dropping "anime" or "cartoons" would
// silently exclude those libraries from the heal.
func TestIsShowLikeLibrary(t *testing.T) {
	tests := []struct {
		libraryType string
		want        bool
	}{
		{"show", true},
		{"anime", true},
		{"cartoons", true},
		{"movie", false},
		{"music", false},
		{"manga", false},
		{"photo", false},
		{"podcast", false},
		{"", false},
	}
	for _, tt := range tests {
		t.Run(fmt.Sprintf("%q", tt.libraryType), func(t *testing.T) {
			if got := isShowLikeLibrary(tt.libraryType); got != tt.want {
				t.Errorf("isShowLikeLibrary(%q) = %v, want %v", tt.libraryType, got, tt.want)
			}
		})
	}
}
