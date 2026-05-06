// Package contentrating implements parental content rating filtering.
// The rank values mirror the content_rating_rank() SQL function in migration 00023.
package contentrating

// Rank returns a numeric rank for a content rating string.
// Lower values are more restrictive. Empty/unrated is rank 4 (most restrictive)
// so restricted profiles are protected until a rating is populated.
// Unknown ratings (NR, UNRATED, X, etc.) also rank 4.
//
// Anime ratings: MAL publishes "R", "R+", "Rx" plus the Japanese
// classification codes ("R-15", "R-17+", "R-18+"). We slot them into
// the existing rank ladder so a parental-control profile maxed at
// "TV-14" still blocks a row tagged "R-18+". AniList itself only
// exposes a Boolean `isAdult`; an explicit tier shows up here only
// when a future MAL agent populates it, when an NFO carries it, or
// when an operator sets it by hand.
func Rank(rating string) int {
	switch rating {
	case "":
		return 4 // treat unrated as most restrictive to protect restricted profiles
	case "G", "TV-Y", "TV-G":
		return 0
	case "PG", "TV-Y7", "TV-PG":
		return 1
	case "PG-13", "TV-14", "R-15":
		return 2
	case "R", "R-17+":
		return 3
	case "NC-17", "TV-MA", "R+", "R-18+":
		return 3
	case "Rx":
		// Hentai. Most restrictive bucket so a parental ceiling of
		// TV-MA / NC-17 still blocks; only an unconstrained adult
		// profile (no max set) sees these rows.
		return 4
	default:
		return 4 // NR, UNRATED, X, empty, etc.
	}
}

// IsAllowed returns true if contentRating is at or below maxRating.
// If maxRating is empty, everything is allowed.
// Empty contentRating is rank 4 (see Rank).
func IsAllowed(contentRating, maxRating string) bool {
	if maxRating == "" {
		return true
	}
	return Rank(contentRating) <= Rank(maxRating)
}

// MaxRatingRank returns a pointer to the numeric rank for the given max rating.
// Returns nil when maxRating is empty (no restriction), which tells SQL filters
// to skip the content_rating_rank() check.
func MaxRatingRank(maxRating string) *int {
	if maxRating == "" {
		return nil
	}
	r := Rank(maxRating)
	return &r
}

// AllRatings returns the ordered list of valid content ratings for UI display.
// Ordered non-descending by Rank so a UI that draws this as a list /
// slider naturally surfaces "more permissive → more restrictive".
// Anime ratings (R-15, R-17+, R+, R-18+, Rx) interleave with the
// Western codes at their matching rank.
func AllRatings() []string {
	return []string{"G", "PG", "PG-13", "R-15", "R", "R-17+", "NC-17", "R+", "R-18+", "Rx"}
}
