package v1

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onscreen/onscreen/internal/api/middleware"
	"github.com/onscreen/onscreen/internal/auth"
	"github.com/onscreen/onscreen/internal/db/gen"
)

type fakeMusicDB struct {
	items      map[uuid.UUID]gen.GetMediaItemRow
	profiles   []gen.ListArtistProfilesRow
	coListens  []gen.ListArtistCoListensRow
	appears    []gen.ListArtistAppearancesRow
	albumRows  []gen.ListMixTracksInAlbumRow
	artistRows map[uuid.UUID][]gen.ListMixTracksForArtistsRow
	genreRows  []gen.ListMixTracksByGenresRow

	profileArgs gen.ListArtistProfilesParams
	appearArgs  gen.ListArtistAppearancesParams
	genreArgs   gen.ListMixTracksByGenresParams
}

func (f *fakeMusicDB) GetMediaItem(_ context.Context, id uuid.UUID) (gen.GetMediaItemRow, error) {
	it, ok := f.items[id]
	if !ok {
		return gen.GetMediaItemRow{}, pgx.ErrNoRows
	}
	return it, nil
}

func (f *fakeMusicDB) ListArtistAppearances(_ context.Context, arg gen.ListArtistAppearancesParams) ([]gen.ListArtistAppearancesRow, error) {
	f.appearArgs = arg
	return f.appears, nil
}

func (f *fakeMusicDB) ListArtistCoListens(context.Context, gen.ListArtistCoListensParams) ([]gen.ListArtistCoListensRow, error) {
	return f.coListens, nil
}

func (f *fakeMusicDB) ListArtistProfiles(_ context.Context, arg gen.ListArtistProfilesParams) ([]gen.ListArtistProfilesRow, error) {
	f.profileArgs = arg
	return f.profiles, nil
}

func (f *fakeMusicDB) ListMixTracksForArtists(_ context.Context, arg gen.ListMixTracksForArtistsParams) ([]gen.ListMixTracksForArtistsRow, error) {
	var out []gen.ListMixTracksForArtistsRow
	for _, id := range arg.ArtistIds {
		out = append(out, f.artistRows[id]...)
	}
	return out, nil
}

func (f *fakeMusicDB) ListMixTracksInAlbum(context.Context, gen.ListMixTracksInAlbumParams) ([]gen.ListMixTracksInAlbumRow, error) {
	return f.albumRows, nil
}

func (f *fakeMusicDB) ListMixTracksByGenres(_ context.Context, arg gen.ListMixTracksByGenresParams) ([]gen.ListMixTracksByGenresRow, error) {
	f.genreArgs = arg
	return f.genreRows, nil
}

func pgID(id uuid.UUID) pgtype.UUID { return pgtype.UUID{Bytes: id, Valid: true} }

// musicFixture: one library with a seed artist (album, three tracks), a
// similar artist sharing its genre, and a genre-only track elsewhere.
type musicFixture struct {
	db                              *fakeMusicDB
	lib, artist, album, other       uuid.UUID
	t1, t2, t3, otherTrack, genreID uuid.UUID
}

func newMusicFixture() *musicFixture {
	f := &musicFixture{
		lib: uuid.New(), artist: uuid.New(), album: uuid.New(), other: uuid.New(),
		t1: uuid.New(), t2: uuid.New(), t3: uuid.New(), otherTrack: uuid.New(), genreID: uuid.New(),
	}
	f.db = &fakeMusicDB{items: map[uuid.UUID]gen.GetMediaItemRow{}, artistRows: map[uuid.UUID][]gen.ListMixTracksForArtistsRow{}}
	f.db.items[f.artist] = gen.GetMediaItemRow{ID: f.artist, LibraryID: f.lib, Type: "artist", Title: "Seed", Genres: []string{"Rock"}}
	f.db.items[f.album] = gen.GetMediaItemRow{ID: f.album, LibraryID: f.lib, Type: "album", Title: "Album", ParentID: pgID(f.artist)}
	for _, id := range []uuid.UUID{f.t1, f.t2, f.t3} {
		f.db.items[id] = gen.GetMediaItemRow{ID: id, LibraryID: f.lib, Type: "track", Title: "T", ParentID: pgID(f.album)}
	}
	f.db.items[f.other] = gen.GetMediaItemRow{ID: f.other, LibraryID: f.lib, Type: "artist", Title: "Other"}
	f.db.profiles = []gen.ListArtistProfilesRow{
		{ID: f.artist, Title: "Seed", Genres: []string{"rock"}, MedianYear: 1975, TrackCount: 3},
		{ID: f.other, Title: "Other", Genres: []string{"rock"}, MedianYear: 1977, TrackCount: 1},
	}
	album := func(id uuid.UUID, score *float64) gen.ListMixTracksInAlbumRow {
		return gen.ListMixTracksInAlbumRow{ID: id, Title: "T", MixArtistID: pgID(f.artist), AlbumID: f.album, ArtistID: pgID(f.artist), FileID: uuid.New(), UserScore: score}
	}
	low := 1.0
	f.db.albumRows = []gen.ListMixTracksInAlbumRow{album(f.t1, nil), album(f.t2, nil), album(f.t3, &low)}
	// The artist's tracks are its album's.
	for _, r := range f.db.albumRows {
		f.db.artistRows[f.artist] = append(f.db.artistRows[f.artist], gen.ListMixTracksForArtistsRow{
			ID: r.ID, Title: r.Title, MixArtistID: r.MixArtistID, AlbumID: r.AlbumID, ArtistID: r.ArtistID, FileID: r.FileID, UserScore: r.UserScore,
		})
	}
	f.db.artistRows[f.other] = []gen.ListMixTracksForArtistsRow{
		{ID: f.otherTrack, Title: "O", MixArtistID: pgID(f.other), AlbumID: uuid.New(), ArtistID: pgID(f.other), FileID: uuid.New()},
	}
	f.db.genreRows = []gen.ListMixTracksByGenresRow{
		{ID: f.genreID, Title: "G", MixArtistID: pgID(uuid.New()), AlbumID: uuid.New(), FileID: uuid.New()},
	}
	return f
}

