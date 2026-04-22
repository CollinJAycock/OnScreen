// Package requests implements the Overseerr-style media-request workflow:
// users ask for a movie or show, admins approve/decline, approved requests
// are forwarded to a configured Radarr/Sonarr instance, and the inbound arr
// webhook flips them to "available" once the file lands.
//
// The service is deliberately the only orchestrator of arr add-flows so
// transitions stay coherent — handlers shouldn't reach for the arr client
// directly.
package requests

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sort"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onscreen/onscreen/internal/arr"
	"github.com/onscreen/onscreen/internal/db/gen"
	"github.com/onscreen/onscreen/internal/metadata"
)

// Errors returned by the service. Handlers translate these to API status
// codes; tests assert on them directly.
var (
	ErrInvalidType        = errors.New("requests: invalid media type")
	ErrInvalidTMDBID      = errors.New("requests: invalid tmdb id")
	ErrAlreadyRequested   = errors.New("requests: an active request already exists for this title")
	ErrTMDBLookupFailed   = errors.New("requests: tmdb lookup failed")
	ErrNotFound           = errors.New("requests: not found")
	ErrNotPending         = errors.New("requests: request is not pending")
	ErrNotOwner           = errors.New("requests: not owned by user")
	ErrNoArrService       = errors.New("requests: no arr service configured for this media type")
	ErrArrServiceMismatch = errors.New("requests: arr service kind does not match request type")
	ErrArrServiceDisabled = errors.New("requests: arr service is disabled")
	ErrArrAddFailed       = errors.New("requests: arr instance rejected the add")
)

// Media types stored in `media_requests.type`. Mirrored on the wire so the
// frontend uses the same strings.
const (
	TypeMovie = "movie"
	TypeShow  = "show"
)

// Status values stored in `media_requests.status`. The CHECK constraint in
// the migration is the source of truth; constants exist so callers don't
// stringly-type the transitions.
const (
	StatusPending     = "pending"
	StatusApproved    = "approved"
	StatusDeclined    = "declined"
	StatusDownloading = "downloading"
	StatusAvailable   = "available"
	StatusFailed      = "failed"
)

// DB is the slice of generated queries the service needs. Defined as an
// interface so tests can substitute an in-memory fake without hitting pgx.
type DB interface {
	// arr_services lookups
	GetArrService(ctx context.Context, id uuid.UUID) (gen.ArrService, error)
	GetDefaultArrServiceByKind(ctx context.Context, kind string) (gen.ArrService, error)

	// requests CRUD + transitions
	CreateMediaRequest(ctx context.Context, arg gen.CreateMediaRequestParams) (gen.MediaRequest, error)
	GetMediaRequest(ctx context.Context, id uuid.UUID) (gen.MediaRequest, error)
	FindActiveRequestForUser(ctx context.Context, arg gen.FindActiveRequestForUserParams) (gen.MediaRequest, error)
	ListMediaRequestsForUser(ctx context.Context, arg gen.ListMediaRequestsForUserParams) ([]gen.MediaRequest, error)
	CountMediaRequestsForUser(ctx context.Context, arg gen.CountMediaRequestsForUserParams) (int64, error)
	ListAllMediaRequests(ctx context.Context, arg gen.ListAllMediaRequestsParams) ([]gen.MediaRequest, error)
	CountAllMediaRequests(ctx context.Context, status *string) (int64, error)
	ListActiveMediaRequestsForTMDB(ctx context.Context, arg gen.ListActiveMediaRequestsForTMDBParams) ([]gen.MediaRequest, error)
	ListMediaItemsByTMDBIDs(ctx context.Context, arg gen.ListMediaItemsByTMDBIDsParams) ([]gen.ListMediaItemsByTMDBIDsRow, error)
	ApproveMediaRequest(ctx context.Context, arg gen.ApproveMediaRequestParams) (gen.MediaRequest, error)
	DeclineMediaRequest(ctx context.Context, arg gen.DeclineMediaRequestParams) (gen.MediaRequest, error)
	MarkMediaRequestDownloading(ctx context.Context, id uuid.UUID) error
	MarkMediaRequestAvailable(ctx context.Context, arg gen.MarkMediaRequestAvailableParams) error
	MarkMediaRequestFailed(ctx context.Context, id uuid.UUID) error
	CancelMediaRequest(ctx context.Context, arg gen.CancelMediaRequestParams) error
	DeleteMediaRequest(ctx context.Context, id uuid.UUID) error
}

