package v1

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/domain/library"
)

// orderLibService keeps the library's type and applies episode_order the
// way the real service does, so the handler sees the stored result.
type orderLibService struct {
	mockLibraryService
	gotOrder *string
}

func (s *orderLibService) Update(_ context.Context, p library.UpdateLibraryParams) (*library.Library, error) {
	s.gotOrder = p.EpisodeOrder
	lib := *s.lib
	if p.EpisodeOrder != nil {
		lib.EpisodeOrder = *p.EpisodeOrder
	}
	return &lib, nil
}

type stubOrderShows struct{ ids []uuid.UUID }

func (s stubOrderShows) ListShowsFollowingLibraryEpisodeOrder(_ context.Context, _ uuid.UUID) ([]uuid.UUID, error) {
	return s.ids, nil
}

func TestLibrary_Update_EpisodeOrder(t *testing.T) {
	showA, showB := uuid.New(), uuid.New()
	tests := []struct {
		name         string
		libType      string
		current      string
		body         string
		wantStatus   int
		wantOrder    string
		wantReenrich int
	}{
		{name: "show library aired → dvd re-enriches its shows", libType: "show", current: "aired", body: `{"episode_order":"dvd"}`, wantStatus: http.StatusOK, wantOrder: "dvd", wantReenrich: 2},
		{name: "anime library → absolute", libType: "anime", current: "aired", body: `{"episode_order":"absolute"}`, wantStatus: http.StatusOK, wantOrder: "absolute", wantReenrich: 2},
		{name: "unchanged: no re-enrich", libType: "show", current: "dvd", body: `{"episode_order":"dvd"}`, wantStatus: http.StatusOK, wantOrder: "dvd"},
		{name: "movie library: stored, nothing to re-number", libType: "movie", current: "aired", body: `{"episode_order":"dvd"}`, wantStatus: http.StatusOK, wantOrder: "dvd"},
		{name: "other fields leave it alone", libType: "show", current: "aired", body: `{"name":"TV"}`, wantStatus: http.StatusOK, wantOrder: "aired"},
		{name: "invalid value", libType: "show", current: "aired", body: `{"episode_order":"story_arc"}`, wantStatus: http.StatusBadRequest},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			id := uuid.New()
			svc := &orderLibService{mockLibraryService: mockLibraryService{lib: &library.Library{
				ID: id, Name: "TV", Type: tt.libType, Paths: []string{"/tv"}, EpisodeOrder: tt.current,
				CreatedAt: time.Now(), UpdatedAt: time.Now(),
			}}}
			enr := &chanEnricher{ids: make(chan uuid.UUID, 4)}
			h := NewLibraryHandler(svc, slog.Default()).
				WithEpisodeOrderReenrich(stubOrderShows{ids: []uuid.UUID{showA, showB}}, enr)

			rec := httptest.NewRecorder()
			req := withChiParam(httptest.NewRequest("PATCH", "/", strings.NewReader(tt.body)), "id", id.String())
			h.Update(rec, req)
			if rec.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d; body=%s", rec.Code, tt.wantStatus, rec.Body.String())
			}
			if tt.wantStatus != http.StatusOK {
				if svc.gotOrder != nil {
					t.Errorf("an invalid order reached the service")
				}
				return
			}
			var resp struct {
				Data LibraryResponse `json:"data"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
				t.Fatalf("decode: %v", err)
			}
			if resp.Data.EpisodeOrder != tt.wantOrder {
				t.Errorf("episode_order = %q, want %q", resp.Data.EpisodeOrder, tt.wantOrder)
			}
			got := 0
			timeout := time.After(300 * time.Millisecond)
		wait:
			for got < 2 {
				select {
				case <-enr.ids:
					got++
				case <-timeout:
					break wait
				}
			}
			if got != tt.wantReenrich {
				t.Errorf("re-enriched %d shows, want %d", got, tt.wantReenrich)
			}
		})
	}
}

func TestLibrary_Create_EpisodeOrderValidated(t *testing.T) {
	h := newLibHandler(&mockLibraryService{})
	rec := httptest.NewRecorder()
	h.Create(rec, httptest.NewRequest("POST", "/", strings.NewReader(`{"name":"TV","type":"show","scan_paths":["/tv"],"episode_order":"weird"}`)))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
}

func TestToLibraryResponse_EpisodeOrderDefaultsToAired(t *testing.T) {
	r := toLibraryResponse(&library.Library{ID: uuid.New()}, false)
	if r.EpisodeOrder != "aired" {
		t.Errorf("episode_order = %q, want aired", r.EpisodeOrder)
	}
}
