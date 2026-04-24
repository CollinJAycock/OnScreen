package transcode

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strconv"
	"time"
)

// keyframeWindowSec is how far back of the requested seek time we scan
// for keyframes. Movies typically have a keyframe every 2–10 s, but BD
// rips with sparse GOPs can stretch to 30 s. Anything beyond that is a
// pathological encode and we fall back to the requested time as-is.
const keyframeWindowSec = 30.0

type packetEntry struct {
	PtsTime string `json:"pts_time"`
	Flags   string `json:"flags"`
}

type packetsOutput struct {
	Packets []packetEntry `json:"packets"`
}

// FindPreviousKeyframe returns the PTS of the most recent video keyframe
// at or before targetSec. When video is being stream-copied we can only
// start a session at a keyframe — input-side -ss already does this, but
// the chosen keyframe can be 5–10 s earlier than the requested time and
// the player has no way to know how far back it actually landed. Pre-
// computing the keyframe lets the caller report a truthful offset back
// to the client (so the scrubber UI matches what's on screen) and skip
// the probe entirely when the target is 0.
//
// Returns targetSec unchanged if ffprobe can't find a keyframe in the
// scan window — better to keep the existing behavior than to fail the
// session startup because of a quirky source.
func FindPreviousKeyframe(ctx context.Context, path string, targetSec float64) float64 {
	if targetSec <= 0 {
		return 0
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	start := targetSec - keyframeWindowSec
	if start < 0 {
		start = 0
	}
	// -read_intervals "<start>%<end>" scopes the scan to a small window
	// — without it ffprobe can chew through gigabytes looking for
	// keyframes in a 2-hour movie.
	interval := fmt.Sprintf("%.3f%%%.3f", start, targetSec)
	args := []string{
		"-v", "error",
		"-select_streams", "v:0",
		"-read_intervals", interval,
		"-show_entries", "packet=pts_time,flags",
		"-print_format", "json",
		path,
	}
	out, err := exec.CommandContext(ctx, "ffprobe", args...).Output()
	if err != nil {
		return targetSec
	}

	var p packetsOutput
	if err := json.Unmarshal(out, &p); err != nil {
		return targetSec
	}

	best := -1.0
	for _, pkt := range p.Packets {
		// Keyframe flag is the first character of the flags string ("K_").
		if len(pkt.Flags) == 0 || pkt.Flags[0] != 'K' {
			continue
		}
		t, perr := strconv.ParseFloat(pkt.PtsTime, 64)
		if perr != nil {
			continue
		}
		if t > targetSec {
			continue
		}
		if t > best {
			best = t
		}
	}
	if best < 0 {
		return targetSec
	}
	return best
}
