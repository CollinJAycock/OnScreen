package scanner

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/onscreen/onscreen/internal/domain/media"
)

// Movie file naming beyond title and year: the edition a file is (Director's
// Cut, Extended, a Jellyfin version suffix) and the part it is of a stacked
// rip (cd1/cd2). Both are labels on the file (media_files.edition /
// part_index, migration 00045); neither changes which movie the file belongs
// to, so the title/year cleanTitle derives stays the item key (ADR-031).
//
// Everything here reads only what comes AFTER the year: "Harry Potter and the
// Deathly Hallows Part 1 (2010)", "Dune Part One (2021)" and "Kill Bill Vol. 1
// (2003)" carry their part words in the title, before the year, and are never
// stacked. A name without a year is read far more cautiously (see
// parseMovieFilename).

// MovieNaming is what a movie file's name says beyond its title and year.
type MovieNaming struct {
	// Edition is the edition label, "" when the name gives none.
	Edition string
	// PartIndex is the 1-based part number of a stacked rip, 0 for none.
	PartIndex int
}

// EditionPtr / PartIndexPtr are the nullable column values.
func (n MovieNaming) EditionPtr() *string {
	if n.Edition == "" {
		return nil
	}
	e := n.Edition
	return &e
}

func (n MovieNaming) PartIndexPtr() *int {
	if n.PartIndex <= 0 {
		return nil
	}
	p := n.PartIndex
	return &p
}

// plexEditionRE is Plex's explicit edition marker, "{edition-Director's Cut}",
// allowed in the file or the folder name.
var plexEditionRE = regexp.MustCompile(`(?i)[\s._-]*\{\s*edition-([^{}]+)\}`)

// partMarkerRE is a stacking marker with a number: cd, dvd, part, pt, disc or
// disk, optionally separated from the number by space/dot/dash/underscore
// ("cd1", "Part 2", "disc.3", "pt-1"). The marker must start the text or
// follow a separator, and the number must end the text or be followed by a
// separator or a bracket, so "1080p" or "PT2H" don't read as parts.
var partMarkerRE = regexp.MustCompile(`(?i)(?:^|[ ._\-]+)(cd|dvd|part|pt|disc|disk)[ ._\-]*(\d{1,2})(?:$|[ ._\-\[\(])`)

// partLetterRE is the lettered form: a marker word followed by a-d at the end
// of the text ("part b", "cd-a").
var partLetterRE = regexp.MustCompile(`(?i)(?:^|[ ._\-]+)(cd|dvd|part|pt|disc|disk)[ ._\-]*([a-d])$`)

// bareLetterRE is the bare "-a"/"-b" suffix. Only accepted when it is ALL
// that follows the year ("Movie (2001)-a.mkv", "Movie (2001) - b.mkv"), so a
// release group that happens to be one letter can't start a stack.
var bareLetterRE = regexp.MustCompile(`(?i)^[ ._]*-[ ._]*([a-d])$`)

// endPartMarkerRE is partMarkerRE anchored to the end of the text, for names
// without a year (see parseMovieFilename).
var endPartMarkerRE = regexp.MustCompile(`(?i)(?:^|[ ._\-]+)(cd|dvd|part|pt|disc|disk)[ ._\-]*(\d{1,2})$`)

// versionSuffixRE is Jellyfin's version suffix: " - <label>" right after the
// year ("Movie (2010) - Director's Cut", "Movie (2010) - 1080p"). Spaces on
// both sides of the dash, so a scene name's "x264-GROUP" never matches.
var versionSuffixRE = regexp.MustCompile(`^ +- +(.+)$`)

// editionTags is the recognised edition vocabulary, tried in order (more
// specific phrases first) against the text after the year with every
// separator and bracket turned into a space. Conservative on purpose: a
// mislabel is the only possible harm, but a label nobody asked for is noise.
var editionTags = []struct {
	re    *regexp.Regexp
	label string
}{
	{regexp.MustCompile(`(?:^| )director'?s? cut(?: |$)`), "Director's Cut"},
	{regexp.MustCompile(`(?:^| )extended edition(?: |$)`), "Extended Edition"},
	{regexp.MustCompile(`(?:^| )extended cut(?: |$)`), "Extended Cut"},
	{regexp.MustCompile(`(?:^| )extended(?: |$)`), "Extended"},
	{regexp.MustCompile(`(?:^| )theatrical(?: cut| edition| version)?(?: |$)`), "Theatrical"},
	{regexp.MustCompile(`(?:^| )unrated(?: |$)`), "Unrated"},
	{regexp.MustCompile(`(?:^| )uncut(?: |$)`), "Uncut"},
	{regexp.MustCompile(`(?:^| )imax(?: |$)`), "IMAX"},
	{regexp.MustCompile(`(?:^| )remastered(?: |$)`), "Remastered"},
	{regexp.MustCompile(`(?:^| )special edition(?: |$)`), "Special Edition"},
	{regexp.MustCompile(`(?:^| )ultimate (?:edition|cut)(?: |$)`), "Ultimate Edition"},
	{regexp.MustCompile(`(?:^| )final cut(?: |$)`), "Final Cut"},
	{regexp.MustCompile(`(?:^| )collector'?s edition(?: |$)`), "Collector's Edition"},
	{regexp.MustCompile(`(?:^| )anniversary edition(?: |$)`), "Anniversary Edition"},
	{regexp.MustCompile(`(?:^| )criterion(?: collection)?(?: |$)`), "Criterion"},
}

