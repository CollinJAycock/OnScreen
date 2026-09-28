package v1

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onscreen/onscreen/internal/api/middleware"
	"github.com/onscreen/onscreen/internal/api/respond"
	"github.com/onscreen/onscreen/internal/arr"
	"github.com/onscreen/onscreen/internal/audit"
	"github.com/onscreen/onscreen/internal/auth"
	"github.com/onscreen/onscreen/internal/db/gen"
	"github.com/onscreen/onscreen/internal/notification"
)

// "Report a problem": users flag an item whose video / audio / subtitles are
// broken or whose metadata is the wrong title; admins work the reports on the
// Library health page (which also lists files the integrity probe marked
// damaged) and can ask Radarr/Sonarr to re-grab the title (items_regrab.go).

// Report kinds (media_issues.kind) and statuses (media_issues.status).
const (
	IssueKindVideo      = "video"
	IssueKindAudio      = "audio"
	IssueKindSubtitles  = "subtitles"
	IssueKindWrongMatch = "wrong_match"
	IssueKindOther      = "other"

	IssueStatusOpen      = "open"
	IssueStatusResolved  = "resolved"
	IssueStatusDismissed = "dismissed"
)

// Limits on reports.
const (
	// issueNoteMaxRunes matches the media_issues note CHECK (char_length).
	issueNoteMaxRunes = 1000
	// maxOpenIssuesPerUser caps a non-admin's open reports, so one account
	// can't flood the admin queue (the route is also rate limited).
	maxOpenIssuesPerUser = 10
	// issueBodyMaxBytes bounds the JSON body of the report / update calls.
	issueBodyMaxBytes = 16 << 10
	// Damaged-file page size on the Library health endpoint.
	libraryHealthDefaultLimit = 100
	libraryHealthMaxLimit     = 500
	// Admin issue list page size.
	issueListDefaultLimit = 50
	issueListMaxLimit     = 200
)

// issueKindLabels is the short wording used in admin notifications.
var issueKindLabels = map[string]string{
	IssueKindVideo:      "video",
	IssueKindAudio:      "audio",
	IssueKindSubtitles:  "subtitles",
	IssueKindWrongMatch: "wrong match",
	IssueKindOther:      "something else",
}

// IsValidIssueKind reports whether k is an accepted report kind.
func IsValidIssueKind(k string) bool {
	_, ok := issueKindLabels[k]
	return ok
}

// IssuesDB is the slice of generated queries the issue endpoints need.
type IssuesDB interface {
	GetMediaItem(ctx context.Context, id uuid.UUID) (gen.GetMediaItemRow, error)
	GetMediaFile(ctx context.Context, id uuid.UUID) (gen.MediaFile, error)

	CreateMediaIssue(ctx context.Context, arg gen.CreateMediaIssueParams) (gen.MediaIssue, error)
	GetMediaIssue(ctx context.Context, id uuid.UUID) (gen.MediaIssue, error)
	CountOpenMediaIssuesForUser(ctx context.Context, userID uuid.UUID) (int32, error)
	GetOpenMediaIssueForUserItemKind(ctx context.Context, arg gen.GetOpenMediaIssueForUserItemKindParams) (gen.MediaIssue, error)
	ListMediaIssuesForUserItem(ctx context.Context, arg gen.ListMediaIssuesForUserItemParams) ([]gen.MediaIssue, error)
	ListMediaIssuesAdmin(ctx context.Context, arg gen.ListMediaIssuesAdminParams) ([]gen.ListMediaIssuesAdminRow, error)
	CountMediaIssuesAdmin(ctx context.Context, status *string) (int32, error)
	CloseMediaIssue(ctx context.Context, arg gen.CloseMediaIssueParams) (gen.MediaIssue, error)

	ListDamagedMediaFiles(ctx context.Context, arg gen.ListDamagedMediaFilesParams) ([]gen.ListDamagedMediaFilesRow, error)
	CountDamagedMediaFiles(ctx context.Context) (int32, error)
	CountMediaItemsMissingArt(ctx context.Context) (int32, error)
	CountUnmatchedTopLevelItems(ctx context.Context) (int32, error)

	ListEnabledArrServicesByKind(ctx context.Context, kind string) ([]gen.ArrService, error)
}

