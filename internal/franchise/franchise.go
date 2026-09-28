// Package franchise maintains the auto-generated "franchise" collections:
// TMDB collections (belongs_to_collection — "Alien Collection", "Toy Story
// Collection") surfaced as server-owned collections once the server holds at
// least two different movies of the same TMDB collection.
//
// Sources of truth, all in the database (migration 00027):
//
//   - media_item_tmdb_collections: which TMDB collection each movie belongs
//     to, recorded at enrichment time from the movie-details response
//     (RecordMovie) or by the maintenance task's bounded backfill for movies
//     enriched before this existed (Backfill).
//   - tmdb_collections: a stored snapshot of /collection/{id} (name, art,
//     every part). Fetching it also links any other movie in the server whose
//     TMDB id is one of the parts, which catches films TMDB only grouped into
//     a collection after they were enriched (a sequel appears later).
//   - collections (type='franchise') + collection_items: the materialised
//     result, kept in sync by Sync: created/updated when >= 2 distinct movies
//     are linked, removed when fewer are.
//
// All TMDB traffic goes through the TMDB interface (the configured client and
// its disk cache); the package never builds URLs itself.
package franchise

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/onscreen/onscreen/internal/db/gen"
	"github.com/onscreen/onscreen/internal/metadata"
)

// MinMembers is how many distinct movies of one TMDB collection the server
// must hold before a franchise collection exists for it.
const MinMembers = 2

// SnapshotTTL is how old a stored /collection/{id} snapshot may get before
// the maintenance task re-fetches it (new sequels become requestable parts).
const SnapshotTTL = 7 * 24 * time.Hour

var (
	// ErrNotFound is returned by a TMDB implementation when the requested
	// movie or collection does not exist on TMDB.
	ErrNotFound = errors.New("franchise: not found on TMDB")
	// ErrUnavailable is returned by a TMDB implementation when TMDB can't be
	// asked right now (no key configured, circuit breaker open). Batch work
	// stops on it instead of burning its budget on guaranteed failures.
	ErrUnavailable = errors.New("franchise: TMDB unavailable")
)

// TMDB is the metadata surface the service needs. Implementations map "no
// such id" to ErrNotFound and "can't ask now" to ErrUnavailable.
type TMDB interface {
	// MovieCollection returns the collection a movie belongs to; nil = none.
	MovieCollection(ctx context.Context, movieTMDBID int) (*metadata.CollectionRef, error)
	GetCollection(ctx context.Context, collectionTMDBID int) (*metadata.CollectionDetail, error)
}

// DB is the slice of generated queries the service uses.
type DB interface {
	GetMovieTMDBCollectionLink(ctx context.Context, mediaItemID uuid.UUID) (gen.MediaItemTmdbCollection, error)
	UpsertMovieTMDBCollectionLink(ctx context.Context, arg gen.UpsertMovieTMDBCollectionLinkParams) error
	LinkMoviesToTMDBCollection(ctx context.Context, arg gen.LinkMoviesToTMDBCollectionParams) error
	ListMoviesNeedingTMDBCollectionCheck(ctx context.Context, lim int32) ([]gen.ListMoviesNeedingTMDBCollectionCheckRow, error)
	CountMoviesNeedingTMDBCollectionCheck(ctx context.Context) (int64, error)
	GetTMDBCollectionSnapshot(ctx context.Context, tmdbCollectionID int32) (gen.TmdbCollection, error)
	UpsertTMDBCollectionSnapshot(ctx context.Context, arg gen.UpsertTMDBCollectionSnapshotParams) error
	InsertTMDBCollectionPlaceholder(ctx context.Context, arg gen.InsertTMDBCollectionPlaceholderParams) error
	ListStaleFranchiseSnapshots(ctx context.Context, arg gen.ListStaleFranchiseSnapshotsParams) ([]int32, error)
	CountFranchiseMembers(ctx context.Context, tmdbCollectionID *int32) (int64, error)
	UpsertFranchiseCollection(ctx context.Context, arg gen.UpsertFranchiseCollectionParams) (uuid.UUID, error)
	SyncFranchiseCollectionItems(ctx context.Context, arg gen.SyncFranchiseCollectionItemsParams) error
	DeleteFranchiseCollection(ctx context.Context, tmdbCollectionID *int32) error
	ListFranchiseSyncCandidates(ctx context.Context) ([]int32, error)
}

