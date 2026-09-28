package main

import (
	"testing"
	"time"

	"github.com/onscreen/onscreen/internal/transcode"
)

func TestHasLivePlayback(t *testing.T) {
	now := time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)
	fresh := now.Add(-30 * time.Second)
	edge := now.Add(-transcode.ActiveSessionWindow)
	stale := now.Add(-transcode.ActiveSessionWindow - time.Second)
	abandoned := now.Add(-3 * time.Hour)

	cases := []struct {
		name     string
		sessions []transcode.Session
		want     bool
	}{
		{"no sessions", nil, false},
		{"recent segment fetch", []transcode.Session{{CreatedAt: abandoned, LastActivityAt: fresh}}, true},
		{"activity exactly at the window edge", []transcode.Session{{CreatedAt: abandoned, LastActivityAt: edge}}, true},
		{"just started, no segment fetched yet", []transcode.Session{{CreatedAt: fresh}}, true},
		// The regression: an abandoned session sits in the store until its
		// idle TTL (hours) and used to hold the backfill for all of it.
		{"abandoned session only", []transcode.Session{{CreatedAt: abandoned, LastActivityAt: abandoned}}, false},
		{"stale activity beats a fresh CreatedAt fallback", []transcode.Session{{CreatedAt: fresh, LastActivityAt: stale}}, false},
		{"never-fetched session gone quiet", []transcode.Session{{CreatedAt: stale}}, false},
		// ABR rung children don't count on their own; the parent's
		// activity (refreshed by SetSelectedRendition) is the signal.
		{"live child of a dead parent", []transcode.Session{
			{ID: "p", CreatedAt: abandoned, LastActivityAt: abandoned},
			{ID: "c", ParentID: "p", CreatedAt: fresh, LastActivityAt: fresh},
		}, false},
		{"one live among abandoned", []transcode.Session{
			{CreatedAt: abandoned, LastActivityAt: abandoned},
			{CreatedAt: abandoned, LastActivityAt: fresh},
		}, true},
	}
	for _, tc := range cases {
		if got := hasLivePlayback(tc.sessions, now); got != tc.want {
			t.Errorf("%s: hasLivePlayback = %v, want %v", tc.name, got, tc.want)
		}
	}
}
