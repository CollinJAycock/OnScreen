// Package scanner - tvparse extracts show title, season number, and episode
// number from TV media filenames. It handles common naming conventions used by
// Kodi, Sonarr, and scene release groups.
package scanner

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"unicode"
)

// tvEpisodeRE matches S##E## patterns (case-insensitive): S01E03, s1e3, S01E03E04.
var tvEpisodeRE = regexp.MustCompile(`(?i)[.\s_-]*S(\d{1,2})E(\d{1,3})`)

// tvCrossRE matches the 1x03 pattern: "1x03", "01x03".
var tvCrossRE = regexp.MustCompile(`(?i)[.\s_-]+(\d{1,2})x(\d{1,3})`)

// tvAnimeAbsoluteRE matches "title - NN" anime conventions where the
// number is an absolute episode index rather than a season/episode
// pair. Captures:
//
//  1. Title (greedy minimum so the trailing " - NN" part doesn't get
//     eaten into the title).
//  2. Episode number (1-4 digits — covers everything from a 12-ep
//     season to long-runners like One Piece in the 1000s).
//
// Optional non-capturing `[Group]` prefix strips fansub release-group
// tags ([SubsPlease], [Erai-raws], etc.).
//
// The dash may be spaced with whitespace or with underscores: the
// 2008-2014 fansub archives (UTW, Doki, Chihiro, gg, Mazui, …) replaced
// every space with "_", giving "[UTW]_Fate_Zero_-_01_[BD]…". An
// optional episode word between the dash and the number ("E01",
// "Ep 01", "Ep.01", "Episode 01" — Reaktor's x265 releases are named
// "[Reaktor] Show - E01 [1080p]") is consumed and ignored. The full
// words may be followed by whitespace; a bare "E" must be glued to the
// digits ("Show - E 2019" is not an episode) and, like the revision
// token, only marks a zero-padded number (see spacedDashEpisodeOK):
// "Giant Bomb - E3 2019 Day 1" is the games expo. So is an optional
// glued revision token ("01v2" — the group re-released the same
// episode; routine for SubsPlease / Erai-raws), but only on a
// zero-padded episode: "3v3" / "5v5" in an esports rip is a score, not
// a revision. Trailing lookahead requires whitespace / bracket / dot /
// underscore / EOL so quality strings like `1080p` or `720p`
// (digit-letter, no separator) don't match — only standalone integer
// episode numbers. (?i) only touches the episode word and the revision
// letter, so "01V2" reads like "01v2".
//
// The dash is the explicit signal that what follows is an episode
// number. Bare "Show NN" patterns are too ambiguous (a year suffix
// like "Show 2024" shouldn't parse as episode 2024), so they stay
// rejected. This rule wants the dash SPACED (" - " / "_-_"); the
// unspaced "Title-NN" form BD-rip groups use is handled by the narrower
// sibling rule tvAnimeTightDashRE below, which also gets a go when this
// rule matched but its guards rejected the match — and, when this rule
// matches a Sonarr-style unspaced name at the wrong dash ("Dungeon
// Meshi-13 - 7 Days" reads here as show "Dungeon Meshi-13", episode 7),
// spacedMatchAtWrongDash prefers the tight rule's reading (title
// "Dungeon Meshi", episode 13) whenever it has one.
var tvAnimeAbsoluteRE = regexp.MustCompile(`(?i)^(?:\[[^\]]+\]\s*)?(.+?)[\s_]+-[\s_]+(?:(?:Episode|Ep\.?)\s*|E)?(\d{1,4})(?:v\d+)?(?:\s|\[|\.|_|$)`)

// episodeKindWords is the episode-kind vocabulary DetectEpisodeKind
// recognises in a filename (OVA / ONA / OAD / special / PV / MV, with
// plurals). episodeKindRE and animeMarkerWords are both built from it,
// so the words the anime rules accept after an episode number and the
// words the kind detector tags can never drift apart.
const episodeKindWords = `OAD|OVAs?|ONAs?|SPECIALS?|SP|PV|MV`

// animeMarkerWords is the closed list of words the tight rule accepts
// between the episode number and its trailing anchor: the finale marker
// END / FIN / FINAL (Moozzi2's convention for the last episode,
// "Dungeon Meshi-24 END"), TV / RAW, and episodeKindWords (Moozzi2's
// KILL la KILL BD-BOX ends "- 24 END", "- 25 OVA"). A closed list, so
// codec tokens stay unreachable — "Title-13 1080p" must keep failing
// the anchor.
const animeMarkerWords = `END|FIN|FINAL|TV|RAW|` + episodeKindWords

// The clauses of tvAnimeTightDashRE, named so each can be read on its
// own and so the marker guards below (episodeMarkerTailRE,
// tightMarkerTailRE, tightMarkerInsideRE, spacedSuffixRE) recognise an
// episode marker with exactly the suffixes the rule itself accepts.
// Left to right they are the shape tvAnimeTightDashRE documents.
const (
	// tightDashGroupTags: zero or more leading `[Group]` tags.
	tightDashGroupTags = `(?:\[[^\]]+\]\s*)*`
	// tightDashTitle: capture 1, the show title; may not contain a
	// bracket.
	tightDashTitle = `([^\[\]]*?)`
	// tightDashEpisodeWord: an optional episode word in front of the
	// dash, consumed so it can't end up in the show title.
	tightDashEpisodeWord = `(?:[\s._-]+(?:Episode|Ep)\.?)?`
	// tightDashNumber: the dash and capture 2, the episode digits.
	tightDashNumber = `-(\d{2,4})`
	// tightDashRevision: an optional revision token ("v2", " v2", "_v2",
	// ".v2" — the dot-everything spelling).
	tightDashRevision = `(?:[\s_.]*v\d+)?`
	// tightDashMarkers: zero or more words from animeMarkerWords, each
	// preceded by any run of whitespace / underscores / dots and ended
	// by a word boundary or an underscore. RE2 counts "_" as a word
	// character, so "END_[BD]" — the underscore-everything spelling of
	// "END [BD]" — has no boundary after the D and needs the second
	// alternative; "Ending" still fails both.
	tightDashMarkers = `(?:[\s_.]*(?:` + animeMarkerWords + `)(?:\b|_))*`
	// tightDashSuffix: everything the rule accepts between the digits
	// and the anchor — a revision on either side of the marker words
	// ("24v2 END" and "24 END v2" are both Moozzi2 re-release forms).
	tightDashSuffix = tightDashRevision + tightDashMarkers + tightDashRevision
	// tightDashAnchor: capture 3, the strict trailing anchor — a
	// bracket, a paren, a spaced dash or the end of the stem, after any
	// run of whitespace / underscores / dots.
	tightDashAnchor = `[\s_.]*([\[(]|[\s_]-[\s_]|$)`
)

