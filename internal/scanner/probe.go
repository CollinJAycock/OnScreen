package scanner

import (
	"context"
	"encoding/json"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// ProbeResult holds the technical metadata extracted from a media file by ffprobe.
type ProbeResult struct {
	Container   *string
	VideoCodec  *string
	AudioCodec  *string
	ResolutionW *int
	ResolutionH *int
	Bitrate     *int64
	DurationMs  *int64
	HDRType     *string
	FrameRate   *float64
	// VideoBitDepth is the primary video stream's bit depth (8/10/12), from
	// pix_fmt. Distinct from BitDepth (audio). nil when not a video stream.
	VideoBitDepth   *int
	AudioStreams    []byte
	SubtitleStreams []byte
	Chapters        []byte
	// Audiophile-grade fields. Populated for the first audio stream when the
	// file is audio-only (music library) — left nil otherwise so video rows
	// don't accumulate stereo 48 kHz metadata that is redundant with the
	// audio_streams JSONB. lossless is derived from the extension/codec at
	// scan time, not from ffprobe directly.
	BitDepth      *int
	SampleRate    *int
	ChannelLayout *string
	Lossless      *bool
}

// ffprobeOutput is the top-level ffprobe JSON output structure.
type ffprobeOutput struct {
	Streams  []ffprobeStream  `json:"streams"`
	Format   ffprobeFormat    `json:"format"`
	Chapters []ffprobeChapter `json:"chapters"`
}

type ffprobeStream struct {
	Index            int               `json:"index"`
	CodecName        string            `json:"codec_name"`
	CodecType        string            `json:"codec_type"`
	Width            int               `json:"width"`
	Height           int               `json:"height"`
	RFrameRate       string            `json:"r_frame_rate"`
	BitRate          string            `json:"bit_rate"`
	Channels         int               `json:"channels"`
	ChannelLayout    string            `json:"channel_layout"`
	SampleRate       string            `json:"sample_rate"`
	BitsPerRawSample string            `json:"bits_per_raw_sample"`
	BitsPerSample    int               `json:"bits_per_sample"`
	PixFmt           string            `json:"pix_fmt"`
	Tags             map[string]string `json:"tags"`
	Disposition      map[string]int    `json:"disposition"`
	ColorTransfer    string            `json:"color_transfer"`
	ColorPrimaries   string            `json:"color_primaries"`
	SideDataList     []ffprobeSideData `json:"side_data_list"`
}

type ffprobeSideData struct {
	SideDataType string `json:"side_data_type"`
}

type ffprobeFormat struct {
	Filename   string `json:"filename"`
	FormatName string `json:"format_name"`
	Duration   string `json:"duration"`
	BitRate    string `json:"bit_rate"`
}

type ffprobeChapter struct {
	ID        int               `json:"id"`
	StartTime string            `json:"start_time"`
	EndTime   string            `json:"end_time"`
	Tags      map[string]string `json:"tags"`
}

// SourceStatus categorises a VerifySource outcome: empty on success, or
// a reason token (e.g. "missing", "unreadable") callers map to a
// structured error code instead of a generic "playback failed."
type SourceStatus string

const (
	// SourceOK — file exists on disk and ffprobe could parse its header.
	SourceOK SourceStatus = ""
	// SourceMissing — file path doesn't resolve on disk. Typical causes:
	// drive unmounted, file moved/deleted since scan, network share down.
	SourceMissing SourceStatus = "missing"
	// SourceUnreadable — file exists but ffprobe couldn't demux it within
	// the time budget (corrupt container, zero-byte file, unsupported codec
	// at the header level). This is the fast path that replaces "spinner
	// forever" when ffmpeg would otherwise hang on a bad input.
	SourceUnreadable SourceStatus = "unreadable"
)

// VerifySource runs a fast ffprobe against path with a 5 s budget and
// returns whether the source is playable. Used as a pre-flight gate by
// the transcode-start handler so a missing or corrupt source rejects in
// ~1 s with a structured error code, instead of stalling the encoder for
// the playlist endpoint's 60 s deadline and surfacing as an indefinite
// spinner in the player.
//
// The probe is intentionally light — `-probesize 5M -analyzeduration 1s`
// keeps healthy files at ~200-500 ms while a corrupt header bails almost
// instantly. Heavier metadata extraction stays in ProbeFile (called at
// scan time or when missing fields trigger a lazy re-probe).
func VerifySource(ctx context.Context, path string) (SourceStatus, error) {
	if _, statErr := os.Stat(path); statErr != nil {
		if os.IsNotExist(statErr) {
			return SourceMissing, statErr
		}
		// Other stat errors (permission, EIO) — treat as missing for the
		// purpose of surfacing an error to the user; the message carries
		// the real reason for an admin reading server logs.
		return SourceMissing, statErr
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	args := []string{
		"-v", "error",
		"-probesize", "5000000", // 5 MB cap — enough for any sane container header
		"-analyzeduration", "1000000", // 1 s of stream data
		"-show_entries", "stream=index",
		"-of", "csv=p=0",
		path,
	}
	out, err := exec.CommandContext(ctx, "ffprobe", args...).Output()
	if err != nil {
		// Context-deadline-exceeded surfaces here as an exec error.
		return SourceUnreadable, fmt.Errorf("ffprobe verify: %w", err)
	}
	if len(strings.TrimSpace(string(out))) == 0 {
		return SourceUnreadable, fmt.Errorf("ffprobe verify: no streams detected")
	}
	return SourceOK, nil
}

// ProbeFile runs a full ffprobe pass against path and returns the parsed
// stream / format / chapters metadata. Used by the scanner at index time
// and by the transcode-start handler's lazy re-probe for files that were
// scanned with incomplete metadata.
//
// probesize and analyzeduration cap how much data ffprobe reads — without
// them, ffprobe on MPEG-TS files can scan the entire file to detect streams.
func ProbeFile(ctx context.Context, path string) (*ProbeResult, error) {
	// 30s hard timeout so a stuck ffprobe doesn't stall the whole scan.
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	args := []string{
		"-v", "quiet",
		"-probesize", "50000000", // read at most 50 MB to detect streams
		"-analyzeduration", "5000000", // analyze at most 5 s of stream data
		"-print_format", "json",
		"-show_streams",
		"-show_format",
		"-show_chapters",
		path,
	}

	cmd := exec.CommandContext(ctx, "ffprobe", args...)
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("ffprobe: %w", err)
	}

	var probe ffprobeOutput
	if err := json.Unmarshal(out, &probe); err != nil {
		return nil, fmt.Errorf("ffprobe parse: %w", err)
	}

	result := &ProbeResult{}

	// Format / container.
	if probe.Format.FormatName != "" {
		// ffprobe returns comma-separated format names; take the first.
		fmtName := strings.SplitN(probe.Format.FormatName, ",", 2)[0]
		result.Container = &fmtName
	}
	if probe.Format.BitRate != "" {
		if br, err := strconv.ParseInt(probe.Format.BitRate, 10, 64); err == nil {
			result.Bitrate = &br
		}
	}
	if probe.Format.Duration != "" {
		if dur, err := strconv.ParseFloat(probe.Format.Duration, 64); err == nil && dur > 0 {
			ms := int64(dur * 1000)
			result.DurationMs = &ms
		}
	}

	var audioStreams []map[string]any
	var subtitleStreams []map[string]any

	for _, s := range probe.Streams {
		switch s.CodecType {
		case "video":
			// Skip attached pictures (embedded cover art in MKV/MP4).
			if s.Disposition["attached_pic"] == 1 {
				continue
			}
			if result.VideoCodec == nil {
				result.VideoCodec = &s.CodecName
				if s.Width > 0 {
					result.ResolutionW = &s.Width
				}
				if s.Height > 0 {
					result.ResolutionH = &s.Height
				}
				if fps := parseFrameRate(s.RFrameRate); fps > 0 {
					result.FrameRate = &fps
				}
				result.HDRType = detectHDR(&s)
				if bd := videoBitDepth(&s); bd > 0 {
					result.VideoBitDepth = &bd
				}
			}

		case "audio":
			lang := s.Tags["language"]
			title := s.Tags["title"]
			audioStreams = append(audioStreams, map[string]any{
				"index":          s.Index,
				"codec":          s.CodecName,
				"channels":       s.Channels,
				"channel_layout": s.ChannelLayout,
				"sample_rate":    parseIntSafe(s.SampleRate),
				"bit_depth":      streamBitDepth(&s),
				"language":       lang,
				"title":          title,
			})
			if result.AudioCodec == nil {
				result.AudioCodec = &s.CodecName
				// First-audio-stream characteristics populate the top-level
				// audiophile fields. For music files these are the definitive
				// values; for video files they describe the primary audio
				// track, which is what a client-side quality badge reflects.
				if sr := parseIntSafe(s.SampleRate); sr > 0 {
					result.SampleRate = &sr
				}
				if bd := streamBitDepth(&s); bd > 0 {
					result.BitDepth = &bd
				}
				if s.ChannelLayout != "" {
					layout := s.ChannelLayout
					result.ChannelLayout = &layout
				} else if s.Channels > 0 {
					layout := channelLayoutFromCount(s.Channels)
					result.ChannelLayout = &layout
				}
			}

		case "subtitle":
			lang := s.Tags["language"]
			title := s.Tags["title"]
			forced := s.Disposition["forced"] == 1
			// hearing_impaired is ffprobe's SDH disposition. External
			// subtitles already carry an sdh flag (from OpenSubtitles); this
			// brings embedded streams to parity so clients can label/filter
			// SDH tracks consistently regardless of source.
			sdh := s.Disposition["hearing_impaired"] == 1
			subtitleStreams = append(subtitleStreams, map[string]any{
				"index":    s.Index,
				"codec":    s.CodecName,
				"language": lang,
				"title":    title,
				"forced":   forced,
				"sdh":      sdh,
			})
		}
	}

	// Marshal JSONB columns.
	if len(audioStreams) > 0 {
		b, _ := json.Marshal(audioStreams)
		result.AudioStreams = b
	}
	if len(subtitleStreams) > 0 {
		b, _ := json.Marshal(subtitleStreams)
		result.SubtitleStreams = b
	}

	// Chapters.
	if len(probe.Chapters) > 0 {
		var chapters []map[string]any
		for _, c := range probe.Chapters {
			title := c.Tags["title"]
			startMS := parseTimeToMS(c.StartTime)
			endMS := parseTimeToMS(c.EndTime)
			chapters = append(chapters, map[string]any{
				"title":    title,
				"start_ms": startMS,
				"end_ms":   endMS,
			})
		}
		b, _ := json.Marshal(chapters)
		result.Chapters = b
	}

	return result, nil
}

// detectHDR returns the HDR type string or nil for SDR content.
func detectHDR(s *ffprobeStream) *string {
	// Check side data for HDR metadata.
	for _, sd := range s.SideDataList {
		switch sd.SideDataType {
		case "DOVI configuration record":
			t := "dolby_vision"
			return &t
		case "Content light level metadata":
			t := "hdr10"
			return &t
		}
	}
	// Fallback: check color transfer / primaries.
	switch s.ColorTransfer {
	case "smpte2084":
		t := "hdr10"
		return &t
	case "arib-std-b67":
		t := "hlg"
		return &t
	}
	return nil
}

func parseFrameRate(s string) float64 {
	// ffprobe returns "24000/1001" format.
	var num, den int
	if n, _ := fmt.Sscanf(s, "%d/%d", &num, &den); n == 2 && den > 0 {
		return float64(num) / float64(den)
	}
	f, _ := strconv.ParseFloat(s, 64)
	return f
}

func parseTimeToMS(s string) int64 {
	f, _ := strconv.ParseFloat(s, 64)
	return int64(f * 1000)
}

func parseIntSafe(s string) int {
	n, _ := strconv.Atoi(s)
	return n
}

// streamBitDepth prefers bits_per_raw_sample (the source format's true depth,
// e.g. 24 for a 24-bit FLAC) and falls back to bits_per_sample (the decoded
// bits, which equals raw depth for most codecs but not all). Returns 0 when
// ffprobe exposes neither — common for lossy formats where "bit depth" is
// not a meaningful concept.
func streamBitDepth(s *ffprobeStream) int {
	if s.BitsPerRawSample != "" {
		if n, err := strconv.Atoi(s.BitsPerRawSample); err == nil && n > 0 {
			return n
		}
	}
	if s.BitsPerSample > 0 {
		return s.BitsPerSample
	}
	return 0
}

// videoBitDepth derives a video stream's bit depth from its pix_fmt — the
// reliable signal (yuv420p10le → 10, p010le → 10, yuv444p12le → 12). Falls
// back to bits_per_raw_sample, then defaults to 8 for a recognized 8-bit
// pix_fmt (yuv420p, nv12, …). Returns 0 only when ffprobe exposes neither,
// so the column stays NULL and clients treat it as 8-bit. Distinct from
// streamBitDepth, which reads audio depth.
func videoBitDepth(s *ffprobeStream) int {
	pf := strings.ToLower(s.PixFmt)
	switch {
	case strings.Contains(pf, "p016") || strings.Contains(pf, "16le") || strings.Contains(pf, "16be"):
		return 16
	case strings.Contains(pf, "p012") || strings.Contains(pf, "p12") || strings.Contains(pf, "12le") || strings.Contains(pf, "12be"):
		return 12
	case strings.Contains(pf, "p010") || strings.Contains(pf, "p10") || strings.Contains(pf, "10le") || strings.Contains(pf, "10be"):
		return 10
	}
	if s.BitsPerRawSample != "" {
		if n, err := strconv.Atoi(s.BitsPerRawSample); err == nil && n > 0 {
			return n
		}
	}
	if pf != "" {
		return 8 // recognized 8-bit pix_fmt (yuv420p, nv12, yuvj420p, …)
	}
	return 0
}

// channelLayoutFromCount is the fallback when ffprobe reports channels but no
// channel_layout string (happens for some codecs and containers). Maps the
// common counts to their canonical layout names; anything exotic returns the
// count as a string ("9 channels") so the caller has *something* to display.
func channelLayoutFromCount(n int) string {
	switch n {
	case 1:
		return "mono"
	case 2:
		return "stereo"
	case 3:
		return "2.1"
	case 4:
		return "quad"
	case 6:
		return "5.1"
	case 8:
		return "7.1"
	}
	return strconv.Itoa(n) + " channels"
}

// IsMP4Container reports whether faststart analysis applies to path's container
// (the ISOBMFF family). Callers gate on this before opening so non-MP4 inputs
// aren't read needlessly.
func IsMP4Container(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".mp4", ".mov", ".m4v", ".m4a":
		return true
	}
	return false
}

