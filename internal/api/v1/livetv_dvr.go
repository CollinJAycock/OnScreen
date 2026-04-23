package v1

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/api/middleware"
	"github.com/onscreen/onscreen/internal/api/respond"
	"github.com/onscreen/onscreen/internal/livetv"
)

// LiveTVDVRService is the slice of DVRService the handler needs. Stubbed
// via interface so tests don't spin up the real worker.
type LiveTVDVRService interface {
	CreateSchedule(ctx context.Context, p livetv.CreateScheduleParams) (livetv.Schedule, error)
	GetSchedule(ctx context.Context, id uuid.UUID) (livetv.Schedule, error)
	ListSchedulesForUser(ctx context.Context, userID uuid.UUID) ([]livetv.Schedule, error)
	DeleteSchedule(ctx context.Context, id uuid.UUID) error
	SetScheduleEnabled(ctx context.Context, id uuid.UUID, enabled bool) error
	GetRecording(ctx context.Context, id uuid.UUID) (livetv.Recording, error)
	ListRecordingsForUser(ctx context.Context, userID uuid.UUID, status string, limit, offset int32) ([]livetv.RecordingWithChannel, error)
	CancelRecording(ctx context.Context, id uuid.UUID) error
}

// WithDVR attaches the DVR service. When nil (DVR not configured),
// DVR endpoints return 503 just like the main live TV endpoints.
func (h *LiveTVHandler) WithDVR(d LiveTVDVRService) *LiveTVHandler {
	h.dvr = d
	return h
}

// ── Response shapes ──────────────────────────────────────────────────────────