// tvAnimeTightDashRE is the unspaced sibling of tvAnimeAbsoluteRE for
// the BD-rip naming Moozzi2 and similar groups use:
//
//   - "[Moozzi2] Dungeon Meshi-13 [BD 1920x1080 x265-10Bit 2Audio].mkv"
//   - "[Moozzi2] Dungeon Meshi-24 END [BD 1920x1080 x265-10Bit 2Audio].mkv"
//   - "[Moozzi2] KILL la KILL-25 OVA [BD 1920x1080 x.264 FLACx2].mkv"
//   - "[Moozzi2] Kanon (2006)-01 [BD 1920x1080 x264 FLAC].mkv"
//
// Every TV parser rejected these (no S##E##, no 1x03, no date, and the
// spaced rule insists on " - "), so processShowHierarchy fell back to a
// parentless flat "episode" titled by the movie parser — "Dungeon
// Meshi-13 [BD", year 1920 scraped from 1920x1080. On QA that orphaned
// 22 of 24 Delicious in Dungeon episodes and 100+ more across Mushishi,
// Dororo, Clannad, Kanon and KILL la KILL.
//
// Shape, left to right (one named clause each, see the consts above):
//
//   - optional leading whitespace, then zero or more `[Group]` tags
//     (Moozzi2 writes one, re-encode chains stack them; "[Erai-raws]"
//     carries a dash inside the tag, which is why tags are consumed
//     before the title is looked at);
//   - title (capture 1): may not contain a bracket, so the "-10" in
//     "[BD … x265-10Bit …]" is unreachable — the title can never run
//     into the quality block. Parens ARE allowed: "Kanon (2006)" is a
//     real Moozzi2 title. May be empty ("[Group]-13 [BD]" inside a show
//     folder), in which case extractShowTitle falls back to the folder
//     name exactly as the spaced rule and the S##E## rule do. Documented
//     divergence: a bracketed subtitle inside the title ("Fate stay
//     night [Unlimited Blade Works]-01") cannot be a title here and
//     stays an orphan; its spaced form parses;
//   - optional episode word in front of the dash ("Show Name Ep-03",
//     "Show Name Episode-05"), consumed so it can't end up in the show
//     title. Before the dash only: "Title-E13" / "Title-Ep13" are not
//     read by this rule (no group writes them; the spaced rule's
//     "- E13" is Reaktor's form and stays covered there);
//   - "-" then 2-4 digits (capture 2). Unspaced releases zero-pad, and
//     accepting a single digit would turn "Spider-Man-2", "Ranma 1-2",
//     "Part-1" and "Vol-1" into episodes;
//   - optional revision ("13v2", "13V2", "13 v2", "13_v2") before or
//     after the marker words ("24v2 END", "24 END v2");
//   - optional trailing words from animeMarkerWords, case-insensitive,
//     each followed by a word boundary or an underscore ("END [BD]",
//     "END_[BD]", "END.[BD]");
//   - STRICT trailing anchor (capture 3): a bracket, a paren, a spaced
//     dash (a Sonarr-style " - Episode Title" after the number, spaced
//     with whitespace or underscores) or the end of the stem, after any
//     whitespace, underscores or dots ("Title-13_[BD]",
//     "[Group].Show.Name-13.[BD]"). Deliberately narrower than the
//     spaced rule's `(?:\s|\[|\.|_|$)` because an unspaced dash also
//     appears in codec / quality tokens ("x265-10bit", "H.264-AAC",
//     "-2HD"). Accepted consequence: "Title-13 1080p.mkv" and
//     "Show.Name-13.1080p.mkv" (bare quality token after the number)
//     are NOT supported by this rule — the spaced forms are.
//
// A no-break space (U+00A0) or ideographic space (U+3000) anywhere in
// the stem is read as a plain space first — RE2's `\s` is ASCII-only,
// and Japanese-origin names and some renamers emit them ("進撃の巨人-13
// [BD]" with U+3000 before the bracket).
//
// tightDashEpisodeOK applies the guards the regex alone can't express:
// a "(YYYY)" / "[YYYY]" movie year after the number, a digits-only
// title, a title that itself ends in or contains an episode marker, a
// season range or an ascending number pair, a part / volume / disc /
// extra word, a codec word, or a season marker. The year window itself
// is shared with the spaced rule (animeEpisodeFromMatch).
var tvAnimeTightDashRE = regexp.MustCompile(`(?i)^\s*` + tightDashGroupTags + tightDashTitle + tightDashEpisodeWord + tightDashNumber + tightDashSuffix + tightDashAnchor)

// unicodeSpaceReplacer folds the two non-ASCII spaces seen in release
// names onto a plain space before the anime rules run (see
// tvAnimeTightDashRE). cleanShowTitle splits on Unicode whitespace
// already, so the title that comes out is the same either way.
var unicodeSpaceReplacer = strings.NewReplacer("\u00a0", " ", "\u3000", " ")

// episodeMarkerTailRE matches a raw title capture that itself ends in
// an episode marker — spaced (" - 01"), tight ("-01") or a bare
// zero-padded pair (" 01", ".01", " E01", " Ep01") — with the same
// optional revision / marker-word suffix the main rule accepts
// (tightDashSuffix). Together with tightMarkerInsideRE — which refuses
// the unspaced form "Title-01-02 [BD]" before this guard is consulted —
// it is what keeps a double-episode or batch file from parsing as the
// wrong show: "Show Name 01-02 [BD]", "Show Name E01-02 [BD]" and the
// one-digit spaced form "Show - 1-02 [BD]" reach tvAnimeTightDashRE as
// title "Show Name 01" / "Show Name E01" / "Show - 1", episode 2, which
// would file them under a show that doesn't exist. Today those files are
// orphans; a wrong-show placement would be a regression, so they stay
// orphans. The same guard rejects "Show Name-2024-13" and keeps a date
// like "Show Name-2024-01-15" out of this parser entirely (the daily
// parser, earlier in the chain, owns it). Ranges in long-runner
// numbering ("One Piece 1071-1072", "Show Name 001-012") end in three
// or four digits and are caught by tightDashEpisodeOK's ascending-pair
// check instead.
//
// Accepted losses, all with a spaced form that still parses: titles
// that themselves end in a hyphen-number ("Catch-22-01",
// "Mob-Psycho-100-05", "Ranma 1-2-01" — the only way to spell Ranma ½
// in a filename — "22-7-01", "Ghost in the Shell SAC-2045-01"), and
// titles that end in a bare two-digit number, in the unspaced form only
// ("Digimon Adventure 02-01 [BD]" is the headline case, with "Area
// 88-01", "Ultraman 80-01", "Ben 10-11", "Gundam 00-01"; "Digimon
// Adventure 02 - 01" parses). Titles ending in one digit
// ("Steins;Gate 0-05") are unaffected.
var episodeMarkerTailRE = regexp.MustCompile(`(?i)(?:(?:\s-\s|-)\d{1,4}|[\s._](?:Episode|Ep\.?|E)?\d{2})` + tightDashSuffix + `$`)

// tightMarkerTailRE matches a raw title that ends in an unspaced
// episode marker with the suffixes the tight rule accepts ("Dungeon
// Meshi-13", "Dungeon Meshi-24 END v2"). spacedMatchAtWrongDash uses it
// to spot the spaced rule matching a Sonarr-style unspaced name at the
// wrong dash.
var tightMarkerTailRE = regexp.MustCompile(`(?i)-\d{2,4}` + tightDashSuffix + `$`)

// tightMarkerInsideRE matches an unspaced episode marker anywhere in a
// raw title, followed by a separator or the end: "-13 " in "Show-13
// x265". The tight rule's lazy title walks past an unspaced number
// whose anchor failed ("-13 x265": a bare codec token is not an anchor)
// and binds to a later one that has an anchor ("x265-10 [BD]"), which
// would file "Show-13 x265-10 [BD]" under a show called "Show-13 x265".
// Such a name is an orphan without the tight rule and stays one.
var tightMarkerInsideRE = regexp.MustCompile(`(?i)-\d{2,4}` + tightDashSuffix + `(?:[\s_.]|$)`)

