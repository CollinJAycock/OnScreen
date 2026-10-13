package serverimport

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
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onscreen/onscreen/internal/db/gen"
)

// Querier is the database surface the service needs. Satisfied by
// *gen.Queries on the primary.
type Querier interface {
	CreateImportJob(ctx context.Context, arg gen.CreateImportJobParams) (gen.ImportJob, error)
	GetImportJob(ctx context.Context, id uuid.UUID) (gen.ImportJob, error)
	ListImportJobs(ctx context.Context, limit int32) ([]gen.ImportJob, error)
	UpdateImportJobProgress(ctx context.Context, arg gen.UpdateImportJobProgressParams) error
	FinishImportJob(ctx context.Context, arg gen.FinishImportJobParams) (gen.ImportJob, error)
	FailStaleImportJobs(ctx context.Context, staleBefore pgtype.Timestamptz) (int64, error)

	ListImportCandidateItems(ctx context.Context) ([]gen.ListImportCandidateItemsRow, error)
	ListImportCandidateEpisodes(ctx context.Context) ([]gen.ListImportCandidateEpisodesRow, error)
	ListImportCandidateFiles(ctx context.Context) ([]gen.ListImportCandidateFilesRow, error)

	ImportWatchedMark(ctx context.Context, arg gen.ImportWatchedMarkParams) (int64, error)
	ImportWatchEvent(ctx context.Context, arg gen.ImportWatchEventParams) (int64, error)
	ImportUserRating(ctx context.Context, arg gen.ImportUserRatingParams) (int64, error)
	ImportFavorite(ctx context.Context, arg gen.ImportFavoriteParams) (int64, error)
	FindOrCreateImportedPlaylist(ctx context.Context, arg gen.FindOrCreateImportedPlaylistParams) (gen.FindOrCreateImportedPlaylistRow, error)
	ImportPlaylistItem(ctx context.Context, arg gen.ImportPlaylistItemParams) (int64, error)
}

// PartitionEnsurer creates the watch_events partition for a month, so
// imported plays land in a month partition the retention sweep drops in
// time, not in the default partition it never does.
type PartitionEnsurer func(ctx context.Context, month time.Time) error

// Job statuses.
const (
	StatusRunning   = "running"
	StatusSucceeded = "succeeded"
	StatusFailed    = "failed"
	StatusCancelled = "cancelled"
)

const (
	// heartbeatInterval: how often a running job touches its row.
	heartbeatInterval = 30 * time.Second
	// staleAfter: a running job whose row is older belongs to a server that
	// went away.
	staleAfter = 3 * time.Minute
	// progressInterval throttles progress writes.
	progressInterval = 2 * time.Second
)

// Errors the API maps to client responses.
var (
	ErrImportRunning   = errors.New("an import is already running")
	ErrJobNotFound     = errors.New("import not found")
	ErrJobNotRunning   = errors.New("the import isn't running")
	ErrInvalidKind     = errors.New("the server type must be plex, jellyfin or emby")
	ErrNoToken         = errors.New("a token is required")
	ErrNoMapping       = errors.New("map at least one account to an OnScreen user")
	ErrNothingSelected = errors.New("choose at least one kind of data to import")
	ErrUnknownAccount  = errors.New("an account in the mapping isn't on the source server")
)

// SourceFactory builds a Source. Tests replace it.
type SourceFactory func(kind Kind, baseURL, token string) Source

// DefaultSources builds the real Plex / Jellyfin / Emby readers.
func DefaultSources(kind Kind, baseURL, token string) Source {
	switch kind {
	case KindPlex:
		return NewPlex(baseURL, token)
	case KindEmby:
		return NewEmby(baseURL, token)
	default:
		return NewJellyfin(baseURL, token)
	}
}

// Service runs imports. One runs at a time.
type Service struct {
	bg           context.Context
	q            Querier
	sources      SourceFactory
	partitions   PartitionEnsurer
	retainMonths func() int
	logger       *slog.Logger
	now          func() time.Time

	mu      sync.Mutex
	cancels map[uuid.UUID]context.CancelFunc
	done    sync.WaitGroup
}

// New builds the service. bg bounds every run; retainMonths is the
// watch-history retention in months (imported plays older than that are
// skipped, as the server would drop them).
func New(bg context.Context, q Querier, partitions PartitionEnsurer, retainMonths func() int, logger *slog.Logger) *Service {
	return &Service{
		bg: bg, q: q, sources: DefaultSources, partitions: partitions,
		retainMonths: retainMonths, logger: logger, now: time.Now,
		cancels: map[uuid.UUID]context.CancelFunc{},
	}
}

