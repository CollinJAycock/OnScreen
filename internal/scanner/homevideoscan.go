package scanner

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/domain/media"
)

// processHomeVideo creates a 'home_video' media_item for a file in a
// home-video library. v2.1 scope is flat — one file = one row — with
// no external metadata agent (TMDB doesn't have your kid's birthday
// party). Title comes from the filename with leading date prefixes
// stripped; the date itself populates originally_available_at, which
// the library page uses as the primary sort key.
//
// Files under a non-root subfolder (e.g.
// `<root>/Yellowstone 2024/<file>`) get auto-added to an event_folder
// collection named after the top-level subfolder. Re-scans use the
// same collection (idempotent upsert). Files loose at the library
// root skip the collection step.
//
// Date sourced from file mtime. ffprobe's container `creation_time`
// would be more accurate for camcorder formats but the scanner doesn't
// route the probe result back into the create-item path here, and
// adding that plumbing is a v2.x polish — mtime is correct for the
// "drop a file in and rescan" workflow most users follow.
func (s *Scanner) processHomeVideo(ctx context.Context, libraryID uuid.UUID, path string, roots []string, mtime time.Time, durationMS *int64) (*media.Item, error) {
	title := parseHomeVideoTitle(path)
	if title == "" {
		title = "Home Video"
	}

	taken := mtime
	p := media.CreateItemParams{
		LibraryID:             libraryID,
		Type:                  "home_video",
		Title:                 title,
		SortTitle:             sortTitle(title),
		OriginallyAvailableAt: &taken,
	}

	item, err := s.media.FindOrCreateItem(ctx, p)
	if err != nil {
		return nil, err
	}

	s.extractHomeVideoArt(ctx, item, path, roots, durationMS)

	if event := eventFolderName(path, roots); event != "" {
		colID, cerr := s.media.UpsertEventCollection(ctx, libraryID, event)
		if cerr != nil {
			s.logger.WarnContext(ctx, "upsert event collection failed",
				"library_id", libraryID, "event", event, "err", cerr)
		} else if aerr := s.media.AddItemToCollection(ctx, colID, item.ID); aerr != nil {
			s.logger.WarnContext(ctx, "add home_video to event collection failed",
				"collection_id", colID, "item_id", item.ID, "err", aerr)
		}
	}

	return item, nil
}

// extractHomeVideoArt grabs a frame from the video and writes it as
// `<videoDir>/<item.id>-poster.jpg`, then updates item.poster_path. No
// external metadata source exists for personal footage, so a frame is
// the best we can do — and a frame from somewhere into the clip is
// usually better than the first frame (avoids title cards / black
// intros / camera-mount fumbling).
//
// Seek target is picked from durationMS:
//   - 60s+: 30s in (covers most clips, keeps the seek constant)
//   - 10–60s: midpoint of the clip
//   - <10s or unknown: 0s
//
// Idempotent: if the {id}-poster.jpg already exists on disk we skip
// the ffmpeg call and just (re-)sync poster_path.
func (s *Scanner) extractHomeVideoArt(ctx context.Context, item *media.Item, filePath string, roots []string, durationMS *int64) {
	if item == nil {
		return
	}
	videoDir := filepath.Dir(filePath)
	posterFile := filepath.Join(videoDir, item.ID.String()+"-poster.jpg")

	relPath := ""
	for _, root := range roots {
		if rel, err := filepath.Rel(root, posterFile); err == nil && !strings.HasPrefix(rel, "..") {
			relPath = filepath.ToSlash(rel)
			break
		}
	}
	if relPath == "" {
		relPath = filepath.ToSlash(filepath.Join(filepath.Base(videoDir), item.ID.String()+"-poster.jpg"))
	}

	if _, err := os.Stat(posterFile); err == nil {
		s.syncHomeVideoPoster(ctx, item, relPath)
		return
	}

	seek := homeVideoSeekTime(durationMS)
	cmd := exec.CommandContext(ctx, "ffmpeg",
		"-hide_banner", "-loglevel", "error",
		"-ss", seek,
		"-i", filePath,
		"-frames:v", "1",
		"-q:v", "2",
		"-y", posterFile,
	)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		s.logger.WarnContext(ctx, "extract home video frame failed",
			"item_id", item.ID, "path", filePath, "err", err,
			"stderr", strings.TrimSpace(stderr.String()))
		// Clean up any zero-byte file ffmpeg may have left behind so a
		// later re-scan re-attempts (the os.Stat short-circuit above
		// would otherwise treat a 0-byte file as "already extracted").
		if info, statErr := os.Stat(posterFile); statErr == nil && info.Size() == 0 {
			_ = os.Remove(posterFile)
		}
		return
	}

	s.syncHomeVideoPoster(ctx, item, relPath)
}

