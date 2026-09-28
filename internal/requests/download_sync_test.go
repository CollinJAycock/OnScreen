package requests

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onscreen/onscreen/internal/arr"
	"github.com/onscreen/onscreen/internal/db/gen"
	"github.com/onscreen/onscreen/internal/notification"
)

// ── fakeDB: download-status queries ─────────────────────────────────────────
// These mirror the SQL in request_downloads.sql closely enough to exercise
// the transitions (the integration test checks the SQL itself).

func (f *fakeDB) ListMediaRequestsForDownloadSync(_ context.Context, p gen.ListMediaRequestsForDownloadSyncParams) ([]gen.MediaRequest, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []gen.MediaRequest
	for _, r := range f.reqs {
		if !r.ServiceID.Valid {
			continue
		}
		switch r.Status {
		case StatusApproved, StatusDownloading:
		case StatusFailed:
			if !r.UpdatedAt.Time.After(p.FailedSince.Time) {
				continue
			}
		default:
			continue
		}
		out = append(out, r)
	}
	// Least recently checked first (never-checked first), then oldest.
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && syncLess(out[j], out[j-1]); j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	if int32(len(out)) > p.MaxRows {
		out = out[:p.MaxRows]
	}
	return out, nil
}

func syncLess(a, b gen.MediaRequest) bool {
	if a.DownloadUpdatedAt.Valid != b.DownloadUpdatedAt.Valid {
		return !a.DownloadUpdatedAt.Valid
	}
	if a.DownloadUpdatedAt.Valid && !a.DownloadUpdatedAt.Time.Equal(b.DownloadUpdatedAt.Time) {
		return a.DownloadUpdatedAt.Time.Before(b.DownloadUpdatedAt.Time)
	}
	return a.CreatedAt.Time.Before(b.CreatedAt.Time)
}

func (f *fakeDB) SetMediaRequestArrItem(_ context.Context, p gen.SetMediaRequestArrItemParams) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if r, ok := f.reqs[p.ID]; ok {
		r.ArrItemID = p.ArrItemID
		f.reqs[p.ID] = r
	}
	return nil
}

func (f *fakeDB) setDownload(r *gen.MediaRequest, state *string, progress *float32, eta pgtype.Timestamptz, size *int64, msg *string) {
	r.DownloadState = state
	r.DownloadProgress = progress
	r.DownloadEta = eta
	r.DownloadSizeBytes = size
	r.DownloadMessage = msg
	r.DownloadUpdatedAt = pgtype.Timestamptz{Time: f.now(), Valid: true}
}

func (f *fakeDB) UpdateMediaRequestDownload(_ context.Context, p gen.UpdateMediaRequestDownloadParams) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	r, ok := f.reqs[p.ID]
	if !ok || (r.Status != StatusApproved && r.Status != StatusDownloading && r.Status != StatusFailed) {
		return 0, nil
	}
	f.setDownload(&r, p.DownloadState, p.DownloadProgress, p.DownloadEta, p.DownloadSizeBytes, p.DownloadMessage)
	f.reqs[p.ID] = r
	return 1, nil
}

func (f *fakeDB) FailMediaRequestDownload(_ context.Context, p gen.FailMediaRequestDownloadParams) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	r, ok := f.reqs[p.ID]
	if !ok || (r.Status != StatusApproved && r.Status != StatusDownloading) {
		return 0, nil
	}
	r.Status = StatusFailed
	r.UpdatedAt = pgtype.Timestamptz{Time: f.now(), Valid: true}
	f.setDownload(&r, ptr(DownloadFailed), p.DownloadProgress, pgtype.Timestamptz{}, p.DownloadSizeBytes, p.DownloadMessage)
	f.reqs[p.ID] = r
	return 1, nil
}

func (f *fakeDB) RecoverMediaRequestDownload(_ context.Context, p gen.RecoverMediaRequestDownloadParams) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	r, ok := f.reqs[p.ID]
	if !ok || r.Status != StatusFailed {
		return 0, nil
	}
	for _, o := range f.reqs {
		if o.ID != r.ID && o.UserID == r.UserID && o.Type == r.Type && o.TmdbID == r.TmdbID &&
			(o.Status == StatusPending || o.Status == StatusApproved || o.Status == StatusDownloading) {
			return 0, nil
		}
	}
	r.Status = StatusDownloading
	r.UpdatedAt = pgtype.Timestamptz{Time: f.now(), Valid: true}
	f.setDownload(&r, p.DownloadState, p.DownloadProgress, p.DownloadEta, p.DownloadSizeBytes, p.DownloadMessage)
	f.reqs[p.ID] = r
	return 1, nil
}

func (f *fakeDB) ListUsernamesByIDs(_ context.Context, ids []uuid.UUID) ([]gen.ListUsernamesByIDsRow, error) {
	var out []gen.ListUsernamesByIDsRow
	for _, id := range ids {
		if p, ok := f.perms[id]; ok {
			out = append(out, gen.ListUsernamesByIDsRow{ID: id, Username: p.Username})
		}
	}
	return out, nil
}

// get returns the current row.
func (f *fakeDB) get(id uuid.UUID) gen.MediaRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.reqs[id]
}

// ── programmable Radarr/Sonarr ──────────────────────────────────────────────

// statusArr is a fake Radarr/Sonarr serving the endpoints the sync reads.
// Bodies are raw JSON so tests can hand it whatever shape a real build sends.
type statusArr struct {
	mu        sync.Mutex
	queue     []string       // queue record objects
	items     map[int]string // movie / series id → object JSON
	byTMDB    map[int]int    // tmdb id → movie id (GET /movie?tmdbId=)
	byTVDB    map[int]int    // tvdb id → series id (GET /series?tvdbId=)
	history   map[int]string // item id → JSON array
	failPaths map[string]int // path prefix → status code to answer with
	calls     map[string]int // path → count
	queueHits atomic.Int32
}

func newStatusArr() *statusArr {
	return &statusArr{
		items: map[int]string{}, byTMDB: map[int]int{}, byTVDB: map[int]int{},
		history: map[int]string{}, failPaths: map[string]int{}, calls: map[string]int{},
	}
}

func (a *statusArr) set(fn func(a *statusArr)) {
	a.mu.Lock()
	defer a.mu.Unlock()
	fn(a)
}

func (a *statusArr) count(path string) int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.calls[path]
}

func (a *statusArr) totalCalls() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	n := 0
	for _, c := range a.calls {
		n += c
	}
	return n
}

