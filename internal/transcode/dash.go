package transcode

import (
	"encoding/xml"
	"fmt"
	"strings"
)

// DASHParams describes everything BuildMPD needs to emit a DASH MPD
// for a single-rendition fMP4 session. The session's segment ladder
// is the same one ffmpeg already writes for the HLS playlist — we
// just expose it under an MPD manifest so DASH-only clients (most
// smart-TV apps) can play the same bytes.
type DASHParams struct {
	// SessionID identifies the session in segment URLs. The handler
	// wraps these in absolute /api/v1/transcode/sessions/{id}/seg/...
	// paths with auth tokens; BuildMPD only needs to reference the
	// segment basenames.
	SessionID string

	// Token is the segment auth token appended to init/media URLs so
	// the MPD is a fully-loadable manifest with no further
	// rewriting on the client. Mirrors the HLS rewritePlaylist
	// approach.
	Token string

	// BaseURL is the absolute prefix for segment fetches — typically
	// "/api/v1/transcode/sessions/<id>/seg/". Trailing slash required.
	BaseURL string

	// SegmentDurationSec is the nominal duration of each segment in
	// seconds. The session uses a single fixed duration (ADR-007),
	// so we encode it as SegmentTemplate@duration without a timeline.
	SegmentDurationSec int

	// SegmentCount is the number of seg*.m4s files currently on disk.
	// Used for mediaPresentationDuration when type=static; for type=
	// dynamic (live transcode-in-progress) we omit duration and let
	// clients re-fetch the manifest as more segments arrive.
	SegmentCount int

	// Live indicates the session is still being produced (transcode
	// running). Sets MPD@type=dynamic and adds availabilityStartTime
	// so DASH clients keep polling the manifest. Set false once the
	// session is closed and the segment list is final.
	Live bool

	// VideoCodec is the codecs string for the Representation, e.g.
	// "avc1.640028" or "hvc1.1.6.L120.90". When empty we fall back to
	// the four-letter fourCC ("hvc1" / "av01") which most modern
	// clients accept by probing the init segment. Future work: parse
	// the init.mp4 box to derive the exact RFC 6381 codec string.
	VideoCodec string

	// AudioCodec is the codecs string for the audio Representation,
	// e.g. "mp4a.40.2" for AAC-LC. When empty we omit the audio
	// AdaptationSet entirely — useful for video-only debug sessions.
	AudioCodec string

	// BandwidthBps is the nominal video bitrate in bits per second.
	// Required by the DASH spec; ABR-capable clients use it to pick
	// the highest stream their connection can sustain (we currently
	// expose a single rendition, so it only affects buffer sizing).
	BandwidthBps int

	// AudioBandwidthBps is the nominal audio bitrate. When zero we
	// estimate 128 kbps for stereo AAC, which is a safe default.
	AudioBandwidthBps int

	// Width / Height are the rendered video dimensions. Optional —
	// when zero we omit the @width/@height attributes (clients still
	// play, just don't show resolution metadata).
	Width  int
	Height int
}

