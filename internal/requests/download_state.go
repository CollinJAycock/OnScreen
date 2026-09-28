package requests

import (
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/onscreen/onscreen/internal/arr"
)

// Download states stored in media_requests.download_state (migration 00025)
// and sent as the request DTO's download.state. The CHECK constraint is the
// source of truth; the web client's RequestDownload type mirrors it.
const (
	// DownloadSearching: monitored in Radarr/Sonarr, nothing grabbed yet.
	DownloadSearching = "searching"
	// DownloadQueued: grabbed, waiting in the download client.
	DownloadQueued = "queued"
	// DownloadDownloading: transferring (progress / eta set).
	DownloadDownloading = "downloading"
	// DownloadImportPending: downloaded, waiting for the *arr import or the
	// library scan that turns the request available.
	DownloadImportPending = "import_pending"
	// DownloadStalled: the download client or *arr app reports a problem
	// (a warning, a blocked import, a paused download, not monitored).
	DownloadStalled = "stalled"
	// DownloadFailed: the grab or import failed with nothing newer grabbed.
	DownloadFailed = "failed"
)

// maxDownloadMessage bounds download_message (runes). *arr status messages
// can list every file of a season pack; the request row needs the gist.
const maxDownloadMessage = 300

// DownloadStatus is the sync's verdict for one request.
type DownloadStatus struct {
	State string
	// Progress is the downloaded fraction in [0, 1]; nil = unknown.
	Progress *float64
	// ETA is the expected completion; zero = unknown.
	ETA time.Time
	// SizeBytes is the total size of the grabbed release(s); nil = unknown.
	SizeBytes *int64
	Message   string
	// Recovers is true when the verdict rests on positive evidence of a new
	// grab or import (a live queue entry, a file on disk, a grab / import
	// newer than the last failure). Only such a verdict moves a failed
	// request back to downloading; "searching" inferred from an empty
	// history does not.
	Recovers bool
}

// queueRowState maps one queue row to a download state and the reason
// text worth showing. Order matters: a failed download may also carry a
// warning status, and an import problem is reported with status "completed".
func queueRowState(r arr.QueueRecord) (state, message string) {
	tds := strings.ToLower(strings.TrimSpace(r.TrackedDownloadState))
	tst := strings.ToLower(strings.TrimSpace(r.TrackedDownloadStatus))
	st := strings.ToLower(strings.TrimSpace(r.Status))
	msgs := r.Messages()
	first := func(fallback string) string {
		if len(msgs) > 0 {
			return msgs[0]
		}
		return fallback
	}

	switch {
	case tds == "failed":
		// Processed by the app's failed-download handling: blocklisted, and
		// re-searched if that's enabled (a new grab shows as another row).
		return DownloadFailed, first("The download failed")
	case tds == "failedpending" || st == "failed":
		// The client reports a failure the app hasn't handled yet. Usually
		// it re-grabs within moments; calling the request failed here would
		// notify people about a failure that fixes itself.
		return DownloadStalled, first("The download failed — waiting for the app to handle it")
	case tds == "importblocked":
		return DownloadStalled, first("Import blocked — needs a manual import")
	case tds == "ignored":
		return DownloadStalled, first("The download was ignored")
	case tds == "importpending" || tds == "importing" || tds == "imported":
		// Before importBlocked existed (Radarr < 5 / Sonarr < 4) a blocked
		// import was importPending with a warning.
		if tst == "warning" || tst == "error" {
			return DownloadStalled, first("Import is waiting on a problem")
		}
		return DownloadImportPending, ""
	case tst == "warning" || tst == "error" || st == "warning" || len(msgs) > 0:
		return DownloadStalled, first("The download client reports a problem")
	case st == "downloadclientunavailable":
		return DownloadStalled, "The download client is unavailable"
	case st == "paused":
		return DownloadStalled, "Paused in the download client"
	case st == "queued":
		return DownloadQueued, ""
	case st == "delay":
		return DownloadQueued, "Held by a delay profile"
	case st == "completed":
		return DownloadImportPending, ""
	case st == "downloading":
		return DownloadDownloading, ""
	}
	// Unknown / missing status (fallback, forks): judge by the bytes.
	if p, ok := r.Progress(); ok && p > 0 {
		return DownloadDownloading, ""
	}
	return DownloadQueued, ""
}

