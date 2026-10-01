package v1

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"path/filepath"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/onscreen/onscreen/internal/api/middleware"
	"github.com/onscreen/onscreen/internal/api/respond"
	"github.com/onscreen/onscreen/internal/audit"
	"github.com/onscreen/onscreen/internal/auth"
	"github.com/onscreen/onscreen/internal/db/gen"
	"github.com/onscreen/onscreen/internal/domain/media"
	"github.com/onscreen/onscreen/internal/notification"
	"github.com/onscreen/onscreen/internal/streaming"
	"github.com/onscreen/onscreen/internal/transcode"
)

// sessionActivityTimeout is how long a transcode session can be inactive
// (no progress heartbeat) before being hidden from "Now Playing". Shares the
// same window as the per-user concurrency cap (transcode.CountByUser), so a
// session that's hidden here is also no longer counted against the cap — one
// definition of "live".
const sessionActivityTimeout = transcode.ActiveSessionWindow

// genericSessionClientName is the placeholder transcode Start stamps on every
// session it creates. A card borrows the player's self-reported device name
// from its progress heartbeat instead whenever one is available.
const genericSessionClientName = "OnScreenWeb"

type sessionItemQuerier interface {
	GetMediaItemsForSessions(ctx context.Context, ids []uuid.UUID) ([]gen.SessionMediaItem, error)
	GetMediaItemByFilePath(ctx context.Context, filePath string) (*gen.SessionMediaItem, error)
}

// sessionUserGetter resolves a stream's viewer for the card. Satisfied by
// *gen.Queries.
type sessionUserGetter interface {
	GetUser(ctx context.Context, id uuid.UUID) (gen.User, error)
}

// sessionFileGetter resolves a stream's source file for the card's
// source → output line. Satisfied by *media.Service.
type sessionFileGetter interface {
	GetFile(ctx context.Context, id uuid.UUID) (*media.File, error)
	GetFiles(ctx context.Context, itemID uuid.UUID) ([]media.File, error)
}

// SessionTerminator tears down a server-side (Valkey) playback session the
// way the transcode Stop route does. Satisfied by *NativeTranscodeHandler.
type SessionTerminator interface {
	TerminateSession(ctx context.Context, sess *transcode.Session)
}

// NativeSessionsHandler handles GET /api/v1/sessions and the admin
// POST /api/v1/sessions/{id}/stop.
type NativeSessionsHandler struct {
	store   *transcode.SessionStore
	tracker *streaming.Tracker
	db      sessionItemQuerier
	logger  *slog.Logger

	// Optional Now Playing enrichment + admin stop wiring (With* setters).
	users      sessionUserGetter
	files      sessionFileGetter
	decisions  *PlayDecisionRecorder
	stops      *PlaybackStopGuard
	terminator SessionTerminator
	broker     *notification.Broker
	audit      *audit.Logger
}

// NewNativeSessionsHandler creates a NativeSessionsHandler.
func NewNativeSessionsHandler(
	store *transcode.SessionStore,
	tracker *streaming.Tracker,
	db sessionItemQuerier,
	logger *slog.Logger,
) *NativeSessionsHandler {
	return &NativeSessionsHandler{store: store, tracker: tracker, db: db, logger: logger}
}

// WithUsers resolves each card's viewer (username, managed-profile name).
func (h *NativeSessionsHandler) WithUsers(u sessionUserGetter) *NativeSessionsHandler {
	h.users = u
	return h
}

// WithFiles resolves each card's source file (codec / resolution / bitrate).
func (h *NativeSessionsHandler) WithFiles(f sessionFileGetter) *NativeSessionsHandler {
	h.files = f
	return h
}

// WithPlayDecisions lets a direct-play card whose client didn't report a
// decision fall back to the server's own attribution (play_decision.go).
func (h *NativeSessionsHandler) WithPlayDecisions(p *PlayDecisionRecorder) *NativeSessionsHandler {
	h.decisions = p
	return h
}

