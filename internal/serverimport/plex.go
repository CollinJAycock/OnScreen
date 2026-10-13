package serverimport

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// plexOwnerAccount is the server owner's account ID on every Plex server. A
// token's per-item state (viewCount, viewOffset, userRating, playlists) is its
// account's; an import uses the owner's token, so that's the owner's.
const plexOwnerAccount = "1"

// plexPageSize is how many items one library request asks for (a var so
// tests can page small lists).
var plexPageSize = 500

// Plex library item types (the type= filter of /library/sections/{id}/all).
const (
	plexTypeMovie   = 1
	plexTypeShow    = 2
	plexTypeEpisode = 4
	plexTypeTrack   = 10
)

type plexSource struct {
	base  string
	token string
	hc    *http.Client
}

// NewPlex reads a Plex Media Server at base with the owner's X-Plex-Token.
func NewPlex(base, token string) Source {
	return &plexSource{base: base, token: token, hc: newHTTPClient()}
}

func (p *plexSource) get(ctx context.Context, path string, q url.Values, out any) error {
	u := p.base + path
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	return getJSON(ctx, p.hc, u, map[string]string{
		"X-Plex-Token":             p.token,
		"X-Plex-Product":           "OnScreen import",
		"X-Plex-Client-Identifier": "onscreen-import",
	}, out)
}

type plexRoot struct {
	MediaContainer struct {
		FriendlyName      string `json:"friendlyName"`
		MachineIdentifier string `json:"machineIdentifier"`
		Version           string `json:"version"`
	} `json:"MediaContainer"`
}

type plexAccounts struct {
	MediaContainer struct {
		Account []struct {
			ID   int    `json:"id"`
			Name string `json:"name"`
		} `json:"Account"`
	} `json:"MediaContainer"`
}

// Probe implements Source.
func (p *plexSource) Probe(ctx context.Context) (ServerInfo, []Account, error) {
	var root plexRoot
	if err := p.get(ctx, "/", nil, &root); err != nil {
		return ServerInfo{}, nil, err
	}
	mc := root.MediaContainer
	if mc.MachineIdentifier == "" {
		return ServerInfo{}, nil, &FetchError{Msg: "the server did not identify itself as Plex Media Server"}
	}
	info := ServerInfo{ID: mc.MachineIdentifier, Name: mc.FriendlyName, Version: mc.Version}

	var accts plexAccounts
	if err := p.get(ctx, "/accounts", nil, &accts); err != nil {
		if errors.Is(err, ErrUnauthorized) {
			// Only the owner may list accounts: a token that can read the
			// server but not this isn't the owner's.
			return ServerInfo{}, nil, &FetchError{Msg: "the token can read the server but isn't its owner's; sign in to Plex as the server owner to get the token"}
		}
		return ServerInfo{}, nil, err
	}
	var out []Account
	for _, a := range accts.MediaContainer.Account {
		if a.Name == "" {
			continue // the system account, which plays nothing
		}
		id := strconv.Itoa(a.ID)
		out = append(out, Account{ID: id, Name: a.Name, FullState: id == plexOwnerAccount})
	}
	return info, out, nil
}

type plexSections struct {
	MediaContainer struct {
		Directory []struct {
			Key   string `json:"key"`
			Type  string `json:"type"`
			Title string `json:"title"`
		} `json:"Directory"`
	} `json:"MediaContainer"`
}

type plexMeta struct {
	RatingKey string `json:"ratingKey"`
	Type      string `json:"type"`
	Title     string `json:"title"`
	GUID      string `json:"guid"`
	GUIDs     []struct {
		ID string `json:"id"`
	} `json:"Guid"`
	GrandparentRatingKey string   `json:"grandparentRatingKey"`
	ParentIndex          *int     `json:"parentIndex"`
	Index                *int     `json:"index"`
	Duration             int64    `json:"duration"`
	ViewCount            int      `json:"viewCount"`
	LastViewedAt         int64    `json:"lastViewedAt"`
	ViewOffset           int64    `json:"viewOffset"`
	UserRating           *float64 `json:"userRating"`
	Smart                bool     `json:"smart"`
	PlaylistType         string   `json:"playlistType"`
	Media                []struct {
		Part []struct {
			File string `json:"file"`
		} `json:"Part"`
	} `json:"Media"`
	// A show's folder(s).
	Location []struct {
		Path string `json:"path"`
	} `json:"Location"`
	// History entries.
	ViewedAt  int64 `json:"viewedAt"`
	AccountID int   `json:"accountID"`
}

