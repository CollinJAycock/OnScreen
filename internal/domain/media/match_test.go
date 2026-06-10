package media

import "testing"

func TestPickFlexibleMatch(t *testing.T) {
	yp := func(i int) *int { return &i }
	mk := func(year *int) Item { return Item{Year: year} }

	// No candidates → nil.
	if got := pickFlexibleMatch(nil, nil); got != nil {
		t.Errorf("nil candidates: want nil, got %+v", got)
	}
	if got := pickFlexibleMatch([]Item{}, yp(2000)); got != nil {
		t.Errorf("empty candidates: want nil, got %+v", got)
	}

	cands := []Item{mk(yp(1999)), mk(yp(2010))}

	// No scanner year → first candidate (SQL already ordered by richness).
	if got := pickFlexibleMatch(cands, nil); got == nil || got.Year == nil || *got.Year != 1999 {
		t.Errorf("nil year: want first (1999), got %+v", got)
	}

	// Exact year match wins.
	if got := pickFlexibleMatch(cands, yp(2010)); got == nil || got.Year == nil || *got.Year != 2010 {
		t.Errorf("year match: want 2010, got %+v", got)
	}

	// No year match, but an un-enriched (no-year) candidate exists → return it.
	if got := pickFlexibleMatch([]Item{mk(yp(1999)), mk(nil)}, yp(2222)); got == nil || got.Year != nil {
		t.Errorf("fallback: want the no-year candidate, got %+v", got)
	}

	// No year match and every candidate has a different year → nil (different shows).
	if got := pickFlexibleMatch([]Item{mk(yp(1999)), mk(yp(2001))}, yp(2222)); got != nil {
		t.Errorf("all different years: want nil, got %+v", got)
	}
}