// IsFaststart reports whether an MP4/MOV file has its moov atom before mdat
// (i.e. is "faststart"). Non-faststart files require the browser to fetch the
// end of the file before playback can begin, causing silence and buffering.
// Returns true for any file format that isn't MP4/MOV (no concern there). Thin
// wrapper over IsFaststartReader for callers that have a local path.
func IsFaststart(path string) bool {
	if !IsMP4Container(path) {
		return true // not an ISOBMFF container — not applicable
	}
	f, err := os.Open(path)
	if err != nil {
		return true // assume ok if we can't read
	}
	defer f.Close()
	return IsFaststartReader(f)
}

// IsFaststartReader walks the top-level atoms of an already-open ISOBMFF file
// (media-store readable), returning whether moov precedes mdat. Assumes the
// caller has gated on IsMP4Container. Returns true (assume ok) on any read
// trouble.
func IsFaststartReader(f io.ReadSeeker) bool {
	// Walk the top-level atoms looking for moov before mdat.
	buf := make([]byte, 8)
	for {
		if _, err := io.ReadFull(f, buf); err != nil {
			break
		}
		size := int64(buf[0])<<24 | int64(buf[1])<<16 | int64(buf[2])<<8 | int64(buf[3])
		atom := string(buf[4:8])
		if atom == "moov" {
			return true // moov before mdat → faststart
		}
		if atom == "mdat" {
			return false // mdat before moov → not faststart
		}
		// Skip past this atom's body. size includes the 8-byte header.
		// size == 0 means "extends to EOF"; size == 1 means 64-bit extended size.
		// Both are rare in practice; treat as non-faststart to be safe.
		if size == 0 || size == 1 {
			return false
		}
		body := size - 8
		if body > 0 {
			if _, err := f.Seek(body, io.SeekCurrent); err != nil {
				break
			}
		}
	}
	return true // couldn't determine — assume ok
}

// ProbeImage extracts dimensions from a local image file. Thin wrapper over
// ProbeImageReader for callers that have a path.
func ProbeImage(path string) *ProbeResult {
	f, err := os.Open(path)
	if err != nil {
		return &ProbeResult{}
	}
	defer f.Close()
	return ProbeImageReader(f, path)
}

// ProbeImageReader extracts dimensions from an already-open image using Go's
// image package, so the source can be read through the media store. ext (e.g.
// the file path or name) supplies the container label. Returns a minimal
// ProbeResult with resolution only (no duration, codecs, etc.).
func ProbeImageReader(r io.Reader, name string) *ProbeResult {
	cfg, _, err := image.DecodeConfig(r)
	if err != nil {
		return &ProbeResult{}
	}
	w, h := cfg.Width, cfg.Height
	container := strings.TrimPrefix(strings.ToLower(filepath.Ext(name)), ".")
	return &ProbeResult{
		Container:   &container,
		ResolutionW: &w,
		ResolutionH: &h,
	}
}
