package v1

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/api/middleware"
	"github.com/onscreen/onscreen/internal/auth"
	"github.com/onscreen/onscreen/internal/domain/media"
	"github.com/onscreen/onscreen/internal/intromarker"
)

// stubMarkers is a no-op ItemMarkerService that records whether List ran.
type stubMarkers struct{ listed bool }

func (s *stubMarkers) List(_ context.Context, _ uuid.UUID) ([]intromarker.Marker, error) {
	s.listed = true
	return []intromarker.Marker{{Kind: "intro", StartMS: 0, EndMS: 1000, Source: "auto"}}, nil
}
func (s *stubMarkers) Upsert(_ context.Context, _ uuid.UUID, _ string, _, _ int64) (intromarker.Marker, error) {
	return intromarker.Marker{}, nil
}
func (s *stubMarkers) Delete(_ context.Context, _ uuid.UUID, _ string) error { return nil }

// A capped profile must get 404 for the markers of an item it can't stream
// (unrated ranks most restrictive), for episodes and non-episodes alike.
func TestListMarkers_AppliesRatingCeiling(t *testing.T) {
	for _, typ := range []string{"episode", "movie"} {
		id := uuid.New()
		mk := &stubMarkers{}
		h := newItemHandler(&mockItemMedia{item: &media.Item{ID: id, LibraryID: uuid.New(), Type: typ}}).WithMarkers(mk)
		get := func(maxRating string) int {
			req := httptest.NewRequest(http.MethodGet, "/api/v1/items/"+id.String()+"/markers", nil)
			req = withChiParam(req, "id", id.String())
			req = req.WithContext(middleware.WithClaims(req.Context(), &auth.Claims{UserID: uuid.New(), MaxContentRating: maxRating}))
			rec := httptest.NewRecorder()
			h.ListMarkers(rec, req)
			return rec.Code
		}
		if got := get("PG"); got != http.StatusNotFound {
			t.Errorf("%s: restricted profile, unrated item: got %d, want 404", typ, got)
		}
		if mk.listed {
			t.Errorf("%s: markers must not be read for an over-ceiling item", typ)
		}
		if got := get(""); got != http.StatusOK {
			t.Errorf("%s: unrestricted caller: got %d, want 200", typ, got)
		}
	}
}

// A capped profile must not be able to record a watch event (progress) against
// an item above its ceiling; an in-ceiling item still records.
func TestProgress_AppliesRatingCeiling(t *testing.T) {
	put := func(rating *string, maxRating string) (int, bool) {
		id := uuid.New()
		ws := &mockItemWatch{}
		ms := &mockItemMedia{item: &media.Item{ID: id, LibraryID: uuid.New(), Type: "movie", ContentRating: rating}}
		h := NewItemHandler(ms, ws, &mockSessionCleaner{}, nil, nil, nil, nil, nil, slog.Default())
		body := `{"view_offset_ms":1000,"duration_ms":60000,"state":"playing"}`
		req := withChiParam(httptest.NewRequest(http.MethodPut, "/", strings.NewReader(body)), "id", id.String())
		req = req.WithContext(middleware.WithClaims(req.Context(), &auth.Claims{UserID: uuid.New(), MaxContentRating: maxRating}))
		rec := httptest.NewRecorder()
		h.Progress(rec, req)
		return rec.Code, ws.recorded
	}
	r, pg := "R", "PG"
	if code, rec := put(nil, "PG"); code != http.StatusNotFound || rec {
		t.Errorf("unrated item, PG profile: got %d (recorded=%v), want 404 and no event", code, rec)
	}
	if code, rec := put(&r, "PG"); code != http.StatusNotFound || rec {
		t.Errorf("R item, PG profile: got %d (recorded=%v), want 404 and no event", code, rec)
	}
	if code, rec := put(&pg, "PG"); code != http.StatusNoContent || !rec {
		t.Errorf("PG item, PG profile: got %d (recorded=%v), want 204 and an event", code, rec)
	}
}
