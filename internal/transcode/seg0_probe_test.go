package transcode

import (
	"context"
	"testing"
	"time"
)

// TestWaitForSeg0Audio_FMP4DeclinesImmediately guards a LATENCY property, not a
// correctness one — which is why it asserts on elapsed time.
//
// The probe locates segments on disk by filename. It only understands MPEG-TS
// (a bare .m4s fragment is moof+mdat with no moov, so ffprobe cannot parse one
// without its init.mp4). Its caller runs on every seeked remux, and an HEVC or
// AV1 remux is fMP4 — so if the probe blocks looking for .ts files that will
// never exist, the full timeout is added to the start latency of every one of
// those playbacks. Nothing breaks; playback just gets slower to begin, which is
// exactly the kind of regression that ships unnoticed.
func TestWaitForSeg0Audio_FMP4DeclinesImmediately(t *testing.T) {
	const timeout = 3 * time.Second

	for _, tc := range []struct {
		name string
		vout VideoOutput
	}{
		{"HEVC remux", VideoOutput{HEVC: true}},
		{"AV1 remux", VideoOutput{AV1: true}},
		{"H.264 forced into fMP4", VideoOutput{ForcedFMP4: true}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// An empty dir: the mpegts path would block here for the whole
			// timeout waiting on seg00001.ts.
			dir := t.TempDir()

			start := time.Now()
			gap, ok := WaitForSeg0Audio(context.Background(), dir, tc.vout, timeout)
			elapsed := time.Since(start)

			if ok {
				t.Errorf("want ok=false for an unprobeable fMP4 session, got gap=%v", gap)
			}
			if elapsed > timeout/2 {
				t.Errorf("blocked %v of a %v timeout — the probe is waiting for .ts "+
					"files an fMP4 session never writes, and that wait lands on "+
					"playback start latency", elapsed, timeout)
			}
		})
	}
}

// TestWaitForSeg0Audio_MPEGTSStillWaits pins the other side: the probe must NOT
// short-circuit for a session it genuinely can read, or the silent-head trim it
// exists to provide would quietly stop happening for H.264 remuxes.
func TestWaitForSeg0Audio_MPEGTSStillWaits(t *testing.T) {
	const timeout = 300 * time.Millisecond
	dir := t.TempDir()

	start := time.Now()
	if _, ok := WaitForSeg0Audio(context.Background(), dir, VideoOutput{}, timeout); ok {
		t.Fatal("want ok=false when no segments exist")
	}
	// It should have spent the timeout waiting for seg00001.ts rather than
	// bailing out on the fMP4 fast path.
	if elapsed := time.Since(start); elapsed < timeout {
		t.Errorf("returned after %v without waiting out the %v timeout — the "+
			"mpegts path must still wait for segments to land", elapsed, timeout)
	}
}
