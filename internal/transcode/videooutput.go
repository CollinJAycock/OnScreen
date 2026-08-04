package transcode

// VideoOutput describes what the video stream in the HLS output ACTUALLY is,
// as opposed to what encoder was asked for.
//
// This type exists because deriving the container from the encoder name is a
// bug that has now been fixed three separate times in three separate places:
//
//   - AV1 source remux wrote MPEG-TS and crashed the muxer outright
//     ("Could not find tag for codec av1").
//   - The ABR ladder predicted its segment container from the PARENT session's
//     client-capability flags while the worker encoded the CHILD with whatever
//     encoder it actually had, so every segment 503'd.
//   - HEVC source remux wrote MPEG-TS, which does not crash — MPEG-TS has a
//     stream type for HEVC — so ffmpeg produced segments no browser can decode
//     through MSE. Audio played, video never did, and the picture alternated
//     black and image with nothing in the logs.
//
// Each was the same mistake: `-c:v copy` means the OUTPUT codec is the SOURCE
// codec, and the encoder name says nothing about it. Answer the question once,
// from the truth, and let every consumer read the answer.
type VideoOutput struct {
	HEVC bool
	AV1  bool
	// ForcedFMP4 pins the container to fMP4 for a codec that would otherwise
	// use MPEG-TS. Set when a client is already holding a playlist that names
	// .m4s URIs and an EXT-X-MAP init — written before we knew which encoder
	// the job would land on — so an encoder fallback must not change the
	// container out from under it.
	//
	// It affects the CONTAINER only, never TagV: H.264 in fMP4 takes the
	// default avc1 and must not be tagged hvc1/av01.
	ForcedFMP4 bool
}

// EncoderCopy is the encoder string that means "remux, don't re-encode".
const EncoderCopy Encoder = "copy"

// IsVideoCopy reports whether this encoder remuxes rather than re-encodes.
// Owned here so no caller can pass an `encoder`/`videoCopy` pair that disagree.
func IsVideoCopy(encoder Encoder) bool { return encoder == EncoderCopy }

// ResolveVideoOutput reports what the video stream will be.
//
// The remux case is the whole reason this function exists: on `-c:v copy` the
// source codec passes through untouched and `encoder` is the literal string
// "copy", which belongs to no codec family — so IsHEVCEncoder and IsAV1Encoder
// both answer false and every container question derived from them is wrong.
// Only when we are really encoding does the encoder determine the output.
// forceFMP4 is honoured only on a re-encode: a remux's container is already
// dictated by the source codec it is passing through untouched.
func ResolveVideoOutput(encoder Encoder, sourceHEVC, sourceAV1, forceFMP4 bool) VideoOutput {
	if IsVideoCopy(encoder) {
		return VideoOutput{HEVC: sourceHEVC, AV1: sourceAV1}
	}
	return VideoOutput{
		HEVC:       IsHEVCEncoder(encoder),
		AV1:        IsAV1Encoder(encoder),
		ForcedFMP4: forceFMP4,
	}
}

// NeedsFMP4 reports whether the output must be packaged as fMP4 (.m4s segments
// plus an EXT-X-MAP init) rather than MPEG-TS.
//
// Both HEVC and AV1 require it, for different reasons that arrive at the same
// place: MPEG-TS has no AV1 stream type at all, and while it does carry HEVC,
// HLS.js cannot transmux HEVC and no browser decodes HEVC-in-TS via MSE.
func (v VideoOutput) NeedsFMP4() bool { return v.HEVC || v.AV1 || v.ForcedFMP4 }

// SegExt is the segment file extension implied by the container.
func (v VideoOutput) SegExt() string {
	if v.NeedsFMP4() {
		return ".m4s"
	}
	return ".ts"
}

// SegType is the ffmpeg `-hls_segment_type` value.
func (v VideoOutput) SegType() string {
	if v.NeedsFMP4() {
		return "fmp4"
	}
	return "mpegts"
}

// TagV is the `-tag:v` fourCC the container needs, or "" when none applies.
// MSE requires the modern tag rather than the codec's native one to recognise
// the track — Safari will not play an untagged HEVC fMP4.
func (v VideoOutput) TagV() string {
	switch {
	case v.HEVC:
		return "hvc1"
	case v.AV1:
		return "av01"
	default:
		return ""
	}
}
