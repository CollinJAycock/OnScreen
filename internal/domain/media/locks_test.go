package media

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/google/uuid"
)

func TestIsLockableField(t *testing.T) {
	for _, f := range LockableFields {
		if !IsLockableField(f) {
			t.Errorf("IsLockableField(%q) = false", f)
		}
	}
	// Tags, cast and artwork are deliberately outside the lock set.
	for _, f := range []string{"tags", "poster_path", "tmdb_id", "duration_ms", "", "Title"} {
		if IsLockableField(f) {
			t.Errorf("IsLockableField(%q) = true", f)
		}
	}
}

func TestEditItemMetadata_RejectsUnknownField(t *testing.T) {
	svc, q := newService(t)
	id := uuid.New()
	q.items[id] = Item{ID: id, Title: "Old"}

	for _, p := range []EditItemMetadataParams{
		{ID: id, Set: []string{"tags"}},
		{ID: id, Unlock: []string{"poster_path"}},
		{ID: id, Lock: []string{"tmdb_id"}},
	} {
		_, err := svc.EditItemMetadata(context.Background(), p)
		if !errors.Is(err, ErrNotLockable) {
			t.Errorf("EditItemMetadata(%+v) err = %v, want ErrNotLockable", p, err)
		}
	}
	if q.lastEdit != nil {
		t.Error("a rejected edit reached the database")
	}
}

func TestEditItemMetadata_NotFound(t *testing.T) {
	svc, _ := newService(t)
	_, err := svc.EditItemMetadata(context.Background(), EditItemMetadataParams{
		ID: uuid.New(), Set: []string{FieldTitle}, Title: "X",
	})
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestEditItemMetadata_PassesSetAndNormalisesNils(t *testing.T) {
	svc, q := newService(t)
	id := uuid.New()
	q.items[id] = Item{ID: id, Title: "Old"}

	got, err := svc.EditItemMetadata(context.Background(), EditItemMetadataParams{
		ID: id, Set: []string{FieldTitle, FieldSortTitle}, Lock: []string{FieldTitle},
		Title: "New", SortTitle: "New",
	})
	if err != nil {
		t.Fatalf("EditItemMetadata: %v", err)
	}
	if got.Title != "New" || !slices.Contains(got.LockedFields, FieldTitle) {
		t.Errorf("item = %+v, want title New and locked", got)
	}
	// nil slices reach SQL as empty arrays, never NULL (see nonNil).
	if q.lastEdit.Unlock == nil || q.lastEdit.Genres == nil {
		t.Errorf("nil slices not normalised: %+v", q.lastEdit)
	}
}
