package userexport

import (
	"archive/zip"
	"errors"
	"io"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

func isNoRows(err error) bool { return errors.Is(err, pgx.ErrNoRows) }

// readme is README.txt, the archive's description of itself.
const readme = `Your OnScreen data
==================

This archive holds what this OnScreen server keeps about your account, as
JSON files (UTF-8, ISO 8601 / RFC 3339 times in UTC). It was generated on
{{GENERATED}}.

Items (movies, episodes, tracks, books ...) are written as an "item" object:
its OnScreen id, type, title, year, the show/album it belongs to, and its
external ids (TMDB, TVDB, IMDb, MusicBrainz, AniList) so it can be matched on
another server. An item that has since been removed, or that your account
can't see any more, shows only its id and "unavailable": true.

Files
-----
manifest.json          Format version, when this was made, row counts, and
                       whether any file hit its size cap.
profile.json           Your account: username, email, when it was created,
                       playback preferences, home layout, restrictions set on
                       the account, request permissions, which sign-in
                       methods are linked, whether 2FA and a PIN are set.
sessions.json          Signed-in devices: device name, platform, address,
                       when signed in and last used.
api_keys.json          Your personal API keys: name, first characters,
                       access, created / last used / expiry dates.
scrobble.json          Whether ListenBrainz, Last.fm and Trakt are linked,
                       and the account name each reports.
watch_history.json     Play events (play, pause, stop, scrobble ...), newest
                       first: item, position, client, address, time. Capped
                       at the newest 100,000 (see manifest.json).
watch_progress.json    Where you are in each item, when you last played and
                       finished it, and items marked watched / unwatched.
watch_status.json      Plan to watch / watching / completed / ... lists.
ratings.json           Your ratings (0-10).
favorites.json         Your favorites.
reading_progress.json  Your position in each ebook, synced between devices.
audiobooks.json        Audiobook playback speeds and bookmarks.
playlists.json         Your playlists (and smart playlists, with their
                       rules), with their items in order.
collections.json       Other collections your account owns, if any.
requests.json          Titles you requested and what became of them.
issues.json            Problems you reported with an item.
notifications.json     Your in-app notifications (newest 1,000). OnScreen
                       has no per-user notification settings beyond the
                       email address in profile.json.

What is not included
--------------------
Secrets are never exported: not your password (only whether one is set),
PIN, two-factor secret or recovery codes, sign-in tokens, API keys
themselves, or the tokens that link scrobbling services. Server-wide data
that isn't about you (the libraries, their metadata and files, other users,
admin settings) is not included either.
`

func writeReadme(zw *zip.Writer, now time.Time) error {
	f, err := zw.CreateHeader(&zip.FileHeader{Name: "README.txt", Method: zip.Deflate, Modified: now})
	if err != nil {
		return err
	}
	text := strings.ReplaceAll(readme, "{{GENERATED}}", now.Format(time.RFC1123))
	// CRLF so it opens cleanly in Notepad as well.
	_, err = io.WriteString(f, strings.ReplaceAll(text, "\n", "\r\n"))
	return err
}