// ScheduleResponse is one user-defined recording rule.
type ScheduleResponse struct {
	ID             uuid.UUID  `json:"id"`
	Type           string     `json:"type"`
	ProgramID      *uuid.UUID `json:"program_id,omitempty"`
	ChannelID      *uuid.UUID `json:"channel_id,omitempty"`
	TitleMatch     *string    `json:"title_match,omitempty"`
	NewOnly        bool       `json:"new_only"`
	TimeStart      *string    `json:"time_start,omitempty"`
	TimeEnd        *string    `json:"time_end,omitempty"`
	PaddingPreSec  int32      `json:"padding_pre_sec"`
	PaddingPostSec int32      `json:"padding_post_sec"`
	Priority       int32      `json:"priority"`
	RetentionDays  *int32     `json:"retention_days,omitempty"`
	Enabled        bool       `json:"enabled"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
}

// RecordingResponse is one recording in the UI list. Channel metadata
// is denormalized so the client can render logo + name without a
// second lookup.
type RecordingResponse struct {
	ID            uuid.UUID  `json:"id"`
	ScheduleID    *uuid.UUID `json:"schedule_id,omitempty"`
	ChannelID     uuid.UUID  `json:"channel_id"`
	ChannelNumber string     `json:"channel_number"`
	ChannelName   string     `json:"channel_name"`
	ChannelLogo   *string    `json:"channel_logo,omitempty"`
	ProgramID     *uuid.UUID `json:"program_id,omitempty"`
	Title         string     `json:"title"`
	Subtitle      *string    `json:"subtitle,omitempty"`
	SeasonNum     *int32     `json:"season_num,omitempty"`
	EpisodeNum    *int32     `json:"episode_num,omitempty"`
	Status        string     `json:"status"`
	StartsAt      time.Time  `json:"starts_at"`
	EndsAt        time.Time  `json:"ends_at"`
	ItemID        *uuid.UUID `json:"item_id,omitempty"`
	Error         *string    `json:"error,omitempty"`
}

// ── Schedules ────────────────────────────────────────────────────────────────

// createScheduleRequest is the POST body for /api/v1/tv/schedules.
type createScheduleRequest struct {
	Type           string     `json:"type"`
	ProgramID      *uuid.UUID `json:"program_id,omitempty"`
	ChannelID      *uuid.UUID `json:"channel_id,omitempty"`
	TitleMatch     *string    `json:"title_match,omitempty"`
	NewOnly        bool       `json:"new_only,omitempty"`
	TimeStart      *string    `json:"time_start,omitempty"`
	TimeEnd        *string    `json:"time_end,omitempty"`
	PaddingPreSec  int32      `json:"padding_pre_sec,omitempty"`
	PaddingPostSec int32      `json:"padding_post_sec,omitempty"`
	Priority       int32      `json:"priority,omitempty"`
	RetentionDays  *int32     `json:"retention_days,omitempty"`
}

// CreateSchedule handles POST /api/v1/tv/schedules. The schedule is
// bound to the calling user.
func (h *LiveTVHandler) CreateSchedule(w http.ResponseWriter, r *http.Request) {
	if !h.dvrAvailable(w, r) {
		return
	}
	claims := middleware.ClaimsFromContext(r.Context())
	if claims == nil {
		respond.Forbidden(w, r)
		return
	}
	var body createScheduleRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		respond.BadRequest(w, r, "invalid JSON body")
		return
	}
	sched, err := h.dvr.CreateSchedule(r.Context(), livetv.CreateScheduleParams{
		UserID:         claims.UserID,
		Type:           livetv.ScheduleType(body.Type),
		ProgramID:      body.ProgramID,
		ChannelID:      body.ChannelID,
		TitleMatch:     body.TitleMatch,
		NewOnly:        body.NewOnly,
		TimeStart:      body.TimeStart,
		TimeEnd:        body.TimeEnd,
		PaddingPreSec:  body.PaddingPreSec,
		PaddingPostSec: body.PaddingPostSec,
		Priority:       body.Priority,
		RetentionDays:  body.RetentionDays,
	})
	if err != nil {
		respond.BadRequest(w, r, err.Error())
		return
	}
	respond.Created(w, r, scheduleToResponse(sched))
}

// ListSchedules handles GET /api/v1/tv/schedules.
func (h *LiveTVHandler) ListSchedules(w http.ResponseWriter, r *http.Request) {
	if !h.dvrAvailable(w, r) {
		return
	}
	claims := middleware.ClaimsFromContext(r.Context())
	if claims == nil {
		respond.Forbidden(w, r)
		return
	}
	rows, err := h.dvr.ListSchedulesForUser(r.Context(), claims.UserID)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "list schedules", "err", err)
		respond.InternalError(w, r)
		return
	}
	out := make([]ScheduleResponse, len(rows))
	for i, s := range rows {
		out[i] = scheduleToResponse(s)
	}
	respond.List(w, r, out, int64(len(out)), "")
}

// DeleteSchedule handles DELETE /api/v1/tv/schedules/{id}. Owner-only —
// foreign-owned rows return 404 to avoid leaking existence (the UUID
// might still be guessed but the attacker learns nothing from the
// response).
func (h *LiveTVHandler) DeleteSchedule(w http.ResponseWriter, r *http.Request) {
	if !h.dvrAvailable(w, r) {
		return
	}
	claims := middleware.ClaimsFromContext(r.Context())
	if claims == nil {
		respond.Forbidden(w, r)
		return
	}
	id, err := parseUUID(r, "id")
	if err != nil {
		respond.BadRequest(w, r, "invalid schedule id")
		return
	}
	row, err := h.dvr.GetSchedule(r.Context(), id)
	if err != nil || row.UserID != claims.UserID {
		// Obfuscate: a wrong-owner delete is indistinguishable from a
		// non-existent row. Admins can still delete their own; we don't
		// grant admins a global delete bypass here since schedules are
		// personal — if an admin needs to clear someone else's, they can
		// log in as that user or touch the DB directly.
		respond.NotFound(w, r)
		return
	}
	if err := h.dvr.DeleteSchedule(r.Context(), id); err != nil {
		h.logger.ErrorContext(r.Context(), "delete schedule", "id", id, "err", err)
		respond.InternalError(w, r)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ── Recordings ───────────────────────────────────────────────────────────────

// ListRecordings handles GET /api/v1/tv/recordings?status=&limit=&offset=.
func (h *LiveTVHandler) ListRecordings(w http.ResponseWriter, r *http.Request) {
	if !h.dvrAvailable(w, r) {
		return
	}
	claims := middleware.ClaimsFromContext(r.Context())
	if claims == nil {
		respond.Forbidden(w, r)
		return
	}
	q := r.URL.Query()
	limit := int32(100)
	if s := q.Get("limit"); s != "" {
		if n, err := strconv.Atoi(s); err == nil && n > 0 && n <= 500 {
			limit = int32(n)
		}
	}
	var offset int32
	if s := q.Get("offset"); s != "" {
		if n, err := strconv.Atoi(s); err == nil && n >= 0 {
			offset = int32(n)
		}
	}
	status := q.Get("status")
	rows, err := h.dvr.ListRecordingsForUser(r.Context(), claims.UserID, status, limit, offset)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "list recordings", "err", err)
		respond.InternalError(w, r)
		return
	}
	out := make([]RecordingResponse, len(rows))
	for i, rc := range rows {
		out[i] = recordingToResponse(rc)
	}
	respond.List(w, r, out, int64(len(out)), "")
}

// CancelRecording handles DELETE /api/v1/tv/recordings/{id}. Transitions
// the row to status='cancelled' — the worker sees the flip on its next
// tick and stops capturing. The on-disk file (if any) stays put; user
// can delete it via the media-item path later.
//
// Owner-only: 404 on foreign rows to obfuscate existence.
func (h *LiveTVHandler) CancelRecording(w http.ResponseWriter, r *http.Request) {
	if !h.dvrAvailable(w, r) {
		return
	}
	claims := middleware.ClaimsFromContext(r.Context())
	if claims == nil {
		respond.Forbidden(w, r)
		return
	}
	id, err := parseUUID(r, "id")
	if err != nil {
		respond.BadRequest(w, r, "invalid recording id")
		return
	}
	row, err := h.dvr.GetRecording(r.Context(), id)
	if err != nil || row.UserID != claims.UserID {
		respond.NotFound(w, r)
		return
	}
	if err := h.dvr.CancelRecording(r.Context(), id); err != nil {
		h.logger.ErrorContext(r.Context(), "cancel recording", "id", id, "err", err)
		respond.InternalError(w, r)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ── helpers ──────────────────────────────────────────────────────────────────

func (h *LiveTVHandler) dvrAvailable(w http.ResponseWriter, r *http.Request) bool {
	if h.dvr == nil {
		respond.Error(w, r, http.StatusServiceUnavailable,
			"DVR_NOT_CONFIGURED", "DVR is not enabled on this server")
		return false
	}
	return true
}

func scheduleToResponse(s livetv.Schedule) ScheduleResponse {
	return ScheduleResponse{
		ID: s.ID, Type: string(s.Type),
		ProgramID: s.ProgramID, ChannelID: s.ChannelID,
		TitleMatch: s.TitleMatch, NewOnly: s.NewOnly,
		TimeStart: s.TimeStart, TimeEnd: s.TimeEnd,
		PaddingPreSec: s.PaddingPreSec, PaddingPostSec: s.PaddingPostSec,
		Priority: s.Priority, RetentionDays: s.RetentionDays,
		Enabled: s.Enabled,
		CreatedAt: s.CreatedAt, UpdatedAt: s.UpdatedAt,
	}
}

func recordingToResponse(r livetv.RecordingWithChannel) RecordingResponse {
	return RecordingResponse{
		ID: r.ID, ScheduleID: r.ScheduleID,
		ChannelID: r.ChannelID, ChannelNumber: r.ChannelNumber,
		ChannelName: r.ChannelName, ChannelLogo: r.ChannelLogo,
		ProgramID: r.ProgramID, Title: r.Title, Subtitle: r.Subtitle,
		SeasonNum: r.SeasonNum, EpisodeNum: r.EpisodeNum,
		Status: string(r.Status),
		StartsAt: r.StartsAt, EndsAt: r.EndsAt,
		ItemID: r.ItemID, Error: r.Error,
	}
}
