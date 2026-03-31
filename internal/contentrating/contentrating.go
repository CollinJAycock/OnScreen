// Package contentrating implements parental content rating filtering.
// The rank values mirror the content_rating_rank() SQL function in migration 00023.
package contentrating

// Rank returns a numeric rank for a content rating string.
// Lower values are more restrictive. Empty/unrated is rank 4 (most restrictive)
// so restricted profiles are protected until a rating is populated.
// Unknown ratings (NR, UNRATED, X, etc.) also rank 4.
func Rank(rating string) int {
	switch rating {
	case "":
		return 4 // treat unrated as most restrictive to protect restricted profiles
	case "G", "TV-Y", "TV-G":
		return 0
	case "PG", "TV-Y7", "TV-PG":
		return 1
	case "PG-13", "TV-14":
		return 2
	case "R":
		return 3
	case "NC-17", "TV-MA":
		return 3
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
func AllRatings() []string {
	return []string{"G", "PG", "PG-13", "R", "NC-17"}
}
