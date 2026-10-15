//go:build integration

// Pins the label queries (migration 00047): media_item_labels_permitted's
// inheritance through ancestors, blocked vs allowed semantics, NULL = no
// restriction; bulk add / remove including the per-item cap; and the user
// restriction's inheritance into new managed profiles and cascade on update.
//
// Run with: go test -tags=integration -run Labels ./internal/db/gen/...
package gen_test

import (
	"context"
	"sort"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onscreen/onscreen/internal/db/gen"
	"github.com/onscreen/onscreen/internal/testdb"
)

func TestLabels_Integration_Permitted(t *testing.T) {
	pool := testdb.New(t)
	q := gen.New(pool)
	ctx := context.Background()
	lib := seedLibrary(ctx, t, q, "lbl-lib-"+uuid.NewString()[:8])

	child := func(typ, title string, parent uuid.UUID) uuid.UUID {
		p := gen.CreateMediaItemParams{LibraryID: lib, Type: typ, Title: title, SortTitle: title}
		if parent != uuid.Nil {
			p.ParentID = pgtype.UUID{Bytes: parent, Valid: true}
		}
		row, err := q.CreateMediaItem(ctx, p)
		if err != nil {
			t.Fatalf("create %s: %v", typ, err)
		}
		return row.ID
	}
	artist := child("artist", "Artist", uuid.Nil)
	album := child("album", "Album", artist)
	track := child("track", "Track", album)
	other := child("track", "Other Track", album)
	movie := child("movie", "Movie", uuid.Nil)

	set := func(id uuid.UUID, labels ...string) {
		if n, err := q.SetMediaItemLabels(ctx, gen.SetMediaItemLabelsParams{ID: id, Labels: labels}); err != nil || n != 1 {
			t.Fatalf("set labels on %s: n=%d err=%v", id, n, err)
		}
	}
	set(artist, "explicit")
	set(other, "kids")

	permitted := func(id uuid.UUID, blocked, allowed []string) bool {
		t.Helper()
		ok, err := q.ItemLabelsPermitted(ctx, gen.ItemLabelsPermittedParams{ID: id, BlockedLabels: blocked, AllowedLabels: allowed})
		if err != nil {
			t.Fatalf("ItemLabelsPermitted: %v", err)
		}
		return ok
	}
	cases := []struct {
		name             string
		id               uuid.UUID
		blocked, allowed []string
		want             bool
	}{
		{"no restriction", track, nil, nil, true},
		{"empty arrays are no restriction", track, []string{}, []string{}, true},
		{"artist label reaches the track (grandparent)", track, []string{"explicit"}, nil, false},
		{"artist label reaches the album (parent)", album, []string{"explicit"}, nil, false},
		{"unrelated block", movie, []string{"explicit"}, nil, true},
		{"allowed: unlabelled movie hidden", movie, nil, []string{"kids"}, false},
		{"allowed: own label admits", other, nil, []string{"kids"}, true},
		{"allowed: sibling's label does not admit", track, nil, []string{"kids"}, false},
		{"blocked wins over allowed", other, []string{"explicit"}, []string{"kids"}, false},
		{"missing item is not permitted", uuid.New(), []string{"x"}, nil, false},
	}
	for _, tc := range cases {
		if got := permitted(tc.id, tc.blocked, tc.allowed); got != tc.want {
			t.Errorf("%s: permitted = %v, want %v", tc.name, got, tc.want)
		}
	}

	kept, err := q.FilterLabelPermittedItems(ctx, gen.FilterLabelPermittedItemsParams{
		Ids: []uuid.UUID{track, other, movie}, BlockedLabels: []string{"kids"},
	})
	if err != nil {
		t.Fatal(err)
	}
	sort.Slice(kept, func(i, j int) bool { return kept[i].String() < kept[j].String() })
	want := []uuid.UUID{track, movie}
	sort.Slice(want, func(i, j int) bool { return want[i].String() < want[j].String() })
	if len(kept) != 2 || kept[0] != want[0] || kept[1] != want[1] {
		t.Errorf("FilterLabelPermittedItems = %v, want %v", kept, want)
	}
}

