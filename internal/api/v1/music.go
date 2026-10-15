package v1

import (
	"context"
	"errors"
	"log/slog"
	"math/rand"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onscreen/onscreen/internal/api/middleware"
	"github.com/onscreen/onscreen/internal/api/respond"
	"github.com/onscreen/onscreen/internal/auth"
	"github.com/onscreen/onscreen/internal/db/gen"
	"github.com/onscreen/onscreen/internal/dbconv"
	"github.com/onscreen/onscreen/internal/musicrec"
)

// MusicDB is the database surface of the music discovery endpoints. Every
// listing query applies the content-rating ceiling and the label restriction
// itself; library access is checked here, on the seed item's library (an
// artist's similar artists, appearances and mix tracks all live in it).
// Satisfied by *gen.Queries.
type MusicDB interface {
	GetMediaItem(ctx context.Context, id uuid.UUID) (gen.GetMediaItemRow, error)
	ListArtistAppearances(ctx context.Context, arg gen.ListArtistAppearancesParams) ([]gen.ListArtistAppearancesRow, error)
	ListArtistCoListens(ctx context.Context, arg gen.ListArtistCoListensParams) ([]gen.ListArtistCoListensRow, error)
	ListArtistProfiles(ctx context.Context, arg gen.ListArtistProfilesParams) ([]gen.ListArtistProfilesRow, error)
	ListMixTracksForArtists(ctx context.Context, arg gen.ListMixTracksForArtistsParams) ([]gen.ListMixTracksForArtistsRow, error)
	ListMixTracksInAlbum(ctx context.Context, arg gen.ListMixTracksInAlbumParams) ([]gen.ListMixTracksInAlbumRow, error)
	ListMixTracksByGenres(ctx context.Context, arg gen.ListMixTracksByGenresParams) ([]gen.ListMixTracksByGenresRow, error)
}

// MusicHandler serves similar artists, "Appears on" and instant mixes.
type MusicHandler struct {
	db     MusicDB
	access LibraryAccessChecker
	logger *slog.Logger
	// rnd seeds each mix's shuffle; tests replace it for determinism.
	rnd func() *rand.Rand
}

// NewMusicHandler creates a MusicHandler.
func NewMusicHandler(db MusicDB, logger *slog.Logger) *MusicHandler {
	return &MusicHandler{db: db, logger: logger, rnd: func() *rand.Rand {
		return rand.New(rand.NewSource(time.Now().UnixNano()))
	}}
}

// WithLibraryAccess attaches the library-access checker.
func (h *MusicHandler) WithLibraryAccess(a LibraryAccessChecker) *MusicHandler {
	h.access = a
	return h
}

// LibraryAccessWired reports whether the library-ACL checker is wired (a nil
// checker fails open; see api.Handlers.ValidateLibraryAccess).
func (h *MusicHandler) LibraryAccessWired() bool { return h.access != nil }

// SimilarArtistResponse is one entry of GET /music/artists/{id}/similar.
type SimilarArtistResponse struct {
	ID         string  `json:"id"`
	Title      string  `json:"title"`
	PosterPath *string `json:"poster_path,omitempty"`
	UpdatedAt  int64   `json:"updated_at"`
	// Score is the similarity in [0, 1].
	Score float64 `json:"score"`
	// SharedGenres are the genres it shares with the artist asked about.
	SharedGenres []string `json:"shared_genres"`
	// CoListened is true when people who listen to one listen to the other.
	CoListened bool `json:"co_listened"`
}

// ArtistAppearanceResponse is one track of GET /music/artists/{id}/appears-on:
// a track the artist is credited on without being its album's artist.
type ArtistAppearanceResponse struct {
	ID         string `json:"id"`
	Title      string `json:"title"`
	Index      *int32 `json:"index,omitempty"`
	DiscNumber *int32 `json:"disc_number,omitempty"`
	DurationMS *int64 `json:"duration_ms,omitempty"`
	// Role is "featured" (a feat. credit) or "artist" (another credit, such
	// as the performer of a compilation track).
	Role             string  `json:"role"`
	AlbumID          string  `json:"album_id"`
	AlbumTitle       string  `json:"album_title"`
	AlbumYear        *int32  `json:"album_year,omitempty"`
	AlbumPosterPath  *string `json:"album_poster_path,omitempty"`
	AlbumUpdatedAt   int64   `json:"album_updated_at"`
	AlbumArtistID    *string `json:"album_artist_id,omitempty"`
	AlbumArtistTitle *string `json:"album_artist_title,omitempty"`
}

