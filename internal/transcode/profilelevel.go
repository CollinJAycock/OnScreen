package transcode

import (
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/onscreen/onscreen/internal/domain/media"
)

// Video profile / level: the most a client's decoder takes for one codec
// (h264Profile=high, h264Level=4.1, hevcProfile=main10, hevcLevel=5.1,
// av1Profile, av1Level), compared with the source's ffprobe profile and level
// (media_files.video_profile / video_level, migration 00060).
//
// Levels are held in tenths everywhere here: 41 = level 4.1, 51 = 5.1. That's
// H.264's own level_idc; HEVC stores 30 × the level and AV1 an index, both
// converted by VideoLevelTenths. Profiles are ranked within their codec, each
// rank decoding everything below it (a High decoder plays Main and Baseline).

// VideoLimit is the most a client decodes of one codec. Zero fields are "not
// declared": that half isn't checked.
type VideoLimit struct {
	// Profile is the highest profile, as a canonical token: h264 "baseline",
	// "main", "high", "high10", "high422", "high444"; hevc "main", "main10",
	// "rext"; av1 "main", "high", "professional".
	Profile string
	// Level is the highest level in tenths (41 = 4.1).
	Level int
}

// declared reports whether the limit says anything.
func (l VideoLimit) declared() bool { return l.Profile != "" || l.Level > 0 }

// VideoLimitFor returns the client's limit for a video codec (any ffprobe or
// client alias: "hevc" and "h265" are the same). The zero VideoLimit when
// none was declared.
func (c ClientCapabilities) VideoLimitFor(codec string) VideoLimit {
	return c.VideoLimits[canonicalVideoCodec(codec)]
}

// limitCodec maps a capability key prefix to the canonical codec it limits.
func limitCodec(key string) (codec, field string, ok bool) {
	for _, p := range []struct{ prefix, codec string }{
		{"h264", "h264"}, {"avc", "h264"},
		{"hevc", "h265"}, {"h265", "h265"},
		{"av1", "av1"},
	} {
		if rest, found := strings.CutPrefix(key, p.prefix); found {
			switch rest {
			case "profile", "maxprofile":
				return p.codec, "profile", true
			case "level", "maxlevel":
				return p.codec, "level", true
			}
		}
	}
	return "", "", false
}

// setVideoLimit records one h264Profile / hevcLevel / ... value. An
// unrecognised profile or an unparseable level is dropped, which leaves that
// half of the limit undeclared (never gated) rather than guessing.
func (c *ClientCapabilities) setVideoLimit(codec, field, val string) {
	lim := c.VideoLimits[codec]
	switch field {
	case "profile":
		p := normaliseProfile(val)
		if profileRank(codec, p) == 0 {
			return
		}
		lim.Profile = canonicalProfile(codec, p)
	case "level":
		l := parseClientLevel(codec, val)
		if l <= 0 {
			return
		}
		lim.Level = l
	}
	if c.VideoLimits == nil {
		c.VideoLimits = map[string]VideoLimit{}
	}
	c.VideoLimits[codec] = lim
}

