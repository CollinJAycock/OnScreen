package bulkedit

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/db/gen"
)

func strp(s string) *string { return &s }

func row(typ string, genres, locked []string) gen.LockMediaItemsForBulkEditRow {
	return gen.LockMediaItemsForBulkEditRow{
		ID: uuid.New(), LibraryID: uuid.New(), Type: typ, Title: typ + " title",
		Genres: genres, LockedFields: locked,
	}
}

func TestPlan_SetWritesAndLocks(t *testing.T) {
	r := row("movie", []string{"Drama"}, nil)
	p, skips, write := Plan(r, Request{
		SetContentRating: true, ContentRating: strp("PG-13"),
		SetGenres: true, Genres: []string{"Comedy"},
	})
	if !write || len(skips) != 0 {
		t.Fatalf("write=%v skips=%v", write, skips)
	}
	if !slices.Equal(p.SetFields, []string{"content_rating", "genres"}) ||
		!slices.Equal(p.LockFields, []string{"content_rating", "genres"}) {
		t.Errorf("set=%v lock=%v", p.SetFields, p.LockFields)
	}
	if p.ContentRating == nil || *p.ContentRating != "PG-13" || !slices.Equal(p.Genres, []string{"Comedy"}) {
		t.Errorf("values = %v / %v", p.ContentRating, p.Genres)
	}
	if len(p.UnlockFields) != 0 {
		t.Errorf("unlock = %v", p.UnlockFields)
	}
}

func TestPlan_LockedFieldSkippedUnlessUnlocked(t *testing.T) {
	r := row("show", nil, []string{"content_rating", "title"})
	req := Request{SetContentRating: true, ContentRating: strp("TV-14")}

	_, skips, write := Plan(r, req)
	if write {
		t.Error("locked field was written")
	}
	if len(skips) != 1 || skips[0].Field != "content_rating" || skips[0].Reason != ReasonLocked {
		t.Fatalf("skips = %+v", skips)
	}

	// Unlocking it in the same request overrides: written and re-locked,
	// and the unlock isn't sent (the query would drop the new lock).
	req.Unlock = []string{"content_rating", "title"}
	p, skips, write := Plan(r, req)
	if !write || len(skips) != 0 {
		t.Fatalf("write=%v skips=%v", write, skips)
	}
	if !slices.Contains(p.SetFields, "content_rating") || !slices.Contains(p.LockFields, "content_rating") {
		t.Errorf("set=%v lock=%v", p.SetFields, p.LockFields)
	}
	if !slices.Equal(p.UnlockFields, []string{"title"}) {
		t.Errorf("unlock = %v, want [title]", p.UnlockFields)
	}
}

func TestPlan_NotApplicableType(t *testing.T) {
	r := row("photo", nil, nil)
	_, skips, write := Plan(r, Request{
		SetContentRating: true, ContentRating: strp("R"),
		AddGenres: []string{"Horror"},
	})
	if write {
		t.Error("photo was written")
	}
	if len(skips) != 2 || skips[0].Reason != ReasonNotApplicable || skips[1].Reason != ReasonNotApplicable {
		t.Fatalf("skips = %+v", skips)
	}
	// Albums carry genres but not a content rating.
	r = row("album", []string{"Rock"}, nil)
	p, skips, _ := Plan(r, Request{SetContentRating: true, AddGenres: []string{"Jazz"}})
	if len(skips) != 1 || skips[0].Field != "content_rating" {
		t.Errorf("album skips = %+v", skips)
	}
	if !slices.Equal(p.Genres, []string{"Rock", "Jazz"}) {
		t.Errorf("album genres = %v", p.Genres)
	}
}

func TestPlan_GenreMergeNoChangeWritesNothing(t *testing.T) {
	r := row("movie", []string{"Drama", "Crime"}, nil)
	_, skips, write := Plan(r, Request{AddGenres: []string{"drama"}, RemoveGenres: []string{"Western"}})
	if write || len(skips) != 0 {
		t.Errorf("write=%v skips=%v; an unchanged merge should be a no-op", write, skips)
	}
}

func TestPlan_LockAndUnlockOnly(t *testing.T) {
	r := row("episode", nil, []string{"summary"})
	p, _, write := Plan(r, Request{Lock: []string{"title", "summary"}, Unlock: []string{"year"}})
	if !write {
		t.Fatal("lock-only edit not written")
	}
	// summary is already locked; year isn't locked so there's nothing to drop.
	if len(p.SetFields) != 0 || !slices.Equal(p.LockFields, []string{"title"}) || len(p.UnlockFields) != 0 {
		t.Errorf("set=%v lock=%v unlock=%v", p.SetFields, p.LockFields, p.UnlockFields)
	}
	_, _, write = Plan(r, Request{Lock: []string{"summary"}})
	if write {
		t.Error("re-locking an already locked field should write nothing")
	}
}