// WithPlaybackStops wires the guard an admin stop records its refusal
// window in (the serving paths check the same guard).
func (h *NativeSessionsHandler) WithPlaybackStops(g *PlaybackStopGuard) *NativeSessionsHandler {
	h.stops = g
	return h
}

// WithTerminator wires the transcode teardown used for server-side sessions.
func (h *NativeSessionsHandler) WithTerminator(t SessionTerminator) *NativeSessionsHandler {
	h.terminator = t
	return h
}

// WithSyncBroker wires the SSE broker the playback.stop event goes out on.
func (h *NativeSessionsHandler) WithSyncBroker(b *notification.Broker) *NativeSessionsHandler {
	h.broker = b
	return h
}

// WithAudit records admin stops (transcode.stop, reason admin_terminated).
func (h *NativeSessionsHandler) WithAudit(a *audit.Logger) *NativeSessionsHandler {
	h.audit = a
	return h
}

type activeSession struct {
	ID          string  `json:"id"`
	Decision    string  `json:"decision"`
	PositionMS  int64   `json:"position_ms"`
	ClientName  string  `json:"client_name,omitempty"`
	StartedAt   string  `json:"started_at"` // RFC 3339, UTC
	Title       string  `json:"title"`
	Year        *int    `json:"year,omitempty"`
	Type        string  `json:"type,omitempty"`
	PosterPath  *string `json:"poster_path,omitempty"`
	ParentTitle *string `json:"parent_title,omitempty"` // show/artist for episodes/tracks
	DurationMS  *int64  `json:"duration_ms,omitempty"`
	BitrateKbps *int    `json:"bitrate_kbps,omitempty"`
	// SelectedRendition is the ABR rung the player's adaptive logic settled on
	// (e.g. "720p"). Empty for non-ABR sessions.
	SelectedRendition *string `json:"selected_rendition,omitempty"`

	// ── Who / where / why (additive) ────────────────────────────────────────
	// UserID + Username name the viewer. For a managed profile Username is
	// the owning account and ProfileName the profile.
	UserID      string `json:"user_id,omitempty"`
	Username    string `json:"username,omitempty"`
	ProfileName string `json:"profile_name,omitempty"`
	// ClientIP is the viewer's address — admins only. Location classifies it
	// "lan" or "remote" with the same rule the first-run setup gate uses.
	ClientIP string `json:"client_ip,omitempty"`
	Location string `json:"location,omitempty"`
	// Source is the file being played; Output what the viewer receives. A
	// direct play has no Output (it IS the source).
	Source *streamFormat `json:"source,omitempty"`
	Output *streamFormat `json:"output,omitempty"`
	// TranscodeReasons explains in words why a remux/transcode isn't a
	// direct play ("HEVC not supported by client", …).
	TranscodeReasons []string `json:"transcode_reasons,omitempty"`
	// CanStop reports whether the admin stop endpoint can act on this card —
	// false only for a direct play recorded before streams carried a user.
	CanStop bool `json:"can_stop"`
}

// streamFormat is one side of a card's source → output line. Codec and
// container values are the scanner's identifiers ("hevc", "truehd",
// "matroska"); "hls" for a server session's output.
type streamFormat struct {
	Container     string `json:"container,omitempty"`
	VideoCodec    string `json:"video_codec,omitempty"`
	AudioCodec    string `json:"audio_codec,omitempty"`
	Width         int    `json:"width,omitempty"`
	Height        int    `json:"height,omitempty"`
	BitrateKbps   int    `json:"bitrate_kbps,omitempty"`
	AudioChannels int    `json:"audio_channels,omitempty"`
	HDR           string `json:"hdr,omitempty"`
}

// liveSession is a card plus what the admin stop needs to act on it.
type liveSession struct {
	dto  activeSession
	sess *transcode.Session // server-side session; nil for a direct play
	// userID/itemID/clientIP scope the stop's refusal window and SSE event.
	userID   uuid.UUID
	itemID   uuid.UUID
	clientIP string
	// clientName is the player's self-reported device name from its progress
	// heartbeat ("" when only byte traffic named the stream).
	clientName string
}

