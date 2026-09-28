package notifyagents

import (
	"context"
	"regexp"
	"strconv"
	"strings"

	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/db/gen"
)

// Channel privacy. An agent channel is shared — a household group chat, not
// an admin console — so a forwarded event is rewritten before it goes out:
//
//   - An event about an item in a PRIVATE library (a problem report, a
//     request that landed there) keeps the event but drops the title: the
//     same reason new_content lists public libraries only and scan notices
//     are scoped to a library's viewers.
//   - request_failed takes the admin copy (it names the requester), but the
//     *arr failure reason in it can name download-client hosts and folder
//     paths, which the API only shows admins. The channel gets the wording
//     non-admins get instead.

// itemLibraryDB is the lookup behind the private-library check. It is
// optional on DB (*gen.Queries provides it); without it every item counts as
// private, so a title is never announced by accident.
type itemLibraryDB interface {
	GetMediaItem(ctx context.Context, id uuid.UUID) (gen.GetMediaItemRow, error)
	GetLibrary(ctx context.Context, id uuid.UUID) (gen.Library, error)
}

var _ itemLibraryDB = (*gen.Queries)(nil)

// privateTitle stands in for a title from a private library.
const privateTitle = "a title in a private library"

// requestFailedChannelText is the request_failed wording for the channel. It
// is the phrase the API shows non-admins for a failed download
// (api/v1 userDownloadMessages[requests.DownloadFailed]).
const requestFailedChannelText = "The download failed — an admin has the details"

// itemIsPrivate reports whether item id is in a private library. Anything it
// can't establish (no lookup, the item or library gone, a DB error) counts as
// private: failing closed drops a title, failing open would leak one.
func (s *Service) itemIsPrivate(ctx context.Context, id uuid.UUID) bool {
	if s.items == nil {
		return true
	}
	item, err := s.items.GetMediaItem(ctx, id)
	if err != nil {
		s.logger.Debug("notification agents: item privacy lookup", "item_id", id, "err", err)
		return true
	}
	lib, err := s.items.GetLibrary(ctx, item.LibraryID)
	if err != nil {
		s.logger.Debug("notification agents: library privacy lookup", "library_id", item.LibraryID, "err", err)
		return true
	}
	return lib.IsPrivate
}

// issueKindLabels are the problem-report kinds as the issue endpoint words
// them (api/v1 issueKindLabels). Only a kind from this list survives the
// private-library rewrite; anything else is dropped with the title.
var issueKindLabels = []string{"video", "audio", "subtitles", "wrong match", "something else"}

// seasonAvailableTitle matches "Season 3 now available: <title>".
var seasonAvailableTitle = regexp.MustCompile(`^Season (\d{1,4}) now available: `)

// privateItemText rewrites a forwarded event about an item in a private
// library so neither line names the title. What is kept is parsed out of the
// producer's wording (a requester, a report kind, a season number) and
// dropped when it doesn't parse — never the reverse.
func privateItemText(event, title, body string) (string, string) {
	switch event {
	case EventIssueReported:
		// "<reporter> reported a problem with <title>: <kind>"
		if reporter, rest, ok := strings.Cut(body, " reported a problem with "); ok &&
			reporter != "" && !strings.ContainsAny(reporter, "\r\n") {
			for _, kind := range issueKindLabels {
				if strings.HasSuffix(rest, ": "+kind) {
					return "Problem reported", reporter + " reported a problem with " + privateTitle + ": " + kind
				}
			}
			return "Problem reported", reporter + " reported a problem with " + privateTitle + "."
		}
		return "Problem reported", "A problem was reported with " + privateTitle + "."
	case EventRequestAvailable:
		return "Request available", "A requested title in a private library is ready to watch."
	case EventRequestSeasonAvailable:
		if m := seasonAvailableTitle.FindStringSubmatch(title); m != nil {
			return "Season " + m[1] + " available",
				"Season " + m[1] + " of a requested show in a private library is ready to watch."
		}
		return "Requested seasons available", "More of a requested show in a private library is ready to watch."
	}
	return eventLabel(event), "About " + privateTitle + "."
}

// requestFailedAdminBody matches the admin request_failed notice:
// `<requester>'s request for "<title>" failed in <service>[: <reason>]`.
var requestFailedAdminBody = regexp.MustCompile(`^([^\r\n]{1,100}?)'s request for ("(?:[^"\\\r\n]|\\.)*") failed in `)

// requestFailedText is the channel body for request_failed: who asked for
// what, and the non-admin wording instead of the *arr reason. When the body
// doesn't parse, only the wording goes out.
func requestFailedText(body string) string {
	m := requestFailedAdminBody.FindStringSubmatch(body)
	if m == nil {
		return requestFailedChannelText + "."
	}
	title, err := strconv.Unquote(m[2])
	if err != nil || title == "" {
		return requestFailedChannelText + "."
	}
	requester := m[1]
	if requester == "A user" { // the producer's fallback for an unknown requester
		requester = "a user"
	}
	return strconv.Quote(title) + ", requested by " + requester + ".\n" + requestFailedChannelText + "."
}

// eventLabel is an event's short label from the catalog, or its key.
func eventLabel(key string) string {
	for _, e := range catalog {
		if e.Key == key {
			return e.Label
		}
	}
	return key
}
