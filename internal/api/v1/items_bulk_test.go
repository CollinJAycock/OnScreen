package v1

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/api/middleware"
	"github.com/onscreen/onscreen/internal/auth"
	"github.com/onscreen/onscreen/internal/bulkedit"
)

type fakeBulkEditor struct {
	reqs []bulkedit.Request
	res  bulkedit.Result
	err  error
}

func (f *fakeBulkEditor) Apply(_ context.Context, req bulkedit.Request) (bulkedit.Result, error) {
	f.reqs = append(f.reqs, req)
	return f.res, f.err
}

type fakeNFOWriter struct {
	mu   sync.Mutex
	ids  []uuid.UUID
	done chan struct{}
	want int
}

func (f *fakeNFOWriter) WriteItemNFO(_ context.Context, id uuid.UUID) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ids = append(f.ids, id)
	if len(f.ids) == f.want {
		close(f.done)
	}
}

func bulkPatch(t *testing.T, h *ItemHandler, body string, admin bool) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("PATCH", "/api/v1/items/bulk", strings.NewReader(body))
	req = req.WithContext(middleware.WithClaims(req.Context(),
		&auth.Claims{UserID: uuid.New(), Username: "u", IsAdmin: admin}))
	h.BulkUpdateMetadata(rec, req)
	return rec
}

func TestBulkUpdateMetadata_NonAdminForbidden(t *testing.T) {
	be := &fakeBulkEditor{}
	h := newItemHandler(&mockItemMedia{}).WithBulkEditor(be)
	rec := bulkPatch(t, h, fmt.Sprintf(`{"item_ids":[%q],"set":{"content_rating":"PG"}}`, uuid.New()), false)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d", rec.Code)
	}
	if len(be.reqs) != 0 {
		t.Error("non-admin edit reached the editor")
	}
}