func (a *statusArr) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.calls[r.URL.Path]++
	for prefix, code := range a.failPaths {
		if strings.HasPrefix(r.URL.Path, prefix) {
			w.WriteHeader(code)
			return
		}
	}
	w.Header().Set("Content-Type", "application/json")
	p := r.URL.Path
	q := r.URL.Query()
	switch {
	case p == "/api/v3/queue":
		a.queueHits.Add(1)
		fmt.Fprintf(w, `{"page":1,"totalRecords":%d,"records":[%s]}`, len(a.queue), strings.Join(a.queue, ","))
	case p == "/api/v3/history/movie" || p == "/api/v3/history/series":
		key := "movieId"
		if strings.HasSuffix(p, "series") {
			key = "seriesId"
		}
		id, _ := strconv.Atoi(q.Get(key))
		if h, ok := a.history[id]; ok {
			_, _ = io.WriteString(w, h)
			return
		}
		_, _ = io.WriteString(w, `[]`)
	case p == "/api/v3/movie" && q.Get("tmdbId") != "":
		tmdb, _ := strconv.Atoi(q.Get("tmdbId"))
		if id, ok := a.byTMDB[tmdb]; ok {
			fmt.Fprintf(w, `[{"id":%d,"tmdbId":%d,"monitored":true}]`, id, tmdb)
			return
		}
		_, _ = io.WriteString(w, `[]`)
	case p == "/api/v3/series" && q.Get("tvdbId") != "":
		tvdb, _ := strconv.Atoi(q.Get("tvdbId"))
		if id, ok := a.byTVDB[tvdb]; ok {
			fmt.Fprintf(w, `[{"id":%d,"tvdbId":%d}]`, id, tvdb)
			return
		}
		_, _ = io.WriteString(w, `[]`)
	case strings.HasPrefix(p, "/api/v3/movie/") || strings.HasPrefix(p, "/api/v3/series/"):
		id, _ := strconv.Atoi(p[strings.LastIndex(p, "/")+1:])
		if body, ok := a.items[id]; ok {
			_, _ = io.WriteString(w, body)
			return
		}
		http.NotFound(w, r)
	default:
		http.NotFound(w, r)
	}
}

// syncHarness is a Service wired to a fake DB with one Radarr and one Sonarr
// service, each backed by its own statusArr.
type syncHarness struct {
	svc            *Service
	db             *fakeDB
	notify         *fakeNotifier
	radarr, sonarr *statusArr
	radarrSvc      gen.ArrService
	sonarrSvc      gen.ArrService
	now            time.Time
	requester      uuid.UUID
}

func newSyncHarness(t *testing.T) *syncHarness {
	t.Helper()
	h := &syncHarness{
		db: newFakeDB(), notify: &fakeNotifier{},
		radarr: newStatusArr(), sonarr: newStatusArr(),
		now: time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC),
	}
	rs := httptest.NewServer(h.radarr)
	ss := httptest.NewServer(h.sonarr)
	t.Cleanup(rs.Close)
	t.Cleanup(ss.Close)
	h.radarrSvc = gen.ArrService{ID: uuid.New(), Name: "Radarr 1080p", Kind: "radarr", BaseUrl: rs.URL, ApiKey: "k", Enabled: true, IsDefault: true}
	h.sonarrSvc = gen.ArrService{ID: uuid.New(), Name: "Sonarr", Kind: "sonarr", BaseUrl: ss.URL, ApiKey: "k", Enabled: true, IsDefault: true}
	h.db.addService(h.radarrSvc)
	h.db.addService(h.sonarrSvc)
	h.db.now = func() time.Time { return h.now }
	h.svc = NewService(h.db, fakeTMDB{}, h.notify, slog.New(slog.NewTextHandler(io.Discard, nil)))
	h.svc.now = func() time.Time { return h.now }
	h.requester = uuid.New()
	h.db.perms[h.requester] = gen.GetUserRequestPermissionsRow{Username: "alice", CanRequest: true}
	return h
}

// request seeds an in-flight request routed to the service of its type.
func (h *syncHarness) request(typ string, tmdbID int32, status string, arrItemID int32) gen.MediaRequest {
	svc := h.radarrSvc
	if typ == TypeShow {
		svc = h.sonarrSvc
	}
	r := gen.MediaRequest{
		ID: uuid.New(), UserID: h.requester, Type: typ, TmdbID: tmdbID, Title: "Title " + strconv.Itoa(int(tmdbID)),
		Status: status, ServiceID: pgUUID(&svc.ID),
		CreatedAt: pgtype.Timestamptz{Time: h.now.Add(-time.Hour), Valid: true},
		UpdatedAt: pgtype.Timestamptz{Time: h.now.Add(-time.Hour), Valid: true},
	}
	if arrItemID > 0 {
		r.ArrItemID = &arrItemID
	}
	h.db.mu.Lock()
	h.db.reqs[r.ID] = r
	h.db.mu.Unlock()
	return r
}

func (h *syncHarness) sync(t *testing.T) SyncResult {
	t.Helper()
	res, err := h.svc.SyncDownloads(context.Background(), SyncFilter{})
	if err != nil {
		t.Fatalf("SyncDownloads: %v", err)
	}
	return res
}

func (h *syncHarness) failNotices() (user, admin int) {
	h.notify.mu.Lock()
	defer h.notify.mu.Unlock()
	for _, n := range h.notify.sent {
		if n.typ == notification.TypeRequestFailed {
			user++
		}
	}
	for _, n := range h.notify.adminSent {
		if n.typ == notification.TypeRequestFailed {
			admin++
		}
	}
	return user, admin
}

func state(r gen.MediaRequest) string {
	if r.DownloadState == nil {
		return "<nil>"
	}
	return *r.DownloadState
}

// ── pure mapping ────────────────────────────────────────────────────────────

func TestQueueRowState(t *testing.T) {
	msg := []arr.StatusMessage{{Title: "Heat.mkv", Messages: []string{"Sample file"}}}
	cases := []struct {
		name    string
		rec     arr.QueueRecord
		want    string
		wantMsg string
	}{
		{"downloading", arr.QueueRecord{Status: "downloading", TrackedDownloadStatus: "ok", TrackedDownloadState: "downloading"}, DownloadDownloading, ""},
		{"capitalised v3", arr.QueueRecord{Status: "Downloading", TrackedDownloadStatus: "Ok"}, DownloadDownloading, ""},
		{"queued", arr.QueueRecord{Status: "queued"}, DownloadQueued, ""},
		{"delay profile", arr.QueueRecord{Status: "delay"}, DownloadQueued, "Held by a delay profile"},
		{"paused", arr.QueueRecord{Status: "paused"}, DownloadStalled, "Paused in the download client"},
		{"client unavailable", arr.QueueRecord{Status: "downloadClientUnavailable"}, DownloadStalled, "The download client is unavailable"},
		{"warning status", arr.QueueRecord{Status: "downloading", TrackedDownloadStatus: "warning", StatusMessages: msg}, DownloadStalled, "Sample file"},
		{"error, no text", arr.QueueRecord{Status: "downloading", TrackedDownloadStatus: "error"}, DownloadStalled, "The download client reports a problem"},
		{"status messages alone", arr.QueueRecord{Status: "downloading", StatusMessages: msg}, DownloadStalled, "Sample file"},
		{"error message", arr.QueueRecord{Status: "warning", ErrorMessage: "tracker down"}, DownloadStalled, "tracker down"},
		{"import pending", arr.QueueRecord{Status: "completed", TrackedDownloadState: "importPending"}, DownloadImportPending, ""},
		{"importing", arr.QueueRecord{Status: "completed", TrackedDownloadState: "importing"}, DownloadImportPending, ""},
		{"import pending with warning (old builds)", arr.QueueRecord{Status: "completed", TrackedDownloadState: "importPending", TrackedDownloadStatus: "warning", StatusMessages: msg}, DownloadStalled, "Sample file"},
		{"import blocked", arr.QueueRecord{Status: "completed", TrackedDownloadState: "importBlocked", TrackedDownloadStatus: "warning"}, DownloadStalled, "Import blocked — needs a manual import"},
		{"completed, no tracked state", arr.QueueRecord{Status: "completed"}, DownloadImportPending, ""},
		{"failed (handled)", arr.QueueRecord{Status: "failed", TrackedDownloadState: "failed", ErrorMessage: "No files"}, DownloadFailed, "No files"},
		{"failed pending (not handled yet)", arr.QueueRecord{Status: "failed", TrackedDownloadState: "failedPending"}, DownloadStalled, "The download failed — waiting for the app to handle it"},
		{"client failed, tracked still downloading", arr.QueueRecord{Status: "failed", TrackedDownloadState: "downloading"}, DownloadStalled, "The download failed — waiting for the app to handle it"},
		{"ignored", arr.QueueRecord{TrackedDownloadState: "ignored"}, DownloadStalled, "The download was ignored"},
		{"unknown status with bytes", arr.QueueRecord{Status: "fallback", Size: 100, SizeLeft: 40}, DownloadDownloading, ""},
		{"unknown status no bytes", arr.QueueRecord{}, DownloadQueued, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, msg := queueRowState(c.rec)
			if got != c.want || msg != c.wantMsg {
				t.Errorf("got %q %q, want %q %q", got, msg, c.want, c.wantMsg)
			}
		})
	}
}