// activeStateRank orders the non-failed states for aggregation: the worst
// meaningful state wins. A stalled download needs attention even while
// others progress; anything still transferring outranks what is merely
// queued; import_pending only shows once everything has downloaded.
var activeStateRank = map[string]int{
	DownloadImportPending: 1,
	DownloadQueued:        2,
	DownloadDownloading:   3,
	DownloadStalled:       4,
}

// queueGroup is one download: a movie's release, or a Sonarr season pack
// that the queue lists once per episode.
type queueGroup struct {
	rows    []arr.QueueRecord
	state   string
	message string
}

// aggregateQueue combines every queue row belonging to one request. Rows are
// grouped by downloadId first so a season pack's shared size is counted
// once. The request is failed only when every download failed — a single bad
// episode among ones still downloading is reported, not escalated; otherwise
// the worst meaningful state of the remaining downloads wins. Progress is
// size-weighted across those downloads, the ETA is the latest one, and the
// message is the chosen state's first reason (prefixed with the episode when
// a show has several downloads).
func aggregateQueue(rows []arr.QueueRecord, now time.Time, show bool) DownloadStatus {
	var order []string
	groups := map[string]*queueGroup{}
	for i, r := range rows {
		key := r.DownloadID
		if key == "" {
			key = fmt.Sprintf("row:%d:%d", r.ID, i)
		}
		g, ok := groups[key]
		if !ok {
			g = &queueGroup{}
			groups[key] = g
			order = append(order, key)
		}
		g.rows = append(g.rows, r)
	}

	var active, failed []*queueGroup
	for _, key := range order {
		g := groups[key]
		// The rows of one download share its status; take the worst.
		g.state, g.message = "", ""
		for _, r := range g.rows {
			st, msg := queueRowState(r)
			if g.state == "" || worseState(st, g.state) {
				g.state, g.message = st, labelled(msg, r, show && len(order) > 1)
			}
		}
		if g.state == DownloadFailed {
			failed = append(failed, g)
		} else {
			active = append(active, g)
		}
	}

	out := DownloadStatus{Recovers: len(active) > 0}
	pick := active
	if len(active) == 0 {
		pick = failed
		out.State = DownloadFailed
	} else {
		for _, g := range active {
			if activeStateRank[g.state] > activeStateRank[out.State] {
				out.State = g.state
			}
		}
	}

	var size, done float64
	for _, g := range pick {
		if g.state == out.State && out.Message == "" {
			out.Message = g.message
		}
		r := g.rows[0]
		if s := float64(r.Size); s > 0 {
			left := float64(r.SizeLeft)
			if left < 0 {
				left = 0
			}
			if left > s {
				left = s
			}
			size += s
			done += s - left
		}
		if out.State != DownloadFailed {
			if eta := r.ETA(now); !eta.IsZero() && eta.After(out.ETA) {
				out.ETA = eta
			}
		}
	}
	if size > 0 {
		p := done / size
		out.Progress = &p
		sb := int64(size)
		out.SizeBytes = &sb
	}
	if len(failed) > 0 && len(active) > 0 {
		note := fmt.Sprintf("%d of %d downloads failed", len(failed), len(order))
		if out.Message == "" {
			out.Message = note
		} else {
			out.Message += " (" + note + ")"
		}
	}
	out.Message = clampMessage(out.Message)
	return out
}

// failedQueue is a request's queue verdict when every one of its queued
// downloads failed. Download clients keep failed items listed until someone
// clears them, so the verdict is held back while the item's file state and
// history are read, and judge decides whether it still stands.
type failedQueue struct {
	st DownloadStatus
	// downloads holds the failed downloads' ids.
	downloads map[string]bool
}

