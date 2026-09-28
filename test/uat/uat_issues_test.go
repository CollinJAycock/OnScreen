// uat_issues_test.go — router-level gates for "Report a problem" and the
// admin Library health / re-grab endpoints.
package uat

import (
	"context"
	"log/slog"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onscreen/onscreen/internal/api"
	v1 "github.com/onscreen/onscreen/internal/api/v1"
	"github.com/onscreen/onscreen/internal/db/gen"
)

// stubIssuesDB satisfies v1.IssuesDB with one visible movie and an otherwise
// empty queue (no *arr instances configured).
type stubIssuesDB struct{ item gen.GetMediaItemRow }

func (s *stubIssuesDB) GetMediaItem(_ context.Context, id uuid.UUID) (gen.GetMediaItemRow, error) {
	if id != s.item.ID {
		return gen.GetMediaItemRow{}, pgx.ErrNoRows
	}
	return s.item, nil
}
func (s *stubIssuesDB) GetMediaFile(context.Context, uuid.UUID) (gen.MediaFile, error) {
	return gen.MediaFile{}, pgx.ErrNoRows
}
func (s *stubIssuesDB) CreateMediaIssue(_ context.Context, arg gen.CreateMediaIssueParams) (gen.MediaIssue, error) {
	return gen.MediaIssue{
		ID: uuid.New(), ItemID: arg.ItemID, UserID: arg.UserID, Kind: arg.Kind, Note: arg.Note, Status: "open",
		CreatedAt: pgtype.Timestamptz{Time: time.Now(), Valid: true},
	}, nil
}
func (s *stubIssuesDB) GetMediaIssue(context.Context, uuid.UUID) (gen.MediaIssue, error) {
	return gen.MediaIssue{}, pgx.ErrNoRows
}
func (s *stubIssuesDB) CountOpenMediaIssuesForUser(context.Context, uuid.UUID) (int32, error) {
	return 0, nil
}
func (s *stubIssuesDB) GetOpenMediaIssueForUserItemKind(context.Context, gen.GetOpenMediaIssueForUserItemKindParams) (gen.MediaIssue, error) {
	return gen.MediaIssue{}, pgx.ErrNoRows
}
func (s *stubIssuesDB) ListMediaIssuesForUserItem(context.Context, gen.ListMediaIssuesForUserItemParams) ([]gen.MediaIssue, error) {
	return nil, nil
}
func (s *stubIssuesDB) ListMediaIssuesAdmin(context.Context, gen.ListMediaIssuesAdminParams) ([]gen.ListMediaIssuesAdminRow, error) {
	return nil, nil
}
func (s *stubIssuesDB) CountMediaIssuesAdmin(context.Context, *string) (int32, error) { return 0, nil }
func (s *stubIssuesDB) CloseMediaIssue(context.Context, gen.CloseMediaIssueParams) (gen.MediaIssue, error) {
	return gen.MediaIssue{}, pgx.ErrNoRows
}
func (s *stubIssuesDB) ListDamagedMediaFiles(context.Context, gen.ListDamagedMediaFilesParams) ([]gen.ListDamagedMediaFilesRow, error) {
	return nil, nil
}
func (s *stubIssuesDB) CountDamagedMediaFiles(context.Context) (int32, error)      { return 0, nil }
func (s *stubIssuesDB) CountMediaItemsMissingArt(context.Context) (int32, error)   { return 0, nil }
func (s *stubIssuesDB) CountUnmatchedTopLevelItems(context.Context) (int32, error) { return 0, nil }
func (s *stubIssuesDB) ListEnabledArrServicesByKind(context.Context, string) ([]gen.ArrService, error) {
	return nil, nil
}

func newIssuesServer(t *testing.T) (*testServer, uuid.UUID) {
	t.Helper()
	tmdb := int32(949)
	pg := "PG"
	item := gen.GetMediaItemRow{ID: uuid.New(), LibraryID: uuid.New(), Type: "movie", Title: "Heat", TmdbID: &tmdb, ContentRating: &pg}
	ts := newExtrasServer(t, func(h *api.Handlers) {
		h.Issues = v1.NewIssueHandler(&stubIssuesDB{item: item}, allowAllLibAccess{}, slog.Default())
	})
	return ts, item.ID
}

