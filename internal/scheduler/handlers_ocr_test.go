package scheduler

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/db/gen"
	"github.com/onscreen/onscreen/internal/domain/media"
	"github.com/onscreen/onscreen/internal/subtitles"
)

type fakeMedia struct {
	files map[uuid.UUID][]media.File
}

func (f *fakeMedia) ListActiveFilesForLibrary(_ context.Context, libID uuid.UUID) ([]media.File, error) {
	return f.files[libID], nil
}

type fakeSubs struct {
	calls []subtitles.OCROpts
	err   error
}

func (f *fakeSubs) OCRStream(_ context.Context, opts subtitles.OCROpts) (gen.ExternalSubtitle, error) {
	f.calls = append(f.calls, opts)
	if f.err != nil {
		return gen.ExternalSubtitle{}, f.err
	}
	return gen.ExternalSubtitle{ID: uuid.New(), FileID: opts.FileID}, nil
}

type fakeChecker struct {
	have map[string]bool // key = fileID|streamIdx
}

func (c *fakeChecker) HasOCR(_ context.Context, fileID uuid.UUID, streamIdx int) (bool, error) {
	key := fileID.String() + "|" + strconv.Itoa(streamIdx)
	return c.have[key], nil
}

func TestOCRHandler_SkipExisting(t *testing.T) {
	libID := uuid.New()
	fileID := uuid.New()
	pgsStreams := mustJSON([]map[string]any{
		{"index": 2, "codec": "hdmv_pgs_subtitle", "language": "en"},
		{"index": 3, "codec": "subrip", "language": "fr"}, // ignored — text-based
	})
	mediaSrc := &fakeMedia{
		files: map[uuid.UUID][]media.File{
			libID: {{ID: fileID, FilePath: "/movies/x.mkv", SubtitleStreams: pgsStreams}},
		},
	}
	subs := &fakeSubs{}
	checker := &fakeChecker{have: map[string]bool{
		fileID.String() + "|2": true, // already OCR'd → skip
	}}
	lister := LibraryListerFunc(func(_ context.Context) ([]uuid.UUID, error) {
		return []uuid.UUID{libID}, nil
	})

	h := NewOCRHandler(mediaSrc, subs, lister, checker)
	out, err := h.Run(context.Background(), json.RawMessage(`{"skip_existing": true}`))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := len(subs.calls); got != 0 {
		t.Errorf("expected 0 OCR calls (skipped), got %d", got)
	}
	if !strings.Contains(out, "skipped 1") {
		t.Errorf("summary %q missing skipped count", out)
	}
}

func TestOCRHandler_SpecificLibrary(t *testing.T) {
	libA := uuid.New()
	libB := uuid.New()
	fileA := uuid.New()
	fileB := uuid.New()
	mediaSrc := &fakeMedia{
		files: map[uuid.UUID][]media.File{
			libA: {{ID: fileA, FilePath: "/a.mkv", SubtitleStreams: mustJSON([]map[string]any{
				{"index": 2, "codec": "hdmv_pgs_subtitle", "language": "en"},
			})}},
			libB: {{ID: fileB, FilePath: "/b.mkv", SubtitleStreams: mustJSON([]map[string]any{
				{"index": 2, "codec": "hdmv_pgs_subtitle", "language": "fr"},
			})}},
		},
	}
	subs := &fakeSubs{}
	lister := LibraryListerFunc(func(_ context.Context) ([]uuid.UUID, error) {
		t.Fatal("should not enumerate libraries when library_id is set")
		return nil, nil
	})

	h := NewOCRHandler(mediaSrc, subs, lister, nil)
	cfg, _ := json.Marshal(map[string]any{"library_id": libA.String()})
	if _, err := h.Run(context.Background(), cfg); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(subs.calls) != 1 || subs.calls[0].FileID != fileA {
		t.Errorf("expected one OCR call for fileA, got %+v", subs.calls)
	}
}

func TestOCRHandler_SubtitleErrorsContinue(t *testing.T) {
	libID := uuid.New()
	mediaSrc := &fakeMedia{
		files: map[uuid.UUID][]media.File{
			libID: {
				{ID: uuid.New(), FilePath: "/a.mkv", SubtitleStreams: mustJSON([]map[string]any{
					{"index": 2, "codec": "hdmv_pgs_subtitle", "language": "en"},
				})},
				{ID: uuid.New(), FilePath: "/b.mkv", SubtitleStreams: mustJSON([]map[string]any{
					{"index": 3, "codec": "dvd_subtitle", "language": "fr"},
				})},
			},
		},
	}
	subs := &fakeSubs{err: errors.New("tesseract crashed")}
	lister := LibraryListerFunc(func(_ context.Context) ([]uuid.UUID, error) {
		return []uuid.UUID{libID}, nil
	})

	h := NewOCRHandler(mediaSrc, subs, lister, nil)
	out, err := h.Run(context.Background(), nil)
	if err != nil {
		t.Fatalf("Run should not propagate per-stream errors: %v", err)
	}
	if !strings.Contains(out, "2 errors") {
		t.Errorf("summary should report 2 errors, got %q", out)
	}
	if len(subs.calls) != 2 {
		t.Errorf("expected handler to attempt both streams, got %d calls", len(subs.calls))
	}
}

func TestImageBasedSubsFilter(t *testing.T) {
	raw := mustJSON([]map[string]any{
		{"index": 0, "codec": "subrip"},
		{"index": 2, "codec": "hdmv_pgs_subtitle"},
		{"index": 3, "codec": "ass"},
		{"index": 4, "codec": "DVD_SUBTITLE"},
	})
	got := imageBasedSubs(raw)
	if len(got) != 2 {
		t.Fatalf("expected 2 image-based subs, got %d: %+v", len(got), got)
	}
	if got[0].Index != 2 || got[1].Index != 4 {
		t.Errorf("wrong streams selected: %+v", got)
	}
	if imageBasedSubs(nil) != nil {
		t.Error("nil input should produce nil output")
	}
	if imageBasedSubs([]byte("not json")) != nil {
		t.Error("invalid JSON should produce nil output")
	}
}

func mustJSON(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}
