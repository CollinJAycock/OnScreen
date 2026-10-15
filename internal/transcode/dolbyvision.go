package transcode

import (
	"context"
	"os/exec"
	"strings"

	"github.com/onscreen/onscreen/internal/domain/media"
)

// Dolby Vision delivery (docs/dolby-vision.md).
//
// A file's Dolby Vision profile key combines media_files.dv_profile with the
// base layer the scanner judged from the stream itself (hdr_type):
//
//	5    hdr_type "dolby_vision" (no base layer other players can show)
//	7    dual layer (base + enhancement layer), HDR10 base — UHD Blu-ray
//	8.1  single layer, HDR10 base
//	8.4  single layer, HLG base
//
// A profile 8 record over SDR video is no key at all: those files (QA's
// Hellraiser, The Sound of Music) carry the record but no metadata, and play
// as SDR.
//
// A client lists the keys it decodes (dovi=5:8.1:8.4) and is sent Dolby
// Vision for those; everyone else keeps getting the base layer, exactly as
// before.

// DVMode is what a stream-copy session does with a file's Dolby Vision.
type DVMode string

const (
	// DVNone: no Dolby Vision handling — the base layer, as before.
	DVNone DVMode = ""
	// DVKeep: carry the Dolby Vision configuration record and the per-frame
	// metadata into the fMP4 segments (ffmpeg drops the record without
	// -strict unofficial).
	DVKeep DVMode = "keep"
	// DVToP81: convert profile 7 to 8.1 while remuxing: drop the enhancement
	// layer and rewrite each frame's metadata for a single-layer decoder.
	// Needs an ffmpeg with OnScreen's dovi_rpu conversion patch
	// (WorkerFeatureDVConvert).
	DVToP81 DVMode = "to_p81"
)

// DVProfileKey is the file's Dolby Vision profile key ("5", "7", "8.1",
// "8.4"), or "" when the file has no Dolby Vision a player could use.
func DVProfileKey(f media.File) string {
	hdr := strings.ToLower(deref(f.HDRType))
	if hdr == "dolby_vision" {
		return "5"
	}
	if f.DVProfile == nil {
		return ""
	}
	switch *f.DVProfile {
	case 7:
		if hdr == "hdr10" || hdr == "hdr10plus" {
			return "7"
		}
	case 8:
		switch hdr {
		case "hdr10", "hdr10plus":
			return "8.1"
		case "hlg":
			return "8.4"
		}
	}
	return ""
}

// SupportsDVProfile reports whether the client listed a Dolby Vision profile
// key in its capability profile.
func (c ClientCapabilities) SupportsDVProfile(key string) bool {
	if key == "" {
		return false
	}
	for _, p := range c.DVProfiles {
		if p == key {
			return true
		}
	}
	return false
}

// PlanDolbyVision picks what a stream copy of f does with its Dolby Vision for
// this client: keep it when the client decodes the file's profile, convert
// profile 7 to 8.1 when the client takes 8.1 and the server can convert, and
// otherwise nothing (the base layer).
func PlanDolbyVision(f media.File, caps ClientCapabilities, canConvert bool) DVMode {
	key := DVProfileKey(f)
	switch {
	case key == "":
		return DVNone
	case caps.SupportsDVProfile(key):
		return DVKeep
	case key == "7" && canConvert && caps.SupportsDVProfile("8.1"):
		return DVToP81
	}
	return DVNone
}

// dvConvertBSF is the -bsf:v filter that converts profile 7 to 8.1 (OnScreen's
// patch to ffmpeg's dovi_rpu filter, docker/Dockerfile.ffmpeg).
const dvConvertBSF = "dovi_rpu=convert_p81=1"

// dvConvertOption is the dovi_rpu option the conversion patch adds; its
// presence in `ffmpeg -h bsf=dovi_rpu` is how a worker knows it can convert.
const dvConvertOption = "convert_p81"

// ProbeDVConvert reports whether this worker's ffmpeg has the profile 7 → 8.1
// conversion in its dovi_rpu filter.
func ProbeDVConvert(ctx context.Context) bool {
	out, err := exec.CommandContext(ctx, "ffmpeg", "-hide_banner", "-h", "bsf=dovi_rpu").CombinedOutput()
	if err != nil {
		return false
	}
	return strings.Contains(string(out), dvConvertOption)
}
