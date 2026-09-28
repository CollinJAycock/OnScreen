package arr

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestRadarrQueue_DecodesRecordsAndQuery(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v3/queue" {
			t.Errorf("path = %q", r.URL.Path)
		}
		q := r.URL.Query()
		if q.Get("page") != "1" || q.Get("pageSize") != strconv.Itoa(queuePageSize) {
			t.Errorf("paging query = %v", q)
		}
		if q.Get("includeUnknownMovieItems") != "false" {
			t.Errorf("includeUnknownMovieItems = %q", q.Get("includeUnknownMovieItems"))
		}
		_, _ = io.WriteString(w, `{"page":1,"pageSize":250,"totalRecords":2,"records":[
			{"id":11,"movieId":7,"title":"Heat.1995.1080p","size":2000.0,"sizeleft":500.0,
			 "timeleft":"00:10:00","estimatedCompletionTime":"2026-09-28T12:00:00Z",
			 "status":"downloading","trackedDownloadStatus":"ok","trackedDownloadState":"downloading",
			 "statusMessages":[],"downloadId":"ABC","protocol":"torrent","downloadClient":"qBit"},
			{"id":12,"movieId":8,"title":"Ronin","size":"1000","sizeleft":null,
			 "status":"warning","trackedDownloadStatus":"warning","trackedDownloadState":"importBlocked",
			 "statusMessages":[{"title":"Ronin.mkv","messages":["No files found are eligible for import"]}],
			 "errorMessage":"stalled"}
		]}`)
	}))
	defer srv.Close()

	q, err := newTestClient(srv, "k").RadarrQueue(context.Background())
	if err != nil {
		t.Fatalf("RadarrQueue: %v", err)
	}
	if len(q.Records) != 2 || q.TotalRecords != 2 || q.Truncated {
		t.Fatalf("queue = %+v", q)
	}
	a := q.Records[0]
	if a.MovieID != 7 || a.DownloadID != "ABC" || a.Status != "downloading" {
		t.Errorf("record 0 = %+v", a)
	}
	if p, ok := a.Progress(); !ok || p != 0.75 {
		t.Errorf("progress = %v %v, want 0.75", p, ok)
	}
	if eta := a.ETA(time.Time{}); !eta.Equal(time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)) {
		t.Errorf("eta = %v", eta)
	}
	b := q.Records[1]
	// A numeric string size decodes; a null sizeleft is 0 (fully downloaded).
	if float64(b.Size) != 1000 || float64(b.SizeLeft) != 0 {
		t.Errorf("sizes = %v / %v", b.Size, b.SizeLeft)
	}
	if got := b.Messages(); len(got) != 2 || got[0] != "stalled" || got[1] != "No files found are eligible for import" {
		t.Errorf("messages = %q", got)
	}
}

func TestSonarrQueue_AsksForEpisodes(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("includeEpisode") != "true" {
			t.Errorf("includeEpisode = %q", r.URL.Query().Get("includeEpisode"))
		}
		_, _ = io.WriteString(w, `{"totalRecords":1,"records":[
			{"id":1,"seriesId":3,"episodeId":30,"size":10,"sizeleft":10,"status":"queued",
			 "episode":{"seasonNumber":1,"episodeNumber":2,"title":"Pilot"}}]}`)
	}))
	defer srv.Close()

	q, err := newTestClient(srv, "k").SonarrQueue(context.Background())
	if err != nil {
		t.Fatalf("SonarrQueue: %v", err)
	}
	if len(q.Records) != 1 || q.Records[0].Episode == nil || q.Records[0].Episode.EpisodeNumber != 2 {
		t.Fatalf("records = %+v", q.Records)
	}
	if p, ok := q.Records[0].Progress(); !ok || p != 0 {
		t.Errorf("progress = %v %v", p, ok)
	}
}

func TestQueue_WalksPages(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		page, _ := strconv.Atoi(r.URL.Query().Get("page"))
		n := queuePageSize
		if page == 2 {
			n = 3
		}
		recs := make([]string, n)
		for i := range recs {
			recs[i] = fmt.Sprintf(`{"id":%d,"movieId":%d}`, page*1000+i, i+1)
		}
		fmt.Fprintf(w, `{"page":%d,"totalRecords":%d,"records":[%s]}`, page, queuePageSize+3, strings.Join(recs, ","))
	}))
	defer srv.Close()

	q, err := newTestClient(srv, "k").RadarrQueue(context.Background())
	if err != nil {
		t.Fatalf("RadarrQueue: %v", err)
	}
	if len(q.Records) != queuePageSize+3 || calls.Load() != 2 {
		t.Errorf("records = %d over %d calls, want %d over 2", len(q.Records), calls.Load(), queuePageSize+3)
	}
}

