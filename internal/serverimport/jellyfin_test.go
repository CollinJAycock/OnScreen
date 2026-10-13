package serverimport

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
)

const jellyfinTestKey = "api-key"

// fakeJellyfin serves the Jellyfin (or, with prefix "/emby", Emby) API for
// one user who watched a movie, is part-way through an episode, favorited a
// show and has a playlist.
type fakeJellyfin struct {
	*httptest.Server
	filters []string
}

func newFakeJellyfin(t *testing.T, kind Kind) *fakeJellyfin {
	t.Helper()
	f := &fakeJellyfin{}
	prefix := ""
	if kind == KindEmby {
		prefix = "/emby"
	}
	movie := map[string]any{"Id": "m1", "Name": "Alien", "Type": "Movie", "Path": `D:\Movies\Alien (1979)\Alien.mkv`,
		"ProviderIds": map[string]string{"Tmdb": "348", "Imdb": "tt0078748"}, "RunTimeTicks": int64(70200000000),
		"UserData": map[string]any{"Played": true, "PlayCount": 3, "LastPlayedDate": "2024-05-01T20:00:00.0000000Z", "Rating": 8.5}}
	episode := map[string]any{"Id": "e1", "Name": "Pilot", "Type": "Episode", "SeriesId": "s1",
		"ParentIndexNumber": 1, "IndexNumber": 1, "RunTimeTicks": int64(30000000000),
		"UserData": map[string]any{"PlaybackPositionTicks": int64(6000000000), "LastPlayedDate": "2024-06-01T10:00:00Z"}}
	manual := map[string]any{"Id": "m2", "Name": "Marked", "Type": "Movie",
		"UserData": map[string]any{"Played": true}} // marked played by hand: no date
	series := map[string]any{"Id": "s1", "Name": "Severance", "Type": "Series",
		"ProviderIds": map[string]string{"Tvdb": "371980"}, "UserData": map[string]any{"IsFavorite": true}}
	unfav := map[string]any{"Id": "s2", "Name": "Other", "Type": "Series", "UserData": map[string]any{}}
	list := func(items ...map[string]any) map[string]any {
		return map[string]any{"Items": items, "TotalRecordCount": len(items)}
	}
	mux := http.NewServeMux()
	mux.HandleFunc(prefix+"/System/Info", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]any{"ServerName": "Attic", "Version": "10.11.2", "Id": "srv-1"})
	})
	mux.HandleFunc(prefix+"/Users", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, []map[string]any{{"Id": "u1", "Name": "Alice"}, {"Id": "u2", "Name": "Bob"}})
	})
	mux.HandleFunc(prefix+"/Users/u1/Items", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Get("EnableImages") != "false" || q.Get("Limit") == "" {
			t.Errorf("item list without paging/EnableImages=false: %s", r.URL)
		}
		switch {
		case q.Get("IncludeItemTypes") == "Series":
			writeJSON(w, list(series, unfav))
		case q.Get("IncludeItemTypes") == "Playlist":
			writeJSON(w, list(map[string]any{"Id": "p1", "Name": "Road trip", "Type": "Playlist"}))
		default:
			f.filters = append(f.filters, q.Get("Filters"))
			switch q.Get("Filters") {
			case "IsPlayed":
				writeJSON(w, list(movie, manual))
			case "IsResumable":
				writeJSON(w, list(episode))
			case "IsFavorite":
				writeJSON(w, list())
			default:
				t.Errorf("unexpected filter %q", q.Get("Filters"))
				writeJSON(w, list())
			}
		}
	})
	mux.HandleFunc(prefix+"/Playlists/p1/Items", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("UserId") != "u1" {
			t.Errorf("playlist items without UserId: %s", r.URL)
		}
		writeJSON(w, list(episode, movie))
	})
	f.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var ok bool
		if kind == KindEmby {
			ok = r.Header.Get("X-Emby-Token") == jellyfinTestKey
		} else {
			auth := r.Header.Get("Authorization")
			ok = strings.HasPrefix(auth, "MediaBrowser ") && strings.Contains(auth, `Token="`+jellyfinTestKey+`"`) &&
				r.Header.Get("X-Emby-Token") == ""
		}
		if !ok {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		mux.ServeHTTP(w, r)
	}))
	t.Cleanup(f.Close)
	return f
}

