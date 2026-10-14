package downloads

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/onscreen/onscreen/internal/db/gen"
	"github.com/onscreen/onscreen/internal/domain/media"
)

// ErrOptimizedNotFound: no such optimized version (or job).
var ErrOptimizedNotFound = errors.New("downloads: optimized version not found")

// MaxOptimizeTargets bounds one "Optimize…" request: a library bigger than
// this is optimized a page at a time (the request reports how many items it
// left out).
const MaxOptimizeTargets = 5000

// OptimizedVersion is one optimized version (or the job producing it) as the
// API shows it.
type OptimizedVersion struct {
	ID          uuid.UUID
	ItemID      uuid.UUID
	FileID      uuid.UUID
	Profile     string
	Status      string
	Progress    float64
	Error       string
	SizeBytes   *int64
	Width       int
	Height      int
	BitrateKbps int
	DurationMS  int64
	CreatedAt   time.Time
	StartedAt   *time.Time
	CompletedAt *time.Time
	RequestedBy *uuid.UUID

	// Display context (list views only).
	ItemTitle    string
	ItemType     string
	LibraryID    uuid.UUID
	ShowTitle    string
	SeasonIndex  *int
	EpisodeIndex *int
}

// OptimizeOutcome says what asking for one item did.
type OptimizeOutcome string

const (
	// OptimizeQueued: a new encode was queued.
	OptimizeQueued OptimizeOutcome = "queued"
	// OptimizeExisting: the item already has this version (queued, encoding
	// or ready).
	OptimizeExisting OptimizeOutcome = "existing"
	// OptimizeSkipped: the item can't be optimized (no video file, damaged,
	// Dolby Vision, …); the reason says why.
	OptimizeSkipped OptimizeOutcome = "skipped"
)

// OptimizeMedia is the media surface the optimize service reads.
type OptimizeMedia interface {
	GetFiles(ctx context.Context, itemID uuid.UUID) ([]media.File, error)
}

// OptimizeService is the API's surface over optimized versions.
type OptimizeService struct {
	q      *gen.Queries
	media  OptimizeMedia
	root   string
	runner *Runner
	logger *slog.Logger
}

// NewOptimizeService builds the service. root is the optimized-versions
// directory (CACHE_PATH/optimized).
func NewOptimizeService(pool *pgxpool.Pool, m OptimizeMedia, root string, logger *slog.Logger) *OptimizeService {
	if logger == nil {
		logger = slog.Default()
	}
	return &OptimizeService{q: gen.New(pool), media: m, root: root, logger: logger}
}

// WithRunner wires the runner new encodes wake and deletes cancel through.
func (s *OptimizeService) WithRunner(r *Runner) *OptimizeService {
	s.runner = r
	return s
}

// Root is the optimized-versions directory.
func (s *OptimizeService) Root() string { return s.root }

// Selection is what an admin asked to optimize: items (movies, shows,
// seasons, episodes — containers expand to their videos), a whole library,
// or a collection.
type Selection struct {
	ItemIDs      []uuid.UUID
	LibraryID    *uuid.UUID
	CollectionID *uuid.UUID
}

// Targets expands a selection into the video items to optimize, at most
// MaxOptimizeTargets of them. truncated reports that the selection held
// more.
func (s *OptimizeService) Targets(ctx context.Context, sel Selection) (ids []uuid.UUID, truncated bool, err error) {
	lim := int32(MaxOptimizeTargets + 1)
	switch {
	case sel.LibraryID != nil:
		ids, err = s.q.ListOptimizeTargetsInLibrary(ctx, gen.ListOptimizeTargetsInLibraryParams{
			LibraryID: *sel.LibraryID, Lim: lim,
		})
	case sel.CollectionID != nil:
		var roots []uuid.UUID
		roots, err = s.collectionRoots(ctx, *sel.CollectionID)
		if err == nil {
			ids, err = s.q.ListOptimizeTargetsUnder(ctx, gen.ListOptimizeTargetsUnderParams{RootIds: roots, Lim: lim})
		}
	default:
		ids, err = s.q.ListOptimizeTargetsUnder(ctx, gen.ListOptimizeTargetsUnderParams{RootIds: sel.ItemIDs, Lim: lim})
	}
	if err != nil {
		return nil, false, err
	}
	if len(ids) > MaxOptimizeTargets {
		return ids[:MaxOptimizeTargets], true, nil
	}
	return ids, false, nil
}

