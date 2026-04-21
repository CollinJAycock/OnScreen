package intromarker

import "math/bits"

// alignment describes the best-matching fingerprint run between two episodes:
// aStart/bStart are frame indices into each episode's fingerprint; length is
// the number of contiguous frames that line up (under the popcount threshold).
type alignment struct {
	aStart int
	bStart int
	length int
	score  float64 // mean similarity (higher is better), in [0,1]
}

// matchBitThreshold — two fingerprint frames are considered "similar" when
// their 32-bit popcount XOR is below this value. Chromaprint uses 6 in the
// reference implementation; we follow suit.
const matchBitThreshold = 6

// minIntroFrames — refuse to emit an intro shorter than ~5s. Below that the
// match is almost certainly spurious (a shared sting or quiet beat).
const minIntroFrames = 40

// alignPair finds the longest contiguous region where frames of a and b
// match under matchBitThreshold. Uses the classic chromaprint offset-search:
// for each candidate offset (bStart - aStart), count aligned matches and
// keep the best run. Fast, O(n*m) worst case but bounded by the intro-window
// (we only fingerprint the first N seconds, so n,m are both small).
func alignPair(a, b []uint32) alignment {
	best := alignment{}
	if len(a) == 0 || len(b) == 0 {
		return best
	}
	// Offsets range from -(len(b)-1) to +(len(a)-1). We iterate offsets and
	// for each offset walk the overlapping window, tracking longest run of
	// sub-threshold popcount distances.
	for off := -(len(b) - 1); off <= len(a)-1; off++ {
		iStart, jStart := 0, 0
		if off >= 0 {
			iStart = off
		} else {
			jStart = -off
		}
		runStart := -1
		var runSim float64
		var runLen int
		emit := func() {
			if runLen > best.length {
				best = alignment{
					aStart: runStart,
					bStart: runStart - off,
					length: runLen,
					score:  1 - runSim/float64(runLen)/32.0,
				}
			}
		}
		i, j := iStart, jStart
		for i < len(a) && j < len(b) {
			d := bits.OnesCount32(a[i] ^ b[j])
			if d <= matchBitThreshold {
				if runStart < 0 {
					runStart = i
					runSim = 0
					runLen = 0
				}
				runSim += float64(d)
				runLen++
			} else if runStart >= 0 {
				emit()
				runStart = -1
			}
			i++
			j++
		}
		if runStart >= 0 {
			emit()
		}
	}
	return best
}

// bestSeasonAlignment runs alignPair across every unordered (i, j) pair of
// episode fingerprints and returns the (aStart, length) that appears most
// often — this is the common intro shared across the season. episodeIdx is
// the episode whose frame coordinates the returned alignment is expressed in.
//
// We accumulate per-episode: the median alignment start for that episode
// across all its pairings. The goal is to be robust to one-off mismatches
// (e.g. an episode with a cold open that shifts the intro by 60 seconds).
type episodeIntro struct {
	startFrame int
	lengthFrames int
	score      float64
}

func detectSeasonIntros(fps [][]uint32) []episodeIntro {
	out := make([]episodeIntro, len(fps))
	if len(fps) < 2 {
		return out
	}
	// For each episode, collect alignments against every other episode.
	for i := range fps {
		var cands []cand
		for j := range fps {
			if i == j {
				continue
			}
			al := alignPair(fps[i], fps[j])
			if al.length < minIntroFrames {
				continue
			}
			cands = append(cands, cand{al.aStart, al.length, al.score})
		}
		if len(cands) == 0 {
			continue
		}
		// Vote by start frame: cluster candidates whose starts fall within
		// ~2 seconds (~16 frames) of each other and pick the largest cluster.
		best := clusterBest(cands)
		out[i] = best
	}
	return out
}

func clusterBest(cands []cand) episodeIntro {
	const clusterTol = 16
	var best episodeIntro
	var bestCount int
	for i := range cands {
		var sumStart, sumLen, count int
		var sumScore float64
		for j := range cands {
			if abs(cands[j].start-cands[i].start) <= clusterTol {
				sumStart += cands[j].start
				sumLen += cands[j].length
				sumScore += cands[j].score
				count++
			}
		}
		if count > bestCount {
			bestCount = count
			best = episodeIntro{
				startFrame:   sumStart / count,
				lengthFrames: sumLen / count,
				score:        sumScore / float64(count),
			}
		}
	}
	return best
}

type cand struct {
	start  int
	length int
	score  float64
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}
