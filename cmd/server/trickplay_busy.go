package main

import (
	"time"

	"github.com/onscreen/onscreen/internal/transcode"
)

// hasLivePlayback reports whether any transcode session is live: an ABR
// parent or plain session (ParentID == "") with activity — LastActivityAt,
// else CreatedAt for one that hasn't fetched its first segment — within
// transcode.ActiveSessionWindow. The same rule as SessionStore.CountByUser
// and Now Playing.
//
// The trickplay queue pauses on this. It used to pause on any session in the
// store, but an abandoned one (crashed client, TV switched off, a DELETE that
// never came) lingers for the full idle TTL (hours) with its ffmpeg long
// gone, so a single dead stream stalled thumbnail backfill for hours.
func hasLivePlayback(sessions []transcode.Session, now time.Time) bool {
	for _, s := range sessions {
		if s.ParentID != "" {
			continue // ABR rung children ride on their parent's liveness
		}
		activeAt := s.LastActivityAt
		if activeAt.IsZero() {
			activeAt = s.CreatedAt
		}
		if now.Sub(activeAt) <= transcode.ActiveSessionWindow {
			return true
		}
	}
	return false
}
