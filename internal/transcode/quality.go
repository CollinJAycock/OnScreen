package transcode

// QualityProfile is a named bitrate + resolution preset.
type QualityProfile struct {
	Name      string
	Bitrate   int // kbps
	MaxWidth  int
	MaxHeight int
}

// Profiles is the quality ladder for transcode output (ADR-017).
// Bitrates are H.264 reference values; HEVC output scales by HEVCBitrateRatio.
var Profiles = []QualityProfile{
	{Name: "40 Mbps 4K", Bitrate: 40000, MaxWidth: 3840, MaxHeight: 2160},
	{Name: "8 Mbps 1080p", Bitrate: 8000, MaxWidth: 1920, MaxHeight: 1080},
	{Name: "4 Mbps 720p", Bitrate: 4000, MaxWidth: 1280, MaxHeight: 720},
	{Name: "2 Mbps 720p", Bitrate: 2000, MaxWidth: 1280, MaxHeight: 720},
	{Name: "1 Mbps 480p", Bitrate: 1000, MaxWidth: 854, MaxHeight: 480},
}

// HEVCBitrateRatio is the HEVC-to-H.264 efficiency factor.
// HEVC achieves equivalent quality at ~60% of the H.264 bitrate.
const HEVCBitrateRatio = 0.6

// AV1BitrateRatio is the AV1-to-H.264 efficiency factor. AV1 is ~20%
// more efficient than HEVC again, so ~50% of the H.264 bitrate.
const AV1BitrateRatio = 0.5

// ScaleBitrateForHEVC adjusts a reference H.264 bitrate for HEVC output.
func ScaleBitrateForHEVC(h264Bitrate int) int {
	return int(float64(h264Bitrate) * HEVCBitrateRatio)
}

// ScaleBitrateForAV1 adjusts a reference H.264 bitrate for AV1 output.
func ScaleBitrateForAV1(h264Bitrate int) int {
	return int(float64(h264Bitrate) * AV1BitrateRatio)
}

// SelectQuality picks the effective quality from client request params + server caps (ADR-017).
// Effective = MIN(client_requested, server_cap). Source resolution/bitrate cap upscaling.
func SelectQuality(
	clientBitrateKbps, clientMaxWidth, clientMaxHeight int,
	sourceW, sourceH int,
	serverCaps ServerCaps,
) QualityProfile {
	// Start with server caps.
	effectiveBitrate := serverCaps.MaxBitrateKbps
	if effectiveBitrate <= 0 {
		effectiveBitrate = 40000
	}
	effectiveW := serverCaps.MaxWidth
	if effectiveW <= 0 {
		effectiveW = 3840
	}
	effectiveH := serverCaps.MaxHeight
	if effectiveH <= 0 {
		effectiveH = 2160
	}

	// Apply client constraints.
	if clientBitrateKbps > 0 && clientBitrateKbps < effectiveBitrate {
		effectiveBitrate = clientBitrateKbps
	}
	if clientMaxWidth > 0 && clientMaxWidth < effectiveW {
		effectiveW = clientMaxWidth
	}
	if clientMaxHeight > 0 && clientMaxHeight < effectiveH {
		effectiveH = clientMaxHeight
	}

	// Don't upscale beyond source resolution.
	if sourceW > 0 && sourceW < effectiveW {
		effectiveW = sourceW
	}
	if sourceH > 0 && sourceH < effectiveH {
		effectiveH = sourceH
	}

	// If the client didn't specify a bitrate, pick one from the profile ladder
	// that matches the REQUESTED resolution (before source capping). This way
	// "1080p" on a 796p source still gets the 1080p bitrate tier (20 Mbps)
	// rather than falling to the 720p tier (4 Mbps) because source-capped
	// effectiveH is only 796.
	if clientBitrateKbps <= 0 {
		bitrateH := effectiveH
		if clientMaxHeight > 0 && clientMaxHeight > bitrateH {
			bitrateH = clientMaxHeight
		}
		for _, p := range Profiles {
			if p.MaxHeight <= bitrateH && p.Bitrate <= effectiveBitrate {
				effectiveBitrate = p.Bitrate
				break
			}
		}
	}

	// Encoders (libx264/x265, every hardware encoder with yuv420p/nv12 output)
	// require EVEN dimensions. The source cap above copies the source's exact
	// size, so an odd-dimension source — rotated phone video, a 1279×719 rip —
	// flowed odd numbers straight into scale/pad targets and the encoder
	// failed at init on every Auto-quality transcode of that file. Round DOWN:
	// one pixel under the cap is always valid, one over may exceed the source.
	effectiveW -= effectiveW % 2
	effectiveH -= effectiveH % 2

	return QualityProfile{
		Name:      "custom",
		Bitrate:   effectiveBitrate,
		MaxWidth:  effectiveW,
		MaxHeight: effectiveH,
	}
}