// spacedSuffixRE matches the revision / marker-word suffix the rules
// accept right after an episode number (" v2", " END", "v2 END v2");
// spacedMatchAtWrongDash consumes it before asking whether a word
// follows the number, so that " END" and " v2" are never the word.
var spacedSuffixRE = regexp.MustCompile(`(?i)^` + tightDashSuffix)

// wordAfterNumberRE matches what follows an episode number and its
// suffix when a word comes next — whitespace or underscores (the spaced
// rule accepts both as the separator) and then a letter: " Days [BD]"
// out of "Dungeon Meshi-13 - 7 Days [BD]", "_Days_[BD]" out of
// "[Group]_Show-01_-_7_Days_[BD]".
var wordAfterNumberRE = regexp.MustCompile(`^[\s_]+\pL`)

// trailingBracketGroupRE matches one balanced bracket or paren group at
// the end of a raw title, with the separators before it: " [BD
// 1920x1080]" out of "Dungeon Meshi-13 [BD 1920x1080]".
// spacedMatchAtWrongDash strips these before looking for the tight
// marker, so a quality block between the unspaced number and a
// Sonarr-style " - 7 Days" doesn't hide it.
var trailingBracketGroupRE = regexp.MustCompile(`[\s_.]*[\[(][^\])]*[\])]$`)

// trailingNumberRE captures a bare 3-4 digit number that ends a raw
// title ("Premier League 2023", "One Piece 1071"); tightDashEpisodeOK
// compares it with the episode capture.
var trailingNumberRE = regexp.MustCompile(`(?:^|[\s._])(\d{3,4})$`)

// bareEpisodeWordRE matches a raw title that is nothing but an episode
// word ("Ep", "Episode", "Ep."): "Episode-01" in a show folder names
// the episode, not a show called "Episode".
var bareEpisodeWordRE = regexp.MustCompile(`(?i)^(?:Episode|Ep)\.?$`)

// tightDashJunkTailRE matches a raw title whose last word says the
// number after the dash is a part / volume / disc index or a BD-extra
// index (creditless OP / ED, menu, preview, commercial), not an
// episode. The regex's 2-digit minimum already refuses "Part-1" and
// "Vol-1"; these are the zero-padded siblings ("Part-01", "Vol.01",
// "Disc 1-01", "NCOP-01"), which would otherwise create a show called
// "<Show> Part". "Part" / "Pt" must be the last word outright — "Final
// Season Part 2-01" is a real cour name and keeps parsing — while a
// disc / volume / extra word may carry its own number ("Disc 1",
// "Vol.01"), since no title ends that way.
//
// A kind word before the dash ("Show Name OVA-01", "Show Name SP-01") is
// deliberately NOT here: it names a separate AniList entry (OVAs and
// specials are listed apart from the TV series), so show "Show Name OVA"
// is the intended reading — the spaced form has always read it that way.
var tightDashJunkTailRE = regexp.MustCompile(`(?i)(?:^|[\s._-])(?:(?:Part|Pt)\.?|(?:Vol|Volume|Disc|Disk|CD|DVD|NCOP|NCED|OP|ED|Menu|Preview|CM|Trailer)\.?\s*\d*)$`)

// codecTailRE matches a raw title whose last word is a codec, audio or
// rate token: the number after the dash is a bit depth, sample width,
// bitrate or an all-numeric scene-group id, not an episode — "x265-10
// [BD]" (the unit detached), "FLAC-24 [BD]", "Opus-128", "x264-1337".
// The strict anchor already refuses the glued forms ("x265-10bit",
// "-2HD"); this covers the ones it cannot see. No show title ends in
// one of these words.
var codecTailRE = regexp.MustCompile(`(?i)(?:^|[\s._-])(?:x26[45]|h\.?26[45]|hevc|avc|av1|vp9|aac|flac|opus|dts|ddp?|ac3|eac3|truehd|mp3|kbps|fps)$`)

// finaleOrRevisionRE matches a suffix between the episode digits and
// the anchor that is nothing but finale markers and revision tokens
// ("END", "v2", "END v2") — the Moozzi2 finale / re-release forms, which
// only an episode carries. Other marker words ("Special", "TV") are
// ordinary words after a broadcast-year range ("Top Gear 2010-11
// Special") and do not lift the season-range guard.
var finaleOrRevisionRE = regexp.MustCompile(`(?i)^(?:[\s_.]*(?:v\d+|END|FIN|FINAL))+$`)

// bracketYearAfterRE matches a "(YYYY" / "[YYYY" group opening right
// after the episode number — "(1970)", "[1970]", "(1970 Remaster)" —
// capturing the year and the rest of the group; tightDashEpisodeOK
// range-checks the year and, via digitRunRE, refuses to call the group
// a year when it carries another number: "(1920 x 1080 x265)" is a
// resolution written with spaces. The word boundary keeps a year-window
// number glued to a letter — "(2019p)" — from being read as a year;
// "(1080p)" is outside the window regardless. Residuals: a non-year
// group in between, "(US) (1970)", is not
// looked through; a release year placed after the number, "Title-13
// (2019) [1080p]" / "Title-13 [2019][1080p]", has the exact shape of
// "Catch-22 (2019) [1080p]" and reads as the movie.
var bracketYearAfterRE = regexp.MustCompile(`^[\[(](\d{4})\b([^\])]*)`)

// digitRunRE matches three digits in a row — a resolution, bitrate or
// codec number inside a bracket group.
var digitRunRE = regexp.MustCompile(`\d{3}`)

// seasonFolderRE matches season folder names — "Season 1", "Season 01",
// "season1", "S01" — with or without a decoration after the number:
// "Season 2 - After Story", "Season 02 (2008)", "S02.After.Story"
// (Jellyfin tolerates text after the number, and hand-named anime
// layouts use it). The number is 1-2 digits and must be followed by the
// end of the name, a separator or an opening paren, so "Season 100" and
// "Season 2024" are not season folders. Captures: 1 for the "Season N"
// spelling, 2 for "SNN". Used by extractShowTitle to skip season folders
// in the title walk and by seasonFromFolder to read the season.
var seasonFolderRE = regexp.MustCompile(`(?i)^(?:Season\s*(\d{1,2})|S(\d{1,2}))(?:$|[\s._-]|\()`)

// tvDailyRE matches date-based (daily / talk-show) episode filenames:
//
//   - "The Daily Show - 2013-10-30 - Guest Name WEBDL-1080p.mkv" (Sonarr daily format)
//   - "The.Daily.Show.2024.01.15.Guest.1080p.WEB.mkv"            (scene naming)
//   - "Conan 2019_06_03 Episode.mkv"
//
// Captures: 1 title, 2 year, 3 month, 4 day. The year is anchored to
// 19xx/20xx so a stray 4-digit number can't start a false date, and the
// month/day pairs are range-validated in code (regex alone would accept
// "2013-99-99"). Date-based shows have no usable S##E## identity — Sonarr
// numbers daily seasons by YEAR (4 digits), which the S##E## pattern
// rightly refuses — so the caller maps season = year and derives a
// deterministic episode index from the date.
var tvDailyRE = regexp.MustCompile(`^(?:\[[^\]]+\]\s*)?(.+?)[\s._-]+((?:19|20)\d{2})[.\s_-](\d{2})[.\s_-](\d{2})(?:[\s._-]|$)`)

