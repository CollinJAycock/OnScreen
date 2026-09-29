package scrobble

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/onscreen/onscreen/internal/auth"
)

// Store is the Postgres-backed settings + media-metadata source for the
// scrobbler. It satisfies SettingsStore and MediaLookup. Credentials are
// encrypted at rest with the server Encryptor — the same AES-256-GCM scheme
// used for webhook and TOTP secrets, and re-sealed by rotate-key — and
// decrypted only in-process when submitting a play.
//
// The Last.fm and Trakt credentials are sealed with CredentialContext as
// associated data, binding each ciphertext to its column and user so it
// can't be moved into another slot and still open. The ListenBrainz token
// predates that and stays unbound.
type Store struct {
	db  *pgxpool.Pool
	enc *auth.Encryptor
}

// NewStore builds a Store over the read-write pool and the server Encryptor.
func NewStore(db *pgxpool.Pool, enc *auth.Encryptor) *Store {
	return &Store{db: db, enc: enc}
}

var (
	_ SettingsStore = (*Store)(nil)
	_ MediaLookup   = (*Store)(nil)
)

// Credential columns of user_scrobble sealed with CredentialContext.
const (
	ColumnLastFMSessionKey  = "lastfm_session_key"
	ColumnTraktAccessToken  = "trakt_access_token"
	ColumnTraktRefreshToken = "trakt_refresh_token"
)

// BoundColumns lists every CredentialContext-sealed column, for rotate-key.
var BoundColumns = []string{ColumnLastFMSessionKey, ColumnTraktAccessToken, ColumnTraktRefreshToken}

// CredentialContext is the associated data a user_scrobble credential in
// column is sealed with. rotate-key uses it to re-seal the column.
func CredentialContext(column string, userID uuid.UUID) string {
	return "user_scrobble." + column + ":" + userID.String()
}

// Get implements SettingsStore — returns decrypted credentials for dispatch.
// A missing row is "nothing linked", not an error.
func (s *Store) Get(ctx context.Context, userID uuid.UUID) (Settings, error) {
	var lbToken, lfmKey, lfmUser, traktAccess, traktRefresh, traktUser *string
	var lbEnabled bool
	var traktExpires *time.Time
	err := s.db.QueryRow(ctx, `
		SELECT listenbrainz_token, listenbrainz_enabled,
		       lastfm_session_key, lastfm_username,
		       trakt_access_token, trakt_refresh_token, trakt_expires_at, trakt_username
		FROM user_scrobble WHERE user_id = $1`,
		userID).Scan(&lbToken, &lbEnabled, &lfmKey, &lfmUser,
		&traktAccess, &traktRefresh, &traktExpires, &traktUser)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Settings{}, nil
		}
		return Settings{}, fmt.Errorf("scrobble: get settings: %w", err)
	}
	out := Settings{ListenBrainzEnabled: lbEnabled, LastFMUsername: deref(lfmUser)}
	if lbToken != nil && *lbToken != "" {
		token, derr := s.enc.Decrypt(*lbToken)
		if derr != nil {
			return Settings{}, fmt.Errorf("scrobble: decrypt token: %w", derr)
		}
		out.ListenBrainzToken = token
	}
	for _, f := range []struct {
		column string
		enc    *string
		dst    *string
	}{
		{ColumnLastFMSessionKey, lfmKey, &out.LastFMSessionKey},
		{ColumnTraktAccessToken, traktAccess, &out.Trakt.AccessToken},
		{ColumnTraktRefreshToken, traktRefresh, &out.Trakt.RefreshToken},
	} {
		if f.enc == nil || *f.enc == "" {
			continue
		}
		plain, derr := s.enc.DecryptContext(*f.enc, CredentialContext(f.column, userID))
		if derr != nil {
			return Settings{}, fmt.Errorf("scrobble: decrypt %s: %w", f.column, derr)
		}
		*f.dst = plain
	}
	if out.Trakt.AccessToken != "" {
		out.Trakt.Username = deref(traktUser)
		if traktExpires != nil {
			out.Trakt.ExpiresAt = *traktExpires
		}
	}
	return out, nil
}