func TestAggregateQueue(t *testing.T) {
	now := time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)
	ep := func(n int) *arr.QueueEpisode { return &arr.QueueEpisode{SeasonNumber: 1, EpisodeNumber: n} }
	eta := func(m int) arr.Timestamp { return arr.Timestamp{Time: now.Add(time.Duration(m) * time.Minute)} }

	t.Run("season pack counted once", func(t *testing.T) {
		// A pack of 3 episodes: the queue lists it three times with the
		// pack's total size on every row.
		rows := []arr.QueueRecord{
			{ID: 1, DownloadID: "PACK", Size: 3000, SizeLeft: 1500, Status: "downloading", Episode: ep(1), EstimatedCompletionTime: eta(10)},
			{ID: 2, DownloadID: "PACK", Size: 3000, SizeLeft: 1500, Status: "downloading", Episode: ep(2), EstimatedCompletionTime: eta(10)},
			{ID: 3, DownloadID: "PACK", Size: 3000, SizeLeft: 1500, Status: "downloading", Episode: ep(3), EstimatedCompletionTime: eta(10)},
		}
		st := aggregateQueue(rows, now, true)
		if st.State != DownloadDownloading || st.Progress == nil || *st.Progress != 0.5 || *st.SizeBytes != 3000 {
			t.Errorf("got %+v progress=%v size=%v", st, deref64(st.Progress), st.SizeBytes)
		}
		if !st.ETA.Equal(now.Add(10*time.Minute)) || !st.Recovers {
			t.Errorf("eta=%v recovers=%v", st.ETA, st.Recovers)
		}
	})

	t.Run("size weighted progress and latest eta", func(t *testing.T) {
		rows := []arr.QueueRecord{
			{ID: 1, DownloadID: "A", Size: 1000, SizeLeft: 0, Status: "completed", TrackedDownloadState: "importPending"},
			{ID: 2, DownloadID: "B", Size: 3000, SizeLeft: 3000, Status: "downloading", EstimatedCompletionTime: eta(30)},
			{ID: 3, DownloadID: "C", Size: 0, Status: "queued", TimeLeft: "01:00:00"},
		}
		st := aggregateQueue(rows, now, true)
		if st.State != DownloadDownloading {
			t.Errorf("state = %q, want downloading (outranks queued and import_pending)", st.State)
		}
		if st.Progress == nil || *st.Progress != 0.25 || *st.SizeBytes != 4000 {
			t.Errorf("progress=%v size=%v, want 0.25 of 4000", deref64(st.Progress), st.SizeBytes)
		}
		if !st.ETA.Equal(now.Add(time.Hour)) {
			t.Errorf("eta = %v, want the latest (now+1h)", st.ETA)
		}
	})

	t.Run("stalled outranks downloading, labelled with the episode", func(t *testing.T) {
		rows := []arr.QueueRecord{
			{ID: 1, DownloadID: "A", Size: 100, SizeLeft: 50, Status: "downloading", Episode: ep(1)},
			{ID: 2, DownloadID: "B", Size: 100, SizeLeft: 100, Status: "warning", ErrorMessage: "No seeds", Episode: ep(2)},
		}
		st := aggregateQueue(rows, now, true)
		if st.State != DownloadStalled || st.Message != "S01E02: No seeds" {
			t.Errorf("got %q %q", st.State, st.Message)
		}
	})

	t.Run("one failed download among active ones is not a failure", func(t *testing.T) {
		rows := []arr.QueueRecord{
			{ID: 1, DownloadID: "A", Size: 100, SizeLeft: 20, Status: "downloading", Episode: ep(1)},
			{ID: 2, DownloadID: "B", Size: 100, SizeLeft: 100, Status: "failed", TrackedDownloadState: "failed", Episode: ep(2)},
		}
		st := aggregateQueue(rows, now, true)
		if st.State != DownloadDownloading || !strings.Contains(st.Message, "1 of 2 downloads failed") {
			t.Errorf("got %q %q", st.State, st.Message)
		}
		// The failed download doesn't drag progress down.
		if *st.Progress != 0.8 {
			t.Errorf("progress = %v, want 0.8", *st.Progress)
		}
	})

	t.Run("every download failed", func(t *testing.T) {
		rows := []arr.QueueRecord{
			{ID: 1, DownloadID: "A", Size: 100, Status: "failed", TrackedDownloadState: "failed", ErrorMessage: "Unpack failed"},
		}
		st := aggregateQueue(rows, now, false)
		if st.State != DownloadFailed || st.Message != "Unpack failed" || st.Recovers {
			t.Errorf("got %+v", st)
		}
		if !st.ETA.IsZero() {
			t.Error("a failed download has no eta")
		}
	})

	t.Run("movie message not episode-labelled", func(t *testing.T) {
		rows := []arr.QueueRecord{{ID: 1, Status: "paused"}}
		st := aggregateQueue(rows, now, false)
		if st.State != DownloadStalled || st.Message != "Paused in the download client" || st.Progress != nil {
			t.Errorf("got %+v", st)
		}
	})
}

func deref64(p *float64) float64 {
	if p == nil {
		return -1
	}
	return *p
}

