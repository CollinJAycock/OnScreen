// Package requests implements the Overseerr-style media-request workflow:
// users ask for a movie or show, admins approve/decline, approved requests
// are forwarded to a configured Radarr/Sonarr instance, and the inbound arr
// webhook flips them to "available" once the file lands. Requests from admins,
// and from users an admin has granted auto-approval for that media type, skip
// the queue and are approved at creation (see Create).
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
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onscreen/onscreen/internal/arr"
	"github.com/onscreen/onscreen/internal/arrcrypt"
	"github.com/onscreen/onscreen/internal/auth"
	"github.com/onscreen/onscreen/internal/db/gen"
	"github.com/onscreen/onscreen/internal/metadata"
	"github.com/onscreen/onscreen/internal/notification"
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
	ErrInvalidSeasons     = errors.New("requests: invalid season list")
	ErrTooManyPending     = errors.New("requests: too many pending requests")
	ErrArrServiceDisabled = errors.New("requests: arr service is disabled")
	ErrArrAddFailed       = errors.New("requests: arr instance rejected the add")
	// ErrRequestsDisabled: an admin turned off this account's ability to
	// request (users.can_request = false). Never returned for an admin.
	ErrRequestsDisabled = errors.New("requests: requesting is disabled for this account")
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

	// requester policy for auto-approval (admin flag, rating ceiling, toggles)
	GetUserRequestPermissions(ctx context.Context, id uuid.UUID) (gen.GetUserRequestPermissionsRow, error)

	// requests CRUD + transitions
	CreateMediaRequest(ctx context.Context, arg gen.CreateMediaRequestParams) (gen.MediaRequest, error)
	GetMediaRequest(ctx context.Context, id uuid.UUID) (gen.MediaRequest, error)
	FindActiveRequestForUser(ctx context.Context, arg gen.FindActiveRequestForUserParams) (gen.MediaRequest, error)
	FindActiveRequestsForUserByTMDB(ctx context.Context, arg gen.FindActiveRequestsForUserByTMDBParams) ([]gen.MediaRequest, error)
	ListMediaRequestsForUser(ctx context.Context, arg gen.ListMediaRequestsForUserParams) ([]gen.MediaRequest, error)
	CountMediaRequestsForUser(ctx context.Context, arg gen.CountMediaRequestsForUserParams) (int64, error)
	// quota usage: non-declined requests of one type since the window start
	CountRecentMediaRequestsForUser(ctx context.Context, arg gen.CountRecentMediaRequestsForUserParams) (int64, error)
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

	// season-level TV requests (migration 00028; see seasons.go)
	ListOwnedEpisodeCountsByShowTMDB(ctx context.Context, arg gen.ListOwnedEpisodeCountsByShowTMDBParams) ([]gen.ListOwnedEpisodeCountsByShowTMDBRow, error)
	MarkMediaRequestSeasonAvailable(ctx context.Context, arg gen.MarkMediaRequestSeasonAvailableParams) (int64, error)

	// live download status (migration 00025; see SyncDownloads)
	ListMediaRequestsForDownloadSync(ctx context.Context, arg gen.ListMediaRequestsForDownloadSyncParams) ([]gen.MediaRequest, error)
	SetMediaRequestArrItem(ctx context.Context, arg gen.SetMediaRequestArrItemParams) error
	UpdateMediaRequestDownload(ctx context.Context, arg gen.UpdateMediaRequestDownloadParams) (int64, error)
	FailMediaRequestDownload(ctx context.Context, arg gen.FailMediaRequestDownloadParams) (int64, error)
	RecoverMediaRequestDownload(ctx context.Context, arg gen.RecoverMediaRequestDownloadParams) (int64, error)
	// requester names for listings
	ListUsernamesByIDs(ctx context.Context, ids []uuid.UUID) ([]gen.ListUsernamesByIDsRow, error)
}

// TMDB is the subset of the TMDB agent used to snapshot title/year/poster
// at request time, to resolve a TVDB id for Sonarr, and to list a show's
// seasons (validating a season request, judging a season complete).
type TMDB interface {
	RefreshMovie(ctx context.Context, tmdbID int) (*metadata.MovieResult, error)
	RefreshTV(ctx context.Context, tmdbID int) (*metadata.TVShowResult, error)
	GetTVExternalIDs(ctx context.Context, tmdbID int) (tvdbID int, imdbID string, err error)
	GetTVSeasons(ctx context.Context, tmdbID int) (*metadata.TVSeasonList, error)
}

// Notifier is the SSE/notification fan-out used to tell the requester about
// state transitions ("approved", "available", "declined") and to alert the
// admins when a request lands in the approval queue. typ is always one of the
// notification.Type* constants.
type Notifier interface {
	Notify(ctx context.Context, userID uuid.UUID, typ, title, body string, itemID *uuid.UUID)
	NotifyAdmins(ctx context.Context, typ, title, body string, itemID *uuid.UUID)
}

