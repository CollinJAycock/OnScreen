package v1

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onscreen/onscreen/internal/api/middleware"
	"github.com/onscreen/onscreen/internal/auth"
	"github.com/onscreen/onscreen/internal/db/gen"
)

// ── mocks ────────────────────────────────────────────────────────────────────

type mockBulkAdminDB struct {
	rows           []gen.ListUnmatchedTopLevelItemsRow
	listErr        error
	titleUpdates   []gen.UpdateMediaItemTitleParams
	updateErr      error
}

func (m *mockBulkAdminDB) ListUnmatchedTopLevelItems(_ context.Context, _ gen.ListUnmatchedTopLevelItemsParams) ([]gen.ListUnmatchedTopLevelItemsRow, error) {
	if m.listErr != nil {
		return nil, m.listErr
	}
	return m.rows, nil
}

func (m *mockBulkAdminDB) UpdateMediaItemTitle(_ context.Context, p gen.UpdateMediaItemTitleParams) error {
	if m.updateErr != nil {
		return m.updateErr
	}
	m.titleUpdates = append(m.titleUpdates, p)
	return nil
}

type mockBulkEnricher struct {
	mu       sync.Mutex
	enriched []uuid.UUID
	enrichErr error
}

func (m *mockBulkEnricher) EnrichItem(_ context.Context, id uuid.UUID) error {
	m.mu.Lock()
	m.enriched = append(m.enriched, id)
	m.mu.Unlock()
	return m.enrichErr
}

func (m *mockBulkEnricher) MatchItem(_ context.Context, _ uuid.UUID, _ int) error {
	return nil
}

func (m *mockBulkEnricher) seen() []uuid.UUID {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := append([]uuid.UUID(nil), m.enriched...)
	return out
}

func adminClaims(r *http.Request) *http.Request {
	claims := &auth.Claims{
		UserID:    uuid.New(),
		Username:  "admin",
		IsAdmin:   true,
		IssuedAt:  time.Now(),
		ExpiresAt: time.Now().Add(time.Hour),
	}
	return r.WithContext(middleware.WithClaims(r.Context(), claims))
}

func nonAdminClaims(r *http.Request) *http.Request {
	claims := &auth.Claims{
		UserID:    uuid.New(),
		Username:  "user",
		IsAdmin:   false,
		IssuedAt:  time.Now(),
		ExpiresAt: time.Now().Add(time.Hour),
	}
	return r.WithContext(middleware.WithClaims(r.Context(), claims))
}

// makeRow constructs a fixture row for a soft-deleted… no, an unenriched
// top-level item. Type defaults to "show".
func makeRow(title string) gen.ListUnmatchedTopLevelItemsRow {
	return gen.ListUnmatchedTopLevelItemsRow{
		ID:        uuid.New(),
		LibraryID: uuid.New(),
		Type:      "show",
		Title:     title,
		SortTitle: title,
		UpdatedAt: pgtype.Timestamptz{Valid: false},
	}
}

// ── tests ────────────────────────────────────────────────────────────────────