func TestIdleStatus(t *testing.T) {
	d := func(h int) arr.Timestamp {
		return arr.Timestamp{Time: time.Date(2026, 9, 27, h, 0, 0, 0, time.UTC)}
	}
	failed := arr.HistoryRecord{ID: 2, EventType: arr.EventDownloadFailed, Date: d(11), Data: map[string]any{"message": "Download client reported failure"}}
	grab := arr.HistoryRecord{ID: 1, EventType: arr.EventGrabbed, Date: d(10)}
	regrab := arr.HistoryRecord{ID: 3, EventType: arr.EventGrabbed, Date: d(12)}
	imported := arr.HistoryRecord{ID: 4, EventType: arr.EventDownloadFolderImported, Date: d(13)}
	renamed := arr.HistoryRecord{ID: 5, EventType: "movieFileRenamed", Date: d(14)}

	cases := []struct {
		name      string
		monitored bool
		hasFiles  bool
		hist      []arr.HistoryRecord
		want      string
		recovers  bool
		msg       string
	}{
		{"file on disk", true, true, nil, DownloadImportPending, true, "Downloaded — waiting for the library scan"},
		{"never grabbed", true, false, nil, DownloadSearching, false, ""},
		{"failed, nothing newer", true, false, []arr.HistoryRecord{grab, failed}, DownloadFailed, false, "Download client reported failure"},
		{"failed then re-grabbed", true, false, []arr.HistoryRecord{grab, failed, regrab}, DownloadSearching, true, ""},
		{"imported after failure", true, false, []arr.HistoryRecord{failed, imported}, DownloadSearching, true, ""},
		{"unrelated newer events ignored", true, false, []arr.HistoryRecord{grab, failed, renamed}, DownloadFailed, false, "Download client reported failure"},
		{"unmonitored", false, false, []arr.HistoryRecord{grab}, DownloadStalled, true, "Not monitored in Radarr"},
		{"failure without a message", true, false, []arr.HistoryRecord{{ID: 9, EventType: arr.EventDownloadFailed, Date: d(1)}}, DownloadFailed, false,
			"The download failed and Radarr found nothing else to grab"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			st := idleStatus("Radarr", c.monitored, c.hasFiles, c.hist, "")
			if st.State != c.want || st.Recovers != c.recovers || st.Message != c.msg {
				t.Errorf("got %q recovers=%v %q, want %q recovers=%v %q", st.State, st.Recovers, st.Message, c.want, c.recovers, c.msg)
			}
		})
	}

	// Same-date events: the higher id is newer.
	tie := []arr.HistoryRecord{
		{ID: 7, EventType: arr.EventDownloadFailed, Date: d(5)},
		{ID: 8, EventType: arr.EventGrabbed, Date: d(5)},
	}
	if st := idleStatus("Radarr", true, false, tie, ""); st.State != DownloadSearching {
		t.Errorf("tie: got %q, want searching (the grab has the higher id)", st.State)
	}
}

func TestSeriesIdleStatus(t *testing.T) {
	stats := func(files, count int) *arr.SeriesStatistics {
		return &arr.SeriesStatistics{EpisodeFileCount: files, EpisodeCount: count}
	}
	cases := []struct {
		name string
		s    arr.Series
		want string
		msg  string
	}{
		{"all aired episodes on disk", arr.Series{Monitored: true, Statistics: stats(10, 10)}, DownloadImportPending, "Downloaded — waiting for the library scan"},
		{"some on disk", arr.Series{Monitored: true, Statistics: stats(3, 10)}, DownloadSearching, "3 of 10 episodes downloaded"},
		{"nothing aired", arr.Series{Monitored: true, Statistics: stats(0, 0)}, DownloadSearching, "No episodes have aired yet"},
		{"no statistics block", arr.Series{Monitored: true}, DownloadSearching, "No episodes have aired yet"},
		{"unmonitored", arr.Series{Statistics: stats(0, 4)}, DownloadStalled, "Not monitored in Sonarr"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			st := seriesIdleStatus(&c.s, nil)
			if st.State != c.want || st.Message != c.msg {
				t.Errorf("got %q %q, want %q %q", st.State, st.Message, c.want, c.msg)
			}
		})
	}
}

func TestMovieWaiting(t *testing.T) {
	if got := movieWaiting(&arr.Movie{Status: "announced"}); got != "Waiting for release (announced)" {
		t.Errorf("announced: %q", got)
	}
	if got := movieWaiting(&arr.Movie{Status: "inCinemas"}); got != "Waiting for release (in cinemas)" {
		t.Errorf("inCinemas: %q", got)
	}
	if got := movieWaiting(&arr.Movie{Status: "released", IsAvailable: true}); got != "" {
		t.Errorf("available: %q", got)
	}
}

func TestClampMessage(t *testing.T) {
	long := strings.Repeat("é", maxDownloadMessage+50)
	got := clampMessage("  " + long + "  ")
	if n := len([]rune(got)); n != maxDownloadMessage || !strings.HasSuffix(got, "…") {
		t.Errorf("len = %d suffix ok = %v", n, strings.HasSuffix(got, "…"))
	}
	if clampMessage("a \n\t b") != "a b" {
		t.Error("whitespace should collapse")
	}
}

// ── sync against fake instances ─────────────────────────────────────────────

func TestSync_MovieDownloading(t *testing.T) {
	h := newSyncHarness(t)
	r := h.request(TypeMovie, 949, StatusDownloading, 7)
	h.radarr.set(func(a *statusArr) {
		a.queue = []string{`{"id":1,"movieId":7,"size":2000,"sizeleft":500,"status":"downloading",
			"trackedDownloadStatus":"ok","trackedDownloadState":"downloading","estimatedCompletionTime":"2026-09-28T12:30:00Z"}`}
	})
	res := h.sync(t)
	got := h.db.get(r.ID)
	if state(got) != DownloadDownloading || got.DownloadProgress == nil || *got.DownloadProgress != 0.75 {
		t.Fatalf("row = state %s progress %v", state(got), got.DownloadProgress)
	}
	if !got.DownloadEta.Valid || !got.DownloadEta.Time.Equal(time.Date(2026, 9, 28, 12, 30, 0, 0, time.UTC)) {
		t.Errorf("eta = %+v", got.DownloadEta)
	}
	if got.DownloadSizeBytes == nil || *got.DownloadSizeBytes != 2000 || !got.DownloadUpdatedAt.Valid {
		t.Errorf("size=%v updated=%v", got.DownloadSizeBytes, got.DownloadUpdatedAt)
	}
	if got.Status != StatusDownloading || !got.UpdatedAt.Time.Equal(r.UpdatedAt.Time) {
		t.Errorf("status %q / updated_at moved (%v); a state refresh must not touch either", got.Status, got.UpdatedAt.Time)
	}
	if res.Updated != 1 || res.Services != 1 || res.ServiceErrors != 0 {
		t.Errorf("result = %+v", res)
	}
	// A queue entry means no per-request lookups at all.
	if h.radarr.count("/api/v3/movie/7") != 0 || h.radarr.count("/api/v3/history/movie") != 0 {
		t.Errorf("unexpected lookups: %v", h.radarr.calls)
	}
}