// QuotaDefaults are the server-wide request limits (Settings ▸ Requests) that
// apply to a user whose own quota for the type is unset (NULL).
type QuotaDefaults struct {
	// Movies / TV: requests of that type allowed per window; 0 = unlimited.
	Movies int
	TV     int
	// WindowDays: length of the rolling window; <= 0 means
	// DefaultQuotaWindowDays.
	WindowDays int
}

// DefaultQuotaWindowDays is the quota window when none is configured.
const DefaultQuotaWindowDays = 7

// QuotaDefaultsFunc reads the current server defaults. Called per request so
// an admin's change applies immediately.
type QuotaDefaultsFunc func(ctx context.Context) QuotaDefaults

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
	enc       *auth.Encryptor // optional; opens sealed arr_services.api_key
	logger    *slog.Logger
	// quotaDefaults supplies the server-wide quota defaults; nil = unlimited
	// with the default window.
	quotaDefaults QuotaDefaultsFunc
	// now is the clock quota windows are measured against (tests pin it).
	now func() time.Time
	// dlSync serialises download-status syncs and coalesces webhook
	// triggers (see SyncDownloads / TriggerDownloadSync).
	dlSync syncState
	// reconcileMu serialises fulfilment passes (ReconcileFulfillments /
	// MarkFulfilled): a scan completing and an arr webhook can fire them
	// together, and each must see the other's status changes so a request
	// is announced available once.
	reconcileMu sync.Mutex
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
		now:       time.Now,
	}
}

// WithQuotaDefaults wires the server-wide quota defaults (typically read from
// settings). Without it every non-overridden quota is unlimited.
func (s *Service) WithQuotaDefaults(f QuotaDefaultsFunc) *Service {
	s.quotaDefaults = f
	return s
}