func TestLabels_Integration_BulkAddRemove(t *testing.T) {
	pool := testdb.New(t)
	q := gen.New(pool)
	ctx := context.Background()
	lib := seedLibrary(ctx, t, q, "lbl-bulk-"+uuid.NewString()[:8])
	a := seedMediaItem(ctx, t, q, lib, "A")
	b := seedMediaItem(ctx, t, q, lib, "B")
	if _, err := q.SetMediaItemLabels(ctx, gen.SetMediaItemLabelsParams{ID: b, Labels: []string{"x", "y"}}); err != nil {
		t.Fatal(err)
	}

	n, err := q.AddMediaItemLabels(ctx, gen.AddMediaItemLabelsParams{Ids: []uuid.UUID{a, b}, Labels: []string{"y", "z"}, MaxPerItem: 50})
	if err != nil || n != 2 {
		t.Fatalf("add: n=%d err=%v", n, err)
	}
	got, _ := q.GetMediaItemLabels(ctx, b)
	if len(got) != 3 || got[0] != "x" || got[1] != "y" || got[2] != "z" {
		t.Errorf("b labels = %v, want sorted union [x y z]", got)
	}
	// Re-adding changes nothing and counts nothing.
	if n, _ := q.AddMediaItemLabels(ctx, gen.AddMediaItemLabelsParams{Ids: []uuid.UUID{a}, Labels: []string{"z"}, MaxPerItem: 50}); n != 0 {
		t.Errorf("idempotent add counted %d", n)
	}
	// The cap leaves an item that would exceed it untouched.
	if n, _ := q.AddMediaItemLabels(ctx, gen.AddMediaItemLabelsParams{Ids: []uuid.UUID{b}, Labels: []string{"w"}, MaxPerItem: 3}); n != 0 {
		t.Errorf("over-cap add counted %d", n)
	}
	n, err = q.RemoveMediaItemLabels(ctx, gen.RemoveMediaItemLabelsParams{Ids: []uuid.UUID{a, b}, Labels: []string{"z", "x"}})
	if err != nil || n != 2 {
		t.Fatalf("remove: n=%d err=%v", n, err)
	}
	got, _ = q.GetMediaItemLabels(ctx, b)
	if len(got) != 1 || got[0] != "y" {
		t.Errorf("b labels after remove = %v, want [y]", got)
	}
	inUse, err := q.ListLabelsInUse(ctx)
	if err != nil {
		t.Fatal(err)
	}
	counts := map[string]int64{}
	for _, r := range inUse {
		counts[r.Label] = r.ItemCount
	}
	if counts["y"] != 2 || counts["x"] != 0 || counts["z"] != 0 {
		t.Errorf("labels in use = %v, want y:2 only", counts)
	}
}

func TestLabels_Integration_UserRestrictionCascade(t *testing.T) {
	pool := testdb.New(t)
	q := gen.New(pool)
	ctx := context.Background()
	parent := seedUser(ctx, t, q, "lbl-parent-"+uuid.NewString()[:8])
	parentPG := pgtype.UUID{Bytes: parent, Valid: true}

	if err := q.UpdateUserLabelRestrictions(ctx, gen.UpdateUserLabelRestrictionsParams{
		ID: parent, BlockedLabels: []string{"horror"}, AllowedLabels: []string{},
	}); err != nil {
		t.Fatal(err)
	}
	// A profile created now inherits the owner's restriction.
	kid, err := q.CreateManagedProfile(ctx, gen.CreateManagedProfileParams{Username: "lbl-kid-" + uuid.NewString()[:8], ParentUserID: parentPG})
	if err != nil {
		t.Fatal(err)
	}
	r, err := q.GetUserLabelRestrictions(ctx, kid.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.BlockedLabels) != 1 || r.BlockedLabels[0] != "horror" {
		t.Errorf("new profile blocked = %v, want inherited [horror]", r.BlockedLabels)
	}
	// A later change on the owner cascades to the profile.
	if err := q.UpdateUserLabelRestrictions(ctx, gen.UpdateUserLabelRestrictionsParams{
		ID: parent, BlockedLabels: []string{}, AllowedLabels: []string{"kids"},
	}); err != nil {
		t.Fatal(err)
	}
	r, _ = q.GetUserLabelRestrictions(ctx, kid.ID)
	if len(r.BlockedLabels) != 0 || len(r.AllowedLabels) != 1 || r.AllowedLabels[0] != "kids" {
		t.Errorf("profile after cascade = %+v, want blocked [] allowed [kids]", r)
	}
	// The full users row (what token issuance reads) carries the lists.
	u, err := q.GetUser(ctx, kid.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(u.AllowedLabels) != 1 || u.AllowedLabels[0] != "kids" {
		t.Errorf("GetUser allowed = %v", u.AllowedLabels)
	}
}