func TestSync_LinksUnlinkedRequestAndSearches(t *testing.T) {
	h := newSyncHarness(t)
	r := h.request(TypeMovie, 949, StatusDownloading, 0)
	h.radarr.set(func(a *statusArr) {
		a.byTMDB[949] = 7
		a.items[7] = `{"id":7,"tmdbId":949,"monitored":true,"hasFile":false,"isAvailable":true,"status":"released"}`
	})
	h.sync(t)
	got := h.db.get(r.ID)
	if got.ArrItemID == nil || *got.ArrItemID != 7 {
		t.Fatalf("arr_item_id = %v, want 7", got.ArrItemID)
	}
	if state(got) != DownloadSearching {
		t.Errorf("state = %s, want searching", state(got))
	}
	// Second run uses the stored link: no second tmdb resolution.
	h.sync(t)
	if n := h.radarr.count("/api/v3/movie"); n != 1 {
		t.Errorf("tmdb resolutions = %d, want 1", n)
	}
}

func TestSync_NotManagedIsStalled(t *testing.T) {
	h := newSyncHarness(t)
	r := h.request(TypeMovie, 949, StatusDownloading, 0)
	h.sync(t)
	got := h.db.get(r.ID)
	if state(got) != DownloadStalled || !strings.Contains(deref(got.DownloadMessage), "Not found in Radarr") {
		t.Errorf("state %s msg %q", state(got), deref(got.DownloadMessage))
	}
	if got.Status != StatusDownloading {
		t.Errorf("status = %s; an unmanaged title is not a download failure", got.Status)
	}
}

func TestSync_FailureTransitionNotifiesExactlyOnce(t *testing.T) {
	h := newSyncHarness(t)
	r := h.request(TypeMovie, 949, StatusDownloading, 7)
	h.radarr.set(func(a *statusArr) {
		a.items[7] = `{"id":7,"monitored":true,"hasFile":false,"isAvailable":true}`
		a.history[7] = `[{"id":1,"eventType":"grabbed","date":"2026-09-28T09:00:00Z"},
			{"id":2,"eventType":"downloadFailed","date":"2026-09-28T10:00:00Z","data":{"message":"Download client reported failure"}}]`
	})
	res := h.sync(t)
	got := h.db.get(r.ID)
	if got.Status != StatusFailed || state(got) != DownloadFailed || deref(got.DownloadMessage) != "Download client reported failure" {
		t.Fatalf("row = %s / %s / %q", got.Status, state(got), deref(got.DownloadMessage))
	}
	if res.Failed != 1 {
		t.Errorf("result.Failed = %d", res.Failed)
	}
	user, admin := h.failNotices()
	if user != 1 || admin != 1 {
		t.Fatalf("notices user=%d admin=%d, want 1/1", user, admin)
	}
	h.notify.mu.Lock()
	body, adminBody := h.notify.sent[0].body, h.notify.adminSent[0].body
	to := h.notify.to[0]
	h.notify.mu.Unlock()
	// The *arr reason can name internal hosts/paths: admins only.
	if to != h.requester || !strings.Contains(body, r.Title) || strings.Contains(body, "Download client reported failure") {
		t.Errorf("requester notice to %v body %q", to, body)
	}
	if !strings.Contains(adminBody, "alice") || !strings.Contains(adminBody, "Radarr 1080p") ||
		!strings.Contains(adminBody, "Download client reported failure") {
		t.Errorf("admin body %q should name the requester, the service and the reason", adminBody)
	}

	// The same failure seen again (and again) notifies nobody.
	for i := 0; i < 3; i++ {
		h.now = h.now.Add(5 * time.Minute)
		if res := h.sync(t); res.Failed != 0 {
			t.Errorf("rerun %d: Failed = %d", i, res.Failed)
		}
	}
	if user, admin := h.failNotices(); user != 1 || admin != 1 {
		t.Errorf("after reruns: notices user=%d admin=%d, want still 1/1", user, admin)
	}
	if got := h.db.get(r.ID); got.Status != StatusFailed {
		t.Errorf("status = %s", got.Status)
	}
}

func TestApplyDownloadStatus_StaleRowDoesNotRenotify(t *testing.T) {
	// Two syncs that both read the row while it was still downloading (the
	// scheduler run and a webhook-triggered one, say) both reach the failed
	// transition; only the one whose UPDATE flips the row may notify.
	h := newSyncHarness(t)
	stale := h.request(TypeMovie, 949, StatusDownloading, 7)
	st := DownloadStatus{State: DownloadFailed, Message: "boom"}
	var res SyncResult
	h.svc.applyDownloadStatus(context.Background(), h.radarrSvc, stale, st, &res)
	h.svc.applyDownloadStatus(context.Background(), h.radarrSvc, stale, st, &res)
	if res.Failed != 1 {
		t.Errorf("Failed = %d, want 1", res.Failed)
	}
	if u, a := h.failNotices(); u != 1 || a != 1 {
		t.Errorf("notices user=%d admin=%d, want 1/1", u, a)
	}
}

func TestSync_ConcurrentSyncsNotifyOnce(t *testing.T) {
	// Syncs started together (scheduler + webhooks) must still produce one
	// notice.
	h := newSyncHarness(t)
	h.request(TypeMovie, 949, StatusDownloading, 7)
	h.radarr.set(func(a *statusArr) {
		a.items[7] = `{"id":7,"monitored":true}`
		a.history[7] = `[{"id":2,"eventType":"downloadFailed","date":"2026-09-28T10:00:00Z"}]`
	})
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = h.svc.SyncDownloads(context.Background(), SyncFilter{})
		}()
	}
	wg.Wait()
	if user, admin := h.failNotices(); user != 1 || admin != 1 {
		t.Errorf("notices user=%d admin=%d, want 1/1", user, admin)
	}
}

func TestSync_AdminRequesterGetsOneNotice(t *testing.T) {
	h := newSyncHarness(t)
	h.db.perms[h.requester] = gen.GetUserRequestPermissionsRow{Username: "root", IsAdmin: true}
	h.request(TypeMovie, 949, StatusDownloading, 7)
	h.radarr.set(func(a *statusArr) {
		a.items[7] = `{"id":7,"monitored":true}`
		a.history[7] = `[{"id":2,"eventType":"downloadFailed","date":"2026-09-28T10:00:00Z"}]`
	})
	h.sync(t)
	if user, admin := h.failNotices(); user != 0 || admin != 1 {
		t.Errorf("notices user=%d admin=%d, want 0/1 (the admin notice covers them)", user, admin)
	}
}

func TestSync_FailedRequestRecoversOnNewGrab(t *testing.T) {
	h := newSyncHarness(t)
	r := h.request(TypeMovie, 949, StatusFailed, 7)
	h.db.mu.Lock()
	row := h.db.reqs[r.ID]
	row.DownloadState, row.DownloadMessage = ptr(DownloadFailed), ptr("old failure")
	h.db.reqs[r.ID] = row
	h.db.mu.Unlock()

	h.radarr.set(func(a *statusArr) {
		a.queue = []string{`{"id":5,"movieId":7,"size":100,"sizeleft":90,"status":"downloading"}`}
	})
	res := h.sync(t)
	got := h.db.get(r.ID)
	if got.Status != StatusDownloading || state(got) != DownloadDownloading || res.Recovered != 1 {
		t.Fatalf("row %s/%s recovered=%d", got.Status, state(got), res.Recovered)
	}
	if u, a := h.failNotices(); u+a != 0 {
		t.Errorf("recovery must not send failure notices (%d/%d)", u, a)
	}

	// ...and a later failure is a new failure: notified again, once.
	h.radarr.set(func(a *statusArr) {
		a.queue = nil
		a.items[7] = `{"id":7,"monitored":true}`
		a.history[7] = `[{"id":9,"eventType":"downloadFailed","date":"2026-09-28T13:00:00Z"}]`
	})
	h.sync(t)
	if u, a := h.failNotices(); u != 1 || a != 1 {
		t.Errorf("second failure notices %d/%d, want 1/1", u, a)
	}
}

