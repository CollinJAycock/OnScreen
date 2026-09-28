package v1

import (
	"encoding/json"
	"slices"
	"testing"

	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/auth"
)

// The same film in two libraries (4K + 1080p) is ONE part: item_id links the
// lowest id and item_ids lists every copy the caller can see, so the web
// collection page can fold the items listing's second copy into the part
// instead of appending it as another film.
func TestCollectionGet_FranchisePartListsEveryVisibleCopy(t *testing.T) {
	alien4K := uuid.MustParse("00000000-0000-0000-0000-000000000001")
	f := newFrFixture(t, frAccess{allowed: map[uuid.UUID]struct{}{frLibA: {}}}, frReqs{})
	f.frDB.items = append(f.frDB.items, frItem{id: alien4K, lib: frLibB, tmdb: 348, rating: "R"})

	type part struct {
		TMDBID  int              `json:"tmdb_id"`
		ItemID  *string          `json:"item_id"`
		ItemIDs *json.RawMessage `json:"item_ids"`
	}
	decode := func(claims *auth.Claims) []part {
		t.Helper()
		data, _ := frDecodeDetail(t, f.do(t, "GET", "/collections/"+f.col.ID.String(), claims, nil))
		var parts []part
		if err := json.Unmarshal(data["parts"], &parts); err != nil {
			t.Fatal(err)
		}
		return parts
	}
	ids := func(p part) []string {
		t.Helper()
		if p.ItemIDs == nil {
			return nil
		}
		var out []string
		if err := json.Unmarshal(*p.ItemIDs, &out); err != nil {
			t.Fatal(err)
		}
		return out
	}

	// Admin sees both copies.
	parts := decode(&auth.Claims{UserID: uuid.New(), IsAdmin: true})
	if parts[0].TMDBID != 348 || parts[0].ItemID == nil || *parts[0].ItemID != alien4K.String() {
		t.Fatalf("Alien must link the lowest copy id: %+v", parts[0])
	}
	if got, want := ids(parts[0]), []string{alien4K.String(), frAlien.String()}; !slices.Equal(got, want) {
		t.Errorf("Alien item_ids = %v, want %v", got, want)
	}
	for _, p := range parts {
		if p.ItemID == nil && p.ItemIDs != nil {
			t.Errorf("missing part %d must omit item_ids, got %s", p.TMDBID, *p.ItemIDs)
		}
	}

	// A caller granted only library A never learns the library-B copy's id.
	parts = decode(&auth.Claims{UserID: uuid.New()})
	if got, want := ids(parts[0]), []string{frAlien.String()}; !slices.Equal(got, want) {
		t.Errorf("restricted caller's Alien item_ids = %v, want %v", got, want)
	}
	if parts[0].ItemID == nil || *parts[0].ItemID != frAlien.String() {
		t.Errorf("restricted caller's Alien item_id = %v, want the library-A copy", parts[0].ItemID)
	}
}
