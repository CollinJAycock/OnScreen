package main

import (
	"context"

	"github.com/onscreen/onscreen/internal/domain/settings"
	"github.com/onscreen/onscreen/internal/scrobble"
)

// scrobbleAppsAdapter implements scrobble.AppCredentials on top of the
// settings service, so the operator's Last.fm / Trakt credentials are read
// per call and an admin's change applies without a restart.
type scrobbleAppsAdapter struct{ svc *settings.Service }

func (a scrobbleAppsAdapter) LastFM(ctx context.Context) scrobble.LastFMApp {
	c := a.svc.LastFM(ctx)
	return scrobble.LastFMApp{APIKey: c.APIKey, SharedSecret: c.SharedSecret}
}

func (a scrobbleAppsAdapter) Trakt(ctx context.Context) scrobble.TraktApp {
	c := a.svc.Trakt(ctx)
	return scrobble.TraktApp{ClientID: c.ClientID, ClientSecret: c.ClientSecret}
}