// syncHomeVideoPoster writes relPath to item.poster_path when it
// differs. Mirrors syncAudiobookPosterPaths' shape minus the author
// cascade — home_video has no parent row to inherit the poster.
func (s *Scanner) syncHomeVideoPoster(ctx context.Context, item *media.Item, relPath string) {
	if item.PosterPath != nil && *item.PosterPath == relPath {
		return
	}
	if _, err := s.media.UpdateItemMetadata(ctx, media.UpdateItemMetadataParams{
		ID:         item.ID,
		Title:      item.Title,
		SortTitle:  item.SortTitle,
		Year:       item.Year,
		PosterPath: &relPath,
	}); err != nil {
		s.logger.WarnContext(ctx, "failed to update home_video poster_path",
			"item_id", item.ID, "err", err)
		return
	}
	item.PosterPath = &relPath
}

// homeVideoSeekTime picks an ffmpeg `-ss` argument given the clip
// duration in milliseconds. Returns "0" when duration is unknown or
// too short to meaningfully seek; otherwise either "30" (≥60s) or
// the midpoint formatted as a whole-second decimal. Going past the
// clip end errors ffmpeg out, hence the duration-aware ladder.
func homeVideoSeekTime(durationMS *int64) string {
	if durationMS == nil || *durationMS < 10000 {
		return "0"
	}
	if *durationMS >= 60000 {
		return "30"
	}
	mid := *durationMS / 2 / 1000 // whole seconds at the midpoint
	return fmt.Sprintf("%d", mid)
}

// eventFolderName returns the immediate child of the library root that
// contains `path`. For `<root>/Yellowstone 2024/clip.mp4` it returns
// "Yellowstone 2024"; for `<root>/Yellowstone 2024/Day 1/clip.mp4` it
// also returns "Yellowstone 2024" (the *top-level* event subfolder
// wins so multi-day trips collapse into one collection rather than
// splitting per day). Returns "" when the file sits directly at the
// library root or when no configured root is an ancestor of path.
func eventFolderName(path string, roots []string) string {
	pathClean := filepath.Clean(path)
	pathDir := filepath.Dir(pathClean)
	for _, r := range roots {
		rootClean := filepath.Clean(r)
		// Loose at root — no event folder above.
		if pathDir == rootClean {
			return ""
		}
		// Walk up from the file's parent until the parent of the
		// current dir IS the library root. That child of root is the
		// event folder. filepath.Dir on the root itself returns the
		// drive (e.g. `C:\`), which never matches a configured root —
		// so the loop exits cleanly when path isn't under r.
		dir := pathDir
		for {
			parent := filepath.Dir(dir)
			if parent == dir { // hit FS root, not under this library root
				break
			}
			if parent == rootClean {
				return filepath.Base(dir)
			}
			dir = parent
		}
	}
	return ""
}

// dateStemPrefix matches a leading "YYYY-MM-DD" or "YYYY_MM_DD" or
// "YYYYMMDD" optionally followed by " - " or " " separators. Stripped
// from titles so a file named "2024-04-15 - Yellowstone hike.mp4"
// surfaces as "Yellowstone hike" rather than the raw filename.
var dateStemPrefix = regexp.MustCompile(`^(?:\d{4}[-_]\d{2}[-_]\d{2}|\d{8})\s*(?:[-—]\s*)?`)

// parseHomeVideoTitle returns a clean title from a home-video file path.
// Strips the extension, normalises common separators, and removes any
// leading date prefix that the user added for their own ordering.
func parseHomeVideoTitle(path string) string {
	stem := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	stem = dateStemPrefix.ReplaceAllString(stem, "")
	stem = strings.ReplaceAll(stem, "_", " ")
	stem = strings.ReplaceAll(stem, ".", " ")
	return strings.TrimSpace(stem)
}