// IssueNotifier is the notification fan-out the handler uses. Satisfied by
// *notification.Service.
type IssueNotifier interface {
	Notify(ctx context.Context, userID uuid.UUID, typ, title, body string, itemID *uuid.UUID)
	NotifyAdmins(ctx context.Context, typ, title, body string, itemID *uuid.UUID)
}

// IssueHandler serves /items/{id}/issues (any user), /admin/issues,
// /admin/library-health and /admin/items/{id}/regrab (admins).
type IssueHandler struct {
	db        IssuesDB
	access    LibraryAccessChecker
	notify    IssueNotifier
	audit     *audit.Logger
	enc       *auth.Encryptor
	arrClient func(baseURL, apiKey string) *arr.Client
	logger    *slog.Logger
}

// NewIssueHandler builds the handler. access enforces the library ACL on the
// user endpoints (the content-rating ceiling is always enforced).
func NewIssueHandler(db IssuesDB, access LibraryAccessChecker, logger *slog.Logger) *IssueHandler {
	return &IssueHandler{db: db, access: access, arrClient: arr.New, logger: logger}
}

// WithNotifier wires admin / reporter notifications.
func (h *IssueHandler) WithNotifier(n IssueNotifier) *IssueHandler {
	h.notify = n
	return h
}

// WithAudit wires the audit log for admin actions.
func (h *IssueHandler) WithAudit(a *audit.Logger) *IssueHandler {
	h.audit = a
	return h
}

// WithEncryptor supplies the key that opens sealed arr_services.api_key
// values for re-grab.
func (h *IssueHandler) WithEncryptor(e *auth.Encryptor) *IssueHandler {
	h.enc = e
	return h
}

// SetArrClientFactory swaps the *arr client constructor (tests).
func (h *IssueHandler) SetArrClientFactory(f func(baseURL, apiKey string) *arr.Client) {
	h.arrClient = f
}

// ── DTOs ──────────────────────────────────────────────────────────────────

// IssueResponse is a report as its reporter sees it.
type IssueResponse struct {
	ID             string     `json:"id"`
	ItemID         string     `json:"item_id"`
	FileID         *string    `json:"file_id,omitempty"`
	Kind           string     `json:"kind"`
	Note           *string    `json:"note,omitempty"`
	Status         string     `json:"status"`
	CreatedAt      time.Time  `json:"created_at"`
	ResolvedAt     *time.Time `json:"resolved_at,omitempty"`
	ResolutionNote *string    `json:"resolution_note,omitempty"`
}

// AdminIssueResponse is one row of the admin queue.
type AdminIssueResponse struct {
	IssueResponse
	LibraryID          string  `json:"library_id"`
	ItemType           string  `json:"item_type"`
	ItemTitle          string  `json:"item_title"`
	ItemYear           *int32  `json:"item_year,omitempty"`
	ShowTitle          *string `json:"show_title,omitempty"`
	SeasonNumber       *int32  `json:"season_number,omitempty"`
	EpisodeNumber      *int32  `json:"episode_number,omitempty"`
	PosterPath         *string `json:"poster_path,omitempty"`
	ReporterID         string  `json:"reporter_id"`
	ReporterUsername   string  `json:"reporter_username"`
	ResolvedByUsername *string `json:"resolved_by_username,omitempty"`
	// FileName is the reported file's base name — never the full server path.
	FileName *string `json:"file_name,omitempty"`
}

type adminIssueListResponse struct {
	Items []AdminIssueResponse `json:"items"`
	Total int                  `json:"total"`
}

