package v1

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/onscreen/onscreen/internal/db/gen"
	"github.com/onscreen/onscreen/internal/domain/media"
)

// fakeOffsetStore is SubtitleOffsetStore + SubtitleStreamOffsetReader over
// maps.
type fakeOffsetStore struct {
	external map[uuid.UUID]gen.ExternalSubtitle
	streams  map[string]int32 // fileID/index
	deleted  int
}

func streamKey(fileID uuid.UUID, idx int32) string { return fmt.Sprintf("%s/%d", fileID, idx) }

func (f *fakeOffsetStore) SetExternalSubtitleOffset(_ context.Context, p gen.SetExternalSubtitleOffsetParams) (gen.ExternalSubtitle, error) {
	row, ok := f.external[p.ID]
	if !ok {
		return gen.ExternalSubtitle{}, pgx.ErrNoRows
	}
	row.OffsetMs = p.OffsetMs
	f.external[p.ID] = row
	return row, nil
}

func (f *fakeOffsetStore) UpsertSubtitleStreamOffset(_ context.Context, p gen.UpsertSubtitleStreamOffsetParams) error {
	if f.streams == nil {
		f.streams = map[string]int32{}
	}
	f.streams[streamKey(p.FileID, p.StreamIndex)] = p.OffsetMs
	return nil
}

func (f *fakeOffsetStore) DeleteSubtitleStreamOffset(_ context.Context, p gen.DeleteSubtitleStreamOffsetParams) error {
	f.deleted++
	delete(f.streams, streamKey(p.FileID, p.StreamIndex))
	return nil
}

func (f *fakeOffsetStore) GetSubtitleStreamOffset(_ context.Context, p gen.GetSubtitleStreamOffsetParams) (int32, error) {
	return f.streams[streamKey(p.FileID, p.StreamIndex)], nil
}

func (f *fakeOffsetStore) ListSubtitleStreamOffsets(_ context.Context, ids []uuid.UUID) ([]gen.ListSubtitleStreamOffsetsRow, error) {
	var out []gen.ListSubtitleStreamOffsetsRow
	for _, id := range ids {
		for i := int32(0); i < 10; i++ {
			if off, ok := f.streams[streamKey(id, i)]; ok {
				out = append(out, gen.ListSubtitleStreamOffsetsRow{FileID: id, StreamIndex: i, OffsetMs: off})
			}
		}
	}
	return out, nil
}

