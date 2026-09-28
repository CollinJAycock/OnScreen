package v1

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onscreen/onscreen/internal/arr"
	"github.com/onscreen/onscreen/internal/audit"
	"github.com/onscreen/onscreen/internal/db/gen"
)

// regrabFakeArr is one scripted *arr instance.
type regrabFakeArr struct {
	srv       *httptest.Server
	routes    map[string]string // "METHOD /path" → JSON body
	failPaths map[string]int    // "METHOD /path" → status

	mu       sync.Mutex
	requests []string
	commands []map[string]any
}

func newRegrabFakeArr(t *testing.T, routes map[string]string) *regrabFakeArr {
	f := &regrabFakeArr{routes: routes, failPaths: map[string]int{}}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := r.Method + " " + r.URL.Path
		f.mu.Lock()
		f.requests = append(f.requests, key+"?"+r.URL.RawQuery)
		f.mu.Unlock()
		if code, ok := f.failPaths[key]; ok {
			w.WriteHeader(code)
			return
		}
		if key == "POST /api/v3/command" {
			raw, _ := io.ReadAll(r.Body)
			var body map[string]any
			_ = json.Unmarshal(raw, &body)
			f.mu.Lock()
			f.commands = append(f.commands, body)
			f.mu.Unlock()
			_, _ = io.WriteString(w, `{"id":42}`)
			return
		}
		if strings.HasPrefix(key, "POST /api/v3/history/failed/") {
			w.WriteHeader(http.StatusOK)
			return
		}
		body, ok := f.routes[key]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *regrabFakeArr) service(name, kind string) gen.ArrService {
	return gen.ArrService{ID: uuid.New(), Name: name, Kind: kind, BaseUrl: f.srv.URL, ApiKey: "k", Enabled: true}
}

type regrabFixture struct {
	*issuesFixture
	capture *auditCapture
}

func newRegrabFixture(t *testing.T) *regrabFixture {
	t.Helper()
	fx := newIssuesFixture()
	capture := &auditCapture{ch: make(chan gen.InsertAuditLogParams, 4)}
	fx.h.WithAudit(audit.New(capture, slog.Default()))
	// Every fake is an httptest server on loopback; the default client's
	// dial policy allows loopback, so arr.New works unmodified.
	fx.h.SetArrClientFactory(arr.New)
	tmdb := int32(949)
	fx.movie.TmdbID = &tmdb
	fx.db.items[fx.movie.ID] = fx.movie
	return &regrabFixture{issuesFixture: fx, capture: capture}
}

func (fx *regrabFixture) regrab(t *testing.T, itemID uuid.UUID, body string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	fx.h.Regrab(rec, issueReq(t, http.MethodPost, "/api/v1/admin/items/"+itemID.String()+"/regrab", body, fx.admin,
		map[string]string{"id": itemID.String()}))
	return rec
}

