package v1

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onscreen/onscreen/internal/api/middleware"
	"github.com/onscreen/onscreen/internal/auth"
	"github.com/onscreen/onscreen/internal/db/gen"
)

type akRow struct {
	gen.CreateAPIKeyParams
	id      uuid.UUID
	created time.Time
}

// akDB is an in-memory APIKeysDB.
type akDB struct {
	rows []akRow
}

func (d *akDB) CreateAPIKey(_ context.Context, arg gen.CreateAPIKeyParams) (gen.CreateAPIKeyRow, error) {
	r := akRow{CreateAPIKeyParams: arg, id: uuid.New(), created: time.Now()}
	d.rows = append(d.rows, r)
	return gen.CreateAPIKeyRow{ID: r.id, UserID: arg.UserID, Name: arg.Name, KeyPrefix: arg.KeyPrefix, Scope: arg.Scope,
		CreatedAt: pgtype.Timestamptz{Time: r.created, Valid: true}, ExpiresAt: arg.ExpiresAt}, nil
}

func (d *akDB) CountAPIKeysForUser(_ context.Context, userID uuid.UUID) (int64, error) {
	var n int64
	for _, r := range d.rows {
		if r.UserID == userID {
			n++
		}
	}
	return n, nil
}

func (d *akDB) ListAPIKeysForUser(_ context.Context, userID uuid.UUID) ([]gen.ListAPIKeysForUserRow, error) {
	var out []gen.ListAPIKeysForUserRow
	for _, r := range d.rows {
		if r.UserID == userID {
			out = append(out, gen.ListAPIKeysForUserRow{ID: r.id, UserID: r.UserID, Name: r.Name, KeyPrefix: r.KeyPrefix,
				Scope: r.Scope, CreatedAt: pgtype.Timestamptz{Time: r.created, Valid: true}, ExpiresAt: r.ExpiresAt})
		}
	}
	return out, nil
}

func (d *akDB) ListAllAPIKeys(context.Context) ([]gen.ListAllAPIKeysRow, error) {
	var out []gen.ListAllAPIKeysRow
	for _, r := range d.rows {
		out = append(out, gen.ListAllAPIKeysRow{ID: r.id, UserID: r.UserID, Username: "u", Name: r.Name,
			KeyPrefix: r.KeyPrefix, Scope: r.Scope, CreatedAt: pgtype.Timestamptz{Time: r.created, Valid: true}})
	}
	return out, nil
}

func (d *akDB) DeleteAPIKeyForUser(_ context.Context, arg gen.DeleteAPIKeyForUserParams) (gen.DeleteAPIKeyForUserRow, error) {
	for i, r := range d.rows {
		if r.id == arg.ID && r.UserID == arg.UserID {
			d.rows = append(d.rows[:i], d.rows[i+1:]...)
			return gen.DeleteAPIKeyForUserRow{ID: r.id, UserID: r.UserID, Name: r.Name, KeyPrefix: r.KeyPrefix}, nil
		}
	}
	return gen.DeleteAPIKeyForUserRow{}, pgx.ErrNoRows
}

func (d *akDB) DeleteAPIKey(_ context.Context, id uuid.UUID) (gen.DeleteAPIKeyRow, error) {
	for i, r := range d.rows {
		if r.id == id {
			d.rows = append(d.rows[:i], d.rows[i+1:]...)
			return gen.DeleteAPIKeyRow{ID: r.id, UserID: r.UserID, Name: r.Name, KeyPrefix: r.KeyPrefix}, nil
		}
	}
	return gen.DeleteAPIKeyRow{}, pgx.ErrNoRows
}

func akDo(h http.HandlerFunc, claims *auth.Claims, method, id, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, "/", strings.NewReader(body))
	if id != "" {
		r = withChiParam(r, "id", id)
	}
	r = r.WithContext(middleware.WithClaims(r.Context(), claims))
	w := httptest.NewRecorder()
	h(w, r)
	return w
}

