package scanner

import (
	"context"
	"regexp"
	"strings"

	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/domain/media"
)

// ArtistCreditStore persists the artists credited on a track besides its
// album artist (track_artists, migration 00048) and remembers which files'
// credits have been read. Optional: a scanner without one (WithArtistCredits
// not called) stores no credits.
type ArtistCreditStore interface {
	// FindArtistByMusicBrainzID returns the library's artist carrying mbid;
	// ok=false when there is none.
	FindArtistByMusicBrainzID(ctx context.Context, libraryID, mbid uuid.UUID) (id uuid.UUID, ok bool, err error)
	// SetTrackArtists replaces the track's credits with credits.
	SetTrackArtists(ctx context.Context, trackID uuid.UUID, credits []TrackArtist) error
	// CreditsScanned reports whether the file's credits have been read.
	CreditsScanned(ctx context.Context, fileID uuid.UUID) (bool, error)
	// MarkCreditsScanned records that they have.
	MarkCreditsScanned(ctx context.Context, fileID uuid.UUID) error
}

// TrackArtist is one stored credit: an artist and how it is credited.
type TrackArtist struct {
	ArtistID uuid.UUID
	// Role is "featured" (named in a feat. clause) or "artist" (credited
	// on the track otherwise, e.g. the performer of a compilation track).
	Role     string
	Position int
}

// Credit roles (track_artists.role).
const (
	CreditFeatured = "featured"
	CreditArtist   = "artist"
)

// WithArtistCredits enables storing featured-artist credits.
func (s *Scanner) WithArtistCredits(c ArtistCreditStore) *Scanner {
	s.credits = c
	return s
}

// ArtistCredit is one artist a track's tags credit.
type ArtistCredit struct {
	Name string
	MBID uuid.UUID // uuid.Nil when unknown
	// Featured is set when the name comes from a feat. clause (of the
	// artist tag or the title), as opposed to a primary performer.
	Featured bool
}

// featClauseRE finds a trailing featured clause: "Artist feat. X & Y",
// "Song ft. X", "Song featuring X". The dot is required for "feat." and
// "ft." — "Little Feat" and "Ft" as words stay put.
var featClauseRE = regexp.MustCompile(`(?i)\s+(?:feat\.|ft\.|featuring)\s+(.+)$`)

// featBracketRE finds a bracketed featured clause anywhere: "Song (feat. X)",
// "Artist [ft. X]".
var featBracketRE = regexp.MustCompile(`(?i)\s*[(\[]\s*(?:feat\.|ft\.|featuring)\s+([^)\]]+?)\s*[)\]]`)

// featSplitRE separates the names inside a featured clause.
var featSplitRE = regexp.MustCompile(`(?i)\s*,\s+|\s+&\s+|\s+and\s+`)

// splitFeatured separates a featured clause from s: primary is s without
// it, clause the names it features ("" when there is none). Only the clause
// is ever split further; the primary stays whole, so "Earth, Wind & Fire"
// and "Simon & Garfunkel" remain one artist.
func splitFeatured(s string) (primary, clause string) {
	if loc := featBracketRE.FindStringSubmatchIndex(s); loc != nil {
		clause = strings.TrimSpace(s[loc[2]:loc[3]])
		primary = strings.TrimSpace(s[:loc[0]] + " " + s[loc[1]:])
		return strings.Join(strings.Fields(primary), " "), clause
	}
	if loc := featClauseRE.FindStringSubmatchIndex(s); loc != nil {
		return strings.TrimSpace(s[:loc[0]]), strings.TrimSpace(s[loc[2]:loc[3]])
	}
	return strings.TrimSpace(s), ""
}

