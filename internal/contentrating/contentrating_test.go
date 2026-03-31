package contentrating

import "testing"

func TestRank_MovieRatings(t *testing.T) {
	tests := []struct {
		rating string
		want   int
	}{
		{"G", 0},
		{"PG", 1},
		{"PG-13", 2},
		{"R", 3},
		{"NC-17", 3},
	}
	for _, tt := range tests {
		if got := Rank(tt.rating); got != tt.want {
			t.Errorf("Rank(%q) = %d, want %d", tt.rating, got, tt.want)
		}
	}
}

func TestRank_TVRatings(t *testing.T) {
	tests := []struct {
		rating string
		want   int
	}{
		{"TV-Y", 0},
		{"TV-Y7", 1},
		{"TV-G", 0},
		{"TV-PG", 1},
		{"TV-14", 2},
		{"TV-MA", 3},
	}
	for _, tt := range tests {
		if got := Rank(tt.rating); got != tt.want {
			t.Errorf("Rank(%q) = %d, want %d", tt.rating, got, tt.want)
		}
	}
}

func TestRank_Empty(t *testing.T) {
	if got := Rank(""); got != 4 {
		t.Errorf("Rank(\"\") = %d, want 4 (unrated = most restrictive)", got)
	}
}

func TestRank_Unknown(t *testing.T) {
	for _, rating := range []string{"NR", "UNRATED", "X", "banana"} {
		if got := Rank(rating); got != 4 {
			t.Errorf("Rank(%q) = %d, want 4", rating, got)
		}
	}
}

func TestIsAllowed_EmptyMaxAllowsAll(t *testing.T) {
	for _, rating := range []string{"G", "R", "NC-17", "TV-MA", ""} {
		if !IsAllowed(rating, "") {
			t.Errorf("IsAllowed(%q, \"\") = false, want true", rating)
		}
	}
}

func TestIsAllowed_EmptyContentTreatedAsMax(t *testing.T) {
	// Unrated content is rank 4 — blocked by everything.
	for _, max := range []string{"G", "PG", "PG-13", "R", "NC-17"} {
		if IsAllowed("", max) {
			t.Errorf("IsAllowed(\"\", %q) = true, want false (unrated = rank 4)", max)
		}
	}
}

func TestIsAllowed_Allowed(t *testing.T) {
	tests := []struct {
		content, max string
	}{
		{"G", "G"},
		{"G", "PG"},
		{"PG", "PG-13"},
		{"PG-13", "R"},
		{"R", "NC-17"},
		{"TV-Y", "TV-MA"},
		{"TV-PG", "TV-14"},
	}
	for _, tt := range tests {
		if !IsAllowed(tt.content, tt.max) {
			t.Errorf("IsAllowed(%q, %q) = false, want true", tt.content, tt.max)
		}
	}
}

func TestIsAllowed_Blocked(t *testing.T) {
	tests := []struct {
		content, max string
	}{
		{"R", "PG-13"},
		{"NC-17", "PG-13"},
		{"PG-13", "G"},
		{"TV-MA", "TV-14"},
		{"TV-14", "PG"},
	}
	for _, tt := range tests {
		if IsAllowed(tt.content, tt.max) {
			t.Errorf("IsAllowed(%q, %q) = true, want false", tt.content, tt.max)
		}
	}
}

func TestMaxRatingRank_EmptyReturnsNil(t *testing.T) {
	if got := MaxRatingRank(""); got != nil {
		t.Errorf("MaxRatingRank(\"\") = %v, want nil", *got)
	}
}

func TestMaxRatingRank_ReturnsRank(t *testing.T) {
	tests := []struct {
		rating string
		want   int
	}{
		{"G", 0},
		{"PG-13", 2},
		{"R", 3},
		{"NC-17", 3},
	}
	for _, tt := range tests {
		got := MaxRatingRank(tt.rating)
		if got == nil {
			t.Fatalf("MaxRatingRank(%q) = nil, want %d", tt.rating, tt.want)
		}
		if *got != tt.want {
			t.Errorf("MaxRatingRank(%q) = %d, want %d", tt.rating, *got, tt.want)
		}
	}
}

func TestAllRatings(t *testing.T) {
	ratings := AllRatings()
	if len(ratings) != 5 {
		t.Fatalf("AllRatings() len = %d, want 5", len(ratings))
	}
	// Should be in non-descending order of restrictiveness.
	for i := 0; i < len(ratings)-1; i++ {
		if Rank(ratings[i]) > Rank(ratings[i+1]) {
			t.Errorf("AllRatings() not ordered: Rank(%q)=%d > Rank(%q)=%d",
				ratings[i], Rank(ratings[i]), ratings[i+1], Rank(ratings[i+1]))
		}
	}
}
