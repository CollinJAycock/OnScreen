//go:build integration

package scrobble

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/onscreen/onscreen/internal/auth"
	"github.com/onscreen/onscreen/internal/db/gen"
	"github.com/onscreen/onscreen/internal/testdb"
)

type storeFixture struct {
	store                  *Store
	db                     *pgxpool.Pool
	userA, userB           uuid.UUID
	movie, episode, orphan uuid.UUID
	track                  uuid.UUID
}

func seedStore(ctx context.Context, t *testing.T) *storeFixture {
	t.Helper()
	pool := testdb.New(t)
	q := gen.New(pool)
	enc, err := auth.NewEncryptor(auth.DeriveKey32(strings.Repeat("s", 32)))
	if err != nil {
		t.Fatalf("encryptor: %v", err)
	}
	f := &storeFixture{store: NewStore(pool, enc), db: pool}

	user := func() uuid.UUID {
		u, err := q.CreateUser(ctx, gen.CreateUserParams{Username: "u-" + uuid.NewString()[:8]})
		if err != nil {
			t.Fatalf("create user: %v", err)
		}
		return u.ID
	}
	f.userA, f.userB = user(), user()

	lib := func(typ string) uuid.UUID {
		l, err := q.CreateLibrary(ctx, gen.CreateLibraryParams{
			Name: typ + "-" + uuid.NewString()[:8], Type: typ, ScanPaths: []string{"/tmp/" + typ},
			Agent: "tmdb", Language: "en", ScanInterval: time.Hour, MetadataRefreshInterval: 24 * time.Hour,
		})
		if err != nil {
			t.Fatalf("create library: %v", err)
		}
		return l.ID
	}
	i32 := func(v int32) *int32 { return &v }
	i64 := func(v int64) *int64 { return &v }
	str := func(v string) *string { return &v }
	item := func(p gen.CreateMediaItemParams, parent uuid.UUID) uuid.UUID {
		p.SortTitle, p.Genres, p.Tags = strings.ToLower(p.Title), []string{}, []string{}
		if parent != uuid.Nil {
			p.ParentID = pgtype.UUID{Bytes: parent, Valid: true}
		}
		row, err := q.CreateMediaItem(ctx, p)
		if err != nil {
			t.Fatalf("create %s %q: %v", p.Type, p.Title, err)
		}
		return row.ID
	}

	movies, shows, music := lib("movie"), lib("show"), lib("music")
	f.movie = item(gen.CreateMediaItemParams{LibraryID: movies, Type: "movie", Title: "Heat",
		Year: i32(1995), TmdbID: i32(949), ImdbID: str("tt0113277"), DurationMs: i64(10_200_000)}, uuid.Nil)
	show := item(gen.CreateMediaItemParams{LibraryID: shows, Type: "show", Title: "The Wire",
		Year: i32(2002), TmdbID: i32(1438), TvdbID: i32(79126), ImdbID: str("tt0306414")}, uuid.Nil)
	season := item(gen.CreateMediaItemParams{LibraryID: shows, Type: "season", Title: "Season 4", Index: i32(4)}, show)
	f.episode = item(gen.CreateMediaItemParams{LibraryID: shows, Type: "episode", Title: "Final Grades",
		Index: i32(13), TmdbID: i32(999), DurationMs: i64(4_800_000)}, season)
	f.orphan = item(gen.CreateMediaItemParams{LibraryID: shows, Type: "episode", Title: "Loose", Index: i32(2)}, uuid.Nil)
	artist := item(gen.CreateMediaItemParams{LibraryID: music, Type: "artist", Title: "Led Zeppelin"}, uuid.Nil)
	album := item(gen.CreateMediaItemParams{LibraryID: music, Type: "album", Title: "IV"}, artist)
	f.track = item(gen.CreateMediaItemParams{LibraryID: music, Type: "track", Title: "Black Dog",
		Index: i32(1), DurationMs: i64(295_000)}, album)
	return f
}