// tagSeparatorRE turns separators and brackets into spaces for the
// vocabulary match.
var tagSeparatorRE = regexp.MustCompile(`[\s._\-\[\]\(\)\{\}]+`)

// maxEditionLen bounds a free-text edition label (Jellyfin suffix, Plex
// marker); longer text is clearly not a label.
const maxEditionLen = 64

// splitAtYear finds the year the way cleanTitle does and returns the text
// before it, the year, and the text after it. ok=false when the name has no
// year in cleanTitle's window. cleanTitle's separator normalisation replaces
// one character with one, so indices into the normalised string are indices
// into name.
func splitAtYear(name string) (head string, year int, tail string, ok bool) {
	name = bracketPrefixRE.ReplaceAllString(name, "")
	norm := strings.ReplaceAll(strings.ReplaceAll(name, "_", "."), " ", ".")
	m := parenYearRE.FindStringSubmatchIndex(norm)
	if m == nil {
		m = yearRE.FindStringSubmatchIndex(norm)
	}
	if m == nil {
		return "", 0, "", false
	}
	y, err := strconv.Atoi(norm[m[2]:m[3]])
	if err != nil || y < 1888 || y > 2100 {
		return "", 0, "", false
	}
	return name[:m[0]], y, name[m[1]:], true
}

// partInTail finds the stacking marker in the text after the year and
// returns its number with the span it covers, or 0.
func partInTail(tail string) (part, start, end int) {
	// Walk the matches by hand rather than FindAll: a match consumes the
	// separator after its number, which is the next marker's leading
	// separator in "DVD9.cd2".
	for off := 0; off < len(tail); {
		m := partMarkerRE.FindStringSubmatchIndex(tail[off:])
		if m == nil {
			break
		}
		for i := range m {
			m[i] += off
		}
		off = m[5]
		word := strings.ToLower(tail[m[2]:m[3]])
		num := tail[m[4]:m[5]]
		// DVD5 / DVD9 are disc formats in DVD rip names, not parts.
		if word == "dvd" && m[4] == m[3] && (num == "5" || num == "9") {
			continue
		}
		n, _ := strconv.Atoi(num)
		if n <= 0 {
			continue
		}
		return n, m[0], m[5]
	}
	trimmed := strings.TrimRight(tail, " ")
	if m := partLetterRE.FindStringSubmatchIndex(trimmed); m != nil {
		return letterPart(trimmed[m[4]:m[5]]), m[0], m[1]
	}
	if m := bareLetterRE.FindStringSubmatchIndex(trimmed); m != nil {
		return letterPart(trimmed[m[2]:m[3]]), m[0], m[1]
	}
	return 0, 0, 0
}

func letterPart(s string) int { return int(strings.ToLower(s)[0]-'a') + 1 }

// editionInTail reads the edition from the text after the year: Jellyfin's
// " - label" suffix first, then the tag vocabulary. partStart/partEnd is the
// stacking marker's span in tail (equal when none), left out of a suffix.
func editionInTail(tail string, partStart, partEnd int) string {
	if m := versionSuffixRE.FindStringSubmatchIndex(tail); m != nil {
		label := tail[m[2]:m[3]]
		if partEnd > partStart && partEnd > m[2] {
			// Cut the part marker (and anything after it) out of the label:
			// "Director's Cut - cd1" names the edition, then the part, and
			// " - cd1" names no edition at all.
			label = tail[m[2]:max(partStart, m[2])]
		}
		if l := cleanEditionLabel(label); l != "" {
			return l
		}
	}
	norm := " " + strings.ToLower(tagSeparatorRE.ReplaceAllString(tail, " ")) + " "
	best, bestAt := "", -1
	for _, t := range editionTags {
		if loc := t.re.FindStringIndex(norm); loc != nil && (bestAt < 0 || loc[0] < bestAt) {
			best, bestAt = t.label, loc[0]
		}
	}
	return best
}

// cleanEditionLabel trims a free-text label: separators at either end, and
// bracket groups around or after it ("[1080p]" alone reads "1080p"). "" when
// nothing usable is left.
func cleanEditionLabel(s string) string {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "[") || strings.HasPrefix(s, "(") {
		// Only bracket groups: use the first one's content.
		closer := "]"
		if s[0] == '(' {
			closer = ")"
		}
		if i := strings.Index(s, closer); i > 0 {
			s = s[1:i]
		}
	} else if i := strings.IndexAny(s, "[("); i > 0 {
		s = s[:i]
	}
	s = strings.Trim(s, " ._-")
	if s == "" || len(s) > maxEditionLen {
		return ""
	}
	return s
}