// normaliseProfile folds a profile name, ffprobe's ("High 4:2:2 Intra",
// "Main 10") or a client token ("main10"), to lower case without spaces or
// punctuation: "high422intra", "main10".
func normaliseProfile(name string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(name) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// profileRank orders a codec's profiles so a decoder of one rank plays every
// lower rank. 0 = unknown, never gated: H.264 Extended and the multiview /
// stereo profiles aren't subsets of High, so no rank fits them.
func profileRank(codec, norm string) int {
	switch codec {
	case "h264":
		switch norm {
		case "baseline", "constrainedbaseline":
			return 1
		case "main":
			return 2
		case "high", "progressivehigh", "constrainedhigh":
			return 3
		case "high10", "high10intra":
			return 4
		case "high422", "high422intra":
			return 5
		case "high444", "high444predictive", "high444intra", "cavlc444", "cavlc444intra":
			return 6
		}
	case "h265":
		switch norm {
		case "main", "mainstillpicture":
			return 1
		case "main10", "main10stillpicture":
			return 2
		case "rext", "main12", "main42210", "main444", "main44410", "scc":
			return 3
		}
	case "av1":
		switch norm {
		case "main":
			return 1
		case "high":
			return 2
		case "professional":
			return 3
		}
	}
	return 0
}

// canonicalProfile is the token a rank is shown and stored as.
func canonicalProfile(codec, norm string) string {
	rank := profileRank(codec, norm)
	names := map[string][]string{
		"h264": {"", "baseline", "main", "high", "high10", "high422", "high444"},
		"h265": {"", "main", "main10", "rext"},
		"av1":  {"", "main", "high", "professional"},
	}[codec]
	if rank <= 0 || rank >= len(names) {
		return ""
	}
	return names[rank]
}

// parseClientLevel reads a declared level: "4.1" (or "4", "5.0"), or a bare
// two-digit number in tenths, H.264's level_idc form ("41"). For HEVC a
// three-digit number is its general_level_idc (153 = 5.1). Returns tenths, 0
// when it can't be read.
func parseClientLevel(codec, val string) int {
	val = strings.TrimSpace(val)
	if major, minor, ok := strings.Cut(val, "."); ok {
		ma, err1 := strconv.Atoi(major)
		mi, err2 := strconv.Atoi(minor)
		if err1 != nil || err2 != nil || ma <= 0 || mi < 0 || mi > 9 {
			return 0
		}
		return ma*10 + mi
	}
	n, err := strconv.Atoi(val)
	switch {
	case err != nil || n <= 0:
		return 0
	case n < 10:
		return n * 10
	case n < 100:
		return n
	case codec == "h265" && n%3 == 0:
		return n / 3
	}
	return 0
}

// VideoLevelTenths converts ffprobe's raw level for a codec into tenths:
// H.264 reports level_idc (41 = 4.1; 9 = level 1b, read as 1.0), HEVC
// general_level_idc (30 × the level: 153 = 5.1), AV1 seq_level_idx (13 = 5.1:
// major 2 + idx/4, minor idx%4; 31 means unconstrained). 0 when unknown.
func VideoLevelTenths(codec string, raw int) int {
	if raw <= 0 {
		return 0
	}
	switch canonicalVideoCodec(codec) {
	case "h264":
		if raw == 9 {
			return 10
		}
		return raw
	case "h265":
		return raw / 3
	case "av1":
		if raw > 23 {
			return 0
		}
		return (2+raw/4)*10 + raw%4
	}
	return 0
}

// FormatLevel renders tenths as a level: 41 → "4.1", 50 → "5".
func FormatLevel(tenths int) string {
	if tenths%10 == 0 {
		return strconv.Itoa(tenths / 10)
	}
	return fmt.Sprintf("%d.%d", tenths/10, tenths%10)
}

// profileLabel is how a profile token reads on a Now Playing card.
func profileLabel(codec, token string) string {
	switch token {
	case "baseline":
		return "Baseline"
	case "main":
		return "Main"
	case "high":
		return "High"
	case "high10":
		return "High 10"
	case "high422":
		return "High 4:2:2"
	case "high444":
		return "High 4:4:4"
	case "main10":
		return "Main 10"
	case "rext":
		return "RExt"
	case "professional":
		return "Professional"
	}
	return token
}

// videoLimitExceeded reports whether the file's video is over the client's
// declared profile and/or level for its codec. A source whose profile or
// level wasn't probed (NULL, pre-00060 rows) or isn't ranked is never over.
func videoLimitExceeded(file media.File, caps ClientCapabilities) (profileOver, levelOver bool) {
	codec := canonicalVideoCodec(deref(file.VideoCodec))
	lim := caps.VideoLimitFor(codec)
	if !lim.declared() {
		return false, false
	}
	if lim.Profile != "" && file.VideoProfile != nil {
		src := profileRank(codec, normaliseProfile(*file.VideoProfile))
		profileOver = src > 0 && src > profileRank(codec, lim.Profile)
	}
	if lim.Level > 0 && file.VideoLevel != nil {
		src := VideoLevelTenths(codec, *file.VideoLevel)
		levelOver = src > 0 && src > lim.Level
	}
	return profileOver, levelOver
}

// levelSpec is one row of a codec's level table: the largest picture (luma
// samples), the luma sample rate, and the main-tier / Main-profile bitrate.
type levelSpec struct {
	tenths     int
	maxPicture int64
	maxRate    int64
	maxKbps    int
}

// H.264 Table A-1, in luma samples (MaxFS and MaxMBPS × 256 per macroblock)
// and the Baseline/Main MaxBR (High allows 1.25×; the lower figure is used).
var h264Levels = []levelSpec{
	{10, 99 * 256, 1485 * 256, 64},
	{11, 396 * 256, 3000 * 256, 192},
	{12, 396 * 256, 6000 * 256, 384},
	{13, 396 * 256, 11880 * 256, 768},
	{20, 396 * 256, 11880 * 256, 2000},
	{21, 792 * 256, 19800 * 256, 4000},
	{22, 1620 * 256, 20250 * 256, 4000},
	{30, 1620 * 256, 40500 * 256, 10000},
	{31, 3600 * 256, 108000 * 256, 14000},
	{32, 5120 * 256, 216000 * 256, 20000},
	{40, 8192 * 256, 245760 * 256, 20000},
	{41, 8192 * 256, 245760 * 256, 50000},
	{42, 8704 * 256, 522240 * 256, 50000},
	{50, 22080 * 256, 589824 * 256, 135000},
	{51, 36864 * 256, 983040 * 256, 240000},
	{52, 36864 * 256, 2073600 * 256, 240000},
	{60, 139264 * 256, 4177920 * 256, 240000},
	{61, 139264 * 256, 8355840 * 256, 480000},
	{62, 139264 * 256, 16711680 * 256, 800000},
}

// HEVC Table A.8 (MaxLumaPs, MaxLumaSr; Main-tier MaxBR).
var hevcLevels = []levelSpec{
	{10, 36864, 552960, 128},
	{20, 122880, 3686400, 1500},
	{21, 245760, 7372800, 3000},
	{30, 552960, 16588800, 6000},
	{31, 983040, 33177600, 10000},
	{40, 2228224, 66846720, 12000},
	{41, 2228224, 133693440, 20000},
	{50, 8912896, 267386880, 25000},
	{51, 8912896, 534773760, 40000},
	{52, 8912896, 1069547520, 60000},
	{60, 35651584, 1069547520, 60000},
	{61, 35651584, 2139095040, 120000},
	{62, 35651584, 4278190080, 240000},
}

// AV1 Annex A (MaxPicSize, MaxDisplayRate; Main-tier MaxBitrate).
var av1Levels = []levelSpec{
	{20, 147456, 4423680, 1500},
	{21, 278784, 8363520, 3000},
	{30, 665856, 19975680, 6000},
	{31, 1065024, 31950720, 10000},
	{40, 2359296, 70778880, 12000},
	{41, 2359296, 141557760, 20000},
	{50, 8912896, 267386880, 30000},
	{51, 8912896, 534773760, 40000},
	{52, 8912896, 1069547520, 60000},
	{53, 8912896, 1069547520, 60000},
	{60, 35651584, 1069547520, 60000},
	{61, 35651584, 2139095040, 100000},
	{62, 35651584, 4278190080, 160000},
	{63, 35651584, 4278190080, 160000},
}

// levelLimits returns the table row for a codec's level: the highest row at
// or below tenths, so an in-between value (4.3) reads as the level beneath it.
func levelLimits(codec string, tenths int) (levelSpec, bool) {
	var table []levelSpec
	switch canonicalVideoCodec(codec) {
	case "h264":
		table = h264Levels
	case "h265":
		table = hevcLevels
	case "av1":
		table = av1Levels
	default:
		return levelSpec{}, false
	}
	var best levelSpec
	found := false
	for _, s := range table {
		if s.tenths <= tenths {
			best, found = s, true
		}
	}
	return best, found
}

// pictureSamples is what a w×h picture counts against a level: H.264 counts
// whole 16×16 macroblocks; HEVC and AV1 count luma samples.
func pictureSamples(codec string, w, h int) int64 {
	if canonicalVideoCodec(codec) == "h264" {
		return int64((w+15)/16) * int64((h+15)/16) * 256
	}
	return int64(w) * int64(h)
}

// FitsLevel reports whether a w×h picture at fps fits a codec's level
// (tenths). fps ≤ 0 checks the picture size only. A level or codec without a
// table always fits.
func FitsLevel(codec string, tenths, w, h int, fps float64) bool {
	spec, ok := levelLimits(codec, tenths)
	if !ok || w <= 0 || h <= 0 {
		return true
	}
	samples := pictureSamples(codec, w, h)
	if samples > spec.maxPicture {
		return false
	}
	return fps <= 0 || float64(samples)*fps <= float64(spec.maxRate)
}

// LevelMaxHeight returns the tallest even output height, at the source's
// aspect ratio and frame rate, whose picture fits a codec's level (tenths);
// 0 when the level doesn't restrict this source (no level, no table, unknown
// source size, or the source already fits). The output keeps the source's
// frame rate, so a 1080p60 source on an H.264 4.1 decoder (1080p30 at most)
// comes out smaller rather than at a level the decoder rejects.
func LevelMaxHeight(codec string, tenths, srcW, srcH int, fps float64) int {
	if tenths <= 0 || srcW <= 0 || srcH <= 0 {
		return 0
	}
	if _, ok := levelLimits(codec, tenths); !ok || FitsLevel(codec, tenths, srcW, srcH, fps) {
		return 0
	}
	aspect := float64(srcW) / float64(srcH)
	for h := srcH &^ 1; h >= 2; h -= 2 {
		w := int(math.Round(float64(h) * aspect))
		w += w & 1
		if FitsLevel(codec, tenths, w, h, fps) {
			return h
		}
	}
	return 2
}

// LevelMaxKbps returns the video bitrate a re-encode may target at a codec's
// level (tenths): the level's maximum less the encoder's maxrate headroom
// (maxrate = bitrate × 1.5), so the peak stays within the level. 0 = no cap.
func LevelMaxKbps(codec string, tenths int) int {
	spec, ok := levelLimits(codec, tenths)
	if !ok || tenths <= 0 {
		return 0
	}
	return spec.maxKbps * 2 / 3
}

// OutputLimits is what a re-encode may produce for the client, per output
// codec, from its h264Profile / h264Level / hevcLevel / av1Level keys. The
// worker reads the entries for the encoder it lands on (a fallback from HEVC
// to H.264 switches to H.264's). The API has already sized the output to fit
// the predicted codec's level (LevelMaxHeight); the level is written into the
// stream only when the output fits it, so a fallback encoder is never handed
// a level its picture is over (NVENC refuses to start on one).
type OutputLimits struct {
	// H264Profile pins a lower H.264 profile than the encoders' default
	// (High): "main" or "baseline" (constrained baseline). Empty = default.
	H264Profile string `json:"h264_profile,omitempty"`
	// H264Level / HEVCLevel / AV1Level are the highest levels, in tenths
	// (41 = 4.1); 0 = the encoder's own choice.
	H264Level int `json:"h264_level,omitempty"`
	HEVCLevel int `json:"hevc_level,omitempty"`
	AV1Level  int `json:"av1_level,omitempty"`
	// FrameRate is the source's (the output keeps it), to check a picture
	// against a level's sample rate. 0 = unknown: picture size only.
	FrameRate float64 `json:"frame_rate,omitempty"`
}

// IsZero reports whether the limits pin nothing.
func (o OutputLimits) IsZero() bool {
	return o.H264Profile == "" && o.H264Level == 0 && o.HEVCLevel == 0 && o.AV1Level == 0
}

// OutputLimitsFor derives a re-encode's limits from the client's profile.
// HEVC's profile isn't here: a Main-only client gets 8-bit output through
// Force8Bit (see HEVCMain8Only). H.264 output is 8-bit 4:2:0 High at most,
// so only a Main or Baseline ceiling pins anything.
func OutputLimitsFor(caps ClientCapabilities, fps float64) OutputLimits {
	o := OutputLimits{
		H264Level: caps.VideoLimitFor("h264").Level,
		HEVCLevel: caps.VideoLimitFor("h265").Level,
		AV1Level:  caps.VideoLimitFor("av1").Level,
	}
	switch caps.VideoLimitFor("h264").Profile {
	case "baseline":
		o.H264Profile = "baseline"
	case "main":
		o.H264Profile = "main"
	}
	if !o.IsZero() && fps > 0 {
		o.FrameRate = fps
	}
	return o
}

// HEVCMain8Only reports whether the client declared HEVC Main as its highest
// HEVC profile: an HEVC re-encode for it must be 8-bit.
func (c ClientCapabilities) HEVCMain8Only() bool {
	return c.VideoLimitFor("h265").Profile == "main"
}

// LevelFor returns the level limit (tenths) for an output codec family.
func (o OutputLimits) LevelFor(codec string) int {
	switch canonicalVideoCodec(codec) {
	case "h264":
		return o.H264Level
	case "h265":
		return o.HEVCLevel
	case "av1":
		return o.AV1Level
	}
	return 0
}

// encoderFamily is the codec an encoder writes: "h264", "h265" or "av1".
func encoderFamily(e Encoder) string {
	switch {
	case IsAV1Encoder(e):
		return "av1"
	case IsHEVCEncoder(e):
		return "h265"
	}
	return "h264"
}

// outputLimitArgs returns the encoder flags that hold a re-encode to the
// client's profile and level: -profile:v for an H.264 Main/Baseline ceiling,
// and the level in the form each encoder takes. w×h is the output box.
// libsvtav1 has no level option and gets none.
func outputLimitArgs(e Encoder, o OutputLimits, w, h int) []string {
	var args []string
	family := encoderFamily(e)
	if family == "h264" && o.H264Profile != "" {
		p := o.H264Profile
		if p == "baseline" {
			switch e {
			case EncoderAMF, EncoderVAAPI:
				p = "constrained_baseline"
			}
		}
		args = append(args, "-profile:v", p)
	}
	level := o.LevelFor(family)
	if level <= 0 || !FitsLevel(family, level, w, h, o.FrameRate) {
		return args
	}
	switch e {
	case EncoderAV1Software:
		return args
	case EncoderHEVCSoftware:
		return append(args, "-x265-params", "level-idc="+levelDotted(level))
	case EncoderQSV, EncoderHEVCQSV, EncoderAV1QSV:
		// No private option: the generic level, which QSV passes on as its
		// MFX level (41 = 4.1 for H.264, HEVC and AV1 alike).
		return append(args, "-level", strconv.Itoa(level))
	case EncoderVAAPI, EncoderHEVCVAAPI:
		// Named levels without a trailing .0 ("4", "4.1").
		return append(args, "-level", FormatLevel(level))
	}
	return append(args, "-level", levelDotted(level))
}

// levelDotted renders tenths as major.minor always: 40 → "4.0".
func levelDotted(tenths int) string {
	return fmt.Sprintf("%d.%d", tenths/10, tenths%10)
}
