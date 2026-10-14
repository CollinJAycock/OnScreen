package scanner

import (
	"context"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/domain/media"
	"github.com/onscreen/onscreen/internal/metadata"
	"github.com/onscreen/onscreen/internal/metadata/nfo"
)

// ItemExtrasStore persists what enrichment learns beyond the item row's core
// metadata: clear logo / banner / disc art paths and studios (migration
// 00049), and reads the library options the NFO writer needs. A narrow
// interface so the scanner doesn't import the studios service or gen.
type ItemExtrasStore interface {
	// SetItemExtraArt stores library-relative art paths; nil keeps the
	// stored one.
	SetItemExtraArt(ctx context.Context, itemID uuid.UUID, logo, banner, disc *string) error
	// SetItemStudios replaces an item's studios (kind "studio" or
	// "network") with list, in order.
	SetItemStudios(ctx context.Context, itemID uuid.UUID, kind string, list []metadata.Studio) error
	// ItemStudioNames returns an item's studio names in order.
	ItemStudioNames(ctx context.Context, itemID uuid.UUID) ([]string, error)
	// LibraryNFOOptions returns whether the library writes NFO files next
	// to its media, and its metadata language.
	LibraryNFOOptions(ctx context.Context, libraryID uuid.UUID) (saveNFO bool, language string, err error)
}

// ExtraArtDownloader is the part of the artwork manager the extra-art step
// uses: the existence check and a download to a caller-chosen file name.
// Optional — an ArtworkFetcher without it (test fakes) skips extra art.
type ExtraArtDownloader interface {
	ArtFileExists(ctx context.Context, absPath string) bool
	DownloadArtFile(ctx context.Context, url, absPath string, force bool) (string, error)
}

// SetItemExtras wires the extras store. nil (the default) disables extra
// art, studios and NFO writing.
func (e *Enricher) SetItemExtras(s ItemExtrasStore) {
	e.extras = s
}

// SetExtraArtFn sets the lazy factory for the extra-art agent (fanart.tv).
// Called per enrichment so a key set in the admin UI applies without a
// restart; returning nil (no key) skips the lookup and leaves local files
// and the AniList banner as the only sources.
func (e *Enricher) SetExtraArtFn(fn func() metadata.ExtraArtAgent) {
	e.extraArtFn = fn
}

// SetLocalMediaFn reports whether media lives on the local filesystem. NFO
// files are only written there; with object storage it returns false and
// the NFO writer stays off. nil = local.
func (e *Enricher) SetLocalMediaFn(fn func() bool) {
	e.localMediaFn = fn
}

// Studio kinds, as stored (studios_kind_check).
const (
	studioKindStudio  = "studio"
	studioKindNetwork = "network"
)

// artSlot is one kind of extra artwork: the Kodi file names a local copy
// may have (first match wins) and the base name a download is saved under.
type artSlot struct {
	local    []string
	saveBase string
	saveExt  string // used when the URL has no recognisable extension
}

var (
	logoSlot   = artSlot{local: []string{"clearlogo.png", "logo.png"}, saveBase: "clearlogo", saveExt: ".png"}
	bannerSlot = artSlot{local: []string{"banner.jpg", "banner.png"}, saveBase: "banner", saveExt: ".jpg"}
	discSlot   = artSlot{local: []string{"disc.png", "discart.png", "cdart.png"}, saveBase: "disc", saveExt: ".png"}
)

// extrasInput is what applyExtras needs to fill an item's extra artwork and
// studios. Zero-valued fields are simply not used.
type extrasInput struct {
	item *media.Item
	// dir is the absolute directory the art lives in (the movie's folder,
	// the show's root folder, the artist's or album's folder).
	dir string
	// idQualified prefixes downloaded file names with the item id: music
	// folders are shared in flat layouts (several albums in one artist
	// folder), where a bare disc.png would belong to all of them.
	idQualified bool
	// noLocal skips the Kodi-name lookup (the folder is shared with other
	// items, so a bare disc.png there can't be told apart).
	noLocal bool
	// localOnly reads the Kodi-named files and never asks fanart.tv (an
	// item with no metadata match has no ids to ask with).
	localOnly bool
	slots     []*artSlot

	tmdbID, tvdbID   int
	imdbID           string
	artistMBID       uuid.UUID
	releaseGroupMBID uuid.UUID
	// bannerFallback is used when neither a local file nor fanart.tv has a
	// banner (AniList's bannerImage for anime).
	bannerFallback string

	// studios, when studiosKnown, replace the item's studios (studioKind).
	studios      []metadata.Studio
	studiosKnown bool
	studioKind   string

	// force re-downloads provider art over an existing file (Fix Match,
	// where the old file belongs to the wrong title).
	force bool
}

