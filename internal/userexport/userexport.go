// Package userexport builds the "Download my data" archive: a ZIP of JSON
// files, plus a README.txt describing them, holding what the server keeps
// about one user. It backs POST /api/v1/users/me/export (the user's own) and
// POST /api/v1/users/{id}/export (an admin answering a data request).
//
// What never goes in: the password hash, the PIN, the TOTP secret and
// recovery codes, refresh-token hashes, API key hashes and scrobble-service
// tokens. The queries (internal/db/queries/user_export.sql) name their
// columns, so a column added later can't leak in by accident.
//
// Items are written as references — title, place in a show or album, and
// external ids (TMDB, TVDB, IMDb, MusicBrainz, AniList) — so the export
// means something on another server. An item the exporting viewer can't see
// now (removed, or outside their library access, rating ceiling or label
// restriction) is written as its id with "unavailable": true.
package userexport

import (
	"archive/zip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/netip"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onscreen/onscreen/internal/db/gen"
)

// FormatVersion is written to manifest.json; bump it on an incompatible
// change to the archive layout.
const FormatVersion = 1

const (
	// MaxWatchEvents caps watch_history.json at the newest N play events.
	// The manifest says when the cap was hit.
	MaxWatchEvents = 100_000
	// MaxNotifications caps notifications.json at the newest N.
	MaxNotifications = 1000
	eventPage        = 5000
	refChunk         = 5000
)

// Store is the subset of *gen.Queries the export reads.
type Store interface {
	ExportUserProfile(ctx context.Context, id uuid.UUID) (gen.ExportUserProfileRow, error)
	ExportUserSessions(ctx context.Context, userID uuid.UUID) ([]gen.ExportUserSessionsRow, error)
	ListAPIKeysForUser(ctx context.Context, userID uuid.UUID) ([]gen.ListAPIKeysForUserRow, error)
	ExportUserScrobble(ctx context.Context, userID uuid.UUID) (gen.ExportUserScrobbleRow, error)
	ExportWatchEvents(ctx context.Context, arg gen.ExportWatchEventsParams) ([]gen.ExportWatchEventsRow, error)
	ExportWatchProgress(ctx context.Context, userID uuid.UUID) ([]gen.ExportWatchProgressRow, error)
	ExportWatchStatus(ctx context.Context, userID uuid.UUID) ([]gen.ExportWatchStatusRow, error)
	ExportRatings(ctx context.Context, userID uuid.UUID) ([]gen.ExportRatingsRow, error)
	ExportFavorites(ctx context.Context, userID uuid.UUID) ([]gen.ExportFavoritesRow, error)
	ExportReadingProgress(ctx context.Context, userID uuid.UUID) ([]gen.ExportReadingProgressRow, error)
	ExportAudiobookRates(ctx context.Context, userID uuid.UUID) ([]gen.ExportAudiobookRatesRow, error)
	ExportAudiobookBookmarks(ctx context.Context, userID uuid.UUID) ([]gen.ExportAudiobookBookmarksRow, error)
	ExportUserCollections(ctx context.Context, userID uuid.UUID) ([]gen.ExportUserCollectionsRow, error)
	ExportUserCollectionItems(ctx context.Context, userID uuid.UUID) ([]gen.ExportUserCollectionItemsRow, error)
	ExportMediaRequests(ctx context.Context, userID uuid.UUID) ([]gen.ExportMediaRequestsRow, error)
	ExportMediaIssues(ctx context.Context, userID uuid.UUID) ([]gen.ExportMediaIssuesRow, error)
	ExportNotifications(ctx context.Context, arg gen.ExportNotificationsParams) ([]gen.ExportNotificationsRow, error)
	ExportItemRefs(ctx context.Context, arg gen.ExportItemRefsParams) ([]gen.ExportItemRefsRow, error)
}

// Visibility is the exporting viewer's item visibility: their library grant
// set (nil = every library), rating ceiling and label restriction.
type Visibility struct {
	LibraryIDs    []uuid.UUID
	MaxRatingRank *int32
	BlockedLabels []string
	AllowedLabels []string
}

// Exporter writes one user's archive.
type Exporter struct {
	st   Store
	vis  Visibility
	refs map[uuid.UUID]*ItemRef
	now  func() time.Time
}

// New returns an Exporter reading st, resolving items under vis.
func New(st Store, vis Visibility) *Exporter {
	return &Exporter{st: st, vis: vis, refs: map[uuid.UUID]*ItemRef{}, now: time.Now}
}