// DamagedFileResponse is one integrity-probe failure on Library health.
type DamagedFileResponse struct {
	FileID          string     `json:"file_id"`
	ItemID          string     `json:"item_id"`
	LibraryID       string     `json:"library_id"`
	ItemType        string     `json:"item_type"`
	Title           string     `json:"title"`
	Year            *int32     `json:"year,omitempty"`
	ShowTitle       *string    `json:"show_title,omitempty"`
	SeasonNumber    *int32     `json:"season_number,omitempty"`
	EpisodeNumber   *int32     `json:"episode_number,omitempty"`
	PathBasename    string     `json:"path_basename"`
	IntegrityDetail *string    `json:"integrity_detail,omitempty"`
	CheckedAt       *time.Time `json:"checked_at,omitempty"`
}

// LibraryHealthResponse is GET /admin/library-health.
type LibraryHealthResponse struct {
	OpenIssues      int                   `json:"open_issues"`
	DamagedFiles    []DamagedFileResponse `json:"damaged_files"`
	DamagedTotal    int                   `json:"damaged_total"`
	UnmatchedCount  int                   `json:"unmatched_count"`
	MissingArtCount int                   `json:"missing_art_count"`
}

type createIssueRequest struct {
	Kind   string  `json:"kind"`
	Note   *string `json:"note"`
	FileID *string `json:"file_id"`
}

type updateIssueRequest struct {
	Status string  `json:"status"`
	Note   *string `json:"note"`
}

// ── User endpoints ────────────────────────────────────────────────────────

// Create handles POST /api/v1/items/{id}/issues.
func (h *IssueHandler) Create(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	claims := middleware.ClaimsFromContext(ctx)
	if claims == nil {
		respond.Unauthorized(w, r)
		return
	}
	item, ok := h.visibleItem(w, r)
	if !ok {
		return
	}

	var body createIssueRequest
	if !decodeIssueBody(w, r, &body) {
		return
	}
	if !IsValidIssueKind(body.Kind) {
		respond.ValidationError(w, r, "kind must be one of video, audio, subtitles, wrong_match, other")
		return
	}
	note, msg := cleanIssueNote(body.Note)
	if msg != "" {
		respond.ValidationError(w, r, msg)
		return
	}
	var fileID pgtype.UUID
	if body.FileID != nil && strings.TrimSpace(*body.FileID) != "" {
		id, err := uuid.Parse(strings.TrimSpace(*body.FileID))
		if err != nil {
			respond.ValidationError(w, r, "invalid file_id")
			return
		}
		f, err := h.db.GetMediaFile(ctx, id)
		if err != nil || f.MediaItemID != item.ID {
			// Same answer for "no such file" and "not this item's file", so the
			// field can't probe files the caller has no other way to see.
			respond.ValidationError(w, r, "file_id does not belong to this item")
			return
		}
		fileID = pgtype.UUID{Bytes: id, Valid: true}
	}

	// One open report per (user, item, kind). The partial unique index
	// enforces it too; this pre-check gives the common case a clean answer.
	if _, err := h.db.GetOpenMediaIssueForUserItemKind(ctx, gen.GetOpenMediaIssueForUserItemKindParams{
		UserID: claims.UserID, ItemID: item.ID, Kind: body.Kind,
	}); err == nil {
		respond.Error(w, r, http.StatusConflict, "ALREADY_REPORTED",
			"you already have an open report of this kind for this item")
		return
	} else if !errors.Is(err, pgx.ErrNoRows) {
		h.logger.ErrorContext(ctx, "issues: check existing report", "item_id", item.ID, "err", err)
		respond.InternalError(w, r)
		return
	}
	if !claims.IsAdmin {
		n, err := h.db.CountOpenMediaIssuesForUser(ctx, claims.UserID)
		if err != nil {
			h.logger.ErrorContext(ctx, "issues: count open reports", "user_id", claims.UserID, "err", err)
			respond.InternalError(w, r)
			return
		}
		if n >= maxOpenIssuesPerUser {
			respond.Error(w, r, http.StatusTooManyRequests, "TOO_MANY_OPEN_ISSUES",
				fmt.Sprintf("you have %d open reports; wait for an admin to review them", n))
			return
		}
	}

	issue, err := h.db.CreateMediaIssue(ctx, gen.CreateMediaIssueParams{
		ItemID: item.ID,
		FileID: fileID,
		UserID: claims.UserID,
		Kind:   body.Kind,
		Note:   note,
	})
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			respond.Error(w, r, http.StatusConflict, "ALREADY_REPORTED",
				"you already have an open report of this kind for this item")
			return
		}
		h.logger.ErrorContext(ctx, "issues: create", "item_id", item.ID, "err", err)
		respond.InternalError(w, r)
		return
	}

	if h.notify != nil {
		itemID := item.ID
		reporter := claims.Username
		if reporter == "" {
			reporter = "A user"
		}
		h.notify.NotifyAdmins(ctx, notification.TypeIssueReported, "Problem reported",
			fmt.Sprintf("%s reported a problem with %s: %s",
				reporter, h.displayTitle(ctx, item), issueKindLabels[body.Kind]),
			&itemID)
	}
	respond.Created(w, r, toIssueResponse(issue))
}