// ParseTVFilename extracts the show title, season number, and episode number
// from a media file path. It handles:
//   - "Show Name S01E03.mkv" or "Show.Name.S01E03.mkv"
//   - "Show Name - S01E03 - Episode Title.mkv"
//   - "Show Name/Season 1/Show Name S01E03.mkv" (folder structure)
//   - "Show Name 1x03.mkv"
//
// Returns (showTitle, season, episode, ok). If parsing fails, ok is false.
func ParseTVFilename(path string) (showTitle string, season int, episode int, ok bool) {
	// Normalise path separators so we can split on "/" uniformly, including
	// backslashes in Windows-style paths received on non-Windows hosts.
	path = strings.ReplaceAll(filepath.ToSlash(path), `\`, `/`)

	base := filepath.Base(path)
	ext := filepath.Ext(base)
	stem := base[:len(base)-len(ext)]

	// Try S##E## first (most common and reliable).
	if m := tvEpisodeRE.FindStringSubmatchIndex(stem); m != nil {
		s, _ := strconv.Atoi(stem[m[2]:m[3]])
		e, _ := strconv.Atoi(stem[m[4]:m[5]])
		title := extractShowTitle(stem[:m[0]], path)
		if title != "" {
			return title, s, e, true
		}
	}

	// Try 1x03 pattern. The rule has no bracket awareness of its own, so
	// a match whose prefix ran into an open bracket / paren is an aspect
	// ratio or channel token inside a quality block ("[BD 4x3]",
	// "(2x2 Audio)"), not a season / episode: skip it so a later
	// balanced match — or, failing that, the anime rules — get their
	// turn ("[Group] Show-01 [BD 4x3]" is episode 1 of "Show").
	for _, m := range tvCrossRE.FindAllStringSubmatchIndex(stem, -1) {
		if hasUnclosedBracket(stem[:m[0]]) {
			continue
		}
		s, _ := strconv.Atoi(stem[m[2]:m[3]])
		e, _ := strconv.Atoi(stem[m[4]:m[5]])
		title := extractShowTitle(stem[:m[0]], path)
		if title != "" {
			return title, s, e, true
		}
	}

	return "", 0, 0, false
}

// ParseDailyFilename extracts a show title and air date from date-based
// (daily / talk-show) filenames. Use as a fallback after [ParseTVFilename]
// returns ok=false — an explicit S##E## always wins over a date that might
// also appear in the name.
//
// Returns (showTitle, year, month, day, ok). The caller maps these onto the
// show hierarchy Plex-style: season = year, episode index derived from the
// date (month*100 + day), which is unique within the year and sorts
// chronologically.
func ParseDailyFilename(path string) (showTitle string, year, month, day int, ok bool) {
	path = strings.ReplaceAll(filepath.ToSlash(path), `\`, `/`)
	base := filepath.Base(path)
	ext := filepath.Ext(base)
	stem := base[:len(base)-len(ext)]

	m := tvDailyRE.FindStringSubmatchIndex(stem)
	if m == nil {
		return "", 0, 0, 0, false
	}
	y, _ := strconv.Atoi(stem[m[4]:m[5]])
	mo, _ := strconv.Atoi(stem[m[6]:m[7]])
	d, _ := strconv.Atoi(stem[m[8]:m[9]])
	if mo < 1 || mo > 12 || d < 1 || d > 31 {
		return "", 0, 0, 0, false
	}
	title := extractShowTitle(stem[m[2]:m[3]], path)
	if title == "" {
		return "", 0, 0, 0, false
	}
	return title, y, mo, d, true
}

// ParseAnimeAbsoluteFilename extracts a show title and absolute
// episode number from anime-style filenames using the "title - NN"
// convention common in fansub releases and the unspaced "title-NN"
// convention BD-rip groups use:
//
//   - "Show Name - 01.mkv"
//   - "Show Name - 1071 [1080p].mkv"
//   - "[SubsPlease] Show Name - 245 (HDR).mkv"
//   - "[SubsPlease] Show Name - 01v2 (1080p).mkv"
//   - "[UTW]_Fate_Zero_-_01_[BD][h264-1080p_FLAC][A1B2C3D4].mkv"
//   - "[Reaktor] Show Name - E01 [1080p][x265][10-bit].mkv"
//   - "Show.Name - 12.mkv"
//   - "[Moozzi2] Dungeon Meshi-13 [BD 1920x1080 x265-10Bit 2Audio].mkv"
//   - "[Moozzi2] Dungeon Meshi-24 END [BD 1920x1080 x265-10Bit 2Audio].mkv"
//   - "[Moozzi2] KILL la KILL-25 OVA [BD 1920x1080 x.264 FLACx2].mkv"
//
// Use as a fallback after [ParseTVFilename] returns ok=false.
//
// Returns (showTitle, episode, ok). The episode is an absolute
// number; the caller slots the file into the season named by its
// "Season N" folder, else a synthetic Season 1 (see seasonFromFolder —
// a single season is conventional for the flat anime layout, where
// long-running series flat-list all episodes by absolute number).
//
// The spaced rule (tvAnimeAbsoluteRE) is tried first; the tight rule
// (tvAnimeTightDashRE) when the spaced one misses or its guards reject
// the match — "[Group] Show-01 (x265 - 10 bit)" is the spaced rule
// misreading " - 10 " inside the quality block (title "Show-01 (x265")
// while the tight rule reads the name correctly. When the spaced rule
// matches a Sonarr-style unspaced name at the wrong dash ("Dungeon
// Meshi-13 - 7 Days": show "Dungeon Meshi-13", episode 7) the tight
// rule's reading is preferred if it has one, and the spaced reading is
// kept otherwise (see spacedMatchAtWrongDash).
//
// Returns ok=false when:
//   - neither rule matches: no " - " (or, for the tight rule, "-")
//     separator before a digit run with an acceptable trailing anchor
//   - the digit run is zero (Episode 0 is reserved for the synthetic
//     "all the things that aren't episodes yet" placeholder elsewhere)
//   - the number is a 4-digit year (1888..2100, cleanTitle's window):
//     "Show - 2019" and "Altered Carbon-2018" are the movie parser's
//   - the raw title ran into an unclosed bracket / paren, i.e. it
//     swallowed part of the quality block
//   - spaced rule only: the number is fractional ("12.5" is a recap),
//     carries a glued revision equal to itself ("3v3", "10v10" are
//     scores), or is a single digit introduced by a bare "E" ("E3" is
//     the games expo) (see spacedDashEpisodeOK)
//   - tight rule only: the number is followed by a "(YYYY)" / "[YYYY]"
//     movie year, or the raw title is digits only, itself ends in or
//     contains an episode marker, a season range or an ascending number
//     pair, a part / volume / disc / extra word, a codec word, or a
//     season marker (see tightDashEpisodeOK)
//   - the title prefix is empty after cleaning
func ParseAnimeAbsoluteFilename(path string) (showTitle string, episode int, ok bool) {
	path = strings.ReplaceAll(filepath.ToSlash(path), `\`, `/`)
	base := filepath.Base(path)
	ext := filepath.Ext(base)
	stem := unicodeSpaceReplacer.Replace(base[:len(base)-len(ext)])

	if m := tvAnimeAbsoluteRE.FindStringSubmatchIndex(stem); m != nil && spacedDashEpisodeOK(stem, m) {
		if title, e, matched := animeEpisodeFromMatch(stem, path, m); matched {
			if spacedMatchAtWrongDash(stem, m) {
				if tightTitle, tightE, tightOK := parseTightDash(stem, path); tightOK {
					return tightTitle, tightE, true
				}
			}
			return title, e, true
		}
	}
	return parseTightDash(stem, path)
}

// parseTightDash runs the tight rule and its guards over a stem.
func parseTightDash(stem, path string) (string, int, bool) {
	if m := tvAnimeTightDashRE.FindStringSubmatchIndex(stem); m != nil && tightDashEpisodeOK(stem, m) {
		return animeEpisodeFromMatch(stem, path, m)
	}
	return "", 0, false
}

// animeEpisodeFromMatch turns a rule match (m from
// FindStringSubmatchIndex: group 1 the raw title, group 2 the episode
// digits) into the parser's result, applying the guards both rules
// share: episode 0 is reserved; a 4-digit number inside cleanTitle's
// year window is a year, not an episode; a raw title whose last bracket
// is an opener has run into the quality block ("Show-01 (x265" out of
// "(x265 - 10 bit)") and is not a title; a raw title that is nothing
// but an episode word ("Episode-01", "Ep - 01" inside the show's
// folder) names the episode, not a show called "Episode", and is
// treated as empty; and the title must survive cleaning, folder
// fallback included.
func animeEpisodeFromMatch(stem, path string, m []int) (string, int, bool) {
	e, err := strconv.Atoi(stem[m[4]:m[5]])
	if err != nil || e == 0 {
		return "", 0, false
	}
	// A 4-digit "episode" inside the year window is a year, whichever
	// rule matched: "Altered Carbon-2018" is a documented QA fixture
	// shape (scanner_test.go) that the movie parser owns — it strips the
	// suffix into year=2018 — and "Show - 2019" / "Show_-_2019_[BD]" are
	// the same file with a spaced dash. Same window as cleanTitle so the
	// TV and movie parsers can never disagree about what a year is. Cost:
	// "One Piece-1999 [BD]" falls back to the flat item; no
	// absolute-numbered series reaches 1888.
	if inYearWindow(e) {
		return "", 0, false
	}
	rawTitle := stem[m[2]:m[3]]
	if hasUnclosedBracket(rawTitle) {
		return "", 0, false
	}
	if bareEpisodeWordRE.MatchString(strings.TrimSpace(rawTitle)) {
		rawTitle = ""
	}
	title := extractShowTitle(rawTitle, path)
	if title == "" {
		return "", 0, false
	}
	return title, e, true
}

// hasUnclosedBracket reports whether the last bracket or paren in s is
// an opener: s ran into a quality block ("Show-01 (x265" out of
// "(x265 - 10 bit)", "[Group] Show-01 [BD" out of "[BD 4x3]") and is
// not a title prefix.
func hasUnclosedBracket(s string) bool {
	return strings.LastIndexAny(s, "[(") > strings.LastIndexAny(s, "])")
}

// spacedDashEpisodeOK applies the guards tvAnimeAbsoluteRE can't
// express on its own.
//
// A fractional number is a recap, not an episode: "[SubsPlease] Show -
// 12.5 (1080p)" is how SubsPlease / Erai-raws name the mid-season recap
// most seasons, and reading it as episode 12 files it ON TOP of the
// real episode 12. The regex accepts the dot in its lookahead so that
// "Show - 12.Episode.Title" still parses; only a dot followed by a
// digit is refused. The tight rule already refuses "Title-12.5".
//
// A glued revision equal to the episode number is a score or a format
// at any width — "3v3", "5v5", "10v10 Custom Games", "12v12" in an
// esports or YouTube rip kept in a show library — not a re-release. On
// a single-digit episode ANY glued revision is refused: every fansub
// group zero-pads ("01v2"), and before the revision token was accepted
// "1v100" was rejected outright. The same reasoning applies to a bare
// "E" in front of a single digit: Reaktor zero-pads ("Show - E01"),
// nobody writes "E3" for an episode, and "Giant Bomb - E3 2019 Day 1"
// is the games expo. "Episode3" / "Ep3" are not bare — the byte before
// the "E" / "p" is a letter — and keep parsing.
//
// Accepted loss: the spaced rule is not retried at a later " - NN"
// after a rejection here, so "Show - 3v3 - 05" (a score, then a real
// episode number) stays an orphan rather than show "Show - 3v3".
func spacedDashEpisodeOK(stem string, m []int) bool {
	digits := stem[m[4]:m[5]]
	rest := stem[m[5]:]
	if len(rest) > 1 && rest[0] == '.' && isASCIIDigit(rest[1]) {
		return false
	}
	if len(rest) > 0 && (rest[0] == 'v' || rest[0] == 'V') {
		rev := rest[1:]
		if i := strings.IndexFunc(rev, func(r rune) bool { return r < '0' || r > '9' }); i >= 0 {
			rev = rev[:i]
		}
		if rev != "" && rev == digits {
			return false
		}
	}
	if len(digits) > 1 {
		return true
	}
	if len(rest) > 0 && (rest[0] == 'v' || rest[0] == 'V') {
		return false
	}
	i := m[4]
	if i > 0 && (stem[i-1] == 'E' || stem[i-1] == 'e') && (i < 2 || !isASCIILetter(stem[i-2])) {
		return false
	}
	return true
}

// spacedMatchAtWrongDash reports whether a spaced-rule match looks like
// a Sonarr-style unspaced name matched at the wrong dash — "Dungeon
// Meshi-13 - 7 Days", "[Group]_Show-01_-_7_Days_[BD]", "Dungeon
// Meshi-13 [BD 1920x1080] - 7 Days", "Dungeon Meshi-13 - Episode 7" —
// which the spaced rule reads as show "Dungeon Meshi-13", episode 7,
// and the tight rule reads correctly. ParseAnimeAbsoluteFilename then
// prefers the tight rule's reading, and keeps the spaced one when the
// tight rule has none (so a misfire can never orphan a file). Three
// things must hold:
//
//   - the spaced number is a single unpadded digit. Fansub and BD-rip
//     groups zero-pad, so "01" IS an episode number, and a
//     hyphen-number title's spaced form keeps parsing as itself:
//     "Catch-22 - 01 Pilot", "R-15 - 01 END", "Mob-Psycho-100 - 05 v2"
//     stay show "Catch-22" episode 1 (and so on) — the documented
//     workaround for their unspaced forms. A bare "7" is a title word
//     ("7 Days", "2 Hours Later"). Unpadded numbers of two or more
//     digits are ambiguous ("Catch-22 - 12 Pilot" is episode 12 of
//     Catch-22, "Dungeon Meshi-13 - 24 Hours" is episode 13 of Dungeon
//     Meshi) and keep the spaced reading, which is what main did;
//   - the raw title, less any trailing bracket / paren groups, ends in
//     a tight episode marker ("-13", "-24 END v2");
//   - the number is decorated: a word follows it, past the revision /
//     marker suffix the rules accept (" v2" and " END" are never the
//     word: "R-15 - 1 END" is episode 1), or an episode word introduced
//     it ("- Episode 7").
//
// Residuals, pinned so a change to them is deliberate: "Catch-22 - 1
// Pilot" (an unpadded single-digit episode of a hyphen-number show,
// no second dash) reads as show "Catch", episode 22; "Dungeon Meshi-13
// - 24 Hours" and "[Group] Show-01 [BD] - 10 Bit" keep main's reading,
// show "Dungeon Meshi-13" / "Show-01 [BD]".
func spacedMatchAtWrongDash(stem string, m []int) bool {
	if m[5]-m[4] != 1 {
		return false
	}
	rawTitle := strings.TrimSpace(stem[m[2]:m[3]])
	for {
		stripped := trailingBracketGroupRE.ReplaceAllString(rawTitle, "")
		if stripped == rawTitle {
			break
		}
		rawTitle = stripped
	}
	if !tightMarkerTailRE.MatchString(rawTitle) {
		return false
	}
	if strings.IndexFunc(stem[m[3]:m[4]], unicode.IsLetter) >= 0 {
		return true
	}
	rest := stem[m[5]:]
	if loc := spacedSuffixRE.FindStringIndex(rest); loc != nil {
		rest = rest[loc[1]:]
	}
	return wordAfterNumberRE.MatchString(rest)
}

// isASCIILetter reports whether b is an ASCII letter.
func isASCIILetter(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z')
}

// isASCIIDigit reports whether b is an ASCII digit.
func isASCIIDigit(b byte) bool {
	return b >= '0' && b <= '9'
}

// tightDashEpisodeOK applies the guards tvAnimeTightDashRE can't
// express on its own. Every rejection here leaves the file exactly
// where it was before the tight rule existed — a flat orphan — which
// is always preferable to filing it under a show that doesn't exist.
func tightDashEpisodeOK(stem string, m []int) bool {
	digits := stem[m[4]:m[5]]
	e, _ := strconv.Atoi(digits)
	rawTitle := strings.TrimSpace(stem[m[2]:m[3]])

	// The year window (a 4-digit "episode" that is really a year) is
	// applied to both rules in animeEpisodeFromMatch, after this.
	//
	// "(YYYY)" or "[YYYY]" right after the number is the Plex / Radarr
	// movie signature — "Catch-22 (1970)", "Catch-22 [1970]", "U-571
	// (2000)", "Apollo-13 (1995)": a film dropped into a show library
	// whose hyphen is part of its title, which the movie parser titles
	// correctly. No episode file puts a year there; a series year sits
	// in front of the number ("Kanon (2006)-01"). Bare "Catch-22.mkv" is
	// indistinguishable from "Title-07.mkv" and still parses. A group
	// that carries another number after the year is a resolution, not
	// a year: "Dungeon Meshi-13 (1920 x 1080 x265)".
	if ym := bracketYearAfterRE.FindStringSubmatch(stem[m[6]:]); ym != nil && !digitRunRE.MatchString(ym[2]) {
		if y, err := strconv.Atoi(ym[1]); err == nil && inYearWindow(y) {
			return false
		}
	}
	// A digits-only title is never a show in the unspaced form: "01-02"
	// and "13-14" inside a show folder are double episodes, "2-01" a
	// compact season-episode, "2023-24" a season range. (An EMPTY title
	// is different — "[Group]-13" falls back to the folder name.)
	// Accepted loss: the anime "86" in this form ("86-01 [BD]", even
	// inside its own folder) — "86 - 01" and "86 EIGHTY-SIX-01" parse.
	if rawTitle != "" && strings.Trim(rawTitle, "0123456789") == "" {
		return false
	}
	// A title that ends in its own episode marker is a double-episode
	// or batch file ("Title-01-02", "Show Name 01-02"); filing it under
	// show "Title - 01" would be a wrong-show regression, so it stays
	// an orphan.
	if episodeMarkerTailRE.MatchString(rawTitle) {
		return false
	}
	// A title that CONTAINS an unspaced episode marker is the lazy
	// title having walked past a number whose anchor failed — "Show-13
	// x265-10 [BD]" reaches here as title "Show-13 x265", episode 10,
	// because "-13 x265" has no anchor and "-10 [BD]" does. Filing it
	// under show "Show-13 x265" would be a wrong-show regression (an
	// orphan without the tight rule), so it stays an orphan. Hyphenated
	// titles ("Re-Zero", "86-Eighty-Six", "Kill-la-Kill") have no digits
	// after their dashes and are unaffected.
	if tightMarkerInsideRE.MatchString(rawTitle) {
		return false
	}
	if tm := trailingNumberRE.FindStringSubmatch(rawTitle); tm != nil {
		tail, _ := strconv.Atoi(tm[1])
		// A title ending in a year followed by the NEXT year's last two
		// digits is a season range, the per-file naming of sports
		// leagues and broadcast-year archives: "Premier League 2023-24
		// - Arsenal vs Chelsea", "NBA 2023-24 [1080p]", "Top Gear
		// 2010-11", "Sherlock Holmes 1984-85" — unless a finale marker or
		// a revision follows the number ("Dororo 2019-20 END", "Hunter x
		// Hunter 2011-12v2"): only an episode carries those, whereas
		// "Top Gear 2010-11 Special" is a range followed by an ordinary
		// word (see finaleOrRevisionRE). Cost: an
		// unparenthesised series year whose episode happens to be that
		// number with nothing after it ("Dororo 2019-20 [BD]", episode
		// 20 alone — one episode per such show: Hunter x Hunter 2011
		// ep 12, Kanon 2006 ep 7, Shaman King 2021 ep 22) stays an
		// orphan; "Dororo 2019-01" and Moozzi2's "Dororo (2019)-20"
		// parse.
		suffix := strings.Trim(stem[m[5]:m[6]], " \t_.")
		if len(tm[1]) == 4 && len(digits) == 2 && inYearWindow(tail) && e == (tail+1)%100 && !finaleOrRevisionRE.MatchString(suffix) {
			return false
		}
		// An ascending pair is a double episode or batch in long-runner
		// numbering — "One Piece 1071-1072", "Detective Conan
		// 1000-1001", "Show Name 001-012 [BD Batch]", and the batch that
		// crosses the thousand mark, "One Piece 999-1000" — the
		// three-and-four-digit sibling of the two-digit tail
		// episodeMarkerTailRE refuses. Width is deliberately NOT
		// compared: "Mob Psycho 100-05" and "Gundam 0083-01" keep parsing
		// because their episode is SMALLER than the title's number, and
		// an equal-width requirement only ever let the 999-1000 batch
		// through as show "One Piece 999". Residuals: a descending pair
		// ("Celtics vs Mavericks 107-89") reads as show "Celtics vs
		// Mavericks 107", an equal pair ("Show 100-100") as show "Show
		// 100".
		if e > tail {
			return false
		}
	}
	// The number is a part / volume / disc / BD-extra index, not an
	// episode.
	if tightDashJunkTailRE.MatchString(rawTitle) {
		return false
	}
	// The word before the dash is a codec / audio / rate token whose
	// number lost its unit or never had one ("x265-10 [BD]", "FLAC-24
	// [BD]", "x264-1337"): not an episode.
	if codecTailRE.MatchString(rawTitle) {
		return false
	}
	// A title ending in a season marker ("Show S2", "Show Season 2",
	// "Show 2nd Season") is folded onto the base title by
	// cleanShowTitle and, outside a "Season N" folder, slotted into the
	// synthetic Season 1 by parseEpisodeIdentity — where "Show S2-01"
	// lands ON TOP of the real Episode 1 as a second file. The spaced
	// rule has always done that and keeps its behaviour; the tight rule
	// is new and these files were orphans before it, so refusing them
	// keeps them healable once a title marker is honoured as the
	// season. Inside a "Season N" folder (seasonFromFolder) the refusal
	// does cost the file its slot — accepted for now; honouring the
	// title marker belongs to both rules at once.
	normalised := strings.ReplaceAll(strings.ReplaceAll(rawTitle, ".", " "), "_", " ")
	return !seasonMarkerRE.MatchString(normalised)
}

// inYearWindow reports whether n is a plausible release year: the same
// 1888..2100 window cleanTitle (scanner.go) uses, so the TV and movie
// parsers can never disagree about what a year is.
func inYearWindow(n int) bool {
	return n >= 1888 && n <= 2100
}

// episodeIdentity is the show → season → episode slot a filename
// resolves to. episodeTitle stays empty for numbered episodes (the
// caller applies the "Episode N" default); date-based episodes carry
// the air date, which is their identity.
type episodeIdentity struct {
	showTitle    string
	season       int
	episode      int
	episodeTitle string
}

// parseEpisodeIdentity runs the TV parser chain in its one and only
// order and reports whether a filename resolves to an episode at all.
// processShowHierarchy builds the hierarchy from it, and processFile's
// orphan-heal gate asks it the same question before deciding whether
// an unchanged file may be fast-skipped — extracting the chain means
// the two can never disagree about which files are episodes.
//
// Precedence, which must not change:
//
//  1. ParseTVFilename — S##E##, then 1x03. An explicit season/episode
//     always wins over anything else in the name.
//  2. ParseDailyFilename — date-based (daily / talk-show) naming such
//     as "The Daily Show - 2013-10-30 - Guest.mkv". Mapped Plex-style:
//     season = year, episode index = month*100+day (unique within the
//     year, sorts chronologically), episode title = the ISO date.
//     Sonarr numbers daily seasons by year, which can never fit S##E##.
//  3. ParseAnimeAbsoluteFilename — "Show - 245.mkv",
//     "[Group] Show - 1071 [1080p].mkv", "[Moozzi2] Show-13 [BD].mkv".
//     Common anime fansub layout: a single flat folder per show with
//     absolute-numbered files. Slotted into the season named by the
//     file's "Season N" folder, else a synthetic Season 1 (see
//     seasonFromFolder) — anime users browse the flat episode list by
//     absolute number anyway, and a single season feels natural for
//     the flat layout. Known gap: a title with a
//     trailing season marker ("Show S2 - 01") is folded onto the base
//     title by cleanShowTitle and still lands in Season 1, on top of
//     the real Episode 1. The tight rule refuses such names
//     (tightDashEpisodeOK) so they stay healable orphans; the spaced
//     rule keeps its long-standing behaviour. Honouring the marker as
//     the season is the fix, and belongs to both rules at once.
func parseEpisodeIdentity(path string) (episodeIdentity, bool) {
	if showTitle, season, episode, ok := ParseTVFilename(path); ok {
		return episodeIdentity{showTitle: showTitle, season: season, episode: episode}, true
	}
	if showTitle, y, mo, d, ok := ParseDailyFilename(path); ok {
		return episodeIdentity{
			showTitle:    showTitle,
			season:       y,
			episode:      mo*100 + d,
			episodeTitle: fmt.Sprintf("%04d-%02d-%02d", y, mo, d),
		}, true
	}
	if showTitle, episode, ok := ParseAnimeAbsoluteFilename(path); ok {
		return episodeIdentity{showTitle: showTitle, season: seasonFromFolder(path), episode: episode}, true
	}
	return episodeIdentity{}, false
}

// seasonFromFolder is the season an absolute-numbered (anime-rule) file
// belongs to: the number of the "Season N" / "SNN" folder it sits in,
// else the synthetic Season 1.
//
// The anime rules only yield an absolute episode number, and every hit
// used to be slotted into Season 1 regardless of where the file lived.
// That is right for the flat fansub layout, but a user who files a
// sequel under "Season 2" has said what season it is: QA's
// "Clannad/Season 2/[Moozzi2] Clannad After Story-20 [BD …].mkv" sits
// beside "Clannad.2007.S02E01…" and is After Story episode 20, i.e.
// Clannad S2E20 — forced into Season 1 it would land on top of Clannad
// S1E20 (the show resolves through the folder hint, so both files
// reach the same show row). The season folder is also what the AniList
// franchise walk maps sequels by, so honouring it keeps the scanner and
// the enricher telling the same story.
//
// Only the file's own folder is consulted, decorated or not ("Season 2
// - After Story" counts, see seasonFolderRE). "Season 00" / "S00" is
// season 0, and so is a "Specials" / "Extras" / "OVAs" / "ONAs" folder
// (specialsFolderRE — the same list DetectEpisodeKind tags kinds from):
// season 0 is the specials season, and a special left in Season 1 would
// sit on top of the real episode 1. Known gaps, both pre-existing: a
// season marker inside the TITLE ("Show S2 - 01") is still folded away
// by cleanShowTitle (see parseEpisodeIdentity), and a cour-named SHOW
// folder ("One Punch Man S2/…") is not read as a season — its files
// land in Season 1 as they always did. Explicit S##E## and date-based
// names never come through here: their season is in the name.
func seasonFromFolder(path string) int {
	parts := strings.Split(strings.ReplaceAll(filepath.ToSlash(path), `\`, `/`), "/")
	if len(parts) < 2 {
		return 1
	}
	parent := strings.TrimSpace(parts[len(parts)-2])
	if specialsFolderRE.MatchString(parent) {
		return 0
	}
	m := seasonFolderRE.FindStringSubmatch(parent)
	if m == nil {
		return 1
	}
	for _, g := range m[1:] {
		if g != "" {
			n, _ := strconv.Atoi(g)
			return n
		}
	}
	return 1
}

// episodeKindRE matches anime / TV episode subtype keywords commonly
// used in fansub / scene release filenames. The captures map onto
// the media_items.kind column values (lowercased before storage).
//
// Word-boundary anchored on both sides so quality / source markers
// like `[1080p]` don't false-match. Case-insensitive: `OVA` /
// `Ova` / `ova` all hit. The scanner picks the first non-empty kind
// for a file, with order chosen so the more specific markers (OAD
// before OVA, since OAD is a kind of OVA but the user has been
// explicit) win.
var episodeKindRE = regexp.MustCompile(`(?i)\b(` + episodeKindWords + `)\b`)

// specialsFolderRE matches folder names that flag every contained
// file as a special episode (Plex / Jellyfin convention). Used as a
// fallback when the filename itself has no kind keyword.
var specialsFolderRE = regexp.MustCompile(`(?i)^(specials?|extras?|ovas?|onas?)$`)

// DetectEpisodeKind returns the subtype keyword for an episode file
// path or "" for a regular episode.
//
//   - filename keyword wins first (most specific): OVA / ONA / SP /
//     SPECIAL / PV / MV / OAD anywhere in the stem
//   - folder fallback: a containing folder named "Specials" /
//     "Extras" / "OVAs" / "ONAs" tags every contained file
//   - season 0 fallback: TMDB / TheTVDB convention is that season
//     0 holds specials. Caller passes seasonNum=0 when the file's
//     resolved season is 0.
//
// All return values lowercased to match the canonical column values
// documented on migration 00075.
func DetectEpisodeKind(path string, seasonNum int) string {
	path = strings.ReplaceAll(filepath.ToSlash(path), `\`, `/`)
	base := filepath.Base(path)
	stem := base
	if ext := filepath.Ext(base); ext != "" {
		stem = base[:len(base)-len(ext)]
	}

	// Filename keyword wins — most specific signal.
	if m := episodeKindRE.FindStringSubmatch(stem); m != nil {
		return canonicalEpisodeKind(m[1])
	}

	// Folder name fallback. Split the already-slash-normalised path
	// directly instead of routing through filepath.Dir, which would
	// re-introduce platform-native separators on Windows and break
	// the per-directory scan.
	parts := strings.Split(path, "/")
	// Skip the last segment (the filename); walk parents inward.
	for i := len(parts) - 2; i >= 0; i-- {
		dir := strings.TrimSpace(parts[i])
		if dir == "" {
			continue
		}
		if specialsFolderRE.MatchString(dir) {
			return canonicalEpisodeKind(dir)
		}
	}

	// TMDB / TheTVDB convention: season 0 is the specials season.
	if seasonNum == 0 {
		return "special"
	}
	return ""
}

// canonicalEpisodeKind normalises the various spellings the scanner
// might encounter (Specials / specials / SP / oad / OVAs / ovas)
// into the lowercase singular form stored in media_items.kind.
func canonicalEpisodeKind(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "ova", "ovas":
		return "ova"
	case "ona", "onas":
		return "ona"
	case "special", "specials", "sp", "extra", "extras":
		return "special"
	case "pv", "mv":
		return "pv"
	case "oad":
		return "oad"
	case "movie":
		return "movie"
	}
	return ""
}

// extractShowTitle cleans a raw prefix into a show title. If the prefix is
// empty or unhelpful (e.g. just episode number), it falls back to the parent
// folder name (skipping "Season N" folders). Any TRaSH/Sonarr-style external
// id marker (`{tmdb-NNN}`, `{tvdb-NNN}`, `[tvdbid-NNN]`, `{imdb-tt...}`) is
// stripped from the folder name before cleaning, so the marker doesn't leak
// into the show title and torpedo title-based dedup matching.
func extractShowTitle(rawPrefix string, fullPath string) string {
	title := cleanShowTitle(StripFolderIDMarkers(rawPrefix))
	if title != "" {
		return title
	}

	// Fall back to folder structure: walk up parent directories looking for
	// the show name (skip Season folders).
	parts := strings.Split(filepath.ToSlash(fullPath), "/")
	// parts: [..., showFolder, seasonFolder, filename]
	for i := len(parts) - 2; i >= 0; i-- {
		dir := parts[i]
		if seasonFolderRE.MatchString(dir) {
			continue
		}
		cleaned := cleanShowTitle(StripFolderIDMarkers(dir))
		if cleaned != "" {
			return cleaned
		}
	}

	return ""
}

// fansubGroupRE matches a leading bracketed or parenthesised tag at
// the start of a string with optional surrounding whitespace —
// `[Group] `, `(Group) `, etc. The repeat-strip loop in
// stripLeadingFansubGroups handles consecutive prefixes like
// `[SubsPlease][Erai-raws] Show…` without backtracking.
var fansubGroupRE = regexp.MustCompile(`^\s*[\[(][^\])]*[\])]\s*`)

// stripLeadingFansubGroups removes one or more leading `[Group]` or
// `(Group)` tags from the input. Trailing bracket / paren clusters
// (quality / source markers) are intentionally left alone — those
// are handled by the downstream filename regex paths or end up in
// the right place anyway.
//
// Bare bracketed strings collapse to "" so cleanShowTitle returns
// empty and the caller falls through to the folder-fallback path.
func stripLeadingFansubGroups(s string) string {
	for {
		stripped := fansubGroupRE.ReplaceAllString(s, "")
		if stripped == s {
			return strings.TrimSpace(stripped)
		}
		s = stripped
	}
}

// cleanShowTitle normalises a raw string into a human-readable show title.
// Replaces dots and underscores with spaces, strips leading/trailing junk.
func cleanShowTitle(raw string) string {
	// Strip trailing separators and whitespace.
	raw = strings.TrimRight(raw, ".-_ ")
	raw = strings.TrimLeft(raw, ".-_ ")
	if raw == "" {
		return ""
	}

	// Strip leading fansub-group tags before normalisation. Anime
	// fansub releases conventionally prefix every filename with
	// `[Group]` (sometimes multiple consecutive groups for re-encodes
	// like `[SubsPlease][Erai-raws] Show…`), and some scene releases
	// use `(Group)`. Without stripping, "Solo Leveling" arrives in
	// the DB as "[jaaj] Solo Leveling" and the rest of the search /
	// dedup / display pipeline carries the group tag everywhere it
	// shouldn't appear.
	//
	// Only LEADING bracket runs are stripped — trailing brackets like
	// `Show [1080p].mkv` are quality / source markers handled by the
	// downstream regex paths and shouldn't be eaten here.
	raw = stripLeadingFansubGroups(raw)
	if raw == "" {
		return ""
	}

	// Replace dots and underscores with spaces.
	raw = strings.ReplaceAll(raw, ".", " ")
	raw = strings.ReplaceAll(raw, "_", " ")

	// Collapse multiple spaces and trim.
	fields := strings.Fields(raw)
	if len(fields) == 0 {
		return ""
	}

	// Strip trailing " -" that can result from "Show Name - S01E03".
	title := strings.Join(fields, " ")
	title = strings.TrimRight(title, " -")
	title = strings.TrimSpace(title)

	// Strip trailing season markers so anime cours folder-named like
	// "One Punch Man S2", "Spy x Family Season 2", "Demon Slayer 2nd
	// Season" all collapse to the franchise base. Without this both
	// AniList search at scan time AND the dedupe SQL miss the join.
	// Conservative: only the trailing season suffix is removed; an
	// internal "Season 2 Special" (rare but possible in subtitles)
	// keeps its label.
	title = stripShowSeasonMarkers(title)

	// The marker may have been the last thing after a " - ": the raw
	// title "Show - S2" (out of "Show - S2 - 01") leaves "Show -"
	// behind, so strip the trailing dash again.
	title = strings.TrimSpace(strings.TrimRight(title, " -"))

	return title
}

// seasonMarkerRE matches the trailing-season patterns observed in
// real anime/show folder names:
//   - "S2", "S 2", "s12"               (compact)
//   - "Season 2", "Season  3"          (Plex/Jellyfin style)
//   - "2nd Season", "3rd Season"       (anime-style ordinal)
//   - "Cour 2"                          (rare but seen)
//
// Anchored to end-of-string so a legit "Season 2 Specials" subtitle
// is preserved.
var seasonMarkerRE = regexp.MustCompile(`(?i)\s+(?:s\s*\d+|season\s+\d+|\d+(?:st|nd|rd|th)\s+season|cour\s+\d+)\s*$`)

// stripShowSeasonMarkers removes a trailing season indicator from a
// show title. Returns the input unchanged if no marker is found.
// Only meant for top-level show titles — episode / season titles
// pass through their own naming pipeline.
func stripShowSeasonMarkers(title string) string {
	stripped := seasonMarkerRE.ReplaceAllString(title, "")
	stripped = strings.TrimSpace(stripped)
	if stripped == "" {
		// Defensive: if the whole title was a season marker (shouldn't
		// happen in practice but possible with bad metadata), keep
		// the original so we don't blank out the row.
		return title
	}
	return stripped
}