// TMDB is the subset of the TMDB agent used to snapshot title/year/poster
// at request time and to resolve a TVDB id for Sonarr.
type TMDB interface {
	RefreshMovie(ctx context.Context, tmdbID int) (*metadata.MovieResult, error)
	RefreshTV(ctx context.Context, tmdbID int) (*metadata.TVShowResult, error)
	GetTVExternalIDs(ctx context.Context, tmdbID int) (tvdbID int, imdbID string, err error)
}

// Notifier is the SSE/notification fan-out used to tell the requester about
// state transitions ("approved", "available", "declined").
type Notifier interface {
	Notify(ctx context.Context, userID uuid.UUID, typ, title, body string, itemID *uuid.UUID)
}

// ArrClientFactory builds an arr.Client for a stored arr_services row.
// Wired as a field so tests can swap in an httptest-backed transport.
type ArrClientFactory func(baseURL, apiKey string) *arr.Client

// Service is the request workflow orchestrator. Safe for concurrent use:
// the underlying DB is concurrent-safe and the service holds no per-request
// state.
type Service struct {
	db        DB
	tmdb      TMDB
	notify    Notifier
	arrClient ArrClientFactory
	logger    *slog.Logger
}

// NewService builds a Service. tmdb may be nil — Create will fail with
// ErrTMDBLookupFailed if invoked, but Approve/Decline still work for
// requests already created.
func NewService(db DB, tmdb TMDB, notify Notifier, logger *slog.Logger) *Service {
	return &Service{
		db:        db,
		tmdb:      tmdb,
		notify:    notify,
		arrClient: arr.New,
		logger:    logger,
	}
}

// SetArrClientFactory overrides the constructor used to build arr transport
// clients. Tests use this to point the service at httptest.Server.
func (s *Service) SetArrClientFactory(f ArrClientFactory) {
	if f != nil {
		s.arrClient = f
	}
}

// CreateInput is the user-supplied portion of a new request. Seasons is
// shows-only; an empty/nil slice means "all seasons".
type CreateInput struct {
	UserID  uuid.UUID
	Type    string
	TMDBID  int
	Seasons []int
	// Optional admin-side overrides at creation. Most users won't set these;
	// the admin UI may pre-pick a service when an admin creates on behalf.
	RequestedServiceID *uuid.UUID
	QualityProfileID   *int32
	RootFolder         *string
}