// MixTrackResponse is one track of GET /music/mix: everything a player
// queue needs, including the file to stream and its ReplayGain values.
type MixTrackResponse struct {
	ID                  string   `json:"id"`
	Title               string   `json:"title"`
	DurationMS          *int64   `json:"duration_ms,omitempty"`
	Index               *int32   `json:"index,omitempty"`
	AlbumID             string   `json:"album_id"`
	AlbumTitle          string   `json:"album_title"`
	AlbumPosterPath     *string  `json:"album_poster_path,omitempty"`
	ArtistID            *string  `json:"artist_id,omitempty"`
	ArtistTitle         *string  `json:"artist_title,omitempty"`
	FileID              string   `json:"file_id"`
	ReplayGainTrackGain *float64 `json:"replaygain_track_gain,omitempty"`
	ReplayGainTrackPeak *float64 `json:"replaygain_track_peak,omitempty"`
	ReplayGainAlbumGain *float64 `json:"replaygain_album_gain,omitempty"`
	ReplayGainAlbumPeak *float64 `json:"replaygain_album_peak,omitempty"`
}

// Mix sizing.
const (
	mixDefaultSize    = 50
	mixMaxSize        = 100
	mixSeedTracks     = 12 // the seed's own tracks, besides a seed track itself
	mixSimilarArtists = 8
	mixPerSimilar     = 5
	// A user rating at or below this (0-10 scale: one star of five) keeps a
	// track out of the user's mixes.
	mixDislikedScore = 2.0
)

// visibleSeed loads id and applies the single-item gates: the item exists
// and isn't deleted, its type is one of types, the caller can see its
// library, and its rating and labels pass. It writes the refusal itself.
func (h *MusicHandler) visibleSeed(w http.ResponseWriter, r *http.Request, claims *auth.Claims, id uuid.UUID, types ...string) (gen.GetMediaItemRow, bool) {
	item, err := h.db.GetMediaItem(r.Context(), id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			respond.NotFound(w, r)
			return item, false
		}
		h.logger.ErrorContext(r.Context(), "music: get item", "id", id, "err", err)
		respond.InternalError(w, r)
		return item, false
	}
	typeOK := false
	for _, t := range types {
		if item.Type == t {
			typeOK = true
		}
	}
	if item.DeletedAt.Valid || !typeOK {
		respond.NotFound(w, r)
		return item, false
	}
	if h.access != nil {
		ok, aerr := h.access.CanAccessLibrary(r.Context(), claims.UserID, item.LibraryID, claims.IsAdmin)
		if aerr != nil {
			h.logger.ErrorContext(r.Context(), "music: library access", "id", id, "err", aerr)
			respond.InternalError(w, r)
			return item, false
		}
		if !ok {
			respond.NotFound(w, r)
			return item, false
		}
	}
	cr := ""
	if item.ContentRating != nil {
		cr = *item.ContentRating
	}
	if !itemPermitted(r.Context(), claims, item.ID, cr) {
		respond.Forbidden(w, r)
		return item, false
	}
	return item, true
}

// Similar handles GET /api/v1/music/artists/{id}/similar: artists like this
// one from the same library — people who listen to one also listen to the
// other, blended with shared genres; genre and era overlap alone when there
// is no listening history.
func (h *MusicHandler) Similar(w http.ResponseWriter, r *http.Request) {
	claims := middleware.ClaimsFromContext(r.Context())
	if claims == nil {
		respond.Unauthorized(w, r)
		return
	}
	id, err := parseUUID(r, "id")
	if err != nil {
		respond.BadRequest(w, r, "invalid artist id")
		return
	}
	artist, ok := h.visibleSeed(w, r, claims, id, "artist")
	if !ok {
		return
	}
	limit := int(respond.ParseLimit(r, 20, 50))
	ranked, profiles, err := h.similarArtists(r.Context(), r, artist, limit)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "music: similar artists", "id", id, "err", err)
		respond.InternalError(w, r)
		return
	}
	out := make([]SimilarArtistResponse, 0, len(ranked))
	for _, s := range ranked {
		p := profiles[s.ID]
		shared := s.SharedGenres
		if shared == nil {
			shared = []string{}
		}
		out = append(out, SimilarArtistResponse{
			ID:           s.ID.String(),
			Title:        p.Title,
			PosterPath:   p.PosterPath,
			UpdatedAt:    p.UpdatedAt.Time.UnixMilli(),
			Score:        s.Score,
			SharedGenres: shared,
			CoListened:   s.CoListened,
		})
	}
	respond.List(w, r, out, int64(len(out)), "")
}

