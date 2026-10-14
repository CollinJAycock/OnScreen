package userexport

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onscreen/onscreen/internal/db/gen"
)

type fakeStore struct {
	events  []gen.ExportWatchEventsRow // newest first
	refs    map[uuid.UUID]gen.ExportItemRefsRow
	refArgs []gen.ExportItemRefsParams
	noUser  bool
	pages   int
}

func tsAt(t time.Time) pgtype.Timestamptz { return pgtype.Timestamptz{Time: t, Valid: true} }

func (f *fakeStore) ExportUserProfile(_ context.Context, id uuid.UUID) (gen.ExportUserProfileRow, error) {
	if f.noUser {
		return gen.ExportUserProfileRow{}, pgx.ErrNoRows
	}
	email := "alice@example.com"
	return gen.ExportUserProfileRow{ID: id, Username: "alice", Email: &email, HasPassword: true, TotpEnabled: true,
		HubLayout: []byte(`{"rows":[]}`)}, nil
}
func (f *fakeStore) ExportUserSessions(context.Context, uuid.UUID) ([]gen.ExportUserSessionsRow, error) {
	name := "Firefox on Linux"
	return []gen.ExportUserSessionsRow{{ID: uuid.New(), ClientName: &name}}, nil
}
func (f *fakeStore) ListAPIKeysForUser(context.Context, uuid.UUID) ([]gen.ListAPIKeysForUserRow, error) {
	return []gen.ListAPIKeysForUserRow{{ID: uuid.New(), Name: "Home Assistant", KeyPrefix: "osk_3fQ9xA2b", Scope: "read_only"}}, nil
}
func (f *fakeStore) ExportUserScrobble(context.Context, uuid.UUID) (gen.ExportUserScrobbleRow, error) {
	return gen.ExportUserScrobbleRow{}, pgx.ErrNoRows
}
func (f *fakeStore) ExportWatchEvents(_ context.Context, arg gen.ExportWatchEventsParams) ([]gen.ExportWatchEventsRow, error) {
	f.pages++
	start := 0
	if arg.BeforeAt.Valid {
		for i, e := range f.events {
			if e.ID == arg.BeforeID {
				start = i + 1
			}
		}
	}
	end := min(start+int(arg.Lim), len(f.events))
	return f.events[start:end], nil
}
func (f *fakeStore) ExportWatchProgress(context.Context, uuid.UUID) ([]gen.ExportWatchProgressRow, error) {
	return nil, nil
}
func (f *fakeStore) ExportWatchStatus(context.Context, uuid.UUID) ([]gen.ExportWatchStatusRow, error) {
	return nil, nil
}
func (f *fakeStore) ExportRatings(context.Context, uuid.UUID) ([]gen.ExportRatingsRow, error) {
	var out []gen.ExportRatingsRow
	for id := range f.refs {
		out = append(out, gen.ExportRatingsRow{MediaItemID: id, Score: 8})
	}
	return out, nil
}
func (f *fakeStore) ExportFavorites(context.Context, uuid.UUID) ([]gen.ExportFavoritesRow, error) {
	return nil, nil
}
func (f *fakeStore) ExportReadingProgress(context.Context, uuid.UUID) ([]gen.ExportReadingProgressRow, error) {
	return nil, nil
}
func (f *fakeStore) ExportAudiobookRates(context.Context, uuid.UUID) ([]gen.ExportAudiobookRatesRow, error) {
	return nil, nil
}
func (f *fakeStore) ExportAudiobookBookmarks(context.Context, uuid.UUID) ([]gen.ExportAudiobookBookmarksRow, error) {
	return nil, nil
}
func (f *fakeStore) ExportUserCollections(context.Context, uuid.UUID) ([]gen.ExportUserCollectionsRow, error) {
	return []gen.ExportUserCollectionsRow{{ID: uuid.New(), Name: "Road trip", Type: "playlist"}}, nil
}
func (f *fakeStore) ExportUserCollectionItems(context.Context, uuid.UUID) ([]gen.ExportUserCollectionItemsRow, error) {
	return nil, nil
}
func (f *fakeStore) ExportMediaRequests(context.Context, uuid.UUID) ([]gen.ExportMediaRequestsRow, error) {
	return nil, nil
}
func (f *fakeStore) ExportMediaIssues(context.Context, uuid.UUID) ([]gen.ExportMediaIssuesRow, error) {
	return nil, nil
}
func (f *fakeStore) ExportNotifications(context.Context, gen.ExportNotificationsParams) ([]gen.ExportNotificationsRow, error) {
	return nil, nil
}
func (f *fakeStore) ExportItemRefs(_ context.Context, arg gen.ExportItemRefsParams) ([]gen.ExportItemRefsRow, error) {
	f.refArgs = append(f.refArgs, arg)
	var out []gen.ExportItemRefsRow
	for _, id := range arg.Ids {
		if r, ok := f.refs[id]; ok {
			out = append(out, r)
		}
	}
	return out, nil
}

func build(t *testing.T, f *fakeStore, vis Visibility) map[string][]byte {
	t.Helper()
	e := New(f, vis)
	snap, err := e.Collect(context.Background(), uuid.New())
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := e.Write(context.Background(), &buf, snap); err != nil {
		t.Fatal(err)
	}
	zr, err := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatalf("not a valid zip: %v", err)
	}
	files := map[string][]byte{}
	for _, zf := range zr.File {
		rc, _ := zf.Open()
		b, _ := io.ReadAll(rc)
		_ = rc.Close()
		files[zf.Name] = b
	}
	return files
}