// Create validates the request, snapshots metadata from TMDB, and inserts a
// pending row. Returns ErrAlreadyRequested if the user has an active request
// for the same title.
func (s *Service) Create(ctx context.Context, in CreateInput) (gen.MediaRequest, error) {
	if in.Type != TypeMovie && in.Type != TypeShow {
		return gen.MediaRequest{}, ErrInvalidType
	}
	if in.TMDBID <= 0 {
		return gen.MediaRequest{}, ErrInvalidTMDBID
	}
	if s.tmdb == nil {
		return gen.MediaRequest{}, ErrTMDBLookupFailed
	}

	// Reject duplicates early. The unique partial index also enforces this
	// at the DB layer, but a clean error message beats a constraint name.
	if _, err := s.db.FindActiveRequestForUser(ctx, gen.FindActiveRequestForUserParams{
		UserID: in.UserID,
		Type:   in.Type,
		TmdbID: int32(in.TMDBID),
	}); err == nil {
		return gen.MediaRequest{}, ErrAlreadyRequested
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return gen.MediaRequest{}, fmt.Errorf("requests: lookup duplicate: %w", err)
	}

	title, year, posterURL, overview, err := s.snapshotMetadata(ctx, in.Type, in.TMDBID)
	if err != nil {
		return gen.MediaRequest{}, err
	}

	seasonsJSON, err := encodeSeasons(in.Seasons)
	if err != nil {
		return gen.MediaRequest{}, fmt.Errorf("requests: encode seasons: %w", err)
	}

	params := gen.CreateMediaRequestParams{
		UserID:             in.UserID,
		Type:               in.Type,
		TmdbID:             int32(in.TMDBID),
		Title:              title,
		Year:               nullableInt32(year),
		PosterUrl:          nullableString(posterURL),
		Overview:           nullableString(overview),
		Status:             StatusPending,
		Seasons:            seasonsJSON,
		RequestedServiceID: pgUUID(in.RequestedServiceID),
		QualityProfileID:   in.QualityProfileID,
		RootFolder:         in.RootFolder,
	}

	req, err := s.db.CreateMediaRequest(ctx, params)
	if err != nil {
		return gen.MediaRequest{}, fmt.Errorf("requests: insert: %w", err)
	}

	s.logger.InfoContext(ctx, "media request created",
		"request_id", req.ID, "user_id", in.UserID, "type", in.Type, "tmdb_id", in.TMDBID, "title", title)

	if s.notify != nil {
		s.notify.Notify(ctx, in.UserID, "request_created",
			"Request submitted",
			fmt.Sprintf("%q is awaiting admin approval.", title),
			nil)
	}
	return req, nil
}

// ApproveInput collects optional overrides for the approve step. Any nil
// field falls back to the request-time selection, then the service default.
type ApproveInput struct {
	RequestID        uuid.UUID
	AdminID          uuid.UUID
	ServiceID        *uuid.UUID
	QualityProfileID *int32
	RootFolder       *string
}

// Approve resolves the destination arr instance, forwards the add via the
// arr client, and transitions the row to approved → downloading. The arr
// add happens inside the same logical step so a partial failure (e.g. arr
// rejects the payload) leaves the request in `pending` for retry instead of
// stranding it in `approved` with nothing on the upstream side.
func (s *Service) Approve(ctx context.Context, in ApproveInput) (gen.MediaRequest, error) {
	req, err := s.db.GetMediaRequest(ctx, in.RequestID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return gen.MediaRequest{}, ErrNotFound
		}
		return gen.MediaRequest{}, fmt.Errorf("requests: get: %w", err)
	}
	if req.Status != StatusPending {
		return gen.MediaRequest{}, ErrNotPending
	}

	svc, err := s.resolveArrService(ctx, req, in.ServiceID)
	if err != nil {
		return gen.MediaRequest{}, err
	}
	qp := firstInt32(in.QualityProfileID, req.QualityProfileID, svc.DefaultQualityProfileID)
	rf := firstString(in.RootFolder, req.RootFolder, svc.DefaultRootFolder)
	tags, _ := decodeTagIDs(svc.DefaultTags)

	if err := s.dispatchToArr(ctx, req, svc, qp, rf, tags); err != nil {
		s.logger.ErrorContext(ctx, "arr add failed",
			"request_id", req.ID, "service_id", svc.ID, "kind", svc.Kind, "err", err)
		return gen.MediaRequest{}, err
	}

	approved, err := s.db.ApproveMediaRequest(ctx, gen.ApproveMediaRequestParams{
		ID:               req.ID,
		ServiceID:        pgUUID(&svc.ID),
		QualityProfileID: qp,
		RootFolder:       rf,
		DecidedBy:        pgUUID(&in.AdminID),
	})
	if err != nil {
		// We already pushed to arr; surface the DB error but the upstream
		// still has the title queued. The next sync will reconcile.
		return gen.MediaRequest{}, fmt.Errorf("requests: approve: %w", err)
	}
	// Most arr instances start a search immediately on add. Roll the row
	// straight to "downloading" so the user sees the right status.
	if err := s.db.MarkMediaRequestDownloading(ctx, approved.ID); err != nil {
		s.logger.WarnContext(ctx, "mark downloading failed", "request_id", approved.ID, "err", err)
	} else {
		approved.Status = StatusDownloading
	}

	s.logger.InfoContext(ctx, "media request approved",
		"request_id", approved.ID, "service_id", svc.ID, "admin_id", in.AdminID)

	if s.notify != nil {
		s.notify.Notify(ctx, approved.UserID, "request_approved",
			"Request approved",
			fmt.Sprintf("%q is being downloaded.", approved.Title),
			nil)
	}
	return approved, nil
}