// ListMine handles GET /api/v1/items/{id}/issues: the caller's own reports on
// the item, newest first.
func (h *IssueHandler) ListMine(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	claims := middleware.ClaimsFromContext(ctx)
	if claims == nil {
		respond.Unauthorized(w, r)
		return
	}
	item, ok := h.visibleItem(w, r)
	if !ok {
		return
	}
	rows, err := h.db.ListMediaIssuesForUserItem(ctx, gen.ListMediaIssuesForUserItemParams{
		UserID: claims.UserID, ItemID: item.ID,
	})
	if err != nil {
		h.logger.ErrorContext(ctx, "issues: list mine", "item_id", item.ID, "err", err)
		respond.InternalError(w, r)
		return
	}
	out := make([]IssueResponse, 0, len(rows))
	for _, row := range rows {
		out = append(out, toIssueResponse(row))
	}
	respond.Success(w, r, out)
}

// visibleItem loads {id} and applies the library ACL + content-rating
// ceiling, answering 404 for anything the caller can't see.
func (h *IssueHandler) visibleItem(w http.ResponseWriter, r *http.Request) (gen.GetMediaItemRow, bool) {
	itemID, err := parseUUID(r, "id")
	if err != nil {
		respond.BadRequest(w, r, "invalid item id")
		return gen.GetMediaItemRow{}, false
	}
	item, err := h.db.GetMediaItem(r.Context(), itemID)
	if err != nil {
		if !errors.Is(err, pgx.ErrNoRows) {
			h.logger.ErrorContext(r.Context(), "issues: get item", "item_id", itemID, "err", err)
			respond.InternalError(w, r)
			return gen.GetMediaItemRow{}, false
		}
		respond.NotFound(w, r)
		return gen.GetMediaItemRow{}, false
	}
	if !itemAddAllowed(w, r, h.access, h.logger, item) {
		return gen.GetMediaItemRow{}, false
	}
	return item, true
}

// ── Admin endpoints ───────────────────────────────────────────────────────

// AdminList handles GET /api/v1/admin/issues?status=open|resolved|dismissed|all.
func (h *IssueHandler) AdminList(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if !issuesRequireAdmin(w, r) {
		return
	}
	var status *string
	switch s := r.URL.Query().Get("status"); s {
	case "", IssueStatusOpen:
		v := IssueStatusOpen
		status = &v
	case IssueStatusResolved, IssueStatusDismissed:
		status = &s
	case "all":
	default:
		respond.ValidationError(w, r, "status must be open, resolved, dismissed or all")
		return
	}
	page := respond.ParsePagination(r, issueListDefaultLimit, issueListMaxLimit)
	rows, err := h.db.ListMediaIssuesAdmin(ctx, gen.ListMediaIssuesAdminParams{
		Status: status, Lim: page.Limit, Off: page.Offset,
	})
	if err != nil {
		h.logger.ErrorContext(ctx, "issues: admin list", "err", err)
		respond.InternalError(w, r)
		return
	}
	total, err := h.db.CountMediaIssuesAdmin(ctx, status)
	if err != nil {
		h.logger.ErrorContext(ctx, "issues: admin count", "err", err)
		respond.InternalError(w, r)
		return
	}
	out := adminIssueListResponse{Items: make([]AdminIssueResponse, 0, len(rows)), Total: int(total)}
	for _, row := range rows {
		out.Items = append(out.Items, toAdminIssueResponse(row))
	}
	respond.Success(w, r, out)
}