// SetListenBrainz upserts the user's ListenBrainz token + enabled flag. An
// empty token clears the link (and forces enabled = false, since there's
// nothing to submit with).
func (s *Store) SetListenBrainz(ctx context.Context, userID uuid.UUID, token string, enabled bool) error {
	var stored *string
	if token != "" {
		ct, err := s.enc.Encrypt(token)
		if err != nil {
			return fmt.Errorf("scrobble: encrypt token: %w", err)
		}
		stored = &ct
	} else {
		enabled = false
	}
	_, err := s.db.Exec(ctx,
		`INSERT INTO user_scrobble (user_id, listenbrainz_token, listenbrainz_enabled, updated_at)
		 VALUES ($1, $2, $3, now())
		 ON CONFLICT (user_id) DO UPDATE
		   SET listenbrainz_token   = EXCLUDED.listenbrainz_token,
		       listenbrainz_enabled = EXCLUDED.listenbrainz_enabled,
		       updated_at           = now()`,
		userID, stored, enabled)
	if err != nil {
		return fmt.Errorf("scrobble: upsert settings: %w", err)
	}
	return nil
}

// SetLastFM stores the user's Last.fm session; an empty key unlinks.
func (s *Store) SetLastFM(ctx context.Context, userID uuid.UUID, sessionKey, username string) error {
	stored, err := s.seal(sessionKey, ColumnLastFMSessionKey, userID)
	if err != nil {
		return err
	}
	if sessionKey == "" {
		username = ""
	}
	_, err = s.db.Exec(ctx,
		`INSERT INTO user_scrobble (user_id, lastfm_session_key, lastfm_username, updated_at)
		 VALUES ($1, $2, $3, now())
		 ON CONFLICT (user_id) DO UPDATE
		   SET lastfm_session_key = EXCLUDED.lastfm_session_key,
		       lastfm_username    = EXCLUDED.lastfm_username,
		       updated_at         = now()`,
		userID, stored, nullIfEmpty(username))
	if err != nil {
		return fmt.Errorf("scrobble: upsert last.fm: %w", err)
	}
	return nil
}

// SetTrakt stores the user's Trakt grant; the zero TraktLink unlinks.
func (s *Store) SetTrakt(ctx context.Context, userID uuid.UUID, link TraktLink) error {
	if !link.linked() {
		link = TraktLink{}
	}
	access, err := s.seal(link.AccessToken, ColumnTraktAccessToken, userID)
	if err != nil {
		return err
	}
	refresh, err := s.seal(link.RefreshToken, ColumnTraktRefreshToken, userID)
	if err != nil {
		return err
	}
	var expires *time.Time
	if !link.ExpiresAt.IsZero() {
		expires = &link.ExpiresAt
	}
	_, err = s.db.Exec(ctx,
		`INSERT INTO user_scrobble (user_id, trakt_access_token, trakt_refresh_token, trakt_expires_at, trakt_username, updated_at)
		 VALUES ($1, $2, $3, $4, $5, now())
		 ON CONFLICT (user_id) DO UPDATE
		   SET trakt_access_token  = EXCLUDED.trakt_access_token,
		       trakt_refresh_token = EXCLUDED.trakt_refresh_token,
		       trakt_expires_at    = EXCLUDED.trakt_expires_at,
		       trakt_username      = EXCLUDED.trakt_username,
		       updated_at          = now()`,
		userID, access, refresh, expires, nullIfEmpty(link.Username))
	if err != nil {
		return fmt.Errorf("scrobble: upsert trakt: %w", err)
	}
	return nil
}

// seal encrypts a credential for column, bound to the user; "" stores NULL.
func (s *Store) seal(plain, column string, userID uuid.UUID) (*string, error) {
	if plain == "" {
		return nil, nil
	}
	ct, err := s.enc.EncryptContext(plain, CredentialContext(column, userID))
	if err != nil {
		return nil, fmt.Errorf("scrobble: encrypt credential: %w", err)
	}
	return &ct, nil
}

