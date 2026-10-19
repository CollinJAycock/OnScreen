package v1

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/onscreen/onscreen/internal/domain/media"
	"github.com/onscreen/onscreen/internal/transcode"
)

// sessionPlaybackMeta is the Now Playing attribution transcode Start stamps
// on the session it creates: who is watching from where, why this isn't a
// direct play, and what the output looks like. Display-only — nothing in the
// pipeline reads it back.
type sessionPlaybackMeta struct {
	clientIP      string
	reasons       []string
	outW, outH    int
	audioCodec    string // "aac" | "copy"
	audioChannels int
	// audioPassthrough: the copied track is bitstreamed to the client's
	// receiver (audioPassthrough), not decoded by the client.
	audioPassthrough bool
}

func (m sessionPlaybackMeta) apply(s *transcode.Session) {
	s.ClientIP = m.clientIP
	s.TranscodeReasons = m.reasons
	s.OutputWidth, s.OutputHeight = m.outW, m.outH
	s.OutputAudioCodec = m.audioCodec
	s.OutputAudioChannels = m.audioChannels
	s.OutputAudioPassthrough = m.audioPassthrough
}

// startReasons explains, in words an admin can act on, why Start is running a
// remux or transcode instead of a direct play. It combines the capability
// mismatches Decide sees (transcode.DecisionReasons) with the two things only
// Start knows: the bitrate cap (the user's, or for a remote client the
// internet limit — bitrateCap.RemoteBinding says which) and an explicit
// quality pick.
//
// A remux copies the video, so only audio/container reasons can explain one;
// video-side mismatches are dropped for it. Never empty: when nothing the
// server can see explains the request, it says so ("Requested by client" —
// e.g. the player escalated after a local decode failure).
func startReasons(file *media.File, caps transcode.ClientCapabilities, decision string,
	reqHeight, sourceH int, bitrateCap streamCap, capForced bool,
) []string {
	var out []string
	srcKbps := 0
	if file.Bitrate != nil {
		srcKbps = int(*file.Bitrate / 1000)
	}
	capKbps := bitrateCap.Kbps
	if capForced || (decision == "transcode" && capKbps > 0 && srcKbps > capKbps) {
		label := "Over the user's bitrate cap"
		if bitrateCap.RemoteBinding {
			label = "Over the internet streaming limit"
		} else if bitrateCap.QualityBinding {
			label = "Over the viewer's quality setting"
		}
		out = append(out, fmt.Sprintf("%s (%s > %s)", label, fmtMbps(srcKbps), fmtMbps(capKbps)))
	}
	for _, r := range transcode.DecisionReasons(*file, caps) {
		if decision == "remux" && r.Kind != transcode.ReasonAudio && r.Kind != transcode.ReasonContainer {
			continue
		}
		out = append(out, r.Text)
	}
	if decision == "transcode" && reqHeight > 0 && (sourceH == 0 || reqHeight < sourceH) {
		out = append(out, fmt.Sprintf("Quality set to %dp by the viewer", reqHeight))
	}
	if len(out) == 0 {
		if decision == "remux" {
			return []string{"Container adapted for streaming (requested by client)"}
		}
		return []string{"Requested by client"}
	}
	return out
}

// fmtMbps renders kbps as "8 Mbps" / "25.3 Mbps".
func fmtMbps(kbps int) string {
	s := strconv.FormatFloat(float64(kbps)/1000, 'f', 1, 64)
	return strings.TrimSuffix(s, ".0") + " Mbps"
}
