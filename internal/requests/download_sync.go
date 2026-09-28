package requests

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onscreen/onscreen/internal/arr"
	"github.com/onscreen/onscreen/internal/arrcrypt"
	"github.com/onscreen/onscreen/internal/db/gen"
	"github.com/onscreen/onscreen/internal/notification"
	"github.com/onscreen/onscreen/internal/observability"
)

// Download-status sync bounds.
const (
	// syncMaxRequests caps the work list one run reads. Far above any real
	// in-flight count; it only bounds a pathological table.
	syncMaxRequests = 500
	// syncFailedWindow is how long a failed request keeps being checked for
	// a recovery (a later grab). Past it the request is left for an admin.
	syncFailedWindow = 30 * 24 * time.Hour
	// syncServiceTimeout bounds all calls to one arr instance in one run, so
	// a black-holed instance costs one timeout, not one per request.
	syncServiceTimeout = 60 * time.Second
	// syncLookupBudget caps the per-request calls (movie/series detail,
	// history, id resolution) one run makes against one instance. Requests
	// with a live queue entry cost nothing extra — the queue is fetched once
	// per instance. Requests past the budget keep their previous state and,
	// being least recently checked, go first next run.
	syncLookupBudget = 60
	// syncTriggeredTimeout bounds a webhook-triggered sync.
	syncTriggeredTimeout = 2 * time.Minute
	// syncNoticeMessage bounds the failure reason quoted in notifications.
	syncNoticeMessage = 200
)

// SyncFilter narrows a sync to the requests an arr webhook was about. The
// zero value syncs everything.
type SyncFilter struct {
	// Kind limits the sync to one app's requests: "radarr" (movies) or
	// "sonarr" (shows). "" = both.
	Kind string
	// TMDBID / ArrItemID pick the title (either may match). Requests not yet
	// linked to an arr item are always included for their kind, since the
	// sync is what links them. Both zero = every request of Kind.
	TMDBID    int
	ArrItemID int
}

func (f SyncFilter) targeted() bool { return f.TMDBID > 0 || f.ArrItemID > 0 }

func (f SyncFilter) key() string {
	return fmt.Sprintf("%s:%d:%d", f.Kind, f.TMDBID, f.ArrItemID)
}

// match reports whether the filter selects the request.
func (f SyncFilter) match(r gen.MediaRequest) bool {
	if f.Kind != "" && arrKindForType(r.Type) != f.Kind {
		return false
	}
	if !f.targeted() {
		return true
	}
	if r.ArrItemID == nil {
		return true
	}
	return (f.TMDBID > 0 && int(r.TmdbID) == f.TMDBID) ||
		(f.ArrItemID > 0 && int(*r.ArrItemID) == f.ArrItemID)
}

// SyncResult summarises one sync run.
type SyncResult struct {
	// Requests is how many requests the run considered.
	Requests int
	// Services / ServiceErrors count the arr instances contacted and the
	// ones that failed (unreachable, bad key, timeout). A failed instance's
	// requests keep their previous state.
	Services      int
	ServiceErrors int
	// Updated is how many requests got a fresh download state.
	Updated int
	// Failed / Recovered count transitions into and out of failed.
	Failed    int
	Recovered int
	// Skipped: requests left untouched (instance disabled or kind mismatch,
	// lookup budget spent, transient lookup errors).
	Skipped int
}

// String renders the summary the scheduler stores as the task's output.
func (r SyncResult) String() string {
	return fmt.Sprintf("%d requests across %d arr services: %d updated, %d failed, %d recovered, %d skipped, %d service errors",
		r.Requests, r.Services, r.Updated, r.Failed, r.Recovered, r.Skipped, r.ServiceErrors)
}

// syncState serialises sync runs and coalesces webhook-triggered ones.
type syncState struct {
	run     sync.Mutex
	mu      sync.Mutex
	pending map[string]bool
}