type plexPage struct {
	MediaContainer struct {
		Size      int        `json:"size"`
		TotalSize int        `json:"totalSize"`
		Metadata  []plexMeta `json:"Metadata"`
	} `json:"MediaContainer"`
}

// pages calls fn with each page of a paged Plex list.
func (p *plexSource) pages(ctx context.Context, path string, q url.Values, fn func([]plexMeta) error) error {
	if q == nil {
		q = url.Values{}
	}
	for start := 0; ; start += plexPageSize {
		q.Set("X-Plex-Container-Start", strconv.Itoa(start))
		q.Set("X-Plex-Container-Size", strconv.Itoa(plexPageSize))
		var page plexPage
		if err := p.get(ctx, path, q, &page); err != nil {
			return err
		}
		md := page.MediaContainer.Metadata
		if err := fn(md); err != nil {
			return err
		}
		total := page.MediaContainer.TotalSize
		if len(md) < plexPageSize || (total > 0 && start+len(md) >= total) {
			return nil
		}
	}
}

// Fetch implements Source.
func (p *plexSource) Fetch(ctx context.Context, accounts []string, opts Options, progress func(string)) (*Snapshot, error) {
	info, accts, err := p.Probe(ctx)
	if err != nil {
		return nil, err
	}
	snap := newSnapshot(info, accts)
	mapped := map[string]bool{}
	others := false // an account other than the owner's is mapped
	for _, a := range accounts {
		mapped[a] = true
		others = others || a != plexOwnerAccount
	}
	owner := mapped[plexOwnerAccount]

	var sections plexSections
	if err := p.get(ctx, "/library/sections", nil, &sections); err != nil {
		return nil, err
	}
	shows := map[string]ExternalIDs{} // show ratingKey -> IDs
	wantTracks := opts.Ratings || opts.History || opts.Playlists
	for _, sec := range sections.MediaContainer.Directory {
		var types []int
		switch sec.Type {
		case "movie":
			types = []int{plexTypeMovie}
		case "show":
			types = []int{plexTypeShow, plexTypeEpisode}
		case "artist":
			if wantTracks {
				types = []int{plexTypeTrack}
			}
		}
		for _, t := range types {
			n := 0
			q := url.Values{"type": {strconv.Itoa(t)}, "includeGuids": {"1"}}
			err := p.pages(ctx, "/library/sections/"+url.PathEscape(sec.Key)+"/all", q, func(md []plexMeta) error {
				for i := range md {
					it := p.item(&md[i], shows)
					if it == nil {
						continue
					}
					snap.Items[it.Key] = it
					if it.Kind == ItemShow {
						shows[it.Key] = it.IDs
					}
					if owner {
						if st, ok := plexState(&md[i]); ok {
							snap.States[plexOwnerAccount] = append(snap.States[plexOwnerAccount], st)
						}
					}
				}
				n += len(md)
				progress(fmt.Sprintf("Reading %s: %s (%d items)", info.Name, sec.Title, n))
				return nil
			})
			if err != nil {
				return nil, err
			}
		}
	}

	// History: every account's plays, and how the accounts other than the
	// owner's are known to have watched anything.
	if opts.History || (opts.Watched && others) {
		n := 0
		err := p.pages(ctx, "/status/sessions/history/all", url.Values{"sort": {"viewedAt:desc"}}, func(md []plexMeta) error {
			for _, h := range md {
				acct := strconv.Itoa(h.AccountID)
				if h.RatingKey == "" || h.ViewedAt == 0 || !mapped[acct] {
					continue
				}
				snap.Plays = append(snap.Plays, Play{Account: acct, Key: h.RatingKey, At: time.Unix(h.ViewedAt, 0).UTC()})
			}
			n += len(md)
			progress(fmt.Sprintf("Reading %s: play history (%d plays)", info.Name, n))
			return nil
		})
		if err != nil {
			return nil, err
		}
	}

	if opts.Playlists && owner {
		progress(fmt.Sprintf("Reading %s: playlists", info.Name))
		var lists plexPage
		if err := p.get(ctx, "/playlists", nil, &lists); err != nil {
			return nil, err
		}
		for _, pl := range lists.MediaContainer.Metadata {
			if pl.PlaylistType == "photo" || pl.RatingKey == "" {
				continue
			}
			list := Playlist{ID: pl.RatingKey, Name: pl.Title}
			err := p.pages(ctx, "/playlists/"+url.PathEscape(pl.RatingKey)+"/items", nil, func(md []plexMeta) error {
				for _, m := range md {
					if m.RatingKey != "" {
						list.Keys = append(list.Keys, m.RatingKey)
					}
				}
				return nil
			})
			if err != nil {
				return nil, err
			}
			snap.Playlists[plexOwnerAccount] = append(snap.Playlists[plexOwnerAccount], list)
		}
	}
	return snap, nil
}

