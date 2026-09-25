package v1

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/api/middleware"
	"github.com/onscreen/onscreen/internal/auth"
	"github.com/onscreen/onscreen/internal/domain/media"
)

// EXIF carries GPS coordinates. A restricted profile that /photos/{id}/image
// refuses (unrated ranks most restrictive) must be refused the EXIF too.
func TestGetEXIF_AppliesRatingCeiling(t *testing.T) {
	id := uuid.New()
	h := newItemHandler(&mockItemMedia{item: &media.Item{ID: id, LibraryID: uuid.New(), Type: "photo"}})
	get := func(maxRating string) int {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/items/"+id.String()+"/exif", nil)
		req = withChiParam(req, "id", id.String())
		req = req.WithContext(middleware.WithClaims(req.Context(), &auth.Claims{UserID: uuid.New(), MaxContentRating: maxRating}))
		rec := httptest.NewRecorder()
		h.GetEXIF(rec, req)
		return rec.Code
	}
	if got := get("PG"); got != http.StatusNotFound {
		t.Errorf("restricted profile, unrated photo: got %d, want 404", got)
	}
	if got := get(""); got != http.StatusOK {
		t.Errorf("unrestricted caller: got %d, want 200", got)
	}
}
