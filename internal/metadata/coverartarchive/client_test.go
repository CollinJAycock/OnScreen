package coverartarchive

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
)

// TestFrontCoverURL_ReleaseRedirect is the happy path: CAA 307s to the
// real image URL for a known release MBID. The client returns the
// Location header without following it.
func TestFrontCoverURL_ReleaseRedirect(t *testing.T) {
	mbid := uuid.MustParse("11111111-1111-1111-1111-111111111111")
	imageURL := "https://ia800.us.archive.org/mbid-" + mbid.String() + "/front-1000.jpg"

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/release/"+mbid.String()+"/front") {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		w.Header().Set("Location", imageURL)
		w.WriteHeader(http.StatusTemporaryRedirect)
	}))
	defer srv.Close()

	c := NewWithClient(srv.Client())
	c = withBase(c, srv.URL)

	got, err := c.FrontCoverURL(context.Background(), mbid, uuid.Nil)
	if err != nil {
		t.Fatalf("FrontCoverURL: %v", err)
	}
	if got != imageURL {
		t.Errorf("got %q, want %q", got, imageURL)
	}
}

// TestFrontCoverURL_ReleaseMissReleaseGroupHit covers the fallback path
// — the specific release has no cover (404) but the release group
// does. The client must try both and return the second one.
func TestFrontCoverURL_ReleaseMissReleaseGroupHit(t *testing.T) {
	relID := uuid.MustParse("22222222-2222-2222-2222-222222222222")
	rgID := uuid.MustParse("33333333-3333-3333-3333-333333333333")
	rgImage := "https://ia800.us.archive.org/rg-" + rgID.String() + "/front.jpg"

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "/release/"+relID.String()+"/front"):
			w.WriteHeader(http.StatusNotFound)
		case strings.Contains(r.URL.Path, "/release-group/"+rgID.String()+"/front"):
			w.Header().Set("Location", rgImage)
			w.WriteHeader(http.StatusTemporaryRedirect)
		default:
			t.Errorf("unexpected path: %s", r.URL.Path)
			w.WriteHeader(http.StatusInternalServerError)
		}
	}))
	defer srv.Close()

	c := withBase(NewWithClient(srv.Client()), srv.URL)
	got, err := c.FrontCoverURL(context.Background(), relID, rgID)
	if err != nil {
		t.Fatalf("FrontCoverURL: %v", err)
	}
	if got != rgImage {
		t.Errorf("expected fallback to release-group URL, got %q", got)
	}
}

// TestFrontCoverURL_BothMissing returns empty string with no error —
// callers need the "no cover available" signal to fall through.
func TestFrontCoverURL_BothMissing(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	c := withBase(NewWithClient(srv.Client()), srv.URL)
	got, err := c.FrontCoverURL(context.Background(), uuid.New(), uuid.New())
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if got != "" {
		t.Errorf("expected empty for double-404, got %q", got)
	}
}

// TestFrontCoverURL_NilIDs short-circuits immediately — no HTTP calls
// when both MBIDs are uuid.Nil. Guards against waking CAA for an
// album we have no MusicBrainz data for.
func TestFrontCoverURL_NilIDs(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := withBase(NewWithClient(srv.Client()), srv.URL)
	got, err := c.FrontCoverURL(context.Background(), uuid.Nil, uuid.Nil)
	if err != nil || got != "" {
		t.Errorf("nil-id case: got=%q err=%v, want empty no error", got, err)
	}
	if calls != 0 {
		t.Errorf("expected 0 HTTP calls for nil MBIDs, got %d", calls)
	}
}

// TestFrontCoverURL_DirectImage handles CAA's occasional behavior of
// serving the image body directly instead of 307-redirecting — we
// treat the request URL itself as the image URL in that case.
func TestFrontCoverURL_DirectImage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := withBase(NewWithClient(srv.Client()), srv.URL)
	got, err := c.FrontCoverURL(context.Background(), uuid.New(), uuid.Nil)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if !strings.HasSuffix(got, "/front") {
		t.Errorf("expected /front URL, got %q", got)
	}
}

// withBase swaps the package-level base URL for a test-local server.
// Exposed via an assignment helper inside tests so we don't need to
// expose the baseURL const in the public API.
func withBase(c *Client, base string) *Client {
	testBaseURL = base
	return c
}