type userItem struct{ user, item uuid.UUID }

// List handles GET /api/v1/sessions.
func (h *NativeSessionsHandler) List(w http.ResponseWriter, r *http.Request) {
	claims := middleware.ClaimsFromContext(r.Context())
	if claims == nil {
		respond.Unauthorized(w, r)
		return
	}
	live := h.collect(r.Context(), claims)
	resp := make([]activeSession, len(live))
	for i := range live {
		resp[i] = live[i].dto
	}
	respond.Success(w, r, resp)
}

// collect builds the Now Playing cards the caller may see: server sessions
// (transcode / remux) from Valkey, then direct plays from the stream tracker.
// Admins see everyone; other users only their own streams.
func (h *NativeSessionsHandler) collect(ctx context.Context, claims *auth.Claims) []liveSession {
	out := make([]liveSession, 0)
	lk := &sessionLookups{h: h}

	var entries []streaming.Entry
	if h.tracker != nil {
		entries = h.tracker.List()
	}
	// The newest progress heartbeat per (user, item): a server session's card
	// borrows the player's own device name and address from it.
	beats := make(map[userItem]streaming.Entry)
	for _, e := range entries {
		if !e.FromHeartbeat || e.UserID == uuid.Nil || e.MediaItemID == uuid.Nil {
			continue
		}
		k := userItem{e.UserID, e.MediaItemID}
		if prev, ok := beats[k]; !ok || e.LastSeen.After(prev.LastSeen) {
			beats[k] = e
		}
	}

	// ── Transcode/remux sessions from Valkey ─────────────────────────────────
	valkeySessions, err := h.store.List(ctx)
	if err != nil {
		h.logger.ErrorContext(ctx, "sessions: list valkey", "err", err)
	}
	coveredPaths := make(map[string]bool, len(valkeySessions))
	coveredItems := make(map[uuid.UUID]bool, len(valkeySessions))
	coveredUserItems := make(map[userItem]bool, len(valkeySessions))
	for _, s := range valkeySessions {
		coveredPaths[s.FilePath] = true
		coveredItems[s.MediaItemID] = true
		coveredUserItems[userItem{s.UserID, s.MediaItemID}] = true
	}
	if len(valkeySessions) > 0 {
		ids := make([]uuid.UUID, 0, len(valkeySessions))
		seen := map[uuid.UUID]bool{}
		for _, s := range valkeySessions {
			if !seen[s.MediaItemID] {
				ids = append(ids, s.MediaItemID)
				seen[s.MediaItemID] = true
			}
		}
		items, err := h.db.GetMediaItemsForSessions(ctx, ids)
		if err != nil {
			h.logger.ErrorContext(ctx, "sessions: item lookup", "err", err)
		}
		itemMap := make(map[uuid.UUID]gen.SessionMediaItem, len(items))
		for _, item := range items {
			itemMap[item.ID] = item
		}
		for i := range valkeySessions {
			s := &valkeySessions[i]
			// Rung children of an ABR parent are internal — an ABR stream shows
			// as the single parent card, not one per rung.
			if s.ParentID != "" {
				continue
			}
			// Non-admins see only their own sessions; admins get the full
			// now-playing dashboard.
			if !claims.IsAdmin && s.UserID != claims.UserID {
				continue
			}
			// Filter out sessions with no recent activity. Use LastActivityAt
			// when set; fall back to CreatedAt for brand-new sessions that have
			// not yet received a progress event. Old stale sessions without
			// LastActivityAt will have a zero value, so CreatedAt is used —
			// sessions created more than sessionActivityTimeout ago with no
			// activity are considered stale and hidden.
			activity := s.LastActivityAt
			if activity.IsZero() {
				activity = s.CreatedAt
			}
			if time.Since(activity) > sessionActivityTimeout {
				continue
			}

			as := activeSession{
				ID:         s.ID,
				Decision:   s.Decision,
				PositionMS: s.PositionMS,
				ClientName: s.ClientName,
				// .UTC(): a literal Z on local wall-clock time would shift the
				// claimed instant by the server's UTC offset.
				StartedAt:        s.CreatedAt.UTC().Format(time.RFC3339),
				Title:            filepath.Base(s.FilePath),
				TranscodeReasons: s.TranscodeReasons,
				CanStop:          true,
			}
			if s.BitrateKbps > 0 {
				br := s.BitrateKbps
				as.BitrateKbps = &br
			}
			// ABR: surface the rung the player settled on, and report that
			// rung's bitrate (the parent itself runs no encode, so its own
			// BitrateKbps is 0).
			if s.ABR && s.SelectedRendition != "" {
				label := s.SelectedRendition
				as.SelectedRendition = &label
				for _, rd := range s.ABRRenditions {
					if rd.Label == label {
						if rd.BitrateKbps > 0 {
							br := rd.BitrateKbps
							as.BitrateKbps = &br
						}
						break
					}
				}
			}
			if mi, ok := itemMap[s.MediaItemID]; ok {
				applySessionItem(&as, &mi)
			}

			ls := liveSession{sess: s, userID: s.UserID, itemID: s.MediaItemID, clientIP: s.ClientIP}
			if b, ok := beats[userItem{s.UserID, s.MediaItemID}]; ok {
				if ls.clientIP == "" {
					ls.clientIP = b.ClientIP
				}
				if b.ClientName != "" {
					ls.clientName = b.ClientName
					if s.ClientName == "" || s.ClientName == genericSessionClientName {
						as.ClientName = b.ClientName
					}
				}
			}
			src := lk.file(ctx, s.FileID)
			if src != nil {
				as.Source = sourceFormat(src)
			}
			as.Output = sessionOutput(s, src, as.BitrateKbps)
			h.attribute(ctx, lk, claims, &as, &ls)
			ls.dto = as
			out = append(out, ls)
		}
	}

	// ── Direct plays from the stream tracker ─────────────────────────────────
	// One card per (viewer, client, item): a direct play surfaces as a
	// byte-traffic entry and a progress-heartbeat entry at once, merged here.
	// Streams already shown as a server session above are skipped.
	type streamGroup struct {
		user, item uuid.UUID
		ip, path   string
		fileID     uuid.UUID
		first      time.Time
		beatName   string
		bytesName  string
		decision   string
		hasBytes   bool
		mi         *gen.SessionMediaItem
	}
	groups := make(map[string]*streamGroup)
	var order []string
	for _, e := range entries {
		// Non-admins only ever see their own, user-attributed direct plays.
		if !claims.IsAdmin && (e.UserID == uuid.Nil || e.UserID != claims.UserID) {
			continue
		}
		itemID := e.MediaItemID
		var mi *gen.SessionMediaItem
		if itemID == uuid.Nil {
			// Legacy file-traffic entry: resolve by path.
			if coveredPaths[e.FilePath] {
				continue
			}
			m, err := h.db.GetMediaItemByFilePath(ctx, e.FilePath)
			if err != nil && !errors.Is(err, pgx.ErrNoRows) {
				h.logger.WarnContext(ctx, "sessions: file path lookup", "err", err)
			}
			mi = m
			if mi != nil {
				itemID = mi.ID
			}
		}
		if itemID != uuid.Nil {
			if e.UserID != uuid.Nil && coveredUserItems[userItem{e.UserID, itemID}] {
				continue
			}
			if e.UserID == uuid.Nil && coveredItems[itemID] {
				continue // unattributed: already shown as some server session
			}
		}
		key := trackerCardID(e.UserID, e.ClientIP, itemID, e.FilePath)
		g, ok := groups[key]
		if !ok {
			g = &streamGroup{user: e.UserID, item: itemID, ip: e.ClientIP, first: e.FirstSeen, mi: mi}
			groups[key] = g
			order = append(order, key)
		}
		if e.FirstSeen.Before(g.first) {
			g.first = e.FirstSeen
		}
		if g.mi == nil {
			g.mi = mi
		}
		if e.FromHeartbeat {
			g.beatName = e.ClientName
			if e.Decision != "" {
				g.decision = e.Decision
			}
		} else {
			g.hasBytes = true
			g.bytesName = e.ClientName
			if e.FileID != uuid.Nil {
				g.fileID = e.FileID
			}
		}
		if g.path == "" {
			g.path = e.FilePath
		}
	}
	for _, key := range order {
		g := groups[key]
		if g.mi == nil && g.item != uuid.Nil {
			// Resolve by item id (heartbeat / attributed byte entries carry it).
			if items, err := h.db.GetMediaItemsForSessions(ctx, []uuid.UUID{g.item}); err != nil {
				h.logger.WarnContext(ctx, "sessions: heartbeat item lookup", "err", err)
			} else if len(items) > 0 {
				g.mi = &items[0]
			}
			if g.mi == nil {
				continue // can't render a useful card without the item
			}
		}
		name := g.beatName
		if name == "" {
			name = g.bytesName
		}
		as := activeSession{
			ID:         key,
			Decision:   h.directDecision(ctx, g.user, g.item, g.decision, g.hasBytes),
			ClientName: name,
			StartedAt:  g.first.UTC().Format(time.RFC3339),
			Title:      filepath.Base(g.path),
			// Stoppable when the stop can be scoped to one viewer's stream.
			CanStop: g.user != uuid.Nil && g.item != uuid.Nil,
		}
		if g.mi != nil {
			applySessionItem(&as, g.mi)
			posMS, durMS := h.tracker.GetItemState(g.mi.ID)
			as.PositionMS = posMS
			if as.DurationMS == nil && durMS > 0 {
				as.DurationMS = &durMS
			}
			if g.mi.Bitrate.Valid {
				br := int(g.mi.Bitrate.Int64 / 1000) // bits/s → kbps
				as.BitrateKbps = &br
			}
		}
		var src *media.File
		if g.fileID != uuid.Nil {
			src = lk.file(ctx, g.fileID)
		} else if g.item != uuid.Nil {
			src = lk.firstFile(ctx, g.item)
		}
		if src != nil {
			as.Source = sourceFormat(src)
			if as.BitrateKbps == nil && as.Source.BitrateKbps > 0 {
				br := as.Source.BitrateKbps
				as.BitrateKbps = &br
			}
		}
		ls := liveSession{userID: g.user, itemID: g.item, clientIP: g.ip, clientName: g.beatName}
		h.attribute(ctx, lk, claims, &as, &ls)
		ls.dto = as
		out = append(out, ls)
	}
	return out
}