// Decline transitions a pending request to declined and notifies the
// requester with the supplied reason.
func (s *Service) Decline(ctx context.Context, requestID, adminID uuid.UUID, reason string) (gen.MediaRequest, error) {
	declined, err := s.db.DeclineMediaRequest(ctx, gen.DeclineMediaRequestParams{
		ID:            requestID,
		DeclineReason: nullableString(reason),
		DecidedBy:     pgUUID(&adminID),
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			// Either it doesn't exist or it's no longer pending.
			if _, getErr := s.db.GetMediaRequest(ctx, requestID); errors.Is(getErr, pgx.ErrNoRows) {
				return gen.MediaRequest{}, ErrNotFound
			}
			return gen.MediaRequest{}, ErrNotPending
		}
		return gen.MediaRequest{}, fmt.Errorf("requests: decline: %w", err)
	}

	s.logger.InfoContext(ctx, "media request declined",
		"request_id", declined.ID, "admin_id", adminID, "reason", reason)

	if s.notify != nil {
		body := "Your request was declined."
		if reason != "" {
			body = "Your request was declined: " + reason
		}
		s.notify.Notify(ctx, declined.UserID, "request_declined",
			"Request declined: "+declined.Title, body, nil)
	}
	return declined, nil
}

// Cancel withdraws the user's own pending request. Returns ErrNotFound if
// the row doesn't exist, ErrNotOwner if it belongs to someone else, or
// ErrNotPending if it has already been decided.
func (s *Service) Cancel(ctx context.Context, requestID, userID uuid.UUID) error {
	req, err := s.db.GetMediaRequest(ctx, requestID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		return fmt.Errorf("requests: get: %w", err)
	}
	if req.UserID != userID {
		return ErrNotOwner
	}
	if req.Status != StatusPending {
		return ErrNotPending
	}
	if err := s.db.CancelMediaRequest(ctx, gen.CancelMediaRequestParams{
		ID:     requestID,
		UserID: userID,
	}); err != nil {
		return fmt.Errorf("requests: cancel: %w", err)
	}
	return nil
}

// Delete is the admin escape hatch — wipes the row regardless of status.
// The arr instance is not contacted; admins should remove the title from
// the upstream tool separately if they want to clean up there too.
func (s *Service) Delete(ctx context.Context, requestID uuid.UUID) error {
	if err := s.db.DeleteMediaRequest(ctx, requestID); err != nil {
		return fmt.Errorf("requests: delete: %w", err)
	}
	return nil
}

