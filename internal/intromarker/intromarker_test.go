package intromarker

import (
	"context"
	"math/rand/v2"
	"strings"
	"testing"

	"github.com/google/uuid"
)

// ----- parseFpcalcOutput -----

func TestParseFpcalcOutput_HappyPath(t *testing.T) {
	out := "DURATION=600\nFINGERPRINT=1,2,3,4,5\n"
	fp, err := parseFpcalcOutput(out)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	want := []uint32{1, 2, 3, 4, 5}
	if len(fp) != len(want) {
		t.Fatalf("len: got %d, want %d", len(fp), len(want))
	}
	for i, v := range want {
		if fp[i] != v {
			t.Errorf("fp[%d]: got %d, want %d", i, fp[i], v)
		}
	}
}

func TestParseFpcalcOutput_ToleratesLineOrder(t *testing.T) {
	// FINGERPRINT before DURATION should still parse.
	out := "FINGERPRINT=10,20\nDURATION=5\n"
	fp, err := parseFpcalcOutput(out)
	if err != nil || len(fp) != 2 || fp[0] != 10 || fp[1] != 20 {
		t.Errorf("got %v err=%v", fp, err)
	}
}

func TestParseFpcalcOutput_HandlesNegativeInts(t *testing.T) {
	// fpcalc emits signed 32-bit integers in -raw mode; uint32 cast is required.
	out := "FINGERPRINT=-2147483648,-1,0,1\n"
	fp, err := parseFpcalcOutput(out)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(fp) != 4 {
		t.Fatalf("len: %d", len(fp))
	}
	if fp[0] != uint32(0x80000000) || fp[1] != ^uint32(0) {
		t.Errorf("signed→unsigned cast mishandled: %v", fp)
	}
}

func TestParseFpcalcOutput_NoFingerprintLine(t *testing.T) {
	if _, err := parseFpcalcOutput("DURATION=600\nFOO=bar\n"); err == nil {
		t.Errorf("expected error when FINGERPRINT line missing")
	}
}

func TestParseFpcalcOutput_GarbageInt(t *testing.T) {
	_, err := parseFpcalcOutput("FINGERPRINT=1,not-a-number,3\n")
	if err == nil || !strings.Contains(err.Error(), "parse fingerprint") {
		t.Errorf("expected parse error, got %v", err)
	}
}

// ----- framesToMs -----

func TestFramesToMs_ZeroAndScale(t *testing.T) {
	if framesToMs(0) != 0 {
		t.Errorf("framesToMs(0) != 0")
	}
	// Each frame ≈ 123.8ms, so 100 frames ≈ 12380ms.
	got := framesToMs(100)
	if got < 12000 || got > 12500 {
		t.Errorf("framesToMs(100) = %d, want ~12380", got)
	}
}

// ----- alignPair -----

func TestAlignPair_FindsIdenticalRun(t *testing.T) {
	common := make([]uint32, 60)
	for i := range common {
		common[i] = uint32(i*7919 + 1) // arbitrary but deterministic
	}
	a := append([]uint32{0xAAAA, 0xBBBB, 0xCCCC}, common...)
	a = append(a, 0xDEAD, 0xBEEF)
	b := append([]uint32{0x1111, 0x2222}, common...)
	b = append(b, 0x3333, 0x4444, 0x5555)

	al := alignPair(a, b)
	if al.length < 60 {
		t.Errorf("expected to align ≥60 frames, got %d", al.length)
	}
	if al.aStart != 3 {
		t.Errorf("aStart: got %d, want 3", al.aStart)
	}
	if al.bStart != 2 {
		t.Errorf("bStart: got %d, want 2", al.bStart)
	}
	if al.score < 0.95 {
		t.Errorf("score: got %f, want >0.95 for identical run", al.score)
	}
}

func TestAlignPair_NoMatchOnRandomData(t *testing.T) {
	// Two completely different sequences; we should NOT confidently align.
	a := []uint32{0x00000000, 0xFFFFFFFF, 0x12345678, 0x87654321}
	b := []uint32{0xAAAAAAAA, 0x55555555, 0xDEADBEEF, 0xCAFEBABE}
	al := alignPair(a, b)
	// Best run might be 0 or 1 frame by luck, but never the full length.
	if al.length >= len(a) {
		t.Errorf("aligned %d frames on random data, expected sparse alignment", al.length)
	}
}

func TestAlignPair_EmptyInputs(t *testing.T) {
	if got := alignPair(nil, []uint32{1, 2}); got.length != 0 {
		t.Errorf("nil a: got length %d, want 0", got.length)
	}
	if got := alignPair([]uint32{1, 2}, nil); got.length != 0 {
		t.Errorf("nil b: got length %d, want 0", got.length)
	}
}