// plexEdition returns the {edition-...} label in name, "" when none.
func plexEdition(name string) string {
	if m := plexEditionRE.FindStringSubmatch(name); m != nil {
		if l := strings.TrimSpace(m[1]); l != "" && len(l) <= maxEditionLen {
			return l
		}
	}
	return ""
}

// stripEditionMarker removes Plex {edition-...} markers so they never reach
// the title.
func stripEditionMarker(name string) string {
	return strings.TrimSpace(plexEditionRE.ReplaceAllString(name, ""))
}

// parseMovieFilename is parseFilename for movie files, plus the edition and
// stacked-part number the name carries. The title and year are exactly what
// parseFilename gives for every name that has no {edition-...} marker and no
// stacking marker, so adding a label never moves a file to another movie.
//
// Two cases do change the title, both only for a name WITHOUT a year (where
// cleanTitle keeps everything): a trailing stacking marker comes off the
// title, so "Kill Bill part1" and "Kill Bill part2" in "Kill Bill (2003)/"
// become one movie instead of two, and a file named only by its part
// ("cd1.mkv") takes its title from the folder like a Blu-ray "00000.m2ts".
// Such a marker is accepted only when the rest of the name matches the
// folder's title, the name is nothing but the marker, or the marker is one of
// cd/dvd/disc/disk (never part/pt, which titles use: "... Part 1").
func parseMovieFilename(path string) (title string, year *int, naming MovieNaming) {
	slashed := strings.ReplaceAll(filepath.ToSlash(path), `\`, `/`)
	base := filepath.Base(slashed)
	stem := base[:len(base)-len(filepath.Ext(base))]
	dir := filepath.Base(filepath.Dir(slashed))
	if dir == "." || dir == "/" || dir == string(os.PathSeparator) {
		dir = ""
	}

	naming.Edition = plexEdition(stem)
	if naming.Edition == "" {
		naming.Edition = plexEdition(dir)
	}
	cleanStem := StripFolderIDMarkers(stripEditionMarker(stem))
	cleanDir := StripFolderIDMarkers(stripEditionMarker(dir))

	// Blu-ray stream index: the folder names the movie (parseFilename).
	if numericNameRE.MatchString(stem) && cleanDir != "" {
		title, year = cleanTitle(cleanDir)
		return title, year, naming
	}

	if _, _, tail, ok := splitAtYear(cleanStem); ok {
		title, year = cleanTitle(cleanStem)
		part, ps, pe := partInTail(tail)
		naming.PartIndex = part
		if naming.Edition == "" {
			naming.Edition = editionInTail(tail, ps, pe)
		}
		return title, year, naming
	}

	// No year in the file name.
	title, year = cleanTitle(cleanStem)
	m := endPartMarkerRE.FindStringSubmatchIndex(cleanStem)
	if m == nil {
		return title, year, naming
	}
	part, _ := strconv.Atoi(cleanStem[m[4]:m[5]])
	if part <= 0 {
		return title, year, naming
	}
	rest := strings.Trim(cleanStem[:m[0]], " ._-")
	word := strings.ToLower(cleanStem[m[2]:m[3]])
	dirTitle, dirYear := "", (*int)(nil)
	if cleanDir != "" {
		dirTitle, dirYear = cleanTitle(cleanDir)
	}
	switch {
	case rest == "" && dirYear != nil:
		// "Movie (2001)/cd1.mkv": the folder names the movie.
		title, year = dirTitle, dirYear
	case rest != "" && dirYear != nil && strings.EqualFold(cleanTitleOnly(rest), dirTitle):
		// "Kill Bill (2003)/Kill Bill part1.mkv".
		title, year = dirTitle, dirYear
	case rest != "" && word != "part" && word != "pt":
		// "Movie cd1.mkv" with no year anywhere: cd/dvd/disc/disk are not
		// title words, so the marker is safe to take off.
		title, year = cleanTitle(rest)
	default:
		return title, year, naming
	}
	naming.PartIndex = part
	return title, year, naming
}

// cleanTitleOnly is cleanTitle's title alone.
func cleanTitleOnly(name string) string {
	t, _ := cleanTitle(name)
	return t
}

// healFileNaming writes the edition and part number an unchanged movie file's
// name gives it when the stored row disagrees: rows scanned before migration
// 00045 have neither. A pure name parse, so it costs nothing on a file whose
// row already agrees, and the write leaves the integrity verdict and
// scanned_at alone (the bytes haven't changed).
func (s *Scanner) healFileNaming(ctx context.Context, existing *media.File, path string) {
	_, _, n := parseMovieFilename(path)
	edition, part := n.EditionPtr(), n.PartIndexPtr()
	if sameStringPtr(existing.Edition, edition) && sameIntPtr(existing.PartIndex, part) {
		return
	}
	if err := s.media.SetFileNaming(ctx, existing.ID, edition, part); err != nil {
		s.logger.WarnContext(ctx, "set file naming failed", "path", path, "file_id", existing.ID, "err", err)
		return
	}
	existing.Edition, existing.PartIndex = edition, part
}

func sameStringPtr(a, b *string) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

func sameIntPtr(a, b *int) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}
