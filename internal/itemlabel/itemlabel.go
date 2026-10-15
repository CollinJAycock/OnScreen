// Package itemlabel implements admin labels on media items and the per-user
// label restriction built on them (Plex's label filters, Jellyfin's blocked /
// allowed tags).
//
// Labels are admin-owned (media_items.labels), never the provider keywords in
// media_items.tags: a metadata refresh rewrites tags, and a restriction that a
// refresh can silently lift is no restriction. Only labels are matched.
//
// The SQL half is media_item_labels_permitted() (migration 00047), which every
// rating-gated query calls next to content_rating_rank(). Both halves compare
// normalised strings, so every write path runs values through Normalize.
package itemlabel

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	// MaxLen bounds a single label, in runes.
	MaxLen = 64
	// MaxPerItem bounds the labels on one item.
	MaxPerItem = 50
	// MaxPerUser bounds each of a user's blocked / allowed lists. The lists
	// ride in every access, stream and asset token (like the rating ceiling),
	// and stream / asset tokens travel in URLs, so they must stay small.
	MaxPerUser = 32
)

// ErrInvalid wraps every validation failure from NormalizeList.
var ErrInvalid = errors.New("invalid label")

// Normalize lower-cases a label and collapses runs of whitespace to a single
// space, trimming both ends. "  Kids   OK " and "kids ok" are the same label.
func Normalize(s string) string {
	return strings.ToLower(strings.Join(strings.FieldsFunc(s, unicode.IsSpace), " "))
}

// NormalizeList normalises, de-duplicates and sorts labels, dropping empties.
// It fails when a label is longer than MaxLen runes, contains a control
// character, or when more than max distinct labels remain.
func NormalizeList(in []string, max int) ([]string, error) {
	seen := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))
	for _, raw := range in {
		l := Normalize(raw)
		if l == "" {
			continue
		}
		if utf8.RuneCountInString(l) > MaxLen {
			return nil, fmt.Errorf("%w: %q is longer than %d characters", ErrInvalid, l, MaxLen)
		}
		for _, r := range l {
			if unicode.IsControl(r) {
				return nil, fmt.Errorf("%w: %q contains a control character", ErrInvalid, l)
			}
		}
		if _, dup := seen[l]; dup {
			continue
		}
		seen[l] = struct{}{}
		out = append(out, l)
	}
	if len(out) > max {
		return nil, fmt.Errorf("%w: at most %d labels", ErrInvalid, max)
	}
	sort.Strings(out)
	return out, nil
}

// Filter is a viewer's label restriction. The zero value restricts nothing.
type Filter struct {
	// Blocked hides every item whose effective labels include any of these.
	Blocked []string
	// Allowed, when non-empty, hides every item whose effective labels
	// include none of these.
	Allowed []string
}

// Active reports whether the filter restricts anything. Callers use it to
// skip the per-item label lookup for the (common) unrestricted viewer.
func (f Filter) Active() bool { return len(f.Blocked) > 0 || len(f.Allowed) > 0 }

// Permits reports whether an item whose effective labels (its own plus its
// ancestors') are effective passes the filter. Mirrors the SQL function
// media_item_labels_permitted; both inputs are expected normalised.
func (f Filter) Permits(effective []string) bool {
	if !f.Active() {
		return true
	}
	has := make(map[string]struct{}, len(effective))
	for _, l := range effective {
		has[l] = struct{}{}
	}
	for _, b := range f.Blocked {
		if _, ok := has[b]; ok {
			return false
		}
	}
	if len(f.Allowed) == 0 {
		return true
	}
	for _, a := range f.Allowed {
		if _, ok := has[a]; ok {
			return true
		}
	}
	return false
}

// BlockedArg and AllowedArg return the lists as the generated queries'
// blocked_labels / allowed_labels arguments. An empty list becomes nil, i.e.
// SQL NULL, which media_item_labels_permitted treats as "no restriction" —
// and which lets a query test the argument with a cheap IS NULL.
func (f Filter) BlockedArg() []string {
	if len(f.Blocked) == 0 {
		return nil
	}
	return f.Blocked
}

// AllowedArg is BlockedArg for the allowed labels.
func (f Filter) AllowedArg() []string {
	if len(f.Allowed) == 0 {
		return nil
	}
	return f.Allowed
}

// Key is a stable cache key for the filter: two viewers with the same key see
// the same items, so a cache shared across viewers must include it.
func (f Filter) Key() string {
	if !f.Active() {
		return ""
	}
	return "b:" + strings.Join(f.Blocked, "\x1f") + "|a:" + strings.Join(f.Allowed, "\x1f")
}
