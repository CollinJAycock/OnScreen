package tvdb

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onscreen/onscreen/internal/metadata"
)

// TestGetEpisodeOrdering_DVDPages reads every page of a season type and
// keys episodes by the type's own season / number.
func TestGetEpisodeOrdering_DVDPages(t *testing.T) {
	var paths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			_, _ = w.Write([]byte(`{"data": {"token": "tok"}}`))
			return
		}
		paths = append(paths, r.URL.Path+"?"+r.URL.RawQuery)
		switch r.URL.Query().Get("page") {
		case "0":
			_, _ = w.Write([]byte(`{"data": {"episodes": [
			  {"seasonNumber": 1, "number": 1, "name": "Serenity", "overview": "The real pilot.", "aired": "2002-12-20", "image": "https://tvdb/s.jpg"}
			]}, "links": {"next": "https://api4.thetvdb.com/v4/series/78874/episodes/dvd?page=1"}}`))
		case "1":
			_, _ = w.Write([]byte(`{"data": {"episodes": [
			  {"seasonNumber": 1, "number": 2, "name": "The Train Job"}
			]}, "links": {"next": null}}`))
		default:
			t.Errorf("unexpected page %q", r.URL.RawQuery)
		}
	}))
	defer srv.Close()

	o, err := testClient(t, srv).GetEpisodeOrdering(context.Background(), 78874, metadata.EpisodeOrderDVD)
	if err != nil {
		t.Fatalf("GetEpisodeOrdering: %v", err)
	}
	if len(paths) != 2 || !strings.Contains(paths[0], "/series/78874/episodes/dvd") {
		t.Errorf("requests = %v, want two pages of the dvd season type", paths)
	}
	if r := o.Lookup(1, 1); r == nil || r.Title != "Serenity" || r.ThumbURL != "https://tvdb/s.jpg" || r.AirDate.Year() != 2002 {
		t.Errorf("S1E1 = %+v", r)
	}
	if r := o.Lookup(1, 2); r == nil || r.Title != "The Train Job" {
		t.Errorf("S1E2 = %+v", r)
	}
}

// TestGetEpisodeOrdering_Absolute: the absolute type's number is the
// absolute number; specials (season 0) have none.
func TestGetEpisodeOrdering_Absolute(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			_, _ = w.Write([]byte(`{"data": {"token": "tok"}}`))
			return
		}
		if !strings.HasSuffix(r.URL.Path, "/episodes/absolute") {
			t.Errorf("path = %s", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"data": {"episodes": [
		  {"seasonNumber": 1, "number": 1071, "name": "Luffy's Peak"},
		  {"seasonNumber": 0, "number": 1, "name": "Special"}
		]}, "links": {"next": null}}`))
	}))
	defer srv.Close()

	o, err := testClient(t, srv).GetEpisodeOrdering(context.Background(), 81797, metadata.EpisodeOrderAbsolute)
	if err != nil {
		t.Fatalf("GetEpisodeOrdering: %v", err)
	}
	if r := o.Lookup(1, 1071); r == nil || r.Title != "Luffy's Peak" {
		t.Errorf("absolute 1071 = %+v", r)
	}
	if r := o.Lookup(1, 1); r != nil {
		t.Errorf("absolute 1 = %+v, want nil (a special has no absolute number)", r)
	}
}

func TestGetEpisodeOrdering_Empty(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			_, _ = w.Write([]byte(`{"data": {"token": "tok"}}`))
			return
		}
		_, _ = w.Write([]byte(`{"data": {"episodes": []}, "links": {"next": null}}`))
	}))
	defer srv.Close()
	_, err := testClient(t, srv).GetEpisodeOrdering(context.Background(), 1, metadata.EpisodeOrderDVD)
	if !errors.Is(err, metadata.ErrEpisodeOrderUnavailable) {
		t.Fatalf("err = %v, want ErrEpisodeOrderUnavailable", err)
	}
	if _, err := testClient(t, srv).GetEpisodeOrdering(context.Background(), 1, "official"); !errors.Is(err, metadata.ErrEpisodeOrderUnavailable) {
		t.Fatalf("unknown order: err = %v", err)
	}
}
