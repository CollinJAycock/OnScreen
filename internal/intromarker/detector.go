package intromarker

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/onscreen/onscreen/internal/db/gen"
	"github.com/onscreen/onscreen/internal/domain/media"
)

// introFingerprintSeconds — how much of each episode we fingerprint when
// searching for a shared intro. Netflix-style intros top out around 90s, but
// we also want enough signal to align noisy tails and cold opens.
const introFingerprintSeconds = 600

// creditsTailSeconds — we only scan the last N seconds of a file for black
// frames. Running blackdetect on a 90-minute file is wasteful and can be
// fooled by mid-show fades.
const creditsTailSeconds = 360

// minCreditsGapSeconds — if black-frames appear within this many seconds of
// the file's end, we assume this is the credits roll and mark from there.
// A black frame further from the end is likely a mid-episode fade.
const minCreditsGapSeconds = 30

// Detector runs intro/credits detection on seasons and episodes. It is safe
// to call concurrently; each DetectSeason / DetectCredits call shells out to
// external binaries independently and only writes to the markers table.
type Detector struct {
	pool     *pgxpool.Pool
	mediaSvc *media.Service
	logger   *slog.Logger
}

// New returns a Detector wired to the given DB and media service.
func New(pool *pgxpool.Pool, mediaSvc *media.Service, logger *slog.Logger) *Detector {
	return &Detector{pool: pool, mediaSvc: mediaSvc, logger: logger}
}

// DetectSeason fingerprints every episode under seasonID, aligns them to
// locate the shared intro, and upserts an 'intro' marker per episode.
// It also runs blackdetect on each episode to find the credits start.
//
// Episodes with an existing manual marker are skipped — manual always wins.
// Errors on individual episodes are logged and skipped; the method returns
// an error only for setup failures that block the whole season (e.g. the
// season isn't found, or it has fewer than two episodes).
func (d *Detector) DetectSeason(ctx context.Context, seasonID uuid.UUID) error {
	episodes, err := d.mediaSvc.ListChildren(ctx, seasonID)
	if err != nil {
		return fmt.Errorf("list episodes for season %s: %w", seasonID, err)
	}
	d.logger.InfoContext(ctx, "detect season",
		"season_id", seasonID, "episodes", len(episodes))
	if len(episodes) < 2 {
		// Need at least two episodes to find a shared intro pattern. We still
		// run credits detection, since that works per-file.
		for i := range episodes {
			d.detectCreditsForItem(ctx, &episodes[i])
		}
		return nil
	}

	fps := make([][]uint32, len(episodes))
	filePaths := make([]string, len(episodes))
	var fingerprinted, noFile int
	for i := range episodes {
		ep := &episodes[i]
		path, ok := d.firstFilePath(ctx, ep.ID)
		if !ok {
			noFile++
			d.logger.WarnContext(ctx, "episode has no file",
				"episode_id", ep.ID)
			continue
		}
		filePaths[i] = path
		fp, err := fingerprint(ctx, path, 0, introFingerprintSeconds)
		if err != nil {
			d.logger.WarnContext(ctx, "fingerprint failed",
				"episode_id", ep.ID, "path", path, "err", err)
			continue
		}
		fps[i] = fp
		fingerprinted++
	}

	intros := detectSeasonIntros(fps)
	q := gen.New(d.pool)
	var intrWritten int
	for i := range episodes {
		ep := &episodes[i]
		intro := intros[i]
		if intro.lengthFrames < minIntroFrames {
			continue
		}
		startMs := framesToMs(intro.startFrame)
		endMs := framesToMs(intro.startFrame + intro.lengthFrames)
		if err := d.upsertAutoMarker(ctx, q, ep.ID, "intro", startMs, endMs); err != nil {
			d.logger.WarnContext(ctx, "upsert intro marker",
				"episode_id", ep.ID, "err", err)
			continue
		}
		intrWritten++
	}

	for i := range episodes {
		d.detectCreditsForItem(ctx, &episodes[i])
	}
	d.logger.InfoContext(ctx, "season done",
		"season_id", seasonID,
		"episodes", len(episodes),
		"fingerprinted", fingerprinted,
		"no_file", noFile,
		"intros_written", intrWritten)
	return nil
}

