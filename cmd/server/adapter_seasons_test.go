package main

import (
	"context"
	"errors"
	"testing"

	v1 "github.com/onscreen/onscreen/internal/api/v1"
	"github.com/onscreen/onscreen/internal/metadata"
)

// With no TMDB agent the season list reports "not configured" — the seasons
// endpoint turns that into a 503 rather than a TMDB failure.
func TestRequestsTMDBAdapter_GetTVSeasonsWithoutAgent(t *testing.T) {
	a := &requestsTMDBAdapter{agentFn: func() metadata.Agent { return nil }}
	list, err := a.GetTVSeasons(context.Background(), 1399)
	if list != nil || !errors.Is(err, v1.ErrSeasonsTMDBUnavailable) || !errors.Is(err, errTMDBUnavailable) {
		t.Errorf("GetTVSeasons = (%v, %v), want the not-configured error", list, err)
	}
}