// WithEncryptor supplies the key needed to open sealed arr api_keys. Nil is
// valid on a deployment that has never encrypted them; a row written by an
// encryptor-equipped server would then fail to dispatch rather than silently
// sending ciphertext as a credential.
func (s *Service) WithEncryptor(e *auth.Encryptor) *Service {
	s.enc = e
	return s
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

// CreateResult is a newly created request plus what Create decided about it.
// The embedded row is exactly what was stored (and, when auto-approved, its
// post-approval state).
type CreateResult struct {
	gen.MediaRequest
	// OverQuota is true when this request took the requester past their quota
	// for its media type: it was created, but held for an admin instead of
	// being auto-approved.
	OverQuota bool
}

// Create validates the request, snapshots metadata from TMDB, and inserts a
// pending row. Returns ErrAlreadyRequested if the user has an active request
// for the same title — for a show, one whose seasons overlap the requested
// ones (no seasons = all seasons, which overlaps everything), so "request
// more seasons" works for a show already partly requested — ErrInvalidSeasons
// for a season TMDB doesn't list, and ErrRequestsDisabled if an admin has
// switched off the account's ability to request (admins are never blocked).
// A show request counts as one request against the TV quota however many
// seasons it names.
//
// When the requester is eligible for auto-approval (see autoApproveEligible)
// and still within their quota (see overQuota) the new row goes straight
// through the same approve step an admin would run, and the returned request
// is already approved/downloading with AutoApproved set. If that step fails —
// no arr service configured, the service disabled or unreachable, the add
// rejected — the row stays pending in the admin queue exactly as if
// auto-approval were off, the failure is logged at WARN, and Create still
// succeeds: the request was accepted, it just needs a human.
//
// A request that exceeds the quota is still created — the quota decides who
// reviews it, not whether it exists — but always waits for an admin, and the
// result carries OverQuota. Every request left pending notifies the requester
// ("awaiting admin approval") and every admin (request_pending).
func (s *Service) Create(ctx context.Context, in CreateInput) (CreateResult, error) {
	if in.Type != TypeMovie && in.Type != TypeShow {
		return CreateResult{}, ErrInvalidType
	}
	if in.TMDBID <= 0 {
		return CreateResult{}, ErrInvalidTMDBID
	}
	if s.tmdb == nil {
		return CreateResult{}, ErrTMDBLookupFailed
	}
	// Bound the season list (stored as JSON and forwarded to Sonarr).
	if len(in.Seasons) > maxRequestSeasons {
		return CreateResult{}, ErrInvalidSeasons
	}
	for _, sn := range in.Seasons {
		if sn < 0 || sn > maxSeasonNumber {
			return CreateResult{}, ErrInvalidSeasons
		}
	}
	// Seasons only mean something for a show; a sorted, de-duplicated list
	// is what's stored and compared (empty = every season).
	if in.Type == TypeShow {
		in.Seasons = normalizeSeasons(in.Seasons)
	} else {
		in.Seasons = nil
	}

	// The requester's policy (admin flag, can_request, quotas, auto-approve
	// toggles) is read from the users row, never from the caller's token
	// claims, which can lag an admin's change by a token lifetime. A lookup
	// failure is not fatal: the request is accepted but can't be
	// auto-approved — the admin queue is the safe fallback.
	perm, permErr := s.db.GetUserRequestPermissions(ctx, in.UserID)
	if permErr != nil {
		s.logger.WarnContext(ctx, "requester policy lookup failed; request will wait for admin review",
			"user_id", in.UserID, "err", permErr)
	} else if !perm.IsAdmin && !perm.CanRequest {
		return CreateResult{}, ErrRequestsDisabled
	}

	// Per-user ceiling on OPEN requests: each create costs a TMDB lookup and
	// lands in the admin queue, so an unbounded count let one account flood
	// both. Independent of the quota, which a user may exceed (into the queue).
	if pending, err := s.db.CountMediaRequestsForUser(ctx, gen.CountMediaRequestsForUserParams{
		UserID: in.UserID,
		Status: ptrString(StatusPending),
	}); err == nil && pending >= maxPendingRequestsPerUser {
		return CreateResult{}, ErrTooManyPending
	}

	// Reject duplicates early — for a show, only an active request whose
	// seasons overlap these (see checkDuplicate). The unique partial indexes
	// also enforce the movie / all-seasons / identical-seasons part at the DB
	// layer, but a clean error message beats a constraint name.
	if err := s.checkDuplicate(ctx, in); err != nil {
		return CreateResult{}, err
	}

	// Quota: counted before the insert, so "used" excludes this request.
	// A failed count can't prove the user is within quota, so it only costs
	// auto-approval (the request is held for an admin) — it isn't reported
	// as over quota, which the UI would present as the user's own doing.
	overQuota, quotaKnown := false, permErr == nil
	if permErr == nil {
		var qErr error
		overQuota, qErr = s.overQuota(ctx, perm, in.UserID, in.Type)
		if qErr != nil {
			quotaKnown = false
			s.logger.WarnContext(ctx, "request quota count failed; request will wait for admin review",
				"user_id", in.UserID, "type", in.Type, "err", qErr)
		}
	}

	title, year, posterURL, overview, err := s.snapshotMetadata(ctx, in.Type, in.TMDBID)
	if err != nil {
		return CreateResult{}, err
	}
	// Every named season must exist on TMDB (after the snapshot, whose TMDB
	// response the season list shares a cache entry with).
	if in.Type == TypeShow && len(in.Seasons) > 0 {
		if err := s.validateSeasons(ctx, in.TMDBID, in.Seasons); err != nil {
			return CreateResult{}, err
		}
	}

	seasonsJSON, err := encodeSeasons(in.Seasons)
	if err != nil {
		return CreateResult{}, fmt.Errorf("requests: encode seasons: %w", err)
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
		// A concurrent create (a double submit, a client retry) won the race
		// past checkDuplicate: for the same movie or all-seasons show it hit
		// media_requests_unique_active, for the same season set
		// media_requests_unique_active_seasons (migration 00028). Two racing
		// requests for different but overlapping season sets aren't caught
		// here — a btree can't compare sets — and both are created.
		if isUniqueViolation(err) {
			if in.Type == TypeShow && len(in.Seasons) > 0 {
				return CreateResult{}, ErrSeasonsAlreadyRequested
			}
			return CreateResult{}, ErrAlreadyRequested
		}
		return CreateResult{}, fmt.Errorf("requests: insert: %w", err)
	}

	s.logger.InfoContext(ctx, "media request created",
		"request_id", req.ID, "user_id", in.UserID, "type", in.Type, "tmdb_id", in.TMDBID, "title", title,
		"over_quota", overQuota)

	if permErr == nil && quotaKnown && !overQuota && autoApproveEligible(perm, in) {
		// approve sends the requester's "approved automatically" notice itself,
		// so the "awaiting admin approval" ones below are only for the queue path.
		actx, cancel := context.WithTimeout(ctx, autoApproveTimeout)
		approved, err := s.approve(actx, ApproveInput{RequestID: req.ID}, true)
		cancel()
		if err == nil {
			return CreateResult{MediaRequest: approved}, nil
		}
		s.logger.WarnContext(ctx, "auto-approve failed; request left pending for admin review",
			"request_id", req.ID, "user_id", in.UserID, "type", in.Type, "tmdb_id", in.TMDBID, "err", err)
	}

	if s.notify != nil {
		body := fmt.Sprintf("%q is awaiting admin approval.", title)
		if overQuota {
			body = fmt.Sprintf("%q is over your %s request limit and is awaiting admin approval.", title, typeNoun(in.Type))
		}
		s.notify.Notify(ctx, in.UserID, notification.TypeRequestCreated, "Request submitted", body, nil)

		requester := "A user"
		if permErr == nil && perm.Username != "" {
			requester = perm.Username
		}
		adminBody := fmt.Sprintf("%s requested %q", requester, titleWithYear(title, year))
		if overQuota {
			adminBody += fmt.Sprintf(" (over their %s quota)", typeNoun(in.Type))
		}
		s.notify.NotifyAdmins(ctx, notification.TypeRequestPending, "New request", adminBody, nil)
	}
	return CreateResult{MediaRequest: req, OverQuota: overQuota}, nil
}

// overQuota reports whether one more request of mediaType would take the user
// past their limit for the current window. Admins and unlimited (0) quotas
// are never over.
func (s *Service) overQuota(ctx context.Context, perm gen.GetUserRequestPermissionsRow, userID uuid.UUID, mediaType string) (bool, error) {
	if perm.IsAdmin {
		return false, nil
	}
	d := s.defaults(ctx)
	limit := quotaLimit(perm, mediaType, d)
	if limit == 0 {
		return false, nil
	}
	used, err := s.quotaUsed(ctx, userID, mediaType, d.WindowDays)
	if err != nil {
		return false, err
	}
	return used >= limit, nil
}

// QuotaUsage is one media type's quota for the current window.
type QuotaUsage struct {
	// Limit: requests allowed per window; 0 = unlimited.
	Limit int
	// Used: non-declined requests of the type created within the window.
	Used int
	// Remaining: Limit-Used floored at 0, or nil when unlimited.
	Remaining *int
}

// QuotaStatus is a user's request allowance, as GET /requests/quota reports it.
type QuotaStatus struct {
	CanRequest bool
	WindowDays int
	Movies     QuotaUsage
	TV         QuotaUsage
}

// Quota reports the user's request allowance: whether they may request at
// all, and per media type the limit (their override, else the server
// default), how many they've used in the window, and how many remain. Admins
// are always allowed and unlimited. ErrNotFound for an unknown user.
func (s *Service) Quota(ctx context.Context, userID uuid.UUID) (QuotaStatus, error) {
	perm, err := s.db.GetUserRequestPermissions(ctx, userID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return QuotaStatus{}, ErrNotFound
		}
		return QuotaStatus{}, fmt.Errorf("requests: quota policy: %w", err)
	}
	d := s.defaults(ctx)
	st := QuotaStatus{CanRequest: perm.IsAdmin || perm.CanRequest, WindowDays: d.WindowDays}
	for _, mt := range []string{TypeMovie, TypeShow} {
		used, err := s.quotaUsed(ctx, userID, mt, d.WindowDays)
		if err != nil {
			return QuotaStatus{}, fmt.Errorf("requests: quota usage: %w", err)
		}
		u := QuotaUsage{Used: used}
		if !perm.IsAdmin {
			u.Limit = quotaLimit(perm, mt, d)
		}
		if u.Limit > 0 {
			rem := u.Limit - used
			if rem < 0 {
				rem = 0
			}
			u.Remaining = &rem
		}
		if mt == TypeMovie {
			st.Movies = u
		} else {
			st.TV = u
		}
	}
	return st, nil
}