func newFailedQueue(st DownloadStatus, rows []arr.QueueRecord) *failedQueue {
	f := &failedQueue{st: st, downloads: map[string]bool{}}
	for _, r := range rows {
		if r.DownloadID != "" {
			f.downloads[r.DownloadID] = true
		}
	}
	return f
}

// judge picks between the idle verdict (file state + history, see idleStatus)
// and the failed queue rows; a nil receiver (no failed queue) is the idle
// verdict. The files being on disk wins. Otherwise the failure stands unless
// the newest grab / failure / import in the history is newer evidence than
// the failed downloads: an import, or a grab of some other download. With no
// such event — history pruned, or its newest event the failure itself — the
// queue rows are the evidence and the request is failed.
func (f *failedQueue) judge(idle DownloadStatus, hist []arr.HistoryRecord) DownloadStatus {
	if f == nil || idle.State == DownloadImportPending {
		return idle
	}
	if latest := latestDownloadEvent(hist); latest != nil {
		switch {
		case latest.EventType.IsImport():
			return idle
		case latest.EventType == arr.EventGrabbed && latest.DownloadID != "" && !f.downloads[latest.DownloadID]:
			return idle
		}
	}
	return f.st
}

// worseState reports whether a is a worse per-download state than b, with
// failed worst of all.
func worseState(a, b string) bool {
	if a == DownloadFailed {
		return b != DownloadFailed
	}
	if b == DownloadFailed {
		return false
	}
	return activeStateRank[a] > activeStateRank[b]
}

// labelled prefixes an episode download's message with "S01E02: " so a show
// with several downloads says which one has the problem.
func labelled(msg string, r arr.QueueRecord, withEpisode bool) string {
	if msg == "" || !withEpisode || r.Episode == nil {
		return msg
	}
	return fmt.Sprintf("S%02dE%02d: %s", r.Episode.SeasonNumber, r.Episode.EpisodeNumber, msg)
}

// latestDownloadEvent returns the newest grab, failure or import in a
// history listing (nil when there is none). Ties on date go to the higher id
// (ids are assigned in insert order).
func latestDownloadEvent(hist []arr.HistoryRecord) *arr.HistoryRecord {
	rows := make([]arr.HistoryRecord, 0, len(hist))
	for _, h := range hist {
		if h.EventType == arr.EventGrabbed || h.EventType == arr.EventDownloadFailed || h.EventType.IsImport() {
			rows = append(rows, h)
		}
	}
	if len(rows) == 0 {
		return nil
	}
	sort.SliceStable(rows, func(i, j int) bool {
		if !rows[i].Date.Equal(rows[j].Date.Time) {
			return rows[i].Date.After(rows[j].Date.Time)
		}
		return rows[i].ID > rows[j].ID
	})
	return &rows[0]
}

// idleStatus decides a request that has no queue entry: Radarr/Sonarr is
// either still searching, has given up after a failed download, has the file
// already, or isn't looking at all.
//
//   - hasFiles: the movie has its file / every monitored aired episode has one
//     → import_pending (the library scan will make it available).
//   - the newest grab/failure/import event is a failure → failed.
//   - monitored → searching (with waiting as the reason, when given).
//   - otherwise → stalled: nobody is looking for it.
func idleStatus(app string, monitored, hasFiles bool, hist []arr.HistoryRecord, waiting string) DownloadStatus {
	if hasFiles {
		one := 1.0
		return DownloadStatus{
			State:    DownloadImportPending,
			Progress: &one,
			Message:  "Downloaded — waiting for the library scan",
			Recovers: true,
		}
	}
	latest := latestDownloadEvent(hist)
	if latest != nil && latest.EventType == arr.EventDownloadFailed {
		msg := latest.Message()
		if msg == "" {
			msg = "The download failed and " + app + " found nothing else to grab"
		}
		return DownloadStatus{State: DownloadFailed, Message: clampMessage(msg)}
	}
	// A grab or import newer than any failure is evidence the title moved on.
	recovers := latest != nil
	if !monitored {
		return DownloadStatus{State: DownloadStalled, Message: "Not monitored in " + app, Recovers: recovers}
	}
	return DownloadStatus{State: DownloadSearching, Message: waiting, Recovers: recovers}
}