// WithSources replaces the source factory (tests).
func (s *Service) WithSources(f SourceFactory) *Service {
	s.sources = f
	return s
}

// ConnectParams locate a source server.
type ConnectParams struct {
	Kind  Kind
	URL   string
	Token string
}

func (p *ConnectParams) normalize() error {
	if !p.Kind.Valid() {
		return ErrInvalidKind
	}
	if p.Token == "" {
		return ErrNoToken
	}
	u, err := NormalizeURL(p.URL)
	if err != nil {
		return err
	}
	p.URL = u
	return nil
}

// Connect checks a source and lists its accounts.
func (s *Service) Connect(ctx context.Context, p ConnectParams) (ServerInfo, []Account, error) {
	if err := p.normalize(); err != nil {
		return ServerInfo{}, nil, err
	}
	return s.sources(p.Kind, p.URL, p.Token).Probe(ctx)
}

// AccountMapping sends one source account's data to an OnScreen user.
type AccountMapping struct {
	AccountID   string    `json:"account_id"`
	AccountName string    `json:"account_name"`
	UserID      uuid.UUID `json:"user_id"`
}

// StartParams describe an import.
type StartParams struct {
	ConnectParams
	Mapping   []AccountMapping
	Options   Options
	DryRun    bool
	CreatedBy *uuid.UUID
}

// Start checks the source, records the job and runs it in the background.
func (s *Service) Start(ctx context.Context, p StartParams) (gen.ImportJob, error) {
	if err := p.normalize(); err != nil {
		return gen.ImportJob{}, err
	}
	if !p.Options.any() {
		return gen.ImportJob{}, ErrNothingSelected
	}
	if len(p.Mapping) == 0 {
		return gen.ImportJob{}, ErrNoMapping
	}
	src := s.sources(p.Kind, p.URL, p.Token)
	info, accounts, err := src.Probe(ctx)
	if err != nil {
		return gen.ImportJob{}, err
	}
	names := map[string]string{}
	for _, a := range accounts {
		names[a.ID] = a.Name
	}
	mapping := map[string]uuid.UUID{}
	for i, m := range p.Mapping {
		name, ok := names[m.AccountID]
		if !ok || m.UserID == uuid.Nil {
			return gen.ImportJob{}, ErrUnknownAccount
		}
		p.Mapping[i].AccountName = name
		mapping[m.AccountID] = m.UserID
	}

	if _, err := s.q.FailStaleImportJobs(ctx, pgtype.Timestamptz{Time: s.now().Add(-staleAfter), Valid: true}); err != nil {
		return gen.ImportJob{}, fmt.Errorf("fail stale imports: %w", err)
	}
	opts, _ := json.Marshal(p.Options)
	mapJSON, _ := json.Marshal(p.Mapping)
	params := gen.CreateImportJobParams{
		SourceKind: string(p.Kind),
		SourceUrl:  p.URL,
		DryRun:     p.DryRun,
		Options:    opts,
		Mapping:    mapJSON,
		Phase:      "Starting",
	}
	if info.Name != "" {
		name := info.Name
		params.ServerName = &name
	}
	if p.CreatedBy != nil {
		params.CreatedBy = pgtype.UUID{Bytes: *p.CreatedBy, Valid: true}
	}
	job, err := s.q.CreateImportJob(ctx, params)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return gen.ImportJob{}, ErrImportRunning
		}
		return gen.ImportJob{}, fmt.Errorf("create import job: %w", err)
	}

	runCtx, cancel := context.WithCancel(s.bg)
	s.mu.Lock()
	s.cancels[job.ID] = cancel
	s.mu.Unlock()
	s.done.Add(1)
	go func() {
		defer s.done.Done()
		defer func() {
			s.mu.Lock()
			delete(s.cancels, job.ID)
			s.mu.Unlock()
			cancel()
		}()
		s.run(runCtx, job.ID, p.Kind, src, mapping, p.Options, p.DryRun)
	}()
	return job, nil
}

// Wait blocks until every run started so far has finished (tests).
func (s *Service) Wait() { s.done.Wait() }

// Cancel stops a running import on this server.
func (s *Service) Cancel(ctx context.Context, id uuid.UUID) error {
	s.mu.Lock()
	cancel, ok := s.cancels[id]
	s.mu.Unlock()
	if ok {
		cancel()
		return nil
	}
	job, err := s.Job(ctx, id)
	if err != nil {
		return err
	}
	if job.Status != StatusRunning {
		return ErrJobNotRunning
	}
	// Running on another server node, or left behind by a stopped one: the
	// stale sweep fails it once its heartbeat stops.
	return ErrJobNotRunning
}