// SyncDownloads refreshes the live download state of in-flight requests from
// their Radarr/Sonarr instances: approved and downloading requests, plus ones
// that failed within syncFailedWindow. Per instance the queue is fetched
// once and matched to requests by the linked arr item id (a show aggregates
// its episodes); a request with no queue entry is judged from the item's
// file state and history (searching / import_pending / failed / stalled), and
// so is one whose queue entries have all failed — a lingering failed entry
// only fails the request when nothing newer was grabbed or imported.
//
// Transitions:
//   - into failed: status becomes failed and the requester and admins are
//     notified — exactly once per failure, because only the conditional
//     UPDATE that actually flipped the row notifies.
//   - out of failed: a new grab or import moves the request back to
//     downloading (MarkFulfilled / ReconcileFulfillments still make it
//     available when the file lands).
//
// Only a database error listing the work is returned; a disabled, unreachable
// or slow instance is skipped (and counted) so one bad instance never fails
// the run. Runs are serialised: a webhook-triggered sync waits for a running
// one rather than racing it.
func (s *Service) SyncDownloads(ctx context.Context, filter SyncFilter) (SyncResult, error) {
	s.dlSync.run.Lock()
	defer s.dlSync.run.Unlock()
	return s.syncDownloads(ctx, filter)
}

// syncDownloads is SyncDownloads' body; the caller holds dlSync.run.
func (s *Service) syncDownloads(ctx context.Context, filter SyncFilter) (SyncResult, error) {
	var res SyncResult
	rows, err := s.db.ListMediaRequestsForDownloadSync(ctx, gen.ListMediaRequestsForDownloadSyncParams{
		FailedSince: pgtype.Timestamptz{Time: s.now().Add(-syncFailedWindow), Valid: true},
		MaxRows:     syncMaxRequests,
	})
	if err != nil {
		return res, fmt.Errorf("requests: list requests for download sync: %w", err)
	}

	byService := map[uuid.UUID][]gen.MediaRequest{}
	var order []uuid.UUID
	for _, r := range rows {
		if !r.ServiceID.Valid || !filter.match(r) {
			continue
		}
		id := uuid.UUID(r.ServiceID.Bytes)
		if _, ok := byService[id]; !ok {
			order = append(order, id)
		}
		byService[id] = append(byService[id], r)
		res.Requests++
	}

	for _, id := range order {
		reqs := byService[id]
		if err := ctx.Err(); err != nil {
			res.Skipped += len(reqs)
			continue
		}
		svc, err := s.db.GetArrService(ctx, id)
		if err != nil {
			s.logger.WarnContext(ctx, "download sync: arr service lookup failed", "service_id", id, "err", err)
			res.ServiceErrors++
			res.Skipped += len(reqs)
			continue
		}
		if !svc.Enabled {
			res.Skipped += len(reqs)
			continue
		}
		res.Services++
		sctx, cancel := context.WithTimeout(ctx, syncServiceTimeout)
		err = s.syncService(sctx, svc, reqs, &res)
		cancel()
		if err != nil {
			// Transport / auth detail stays in the log; the base URL is the
			// admin's own config and no API key is ever part of the error.
			s.logger.WarnContext(ctx, "download sync: arr service unavailable",
				"service_id", svc.ID, "service", svc.Name, "kind", svc.Kind, "err", err)
			res.ServiceErrors++
		}
	}
	if res.Failed > 0 || res.Recovered > 0 || res.ServiceErrors > 0 {
		s.logger.InfoContext(ctx, "download sync finished", "summary", res.String())
	}
	return res, nil
}

// TriggerDownloadSync runs a targeted SyncDownloads in the background, for the
// arr webhook: kind is "radarr" / "sonarr", tmdbID / arrItemID identify the
// title (0 = unknown). Bursts coalesce — while a sync for the same target is
// waiting to run, further triggers for it are dropped (the waiting run will
// see their state) — so a season pack's per-episode webhooks cost one sync.
func (s *Service) TriggerDownloadSync(kind string, tmdbID, arrItemID int) {
	f := SyncFilter{Kind: kind, TMDBID: tmdbID, ArrItemID: arrItemID}
	key := f.key()
	s.dlSync.mu.Lock()
	if s.dlSync.pending == nil {
		s.dlSync.pending = map[string]bool{}
	}
	if s.dlSync.pending[key] {
		s.dlSync.mu.Unlock()
		return
	}
	s.dlSync.pending[key] = true
	s.dlSync.mu.Unlock()

	observability.SafeGo(s.logger, "requests:triggered-download-sync", func() {
		// Take the run lock before clearing pending, so triggers that arrive
		// while an earlier run holds it coalesce into this one.
		s.dlSync.run.Lock()
		defer s.dlSync.run.Unlock()
		s.dlSync.mu.Lock()
		delete(s.dlSync.pending, key)
		s.dlSync.mu.Unlock()

		ctx, cancel := context.WithTimeout(context.Background(), syncTriggeredTimeout)
		defer cancel()
		if _, err := s.syncDownloads(ctx, f); err != nil {
			s.logger.WarnContext(ctx, "triggered download sync failed", "filter", key, "err", err)
		}
	})
}