// AdminUpdate handles PATCH /api/v1/admin/issues/{id} {status, note?}:
// resolves or dismisses an open report, tells the reporter, and audits it.
func (h *IssueHandler) AdminUpdate(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if !issuesRequireAdmin(w, r) {
		return
	}
	claims := middleware.ClaimsFromContext(ctx)
	issueID, err := parseUUID(r, "id")
	if err != nil {
		respond.BadRequest(w, r, "invalid issue id")
		return
	}
	var body updateIssueRequest
	if !decodeIssueBody(w, r, &body) {
		return
	}
	if body.Status != IssueStatusResolved && body.Status != IssueStatusDismissed {
		respond.ValidationError(w, r, "status must be resolved or dismissed")
		return
	}
	note, msg := cleanIssueNote(body.Note)
	if msg != "" {
		respond.ValidationError(w, r, msg)
		return
	}

	issue, err := h.db.CloseMediaIssue(ctx, gen.CloseMediaIssueParams{
		ID:             issueID,
		Status:         body.Status,
		ResolvedBy:     pgtype.UUID{Bytes: claims.UserID, Valid: true},
		ResolutionNote: note,
	})
	if err != nil {
		if !errors.Is(err, pgx.ErrNoRows) {
			h.logger.ErrorContext(ctx, "issues: close", "issue_id", issueID, "err", err)
			respond.InternalError(w, r)
			return
		}
		// Missing, or already closed?
		if _, gerr := h.db.GetMediaIssue(ctx, issueID); gerr != nil {
			respond.NotFound(w, r)
			return
		}
		respond.Error(w, r, http.StatusConflict, "NOT_OPEN", "this report is already closed")
		return
	}

	if h.audit != nil {
		actor := claims.UserID
		h.audit.Log(ctx, &actor, audit.ActionIssueUpdate, "issue:"+issue.ID.String(), map[string]any{
			"status":      issue.Status,
			"item_id":     issue.ItemID.String(),
			"kind":        issue.Kind,
			"reporter_id": issue.UserID.String(),
		}, audit.ClientIP(r))
	}

	// Tell the reporter — unless the admin closed their own report.
	if h.notify != nil && issue.UserID != claims.UserID {
		title := "Problem report resolved"
		verb := "resolved"
		if issue.Status == IssueStatusDismissed {
			title = "Problem report closed"
			verb = "closed"
		}
		subject := "the item"
		if item, err := h.db.GetMediaItem(ctx, issue.ItemID); err == nil {
			subject = h.displayTitle(ctx, item)
		}
		text := fmt.Sprintf("An admin %s your report about %s.", verb, subject)
		if note != nil {
			text += " " + *note
		}
		itemID := issue.ItemID
		h.notify.Notify(ctx, issue.UserID, notification.TypeIssueResolved, title, text, &itemID)
	}
	respond.Success(w, r, toIssueResponse(issue))
}

