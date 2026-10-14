package loudness

import (
	"context"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const sampleSummary = `[Parsed_ebur128_0 @ 000002523ddd7740] t: 4.9 TARGET:-23 LUFS M: -31.1 S: -31.1 I: -31.1 LUFS LRA: 0.0 LU FTPK: -28.1 dBFS TPK: -28.1 dBFS
[Parsed_ebur128_0 @ 000002523ddd7740] Summary:

  Integrated loudness:
    I:         -31.1 LUFS
    Threshold: -41.1 LUFS

  Loudness range:
    LRA:         0.0 LU
    Threshold: -51.1 LUFS
    LRA low:   -31.1 LUFS
    LRA high:  -31.1 LUFS

  True peak:
    Peak:      -28.1 dBFS
[out#0/null @ 000002523dda9f40] video:0KiB audio:431KiB subtitle:0KiB
`

const silentSummary = `[Parsed_ebur128_0 @ 0x1] Summary:

  Integrated loudness:
    I:         -70.0 LUFS
    Threshold:   0.0 LUFS

  Loudness range:
    LRA:         0.0 LU
    Threshold:   0.0 LUFS
    LRA low:     0.0 LUFS
    LRA high:    0.0 LUFS

  True peak:
    Peak:       -inf dBFS
`

func TestParseSummary(t *testing.T) {
	m, ok := ParseSummary(sampleSummary)
	if !ok {
		t.Fatal("summary not found")
	}
	// The per-frame line before the summary also says "I: -31.1 LUFS"; the
	// parse must read the summary's own value, and the peak is linear.
	if m.IntegratedLUFS == nil || *m.IntegratedLUFS != -31.1 {
		t.Fatalf("integrated = %v, want -31.1", m.IntegratedLUFS)
	}
	if m.TruePeak == nil || math.Abs(*m.TruePeak-math.Pow(10, -28.1/20)) > 1e-6 {
		t.Fatalf("true peak = %v", m.TruePeak)
	}

	s, ok := ParseSummary(silentSummary)
	if !ok {
		t.Fatal("silent summary not found")
	}
	if s.IntegratedLUFS != nil || s.TruePeak != nil {
		t.Errorf("silence must have no loudness and no peak: %+v", s)
	}

	if _, ok := ParseSummary("Input #0, flac\nno summary here"); ok {
		t.Error("output without a summary must not parse")
	}
}

func TestReplayGainConversions(t *testing.T) {
	// ReplayGain 2.0: −18 LUFS reference.
	if g := TrackGain(-31.1); g != 13.1 {
		t.Errorf("TrackGain(-31.1) = %v, want 13.1", g)
	}
	if g := TrackGain(-8.37); g != -9.63 {
		t.Errorf("TrackGain(-8.37) = %v, want -9.63", g)
	}
	if p := LinearPeak(0); p != 1 {
		t.Errorf("LinearPeak(0) = %v, want 1", p)
	}
	// Inter-sample overs exceed full scale and must not be clamped.
	if p := LinearPeak(0.5); p <= 1 {
		t.Errorf("LinearPeak(+0.5 dBTP) = %v, want > 1", p)
	}
}

func TestAlbumLoudnessIsEnergyWeighted(t *testing.T) {
	// Two equally long tracks at −10 and −20 LUFS: the energy mean is
	// 10·log10((10^-1 + 10^-2)/2) ≈ −12.60 LUFS, not the dB mean of −15.
	got, ok := AlbumLoudness([]Track{{-10, 180000}, {-20, 180000}})
	if !ok || math.Abs(got-(-12.596)) > 0.001 {
		t.Fatalf("album loudness = %v (ok=%v), want ≈ -12.596", got, ok)
	}
	// Duration weights: a long loud track outweighs a short quiet one.
	long, _ := AlbumLoudness([]Track{{-10, 600000}, {-30, 60000}})
	short, _ := AlbumLoudness([]Track{{-10, 60000}, {-30, 600000}})
	if !(long > short) {
		t.Errorf("duration weighting: long-loud %v should be louder than short-loud %v", long, short)
	}
	// One track: its own loudness.
	if one, _ := AlbumLoudness([]Track{{-14.2, 1000}}); math.Abs(one-(-14.2)) > 1e-9 {
		t.Errorf("single track album = %v", one)
	}
	// No durations: no album loudness.
	if _, ok := AlbumLoudness([]Track{{-10, 0}}); ok {
		t.Error("tracks without duration must not yield an album loudness")
	}
}

func TestTailBufferKeepsTheEnd(t *testing.T) {
	b := &tailBuffer{max: 8}
	b.Write([]byte("abcdef"))
	b.Write([]byte("ghij"))
	if got := b.String(); got != "cdefghij" {
		t.Errorf("tail = %q", got)
	}
	b.Write([]byte("0123456789"))
	if got := b.String(); got != "23456789" {
		t.Errorf("tail after a long write = %q", got)
	}
}

// TestMeasureWithFFmpeg runs the real filter on generated audio. Skipped
// when ffmpeg isn't on PATH.
func TestMeasureWithFFmpeg(t *testing.T) {
	if testing.Short() {
		t.Skip("runs ffmpeg")
	}
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not on PATH")
	}
	dir := t.TempDir()
	tone := filepath.Join(dir, "tone.flac")
	silence := filepath.Join(dir, "silence.flac")
	gen := func(out string, args ...string) {
		cmd := exec.Command("ffmpeg", append([]string{"-hide_banner", "-loglevel", "error", "-y"}, append(args, out)...)...)
		if b, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("generate %s: %v %s", out, err, b)
		}
	}
	gen(tone, "-f", "lavfi", "-i", "sine=frequency=1000:duration=5", "-af", "volume=-10dB", "-c:a", "flac")
	gen(silence, "-f", "lavfi", "-i", "anullsrc=r=44100:cl=stereo", "-t", "3", "-c:a", "flac")

	m, err := Measure(context.Background(), tone)
	if err != nil || m.Error != "" {
		t.Fatalf("measure tone: %v %q", err, m.Error)
	}
	if m.IntegratedLUFS == nil || math.Abs(*m.IntegratedLUFS-(-31.1)) > 0.3 {
		t.Errorf("tone loudness = %v, want ≈ -31.1 LUFS", m.IntegratedLUFS)
	}
	if m.TruePeak == nil || math.Abs(*m.TruePeak-math.Pow(10, -28.1/20)) > 0.01 {
		t.Errorf("tone true peak = %v", m.TruePeak)
	}

	s, err := Measure(context.Background(), silence)
	if err != nil || s.Error != "" || s.IntegratedLUFS != nil {
		t.Errorf("silence: %+v %v", s, err)
	}

	// Not audio at all: ffmpeg runs and fails — a file-level error, not a
	// tool failure.
	junk := filepath.Join(dir, "junk.flac")
	if err := os.WriteFile(junk, []byte(strings.Repeat("not audio ", 100)), 0o600); err != nil {
		t.Fatal(err)
	}
	j, err := Measure(context.Background(), junk)
	if err != nil || j.Error == "" {
		t.Errorf("junk input: measurement %+v err %v, want a file-level error", j, err)
	}
}
