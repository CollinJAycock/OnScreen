package v1

import (
	"context"
	"log/slog"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/api/middleware"
	"github.com/onscreen/onscreen/internal/auth"
	"github.com/onscreen/onscreen/internal/livetv"
)

// fakeScheduleDVR records what the schedule / recording handlers pass on.
type fakeScheduleDVR struct {
	LiveTVDVRService // anything else panics
	createErr        error
	created          []livetv.CreateScheduleParams
	listLimit        int32
	listOffset       int32
}

func (f *fakeScheduleDVR) CreateSchedule(_ context.Context, p livetv.CreateScheduleParams) (livetv.Schedule, error) {
	f.created = append(f.created, p)
	if f.createErr != nil {
		return livetv.Schedule{}, f.createErr
	}
	return livetv.Schedule{ID: uuid.New(), UserID: p.UserID, Type: p.Type}, nil
}

func (f *fakeScheduleDVR) ListRecordingsForUser(_ context.Context, _ uuid.UUID, _ string, limit, offset int32) ([]livetv.RecordingWithChannel, error) {
	f.listLimit, f.listOffset = limit, offset
	return nil, nil
}

func dvrReq(method, target, body string, admin bool) *http.Request {
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	return req.WithContext(middleware.WithClaims(req.Context(), &auth.Claims{UserID: uuid.New(), IsAdmin: admin}))
}

// Item 12: only admins may schedule on a disabled channel; everyone else gets
// the 404 a disabled channel answers on the stream routes.
func TestLiveTV_CreateSchedule_DisabledChannel(t *testing.T) {
	dvr := &fakeScheduleDVR{createErr: livetv.ErrChannelUnavailable}
	h := NewLiveTVHandler(newMockLiveTVService(), slog.Default()).WithDVR(dvr)

	rec := httptest.NewRecorder()
	h.CreateSchedule(rec, dvrReq("POST", "/api/v1/tv/schedules",
		`{"type":"series","channel_id":"`+uuid.NewString()+`","title_match":"News"}`, false))
	if rec.Code != http.StatusNotFound {
		t.Errorf("non-admin on a disabled channel: status %d, want 404", rec.Code)
	}
	if len(dvr.created) != 1 || dvr.created[0].AllowDisabledChannel {
		t.Errorf("non-admin request must not allow disabled channels: %+v", dvr.created)
	}

	dvr = &fakeScheduleDVR{}
	h = NewLiveTVHandler(newMockLiveTVService(), slog.Default()).WithDVR(dvr)
	rec = httptest.NewRecorder()
	h.CreateSchedule(rec, dvrReq("POST", "/api/v1/tv/schedules",
		`{"type":"once","program_id":"`+uuid.NewString()+`"}`, true))
	if rec.Code != http.StatusCreated || len(dvr.created) != 1 || !dvr.created[0].AllowDisabledChannel {
		t.Errorf("admin: status %d, params %+v (want AllowDisabledChannel)", rec.Code, dvr.created)
	}
}

// Item 12: ?offset past int32 is clamped instead of wrapping negative and
// failing the query with a 500.
func TestLiveTV_ListRecordings_OffsetClamped(t *testing.T) {
	dvr := &fakeScheduleDVR{}
	h := NewLiveTVHandler(newMockLiveTVService(), slog.Default()).WithDVR(dvr)

	rec := httptest.NewRecorder()
	h.ListRecordings(rec, dvrReq("GET", "/api/v1/tv/recordings?offset=2147483648&limit=10", "", false))
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	if dvr.listOffset != math.MaxInt32 || dvr.listLimit != 10 {
		t.Errorf("limit/offset = %d/%d, want 10/%d", dvr.listLimit, dvr.listOffset, math.MaxInt32)
	}

	rec = httptest.NewRecorder()
	h.ListRecordings(rec, dvrReq("GET", "/api/v1/tv/recordings?limit=9999", "", false))
	if dvr.listLimit != 500 || dvr.listOffset != 0 {
		t.Errorf("over-large limit: limit/offset = %d/%d, want 500/0", dvr.listLimit, dvr.listOffset)
	}
}