// item converts one library entry. shows supplies an episode's show IDs
// (shows are listed before their episodes).
func (p *plexSource) item(m *plexMeta, shows map[string]ExternalIDs) *Item {
	if m.RatingKey == "" {
		return nil
	}
	it := &Item{Key: m.RatingKey, Title: m.Title, DurationMS: m.Duration}
	switch m.Type {
	case "movie":
		it.Kind = ItemMovie
	case "show":
		it.Kind = ItemShow
	case "episode":
		it.Kind = ItemEpisode
	case "track":
		it.Kind = ItemTrack
	default:
		it.Kind = ItemVideo
	}
	for _, g := range m.GUIDs {
		parsePlexGUID(g.ID, &it.IDs)
	}
	legacyShow := parsePlexGUID(m.GUID, &it.IDs)
	if len(m.Media) > 0 && len(m.Media[0].Part) > 0 {
		it.Path = m.Media[0].Part[0].File
	} else if len(m.Location) > 0 {
		it.Path = m.Location[0].Path
	}
	if it.Kind == ItemEpisode {
		it.ShowIDs = shows[m.GrandparentRatingKey]
		if it.ShowIDs.empty() && legacyShow != 0 {
			// Legacy TheTVDB agent: the episode's GUID names its show.
			it.ShowIDs.TVDB = legacyShow
		}
		if m.ParentIndex != nil && m.Index != nil {
			it.Season, it.Episode, it.HasSE = *m.ParentIndex, *m.Index, true
		}
	}
	return it
}

// plexState is the token account's state for an item, if it has any.
func plexState(m *plexMeta) (State, bool) {
	st := State{Key: m.RatingKey, Played: m.ViewCount > 0, PositionMS: m.ViewOffset, Rating: m.UserRating}
	if m.LastViewedAt > 0 {
		st.LastViewedAt = time.Unix(m.LastViewedAt, 0).UTC()
	}
	return st, st.Played || st.PositionMS > 0 || st.Rating != nil
}

// parsePlexGUID records the provider ID in a Plex GUID: the new agents'
// "imdb://tt0111161", "tmdb://603" and "tvdb://81189", or the legacy agents'
// "com.plexapp.agents.imdb://tt0111161?lang=en" and kin. A legacy TheTVDB
// episode GUID ("…thetvdb://81189/1/1") names the show; its show ID is
// returned so the caller can file it as the show's. Others ("plex://…",
// "local://…") carry no external ID.
func parsePlexGUID(guid string, ids *ExternalIDs) (legacyShowTVDB int) {
	scheme, rest, ok := strings.Cut(guid, "://")
	if !ok {
		return 0
	}
	rest, _, _ = strings.Cut(rest, "?")
	scheme = strings.TrimPrefix(scheme, "com.plexapp.agents.")
	first, more, _ := strings.Cut(rest, "/")
	switch scheme {
	case "imdb":
		if strings.HasPrefix(first, "tt") {
			ids.IMDB = first
		}
	case "tmdb", "themoviedb":
		if n, err := strconv.Atoi(first); err == nil && n > 0 {
			ids.TMDB = n
		}
	case "tvdb", "thetvdb":
		if n, err := strconv.Atoi(first); err == nil && n > 0 {
			if more != "" {
				return n
			}
			ids.TVDB = n
		}
	}
	return 0
}
