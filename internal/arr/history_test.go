package arr

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestMovieHistory_DecodesEventsAndMessages(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v3/history/movie" || r.URL.Query().Get("movieId") != "7" {
			t.Errorf("request = %s?%s", r.URL.Path, r.URL.RawQuery)
		}
		_, _ = io.WriteString(w, `[
			{"id":1,"movieId":7,"eventType":"grabbed","date":"2026-09-27T10:00:00Z","downloadId":"A","sourceTitle":"Heat.1080p"},
			{"id":2,"movieId":7,"eventType":"downloadFailed","date":"2026-09-27T11:00:00Z","downloadId":"A",
			 "data":{"message":"Download client reported failure","reason":"x"}},
			{"id":3,"movieId":7,"eventType":"DownloadFolderImported","date":"2026-09-27T12:00:00Z"},
			{"id":4,"movieId":7,"eventType":4,"date":"2026-09-27T13:00:00Z"},
			{"id":5,"movieId":7,"eventType":"somethingNew","date":null}
		]`)
	}))
	defer srv.Close()

	got, err := newTestClient(srv, "k").MovieHistory(context.Background(), 7)
	if err != nil {
		t.Fatalf("MovieHistory: %v", err)
	}
	want := []HistoryEventType{EventGrabbed, EventDownloadFailed, EventDownloadFolderImported, EventDownloadFailed, "somethingNew"}
	if len(got) != len(want) {
		t.Fatalf("got %d records, want %d", len(got), len(want))
	}
	for i, w := range want {
		if got[i].EventType != w {
			t.Errorf("record %d event = %q, want %q", i, got[i].EventType, w)
		}
	}
	if got[1].Message() != "Download client reported failure" {
		t.Errorf("message = %q", got[1].Message())
	}
	if !got[0].Date.Equal(time.Date(2026, 9, 27, 10, 0, 0, 0, time.UTC)) {
		t.Errorf("date = %v", got[0].Date)
	}
	if !got[2].EventType.IsImport() || got[0].EventType.IsImport() {
		t.Error("IsImport misclassifies")
	}
	if !got[4].Date.IsZero() {
		t.Error("null date should decode to zero")
	}
}

func TestSeriesHistory_AcceptsPagedEnvelope(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v3/history/series" || r.URL.Query().Get("seriesId") != "3" {
			t.Errorf("request = %s?%s", r.URL.Path, r.URL.RawQuery)
		}
		_, _ = io.WriteString(w, `{"totalRecords":1,"records":[{"id":9,"seriesId":3,"episodeId":30,"eventType":"seriesFolderImported"}]}`)
	}))
	defer srv.Close()

	got, err := newTestClient(srv, "k").SeriesHistory(context.Background(), 3)
	if err != nil {
		t.Fatalf("SeriesHistory: %v", err)
	}
	if len(got) != 1 || got[0].EventType != EventSeriesFolderImported || got[0].EpisodeID != 30 {
		t.Errorf("got %+v", got)
	}
}

func TestHistory_404IsErrNotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()
	if _, err := newTestClient(srv, "k").MovieHistory(context.Background(), 1); !errors.Is(err, ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}

func TestHistoryEventType_Unmarshal(t *testing.T) {
	cases := map[string]HistoryEventType{
		`"grabbed"`:        EventGrabbed,
		`"Grabbed"`:        EventGrabbed,
		`"DOWNLOADFAILED"`: EventDownloadFailed,
		`1`:                EventGrabbed,
		`3`:                EventDownloadFolderImported,
		`4`:                EventDownloadFailed,
		`99`:               "",
		`null`:             "",
		`{}`:               "",
		`"custom"`:         "custom",
	}
	for in, want := range cases {
		var e HistoryEventType
		if err := json.Unmarshal([]byte(in), &e); err != nil {
			t.Errorf("%s: error %v", in, err)
			continue
		}
		if e != want {
			t.Errorf("%s: got %q, want %q", in, e, want)
		}
	}
}