func TestRegrab_Movie_FirstManagingInstanceWins(t *testing.T) {
	fx := newRegrabFixture(t)
	notMine := newRegrabFakeArr(t, map[string]string{"GET /api/v3/movie": `[]`})
	mine := newRegrabFakeArr(t, map[string]string{
		"GET /api/v3/movie":         `[{"id":12,"title":"Heat","tmdbId":949,"hasFile":true}]`,
		"GET /api/v3/history/movie": `[{"id":88,"eventType":"grabbed","date":"2026-09-01T00:00:00Z","sourceTitle":"Heat.1995.BAD"}]`,
	})
	fx.db.arrServices["radarr"] = []gen.ArrService{notMine.service("4K", "radarr"), mine.service("HD", "radarr")}

	rec := fx.regrab(t, fx.movie.ID, `{"blocklist":true}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	var got RegrabResponse
	issueDecodeData(t, rec, &got)
	if got.ServiceName != "HD" || got.ServiceKind != "radarr" || got.Command != arr.CommandMoviesSearch ||
		got.ArrID != 12 || got.CommandID != 42 || !got.Blocklisted || got.BlocklistedRelease != "Heat.1995.BAD" ||
		got.HasFile == nil || !*got.HasFile {
		t.Errorf("response = %+v", got)
	}
	if len(notMine.commands) != 0 {
		t.Errorf("non-owning instance got a command: %v", notMine.commands)
	}
	if len(mine.commands) != 1 || mine.commands[0]["name"] != "MoviesSearch" {
		t.Errorf("commands = %v", mine.commands)
	}
	found := false
	for _, r := range mine.requests {
		if strings.HasPrefix(r, "POST /api/v3/history/failed/88") {
			found = true
		}
	}
	if !found {
		t.Errorf("grab 88 not marked failed: %v", mine.requests)
	}
	select {
	case a := <-fx.capture.ch:
		if a.Action != audit.ActionItemRegrab || a.Target == nil || *a.Target != "item:"+fx.movie.ID.String() ||
			!strings.Contains(string(a.Detail), `"blocklist":true`) {
			t.Errorf("audit = %+v %s", a, a.Detail)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no audit entry")
	}
	// The response must not echo the instance's base URL or key.
	if strings.Contains(rec.Body.String(), mine.srv.URL) {
		t.Errorf("base URL leaked: %s", rec.Body)
	}
}

func TestRegrab_ErrorMapping(t *testing.T) {
	t.Run("no instances", func(t *testing.T) {
		fx := newRegrabFixture(t)
		rec := fx.regrab(t, fx.movie.ID, "")
		if rec.Code != http.StatusConflict || issueErrorCode(t, rec) != "NOT_MANAGED" {
			t.Errorf("%d %s", rec.Code, rec.Body)
		}
	})
	t.Run("none manages it", func(t *testing.T) {
		fx := newRegrabFixture(t)
		a := newRegrabFakeArr(t, map[string]string{"GET /api/v3/movie": `[]`})
		fx.db.arrServices["radarr"] = []gen.ArrService{a.service("HD", "radarr")}
		rec := fx.regrab(t, fx.movie.ID, `{}`)
		if rec.Code != http.StatusConflict || issueErrorCode(t, rec) != "NOT_MANAGED" {
			t.Errorf("%d %s", rec.Code, rec.Body)
		}
	})
	t.Run("unreachable and none manages", func(t *testing.T) {
		fx := newRegrabFixture(t)
		down := newRegrabFakeArr(t, nil)
		down.failPaths["GET /api/v3/movie"] = http.StatusInternalServerError
		empty := newRegrabFakeArr(t, map[string]string{"GET /api/v3/movie": `[]`})
		fx.db.arrServices["radarr"] = []gen.ArrService{down.service("A", "radarr"), empty.service("B", "radarr")}
		rec := fx.regrab(t, fx.movie.ID, `{}`)
		if rec.Code != http.StatusBadGateway || issueErrorCode(t, rec) != "ARR_UNAVAILABLE" {
			t.Errorf("%d %s", rec.Code, rec.Body)
		}
		if strings.Contains(rec.Body.String(), down.srv.URL) {
			t.Errorf("base URL leaked: %s", rec.Body)
		}
	})
	t.Run("unreachable but another manages it", func(t *testing.T) {
		fx := newRegrabFixture(t)
		down := newRegrabFakeArr(t, nil)
		down.failPaths["GET /api/v3/movie"] = http.StatusInternalServerError
		ok := newRegrabFakeArr(t, map[string]string{"GET /api/v3/movie": `[{"id":1,"title":"Heat","tmdbId":949}]`})
		fx.db.arrServices["radarr"] = []gen.ArrService{down.service("A", "radarr"), ok.service("B", "radarr")}
		if rec := fx.regrab(t, fx.movie.ID, `{}`); rec.Code != http.StatusOK {
			t.Errorf("%d %s", rec.Code, rec.Body)
		}
	})
	t.Run("command fails on the owner", func(t *testing.T) {
		fx := newRegrabFixture(t)
		owner := newRegrabFakeArr(t, map[string]string{"GET /api/v3/movie": `[{"id":1,"title":"Heat","tmdbId":949}]`})
		owner.failPaths["POST /api/v3/command"] = http.StatusInternalServerError
		second := newRegrabFakeArr(t, map[string]string{"GET /api/v3/movie": `[{"id":2,"title":"Heat","tmdbId":949}]`})
		fx.db.arrServices["radarr"] = []gen.ArrService{owner.service("A", "radarr"), second.service("B", "radarr")}
		rec := fx.regrab(t, fx.movie.ID, `{}`)
		if rec.Code != http.StatusBadGateway {
			t.Errorf("%d %s", rec.Code, rec.Body)
		}
		if len(second.requests) != 0 {
			t.Errorf("a second instance was tried after the owner failed: %v", second.requests)
		}
	})
	t.Run("movie without tmdb id", func(t *testing.T) {
		fx := newRegrabFixture(t)
		m := gen.GetMediaItemRow{ID: uuid.New(), LibraryID: fx.lib, Type: "movie", Title: "Unmatched"}
		fx.db.items[m.ID] = m
		rec := fx.regrab(t, m.ID, "")
		if rec.Code != http.StatusConflict || issueErrorCode(t, rec) != "NOT_MANAGED" {
			t.Errorf("%d %s", rec.Code, rec.Body)
		}
	})
	t.Run("unsupported type", func(t *testing.T) {
		fx := newRegrabFixture(t)
		tr := gen.GetMediaItemRow{ID: uuid.New(), LibraryID: fx.lib, Type: "track", Title: "Song"}
		fx.db.items[tr.ID] = tr
		if rec := fx.regrab(t, tr.ID, ""); rec.Code != http.StatusUnprocessableEntity {
			t.Errorf("%d %s", rec.Code, rec.Body)
		}
	})
	t.Run("missing item", func(t *testing.T) {
		fx := newRegrabFixture(t)
		if rec := fx.regrab(t, uuid.New(), ""); rec.Code != http.StatusNotFound {
			t.Errorf("%d", rec.Code)
		}
	})
	t.Run("non-admin", func(t *testing.T) {
		fx := newRegrabFixture(t)
		rec := httptest.NewRecorder()
		fx.h.Regrab(rec, issueReq(t, http.MethodPost, "/", "", fx.user, map[string]string{"id": fx.movie.ID.String()}))
		if rec.Code != http.StatusForbidden {
			t.Errorf("%d", rec.Code)
		}
	})
}

func TestRegrab_EpisodeMapsToSonarr(t *testing.T) {
	fx := newRegrabFixture(t)
	tvdb, three, seven := int32(81189), int32(3), int32(7)
	show := gen.GetMediaItemRow{ID: uuid.New(), LibraryID: fx.lib, Type: "show", Title: "Show", TvdbID: &tvdb}
	season := gen.GetMediaItemRow{ID: uuid.New(), LibraryID: fx.lib, Type: "season", Title: "Season 3", Index: &three,
		ParentID: pgtype.UUID{Bytes: show.ID, Valid: true}}
	ep := gen.GetMediaItemRow{ID: uuid.New(), LibraryID: fx.lib, Type: "episode", Title: "Ep", Index: &seven,
		ParentID: pgtype.UUID{Bytes: season.ID, Valid: true}}
	for _, it := range []gen.GetMediaItemRow{show, season, ep} {
		fx.db.items[it.ID] = it
	}
	son := newRegrabFakeArr(t, map[string]string{
		"GET /api/v3/series":  `[{"id":5,"title":"Show","tvdbId":81189}]`,
		"GET /api/v3/episode": `[{"id":300,"seriesId":5,"seasonNumber":3,"episodeNumber":7,"hasFile":false}]`,
	})
	fx.db.arrServices["sonarr"] = []gen.ArrService{son.service("Sonarr", "sonarr")}
	// A Radarr instance must not be consulted for an episode.
	rad := newRegrabFakeArr(t, nil)
	fx.db.arrServices["radarr"] = []gen.ArrService{rad.service("Radarr", "radarr")}

	rec := fx.regrab(t, ep.ID, `{"blocklist":false}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	if len(son.commands) != 1 || son.commands[0]["name"] != "EpisodeSearch" {
		t.Fatalf("commands = %v", son.commands)
	}
	if ids, _ := son.commands[0]["episodeIds"].([]any); len(ids) != 1 || ids[0] != float64(300) {
		t.Errorf("episodeIds = %v", son.commands[0]["episodeIds"])
	}
	if len(rad.requests) != 0 {
		t.Errorf("radarr consulted for an episode: %v", rad.requests)
	}

	// Season → SeasonSearch with that season number; show → SeriesSearch.
	son.commands = nil
	if rec := fx.regrab(t, season.ID, ""); rec.Code != http.StatusOK {
		t.Fatalf("season: %d %s", rec.Code, rec.Body)
	}
	if c := son.commands[0]; c["name"] != "SeasonSearch" || c["seasonNumber"] != float64(3) || c["seriesId"] != float64(5) {
		t.Errorf("season command = %v", c)
	}
	son.commands = nil
	if rec := fx.regrab(t, show.ID, ""); rec.Code != http.StatusOK {
		t.Fatalf("show: %d %s", rec.Code, rec.Body)
	}
	if c := son.commands[0]; c["name"] != "SeriesSearch" || c["seriesId"] != float64(5) {
		t.Errorf("show command = %v", c)
	}

	// A flat episode hanging directly off the show has no season number.
	flat := gen.GetMediaItemRow{ID: uuid.New(), LibraryID: fx.lib, Type: "episode", Title: "Flat", Index: &seven,
		ParentID: pgtype.UUID{Bytes: show.ID, Valid: true}}
	fx.db.items[flat.ID] = flat
	if rec := fx.regrab(t, flat.ID, ""); rec.Code != http.StatusConflict {
		t.Errorf("flat episode: %d %s", rec.Code, rec.Body)
	}
}