// collectionRoots is a collection's members: its stored items, or for an
// auto-genre collection the movies and shows of that genre.
func (s *OptimizeService) collectionRoots(ctx context.Context, id uuid.UUID) ([]uuid.UUID, error) {
	col, err := s.q.GetCollection(ctx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrOptimizedNotFound
		}
		return nil, err
	}
	if col.Type == "auto_genre" && col.Genre != nil {
		return s.q.ListGenreMemberIDs(ctx, *col.Genre)
	}
	return s.q.ListCollectionMemberIDs(ctx, id)
}

// SourceFile picks the file an item's optimized version encodes: its best
// file, and for a stacked movie the first part (the encode joins them).
// Returns nil when the item has no file.
func SourceFile(files []media.File) *media.File {
	if len(files) == 0 {
		return nil
	}
	f := files[0] // sorted best quality first
	if parts := media.Stack(files, f); parts != nil {
		f = parts[0]
	}
	return &f
}

// RequestItem queues the item's optimized version at profile, or reports the
// live one it already has. An existing version whose source was replaced
// since it was encoded is thrown away and queued afresh. The caller wakes
// the runner (Wake) once after a batch.
func (s *OptimizeService) RequestItem(ctx context.Context, requestedBy, itemID uuid.UUID, profile string) (OptimizeOutcome, string, error) {
	if _, ok := OptimizeProfileByID(profile); !ok {
		return "", "", fmt.Errorf("unknown optimize profile %q", profile)
	}
	files, err := s.media.GetFiles(ctx, itemID)
	if err != nil {
		return "", "", fmt.Errorf("list files: %w", err)
	}
	f := SourceFile(files)
	if f == nil {
		return OptimizeSkipped, "no playable file", nil
	}
	if err := Convertible(f); err != nil {
		return OptimizeSkipped, err.Error(), nil
	}

	live, err := s.q.GetLiveOptimizeConversion(ctx, gen.GetLiveOptimizeConversionParams{
		MediaFileID: f.ID, Quality: profile,
	})
	switch {
	case err == nil:
		if !sourceChanged(live.SourceFileHash, f.FileHash) {
			return OptimizeExisting, "", nil
		}
		s.logger.InfoContext(ctx, "optimize: source replaced since it was encoded; re-encoding",
			"conversion_id", live.ID, "file_id", f.ID)
		if _, err := s.Delete(ctx, live.ID); err != nil && !errors.Is(err, ErrOptimizedNotFound) {
			return "", "", err
		}
	case !errors.Is(err, pgx.ErrNoRows):
		return "", "", fmt.Errorf("find optimized version: %w", err)
	}

	_, err = s.q.CreateOptimizeConversion(ctx, gen.CreateOptimizeConversionParams{
		MediaItemID:    itemID,
		MediaFileID:    f.ID,
		Quality:        profile,
		RequestedBy:    pgtype.UUID{Bytes: requestedBy, Valid: requestedBy != uuid.Nil},
		SourceFileHash: f.FileHash,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return OptimizeExisting, "", nil // a concurrent request won
	}
	if err != nil {
		return "", "", fmt.Errorf("queue optimized version: %w", err)
	}
	return OptimizeQueued, "", nil
}

// sourceChanged reports whether the file's content hash differs from the one
// the version was encoded from. Unknown on either side counts as unchanged.
func sourceChanged(encoded, current *string) bool {
	return encoded != nil && current != nil && *encoded != *current
}

// Wake nudges the runner after new encodes were queued.
func (s *OptimizeService) Wake() {
	if s.runner != nil {
		s.runner.Wake()
	}
}

// ListFilter narrows the admin's list. Zero values mean "any".
type ListFilter struct {
	ItemID    *uuid.UUID
	LibraryID *uuid.UUID
	Status    string
	Limit     int
	Offset    int
}

// List returns optimized versions and jobs, newest first, with per-status
// totals under the same item/library filter.
func (s *OptimizeService) List(ctx context.Context, f ListFilter) ([]OptimizedVersion, map[string]int64, error) {
	lim := f.Limit
	if lim <= 0 || lim > 500 {
		lim = 100
	}
	p := gen.ListOptimizeConversionViewsParams{
		ItemID: uuidArg(f.ItemID), LibraryID: uuidArg(f.LibraryID),
		Lim: int32(lim), Off: int32(max(f.Offset, 0)),
	}
	if f.Status != "" {
		st := f.Status
		p.Status = &st
	}
	rows, err := s.q.ListOptimizeConversionViews(ctx, p)
	if err != nil {
		return nil, nil, err
	}
	out := make([]OptimizedVersion, 0, len(rows))
	for _, r := range rows {
		out = append(out, versionFromView(r))
	}
	counts, err := s.q.CountOptimizeConversionsByStatus(ctx, gen.CountOptimizeConversionsByStatusParams{
		ItemID: p.ItemID, LibraryID: p.LibraryID,
	})
	if err != nil {
		return nil, nil, err
	}
	totals := map[string]int64{}
	for _, c := range counts {
		totals[c.Status] = c.N
	}
	return out, totals, nil
}

// Get reads one optimized version or job.
func (s *OptimizeService) Get(ctx context.Context, id uuid.UUID) (OptimizedVersion, error) {
	row, err := s.q.GetOptimizeConversionView(ctx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return OptimizedVersion{}, ErrOptimizedNotFound
		}
		return OptimizedVersion{}, err
	}
	return versionFromView(gen.ListOptimizeConversionViewsRow(row)), nil
}

