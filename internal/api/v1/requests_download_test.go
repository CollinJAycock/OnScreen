package v1

import (
	"context"
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
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onscreen/onscreen/internal/arr"
	"github.com/onscreen/onscreen/internal/db/gen"
	"github.com/onscreen/onscreen/internal/requests"
)

// Live download status at the HTTP layer: the request DTO's download and
// username fields, the arr webhook's targeted sync trigger, and the
// arr-service health endpoint.

// ── requests.DB additions for the quota tests' fake ─────────────────────────

func (f *rqFakeDB) ListMediaRequestsForDownloadSync(context.Context, gen.ListMediaRequestsForDownloadSyncParams) ([]gen.MediaRequest, error) {
	return nil, nil
}
func (f *rqFakeDB) SetMediaRequestArrItem(context.Context, gen.SetMediaRequestArrItemParams) error {
	return nil
}
func (f *rqFakeDB) UpdateMediaRequestDownload(context.Context, gen.UpdateMediaRequestDownloadParams) (int64, error) {
	return 0, nil
}
func (f *rqFakeDB) FailMediaRequestDownload(context.Context, gen.FailMediaRequestDownloadParams) (int64, error) {
	return 0, nil
}
func (f *rqFakeDB) RecoverMediaRequestDownload(context.Context, gen.RecoverMediaRequestDownloadParams) (int64, error) {
	return 0, nil
}
func (f *rqFakeDB) ListUsernamesByIDs(_ context.Context, ids []uuid.UUID) ([]gen.ListUsernamesByIDsRow, error) {
	var out []gen.ListUsernamesByIDsRow
	for _, id := range ids {
		if p, ok := f.perms[id]; ok {
			out = append(out, gen.ListUsernamesByIDsRow{ID: id, Username: p.Username})
		}
	}
	return out, nil
}

// listFakeDB is rqFakeDB with a fixed set of rows for the listing endpoints.
type listFakeDB struct {
	*rqFakeDB
	rows []gen.MediaRequest
}

func (f *listFakeDB) ListAllMediaRequests(context.Context, gen.ListAllMediaRequestsParams) ([]gen.MediaRequest, error) {
	return f.rows, nil
}
func (f *listFakeDB) CountAllMediaRequests(context.Context, *string) (int64, error) {
	return int64(len(f.rows)), nil
}
func (f *listFakeDB) ListMediaRequestsForUser(_ context.Context, p gen.ListMediaRequestsForUserParams) ([]gen.MediaRequest, error) {
	var out []gen.MediaRequest
	for _, r := range f.rows {
		if r.UserID == p.UserID {
			out = append(out, r)
		}
	}
	return out, nil
}
func (f *listFakeDB) GetMediaRequest(_ context.Context, id uuid.UUID) (gen.MediaRequest, error) {
	for _, r := range f.rows {
		if r.ID == id {
			return r, nil
		}
	}
	return f.rqFakeDB.GetMediaRequest(context.Background(), id)
}

// ── DTO shape ───────────────────────────────────────────────────────────────

func dlRow(user uuid.UUID, state string, msg string) gen.MediaRequest {
	updated := time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)
	r := gen.MediaRequest{
		ID: uuid.New(), UserID: user, Type: "movie", TmdbID: 949, Title: "Heat", Status: requests.StatusDownloading,
		CreatedAt: pgtype.Timestamptz{Time: updated.Add(-time.Hour), Valid: true},
		UpdatedAt: pgtype.Timestamptz{Time: updated.Add(-time.Hour), Valid: true},
	}
	if state != "" {
		p := float32(0.6)
		size := int64(4_000_000_000)
		r.DownloadState = &state
		r.DownloadProgress = &p
		r.DownloadEta = pgtype.Timestamptz{Time: updated.Add(30 * time.Minute), Valid: true}
		r.DownloadSizeBytes = &size
		r.DownloadMessage = &msg
		r.DownloadUpdatedAt = pgtype.Timestamptz{Time: updated, Valid: true}
	}
	return r
}

