// Hand-written: where an item sits in a library listing. sqlc can't share
// one query's ORDER BY with another, and a copied ORDER BY drifts, so the
// position queries are derived at startup from the generated listing
// queries themselves: the same FROM, WHERE, ORDER BY and tie-breakers, with
// the rows numbered by a window over that ORDER BY instead of paged.
package gen

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/google/uuid"
)

// ListingPosition is an item's 0-based row in a library listing and how
// many rows the whole listing has.
type ListingPosition struct {
	Offset int64
	Total  int64
}

// listingQueries are the listing queries a position can be asked of, by
// query name. ListMediaItems is the unfiltered title listing; the others
// are the filtered listings, one per sort, which share
// ListMediaItemsByTitleParams' parameters.
var listingQueries = map[string]string{
	"ListMediaItems":                  listMediaItems,
	"ListMediaItemsByTitle":           listMediaItemsByTitle,
	"ListMediaItemsByTitleDesc":       listMediaItemsByTitleDesc,
	"ListMediaItemsByYear":            listMediaItemsByYear,
	"ListMediaItemsByYearDesc":        listMediaItemsByYearDesc,
	"ListMediaItemsByRating":          listMediaItemsByRating,
	"ListMediaItemsByRatingAsc":       listMediaItemsByRatingAsc,
	"ListMediaItemsByDateAdded":       listMediaItemsByDateAdded,
	"ListMediaItemsByDateAddedAsc":    listMediaItemsByDateAddedAsc,
	"ListMediaItemsByTakenAt":         listMediaItemsByTakenAt,
	"ListMediaItemsByTakenAtAsc":      listMediaItemsByTakenAtAsc,
	"ListMediaItemsByParentTitle":     listMediaItemsByParentTitle,
	"ListMediaItemsByParentTitleDesc": listMediaItemsByParentTitleDesc,
}

// The parameter count of each listing shape: library, type, limit, offset,
// then (filtered) genre, years, rating, rating ceiling, labels, watch.
const (
	unfilteredListingParams = 4
	filteredListingParams   = 13
)

type positionQuery struct {
	sql string
	err error
}

var listingPositionQueries = func() map[string]positionQuery {
	out := make(map[string]positionQuery, len(listingQueries))
	for name, q := range listingQueries {
		want := filteredListingParams
		if name == "ListMediaItems" {
			want = unfilteredListingParams
		}
		sql, err := listingPositionSQL(q, want)
		if err != nil {
			err = fmt.Errorf("position in %s: %w", name, err)
		}
		out[name] = positionQuery{sql: sql, err: err}
	}
	return out
}()

var sqlParam = regexp.MustCompile(`\$(\d+)`)

// listingPositionSQL turns a generated listing query (SELECT … FROM …
// ORDER BY … LIMIT $3 OFFSET $4) into one answering an item's row in it.
// The FROM … LIMIT tail is kept as it is; the select list becomes the id,
// row_number() over the listing's own ORDER BY, and the row count. The
// caller binds LIMIT to NULL (no limit) and OFFSET to 0, and the item's id
// to the one parameter after the listing's.
func listingPositionSQL(listing string, wantParams int) (string, error) {
	body := listing
	if strings.HasPrefix(body, "-- name:") {
		_, body, _ = strings.Cut(body, "\n")
	}
	body = strings.TrimSpace(body)
	cols, ok := strings.CutPrefix(body, "SELECT ")
	if !ok {
		return "", fmt.Errorf("does not start with SELECT")
	}
	cols, tail, ok := strings.Cut(cols, "\nFROM ")
	if !ok {
		return "", fmt.Errorf("no FROM line")
	}
	idCol, _, _ := strings.Cut(cols, ",")
	idCol = strings.TrimSpace(idCol)
	if idCol != "id" && !strings.HasSuffix(idCol, ".id") {
		return "", fmt.Errorf("first column is %q, not the item id", idCol)
	}
	if n := strings.Count(tail, "\nORDER BY "); n != 1 {
		return "", fmt.Errorf("%d ORDER BY lines, want 1", n)
	}
	_, orderBy, _ := strings.Cut(tail, "\nORDER BY ")
	orderBy, page, ok := strings.Cut(orderBy, "\n")
	if !ok || page != "LIMIT $3 OFFSET $4" {
		return "", fmt.Errorf("does not end ORDER BY …, LIMIT $3 OFFSET $4")
	}
	params := 0
	for _, m := range sqlParam.FindAllStringSubmatch(tail, -1) {
		if n, _ := strconv.Atoi(m[1]); n > params {
			params = n
		}
	}
	if params != wantParams {
		return "", fmt.Errorf("takes %d parameters, want %d", params, wantParams)
	}
	return fmt.Sprintf(`SELECT pos, total FROM (
SELECT %s AS position_item_id,
       row_number() OVER (ORDER BY %s) - 1 AS pos,
       count(*) OVER () AS total
FROM %s
) listing
WHERE position_item_id = $%d`, idCol, orderBy, tail, params+1), nil
}

func (q *Queries) listingPosition(ctx context.Context, listing string, args ...any) (ListingPosition, error) {
	pq, ok := listingPositionQueries[listing]
	if !ok {
		return ListingPosition{}, fmt.Errorf("no listing query %q", listing)
	}
	if pq.err != nil {
		return ListingPosition{}, pq.err
	}
	var p ListingPosition
	err := q.db.QueryRow(ctx, pq.sql, args...).Scan(&p.Offset, &p.Total)
	return p, err
}

// PositionInListMediaItems is itemID's row in ListMediaItems for the
// library and type, and that listing's row count. pgx.ErrNoRows when the
// item is not in it.
func (q *Queries) PositionInListMediaItems(ctx context.Context, libraryID uuid.UUID, itemType string, itemID uuid.UUID) (ListingPosition, error) {
	return q.listingPosition(ctx, "ListMediaItems", libraryID, itemType, nil, int32(0), itemID)
}

// PositionInFilteredListing is itemID's row in the filtered listing query
// named listing (one of the ListMediaItemsBy* sorts) under arg's filters,
// and that listing's row count; arg.Limit and arg.Offset are ignored.
// pgx.ErrNoRows when the item is not in it.
func (q *Queries) PositionInFilteredListing(ctx context.Context, listing string, arg ListMediaItemsByTitleParams, itemID uuid.UUID) (ListingPosition, error) {
	if listing == "ListMediaItems" {
		return ListingPosition{}, fmt.Errorf("ListMediaItems is not a filtered listing")
	}
	return q.listingPosition(ctx, listing,
		arg.LibraryID,
		arg.Type,
		nil,      // LIMIT NULL: every row
		int32(0), // OFFSET 0
		arg.Genre,
		arg.YearMin,
		arg.YearMax,
		arg.RatingMin,
		arg.MaxRatingRank,
		arg.BlockedLabels,
		arg.AllowedLabels,
		arg.Watch,
		arg.WatchUserID,
		itemID,
	)
}