// syncService syncs one instance's requests. Returns an error only when the
// instance itself is unusable (key won't open, queue unreachable); per-request
// lookup failures are logged and skipped.
func (s *Service) syncService(ctx context.Context, svc gen.ArrService, reqs []gen.MediaRequest, res *SyncResult) error {
	wantType := TypeMovie
	if svc.Kind == "sonarr" {
		wantType = TypeShow
	} else if svc.Kind != "radarr" {
		res.Skipped += len(reqs)
		return fmt.Errorf("unknown arr kind %q", svc.Kind)
	}
	apiKey, err := arrcrypt.Open(s.enc, svc.ID, svc.ApiKey)
	if err != nil {
		res.Skipped += len(reqs)
		return fmt.Errorf("open api key: %w", err)
	}
	client := s.arrClient(svc.BaseUrl, apiKey)

	var queue *arr.Queue
	if svc.Kind == "radarr" {
		queue, err = client.RadarrQueue(ctx)
	} else {
		queue, err = client.SonarrQueue(ctx)
	}
	if err != nil {
		res.Skipped += len(reqs)
		return fmt.Errorf("queue: %w", err)
	}
	byItem := map[int][]arr.QueueRecord{}
	for _, q := range queue.Records {
		id := q.MovieID
		if svc.Kind == "sonarr" {
			id = q.SeriesID
		}
		if id > 0 {
			byItem[id] = append(byItem[id], q)
		}
	}

	w := &serviceSync{s: s, svc: svc, client: client, budget: syncLookupBudget, now: s.now()}
	for _, r := range reqs {
		if r.Type != wantType {
			res.Skipped++
			continue
		}
		if ctx.Err() != nil {
			res.Skipped++
			continue
		}
		st, ok := w.status(ctx, r, byItem)
		if !ok {
			res.Skipped++
			continue
		}
		s.applyDownloadStatus(ctx, svc, r, st, res)
	}
	return nil
}

// serviceSync carries one instance's per-run state.
type serviceSync struct {
	s      *Service
	svc    gen.ArrService
	client *arr.Client
	budget int
	now    time.Time
}

// spend takes one lookup from the budget, reporting false when it's gone.
func (w *serviceSync) spend() bool {
	if w.budget <= 0 {
		return false
	}
	w.budget--
	return true
}

func (w *serviceSync) appName() string {
	if w.svc.Kind == "sonarr" {
		return "Sonarr"
	}
	return "Radarr"
}

