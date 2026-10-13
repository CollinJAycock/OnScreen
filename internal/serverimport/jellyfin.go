package serverimport

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// jellyfinPageSize is how many items one request asks for.
const jellyfinPageSize = 500

// ticksPerMS converts Jellyfin/Emby ticks (100 ns) to milliseconds.
const ticksPerMS = 10_000

// jellyfinItemTypes are the item types whose state an import reads.
const jellyfinItemTypes = "Movie,Episode,Audio,MusicVideo,Video"

// jellyfinSource reads Jellyfin and Emby, whose APIs share these endpoints
// and shapes. They differ in how the API key is sent (Jellyfin 10.11 can
// turn off the old X-Emby-Token header, and 10.12 does by default) and in
// Emby's /emby path prefix.
type jellyfinSource struct {
	kind  Kind
	base  string
	token string
	hc    *http.Client
}

// NewJellyfin reads a Jellyfin server at base with an API key.
func NewJellyfin(base, token string) Source {
	return &jellyfinSource{kind: KindJellyfin, base: base, token: token, hc: newHTTPClient()}
}

// NewEmby reads an Emby server at base with an API key.
func NewEmby(base, token string) Source {
	return &jellyfinSource{kind: KindEmby, base: base, token: token, hc: newHTTPClient()}
}

func (j *jellyfinSource) get(ctx context.Context, path string, q url.Values, out any) error {
	u := j.base
	if j.kind == KindEmby {
		u += "/emby"
	}
	u += path
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	headers := map[string]string{}
	if j.kind == KindEmby {
		headers["X-Emby-Token"] = j.token
	} else {
		headers["Authorization"] = fmt.Sprintf(`MediaBrowser Client="OnScreen", Device="OnScreen import", DeviceId="onscreen-import", Version="1", Token="%s"`, j.token)
	}
	return getJSON(ctx, j.hc, u, headers, out)
}

type jellyfinSystemInfo struct {
	ServerName string `json:"ServerName"`
	Version    string `json:"Version"`
	ID         string `json:"Id"`
}

type jellyfinUser struct {
	ID   string `json:"Id"`
	Name string `json:"Name"`
}

// Probe implements Source. /System/Info and /Users both need an admin's key.
func (j *jellyfinSource) Probe(ctx context.Context) (ServerInfo, []Account, error) {
	var sys jellyfinSystemInfo
	if err := j.get(ctx, "/System/Info", nil, &sys); err != nil {
		return ServerInfo{}, nil, err
	}
	if sys.ID == "" {
		return ServerInfo{}, nil, &FetchError{Msg: fmt.Sprintf("the server did not identify itself as %s", j.kind.Label())}
	}
	var users []jellyfinUser
	if err := j.get(ctx, "/Users", nil, &users); err != nil {
		return ServerInfo{}, nil, err
	}
	accts := make([]Account, 0, len(users))
	for _, u := range users {
		if u.ID != "" {
			accts = append(accts, Account{ID: u.ID, Name: u.Name, FullState: true})
		}
	}
	return ServerInfo{ID: sys.ID, Name: sys.ServerName, Version: sys.Version}, accts, nil
}

type jellyfinItem struct {
	ID                string            `json:"Id"`
	Name              string            `json:"Name"`
	Type              string            `json:"Type"`
	Path              string            `json:"Path"`
	ProviderIDs       map[string]string `json:"ProviderIds"`
	SeriesID          string            `json:"SeriesId"`
	ParentIndexNumber *int              `json:"ParentIndexNumber"`
	IndexNumber       *int              `json:"IndexNumber"`
	RunTimeTicks      int64             `json:"RunTimeTicks"`
	UserData          *struct {
		PlaybackPositionTicks int64    `json:"PlaybackPositionTicks"`
		PlayCount             int      `json:"PlayCount"`
		IsFavorite            bool     `json:"IsFavorite"`
		Played                bool     `json:"Played"`
		LastPlayedDate        string   `json:"LastPlayedDate"`
		Rating                *float64 `json:"Rating"`
	} `json:"UserData"`
}

type jellyfinPage struct {
	Items            []jellyfinItem `json:"Items"`
	TotalRecordCount int            `json:"TotalRecordCount"`
}

// pages calls fn with each page of a paged item list.
func (j *jellyfinSource) pages(ctx context.Context, path string, q url.Values, fn func([]jellyfinItem)) error {
	q.Set("Limit", strconv.Itoa(jellyfinPageSize))
	q.Set("EnableImages", "false")
	for start := 0; ; start += jellyfinPageSize {
		q.Set("StartIndex", strconv.Itoa(start))
		var page jellyfinPage
		if err := j.get(ctx, path, q, &page); err != nil {
			return err
		}
		fn(page.Items)
		if len(page.Items) < jellyfinPageSize || start+len(page.Items) >= page.TotalRecordCount {
			return nil
		}
	}
}

