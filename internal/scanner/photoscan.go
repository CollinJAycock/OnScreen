package scanner

import (
	"context"
	"hash/fnv"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/domain/media"
	"github.com/onscreen/onscreen/internal/rawpreview"
)

// PhotoStore resolves photo items by file rather than by title, and reads
// the per-library photo options (migration 00057). Optional: a scanner
// without one (WithPhotoStore not called) keeps the old title-keyed photo
// items, under which two IMG_0001.JPG in different folders are one photo.
type PhotoStore interface {
	// RawJPEGPairing reports the library's RAW + JPEG pairing option
	// (default on when the library has no photo settings row).
	RawJPEGPairing(ctx context.Context, libraryID uuid.UUID) (bool, error)
	// PhotoFilesByPaths returns the indexed, active photo files among paths.
	PhotoFilesByPaths(ctx context.Context, libraryID uuid.UUID, paths []string) ([]PhotoFileRef, error)
	// CreatePhotoItem inserts a new photo item and returns its ID.
	CreatePhotoItem(ctx context.Context, libraryID uuid.UUID, title, sortTitle string) (uuid.UUID, error)
}

// PhotoFileRef is one indexed photo file.
type PhotoFileRef struct {
	FileID, ItemID uuid.UUID
	Path           string
}

// WithPhotoStore enables path-keyed photo items and RAW + JPEG pairing.
func (s *Scanner) WithPhotoStore(p PhotoStore) *Scanner {
	s.photos = p
	return s
}

// photoLockStripes serialises item resolution for files that could pair
// (same folder, same name): without it a RAW and its JPEG scanned
// concurrently would each find no partner and create two items. Striped so
// the lock set doesn't grow with the library.
const photoLockStripes = 256

func (s *Scanner) photoLock(path string) *sync.Mutex {
	h := fnv.New32a()
	_, _ = h.Write([]byte(strings.ToLower(photoStemPath(path))))
	return &s.photoLocks[h.Sum32()%photoLockStripes]
}

// photoStemPath is path without its extension: the RAW + JPEG pair key.
func photoStemPath(path string) string {
	return strings.TrimSuffix(path, filepath.Ext(path))
}

// photoGroup is the key files must share to be one photo: the pair key when
// pairing is on, else the file itself.
func photoGroup(path string, pairing bool) string {
	if pairing {
		return strings.ToLower(photoStemPath(path))
	}
	return path
}

// PhotoDisplayPath picks the file a photo item shows from its files' paths:
// the first non-RAW file (by path), else the first RAW. "" for no files.
func PhotoDisplayPath(paths []string) string {
	if len(paths) == 0 {
		return ""
	}
	sorted := append([]string(nil), paths...)
	sort.Slice(sorted, func(i, j int) bool { return photoLess(sorted[i], sorted[j]) })
	return sorted[0]
}

// photoLess orders photo files display-first: non-RAW before RAW, then by
// path.
func photoLess(a, b string) bool {
	ra, rb := rawpreview.IsRAW(a), rawpreview.IsRAW(b)
	if ra != rb {
		return !ra
	}
	return a < b
}

// rawPairing reads the library's pairing option, defaulting to on when it
// can't be read.
func (s *Scanner) rawPairing(ctx context.Context, libraryID uuid.UUID) bool {
	if v, ok := s.pairingCache.Load(libraryID); ok {
		return v.(bool)
	}
	on, err := s.photos.RawJPEGPairing(ctx, libraryID)
	if err != nil {
		s.logger.WarnContext(ctx, "read photo pairing option failed; pairing on", "library_id", libraryID, "err", err)
		return true
	}
	return on
}

// photoSiblingPaths lists the paths a file pairs with: the same folder and
// name with every other photo extension, in lower and upper case (cameras
// write IMG_0001.CR2 next to IMG_0001.JPG).
func photoSiblingPaths(path string) []string {
	stem := photoStemPath(path)
	self := filepath.Ext(path)
	var out []string
	for ext := range imageExtensions {
		for _, e := range []string{ext, strings.ToUpper(ext)} {
			if e != self {
				out = append(out, stem+e)
			}
		}
	}
	sort.Strings(out)
	return out
}

