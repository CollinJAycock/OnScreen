// Package musicrec ranks similar artists and orders instant mixes. It is
// the pure half of GET /music/artists/{id}/similar and GET /music/mix: the
// handlers fetch candidates (already filtered by library access, the rating
// ceiling and label restrictions) and this package scores and arranges them.
package musicrec

import (
	"math"
	"math/rand"
	"sort"
	"strings"

	"github.com/google/uuid"
)

// ArtistProfile is what similarity is judged on for one artist.
type ArtistProfile struct {
	ID         uuid.UUID
	Genres     []string // lower-cased
	MedianYear int      // 0 when unknown
	TrackCount int64
}

// SimilarArtist is a ranked result.
type SimilarArtist struct {
	ID uuid.UUID
	// Score is in [0, 1].
	Score float64
	// SharedGenres are the genres the artist shares with the seed.
	SharedGenres []string
	// CoListened is set when listeners played both artists.
	CoListened bool
}

// Blend weights. With listening data, co-listening dominates and shared
// genres refine it; without any, genre overlap carries the ranking and era
// breaks ties between artists of the same genres.
const (
	weightCoListen       = 0.6
	weightGenreWithData  = 0.3
	weightEraWithData    = 0.1
	weightGenreNoData    = 0.8
	weightEraNoData      = 0.2
	eraSpanYears         = 20.0 // years apart at which era similarity reaches 0
	eraOnlyMinSimilarity = 0.75 // ±5 years, when the seed has no genres at all
)

// RankSimilar scores every candidate against seed and returns the best
// limit. coListens holds, per artist, the summed distinct-listener counts
// of track pairs between it and the seed (item_cooccurrence aggregated to
// artists); it is normalised by both artists' track counts so a prolific
// artist doesn't win on volume alone.
//
// An artist qualifies through co-listening or a shared genre. When nobody
// has listened to the seed alongside anything, genre overlap ranks alone
// (era as a tie-breaker), and a seed with no genres falls back to artists
// of its era.
func RankSimilar(seed ArtistProfile, candidates []ArtistProfile, coListens map[uuid.UUID]int64, limit int) []SimilarArtist {
	seedGenres := toSet(seed.Genres)

	// Normalised co-listening, scaled so the strongest candidate is 1.
	co := map[uuid.UUID]float64{}
	var maxCo float64
	for _, c := range candidates {
		raw := coListens[c.ID]
		if raw <= 0 || c.ID == seed.ID {
			continue
		}
		v := float64(raw) / math.Sqrt(float64(max64(seed.TrackCount, 1))*float64(max64(c.TrackCount, 1)))
		co[c.ID] = v
		if v > maxCo {
			maxCo = v
		}
	}
	haveData := maxCo > 0

	var out []SimilarArtist
	for _, c := range candidates {
		if c.ID == seed.ID {
			continue
		}
		shared := intersect(seedGenres, c.Genres)
		genre := jaccard(seedGenres, toSet(c.Genres), len(shared))
		era := eraSimilarity(seed.MedianYear, c.MedianYear)
		var score float64
		coListened := false
		switch {
		case haveData:
			cv := co[c.ID] / maxCo
			if cv == 0 && genre == 0 {
				continue
			}
			coListened = cv > 0
			score = weightCoListen*cv + weightGenreWithData*genre + weightEraWithData*era
		case len(seedGenres) > 0:
			if genre == 0 {
				continue
			}
			score = weightGenreNoData*genre + weightEraNoData*era
		default:
			if era < eraOnlyMinSimilarity {
				continue
			}
			score = weightEraNoData * era
		}
		out = append(out, SimilarArtist{ID: c.ID, Score: round3(score), SharedGenres: shared, CoListened: coListened})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Score != out[j].Score {
			return out[i].Score > out[j].Score
		}
		return out[i].ID.String() < out[j].ID.String()
	})
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out
}