// trackerCardID is a direct-play card's stable id. Attributed streams key on
// (client, item, user) so two viewers behind one address stay two cards; the
// legacy "client|item" / "client|path" forms remain for unattributed entries.
func trackerCardID(user uuid.UUID, clientIP string, item uuid.UUID, path string) string {
	switch {
	case item == uuid.Nil:
		return clientIP + "|" + path
	case user == uuid.Nil:
		return clientIP + "|" + item.String()
	}
	return clientIP + "|" + item.String() + "|" + user.String()
}

// directDecision labels a tracker card: the decision the client reported on
// its heartbeat wins; bytes served by StreamFile are a direct play by
// definition; otherwise the server's own last attribution for (user, item).
func (h *NativeSessionsHandler) directDecision(ctx context.Context, user, item uuid.UUID, reported string, hasBytes bool) string {
	if d := normalizePlayDecision(reported); d != "" {
		return d
	}
	if !hasBytes && user != uuid.Nil && item != uuid.Nil {
		if d := h.decisions.Last(ctx, user, item); d != "" {
			return d
		}
	}
	return decisionDirectPlay
}

// applySessionItem copies the item's display metadata onto a card.
func applySessionItem(as *activeSession, mi *gen.SessionMediaItem) {
	as.Title = mi.Title
	as.Type = mi.Type
	if mi.Year.Valid {
		y := int(mi.Year.Int32)
		as.Year = &y
	}
	if mi.PosterPath.Valid {
		p := mi.PosterPath.String
		as.PosterPath = &p
	}
	if mi.ParentTitle.Valid {
		p := mi.ParentTitle.String
		as.ParentTitle = &p
	}
	if mi.DurationMS.Valid {
		d := mi.DurationMS.Int64
		as.DurationMS = &d
	}
}