// Collect reads everything except the watch history, which Write pages
// through while streaming. It is split out so a caller can fail with a
// proper error status before the first byte of the archive is written.
func (e *Exporter) Collect(ctx context.Context, userID uuid.UUID) (*Snapshot, error) {
	s := &Snapshot{userID: userID}
	var err error
	if s.profile, err = e.st.ExportUserProfile(ctx, userID); err != nil {
		return nil, fmt.Errorf("profile: %w", err)
	}
	if s.sessions, err = e.st.ExportUserSessions(ctx, userID); err != nil {
		return nil, fmt.Errorf("sessions: %w", err)
	}
	if s.keys, err = e.st.ListAPIKeysForUser(ctx, userID); err != nil {
		return nil, fmt.Errorf("api keys: %w", err)
	}
	if sc, err := e.st.ExportUserScrobble(ctx, userID); err == nil {
		s.scrobble = &sc
	} else if !isNoRows(err) {
		return nil, fmt.Errorf("scrobble: %w", err)
	}
	if s.progress, err = e.st.ExportWatchProgress(ctx, userID); err != nil {
		return nil, fmt.Errorf("watch progress: %w", err)
	}
	if s.status, err = e.st.ExportWatchStatus(ctx, userID); err != nil {
		return nil, fmt.Errorf("watch status: %w", err)
	}
	if s.ratings, err = e.st.ExportRatings(ctx, userID); err != nil {
		return nil, fmt.Errorf("ratings: %w", err)
	}
	if s.favorites, err = e.st.ExportFavorites(ctx, userID); err != nil {
		return nil, fmt.Errorf("favorites: %w", err)
	}
	if s.reading, err = e.st.ExportReadingProgress(ctx, userID); err != nil {
		return nil, fmt.Errorf("reading progress: %w", err)
	}
	if s.abRates, err = e.st.ExportAudiobookRates(ctx, userID); err != nil {
		return nil, fmt.Errorf("audiobook rates: %w", err)
	}
	if s.abMarks, err = e.st.ExportAudiobookBookmarks(ctx, userID); err != nil {
		return nil, fmt.Errorf("audiobook bookmarks: %w", err)
	}
	if s.collections, err = e.st.ExportUserCollections(ctx, userID); err != nil {
		return nil, fmt.Errorf("collections: %w", err)
	}
	if s.collItems, err = e.st.ExportUserCollectionItems(ctx, userID); err != nil {
		return nil, fmt.Errorf("collection items: %w", err)
	}
	if s.requests, err = e.st.ExportMediaRequests(ctx, userID); err != nil {
		return nil, fmt.Errorf("requests: %w", err)
	}
	if s.issues, err = e.st.ExportMediaIssues(ctx, userID); err != nil {
		return nil, fmt.Errorf("issues: %w", err)
	}
	if s.notifications, err = e.st.ExportNotifications(ctx, gen.ExportNotificationsParams{UserID: userID, Lim: MaxNotifications}); err != nil {
		return nil, fmt.Errorf("notifications: %w", err)
	}
	// First page of history up front too: it is the query most likely to be
	// slow, and an error here can still become a clean 500.
	if s.firstEvents, err = e.watchPage(ctx, userID, nil); err != nil {
		return nil, fmt.Errorf("watch history: %w", err)
	}

	var ids []uuid.UUID
	for _, r := range s.progress {
		ids = append(ids, r.MediaID)
	}
	for _, r := range s.status {
		ids = append(ids, r.MediaItemID)
	}
	for _, r := range s.ratings {
		ids = append(ids, r.MediaItemID)
	}
	for _, r := range s.favorites {
		ids = append(ids, r.MediaID)
	}
	for _, r := range s.reading {
		ids = append(ids, r.MediaID)
	}
	for _, r := range s.abRates {
		ids = append(ids, r.BookID)
	}
	for _, r := range s.abMarks {
		ids = append(ids, r.BookID, r.ItemID)
	}
	for _, r := range s.collItems {
		ids = append(ids, r.MediaItemID)
	}
	for _, r := range s.requests {
		if r.FulfilledItemID.Valid {
			ids = append(ids, uuid.UUID(r.FulfilledItemID.Bytes))
		}
	}
	for _, r := range s.issues {
		ids = append(ids, r.ItemID)
	}
	for _, r := range s.notifications {
		if r.ItemID.Valid {
			ids = append(ids, uuid.UUID(r.ItemID.Bytes))
		}
	}
	for _, r := range s.firstEvents {
		ids = append(ids, r.MediaID)
	}
	if err := e.resolve(ctx, ids); err != nil {
		return nil, fmt.Errorf("item references: %w", err)
	}
	return s, nil
}

