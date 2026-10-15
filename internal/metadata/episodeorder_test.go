package metadata

import "testing"

func TestEpisodeOrderingLookup(t *testing.T) {
	dvd := &EpisodeOrdering{Order: EpisodeOrderDVD, Episodes: []OrderedEpisode{
		{Season: 0, Number: 1, Episode: EpisodeResult{Title: "DVD Special"}},
		{Season: 1, Number: 1, Absolute: 1, Episode: EpisodeResult{Title: "Serenity"}},
		{Season: 1, Number: 2, Absolute: 2, Episode: EpisodeResult{Title: "The Train Job"}},
	}}
	if r := dvd.Lookup(1, 2); r == nil || r.Title != "The Train Job" {
		t.Errorf("dvd S1E2 = %+v", r)
	}
	if r := dvd.Lookup(0, 1); r == nil || r.Title != "DVD Special" {
		t.Errorf("dvd S0E1 = %+v", r)
	}
	if r := dvd.Lookup(2, 1); r != nil {
		t.Errorf("dvd S2E1 = %+v, want nil", r)
	}

	abs := &EpisodeOrdering{Order: EpisodeOrderAbsolute, Episodes: []OrderedEpisode{
		{Season: 1, Number: 1, Absolute: 1, Episode: EpisodeResult{Title: "One"}},
		{Season: 2, Number: 1, Absolute: 13, Episode: EpisodeResult{Title: "Thirteen"}},
		{Season: 0, Number: 1, Episode: EpisodeResult{Title: "Special"}},
	}}
	// The file's season doesn't matter for absolute numbers...
	for _, season := range []int{1, 2, 5} {
		if r := abs.Lookup(season, 13); r == nil || r.Title != "Thirteen" {
			t.Errorf("absolute S%d E13 = %+v", season, r)
		}
	}
	// ...except season 0, which has none.
	if r := abs.Lookup(0, 1); r != nil {
		t.Errorf("absolute S0E1 = %+v, want nil (aired order keeps specials)", r)
	}
	if r := abs.Lookup(1, 0); r != nil {
		t.Errorf("absolute E0 = %+v, want nil", r)
	}
	var none *EpisodeOrdering
	if r := none.Lookup(1, 1); r != nil {
		t.Errorf("nil ordering lookup = %+v", r)
	}
}
