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

	"github.com/onscreen/onscreen/internal/api/middleware"
	"github.com/onscreen/onscreen/internal/auth"
	"github.com/onscreen/onscreen/internal/domain/media"
)

type stubEpisodeOrderSetter struct {
	calls []*string
	err   error
}

func (s *stubEpisodeOrderSetter) SetShowEpisodeOrder(_ context.Context, _ uuid.UUID, order *string) error {
	s.calls = append(s.calls, order)
	return s.err
}

// chanEnricher reports EnrichItem calls on a channel: the handler runs the
// re-enrich in the background.
type chanEnricher struct{ ids chan uuid.UUID }

func (c *chanEnricher) EnrichItem(_ context.Context, id uuid.UUID) error {
	c.ids <- id
	return nil
}
func (c *chanEnricher) MatchItem(_ context.Context, _ uuid.UUID, _ int) error { return nil }

func episodeOrderRequestFor(id uuid.UUID, body string, admin bool) *http.Request {
	req := withChiParam(httptest.NewRequest(http.MethodPut, "/api/v1/items/"+id.String()+"/episode-order", strings.NewReader(body)), "id", id.String())
	return req.WithContext(middleware.WithClaims(req.Context(), &auth.Claims{UserID: uuid.New(), Username: "u", IsAdmin: admin}))
}

func TestSetEpisodeOrder(t *testing.T) {
	showID := uuid.New()
	dvd := "dvd"
	tests := []struct {
		name       string
		item       *media.Item
		body       string
		admin      bool
		wantStatus int
		wantSet    bool
		wantOrder  *string
		wantEnrich bool
	}{
		{name: "admin sets dvd → re-enrich", item: &media.Item{ID: showID, Type: "show"}, body: `{"episode_order":"dvd"}`, admin: true, wantStatus: http.StatusOK, wantSet: true, wantOrder: &dvd, wantEnrich: true},
		{name: "admin clears with null", item: &media.Item{ID: showID, Type: "show", EpisodeOrder: &dvd}, body: `{"episode_order":null}`, admin: true, wantStatus: http.StatusOK, wantSet: true, wantEnrich: true},
		{name: "empty string clears too", item: &media.Item{ID: showID, Type: "show", EpisodeOrder: &dvd}, body: `{"episode_order":""}`, admin: true, wantStatus: http.StatusOK, wantSet: true, wantEnrich: true},
		{name: "unchanged order: no re-enrich", item: &media.Item{ID: showID, Type: "show", EpisodeOrder: &dvd}, body: `{"episode_order":"dvd"}`, admin: true, wantStatus: http.StatusOK, wantSet: true, wantOrder: &dvd},
		{name: "non-admin forbidden", item: &media.Item{ID: showID, Type: "show"}, body: `{"episode_order":"dvd"}`, wantStatus: http.StatusForbidden},
		{name: "invalid order", item: &media.Item{ID: showID, Type: "show"}, body: `{"episode_order":"production"}`, admin: true, wantStatus: http.StatusBadRequest},
		{name: "unknown field", item: &media.Item{ID: showID, Type: "show"}, body: `{"order":"dvd"}`, admin: true, wantStatus: http.StatusBadRequest},
		{name: "not a show", item: &media.Item{ID: showID, Type: "season"}, body: `{"episode_order":"dvd"}`, admin: true, wantStatus: http.StatusUnprocessableEntity},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			setter := &stubEpisodeOrderSetter{}
			enr := &chanEnricher{ids: make(chan uuid.UUID, 1)}
			h := NewItemHandler(&mockItemMedia{item: tt.item}, &mockItemWatch{}, &mockSessionCleaner{}, enr, nil, nil, nil, nil, slog.Default()).
				WithEpisodeOrder(setter)

			rec := httptest.NewRecorder()
			h.SetEpisodeOrder(rec, episodeOrderRequestFor(showID, tt.body, tt.admin))
			if rec.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d; body=%s", rec.Code, tt.wantStatus, rec.Body.String())
			}
			if got := len(setter.calls) == 1; got != tt.wantSet {
				t.Fatalf("set called %d times, want set=%v", len(setter.calls), tt.wantSet)
			}
			if tt.wantSet {
				got := setter.calls[0]
				if (got == nil) != (tt.wantOrder == nil) || (got != nil && *got != *tt.wantOrder) {
					t.Errorf("order set = %v, want %v", got, tt.wantOrder)
				}
				var resp struct {
					Data struct {
						EpisodeOrder *string `json:"episode_order"`
					} `json:"data"`
				}
				if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
					t.Fatalf("decode: %v", err)
				}
			}
			select {
			case id := <-enr.ids:
				if !tt.wantEnrich {
					t.Errorf("unexpected re-enrich of %s", id)
				} else if id != showID {
					t.Errorf("re-enriched %s, want the show", id)
				}
			case <-time.After(200 * time.Millisecond):
				if tt.wantEnrich {
					t.Errorf("show was not re-enriched")
				}
			}
		})
	}
}

func TestSetEpisodeOrder_NotConfigured(t *testing.T) {
	id := uuid.New()
	h := newItemHandler(&mockItemMedia{item: &media.Item{ID: id, Type: "show"}})
	rec := httptest.NewRecorder()
	h.SetEpisodeOrder(rec, episodeOrderRequestFor(id, `{"episode_order":"dvd"}`, true))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

// TestItemGetAndChildren_IndexEnd: a multi-episode episode reports
// index_end on both the detail and the children payloads; a single one
// omits it, and a show reports its own episode order.
func TestItemGetAndChildren_IndexEnd(t *testing.T) {
	one, two, three := 1, 2, 3
	epID := uuid.New()
	ms := &mockItemMedia{
		item: &media.Item{ID: epID, Type: "episode", Title: "Pilot / Part 2", Index: &one, IndexEnd: &two},
		children: []media.Item{
			{ID: uuid.New(), Type: "episode", Title: "Pilot / Part 2", Index: &one, IndexEnd: &two},
			{ID: uuid.New(), Type: "episode", Title: "Three", Index: &three},
		},
	}
	h := newItemHandler(ms)

	rec := httptest.NewRecorder()
	h.Get(rec, withChiParam(httptest.NewRequest("GET", "/api/v1/items/"+epID.String(), nil), "id", epID.String()))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"index_end":2`) {
		t.Fatalf("get: status %d body %s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), `"episode_order"`) {
		t.Errorf("episode_order must be omitted on an episode")
	}

	rec = httptest.NewRecorder()
	h.Children(rec, withChiParam(httptest.NewRequest("GET", "/api/v1/items/x/children", nil), "id", epID.String()))
	body := rec.Body.String()
	if rec.Code != http.StatusOK || strings.Count(body, `"index_end"`) != 1 || !strings.Contains(body, `"index_end":2`) {
		t.Fatalf("children: status %d body %s", rec.Code, body)
	}

	abs := "absolute"
	ms.item = &media.Item{ID: epID, Type: "show", Title: "Show", EpisodeOrder: &abs}
	rec = httptest.NewRecorder()
	h.Get(rec, withChiParam(httptest.NewRequest("GET", "/api/v1/items/"+epID.String(), nil), "id", epID.String()))
	if !strings.Contains(rec.Body.String(), `"episode_order":"absolute"`) {
		t.Errorf("show detail missing episode_order: %s", rec.Body.String())
	}
}
