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
	"github.com/onscreen/onscreen/internal/domain/media"
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