// BuildMPD writes a DASH 2011-schema MPD describing a session's fMP4
// ladder. The generated manifest references the same init.mp4 +
// seg*.m4s files ffmpeg already produces for HLS — no parallel
// encoding ladder, no second muxer. The handler serves it as
// application/dash+xml and the same segment fetch path serves both
// HLS and DASH clients.
//
// Limitations (v2.1 intro):
//   - Single rendition only (no ABR ladder yet — same as the HLS side).
//   - Static codec strings ("hvc1", "av01") rather than parsed RFC
//     6381; works on shaka-player / exoplayer / dash.js but may need
//     refinement for stricter clients.
//   - No subtitle AdaptationSets — DASH callers fall back to the
//     existing /sub/{i}.vtt sidecars served via WebVTT.
func BuildMPD(p DASHParams) ([]byte, error) {
	if p.SessionID == "" || p.Token == "" {
		return nil, fmt.Errorf("dash: SessionID and Token are required")
	}
	if p.SegmentDurationSec <= 0 {
		p.SegmentDurationSec = SegmentDuration
	}
	if p.BaseURL == "" {
		p.BaseURL = fmt.Sprintf("/api/v1/transcode/sessions/%s/seg/", p.SessionID)
	}
	if !strings.HasSuffix(p.BaseURL, "/") {
		p.BaseURL += "/"
	}

	mpd := mpdRoot{
		XMLNS:                     "urn:mpeg:dash:schema:mpd:2011",
		MinBufferTime:             "PT4S",
		Profiles:                  "urn:mpeg:dash:profile:isoff-live:2011",
		Type:                      "static",
		MediaPresentationDuration: fmt.Sprintf("PT%dS", p.SegmentCount*p.SegmentDurationSec),
	}
	if p.Live {
		mpd.Type = "dynamic"
		mpd.MediaPresentationDuration = "" // unknown until the transcode finishes
		mpd.MinimumUpdatePeriod = fmt.Sprintf("PT%dS", p.SegmentDurationSec)
	}

	mpd.BaseURL = p.BaseURL

	// Token query string appended to every segment URL. SegmentTemplate
	// puts these in @initialization and @media so the segment fetcher
	// doesn't need to know about auth.
	tokenQS := "?token=" + p.Token

	// ── Video AdaptationSet ──────────────────────────────────────────────────
	videoCodec := p.VideoCodec
	if videoCodec == "" {
		videoCodec = "avc1" // safest fall-through; client will probe init
	}
	video := adaptationSet{
		MimeType:        "video/mp4",
		SegmentAlignment: "true",
		StartWithSAP:     1,
		Representations: []representation{{
			ID:           "v0",
			Codecs:       videoCodec,
			Bandwidth:    p.BandwidthBps,
			Width:        p.Width,
			Height:       p.Height,
			SegmentTemplate: &segmentTemplate{
				Timescale:      1,
				Duration:       p.SegmentDurationSec,
				StartNumber:    1,
				Initialization: "init.mp4" + tokenQS,
				Media:          "seg$Number%05d$.m4s" + tokenQS,
			},
		}},
	}
	mpd.Period.AdaptationSets = []adaptationSet{video}

	// ── Audio AdaptationSet ──────────────────────────────────────────────────
	if p.AudioCodec != "" {
		audioBW := p.AudioBandwidthBps
		if audioBW == 0 {
			audioBW = 128_000 // stereo AAC default
		}
		audio := adaptationSet{
			MimeType:         "audio/mp4",
			SegmentAlignment: "true",
			StartWithSAP:     1,
			Lang:             "und",
			Representations: []representation{{
				ID:        "a0",
				Codecs:    p.AudioCodec,
				Bandwidth: audioBW,
				SegmentTemplate: &segmentTemplate{
					Timescale:      1,
					Duration:       p.SegmentDurationSec,
					StartNumber:    1,
					Initialization: "init.mp4" + tokenQS,
					Media:          "seg$Number%05d$.m4s" + tokenQS,
				},
			}},
		}
		mpd.Period.AdaptationSets = append(mpd.Period.AdaptationSets, audio)
	}

	out, err := xml.MarshalIndent(mpd, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshal mpd: %w", err)
	}
	// Prepend XML declaration — required by the DASH spec; encoding/xml
	// only emits one when explicitly asked.
	return []byte(xml.Header + string(out)), nil
}

// mpdRoot mirrors the small subset of MPD@2011 schema we emit.
// Field order is significant for some validators (XSD enforces it),
// so the struct ordering matches MPD's spec ordering: profiles,
// type, durations, BaseURL, then Period.
type mpdRoot struct {
	XMLName                   xml.Name `xml:"MPD"`
	XMLNS                     string   `xml:"xmlns,attr"`
	Profiles                  string   `xml:"profiles,attr"`
	Type                      string   `xml:"type,attr"`
	MinBufferTime             string   `xml:"minBufferTime,attr"`
	MediaPresentationDuration string   `xml:"mediaPresentationDuration,attr,omitempty"`
	MinimumUpdatePeriod       string   `xml:"minimumUpdatePeriod,attr,omitempty"`
	BaseURL                   string   `xml:"BaseURL,omitempty"`
	Period                    period   `xml:"Period"`
}

type period struct {
	AdaptationSets []adaptationSet `xml:"AdaptationSet"`
}

type adaptationSet struct {
	MimeType         string           `xml:"mimeType,attr"`
	SegmentAlignment string           `xml:"segmentAlignment,attr"`
	StartWithSAP     int              `xml:"startWithSAP,attr"`
	Lang             string           `xml:"lang,attr,omitempty"`
	Representations  []representation `xml:"Representation"`
}

type representation struct {
	ID              string           `xml:"id,attr"`
	Codecs          string           `xml:"codecs,attr"`
	Bandwidth       int              `xml:"bandwidth,attr"`
	Width           int              `xml:"width,attr,omitempty"`
	Height          int              `xml:"height,attr,omitempty"`
	SegmentTemplate *segmentTemplate `xml:"SegmentTemplate,omitempty"`
}

type segmentTemplate struct {
	Timescale      int    `xml:"timescale,attr"`
	Duration       int    `xml:"duration,attr"`
	StartNumber    int    `xml:"startNumber,attr"`
	Initialization string `xml:"initialization,attr"`
	Media          string `xml:"media,attr"`
}
