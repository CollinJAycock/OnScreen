package scheduler

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/domain/media"
)

type stubMissingArtLister struct {
	items []media.Item
	err   error
	limit int32
}

func (s *stubMissingArtLister) ListItemsMissingArt(_ context.Context, limit int32) ([]media.Item, error) {
	s.limit = limit
	return s.items, s.err
}

type stubMissingArtEnricher struct {
	errForIDs map[uuid.UUID]error
	called    []uuid.UUID
}

func (s *stubMissingArtEnricher) EnrichItem(_ context.Context, id uuid.UUID) error {
	s.called = append(s.called, id)
	if e, ok := s.errForIDs[id]; ok {
		return e
	}
	return nil
}

func TestRefreshMissingArtHandler_NoItems(t *testing.T) {
	h := NewRefreshMissingArtHandler(&stubMissingArtLister{}, &stubMissingArtEnricher{}, slog.Default())
	out, err := h.Run(context.Background(), nil)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if !strings.Contains(out, "no items missing art") {
		t.Errorf("output: got %q, want substring 'no items missing art'", out)
	}
}

func TestRefreshMissingArtHandler_DefaultLimit(t *testing.T) {
	lister := &stubMissingArtLister{}
	h := NewRefreshMissingArtHandler(lister, &stubMissingArtEnricher{}, slog.Default())
	if _, err := h.Run(context.Background(), nil); err != nil {
		t.Fatalf("err: %v", err)
	}
	if lister.limit != 200 {
		t.Errorf("default limit: got %d, want 200", lister.limit)
	}
}

func TestRefreshMissingArtHandler_ConfigLimitClampsTo1000(t *testing.T) {
	lister := &stubMissingArtLister{}
	h := NewRefreshMissingArtHandler(lister, &stubMissingArtEnricher{}, slog.Default())
	if _, err := h.Run(context.Background(), []byte(`{"limit":50000}`)); err != nil {
		t.Fatalf("err: %v", err)
	}
	if lister.limit != 1000 {
		t.Errorf("clamp: got %d, want 1000", lister.limit)
	}
}

func TestRefreshMissingArtHandler_CountsRefreshAndFailure(t *testing.T) {
	good1 := uuid.New()
	good2 := uuid.New()
	bad := uuid.New()
	lister := &stubMissingArtLister{items: []media.Item{
		{ID: good1, Title: "Good 1"},
		{ID: bad, Title: "Bad"},
		{ID: good2, Title: "Good 2"},
	}}
	enr := &stubMissingArtEnricher{
		errForIDs: map[uuid.UUID]error{bad: errors.New("tmdb down")},
	}
	h := NewRefreshMissingArtHandler(lister, enr, slog.Default())
	out, err := h.Run(context.Background(), nil)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if !strings.Contains(out, "candidates=3") || !strings.Contains(out, "refreshed=2") || !strings.Contains(out, "failed=1") {
		t.Errorf("output: got %q, want candidates=3 refreshed=2 failed=1", out)
	}
	if len(enr.called) != 3 {
		t.Errorf("EnrichItem calls: got %d, want 3", len(enr.called))
	}
}

func TestRefreshMissingArtHandler_ListerErrorBubbles(t *testing.T) {
	lister := &stubMissingArtLister{err: errors.New("db down")}
	h := NewRefreshMissingArtHandler(lister, &stubMissingArtEnricher{}, slog.Default())
	if _, err := h.Run(context.Background(), nil); err == nil {
		t.Fatal("expected err when lister fails")
	}
}

// ── dangling-artwork verification ────────────────────────────────────────────

// stubArtVerifier serves keyset pages from a pre-sorted item slice and
// records ClearItemArtPaths calls as {clearPoster, clearFanart} per item.
type stubArtVerifier struct {
	items     []media.ArtPathsItem // must be sorted by ID bytes
	listErr   error
	clearErr  error
	cleared   map[uuid.UUID][2]bool
	listCalls int
}

func (s *stubArtVerifier) ListItemsWithArt(_ context.Context, afterID uuid.UUID, limit int32) ([]media.ArtPathsItem, error) {
	s.listCalls++
	if s.listErr != nil {
		return nil, s.listErr
	}
	out := []media.ArtPathsItem{}
	for _, it := range s.items {
		if bytes.Compare(it.ID[:], afterID[:]) <= 0 {
			continue
		}
		out = append(out, it)
		if int32(len(out)) == limit {
			break
		}
	}
	return out, nil
}

func (s *stubArtVerifier) ClearItemArtPaths(_ context.Context, id uuid.UUID, clearPoster, clearFanart bool) error {
	if s.clearErr != nil {
		return s.clearErr
	}
	if s.cleared == nil {
		s.cleared = map[uuid.UUID][2]bool{}
	}
	s.cleared[id] = [2]bool{clearPoster, clearFanart}
	return nil
}