// Part is one film of a stored collection snapshot (tmdb_collections.parts).
type Part struct {
	TMDBID      int    `json:"tmdb_id"`
	Title       string `json:"title"`
	ReleaseDate string `json:"release_date,omitempty"`
	Year        int    `json:"year,omitempty"`
	PosterURL   string `json:"poster_url,omitempty"`
	Overview    string `json:"overview,omitempty"`
}

// DecodeParts parses a stored parts JSON array. Malformed or empty input
// yields no parts rather than an error — the snapshot is best-effort garnish
// on top of the owned items.
func DecodeParts(raw []byte) []Part {
	if len(raw) == 0 {
		return nil
	}
	var parts []Part
	if json.Unmarshal(raw, &parts) != nil {
		return nil
	}
	return parts
}

// Service keeps franchise collections in sync. Safe for concurrent use.
type Service struct {
	db     DB
	tmdb   func() TMDB
	logger *slog.Logger
	now    func() time.Time

	// locks serialises Sync per TMDB collection within this process, so two
	// sibling movies enriched concurrently don't interleave count → upsert →
	// membership rewrites for the same collection.
	locks sync.Map // map[int32]*sync.Mutex
}

// New builds a Service. tmdbFn is consulted per call and may return nil when
// TMDB isn't configured (the TMDB key is settable at runtime).
func New(db DB, tmdbFn func() TMDB, logger *slog.Logger) *Service {
	if logger == nil {
		logger = slog.Default()
	}
	if tmdbFn == nil {
		tmdbFn = func() TMDB { return nil }
	}
	return &Service{db: db, tmdb: tmdbFn, logger: logger, now: time.Now}
}

func (s *Service) lock(id int32) func() {
	m, _ := s.locks.LoadOrStore(id, &sync.Mutex{})
	mu := m.(*sync.Mutex)
	mu.Lock()
	return mu.Unlock
}

// fetchPolicy says when Sync may call TMDB for the collection snapshot.
type fetchPolicy int

const (
	fetchNever fetchPolicy = iota
	// fetchIfMissing: only when no snapshot is stored (enrichment path).
	fetchIfMissing
	// fetchIfStale: also when the stored snapshot is older than SnapshotTTL.
	fetchIfStale
)

// budget caps TMDB collection fetches within one maintenance run. nil means
// unlimited (the enrichment path only ever fetches missing snapshots).
type budget struct{ remaining int }

func (b *budget) take() bool {
	if b == nil {
		return true
	}
	if b.remaining <= 0 {
		return false
	}
	b.remaining--
	return true
}

// RecordMovie stores which TMDB collection a movie belongs to (ref nil =
// none, as answered by a movie-details response for movieTMDBID) and syncs
// the affected franchise collections: the new one, and the old one when the
// movie moved (re-matched to a different film).
func (s *Service) RecordMovie(ctx context.Context, itemID uuid.UUID, movieTMDBID int, ref *metadata.CollectionRef) error {
	return s.recordMovie(ctx, itemID, movieTMDBID, ref, nil)
}