func TestStore_CredentialsRoundTripAndStayBound(t *testing.T) {
	ctx := context.Background()
	f := seedStore(ctx, t)
	s := f.store

	if st, err := s.Get(ctx, f.userA); err != nil || st != (Settings{}) {
		t.Fatalf("no row must read as nothing linked: %+v, %v", st, err)
	}

	expires := time.Unix(1_800_000_000, 0).UTC()
	if err := s.SetListenBrainz(ctx, f.userA, "lb-token", true); err != nil {
		t.Fatal(err)
	}
	if err := s.SetLastFM(ctx, f.userA, "lfm-session", "rj"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetTrakt(ctx, f.userA, TraktLink{AccessToken: "acc", RefreshToken: "ref", ExpiresAt: expires, Username: "tr"}); err != nil {
		t.Fatal(err)
	}
	st, err := s.Get(ctx, f.userA)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	want := Settings{
		ListenBrainzToken: "lb-token", ListenBrainzEnabled: true,
		LastFMSessionKey: "lfm-session", LastFMUsername: "rj",
		Trakt: TraktLink{AccessToken: "acc", RefreshToken: "ref", Username: "tr"},
	}
	if !st.Trakt.ExpiresAt.Equal(expires) {
		t.Errorf("expires: got %s, want %s", st.Trakt.ExpiresAt, expires)
	}
	st.Trakt.ExpiresAt = time.Time{}
	if st != want {
		t.Errorf("round trip:\n got  %+v\n want %+v", st, want)
	}

	// Nothing is stored in the clear.
	var lfm, acc string
	if err := f.db.QueryRow(ctx, `SELECT lastfm_session_key, trakt_access_token FROM user_scrobble WHERE user_id = $1`,
		f.userA).Scan(&lfm, &acc); err != nil {
		t.Fatal(err)
	}
	if lfm == "lfm-session" || acc == "acc" {
		t.Errorf("credentials must be encrypted at rest: %q %q", lfm, acc)
	}

	// A ciphertext copied into another user's row doesn't open there.
	if err := s.SetListenBrainz(ctx, f.userB, "", false); err != nil { // create B's row
		t.Fatal(err)
	}
	if _, err := f.db.Exec(ctx, `UPDATE user_scrobble SET lastfm_session_key = $1 WHERE user_id = $2`, lfm, f.userB); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Get(ctx, f.userB); err == nil {
		t.Error("a credential moved to another user must not decrypt")
	}

	// Unlinking Trakt leaves the other services alone.
	if err := s.SetTrakt(ctx, f.userA, TraktLink{}); err != nil {
		t.Fatal(err)
	}
	if st, _ := s.Get(ctx, f.userA); st.Trakt != (TraktLink{}) || st.LastFMSessionKey != "lfm-session" || st.ListenBrainzToken != "lb-token" {
		t.Errorf("after trakt unlink: %+v", st)
	}
	if err := s.SetLastFM(ctx, f.userA, "", "ignored"); err != nil {
		t.Fatal(err)
	}
	if st, _ := s.Get(ctx, f.userA); st.LastFMSessionKey != "" || st.LastFMUsername != "" {
		t.Errorf("after last.fm unlink: %+v", st)
	}
}

func TestStore_MediaLookups(t *testing.T) {
	ctx := context.Background()
	f := seedStore(ctx, t)
	s := f.store

	tr, ok, err := s.Track(ctx, f.track)
	if err != nil || !ok {
		t.Fatalf("track: %v %v", ok, err)
	}
	if tr.TrackName != "Black Dog" || tr.ArtistName != "Led Zeppelin" || tr.ReleaseName != "IV" ||
		tr.TrackNumber != 1 || tr.DurationMS != 295_000 {
		t.Errorf("track: %+v", tr)
	}

	mv, ok, err := s.Video(ctx, f.movie)
	if err != nil || !ok {
		t.Fatalf("movie: %v %v", ok, err)
	}
	if mv != (Video{Title: "Heat", Year: 1995, TMDBID: 949, IMDBID: "tt0113277", DurationMS: 10_200_000}) {
		t.Errorf("movie: %+v", mv)
	}

	ep, ok, err := s.Video(ctx, f.episode)
	if err != nil || !ok {
		t.Fatalf("episode: %v %v", ok, err)
	}
	// The show's ids and title, not the episode's own tmdb id.
	want := Video{Episode: true, Title: "The Wire", Year: 2002, TMDBID: 1438, TVDBID: 79126, IMDBID: "tt0306414",
		Season: 4, Number: 13, DurationMS: 4_800_000}
	if ep != want {
		t.Errorf("episode:\n got  %+v\n want %+v", ep, want)
	}

	for name, id := range map[string]uuid.UUID{"parentless episode": f.orphan, "track": f.track, "missing": uuid.New()} {
		if _, ok, err := s.Video(ctx, id); ok || err != nil {
			t.Errorf("%s must not resolve as a video: ok=%v err=%v", name, ok, err)
		}
	}
	if _, ok, err := s.Track(ctx, f.movie); ok || err != nil {
		t.Errorf("a movie must not resolve as a track: ok=%v err=%v", ok, err)
	}

	// The batch form resolves the same rows and leaves out what can't be named.
	all, err := s.Videos(ctx, []uuid.UUID{f.movie, f.episode, f.orphan, f.track, uuid.New()})
	if err != nil {
		t.Fatalf("videos: %v", err)
	}
	if len(all) != 2 || !((all[0] == mv && all[1] == want) || (all[0] == want && all[1] == mv)) {
		t.Errorf("videos: %+v", all)
	}
}