func TestJellyfinFetch(t *testing.T) {
	for _, kind := range []Kind{KindJellyfin, KindEmby} {
		t.Run(string(kind), func(t *testing.T) {
			f := newFakeJellyfin(t, kind)
			src := DefaultSources(kind, f.URL, jellyfinTestKey)
			info, accts, err := src.Probe(context.Background())
			if err != nil {
				t.Fatalf("Probe: %v", err)
			}
			if info.Name != "Attic" || info.ID != "srv-1" || len(accts) != 2 || !accts[0].FullState {
				t.Errorf("probe = %+v %+v", info, accts)
			}

			all := Options{Watched: true, Ratings: true, Favorites: true, Playlists: true, History: true}
			snap, err := src.Fetch(context.Background(), []string{"u1"}, all, noProgress)
			if err != nil {
				t.Fatalf("Fetch: %v", err)
			}
			if got := strings.Join(f.filters, ","); got != "IsPlayed,IsResumable,IsFavorite" {
				t.Errorf("filters read = %s", got)
			}
			m := snap.Items["m1"]
			if m == nil || m.IDs.TMDB != 348 || m.IDs.IMDB != "tt0078748" || m.DurationMS != 7020000 || m.Path == "" {
				t.Errorf("movie = %+v", m)
			}
			e := snap.Items["e1"]
			if e == nil || e.ShowIDs.TVDB != 371980 || e.Season != 1 || e.Episode != 1 || !e.HasSE {
				t.Errorf("episode = %+v", e)
			}
			states := map[string]State{}
			for _, st := range snap.States["u1"] {
				states[st.Key] = st
			}
			if st := states["m1"]; !st.Played || st.Rating == nil || *st.Rating != 8.5 ||
				!st.LastViewedAt.Equal(time.Date(2024, 5, 1, 20, 0, 0, 0, time.UTC)) {
				t.Errorf("movie state = %+v", st)
			}
			if st := states["e1"]; st.Played || st.PositionMS != 600000 {
				t.Errorf("episode state = %+v", st)
			}
			if st := states["s1"]; !st.Favorite {
				t.Errorf("show state = %+v, want favorite", st)
			}
			if _, ok := states["s2"]; ok {
				t.Error("a show with no user data has a state")
			}
			if st := states["m2"]; !st.Played || !st.LastViewedAt.IsZero() {
				t.Errorf("hand-marked movie = %+v", st)
			}
			// History: one play per played item with a date.
			if len(snap.Plays) != 1 || snap.Plays[0].Key != "m1" {
				t.Errorf("plays = %+v", snap.Plays)
			}
			pls := snap.Playlists["u1"]
			if len(pls) != 1 || pls[0].Name != "Road trip" || strings.Join(pls[0].Keys, ",") != "e1,m1" {
				t.Errorf("playlists = %+v", pls)
			}
			if _, ok := snap.States["u2"]; ok {
				t.Error("read an unmapped user")
			}
		})
	}
}

func TestJellyfinFetch_ReadsOnlyWhatsNeeded(t *testing.T) {
	f := newFakeJellyfin(t, KindJellyfin)
	_, err := NewJellyfin(f.URL, jellyfinTestKey).Fetch(context.Background(), []string{"u1"}, Options{Favorites: true}, noProgress)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if got := strings.Join(f.filters, ","); got != "IsFavorite" {
		t.Errorf("filters read for a favorites-only import = %s", got)
	}
}

func TestJellyfinProbe_WrongKey(t *testing.T) {
	f := newFakeJellyfin(t, KindJellyfin)
	if _, _, err := NewJellyfin(f.URL, "nope").Probe(context.Background()); !errors.Is(err, ErrUnauthorized) {
		t.Errorf("wrong key: %v, want ErrUnauthorized", err)
	}
	// Emby's header against a Jellyfin that turned legacy auth off.
	if _, _, err := NewEmby(f.URL, jellyfinTestKey).Probe(context.Background()); err == nil {
		t.Error("Emby-style request accepted by the Jellyfin fake")
	}
}

func TestJellyfinPages(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		start, _ := strconv.Atoi(r.URL.Query().Get("StartIndex"))
		n := min(jellyfinPageSize, 1200-start)
		items := make([]map[string]any, n)
		for i := range items {
			items[i] = map[string]any{"Id": strconv.Itoa(start + i)}
		}
		writeJSON(w, map[string]any{"Items": items, "TotalRecordCount": 1200})
	}))
	defer srv.Close()
	src := NewJellyfin(srv.URL, "k").(*jellyfinSource)
	total := 0
	err := src.pages(context.Background(), "/Users/u/Items", map[string][]string{}, func(items []jellyfinItem) { total += len(items) })
	if err != nil || total != 1200 || calls != 3 {
		t.Errorf("paged %d items in %d calls (err %v), want 1200 in 3", total, calls, err)
	}
}