func (s *Service) recordMovie(ctx context.Context, itemID uuid.UUID, movieTMDBID int, ref *metadata.CollectionRef, b *budget) error {
	if movieTMDBID <= 0 {
		return nil
	}
	var prev *int32
	link, err := s.db.GetMovieTMDBCollectionLink(ctx, itemID)
	switch {
	case err == nil:
		prev = link.TmdbCollectionID
	case errors.Is(err, pgx.ErrNoRows):
	default:
		return fmt.Errorf("franchise: get link: %w", err)
	}

	var next *int32
	if ref != nil && ref.TMDBID > 0 {
		v := int32(ref.TMDBID)
		next = &v
	}
	if err := s.db.UpsertMovieTMDBCollectionLink(ctx, gen.UpsertMovieTMDBCollectionLinkParams{
		MediaItemID:      itemID,
		TmdbID:           int32(movieTMDBID),
		TmdbCollectionID: next,
	}); err != nil {
		return fmt.Errorf("franchise: record link: %w", err)
	}

	var errs []error
	if prev != nil && (next == nil || *prev != *next) {
		if err := s.sync(ctx, *prev, fetchNever, nil, nil); err != nil {
			errs = append(errs, err)
		}
	}
	if next != nil {
		if err := s.sync(ctx, *next, fetchIfMissing, ref, b); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// Sync reconciles one TMDB collection's franchise row with the movies linked
// to it, fetching the TMDB snapshot only when none is stored.
func (s *Service) Sync(ctx context.Context, tmdbCollectionID int32) error {
	return s.sync(ctx, tmdbCollectionID, fetchIfMissing, nil, nil)
}

func (s *Service) sync(ctx context.Context, cid int32, policy fetchPolicy, fallback *metadata.CollectionRef, b *budget) error {
	unlock := s.lock(cid)
	defer unlock()

	snap, haveSnap, err := s.loadSnapshot(ctx, cid)
	if err != nil {
		return err
	}
	needFetch := false
	switch policy {
	case fetchNever:
	case fetchIfMissing:
		needFetch = !haveSnap || isPlaceholder(snap)
	case fetchIfStale:
		needFetch = !haveSnap || s.now().Sub(snap.FetchedAt.Time) > SnapshotTTL
	}
	if needFetch && b.take() {
		fresh, ferr := s.fetchSnapshot(ctx, cid)
		switch {
		case ferr == nil:
			snap, haveSnap = fresh, true
		case errors.Is(ferr, ErrNotFound):
			// The collection is gone from TMDB. Keep whatever membership
			// the per-movie links say; a stored snapshot (if any) stays.
			s.logger.InfoContext(ctx, "franchise: tmdb collection not found", "tmdb_collection_id", cid)
		default:
			// Transient: carry on with what's stored; the maintenance
			// task retries missing/stale snapshots.
			s.logger.WarnContext(ctx, "franchise: fetch collection failed",
				"tmdb_collection_id", cid, "err", ferr)
		}
	}
	if !haveSnap && fallback != nil && fallback.Name != "" {
		// No snapshot and TMDB didn't give one: keep the name/art the
		// movie's belongs_to_collection carried, as a placeholder the next
		// fetch replaces, so the franchise can be named now and on every
		// later sync (which may not have a movie ref to hand).
		ph := gen.InsertTMDBCollectionPlaceholderParams{
			TmdbCollectionID: cid,
			Name:             fallback.Name,
			PosterUrl:        nonEmpty(fallback.PosterURL),
			BackdropUrl:      nonEmpty(fallback.BackdropURL),
		}
		if err := s.db.InsertTMDBCollectionPlaceholder(ctx, ph); err != nil {
			return fmt.Errorf("franchise: store placeholder %d: %w", cid, err)
		}
		snap = gen.TmdbCollection{TmdbCollectionID: cid, Name: ph.Name, PosterUrl: ph.PosterUrl, BackdropUrl: ph.BackdropUrl}
		haveSnap = true
	}

	cidp := &cid
	n, err := s.db.CountFranchiseMembers(ctx, cidp)
	if err != nil {
		return fmt.Errorf("franchise: count members: %w", err)
	}
	if n < MinMembers {
		if err := s.db.DeleteFranchiseCollection(ctx, cidp); err != nil {
			return fmt.Errorf("franchise: delete collection %d: %w", cid, err)
		}
		return nil
	}

	name, desc := "", (*string)(nil)
	if haveSnap {
		name, desc = snap.Name, snap.Overview
	}
	if name == "" {
		// No snapshot and no name to go on (TMDB unreachable on first
		// sight of this collection). Leave it for the next pass rather
		// than creating an unnamed collection.
		return fmt.Errorf("franchise: collection %d has no name yet", cid)
	}
	colID, err := s.db.UpsertFranchiseCollection(ctx, gen.UpsertFranchiseCollectionParams{
		Name:             name,
		Description:      desc,
		TmdbCollectionID: cid,
	})
	if err != nil {
		return fmt.Errorf("franchise: upsert collection %d: %w", cid, err)
	}
	if err := s.db.SyncFranchiseCollectionItems(ctx, gen.SyncFranchiseCollectionItemsParams{
		CollectionID:     colID,
		TmdbCollectionID: cid,
	}); err != nil {
		return fmt.Errorf("franchise: sync items %d: %w", cid, err)
	}
	return nil
}

func (s *Service) loadSnapshot(ctx context.Context, cid int32) (gen.TmdbCollection, bool, error) {
	snap, err := s.db.GetTMDBCollectionSnapshot(ctx, cid)
	if err == nil {
		return snap, true, nil
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return gen.TmdbCollection{}, false, nil
	}
	return gen.TmdbCollection{}, false, fmt.Errorf("franchise: load snapshot %d: %w", cid, err)
}

// fetchSnapshot pulls /collection/{id}, stores it, and links every movie in
// the server that is one of its parts.
func (s *Service) fetchSnapshot(ctx context.Context, cid int32) (gen.TmdbCollection, error) {
	t := s.tmdb()
	if t == nil {
		return gen.TmdbCollection{}, ErrUnavailable
	}
	detail, err := t.GetCollection(ctx, int(cid))
	if err != nil {
		return gen.TmdbCollection{}, err
	}
	if detail == nil || detail.Name == "" {
		return gen.TmdbCollection{}, ErrNotFound
	}
	parts := make([]Part, 0, len(detail.Parts))
	ids := make([]int32, 0, len(detail.Parts))
	for _, p := range detail.Parts {
		if p.TMDBID <= 0 {
			continue
		}
		parts = append(parts, Part{
			TMDBID:      p.TMDBID,
			Title:       p.Title,
			ReleaseDate: p.ReleaseDate,
			Year:        p.Year,
			PosterURL:   p.PosterURL,
			Overview:    p.Overview,
		})
		ids = append(ids, int32(p.TMDBID))
	}
	raw, err := json.Marshal(parts)
	if err != nil {
		return gen.TmdbCollection{}, fmt.Errorf("franchise: encode parts: %w", err)
	}
	params := gen.UpsertTMDBCollectionSnapshotParams{
		TmdbCollectionID: cid,
		Name:             detail.Name,
		Overview:         nonEmpty(detail.Overview),
		PosterUrl:        nonEmpty(detail.PosterURL),
		BackdropUrl:      nonEmpty(detail.BackdropURL),
		Parts:            raw,
	}
	if err := s.db.UpsertTMDBCollectionSnapshot(ctx, params); err != nil {
		return gen.TmdbCollection{}, fmt.Errorf("franchise: store snapshot %d: %w", cid, err)
	}
	if len(ids) > 0 {
		if err := s.db.LinkMoviesToTMDBCollection(ctx, gen.LinkMoviesToTMDBCollectionParams{
			TmdbCollectionID: cid,
			PartTmdbIds:      ids,
		}); err != nil {
			return gen.TmdbCollection{}, fmt.Errorf("franchise: link parts %d: %w", cid, err)
		}
	}
	return gen.TmdbCollection{
		TmdbCollectionID: cid,
		Name:             params.Name,
		Overview:         params.Overview,
		PosterUrl:        params.PosterUrl,
		BackdropUrl:      params.BackdropUrl,
		Parts:            raw,
	}, nil
}

// isPlaceholder reports whether a stored snapshot is the name-only stand-in
// written when the first fetch failed (fetched_at = epoch).
func isPlaceholder(snap gen.TmdbCollection) bool {
	return !snap.FetchedAt.Valid || snap.FetchedAt.Time.Unix() <= 0
}

func nonEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