// CountPending returns the number of requests waiting in the admin queue
// (backs the admin nav badge).
func (s *Service) CountPending(ctx context.Context) (int64, error) {
	n, err := s.db.CountAllMediaRequests(ctx, ptrString(StatusPending))
	if err != nil {
		return 0, fmt.Errorf("requests: count pending: %w", err)
	}
	return n, nil
}

// defaults returns the normalised server-wide quota defaults.
func (s *Service) defaults(ctx context.Context) QuotaDefaults {
	var d QuotaDefaults
	if s.quotaDefaults != nil {
		d = s.quotaDefaults(ctx)
	}
	if d.WindowDays <= 0 {
		d.WindowDays = DefaultQuotaWindowDays
	}
	if d.Movies < 0 {
		d.Movies = 0
	}
	if d.TV < 0 {
		d.TV = 0
	}
	return d
}

// quotaUsed counts the user's non-declined requests of mediaType created
// within the last windowDays days.
func (s *Service) quotaUsed(ctx context.Context, userID uuid.UUID, mediaType string, windowDays int) (int, error) {
	since := s.now().Add(-time.Duration(windowDays) * 24 * time.Hour)
	n, err := s.db.CountRecentMediaRequestsForUser(ctx, gen.CountRecentMediaRequestsForUserParams{
		UserID: userID,
		Type:   mediaType,
		Since:  pgtype.Timestamptz{Time: since, Valid: true},
	})
	if err != nil {
		return 0, err
	}
	return int(n), nil
}

// quotaLimit resolves the per-type limit: the user's own quota when set
// (including 0 = unlimited), otherwise the server default.
func quotaLimit(perm gen.GetUserRequestPermissionsRow, mediaType string, d QuotaDefaults) int {
	override, def := perm.RequestQuotaMovies, d.Movies
	if mediaType == TypeShow {
		override, def = perm.RequestQuotaTv, d.TV
	}
	if override != nil {
		if *override < 0 {
			return 0
		}
		return int(*override)
	}
	return def
}

// typeNoun is the user-facing word for a media type in notices.
func typeNoun(mediaType string) string {
	if mediaType == TypeShow {
		return "TV"
	}
	return "movie"
}