// splitFeaturedNames splits a featured clause into its names on ", ", " & "
// and " and ". known reports whether a name is an artist the library
// already has: a clause that is one known artist as a whole ("feat. Earth,
// Wind & Fire") is kept whole, and so is any run of pieces that together
// name one ("feat. X & Earth, Wind & Fire" → X, Earth, Wind & Fire). known
// may be nil.
func splitFeaturedNames(clause string, known func(string) bool) []string {
	clause = strings.TrimSpace(clause)
	if clause == "" {
		return nil
	}
	if known != nil && known(clause) {
		return []string{clause}
	}
	// Pieces and the separators between them, so a run can be re-joined
	// exactly as written.
	idx := featSplitRE.FindAllStringIndex(clause, -1)
	if len(idx) == 0 {
		return []string{clause}
	}
	var pieces, seps []string
	start := 0
	for _, m := range idx {
		pieces = append(pieces, clause[start:m[0]])
		seps = append(seps, clause[m[0]:m[1]])
		start = m[1]
	}
	pieces = append(pieces, clause[start:])

	var out []string
	for i := 0; i < len(pieces); {
		// Longest run starting at i that names a known artist.
		end := i
		if known != nil {
			for j := len(pieces) - 1; j > i; j-- {
				var b strings.Builder
				for k := i; k <= j; k++ {
					b.WriteString(pieces[k])
					if k < j {
						b.WriteString(seps[k])
					}
				}
				if known(strings.TrimSpace(b.String())) {
					end = j
					break
				}
			}
		}
		var b strings.Builder
		for k := i; k <= end; k++ {
			b.WriteString(pieces[k])
			if k < end {
				b.WriteString(seps[k])
			}
		}
		if name := strings.TrimSpace(b.String()); name != "" {
			out = append(out, name)
		}
		i = end + 1
	}
	return out
}

// trackCredits lists the artists a track's tags credit, primary first.
//
// A multi-valued ARTISTS tag (MusicBrainz Picard) is preferred: it names
// every credited artist exactly, and MUSICBRAINZ_ARTISTID gives their ids
// in the same order. Which of them are featured is read off the feat.
// clauses of the artist tag and the title. Without ARTISTS, the artist tag
// is split into its primary (kept whole) and its featured clause, and a
// title's "(feat. X)" adds X. Names are de-duplicated case-insensitively.
func trackCredits(t *MusicTags, known func(string) bool) []ArtistCredit {
	artistPrimary, artistClause := splitFeatured(t.Artist)
	_, titleClause := splitFeatured(t.Title)
	featured := append(splitFeaturedNames(artistClause, known), splitFeaturedNames(titleClause, known)...)

	var out []ArtistCredit
	seen := map[string]bool{}
	add := func(c ArtistCredit) {
		k := creditKey(c.Name)
		if k == "" || seen[k] {
			return
		}
		seen[k] = true
		out = append(out, c)
	}

	if len(t.Artists) > 0 {
		featuredSet := map[string]bool{}
		for _, n := range featured {
			featuredSet[creditKey(n)] = true
		}
		pairIDs := len(t.ArtistMBIDs) == len(t.Artists)
		for i, n := range t.Artists {
			c := ArtistCredit{Name: n, Featured: featuredSet[creditKey(n)]}
			if pairIDs {
				c.MBID = t.ArtistMBIDs[i]
			}
			add(c)
		}
		// A title's featured guest the ARTISTS list leaves out still counts.
		for _, n := range featured {
			add(ArtistCredit{Name: n, Featured: true})
		}
		return out
	}

	if artistPrimary != "" {
		c := ArtistCredit{Name: artistPrimary}
		// A single artist id belongs to the primary; with a featured
		// clause and several ids there is no telling, so none is used.
		if len(t.ArtistMBIDs) == 1 || (len(t.ArtistMBIDs) == 0 && t.MBArtistID != uuid.Nil) {
			c.MBID = t.MBArtistID
			if len(t.ArtistMBIDs) == 1 {
				c.MBID = t.ArtistMBIDs[0]
			}
		}
		add(c)
	}
	for _, n := range featured {
		add(ArtistCredit{Name: n, Featured: true})
	}
	return out
}

// creditKey folds a name for de-duplication: case and runs of spaces.
func creditKey(s string) string {
	return strings.ToLower(strings.Join(strings.Fields(s), " "))
}

// albumArtistName is the name a track's album hangs under: the album
// artist tag without any featured clause ("Jay-Z feat. Rihanna" files the
// album under Jay-Z; the guest is a credit, not a second artist).
func albumArtistName(tagged string) string {
	if primary, clause := splitFeatured(tagged); clause != "" && primary != "" {
		return primary
	}
	return tagged
}