// status computes one request's verdict. ok=false leaves the row untouched
// (budget spent, or a lookup failed transiently).
func (w *serviceSync) status(ctx context.Context, r gen.MediaRequest, byItem map[int][]arr.QueueRecord) (DownloadStatus, bool) {
	itemID, ok := w.linkedItem(ctx, r)
	if !ok {
		return DownloadStatus{}, false
	}
	if itemID == 0 {
		return DownloadStatus{
			State:   DownloadStalled,
			Message: "Not found in " + w.appName() + " — it may have been removed there",
		}, true
	}
	// A show request naming seasons only counts those seasons' downloads.
	seasons := RequestSeasons(r)
	var failedQ *failedQueue
	if rows := queueRowsForSeasons(byItem[itemID], seasons); len(rows) > 0 {
		st := aggregateQueue(rows, w.now, r.Type == TypeShow)
		if st.State != DownloadFailed {
			return st, true
		}
		// Every queued download failed. Download clients keep failed items
		// listed until someone clears them, so these rows can be old news:
		// the title may since have been imported or grabbed again. They are
		// judged against the file state and history below, like an idle
		// request, and only stand when nothing newer happened.
		failedQ = newFailedQueue(st, rows)
	}

	switch r.Type {
	case TypeMovie:
		if !w.spend() {
			return DownloadStatus{}, false
		}
		m, err := w.client.MovieByID(ctx, itemID)
		if errors.Is(err, arr.ErrNotFound) {
			w.relink(ctx, r, 0)
			return DownloadStatus{State: DownloadStalled, Message: "No longer in Radarr — it was removed there"}, true
		}
		if err != nil {
			w.lookupFailed(ctx, r, "movie", err)
			return DownloadStatus{}, false
		}
		if m.HasFile {
			return idleStatus("Radarr", m.Monitored, true, nil, ""), true
		}
		hist, ok := w.history(ctx, r, itemID)
		if !ok {
			return DownloadStatus{}, false
		}
		return failedQ.judge(idleStatus("Radarr", m.Monitored, false, hist, movieWaiting(m)), hist), true
	default:
		if !w.spend() {
			return DownloadStatus{}, false
		}
		series, err := w.client.SeriesByID(ctx, itemID)
		if errors.Is(err, arr.ErrNotFound) {
			w.relink(ctx, r, 0)
			return DownloadStatus{State: DownloadStalled, Message: "No longer in Sonarr — it was removed there"}, true
		}
		if err != nil {
			w.lookupFailed(ctx, r, "series", err)
			return DownloadStatus{}, false
		}
		if st := seasonsIdleStatus(series, nil, seasons); st.State == DownloadImportPending {
			return st, true
		}
		hist, ok := w.seasonHistory(ctx, r, itemID, seasons)
		if !ok {
			return DownloadStatus{}, false
		}
		return failedQ.judge(seasonsIdleStatus(series, hist, seasons), hist), true
	}
}

// maxSeasonHistoryCalls caps the per-season history reads for one request;
// a request naming more seasons reads the whole series' history instead.
const maxSeasonHistoryCalls = 3

// seasonHistory is history for a show request: the requested seasons' own
// history when it names a few (so another season's failed grab isn't pinned
// on this request), else the whole series'.
func (w *serviceSync) seasonHistory(ctx context.Context, r gen.MediaRequest, itemID int, seasons []int) ([]arr.HistoryRecord, bool) {
	if len(seasons) == 0 || len(seasons) > maxSeasonHistoryCalls {
		return w.history(ctx, r, itemID)
	}
	var out []arr.HistoryRecord
	for _, n := range seasons {
		if !w.spend() {
			return nil, false
		}
		hist, err := w.client.SeriesSeasonHistory(ctx, itemID, n)
		if errors.Is(err, arr.ErrNotFound) {
			continue
		}
		if err != nil {
			w.lookupFailed(ctx, r, "history", err)
			return nil, false
		}
		out = append(out, hist...)
	}
	return out, true
}

// history fetches the item's history within the budget.
func (w *serviceSync) history(ctx context.Context, r gen.MediaRequest, itemID int) ([]arr.HistoryRecord, bool) {
	if !w.spend() {
		return nil, false
	}
	var (
		hist []arr.HistoryRecord
		err  error
	)
	if r.Type == TypeMovie {
		hist, err = w.client.MovieHistory(ctx, itemID)
	} else {
		hist, err = w.client.SeriesHistory(ctx, itemID)
	}
	if errors.Is(err, arr.ErrNotFound) {
		// No history endpoint / no rows: judge on file state alone.
		return nil, true
	}
	if err != nil {
		w.lookupFailed(ctx, r, "history", err)
		return nil, false
	}
	return hist, true
}

// linkedItem returns the request's arr item id, resolving and storing it on
// first sync. id 0 with ok=true means the instance doesn't manage the title.
func (w *serviceSync) linkedItem(ctx context.Context, r gen.MediaRequest) (int, bool) {
	if r.ArrItemID != nil && *r.ArrItemID > 0 {
		return int(*r.ArrItemID), true
	}
	if !w.spend() {
		return 0, false
	}
	var (
		id  int
		err error
	)
	if r.Type == TypeMovie {
		var m *arr.Movie
		if m, err = w.client.MovieByTMDB(ctx, int(r.TmdbID)); err == nil {
			id = m.ID
		}
	} else {
		id, err = w.resolveSeries(ctx, r)
	}
	switch {
	case errors.Is(err, arr.ErrNotFound):
		return 0, true
	case err != nil:
		w.lookupFailed(ctx, r, "resolve", err)
		return 0, false
	}
	w.relink(ctx, r, id)
	return id, true
}