// LibraryHealth handles GET /api/v1/admin/library-health?limit&offset: open
// report count, the integrity probe's damaged files (paged), and the
// Unmatched / Missing art counts.
func (h *IssueHandler) LibraryHealth(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if !issuesRequireAdmin(w, r) {
		return
	}
	page := respond.ParsePagination(r, libraryHealthDefaultLimit, libraryHealthMaxLimit)
	open := IssueStatusOpen
	openCount, err := h.db.CountMediaIssuesAdmin(ctx, &open)
	if err != nil {
		h.logger.ErrorContext(ctx, "library health: count open issues", "err", err)
		respond.InternalError(w, r)
		return
	}
	rows, err := h.db.ListDamagedMediaFiles(ctx, gen.ListDamagedMediaFilesParams{Lim: page.Limit, Off: page.Offset})
	if err != nil {
		h.logger.ErrorContext(ctx, "library health: list damaged files", "err", err)
		respond.InternalError(w, r)
		return
	}
	damagedTotal, err := h.db.CountDamagedMediaFiles(ctx)
	if err != nil {
		h.logger.ErrorContext(ctx, "library health: count damaged files", "err", err)
		respond.InternalError(w, r)
		return
	}
	// The two counts are the same snapshot /jobs shows; a failure there is
	// logged and reported as 0 rather than failing the page.
	unmatched, err := h.db.CountUnmatchedTopLevelItems(ctx)
	if err != nil {
		h.logger.WarnContext(ctx, "library health: count unmatched", "err", err)
		unmatched = 0
	}
	missingArt, err := h.db.CountMediaItemsMissingArt(ctx)
	if err != nil {
		h.logger.WarnContext(ctx, "library health: count missing art", "err", err)
		missingArt = 0
	}

	out := LibraryHealthResponse{
		OpenIssues:      int(openCount),
		DamagedFiles:    make([]DamagedFileResponse, 0, len(rows)),
		DamagedTotal:    int(damagedTotal),
		UnmatchedCount:  int(unmatched),
		MissingArtCount: int(missingArt),
	}
	for _, row := range rows {
		show, season, episode := issueShowContext(row.ItemType, row.ItemIndex, row.ParentTitle, row.ParentIndex, row.GrandparentTitle)
		out.DamagedFiles = append(out.DamagedFiles, DamagedFileResponse{
			FileID:          row.FileID.String(),
			ItemID:          row.ItemID.String(),
			LibraryID:       row.LibraryID.String(),
			ItemType:        row.ItemType,
			Title:           row.ItemTitle,
			Year:            row.ItemYear,
			ShowTitle:       show,
			SeasonNumber:    season,
			EpisodeNumber:   episode,
			PathBasename:    issuePathBasename(row.FilePath),
			IntegrityDetail: row.IntegrityDetail,
			CheckedAt:       issueTimePtr(row.IntegrityCheckedAt),
		})
	}
	respond.Success(w, r, out)
}

// ── helpers ───────────────────────────────────────────────────────────────

// issuesRequireAdmin re-checks admin claims (the routes also sit behind
// AdminRequired).
func issuesRequireAdmin(w http.ResponseWriter, r *http.Request) bool {
	claims := middleware.ClaimsFromContext(r.Context())
	if claims == nil {
		respond.Unauthorized(w, r)
		return false
	}
	if !claims.IsAdmin {
		respond.Forbidden(w, r)
		return false
	}
	return true
}

// decodeIssueBody decodes a bounded JSON body. An empty body decodes to the
// zero value (callers validate required fields).
func decodeIssueBody(w http.ResponseWriter, r *http.Request, dst any) bool {
	if r.Body == nil {
		return true
	}
	r.Body = http.MaxBytesReader(w, r.Body, issueBodyMaxBytes)
	if err := json.NewDecoder(r.Body).Decode(dst); err != nil && !errors.Is(err, io.EOF) {
		respond.BadRequest(w, r, "invalid JSON body")
		return false
	}
	return true
}

// cleanIssueNote trims a note, drops NUL bytes (Postgres text refuses them),
// and enforces the length limit. An empty note becomes nil.
func cleanIssueNote(raw *string) (*string, string) {
	if raw == nil {
		return nil, ""
	}
	s := strings.TrimSpace(strings.ReplaceAll(*raw, "\x00", ""))
	if s == "" {
		return nil, ""
	}
	if !utf8.ValidString(s) {
		return nil, "note must be valid UTF-8"
	}
	if utf8.RuneCountInString(s) > issueNoteMaxRunes {
		return nil, fmt.Sprintf("note must be at most %d characters", issueNoteMaxRunes)
	}
	return &s, ""
}