// attribute fills the card's viewer and address fields. The raw IP is shown
// to admins only; the LAN/remote label uses isLocalAddr, the classifier the
// first-run setup gate uses.
func (h *NativeSessionsHandler) attribute(ctx context.Context, lk *sessionLookups, claims *auth.Claims, as *activeSession, ls *liveSession) {
	if ls.userID != uuid.Nil {
		as.UserID = ls.userID.String()
		if u := lk.user(ctx, ls.userID); u != nil {
			as.Username = u.username
			as.ProfileName = u.profile
		}
	}
	if ls.clientIP != "" {
		as.Location = "remote"
		if isLocalAddr(ls.clientIP) {
			as.Location = "lan"
		}
		if claims.IsAdmin {
			as.ClientIP = ls.clientIP
		}
	}
}

// sourceFormat describes a source file for the card.
func sourceFormat(f *media.File) *streamFormat {
	sf := &streamFormat{
		Container:     derefStr(f.Container),
		VideoCodec:    derefStr(f.VideoCodec),
		AudioCodec:    derefStr(f.AudioCodec),
		HDR:           derefStr(f.HDRType),
		AudioChannels: transcode.SourceAudioChannels(f.AudioStreams, -1),
	}
	if f.ResolutionW != nil && f.ResolutionH != nil {
		sf.Width, sf.Height = *f.ResolutionW, *f.ResolutionH
	}
	if f.Bitrate != nil {
		sf.BitrateKbps = int(*f.Bitrate / 1000)
	}
	return sf
}

