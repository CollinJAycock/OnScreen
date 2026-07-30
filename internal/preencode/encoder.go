// Package preencode implements staticabr.LadderEncoder: it runs ffmpeg to
// pre-encode a source's full ABR ladder to VOD HLS and writes the segments,
// per-rung playlists, master playlist, and source-hash sidecar back through the
// media store (HA roadmap §5). Software encoders are used for portability; the
// fleet can later distribute these jobs.
package preencode

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/mediastore"
	"github.com/onscreen/onscreen/internal/staticabr"
	"github.com/onscreen/onscreen/internal/transcode"
)

// h264MasterCodecs is the HLS CODECS attribute for an H.264 + AAC variant
// (High@4.0). HEVC/AV1 use the level-aware helpers in the transcode package.
const h264MasterCodecs = "avc1.640028,mp4a.40.2"

// Encoder pre-encodes ABR ladders via ffmpeg and stores them through a media
// store. Implements staticabr.LadderEncoder.
type Encoder struct {
	store      mediastore.Store
	keyPrefix  string // prepended to every static key; "" for bucket-relative (S3)
	ffmpegPath string
	segmentSec int
	maxHeight  int // ladder height cap (0 = none)
	logger     *slog.Logger
}

var _ staticabr.LadderEncoder = (*Encoder)(nil)

// New builds an Encoder. keyPrefix is "" for object storage (keys are
// bucket-relative); set it to a directory for a local-disk static root. ffmpeg is
// the binary name/path; segmentSec is the HLS target duration.
func New(store mediastore.Store, keyPrefix, ffmpeg string, segmentSec, maxHeight int, logger *slog.Logger) *Encoder {
	if segmentSec <= 0 {
		segmentSec = 6
	}
	if ffmpeg == "" {
		ffmpeg = "ffmpeg"
	}
	return &Encoder{store: store, keyPrefix: keyPrefix, ffmpegPath: ffmpeg, segmentSec: segmentSec, maxHeight: maxHeight, logger: logger}
}

func (e *Encoder) key(rel string) string { return path.Join(e.keyPrefix, rel) }

// Encode runs the full ladder encode and writes it through the store. The
// source-hash sidecar is written LAST, so a crash mid-encode leaves the ladder
// "stale" (StoreChecker requires both master + matching hash) and it re-encodes
// next run rather than serving a partial ladder.
func (e *Encoder) Encode(ctx context.Context, src staticabr.Source) error {
	putter, ok := e.store.(mediastore.Putter)
	if !ok {
		return fmt.Errorf("preencode: media store is read-only, cannot write static ladder")
	}

	// ffmpeg input: a signed URL when the source is in object storage, else the
	// local path (Local.SignedURL returns "").
	input := src.FilePath
	if u, err := e.store.SignedURL(ctx, src.FilePath, time.Hour); err == nil && u != "" {
		input = u
	}

	rungs := // Pre-encode is an operator-driven batch job with no requesting user, so
		// there is no per-user cap to apply; 0 = unrestricted.
		transcode.BuildLadder(src.Width, src.Height, src.BitrateKbps, src.Codec, e.maxHeight, 0)
	if len(rungs) == 0 {
		return fmt.Errorf("preencode: no ladder rungs for %s", src.FileID)
	}

	tmp, err := os.MkdirTemp("", "preencode-")
	if err != nil {
		return fmt.Errorf("preencode: temp dir: %w", err)
	}
	defer os.RemoveAll(tmp)

	for _, r := range rungs {
		rdir := filepath.Join(tmp, r.Label)
		if err := os.MkdirAll(rdir, 0o755); err != nil {
			return err
		}
		cmd := exec.CommandContext(ctx, e.ffmpegPath, e.rungArgs(input, src.Codec, r, rdir)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("preencode: ffmpeg rung %s: %w: %s", r.Label, err, tail(out, 600))
		}
		if err := e.putRung(ctx, putter, rdir, src.FileID, r.Label); err != nil {
			return err
		}
	}

	master := transcode.BuildMasterPlaylist(rungs, e.masterCodecs(src.Codec, rungs[0].Height),
		func(r transcode.Rendition) string { return r.Label + "/index.m3u8" })
	if err := putter.Put(ctx, e.key(staticabr.MasterKey(src.FileID)), []byte(master)); err != nil {
		return fmt.Errorf("preencode: put master: %w", err)
	}
	if err := putter.Put(ctx, e.key(staticabr.HashKey(src.FileID)), []byte(src.Hash)); err != nil {
		return fmt.Errorf("preencode: put hash sidecar: %w", err)
	}
	e.logger.InfoContext(ctx, "static-abr: pre-encoded ladder", "file_id", src.FileID, "rungs", len(rungs))
	return nil
}

