package v1

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onscreen/onscreen/internal/api/middleware"
	"github.com/onscreen/onscreen/internal/api/respond"
	"github.com/onscreen/onscreen/internal/db/gen"
	"github.com/onscreen/onscreen/internal/m3u"
)

// PlaylistM3UDB is what M3U import and export need beyond PlaylistDB.
// Satisfied by *gen.Queries; optional (WithM3U) so the existing PlaylistDB
// fakes keep compiling.
type PlaylistM3UDB interface {
	ListLibraries(ctx context.Context) ([]gen.Library, error)
	ListM3UExportRows(ctx context.Context, arg gen.ListM3UExportRowsParams) ([]gen.ListM3UExportRowsRow, error)
	FindM3UFilesByPath(ctx context.Context, arg gen.FindM3UFilesByPathParams) ([]gen.FindM3UFilesByPathRow, error)
	FindM3UFilesByBasename(ctx context.Context, arg gen.FindM3UFilesByBasenameParams) ([]gen.FindM3UFilesByBasenameRow, error)
	FindM3UTracksByTitle(ctx context.Context, arg gen.FindM3UTracksByTitleParams) ([]gen.FindM3UTracksByTitleRow, error)
	AppendCollectionItems(ctx context.Context, arg gen.AppendCollectionItemsParams) error
}

// WithM3U enables GET /playlists/{id}/export.m3u8 and POST
// /playlists/import. Without it both answer 404.
func (h *PlaylistHandler) WithM3U(db PlaylistM3UDB) *PlaylistHandler {
	h.m3u = db
	return h
}

const (
	// m3uMaxBytes caps an imported playlist file. 1 MiB holds ~10k entries
	// of ordinary path length, which is also m3u.MaxEntries.
	m3uMaxBytes = 1 << 20
	// m3uReportCap bounds the per-line match / unmatched lists in the
	// import response; the counts are always complete.
	m3uReportCap = 500
	// m3uCandidateCap bounds the suffix and tag candidate queries.
	m3uCandidateCap = 50000
	// m3uSuffixDepth is how many trailing path components the suffix match
	// compares (file name, album, artist).
	m3uSuffixDepth = 3
)

// How an import entry was matched.
const (
	m3uByPath     = "path"     // exact absolute path
	m3uByRelative = "relative" // relative to base_dir or a library root
	m3uBySuffix   = "suffix"   // same trailing path components, other mount
	m3uByTags     = "tags"     // #EXTINF artist + title against tracks
)

// m3uVisibility is the caller's item visibility in query-argument form.
type m3uVisibility struct {
	libraryIDs []uuid.UUID // nil = every library (admin / no ACL)
	allowed    map[uuid.UUID]struct{}
	maxRank    *int32
	blocked    []string
	allowedLbl []string
}

func (h *PlaylistHandler) visibility(r *http.Request) (m3uVisibility, error) {
	allowed, err := h.allowedLibraries(r)
	if err != nil {
		return m3uVisibility{}, err
	}
	v := m3uVisibility{allowed: allowed}
	if allowed != nil {
		v.libraryIDs = make([]uuid.UUID, 0, len(allowed))
		for id := range allowed {
			v.libraryIDs = append(v.libraryIDs, id)
		}
	}
	if claims := middleware.ClaimsFromContext(r.Context()); claims != nil {
		v.maxRank = maxRatingRankFromClaims(claims.MaxContentRating)
		v.blocked = claims.LabelFilter().BlockedArg()
		v.allowedLbl = claims.LabelFilter().AllowedArg()
	}
	return v, nil
}

