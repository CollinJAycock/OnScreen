package staticabr

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/google/uuid"
)

// Popular is a top-played item (already sorted most-played first).
type Popular struct {
	ItemID    uuid.UUID
	PlayCount int
}

// PopularitySource returns the most-played items — production wires it to the
// watch_plays-backed getTopPlayed query.
type PopularitySource interface {
	TopPlayed(ctx context.Context) ([]Popular, error)
}

// Source describes a file to pre-encode: its identity, the media-store key to
// read it from, the current content hash (for invalidation), and the params the
// ladder build needs. The encoder owns ladder construction, so this package
// stays free of the transcode dependency.
type Source struct {
	FileID      uuid.UUID
	FilePath    string // media-store key of the source
	Hash        string
	Width       int
	Height      int
	BitrateKbps int
	Codec       string // "h264" | "hevc" | "av1"
}

// SourceResolver maps a played item to its single encodable source file. Returns
// ok=false when the item has no encodable file to pre-encode (e.g. a show parent,
// a music track, a still-scanning row) so it's quietly skipped.
type SourceResolver interface {
	Resolve(ctx context.Context, itemID uuid.UUID) (src Source, ok bool, err error)
}

// LadderEncoder pre-encodes the full ABR ladder for a source into the media store
// — segments, per-rung + master playlists, and the source.hash sidecar. The real
// implementation builds the ladder (transcode.BuildLadder), runs ffmpeg per rung,
// and Puts the output; tests use a recorder.
type LadderEncoder interface {
	Encode(ctx context.Context, src Source) error
}

// Service orchestrates pre-encoding: pick popular titles, skip those already
// encoded (or unchanged), and dispatch the rest to the encoder. It holds no
// ffmpeg or DB logic itself — all of that is behind the injected interfaces — so
// the selection pipeline is unit-testable.
type Service struct {
	popular  PopularitySource
	resolver SourceResolver
	checker  EncodedChecker
	encoder  LadderEncoder
	logger   *slog.Logger

	minPlays int // ignore titles below this many plays
	limit    int // cap titles pre-encoded per run (0 = no cap)
}

// NewService builds a Service. minPlays gates which titles are worth caching;
// limit bounds fleet load per run (0 = unbounded).
func NewService(popular PopularitySource, resolver SourceResolver, checker EncodedChecker, encoder LadderEncoder, minPlays, limit int, logger *slog.Logger) *Service {
	return &Service{
		popular:  popular,
		resolver: resolver,
		checker:  checker,
		encoder:  encoder,
		logger:   logger,
		minPlays: minPlays,
		limit:    limit,
	}
}

// RunOnce executes one pre-encode pass: resolve the popular set to encodable
// sources, Plan the subset that's missing or stale, and encode each. A failure
// on one title is logged and skipped — the pass still processes the rest.
// Returns a short summary for the scheduler log.
func (s *Service) RunOnce(ctx context.Context) (string, error) {
	popular, err := s.popular.TopPlayed(ctx)
	if err != nil {
		return "", fmt.Errorf("static-abr: top played: %w", err)
	}

	titles := make([]Title, 0, len(popular))
	srcByFile := make(map[uuid.UUID]Source, len(popular))
	for _, p := range popular {
		src, ok, err := s.resolver.Resolve(ctx, p.ItemID)
		if err != nil {
			s.logger.WarnContext(ctx, "static-abr: resolve source", "item_id", p.ItemID, "err", err)
			continue
		}
		if !ok {
			continue // no single encodable file (show parent, music, etc.)
		}
		titles = append(titles, Title{FileID: src.FileID, SourceHash: src.Hash, PlayCount: p.PlayCount})
		srcByFile[src.FileID] = src
	}

	plan, err := Plan(ctx, titles, s.checker, s.minPlays, s.limit)
	if err != nil {
		return "", fmt.Errorf("static-abr: plan: %w", err)
	}

	encoded := 0
	for _, t := range plan {
		if err := s.encoder.Encode(ctx, srcByFile[t.FileID]); err != nil {
			s.logger.WarnContext(ctx, "static-abr: encode", "file_id", t.FileID, "err", err)
			continue
		}
		encoded++
	}
	return fmt.Sprintf("static-abr: %d candidates, %d planned, %d encoded", len(titles), len(plan), encoded), nil
}