// Job returns one import.
func (s *Service) Job(ctx context.Context, id uuid.UUID) (gen.ImportJob, error) {
	job, err := s.q.GetImportJob(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return gen.ImportJob{}, ErrJobNotFound
	}
	return job, err
}

// Jobs lists recent imports, newest first.
func (s *Service) Jobs(ctx context.Context, limit int32) ([]gen.ImportJob, error) {
	if _, err := s.q.FailStaleImportJobs(ctx, pgtype.Timestamptz{Time: s.now().Add(-staleAfter), Valid: true}); err != nil {
		s.logger.WarnContext(ctx, "import: fail stale jobs", "err", err)
	}
	return s.q.ListImportJobs(ctx, limit)
}

// progress publishes a running job's phase and counts, throttled, and keeps
// its heartbeat.
type progress struct {
	s     *Service
	id    uuid.UUID
	mu    sync.Mutex
	phase string
	stats Stats
	last  time.Time
}

func (pr *progress) set(phase string, stats *Stats, force bool) {
	pr.mu.Lock()
	if phase != "" {
		pr.phase = phase
	}
	if stats != nil {
		pr.stats = *stats
	}
	if !force && time.Since(pr.last) < progressInterval {
		pr.mu.Unlock()
		return
	}
	pr.last = time.Now()
	phase, statsJSON := pr.phase, mustJSON(pr.stats)
	pr.mu.Unlock()
	if err := pr.s.q.UpdateImportJobProgress(pr.s.bg, gen.UpdateImportJobProgressParams{
		ID: pr.id, Phase: phase, Stats: statsJSON,
	}); err != nil {
		pr.s.logger.Warn("import: save progress", "job_id", pr.id, "err", err)
	}
}

func (s *Service) run(ctx context.Context, id uuid.UUID, kind Kind, src Source, mapping map[string]uuid.UUID, o Options, dryRun bool) {
	pr := &progress{s: s, id: id}
	stopBeat := make(chan struct{})
	go func() {
		t := time.NewTicker(heartbeatInterval)
		defer t.Stop()
		for {
			select {
			case <-stopBeat:
				return
			case <-t.C:
				pr.set("", nil, true)
			}
		}
	}()
	defer close(stopBeat)

	plan, err := s.execute(ctx, kind, src, mapping, o, dryRun, pr)
	status, phase, errMsg := StatusSucceeded, "Done", ""
	switch {
	case ctx.Err() != nil:
		status, phase = StatusCancelled, "Cancelled"
	case err != nil:
		status, phase, errMsg = StatusFailed, "Failed", err.Error()
	case dryRun:
		phase = "Preview ready"
	}
	var stats Stats
	unmatched := []string{}
	if plan != nil {
		stats, unmatched = plan.Stats, plan.Unmatched
		if unmatched == nil {
			unmatched = []string{}
		}
	}
	fin := gen.FinishImportJobParams{
		ID: id, Status: status, Phase: phase, Stats: mustJSON(stats), Unmatched: mustJSON(unmatched),
	}
	if errMsg != "" {
		fin.Error = &errMsg
	}
	if _, err := s.q.FinishImportJob(s.bg, fin); err != nil {
		s.logger.Error("import: finish job", "job_id", id, "err", err)
	}
	s.logger.Info("import finished", "job_id", id, "source", kind, "status", status, "dry_run", dryRun,
		"matched", stats.Matched, "unmatched", stats.Unmatched, "err", errMsg)
}

// execute reads, matches, plans and (unless dryRun) writes.
func (s *Service) execute(ctx context.Context, kind Kind, src Source, mapping map[string]uuid.UUID, o Options, dryRun bool, pr *progress) (*Plan, error) {
	accounts := make([]string, 0, len(mapping))
	for a := range mapping {
		accounts = append(accounts, a)
	}
	pr.set("Reading the "+kind.Label()+" server", nil, true)
	snap, err := src.Fetch(ctx, accounts, o, func(phase string) { pr.set(phase, nil, false) })
	if err != nil {
		return nil, err
	}

	pr.set("Matching items", nil, true)
	var c Candidates
	if c.Items, err = s.q.ListImportCandidateItems(ctx); err != nil {
		return nil, fmt.Errorf("list items: %w", err)
	}
	if c.Episodes, err = s.q.ListImportCandidateEpisodes(ctx); err != nil {
		return nil, fmt.Errorf("list episodes: %w", err)
	}
	if c.Files, err = s.q.ListImportCandidateFiles(ctx); err != nil {
		return nil, fmt.Errorf("list files: %w", err)
	}
	historyFrom := s.now().AddDate(0, -max(s.retainMonths(), 1), 0)
	plan := BuildPlan(snap, NewIndex(c), mapping, o, historyFrom)
	pr.set("Matched", &plan.Stats, true)
	if dryRun {
		return plan, nil
	}
	if err := s.apply(ctx, plan, kind, pr); err != nil {
		return plan, err
	}
	return plan, nil
}