// ----- detectSeasonIntros -----

func TestDetectSeasonIntros_FindsSharedIntroAcrossEpisodes(t *testing.T) {
	// 5 episodes, each starting with a 60-frame shared intro then unique tail.
	// Use PCG-seeded PRNG for tails so they look like real audio fingerprints
	// (no spurious alignments at structured bit patterns).
	intro := make([]uint32, 60)
	introRng := rand.New(rand.NewPCG(0xC0FFEE, 0xBABE))
	for i := range intro {
		intro[i] = introRng.Uint32()
	}
	fps := make([][]uint32, 5)
	for ep := range fps {
		rng := rand.New(rand.NewPCG(uint64(ep+1)*100003, uint64(ep+1)*200023))
		uniq := make([]uint32, 200)
		for j := range uniq {
			uniq[j] = rng.Uint32()
		}
		fps[ep] = append(append([]uint32{}, intro...), uniq...)
	}

	out := detectSeasonIntros(fps)
	if len(out) != 5 {
		t.Fatalf("want 5 episode results, got %d", len(out))
	}
	for i, ei := range out {
		if ei.lengthFrames < minIntroFrames {
			t.Errorf("ep %d: lengthFrames=%d, want ≥%d", i, ei.lengthFrames, minIntroFrames)
		}
		if ei.startFrame != 0 {
			t.Errorf("ep %d: startFrame=%d, want 0 (intro at very start)", i, ei.startFrame)
		}
	}
}

func TestDetectSeasonIntros_TooFewEpisodes(t *testing.T) {
	out := detectSeasonIntros([][]uint32{{1, 2, 3}})
	if len(out) != 1 {
		t.Errorf("len: got %d, want 1", len(out))
	}
	if out[0].lengthFrames != 0 {
		t.Errorf("singleton season should produce no intro, got length %d", out[0].lengthFrames)
	}
}

func TestDetectSeasonIntros_NoSharedSequence(t *testing.T) {
	// Each episode entirely random — no cluster should win.
	fps := make([][]uint32, 4)
	for i := range fps {
		rng := rand.New(rand.NewPCG(uint64(i+1)*999331, uint64(i+1)*888523))
		fps[i] = make([]uint32, 200)
		for j := range fps[i] {
			fps[i][j] = rng.Uint32()
		}
	}
	out := detectSeasonIntros(fps)
	for i, ei := range out {
		if ei.lengthFrames >= minIntroFrames {
			t.Errorf("ep %d: false-positive intro with length=%d", i, ei.lengthFrames)
		}
	}
}

// ----- clusterBest / abs -----

func TestAbs(t *testing.T) {
	if abs(-5) != 5 || abs(5) != 5 || abs(0) != 0 {
		t.Errorf("abs broken")
	}
}

func TestClusterBest_PicksLargestCluster(t *testing.T) {
	cands := []cand{
		{start: 100, length: 80, score: 0.9},
		{start: 102, length: 82, score: 0.91},
		{start: 99, length: 81, score: 0.92},
		{start: 5000, length: 50, score: 0.6}, // outlier
	}
	best := clusterBest(cands)
	if best.startFrame < 95 || best.startFrame > 110 {
		t.Errorf("clusterBest start: got %d, want ~100 (the dense cluster)", best.startFrame)
	}
}

func TestClusterBest_EmptyReturnsZero(t *testing.T) {
	got := clusterBest(nil)
	if got.startFrame != 0 || got.lengthFrames != 0 {
		t.Errorf("empty cands: got %+v, want zero", got)
	}
}

// ----- Store validation -----

// These tests exercise validation paths that return BEFORE touching the DB,
// so a nil *pgxpool.Pool is safe. Anything that reaches gen.New(pool) will
// panic on the nil deref — which is exactly the assertion we want for the
// "validation must run first" contract.

func TestStore_Upsert_RejectsInvalidKind(t *testing.T) {
	s := NewStore(nil)
	_, err := s.Upsert(context.Background(), uuid.New(), "outro", 0, 1000)
	if err == nil || !strings.Contains(err.Error(), "invalid kind") {
		t.Errorf("got %v, want invalid kind error", err)
	}
}

func TestStore_Upsert_RejectsInvalidRange(t *testing.T) {
	s := NewStore(nil)
	cases := []struct {
		name           string
		start, end     int64
	}{
		{"negative start", -1, 1000},
		{"end equals start", 500, 500},
		{"end before start", 500, 100},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := s.Upsert(context.Background(), uuid.New(), "intro", c.start, c.end)
			if err == nil || !strings.Contains(err.Error(), "invalid range") {
				t.Errorf("got %v, want invalid range error", err)
			}
		})
	}
}
