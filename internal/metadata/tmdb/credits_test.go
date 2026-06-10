package tmdb

import (
	"context"
	"testing"
)

func TestMapJobToRole(t *testing.T) {
	cases := map[string]string{
		"Director":           "director",
		"Writer":             "writer",
		"Screenplay":         "writer",
		"Story":              "writer",
		"Teleplay":           "writer",
		"Producer":           "producer",
		"Executive Producer": "producer",
		"Creator":            "creator",
		"Gaffer":             "", // unlisted job → dropped
		"":                   "",
	}
	for job, want := range cases {
		if got := mapJobToRole(job); got != want {
			t.Errorf("mapJobToRole(%q) = %q, want %q", job, got, want)
		}
	}
}

func TestToCreditsResult(t *testing.T) {
	c := &Client{}
	in := tmdbCredits{
		Cast: []tmdbCastMember{
			{ID: 1, Name: "Lead", Character: "Hero", ProfilePath: "/a.jpg", Order: 0},
			{ID: 2, Name: "Support", Character: "Sidekick", Order: 1},
		},
		Crew: []tmdbCrewMember{
			{ID: 10, Name: "Dir", Job: "Director"},
			{ID: 11, Name: "Scribe", Job: "Screenplay"},
			{ID: 12, Name: "Boom Op", Job: "Sound"}, // unknown job → dropped
		},
	}
	out := c.toCreditsResult(context.Background(), in)
	if len(out.Cast) != 2 {
		t.Fatalf("cast: got %d, want 2", len(out.Cast))
	}
	if out.Cast[0].Name != "Lead" || out.Cast[0].Character != "Hero" || out.Cast[0].TMDBID != 1 {
		t.Errorf("cast[0] mismapped: %+v", out.Cast[0])
	}
	if len(out.Crew) != 2 {
		t.Fatalf("crew: got %d (unknown job should be dropped), want 2", len(out.Crew))
	}
	if out.Crew[0].Role != "director" || out.Crew[1].Role != "writer" {
		t.Errorf("crew roles: got %q,%q want director,writer", out.Crew[0].Role, out.Crew[1].Role)
	}
}
