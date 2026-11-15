package gen

import (
	"strings"
	"testing"
)

// TestListingPositionQueries derives every listing's position query (as
// package init does) and checks each numbers its rows by exactly the
// listing's ORDER BY over exactly its FROM and WHERE. A change to the
// generated listing SQL that the derivation can't follow fails here, not
// as a 500 from /items/position.
func TestListingPositionQueries(t *testing.T) {
	if len(listingPositionQueries) != len(listingQueries) {
		t.Fatalf("%d position queries for %d listings", len(listingPositionQueries), len(listingQueries))
	}
	for name, listing := range listingQueries {
		if !strings.HasPrefix(listing, "-- name: "+name+" :many\n") {
			t.Errorf("listingQueries[%q] is not the %s query: %.60q", name, name, listing)
		}
		pq := listingPositionQueries[name]
		if pq.err != nil {
			t.Errorf("%s: %v", name, pq.err)
			continue
		}
		_, orderBy, _ := strings.Cut(listing, "\nORDER BY ")
		orderBy, _, _ = strings.Cut(orderBy, "\n")
		if !strings.Contains(pq.sql, "row_number() OVER (ORDER BY "+orderBy+") - 1 AS pos") {
			t.Errorf("%s: position query does not number rows by ORDER BY %s:\n%s", name, orderBy, pq.sql)
		}
		_, from, _ := strings.Cut(listing, "\nFROM ")
		from = strings.TrimSpace(from)
		if !strings.Contains(pq.sql, "\nFROM "+from+"\n) listing\n") {
			t.Errorf("%s: position query does not keep the listing's FROM … LIMIT verbatim:\n%s", name, pq.sql)
		}
		want := "$14"
		if name == "ListMediaItems" {
			want = "$5"
		}
		if !strings.HasSuffix(pq.sql, "WHERE position_item_id = "+want) {
			t.Errorf("%s: item id is not parameter %s:\n%s", name, want, pq.sql)
		}
	}
}

func TestListingPositionSQL_RefusesUnknownShapes(t *testing.T) {
	for name, sql := range map[string]string{
		"no select":      "WITH x AS (SELECT 1) SELECT id FROM x ORDER BY id\nLIMIT $3 OFFSET $4",
		"not id first":   "SELECT title, id\nFROM media_items\nWHERE library_id = $1 AND type = $2\nORDER BY title\nLIMIT $3 OFFSET $4",
		"no order by":    "SELECT id\nFROM media_items\nWHERE library_id = $1 AND type = $2\nLIMIT $3 OFFSET $4",
		"two order bys":  "SELECT id\nFROM (SELECT id FROM media_items\nORDER BY id) x\nWHERE $1 = $2\nORDER BY id\nLIMIT $3 OFFSET $4",
		"other paging":   "SELECT id\nFROM media_items\nWHERE library_id = $1 AND type = $2\nORDER BY id\nLIMIT $4 OFFSET $3",
		"extra param":    "SELECT id\nFROM media_items\nWHERE library_id = $1 AND type = $2 AND year = $5\nORDER BY id\nLIMIT $3 OFFSET $4",
		"order by split": "SELECT id\nFROM media_items\nWHERE library_id = $1 AND type = $2\nORDER BY sort_title,\n  id\nLIMIT $3 OFFSET $4",
	} {
		if got, err := listingPositionSQL(sql, unfilteredListingParams); err == nil {
			t.Errorf("%s: derived %q, want an error", name, got)
		}
	}
}