// ExportM3U handles GET /api/v1/playlists/{id}/export.m3u8: the playlist as
// an extended M3U. Entries are paths relative to the item's library root, or
// the absolute server paths with ?paths=absolute (for another player on the
// same machine or NAS). Items the caller can't see — another library, over
// the rating ceiling, label-restricted — and items with no file are left out.
func (h *PlaylistHandler) ExportM3U(w http.ResponseWriter, r *http.Request) {
	if h.m3u == nil {
		respond.NotFound(w, r)
		return
	}
	id, col, ok := h.loadOwned(w, r, "id")
	if !ok {
		return
	}
	vis, err := h.visibility(r)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "playlist export: allowed libraries", "err", err)
		respond.InternalError(w, r)
		return
	}

	var ids []uuid.UUID
	if col.Type == "smart_playlist" {
		items, err := h.resolveSmartPlaylist(r, col, vis.allowed)
		if err != nil {
			h.logger.ErrorContext(r.Context(), "playlist export: smart resolve", "id", id, "err", err)
			respond.InternalError(w, r)
			return
		}
		for _, it := range items {
			if u, err := uuid.Parse(it.ID); err == nil {
				ids = append(ids, u)
			}
		}
	} else {
		rows, err := h.db.ListCollectionItems(r.Context(), gen.ListCollectionItemsParams{
			CollectionID:  id,
			MaxRatingRank: vis.maxRank,
			BlockedLabels: vis.blocked,
			AllowedLabels: vis.allowedLbl,
			Lim:           m3u.MaxEntries,
		})
		if err != nil {
			h.logger.ErrorContext(r.Context(), "playlist export: list items", "id", id, "err", err)
			respond.InternalError(w, r)
			return
		}
		for _, row := range rows {
			if vis.allowed != nil {
				if _, ok := vis.allowed[row.LibraryID]; !ok {
					continue
				}
			}
			ids = append(ids, row.ID)
		}
	}

	rows := []gen.ListM3UExportRowsRow{}
	if len(ids) > 0 {
		rows, err = h.m3u.ListM3UExportRows(r.Context(), gen.ListM3UExportRowsParams{
			ItemIds:       ids,
			LibraryIds:    vis.libraryIDs,
			MaxRatingRank: vis.maxRank,
			BlockedLabels: vis.blocked,
			AllowedLabels: vis.allowedLbl,
		})
		if err != nil {
			h.logger.ErrorContext(r.Context(), "playlist export: rows", "id", id, "err", err)
			respond.InternalError(w, r)
			return
		}
	}

	absolute := r.URL.Query().Get("paths") == "absolute"
	var buf bytes.Buffer
	mw := m3u.NewWriter(&buf)
	for _, row := range rows {
		p := row.FilePath
		if !absolute {
			p = libraryRelativePath(p, row.ScanPaths)
		}
		ms := row.FileDurationMs
		if ms == nil || *ms <= 0 {
			ms = row.DurationMs
		}
		secs := -1
		if ms != nil && *ms > 0 {
			secs = int((*ms + 500) / 1000)
		}
		artist := row.Artist
		if row.Type != "track" && row.Type != "episode" {
			artist = ""
		}
		mw.Entry(secs, artist, row.Title, p)
	}
	if err := mw.Err(); err != nil {
		respond.InternalError(w, r)
		return
	}
	fname := downloadFilename(col.Name, "playlist.m3u8")
	w.Header().Set("Content-Type", "audio/x-mpegurl; charset=utf-8")
	w.Header().Set("Content-Disposition",
		`attachment; filename="`+fname+`"; filename*=UTF-8''`+url.PathEscape(fname))
	w.Header().Set("Cache-Control", "private, no-store")
	_, _ = w.Write(buf.Bytes())
}