// similarArtists ranks the artists of the seed artist's library against it.
// Only artists the caller may see (rating ceiling, labels) are candidates.
// The profiles of every visible artist are returned too, by id, for the
// response and the mix's genre pool.
func (h *MusicHandler) similarArtists(ctx context.Context, r *http.Request, artist gen.GetMediaItemRow, limit int) ([]musicrec.SimilarArtist, map[uuid.UUID]gen.ListArtistProfilesRow, error) {
	labels := callerLabels(r)
	rows, err := h.db.ListArtistProfiles(ctx, gen.ListArtistProfilesParams{
		LibraryID:     artist.LibraryID,
		MaxRatingRank: dbconv.IntPtrToInt32Ptr(callerRatingRank(r)),
		BlockedLabels: labels.BlockedArg(),
		AllowedLabels: labels.AllowedArg(),
	})
	if err != nil {
		return nil, nil, err
	}
	profiles := make(map[uuid.UUID]gen.ListArtistProfilesRow, len(rows))
	candidates := make([]musicrec.ArtistProfile, 0, len(rows))
	seed := musicrec.ArtistProfile{ID: artist.ID, Genres: artist.Genres}
	for _, row := range rows {
		profiles[row.ID] = row
		p := musicrec.ArtistProfile{ID: row.ID, Genres: row.Genres, MedianYear: int(row.MedianYear), TrackCount: row.TrackCount}
		if row.ID == artist.ID {
			seed = p
			continue
		}
		candidates = append(candidates, p)
	}
	co, err := h.db.ListArtistCoListens(ctx, gen.ListArtistCoListensParams{ArtistID: artist.ID, LibraryID: artist.LibraryID})
	if err != nil {
		return nil, nil, err
	}
	coMap := make(map[uuid.UUID]int64, len(co))
	for _, c := range co {
		coMap[c.ArtistID] = c.Score
	}
	return musicrec.RankSimilar(seed, candidates, coMap, limit), profiles, nil
}

// AppearsOn handles GET /api/v1/music/artists/{id}/appears-on: tracks on
// other artists' albums this artist is credited on (featured artists, and
// performers of compilation tracks), newest album first.
func (h *MusicHandler) AppearsOn(w http.ResponseWriter, r *http.Request) {
	claims := middleware.ClaimsFromContext(r.Context())
	if claims == nil {
		respond.Unauthorized(w, r)
		return
	}
	id, err := parseUUID(r, "id")
	if err != nil {
		respond.BadRequest(w, r, "invalid artist id")
		return
	}
	if _, ok := h.visibleSeed(w, r, claims, id, "artist"); !ok {
		return
	}
	labels := callerLabels(r)
	rows, err := h.db.ListArtistAppearances(r.Context(), gen.ListArtistAppearancesParams{
		ArtistID:      id,
		MaxRatingRank: dbconv.IntPtrToInt32Ptr(callerRatingRank(r)),
		BlockedLabels: labels.BlockedArg(),
		AllowedLabels: labels.AllowedArg(),
		ResultLimit:   respond.ParseLimit(r, 200, 500),
	})
	if err != nil {
		h.logger.ErrorContext(r.Context(), "music: appears on", "id", id, "err", err)
		respond.InternalError(w, r)
		return
	}
	libOK := h.libraryFilter(r, claims)
	out := make([]ArtistAppearanceResponse, 0, len(rows))
	for _, row := range rows {
		if !libOK(row.LibraryID) {
			continue
		}
		out = append(out, ArtistAppearanceResponse{
			ID:               row.ID.String(),
			Title:            row.Title,
			Index:            row.Index,
			DiscNumber:       row.DiscNumber,
			DurationMS:       row.DurationMs,
			Role:             row.Role,
			AlbumID:          row.AlbumID.String(),
			AlbumTitle:       row.AlbumTitle,
			AlbumYear:        row.AlbumYear,
			AlbumPosterPath:  row.AlbumPosterPath,
			AlbumUpdatedAt:   row.AlbumUpdatedAt.Time.UnixMilli(),
			AlbumArtistID:    pgUUIDString(row.AlbumArtistID),
			AlbumArtistTitle: row.AlbumArtistTitle,
		})
	}
	respond.List(w, r, out, int64(len(out)), "")
}