func eraSimilarity(a, b int) float64 {
	if a <= 0 || b <= 0 {
		return 0
	}
	d := math.Abs(float64(a - b))
	if d >= eraSpanYears {
		return 0
	}
	return 1 - d/eraSpanYears
}

func toSet(genres []string) map[string]bool {
	s := make(map[string]bool, len(genres))
	for _, g := range genres {
		if g = strings.ToLower(strings.TrimSpace(g)); g != "" {
			s[g] = true
		}
	}
	return s
}

// intersect lists the genres of other that are in set, sorted.
func intersect(set map[string]bool, other []string) []string {
	var out []string
	seen := map[string]bool{}
	for _, g := range other {
		g = strings.ToLower(strings.TrimSpace(g))
		if set[g] && !seen[g] {
			seen[g] = true
			out = append(out, g)
		}
	}
	sort.Strings(out)
	return out
}

func jaccard(a, b map[string]bool, shared int) float64 {
	union := len(a) + len(b) - shared
	if union == 0 || shared == 0 {
		return 0
	}
	return float64(shared) / float64(union)
}

func round3(v float64) float64 { return math.Round(v*1000) / 1000 }

func max64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}

// Candidate is one track offered to a mix.
type Candidate struct {
	TrackID  uuid.UUID
	ArtistID uuid.UUID // uuid.Nil when unknown; never spaced
}

// ArtistGap is how many other tracks must separate two by the same artist
// in a mix, while the pools allow it.
const ArtistGap = 2

// BuildMix arranges up to size tracks from pools, which are in priority
// order (the seed's own tracks, then similar artists', then genre matches).
// Each slot takes the first remaining candidate, in pool order, whose artist
// hasn't played in the last ArtistGap slots; when every remaining candidate
// would repeat an artist that soon, the first remaining one plays anyway.
// So the seed's tracks come early but are spaced out by the others. A track
// appears once. pinFirst, when set, is placed first regardless (the seed
// track of a mix started from a track).
func BuildMix(pools [][]Candidate, size int, pinFirst *Candidate) []Candidate {
	var queue []Candidate
	seen := map[uuid.UUID]bool{}
	if pinFirst != nil {
		seen[pinFirst.TrackID] = true
	}
	for _, p := range pools {
		for _, c := range p {
			if seen[c.TrackID] {
				continue
			}
			seen[c.TrackID] = true
			queue = append(queue, c)
		}
	}
	var out []Candidate
	if pinFirst != nil && size > 0 {
		out = append(out, *pinFirst)
	}
	for len(out) < size && len(queue) > 0 {
		pick := 0
		for i, c := range queue {
			if !recentlyPlayed(out, c.ArtistID) {
				pick = i
				break
			}
		}
		out = append(out, queue[pick])
		queue = append(queue[:pick], queue[pick+1:]...)
	}
	return out
}

func recentlyPlayed(out []Candidate, artist uuid.UUID) bool {
	if artist == uuid.Nil {
		return false
	}
	for i := len(out) - 1; i >= 0 && i >= len(out)-ArtistGap; i-- {
		if out[i].ArtistID == artist {
			return true
		}
	}
	return false
}

// Shuffle returns a shuffled copy of cs.
func Shuffle(cs []Candidate, rnd *rand.Rand) []Candidate {
	out := append([]Candidate(nil), cs...)
	rnd.Shuffle(len(out), func(i, j int) { out[i], out[j] = out[j], out[i] })
	return out
}

// RoundRobin interleaves groups — one candidate from each group in turn,
// in group order — so the most similar artist leads each round without
// playing all its tracks back to back.
func RoundRobin(groups [][]Candidate) []Candidate {
	var out []Candidate
	for i := 0; ; i++ {
		added := false
		for _, g := range groups {
			if i < len(g) {
				out = append(out, g[i])
				added = true
			}
		}
		if !added {
			return out
		}
	}
}