// ReconcileFulfillments scans every active (approved/downloading) request
// and flips it to available when a matching media_item exists in the
// library. Called after every directory scan so a freshly imported file
// closes out the request without an admin touch, and from the arr webhook
// so re-imports of an already-present title settle immediately.
//
// Cheap by design — bounded by the number of active requests, not the
// library size. Errors on individual rows are logged and skipped so a
// stuck row can't block the rest of the queue.
func (s *Service) ReconcileFulfillments(ctx context.Context) {
	pending := []string{StatusApproved, StatusDownloading}
	var active []gen.MediaRequest
	for _, status := range pending {
		st := status
		rows, err := s.db.ListAllMediaRequests(ctx, gen.ListAllMediaRequestsParams{
			Status: &st,
			Limit:  500,
			Offset: 0,
		})
		if err != nil {
			s.logger.ErrorContext(ctx, "reconcile: list active requests",
				"status", status, "err", err)
			continue
		}
		active = append(active, rows...)
	}
	if len(active) == 0 {
		return
	}

	// Bucket TMDB ids by media type so each type runs one batched library
	// lookup instead of one per request.
	byType := map[string][]int32{}
	for _, r := range active {
		byType[r.Type] = append(byType[r.Type], r.TmdbID)
	}

	// itemByKey: type+tmdb_id → media_item_id of the library row.
	type lookupKey struct {
		mediaType string
		tmdbID    int32
	}
	itemByKey := map[lookupKey]uuid.UUID{}
	for mediaType, ids := range byType {
		rows, err := s.db.ListMediaItemsByTMDBIDs(ctx, gen.ListMediaItemsByTMDBIDsParams{
			Type:    mediaType,
			TmdbIds: ids,
		})
		if err != nil {
			s.logger.ErrorContext(ctx, "reconcile: library lookup",
				"type", mediaType, "err", err)
			continue
		}
		for _, row := range rows {
			if row.TmdbID == nil {
				continue
			}
			itemByKey[lookupKey{mediaType: mediaType, tmdbID: *row.TmdbID}] = row.ID
		}
	}

	fulfilled := 0
	for _, r := range active {
		itemID, ok := itemByKey[lookupKey{mediaType: r.Type, tmdbID: r.TmdbID}]
		if !ok {
			continue
		}
		if err := s.db.MarkMediaRequestAvailable(ctx, gen.MarkMediaRequestAvailableParams{
			ID:              r.ID,
			FulfilledItemID: pgUUID(&itemID),
		}); err != nil {
			s.logger.ErrorContext(ctx, "reconcile: mark available",
				"request_id", r.ID, "err", err)
			continue
		}
		fulfilled++
		if s.notify != nil {
			s.notify.Notify(ctx, r.UserID, "request_available",
				"Now available: "+r.Title,
				fmt.Sprintf("%q is ready to watch.", r.Title),
				&itemID)
		}
	}
	if fulfilled > 0 {
		s.logger.InfoContext(ctx, "reconcile: requests fulfilled",
			"count", fulfilled, "scanned", len(active))
	}
}

// MarkFulfilled is called from the arr webhook handler when a Download
// event arrives. It finds every active request for the (type, tmdb_id)
// pair and flips them to available, attaching the freshly imported media
// item id and notifying each requester.
func (s *Service) MarkFulfilled(ctx context.Context, mediaType string, tmdbID int, itemID uuid.UUID) {
	rows, err := s.db.ListActiveMediaRequestsForTMDB(ctx, gen.ListActiveMediaRequestsForTMDBParams{
		Type:   mediaType,
		TmdbID: int32(tmdbID),
	})
	if err != nil {
		s.logger.ErrorContext(ctx, "list active requests for fulfillment",
			"type", mediaType, "tmdb_id", tmdbID, "err", err)
		return
	}
	if len(rows) == 0 {
		return
	}
	itemPG := pgUUID(&itemID)
	for _, r := range rows {
		if err := s.db.MarkMediaRequestAvailable(ctx, gen.MarkMediaRequestAvailableParams{
			ID:              r.ID,
			FulfilledItemID: itemPG,
		}); err != nil {
			s.logger.ErrorContext(ctx, "mark request available",
				"request_id", r.ID, "err", err)
			continue
		}
		if s.notify != nil {
			s.notify.Notify(ctx, r.UserID, "request_available",
				"Now available: "+r.Title,
				fmt.Sprintf("%q is ready to watch.", r.Title),
				&itemID)
		}
	}
	s.logger.InfoContext(ctx, "media requests marked fulfilled",
		"type", mediaType, "tmdb_id", tmdbID, "count", len(rows))
}