func TestMergeGenres(t *testing.T) {
	got, changed := MergeGenres([]string{"Drama", "Sci-Fi"}, []string{"sci-fi", "Noir"}, []string{"DRAMA"})
	if !changed || !slices.Equal(got, []string{"Sci-Fi", "Noir"}) {
		t.Errorf("got %v changed=%v", got, changed)
	}
	// In both lists: removed, then added back.
	got, _ = MergeGenres([]string{"Drama"}, []string{"Drama"}, []string{"Drama"})
	if !slices.Equal(got, []string{"Drama"}) {
		t.Errorf("got %v", got)
	}
	got, changed = MergeGenres(nil, nil, []string{"x"})
	if changed || len(got) != 0 {
		t.Errorf("got %v changed=%v", got, changed)
	}
}

type fakeStore struct {
	rows    []gen.LockMediaItemsForBulkEditRow
	edits   []gen.EditMediaItemMetadataParams
	added   *gen.AddMediaItemLabelsParams
	removed *gen.RemoveMediaItemLabelsParams
	editErr error
}

func (f *fakeStore) LockMediaItemsForBulkEdit(_ context.Context, ids []uuid.UUID) ([]gen.LockMediaItemsForBulkEditRow, error) {
	var out []gen.LockMediaItemsForBulkEditRow
	for _, r := range f.rows {
		if slices.Contains(ids, r.ID) {
			out = append(out, r)
		}
	}
	return out, nil
}

func (f *fakeStore) EditMediaItemMetadata(_ context.Context, p gen.EditMediaItemMetadataParams) (gen.EditMediaItemMetadataRow, error) {
	if f.editErr != nil {
		return gen.EditMediaItemMetadataRow{}, f.editErr
	}
	f.edits = append(f.edits, p)
	return gen.EditMediaItemMetadataRow{ID: p.ID}, nil
}

func (f *fakeStore) AddMediaItemLabels(_ context.Context, p gen.AddMediaItemLabelsParams) (int64, error) {
	f.added = &p
	return int64(len(p.Ids)), nil
}

func (f *fakeStore) RemoveMediaItemLabels(_ context.Context, p gen.RemoveMediaItemLabelsParams) (int64, error) {
	f.removed = &p
	return 1, nil
}

func TestRun_MixedSelection(t *testing.T) {
	movie := row("movie", []string{"Drama"}, nil)
	locked := row("movie", nil, []string{"genres"})
	photo := row("photo", nil, nil)
	missing := uuid.New()
	st := &fakeStore{rows: []gen.LockMediaItemsForBulkEditRow{movie, locked, photo}}

	res, err := Run(context.Background(), st, Request{
		ItemIDs:   []uuid.UUID{movie.ID, locked.ID, photo.ID, missing},
		AddGenres: []string{"Heist"},
		AddLabels: []string{"kids"}, RemoveLabels: []string{"adult"}, MaxLabels: 50,
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Matched != 3 || !slices.Equal(res.Updated, []uuid.UUID{movie.ID}) {
		t.Errorf("matched=%d updated=%v", res.Matched, res.Updated)
	}
	reasons := map[uuid.UUID]string{}
	for _, s := range res.Skipped {
		reasons[s.ItemID] = s.Reason
	}
	if reasons[missing] != ReasonNotFound || reasons[locked.ID] != ReasonLocked || reasons[photo.ID] != ReasonNotApplicable {
		t.Errorf("skips = %+v", res.Skipped)
	}
	// Labels go to every live item regardless of type or locks.
	if st.added == nil || len(st.added.Ids) != 3 || st.added.MaxPerItem != 50 || st.removed == nil {
		t.Errorf("labels add=%+v remove=%+v", st.added, st.removed)
	}
	if res.LabelsAdded != 3 || res.LabelsRemoved != 1 {
		t.Errorf("labels added=%d removed=%d", res.LabelsAdded, res.LabelsRemoved)
	}
}

func TestRun_EditErrorAborts(t *testing.T) {
	m := row("movie", nil, nil)
	st := &fakeStore{rows: []gen.LockMediaItemsForBulkEditRow{m}, editErr: errors.New("boom")}
	_, err := Run(context.Background(), st, Request{ItemIDs: []uuid.UUID{m.ID}, SetContentRating: true})
	if err == nil {
		t.Fatal("want error so the transaction rolls back")
	}
	if st.added != nil {
		t.Error("labels applied after a failed edit")
	}
}