func TestRequestDTO_DownloadShape(t *testing.T) {
	dto := toRequestDTO(dlRow(uuid.New(), "downloading", ""))
	b, _ := json.Marshal(dto)
	var got struct {
		Download map[string]any `json:"download"`
	}
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	want := map[string]any{
		"state":      "downloading",
		"progress":   0.6, // float32 rounded, not 0.6000000238
		"eta":        "2026-09-28T12:30:00Z",
		"size_bytes": float64(4_000_000_000),
		"updated_at": "2026-09-28T12:00:00Z",
	}
	for k, v := range want {
		if got.Download[k] != v {
			t.Errorf("download.%s = %v (%T), want %v", k, got.Download[k], got.Download[k], v)
		}
	}
	if _, ok := got.Download["message"]; ok {
		t.Error("empty message should be omitted")
	}

	// No state → no download key at all (the contract's "absent until the
	// first sync"), and no username key when unknown.
	b, _ = json.Marshal(toRequestDTO(dlRow(uuid.New(), "", "")))
	if strings.Contains(string(b), `"download"`) || strings.Contains(string(b), `"username"`) {
		t.Errorf("unsynced request rendered %s", b)
	}
}

func TestRequestDTO_UpdatedAtFallsBack(t *testing.T) {
	r := dlRow(uuid.New(), "searching", "")
	r.DownloadUpdatedAt = pgtype.Timestamptz{}
	d := toRequestDownloadDTO(r)
	if d == nil || d.UpdatedAt != "2026-09-28T11:00:00Z" {
		t.Errorf("updated_at = %+v, want the request's updated_at", d)
	}
}

func TestRequestDTO_RedactsArrTextForUsers(t *testing.T) {
	raw := "Import failed, path does not exist: /mnt/nas/downloads/Heat.1995"
	for _, state := range []string{requests.DownloadStalled, requests.DownloadFailed} {
		row := dlRow(uuid.New(), state, raw)
		if got := toRequestDTOFor(row, true).Download.Message; got != raw {
			t.Errorf("%s admin message = %q, want the raw text", state, got)
		}
		if got := toRequestDTOFor(row, false).Download.Message; strings.Contains(got, "/mnt") || got == "" {
			t.Errorf("%s user message = %q, want a plain phrase", state, got)
		}
	}
	// Sync-written messages on other states pass through.
	row := dlRow(uuid.New(), requests.DownloadSearching, "Waiting for release (announced)")
	if got := toRequestDTOFor(row, false).Download.Message; got != "Waiting for release (announced)" {
		t.Errorf("searching user message = %q", got)
	}
}

