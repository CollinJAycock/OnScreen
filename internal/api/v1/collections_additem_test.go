package v1

import (
	"bytes"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/onscreen/onscreen/internal/api/middleware"
	"github.com/onscreen/onscreen/internal/auth"
	"github.com/onscreen/onscreen/internal/db/gen"
)

// AddCollectionItem is ON CONFLICT DO NOTHING ... RETURNING, so re-adding an
// item already in the collection yields pgx.ErrNoRows: a no-op (204), while
// any other insert failure still 500s.
func TestCollectionAddItem_AlreadyInCollectionIsNoOp(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		want int
	}{
		{"added", nil, http.StatusNoContent},
		{"duplicate", pgx.ErrNoRows, http.StatusNoContent},
		{"db failure", errors.New("connection reset"), http.StatusInternalServerError},
	} {
		t.Run(tc.name, func(t *testing.T) {
			col := gen.Collection{ID: uuid.New(), Name: "Favourites", Type: "collection"}
			db := &frCollectionDB{cols: map[uuid.UUID]gen.Collection{col.ID: col}, addErr: tc.err}
			r := chi.NewRouter()
			r.Post("/collections/{id}/items", NewCollectionHandler(db, slog.Default()).AddItem)

			body := []byte(`{"media_item_id":"` + uuid.NewString() + `"}`)
			req := httptest.NewRequest(http.MethodPost, "/collections/"+col.ID.String()+"/items", bytes.NewReader(body))
			req = req.WithContext(middleware.WithClaims(req.Context(), &auth.Claims{UserID: uuid.New(), IsAdmin: true}))
			rec := httptest.NewRecorder()
			r.ServeHTTP(rec, req)
			if rec.Code != tc.want {
				t.Fatalf("got %d, want %d; body=%s", rec.Code, tc.want, rec.Body.String())
			}
		})
	}
}