// resolveSeries finds the Sonarr series for a show request: by the TVDB id
// TMDB reports for it, else through Sonarr's own TMDB lookup (v4+), trusting
// only an exact TMDB match. The title search the add path falls back to is
// not used here — linking a request to the wrong series would report another
// show's downloads.
func (w *serviceSync) resolveSeries(ctx context.Context, r gen.MediaRequest) (int, error) {
	tvdbID := 0
	if w.s.tmdb != nil {
		if id, _, err := w.s.tmdb.GetTVExternalIDs(ctx, int(r.TmdbID)); err == nil {
			tvdbID = id
		}
	}
	if tvdbID <= 0 {
		look, err := w.client.LookupSeriesByTMDB(ctx, int(r.TmdbID))
		if err != nil {
			return 0, err
		}
		if look.TMDBID != int(r.TmdbID) || look.TVDBID <= 0 {
			return 0, arr.ErrNotFound
		}
		tvdbID = look.TVDBID
	}
	series, err := w.client.SeriesByTVDB(ctx, tvdbID)
	if err != nil {
		return 0, err
	}
	return series.ID, nil
}

// relink stores (or, with 0, clears) the request's arr item id.
func (w *serviceSync) relink(ctx context.Context, r gen.MediaRequest, id int) {
	var v *int32
	if id > 0 {
		n := int32(id)
		v = &n
	}
	if err := w.s.db.SetMediaRequestArrItem(ctx, gen.SetMediaRequestArrItemParams{ID: r.ID, ArrItemID: v}); err != nil {
		w.s.logger.WarnContext(ctx, "download sync: store arr item id", "request_id", r.ID, "err", err)
	}
}

func (w *serviceSync) lookupFailed(ctx context.Context, r gen.MediaRequest, what string, err error) {
	w.s.logger.WarnContext(ctx, "download sync: lookup failed",
		"request_id", r.ID, "service_id", w.svc.ID, "lookup", what, "err", err)
}

// applyDownloadStatus writes a verdict, performing the failed transitions.
func (s *Service) applyDownloadStatus(ctx context.Context, svc gen.ArrService, r gen.MediaRequest, st DownloadStatus, res *SyncResult) {
	progress, size, eta := statusColumns(st)
	msg := nullableString(st.Message)

	switch {
	case st.State == DownloadFailed && r.Status != StatusFailed:
		n, err := s.db.FailMediaRequestDownload(ctx, gen.FailMediaRequestDownloadParams{
			ID: r.ID, DownloadProgress: progress, DownloadSizeBytes: size, DownloadMessage: msg,
		})
		if err != nil {
			s.logger.ErrorContext(ctx, "download sync: mark failed", "request_id", r.ID, "err", err)
			return
		}
		res.Updated++
		if n == 1 {
			res.Failed++
			s.logger.WarnContext(ctx, "media request download failed",
				"request_id", r.ID, "service_id", svc.ID, "title", r.Title, "reason", st.Message)
			s.notifyDownloadFailed(ctx, svc, r, st.Message)
		}
		return

	case r.Status == StatusFailed && st.State != DownloadFailed && st.Recovers:
		n, err := s.db.RecoverMediaRequestDownload(ctx, gen.RecoverMediaRequestDownloadParams{
			ID: r.ID, DownloadState: &st.State, DownloadProgress: progress,
			DownloadEta: eta, DownloadSizeBytes: size, DownloadMessage: msg,
		})
		if err != nil {
			s.logger.ErrorContext(ctx, "download sync: recover", "request_id", r.ID, "err", err)
			return
		}
		if n == 1 {
			res.Updated++
			res.Recovered++
			s.logger.InfoContext(ctx, "media request download recovered",
				"request_id", r.ID, "service_id", svc.ID, "state", st.State)
			return
		}
		// Superseded by a newer active request for the title: stays failed.
		st = DownloadStatus{State: DownloadFailed, Message: deref(r.DownloadMessage)}
		progress, size, eta = statusColumns(st)
		msg = nullableString(st.Message)

	case r.Status == StatusFailed && st.State != DownloadFailed:
		// No evidence of a new grab: keep the failure as recorded, only
		// marking the row checked.
		st = DownloadStatus{State: DownloadFailed, Message: deref(r.DownloadMessage)}
		progress, size, eta = statusColumns(st)
		msg = nullableString(st.Message)
	}

	n, err := s.db.UpdateMediaRequestDownload(ctx, gen.UpdateMediaRequestDownloadParams{
		ID: r.ID, DownloadState: &st.State, DownloadProgress: progress,
		DownloadEta: eta, DownloadSizeBytes: size, DownloadMessage: msg,
	})
	if err != nil {
		s.logger.ErrorContext(ctx, "download sync: update", "request_id", r.ID, "err", err)
		return
	}
	if n == 1 {
		res.Updated++
	}
	// An approved row whose post-approval flip was lost catches up here.
	if r.Status == StatusApproved && st.State != DownloadFailed {
		if err := s.db.MarkMediaRequestDownloading(ctx, r.ID); err != nil {
			s.logger.WarnContext(ctx, "download sync: mark downloading", "request_id", r.ID, "err", err)
		}
	}
}