func TestRequests_List_UsernamesAndDownload(t *testing.T) {
	alice, bob, admin := uuid.New(), uuid.New(), uuid.New()
	db := &listFakeDB{
		rqFakeDB: &rqFakeDB{perms: map[uuid.UUID]gen.GetUserRequestPermissionsRow{
			alice: {Username: "alice"}, bob: {Username: "bob"}, admin: {Username: "root", IsAdmin: true},
		}},
		rows: []gen.MediaRequest{
			dlRow(alice, requests.DownloadStalled, "qBittorrent at 10.0.0.5:8080 is unreachable"),
			dlRow(bob, "", ""),
		},
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	h := NewRequestHandler(requests.NewService(db, rqFakeTMDB{}, nil, logger), logger)

	type listResp struct {
		Data []struct {
			UserID   uuid.UUID `json:"user_id"`
			Username string    `json:"username"`
			Download *struct {
				State   string `json:"state"`
				Message string `json:"message"`
			} `json:"download"`
		} `json:"data"`
	}

	rec := httptest.NewRecorder()
	h.List(rec, rqAs(httptest.NewRequest("GET", "/api/v1/requests?scope=all", nil), admin, true))
	var all listResp
	if err := json.Unmarshal(rec.Body.Bytes(), &all); err != nil || rec.Code != http.StatusOK {
		t.Fatalf("admin list: %d %s", rec.Code, rec.Body)
	}
	if len(all.Data) != 2 || all.Data[0].Username != "alice" || all.Data[1].Username != "bob" {
		t.Fatalf("admin list = %+v", all.Data)
	}
	if all.Data[0].Download == nil || all.Data[0].Download.Message != "qBittorrent at 10.0.0.5:8080 is unreachable" {
		t.Errorf("admin should see the raw reason: %+v", all.Data[0].Download)
	}
	if all.Data[1].Download != nil {
		t.Error("unsynced request has a download block")
	}

	rec = httptest.NewRecorder()
	h.List(rec, rqAs(httptest.NewRequest("GET", "/api/v1/requests", nil), alice, false))
	var mine listResp
	if err := json.Unmarshal(rec.Body.Bytes(), &mine); err != nil || rec.Code != http.StatusOK {
		t.Fatalf("user list: %d %s", rec.Code, rec.Body)
	}
	if len(mine.Data) != 1 || mine.Data[0].Username != "alice" || mine.Data[0].Download == nil {
		t.Fatalf("user list = %+v", mine.Data)
	}
	if strings.Contains(mine.Data[0].Download.Message, "10.0.0.5") {
		t.Errorf("user saw the download client's address: %q", mine.Data[0].Download.Message)
	}

	// Single get carries the username too.
	rec = httptest.NewRecorder()
	h.Get(rec, withChiParam(rqAs(httptest.NewRequest("GET", "/", nil), alice, false), "id", db.rows[0].ID.String()))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"username":"alice"`) {
		t.Errorf("get: %d %s", rec.Code, rec.Body)
	}
}

// ── webhook-triggered sync ──────────────────────────────────────────────────

type syncCall struct {
	kind          string
	tmdbID, arrID int
}

type recordingSyncer struct {
	mu    sync.Mutex
	calls []syncCall
}

func (s *recordingSyncer) TriggerDownloadSync(kind string, tmdbID, arrItemID int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = append(s.calls, syncCall{kind, tmdbID, arrItemID})
}

func TestArrWebhook_TriggersDownloadSync(t *testing.T) {
	cases := []struct {
		name     string
		payload  map[string]any
		wantCode int
		want     []syncCall
	}{
		{"radarr grab", map[string]any{"eventType": "Grab", "movie": map[string]any{"id": 7, "tmdbId": 949, "title": "Heat"}},
			http.StatusNoContent, []syncCall{{"radarr", 949, 7}}},
		{"sonarr grab without tmdb (v3)", map[string]any{"eventType": "Grab", "series": map[string]any{"id": 3, "tvdbId": 371980}},
			http.StatusNoContent, []syncCall{{"sonarr", 0, 3}}},
		{"sonarr import complete", map[string]any{"eventType": "ImportComplete", "series": map[string]any{"id": 3, "tmdbId": 95396}},
			http.StatusNoContent, []syncCall{{"sonarr", 95396, 3}}},
		{"manual interaction", map[string]any{"eventType": "ManualInteractionRequired", "movie": map[string]any{"id": 7}},
			http.StatusNoContent, []syncCall{{"radarr", 0, 7}}},
		{"download still scans", map[string]any{"eventType": "Download", "movie": map[string]any{"id": 7, "tmdbId": 949, "folderPath": "/movies/Heat"},
			"movieFile": map[string]any{"path": "/movies/Heat/Heat.mkv"}},
			http.StatusOK, []syncCall{{"radarr", 949, 7}}},
		{"test event", map[string]any{"eventType": "Test"}, http.StatusOK, nil},
		{"health event", map[string]any{"eventType": "Health", "movie": map[string]any{"id": 7}}, http.StatusNoContent, nil},
		{"lidarr grab", map[string]any{"eventType": "Grab", "artist": map[string]any{"artistName": "x"}}, http.StatusNoContent, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			libs := &mockArrLibs{foundID: uuid.New()}
			syncer := &recordingSyncer{}
			h := NewArrHandler(&mockArrSettings{key: "k"}, libs, slog.Default()).WithDownloadSyncer(syncer)
			rec := arrRequest(t, h, "k", c.payload)
			if rec.Code != c.wantCode {
				t.Errorf("status %d, want %d (%s)", rec.Code, c.wantCode, rec.Body)
			}
			if len(syncer.calls) != len(c.want) {
				t.Fatalf("sync calls = %+v, want %+v", syncer.calls, c.want)
			}
			for i := range c.want {
				if syncer.calls[i] != c.want[i] {
					t.Errorf("call %d = %+v, want %+v", i, syncer.calls[i], c.want[i])
				}
			}
			if c.payload["eventType"] == "Download" && libs.lastDir == "" {
				t.Error("the Download event no longer triggers the library scan")
			}
		})
	}
}

func TestArrWebhook_UnauthenticatedNeverSyncs(t *testing.T) {
	syncer := &recordingSyncer{}
	h := NewArrHandler(&mockArrSettings{key: "k"}, &mockArrLibs{}, slog.Default()).WithDownloadSyncer(syncer)
	rec := arrRequest(t, h, "wrong", map[string]any{"eventType": "Grab", "movie": map[string]any{"id": 7}})
	if rec.Code != http.StatusUnauthorized || len(syncer.calls) != 0 {
		t.Errorf("status %d calls %d", rec.Code, len(syncer.calls))
	}
}

func TestArrWebhook_NoSyncerIsFine(t *testing.T) {
	h := NewArrHandler(&mockArrSettings{key: "k"}, &mockArrLibs{}, slog.Default())
	rec := arrRequest(t, h, "k", map[string]any{"eventType": "Grab", "movie": map[string]any{"id": 7}})
	if rec.Code != http.StatusNoContent {
		t.Errorf("status %d", rec.Code)
	}
}

// ── arr-service health ──────────────────────────────────────────────────────

func healthArr(t *testing.T, wantKey string, override map[string]string) *httptest.Server {
	t.Helper()
	bodies := map[string]string{
		"/api/v3/system/status": `{"appName":"Radarr","version":"5.9.1"}`,
		"/api/v3/health": `[{"source":"IndexerStatusCheck","type":"warning","message":"Indexers unavailable","wikiUrl":"https://wiki.servarr.com/x"},
			{"source":"Evil","type":"error","message":"bad link","wikiUrl":"javascript:alert(1)"}]`,
		"/api/v3/diskspace": `[{"path":"/","label":"root","freeSpace":10,"totalSpace":100},
			{"path":"/movies","label":"media","freeSpace":500,"totalSpace":10000},
			{"path":"/boot","label":"","freeSpace":1,"totalSpace":2}]`,
		"/api/v3/rootfolder": `[{"id":1,"path":"/movies/hd","freeSpace":500}]`,
		"/api/v3/queue":      `{"totalRecords":3,"records":[{"id":1}]}`,
	}
	for k, v := range override {
		bodies[k] = v
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Api-Key") != wantKey {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		body, ok := bodies[r.URL.Path]
		if !ok || body == "500" {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(srv.Close)
	return srv
}

type healthResp struct {
	Data struct {
		Reachable bool   `json:"reachable"`
		Version   string `json:"version"`
		Error     string `json:"error"`
		Checks    []struct {
			Type, Source, Message string
			WikiURL               string `json:"wiki_url"`
		} `json:"checks"`
		Disks []struct {
			Path       string `json:"path"`
			Label      string `json:"label"`
			FreeBytes  int64  `json:"free_bytes"`
			TotalBytes int64  `json:"total_bytes"`
		} `json:"disks"`
		QueueCount *int `json:"queue_count"`
	} `json:"data"`
}

func callHealth(t *testing.T, h *ArrServicesHandler, id uuid.UUID) (int, healthResp, string) {
	t.Helper()
	rec := httptest.NewRecorder()
	h.Health(rec, withChiParam(httptest.NewRequest("GET", "/", nil), "id", id.String()))
	var out healthResp
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return rec.Code, out, rec.Body.String()
}

func TestArrServicesHealth_Reachable(t *testing.T) {
	srv := healthArr(t, "the-key", nil)
	id := uuid.New()
	db := &recordingArrDB{stored: gen.ArrService{ID: id, Kind: "radarr", BaseUrl: srv.URL, ApiKey: "the-key", Enabled: true}}
	h := NewArrServicesHandler(db, slog.Default())
	h.SetArrClientFactory(func(base, key string) *arr.Client {
		c := arr.New(base, key)
		c.HTTPClient = srv.Client()
		return c
	})

	code, got, body := callHealth(t, h, id)
	if code != http.StatusOK || !got.Data.Reachable || got.Data.Version != "5.9.1" {
		t.Fatalf("%d %s", code, body)
	}
	if len(got.Data.Checks) != 2 || got.Data.Checks[0].Type != "warning" || got.Data.Checks[0].WikiURL != "https://wiki.servarr.com/x" {
		t.Errorf("checks = %+v", got.Data.Checks)
	}
	if got.Data.Checks[1].WikiURL != "" {
		t.Errorf("non-http wiki link passed through: %q", got.Data.Checks[1].WikiURL)
	}
	// Only the mount behind the root folder, not / or /boot.
	if len(got.Data.Disks) != 1 || got.Data.Disks[0].Path != "/movies" || got.Data.Disks[0].FreeBytes != 500 || got.Data.Disks[0].TotalBytes != 10000 {
		t.Errorf("disks = %+v", got.Data.Disks)
	}
	if got.Data.QueueCount == nil || *got.Data.QueueCount != 3 {
		t.Errorf("queue_count = %v", got.Data.QueueCount)
	}
	if strings.Contains(body, "the-key") {
		t.Error("response echoes the api key")
	}
}

func TestArrServicesHealth_SectionsDegradeIndependently(t *testing.T) {
	srv := healthArr(t, "k", map[string]string{"/api/v3/rootfolder": "500", "/api/v3/queue": "500", "/api/v3/health": "500"})
	id := uuid.New()
	db := &recordingArrDB{stored: gen.ArrService{ID: id, Kind: "sonarr", BaseUrl: srv.URL, ApiKey: "k"}}
	h := NewArrServicesHandler(db, slog.Default())
	h.SetArrClientFactory(func(base, key string) *arr.Client {
		c := arr.New(base, key)
		c.HTTPClient = srv.Client()
		return c
	})
	code, got, body := callHealth(t, h, id)
	if code != http.StatusOK || !got.Data.Reachable {
		t.Fatalf("%d %s", code, body)
	}
	// Root folders unknown → every sized mount.
	if len(got.Data.Disks) != 3 {
		t.Errorf("disks = %+v, want all three mounts", got.Data.Disks)
	}
	if got.Data.QueueCount != nil || len(got.Data.Checks) != 0 {
		t.Errorf("queue_count=%v checks=%v, want null / empty", got.Data.QueueCount, got.Data.Checks)
	}
	if !strings.Contains(body, `"checks":[]`) {
		t.Errorf("checks should be an empty array, not null: %s", body)
	}
}

func TestArrServicesHealth_Unreachable(t *testing.T) {
	srv := healthArr(t, "right", nil)
	id := uuid.New()
	db := &recordingArrDB{stored: gen.ArrService{ID: id, Kind: "radarr", BaseUrl: srv.URL, ApiKey: "wrong"}}
	h := NewArrServicesHandler(db, slog.Default())
	h.SetArrClientFactory(func(base, key string) *arr.Client {
		c := arr.New(base, key)
		c.HTTPClient = srv.Client()
		return c
	})
	code, got, body := callHealth(t, h, id)
	if code != http.StatusOK || got.Data.Reachable || !strings.Contains(got.Data.Error, "rejected the API key") {
		t.Fatalf("%d %s", code, body)
	}
	if strings.Contains(body, srv.URL) {
		t.Error("error leaks the base URL")
	}

	// A dead host: generic phrase, no transport detail.
	dead := httptest.NewServer(http.NotFoundHandler())
	deadURL := dead.URL
	dead.Close()
	db.stored.BaseUrl = deadURL
	code, got, body = callHealth(t, h, id)
	if code != http.StatusOK || got.Data.Reachable || got.Data.Error != "the instance could not be reached" {
		t.Errorf("%d %s", code, body)
	}
	if strings.Contains(body, deadURL) {
		t.Error("error leaks the base URL")
	}
}

func TestArrServicesHealth_NotFoundAndBadID(t *testing.T) {
	h := NewArrServicesHandler(&stubNoArrDB{}, slog.Default())
	if code, _, _ := callHealth(t, h, uuid.New()); code != http.StatusNotFound {
		t.Errorf("unknown service: %d", code)
	}
	rec := httptest.NewRecorder()
	h.Health(rec, withChiParam(httptest.NewRequest("GET", "/", nil), "id", "nope"))
	if rec.Code != http.StatusBadRequest {
		t.Errorf("bad id: %d", rec.Code)
	}
}

// stubNoArrDB is an ArrServicesDB with no rows.
type stubNoArrDB struct{ recordingArrDB }

func (stubNoArrDB) GetArrService(context.Context, uuid.UUID) (gen.ArrService, error) {
	return gen.ArrService{}, pgx.ErrNoRows
}

func TestRootFolderDisks(t *testing.T) {
	disks := []arr.DiskSpace{
		{Path: "/", TotalSpace: 100},
		{Path: "/data", TotalSpace: 200},
		{Path: "/data/media", TotalSpace: 300},
		{Path: `D:\`, TotalSpace: 400},
		{Path: "/proc", TotalSpace: 0},
	}
	cases := []struct {
		name    string
		folders []arr.RootFolder
		want    []string
	}{
		{"longest prefix wins", []arr.RootFolder{{Path: "/data/media/movies"}}, []string{"/data/media"}},
		{"sibling folder", []arr.RootFolder{{Path: "/data/tv/"}}, []string{"/data"}},
		{"windows path", []arr.RootFolder{{Path: `d:\Movies`}}, []string{`D:\`}},
		{"two folders two disks, list order", []arr.RootFolder{{Path: "/data/media/x"}, {Path: "/other"}}, []string{"/", "/data/media"}},
		{"no folders: every sized mount", nil, []string{"/", "/data", "/data/media", `D:\`}},
		{"no prefix match is not a partial-name match", []arr.RootFolder{{Path: "/database"}}, []string{"/"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := rootFolderDisks(disks, c.folders)
			var paths []string
			for _, d := range got {
				paths = append(paths, d.Path)
			}
			if strings.Join(paths, ",") != strings.Join(c.want, ",") {
				t.Errorf("got %v, want %v", paths, c.want)
			}
		})
	}
}