// libraryRelativePath returns filePath relative to whichever library root
// contains it, with forward slashes (what every player accepts). A file under
// no root (a root edited since the scan) keeps its absolute path rather than
// becoming a dangling relative one.
func libraryRelativePath(filePath string, roots []string) string {
	fp := strings.ReplaceAll(filePath, `\`, "/")
	best := ""
	for _, root := range roots {
		rt := strings.TrimRight(strings.ReplaceAll(root, `\`, "/"), "/")
		if rt == "" {
			continue
		}
		if strings.HasPrefix(fp, rt+"/") && len(rt) > len(best) {
			best = rt
		}
	}
	if best == "" {
		return filePath
	}
	return fp[len(best)+1:]
}

type m3uMatchLine struct {
	Line   int    `json:"line"`
	Entry  string `json:"entry"`
	ItemID string `json:"item_id"`
	Title  string `json:"title"`
	Method string `json:"method"`
}

type m3uUnmatchedLine struct {
	Line  int    `json:"line"`
	Entry string `json:"entry"`
	// Reason: "url" (a stream or web address — never fetched) or
	// "not_found" (nothing visible to the caller matched).
	Reason string `json:"reason"`
}

type m3uImportResponse struct {
	Playlist  playlistResponse `json:"playlist"`
	Total     int              `json:"total"`
	Matched   int              `json:"matched"`
	Unmatched int              `json:"unmatched"`
	// Added is the number of distinct items now in the playlist (an item
	// listed twice in the file is added once).
	Added    int            `json:"added"`
	ByMethod map[string]int `json:"by_method"`
	// Matches and UnmatchedEntries list lines, capped at 500 each;
	// *Truncated says the cap was hit.
	Matches            []m3uMatchLine     `json:"matches"`
	MatchesTruncated   bool               `json:"matches_truncated"`
	UnmatchedEntries   []m3uUnmatchedLine `json:"unmatched_entries"`
	UnmatchedTruncated bool               `json:"unmatched_truncated"`
}

// ImportM3U handles POST /api/v1/playlists/import: a multipart form with the
// playlist as `file` (.m3u or .m3u8, at most 1 MiB and 10,000 entries), an
// optional `name` (default: the file name) and an optional `base_dir` (the
// server directory the playlist came from, for its relative entries). It
// creates a static playlist owned by the caller from the entries that match
// an item the caller can see, and reports how each line fared.
//
// The entries are strings to match, never files to open or URLs to fetch.
func (h *PlaylistHandler) ImportM3U(w http.ResponseWriter, r *http.Request) {
	if h.m3u == nil {
		respond.NotFound(w, r)
		return
	}
	claims := middleware.ClaimsFromContext(r.Context())
	if claims == nil {
		respond.Unauthorized(w, r)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, m3uMaxBytes+64<<10)
	if err := r.ParseMultipartForm(m3uMaxBytes + 64<<10); err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			respond.Error(w, r, http.StatusRequestEntityTooLarge, "PLAYLIST_TOO_LARGE",
				"playlist files are limited to 1 MB")
			return
		}
		respond.BadRequest(w, r, "expected a multipart form with a playlist file")
		return
	}
	defer func() { _ = r.MultipartForm.RemoveAll() }()
	file, hdr, err := r.FormFile("file")
	if err != nil {
		respond.BadRequest(w, r, "file is required")
		return
	}
	defer file.Close()
	ext := strings.ToLower(filepath.Ext(hdr.Filename))
	if ext != ".m3u" && ext != ".m3u8" {
		respond.BadRequest(w, r, "the playlist must be an .m3u or .m3u8 file")
		return
	}
	data, err := io.ReadAll(io.LimitReader(file, m3uMaxBytes+1))
	if err != nil {
		respond.BadRequest(w, r, "could not read the playlist file")
		return
	}
	if len(data) > m3uMaxBytes {
		respond.Error(w, r, http.StatusRequestEntityTooLarge, "PLAYLIST_TOO_LARGE",
			"playlist files are limited to 1 MB")
		return
	}
	// .m3u is historically Latin-1; .m3u8 is UTF-8 by definition.
	text, err := m3u.Decode(data, ext == ".m3u")
	if err != nil {
		respond.BadRequest(w, r, "an .m3u8 playlist must be UTF-8")
		return
	}
	entries, err := m3u.Parse(text)
	if err != nil {
		if errors.Is(err, m3u.ErrTooManyEntries) {
			respond.Error(w, r, http.StatusRequestEntityTooLarge, "PLAYLIST_TOO_LARGE",
				"playlists are limited to 10,000 entries")
			return
		}
		respond.BadRequest(w, r, "could not read the playlist")
		return
	}

	name := strings.TrimSpace(r.FormValue("name"))
	if name == "" {
		name = strings.TrimSpace(strings.TrimSuffix(filepath.Base(strings.ReplaceAll(hdr.Filename, `\`, "/")), filepath.Ext(hdr.Filename)))
	}
	if name == "" {
		name = "Imported playlist"
	}
	if utf8.RuneCountInString(name) > 200 {
		name = string([]rune(name)[:200])
	}

	vis, err := h.visibility(r)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "playlist import: allowed libraries", "err", err)
		respond.InternalError(w, r)
		return
	}
	res, err := h.resolveM3U(r.Context(), vis, entries, strings.TrimSpace(r.FormValue("base_dir")))
	if err != nil {
		h.logger.ErrorContext(r.Context(), "playlist import: resolve", "err", err)
		respond.InternalError(w, r)
		return
	}

	userPG := pgtype.UUID{Bytes: [16]byte(claims.UserID), Valid: true}
	col, err := h.db.CreateCollection(r.Context(), gen.CreateCollectionParams{
		UserID: userPG,
		Name:   name,
		Type:   "playlist",
	})
	if err != nil {
		h.logger.ErrorContext(r.Context(), "playlist import: create", "err", err)
		respond.InternalError(w, r)
		return
	}
	ids := res.itemIDs()
	if len(ids) > 0 {
		if err := h.m3u.AppendCollectionItems(r.Context(), gen.AppendCollectionItemsParams{
			CollectionID: col.ID,
			ItemIds:      ids,
		}); err != nil {
			h.logger.ErrorContext(r.Context(), "playlist import: add items", "id", col.ID, "err", err)
			// Don't leave a half-built playlist behind.
			_ = h.db.DeleteCollection(r.Context(), col.ID)
			respond.InternalError(w, r)
			return
		}
	}

	out := m3uImportResponse{
		Playlist:         toPlaylistResponse(col),
		Total:            len(entries),
		Added:            len(ids),
		ByMethod:         map[string]int{m3uByPath: 0, m3uByRelative: 0, m3uBySuffix: 0, m3uByTags: 0},
		Matches:          []m3uMatchLine{},
		UnmatchedEntries: []m3uUnmatchedLine{},
	}
	for i, e := range entries {
		m := res.matches[i]
		if m.method == "" {
			out.Unmatched++
			if len(out.UnmatchedEntries) < m3uReportCap {
				reason := "not_found"
				if m.url {
					reason = "url"
				}
				out.UnmatchedEntries = append(out.UnmatchedEntries, m3uUnmatchedLine{Line: e.Line, Entry: e.Path, Reason: reason})
			} else {
				out.UnmatchedTruncated = true
			}
			continue
		}
		out.Matched++
		out.ByMethod[m.method]++
		if len(out.Matches) < m3uReportCap {
			out.Matches = append(out.Matches, m3uMatchLine{
				Line: e.Line, Entry: e.Path, ItemID: m.itemID.String(), Title: m.title, Method: m.method,
			})
		} else {
			out.MatchesTruncated = true
		}
	}
	respond.Created(w, r, out)
}

type m3uMatch struct {
	itemID uuid.UUID
	title  string
	method string // "" = unmatched
	url    bool
}

type m3uResolution struct {
	matches []m3uMatch // parallel to the entries
}

// itemIDs returns the matched items in playlist order, each once.
func (res m3uResolution) itemIDs() []uuid.UUID {
	seen := make(map[uuid.UUID]struct{}, len(res.matches))
	out := make([]uuid.UUID, 0, len(res.matches))
	for _, m := range res.matches {
		if m.method == "" {
			continue
		}
		if _, dup := seen[m.itemID]; dup {
			continue
		}
		seen[m.itemID] = struct{}{}
		out = append(out, m.itemID)
	}
	return out
}

// resolveM3U matches each entry to an item the caller can see, trying in
// order: the exact absolute path; the path relative to baseDir or to a
// library root; the same trailing path components under another mount point
// (/mnt/music/… vs M:\…, compared case-insensitively); and last the #EXTINF
// artist + title against tracks. Every lookup goes through the caller's
// visibility, so the result can't name an item they couldn't open.
func (h *PlaylistHandler) resolveM3U(ctx context.Context, vis m3uVisibility, entries []m3u.Entry, baseDir string) (m3uResolution, error) {
	res := m3uResolution{matches: make([]m3uMatch, len(entries))}

	libs, err := h.m3u.ListLibraries(ctx)
	if err != nil {
		return res, err
	}
	var roots []string
	for _, l := range libs {
		if vis.allowed != nil {
			if _, ok := vis.allowed[l.ID]; !ok {
				continue
			}
		}
		roots = append(roots, l.ScanPaths...)
	}

	// Pass 1: exact and relative paths, one indexed lookup for all of them.
	type cand struct {
		paths  []string // tried in order
		method []string
	}
	cands := make([]cand, len(entries))
	var lookup []string
	clean := make([]string, len(entries))
	for i, e := range entries {
		p, kind := m3u.Classify(e.Path)
		clean[i] = p
		switch kind {
		case m3u.KindURL:
			res.matches[i].url = true
			continue
		case m3u.KindAbsolute:
			cands[i].paths = append(cands[i].paths, p)
			cands[i].method = append(cands[i].method, m3uByPath)
		case m3u.KindRelative:
			bases := roots
			if baseDir != "" {
				bases = append([]string{baseDir}, roots...)
			}
			for _, b := range bases {
				if j := m3u.Join(b, p); j != "" {
					cands[i].paths = append(cands[i].paths, j)
					cands[i].method = append(cands[i].method, m3uByRelative)
				}
			}
		}
		lookup = append(lookup, cands[i].paths...)
	}
	byPath := map[string]gen.FindM3UFilesByPathRow{}
	for start := 0; start < len(lookup); start += 5000 {
		end := min(start+5000, len(lookup))
		rows, err := h.m3u.FindM3UFilesByPath(ctx, gen.FindM3UFilesByPathParams{
			Paths:         lookup[start:end],
			LibraryIds:    vis.libraryIDs,
			MaxRatingRank: vis.maxRank,
			BlockedLabels: vis.blocked,
			AllowedLabels: vis.allowedLbl,
		})
		if err != nil {
			return res, err
		}
		for _, row := range rows {
			byPath[row.FilePath] = row
		}
	}
	for i := range entries {
		for k, p := range cands[i].paths {
			if row, ok := byPath[p]; ok {
				res.matches[i] = m3uMatch{itemID: row.MediaItemID, title: row.Title, method: cands[i].method[k]}
				break
			}
		}
	}

	// Pass 2: trailing path components, for a playlist written against
	// another mount point of the same files.
	baseSet := map[string]struct{}{}
	for i := range entries {
		if res.matches[i].method != "" || res.matches[i].url {
			continue
		}
		if c := m3u.FoldedComponents(clean[i]); len(c) > 0 {
			baseSet[c[len(c)-1]] = struct{}{}
		}
	}
	if len(baseSet) > 0 {
		basenames := make([]string, 0, len(baseSet))
		for b := range baseSet {
			basenames = append(basenames, b)
		}
		rows, err := h.m3u.FindM3UFilesByBasename(ctx, gen.FindM3UFilesByBasenameParams{
			Basenames:     basenames,
			LibraryIds:    vis.libraryIDs,
			MaxRatingRank: vis.maxRank,
			BlockedLabels: vis.blocked,
			AllowedLabels: vis.allowedLbl,
			Lim:           m3uCandidateCap,
		})
		if err != nil {
			return res, err
		}
		type fileCand struct {
			comps []string
			row   gen.FindM3UFilesByBasenameRow
		}
		byBase := map[string][]fileCand{}
		for _, row := range rows {
			c := m3u.FoldedComponents(row.FilePath)
			if len(c) == 0 {
				continue
			}
			byBase[c[len(c)-1]] = append(byBase[c[len(c)-1]], fileCand{comps: c, row: row})
		}
		for i := range entries {
			if res.matches[i].method != "" || res.matches[i].url {
				continue
			}
			ec := m3u.FoldedComponents(clean[i])
			if len(ec) == 0 {
				continue
			}
			// The file name plus at least one directory must agree, unless
			// the entry is a bare file name — then only a unique file
			// name counts.
			need := min(2, len(ec))
			list := byBase[ec[len(ec)-1]]
			if need == 1 && len(list) != 1 {
				continue
			}
			best, bestN := -1, 0
			for k, fc := range list {
				if n := m3u.TrailingMatch(ec, fc.comps, m3uSuffixDepth); n >= need && n > bestN {
					best, bestN = k, n
				}
			}
			if best >= 0 {
				row := list[best].row
				res.matches[i] = m3uMatch{itemID: row.MediaItemID, title: row.Title, method: m3uBySuffix}
			}
		}
	}

	// Pass 3: #EXTINF artist + title against tracks.
	titleSet := map[string]struct{}{}
	for i, e := range entries {
		if res.matches[i].method != "" || res.matches[i].url || e.Title == "" {
			continue
		}
		titleSet[strings.ToLower(e.Title)] = struct{}{}
	}
	if len(titleSet) > 0 {
		titles := make([]string, 0, len(titleSet))
		for t := range titleSet {
			titles = append(titles, t)
		}
		rows, err := h.m3u.FindM3UTracksByTitle(ctx, gen.FindM3UTracksByTitleParams{
			Titles:        titles,
			LibraryIds:    vis.libraryIDs,
			MaxRatingRank: vis.maxRank,
			BlockedLabels: vis.blocked,
			AllowedLabels: vis.allowedLbl,
			Lim:           m3uCandidateCap,
		})
		if err != nil {
			return res, err
		}
		byTitle := map[string][]gen.FindM3UTracksByTitleRow{}
		for _, row := range rows {
			k := strings.ToLower(row.Title)
			byTitle[k] = append(byTitle[k], row)
		}
		for i, e := range entries {
			if res.matches[i].method != "" || res.matches[i].url || e.Title == "" {
				continue
			}
			list := byTitle[strings.ToLower(e.Title)]
			var pick *gen.FindM3UTracksByTitleRow
			if e.Artist == "" {
				// Without an artist only an unambiguous title will do.
				if len(list) == 1 {
					pick = &list[0]
				}
			} else {
				for k := range list {
					if strings.EqualFold(list[k].Artist, e.Artist) || strings.EqualFold(list[k].AlbumArtist, e.Artist) {
						pick = &list[k]
						break
					}
				}
			}
			if pick != nil {
				res.matches[i] = m3uMatch{itemID: pick.ID, title: pick.Title, method: m3uByTags}
			}
		}
	}
	return res, nil
}