// libraryFilter returns a per-row library check for listings whose rows
// could come from other libraries. Fails closed on a lookup error.
func (h *MusicHandler) libraryFilter(r *http.Request, claims *auth.Claims) func(uuid.UUID) bool {
	if h.access == nil {
		return func(uuid.UUID) bool { return true }
	}
	allowed, err := h.access.AllowedLibraryIDs(r.Context(), claims.UserID, claims.IsAdmin)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "music: allowed libraries", "err", err)
		return func(uuid.UUID) bool { return false }
	}
	return func(id uuid.UUID) bool {
		if allowed == nil {
			return true
		}
		_, ok := allowed[id]
		return ok
	}
}

// mixRow is the common shape of the three mix track queries.
type mixRow struct {
	resp      MixTrackResponse
	trackID   uuid.UUID
	mixArtist uuid.UUID
	disliked  bool
}

// Mix handles GET /api/v1/music/mix?seed=<track|album|artist id>: an
// instant mix of about limit (default 50) tracks — the seed's own first
// (a seed track itself leads), then tracks by similar artists, then tracks
// sharing the seed artist's genres — with an artist's tracks spaced apart,
// tracks the caller rated one star or less left out, and a fresh shuffle on
// every call.
func (h *MusicHandler) Mix(w http.ResponseWriter, r *http.Request) {
	claims := middleware.ClaimsFromContext(r.Context())
	if claims == nil {
		respond.Unauthorized(w, r)
		return
	}
	seedID, err := uuid.Parse(r.URL.Query().Get("seed"))
	if err != nil {
		respond.BadRequest(w, r, "seed must be a track, album or artist id")
		return
	}
	seed, ok := h.visibleSeed(w, r, claims, seedID, "track", "album", "artist")
	if !ok {
		return
	}
	size := int(respond.ParseLimit(r, mixDefaultSize, mixMaxSize))
	ctx := r.Context()
	rnd := h.rnd()
	maxRank := dbconv.IntPtrToInt32Ptr(callerRatingRank(r))
	labels := callerLabels(r)

	// Resolve the seed's album and artist.
	var albumID, artistID uuid.UUID
	switch seed.Type {
	case "track":
		if seed.ParentID.Valid {
			albumID = seed.ParentID.Bytes
			if album, err := h.db.GetMediaItem(ctx, albumID); err == nil && album.ParentID.Valid {
				artistID = album.ParentID.Bytes
			}
		}
	case "album":
		albumID = seed.ID
		if seed.ParentID.Valid {
			artistID = seed.ParentID.Bytes
		}
	case "artist":
		artistID = seed.ID
	}

	byTrack := map[uuid.UUID]mixRow{}
	keep := func(rows []mixRow) []musicrec.Candidate {
		var out []musicrec.Candidate
		for _, m := range rows {
			if m.disliked && m.trackID != seed.ID {
				continue
			}
			if _, dup := byTrack[m.trackID]; !dup {
				byTrack[m.trackID] = m
			}
			out = append(out, musicrec.Candidate{TrackID: m.trackID, ArtistID: m.mixArtist})
		}
		return out
	}

	// 1. The seed's own tracks: its album's, then its artist's.
	var seedPool []musicrec.Candidate
	var pin *musicrec.Candidate
	if albumID != uuid.Nil {
		rows, err := h.db.ListMixTracksInAlbum(ctx, gen.ListMixTracksInAlbumParams{
			UserID: claims.UserID, AlbumID: pgtype.UUID{Bytes: albumID, Valid: true},
			MaxRatingRank: maxRank, BlockedLabels: labels.BlockedArg(), AllowedLabels: labels.AllowedArg(),
		})
		if err != nil {
			h.mixError(w, r, seedID, err)
			return
		}
		cands := keep(albumMixRows(rows))
		for i, c := range cands {
			if c.TrackID == seed.ID {
				pinned := c
				pin = &pinned
				cands = append(cands[:i:i], cands[i+1:]...)
				break
			}
		}
		seedPool = append(seedPool, musicrec.Shuffle(cands, rnd)...)
	}
	if seed.Type == "track" && pin == nil {
		// The seed track itself didn't pass (no active file, or gated).
		respond.NotFound(w, r)
		return
	}
	if artistID != uuid.Nil {
		rows, err := h.db.ListMixTracksForArtists(ctx, gen.ListMixTracksForArtistsParams{
			UserID: claims.UserID, PerArtist: mixSeedTracks * 2, ArtistIds: []uuid.UUID{artistID},
			MaxRatingRank: maxRank, BlockedLabels: labels.BlockedArg(), AllowedLabels: labels.AllowedArg(),
		})
		if err != nil {
			h.mixError(w, r, seedID, err)
			return
		}
		seedPool = append(seedPool, musicrec.Shuffle(keep(artistMixRows(rows)), rnd)...)
	}
	// An album seed keeps more of its own album; any seed caps its share so
	// the mix is a mix.
	seedPool = firstUnique(seedPool, mixSeedTracks)

	// 2. Similar artists, most similar first in each round.
	var similarPool []musicrec.Candidate
	exclude := []uuid.UUID{}
	var seedGenres []string
	if artistID != uuid.Nil {
		exclude = append(exclude, artistID)
		artist, err := h.db.GetMediaItem(ctx, artistID)
		if err == nil {
			ranked, profiles, err := h.similarArtists(ctx, r, artist, mixSimilarArtists)
			if err != nil {
				h.mixError(w, r, seedID, err)
				return
			}
			if p, ok := profiles[artistID]; ok {
				seedGenres = p.Genres
			}
			ids := make([]uuid.UUID, 0, len(ranked))
			for _, s := range ranked {
				ids = append(ids, s.ID)
				exclude = append(exclude, s.ID)
			}
			if len(ids) > 0 {
				rows, err := h.db.ListMixTracksForArtists(ctx, gen.ListMixTracksForArtistsParams{
					UserID: claims.UserID, PerArtist: mixPerSimilar, ArtistIds: ids,
					MaxRatingRank: maxRank, BlockedLabels: labels.BlockedArg(), AllowedLabels: labels.AllowedArg(),
				})
				if err != nil {
					h.mixError(w, r, seedID, err)
					return
				}
				groups := map[uuid.UUID][]musicrec.Candidate{}
				for _, c := range musicrec.Shuffle(keep(artistMixRows(rows)), rnd) {
					groups[c.ArtistID] = append(groups[c.ArtistID], c)
				}
				ordered := make([][]musicrec.Candidate, 0, len(ids))
				for _, id := range ids {
					ordered = append(ordered, groups[id])
				}
				similarPool = musicrec.RoundRobin(ordered)
			}
		}
	}

	// 3. Genre matches from the rest of the library.
	var genrePool []musicrec.Candidate
	if len(seedGenres) > 0 {
		rows, err := h.db.ListMixTracksByGenres(ctx, gen.ListMixTracksByGenresParams{
			UserID: claims.UserID, LibraryID: seed.LibraryID, Genres: seedGenres, ExcludeArtistIds: exclude,
			MaxRatingRank: maxRank, BlockedLabels: labels.BlockedArg(), AllowedLabels: labels.AllowedArg(),
			ResultLimit: int32(size),
		})
		if err != nil {
			h.mixError(w, r, seedID, err)
			return
		}
		genrePool = keep(genreMixRows(rows))
	}

	mix := musicrec.BuildMix([][]musicrec.Candidate{seedPool, similarPool, genrePool}, size, pin)
	out := make([]MixTrackResponse, 0, len(mix))
	for _, c := range mix {
		out = append(out, byTrack[c.TrackID].resp)
	}
	respond.List(w, r, out, int64(len(out)), "")
}

