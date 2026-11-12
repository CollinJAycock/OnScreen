package transcode

import (
	"context"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestVODRemux_RunsSplice is the end-to-end guard for the VOD remux: with
// the production BuildHLS arguments, a run started at 0 and a run restarted
// at a planned boundary must produce the SAME video for every segment, at
// the SAME timestamps — that is what lets the segment handler serve one
// segment from one run and the next from another. Checked for MPEG-TS and
// fMP4 pieces, on a B-frame source (the case where ffmpeg's -ss and its
// negative-timestamp shift both misbehave without the fixes).
func TestVODRemux_RunsSplice(t *testing.T) {
	for _, bin := range []string{"ffmpeg", "ffprobe"} {
		if _, err := exec.LookPath(bin); err != nil {
			t.Skipf("%s not available", bin)
		}
	}
	dir := t.TempDir()
	src := filepath.Join(dir, "src.mkv")
	if out, err := exec.Command("ffmpeg", "-v", "error",
		"-f", "lavfi", "-i", "testsrc2=size=320x180:rate=24",
		"-f", "lavfi", "-i", "sine=frequency=440:sample_rate=48000",
		"-t", "40", "-c:v", "libx264", "-preset", "veryfast", "-bf", "2", "-g", "1000",
		"-force_key_frames", "0,1.5,4,4.5,9,13.2,14,20,21,27.5,30,31,38",
		"-c:a", "ac3", "-b:a", "192k", "-y", src).CombinedOutput(); err != nil {
		t.Fatalf("synthesise source: %v: %s", err, out)
	}
	kf, err := MKVVideoKeyframes(src)
	if err != nil {
		t.Fatal(err)
	}
	plan, ok := PlanRemux(kf, 40)
	if !ok {
		t.Fatalf("plan refused for keyframes %v", kf)
	}
	bf, fps := ProbeVideoDelay(context.Background(), src)
	if bf < 1 || math.Abs(fps-24) > 0.01 {
		t.Fatalf("ProbeVideoDelay = (%d, %v), want B-frames at 24 fps", bf, fps)
	}
	offset := RemuxTSOffset(bf, fps)
	// Restart at the boundary after 13.208 — a keyframe whose -ss without
	// the lead lands one GOP early.
	restartSeg := plan.SegmentAt(13.3)
	if plan.Starts[restartSeg] != 13.208 {
		t.Fatalf("plan %v: segment at 13.3 starts at %v, want 13.208", plan.Starts, plan.Starts[restartSeg])
	}

	for _, fmp4 := range []bool{false, true} {
		t.Run(fmt.Sprintf("fmp4=%v", fmp4), func(t *testing.T) {
			run := func(name string, seg int) (string, []float64, bool) {
				sess := filepath.Join(dir, fmt.Sprintf("%s-%v", name, fmp4))
				if err := os.MkdirAll(sess, 0o755); err != nil {
					t.Fatal(err)
				}
				args := BuildHLS(BuildArgs{
					InputPath:         src,
					StartOffset:       plan.SeekTarget(seg),
					OutputTSOffsetSec: offset,
					CopyTS:            true,
					KeyframePieces:    true,
					ForceFMP4:         fmp4,
					Encoder:           "copy",
					AudioCodec:        "aac",
					AudioChannels:     2,
					SessionDir:        sess,
					SegmentPrefix:     "seg",
				})
				cmd := exec.Command("ffmpeg", args...)
				cmd.Dir = sess
				if out, err := cmd.CombinedOutput(); err != nil {
					t.Fatalf("%s run: %v: %s", name, err, out)
				}
				pl, err := os.ReadFile(filepath.Join(sess, "index.m3u8"))
				if err != nil {
					t.Fatal(err)
				}
				durs, ended := ParseRunPieces(string(pl))
				return sess, durs, ended
			}
			fullDir, fullDurs, fullEnded := run("full", 0)
			reDir, reDurs, reEnded := run("restart", restartSeg)
			if !fullEnded || !reEnded {
				t.Fatal("runs did not finish (no ENDLIST)")
			}
			ext := ".ts"
			if fmp4 {
				ext = ".m4s"
			}
			for seg := restartSeg; seg < plan.Count(); seg++ {
				s, e := plan.Span(seg)
				last := seg == plan.Count()-1
				a := RemuxPieceSpan(0, fullDurs, true, s, e, last)
				b := RemuxPieceSpan(plan.Starts[restartSeg], reDurs, true, s, e, last)
				if !a.Ready || !b.Ready {
					t.Fatalf("segment %d [%v,%v): full %+v restart %+v", seg, s, e, a, b)
				}
				va := videoPackets(t, fullDir, a, ext, fmp4)
				vb := videoPackets(t, reDir, b, ext, fmp4)
				if va == "" || strings.Count(va, ";") < 10 {
					t.Fatalf("segment %d: ffprobe found almost no video packets (%q)", seg, va)
				}
				if va != vb {
					t.Fatalf("segment %d [%v,%v): video differs between runs\nfull:    %.200s\nrestart: %.200s", seg, s, e, va, vb)
				}
				if seg == restartSeg {
					first := strings.SplitN(vb, ";", 2)[0]
					var pts float64
					fmt.Sscanf(first, "%f", &pts)
					if math.Abs(pts-(s+offset)) > 0.01 {
						t.Errorf("restart's first video pts %.3f, want boundary %.3f + offset %.3f", pts, s, offset)
					}
				}
			}
		})
	}
}

// videoPackets returns "pts,size;..." for the video packets of pieces
// span.First..span.Last of a run, joined the way the segment handler serves
// them (init segment first for fMP4).
func videoPackets(t *testing.T, sess string, span PieceSpan, ext string, fmp4 bool) string {
	t.Helper()
	joined := filepath.Join(sess, fmt.Sprintf("joined-%d-%d%s", span.First, span.Last, ext))
	var buf []byte
	if fmp4 {
		b, err := os.ReadFile(filepath.Join(sess, "init.mp4"))
		if err != nil {
			t.Fatal(err)
		}
		buf = append(buf, b...)
	}
	for j := span.First; j <= span.Last; j++ {
		b, err := os.ReadFile(filepath.Join(sess, fmt.Sprintf("seg%05d%s", j, ext)))
		if err != nil {
			t.Fatal(err)
		}
		buf = append(buf, b...)
	}
	if err := os.WriteFile(joined, buf, 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command("ffprobe", "-v", "error", "-select_streams", "v:0",
		"-show_entries", "packet=pts_time,size", "-of", "csv=p=0", joined).Output()
	if err != nil {
		t.Fatalf("ffprobe %s: %v", joined, err)
	}
	return strings.ReplaceAll(strings.TrimSpace(string(out)), "\n", ";")
}