// applyExtras fills an item's extra artwork and studios. Every failure is
// logged and swallowed: extras must never fail an enrichment.
func (e *Enricher) applyExtras(ctx context.Context, in extrasInput) {
	if e.extras == nil || in.item == nil {
		return
	}
	if in.studiosKnown && in.studioKind != "" {
		if err := e.extras.SetItemStudios(ctx, in.item.ID, in.studioKind, in.studios); err != nil {
			e.logger.WarnContext(ctx, "set item studios failed", "item_id", in.item.ID, "err", err)
		}
	}
	if len(in.slots) > 0 {
		e.applyExtraArt(ctx, in)
	}
}

// applyExtraArt resolves each slot from, in order: a Kodi-named file next to
// the media, fanart.tv, the banner fallback. fanart.tv is asked at most
// once, and only when a slot is still empty.
func (e *Enricher) applyExtraArt(ctx context.Context, in extrasInput) {
	dl, ok := e.artwork.(ExtraArtDownloader)
	if !ok || in.dir == "" || in.dir == "." {
		return
	}
	found := map[*artSlot]string{} // slot → absolute path
	if !in.force && !in.noLocal {
		for _, slot := range in.slots {
			for _, name := range slot.local {
				abs := filepath.Join(in.dir, name)
				if dl.ArtFileExists(ctx, abs) {
					found[slot] = abs
					break
				}
			}
		}
	}
	// Our own earlier downloads (id-qualified names) count as local too.
	if !in.force && in.idQualified {
		for _, slot := range in.slots {
			if _, ok := found[slot]; ok {
				continue
			}
			for _, ext := range []string{".png", ".jpg"} {
				abs := filepath.Join(in.dir, in.item.ID.String()+"-"+slot.saveBase+ext)
				if dl.ArtFileExists(ctx, abs) {
					found[slot] = abs
					break
				}
			}
		}
	}

	var remote metadata.ExtraArt
	if len(found) < len(in.slots) && !in.localOnly {
		remote = e.lookupExtraArt(ctx, in)
		if remote.BannerURL == "" {
			remote.BannerURL = in.bannerFallback
		}
	}
	for _, slot := range in.slots {
		if _, ok := found[slot]; ok {
			continue
		}
		u := remoteURLFor(slot, remote)
		if u == "" {
			continue
		}
		name := slot.saveBase + artExt(u, slot.saveExt)
		if in.idQualified {
			name = in.item.ID.String() + "-" + name
		}
		abs, err := dl.DownloadArtFile(ctx, u, filepath.Join(in.dir, name), in.force)
		if err != nil {
			e.logger.WarnContext(ctx, "extra art download failed",
				"item_id", in.item.ID, "kind", slot.saveBase, "err", err)
			continue
		}
		found[slot] = abs
	}

	var logo, banner, disc *string
	for slot, abs := range found {
		var dest **string
		switch slot {
		case &logoSlot:
			dest = &logo
		case &bannerSlot:
			dest = &banner
		case &discSlot:
			dest = &disc
		default:
			continue
		}
		e.setRelPath(dest, abs)
	}
	if logo == nil && banner == nil && disc == nil {
		return
	}
	if err := e.extras.SetItemExtraArt(ctx, in.item.ID, logo, banner, disc); err != nil {
		e.logger.WarnContext(ctx, "set extra art failed", "item_id", in.item.ID, "err", err)
	}
}

// lookupExtraArt asks fanart.tv for the item's art in the library language.
func (e *Enricher) lookupExtraArt(ctx context.Context, in extrasInput) metadata.ExtraArt {
	if e.extraArtFn == nil {
		return metadata.ExtraArt{}
	}
	agent := e.extraArtFn()
	if agent == nil {
		return metadata.ExtraArt{}
	}
	lang := e.libraryLanguage(ctx, in.item.LibraryID)
	var (
		art metadata.ExtraArt
		err error
	)
	switch in.item.Type {
	case "movie":
		art, err = agent.MovieArt(ctx, in.tmdbID, in.imdbID, lang)
	case "show":
		art, err = agent.ShowArt(ctx, in.tvdbID, lang)
	case "artist":
		art, err = agent.ArtistArt(ctx, in.artistMBID, lang)
	case "album":
		art, err = agent.AlbumArt(ctx, in.releaseGroupMBID, lang)
	}
	if err != nil {
		e.logger.WarnContext(ctx, "fanart.tv lookup failed", "item_id", in.item.ID, "err", err)
		return metadata.ExtraArt{}
	}
	return art
}

