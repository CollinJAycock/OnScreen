package v1

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/onscreen/onscreen/internal/db/gen"
	"github.com/onscreen/onscreen/internal/domain/media"
)

type fakeExternalRatings map[string]gen.ExternalRating

func (f fakeExternalRatings) GetExternalRatings(_ context.Context, id string) (gen.ExternalRating, error) {
	r, ok := f[id]
	if !ok {
		return gen.ExternalRating{}, pgx.ErrNoRows
	}
	return r, nil
}

func TestExternalRatingsFor(t *testing.T) {
	score, rt := 8.5, int16(93)
	db := fakeExternalRatings{
		"tt1": {ImdbID: "tt1", ImdbRating: &score, RottenTomatoes: &rt, Found: true},
		"tt2": {ImdbID: "tt2", Found: false},
		"tt3": {ImdbID: "tt3", Found: true}, // found, but OMDb had no scores
	}
	h := (&ItemHandler{}).WithExternalRatings(db)
	id := func(s string) *string { return &s }
	cases := []struct {
		name string
		item media.Item
		want bool
	}{
		{"movie with scores", media.Item{Type: "movie", IMDBID: id("tt1")}, true},
		{"show with scores", media.Item{Type: "show", IMDBID: id("tt1")}, true},
		{"episode: no", media.Item{Type: "episode", IMDBID: id("tt1")}, false},
		{"no IMDb ID", media.Item{Type: "movie"}, false},
		{"not fetched yet", media.Item{Type: "movie", IMDBID: id("tt9")}, false},
		{"unknown to OMDb", media.Item{Type: "movie", IMDBID: id("tt2")}, false},
		{"no scores", media.Item{Type: "movie", IMDBID: id("tt3")}, false},
	}
	for _, c := range cases {
		got := h.externalRatingsFor(context.Background(), &c.item)
		if (got != nil) != c.want {
			t.Errorf("%s: got %+v", c.name, got)
		}
	}
	got := h.externalRatingsFor(context.Background(), &media.Item{Type: "movie", IMDBID: id("tt1")})
	if got.IMDbID != "tt1" || *got.IMDbRating != 8.5 || *got.RottenTomatoes != 93 || got.Metacritic != nil {
		t.Errorf("scores = %+v", got)
	}
	if (&ItemHandler{}).externalRatingsFor(context.Background(), &media.Item{Type: "movie", IMDBID: id("tt1")}) != nil {
		t.Error("an unwired handler returned scores")
	}
}