// sessionOutput describes what a server session sends the viewer: the encode
// target for a transcode, the source video in a new container for a remux.
func sessionOutput(s *transcode.Session, src *media.File, bitrateKbps *int) *streamFormat {
	out := &streamFormat{Container: "hls", AudioChannels: s.OutputAudioChannels}
	if bitrateKbps != nil {
		out.BitrateKbps = *bitrateKbps
	}
	hasVideo := src == nil || src.VideoCodec != nil
	if s.Decision == "transcode" {
		if hasVideo {
			switch {
			case s.AV1Output:
				out.VideoCodec = "av1"
			case s.HEVCOutput:
				out.VideoCodec = "hevc"
			default:
				out.VideoCodec = "h264"
			}
			out.Width, out.Height = s.OutputWidth, s.OutputHeight
			if s.ABR {
				for _, rd := range s.ABRRenditions {
					if rd.Label == s.SelectedRendition || (s.SelectedRendition == "" && out.Height == 0) {
						out.Width, out.Height = rd.Width, rd.Height
						break
					}
				}
			}
		}
	} else if src != nil && hasVideo {
		// Remux / direct stream: the video is copied unchanged.
		out.VideoCodec = derefStr(src.VideoCodec)
		if src.ResolutionW != nil && src.ResolutionH != nil {
			out.Width, out.Height = *src.ResolutionW, *src.ResolutionH
		}
	}
	switch s.OutputAudioCodec {
	case "":
		// Sessions created before attribution didn't record it.
	case "copy":
		// The track the session maps, which need not be the first one
		// src.AudioCodec describes (an older session reads 0: the first).
		if src != nil {
			out.AudioCodec = transcode.SourceAudioCodec(src, s.AudioStreamIndex)
		}
	default:
		out.AudioCodec = s.OutputAudioCodec
	}
	return out
}

