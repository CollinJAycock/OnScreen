package v1

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/domain/media"
)

// Item JSON: files[] carry edition and part_index (part_index only for a
// real stack — a lone "cd1" omits it), and a stacked movie's duration_ms is
// the parts' sum. Every other field keeps its shape.
func TestItemGet_EditionsAndParts(t *testing.T) {
	id := uuid.New()
	itemDur := int64(5_000_000)
	d1, d2, d3 := int64(3_000_000), int64(2_400_000), int64(7_000_000)
	dc := "Director's Cut"
	mk := func(path string, part *int, edition *string, dur *int64) media.File {
		return media.File{ID: uuid.New(), MediaItemID: id, Status: "active", FilePath: path,
			PartIndex: part, Edition: edition, DurationMS: dur}
	}
	cd1 := mk("/m/Movie (2001)/Movie (2001) cd1.mkv", ip(1), nil, &d1)
	cd2 := mk("/m/Movie (2001)/Movie (2001) cd2.mkv", ip(2), nil, &d2)
	lone := mk("/m/Movie (2001)/Movie (2001) - Director's Cut cd1.mkv", ip(1), &dc, &d3)
	ms := &mockItemMedia{
		item:  &media.Item{ID: id, Title: "Movie", Type: "movie", DurationMS: &itemDur},
		files: []media.File{cd1, cd2, lone},
	}
	h := newItemHandler(ms)
	rec := httptest.NewRecorder()
	h.Get(rec, withChiParam(httptest.NewRequest("GET", "/api/v1/items/"+id.String(), nil), "id", id.String()))
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Data struct {
			DurationMS int64            `json:"duration_ms"`
			Files      []map[string]any `json:"files"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Data.DurationMS != 5_400_000 {
		t.Errorf("item duration_ms = %d, want the parts' sum 5400000", resp.Data.DurationMS)
	}
	f := resp.Data.Files
	if len(f) != 3 {
		t.Fatalf("files = %d", len(f))
	}
	if f[0]["part_index"] != float64(1) || f[1]["part_index"] != float64(2) {
		t.Errorf("part_index = %v, %v; want 1, 2", f[0]["part_index"], f[1]["part_index"])
	}
	if _, ok := f[2]["part_index"]; ok {
		t.Errorf("a lone cd1 must omit part_index: %v", f[2])
	}
	if _, ok := f[0]["edition"]; ok {
		t.Errorf("no edition must omit the field: %v", f[0])
	}
	if f[2]["edition"] != dc {
		t.Errorf("edition = %v, want %q", f[2]["edition"], dc)
	}
	if f[0]["duration_ms"] != float64(d1) {
		t.Errorf("a part's own duration_ms = %v, want %d", f[0]["duration_ms"], d1)
	}

	// A progress beat without a duration, from a player on part 2, is
	// judged against the whole movie, not part 2's length.
	if got := playableDurationMS(ms.files, cd2); got == nil || *got != 5_400_000 {
		t.Errorf("playableDurationMS(cd2) = %v, want 5400000", got)
	}
	if got := playableDurationMS(ms.files, lone); got == nil || *got != d3 {
		t.Errorf("playableDurationMS(lone) = %v, want its own %d", got, d3)
	}
}
