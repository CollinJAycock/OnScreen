package v1

import (
	"context"
	"net/http"

	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/api/middleware"
	"github.com/onscreen/onscreen/internal/auth"
	"github.com/onscreen/onscreen/internal/contentrating"
	"github.com/onscreen/onscreen/internal/db/gen"
	"github.com/onscreen/onscreen/internal/itemlabel"
)

// ItemLabelGate answers whether a label-restricted viewer may see one item
// (its own labels plus its ancestors'). It is the per-item half of the label
// restriction; listings apply the same rule inside their SQL. Satisfied by
// *gen.Queries.
//
// Every handler that applies the content-rating ceiling to a single item
// applies this next to it (itemPermitted), refusing with the same status the
// ceiling uses there, so a label refusal is indistinguishable from a rating
// one.
type ItemLabelGate interface {
	ItemLabelsPermitted(ctx context.Context, arg gen.ItemLabelsPermittedParams) (bool, error)
	FilterLabelPermittedItems(ctx context.Context, arg gen.FilterLabelPermittedItemsParams) ([]uuid.UUID, error)
}

type labelGateKey struct{}

// WithItemLabelGate returns ctx carrying gate. The gate travels on the
// request context rather than being wired into each handler because the
// single-item check sits in two dozen handlers (and the shared itemAddAllowed
// helper); one middleware at the router root reaches all of them, including
// handlers added later.
func WithItemLabelGate(ctx context.Context, gate ItemLabelGate) context.Context {
	return context.WithValue(ctx, labelGateKey{}, gate)
}

// ItemLabelGateMiddleware puts gate on every request's context.
func ItemLabelGateMiddleware(gate ItemLabelGate) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r.WithContext(WithItemLabelGate(r.Context(), gate)))
		})
	}
}

func labelGateFrom(ctx context.Context) ItemLabelGate {
	g, _ := ctx.Value(labelGateKey{}).(ItemLabelGate)
	return g
}

// labelsPermit reports whether claims' label restriction admits itemID. An
// unrestricted caller (the common case) passes without a query. It fails
// closed: a restricted caller is refused when no gate is on the context or
// the lookup errors, because the alternative is serving an item the admin
// hid.
func labelsPermit(ctx context.Context, claims *auth.Claims, itemID uuid.UUID) bool {
	f := claims.LabelFilter()
	if !f.Active() {
		return true
	}
	gate := labelGateFrom(ctx)
	if gate == nil {
		return false
	}
	ok, err := gate.ItemLabelsPermitted(ctx, gen.ItemLabelsPermittedParams{
		ID:            itemID,
		BlockedLabels: f.BlockedArg(),
		AllowedLabels: f.AllowedArg(),
	})
	return err == nil && ok
}

// labelPermittedSet is labelsPermit over a list in hand: it returns the
// subset of ids the caller's label restriction admits, in one query. nil
// means "no restriction, every id passes"; a non-nil (possibly empty) set
// means only its members pass. Fails closed like labelsPermit: an error or a
// missing gate yields the empty set.
func labelPermittedSet(ctx context.Context, claims *auth.Claims, ids []uuid.UUID) map[uuid.UUID]struct{} {
	f := claims.LabelFilter()
	if !f.Active() {
		return nil
	}
	out := map[uuid.UUID]struct{}{}
	gate := labelGateFrom(ctx)
	if gate == nil || len(ids) == 0 {
		return out
	}
	kept, err := gate.FilterLabelPermittedItems(ctx, gen.FilterLabelPermittedItemsParams{
		Ids:           ids,
		BlockedLabels: f.BlockedArg(),
		AllowedLabels: f.AllowedArg(),
	})
	if err != nil {
		return out
	}
	for _, id := range kept {
		out[id] = struct{}{}
	}
	return out
}

// inLabelSet reports whether id passes a set from labelPermittedSet.
func inLabelSet(set map[uuid.UUID]struct{}, id uuid.UUID) bool {
	if set == nil {
		return true
	}
	_, ok := set[id]
	return ok
}

// itemPermitted is the single-item gate: the content-rating ceiling AND the
// label restriction. contentRating is the item's rating ("" when unrated,
// which the ceiling treats as most restrictive). Nil claims pass, as they do
// for the ceiling alone; callers that require auth check that first.
func itemPermitted(ctx context.Context, claims *auth.Claims, itemID uuid.UUID, contentRating string) bool {
	if claims == nil {
		return true
	}
	return contentrating.IsAllowed(contentRating, claims.MaxContentRating) &&
		labelsPermit(ctx, claims, itemID)
}

// viewerRestricted reports whether claims carry a rating ceiling or a label
// restriction — the fast-path guard single-item gates use to skip the item
// lookup for unrestricted viewers.
func viewerRestricted(claims *auth.Claims) bool {
	return claims != nil && (claims.MaxContentRating != "" || claims.LabelFilter().Active())
}

// callerLabels returns the label restriction of the caller on r (zero when
// unauthenticated or unrestricted). The listing counterpart of
// callerRatingRank.
func callerLabels(r *http.Request) itemlabel.Filter {
	return ctxLabels(r.Context())
}

// ctxLabels is callerLabels for code that holds the request context rather
// than the request.
func ctxLabels(ctx context.Context) itemlabel.Filter {
	return middleware.ClaimsFromContext(ctx).LabelFilter()
}