// statFor returns a stat func that reports only the given absolute paths as
// existing. Keys must be built with filepath.Join so separators match the
// handler's resolution on every OS.
func statFor(existing map[string]bool) func(context.Context, string) error {
	return func(_ context.Context, abs string) error {
		if existing[abs] {
			return nil
		}
		return errors.New("not found")
	}
}

// seqID builds a deterministic UUID whose byte order matches its numeric
// order, so keyset pagination in the stub walks items the way Postgres would.
func seqID(n byte) uuid.UUID {
	var id uuid.UUID
	id[15] = n
	return id
}

func TestRefreshMissingArtHandler_DanglingPosterClearedAndHealed(t *testing.T) {
	id := seqID(1)
	root := filepath.FromSlash("/lib")
	poster := "The Invite (2026)tt14173636/poster.jpg"
	// Folder still there, poster.jpg inside it gone — the confirmed-dangling shape.
	exists := map[string]bool{filepath.Join(root, "The Invite (2026)tt14173636"): true}

	ver := &stubArtVerifier{items: []media.ArtPathsItem{{ID: id, Type: "movie", PosterPath: &poster}}}
	enr := &stubMissingArtEnricher{}
	h := NewRefreshMissingArtHandler(&stubMissingArtLister{}, enr, slog.Default()).
		WithArtVerification(ver, func() []string { return []string{root} }, statFor(exists))

	out, err := h.Run(context.Background(), nil)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if got, want := ver.cleared[id], [2]bool{true, false}; got != want {
		t.Errorf("cleared flags: got %v, want %v", got, want)
	}
	if len(enr.called) != 1 || enr.called[0] != id {
		t.Errorf("EnrichItem calls: got %v, want exactly [%s]", enr.called, id)
	}
	for _, want := range []string{"art_checked=1", "dangling_cleared=1", "candidates=1", "refreshed=1"} {
		if !strings.Contains(out, want) {
			t.Errorf("output %q missing %q", out, want)
		}
	}
}

func TestRefreshMissingArtHandler_PresentArtUntouched(t *testing.T) {
	id := seqID(1)
	root := filepath.FromSlash("/lib")
	poster := "His Girl Friday (1940)tt0032599/poster.jpg"
	exists := map[string]bool{filepath.Join(root, "His Girl Friday (1940)tt0032599", "poster.jpg"): true}

	ver := &stubArtVerifier{items: []media.ArtPathsItem{{ID: id, Type: "movie", PosterPath: &poster}}}
	enr := &stubMissingArtEnricher{}
	h := NewRefreshMissingArtHandler(&stubMissingArtLister{}, enr, slog.Default()).
		WithArtVerification(ver, func() []string { return []string{root} }, statFor(exists))

	out, err := h.Run(context.Background(), nil)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(ver.cleared) != 0 {
		t.Errorf("cleared: got %v, want none", ver.cleared)
	}
	if len(enr.called) != 0 {
		t.Errorf("EnrichItem calls: got %v, want none", enr.called)
	}
	if !strings.Contains(out, "art_checked=1 dangling_cleared=0; no items missing art") {
		t.Errorf("output: got %q", out)
	}
}

func TestRefreshMissingArtHandler_UnverifiableLeftAlone(t *testing.T) {
	root := filepath.FromSlash("/lib")
	gone := "Vanished (2020)/poster.jpg" // neither file nor parent dir exists
	abs := "C:/absolute/legacy/poster.jpg"
	ver := &stubArtVerifier{items: []media.ArtPathsItem{
		{ID: seqID(1), Type: "movie", PosterPath: &gone},
		{ID: seqID(2), Type: "movie", PosterPath: &abs},
	}}
	enr := &stubMissingArtEnricher{}
	h := NewRefreshMissingArtHandler(&stubMissingArtLister{}, enr, slog.Default()).
		WithArtVerification(ver, func() []string { return []string{root} }, statFor(nil))

	out, err := h.Run(context.Background(), nil)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(ver.cleared) != 0 {
		t.Errorf("cleared: got %v, want none (unverifiable must never clear)", ver.cleared)
	}
	if len(enr.called) != 0 {
		t.Errorf("EnrichItem calls: got %v, want none", enr.called)
	}
	if !strings.Contains(out, "unverifiable=2") {
		t.Errorf("output %q missing unverifiable=2", out)
	}
}