func derefStr(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// sessionLookups memoizes per-request user and file lookups (a poll renders
// every card, and one viewer or file often backs several).
type sessionLookups struct {
	h      *NativeSessionsHandler
	users  map[uuid.UUID]*sessionUser
	files  map[uuid.UUID]*media.File
	firsts map[uuid.UUID]*media.File
}

type sessionUser struct{ username, profile string }

func (l *sessionLookups) user(ctx context.Context, id uuid.UUID) *sessionUser {
	if l.h.users == nil {
		return nil
	}
	if u, ok := l.users[id]; ok {
		return u
	}
	if l.users == nil {
		l.users = make(map[uuid.UUID]*sessionUser)
	}
	var su *sessionUser
	if u, err := l.h.users.GetUser(ctx, id); err != nil {
		l.h.logger.DebugContext(ctx, "sessions: user lookup", "user_id", id, "err", err)
	} else {
		su = &sessionUser{username: u.Username}
		// A managed profile reads as "owner · profile".
		if u.ParentUserID.Valid {
			if p, perr := l.h.users.GetUser(ctx, uuid.UUID(u.ParentUserID.Bytes)); perr == nil {
				su = &sessionUser{username: p.Username, profile: u.Username}
			}
		}
	}
	l.users[id] = su
	return su
}

func (l *sessionLookups) file(ctx context.Context, id uuid.UUID) *media.File {
	if l.h.files == nil || id == uuid.Nil {
		return nil
	}
	if f, ok := l.files[id]; ok {
		return f
	}
	if l.files == nil {
		l.files = make(map[uuid.UUID]*media.File)
	}
	f, err := l.h.files.GetFile(ctx, id)
	if err != nil {
		l.h.logger.DebugContext(ctx, "sessions: file lookup", "file_id", id, "err", err)
		f = nil
	}
	l.files[id] = f
	return f
}

// firstFile is the item's best-quality active file — what a player picking
// the default file direct-plays.
func (l *sessionLookups) firstFile(ctx context.Context, itemID uuid.UUID) *media.File {
	if l.h.files == nil {
		return nil
	}
	if f, ok := l.firsts[itemID]; ok {
		return f
	}
	if l.firsts == nil {
		l.firsts = make(map[uuid.UUID]*media.File)
	}
	var f *media.File
	if files, err := l.h.files.GetFiles(ctx, itemID); err != nil {
		l.h.logger.DebugContext(ctx, "sessions: item files lookup", "item_id", itemID, "err", err)
	} else if len(files) > 0 {
		f = &files[0]
	}
	l.firsts[itemID] = f
	return f
}

// stopRequest is the optional body of POST /api/v1/sessions/{id}/stop.
type stopRequest struct {
	// Message is shown to the viewer ("Playback was stopped by the server
	// admin: <message>"). Optional, at most 200 characters.
	Message string `json:"message"`
}

// Stop handles POST /api/v1/sessions/{id}/stop — the admin "stop this
// stream" on a Now Playing card, for every playback mode.
//
//   - Server sessions (transcode, remux) are torn down exactly as the
//     transcode Stop route does.
//   - Every stop publishes a playback.stop SSE event to the viewer (item,
//     session id, client name, message) so a cooperating player stops and
//     shows the message.
//   - Direct play / direct stream / remux — the modes a client can simply
//     restart on its own — are also refused server-side for
//     playbackStopWindow for that exact (user, item, client IP): StreamFile,
//     transcode Start and 'playing' beacons answer 403 PLAYBACK_STOPPED. A
//     transcode keeps its historical behaviour (teardown only).
//
// 404 when id isn't a live card (it ended, or never existed); 409 for the
// rare unattributed legacy direct-play entry. Audited as transcode.stop.
func (h *NativeSessionsHandler) Stop(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	claims := middleware.ClaimsFromContext(ctx)
	if claims == nil {
		respond.Unauthorized(w, r)
		return
	}
	if !claims.IsAdmin {
		respond.Forbidden(w, r)
		return
	}
	id := chi.URLParam(r, "id")
	if id == "" || len(id) > 256 {
		respond.BadRequest(w, r, "invalid session id")
		return
	}
	// A direct-play card id embeds the client address ("::1|item|user" for an
	// IPv6 viewer). Clients percent-encode the segment, and an escape Go
	// wouldn't have produced itself (%3A for ':') makes chi route on the raw
	// path, so the param arrives still encoded and would never match. Match
	// the id as sent first, then its unescaped form.
	unescapedID := id
	if u, uerr := url.PathUnescape(id); uerr == nil {
		unescapedID = u
	}
	var body stopRequest
	if r.Body != nil {
		if err := json.NewDecoder(io.LimitReader(r.Body, 8<<10)).Decode(&body); err != nil && !errors.Is(err, io.EOF) {
			respond.BadRequest(w, r, "invalid request body")
			return
		}
	}
	msg, err := cleanStopMessage(body.Message)
	if err != nil {
		respond.BadRequest(w, r, err.Error())
		return
	}

	live := h.collect(ctx, claims)
	find := func(want string) *liveSession {
		for i := range live {
			if live[i].dto.ID == want {
				return &live[i]
			}
		}
		return nil
	}
	target := find(id)
	if target == nil && unescapedID != id {
		target = find(unescapedID)
	}
	if target == nil {
		respond.NotFound(w, r)
		return
	}
	if !target.dto.CanStop {
		respond.Error(w, r, http.StatusConflict, "STOP_UNSUPPORTED",
			"this stream can't be targeted yet; it leaves Now Playing within a minute")
		return
	}

	decision := target.dto.Decision
	if target.sess != nil {
		if h.terminator != nil {
			h.terminator.TerminateSession(ctx, target.sess)
		} else if derr := h.store.Delete(ctx, target.sess.ID); derr != nil {
			h.logger.WarnContext(ctx, "sessions: stop delete", "session_id", target.sess.ID, "err", derr)
		}
	}
	var blockedUntil time.Time
	if decision != "transcode" && target.userID != uuid.Nil && target.clientIP != "" {
		blockedUntil = h.stops.Block(ctx, target.userID, target.itemID, target.clientIP, msg)
	}
	if h.tracker != nil && target.userID != uuid.Nil && target.clientIP != "" {
		h.tracker.RemoveStream(target.userID, target.clientIP, target.itemID)
	}
	h.publishStop(target, decision, msg)

	h.logger.InfoContext(ctx, "sessions: admin stopped stream",
		"card_id", target.dto.ID, "admin_id", claims.UserID, "owner_id", target.userID,
		"item_id", target.itemID, "decision", decision, "blocked", !blockedUntil.IsZero())
	if h.audit != nil {
		actor := claims.UserID
		detail := map[string]any{
			"owner_id": target.userID.String(),
			"item_id":  target.itemID.String(),
			"decision": decision,
			"reason":   "admin_terminated",
		}
		if target.dto.ClientName != "" {
			detail["client_name"] = target.dto.ClientName
		}
		if target.clientIP != "" {
			detail["client_ip"] = target.clientIP
		}
		if msg != "" {
			detail["message"] = msg
		}
		if !blockedUntil.IsZero() {
			detail["blocked_until"] = blockedUntil.UTC().Format(time.RFC3339)
		}
		h.audit.Log(ctx, &actor, audit.ActionTranscodeStop, target.dto.ID, detail, audit.ClientIP(r))
	}
	respond.NoContent(w)
}

// publishStop sends the playback.stop event to the viewer's SSE channel.
func (h *NativeSessionsHandler) publishStop(target *liveSession, decision, msg string) {
	if h.broker == nil || target.userID == uuid.Nil {
		return
	}
	p := PlaybackStopPayload{
		ItemID:     target.itemID.String(),
		ClientName: target.clientName,
		Decision:   decision,
		Message:    msg,
	}
	if target.sess != nil {
		p.SessionID = target.sess.ID
	}
	data, err := json.Marshal(p)
	if err != nil {
		return
	}
	itemID := p.ItemID
	h.broker.Publish(target.userID, notification.Event{
		Type:      PlaybackStopEventType,
		ItemID:    &itemID,
		CreatedAt: time.Now().UnixMilli(),
		Data:      data,
	})
}