// titleWithYear renders `Heat (1995)`, or just the title when the year is
// unknown.
func titleWithYear(title string, year int) string {
	if year > 0 {
		return fmt.Sprintf("%s (%d)", title, year)
	}
	return title
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

// autoApproveEligible decides whether a just-created request skips the admin
// queue. The requester's policy is read from the users row (by Create), never
// from the caller's token claims or request body, in this order:
//
//  1. Admins are always auto-approved — they are the approvers.
//  2. A content-rating ceiling vetoes auto-approval whatever the toggles say:
//     a capped (typically child) account's requests always get a human look.
//  3. Otherwise the per-type toggle decides (movie → auto_approve_movies,
//     show → auto_approve_tv).
//
// A non-admin request that names a specific arr service also stays queued:
// that choice is only a preference surfaced to the approving admin (see the
// handler's Create), and auto-approving it would let a user route a grab to,
// say, the 4K instance with nobody reviewing it.
//
// Create only asks when the policy lookup succeeded and the request is within
// the user's quota; anything else goes to the queue, the safe fallback.
func autoApproveEligible(perm gen.GetUserRequestPermissionsRow, in CreateInput) bool {
	if perm.IsAdmin {
		return true
	}
	if perm.MaxContentRating != nil && *perm.MaxContentRating != "" {
		return false
	}
	if in.RequestedServiceID != nil {
		return false
	}
	switch in.Type {
	case TypeMovie:
		return perm.AutoApproveMovies
	case TypeShow:
		return perm.AutoApproveTv
	}
	return false
}

// Approve resolves the destination arr instance, forwards the add via the
// arr client, and transitions the row to approved → downloading. The arr
// add happens inside the same logical step so a partial failure (e.g. arr
// rejects the payload) leaves the request in `pending` for retry instead of
// stranding it in `approved` with nothing on the upstream side.
func (s *Service) Approve(ctx context.Context, in ApproveInput) (gen.MediaRequest, error) {
	return s.approve(ctx, in, false)
}

// approve is the body of Approve, shared with Create's auto-approval so both
// resolve the arr service, dispatch and transition identically. auto records
// the decision as automatic: decided_by is left NULL — no admin made the call,
// and naming the requester would read as "approved their own request" — and
// auto_approved is set in the same UPDATE that flips the status, which is what
// tells the row apart from an admin approval. in.AdminID is ignored when auto.
func (s *Service) approve(ctx context.Context, in ApproveInput, auto bool) (gen.MediaRequest, error) {
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

	arrItemID, err := s.dispatchToArr(ctx, req, svc, qp, rf, tags)
	if err != nil {
		// An automatic attempt is logged once, at WARN, by Create — the
		// request just falls back to the admin queue.
		if !auto {
			s.logger.ErrorContext(ctx, "arr add failed",
				"request_id", req.ID, "service_id", svc.ID, "kind", svc.Kind, "err", err)
		}
		return gen.MediaRequest{}, err
	}

	decidedBy := pgUUID(&in.AdminID)
	if auto {
		decidedBy = pgtype.UUID{}
	}
	approved, err := s.db.ApproveMediaRequest(ctx, gen.ApproveMediaRequestParams{
		ID:               req.ID,
		ServiceID:        pgUUID(&svc.ID),
		QualityProfileID: qp,
		RootFolder:       rf,
		DecidedBy:        decidedBy,
		AutoApproved:     auto,
	})
	if err != nil {
		// We already pushed to arr; surface the DB error but the upstream
		// still has the title queued. The next sync will reconcile.
		return gen.MediaRequest{}, fmt.Errorf("requests: approve: %w", err)
	}
	// Link the row to the movie / series the add created (or found already
	// there), so the download sync can match it against the instance's queue.
	// Without an id here the sync resolves it lazily.
	if arrItemID > 0 {
		n := int32(arrItemID)
		if err := s.db.SetMediaRequestArrItem(ctx, gen.SetMediaRequestArrItemParams{ID: approved.ID, ArrItemID: &n}); err != nil {
			s.logger.WarnContext(ctx, "store arr item id failed", "request_id", approved.ID, "err", err)
		} else {
			approved.ArrItemID = &n
		}
	}
	// Most arr instances start a search immediately on add. Roll the row
	// straight to "downloading" so the user sees the right status.
	if err := s.db.MarkMediaRequestDownloading(ctx, approved.ID); err != nil {
		s.logger.WarnContext(ctx, "mark downloading failed", "request_id", approved.ID, "err", err)
	} else {
		approved.Status = StatusDownloading
	}

	if auto {
		s.logger.InfoContext(ctx, "media request auto-approved",
			"request_id", approved.ID, "service_id", svc.ID, "user_id", approved.UserID)
	} else {
		s.logger.InfoContext(ctx, "media request approved",
			"request_id", approved.ID, "service_id", svc.ID, "admin_id", in.AdminID)
	}

	if s.notify != nil {
		body := fmt.Sprintf("%q is being downloaded.", approved.Title)
		if auto {
			body = fmt.Sprintf("%q was approved automatically and is being downloaded.", approved.Title)
		}
		s.notify.Notify(ctx, approved.UserID, notification.TypeRequestApproved, "Request approved", body, nil)
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
		s.notify.Notify(ctx, declined.UserID, notification.TypeRequestDeclined,
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

// ReconcileFulfillments scans every active (approved/downloading) request —
// and every failed one, since a file can still land after a failed download
// (a manual import, a later grab) — and flips it to available when a
// matching media_item exists in the library. Called after every directory scan so a freshly imported file
// closes out the request without an admin touch, and from the arr webhook
// so re-imports of an already-present title settle immediately.
//
// Cheap by design — bounded by the number of active requests, not the
// library size. Errors on individual rows are logged and skipped so a
// stuck row can't block the rest of the queue.
//
// A movie request is fulfilled by the movie being in the library. A show
// request is judged season by season (see settleShowRequests): it turns
// available only once every season it asked for is complete, and each season
// that completes before then is recorded and announced on its own — one
// imported episode no longer closes a whole-series request.
func (s *Service) ReconcileFulfillments(ctx context.Context) {
	s.reconcileMu.Lock()
	defer s.reconcileMu.Unlock()
	ctx, cancel := context.WithTimeout(ctx, reconcileTimeout)
	defer cancel()

	pending := []string{StatusApproved, StatusDownloading, StatusFailed}
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
	var shows []gen.MediaRequest
	showItems := map[int32]uuid.UUID{}
	for _, r := range active {
		itemID, ok := itemByKey[lookupKey{mediaType: r.Type, tmdbID: r.TmdbID}]
		if !ok {
			continue
		}
		if r.Type == TypeShow {
			shows = append(shows, r)
			showItems[r.TmdbID] = itemID
			continue
		}
		if s.markAvailable(ctx, r, itemID) {
			fulfilled++
		}
	}
	fulfilled += s.settleShowRequests(ctx, shows, showItems)
	if fulfilled > 0 {
		s.logger.InfoContext(ctx, "reconcile: requests fulfilled",
			"count", fulfilled, "scanned", len(active))
	}
}

// markAvailable flips one request to available, linked to itemID, and tells
// the requester. Reports whether the row was updated.
func (s *Service) markAvailable(ctx context.Context, r gen.MediaRequest, itemID uuid.UUID) bool {
	if err := s.db.MarkMediaRequestAvailable(ctx, gen.MarkMediaRequestAvailableParams{
		ID:              r.ID,
		FulfilledItemID: pgUUID(&itemID),
	}); err != nil {
		s.logger.ErrorContext(ctx, "mark request available",
			"request_id", r.ID, "err", err)
		return false
	}
	if s.notify != nil {
		s.notify.Notify(ctx, r.UserID, notification.TypeRequestAvailable,
			"Now available: "+r.Title,
			fmt.Sprintf("%q is ready to watch.", r.Title),
			&itemID)
	}
	return true
}

// MarkFulfilled settles the active requests for one (type, tmdb_id) title
// whose media item just landed in the library: movie requests flip to
// available, attaching the item id and notifying each requester; show
// requests are judged season by season exactly as ReconcileFulfillments
// judges them, so a single imported episode can't close a request for
// seasons that haven't arrived.
func (s *Service) MarkFulfilled(ctx context.Context, mediaType string, tmdbID int, itemID uuid.UUID) {
	s.reconcileMu.Lock()
	defer s.reconcileMu.Unlock()
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
	fulfilled := 0
	if mediaType == TypeShow {
		fulfilled = s.settleShowRequests(ctx, rows, map[int32]uuid.UUID{int32(tmdbID): itemID})
	} else {
		for _, r := range rows {
			if s.markAvailable(ctx, r, itemID) {
				fulfilled++
			}
		}
	}
	s.logger.InfoContext(ctx, "media requests marked fulfilled",
		"type", mediaType, "tmdb_id", tmdbID, "count", fulfilled, "active", len(rows))
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

// FindActiveForUserBatch is the batched form of FindActiveForUser used by the
// Discover grid: one query for every result row instead of N. Returns the user's
// active requests whose tmdb_id is in tmdbIDs; the caller keys them by
// (type, tmdb_id) since an id can collide across movie/show.
func (s *Service) FindActiveForUserBatch(ctx context.Context, userID uuid.UUID, tmdbIDs []int) ([]gen.MediaRequest, error) {
	if len(tmdbIDs) == 0 {
		return nil, nil
	}
	ids := make([]int32, len(tmdbIDs))
	for i, id := range tmdbIDs {
		ids[i] = int32(id)
	}
	rows, err := s.db.FindActiveRequestsForUserByTMDB(ctx, gen.FindActiveRequestsForUserByTMDBParams{
		UserID:  userID,
		TmdbIds: ids,
	})
	if err != nil {
		return nil, fmt.Errorf("requests: find active batch: %w", err)
	}
	return rows, nil
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

// dispatchToArr adds the request's title to the arr instance and returns the
// id the instance gave it (the Radarr movie / Sonarr series id). A title the
// instance already manages isn't added again: an existing movie is monitored
// and searched (monitorExistingMovie), an existing series gets the requested
// seasons monitored (monitorExistingSeries), and the existing id is returned.
func (s *Service) dispatchToArr(ctx context.Context, req gen.MediaRequest, svc gen.ArrService, qualityProfileID *int32, rootFolder *string, tags []int32) (int, error) {
	if qualityProfileID == nil || *qualityProfileID == 0 {
		return 0, fmt.Errorf("%w: missing quality profile", ErrArrAddFailed)
	}
	if rootFolder == nil || *rootFolder == "" {
		return 0, fmt.Errorf("%w: missing root folder", ErrArrAddFailed)
	}

	// svc.ApiKey is the stored form; sealed rows must be opened before the key
	// can be used as a credential.
	apiKey, err := arrcrypt.Open(s.enc, svc.ID, svc.ApiKey)
	if err != nil {
		return 0, fmt.Errorf("%w: %v", ErrArrAddFailed, err)
	}
	client := s.arrClient(svc.BaseUrl, apiKey)

	switch req.Type {
	case TypeMovie:
		return s.addMovie(ctx, client, req, svc, *qualityProfileID, *rootFolder, tags)
	case TypeShow:
		return s.addSeries(ctx, client, req, svc, *qualityProfileID, *rootFolder, tags)
	default:
		return 0, ErrInvalidType
	}
}

// addMovie adds the request's movie to Radarr. A movie Radarr already manages
// can't be added again — Radarr refuses with a 400 MovieExistsValidator (409
// on some builds) — which is what re-requesting a title Radarr has, or one
// whose download failed, runs into. So the library is checked first and an
// existing movie is monitored and searched instead (monitorExistingMovie); an
// add refused as a duplicate anyway (added meanwhile, or the check couldn't
// see it) falls back to the same path.
func (s *Service) addMovie(ctx context.Context, client *arr.Client, req gen.MediaRequest, svc gen.ArrService, qp int32, rf string, tags []int32) (int, error) {
	existing, err := client.MovieByTMDB(ctx, int(req.TmdbID))
	switch {
	case err == nil:
		return s.monitorExistingMovie(ctx, client, req, existing.ID)
	case !errors.Is(err, arr.ErrNotFound):
		// Not fatal: the add below either works or reports the real problem.
		s.logger.WarnContext(ctx, "radarr existence check failed; trying the add",
			"request_id", req.ID, "tmdb_id", req.TmdbID, "err", err)
	}

	lookup, err := client.LookupMovieByTMDB(ctx, int(req.TmdbID))
	if err != nil {
		return 0, fmt.Errorf("%w: lookup: %v", ErrArrAddFailed, err)
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
	added, err := client.AddMovie(ctx, body)
	if err != nil {
		if errors.Is(err, arr.ErrConflict) {
			existing, ferr := client.MovieByTMDB(ctx, int(req.TmdbID))
			if ferr != nil {
				return 0, fmt.Errorf("%w: the movie is already in Radarr but could not be found there: %v", ErrArrAddFailed, ferr)
			}
			return s.monitorExistingMovie(ctx, client, req, existing.ID)
		}
		return 0, fmt.Errorf("%w: add: %v", ErrArrAddFailed, err)
	}
	return added.ID, nil
}

// monitorExistingMovie handles a movie request for a movie Radarr already
// manages: it is made monitored and, unless Radarr has its file, searched (see
// arr.Client.MonitorMovie). Returns the Radarr movie id so the request is
// linked to it. A failure to read or update the movie fails the add — the
// request stays pending for an admin rather than being approved with nothing
// changed upstream; a failed search does not (the movie is monitored, and
// Radarr's RSS sync still grabs it).
func (s *Service) monitorExistingMovie(ctx context.Context, client *arr.Client, req gen.MediaRequest, movieID int) (int, error) {
	res, err := client.MonitorMovie(ctx, movieID)
	if err != nil {
		return 0, fmt.Errorf("%w: monitor existing movie: %v", ErrArrAddFailed, err)
	}
	s.logger.InfoContext(ctx, "movie already in radarr; monitored and searched there",
		"request_id", req.ID, "movie_id", movieID,
		"switched_on", res.Monitored, "has_file", res.HasFile, "searched", res.Searched)
	if res.SearchErr != nil {
		s.logger.WarnContext(ctx, "movie search failed after monitoring",
			"request_id", req.ID, "movie_id", movieID, "err", res.SearchErr)
	}
	return movieID, nil
}

// addSeries adds the request's show to Sonarr. A series Sonarr already manages
// can't be added again — Sonarr refuses with a 400 SeriesExistsValidator (409
// on some builds) — which is exactly the "request more seasons" case for a
// show already partly downloaded, and a re-request after a failure. So the
// library is checked first and an existing series gets the requested seasons
// monitored instead (monitorExistingSeries); an add refused as a duplicate
// anyway (added meanwhile, or the check couldn't see it) falls back to the
// same path.
func (s *Service) addSeries(ctx context.Context, client *arr.Client, req gen.MediaRequest, svc gen.ArrService, qp int32, rf string, tags []int32) (int, error) {
	lookup, err := s.lookupSeries(ctx, client, int(req.TmdbID), req.Title)
	if err != nil {
		return 0, fmt.Errorf("%w: lookup: %v", ErrArrAddFailed, err)
	}

	want, _ := decodeSeasons(req.Seasons)
	seriesID, err := existingSeriesID(ctx, client, lookup)
	switch {
	case err == nil:
		return s.monitorExistingSeries(ctx, client, req, seriesID, want)
	case !errors.Is(err, arr.ErrNotFound):
		// Not fatal: the add below either works or reports the real problem.
		s.logger.WarnContext(ctx, "sonarr existence check failed; trying the add",
			"request_id", req.ID, "tvdb_id", lookup.TVDBID, "err", err)
	}

	seasons := buildSeasonSelection(lookup.Seasons, req.Seasons)
	// Sonarr v4's addOptions.monitor overrides the per-season flags ("all"
	// monitors every season), so it's only sent for an all-seasons request.
	// Without it Sonarr monitors exactly the seasons flagged above (and v3,
	// which has no monitor option, always does).
	opts := arr.AddSeriesOptions{SearchForMissingEpisodes: true}
	if len(want) == 0 {
		opts.Monitor = "all"
	}

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
		AddOptions:        opts,
	}
	added, err := client.AddSeries(ctx, body)
	if err != nil {
		if errors.Is(err, arr.ErrConflict) {
			// Sonarr already has the show: switch the requested seasons on
			// there instead — treating the conflict as done would leave the
			// seasons this request is for unmonitored.
			seriesID, ferr := existingSeriesID(ctx, client, lookup)
			if ferr != nil {
				return 0, fmt.Errorf("%w: the series is already in Sonarr but could not be found there: %v", ErrArrAddFailed, ferr)
			}
			return s.monitorExistingSeries(ctx, client, req, seriesID, want)
		}
		return 0, fmt.Errorf("%w: add: %v", ErrArrAddFailed, err)
	}
	return added.ID, nil
}

// existingSeriesID returns the id of the looked-up series in Sonarr's
// library: the id Sonarr put on the lookup result, else the series with the
// lookup's TVDB id. arr.ErrNotFound when Sonarr doesn't manage it (or the
// lookup has no TVDB id to find it by).
func existingSeriesID(ctx context.Context, client *arr.Client, lookup *arr.SeriesLookup) (int, error) {
	if lookup.ID > 0 {
		return lookup.ID, nil
	}
	if lookup.TVDBID <= 0 {
		return 0, arr.ErrNotFound
	}
	series, err := client.SeriesByTVDB(ctx, lookup.TVDBID)
	if err != nil {
		return 0, err
	}
	return series.ID, nil
}

// monitorExistingSeries handles a show request for a series Sonarr already
// manages: the requested seasons (every season but specials, for an
// all-seasons request) are set monitored on the existing series and the newly
// monitored ones are searched. Returns the Sonarr series id so the request is
// linked to it. A failure to update the series fails the add — the request
// stays pending for an admin rather than being approved with nothing changed
// upstream; a failed search does not (the seasons are monitored, and Sonarr's
// RSS sync still grabs them).
func (s *Service) monitorExistingSeries(ctx context.Context, client *arr.Client, req gen.MediaRequest, seriesID int, want []int) (int, error) {
	res, err := client.MonitorSeasons(ctx, seriesID, want)
	if err != nil {
		return 0, fmt.Errorf("%w: monitor seasons on existing series: %v", ErrArrAddFailed, err)
	}
	s.logger.InfoContext(ctx, "series already in sonarr; requested seasons monitored",
		"request_id", req.ID, "series_id", seriesID,
		"monitored", res.Monitored, "searched", res.Searched)
	if len(res.Missing) > 0 {
		s.logger.WarnContext(ctx, "requested seasons not found in sonarr series",
			"request_id", req.ID, "series_id", seriesID, "seasons", res.Missing)
	}
	if res.SearchErr != nil {
		s.logger.WarnContext(ctx, "season search failed after monitoring",
			"request_id", req.ID, "series_id", seriesID, "err", res.SearchErr)
	}
	return seriesID, nil
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

// Request bounds (see Create).
const (
	maxRequestSeasons         = 100
	maxSeasonNumber           = 1000
	maxPendingRequestsPerUser = 25
)

// autoApproveTimeout bounds the arr round-trips an auto-approval adds to the
// requester's POST. Each arr call has its own 15 s client timeout and a show
// lookup can make several, so a slow or black-holed Radarr/Sonarr could
// otherwise hold the request open past the server's 60 s write deadline.
// Running out is just another failure: the row stays pending for an admin.
const autoApproveTimeout = 20 * time.Second

// reconcileTimeout bounds one fulfilment pass. Show requests may ask Sonarr
// and TMDB about their seasons; a black-holed instance must not hold the
// pass (and the reconcile lock) indefinitely.
const reconcileTimeout = 2 * time.Minute

func ptrString(v string) *string { return &v }