// Get returns a single request by id. ErrNotFound if missing.
func (s *Service) Get(ctx context.Context, id uuid.UUID) (gen.MediaRequest, error) {
	req, err := s.db.GetMediaRequest(ctx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return gen.MediaRequest{}, ErrNotFound
		}
		return gen.MediaRequest{}, fmt.Errorf("requests: get: %w", err)
	}
	return req, nil
}

// ListForUser returns one user's request history with optional status
// filter. Pagination is the caller's responsibility (handlers parse via
// respond.ParsePagination).
func (s *Service) ListForUser(ctx context.Context, userID uuid.UUID, status *string, limit, offset int32) ([]gen.MediaRequest, int64, error) {
	rows, err := s.db.ListMediaRequestsForUser(ctx, gen.ListMediaRequestsForUserParams{
		UserID: userID,
		Status: status,
		Limit:  limit,
		Offset: offset,
	})
	if err != nil {
		return nil, 0, fmt.Errorf("requests: list for user: %w", err)
	}
	total, err := s.db.CountMediaRequestsForUser(ctx, gen.CountMediaRequestsForUserParams{
		UserID: userID,
		Status: status,
	})
	if err != nil {
		return nil, 0, fmt.Errorf("requests: count for user: %w", err)
	}
	return rows, total, nil
}

// ListAll is the admin queue view — every request, optionally filtered by
// status.
func (s *Service) ListAll(ctx context.Context, status *string, limit, offset int32) ([]gen.MediaRequest, int64, error) {
	rows, err := s.db.ListAllMediaRequests(ctx, gen.ListAllMediaRequestsParams{
		Status: status,
		Limit:  limit,
		Offset: offset,
	})
	if err != nil {
		return nil, 0, fmt.Errorf("requests: list all: %w", err)
	}
	total, err := s.db.CountAllMediaRequests(ctx, status)
	if err != nil {
		return nil, 0, fmt.Errorf("requests: count all: %w", err)
	}
	return rows, total, nil
}

// FindActiveForUser is used by the discover/search UI to mark a title as
// "you already requested this." Returns (nil, nil) when none exists so
// callers can branch without error-checking pgx.ErrNoRows.
func (s *Service) FindActiveForUser(ctx context.Context, userID uuid.UUID, mediaType string, tmdbID int) (*gen.MediaRequest, error) {
	r, err := s.db.FindActiveRequestForUser(ctx, gen.FindActiveRequestForUserParams{
		UserID: userID,
		Type:   mediaType,
		TmdbID: int32(tmdbID),
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("requests: find active: %w", err)
	}
	return &r, nil
}

// ── arr dispatch ──────────────────────────────────────────────────────────

func (s *Service) resolveArrService(ctx context.Context, req gen.MediaRequest, override *uuid.UUID) (gen.ArrService, error) {
	wantKind := arrKindForType(req.Type)

	candidate := override
	if candidate == nil && req.RequestedServiceID.Valid {
		id := uuid.UUID(req.RequestedServiceID.Bytes)
		candidate = &id
	}

	if candidate != nil {
		svc, err := s.db.GetArrService(ctx, *candidate)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return gen.ArrService{}, ErrNoArrService
			}
			return gen.ArrService{}, fmt.Errorf("requests: get arr service: %w", err)
		}
		if svc.Kind != wantKind {
			return gen.ArrService{}, ErrArrServiceMismatch
		}
		if !svc.Enabled {
			return gen.ArrService{}, ErrArrServiceDisabled
		}
		return svc, nil
	}

	svc, err := s.db.GetDefaultArrServiceByKind(ctx, wantKind)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return gen.ArrService{}, ErrNoArrService
		}
		return gen.ArrService{}, fmt.Errorf("requests: default arr service: %w", err)
	}
	return svc, nil
}