// movieWaiting explains a monitored movie Radarr isn't grabbing yet because
// it hasn't reached its minimum availability.
func movieWaiting(m *arr.Movie) string {
	if m.IsAvailable {
		return ""
	}
	switch strings.ToLower(m.Status) {
	case "tba", "announced":
		return "Waiting for release (announced)"
	case "incinemas":
		return "Waiting for release (in cinemas)"
	}
	return ""
}

// seriesIdleStatus is idleStatus for a Sonarr series.
func seriesIdleStatus(s *arr.Series, hist []arr.HistoryRecord) DownloadStatus {
	var files, count int
	if s.Statistics != nil {
		files, count = s.Statistics.EpisodeFileCount, s.Statistics.EpisodeCount
	}
	waiting := ""
	switch {
	case count == 0:
		waiting = "No episodes have aired yet"
	case files > 0:
		waiting = fmt.Sprintf("%d of %d episodes downloaded", files, count)
	}
	return idleStatus("Sonarr", s.Monitored, count > 0 && files >= count, hist, waiting)
}

// seasonsIdleStatus is seriesIdleStatus for a request that names seasons:
// the file tally and monitoring come from those seasons alone, so a request
// for season 3 isn't reported "8 of 30 episodes downloaded" because of
// seasons it never asked for. An all-seasons request (seasons empty), or a
// series whose resource lists none of the seasons, uses the series totals.
func seasonsIdleStatus(s *arr.Series, hist []arr.HistoryRecord, seasons []int) DownloadStatus {
	if len(seasons) == 0 {
		return seriesIdleStatus(s, hist)
	}
	var files, count int
	found := 0
	monitored := s.Monitored
	for _, n := range seasons {
		se, ok := s.Season(n)
		if !ok {
			continue
		}
		found++
		if !se.Monitored {
			monitored = false
		}
		if se.Statistics != nil {
			files += se.Statistics.EpisodeFileCount
			count += se.Statistics.EpisodeCount
		}
	}
	if found == 0 {
		return seriesIdleStatus(s, hist)
	}
	waiting := ""
	switch {
	case count == 0:
		waiting = "No episodes of the requested seasons have aired yet"
	case files > 0:
		waiting = fmt.Sprintf("%d of %d episodes downloaded", files, count)
	}
	return idleStatus("Sonarr", monitored, count > 0 && files >= count, hist, waiting)
}

// queueRowsForSeasons keeps the queue rows of the requested seasons. Rows
// that don't say which season they are for are kept (better to show a
// download than to hide one). seasons empty = every row. The input slice is
// shared between requests and is never modified.
func queueRowsForSeasons(rows []arr.QueueRecord, seasons []int) []arr.QueueRecord {
	if len(seasons) == 0 {
		return rows
	}
	want := make(map[int]bool, len(seasons))
	for _, n := range seasons {
		want[n] = true
	}
	out := make([]arr.QueueRecord, 0, len(rows))
	for _, r := range rows {
		season, known := r.SeasonNumber, r.SeasonNumber > 0
		if r.Episode != nil {
			season, known = r.Episode.SeasonNumber, true
		}
		if !known || want[season] {
			out = append(out, r)
		}
	}
	return out
}

// clampMessage trims whitespace and bounds a message to maxDownloadMessage
// runes, marking the cut.
func clampMessage(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if utf8.RuneCountInString(s) <= maxDownloadMessage {
		return s
	}
	r := []rune(s)
	return string(r[:maxDownloadMessage-1]) + "…"
}