// libraryLanguage returns the library's metadata language ("" when unknown).
func (e *Enricher) libraryLanguage(ctx context.Context, libraryID uuid.UUID) string {
	if e.extras == nil {
		return ""
	}
	_, lang, err := e.extras.LibraryNFOOptions(ctx, libraryID)
	if err != nil {
		return ""
	}
	return lang
}

func remoteURLFor(slot *artSlot, art metadata.ExtraArt) string {
	switch slot {
	case &logoSlot:
		return art.LogoURL
	case &bannerSlot:
		return art.BannerURL
	case &discSlot:
		return art.DiscURL
	}
	return ""
}

// artExt returns the image extension of an artwork URL (".png", ".jpg"),
// or def when it has none the artwork route serves.
func artExt(rawURL, def string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return def
	}
	switch ext := strings.ToLower(path.Ext(u.Path)); ext {
	case ".png", ".jpg", ".webp":
		return ext
	case ".jpeg":
		return ".jpg"
	}
	return def
}

// movieStudios picks a movie's studios: the NFO's <studio> tags when it has
// any (operator-curated), else the provider's production companies. ok =
// false when neither is known, which leaves the stored list alone.
func movieStudios(result *metadata.MovieResult, m *nfo.Movie) ([]metadata.Studio, bool) {
	if m != nil && len(m.Studios) > 0 {
		return namesToStudios(m.Studios), true
	}
	if result != nil && result.Studios != nil {
		return result.Studios, true
	}
	return nil, false
}

// showStudios is movieStudios for shows: the NFO's <studio> tags, else the
// provider's networks when the result came from a details payload.
func showStudios(result *metadata.TVShowResult, s *nfo.Show) ([]metadata.Studio, bool) {
	if s != nil && len(s.Studios) > 0 {
		return namesToStudios(s.Studios), true
	}
	if result != nil && result.StudiosChecked {
		return result.Studios, true
	}
	return nil, false
}

func namesToStudios(names []string) []metadata.Studio {
	out := make([]metadata.Studio, 0, len(names))
	for _, n := range names {
		out = append(out, metadata.Studio{Name: n})
	}
	return out
}

func derefInt(p *int) int {
	if p == nil {
		return 0
	}
	return *p
}

