//go:build integration

// Guards the collections_type_check CHECK constraint (migration 00017). The
// photo-albums API stores albums as collections with type='photo_album'; before
// 00017 the squashed 00001 constraint omitted that value, so POST
// /api/v1/photo-albums failed with
//   new row for relation "collections" violates check constraint
//   "collections_type_check" (SQLSTATE 23514)
// This test proves every type the app inserts is accepted and a bogus one is not.
package gen_test

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onscreen/onscreen/internal/db/gen"
	"github.com/onscreen/onscreen/internal/testdb"
)

func TestCreateCollection_Integration_AllowedTypes(t *testing.T) {
	pool := testdb.New(t)
	q := gen.New(pool)
	ctx := context.Background()

	owner := seedUser(ctx, t, q, "col-type-"+uuid.New().String()[:8])
	uid := pgtype.UUID{Bytes: owner, Valid: true}

	for _, typ := range []string{"auto_genre", "playlist", "smart_playlist", "event_folder", "photo_album"} {
		col, err := q.CreateCollection(ctx, gen.CreateCollectionParams{
			UserID: uid,
			Name:   "c-" + typ,
			Type:   typ,
		})
		if err != nil {
			t.Errorf("CreateCollection type=%q rejected: %v", typ, err)
			continue
		}
		if col.Type != typ {
			t.Errorf("type round-trip: got %q want %q", col.Type, typ)
		}
	}

	// A value outside the constraint must still be rejected (23514).
	if _, err := q.CreateCollection(ctx, gen.CreateCollectionParams{
		UserID: uid, Name: "bogus", Type: "not_a_real_type",
	}); err == nil {
		t.Error("CreateCollection accepted an invalid type — CHECK constraint not enforced")
	} else if !strings.Contains(err.Error(), "collections_type_check") {
		t.Errorf("expected collections_type_check violation, got: %v", err)
	}
}