// displayTitle names an item for a notification: "Show — S01E02 · Title" for
// an episode, "Show — Season 2" for a season, the title otherwise.
func (h *IssueHandler) displayTitle(ctx context.Context, item gen.GetMediaItemRow) string {
	switch item.Type {
	case "episode", "season":
	default:
		return item.Title
	}
	if !item.ParentID.Valid {
		return item.Title
	}
	parent, err := h.db.GetMediaItem(ctx, uuid.UUID(item.ParentID.Bytes))
	if err != nil {
		return item.Title
	}
	if item.Type == "season" {
		return parent.Title + " — " + item.Title
	}
	showTitle := parent.Title
	if parent.ParentID.Valid {
		if show, err := h.db.GetMediaItem(ctx, uuid.UUID(parent.ParentID.Bytes)); err == nil {
			showTitle = show.Title
		}
	}
	if parent.Index != nil && item.Index != nil {
		return fmt.Sprintf("%s — S%02dE%02d · %s", showTitle, *parent.Index, *item.Index, item.Title)
	}
	return showTitle + " — " + item.Title
}

// issueShowContext derives the show title / season / episode numbers for an
// episode or season row from its parent and grandparent columns.
func issueShowContext(itemType string, itemIndex *int32, parentTitle *string, parentIndex *int32, grandparentTitle *string) (show *string, season, episode *int32) {
	switch itemType {
	case "episode":
		return grandparentTitle, parentIndex, itemIndex
	case "season":
		return parentTitle, itemIndex, nil
	default:
		return nil, nil, nil
	}
}

// issuePathBasename returns the last element of a file path written with either
// separator, so a Windows-scanned path doesn't leak its directories.
func issuePathBasename(p string) string {
	if i := strings.LastIndexAny(p, `/\`); i >= 0 {
		return p[i+1:]
	}
	return p
}

func issueTimePtr(ts pgtype.Timestamptz) *time.Time {
	if !ts.Valid {
		return nil
	}
	t := ts.Time
	return &t
}

func toIssueResponse(i gen.MediaIssue) IssueResponse {
	out := IssueResponse{
		ID:             i.ID.String(),
		ItemID:         i.ItemID.String(),
		Kind:           i.Kind,
		Note:           i.Note,
		Status:         i.Status,
		CreatedAt:      i.CreatedAt.Time,
		ResolvedAt:     issueTimePtr(i.ResolvedAt),
		ResolutionNote: i.ResolutionNote,
	}
	if i.FileID.Valid {
		s := uuid.UUID(i.FileID.Bytes).String()
		out.FileID = &s
	}
	return out
}

func toAdminIssueResponse(row gen.ListMediaIssuesAdminRow) AdminIssueResponse {
	base := toIssueResponse(gen.MediaIssue{
		ID: row.ID, ItemID: row.ItemID, FileID: row.FileID, UserID: row.UserID,
		Kind: row.Kind, Note: row.Note, Status: row.Status, CreatedAt: row.CreatedAt,
		ResolvedAt: row.ResolvedAt, ResolvedBy: row.ResolvedBy, ResolutionNote: row.ResolutionNote,
	})
	show, season, episode := issueShowContext(row.ItemType, row.ItemIndex, row.ParentTitle, row.ParentIndex, row.GrandparentTitle)
	out := AdminIssueResponse{
		IssueResponse:      base,
		LibraryID:          row.LibraryID.String(),
		ItemType:           row.ItemType,
		ItemTitle:          row.ItemTitle,
		ItemYear:           row.ItemYear,
		ShowTitle:          show,
		SeasonNumber:       season,
		EpisodeNumber:      episode,
		PosterPath:         row.PosterPath,
		ReporterID:         row.UserID.String(),
		ReporterUsername:   row.ReporterUsername,
		ResolvedByUsername: row.ResolvedByUsername,
	}
	if row.FilePath != nil {
		name := issuePathBasename(*row.FilePath)
		out.FileName = &name
	}
	return out
}
