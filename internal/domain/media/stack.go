package media

import (
	"path"
	"sort"
	"strings"
)

// Stacked (multi-part) movies.
//
// A rip split across files ("Movie (2001) cd1.mkv" + "cd2.mkv") is one movie
// that plays continuously. The scanner records each file's part number from
// its name (File.PartIndex, migration 00045); which files belong together is
// decided here, when the files are read, so a stack forms or dissolves as its
// files come and go without any row having to be rewritten.
//
// Files stack when they belong to the same item, sit in the same folder, carry
// the same edition label and the same extension, and each has a distinct part
// number. That key is deliberately narrow: two parallel stacks of one movie in
// one folder ("1080p cd1/cd2" and "720p cd1/cd2" without a " - 1080p" version
// suffix) collide on their part numbers and are shown as separate versions
// rather than spliced into one wrong sequence. A group of one is no stack: a
// lone "cd1" plays like any other file.

// stackKey is the grouping key of a file that names a part, ok=false for a
// file that doesn't.
func stackKey(f File) (string, bool) {
	if f.PartIndex == nil || *f.PartIndex <= 0 {
		return "", false
	}
	p := strings.ReplaceAll(f.FilePath, `\`, `/`)
	edition := ""
	if f.Edition != nil {
		edition = strings.ToLower(strings.TrimSpace(*f.Edition))
	}
	return f.MediaItemID.String() + "\x00" + path.Dir(p) + "\x00" +
		edition + "\x00" + strings.ToLower(path.Ext(p)), true
}

// Stack returns the parts of the stack f belongs to, in part order, f among
// them. nil when f is not part of a playable stack: no part number, no second
// part beside it, or two files claiming the same part number. Only files in
// files are considered, so callers pass the item's active files.
func Stack(files []File, f File) []File {
	key, ok := stackKey(f)
	if !ok {
		return nil
	}
	var parts []File
	seen := map[int]bool{}
	for _, g := range files {
		k, ok := stackKey(g)
		if !ok || k != key {
			continue
		}
		if seen[*g.PartIndex] {
			return nil // ambiguous: two files claim this part
		}
		seen[*g.PartIndex] = true
		parts = append(parts, g)
	}
	if len(parts) < 2 {
		return nil
	}
	found := false
	for _, p := range parts {
		if p.ID == f.ID {
			found = true
			break
		}
	}
	if !found {
		return nil
	}
	sort.SliceStable(parts, func(i, j int) bool { return *parts[i].PartIndex < *parts[j].PartIndex })
	return parts
}

// IsStacked reports whether f plays as one part of a multi-part stack.
func IsStacked(files []File, f File) bool { return Stack(files, f) != nil }

// OrderFiles keeps files' order (best quality first) but moves the parts of
// each stack together, in part order, to the place of the stack's first file.
// Files outside any stack keep their positions relative to each other.
func OrderFiles(files []File) []File {
	if len(files) < 2 {
		return files
	}
	out := make([]File, 0, len(files))
	placed := make(map[int]bool, len(files)) // index into files
	byID := make(map[string]int, len(files))
	for i, f := range files {
		byID[f.ID.String()] = i
	}
	for i, f := range files {
		if placed[i] {
			continue
		}
		parts := Stack(files, f)
		if parts == nil {
			out = append(out, f)
			placed[i] = true
			continue
		}
		for _, p := range parts {
			j := byID[p.ID.String()]
			if !placed[j] {
				out = append(out, p)
				placed[j] = true
			}
		}
	}
	return out
}

// StackDurationMS is the combined running time of parts, nil when any part's
// duration is unknown (a partial sum would cut the timeline short).
func StackDurationMS(parts []File) *int64 {
	if len(parts) == 0 {
		return nil
	}
	var total int64
	for _, p := range parts {
		if p.DurationMS == nil || *p.DurationMS <= 0 {
			return nil
		}
		total += *p.DurationMS
	}
	return &total
}

// samePtr reports whether two optional values are both nil or both set to
// equal values.
func samePtr[T comparable](a, b *T) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}