func TestSync_FailedWithoutEvidenceStaysFailed(t *testing.T) {
	h := newSyncHarness(t)
	r := h.request(TypeMovie, 949, StatusFailed, 7)
	h.db.mu.Lock()
	row := h.db.reqs[r.ID]
	row.DownloadState, row.DownloadMessage = ptr(DownloadFailed), ptr("Unpack failed")
	h.db.reqs[r.ID] = row
	h.db.mu.Unlock()
	// History was cleared; Radarr is monitoring but grabbed nothing new.
	h.radarr.set(func(a *statusArr) { a.items[7] = `{"id":7,"monitored":true}` })

	h.sync(t)
	got := h.db.get(r.ID)
	if got.Status != StatusFailed || state(got) != DownloadFailed || deref(got.DownloadMessage) != "Unpack failed" {
		t.Errorf("row %s/%s %q; with no new grab the failure must stand", got.Status, state(got), deref(got.DownloadMessage))
	}
	if !got.DownloadUpdatedAt.Valid {
		t.Error("the row should still be marked as checked")
	}
}

func TestSync_RecoverySkippedWhenSuperseded(t *testing.T) {
	h := newSyncHarness(t)
	old := h.request(TypeMovie, 949, StatusFailed, 7)
	// The user re-requested the title after it failed.
	h.request(TypeMovie, 949, StatusPending, 0)
	h.radarr.set(func(a *statusArr) {
		a.queue = []string{`{"id":5,"movieId":7,"size":100,"sizeleft":90,"status":"downloading"}`}
	})
	res := h.sync(t)
	if got := h.db.get(old.ID); got.Status != StatusFailed || state(got) != DownloadFailed {
		t.Errorf("superseded row %s/%s, want to stay failed", got.Status, state(got))
	}
	if res.Recovered != 0 {
		t.Errorf("Recovered = %d", res.Recovered)
	}
}

func TestSync_ShowAggregatesEpisodes(t *testing.T) {
	h := newSyncHarness(t)
	r := h.request(TypeShow, 95396, StatusDownloading, 3)
	h.sonarr.set(func(a *statusArr) {
		a.queue = []string{
			`{"id":1,"seriesId":3,"episodeId":31,"downloadId":"P","size":4000,"sizeleft":1000,"status":"downloading","episode":{"seasonNumber":1,"episodeNumber":1}}`,
			`{"id":2,"seriesId":3,"episodeId":32,"downloadId":"P","size":4000,"sizeleft":1000,"status":"downloading","episode":{"seasonNumber":1,"episodeNumber":2}}`,
			`{"id":3,"seriesId":3,"episodeId":33,"downloadId":"Q","size":1000,"sizeleft":1000,"status":"queued","episode":{"seasonNumber":1,"episodeNumber":3}}`,
			`{"id":4,"seriesId":99,"episodeId":990,"downloadId":"Z","size":1,"sizeleft":1,"status":"downloading"}`,
		}
	})
	h.sync(t)
	got := h.db.get(r.ID)
	if state(got) != DownloadDownloading || got.DownloadProgress == nil || *got.DownloadProgress != 0.6 || *got.DownloadSizeBytes != 5000 {
		t.Errorf("row state %s progress %v size %v, want downloading 0.6 of 5000", state(got), got.DownloadProgress, got.DownloadSizeBytes)
	}
}

func TestSync_ShowLinkedByTVDB(t *testing.T) {
	h := newSyncHarness(t)
	r := h.request(TypeShow, 95396, StatusDownloading, 0)
	h.sonarr.set(func(a *statusArr) {
		a.byTVDB[371980] = 3 // fakeTMDB reports tvdb 371980 for any show
		a.items[3] = `{"id":3,"monitored":true,"statistics":{"episodeFileCount":9,"episodeCount":9}}`
	})
	h.sync(t)
	got := h.db.get(r.ID)
	if got.ArrItemID == nil || *got.ArrItemID != 3 || state(got) != DownloadImportPending {
		t.Errorf("arr id %v state %s", got.ArrItemID, state(got))
	}
	// All episodes on disk: no history lookup needed.
	if h.sonarr.count("/api/v3/history/series") != 0 {
		t.Error("history fetched for a fully downloaded series")
	}
}

func TestSync_BadServiceDoesNotSinkTheRun(t *testing.T) {
	h := newSyncHarness(t)
	movie := h.request(TypeMovie, 949, StatusDownloading, 7)
	show := h.request(TypeShow, 1, StatusDownloading, 3)
	h.radarr.set(func(a *statusArr) { a.failPaths["/api/v3/queue"] = http.StatusInternalServerError })
	h.sonarr.set(func(a *statusArr) {
		a.queue = []string{`{"id":1,"seriesId":3,"size":10,"sizeleft":5,"status":"downloading"}`}
	})
	res := h.sync(t)
	if res.ServiceErrors != 1 || res.Services != 2 {
		t.Errorf("result = %+v", res)
	}
	if got := h.db.get(movie.ID); got.DownloadState != nil {
		t.Errorf("unreachable instance's request was touched: %s", state(got))
	}
	if got := h.db.get(show.ID); state(got) != DownloadDownloading {
		t.Errorf("healthy instance's request = %s", state(got))
	}
}

func TestSync_SkipsDisabledService(t *testing.T) {
	h := newSyncHarness(t)
	h.radarrSvc.Enabled = false
	h.db.addService(h.radarrSvc)
	r := h.request(TypeMovie, 949, StatusDownloading, 7)
	res := h.sync(t)
	if h.radarr.totalCalls() != 0 {
		t.Errorf("disabled instance was contacted: %v", h.radarr.calls)
	}
	if res.Skipped != 1 || h.db.get(r.ID).DownloadState != nil {
		t.Errorf("result %+v", res)
	}
}

func TestSync_LookupBudgetBoundsCalls(t *testing.T) {
	h := newSyncHarness(t)
	n := syncLookupBudget + 20
	h.radarr.set(func(a *statusArr) {
		for i := 1; i <= n; i++ {
			a.items[i] = fmt.Sprintf(`{"id":%d,"monitored":true,"hasFile":true}`, i)
		}
	})
	for i := 1; i <= n; i++ {
		h.request(TypeMovie, int32(1000+i), StatusDownloading, int32(i))
	}
	res := h.sync(t)
	if calls := h.radarr.totalCalls() - h.radarr.count("/api/v3/queue"); calls != syncLookupBudget {
		t.Errorf("per-request calls = %d, want the budget (%d)", calls, syncLookupBudget)
	}
	if res.Updated != syncLookupBudget || res.Skipped != n-syncLookupBudget {
		t.Errorf("result = %+v", res)
	}
	// The next run starts with the skipped (never-checked) rows.
	h.now = h.now.Add(time.Minute)
	h.sync(t)
	h.db.mu.Lock()
	unchecked := 0
	for _, r := range h.db.reqs {
		if !r.DownloadUpdatedAt.Valid {
			unchecked++
		}
	}
	h.db.mu.Unlock()
	if unchecked != 0 {
		t.Errorf("%d rows still never checked after two runs", unchecked)
	}
}

