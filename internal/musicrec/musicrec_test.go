package musicrec

import (
	"math/rand"
	"testing"

	"github.com/google/uuid"
)

func ids(n int) []uuid.UUID {
	out := make([]uuid.UUID, n)
	for i := range out {
		out[i] = uuid.New()
	}
	return out
}

func TestRankSimilar_CoListeningLeads(t *testing.T) {
	a := ids(4)
	seed := ArtistProfile{ID: a[0], Genres: []string{"rock", "blues"}, MedianYear: 1970, TrackCount: 100}
	cands := []ArtistProfile{
		seed, // the seed itself is never similar to itself
		{ID: a[1], Genres: []string{"rock", "blues"}, MedianYear: 1971, TrackCount: 50}, // same genres, no co-listening
		{ID: a[2], Genres: []string{"jazz"}, MedianYear: 1990, TrackCount: 50},          // co-listened, no shared genre
		{ID: a[3], Genres: []string{"polka"}, MedianYear: 1970, TrackCount: 50},         // neither
	}
	got := RankSimilar(seed, cands, map[uuid.UUID]int64{a[2]: 40}, 10)
	if len(got) != 2 {
		t.Fatalf("got %d results, want 2 (co-listened + genre match): %+v", len(got), got)
	}
	if got[0].ID != a[2] || !got[0].CoListened {
		t.Errorf("co-listening should lead: %+v", got)
	}
	if got[1].ID != a[1] || len(got[1].SharedGenres) != 2 {
		t.Errorf("genre match second with shared genres: %+v", got[1])
	}
}

func TestRankSimilar_CoListeningNormalisedByTrackCount(t *testing.T) {
	a := ids(3)
	seed := ArtistProfile{ID: a[0], TrackCount: 10}
	cands := []ArtistProfile{
		{ID: a[1], TrackCount: 1000}, // many pairs only because it has many tracks
		{ID: a[2], TrackCount: 10},
	}
	got := RankSimilar(seed, cands, map[uuid.UUID]int64{a[1]: 100, a[2]: 60}, 10)
	if len(got) != 2 || got[0].ID != a[2] {
		t.Errorf("a prolific artist must not win on volume: %+v", got)
	}
}

func TestRankSimilar_FallbackWithoutListening(t *testing.T) {
	a := ids(4)
	seed := ArtistProfile{ID: a[0], Genres: []string{"Synthpop", "new wave"}, MedianYear: 1984}
	cands := []ArtistProfile{
		{ID: a[1], Genres: []string{"synthpop"}, MedianYear: 2015},
		{ID: a[2], Genres: []string{"synthpop"}, MedianYear: 1983}, // same genre, same era: first
		{ID: a[3], Genres: []string{"metal"}, MedianYear: 1984},    // same era, no shared genre: out
	}
	got := RankSimilar(seed, cands, nil, 10)
	if len(got) != 2 || got[0].ID != a[2] || got[1].ID != a[1] {
		t.Errorf("genre overlap with era tie-break: %+v", got)
	}

	// No genres on the seed at all: artists of its era.
	bare := ArtistProfile{ID: a[0], MedianYear: 1984}
	got = RankSimilar(bare, cands, nil, 10)
	if len(got) != 2 {
		t.Errorf("era fallback: %+v", got)
	}
	for _, s := range got {
		if s.ID == a[1] {
			t.Error("an artist 31 years apart is not of the same era")
		}
	}

	if got := RankSimilar(seed, cands, nil, 1); len(got) != 1 {
		t.Errorf("limit: %d", len(got))
	}
}

func TestBuildMix_SeedFirstAndArtistsSpaced(t *testing.T) {
	seedArtist, b, c := uuid.New(), uuid.New(), uuid.New()
	cand := func(artist uuid.UUID) Candidate { return Candidate{TrackID: uuid.New(), ArtistID: artist} }
	pin := cand(seedArtist)
	seedPool := []Candidate{cand(seedArtist), cand(seedArtist), cand(seedArtist)}
	similar := []Candidate{cand(b), cand(c), cand(b), cand(c)}
	mix := BuildMix([][]Candidate{seedPool, similar, {pin}}, 50, &pin)

	if mix[0] != pin {
		t.Fatal("the seed track leads")
	}
	if len(mix) != 1+3+4 {
		t.Fatalf("mix length %d (the pinned track listed again in a pool must not repeat)", len(mix))
	}
	// The seed artist's own tracks come before any other's second track…
	if mix[3].ArtistID != seedArtist {
		t.Errorf("seed tracks should come early: %v", mix)
	}
	// …but no artist plays twice within ArtistGap tracks while others remain.
	for i := 1; i < len(mix)-2; i++ {
		for j := i - ArtistGap; j < i; j++ {
			if j >= 0 && mix[j].ArtistID == mix[i].ArtistID && i < 6 {
				t.Errorf("artist repeated at %d and %d: %v", j, i, mix)
			}
		}
	}

	// Size caps the mix.
	if got := BuildMix([][]Candidate{seedPool, similar}, 2, nil); len(got) != 2 {
		t.Errorf("size cap: %d", len(got))
	}
	// When only one artist is left, it plays rather than ending the mix.
	if got := BuildMix([][]Candidate{seedPool}, 10, nil); len(got) != 3 {
		t.Errorf("single-artist pool: %d", len(got))
	}
}

func TestRoundRobinAndShuffle(t *testing.T) {
	a, b := uuid.New(), uuid.New()
	g1 := []Candidate{{TrackID: uuid.New(), ArtistID: a}, {TrackID: uuid.New(), ArtistID: a}, {TrackID: uuid.New(), ArtistID: a}}
	g2 := []Candidate{{TrackID: uuid.New(), ArtistID: b}}
	got := RoundRobin([][]Candidate{g1, g2})
	want := []Candidate{g1[0], g2[0], g1[1], g1[2]}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("round robin = %v", got)
		}
	}
	s := Shuffle(g1, rand.New(rand.NewSource(1)))
	if len(s) != 3 || &s[0] == &g1[0] {
		t.Error("shuffle returns a copy of the same length")
	}
}