// Delete removes an optimized version or cancels its job: the row goes, a
// running encode stops, and the file (finished or partial) is removed.
func (s *OptimizeService) Delete(ctx context.Context, id uuid.UUID) (OptimizedVersion, error) {
	row, err := s.q.DeleteOptimizeConversion(ctx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return OptimizedVersion{}, ErrOptimizedNotFound
		}
		return OptimizedVersion{}, err
	}
	if s.runner != nil {
		s.runner.Cancel(row.ID)
	}
	s.removeFiles(ctx, row.ID)
	return OptimizedVersion{
		ID: row.ID, ItemID: row.MediaItemID, FileID: row.MediaFileID,
		Profile: row.Quality, Status: row.Status,
	}, nil
}

// removeFiles deletes a version's finished and partial output. A file that
// can't be removed now (Windows: open by a viewer) is left for the cleanup
// task's orphan sweep.
func (s *OptimizeService) removeFiles(ctx context.Context, id uuid.UUID) {
	if s.root == "" {
		return
	}
	for _, p := range []string{OutputPath(s.root, id), partPath(s.root, id)} {
		if err := os.Remove(p); err != nil && !errors.Is(err, fs.ErrNotExist) {
			s.logger.WarnContext(ctx, "optimize: remove file", "conversion_id", id, "err", err)
		}
	}
}

// PlayableForItem returns the item's finished optimized versions that still
// match their source and whose file this server has, highest bitrate first.
// The file check keeps the play decision from offering a version the stream
// route would then 404 (removed by hand, or a node without the shared
// CACHE_PATH).
func (s *OptimizeService) PlayableForItem(ctx context.Context, itemID uuid.UUID) ([]OptimizedVersion, error) {
	rows, err := s.q.ListPlayableOptimizedForItem(ctx, itemID)
	if err != nil {
		return nil, err
	}
	out := make([]OptimizedVersion, 0, len(rows))
	for _, r := range rows {
		if s.root == "" {
			break
		}
		if _, err := os.Stat(OutputPath(s.root, r.ID)); err != nil {
			continue
		}
		out = append(out, versionFromConversion(r))
	}
	return out, nil
}