func TestSync_FilterTargetsOneTitle(t *testing.T) {
	h := newSyncHarness(t)
	target := h.request(TypeMovie, 949, StatusDownloading, 7)
	other := h.request(TypeMovie, 603, StatusDownloading, 8)
	show := h.request(TypeShow, 949, StatusDownloading, 3)
	h.radarr.set(func(a *statusArr) {
		a.queue = []string{
			`{"id":1,"movieId":7,"size":10,"sizeleft":5,"status":"downloading"}`,
			`{"id":2,"movieId":8,"size":10,"sizeleft":5,"status":"downloading"}`,
		}
	})
	res, err := h.svc.SyncDownloads(context.Background(), SyncFilter{Kind: "radarr", TMDBID: 949})
	if err != nil {
		t.Fatal(err)
	}
	if res.Requests != 1 || h.db.get(target.ID).DownloadState == nil {
		t.Errorf("target not synced: %+v", res)
	}
	if h.db.get(other.ID).DownloadState != nil || h.db.get(show.ID).DownloadState != nil {
		t.Error("filter leaked to other requests")
	}
	if h.sonarr.totalCalls() != 0 {
		t.Error("sonarr contacted for a radarr-targeted sync")
	}
	// By arr id too.
	if res, _ := h.svc.SyncDownloads(context.Background(), SyncFilter{Kind: "radarr", ArrItemID: 8}); res.Requests != 1 {
		t.Errorf("arr-id filter matched %d", res.Requests)
	}
}

func TestSyncFilter_IncludesUnlinked(t *testing.T) {
	f := SyncFilter{Kind: "sonarr", ArrItemID: 3}
	unlinked := gen.MediaRequest{Type: TypeShow, TmdbID: 1}
	if !f.match(unlinked) {
		t.Error("an unlinked show request should be included so the sync can link it")
	}
	if f.match(gen.MediaRequest{Type: TypeMovie}) {
		t.Error("kind filter ignored")
	}
	four := int32(4)
	if f.match(gen.MediaRequest{Type: TypeShow, ArrItemID: &four}) {
		t.Error("a request linked to another series matched")
	}
}

func TestTriggerDownloadSync_Coalesces(t *testing.T) {
	h := newSyncHarness(t)
	h.request(TypeMovie, 949, StatusDownloading, 7)
	h.radarr.set(func(a *statusArr) {
		a.queue = []string{`{"id":1,"movieId":7,"size":10,"sizeleft":5,"status":"downloading"}`}
	})
	// Hold the run lock as a running scheduled sync would.
	h.svc.dlSync.run.Lock()
	for i := 0; i < 5; i++ {
		h.svc.TriggerDownloadSync("radarr", 949, 7)
	}
	h.svc.dlSync.run.Unlock()

	deadline := time.Now().Add(5 * time.Second)
	for h.radarr.queueHits.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	// Taking the run lock waits out the triggered run.
	h.svc.dlSync.run.Lock()
	got := h.radarr.queueHits.Load()
	h.svc.dlSync.run.Unlock()
	if got != 1 {
		t.Errorf("queue fetched %d times for 5 coalesced triggers, want 1", got)
	}

	// Once that run started, a new trigger runs again.
	h.svc.TriggerDownloadSync("radarr", 949, 7)
	deadline = time.Now().Add(5 * time.Second)
	for h.radarr.queueHits.Load() < 2 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if got := h.radarr.queueHits.Load(); got != 2 {
		t.Errorf("queue fetched %d times, want 2", got)
	}
}

func TestApprove_StoresArrItemID(t *testing.T) {
	h := newHarness(t)
	uid := h.user(gen.GetUserRequestPermissionsRow{IsAdmin: true})
	got, err := h.svc.Create(context.Background(), CreateInput{UserID: uid, Type: TypeMovie, TMDBID: 949})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	// fakeArr answers every add with {"id": 1}.
	if got.ArrItemID == nil || *got.ArrItemID != 1 {
		t.Errorf("returned arr_item_id = %v, want 1", got.ArrItemID)
	}
	if row := h.db.get(got.ID); row.ArrItemID == nil || *row.ArrItemID != 1 {
		t.Errorf("stored arr_item_id = %v, want 1", row.ArrItemID)
	}
}

func TestUsernames(t *testing.T) {
	h := newSyncHarness(t)
	bob := uuid.New()
	h.db.perms[bob] = gen.GetUserRequestPermissionsRow{Username: "bob"}
	rows := []gen.MediaRequest{{UserID: h.requester}, {UserID: bob}, {UserID: h.requester}, {UserID: uuid.New()}}
	got := h.svc.Usernames(context.Background(), rows)
	if len(got) != 2 || got[h.requester] != "alice" || got[bob] != "bob" {
		t.Errorf("got %v", got)
	}
	if len(h.svc.Usernames(context.Background(), nil)) != 0 {
		t.Error("nil rows")
	}
}

// ── failed queue rows vs. newer evidence ────────────────────────────────────

// failedQueueRow is a download Radarr/Sonarr handled as failed; download
// clients keep such items listed until someone clears them.
const failedMovieQueueRow = `{"id":5,"movieId":7,"downloadId":"A","size":100,"sizeleft":100,
	"status":"failed","trackedDownloadState":"failed","errorMessage":"Unpack failed"}`

// The reported regression: a failed download lingers in the queue after the
// title was grabbed again and imported. The request must not flip to failed
// (and nobody may be told it did).
func TestSync_LingeringFailedQueueRowAfterImport(t *testing.T) {
	h := newSyncHarness(t)
	r := h.request(TypeMovie, 949, StatusDownloading, 7)
	h.radarr.set(func(a *statusArr) {
		a.queue = []string{failedMovieQueueRow}
		a.items[7] = `{"id":7,"tmdbId":949,"monitored":true,"hasFile":true}`
		a.history[7] = `[{"id":1,"eventType":"grabbed","downloadId":"A","date":"2026-09-28T08:00:00Z"},
			{"id":2,"eventType":"downloadFailed","downloadId":"A","date":"2026-09-28T09:00:00Z"},
			{"id":3,"eventType":"grabbed","downloadId":"B","date":"2026-09-28T10:00:00Z"},
			{"id":4,"eventType":"downloadFolderImported","downloadId":"B","date":"2026-09-28T11:00:00Z"}]`
	})
	res := h.sync(t)
	got := h.db.get(r.ID)
	if got.Status != StatusDownloading || state(got) != DownloadImportPending {
		t.Errorf("row = %s / %s, want downloading / import_pending (the file is on disk)", got.Status, state(got))
	}
	if res.Failed != 0 {
		t.Errorf("result.Failed = %d, want 0", res.Failed)
	}
	if u, a := h.failNotices(); u+a != 0 {
		t.Errorf("failure notices user=%d admin=%d, want none", u, a)
	}
}