func (h *MusicHandler) mixError(w http.ResponseWriter, r *http.Request, seed uuid.UUID, err error) {
	h.logger.ErrorContext(r.Context(), "music: instant mix", "seed", seed, "err", err)
	respond.InternalError(w, r)
}

// firstUnique keeps the first n candidates with distinct tracks.
func firstUnique(cs []musicrec.Candidate, n int) []musicrec.Candidate {
	seen := map[uuid.UUID]bool{}
	var out []musicrec.Candidate
	for _, c := range cs {
		if len(out) >= n {
			break
		}
		if seen[c.TrackID] {
			continue
		}
		seen[c.TrackID] = true
		out = append(out, c)
	}
	return out
}

func newMixRow(id uuid.UUID, title string, dur *int64, index *int32, mixArtist, artistID pgtype.UUID,
	albumID uuid.UUID, albumTitle string, albumPoster *string, artistTitle *string, fileID uuid.UUID,
	tg, tp, ag, ap pgtype.Numeric, userScore *float64) mixRow {
	m := mixRow{
		trackID:  id,
		disliked: userScore != nil && *userScore <= mixDislikedScore,
		resp: MixTrackResponse{
			ID:                  id.String(),
			Title:               title,
			DurationMS:          dur,
			Index:               index,
			AlbumID:             albumID.String(),
			AlbumTitle:          albumTitle,
			AlbumPosterPath:     albumPoster,
			ArtistID:            pgUUIDString(artistID),
			ArtistTitle:         artistTitle,
			FileID:              fileID.String(),
			ReplayGainTrackGain: dbconv.NumericToFloat64Ptr(tg),
			ReplayGainTrackPeak: dbconv.NumericToFloat64Ptr(tp),
			ReplayGainAlbumGain: dbconv.NumericToFloat64Ptr(ag),
			ReplayGainAlbumPeak: dbconv.NumericToFloat64Ptr(ap),
		},
	}
	// Spacing goes by the artist the track was picked for; a track found
	// through a featured credit is spaced as that artist's.
	if mixArtist.Valid {
		m.mixArtist = mixArtist.Bytes
	} else if artistID.Valid {
		m.mixArtist = artistID.Bytes
	}
	return m
}