func (s *Service) dispatchToArr(ctx context.Context, req gen.MediaRequest, svc gen.ArrService, qualityProfileID *int32, rootFolder *string, tags []int32) error {
	if qualityProfileID == nil || *qualityProfileID == 0 {
		return fmt.Errorf("%w: missing quality profile", ErrArrAddFailed)
	}
	if rootFolder == nil || *rootFolder == "" {
		return fmt.Errorf("%w: missing root folder", ErrArrAddFailed)
	}

	client := s.arrClient(svc.BaseUrl, svc.ApiKey)

	switch req.Type {
	case TypeMovie:
		return s.addMovie(ctx, client, req, svc, *qualityProfileID, *rootFolder, tags)
	case TypeShow:
		return s.addSeries(ctx, client, req, svc, *qualityProfileID, *rootFolder, tags)
	default:
		return ErrInvalidType
	}
}

func (s *Service) addMovie(ctx context.Context, client *arr.Client, req gen.MediaRequest, svc gen.ArrService, qp int32, rf string, tags []int32) error {
	lookup, err := client.LookupMovieByTMDB(ctx, int(req.TmdbID))
	if err != nil {
		return fmt.Errorf("%w: lookup: %v", ErrArrAddFailed, err)
	}

	body := arr.AddMovieRequest{
		Title:               lookup.Title,
		OriginalTitle:       lookup.OriginalTitle,
		TMDBID:              lookup.TMDBID,
		Year:                lookup.Year,
		TitleSlug:           lookup.TitleSlug,
		Images:              lookup.Images,
		QualityProfileID:    qp,
		RootFolderPath:      rf,
		Monitored:           true,
		MinimumAvailability: deref(svc.MinimumAvailability),
		Tags:                tags,
		AddOptions: arr.AddMovieOptions{
			SearchForMovie: true,
		},
	}
	if _, err := client.AddMovie(ctx, body); err != nil {
		if errors.Is(err, arr.ErrConflict) {
			// Already managed upstream — treat as success; the existing
			// title will satisfy this request when the next file lands.
			return nil
		}
		return fmt.Errorf("%w: add: %v", ErrArrAddFailed, err)
	}
	return nil
}

func (s *Service) addSeries(ctx context.Context, client *arr.Client, req gen.MediaRequest, svc gen.ArrService, qp int32, rf string, tags []int32) error {
	lookup, err := s.lookupSeries(ctx, client, int(req.TmdbID), req.Title)
	if err != nil {
		return fmt.Errorf("%w: lookup: %v", ErrArrAddFailed, err)
	}

	seasons := buildSeasonSelection(lookup.Seasons, req.Seasons)

	body := arr.AddSeriesRequest{
		Title:             lookup.Title,
		TVDBID:            lookup.TVDBID,
		Year:              lookup.Year,
		TitleSlug:         lookup.TitleSlug,
		Images:            lookup.Images,
		Seasons:           seasons,
		QualityProfileID:  qp,
		LanguageProfileID: derefInt32(svc.LanguageProfileID),
		RootFolderPath:    rf,
		Monitored:         true,
		SeasonFolder:      derefBool(svc.SeasonFolder, true),
		SeriesType:        deref(svc.SeriesType),
		Tags:              tags,
		AddOptions: arr.AddSeriesOptions{
			SearchForMissingEpisodes: true,
			Monitor:                  "all",
		},
	}
	if _, err := client.AddSeries(ctx, body); err != nil {
		if errors.Is(err, arr.ErrConflict) {
			return nil
		}
		return fmt.Errorf("%w: add: %v", ErrArrAddFailed, err)
	}
	return nil
}

