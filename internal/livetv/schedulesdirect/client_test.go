package schedulesdirect

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

// TestHashPassword confirms we emit lowercase hex sha1 — SD's auth
// contract takes that exact form, not the raw password.
func TestHashPassword(t *testing.T) {
	got := HashPassword("password")
	want := "5baa61e4c9b93f3f0682250b6cf8331b7ee68fd8"
	if got != want {
		t.Errorf("HashPassword(password) = %q, want %q", got, want)
	}
}

// TestAuthenticate_TokenCaching verifies we hit /token once per
// 23 h window, not once per call.
func TestAuthenticate_TokenCaching(t *testing.T) {
	var tokenCalls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/token"):
			atomic.AddInt32(&tokenCalls, 1)
			_, _ = w.Write([]byte(`{"token":"abc","code":0}`))
		case strings.HasSuffix(r.URL.Path, "/lineups"):
			if r.Header.Get("Token") != "abc" {
				t.Errorf("missing Token header")
			}
			_, _ = w.Write([]byte(`{"lineups":[]}`))
		}
	}))
	defer srv.Close()

	c := NewWithHTTPClient("u", "p", srv.Client()).WithBaseURL(srv.URL)
	ctx := context.Background()
	for i := 0; i < 5; i++ {
		if _, err := c.ListLineups(ctx); err != nil {
			t.Fatalf("call %d: %v", i, err)
		}
	}
	if got := atomic.LoadInt32(&tokenCalls); got != 1 {
		t.Errorf("token calls = %d, want 1 (cached)", got)
	}
}

// Test401Retry confirms a stale-token 401 triggers exactly one
// re-authentication and retry — not an infinite loop, not a fail.
func TestAuthenticate_401RetriesOnce(t *testing.T) {
	var firstHit int32 = 1
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/token"):
			_, _ = w.Write([]byte(`{"token":"abc","code":0}`))
		case strings.HasSuffix(r.URL.Path, "/lineups"):
			// First call: 401 (stale token). Second: success.
			if atomic.CompareAndSwapInt32(&firstHit, 1, 0) {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			_, _ = w.Write([]byte(`{"lineups":[]}`))
		}
	}))
	defer srv.Close()

	c := NewWithHTTPClient("u", "p", srv.Client()).WithBaseURL(srv.URL)
	if _, err := c.ListLineups(context.Background()); err != nil {
		t.Fatalf("ListLineups should have recovered: %v", err)
	}
}

// TestListLineups_DecodesShape uses the real SD response shape.
func TestListLineups_DecodesShape(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/token") {
			_, _ = w.Write([]byte(`{"token":"x","code":0}`))
			return
		}
		_, _ = w.Write([]byte(`{
			"lineups":[
				{"lineup":"USA-OTA-90210","name":"Beverly Hills OTA","transport":"Antenna","modified":"2025-01-01T00:00:00Z"},
				{"lineup":"USA-CABLE-NYC","name":"NYC Cable","transport":"Cable","modified":"2025-01-01T00:00:00Z"}
			]
		}`))
	}))
	defer srv.Close()

	c := NewWithHTTPClient("u", "p", srv.Client()).WithBaseURL(srv.URL)
	got, err := c.ListLineups(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2", len(got))
	}
	if got[0].Lineup != "USA-OTA-90210" {
		t.Errorf("first lineup = %q", got[0].Lineup)
	}
}

// TestFetchSchedules_BatchPostBody sends a request batch and
// verifies the POST body shape SD expects.
func TestFetchSchedules_BatchPostBody(t *testing.T) {
	var seenBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/token") {
			_, _ = w.Write([]byte(`{"token":"x","code":0}`))
			return
		}
		seenBody, _ = readAllSafe(r.Body, 1<<20)
		_, _ = w.Write([]byte(`[
			{"stationID":"1234","programs":[
				{"programID":"EP000000010001","airDateTime":"2026-04-26T20:00:00Z","duration":3600,"new":true}
			],"metadata":{"modified":"2026-04-26T00:00:00Z","md5":"abc","startDate":"2026-04-26"}}
		]`))
	}))
	defer srv.Close()

	c := NewWithHTTPClient("u", "p", srv.Client()).WithBaseURL(srv.URL)
	got, err := c.FetchSchedules(context.Background(), []ScheduleRequest{
		{StationID: "1234", Date: []string{"2026-04-26"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].StationID != "1234" {
		t.Errorf("response shape: %+v", got)
	}
	if len(got[0].Programs) != 1 || !got[0].Programs[0].IsNew {
		t.Errorf("program decode: %+v", got[0].Programs)
	}
	// POST body should be a JSON array of {stationID, date}.
	var sent []ScheduleRequest
	if err := json.Unmarshal(seenBody, &sent); err != nil {
		t.Fatalf("server received non-JSON: %s", seenBody)
	}
	if len(sent) != 1 || sent[0].StationID != "1234" {
		t.Errorf("sent body: %+v", sent)
	}
}

// TestAuthenticate_CredsRejected surfaces the SD error response so
// the operator sees "invalid credentials" instead of a generic
// decode failure when they typo their password.
func TestAuthenticate_CredsRejected(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"code":4003,"message":"INVALID_USER","token":""}`))
	}))
	defer srv.Close()

	c := NewWithHTTPClient("u", "wrongpw", srv.Client()).WithBaseURL(srv.URL)
	_, err := c.ListLineups(context.Background())
	if err == nil {
		t.Fatal("expected auth failure")
	}
	if !strings.Contains(err.Error(), "INVALID_USER") {
		t.Errorf("error message lost SD response: %v", err)
	}
}

// readAllSafe reads up to max bytes from r — used in tests where the
// real io.ReadAll would lock us into reading whatever the server sent.
func readAllSafe(r interface {
	Read(p []byte) (n int, err error)
}, max int) ([]byte, error) {
	buf := make([]byte, 0, 1024)
	for {
		if len(buf) >= max {
			return buf, nil
		}
		tmp := make([]byte, 4096)
		n, err := r.Read(tmp)
		if n > 0 {
			buf = append(buf, tmp[:n]...)
		}
		if err != nil {
			if err.Error() == "EOF" {
				return buf, nil
			}
			return buf, err
		}
	}
}