func TestQueue_StopsAtPageCap(t *testing.T) {
	// An instance that ignores paging (always a full page, huge total) must
	// not keep the sync looping.
	var calls atomic.Int32
	full := make([]string, queuePageSize)
	for i := range full {
		full[i] = `{"id":1,"movieId":1}`
	}
	body := `{"totalRecords":999999,"records":[` + strings.Join(full, ",") + `]}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		_, _ = io.WriteString(w, body)
	}))
	defer srv.Close()

	q, err := newTestClient(srv, "k").RadarrQueue(context.Background())
	if err != nil {
		t.Fatalf("RadarrQueue: %v", err)
	}
	if !q.Truncated || calls.Load() != queueMaxPages {
		t.Errorf("truncated=%v calls=%d, want true / %d", q.Truncated, calls.Load(), queueMaxPages)
	}
}

func TestQueue_BareArrayIsOnePage(t *testing.T) {
	// A build that returns the whole queue as a bare array must not be asked
	// for "page 2" (it would answer with the same array again).
	var calls atomic.Int32
	full := make([]string, queuePageSize+5)
	for i := range full {
		full[i] = fmt.Sprintf(`{"id":%d,"movieId":1}`, i)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		_, _ = io.WriteString(w, "["+strings.Join(full, ",")+"]")
	}))
	defer srv.Close()

	q, err := newTestClient(srv, "k").RadarrQueue(context.Background())
	if err != nil {
		t.Fatalf("RadarrQueue: %v", err)
	}
	if calls.Load() != 1 || len(q.Records) != queuePageSize+5 {
		t.Errorf("calls=%d records=%d", calls.Load(), len(q.Records))
	}
}

func TestQueue_SkipsGarbageRowsKeepsTheRest(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"totalRecords":3,"records":[
			"not an object",
			{"id":2,"movieId":"oops","size":100,"sizeleft":25,"status":"downloading"},
			{"id":3,"movieId":9,"statusMessages":"wrong shape","status":"queued"}]}`)
	}))
	defer srv.Close()

	q, err := newTestClient(srv, "k").RadarrQueue(context.Background())
	if err != nil {
		t.Fatalf("RadarrQueue: %v", err)
	}
	if len(q.Records) != 2 {
		t.Fatalf("records = %+v, want the two object rows", q.Records)
	}
	// The bad movieId zeroes that field but keeps the rest of the row.
	if q.Records[0].MovieID != 0 || q.Records[0].Status != "downloading" {
		t.Errorf("row 2 = %+v", q.Records[0])
	}
	if q.Records[1].MovieID != 9 || q.Records[1].StatusMessages != nil {
		t.Errorf("row 3 = %+v", q.Records[1])
	}
}

func TestQueue_ErrorsPropagate(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()
	if _, err := newTestClient(srv, "k").RadarrQueue(context.Background()); err != ErrUnauthorized {
		t.Errorf("err = %v, want ErrUnauthorized", err)
	}

	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `<html>proxy error</html>`)
	}))
	defer bad.Close()
	if _, err := newTestClient(bad, "k").RadarrQueue(context.Background()); err == nil {
		t.Error("a non-JSON body must be an error, not an empty queue")
	}
}

func TestQueueRecord_ETAFromTimeLeft(t *testing.T) {
	now := time.Date(2026, 9, 28, 10, 0, 0, 0, time.UTC)
	cases := []struct {
		left string
		want time.Time
	}{
		{"00:30:00", now.Add(30 * time.Minute)},
		{"1.02:00:00", now.Add(26 * time.Hour)},
		{"00:00:05.5000000", now.Add(5500 * time.Millisecond)},
		{"", time.Time{}},
		{"soon", time.Time{}},
		{"-00:01:00", time.Time{}},
	}
	for _, c := range cases {
		got := QueueRecord{TimeLeft: c.left}.ETA(now)
		if !got.Equal(c.want) {
			t.Errorf("timeleft %q: eta = %v, want %v", c.left, got, c.want)
		}
	}
}

func TestQueueRecord_ProgressEdgeCases(t *testing.T) {
	cases := []struct {
		size, left float64
		want       float64
		ok         bool
	}{
		{0, 0, 0, false},
		{-5, 0, 0, false},
		{100, 100, 0, true},
		{100, 0, 1, true},
		{100, 150, 0, true}, // sizeleft past size clamps to 0
		{100, -3, 1, true},  // negative sizeleft clamps to done
	}
	for _, c := range cases {
		p, ok := QueueRecord{Size: FlexFloat(c.size), SizeLeft: FlexFloat(c.left)}.Progress()
		if p != c.want || ok != c.ok {
			t.Errorf("size=%v left=%v: got %v %v, want %v %v", c.size, c.left, p, ok, c.want, c.ok)
		}
	}
}

func TestQueueCount_ReadsTotalRecords(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("pageSize") != "1" {
			t.Errorf("pageSize = %q", r.URL.Query().Get("pageSize"))
		}
		_, _ = io.WriteString(w, `{"totalRecords":42,"records":[{"id":1}]}`)
	}))
	defer srv.Close()
	n, err := newTestClient(srv, "k").QueueCount(context.Background())
	if err != nil || n != 42 {
		t.Errorf("QueueCount = %d, %v; want 42", n, err)
	}
}