// TestIssues_UserRoutesRequireAuth: any signed-in user can report a problem
// and read their own reports; anonymous callers get 401.
func TestIssues_UserRoutesRequireAuth(t *testing.T) {
	ts, itemID := newIssuesServer(t)
	path := "/api/v1/items/" + itemID.String() + "/issues"

	resp := ts.do("GET", path, "", nil)
	assertStatus(t, resp, http.StatusUnauthorized)
	resp.Body.Close()
	resp = ts.do("POST", path, "", map[string]any{"kind": "video"})
	assertStatus(t, resp, http.StatusUnauthorized)
	resp.Body.Close()

	resp = ts.do("GET", path, ts.userToken(), nil)
	assertStatus(t, resp, http.StatusOK)
	resp.Body.Close()
	resp = ts.do("POST", path, ts.userToken(), map[string]any{"kind": "video", "note": "green frames"})
	assertStatus(t, resp, http.StatusCreated)
	var env struct {
		Data v1.IssueResponse `json:"data"`
	}
	mustDecode(t, resp, &env)
	if env.Data.Kind != "video" || env.Data.Status != "open" || env.Data.ItemID != itemID.String() {
		t.Errorf("created = %+v", env.Data)
	}

	// Unknown item: 404, not a created report.
	resp = ts.do("POST", "/api/v1/items/"+uuid.New().String()+"/issues", ts.userToken(), map[string]any{"kind": "video"})
	assertStatus(t, resp, http.StatusNotFound)
	resp.Body.Close()
}

// TestIssues_AdminRoutesAdminOnly proves the queue, Library health and
// re-grab routes sit behind AdminRequired: anonymous → 401, a regular user
// → 403, an admin reaches the handler.
func TestIssues_AdminRoutesAdminOnly(t *testing.T) {
	ts, itemID := newIssuesServer(t)
	routes := []struct {
		method, path string
		body         any
		adminWant    int
	}{
		{"GET", "/api/v1/admin/issues", nil, http.StatusOK},
		{"PATCH", "/api/v1/admin/issues/" + uuid.New().String(), map[string]any{"status": "resolved"}, http.StatusNotFound},
		{"GET", "/api/v1/admin/library-health", nil, http.StatusOK},
		// No *arr instance configured: the handler answers NOT_MANAGED.
		{"POST", "/api/v1/admin/items/" + itemID.String() + "/regrab", map[string]any{"blocklist": true}, http.StatusConflict},
	}
	for _, rt := range routes {
		resp := ts.do(rt.method, rt.path, "", rt.body)
		if resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("%s %s anonymous: status = %d, want 401", rt.method, rt.path, resp.StatusCode)
		}
		resp.Body.Close()

		resp = ts.do(rt.method, rt.path, ts.userToken(), rt.body)
		if resp.StatusCode != http.StatusForbidden {
			t.Errorf("%s %s as non-admin: status = %d, want 403", rt.method, rt.path, resp.StatusCode)
		}
		resp.Body.Close()

		resp = ts.do(rt.method, rt.path, ts.adminToken(), rt.body)
		if resp.StatusCode != rt.adminWant {
			t.Errorf("%s %s as admin: status = %d, want %d", rt.method, rt.path, resp.StatusCode, rt.adminWant)
		}
		resp.Body.Close()
	}
}

func TestIssues_LibraryHealthShape(t *testing.T) {
	ts, _ := newIssuesServer(t)
	resp := ts.do("GET", "/api/v1/admin/library-health", ts.adminToken(), nil)
	assertStatus(t, resp, http.StatusOK)
	var env struct {
		Data map[string]any `json:"data"`
	}
	mustDecode(t, resp, &env)
	for _, k := range []string{"open_issues", "damaged_files", "damaged_total", "unmatched_count", "missing_art_count"} {
		if _, ok := env.Data[k]; !ok {
			t.Errorf("library-health missing %q: %v", k, env.Data)
		}
	}
	if files, ok := env.Data["damaged_files"].([]any); !ok || len(files) != 0 {
		t.Errorf("damaged_files = %v, want []", env.Data["damaged_files"])
	}
}
