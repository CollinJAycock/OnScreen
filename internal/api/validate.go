package api

import (
	"fmt"
	"sort"
	"strings"
)

// libraryACLGuard is satisfied by every handler that filters its responses by
// the caller's library grants (via .WithLibraryAccess). A nil checker fails
// open — it serves every library — so the wiring must not be forgotten.
type libraryACLGuard interface{ LibraryAccessWired() bool }

// ValidateLibraryAccess returns an error naming any security-critical handler
// that was constructed without a per-library access checker. main wires each
// one with .WithLibraryAccess(libSvc); calling this at startup turns a
// forgotten wiring into a fail-fast instead of silently serving every library
// to every user. Disabled handlers (nil fields) are skipped.
func (h Handlers) ValidateLibraryAccess() error {
	var missing []string
	check := func(name string, present bool, g libraryACLGuard) {
		// present guards against a typed-nil interface: when the handler
		// field is nil the feature is off, so skip it (and never call the
		// method on a nil pointer).
		if present && !g.LibraryAccessWired() {
			missing = append(missing, name)
		}
	}

	check("items", h.Items != nil, h.Items)
	check("hub", h.Hub != nil, h.Hub)
	check("search", h.Search != nil, h.Search)
	check("history", h.History != nil, h.History)
	check("favorites", h.Favorites != nil, h.Favorites)
	check("collections", h.Collections != nil, h.Collections)
	check("playlists", h.Playlists != nil, h.Playlists)
	check("photo_albums", h.PhotoAlbums != nil, h.PhotoAlbums)
	check("photos", h.Photos != nil, h.Photos)
	check("books", h.Books != nil, h.Books)

	if len(missing) > 0 {
		sort.Strings(missing)
		return fmt.Errorf("library-access checker not wired on handler(s): %s", strings.Join(missing, ", "))
	}
	return nil
}