// photoItemFor decides which item a photo file belongs to. ok=false means
// the file needs a new item of its own.
//
// The rules, so every file reaches the same answer whatever order the scan
// meets them in:
//   - With pairing on, a RAW joins its non-RAW partner's item; a new non-RAW
//     file joins its RAW partner's item.
//   - A file already indexed keeps its item unless the item also holds files
//     of a different group (another folder's photo of the same name, or the
//     RAW of a pair once pairing is off). Then only the group holding the
//     item's display file (non-RAW first, then by path) stays; the others
//     leave for new items.
func (s *Scanner) photoItemFor(ctx context.Context, libraryID uuid.UUID, path string, existing *media.File) (uuid.UUID, bool) {
	pairing := s.rawPairing(ctx, libraryID)
	isRAW := rawpreview.IsRAW(path)

	var partner uuid.UUID
	if pairing && (isRAW || existing == nil) {
		sibs, err := s.photos.PhotoFilesByPaths(ctx, libraryID, photoSiblingPaths(path))
		if err != nil {
			s.logger.WarnContext(ctx, "photo sibling lookup failed", "path", path, "err", err)
		}
		sort.Slice(sibs, func(i, j int) bool { return photoLess(sibs[i].Path, sibs[j].Path) })
		for _, sib := range sibs {
			if rawpreview.IsRAW(sib.Path) != isRAW {
				partner = sib.ItemID
				break
			}
		}
	}
	if isRAW && partner != uuid.Nil {
		return partner, true
	}
	if existing != nil {
		if s.photoKeepsItem(ctx, existing.MediaItemID, path, pairing) {
			return existing.MediaItemID, true
		}
		return uuid.Nil, false
	}
	if partner != uuid.Nil {
		return partner, true
	}
	return uuid.Nil, false
}

// photoKeepsItem reports whether path may stay on itemID (see photoItemFor).
func (s *Scanner) photoKeepsItem(ctx context.Context, itemID uuid.UUID, path string, pairing bool) bool {
	files, err := s.media.GetFiles(ctx, itemID)
	if err != nil || len(files) <= 1 {
		return true
	}
	paths := make([]string, 0, len(files))
	for _, f := range files {
		paths = append(paths, f.FilePath)
	}
	return photoGroup(PhotoDisplayPath(paths), pairing) == photoGroup(path, pairing)
}

// resolvePhotoItem finds or creates the item for a photo file. The caller
// holds the file's photoLock until the file row is written.
func (s *Scanner) resolvePhotoItem(ctx context.Context, libraryID uuid.UUID, path string, existing *media.File) (*media.Item, error) {
	if id, ok := s.photoItemFor(ctx, libraryID, path, existing); ok {
		if item, err := s.media.GetItem(ctx, id); err == nil {
			return item, nil
		}
	}
	stem := filepath.Base(path)
	stem = stem[:len(stem)-len(filepath.Ext(stem))]
	title := photoTitle(stem)
	id, err := s.photos.CreatePhotoItem(ctx, libraryID, title, stem)
	if err != nil {
		return nil, err
	}
	return s.media.GetItem(ctx, id)
}

// photoTitle turns a file stem into a display title ("IMG_2024" → "IMG 2024").
func photoTitle(stem string) string {
	title := strings.ReplaceAll(strings.ReplaceAll(stem, "_", " "), ".", " ")
	if title == "" {
		title = "Photo"
	}
	return title
}

// photoNeedsRegroup reports whether an unchanged photo file sits on the
// wrong item — its pairing changed, or an older scanner put it on a
// same-named photo from another folder — so it takes the slow path once.
// Checked once per file per pairing setting in this process.
func (s *Scanner) photoNeedsRegroup(ctx context.Context, libraryID uuid.UUID, path string, existing *media.File) bool {
	if s.photos == nil {
		return false
	}
	pairing := s.rawPairing(ctx, libraryID)
	key := existing.ID.String()
	if pairing {
		key += "|p"
	}
	if _, done := s.photoChecked.LoadOrStore(key, struct{}{}); done {
		return false
	}
	mu := s.photoLock(path)
	mu.Lock()
	defer mu.Unlock()
	id, ok := s.photoItemFor(ctx, libraryID, path, existing)
	return !ok || id != existing.MediaItemID
}

// photoRelPath is path relative to the library root that contains it, in
// slash form (the shape poster_path stores), or "".
func photoRelPath(roots []string, path string) string {
	for _, root := range roots {
		if rel, err := filepath.Rel(root, path); err == nil && !strings.HasPrefix(rel, "..") {
			return filepath.ToSlash(rel)
		}
	}
	return ""
}

// refreshPhotoDisplay points a photo item's poster and EXIF at its display
// file. Run for the file's own item after each photo file is written, and
// for the item a file just left.
func (s *Scanner) refreshPhotoDisplay(ctx context.Context, itemID uuid.UUID, roots []string, onlyIf string) {
	files, err := s.media.GetFiles(ctx, itemID)
	if err != nil || len(files) == 0 {
		return
	}
	paths := make([]string, 0, len(files))
	for _, f := range files {
		paths = append(paths, f.FilePath)
	}
	display := PhotoDisplayPath(paths)
	if onlyIf != "" && display != onlyIf {
		return
	}
	item, err := s.media.GetItem(ctx, itemID)
	if err != nil {
		return
	}
	if rel := photoRelPath(roots, display); rel != "" && (item.PosterPath == nil || *item.PosterPath != rel) {
		// Only the poster: UpdateItemMetadata would also blank the photo's
		// taken date and the rest.
		if err := s.media.UpdateItemPosterPath(ctx, item.ID, rel); err != nil {
			s.logger.WarnContext(ctx, "update photo poster failed", "item_id", item.ID, "err", err)
		}
	}
	s.persistPhotoEXIF(ctx, item, display)
}
