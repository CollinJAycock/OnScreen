package scheduler

import (
	"context"
	"encoding/json"
)

// SubtitleAutoDownloader downloads missing subtitles for new movies and
// episodes. Satisfied by *subtitles.AutoDownloader.
type SubtitleAutoDownloader interface {
	Run(ctx context.Context) (string, error)
}

// NewSubtitleAutoDownloadHandler returns the subtitle_auto_download task: for
// movies and episodes with no subtitle in a wanted language, search
// OpenSubtitles and download the best match. Does nothing unless an admin
// turned automatic downloads on and OpenSubtitles is set up.
func NewSubtitleAutoDownloadHandler(d SubtitleAutoDownloader) HandlerFunc {
	return func(ctx context.Context, _ json.RawMessage) (string, error) {
		return d.Run(ctx)
	}
}