func TestSync_FailedQueueRowsJudgedAgainstHistory(t *testing.T) {
	cases := []struct {
		name       string
		hasFile    bool
		history    string
		wantStatus string
		wantState  string
		wantNotice bool
	}{
		{"import after the failure", false, `[
			{"id":1,"eventType":"grabbed","downloadId":"A","date":"2026-09-28T08:00:00Z"},
			{"id":2,"eventType":"downloadFailed","downloadId":"A","date":"2026-09-28T09:00:00Z"},
			{"id":4,"eventType":"downloadFolderImported","downloadId":"B","date":"2026-09-28T11:00:00Z"}]`,
			StatusDownloading, DownloadSearching, false},
		{"another release grabbed since", false, `[
			{"id":1,"eventType":"grabbed","downloadId":"A","date":"2026-09-28T08:00:00Z"},
			{"id":2,"eventType":"downloadFailed","downloadId":"A","date":"2026-09-28T09:00:00Z"},
			{"id":3,"eventType":"grabbed","downloadId":"B","date":"2026-09-28T10:00:00Z"}]`,
			StatusDownloading, DownloadSearching, false},
		{"nothing newer than the failure", false, `[
			{"id":1,"eventType":"grabbed","downloadId":"A","date":"2026-09-28T08:00:00Z"},
			{"id":2,"eventType":"downloadFailed","downloadId":"A","date":"2026-09-28T09:00:00Z"}]`,
			StatusFailed, DownloadFailed, true},
		{"only the failed download's own grab", false, `[
			{"id":1,"eventType":"grabbed","downloadId":"A","date":"2026-09-28T08:00:00Z"}]`,
			StatusFailed, DownloadFailed, true},
		{"history pruned", false, `[]`, StatusFailed, DownloadFailed, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := newSyncHarness(t)
			r := h.request(TypeMovie, 949, StatusDownloading, 7)
			h.radarr.set(func(a *statusArr) {
				a.queue = []string{failedMovieQueueRow}
				a.items[7] = fmt.Sprintf(`{"id":7,"tmdbId":949,"monitored":true,"hasFile":%v,"isAvailable":true}`, c.hasFile)
				a.history[7] = c.history
			})
			h.sync(t)
			got := h.db.get(r.ID)
			if got.Status != c.wantStatus || state(got) != c.wantState {
				t.Errorf("row = %s / %s, want %s / %s", got.Status, state(got), c.wantStatus, c.wantState)
			}
			if c.wantState == DownloadFailed && deref(got.DownloadMessage) != "Unpack failed" {
				t.Errorf("message = %q, want the queue's failure reason", deref(got.DownloadMessage))
			}
			wantU, wantA := 0, 0
			if c.wantNotice {
				wantU, wantA = 1, 1
			}
			if u, a := h.failNotices(); u != wantU || a != wantA {
				t.Errorf("failure notices user=%d admin=%d, want %d/%d", u, a, wantU, wantA)
			}
		})
	}
}

// A request already marked failed whose lingering failed queue row is joined
// by an import recovers instead of staying failed.
func TestSync_FailedRequestRecoversPastLingeringFailedRow(t *testing.T) {
	h := newSyncHarness(t)
	r := h.request(TypeMovie, 949, StatusFailed, 7)
	h.radarr.set(func(a *statusArr) {
		a.queue = []string{failedMovieQueueRow}
		a.items[7] = `{"id":7,"tmdbId":949,"monitored":true,"hasFile":true}`
	})
	res := h.sync(t)
	if got := h.db.get(r.ID); got.Status != StatusDownloading || state(got) != DownloadImportPending || res.Recovered != 1 {
		t.Errorf("row = %s / %s recovered=%d, want downloading / import_pending, recovered", got.Status, state(got), res.Recovered)
	}
}

// The TV variant: a season request whose season pack failed and lingers in
// the queue, while the season has since been downloaded — or grabbed again.
func TestSync_ShowLingeringFailedSeasonPack(t *testing.T) {
	failedPack := []string{
		`{"id":1,"seriesId":42,"episodeId":11,"downloadId":"A","size":900,"sizeleft":900,"status":"failed",
		  "trackedDownloadState":"failed","errorMessage":"Unpack failed","episode":{"seasonNumber":1,"episodeNumber":1}}`,
		`{"id":2,"seriesId":42,"episodeId":12,"downloadId":"A","size":900,"sizeleft":900,"status":"failed",
		  "trackedDownloadState":"failed","errorMessage":"Unpack failed","episode":{"seasonNumber":1,"episodeNumber":2}}`,
	}
	cases := []struct {
		name       string
		stats      map[int][2]int
		history    string
		wantStatus string
		wantState  string
	}{
		{"season complete on disk", map[int][2]int{1: {10, 10}}, `[]`, StatusDownloading, DownloadImportPending},
		{"season imported since, partly", map[int][2]int{1: {6, 10}}, `[
			{"id":1,"eventType":"grabbed","downloadId":"A","date":"2026-09-28T08:00:00Z"},
			{"id":2,"eventType":"downloadFailed","downloadId":"A","date":"2026-09-28T09:00:00Z"},
			{"id":3,"eventType":"downloadFolderImported","downloadId":"B","date":"2026-09-28T11:00:00Z"}]`,
			StatusDownloading, DownloadSearching},
		{"nothing newer than the failure", map[int][2]int{1: {0, 10}}, `[
			{"id":1,"eventType":"grabbed","downloadId":"A","date":"2026-09-28T08:00:00Z"},
			{"id":2,"eventType":"downloadFailed","downloadId":"A","date":"2026-09-28T09:00:00Z"}]`,
			StatusFailed, DownloadFailed},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := newSyncHarness(t)
			r := h.request(TypeShow, 1399, StatusDownloading, 42)
			r.Seasons = []byte(`[1]`)
			h.db.reqs[r.ID] = r
			h.sonarr.set(func(a *statusArr) {
				a.queue = failedPack
				a.items[42] = seriesJSON(c.stats)
				a.history[42] = c.history
			})
			res := h.sync(t)
			got := h.db.get(r.ID)
			if got.Status != c.wantStatus || state(got) != c.wantState {
				t.Errorf("row = %s / %s, want %s / %s", got.Status, state(got), c.wantStatus, c.wantState)
			}
			wantFailed := 0
			if c.wantStatus == StatusFailed {
				wantFailed = 1
			}
			if u, a := h.failNotices(); res.Failed != wantFailed || u != wantFailed || a != wantFailed {
				t.Errorf("Failed=%d notices user=%d admin=%d, want %d each", res.Failed, u, a, wantFailed)
			}
		})
	}
}

func TestSyncResult_String(t *testing.T) {
	s := SyncResult{Requests: 3, Services: 1, Updated: 2, Failed: 1}.String()
	if !strings.Contains(s, "3 requests") || !strings.Contains(s, "1 failed") {
		t.Errorf("summary %q", s)
	}
}