func albumMixRows(rows []gen.ListMixTracksInAlbumRow) []mixRow {
	out := make([]mixRow, 0, len(rows))
	for _, r := range rows {
		out = append(out, newMixRow(r.ID, r.Title, r.DurationMs, r.Index, r.MixArtistID, r.ArtistID, r.AlbumID,
			r.AlbumTitle, r.AlbumPosterPath, r.ArtistTitle, r.FileID, r.ReplaygainTrackGain, r.ReplaygainTrackPeak,
			r.ReplaygainAlbumGain, r.ReplaygainAlbumPeak, r.UserScore))
	}
	return out
}

func artistMixRows(rows []gen.ListMixTracksForArtistsRow) []mixRow {
	out := make([]mixRow, 0, len(rows))
	for _, r := range rows {
		out = append(out, newMixRow(r.ID, r.Title, r.DurationMs, r.Index, r.MixArtistID, r.ArtistID, r.AlbumID,
			r.AlbumTitle, r.AlbumPosterPath, r.ArtistTitle, r.FileID, r.ReplaygainTrackGain, r.ReplaygainTrackPeak,
			r.ReplaygainAlbumGain, r.ReplaygainAlbumPeak, r.UserScore))
	}
	return out
}

func genreMixRows(rows []gen.ListMixTracksByGenresRow) []mixRow {
	out := make([]mixRow, 0, len(rows))
	for _, r := range rows {
		out = append(out, newMixRow(r.ID, r.Title, r.DurationMs, r.Index, r.MixArtistID, r.ArtistID, r.AlbumID,
			r.AlbumTitle, r.AlbumPosterPath, r.ArtistTitle, r.FileID, r.ReplaygainTrackGain, r.ReplaygainTrackPeak,
			r.ReplaygainAlbumGain, r.ReplaygainAlbumPeak, r.UserScore))
	}
	return out
}

// pgUUIDString renders a nullable uuid as *string (nil for NULL).
func pgUUIDString(u pgtype.UUID) *string {
	if !u.Valid {
		return nil
	}
	s := uuid.UUID(u.Bytes).String()
	return &s
}