// Track implements TrackLookup — resolves a music track's scrobble metadata by
// walking media_items track → album (parent) → artist (grandparent). Returns
// ok=false for any non-'track' item so non-music plays are skipped without an
// error.
func (s *Store) Track(ctx context.Context, mediaID uuid.UUID) (Track, bool, error) {
	var t Track
	var recording, release, artist *string
	var index *int32
	var duration *int64
	err := s.db.QueryRow(ctx, `
		SELECT
		    t.title,
		    COALESCE(album.title, ''),
		    COALESCE(artist.title, ''),
		    t.index,
		    t.duration_ms,
		    t.musicbrainz_id::text,
		    t.musicbrainz_release_id::text,
		    t.musicbrainz_artist_id::text
		FROM media_items t
		LEFT JOIN media_items album  ON album.id = t.parent_id
		LEFT JOIN media_items artist ON artist.id = album.parent_id
		WHERE t.id = $1 AND t.type = 'track' AND t.deleted_at IS NULL`,
		mediaID).Scan(&t.TrackName, &t.ReleaseName, &t.ArtistName, &index, &duration,
		&recording, &release, &artist)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Track{}, false, nil
		}
		return Track{}, false, fmt.Errorf("scrobble: track lookup: %w", err)
	}
	if index != nil {
		t.TrackNumber = int(*index)
	}
	if duration != nil {
		t.DurationMS = *duration
	}
	t.RecordingMBID = deref(recording)
	t.ReleaseMBID = deref(release)
	t.ArtistMBID = deref(artist)
	return t, true, nil
}

// videoQuery selects a movie, or an episode walked up to its season (the
// season number) and show (the title and ids Trakt matches), for the ids in
// $1.
const videoQuery = `
	SELECT
	    v.type, v.title, v.year, v.tmdb_id, v.tvdb_id, v.imdb_id, v.duration_ms, v.index,
	    season.index,
	    show.title, show.year, show.tmdb_id, show.tvdb_id, show.imdb_id
	FROM media_items v
	LEFT JOIN media_items season
	       ON season.id = v.parent_id AND season.type = 'season' AND season.deleted_at IS NULL
	LEFT JOIN media_items show
	       ON show.id = season.parent_id AND show.deleted_at IS NULL
	WHERE v.id = ANY($1::uuid[]) AND v.type IN ('movie', 'episode') AND v.deleted_at IS NULL`

// Video implements VideoLookup. An episode without a season and show above
// it, or without numbers, can't be named to Trakt and comes back ok=false.
func (s *Store) Video(ctx context.Context, mediaID uuid.UUID) (Video, bool, error) {
	v, ok, err := scanVideo(s.db.QueryRow(ctx, videoQuery, []uuid.UUID{mediaID}))
	if errors.Is(err, pgx.ErrNoRows) {
		return Video{}, false, nil
	}
	return v, ok, err
}

// Videos implements VideoLookup.
func (s *Store) Videos(ctx context.Context, mediaIDs []uuid.UUID) ([]Video, error) {
	rows, err := s.db.Query(ctx, videoQuery, mediaIDs)
	if err != nil {
		return nil, fmt.Errorf("scrobble: video lookup: %w", err)
	}
	defer rows.Close()
	var out []Video
	for rows.Next() {
		v, ok, err := scanVideo(rows)
		if err != nil {
			return nil, err
		}
		if ok {
			out = append(out, v)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("scrobble: video lookup: %w", err)
	}
	return out, nil
}

// scanVideo reads one videoQuery row. pgx.ErrNoRows is passed through
// unwrapped for Video to tell apart.
func scanVideo(row pgx.Row) (Video, bool, error) {
	var (
		typ, title                   string
		year, tmdb, tvdb, index      *int32
		imdb                         *string
		duration                     *int64
		seasonIndex                  *int32
		showTitle, showIMDB          *string
		showYear, showTMDB, showTVDB *int32
	)
	err := row.Scan(&typ, &title, &year, &tmdb, &tvdb, &imdb, &duration, &index,
		&seasonIndex, &showTitle, &showYear, &showTMDB, &showTVDB, &showIMDB)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Video{}, false, err
		}
		return Video{}, false, fmt.Errorf("scrobble: video lookup: %w", err)
	}
	v := Video{DurationMS: derefInt64(duration)}
	if typ == "movie" {
		v.Title, v.Year = title, derefInt(year)
		v.TMDBID, v.IMDBID = derefInt(tmdb), deref(imdb)
		return v, true, nil
	}
	if showTitle == nil || seasonIndex == nil || index == nil {
		return Video{}, false, nil
	}
	v.Episode = true
	v.Title, v.Year = *showTitle, derefInt(showYear)
	v.TMDBID, v.TVDBID, v.IMDBID = derefInt(showTMDB), derefInt(showTVDB), deref(showIMDB)
	v.Season, v.Number = int(*seasonIndex), int(*index)
	return v, true, nil
}

func deref(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

func derefInt(p *int32) int {
	if p == nil {
		return 0
	}
	return int(*p)
}

func derefInt64(p *int64) int64 {
	if p == nil {
		return 0
	}
	return *p
}

func nullIfEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
