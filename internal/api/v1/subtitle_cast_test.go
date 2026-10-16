package v1

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/db/gen"
	"github.com/onscreen/onscreen/internal/domain/media"
)

// A cast that starts mid-title plays an HLS session whose timeline begins at
// 0, and the receiver times a side-loaded text track on that timeline, so the
// sender asks for the cues rebased: ?start_ms= moves them that much earlier,
// on top of the track's saved offset, dropping cues that already ended.
func TestSubtitles_Serve_StartMSRebasesCues(t *testing.T) {
	path := filepath.Join(t.TempDir(), "subs.vtt")
	vtt := "WEBVTT\n\n00:00:01.000 --> 00:00:02.000\nGone\n\n00:01:05.000 --> 00:01:07.000\nKept\n"
	if err := os.WriteFile(path, []byte(vtt), 0o644); err != nil {
		t.Fatal(err)
	}
	subID, fileID, itemID := uuid.New(), uuid.New(), uuid.New()
	svc := &mockSubtitleService{rows: map[uuid.UUID]gen.ExternalSubtitle{
		subID: {ID: subID, FileID: fileID, StoragePath: path, OffsetMs: 1000},
	}}
	mm := &mockSubsMedia{
		items: map[uuid.UUID]*media.Item{itemID: {ID: itemID, LibraryID: uuid.New()}},
		files: map[uuid.UUID][]media.File{itemID: {{ID: fileID, MediaItemID: itemID, Status: "active"}}},
	}
	h := NewSubtitleHandler(svc, mm, slog.Default())

	for _, tc := range []struct {
		query, want string
	}{
		// Saved offset +1 s, rebased to 60 s: 65 s → 66 s → 6 s.
		{"?start_ms=60000", "WEBVTT\n\n00:00:06.000 --> 00:00:08.000\nKept\n"},
		// Garbage and negative values are ignored: just the saved offset.
		{"?start_ms=abc", "WEBVTT\n\n00:00:02.000 --> 00:00:03.000\nGone\n\n00:01:06.000 --> 00:01:08.000\nKept\n"},
		{"?start_ms=-5000", "WEBVTT\n\n00:00:02.000 --> 00:00:03.000\nGone\n\n00:01:06.000 --> 00:01:08.000\nKept\n"},
	} {
		t.Run(tc.query, func(t *testing.T) {
			req := subReq(http.MethodGet, "/x"+tc.query, nil, uuid.Nil, map[string]string{"subId": subID.String()})
			rec := httptest.NewRecorder()
			h.Serve(rec, req)
			if rec.Code != http.StatusOK {
				t.Fatalf("status %d", rec.Code)
			}
			if rec.Body.String() != tc.want {
				t.Errorf("body = %q, want %q", rec.Body.String(), tc.want)
			}
		})
	}
}

// /media/subtitles/{fileId}/external/{subId} serves an external subtitle
// through its file, so the file-bound stream token a cast carries can
// authenticate it — and only for that file's own subtitles.
func TestSubtitles_ServeForFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "subs.vtt")
	body := "WEBVTT\n\n00:00:01.000 --> 00:00:02.000\nHello\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	subID, fileID, otherFile, itemID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	svc := &mockSubtitleService{rows: map[uuid.UUID]gen.ExternalSubtitle{
		subID: {ID: subID, FileID: fileID, StoragePath: path},
	}}
	mm := &mockSubsMedia{
		items: map[uuid.UUID]*media.Item{itemID: {ID: itemID, LibraryID: uuid.New()}},
		files: map[uuid.UUID][]media.File{itemID: {{ID: fileID, MediaItemID: itemID, Status: "active"}}},
	}
	h := NewSubtitleHandler(svc, mm, slog.Default())

	rec := httptest.NewRecorder()
	h.ServeForFile(rec, subReq(http.MethodGet, "/x", nil, uuid.Nil,
		map[string]string{"fileId": fileID.String(), "subId": subID.String()}))
	if rec.Code != http.StatusOK || rec.Body.String() != body {
		t.Fatalf("own file: %d %q", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	h.ServeForFile(rec, subReq(http.MethodGet, "/x", nil, uuid.Nil,
		map[string]string{"fileId": otherFile.String(), "subId": subID.String()}))
	if rec.Code != http.StatusNotFound {
		t.Errorf("another file's subtitle: %d, want 404 (a token bound to one file must not reach another's)", rec.Code)
	}

	rec = httptest.NewRecorder()
	h.ServeForFile(rec, subReq(http.MethodGet, "/x", nil, uuid.Nil,
		map[string]string{"fileId": "nope", "subId": subID.String()}))
	if rec.Code != http.StatusBadRequest {
		t.Errorf("bad file id: %d, want 400", rec.Code)
	}
}

// The embedded-stream route takes ?start_ms= too.
func TestServeSubtitle_StartMSRebasesCues(t *testing.T) {
	cacheDir := t.TempDir()
	fileID, itemID := uuid.New(), uuid.New()
	ms := &mockItemMedia{
		file: &media.File{ID: fileID, Status: "active", FilePath: "/movies/x.mkv", MediaItemID: itemID,
			SubtitleStreams: []byte(`[{"index":3,"codec":"subrip"}]`)},
		item: &media.Item{ID: itemID, LibraryID: uuid.New(), Type: "movie"},
	}
	h := newItemHandler(ms).WithSubtitleCache(cacheDir)
	cachePath := filepath.Join(cacheDir, "embedded", fileID.String(), "3.vtt")
	if err := os.MkdirAll(filepath.Dir(cachePath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cachePath, []byte("WEBVTT\n\n00:10:01.000 --> 00:10:02.000\nLine\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	req := withChiParams(httptest.NewRequest("GET", "/x?start_ms=600000", nil), "fileId", fileID.String(), "streamIndex", "3")
	rec := httptest.NewRecorder()
	h.ServeSubtitle(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	if want := "WEBVTT\n\n00:00:01.000 --> 00:00:02.000\nLine\n"; rec.Body.String() != want {
		t.Errorf("body = %q, want %q", rec.Body.String(), want)
	}
}