// PlayableFile returns a finished, still-current optimized version and the
// path of its MP4. ErrOptimizedNotFound when there is no such version or its
// file is gone from disk.
func (s *OptimizeService) PlayableFile(ctx context.Context, id uuid.UUID) (OptimizedVersion, string, error) {
	row, err := s.q.GetPlayableOptimizedVersion(ctx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return OptimizedVersion{}, "", ErrOptimizedNotFound
		}
		return OptimizedVersion{}, "", err
	}
	if s.root == "" {
		return OptimizedVersion{}, "", ErrOptimizedNotFound
	}
	p := OutputPath(s.root, row.ID)
	if _, err := os.Stat(p); err != nil {
		return OptimizedVersion{}, "", ErrOptimizedNotFound
	}
	return versionFromConversion(row), p, nil
}

func uuidArg(id *uuid.UUID) pgtype.UUID {
	if id == nil {
		return pgtype.UUID{}
	}
	return pgtype.UUID{Bytes: *id, Valid: true}
}

func clampProgress(p float32) float64 {
	return min(max(float64(p), 0), 1)
}

func timePtr(t pgtype.Timestamptz) *time.Time {
	if !t.Valid {
		return nil
	}
	v := t.Time
	return &v
}

func intOf(p *int32) int {
	if p == nil {
		return 0
	}
	return int(*p)
}

func versionFromConversion(c gen.DownloadConversion) OptimizedVersion {
	v := OptimizedVersion{
		ID: c.ID, ItemID: c.MediaItemID, FileID: c.MediaFileID,
		Profile: c.Quality, Status: c.Status, Progress: clampProgress(c.Progress),
		SizeBytes: c.SizeBytes, Width: intOf(c.Width), Height: intOf(c.Height),
		BitrateKbps: intOf(c.BitrateKbps), CreatedAt: c.CreatedAt.Time,
		StartedAt: timePtr(c.StartedAt), CompletedAt: timePtr(c.CompletedAt),
	}
	if c.Error != nil {
		v.Error = *c.Error
	}
	if c.DurationMs != nil {
		v.DurationMS = *c.DurationMs
	}
	if c.RequestedBy.Valid {
		id := uuid.UUID(c.RequestedBy.Bytes)
		v.RequestedBy = &id
	}
	return v
}

func versionFromView(r gen.ListOptimizeConversionViewsRow) OptimizedVersion {
	v := OptimizedVersion{
		ID: r.ID, ItemID: r.MediaItemID, FileID: r.MediaFileID,
		Profile: r.Quality, Status: r.Status, Progress: clampProgress(r.Progress),
		SizeBytes: r.SizeBytes, Width: intOf(r.Width), Height: intOf(r.Height),
		BitrateKbps: intOf(r.BitrateKbps), CreatedAt: r.CreatedAt.Time,
		StartedAt: timePtr(r.StartedAt), CompletedAt: timePtr(r.CompletedAt),
		ItemTitle: r.ItemTitle, ItemType: r.ItemType, LibraryID: r.LibraryID,
	}
	if r.Error != nil {
		v.Error = *r.Error
	}
	if r.DurationMs != nil {
		v.DurationMS = *r.DurationMs
	}
	if r.RequestedBy.Valid {
		id := uuid.UUID(r.RequestedBy.Bytes)
		v.RequestedBy = &id
	}
	if r.ShowTitle != nil {
		v.ShowTitle = *r.ShowTitle
	}
	if r.SeasonIndex != nil {
		n := int(*r.SeasonIndex)
		v.SeasonIndex = &n
	}
	if r.ItemIndex != nil && r.ItemType == "episode" {
		n := int(*r.ItemIndex)
		v.EpisodeIndex = &n
	}
	return v
}