// DetectLibrary runs intro + credits detection for every season in a show
// library. Errors on individual seasons are logged and skipped; this is a
// best-effort background pass, not a transactional operation.
func (d *Detector) DetectLibrary(ctx context.Context, libraryID uuid.UUID) error {
	seasons, err := d.mediaSvc.ListItems(ctx, libraryID, "season", 10000, 0)
	if err != nil {
		return fmt.Errorf("list seasons for library %s: %w", libraryID, err)
	}
	d.logger.InfoContext(ctx, "intro detection starting",
		"library_id", libraryID, "seasons", len(seasons))
	for i := range seasons {
		if err := d.DetectSeason(ctx, seasons[i].ID); err != nil {
			d.logger.WarnContext(ctx, "detect season",
				"season_id", seasons[i].ID, "err", err)
		}
	}
	d.logger.InfoContext(ctx, "intro detection finished",
		"library_id", libraryID)
	return nil
}

// DetectCredits locates the credits start for a single episode and upserts
// the marker. Intended for one-off calls (e.g. an admin re-running detection
// after replacing a file). Manual markers are preserved.
func (d *Detector) DetectCredits(ctx context.Context, episodeID uuid.UUID) error {
	item, err := d.mediaSvc.GetItem(ctx, episodeID)
	if err != nil {
		return fmt.Errorf("get episode %s: %w", episodeID, err)
	}
	d.detectCreditsForItem(ctx, item)
	return nil
}

func (d *Detector) detectCreditsForItem(ctx context.Context, ep *media.Item) {
	if ep.DurationMS == nil || *ep.DurationMS <= 0 {
		return
	}
	path, ok := d.firstFilePath(ctx, ep.ID)
	if !ok {
		return
	}
	durationSec := int(*ep.DurationMS / 1000)
	creditStartMs, err := blackdetect(ctx, path, durationSec, creditsTailSeconds)
	if err != nil {
		d.logger.WarnContext(ctx, "blackdetect failed",
			"episode_id", ep.ID, "path", path, "err", err)
		return
	}
	if creditStartMs <= 0 {
		return
	}
	// Require the detected black region to be within the last N seconds —
	// otherwise it's a mid-show fade, not the credits roll.
	endMs := *ep.DurationMS
	if creditStartMs >= endMs || endMs-creditStartMs > int64(creditsTailSeconds)*1000 {
		return
	}
	q := gen.New(d.pool)
	if err := d.upsertAutoMarker(ctx, q, ep.ID, "credits", creditStartMs, endMs); err != nil {
		d.logger.WarnContext(ctx, "upsert credits marker",
			"episode_id", ep.ID, "err", err)
	}
}

// upsertAutoMarker writes an auto-sourced marker unless a manual marker of
// the same kind already exists for this episode. Chapter-sourced markers
// are also preserved (they came from the container's own chapter track).
func (d *Detector) upsertAutoMarker(ctx context.Context, q *gen.Queries, episodeID uuid.UUID, kind string, startMs, endMs int64) error {
	src, err := q.GetIntroMarkerSource(ctx, gen.GetIntroMarkerSourceParams{
		MediaItemID: episodeID,
		Kind:        kind,
	})
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	if src == "manual" || src == "chapter" {
		return nil
	}
	_, err = q.UpsertIntroMarker(ctx, gen.UpsertIntroMarkerParams{
		MediaItemID: episodeID,
		Kind:        kind,
		StartMs:     startMs,
		EndMs:       endMs,
		Source:      "auto",
	})
	return err
}

// firstFilePath returns the filesystem path of the first active file
// attached to itemID, or ("", false) if the episode has no file, or if the
// file is missing on disk (stale DB row from a folder rename or manual delete).
func (d *Detector) firstFilePath(ctx context.Context, itemID uuid.UUID) (string, bool) {
	files, err := d.mediaSvc.GetFiles(ctx, itemID)
	if err != nil || len(files) == 0 {
		return "", false
	}
	path := files[0].FilePath
	if _, err := os.Stat(path); err != nil {
		d.logger.WarnContext(ctx, "episode file missing on disk",
			"episode_id", itemID, "path", path)
		return "", false
	}
	return path, true
}