func derefStr(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

func derefUUID(p *uuid.UUID) uuid.UUID {
	if p == nil {
		return uuid.Nil
	}
	return *p
}

// finishMovie runs the post-write steps for a movie the enricher (or Fix
// Match) just updated: studios, extra art, then the NFO (which includes the
// studios). itemID is the row that was written, which a sibling merge may
// make different from item.ID.
func (e *Enricher) finishMovie(ctx context.Context, itemID uuid.UUID, file *media.File, result *metadata.MovieResult, nfoMovie *nfo.Movie, force bool) {
	if e.extras == nil {
		return
	}
	item, err := e.updater.GetItem(ctx, itemID)
	if err != nil || item == nil {
		return
	}
	in := extrasInput{
		item:   item,
		slots:  []*artSlot{&logoSlot, &bannerSlot, &discSlot},
		tmdbID: derefInt(item.TMDBID),
		imdbID: derefStr(item.IMDBID),
		force:  force,
	}
	if file != nil {
		in.dir = filepath.Dir(file.FilePath)
		// A movie file sitting in the library root shares its folder with
		// every other such movie: a bare clearlogo.png there can't be told
		// apart, so neither read nor write one.
		if e.isScanRoot(in.dir) {
			in.slots = nil
		}
	}
	if result != nil {
		if in.imdbID == "" {
			in.imdbID = result.IMDBID
		}
	}
	// Fix Match doesn't read the NFO; its <studio> tags still win.
	if nfoMovie == nil && file != nil {
		nfoMovie = e.readMovieNFO(ctx, file)
	}
	in.studios, in.studiosKnown = movieStudios(result, nfoMovie)
	in.studioKind = studioKindStudio
	e.applyExtras(ctx, in)
	e.WriteItemNFO(ctx, itemID)
}

// localExtraArt reads the Kodi-named logo / banner / disc files next to a
// movie or show that has no metadata match (no agent configured, or nothing
// found), the same way a local poster.jpg is used without one. Nothing is
// downloaded, studios are left alone and no NFO is written.
func (e *Enricher) localExtraArt(ctx context.Context, item *media.Item, file *media.File) {
	if e.extras == nil || item == nil || file == nil {
		return
	}
	in := extrasInput{item: item, localOnly: true}
	switch item.Type {
	case "movie":
		in.dir = filepath.Dir(file.FilePath)
		in.slots = []*artSlot{&logoSlot, &bannerSlot, &discSlot}
	case "show":
		in.dir = showDirFromFile(file.FilePath)
		in.slots = []*artSlot{&logoSlot, &bannerSlot}
	default:
		return
	}
	if e.isScanRoot(in.dir) {
		return
	}
	e.applyExtras(ctx, in)
}

// finishShow is finishMovie for shows: networks, logo and banner (fanart.tv
// by TheTVDB id, then AniList's banner), then tvshow.nfo.
func (e *Enricher) finishShow(ctx context.Context, itemID uuid.UUID, file *media.File, result *metadata.TVShowResult, nfoShow *nfo.Show, force bool) {
	if e.extras == nil {
		return
	}
	item, err := e.updater.GetItem(ctx, itemID)
	if err != nil || item == nil {
		return
	}
	in := extrasInput{
		item:   item,
		slots:  []*artSlot{&logoSlot, &bannerSlot},
		tvdbID: derefInt(item.TVDBID),
		force:  force,
	}
	if file != nil {
		in.dir = showDirFromFile(file.FilePath)
		if e.isScanRoot(in.dir) {
			in.slots = nil
		}
	}
	if result != nil {
		if in.tvdbID == 0 {
			in.tvdbID = result.TVDBID
		}
		in.bannerFallback = result.BannerURL
	}
	// Fix Match doesn't read the NFO; its <studio> tags still win.
	if nfoShow == nil && in.dir != "" {
		nfoShow = e.readShowNFO(ctx, in.dir)
	}
	in.studios, in.studiosKnown = showStudios(result, nfoShow)
	in.studioKind = studioKindNetwork
	e.applyExtras(ctx, in)
	e.WriteItemNFO(ctx, itemID)
}

// finishMusic fills an artist's logo and banner or an album's disc art.
// Downloads are id-qualified (see extrasInput.idQualified); an album only
// reads Kodi's cdart.png / disc.png from a folder of its own.
func (e *Enricher) finishMusic(ctx context.Context, item *media.Item, dir string) {
	if e.extras == nil || item == nil || dir == "" || e.isScanRoot(dir) {
		return
	}
	in := extrasInput{item: item, dir: dir, idQualified: true}
	switch item.Type {
	case "artist":
		in.slots = []*artSlot{&logoSlot, &bannerSlot}
		in.artistMBID = derefUUID(item.MusicBrainzID)
	case "album":
		in.slots = []*artSlot{&discSlot}
		in.releaseGroupMBID = derefUUID(item.MusicBrainzReleaseGroupID)
		// Flat layout: the album's tracks sit in the artist's folder, which
		// other albums share.
		in.noLocal = e.albumFolderShared(ctx, item, dir)
	default:
		return
	}
	e.applyExtras(ctx, in)
}

// albumFolderShared reports whether dir also holds another album's tracks,
// in which case Kodi's bare cdart.png there can't be attributed.
func (e *Enricher) albumFolderShared(ctx context.Context, album *media.Item, dir string) bool {
	if album.ParentID == nil {
		return false
	}
	siblings, err := e.updater.ListChildren(ctx, *album.ParentID)
	if err != nil {
		return true
	}
	for _, s := range siblings {
		if s.ID == album.ID || s.Type != "album" {
			continue
		}
		if f := e.findDescendantFile(ctx, s.ID); f != nil && filepath.Dir(f.FilePath) == dir {
			return true
		}
	}
	return false
}

// isScanRoot reports whether dir is one of the library scan roots.
func (e *Enricher) isScanRoot(dir string) bool {
	if e.scanPaths == nil {
		return false
	}
	clean := filepath.Clean(dir)
	for _, root := range e.scanPaths() {
		if filepath.Clean(root) == clean {
			return true
		}
	}
	return false
}

// readShowNFO parses showDir/tvshow.nfo (ids only when OnScreen wrote it);
// nil when there is none or it doesn't parse.
func (e *Enricher) readShowNFO(ctx context.Context, showDir string) *nfo.Show {
	p, err := nfo.FindShowNFO(showDir)
	if err != nil {
		return nil
	}
	f, err := os.Open(p)
	if err != nil {
		return nil
	}
	defer f.Close()
	parsed, err := nfo.ParseShow(f)
	if err != nil {
		e.logger.WarnContext(ctx, "tvshow.nfo parse failed", "path", p, "err", err)
		return nil
	}
	if nfo.IsOurs(p) {
		return ownShowNFO(parsed)
	}
	return parsed
}