// lookupSeries tries TVDB → TMDB → title, in that order. The TMDB lookup
// works only on Sonarr v4+; older instances fall through to title-based
// search, which is good enough when the snapshotted title is reasonable.
func (s *Service) lookupSeries(ctx context.Context, client *arr.Client, tmdbID int, title string) (*arr.SeriesLookup, error) {
	if s.tmdb != nil {
		if tvdbID, _, err := s.tmdb.GetTVExternalIDs(ctx, tmdbID); err == nil && tvdbID > 0 {
			if l, err := client.LookupSeriesByTVDB(ctx, tvdbID); err == nil {
				return l, nil
			}
		}
	}
	if l, err := client.LookupSeriesByTMDB(ctx, tmdbID); err == nil {
		return l, nil
	}
	if title != "" {
		if l, err := client.LookupSeriesByTitle(ctx, title); err == nil {
			return l, nil
		}
	}
	return nil, fmt.Errorf("no sonarr lookup matched (tmdb_id=%d title=%q)", tmdbID, title)
}

// buildSeasonSelection turns a user's "I want seasons 3 and 4" into the
// per-season monitored=true/false flags Sonarr expects. Empty selection
// monitors every season returned by lookup.
func buildSeasonSelection(lookupSeasons []arr.SeriesSeason, requested []byte) []arr.SeriesSeason {
	want, _ := decodeSeasons(requested)
	out := make([]arr.SeriesSeason, len(lookupSeasons))
	wantSet := map[int]bool{}
	for _, n := range want {
		wantSet[n] = true
	}
	for i, s := range lookupSeasons {
		monitored := s.Monitored
		if len(want) > 0 {
			monitored = wantSet[s.SeasonNumber]
		}
		out[i] = arr.SeriesSeason{SeasonNumber: s.SeasonNumber, Monitored: monitored}
	}
	return out
}

// ── metadata snapshot ─────────────────────────────────────────────────────

func (s *Service) snapshotMetadata(ctx context.Context, mediaType string, tmdbID int) (title string, year int, posterURL, overview string, err error) {
	switch mediaType {
	case TypeMovie:
		m, err := s.tmdb.RefreshMovie(ctx, tmdbID)
		if err != nil {
			return "", 0, "", "", fmt.Errorf("%w: %v", ErrTMDBLookupFailed, err)
		}
		return m.Title, m.Year, m.PosterURL, m.Summary, nil
	case TypeShow:
		t, err := s.tmdb.RefreshTV(ctx, tmdbID)
		if err != nil {
			return "", 0, "", "", fmt.Errorf("%w: %v", ErrTMDBLookupFailed, err)
		}
		return t.Title, t.FirstAirYear, t.PosterURL, t.Summary, nil
	}
	return "", 0, "", "", ErrInvalidType
}

// ── helpers ───────────────────────────────────────────────────────────────

func arrKindForType(t string) string {
	if t == TypeMovie {
		return "radarr"
	}
	return "sonarr"
}

func encodeSeasons(seasons []int) ([]byte, error) {
	if len(seasons) == 0 {
		return nil, nil
	}
	sort.Ints(seasons)
	return json.Marshal(seasons)
}

func decodeSeasons(raw []byte) ([]int, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	var out []int
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func decodeTagIDs(raw []byte) ([]int32, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	var out []int32
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func nullableInt32(n int) *int32 {
	if n == 0 {
		return nil
	}
	v := int32(n)
	return &v
}

func nullableString(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func pgUUID(id *uuid.UUID) pgtype.UUID {
	if id == nil {
		return pgtype.UUID{}
	}
	return pgtype.UUID{Bytes: [16]byte(*id), Valid: true}
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func derefInt32(v *int32) int32 {
	if v == nil {
		return 0
	}
	return *v
}

func derefBool(v *bool, fallback bool) bool {
	if v == nil {
		return fallback
	}
	return *v
}

// firstInt32 returns the first non-nil, non-zero pointer it sees.
func firstInt32(values ...*int32) *int32 {
	for _, v := range values {
		if v != nil && *v != 0 {
			return v
		}
	}
	return nil
}

// firstString returns the first non-nil, non-empty pointer it sees.
func firstString(values ...*string) *string {
	for _, v := range values {
		if v != nil && *v != "" {
			return v
		}
	}
	return nil
}