// TestReEnrichUnmatched_StripsBracketAndQueuesEnrichment is the
// happy-path: a [release-group]-prefixed show is found, its title is
// rewritten in the DB, and an enrichment is queued on the background
// goroutine. The mock enricher records the queued IDs so we can assert
// exactly which items got picked up.
func TestReEnrichUnmatched_StripsBracketAndQueuesEnrichment(t *testing.T) {
	r1 := makeRow("[ToonsHub] My Hero Academia")
	r2 := makeRow("[DKB] Sentai Daishikkaku")
	r3 := makeRow("FH") // no bracket — title left alone, but still queued
	db := &mockBulkAdminDB{rows: []gen.ListUnmatchedTopLevelItemsRow{r1, r2, r3}}
	enr := &mockBulkEnricher{}
	h := NewItemBulkAdminHandler(db, enr, slog.Default())

	body := bytes.NewBufferString(`{}`)
	rec := httptest.NewRecorder()
	req := adminClaims(httptest.NewRequest("POST", "/api/v1/admin/items/re-enrich-unmatched", body))
	req.Header.Set("Content-Type", "application/json")
	h.ReEnrichUnmatched(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200", rec.Code)
	}
	var resp struct {
		Data reEnrichUnmatchedResponse `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if resp.Data.Found != 3 {
		t.Errorf("found: got %d, want 3", resp.Data.Found)
	}
	if resp.Data.TitlesCleaned != 2 {
		t.Errorf("titles_cleaned: got %d, want 2 (only the two bracket-prefixed)", resp.Data.TitlesCleaned)
	}
	if resp.Data.EnrichmentQueued != 3 {
		t.Errorf("enrichment_queued: got %d, want 3 (all candidates queued regardless of title shape)", resp.Data.EnrichmentQueued)
	}
	// DB updates must include only the bracket-prefixed entries with the
	// stripped titles.
	if len(db.titleUpdates) != 2 {
		t.Fatalf("title updates: got %d, want 2", len(db.titleUpdates))
	}
	wantTitles := map[string]bool{"My Hero Academia": false, "Sentai Daishikkaku": false}
	for _, u := range db.titleUpdates {
		if _, ok := wantTitles[u.Title]; !ok {
			t.Errorf("unexpected title update: %q", u.Title)
		}
		wantTitles[u.Title] = true
	}
	for title, hit := range wantTitles {
		if !hit {
			t.Errorf("missing title update for %q", title)
		}
	}

	// The background enrichment is async — wait for it.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if len(enr.seen()) >= 3 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if got := len(enr.seen()); got != 3 {
		t.Errorf("enrichments queued: got %d, want 3", got)
	}
}

// TestReEnrichUnmatched_DryRun returns the candidates without applying
// any side effects — no DB updates, no enrichment queued. Lets the
// admin preview before committing.
func TestReEnrichUnmatched_DryRun(t *testing.T) {
	db := &mockBulkAdminDB{rows: []gen.ListUnmatchedTopLevelItemsRow{
		makeRow("[ToonsHub] My Hero Academia"),
	}}
	enr := &mockBulkEnricher{}
	h := NewItemBulkAdminHandler(db, enr, slog.Default())

	body := bytes.NewBufferString(`{"dry_run": true}`)
	rec := httptest.NewRecorder()
	req := adminClaims(httptest.NewRequest("POST", "/api/v1/admin/items/re-enrich-unmatched", body))
	req.Header.Set("Content-Type", "application/json")
	h.ReEnrichUnmatched(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200", rec.Code)
	}
	if len(db.titleUpdates) != 0 {
		t.Errorf("dry_run must not update titles; got %d updates", len(db.titleUpdates))
	}
	// Wait briefly to confirm no goroutine enrichments fire either.
	time.Sleep(100 * time.Millisecond)
	if len(enr.seen()) != 0 {
		t.Errorf("dry_run must not queue enrichments; got %d", len(enr.seen()))
	}
	var resp struct {
		Data reEnrichUnmatchedResponse `json:"data"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	if !resp.Data.DryRun {
		t.Error("response must echo dry_run=true")
	}
	if resp.Data.TitlesCleaned != 1 {
		t.Errorf("dry_run still reports the planned cleanup count; got %d, want 1", resp.Data.TitlesCleaned)
	}
}

// TestReEnrichUnmatched_NonAdmin_Forbidden — only admins can run a
// bulk re-enrich. Mirrors the per-item Enrich endpoint's auth check.
func TestReEnrichUnmatched_NonAdmin_Forbidden(t *testing.T) {
	h := NewItemBulkAdminHandler(&mockBulkAdminDB{}, &mockBulkEnricher{}, slog.Default())

	rec := httptest.NewRecorder()
	req := nonAdminClaims(httptest.NewRequest("POST", "/api/v1/admin/items/re-enrich-unmatched", nil))
	h.ReEnrichUnmatched(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Errorf("status: got %d, want 403", rec.Code)
	}
}

// TestReEnrichUnmatched_NoEnricher — when the enricher dependency is
// nil (e.g. TMDB key not configured), the route returns BadRequest
// instead of panicking. Mirrors per-item Enrich.
func TestReEnrichUnmatched_NoEnricher(t *testing.T) {
	h := NewItemBulkAdminHandler(&mockBulkAdminDB{}, nil, slog.Default())

	rec := httptest.NewRecorder()
	req := adminClaims(httptest.NewRequest("POST", "/api/v1/admin/items/re-enrich-unmatched", nil))
	h.ReEnrichUnmatched(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status: got %d, want 400", rec.Code)
	}
}

// TestReEnrichUnmatched_TitleUpdateFailure_StillQueuesEnrichment —
// if the synchronous title rewrite fails (e.g. transient DB blip), we
// still queue the per-item enrichment. The enricher applies its own
// in-memory cleanTitle to whatever the stored title is, so the TMDB
// search query is still cleaned even if the persisted title stays
// dirty until next time.
func TestReEnrichUnmatched_TitleUpdateFailure_StillQueuesEnrichment(t *testing.T) {
	db := &mockBulkAdminDB{
		rows:      []gen.ListUnmatchedTopLevelItemsRow{makeRow("[ToonsHub] My Hero Academia")},
		updateErr: errors.New("db down"),
	}
	enr := &mockBulkEnricher{}
	h := NewItemBulkAdminHandler(db, enr, slog.Default())

	rec := httptest.NewRecorder()
	req := adminClaims(httptest.NewRequest("POST", "/api/v1/admin/items/re-enrich-unmatched", bytes.NewBufferString(`{}`)))
	req.Header.Set("Content-Type", "application/json")
	h.ReEnrichUnmatched(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200 (DB update failure shouldn't fail the request)", rec.Code)
	}
	deadline := time.Now().Add(1 * time.Second)
	for time.Now().Before(deadline) {
		if len(enr.seen()) > 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if got := len(enr.seen()); got != 1 {
		t.Errorf("enrichments queued: got %d, want 1 even on title-update failure", got)
	}
}