func TestExport_ArchiveContents(t *testing.T) {
	movie, hidden := uuid.New(), uuid.New()
	tmdb := int32(603)
	f := &fakeStore{refs: map[uuid.UUID]gen.ExportItemRefsRow{
		movie:  {ID: movie, Type: "movie", Title: "The Matrix", TmdbID: &tmdb, Visible: true},
		hidden: {ID: hidden, Type: "movie", Title: "Secret Title", Visible: false},
	}}
	now := time.Now()
	f.events = []gen.ExportWatchEventsRow{
		{ID: uuid.New(), MediaID: movie, EventType: "stop", OccurredAt: tsAt(now)},
		{ID: uuid.New(), MediaID: hidden, EventType: "play", OccurredAt: tsAt(now.Add(-time.Hour))},
	}
	libs := []uuid.UUID{uuid.New()}
	files := build(t, f, Visibility{LibraryIDs: libs})

	for _, name := range []string{"README.txt", "manifest.json", "profile.json", "sessions.json", "api_keys.json",
		"scrobble.json", "watch_history.json", "watch_progress.json", "watch_status.json", "ratings.json",
		"favorites.json", "reading_progress.json", "audiobooks.json", "playlists.json", "collections.json",
		"requests.json", "issues.json", "notifications.json"} {
		if _, ok := files[name]; !ok {
			t.Errorf("archive is missing %s", name)
		}
		if strings.HasSuffix(name, ".json") && !json.Valid(files[name]) {
			t.Errorf("%s is not valid JSON:\n%s", name, files[name])
		}
	}

	var history []struct {
		Item ItemRef `json:"item"`
	}
	if err := json.Unmarshal(files["watch_history.json"], &history); err != nil || len(history) != 2 {
		t.Fatalf("history: %v %s", err, files["watch_history.json"])
	}
	if history[0].Item.Title != "The Matrix" || history[0].Item.ExternalIDs["tmdb"] != "603" {
		t.Errorf("visible item ref = %+v", history[0].Item)
	}
	if !history[1].Item.Unavailable || history[1].Item.Title != "" {
		t.Errorf("hidden item ref = %+v, want id only", history[1].Item)
	}
	all := ""
	for _, b := range files {
		all += string(b)
	}
	if strings.Contains(all, "Secret Title") {
		t.Error("a hidden item's title reached the archive")
	}
	// Ref lookups ran under the viewer's visibility.
	for _, a := range f.refArgs {
		if len(a.LibraryIds) != 1 || a.LibraryIds[0] != libs[0] {
			t.Errorf("ref lookup visibility = %+v", a)
		}
	}
	// API keys: names and prefixes, nothing that looks like a hash.
	if !strings.Contains(string(files["api_keys.json"]), "osk_3fQ9xA2b") ||
		strings.Contains(string(files["api_keys.json"]), "hash") {
		t.Errorf("api_keys.json = %s", files["api_keys.json"])
	}
	var profile map[string]any
	_ = json.Unmarshal(files["profile.json"], &profile)
	for _, k := range []string{"password_hash", "pin", "totp_secret"} {
		if _, ok := profile[k]; ok {
			t.Errorf("profile.json has %q", k)
		}
	}
	if profile["two_factor_enabled"] != true || profile["has_password"] != true {
		t.Errorf("profile flags = %v", profile)
	}
	if !strings.Contains(string(files["playlists.json"]), "Road trip") {
		t.Errorf("playlists.json = %s", files["playlists.json"])
	}
}

func TestExport_HistoryIsPagedAndCapped(t *testing.T) {
	item := uuid.New()
	f := &fakeStore{refs: map[uuid.UUID]gen.ExportItemRefsRow{item: {ID: item, Type: "track", Title: "T", Visible: true}}}
	base := time.Now()
	for i := 0; i < MaxWatchEvents+10; i++ {
		f.events = append(f.events, gen.ExportWatchEventsRow{ID: uuid.New(), MediaID: item, EventType: "play",
			OccurredAt: tsAt(base.Add(-time.Duration(i) * time.Second))})
	}
	files := build(t, f, Visibility{})
	var history []json.RawMessage
	if err := json.Unmarshal(files["watch_history.json"], &history); err != nil {
		t.Fatal(err)
	}
	if len(history) != MaxWatchEvents {
		t.Fatalf("history rows = %d, want the cap %d", len(history), MaxWatchEvents)
	}
	var manifest struct {
		Limits struct {
			Truncated bool `json:"watch_history_truncated"`
		} `json:"limits"`
	}
	_ = json.Unmarshal(files["manifest.json"], &manifest)
	if !manifest.Limits.Truncated {
		t.Error("manifest doesn't say the history was truncated")
	}
	if f.pages < MaxWatchEvents/eventPage {
		t.Errorf("history read in %d pages; expected keyset paging", f.pages)
	}
}

func TestExport_UnknownUser(t *testing.T) {
	_, err := New(&fakeStore{noUser: true}, Visibility{}).Collect(context.Background(), uuid.New())
	if err == nil || !isNoRows(err) {
		t.Fatalf("err = %v, want a wrapped ErrNoRows", err)
	}
}
