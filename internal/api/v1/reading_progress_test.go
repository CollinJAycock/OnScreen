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

	"github.com/onscreen/onscreen/internal/api/middleware"
	"github.com/onscreen/onscreen/internal/auth"
	"github.com/onscreen/onscreen/internal/db/gen"
)

// rpDB is an in-memory ReadingProgressDB with the same newest-wins rule as
// the SQL.
type rpDB struct {
	items    map[uuid.UUID]gen.GetMediaItemRow
	rows     map[[2]uuid.UUID]gen.ReadingProgress
	upserts  []gen.UpsertReadingProgressParams
	markRead []gen.MarkItemReadParams
}

func newRPDB() *rpDB {
	return &rpDB{items: map[uuid.UUID]gen.GetMediaItemRow{}, rows: map[[2]uuid.UUID]gen.ReadingProgress{}}
}

func (d *rpDB) GetMediaItem(_ context.Context, id uuid.UUID) (gen.GetMediaItemRow, error) {
	it, ok := d.items[id]
	if !ok {
		return gen.GetMediaItemRow{}, pgx.ErrNoRows
	}
	return it, nil
}

func (d *rpDB) GetReadingProgress(_ context.Context, arg gen.GetReadingProgressParams) (gen.ReadingProgress, error) {
	row, ok := d.rows[[2]uuid.UUID{arg.UserID, arg.MediaID}]
	if !ok {
		return gen.ReadingProgress{}, pgx.ErrNoRows
	}
	return row, nil
}

func (d *rpDB) UpsertReadingProgress(_ context.Context, arg gen.UpsertReadingProgressParams) (int64, error) {
	d.upserts = append(d.upserts, arg)
	k := [2]uuid.UUID{arg.UserID, arg.MediaID}
	if cur, ok := d.rows[k]; ok && !cur.ClientUpdatedAt.Time.Before(arg.ClientUpdatedAt.Time) {
		return 0, nil
	}
	d.rows[k] = gen.ReadingProgress{
		UserID: arg.UserID, MediaID: arg.MediaID, Locator: arg.Locator, Progression: arg.Progression,
		DeviceName: arg.DeviceName, ClientUpdatedAt: arg.ClientUpdatedAt,
	}
	return 1, nil
}

func (d *rpDB) MarkItemRead(_ context.Context, arg gen.MarkItemReadParams) error {
	d.markRead = append(d.markRead, arg)
	return nil
}

// rpAccess grants every library except deny.
type rpAccess struct{ deny uuid.UUID }

func (a rpAccess) CanAccessLibrary(_ context.Context, _, lib uuid.UUID, _ bool) (bool, error) {
	return lib != a.deny, nil
}
func (a rpAccess) AllowedLibraryIDs(context.Context, uuid.UUID, bool) (map[uuid.UUID]struct{}, error) {
	return nil, nil
}

func rpItem(db *rpDB, typ, rating string, lib uuid.UUID) uuid.UUID {
	id := uuid.New()
	row := gen.GetMediaItemRow{ID: id, LibraryID: lib, Type: typ, Title: "x"}
	if rating != "" {
		row.ContentRating = &rating
	}
	db.items[id] = row
	return id
}

func rpDo(h http.HandlerFunc, claims *auth.Claims, method string, id uuid.UUID, body string) *httptest.ResponseRecorder {
	var r *http.Request
	if body == "" {
		r = httptest.NewRequest(method, "/", nil)
	} else {
		r = httptest.NewRequest(method, "/", strings.NewReader(body))
	}
	r = withChiParam(r, "id", id.String())
	r = r.WithContext(middleware.WithClaims(r.Context(), claims))
	w := httptest.NewRecorder()
	h(w, r)
	return w
}

type rpEnvelope struct {
	Data *readingProgressJSON `json:"data"`
}