func TestAPIKeys_CreateShowsKeyOnce(t *testing.T) {
	db := &akDB{}
	h := NewAPIKeyHandler(db, slog.New(slog.NewTextHandler(io.Discard, nil)))
	alice := &auth.Claims{UserID: uuid.New()}

	w := akDo(h.CreateMine, alice, http.MethodPost, "", `{"name":"  Home Assistant  ","scope":"read_only"}`)
	if w.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", w.Code, w.Body.String())
	}
	var created struct {
		Data createdAPIKeyJSON `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	k := created.Data
	if !auth.LooksLikeAPIKey(k.Key) || k.Name != "Home Assistant" || k.Scope != "read_only" || !strings.HasPrefix(k.Key, k.Prefix) {
		t.Fatalf("created: %+v", k)
	}
	// Only the hash is stored.
	if len(db.rows) != 1 || db.rows[0].KeyHash != auth.HashAPIKey(k.Key) || strings.Contains(db.rows[0].KeyHash, k.Key) {
		t.Fatalf("stored row: %+v", db.rows)
	}

	// The list never carries the key.
	w = akDo(h.ListMine, alice, http.MethodGet, "", "")
	if strings.Contains(w.Body.String(), k.Key) || !strings.Contains(w.Body.String(), k.Prefix) {
		t.Fatalf("list leaks the key or lacks the prefix: %s", w.Body.String())
	}

	// Default scope is full.
	w = akDo(h.CreateMine, alice, http.MethodPost, "", `{"name":"Kometa"}`)
	if w.Code != http.StatusCreated || !strings.Contains(w.Body.String(), `"scope":"full"`) {
		t.Fatalf("default scope: %d %s", w.Code, w.Body.String())
	}
}

func TestAPIKeys_CreateValidation(t *testing.T) {
	db := &akDB{}
	h := NewAPIKeyHandler(db, slog.New(slog.NewTextHandler(io.Discard, nil)))
	alice := &auth.Claims{UserID: uuid.New()}
	past := time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)
	for _, body := range []string{
		`{}`,
		`{"name":"   "}`,
		`{"name":"` + strings.Repeat("x", 101) + `"}`,
		`{"name":"a","scope":"admin"}`,
		`{"name":"a","expires_at":"` + past + `"}`,
		`nope`,
	} {
		if w := akDo(h.CreateMine, alice, http.MethodPost, "", body); w.Code != http.StatusBadRequest && w.Code != http.StatusUnprocessableEntity {
			t.Errorf("%.40q: status %d", body, w.Code)
		}
	}
	future := time.Now().Add(48 * time.Hour).UTC().Format(time.RFC3339)
	if w := akDo(h.CreateMine, alice, http.MethodPost, "", `{"name":"a","expires_at":"`+future+`"}`); w.Code != http.StatusCreated {
		t.Fatalf("future expiry: %d", w.Code)
	}
	if !db.rows[0].ExpiresAt.Valid {
		t.Fatal("expiry not stored")
	}

	// Per-user cap.
	for len(db.rows) < maxAPIKeysPerUser {
		db.rows = append(db.rows, akRow{CreateAPIKeyParams: gen.CreateAPIKeyParams{UserID: alice.UserID}, id: uuid.New()})
	}
	if w := akDo(h.CreateMine, alice, http.MethodPost, "", `{"name":"one too many"}`); w.Code != http.StatusConflict {
		t.Fatalf("cap: %d", w.Code)
	}
}

func TestAPIKeys_DeleteScopedToOwnerAdminCanRevokeAny(t *testing.T) {
	db := &akDB{}
	h := NewAPIKeyHandler(db, slog.New(slog.NewTextHandler(io.Discard, nil)))
	alice := &auth.Claims{UserID: uuid.New()}
	bob := &auth.Claims{UserID: uuid.New()}
	admin := &auth.Claims{UserID: uuid.New(), IsAdmin: true}
	akDo(h.CreateMine, alice, http.MethodPost, "", `{"name":"a1"}`)
	akDo(h.CreateMine, alice, http.MethodPost, "", `{"name":"a2"}`)
	a1, a2 := db.rows[0].id.String(), db.rows[1].id.String()

	if w := akDo(h.DeleteMine, bob, http.MethodDelete, a1, ""); w.Code != http.StatusNotFound {
		t.Fatalf("bob deleting alice's key: %d", w.Code)
	}
	if w := akDo(h.DeleteMine, alice, http.MethodDelete, a1, ""); w.Code != http.StatusNoContent {
		t.Fatalf("alice deleting her key: %d", w.Code)
	}
	if w := akDo(h.DeleteMine, alice, http.MethodDelete, "not-a-uuid", ""); w.Code != http.StatusBadRequest {
		t.Fatalf("bad id: %d", w.Code)
	}

	if w := akDo(h.AdminList, bob, http.MethodGet, "", ""); w.Code != http.StatusForbidden {
		t.Fatalf("non-admin list: %d", w.Code)
	}
	if w := akDo(h.AdminDelete, bob, http.MethodDelete, a2, ""); w.Code != http.StatusForbidden {
		t.Fatalf("non-admin delete: %d", w.Code)
	}
	w := akDo(h.AdminList, admin, http.MethodGet, "", "")
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), alice.UserID.String()) {
		t.Fatalf("admin list: %d %s", w.Code, w.Body.String())
	}
	if w := akDo(h.AdminDelete, admin, http.MethodDelete, a2, ""); w.Code != http.StatusNoContent {
		t.Fatalf("admin delete: %d", w.Code)
	}
	if len(db.rows) != 0 {
		t.Fatalf("rows left: %d", len(db.rows))
	}
	if w := akDo(h.AdminDelete, admin, http.MethodDelete, a2, ""); w.Code != http.StatusNotFound {
		t.Fatalf("admin delete twice: %d", w.Code)
	}
}

func TestAPIKeys_ExpiredFlag(t *testing.T) {
	h := NewAPIKeyHandler(&akDB{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	past := pgtype.Timestamptz{Time: time.Now().Add(-time.Minute), Valid: true}
	if !h.toJSON(uuid.New(), "n", "osk_x", "full", past, pgtype.Timestamptz{}, past).Expired {
		t.Fatal("past expiry not flagged")
	}
	if h.toJSON(uuid.New(), "n", "osk_x", "full", past, pgtype.Timestamptz{}, pgtype.Timestamptz{}).Expired {
		t.Fatal("no expiry flagged expired")
	}
}
