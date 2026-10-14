package downloads

import (
	"strings"

	"github.com/onscreen/onscreen/internal/domain/media"
	"github.com/onscreen/onscreen/internal/transcode"
)

// ── Optimized versions ───────────────────────────────────────────────────────
//
// An optimized version is an admin-requested copy of a file encoded ahead of
// time (Plex's "Optimize"), so a weak device or a viewer outside the home
// network direct-plays it instead of holding a live transcode. It is the same
// encode as a converted download — one progressive MP4, H.264 + stereo AAC,
// faststart — run by the same Runner, so it shares the queue, the encoder
// choice and fallback and the transcode-slot accounting. The differences are
// where the file lives (CACHE_PATH/optimized), that it never expires, and that
// the play decision can pick it (internal/api/v1/optimize.go).
//
// Schema: download_conversions rows with purpose 'optimize', migration 00056.

// Conversion purposes (download_conversions.purpose).
const (
	PurposeDownload = "download"
	PurposeOptimize = "optimize"
)

// OptimizeProfile is one "Optimize…" choice.
type OptimizeProfile struct {
	// ID is the API and database value ("mobile_720p").
	ID string
	// Label is the human name ("Mobile · 720p 4 Mbps").
	Label string
	// Profile is the ladder rung the encode targets. For the "original
	// quality" profile it is only the box (4K); the bitrate is picked per
	// source by Plan.
	Profile transcode.QualityProfile
	// KeepResolution encodes at the source's resolution and a bitrate from
	// the rung matching it, instead of this profile's rung.
	KeepResolution bool
}

// The optimize profile ids.
const (
	OptimizeMobile   = "mobile_720p"
	OptimizeTV       = "tv_1080p"
	OptimizeOriginal = "original_compatible"
)

// OptimizeProfiles is the "Optimize…" menu, smallest first.
var OptimizeProfiles = []OptimizeProfile{
	{ID: OptimizeMobile, Label: "Mobile · 720p 4 Mbps", Profile: rung(4000)},
	{ID: OptimizeTV, Label: "TV · 1080p 8 Mbps", Profile: rung(8000)},
	{ID: OptimizeOriginal, Label: "Original quality, compatible (H.264/AAC)", Profile: rung(40000), KeepResolution: true},
}

// rung returns the transcode.Profiles rung at kbps.
func rung(kbps int) transcode.QualityProfile {
	for _, p := range transcode.Profiles {
		if p.Bitrate == kbps {
			return p
		}
	}
	return transcode.Profiles[0]
}

// OptimizeProfileByID looks an optimize profile up by its id.
func OptimizeProfileByID(id string) (OptimizeProfile, bool) {
	for _, p := range OptimizeProfiles {
		if p.ID == id {
			return p, true
		}
	}
	return OptimizeProfile{}, false
}

// OptimizeProfileIDs lists the ids, for validation messages.
func OptimizeProfileIDs() string {
	ids := make([]string, 0, len(OptimizeProfiles))
	for _, p := range OptimizeProfiles {
		ids = append(ids, p.ID)
	}
	return strings.Join(ids, ", ")
}

// EncodePlan is what one encode targets.
type EncodePlan struct {
	BitrateKbps         int
	MaxWidth, MaxHeight int
}

// Plan works out the encode for f. A fixed profile is its rung. The
// "original quality" profile keeps the source's resolution (inside the 4K
// box) at the bitrate of the smallest rung that holds that resolution — the
// best of them where two rungs share it — and never above the source's own
// bitrate when the source is already H.264 (re-encoding H.264 at a higher
// rate than it came in at only makes the file bigger).
func (p OptimizeProfile) Plan(f *media.File) EncodePlan {
	plan := EncodePlan{BitrateKbps: p.Profile.Bitrate, MaxWidth: p.Profile.MaxWidth, MaxHeight: p.Profile.MaxHeight}
	if !p.KeepResolution {
		return plan
	}
	if f == nil || f.ResolutionH == nil || *f.ResolutionH <= 0 {
		// Unknown size: the 1080p rung is a safe middle.
		plan.BitrateKbps = rung(8000).Bitrate
		return plan
	}
	h := *f.ResolutionH
	if f.ResolutionW != nil && *f.ResolutionW > 0 {
		// A scope 4K film is 3840×1600: judge the tier by width too.
		if w16x9 := *f.ResolutionW * 9 / 16; w16x9 > h {
			h = w16x9
		}
	}
	var pick *transcode.QualityProfile
	for i := range transcode.Profiles {
		r := &transcode.Profiles[i]
		if r.MaxHeight < h {
			continue
		}
		if pick == nil || r.MaxHeight < pick.MaxHeight ||
			(r.MaxHeight == pick.MaxHeight && r.Bitrate > pick.Bitrate) {
			pick = r
		}
	}
	if pick == nil {
		pick = &transcode.Profiles[0] // above 4K: the top rung
	}
	plan.BitrateKbps = pick.Bitrate
	if f.VideoCodec != nil && strings.EqualFold(*f.VideoCodec, "h264") && f.Bitrate != nil && *f.Bitrate > 0 {
		srcKbps := int(*f.Bitrate / 1000)
		if srcKbps > 0 && srcKbps < plan.BitrateKbps {
			plan.BitrateKbps = max(srcKbps, 500)
		}
	}
	return plan
}
