package scanner

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/domain/media"
)

func strPtr(s string) *string { return &s }

func TestItemNeedsEnrich(t *testing.T) {
	tests := []struct {
		name string
		item *media.Item
		want bool
	}{
		{
			name: "movie with no poster needs enrich",
			item: &media.Item{Type: "movie"},
			want: true,
		},
		{
			name: "movie with bad poster path needs enrich",
			item: &media.Item{Type: "movie", PosterPath: strPtr("/etc/poster.jpg")},
			want: true,
		},
		{
			name: "movie with poster but no content rating is enriched (no longer retriggers)",
			item: &media.Item{Type: "movie", PosterPath: strPtr("movies/Foo (2020)/poster.jpg")},
			want: false,
		},
		{
			name: "movie with poster and content rating is enriched",
			item: &media.Item{Type: "movie", PosterPath: strPtr("movies/Foo (2020)/poster.jpg"), ContentRating: strPtr("PG")},
			want: false,
		},
		{
			name: "show with poster but no content rating is enriched",
			item: &media.Item{Type: "show", PosterPath: strPtr("tv/Foo/poster.jpg")},
			want: false,
		},
		{
			name: "episode with thumb is enriched",
			item: &media.Item{Type: "episode", ThumbPath: strPtr("tv/Foo/S01E01-thumb.jpg")},
			want: false,
		},
		{
			name: "episode with no thumb but has summary is enriched (TMDB had no thumb)",
			item: &media.Item{Type: "episode", Summary: strPtr("a real summary")},
			want: false,
		},
		{
			name: "episode with no thumb and no summary needs enrich",
			item: &media.Item{Type: "episode"},
			want: true,
		},
		{
			name: "photo never needs enrich",
			item: &media.Item{Type: "photo"},
			want: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := itemNeedsEnrich(tt.item); got != tt.want {
				t.Errorf("itemNeedsEnrich = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestShouldEnrich(t *testing.T) {
	libID := uuid.New()
	mkUnenriched := func(svc *mockMediaService) *media.Item {
		it := &media.Item{ID: uuid.New(), LibraryID: libID, Type: "movie", Title: "Junk"}
		svc.items[it.ID] = it
		return it
	}
	mkEnriched := func(svc *mockMediaService) *media.Item {
		it := &media.Item{
			ID:         uuid.New(),
			LibraryID:  libID,
			Type:       "movie",
			Title:      "Good",
			PosterPath: strPtr("movies/Good (2020)/poster.jpg"),
		}
		svc.items[it.ID] = it
		return it
	}

	t.Run("new items always enrich regardless of cooldown", func(t *testing.T) {
		svc := newMockMediaService()
		s := newTestScanner(svc)
		it := mkUnenriched(svc)
		svc.enrichAttempts = map[uuid.UUID]time.Time{it.ID: time.Now()}
		if !s.shouldEnrich(context.Background(), it, true) {
			t.Fatal("new item should always be enqueued for enrichment")
		}
	})

	t.Run("enriched item is skipped even with no prior attempt", func(t *testing.T) {
		svc := newMockMediaService()
		s := newTestScanner(svc)
		it := mkEnriched(svc)
		if s.shouldEnrich(context.Background(), it, false) {
			t.Fatal("fully enriched item should not re-enrich")
		}
	})

	t.Run("unenriched item with no prior attempt enriches", func(t *testing.T) {
		svc := newMockMediaService()
		s := newTestScanner(svc)
		it := mkUnenriched(svc)
		if !s.shouldEnrich(context.Background(), it, false) {
			t.Fatal("first attempt should run")
		}
	})

	t.Run("unenriched item with recent attempt is suppressed", func(t *testing.T) {
		svc := newMockMediaService()
		s := newTestScanner(svc)
		it := mkUnenriched(svc)
		svc.enrichAttempts = map[uuid.UUID]time.Time{it.ID: time.Now().Add(-1 * time.Hour)}
		if s.shouldEnrich(context.Background(), it, false) {
			t.Fatal("attempt within cooldown should be suppressed")
		}
	})

	t.Run("unenriched item past cooldown enriches again", func(t *testing.T) {
		svc := newMockMediaService()
		s := newTestScanner(svc)
		it := mkUnenriched(svc)
		svc.enrichAttempts = map[uuid.UUID]time.Time{
			it.ID: time.Now().Add(-(enrichCooldown + time.Hour)),
		}
		if !s.shouldEnrich(context.Background(), it, false) {
			t.Fatal("attempt past cooldown should retry")
		}
	})

	t.Run("attempt exactly at cooldown boundary retries", func(t *testing.T) {
		svc := newMockMediaService()
		s := newTestScanner(svc)
		it := mkUnenriched(svc)
		svc.enrichAttempts = map[uuid.UUID]time.Time{
			it.ID: time.Now().Add(-enrichCooldown),
		}
		if !s.shouldEnrich(context.Background(), it, false) {
			t.Fatal("attempt exactly at cooldown should retry (>= comparison)")
		}
	})
}
