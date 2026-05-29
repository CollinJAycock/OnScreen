package scrobble

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/onscreen/onscreen/internal/auth"
)

// Store is the Postgres-backed settings + track-metadata source for the
// scrobbler. It satisfies both SettingsStore and TrackLookup (consumed by the
// dispatcher) and adds the write/status operations the per-user link API
// needs. Tokens are encrypted at rest with the server Encryptor — the same
// AES-256-GCM scheme used for webhook and TOTP secrets — and decrypted only
// in-process when submitting a listen.
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
	_ TrackLookup   = (*Store)(nil)
)

// Status is the client-safe view of a user's scrobble config. It never carries
// the token itself — only whether one is linked and whether export is on.
type Status struct {
	ListenBrainzLinked  bool
	ListenBrainzEnabled bool
}

// Get implements SettingsStore — returns the decrypted token for dispatch. A
// missing row is "not linked", not an error.
func (s *Store) Get(ctx context.Context, userID uuid.UUID) (Settings, error) {
	var enc *string
	var enabled bool
	err := s.db.QueryRow(ctx,
		`SELECT listenbrainz_token, listenbrainz_enabled FROM user_scrobble WHERE user_id = $1`,
		userID).Scan(&enc, &enabled)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Settings{}, nil
		}
		return Settings{}, fmt.Errorf("scrobble: get settings: %w", err)
	}
	out := Settings{ListenBrainzEnabled: enabled}
	if enc != nil && *enc != "" {
		token, derr := s.enc.Decrypt(*enc)
		if derr != nil {
			return Settings{}, fmt.Errorf("scrobble: decrypt token: %w", derr)
		}
		out.ListenBrainzToken = token
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

// Status returns the client-safe linked/enabled view (never the token).
func (s *Store) Status(ctx context.Context, userID uuid.UUID) (Status, error) {
	var enc *string
	var enabled bool
	err := s.db.QueryRow(ctx,
		`SELECT listenbrainz_token, listenbrainz_enabled FROM user_scrobble WHERE user_id = $1`,
		userID).Scan(&enc, &enabled)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Status{}, nil
		}
		return Status{}, fmt.Errorf("scrobble: status: %w", err)
	}
	return Status{
		ListenBrainzLinked:  enc != nil && *enc != "",
		ListenBrainzEnabled: enabled,
	}, nil
}

// Track implements TrackLookup — resolves a music track's scrobble metadata by
// walking media_items track → album (parent) → artist (grandparent). Returns
// ok=false for any non-'track' item so non-music plays are skipped without an
// error.
func (s *Store) Track(ctx context.Context, mediaID uuid.UUID) (Track, bool, error) {
	var t Track
	var recording, release, artist *string
	err := s.db.QueryRow(ctx, `
		SELECT
		    t.title,
		    COALESCE(album.title, ''),
		    COALESCE(artist.title, ''),
		    t.musicbrainz_id::text,
		    t.musicbrainz_release_id::text,
		    t.musicbrainz_artist_id::text
		FROM media_items t
		LEFT JOIN media_items album  ON album.id = t.parent_id
		LEFT JOIN media_items artist ON artist.id = album.parent_id
		WHERE t.id = $1 AND t.type = 'track' AND t.deleted_at IS NULL`,
		mediaID).Scan(&t.TrackName, &t.ReleaseName, &t.ArtistName, &recording, &release, &artist)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Track{}, false, nil
		}
		return Track{}, false, fmt.Errorf("scrobble: track lookup: %w", err)
	}
	if recording != nil {
		t.RecordingMBID = *recording
	}
	if release != nil {
		t.ReleaseMBID = *release
	}
	if artist != nil {
		t.ArtistMBID = *artist
	}
	return t, true, nil
}
