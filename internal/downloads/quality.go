// Package downloads runs converted ("lower-quality") downloads: a movie or
// episode encoded once into a progressive MP4 at one rung of the transcode
// ladder, cached, and served to every user who asks for that file at that
// quality until it expires.
//
// The pieces:
//
//   - Service: what the API calls — request a conversion (creating or
//     reusing the shared encode), read and list a user's jobs, delete one,
//     find a ready file.
//   - Runner: claims queued encodes from the database and runs ffmpeg on this
//     server, at most DOWNLOAD_MAX_CONVERSIONS at once and each holding a
//     slot of the embedded transcode worker, so conversions count against
//     the same transcode concurrency as playback. The admin's optimized
//     versions (optimize_profiles.go) run from the same queue.
//   - Cleaner: the download_cache_cleanup scheduler task — evicts expired
//     files, then the least recently used ones past the cache size limit,
//     and removes orphans.
//
// Schema: download_conversions + download_jobs, migration 00051.
package downloads

import (
	"fmt"

	"github.com/onscreen/onscreen/internal/transcode"
)

// Quality is one converted-download choice: a rung of transcode.Profiles.
type Quality struct {
	// ID is the API value ("720p_4mbps"); also the database value.
	ID string
	// Label is the human name ("720p · 4 Mbps").
	Label   string
	Profile transcode.QualityProfile
}

// qualityRungs names the ladder rungs offered as downloads, by bitrate. The
// 4K rung is left out: a "lower-quality" download that large is the original
// in all but name.
var qualityRungs = []struct {
	id   string
	kbps int
}{
	{"1080p_8mbps", 8000},
	{"720p_4mbps", 4000},
	{"720p_2mbps", 2000},
	{"480p_1mbps", 1000},
}

// Qualities is the converted-download menu, best first.
var Qualities = buildQualities()

func buildQualities() []Quality {
	out := make([]Quality, 0, len(qualityRungs))
	for _, r := range qualityRungs {
		for _, p := range transcode.Profiles {
			if p.Bitrate == r.kbps {
				out = append(out, Quality{
					ID:      r.id,
					Label:   fmt.Sprintf("%dp · %d Mbps", p.MaxHeight, p.Bitrate/1000),
					Profile: p,
				})
				break
			}
		}
	}
	return out
}

// QualityByID looks a quality up by its API id.
func QualityByID(id string) (Quality, bool) {
	for _, q := range Qualities {
		if q.ID == id {
			return q, true
		}
	}
	return Quality{}, false
}

// AllowedUnder reports whether the quality fits a bitrate cap in kbps (0 =
// no cap). It follows transcode.ProfileForBitrate, the rule playback uses:
// the highest rung whose bitrate fits, or the lowest rung when none does — so
// a user capped at 4 Mbps can take 720p 4 Mbps but not 1080p 8 Mbps, and a
// user capped below 1 Mbps still gets 480p rather than nothing.
func (q Quality) AllowedUnder(capKbps int) bool {
	if capKbps <= 0 {
		return true
	}
	return q.Profile.Bitrate <= transcode.ProfileForBitrate(capKbps).Bitrate
}