// storeTrackCredits stores the artists track's tags credit besides its
// album artist (albumArtistID). A featured artist the library doesn't have
// yet is created — its page then lists what it appears on — and found again
// by MusicBrainz id first, then by name, so it is never duplicated. Any
// other credited artist (a compilation track's own performer) is linked only
// when the library already has it: that is a credit, not a reason to grow
// an artist row per compilation track. Returns false when nothing could be
// stored (no store, or a write failed), so the file isn't marked read.
func (s *Scanner) storeTrackCredits(ctx context.Context, libraryID, albumArtistID uuid.UUID, track *media.Item, tags *MusicTags) bool {
	if s.credits == nil || track == nil || tags == nil {
		return false
	}
	known := func(name string) bool {
		a, err := s.media.FindTopLevelItem(ctx, libraryID, "artist", name)
		return err == nil && a != nil
	}
	credits := trackCredits(tags, known)
	var rows []TrackArtist
	seen := map[uuid.UUID]bool{albumArtistID: true}
	for _, c := range credits {
		id, ok := s.resolveCreditArtist(ctx, libraryID, c)
		if !ok || seen[id] {
			continue
		}
		seen[id] = true
		role := CreditArtist
		if c.Featured {
			role = CreditFeatured
		}
		rows = append(rows, TrackArtist{ArtistID: id, Role: role, Position: len(rows)})
	}
	if err := s.credits.SetTrackArtists(ctx, track.ID, rows); err != nil {
		s.logger.WarnContext(ctx, "store track artist credits failed", "track_id", track.ID, "err", err)
		return false
	}
	return true
}

// resolveCreditArtist finds (or, for a featured credit, creates) the
// library's artist for c.
func (s *Scanner) resolveCreditArtist(ctx context.Context, libraryID uuid.UUID, c ArtistCredit) (uuid.UUID, bool) {
	if c.MBID != uuid.Nil {
		if id, ok, err := s.credits.FindArtistByMusicBrainzID(ctx, libraryID, c.MBID); err == nil && ok {
			return id, true
		}
	}
	name := c.Name
	if flipped := flipLastFirst(name); flipped != "" {
		name = flipped
	}
	if a, err := s.media.FindTopLevelItem(ctx, libraryID, "artist", name); err == nil && a != nil {
		return a.ID, true
	}
	if !c.Featured {
		return uuid.Nil, false
	}
	p := media.CreateItemParams{
		LibraryID: libraryID,
		Type:      "artist",
		Title:     name,
		SortTitle: sortTitle(name),
	}
	if c.MBID != uuid.Nil {
		mbid := c.MBID
		p.MusicBrainzID = &mbid
		p.MusicBrainzArtistID = &mbid
	}
	a, err := s.media.FindOrCreateHierarchyItem(ctx, p)
	if err != nil || a == nil {
		s.logger.WarnContext(ctx, "create featured artist failed", "name", name, "err", err)
		return uuid.Nil, false
	}
	return a.ID, true
}

// healTrackCredits reads the credits of an unchanged track file once: a
// library scanned before credits were stored gains them on its next scan
// without re-probing anything. The file is remembered (track_credit_scans,
// and in memory for this process) so later scans skip it; a file that
// changes takes the slow path, which stores its credits anyway.
func (s *Scanner) healTrackCredits(ctx context.Context, libraryID uuid.UUID, item *media.Item, file *media.File, path string) {
	if s.credits == nil || item == nil || item.Type != "track" || item.ParentID == nil || file == nil {
		return
	}
	if _, seen := s.creditsChecked.LoadOrStore(file.ID, struct{}{}); seen {
		return
	}
	done, err := s.credits.CreditsScanned(ctx, file.ID)
	if err != nil || done {
		return
	}
	album, err := s.media.GetItem(ctx, *item.ParentID)
	if err != nil || album == nil || album.ParentID == nil {
		return
	}
	tags, err := ReadMusicTagsStore(ctx, s.mediaStore(), path)
	if err != nil || !tags.TagsRead {
		// Unreadable now; try again next process rather than marking it.
		return
	}
	if s.storeTrackCredits(ctx, libraryID, *album.ParentID, item, tags) {
		if err := s.credits.MarkCreditsScanned(ctx, file.ID); err != nil {
			s.logger.WarnContext(ctx, "mark track credits read failed", "file_id", file.ID, "err", err)
		}
	}
}