func (f *musicFixture) handler(allowed bool) *MusicHandler {
	acc := &stubLibraryAccessChecker{allowed: map[uuid.UUID]struct{}{}}
	if allowed {
		acc.allowed[f.lib] = struct{}{}
	}
	h := NewMusicHandler(f.db, slog.New(slog.NewTextHandler(io.Discard, nil))).WithLibraryAccess(acc)
	h.rnd = func() *rand.Rand { return rand.New(rand.NewSource(7)) }
	return h
}

func musicRequest(claims *auth.Claims, target, id string, gate ItemLabelGate) *http.Request {
	r := httptest.NewRequest(http.MethodGet, target, nil)
	if id != "" {
		r = withChiParam(r, "id", id)
	}
	ctx := middleware.WithClaims(r.Context(), claims)
	if gate != nil {
		ctx = WithItemLabelGate(ctx, gate)
	}
	return r.WithContext(ctx)
}

func decodeList[T any](t *testing.T, rec *httptest.ResponseRecorder) []T {
	t.Helper()
	var env struct {
		Data []T `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode %d %s: %v", rec.Code, rec.Body.String(), err)
	}
	return env.Data
}

func TestMusicSimilar_Gates(t *testing.T) {
	f := newMusicFixture()
	user := &auth.Claims{UserID: uuid.New()}

	// No access to the library: not found, as if the artist didn't exist.
	rec := httptest.NewRecorder()
	f.handler(false).Similar(rec, musicRequest(user, "/", f.artist.String(), nil))
	if rec.Code != http.StatusNotFound {
		t.Errorf("no library access: %d", rec.Code)
	}

	// Rating ceiling: an unrated artist ranks most restrictive.
	rec = httptest.NewRecorder()
	f.handler(true).Similar(rec, musicRequest(&auth.Claims{UserID: uuid.New(), MaxContentRating: "PG"}, "/", f.artist.String(), nil))
	if rec.Code != http.StatusForbidden {
		t.Errorf("over the rating ceiling: %d", rec.Code)
	}

	// Label restriction on the seed.
	restricted := &auth.Claims{UserID: uuid.New(), BlockedLabels: []string{"explicit"}}
	rec = httptest.NewRecorder()
	f.handler(true).Similar(rec, musicRequest(restricted, "/", f.artist.String(), &fakeLabelGate{permitted: false}))
	if rec.Code != http.StatusForbidden {
		t.Errorf("label-blocked seed: %d", rec.Code)
	}

	// Permitted: the candidates query carries the caller's label filter.
	rec = httptest.NewRecorder()
	f.handler(true).Similar(rec, musicRequest(restricted, "/", f.artist.String(), &fakeLabelGate{permitted: true}))
	if rec.Code != http.StatusOK {
		t.Fatalf("similar: %d %s", rec.Code, rec.Body.String())
	}
	if len(f.db.profileArgs.BlockedLabels) != 1 || f.db.profileArgs.LibraryID != f.lib {
		t.Errorf("profile query args: %+v", f.db.profileArgs)
	}
	got := decodeList[SimilarArtistResponse](t, rec)
	if len(got) != 1 || got[0].ID != f.other.String() || got[0].Title != "Other" || len(got[0].SharedGenres) != 1 {
		t.Errorf("similar = %+v", got)
	}

	// Not an artist: not found.
	rec = httptest.NewRecorder()
	f.handler(true).Similar(rec, musicRequest(user, "/", f.album.String(), nil))
	if rec.Code != http.StatusNotFound {
		t.Errorf("album as artist: %d", rec.Code)
	}
}

func TestMusicAppearsOn(t *testing.T) {
	f := newMusicFixture()
	elsewhere := uuid.New()
	f.db.appears = []gen.ListArtistAppearancesRow{
		{ID: uuid.New(), LibraryID: f.lib, Title: "Guest spot", Role: "featured", AlbumID: uuid.New(), AlbumTitle: "Their Album"},
		{ID: uuid.New(), LibraryID: elsewhere, Title: "Hidden", Role: "featured", AlbumID: uuid.New()},
	}
	rec := httptest.NewRecorder()
	f.handler(true).AppearsOn(rec, musicRequest(&auth.Claims{UserID: uuid.New()}, "/", f.artist.String(), nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("appears on: %d %s", rec.Code, rec.Body.String())
	}
	got := decodeList[ArtistAppearanceResponse](t, rec)
	if len(got) != 1 || got[0].Title != "Guest spot" || got[0].Role != "featured" || got[0].AlbumTitle != "Their Album" {
		t.Errorf("appears on = %+v (a row from a library the caller can't see must be dropped)", got)
	}
	if f.db.appearArgs.ArtistID != f.artist || f.db.appearArgs.ResultLimit != 200 {
		t.Errorf("query args: %+v", f.db.appearArgs)
	}
}

func TestMusicMix(t *testing.T) {
	f := newMusicFixture()
	user := &auth.Claims{UserID: uuid.New()}

	rec := httptest.NewRecorder()
	f.handler(true).Mix(rec, musicRequest(user, "/music/mix?seed="+f.t2.String(), "", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("mix: %d %s", rec.Code, rec.Body.String())
	}
	got := decodeList[MixTrackResponse](t, rec)
	if len(got) == 0 || got[0].ID != f.t2.String() {
		t.Fatalf("the seed track must lead: %+v", got)
	}
	seen := map[string]bool{}
	for _, m := range got {
		if seen[m.ID] {
			t.Errorf("track %s twice", m.ID)
		}
		seen[m.ID] = true
		if m.FileID == "" {
			t.Errorf("track %s has no file to play", m.ID)
		}
	}
	if seen[f.t3.String()] {
		t.Error("a track the user rated one star must be left out")
	}
	for _, want := range []uuid.UUID{f.t1, f.otherTrack, f.genreID} {
		if !seen[want.String()] {
			t.Errorf("missing %s from the mix: %+v", want, got)
		}
	}
	// Genre matches leave out the seed and similar artists, whose tracks
	// are already in.
	if len(f.db.genreArgs.ExcludeArtistIds) != 2 || f.db.genreArgs.Genres[0] != "rock" {
		t.Errorf("genre query: %+v", f.db.genreArgs)
	}

	// A low-rated seed track still plays: the user asked for it.
	rec = httptest.NewRecorder()
	f.handler(true).Mix(rec, musicRequest(user, "/music/mix?seed="+f.t3.String(), "", nil))
	if got := decodeList[MixTrackResponse](t, rec); len(got) == 0 || got[0].ID != f.t3.String() {
		t.Errorf("low-rated seed: %+v", got)
	}

	// Artist and album seeds work too.
	for _, seed := range []uuid.UUID{f.artist, f.album} {
		rec = httptest.NewRecorder()
		f.handler(true).Mix(rec, musicRequest(user, "/music/mix?seed="+seed.String(), "", nil))
		if rec.Code != http.StatusOK || len(decodeList[MixTrackResponse](t, rec)) < 3 {
			t.Errorf("seed %s: %d %s", seed, rec.Code, rec.Body.String())
		}
	}

	// Bad seeds.
	for target, want := range map[string]int{
		"/music/mix":                          http.StatusBadRequest,
		"/music/mix?seed=nope":                http.StatusBadRequest,
		"/music/mix?seed=" + uuid.NewString(): http.StatusNotFound,
	} {
		rec = httptest.NewRecorder()
		f.handler(true).Mix(rec, musicRequest(user, target, "", nil))
		if rec.Code != want {
			t.Errorf("%s: %d, want %d", target, rec.Code, want)
		}
	}
	// Library the caller can't see.
	rec = httptest.NewRecorder()
	f.handler(false).Mix(rec, musicRequest(user, "/music/mix?seed="+f.t1.String(), "", nil))
	if rec.Code != http.StatusNotFound {
		t.Errorf("no access: %d", rec.Code)
	}
	// Unauthenticated.
	rec = httptest.NewRecorder()
	f.handler(true).Mix(rec, httptest.NewRequest(http.MethodGet, "/music/mix?seed="+f.t1.String(), nil))
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("no claims: %d", rec.Code)
	}
}