// putRung uploads every file ffmpeg produced for a rung (the index.m3u8 + its
// segments) under the rung's static keys.
func (e *Encoder) putRung(ctx context.Context, putter mediastore.Putter, rdir string, fileID uuid.UUID, rung string) error {
	entries, err := os.ReadDir(rdir)
	if err != nil {
		return err
	}
	for _, ent := range entries {
		if ent.IsDir() {
			continue
		}
		data, err := os.ReadFile(filepath.Join(rdir, ent.Name()))
		if err != nil {
			return err
		}
		var key string
		if ent.Name() == "index.m3u8" {
			key = staticabr.RungPlaylistKey(fileID, rung)
		} else {
			key = staticabr.SegmentKey(fileID, rung, ent.Name())
		}
		if err := putter.Put(ctx, e.key(key), data); err != nil {
			return fmt.Errorf("preencode: put %s: %w", key, err)
		}
	}
	return nil
}

// rungArgs builds the ffmpeg command for one rung: scale to the rung height,
// software-encode video at the rung bitrate, AAC stereo audio, VOD HLS output.
func (e *Encoder) rungArgs(input, codec string, r transcode.Rendition, rdir string) []string {
	enc := transcode.EncoderSoftware
	switch codec {
	case transcode.LadderHEVC:
		enc = transcode.EncoderHEVCSoftware
	case transcode.LadderAV1:
		enc = transcode.EncoderAV1Software
	}

	args := []string{"-nostdin", "-y"}
	// Reconnect on flaky object-storage/CDN reads when the input is a URL.
	if strings.Contains(input, "://") {
		args = append(args, "-reconnect", "1", "-reconnect_streamed", "1", "-reconnect_delay_max", "5")
	}
	args = append(args,
		"-i", input,
		"-map", "0:v:0", "-map", "0:a:0?",
		"-vf", fmt.Sprintf("scale=-2:%d", r.Height),
		"-c:v", string(enc),
		"-preset", "veryfast",
		"-b:v", fmt.Sprintf("%dk", r.BitrateKbps),
		"-maxrate", fmt.Sprintf("%dk", r.BitrateKbps*11/10),
		"-bufsize", fmt.Sprintf("%dk", r.BitrateKbps*2),
	)
	if codec == transcode.LadderHEVC {
		args = append(args, "-tag:v", "hvc1") // Safari/HLS requires hvc1, not hev1
	}
	args = append(args,
		"-c:a", "aac", "-b:a", "128k", "-ac", "2",
		"-f", "hls",
		"-hls_time", strconv.Itoa(e.segmentSec),
		"-hls_playlist_type", "vod",
		"-hls_flags", "independent_segments",
		"-hls_segment_filename", filepath.Join(rdir, "seg%05d.ts"),
		filepath.Join(rdir, "index.m3u8"),
	)
	return args
}

func (e *Encoder) masterCodecs(codec string, maxHeight int) string {
	switch codec {
	case transcode.LadderHEVC:
		return transcode.HEVCMasterCodecs(maxHeight)
	case transcode.LadderAV1:
		return transcode.AV1MasterCodecs(maxHeight)
	default:
		return h264MasterCodecs
	}
}

// tail returns the last n bytes of b (ffmpeg's error tail) for log messages.
func tail(b []byte, n int) string {
	if len(b) > n {
		b = b[len(b)-n:]
	}
	return strings.TrimSpace(string(b))
}