func (e *Exporter) watchPage(ctx context.Context, userID uuid.UUID, after *gen.ExportWatchEventsRow) ([]gen.ExportWatchEventsRow, error) {
	arg := gen.ExportWatchEventsParams{UserID: userID, Lim: eventPage}
	if after != nil {
		arg.BeforeAt = after.OccurredAt
		arg.BeforeID = after.ID
	}
	return e.st.ExportWatchEvents(ctx, arg)
}

// resolve looks up the references for ids not resolved yet.
func (e *Exporter) resolve(ctx context.Context, ids []uuid.UUID) error {
	var todo []uuid.UUID
	seen := map[uuid.UUID]struct{}{}
	for _, id := range ids {
		if _, ok := e.refs[id]; ok {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		todo = append(todo, id)
	}
	for start := 0; start < len(todo); start += refChunk {
		chunk := todo[start:min(start+refChunk, len(todo))]
		rows, err := e.st.ExportItemRefs(ctx, gen.ExportItemRefsParams{
			Ids:           chunk,
			LibraryIds:    e.vis.LibraryIDs,
			MaxRatingRank: e.vis.MaxRatingRank,
			BlockedLabels: e.vis.BlockedLabels,
			AllowedLabels: e.vis.AllowedLabels,
		})
		if err != nil {
			return err
		}
		for _, r := range rows {
			e.refs[r.ID] = refFromRow(r)
		}
		for _, id := range chunk {
			if _, ok := e.refs[id]; !ok {
				// Hard-deleted since.
				e.refs[id] = &ItemRef{ID: id.String(), Unavailable: true}
			}
		}
	}
	return nil
}

func (e *Exporter) ref(id uuid.UUID) *ItemRef {
	if r, ok := e.refs[id]; ok {
		return r
	}
	return &ItemRef{ID: id.String(), Unavailable: true}
}

func (e *Exporter) refPG(id pgtype.UUID) *ItemRef {
	if !id.Valid {
		return nil
	}
	return e.ref(uuid.UUID(id.Bytes))
}

// Snapshot is what Collect read.
type Snapshot struct {
	userID        uuid.UUID
	profile       gen.ExportUserProfileRow
	sessions      []gen.ExportUserSessionsRow
	keys          []gen.ListAPIKeysForUserRow
	scrobble      *gen.ExportUserScrobbleRow
	progress      []gen.ExportWatchProgressRow
	status        []gen.ExportWatchStatusRow
	ratings       []gen.ExportRatingsRow
	favorites     []gen.ExportFavoritesRow
	reading       []gen.ExportReadingProgressRow
	abRates       []gen.ExportAudiobookRatesRow
	abMarks       []gen.ExportAudiobookBookmarksRow
	collections   []gen.ExportUserCollectionsRow
	collItems     []gen.ExportUserCollectionItemsRow
	requests      []gen.ExportMediaRequestsRow
	issues        []gen.ExportMediaIssuesRow
	notifications []gen.ExportNotificationsRow
	firstEvents   []gen.ExportWatchEventsRow
}

// Username is the exported account's username (for the download's name).
func (s *Snapshot) Username() string { return s.profile.Username }

// Write streams the archive for s to w. The watch history is paged from the
// store as it is written; an error mid-way leaves w holding a truncated
// archive, which the caller must treat as failed (abort the response).
func (e *Exporter) Write(ctx context.Context, w io.Writer, s *Snapshot) error {
	zw := zip.NewWriter(w)
	now := e.now().UTC()
	counts := map[string]int{}

	put := func(name string, v any) error {
		f, err := zw.CreateHeader(&zip.FileHeader{Name: name, Method: zip.Deflate, Modified: now})
		if err != nil {
			return err
		}
		enc := json.NewEncoder(f)
		enc.SetIndent("", "  ")
		return enc.Encode(v)
	}

	files := []struct {
		name  string
		build func() (any, int)
	}{
		{"profile.json", func() (any, int) { return e.profile(s), 1 }},
		{"sessions.json", func() (any, int) { return e.sessions(s), len(s.sessions) }},
		{"api_keys.json", func() (any, int) { return e.apiKeys(s), len(s.keys) }},
		{"scrobble.json", func() (any, int) { return e.scrobble(s), 1 }},
		{"watch_progress.json", func() (any, int) { return e.watchProgress(s), len(s.progress) }},
		{"watch_status.json", func() (any, int) { return e.watchStatus(s), len(s.status) }},
		{"ratings.json", func() (any, int) { return e.ratings(s), len(s.ratings) }},
		{"favorites.json", func() (any, int) { return e.favorites(s), len(s.favorites) }},
		{"reading_progress.json", func() (any, int) { return e.readingProgress(s), len(s.reading) }},
		{"audiobooks.json", func() (any, int) { return e.audiobooks(s), len(s.abRates) + len(s.abMarks) }},
		{"playlists.json", func() (any, int) { v, n := e.collectionsOf(s, true); return v, n }},
		{"collections.json", func() (any, int) { v, n := e.collectionsOf(s, false); return v, n }},
		{"requests.json", func() (any, int) { return e.requests(s), len(s.requests) }},
		{"issues.json", func() (any, int) { return e.issues(s), len(s.issues) }},
		{"notifications.json", func() (any, int) { return e.notifications(s), len(s.notifications) }},
	}
	if err := writeReadme(zw, now); err != nil {
		return err
	}
	for _, f := range files {
		v, n := f.build()
		counts[f.name] = n
		if err := put(f.name, v); err != nil {
			return err
		}
	}

	n, truncated, err := e.writeHistory(ctx, zw, now, s)
	if err != nil {
		return err
	}
	counts["watch_history.json"] = n

	if err := put("manifest.json", map[string]any{
		"format_version": FormatVersion,
		"generated_at":   now.Format(time.RFC3339),
		"user_id":        s.userID.String(),
		"username":       s.profile.Username,
		"counts":         counts,
		"limits": map[string]any{
			"watch_history_max":          MaxWatchEvents,
			"watch_history_truncated":    truncated,
			"notifications_max":          MaxNotifications,
			"notifications_maybe_capped": len(s.notifications) >= MaxNotifications,
		},
	}); err != nil {
		return err
	}
	return zw.Close()
}

// writeHistory streams watch_history.json as a JSON array, newest first,
// paging the store and resolving each page's items as it goes.
func (e *Exporter) writeHistory(ctx context.Context, zw *zip.Writer, now time.Time, s *Snapshot) (int, bool, error) {
	f, err := zw.CreateHeader(&zip.FileHeader{Name: "watch_history.json", Method: zip.Deflate, Modified: now})
	if err != nil {
		return 0, false, err
	}
	if _, err := io.WriteString(f, "[\n"); err != nil {
		return 0, false, err
	}
	page := s.firstEvents
	written := 0
	truncated := false
	for len(page) > 0 {
		if err := e.resolve(ctx, eventItemIDs(page)); err != nil {
			return written, false, err
		}
		for i := range page {
			if written >= MaxWatchEvents {
				truncated = true
				break
			}
			b, err := json.Marshal(e.event(page[i]))
			if err != nil {
				return written, false, err
			}
			sep := "  "
			if written > 0 {
				sep = ",\n  "
			}
			if _, err := io.WriteString(f, sep); err != nil {
				return written, false, err
			}
			if _, err := f.Write(b); err != nil {
				return written, false, err
			}
			written++
		}
		if truncated || len(page) < eventPage {
			break
		}
		last := page[len(page)-1]
		if page, err = e.watchPage(ctx, s.userID, &last); err != nil {
			return written, false, err
		}
		if written >= MaxWatchEvents && len(page) > 0 {
			truncated = true
			break
		}
	}
	if _, err := io.WriteString(f, "\n]\n"); err != nil {
		return written, false, err
	}
	return written, truncated, nil
}

func eventItemIDs(rows []gen.ExportWatchEventsRow) []uuid.UUID {
	out := make([]uuid.UUID, len(rows))
	for i, r := range rows {
		out[i] = r.MediaID
	}
	return out
}

// ── shapes ───────────────────────────────────────────────────────────────

// ItemRef identifies a library item outside this server.
type ItemRef struct {
	ID          string            `json:"id"`
	Type        string            `json:"type,omitempty"`
	Title       string            `json:"title,omitempty"`
	Year        *int32            `json:"year,omitempty"`
	Parent      string            `json:"parent_title,omitempty"`
	Grandparent string            `json:"grandparent_title,omitempty"`
	Season      *int32            `json:"season,omitempty"`
	Episode     *int32            `json:"episode,omitempty"`
	Track       *int32            `json:"track,omitempty"`
	ExternalIDs map[string]string `json:"external_ids,omitempty"`
	// Unavailable: removed from the server, or hidden from the viewer the
	// export was made for. Only the id is given.
	Unavailable bool `json:"unavailable,omitempty"`
}

func refFromRow(r gen.ExportItemRefsRow) *ItemRef {
	if !r.Visible {
		return &ItemRef{ID: r.ID.String(), Unavailable: true}
	}
	ref := &ItemRef{ID: r.ID.String(), Type: r.Type, Title: r.Title, Year: r.Year}
	if r.ParentTitle != nil {
		ref.Parent = *r.ParentTitle
	}
	if r.GrandparentTitle != nil {
		ref.Grandparent = *r.GrandparentTitle
	}
	switch r.Type {
	case "episode":
		ref.Season, ref.Episode = r.ParentIndex, r.Index
	case "track":
		ref.Track = r.Index
	}
	ids := map[string]string{}
	setInt := func(k string, v *int32) {
		if v != nil && *v != 0 {
			ids[k] = fmt.Sprint(*v)
		}
	}
	setStr := func(k string, v *string) {
		if v != nil && *v != "" {
			ids[k] = *v
		}
	}
	setInt("tmdb", r.TmdbID)
	setInt("tvdb", r.TvdbID)
	setStr("imdb", r.ImdbID)
	setInt("anilist", r.AnilistID)
	if r.MusicbrainzID.Valid {
		ids["musicbrainz"] = uuid.UUID(r.MusicbrainzID.Bytes).String()
	}
	if r.Type == "episode" {
		setInt("show_tmdb", r.GrandparentTmdbID)
		setInt("show_tvdb", r.GrandparentTvdbID)
		setStr("show_imdb", r.GrandparentImdbID)
	}
	if len(ids) > 0 {
		ref.ExternalIDs = ids
	}
	return ref
}

func ts(t pgtype.Timestamptz) *string {
	if !t.Valid {
		return nil
	}
	s := t.Time.UTC().Format(time.RFC3339)
	return &s
}

func addr(a *netip.Addr) string {
	if a == nil || !a.IsValid() {
		return ""
	}
	return a.String()
}

func rawJSON(b []byte) json.RawMessage {
	if len(b) == 0 || !json.Valid(b) {
		return nil
	}
	return json.RawMessage(b)
}

func (e *Exporter) profile(s *Snapshot) any {
	p := s.profile
	var parent *string
	if p.ParentUserID.Valid {
		v := uuid.UUID(p.ParentUserID.Bytes).String()
		parent = &v
	}
	return map[string]any{
		"id":                      p.ID.String(),
		"username":                p.Username,
		"email":                   p.Email,
		"is_admin":                p.IsAdmin,
		"created_at":              ts(p.CreatedAt),
		"updated_at":              ts(p.UpdatedAt),
		"managed_profile_of":      parent,
		"avatar_url":              p.AvatarUrl,
		"google_avatar_url":       p.GoogleAvatarUrl,
		"has_password":            p.HasPassword,
		"has_pin":                 p.HasPin,
		"two_factor_enabled":      p.TotpEnabled,
		"linked_sign_in":          map[string]bool{"google": p.GoogleLinked, "github": p.GithubLinked, "discord": p.DiscordLinked, "oidc": p.OidcLinked, "saml": p.SamlLinked, "ldap": p.LdapLinked},
		"inherits_library_access": p.InheritLibraryAccess,
		"preferences": map[string]any{
			"preferred_audio_lang":    p.PreferredAudioLang,
			"preferred_subtitle_lang": p.PreferredSubtitleLang,
			"forced_subtitles_only":   p.ForcedSubtitlesOnly,
			"max_video_bitrate_kbps":  p.MaxVideoBitrateKbps,
			"max_audio_bitrate_kbps":  p.MaxAudioBitrateKbps,
			"max_video_height":        p.MaxVideoHeight,
			"preferred_video_codec":   p.PreferredVideoCodec,
			"episode_use_show_poster": p.EpisodeUseShowPoster,
			"home_layout":             rawJSON(p.HubLayout),
		},
		"restrictions": map[string]any{
			"max_content_rating":             p.MaxContentRating,
			"blocked_labels":                 p.BlockedLabels,
			"allowed_labels":                 p.AllowedLabels,
			"max_concurrent_streams":         p.MaxConcurrentStreams,
			"max_stream_bitrate_kbps":        p.MaxStreamBitrateKbps,
			"max_remote_stream_bitrate_kbps": p.MaxRemoteStreamBitrateKbps,
		},
		"requests": map[string]any{
			"can_request":         p.CanRequest,
			"quota_movies":        p.RequestQuotaMovies,
			"quota_tv":            p.RequestQuotaTv,
			"auto_approve_movies": p.AutoApproveMovies,
			"auto_approve_tv":     p.AutoApproveTv,
		},
	}
}

func (e *Exporter) sessions(s *Snapshot) any {
	out := make([]map[string]any, 0, len(s.sessions))
	for _, r := range s.sessions {
		out = append(out, map[string]any{
			"id": r.ID.String(), "device": r.ClientName, "platform": r.Platform,
			"device_id": r.DeviceID, "ip": addr(r.IpAddr),
			"created_at": ts(r.CreatedAt), "last_used_at": ts(r.LastSeen), "expires_at": ts(r.ExpiresAt),
		})
	}
	return out
}

func (e *Exporter) apiKeys(s *Snapshot) any {
	out := make([]map[string]any, 0, len(s.keys))
	for _, k := range s.keys {
		out = append(out, map[string]any{
			"id": k.ID.String(), "name": k.Name, "prefix": k.KeyPrefix, "scope": k.Scope,
			"created_at": ts(k.CreatedAt), "last_used_at": ts(k.LastUsedAt), "expires_at": ts(k.ExpiresAt),
		})
	}
	return out
}

func (e *Exporter) scrobble(s *Snapshot) any {
	if s.scrobble == nil {
		return map[string]any{"listenbrainz": map[string]any{"linked": false}, "lastfm": map[string]any{"linked": false}, "trakt": map[string]any{"linked": false}}
	}
	sc := s.scrobble
	return map[string]any{
		"listenbrainz": map[string]any{"linked": sc.ListenbrainzLinked, "enabled": sc.ListenbrainzEnabled},
		"lastfm":       map[string]any{"linked": sc.LastfmLinked, "username": sc.LastfmUsername},
		"trakt":        map[string]any{"linked": sc.TraktLinked, "username": sc.TraktUsername},
		"updated_at":   ts(sc.UpdatedAt),
	}
}

func (e *Exporter) event(r gen.ExportWatchEventsRow) any {
	return map[string]any{
		"id": r.ID.String(), "item": e.ref(r.MediaID), "event": r.EventType,
		"position_ms": r.PositionMs, "duration_ms": r.DurationMs,
		"client": r.ClientName, "ip": addr(r.ClientIp), "playback": r.Decision,
		"at": ts(r.OccurredAt),
	}
}

func (e *Exporter) watchProgress(s *Snapshot) any {
	out := make([]map[string]any, 0, len(s.progress))
	for _, r := range s.progress {
		out = append(out, map[string]any{
			"item": e.ref(r.MediaID), "position_ms": r.PositionMs, "duration_ms": r.DurationMs,
			"last_played_at": ts(r.LastEventAt), "last_client": r.LastClientName,
			"last_completed_at": ts(r.LastCompletedAt),
			"mark":              r.MarkState, "marked_at": ts(r.MarkedAt),
		})
	}
	return out
}

func (e *Exporter) watchStatus(s *Snapshot) any {
	out := make([]map[string]any, 0, len(s.status))
	for _, r := range s.status {
		out = append(out, map[string]any{
			"item": e.ref(r.MediaItemID), "status": r.Status,
			"created_at": ts(r.CreatedAt), "updated_at": ts(r.UpdatedAt),
		})
	}
	return out
}

func (e *Exporter) ratings(s *Snapshot) any {
	out := make([]map[string]any, 0, len(s.ratings))
	for _, r := range s.ratings {
		out = append(out, map[string]any{
			"item": e.ref(r.MediaItemID), "score": r.Score,
			"created_at": ts(r.CreatedAt), "updated_at": ts(r.UpdatedAt),
		})
	}
	return out
}

func (e *Exporter) favorites(s *Snapshot) any {
	out := make([]map[string]any, 0, len(s.favorites))
	for _, r := range s.favorites {
		out = append(out, map[string]any{"item": e.ref(r.MediaID), "added_at": ts(r.CreatedAt)})
	}
	return out
}

func (e *Exporter) readingProgress(s *Snapshot) any {
	out := make([]map[string]any, 0, len(s.reading))
	for _, r := range s.reading {
		out = append(out, map[string]any{
			"item": e.ref(r.MediaID), "locator": r.Locator, "progression": r.Progression,
			"device": r.DeviceName, "client_updated_at": ts(r.ClientUpdatedAt), "updated_at": ts(r.UpdatedAt),
		})
	}
	return out
}

func (e *Exporter) audiobooks(s *Snapshot) any {
	rates := make([]map[string]any, 0, len(s.abRates))
	for _, r := range s.abRates {
		rates = append(rates, map[string]any{"book": e.ref(r.BookID), "playback_rate": r.PlaybackRate, "updated_at": ts(r.UpdatedAt)})
	}
	marks := make([]map[string]any, 0, len(s.abMarks))
	for _, r := range s.abMarks {
		marks = append(marks, map[string]any{
			"id": r.ID.String(), "book": e.ref(r.BookID), "chapter": e.ref(r.ItemID),
			"position_ms": r.PositionMs, "note": r.Note, "created_at": ts(r.CreatedAt),
		})
	}
	return map[string]any{"playback_rates": rates, "bookmarks": marks}
}

func (e *Exporter) collectionsOf(s *Snapshot, playlists bool) (any, int) {
	items := map[uuid.UUID][]map[string]any{}
	for _, it := range s.collItems {
		items[it.CollectionID] = append(items[it.CollectionID], map[string]any{
			"position": it.Position, "item": e.ref(it.MediaItemID), "added_at": ts(it.AddedAt),
		})
	}
	out := []map[string]any{}
	for _, c := range s.collections {
		isPlaylist := c.Type == "playlist" || c.Type == "smart_playlist"
		if isPlaylist != playlists {
			continue
		}
		entry := map[string]any{
			"id": c.ID.String(), "name": c.Name, "description": c.Description, "type": c.Type,
			"created_at": ts(c.CreatedAt), "updated_at": ts(c.UpdatedAt),
		}
		if r := rawJSON(c.Rules); r != nil {
			entry["rules"] = r
		}
		if list := items[c.ID]; list != nil {
			entry["items"] = list
		} else {
			entry["items"] = []map[string]any{}
		}
		out = append(out, entry)
	}
	return out, len(out)
}

func (e *Exporter) requests(s *Snapshot) any {
	out := make([]map[string]any, 0, len(s.requests))
	for _, r := range s.requests {
		out = append(out, map[string]any{
			"id": r.ID.String(), "type": r.Type, "tmdb_id": r.TmdbID, "title": r.Title, "year": r.Year,
			"status": r.Status, "seasons": rawJSON(r.Seasons), "decline_reason": r.DeclineReason,
			"decided_at": ts(r.DecidedAt), "fulfilled_item": e.refPG(r.FulfilledItemID),
			"fulfilled_at": ts(r.FulfilledAt), "auto_approved": r.AutoApproved,
			"created_at": ts(r.CreatedAt), "updated_at": ts(r.UpdatedAt),
		})
	}
	return out
}

func (e *Exporter) issues(s *Snapshot) any {
	out := make([]map[string]any, 0, len(s.issues))
	for _, r := range s.issues {
		out = append(out, map[string]any{
			"id": r.ID.String(), "item": e.ref(r.ItemID), "kind": r.Kind, "note": r.Note,
			"status": r.Status, "created_at": ts(r.CreatedAt), "resolved_at": ts(r.ResolvedAt),
			"resolution_note": r.ResolutionNote,
		})
	}
	return out
}

func (e *Exporter) notifications(s *Snapshot) any {
	inbox := make([]map[string]any, 0, len(s.notifications))
	for _, r := range s.notifications {
		inbox = append(inbox, map[string]any{
			"id": r.ID.String(), "type": r.Type, "title": r.Title, "body": r.Body,
			"item": e.refPG(r.ItemID), "read": r.Read, "created_at": ts(r.CreatedAt),
		})
	}
	return map[string]any{
		// OnScreen keeps no per-user notification preferences: where
		// notifications go is the email address in profile.json plus the
		// server's own (admin) notification agents.
		"settings": map[string]any{"email": s.profile.Email},
		"inbox":    inbox,
	}
}