func TestReadingProgress_SaveLoadNewestWins(t *testing.T) {
	db := newRPDB()
	now := time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)
	h := NewReadingProgressHandler(db, rpAccess{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	h.now = func() time.Time { return now }
	user := &auth.Claims{UserID: uuid.New()}
	book := rpItem(db, "book", "", uuid.New())

	// Nothing saved yet: data null.
	w := rpDo(h.Get, user, http.MethodGet, book, "")
	if w.Code != http.StatusOK || strings.TrimSpace(w.Body.String()) != `{"data":null}` {
		t.Fatalf("empty get: %d %s", w.Code, w.Body.String())
	}

	w = rpDo(h.Put, user, http.MethodPut, book,
		`{"locator":"epubcfi(/6/14!/4/2)","progression":0.4,"device_name":"Tablet","updated_at":"2026-10-14T11:00:00Z"}`)
	var env rpEnvelope
	_ = json.Unmarshal(w.Body.Bytes(), &env)
	if w.Code != http.StatusOK || env.Data == nil || !*env.Data.Applied || env.Data.DeviceName != "Tablet" {
		t.Fatalf("first put: %d %s", w.Code, w.Body.String())
	}

	// An older position from another device doesn't rewind it; the response
	// carries the newer one.
	w = rpDo(h.Put, user, http.MethodPut, book,
		`{"locator":"12","progression":0.1,"device_name":"Phone","updated_at":"2026-10-14T10:00:00Z"}`)
	env = rpEnvelope{}
	_ = json.Unmarshal(w.Body.Bytes(), &env)
	if env.Data == nil || *env.Data.Applied || env.Data.Locator != "epubcfi(/6/14!/4/2)" || env.Data.DeviceName != "Tablet" {
		t.Fatalf("stale put replaced the newer position: %s", w.Body.String())
	}

	// A future timestamp is clamped to the server's clock, so it can't pin
	// the position against later saves.
	w = rpDo(h.Put, user, http.MethodPut, book, `{"locator":"20","progression":0.5,"updated_at":"2030-01-01T00:00:00Z"}`)
	env = rpEnvelope{}
	_ = json.Unmarshal(w.Body.Bytes(), &env)
	if env.Data == nil || !env.Data.UpdatedAt.Equal(now) {
		t.Fatalf("future timestamp not clamped: %s", w.Body.String())
	}
	now = now.Add(time.Minute)
	w = rpDo(h.Put, user, http.MethodPut, book, `{"locator":"21","progression":0.51}`)
	env = rpEnvelope{}
	_ = json.Unmarshal(w.Body.Bytes(), &env)
	if env.Data == nil || env.Data.Locator != "21" {
		t.Fatalf("save after a clamped future one was refused: %s", w.Body.String())
	}

	w = rpDo(h.Get, user, http.MethodGet, book, "")
	env = rpEnvelope{}
	_ = json.Unmarshal(w.Body.Bytes(), &env)
	if env.Data == nil || env.Data.Locator != "21" || env.Data.Applied != nil {
		t.Fatalf("get: %s", w.Body.String())
	}
	if len(db.markRead) != 0 {
		t.Fatal("marked read before the end")
	}

	// Another user's position is separate.
	other := &auth.Claims{UserID: uuid.New()}
	w = rpDo(h.Get, other, http.MethodGet, book, "")
	if strings.TrimSpace(w.Body.String()) != `{"data":null}` {
		t.Fatalf("other user sees the position: %s", w.Body.String())
	}
}

func TestReadingProgress_FinishMarksRead(t *testing.T) {
	db := newRPDB()
	h := NewReadingProgressHandler(db, rpAccess{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	user := &auth.Claims{UserID: uuid.New()}
	book := rpItem(db, "book", "", uuid.New())
	if w := rpDo(h.Put, user, http.MethodPut, book, `{"locator":"99","progression":0.97}`); w.Code != http.StatusOK {
		t.Fatalf("put: %d", w.Code)
	}
	if len(db.markRead) != 0 {
		t.Fatal("0.97 marked read")
	}
	if w := rpDo(h.Put, user, http.MethodPut, book, `{"locator":"100","progression":0.98}`); w.Code != http.StatusOK {
		t.Fatalf("put: %d", w.Code)
	}
	if len(db.markRead) != 1 || db.markRead[0].UserID != user.UserID || db.markRead[0].MediaID != book {
		t.Fatalf("finish not marked: %+v", db.markRead)
	}
}

func TestReadingProgress_Validation(t *testing.T) {
	db := newRPDB()
	h := NewReadingProgressHandler(db, rpAccess{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	user := &auth.Claims{UserID: uuid.New()}
	book := rpItem(db, "book", "", uuid.New())
	for _, body := range []string{
		`{"progression":0.5}`,
		`{"locator":"  ","progression":0.5}`,
		`{"locator":"1"}`,
		`{"locator":"1","progression":-0.1}`,
		`{"locator":"1","progression":1.5}`,
		`{"locator":"` + strings.Repeat("a", 4097) + `","progression":0.5}`,
		`not json`,
	} {
		if w := rpDo(h.Put, user, http.MethodPut, book, body); w.Code != http.StatusBadRequest && w.Code != http.StatusUnprocessableEntity {
			t.Errorf("body %.40q: status %d", body, w.Code)
		}
	}
	if len(db.upserts) != 0 {
		t.Fatal("invalid body was stored")
	}
}

// The item gate: anything that isn't a book the caller may see is a 404, on
// read and write alike — library access, rating ceiling, item type.
func TestReadingProgress_VisibilityGate(t *testing.T) {
	db := newRPDB()
	hidden := uuid.New()
	h := NewReadingProgressHandler(db, rpAccess{deny: hidden}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	kid := &auth.Claims{UserID: uuid.New(), MaxContentRating: "PG"}
	cases := map[string]uuid.UUID{
		"other library":    rpItem(db, "book", "", hidden),
		"over ceiling":     rpItem(db, "book", "R", uuid.New()),
		"unrated (rank 4)": rpItem(db, "book", "", uuid.New()),
		"not a book":       rpItem(db, "movie", "G", uuid.New()),
		"no such item":     uuid.New(),
	}
	for name, id := range cases {
		for _, m := range []string{http.MethodGet, http.MethodPut} {
			fn := h.Get
			body := ""
			if m == http.MethodPut {
				fn, body = h.Put, `{"locator":"1","progression":0.5}`
			}
			if w := rpDo(fn, kid, m, id, body); w.Code != http.StatusNotFound {
				t.Errorf("%s %s: status %d", name, m, w.Code)
			}
		}
	}
	if len(db.upserts) != 0 {
		t.Fatal("a hidden item's position was stored")
	}
	ok := rpItem(db, "book", "G", uuid.New())
	if w := rpDo(h.Put, kid, http.MethodPut, ok, `{"locator":"1","progression":0.5}`); w.Code != http.StatusOK {
		t.Fatalf("visible book: %d", w.Code)
	}
}
