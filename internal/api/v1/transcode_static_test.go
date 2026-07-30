package v1

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/api/middleware"
	"github.com/onscreen/onscreen/internal/auth"
	"github.com/onscreen/onscreen/internal/config"
	"github.com/onscreen/onscreen/internal/domain/media"
	"github.com/onscreen/onscreen/internal/domain/watchlimit"
	"github.com/onscreen/onscreen/internal/mediastore"
	"github.com/onscreen/onscreen/internal/staticabr"
)

func TestRewritePlaylistURIs(t *testing.T) {
	in := "#EXTM3U\n#EXT-X-STREAM-INF:BANDWIDTH=800000\n720p/index.m3u8\n\n#EXTINF:6.0,\nseg0.ts\n#EXT-X-ENDLIST\n"
	out := rewritePlaylistURIs(in, func(uri string) string { return "X(" + uri + ")" })
	// URI lines rewritten; #tags and blank lines untouched.
	if !strings.Contains(out, "X(720p/index.m3u8)") || !strings.Contains(out, "X(seg0.ts)") {
		t.Errorf("URI lines not rewritten:\n%s", out)
	}
	if !strings.Contains(out, "#EXT-X-STREAM-INF:BANDWIDTH=800000") || !strings.Contains(out, "#EXT-X-ENDLIST") {
		t.Errorf("tag lines should be untouched:\n%s", out)
	}
	if strings.Contains(out, "X(#") {
		t.Errorf("a comment line was rewritten:\n%s", out)
	}
}

// memStaticStore is a map-backed Store; signedBase != "" makes SignedURL offload.
type memStaticStore struct {
	files      map[string][]byte
	signedBase string
}

func (s memStaticStore) Open(_ context.Context, key string) (io.ReadSeekCloser, error) {
	b, ok := s.files[key]
	if !ok {
		return nil, mediastore.ErrNotFound
	}
	return staticNopCloser{bytes.NewReader(b)}, nil
}
func (s memStaticStore) Stat(_ context.Context, key string) (mediastore.FileInfo, error) {
	b, ok := s.files[key]
	if !ok {
		return mediastore.FileInfo{}, mediastore.ErrNotFound
	}
	return mediastore.FileInfo{Size: int64(len(b))}, nil
}
func (s memStaticStore) SignedURL(_ context.Context, key string, _ time.Duration) (string, error) {
	if s.signedBase == "" {
		return "", nil
	}
	return s.signedBase + "/" + key, nil
}

type staticNopCloser struct{ io.ReadSeeker }

func (staticNopCloser) Close() error { return nil }

