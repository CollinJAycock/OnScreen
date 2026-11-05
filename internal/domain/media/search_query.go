package media

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// maxPrefixWords caps the words PrefixTSQuery keeps: a query is a title,
// not a paragraph, and every word is another index lookup.
const maxPrefixWords = 8

// minPrefixRunes is the shortest word PrefixTSQuery keeps. As a prefix, a
// word of one or two letters matches a word in nearly every title or
// summary ("ti:*": time, title, tiny…), and ranking all of those took a
// second on a large library. Shorter words still match whole (the
// full-text arm) and inside a title (the contains arm).
const minPrefixRunes = 3

// PrefixTSQuery turns a search query into a to_tsquery expression matching
// every word as a prefix ("avat wat" → "avat:* & wat:*"), so a word still
// being typed matches (full-text search alone matches whole words only).
//
// Only letters and digits survive: each word is cut at anything else, so
// the result is always valid tsquery syntax and the user's text can't
// reach to_tsquery's operators (&, |, !, <->, :, parentheses). Words
// shorter than minPrefixRunes are left out. Nil when no word is left, for
// the SQL's "no prefix query" branch.
func PrefixTSQuery(query string) *string {
	var words []string
	for _, w := range strings.FieldsFunc(query, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	}) {
		if utf8.RuneCountInString(w) >= minPrefixRunes {
			words = append(words, w)
		}
	}
	if len(words) == 0 {
		return nil
	}
	if len(words) > maxPrefixWords {
		words = words[:maxPrefixWords]
	}
	parts := make([]string, len(words))
	for i, w := range words {
		parts[i] = strings.ToLower(w) + ":*"
	}
	out := strings.Join(parts, " & ")
	return &out
}