func TestBulkUpdateMetadata_ValidRequest(t *testing.T) {
	a, b := uuid.New(), uuid.New()
	updated := []uuid.UUID{a}
	be := &fakeBulkEditor{res: bulkedit.Result{
		Matched: 2, Updated: updated, LabelsAdded: 2,
		Skipped: []bulkedit.Skip{{ItemID: b, Title: "B", Field: "genres", Reason: bulkedit.ReasonLocked}},
	}}
	caches := &fakeItemCaches{}
	nfo := &fakeNFOWriter{done: make(chan struct{}), want: 1}
	h := newItemHandler(&mockItemMedia{}).WithBulkEditor(be).WithCacheInvalidator(caches).WithNFOWriter(nfo)

	rec := bulkPatch(t, h, fmt.Sprintf(`{
		"item_ids": [%q, %q],
		"set": {"content_rating": " PG-13 "},
		"add_genres": ["Heist", " heist "], "remove_genres": ["Drama"],
		"add_labels": ["Late  Night"], "lock": ["summary", "summary"], "unlock": ["genres"]
	}`, a, b), true)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body)
	}
	req := be.reqs[0]
	if !slices.Equal(req.ItemIDs, []uuid.UUID{a, b}) {
		t.Errorf("ids = %v", req.ItemIDs)
	}
	if !req.SetContentRating || req.ContentRating == nil || *req.ContentRating != "PG-13" {
		t.Errorf("content rating = %v %v", req.SetContentRating, req.ContentRating)
	}
	if req.SetGenres || !slices.Equal(req.AddGenres, []string{"Heist"}) || !slices.Equal(req.RemoveGenres, []string{"Drama"}) {
		t.Errorf("genres = %v %v %v", req.SetGenres, req.AddGenres, req.RemoveGenres)
	}
	if !slices.Equal(req.AddLabels, []string{"late night"}) || req.MaxLabels == 0 {
		t.Errorf("labels = %v max %d", req.AddLabels, req.MaxLabels)
	}
	if !slices.Equal(req.Lock, []string{"summary"}) || !slices.Equal(req.Unlock, []string{"genres"}) {
		t.Errorf("lock %v unlock %v", req.Lock, req.Unlock)
	}

	var env struct {
		Data bulkEditResponse `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatal(err)
	}
	if env.Data.Matched != 2 || env.Data.Updated != 1 || env.Data.LabelsAdded != 2 ||
		len(env.Data.Skipped) != 1 || env.Data.Skipped[0].Reason != "locked" || env.Data.Skipped[0].ItemID != b.String() {
		t.Errorf("response = %+v", env.Data)
	}
	if caches.calls != 1 {
		t.Errorf("cache invalidations = %d", caches.calls)
	}
	select {
	case <-nfo.done:
	case <-time.After(2 * time.Second):
		t.Fatal("NFO rewrite never ran")
	}
	if !slices.Equal(nfo.ids, []uuid.UUID{a}) {
		t.Errorf("NFO rewrites = %v", nfo.ids)
	}
}

func TestBulkUpdateMetadata_NullContentRatingClears(t *testing.T) {
	be := &fakeBulkEditor{}
	h := newItemHandler(&mockItemMedia{}).WithBulkEditor(be)
	rec := bulkPatch(t, h, fmt.Sprintf(`{"item_ids":[%q],"set":{"content_rating":null,"genres":[]}}`, uuid.New()), true)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body)
	}
	req := be.reqs[0]
	if !req.SetContentRating || req.ContentRating != nil || !req.SetGenres || len(req.Genres) != 0 {
		t.Errorf("req = %+v", req)
	}
}

func TestBulkUpdateMetadata_BadRequests(t *testing.T) {
	id := uuid.New().String()
	many := make([]string, maxBulkEditItems+1)
	for i := range many {
		many[i] = fmt.Sprintf("%q", uuid.New())
	}
	cases := map[string]string{
		"empty ids":         `{"item_ids":[],"set":{"content_rating":"PG"}}`,
		"too many":          `{"item_ids":[` + strings.Join(many, ",") + `],"lock":["title"]}`,
		"bad id":            `{"item_ids":["nope"],"lock":["title"]}`,
		"duplicate id":      fmt.Sprintf(`{"item_ids":[%q,%q],"lock":["title"]}`, id, id),
		"nothing to change": fmt.Sprintf(`{"item_ids":[%q]}`, id),
		"unknown set field": fmt.Sprintf(`{"item_ids":[%q],"set":{"year":2001}}`, id),
		"unknown top field": fmt.Sprintf(`{"item_ids":[%q],"title":"x"}`, id),
		"replace and merge": fmt.Sprintf(`{"item_ids":[%q],"set":{"genres":["A"]},"add_genres":["B"]}`, id),
		"unlockable lock":   fmt.Sprintf(`{"item_ids":[%q],"lock":["tags"]}`, id),
		"lock and unlock":   fmt.Sprintf(`{"item_ids":[%q],"lock":["year"],"unlock":["year"]}`, id),
		"long rating":       fmt.Sprintf(`{"item_ids":[%q],"set":{"content_rating":%q}}`, id, strings.Repeat("x", maxEditRatingLen+1)),
		"too many genres":   fmt.Sprintf(`{"item_ids":[%q],"add_genres":%s}`, id, genresJSON(maxEditGenres+1)),
		"bad label":         fmt.Sprintf(`{"item_ids":[%q],"add_labels":[%q]}`, id, strings.Repeat("x", 200)),
		"malformed":         `{"item_ids":`,
	}
	for name, body := range cases {
		be := &fakeBulkEditor{}
		h := newItemHandler(&mockItemMedia{}).WithBulkEditor(be)
		if rec := bulkPatch(t, h, body, true); rec.Code != http.StatusBadRequest {
			t.Errorf("%s: status = %d, want 400 (%s)", name, rec.Code, rec.Body)
		}
		if len(be.reqs) != 0 {
			t.Errorf("%s: reached the editor", name)
		}
	}
}

func TestBulkUpdateMetadata_EditorErrorIs500(t *testing.T) {
	be := &fakeBulkEditor{err: errors.New("tx failed")}
	caches := &fakeItemCaches{}
	h := newItemHandler(&mockItemMedia{}).WithBulkEditor(be).WithCacheInvalidator(caches)
	rec := bulkPatch(t, h, fmt.Sprintf(`{"item_ids":[%q],"lock":["title"]}`, uuid.New()), true)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d", rec.Code)
	}
	if caches.calls != 0 {
		t.Error("caches invalidated after a failed edit")
	}
}

func TestBulkUpdateMetadata_NotWiredIs404(t *testing.T) {
	h := newItemHandler(&mockItemMedia{})
	if rec := bulkPatch(t, h, `{}`, true); rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d", rec.Code)
	}
}