func staticTestHandler(store mediastore.Store, fileID, itemID uuid.UUID) *NativeTranscodeHandler {
	h := &NativeTranscodeHandler{
		media: &mockTranscodeMedia{
			item:  &media.Item{ID: itemID, Type: "movie", Title: "Test"},
			files: []media.File{{ID: fileID, MediaItemID: itemID, FileHash: strPtr("h1")}},
		},
		logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	h.store = store
	h.staticEnabled = true // staticRoot "" → bucket-relative keys
	return h
}

// staticReq builds a request with chi params AND authenticated claims —
// static-ABR handlers now require claims (library ACL + content-rating
// ceiling), matching the RequiredAllowQueryToken middleware in production.
func staticReq(method, target string, kv ...string) *http.Request {
	req := withChiParams(httptest.NewRequest(method, target, nil), kv...)
	return req.WithContext(middleware.WithClaims(req.Context(), &auth.Claims{UserID: uuid.New()}))
}

func TestStaticMaster_RewritesRungsAndChecksAvailability(t *testing.T) {
	fileID, itemID := uuid.New(), uuid.New()
	store := memStaticStore{files: map[string][]byte{
		staticabr.MasterKey(fileID): []byte("#EXTM3U\n#EXT-X-STREAM-INF:BANDWIDTH=800000\n720p/index.m3u8\n"),
		staticabr.HashKey(fileID):   []byte("h1"),
	}}
	h := staticTestHandler(store, fileID, itemID)

	req := staticReq(http.MethodGet, "/m?token=T", "fileID", fileID.String())
	rec := httptest.NewRecorder()
	h.StaticMaster(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	want := "/api/v1/transcode/static/" + fileID.String() + "/720p/index.m3u8?token=T"
	if !strings.Contains(rec.Body.String(), want) {
		t.Errorf("rung URI not rewritten to %q:\n%s", want, rec.Body.String())
	}
}

// TestStaticMaster_RewritesAgainstPublicSegmentBase runs the handler with a
// non-nil cfg — the production shape that the segBase() self-recursion bug
// crashed on (the other static tests leave cfg nil and so took the early-return
// branch). Asserts rung URIs are made absolute against PUBLIC_SEGMENT_BASE_URL.
func TestStaticMaster_RewritesAgainstPublicSegmentBase(t *testing.T) {
	fileID, itemID := uuid.New(), uuid.New()
	store := memStaticStore{files: map[string][]byte{
		staticabr.MasterKey(fileID): []byte("#EXTM3U\n#EXT-X-STREAM-INF:BANDWIDTH=800000\n720p/index.m3u8\n"),
		staticabr.HashKey(fileID):   []byte("h1"),
	}}
	h := staticTestHandler(store, fileID, itemID)
	h.cfg = &config.Config{PublicSegmentBaseURL: "https://segments.example.com"}

	req := staticReq(http.MethodGet, "/m?token=T", "fileID", fileID.String())
	rec := httptest.NewRecorder()
	h.StaticMaster(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	want := "https://segments.example.com/api/v1/transcode/static/" + fileID.String() + "/720p/index.m3u8?token=T"
	if !strings.Contains(rec.Body.String(), want) {
		t.Errorf("rung URI not rewritten against segment base, want %q:\n%s", want, rec.Body.String())
	}
}

func TestStaticMaster_StaleHashIs404(t *testing.T) {
	fileID, itemID := uuid.New(), uuid.New()
	store := memStaticStore{files: map[string][]byte{
		staticabr.MasterKey(fileID): []byte("#EXTM3U\n"),
		staticabr.HashKey(fileID):   []byte("OLD"), // file hash is h1 → stale
	}}
	h := staticTestHandler(store, fileID, itemID)
	req := staticReq(http.MethodGet, "/m", "fileID", fileID.String())
	rec := httptest.NewRecorder()
	h.StaticMaster(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("stale ladder: status = %d, want 404", rec.Code)
	}
}

// TestStaticMaster_RequiresClaims locks in the fail-closed behavior: with no
// authenticated claims on the context, the static handler must 401 rather than
// serve a ladder (previously it served when the ACL checker was nil).
func TestStaticMaster_RequiresClaims(t *testing.T) {
	fileID, itemID := uuid.New(), uuid.New()
	h := staticTestHandler(memStaticStore{files: map[string][]byte{}}, fileID, itemID)
	req := withChiParams(httptest.NewRequest(http.MethodGet, "/m", nil), "fileID", fileID.String())
	rec := httptest.NewRecorder()
	h.StaticMaster(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("no claims: status = %d, want 401", rec.Code)
	}
}

func TestStaticRung_SegmentsToAppEndpointWhenNoOffload(t *testing.T) {
	fileID, itemID := uuid.New(), uuid.New()
	store := memStaticStore{files: map[string][]byte{
		staticabr.RungPlaylistKey(fileID, "720p"): []byte("#EXTM3U\n#EXTINF:6.0,\nseg00000.ts\n#EXT-X-ENDLIST\n"),
	}}
	h := staticTestHandler(store, fileID, itemID)
	req := staticReq(http.MethodGet, "/r?token=T", "fileID", fileID.String(), "rung", "720p")
	rec := httptest.NewRecorder()
	h.StaticRung(rec, req)

	want := "/api/v1/transcode/static/" + fileID.String() + "/720p/seg/seg00000.ts?token=T"
	if !strings.Contains(rec.Body.String(), want) {
		t.Errorf("segment not rewritten to app endpoint %q:\n%s", want, rec.Body.String())
	}
}

func TestStaticRung_SegmentsToSignedURLWhenOffloadable(t *testing.T) {
	fileID, itemID := uuid.New(), uuid.New()
	store := memStaticStore{
		files: map[string][]byte{
			staticabr.RungPlaylistKey(fileID, "720p"): []byte("#EXTM3U\n#EXTINF:6.0,\nseg00000.ts\n#EXT-X-ENDLIST\n"),
		},
		signedBase: "https://cdn.example",
	}
	h := staticTestHandler(store, fileID, itemID)
	req := staticReq(http.MethodGet, "/r", "fileID", fileID.String(), "rung", "720p")
	rec := httptest.NewRecorder()
	h.StaticRung(rec, req)

	want := "https://cdn.example/" + staticabr.SegmentKey(fileID, "720p", "seg00000.ts")
	if !strings.Contains(rec.Body.String(), want) {
		t.Errorf("segment not rewritten to signed URL %q:\n%s", want, rec.Body.String())
	}
}

// The parental watch limit used to be checked only in StaticMaster, on the
// assumption that playback begins by fetching the master. Nothing enforces
// that: the rung and segment URLs are derivable from the fileID and are served
// by the same asset token, so a client that skips the master — or replays URLs
// from an earlier session — streamed the whole pre-encoded ladder outside its
// allowed hours. All three entry points route through staticFileAccess, which
// is where the gate now lives.
func TestStaticABR_WatchLimitGatesEveryEntryPoint(t *testing.T) {
	fileID, itemID := uuid.New(), uuid.New()
	store := memStaticStore{files: map[string][]byte{
		staticabr.MasterKey(fileID):                         []byte("#EXTM3U\n720p/index.m3u8\n"),
		staticabr.HashKey(fileID):                           []byte("h1"),
		staticabr.RungPlaylistKey(fileID, "720p"):           []byte("#EXTM3U\nseg00000.ts\n"),
		staticabr.SegmentKey(fileID, "720p", "seg00000.ts"): []byte("TSDATA"),
	}}

	entries := []struct {
		name string
		call func(*NativeTranscodeHandler, http.ResponseWriter, *http.Request)
		req  func() *http.Request
	}{
		{"master", (*NativeTranscodeHandler).StaticMaster, func() *http.Request {
			return staticReq(http.MethodGet, "/m", "fileID", fileID.String())
		}},
		{"rung", (*NativeTranscodeHandler).StaticRung, func() *http.Request {
			return staticReq(http.MethodGet, "/r", "fileID", fileID.String(), "rung", "720p")
		}},
		{"segment", (*NativeTranscodeHandler).StaticSegment, func() *http.Request {
			return staticReq(http.MethodGet, "/s", "fileID", fileID.String(),
				"rung", "720p", "name", "seg00000.ts")
		}},
	}
	for _, e := range entries {
		t.Run(e.name, func(t *testing.T) {
			h := staticTestHandler(store, fileID, itemID)
			h.WithWatchLimit(&mockItemWatchLimit{
				policy: watchlimit.Policy{DailyLimitMinutes: wlIntPtr(60)},
				used:   120 * 60, // double the cap
			})
			rec := httptest.NewRecorder()
			e.call(h, rec, e.req())

			if rec.Code != http.StatusForbidden {
				t.Errorf("%s: got %d, want 403 — this entry point streams the ladder "+
					"past the daily cap", e.name, rec.Code)
			}
		})
	}
}

// An unrestricted user must still be served on every entry point.
func TestStaticABR_UnrestrictedUserUnaffected(t *testing.T) {
	fileID, itemID := uuid.New(), uuid.New()
	store := memStaticStore{files: map[string][]byte{
		staticabr.MasterKey(fileID): []byte("#EXTM3U\n720p/index.m3u8\n"),
		staticabr.HashKey(fileID):   []byte("h1"),
	}}
	h := staticTestHandler(store, fileID, itemID)
	h.WithWatchLimit(&mockItemWatchLimit{}) // zero policy = unrestricted

	rec := httptest.NewRecorder()
	h.StaticMaster(rec, staticReq(http.MethodGet, "/m", "fileID", fileID.String()))
	if rec.Code != http.StatusOK {
		t.Errorf("unrestricted user: got %d, want 200", rec.Code)
	}
}
