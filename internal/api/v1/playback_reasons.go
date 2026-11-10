package v1

import (
	"strconv"
	"strings"

	"github.com/onscreen/onscreen/internal/domain/media"
	"github.com/onscreen/onscreen/internal/i18n"
	"github.com/onscreen/onscreen/internal/transcode"
)

// reasonRequestedByClient is startReasonMessages' fallback when nothing the
// server can see explains the request ("Requested by client").
const reasonRequestedByClient = "reason.requested"

// sessionPlaybackMeta is the Now Playing attribution transcode Start stamps
// on the session it creates: who is watching from where, why this isn't a
// direct play, and what the output looks like. Display-only — nothing in the
// pipeline reads it back. The reasons are messages, written in the language
// of whoever reads the card (transcode.Session.ReasonsIn).
type sessionPlaybackMeta struct {
	clientIP      string
	reasons       []transcode.ReasonMessage
	outW, outH    int
	audioCodec    string // "aac" | "copy" | "eac3" / "ac3" (encoded for the receiver)
	audioChannels int
	// audioPassthrough: the copied track is bitstreamed to the client's
	// receiver (audioPassthrough), not decoded by the client.
	audioPassthrough bool
}

func (m sessionPlaybackMeta) apply(s *transcode.Session) {
	s.ClientIP = m.clientIP
	s.SetTranscodeReasons(m.reasons)
	s.OutputWidth, s.OutputHeight = m.outW, m.outH
	s.OutputAudioCodec = m.audioCodec
	s.OutputAudioChannels = m.audioChannels
	s.OutputAudioPassthrough = m.audioPassthrough
}

// startReasonMessages explains, in words an admin can act on, why Start is
// running a remux or transcode instead of a direct play. It combines the
// capability mismatches Decide sees (transcode.DecisionReasonMsgs) with the
// two things only Start knows: the bitrate cap (the user's, or for a remote
// client the internet limit — bitrateCap.RemoteBinding says which) and an
// explicit quality pick.
//
// A remux copies the video, so only audio/container reasons can explain one;
// video-side mismatches are dropped for it. Never empty: when nothing the
// server can see explains the request, it says so ("Requested by client" —
// e.g. the player escalated after a local decode failure).
func startReasonMessages(file *media.File, caps transcode.ClientCapabilities, decision string,
	reqHeight, sourceH int, bitrateCap streamCap, capForced bool,
) []transcode.ReasonMessage {
	var out []transcode.ReasonMessage
	srcKbps := 0
	if file.Bitrate != nil {
		srcKbps = int(*file.Bitrate / 1000)
	}
	capKbps := bitrateCap.Kbps
	if capForced || (decision == "transcode" && capKbps > 0 && srcKbps > capKbps) {
		key := "reason.overUserCap"
		if bitrateCap.RemoteBinding {
			key = "reason.overInternetLimit"
		} else if bitrateCap.QualityBinding {
			key = "reason.overQualitySetting"
		}
		out = append(out, transcode.NewReasonMessage(key, "source", fmtMbps(srcKbps), "cap", fmtMbps(capKbps)))
	}
	for _, r := range transcode.DecisionReasonMsgs(*file, caps) {
		if decision == "remux" && r.Kind != transcode.ReasonAudio && r.Kind != transcode.ReasonContainer {
			continue
		}
		out = append(out, r.Msg)
	}
	if decision == "transcode" && reqHeight > 0 && (sourceH == 0 || reqHeight < sourceH) {
		out = append(out, transcode.NewReasonMessage("reason.qualityPicked", "height", reqHeight))
	}
	if len(out) == 0 {
		if decision == "remux" {
			return []transcode.ReasonMessage{transcode.NewReasonMessage("reason.remuxRequested")}
		}
		return []transcode.ReasonMessage{transcode.NewReasonMessage(reasonRequestedByClient)}
	}
	return out
}

// startReasons is startReasonMessages in English.
func startReasons(file *media.File, caps transcode.ClientCapabilities, decision string,
	reqHeight, sourceH int, bitrateCap streamCap, capForced bool,
) []string {
	return transcode.MessageTexts(
		startReasonMessages(file, caps, decision, reqHeight, sourceH, bitrateCap, capForced), i18n.English)
}

// fmtMbps renders kbps as "8 Mbps" / "25.3 Mbps".
func fmtMbps(kbps int) string {
	s := strconv.FormatFloat(float64(kbps)/1000, 'f', 1, 64)
	return strings.TrimSuffix(s, ".0") + " Mbps"
}
