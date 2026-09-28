package arr

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHealth_DecodesAndNormalises(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v3/health" {
			t.Errorf("path = %q", r.URL.Path)
		}
		_, _ = io.WriteString(w, `[
			{"source":"IndexerStatusCheck","type":"warning","message":"Indexers unavailable","wikiUrl":"https://wiki.servarr.com/radarr/system#indexers"},
			{"source":"DownloadClientCheck","type":"Error","message":"Unable to communicate with qBittorrent","wikiUrl":{"fullUri":"https://wiki.servarr.com/x"}},
			{"source":"UpdateCheck","type":"notice","message":"New update available","wikiUrl":null},
			{"source":"Resolved","type":"ok","message":""},
			{"source":"Odd","message":"no type given"},
			{"source":"Empty"}
		]`)
	}))
	defer srv.Close()

	got, err := newTestClient(srv, "k").Health(context.Background())
	if err != nil {
		t.Fatalf("Health: %v", err)
	}
	if len(got) != 4 {
		t.Fatalf("got %d checks (%+v), want 4 (ok and empty rows dropped)", len(got), got)
	}
	if got[0].Type != "warning" || got[0].WikiURL != "https://wiki.servarr.com/radarr/system#indexers" {
		t.Errorf("check 0 = %+v", got[0])
	}
	if got[1].Type != "error" || got[1].WikiURL != "https://wiki.servarr.com/x" {
		t.Errorf("check 1 = %+v (type lower-cased, object wikiUrl unwrapped)", got[1])
	}
	if got[2].Type != "notice" || got[2].WikiURL != "" {
		t.Errorf("check 2 = %+v", got[2])
	}
	if got[3].Type != "warning" {
		t.Errorf("a check with a message but no type should read as a warning, got %+v", got[3])
	}
}

func TestDiskSpace_Decodes(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v3/diskspace" {
			t.Errorf("path = %q", r.URL.Path)
		}
		_, _ = io.WriteString(w, `[
			{"path":"/movies","label":"media","freeSpace":1000,"totalSpace":20000},
			{"path":"/","label":"","freeSpace":"5","totalSpace":null},
			{"label":"no path"}
		]`)
	}))
	defer srv.Close()

	got, err := newTestClient(srv, "k").DiskSpace(context.Background())
	if err != nil {
		t.Fatalf("DiskSpace: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %+v, want the two rows with a path", got)
	}
	if got[0].Path != "/movies" || got[0].FreeSpace != 1000 || got[0].TotalSpace != 20000 {
		t.Errorf("disk 0 = %+v", got[0])
	}
	if got[1].FreeSpace != 5 || got[1].TotalSpace != 0 {
		t.Errorf("disk 1 = %+v", got[1])
	}
}

func TestHealth_ErrorPropagates(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	if _, err := newTestClient(srv, "k").Health(context.Background()); err == nil {
		t.Error("want error on 500")
	}
	if _, err := newTestClient(srv, "k").DiskSpace(context.Background()); err == nil {
		t.Error("want error on 500")
	}
}