func TestRefreshMissingArtHandler_FanartOnlyDanglingClearsJustFanart(t *testing.T) {
	id := seqID(1)
	root := filepath.FromSlash("/lib")
	poster := "Show/poster.jpg"
	fanart := "Show/fanart.jpg"
	exists := map[string]bool{
		filepath.Join(root, "Show"):               true,
		filepath.Join(root, "Show", "poster.jpg"): true,
	}

	ver := &stubArtVerifier{items: []media.ArtPathsItem{
		{ID: id, Type: "show", PosterPath: &poster, FanartPath: &fanart},
	}}
	enr := &stubMissingArtEnricher{}
	h := NewRefreshMissingArtHandler(&stubMissingArtLister{}, enr, slog.Default()).
		WithArtVerification(ver, func() []string { return []string{root} }, statFor(exists))

	out, err := h.Run(context.Background(), nil)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if got, want := ver.cleared[id], [2]bool{false, true}; got != want {
		t.Errorf("cleared flags: got %v, want %v", got, want)
	}
	// A fanart-only clear leaves poster_path set, so the item is NOT in the
	// missing-art pool — the heal-ID merge is what re-enriches it this run.
	if len(enr.called) != 1 || enr.called[0] != id {
		t.Errorf("EnrichItem calls: got %v, want exactly [%s]", enr.called, id)
	}
	if !strings.Contains(out, "dangling_cleared=1") {
		t.Errorf("output %q missing dangling_cleared=1", out)
	}
}

func TestRefreshMissingArtHandler_HealedItemAlsoInMissingListEnrichedOnce(t *testing.T) {
	id := seqID(1)
	root := filepath.FromSlash("/lib")
	poster := "Movie/poster.jpg"
	exists := map[string]bool{filepath.Join(root, "Movie"): true}

	ver := &stubArtVerifier{items: []media.ArtPathsItem{{ID: id, Type: "movie", PosterPath: &poster}}}
	lister := &stubMissingArtLister{items: []media.Item{{ID: id, Title: "Movie"}}}
	enr := &stubMissingArtEnricher{}
	h := NewRefreshMissingArtHandler(lister, enr, slog.Default()).
		WithArtVerification(ver, func() []string { return []string{root} }, statFor(exists))

	out, err := h.Run(context.Background(), nil)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(enr.called) != 1 {
		t.Errorf("EnrichItem calls: got %d, want 1 (no double enrich)", len(enr.called))
	}
	if !strings.Contains(out, "candidates=1") {
		t.Errorf("output %q missing candidates=1", out)
	}
}

func TestRefreshMissingArtHandler_VerifyWalksAllKeysetPages(t *testing.T) {
	root := filepath.FromSlash("/lib")
	poster := "Movie/poster.jpg" // present for everyone — sweep only counts
	exists := map[string]bool{filepath.Join(root, "Movie", "poster.jpg"): true}

	items := make([]media.ArtPathsItem, 5)
	for i := range items {
		items[i] = media.ArtPathsItem{ID: seqID(byte(i + 1)), Type: "movie", PosterPath: &poster}
	}
	ver := &stubArtVerifier{items: items}
	h := NewRefreshMissingArtHandler(&stubMissingArtLister{}, &stubMissingArtEnricher{}, slog.Default()).
		WithArtVerification(ver, func() []string { return []string{root} }, statFor(exists))
	h.batchSize = 2

	out, err := h.Run(context.Background(), nil)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if !strings.Contains(out, "art_checked=5") {
		t.Errorf("output %q missing art_checked=5", out)
	}
	// 5 items at batch size 2 → pages of 2, 2, 1; the short page stops the walk.
	if ver.listCalls != 3 {
		t.Errorf("list calls: got %d, want 3", ver.listCalls)
	}
}

func TestRefreshMissingArtHandler_VerifyListErrorDegradesToClassicRun(t *testing.T) {
	id := seqID(1)
	lister := &stubMissingArtLister{items: []media.Item{{ID: id, Title: "Movie"}}}
	ver := &stubArtVerifier{listErr: errors.New("db hiccup")}
	enr := &stubMissingArtEnricher{}
	h := NewRefreshMissingArtHandler(lister, enr, slog.Default()).
		WithArtVerification(ver, func() []string { return []string{filepath.FromSlash("/lib")} }, statFor(nil))

	out, err := h.Run(context.Background(), nil)
	if err != nil {
		t.Fatalf("verification failure must not abort the run: %v", err)
	}
	if len(enr.called) != 1 {
		t.Errorf("EnrichItem calls: got %d, want 1 (missing-art phase must still run)", len(enr.called))
	}
	if !strings.Contains(out, "candidates=1") {
		t.Errorf("output: got %q", out)
	}
}

func TestRefreshMissingArtHandler_NoRootsSkipsVerification(t *testing.T) {
	poster := "Movie/poster.jpg"
	ver := &stubArtVerifier{items: []media.ArtPathsItem{{ID: seqID(1), Type: "movie", PosterPath: &poster}}}
	h := NewRefreshMissingArtHandler(&stubMissingArtLister{}, &stubMissingArtEnricher{}, slog.Default()).
		WithArtVerification(ver, func() []string { return nil }, statFor(nil))

	out, err := h.Run(context.Background(), nil)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if ver.listCalls != 0 {
		t.Errorf("list calls: got %d, want 0 (no roots → nothing resolvable)", ver.listCalls)
	}
	if len(ver.cleared) != 0 {
		t.Errorf("cleared: got %v, want none", ver.cleared)
	}
	if !strings.Contains(out, "art_checked=0") {
		t.Errorf("output: got %q", out)
	}
}