// apply writes a plan. Counts in plan.Stats.Written are rows that changed:
// a re-run of the same import writes only what's new.
func (s *Service) apply(ctx context.Context, p *Plan, kind Kind, pr *progress) error {
	total, done := p.total(), 0
	w := &p.Stats.Written
	step := func(n int) {
		done += n
		if total > 0 {
			p.Stats.WriteProgress = float64(done) / float64(total)
		}
		pr.set("Writing", &p.Stats, false)
	}
	pr.set("Writing", &p.Stats, true)

	for _, m := range p.Marks {
		if err := ctx.Err(); err != nil {
			return err
		}
		n, err := s.q.ImportWatchedMark(ctx, gen.ImportWatchedMarkParams{
			UserID: m.User, MediaID: m.Media, MarkedAt: pgtype.Timestamptz{Time: m.At, Valid: true},
		})
		if err != nil {
			return fmt.Errorf("write watched mark: %w", err)
		}
		w.Watched += int(n)
		step(1)
	}

	clientID, clientName := "import:"+string(kind), kind.Label()+" (imported)"
	months := map[string]bool{}
	for _, e := range p.Events {
		if err := ctx.Err(); err != nil {
			return err
		}
		month := e.At.UTC().Format("2006-01")
		if !months[month] && s.partitions != nil {
			months[month] = true
			if err := s.partitions(ctx, e.At.UTC()); err != nil {
				// The default partition still takes the rows.
				s.logger.WarnContext(ctx, "import: watch_events partition", "month", month, "err", err)
			}
		}
		d := e.DurationMS
		n, err := s.q.ImportWatchEvent(ctx, gen.ImportWatchEventParams{
			UserID: e.User, MediaID: e.Media, EventType: e.Type, PositionMs: e.PositionMS,
			DurationMs: &d, ClientID: clientID, ClientName: clientName,
			OccurredAt: pgtype.Timestamptz{Time: e.At, Valid: true},
		})
		if err != nil {
			return fmt.Errorf("write watch event: %w", err)
		}
		if e.Type == eventPlay {
			w.Plays += int(n)
		} else {
			w.ResumePoints += int(n)
		}
		step(1)
	}

	for _, r := range p.Ratings {
		if err := ctx.Err(); err != nil {
			return err
		}
		n, err := s.q.ImportUserRating(ctx, gen.ImportUserRatingParams{UserID: r.User, MediaItemID: r.Media, Score: r.Score})
		if err != nil {
			return fmt.Errorf("write rating: %w", err)
		}
		w.Ratings += int(n)
		step(1)
	}

	for _, f := range p.Favorites {
		if err := ctx.Err(); err != nil {
			return err
		}
		n, err := s.q.ImportFavorite(ctx, gen.ImportFavoriteParams{UserID: f.User, MediaID: f.Media})
		if err != nil {
			return fmt.Errorf("write favorite: %w", err)
		}
		w.Favorites += int(n)
		step(1)
	}

	source := "import:" + string(kind)
	for _, pl := range p.Playlists {
		if err := ctx.Err(); err != nil {
			return err
		}
		name, key := pl.Name, pl.SourceKey
		row, err := s.q.FindOrCreateImportedPlaylist(ctx, gen.FindOrCreateImportedPlaylistParams{
			UserID: pgtype.UUID{Bytes: pl.User, Valid: true}, Name: name, Source: &source, SourceKey: &key,
		})
		if err != nil {
			return fmt.Errorf("write playlist: %w", err)
		}
		if row.Created {
			w.Playlists++
		}
		step(1)
		for _, item := range pl.Items {
			n, err := s.q.ImportPlaylistItem(ctx, gen.ImportPlaylistItemParams{CollectionID: row.ID, MediaItemID: item})
			if err != nil {
				return fmt.Errorf("write playlist item: %w", err)
			}
			w.PlaylistItems += int(n)
			step(1)
		}
	}
	p.Stats.WriteProgress = 1
	return nil
}

func mustJSON(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		return []byte("{}")
	}
	return b
}
