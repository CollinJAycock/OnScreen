package v1

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/domain/media"
)

type fakeChapterThumbs struct {
	fileID uuid.UUID
	count  int
}

func (f fakeChapterThumbs) ChapterThumbs(context.Context, uuid.UUID) (uuid.UUID, int, error) {
	return f.fileID, f.count, nil
}

// Chapters of the file the trickplay sprites came from carry thumb_url (up
// to the generated count); other files' chapters, and a handler with no
// lookup, carry none — the field is additive.
func TestItemGet_ChapterThumbURL(t *testing.T) {
	id := uuid.New()
	primary, other := uuid.New(), uuid.New()
	chapters := []byte(`[{"title":"A","start_ms":0,"end_ms":1000},{"title":"B","start_ms":1000,"end_ms":2000},{"title":"C","start_ms":2000,"end_ms":3000}]`)
	ms := &mockItemMedia{
		item: &media.Item{ID: id, Title: "Film", Type: "movie"},
		files: []media.File{
			{ID: primary, Status: "active", FilePath: "/film.mkv", Chapters: chapters},
			{ID: other, Status: "active", FilePath: "/film-extended.mkv", Chapters: chapters},
		},
	}
	get := func(h *ItemHandler) []ItemFileResponse {
		rec := httptest.NewRecorder()
		req := withChiParam(httptest.NewRequest("GET", "/api/v1/items/"+id.String(), nil), "id", id.String())
		h.Get(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("status %d", rec.Code)
		}
		var resp struct {
			Data struct {
				Files []ItemFileResponse `json:"files"`
			} `json:"data"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
			t.Fatal(err)
		}
		if len(resp.Data.Files) != 2 {
			t.Fatalf("files = %d, want 2", len(resp.Data.Files))
		}
		return resp.Data.Files
	}

	files := get(newItemHandler(ms).WithChapterThumbs(fakeChapterThumbs{fileID: primary, count: 2}))
	want := []string{
		"/trickplay/" + id.String() + "/chapter_000.jpg",
		"/trickplay/" + id.String() + "/chapter_001.jpg",
		"",
	}
	for i, c := range files[0].Chapters {
		if c.ThumbURL != want[i] {
			t.Errorf("chapter %d thumb_url = %q, want %q", i, c.ThumbURL, want[i])
		}
	}
	for _, c := range files[1].Chapters {
		if c.ThumbURL != "" {
			t.Errorf("other file's chapter got thumb_url %q", c.ThumbURL)
		}
	}
	for _, c := range get(newItemHandler(ms))[0].Chapters {
		if c.ThumbURL != "" {
			t.Errorf("no lookup wired, still got thumb_url %q", c.ThumbURL)
		}
	}
}
