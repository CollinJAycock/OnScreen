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
	Container       *string
	VideoCodec      *string
	AudioCodec      *string
	ResolutionW     *int
	ResolutionH     *int
	Bitrate         *int64
	DurationMs      *int64
	HDRType         *string
	FrameRate       *float64
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
	Index             int               `json:"index"`
	CodecName         string            `json:"codec_name"`
	CodecType         string            `json:"codec_type"`
	Width             int               `json:"width"`
	Height            int               `json:"height"`
	RFrameRate        string            `json:"r_frame_rate"`
	BitRate           string            `json:"bit_rate"`
	Channels          int               `json:"channels"`
	ChannelLayout     string            `json:"channel_layout"`
	SampleRate        string            `json:"sample_rate"`
	BitsPerRawSample  string            `json:"bits_per_raw_sample"`
	BitsPerSample     int               `json:"bits_per_sample"`
	Tags              map[string]string `json:"tags"`
	Disposition       map[string]int    `json:"disposition"`
	ColorTransfer     string            `json:"color_transfer"`
	ColorPrimaries    string            `json:"color_primaries"`
	SideDataList      []ffprobeSideData `json:"side_data_list"`
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

// ProbeFile runs ffprobe on the given path and returns extracted metadata.
// Returns an empty ProbeResult (not an error) if ffprobe is not installed.
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
			subtitleStreams = append(subtitleStreams, map[string]any{
				"index":    s.Index,
				"codec":    s.CodecName,
				"language": lang,
				"title":    title,
				"forced":   forced,
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

// IsFaststart reports whether an MP4/MOV file has its moov atom before mdat
// (i.e. is "faststart"). Non-faststart files require the browser to fetch the
// end of the file before playback can begin, causing silence and buffering.
// Returns true for any file format that isn't MP4/MOV (no concern there).
func IsFaststart(path string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	if ext != ".mp4" && ext != ".mov" && ext != ".m4v" && ext != ".m4a" {
		return true // not an ISOBMFF container — not applicable
	}
	f, err := os.Open(path)
	if err != nil {
		return true // assume ok if we can't read
	}
	defer f.Close()

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

// ProbeImage extracts dimensions from an image file using Go's image package.
// Returns a minimal ProbeResult with resolution only (no duration, codecs, etc.).
func ProbeImage(path string) *ProbeResult {
	f, err := os.Open(path)
	if err != nil {
		return &ProbeResult{}
	}
	defer f.Close()

	cfg, _, err := image.DecodeConfig(f)
	if err != nil {
		return &ProbeResult{}
	}
	w, h := cfg.Width, cfg.Height
	container := strings.TrimPrefix(strings.ToLower(filepath.Ext(path)), ".")
	return &ProbeResult{
		Container:   &container,
		ResolutionW: &w,
		ResolutionH: &h,
	}
}