// Fetch implements Source. Jellyfin and Emby keep no log of individual plays:
// a played item's history is one play at its last-played date.
func (j *jellyfinSource) Fetch(ctx context.Context, accounts []string, opts Options, progress func(string)) (*Snapshot, error) {
	info, accts, err := j.Probe(ctx)
	if err != nil {
		return nil, err
	}
	snap := newSnapshot(info, accts)
	names := map[string]string{}
	for _, a := range accts {
		names[a.ID] = a.Name
	}

	for _, uid := range accounts {
		name := names[uid]
		if name == "" {
			continue // not a user of this server
		}
		userPath := "/Users/" + url.PathEscape(uid) + "/Items"
		states := map[string]*State{}
		var order []string
		state := func(key string) *State {
			st, ok := states[key]
			if !ok {
				st = &State{Key: key}
				states[key] = st
				order = append(order, key)
			}
			return st
		}
		record := func(it jellyfinItem, item *Item) {
			ud := it.UserData
			if ud == nil {
				return
			}
			if !ud.Played && ud.PlaybackPositionTicks <= 0 && !ud.IsFavorite && ud.Rating == nil {
				return
			}
			st := state(item.Key)
			st.Played = st.Played || ud.Played
			st.PositionMS = ud.PlaybackPositionTicks / ticksPerMS
			st.Favorite = st.Favorite || ud.IsFavorite
			if ud.Rating != nil {
				st.Rating = ud.Rating
			}
			if t, err := time.Parse(time.RFC3339Nano, ud.LastPlayedDate); err == nil && t.Year() > 1 {
				st.LastViewedAt = t.UTC()
			}
		}

		// Shows first: an episode carries only its show's item ID.
		progress(fmt.Sprintf("Reading %s: %s's shows", info.Name, name))
		shows := map[string]ExternalIDs{}
		q := url.Values{"Recursive": {"true"}, "IncludeItemTypes": {"Series"}, "Fields": {"ProviderIds,Path"}, "EnableUserData": {"true"}}
		err := j.pages(ctx, userPath, q, func(items []jellyfinItem) {
			for _, it := range items {
				item := j.item(it, shows)
				shows[it.ID] = item.IDs
				snap.Items[item.Key] = item
				record(it, item)
			}
		})
		if err != nil {
			return nil, err
		}

		var filters []string
		if opts.Watched || opts.History {
			filters = append(filters, "IsPlayed")
		}
		if opts.Watched {
			filters = append(filters, "IsResumable")
		}
		if opts.Favorites {
			filters = append(filters, "IsFavorite")
		}
		for _, f := range filters {
			n := 0
			q := url.Values{"Recursive": {"true"}, "IncludeItemTypes": {jellyfinItemTypes}, "Filters": {f},
				"Fields": {"ProviderIds,Path"}, "EnableUserData": {"true"}}
			err := j.pages(ctx, userPath, q, func(items []jellyfinItem) {
				for _, it := range items {
					item := j.item(it, shows)
					snap.Items[item.Key] = item
					record(it, item)
				}
				n += len(items)
				progress(fmt.Sprintf("Reading %s: %s's %s items (%d)", info.Name, name, jellyfinFilterLabel(f), n))
			})
			if err != nil {
				return nil, err
			}
		}
		for _, key := range order {
			st := *states[key]
			snap.States[uid] = append(snap.States[uid], st)
			if opts.History && st.Played && !st.LastViewedAt.IsZero() {
				snap.Plays = append(snap.Plays, Play{Account: uid, Key: key, At: st.LastViewedAt})
			}
		}

		if opts.Playlists {
			progress(fmt.Sprintf("Reading %s: %s's playlists", info.Name, name))
			var lists []jellyfinItem
			q := url.Values{"Recursive": {"true"}, "IncludeItemTypes": {"Playlist"}}
			if err := j.pages(ctx, userPath, q, func(items []jellyfinItem) { lists = append(lists, items...) }); err != nil {
				return nil, err
			}
			for _, pl := range lists {
				list := Playlist{ID: pl.ID, Name: pl.Name}
				q := url.Values{"UserId": {uid}, "Fields": {"ProviderIds,Path"}}
				err := j.pages(ctx, "/Playlists/"+url.PathEscape(pl.ID)+"/Items", q, func(items []jellyfinItem) {
					for _, it := range items {
						item := j.item(it, shows)
						snap.Items[item.Key] = item
						list.Keys = append(list.Keys, item.Key)
					}
				})
				if err != nil {
					return nil, err
				}
				snap.Playlists[uid] = append(snap.Playlists[uid], list)
			}
		}
	}
	return snap, nil
}

func jellyfinFilterLabel(f string) string {
	switch f {
	case "IsPlayed":
		return "played"
	case "IsResumable":
		return "in-progress"
	case "IsFavorite":
		return "favorite"
	}
	return f
}

// item converts one item. shows supplies an episode's show IDs.
func (j *jellyfinSource) item(it jellyfinItem, shows map[string]ExternalIDs) *Item {
	item := &Item{Key: it.ID, Title: it.Name, Path: it.Path, DurationMS: it.RunTimeTicks / ticksPerMS}
	switch it.Type {
	case "Movie":
		item.Kind = ItemMovie
	case "Series":
		item.Kind = ItemShow
	case "Episode":
		item.Kind = ItemEpisode
	case "Audio":
		item.Kind = ItemTrack
	default:
		item.Kind = ItemVideo
	}
	for k, v := range it.ProviderIDs {
		switch strings.ToLower(k) {
		case "tmdb":
			if n, err := strconv.Atoi(v); err == nil && n > 0 {
				item.IDs.TMDB = n
			}
		case "tvdb":
			if n, err := strconv.Atoi(v); err == nil && n > 0 {
				item.IDs.TVDB = n
			}
		case "imdb":
			if strings.HasPrefix(v, "tt") {
				item.IDs.IMDB = v
			}
		}
	}
	if item.Kind == ItemEpisode {
		item.ShowIDs = shows[it.SeriesID]
		if it.ParentIndexNumber != nil && it.IndexNumber != nil {
			item.Season, item.Episode, item.HasSE = *it.ParentIndexNumber, *it.IndexNumber, true
		}
	}
	return item
}