// statusColumns converts a verdict to its nullable column values.
func statusColumns(st DownloadStatus) (progress *float32, size *int64, eta pgtype.Timestamptz) {
	if st.Progress != nil {
		p := float32(*st.Progress)
		if p < 0 {
			p = 0
		}
		if p > 1 {
			p = 1
		}
		progress = &p
	}
	if st.SizeBytes != nil && *st.SizeBytes >= 0 {
		v := *st.SizeBytes
		size = &v
	}
	if !st.ETA.IsZero() {
		eta = pgtype.Timestamptz{Time: st.ETA, Valid: true}
	}
	return progress, size, eta
}

// notifyDownloadFailed tells the requester and every admin. An admin
// requester only gets the admin notice (they're one of the admins). The
// failure reason is the *arr app's own text — it can name download-client
// hosts and folder paths — so only the admin notice quotes it.
func (s *Service) notifyDownloadFailed(ctx context.Context, svc gen.ArrService, r gen.MediaRequest, reason string) {
	if s.notify == nil {
		return
	}
	reason = noticeReason(reason)
	title := titleWithYear(r.Title, int(derefInt32(r.Year)))

	requester, isAdmin := "A user", false
	if perm, err := s.db.GetUserRequestPermissions(ctx, r.UserID); err == nil {
		isAdmin = perm.IsAdmin
		if perm.Username != "" {
			requester = perm.Username
		}
	}
	if !isAdmin {
		body := fmt.Sprintf("%q couldn't be downloaded. An admin has been told and can retry it.", r.Title)
		s.notify.Notify(ctx, r.UserID, notification.TypeRequestFailed, "Download failed: "+r.Title, body, nil)
	}
	adminBody := fmt.Sprintf("%s's request for %q failed in %s", requester, title, svc.Name)
	if reason != "" {
		adminBody += ": " + reason
	}
	s.notify.NotifyAdmins(ctx, notification.TypeRequestFailed, "Request download failed", adminBody, nil)
}

// noticeReason bounds a failure reason for a notification body.
func noticeReason(s string) string {
	s = strings.TrimSuffix(clampMessage(s), ".")
	if r := []rune(s); len(r) > syncNoticeMessage {
		s = string(r[:syncNoticeMessage-1]) + "…"
	}
	return s
}

// Usernames maps the requesters of rows to their usernames (the admin queue
// shows who asked). A lookup failure yields an empty map — names are a
// convenience, not worth failing a listing over.
func (s *Service) Usernames(ctx context.Context, rows []gen.MediaRequest) map[uuid.UUID]string {
	out := map[uuid.UUID]string{}
	if len(rows) == 0 {
		return out
	}
	seen := map[uuid.UUID]bool{}
	ids := make([]uuid.UUID, 0, len(rows))
	for _, r := range rows {
		if !seen[r.UserID] {
			seen[r.UserID] = true
			ids = append(ids, r.UserID)
		}
	}
	names, err := s.db.ListUsernamesByIDs(ctx, ids)
	if err != nil {
		s.logger.WarnContext(ctx, "requests: username lookup failed", "err", err)
		return out
	}
	for _, n := range names {
		out[n.ID] = n.Username
	}
	return out
}