func TestSubtitles_Serve_AppliesOffsetAndRevalidates(t *testing.T) {
	path := filepath.Join(t.TempDir(), "subs.vtt")
	if err := os.WriteFile(path, []byte("WEBVTT\n\n00:00:01.000 --> 00:00:02.000\nHello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	subID, fileID, itemID := uuid.New(), uuid.New(), uuid.New()
	svc := &mockSubtitleService{rows: map[uuid.UUID]gen.ExternalSubtitle{
		subID: {ID: subID, FileID: fileID, StoragePath: path, OffsetMs: 1500},
	}}
	mm := &mockSubsMedia{
		items: map[uuid.UUID]*media.Item{itemID: {ID: itemID, LibraryID: uuid.New()}},
		files: map[uuid.UUID][]media.File{itemID: {{ID: fileID, MediaItemID: itemID, Status: "active"}}},
	}
	h := NewSubtitleHandler(svc, mm, slog.Default())

	rec := httptest.NewRecorder()
	h.Serve(rec, subReq(http.MethodGet, "/x", nil, uuid.Nil, map[string]string{"subId": subID.String()}))
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	if want := "WEBVTT\n\n00:00:02.500 --> 00:00:03.500\nHello\n"; rec.Body.String() != want {
		t.Errorf("body = %q, want %q", rec.Body.String(), want)
	}
	if cc := rec.Header().Get("Cache-Control"); !strings.Contains(cc, "no-cache") || !strings.Contains(cc, "private") {
		t.Errorf("Cache-Control = %q; an offset change must reach clients on their next load", cc)
	}
	etag := rec.Header().Get("ETag")
	if etag == "" {
		t.Fatal("no ETag")
	}

	// Same offset: 304. Changed offset: a new body and ETag.
	req := subReq(http.MethodGet, "/x", nil, uuid.Nil, map[string]string{"subId": subID.String()})
	req.Header.Set("If-None-Match", etag)
	rec = httptest.NewRecorder()
	h.Serve(rec, req)
	if rec.Code != http.StatusNotModified {
		t.Errorf("revalidation: %d, want 304", rec.Code)
	}
	row := svc.rows[subID]
	row.OffsetMs = -500
	svc.rows[subID] = row
	rec = httptest.NewRecorder()
	h.Serve(rec, req)
	if rec.Code != http.StatusOK || rec.Header().Get("ETag") == etag {
		t.Errorf("after an offset change: %d etag %q (old %q)", rec.Code, rec.Header().Get("ETag"), etag)
	}
	if !strings.Contains(rec.Body.String(), "00:00:00.500 --> 00:00:01.500") {
		t.Errorf("body = %q", rec.Body.String())
	}
}

func putOffset(t *testing.T, fn http.HandlerFunc, body string, admin bool, params map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	req := subReq(http.MethodPut, "/x", []byte(body), uuid.New(), params)
	if admin {
		req = asSubAdmin(req)
	}
	rec := httptest.NewRecorder()
	fn(rec, req)
	return rec
}

func TestSubtitles_SetOffset(t *testing.T) {
	subID := uuid.New()
	row := gen.ExternalSubtitle{ID: subID, FileID: uuid.New(), Language: "en"}
	svc := &mockSubtitleService{rows: map[uuid.UUID]gen.ExternalSubtitle{subID: row}}
	store := &fakeOffsetStore{external: map[uuid.UUID]gen.ExternalSubtitle{subID: row}}
	h := NewSubtitleHandler(svc, &mockSubsMedia{}, slog.Default()).WithOffsets(store)
	params := map[string]string{"id": subID.String()}

	if rec := putOffset(t, h.SetOffset, `{"offset_ms": 100}`, false, params); rec.Code != http.StatusForbidden {
		t.Errorf("non-admin: %d", rec.Code)
	}
	for _, bad := range []string{`{}`, `{"offset_ms": "1"}`, `{"offset_ms": 1, "x": 2}`} {
		if rec := putOffset(t, h.SetOffset, bad, true, params); rec.Code != http.StatusBadRequest {
			t.Errorf("%s: %d, want 400", bad, rec.Code)
		}
	}
	if rec := putOffset(t, h.SetOffset, `{"offset_ms": 600001}`, true, params); rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("out of range: %d, want 422", rec.Code)
	}
	if rec := putOffset(t, h.SetOffset, `{"offset_ms": 5}`, true, map[string]string{"id": uuid.NewString()}); rec.Code != http.StatusNotFound {
		t.Errorf("unknown subtitle: %d", rec.Code)
	}

	rec := putOffset(t, h.SetOffset, `{"offset_ms": -1200}`, true, params)
	if rec.Code != http.StatusOK {
		t.Fatalf("set: %d %s", rec.Code, rec.Body)
	}
	var env struct {
		Data ExternalSubtitleJSON `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatal(err)
	}
	if env.Data.OffsetMS != -1200 || store.external[subID].OffsetMs != -1200 {
		t.Errorf("response %d stored %d", env.Data.OffsetMS, store.external[subID].OffsetMs)
	}
}

func TestSubtitles_SetOffset_NotWiredIs404(t *testing.T) {
	h := NewSubtitleHandler(&mockSubtitleService{}, &mockSubsMedia{}, slog.Default())
	if rec := putOffset(t, h.SetOffset, `{"offset_ms": 1}`, true, map[string]string{"id": uuid.NewString()}); rec.Code != http.StatusNotFound {
		t.Errorf("got %d", rec.Code)
	}
}

func TestSubtitles_SetStreamOffset(t *testing.T) {
	itemID, fileID := uuid.New(), uuid.New()
	mm := &mockSubsMedia{
		items: map[uuid.UUID]*media.Item{itemID: {ID: itemID}},
		files: map[uuid.UUID][]media.File{itemID: {{
			ID: fileID, MediaItemID: itemID, Status: "active",
			SubtitleStreams: []byte(`[{"index":2,"codec":"subrip"},{"index":3,"codec":"hdmv_pgs_subtitle"}]`),
		}}},
	}
	store := &fakeOffsetStore{}
	h := NewSubtitleHandler(&mockSubtitleService{}, mm, slog.Default()).WithOffsets(store)
	p := func(idx string) map[string]string { return map[string]string{"file_id": fileID.String(), "index": idx} }

	if rec := putOffset(t, h.SetStreamOffset, `{"offset_ms": 1}`, false, p("2")); rec.Code != http.StatusForbidden {
		t.Errorf("non-admin: %d", rec.Code)
	}
	if rec := putOffset(t, h.SetStreamOffset, `{"offset_ms": 1}`, true, p("7")); rec.Code != http.StatusNotFound {
		t.Errorf("unknown stream: %d", rec.Code)
	}
	if rec := putOffset(t, h.SetStreamOffset, `{"offset_ms": 1}`, true, p("3")); rec.Code != http.StatusUnsupportedMediaType {
		t.Errorf("image stream: %d", rec.Code)
	}
	if rec := putOffset(t, h.SetStreamOffset, `{"offset_ms": 1}`, true, p("-1")); rec.Code != http.StatusBadRequest {
		t.Errorf("negative index: %d", rec.Code)
	}
	if rec := putOffset(t, h.SetStreamOffset, `{"offset_ms": 2500}`, true, p("2")); rec.Code != http.StatusOK {
		t.Fatalf("set: %d %s", rec.Code, rec.Body)
	}
	if store.streams[streamKey(fileID, 2)] != 2500 {
		t.Errorf("stored = %v", store.streams)
	}
	// Zero deletes the row (the table is sparse).
	if rec := putOffset(t, h.SetStreamOffset, `{"offset_ms": 0}`, true, p("2")); rec.Code != http.StatusOK {
		t.Fatalf("reset: %d", rec.Code)
	}
	if store.deleted != 1 || len(store.streams) != 0 {
		t.Errorf("reset left %v (deleted %d)", store.streams, store.deleted)
	}
}

// TestServeSubtitle_AppliesStreamOffset: the cached extraction stays as
// extracted; the served cues carry the stream's offset.
func TestServeSubtitle_AppliesStreamOffset(t *testing.T) {
	cacheDir := t.TempDir()
	fileID, itemID := uuid.New(), uuid.New()
	ms := &mockItemMedia{
		file: &media.File{ID: fileID, Status: "active", FilePath: "/movies/x.mkv", MediaItemID: itemID,
			SubtitleStreams: []byte(`[{"index":3,"codec":"subrip"}]`)},
		item: &media.Item{ID: itemID, LibraryID: uuid.New(), Type: "movie"},
	}
	store := &fakeOffsetStore{streams: map[string]int32{streamKey(fileID, 3): 2000}}
	h := newItemHandler(ms).WithSubtitleCache(cacheDir).WithSubtitleOffsets(store)

	cachePath := filepath.Join(cacheDir, "embedded", fileID.String(), "3.vtt")
	if err := os.MkdirAll(filepath.Dir(cachePath), 0o755); err != nil {
		t.Fatal(err)
	}
	orig := "WEBVTT\n\n00:00:01.000 --> 00:00:02.000\nLine\n"
	if err := os.WriteFile(cachePath, []byte(orig), 0o644); err != nil {
		t.Fatal(err)
	}
	req := withChiParams(httptest.NewRequest("GET", "/x", nil), "fileId", fileID.String(), "streamIndex", "3")
	rec := httptest.NewRecorder()
	h.ServeSubtitle(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	if want := "WEBVTT\n\n00:00:03.000 --> 00:00:04.000\nLine\n"; rec.Body.String() != want {
		t.Errorf("body = %q, want %q", rec.Body.String(), want)
	}
	if data, _ := os.ReadFile(cachePath); string(data) != orig {
		t.Error("the cache must keep the unshifted extraction")
	}
}

func TestApplySubtitleStreamOffsets(t *testing.T) {
	fileID := uuid.New()
	h := newItemHandler(&mockItemMedia{}).WithSubtitleOffsets(&fakeOffsetStore{
		streams: map[string]int32{streamKey(fileID, 4): -300},
	})
	files := []ItemFileResponse{{
		ID:              fileID.String(),
		SubtitleStreams: []SubtitleStreamJSON{{Index: 3}, {Index: 4}},
	}}
	h.applySubtitleStreamOffsets(context.Background(), files)
	if files[0].SubtitleStreams[0].OffsetMS != 0 || files[0].SubtitleStreams[1].OffsetMS != -300 {
		t.Errorf("offsets = %+v", files[0].SubtitleStreams)
	}
}

// errOffsetReader fails every lookup.
type errOffsetReader struct{}

func (errOffsetReader) GetSubtitleStreamOffset(context.Context, gen.GetSubtitleStreamOffsetParams) (int32, error) {
	return 0, errors.New("db down")
}
func (errOffsetReader) ListSubtitleStreamOffsets(context.Context, []uuid.UUID) ([]gen.ListSubtitleStreamOffsetsRow, error) {
	return nil, errors.New("db down")
}

func TestSubtitleStreamOffset_LookupFailureServesUnshifted(t *testing.T) {
	h := newItemHandler(&mockItemMedia{}).WithSubtitleOffsets(errOffsetReader{})
	if got := h.subtitleStreamOffset(context.Background(), uuid.New(), 2); got != 0 {
		t.Errorf("got %d, want 0", got)
	}
}
