package metadata

import (
	"regexp"
	"strconv"
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"
)

// Auto-match: what a scanner-side search may accept unattended. A search
// hit is the file's title only when one of its titles folds to the same
// thing (FoldTitle) and it was released within a year of the year the file
// names. TMDB's or TVDB's top hit alone is not a match: a popular near-miss
// ranks above an obscure exact title ("Spring" 2019 → Spring Breakers).

// FoldTitle reduces a title to the form auto-match compares: lowercase,
// diacritics stripped, "&" read as "and", a leading English article
// dropped, then everything but letters and digits removed. So "Spider-Man"
// and "Spiderman" fold alike, as do "S W A T" (cleaned from "S.W.A.T.") and
// "S.W.A.T.", "Amélie" and "Amelie", "Wing It" and "Wing It!". Letters of
// every script survive, so two different Japanese titles don't both fold
// to "".
func FoldTitle(s string) string {
	s = strings.ToLower(strings.TrimSpace(norm.NFKD.String(s)))
	s = strings.ReplaceAll(s, "&", " and ")
	for _, article := range []string{"the ", "a ", "an "} {
		if strings.HasPrefix(s, article) {
			s = s[len(article):]
			break
		}
	}
	return strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return r
		}
		return -1
	}, s)
}

var showQualifierRE = regexp.MustCompile(`^(.*\S)\s*\(([A-Z]{2}|\d{4})\)$`)

// SplitShowQualifier splits off the disambiguator TVDB, and so Sonarr's
// folder names, append to a show's name: "The Office (US)" is "The Office"
// from the US, "Doctor Who (2005)" is "Doctor Who" first aired in 2005.
// country is the ISO 3166-1 code ("UK" read as "GB"), "" when the name
// carries none; year is 0 when it carries none.
func SplitShowQualifier(s string) (title, country string, year int) {
	s = strings.TrimSpace(s)
	m := showQualifierRE.FindStringSubmatch(s)
	if m == nil {
		return s, "", 0
	}
	if y, err := strconv.Atoi(m[2]); err == nil {
		return m[1], "", y
	}
	if m[2] == "UK" {
		return m[1], "GB", 0
	}
	return m[1], m[2], 0
}

// Candidate is one search hit as MatchTitle sees it.
type Candidate struct {
	ID     int      // the caller's own; MatchTitle ignores it
	Titles []string // name, original name, aliases — any one may match
	Year   int      // first release or air year; 0 when the hit has none
}

// MatchTitle returns the indexes, in the order given, of the candidates
// with a title that folds to query's and the year closest to year: year
// itself when any candidate has it, else a year either side. With year 0
// any year will do; with a year, a candidate without one never qualifies.
// A candidate that only shares words with the query ("Spring Breakers" for
// "Spring") never qualifies.
func MatchTitle(query string, year int, cands []Candidate) []int {
	want := FoldTitle(query)
	if want == "" {
		return nil
	}
	var exact, near []int
	for i, c := range cands {
		if !hasTitle(c.Titles, want) {
			continue
		}
		switch YearDistance(c.Year, year) {
		case 0:
			exact = append(exact, i)
		case 1:
			near = append(near, i)
		}
	}
	if len(exact) > 0 {
		return exact
	}
	return near
}

// HasTitle reports whether any of titles folds to query's fold.
func HasTitle(titles []string, query string) bool {
	want := FoldTitle(query)
	return want != "" && hasTitle(titles, want)
}

func hasTitle(titles []string, folded string) bool {
	for _, t := range titles {
		if FoldTitle(t) == folded {
			return true
		}
	}
	return false
}

// YearDistance is how many years got lies from want: 0 when want is 0 (no
// year to check), and past MatchTitle's one-year tolerance when got is 0
// (no year to check it against).
func YearDistance(got, want int) int {
	if want <= 0 {
		return 0
	}
	if got <= 0 {
		return 1 << 30
	}
	if got > want {
		return got - want
	}
	return want - got
}
