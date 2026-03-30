// Package transcode implements the transcode decision pipeline, FFmpeg command
// building, session management, and HLS segment authentication.
package transcode

import (
	"strings"
)

// ClientCapabilities holds parsed client playback capabilities.
// Derived from the X-Client-Capabilities request header.
type ClientCapabilities struct {
	VideoCodecs     []string
	AudioCodecs     []string
	Containers      []string
	MaxWidth        int
	MaxHeight       int
	MaxAudioChannels int
	SupportsHDR     bool // HDR10
	SupportsDV      bool // Dolby Vision
	SupportsHEVC    bool // H.265
	SupportsAV1     bool
}

// ParseCapabilities parses the X-Client-Capabilities header value.
// Format: "videoDecoder=h264:h265,audioDecoder=ac3:aac,maxWidth=1920,maxHeight=1080"
func ParseCapabilities(header string) ClientCapabilities {
	caps := ClientCapabilities{
		MaxWidth:         1920,
		MaxHeight:        1080,
		MaxAudioChannels: 2,
	}
	if header == "" {
		return caps
	}

	// Parse key=val pairs separated by & or ,
	parts := strings.FieldsFunc(header, func(r rune) bool {
		return r == '&' || r == ','
	})
	for _, part := range parts {
		kv := strings.SplitN(strings.TrimSpace(part), "=", 2)
		if len(kv) != 2 {
			continue
		}
		key, val := strings.ToLower(kv[0]), kv[1]
		vals := strings.Split(val, ":")

		switch key {
		case "videodecoder":
			caps.VideoCodecs = append(caps.VideoCodecs, vals...)
			for _, v := range vals {
				switch strings.ToLower(v) {
				case "h265", "hevc":
					caps.SupportsHEVC = true
				case "av1":
					caps.SupportsAV1 = true
				}
			}
		case "audiodecoder":
			caps.AudioCodecs = append(caps.AudioCodecs, vals...)
		case "protocols":
			caps.Containers = append(caps.Containers, vals...)
		case "maxwidth":
			if w := parseInt(val); w > 0 {
				caps.MaxWidth = w
			}
		case "maxheight":
			if h := parseInt(val); h > 0 {
				caps.MaxHeight = h
			}
		case "maxaudiochannels":
			if c := parseInt(val); c > 0 {
				caps.MaxAudioChannels = c
			}
		}
	}
	return caps
}

// SupportsVideoCodec reports whether the client declared support for a video codec.
func (c ClientCapabilities) SupportsVideoCodec(codec string) bool {
	codec = strings.ToLower(codec)
	for _, v := range c.VideoCodecs {
		if strings.ToLower(v) == codec {
			return true
		}
	}
	return false
}

// SupportsAudioCodec reports whether the client declared support for an audio codec.
func (c ClientCapabilities) SupportsAudioCodec(codec string) bool {
	codec = strings.ToLower(codec)
	for _, v := range c.AudioCodecs {
		if strings.ToLower(v) == codec {
			return true
		}
	}
	return false
}

// SupportsContainer reports whether the client declared support for a container.
func (c ClientCapabilities) SupportsContainer(container string) bool {
	container = strings.ToLower(container)
	for _, v := range c.Containers {
		if strings.ToLower(v) == container {
			return true
		}
	}
	// No declared containers — assume basic support for mkv/mp4.
	if len(c.Containers) == 0 {
		return container == "mkv" || container == "mp4" || container == "mov"
	}
	return false
}

func parseInt(s string) int {
	n := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			break
		}
		n = n*10 + int(c-'0')
	}
	return n
}
