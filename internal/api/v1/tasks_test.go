package v1

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onscreen/onscreen/internal/db/gen"
	"github.com/onscreen/onscreen/internal/scheduler"
)

// fakeTasksDB implements TasksQuerier for handler tests.
type fakeTasksDB struct {
	listOut []gen.ScheduledTask
	listErr error

	getOut   gen.ScheduledTask
	getErr   error
	getCalls []uuid.UUID

	createIn  gen.CreateScheduledTaskParams
	createOut gen.ScheduledTask
	createErr error

	updateIn  gen.UpdateScheduledTaskParams
	updateOut gen.ScheduledTask
	updateErr error

	deleteID  uuid.UUID
	deleteErr error

	setNextRunIn  gen.SetScheduledTaskNextRunParams
	setNextRunErr error

	runsIn  gen.ListTaskRunsParams
	runsOut []gen.TaskRun
	runsErr error
}

func (f *fakeTasksDB) ListScheduledTasks(context.Context) ([]gen.ScheduledTask, error) {
	return f.listOut, f.listErr
}
func (f *fakeTasksDB) GetScheduledTask(_ context.Context, id uuid.UUID) (gen.ScheduledTask, error) {
	f.getCalls = append(f.getCalls, id)
	return f.getOut, f.getErr
}
func (f *fakeTasksDB) CreateScheduledTask(_ context.Context, arg gen.CreateScheduledTaskParams) (gen.ScheduledTask, error) {
	f.createIn = arg
	return f.createOut, f.createErr
}
func (f *fakeTasksDB) UpdateScheduledTask(_ context.Context, arg gen.UpdateScheduledTaskParams) (gen.ScheduledTask, error) {
	f.updateIn = arg
	return f.updateOut, f.updateErr
}
func (f *fakeTasksDB) DeleteScheduledTask(_ context.Context, id uuid.UUID) error {
	f.deleteID = id
	return f.deleteErr
}
func (f *fakeTasksDB) SetScheduledTaskNextRun(_ context.Context, arg gen.SetScheduledTaskNextRunParams) error {
	f.setNextRunIn = arg
	return f.setNextRunErr
}
func (f *fakeTasksDB) ListTaskRuns(_ context.Context, arg gen.ListTaskRunsParams) ([]gen.TaskRun, error) {
	f.runsIn = arg
	return f.runsOut, f.runsErr
}

func tasksLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func newTasksTestHandler(db TasksQuerier, registerTypes ...string) *TasksHandler {
	reg := scheduler.NewRegistry()
	for _, tt := range registerTypes {
		reg.Register(tt, scheduler.HandlerFunc(func(context.Context, json.RawMessage) (string, error) {
			return "", nil
		}))
	}
	return NewTasksHandler(db, reg, tasksLogger())
}

func reqWithID(method, url, id string, body []byte) *http.Request {
	var r *http.Request
	if body != nil {
		r = httptest.NewRequest(method, url, bytes.NewReader(body))
	} else {
		r = httptest.NewRequest(method, url, nil)
	}
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", id)
	return r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, rctx))
}

func decode(t *testing.T, rec *httptest.ResponseRecorder, into any) {
	t.Helper()
	var env struct {
		Data json.RawMessage `json:"data"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&env); err != nil {
		t.Fatalf("decode envelope: %v (body=%s)", err, rec.Body.String())
	}
	if into != nil && len(env.Data) > 0 {
		if err := json.Unmarshal(env.Data, into); err != nil {
			t.Fatalf("decode data: %v (data=%s)", err, env.Data)
		}
	}
}

func TestTasksList(t *testing.T) {
	db := &fakeTasksDB{
		listOut: []gen.ScheduledTask{{
			ID:       uuid.New(),
			Name:     "nightly",
			TaskType: "backup_database",
			CronExpr: "0 3 * * *",
			Enabled:  true,
			Config:   []byte(`{"output_dir":"/tmp"}`),
		}},
	}
	h := newTasksTestHandler(db)

	rec := httptest.NewRecorder()
	h.List(rec, httptest.NewRequest(http.MethodGet, "/api/v1/admin/tasks", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status: %d body=%s", rec.Code, rec.Body.String())
	}
	var got []taskResponse
	decode(t, rec, &got)
	if len(got) != 1 || got[0].Name != "nightly" {
		t.Fatalf("got %+v", got)
	}
}

func TestTasksListTypes(t *testing.T) {
	h := newTasksTestHandler(&fakeTasksDB{}, "backup_database", "scan_library")
	rec := httptest.NewRecorder()
	h.ListTypes(rec, httptest.NewRequest(http.MethodGet, "/api/v1/admin/tasks/types", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status: %d", rec.Code)
	}
	var env struct {
		Data []string `json:"data"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&env); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(env.Data) != 2 {
		t.Fatalf("expected 2 types, got %v", env.Data)
	}
}

func TestTasksCreateSuccess(t *testing.T) {
	db := &fakeTasksDB{
		createOut: gen.ScheduledTask{
			ID:       uuid.New(),
			Name:     "n",
			TaskType: "scan_library",
			CronExpr: "*/5 * * * *",
			Enabled:  true,
		},
	}
	h := newTasksTestHandler(db, "scan_library")
	body := []byte(`{"name":"n","task_type":"scan_library","cron_expr":"*/5 * * * *","config":{"library_id":"all"}}`)
	rec := httptest.NewRecorder()
	h.Create(rec, httptest.NewRequest(http.MethodPost, "/api/v1/admin/tasks", bytes.NewReader(body)))

	if rec.Code != http.StatusCreated {
		t.Fatalf("status: %d body=%s", rec.Code, rec.Body.String())
	}
	if db.createIn.Name != "n" || db.createIn.TaskType != "scan_library" {
		t.Fatalf("create params: %+v", db.createIn)
	}
	if !db.createIn.NextRunAt.Valid {
		t.Fatal("expected NextRunAt to be set")
	}
	if string(db.createIn.Config) != `{"library_id":"all"}` {
		t.Fatalf("config: %s", db.createIn.Config)
	}
	if !db.createIn.Enabled {
		t.Fatal("expected enabled=true by default")
	}
}

func TestTasksCreateDefaultsConfigToEmptyObject(t *testing.T) {
	db := &fakeTasksDB{}
	h := newTasksTestHandler(db, "scan_library")
	body := []byte(`{"name":"n","task_type":"scan_library","cron_expr":"0 * * * *"}`)
	rec := httptest.NewRecorder()
	h.Create(rec, httptest.NewRequest(http.MethodPost, "/api/v1/admin/tasks", bytes.NewReader(body)))
	if rec.Code != http.StatusCreated {
		t.Fatalf("status: %d body=%s", rec.Code, rec.Body.String())
	}
	if string(db.createIn.Config) != `{}` {
		t.Fatalf("expected default empty object, got %s", db.createIn.Config)
	}
}

func TestTasksCreateValidations(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{"missing name", `{"task_type":"scan_library","cron_expr":"0 * * * *"}`},
		{"unknown type", `{"name":"n","task_type":"missing","cron_expr":"0 * * * *"}`},
		{"bad cron", `{"name":"n","task_type":"scan_library","cron_expr":"not cron"}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newTasksTestHandler(&fakeTasksDB{}, "scan_library")
			rec := httptest.NewRecorder()
			h.Create(rec, httptest.NewRequest(http.MethodPost, "/api/v1/admin/tasks",
				bytes.NewReader([]byte(tc.body))))
			if rec.Code < 400 || rec.Code >= 500 {
				t.Fatalf("expected 4xx, got %d body=%s", rec.Code, rec.Body.String())
			}
		})
	}
}

func TestTasksCreateInvalidJSON(t *testing.T) {
	h := newTasksTestHandler(&fakeTasksDB{}, "scan_library")
	rec := httptest.NewRecorder()
	h.Create(rec, httptest.NewRequest(http.MethodPost, "/api/v1/admin/tasks",
		bytes.NewReader([]byte(`{not json`))))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
}

func TestTasksUpdatePartial(t *testing.T) {
	id := uuid.New()
	existing := gen.ScheduledTask{
		ID:       id,
		Name:     "old",
		TaskType: "scan_library",
		Config:   []byte(`{}`),
		CronExpr: "0 0 * * *",
		Enabled:  true,
	}
	db := &fakeTasksDB{getOut: existing, updateOut: existing}
	h := newTasksTestHandler(db, "scan_library")

	body := []byte(`{"enabled":false}`)
	rec := httptest.NewRecorder()
	h.Update(rec, reqWithID(http.MethodPatch, "/api/v1/admin/tasks/"+id.String(), id.String(), body))

	if rec.Code != http.StatusOK {
		t.Fatalf("status: %d body=%s", rec.Code, rec.Body.String())
	}
	if db.updateIn.Enabled {
		t.Fatal("expected enabled=false")
	}
	// Other fields preserved.
	if db.updateIn.Name != "old" || db.updateIn.TaskType != "scan_library" || db.updateIn.CronExpr != "0 0 * * *" {
		t.Fatalf("preserved fields lost: %+v", db.updateIn)
	}
}

func TestTasksUpdateChangesCronAdvancesNextRun(t *testing.T) {
	id := uuid.New()
	existing := gen.ScheduledTask{
		ID:        id,
		Name:      "x",
		TaskType:  "scan_library",
		Config:    []byte(`{}`),
		CronExpr:  "0 0 * * *",
		Enabled:   true,
		NextRunAt: pgtype.Timestamptz{Valid: true},
	}
	db := &fakeTasksDB{getOut: existing, updateOut: existing}
	h := newTasksTestHandler(db, "scan_library")

	body := []byte(`{"cron_expr":"*/15 * * * *"}`)
	rec := httptest.NewRecorder()
	h.Update(rec, reqWithID(http.MethodPatch, "/", id.String(), body))
	if rec.Code != http.StatusOK {
		t.Fatalf("status: %d body=%s", rec.Code, rec.Body.String())
	}
	if db.updateIn.CronExpr != "*/15 * * * *" {
		t.Fatalf("cron not updated: %q", db.updateIn.CronExpr)
	}
	if !db.updateIn.NextRunAt.Valid {
		t.Fatal("NextRunAt should be set after cron change")
	}
}

func TestTasksUpdateNotFound(t *testing.T) {
	id := uuid.New()
	db := &fakeTasksDB{getErr: pgx.ErrNoRows}
	h := newTasksTestHandler(db, "scan_library")
	rec := httptest.NewRecorder()
	h.Update(rec, reqWithID(http.MethodPatch, "/", id.String(), []byte(`{}`)))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rec.Code)
	}
}

func TestTasksUpdateBadID(t *testing.T) {
	h := newTasksTestHandler(&fakeTasksDB{})
	rec := httptest.NewRecorder()
	h.Update(rec, reqWithID(http.MethodPatch, "/", "not-a-uuid", []byte(`{}`)))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
}

func TestTasksUpdateValidations(t *testing.T) {
	id := uuid.New()
	existing := gen.ScheduledTask{ID: id, Name: "n", TaskType: "scan_library", Config: []byte(`{}`), CronExpr: "0 * * * *", Enabled: true}
	cases := []struct {
		name string
		body string
	}{
		{"empty name", `{"name":""}`},
		{"unknown type", `{"task_type":"missing"}`},
		{"bad cron", `{"cron_expr":"not cron"}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			db := &fakeTasksDB{getOut: existing, updateOut: existing}
			h := newTasksTestHandler(db, "scan_library")
			rec := httptest.NewRecorder()
			h.Update(rec, reqWithID(http.MethodPatch, "/", id.String(), []byte(tc.body)))
			if rec.Code < 400 || rec.Code >= 500 {
				t.Fatalf("expected 4xx, got %d body=%s", rec.Code, rec.Body.String())
			}
		})
	}
}

func TestTasksDelete(t *testing.T) {
	id := uuid.New()
	db := &fakeTasksDB{}
	h := newTasksTestHandler(db)
	rec := httptest.NewRecorder()
	h.Delete(rec, reqWithID(http.MethodDelete, "/", id.String(), nil))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status: %d body=%s", rec.Code, rec.Body.String())
	}
	if db.deleteID != id {
		t.Fatalf("delete id: %v", db.deleteID)
	}
}

func TestTasksDeleteBadID(t *testing.T) {
	h := newTasksTestHandler(&fakeTasksDB{})
	rec := httptest.NewRecorder()
	h.Delete(rec, reqWithID(http.MethodDelete, "/", "nope", nil))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
}

func TestTasksRunNowBumpsNextRun(t *testing.T) {
	id := uuid.New()
	db := &fakeTasksDB{getOut: gen.ScheduledTask{ID: id}}
	h := newTasksTestHandler(db)
	rec := httptest.NewRecorder()
	h.RunNow(rec, reqWithID(http.MethodPost, "/", id.String(), nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status: %d body=%s", rec.Code, rec.Body.String())
	}
	if db.setNextRunIn.ID != id || !db.setNextRunIn.NextRunAt.Valid {
		t.Fatalf("set next run: %+v", db.setNextRunIn)
	}
}

func TestTasksRunNowNotFound(t *testing.T) {
	db := &fakeTasksDB{getErr: pgx.ErrNoRows}
	h := newTasksTestHandler(db)
	rec := httptest.NewRecorder()
	h.RunNow(rec, reqWithID(http.MethodPost, "/", uuid.New().String(), nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rec.Code)
	}
}

func TestTasksRuns(t *testing.T) {
	id := uuid.New()
	db := &fakeTasksDB{
		runsOut: []gen.TaskRun{{
			ID:     uuid.New(),
			TaskID: id,
			Status: "success",
			Output: "ok",
		}},
	}
	h := newTasksTestHandler(db)
	rec := httptest.NewRecorder()
	h.Runs(rec, reqWithID(http.MethodGet, "/?limit=10", id.String(), nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status: %d body=%s", rec.Code, rec.Body.String())
	}
	if db.runsIn.TaskID != id || db.runsIn.Limit != 10 {
		t.Fatalf("runs params: %+v", db.runsIn)
	}
	var got []runResponse
	decode(t, rec, &got)
	if len(got) != 1 || got[0].Status != "success" {
		t.Fatalf("got %+v", got)
	}
}

func TestTasksRunsLimitClampedAndDefault(t *testing.T) {
	id := uuid.New()

	// Default 50.
	db := &fakeTasksDB{}
	h := newTasksTestHandler(db)
	rec := httptest.NewRecorder()
	h.Runs(rec, reqWithID(http.MethodGet, "/", id.String(), nil))
	if db.runsIn.Limit != 50 {
		t.Fatalf("expected default 50, got %d", db.runsIn.Limit)
	}

	// Clamp to 500.
	rec = httptest.NewRecorder()
	h.Runs(rec, reqWithID(http.MethodGet, "/?limit=10000", id.String(), nil))
	if db.runsIn.Limit != 500 {
		t.Fatalf("expected clamp to 500, got %d", db.runsIn.Limit)
	}

	// Garbage falls back to default.
	rec = httptest.NewRecorder()
	h.Runs(rec, reqWithID(http.MethodGet, "/?limit=abc", id.String(), nil))
	if db.runsIn.Limit != 50 {
		t.Fatalf("expected fallback 50, got %d", db.runsIn.Limit)
	}
}

// ── DB error paths — every handler must return 500 (never leak the error
// string) when the underlying query fails. The real prod issue is caught
// by logs, not the response.
// ────────────────────────────────────────────────────────────────────────

func TestTasksListDBError(t *testing.T) {
	db := &fakeTasksDB{listErr: errors.New("db down")}
	h := newTasksTestHandler(db)
	rec := httptest.NewRecorder()
	h.List(rec, httptest.NewRequest(http.MethodGet, "/api/v1/admin/tasks", nil))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", rec.Code)
	}
}

func TestTasksCreateDBError(t *testing.T) {
	db := &fakeTasksDB{createErr: errors.New("db down")}
	h := newTasksTestHandler(db, "scan_library")
	body := []byte(`{"name":"n","task_type":"scan_library","cron_expr":"0 * * * *"}`)
	rec := httptest.NewRecorder()
	h.Create(rec, httptest.NewRequest(http.MethodPost, "/api/v1/admin/tasks", bytes.NewReader(body)))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestTasksUpdateBadJSON(t *testing.T) {
	h := newTasksTestHandler(&fakeTasksDB{})
	id := uuid.New()
	rec := httptest.NewRecorder()
	h.Update(rec, reqWithID(http.MethodPatch, "/", id.String(), []byte(`{not json`)))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
}

func TestTasksUpdateGetGenericError(t *testing.T) {
	db := &fakeTasksDB{getErr: errors.New("db down")}
	h := newTasksTestHandler(db)
	rec := httptest.NewRecorder()
	h.Update(rec, reqWithID(http.MethodPatch, "/", uuid.New().String(), []byte(`{}`)))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", rec.Code)
	}
}

func TestTasksUpdateDBError(t *testing.T) {
	id := uuid.New()
	existing := gen.ScheduledTask{ID: id, Name: "n", TaskType: "scan_library", Config: []byte(`{}`), CronExpr: "0 * * * *", Enabled: true}
	db := &fakeTasksDB{getOut: existing, updateErr: errors.New("db down")}
	h := newTasksTestHandler(db, "scan_library")
	rec := httptest.NewRecorder()
	h.Update(rec, reqWithID(http.MethodPatch, "/", id.String(), []byte(`{"enabled":false}`)))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", rec.Code)
	}
}

func TestTasksDeleteDBError(t *testing.T) {
	db := &fakeTasksDB{deleteErr: errors.New("db down")}
	h := newTasksTestHandler(db)
	rec := httptest.NewRecorder()
	h.Delete(rec, reqWithID(http.MethodDelete, "/", uuid.New().String(), nil))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", rec.Code)
	}
}

func TestTasksRunNowBadID(t *testing.T) {
	h := newTasksTestHandler(&fakeTasksDB{})
	rec := httptest.NewRecorder()
	h.RunNow(rec, reqWithID(http.MethodPost, "/", "not-a-uuid", nil))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
}

func TestTasksRunNowGetGenericError(t *testing.T) {
	db := &fakeTasksDB{getErr: errors.New("db down")}
	h := newTasksTestHandler(db)
	rec := httptest.NewRecorder()
	h.RunNow(rec, reqWithID(http.MethodPost, "/", uuid.New().String(), nil))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", rec.Code)
	}
}

func TestTasksRunNowSetNextRunError(t *testing.T) {
	db := &fakeTasksDB{setNextRunErr: errors.New("db down")}
	h := newTasksTestHandler(db)
	rec := httptest.NewRecorder()
	h.RunNow(rec, reqWithID(http.MethodPost, "/", uuid.New().String(), nil))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", rec.Code)
	}
}

func TestTasksRunsBadID(t *testing.T) {
	h := newTasksTestHandler(&fakeTasksDB{})
	rec := httptest.NewRecorder()
	h.Runs(rec, reqWithID(http.MethodGet, "/", "not-a-uuid", nil))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
}

func TestTasksRunsDBError(t *testing.T) {
	db := &fakeTasksDB{runsErr: errors.New("db down")}
	h := newTasksTestHandler(db)
	rec := httptest.NewRecorder()
	h.Runs(rec, reqWithID(http.MethodGet, "/", uuid.New().String(), nil))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", rec.Code)
	}
}

// ── Response serialization ──────────────────────────────────────────────

func TestTasksResponseSerializesTimestampsAndNullables(t *testing.T) {
	id := uuid.New()
	lastRun := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	nextRun := time.Date(2026, 3, 2, 12, 0, 0, 0, time.UTC)
	created := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)
	updated := time.Date(2026, 2, 15, 0, 0, 0, 0, time.UTC)
	db := &fakeTasksDB{
		listOut: []gen.ScheduledTask{{
			ID:         id,
			Name:       "n",
			TaskType:   "t",
			Config:     []byte(`{"k":1}`),
			CronExpr:   "0 * * * *",
			Enabled:    true,
			LastRunAt:  pgtype.Timestamptz{Time: lastRun, Valid: true},
			NextRunAt:  pgtype.Timestamptz{Time: nextRun, Valid: true},
			LastStatus: "success",
			LastError:  "",
			CreatedAt:  pgtype.Timestamptz{Time: created, Valid: true},
			UpdatedAt:  pgtype.Timestamptz{Time: updated, Valid: true},
		}},
	}
	h := newTasksTestHandler(db)
	rec := httptest.NewRecorder()
	h.List(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	var got []taskResponse
	decode(t, rec, &got)
	if got[0].LastRunAt == nil || !got[0].LastRunAt.Equal(lastRun) {
		t.Fatalf("last_run_at: %v", got[0].LastRunAt)
	}
	if !got[0].NextRunAt.Equal(nextRun) {
		t.Fatalf("next_run_at: %v", got[0].NextRunAt)
	}
	if string(got[0].Config) != `{"k":1}` {
		t.Fatalf("config roundtrip: %s", got[0].Config)
	}
}

func TestTasksResponseHandlesNullLastRunAndEmptyConfig(t *testing.T) {
	// A fresh task: LastRunAt not valid, Config is empty bytes.
	db := &fakeTasksDB{
		listOut: []gen.ScheduledTask{{
			ID:        uuid.New(),
			Name:      "new",
			TaskType:  "t",
			Config:    nil,
			CronExpr:  "0 * * * *",
			LastRunAt: pgtype.Timestamptz{Valid: false},
		}},
	}
	h := newTasksTestHandler(db)
	rec := httptest.NewRecorder()
	h.List(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	var got []taskResponse
	decode(t, rec, &got)
	if got[0].LastRunAt != nil {
		t.Fatalf("expected nil last_run_at, got %v", got[0].LastRunAt)
	}
	if string(got[0].Config) != `{}` {
		t.Fatalf("expected config to default to {}, got %s", got[0].Config)
	}
}

func TestTasksRunResponseSerialization(t *testing.T) {
	id := uuid.New()
	started := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	ended := time.Date(2026, 3, 1, 12, 5, 0, 0, time.UTC)

	db := &fakeTasksDB{
		runsOut: []gen.TaskRun{
			{ // completed
				ID:        uuid.New(),
				TaskID:    id,
				Status:    "success",
				Output:    "done",
				StartedAt: pgtype.Timestamptz{Time: started, Valid: true},
				EndedAt:   pgtype.Timestamptz{Time: ended, Valid: true},
			},
			{ // in-flight (EndedAt not yet set)
				ID:        uuid.New(),
				TaskID:    id,
				Status:    "running",
				StartedAt: pgtype.Timestamptz{Time: started, Valid: true},
				EndedAt:   pgtype.Timestamptz{Valid: false},
			},
		},
	}
	h := newTasksTestHandler(db)
	rec := httptest.NewRecorder()
	h.Runs(rec, reqWithID(http.MethodGet, "/", id.String(), nil))

	var got []runResponse
	decode(t, rec, &got)
	if got[0].EndedAt == nil || !got[0].EndedAt.Equal(ended) {
		t.Fatalf("completed run missing ended_at: %+v", got[0])
	}
	if got[1].EndedAt != nil {
		t.Fatalf("in-flight run should have null ended_at: %+v", got[1])
	}
}

// ── Create edge cases ───────────────────────────────────────────────────

func TestTasksCreateExplicitEnabledFalse(t *testing.T) {
	db := &fakeTasksDB{}
	h := newTasksTestHandler(db, "scan_library")
	body := []byte(`{"name":"n","task_type":"scan_library","cron_expr":"0 * * * *","enabled":false}`)
	rec := httptest.NewRecorder()
	h.Create(rec, httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(body)))
	if rec.Code != http.StatusCreated {
		t.Fatalf("status: %d body=%s", rec.Code, rec.Body.String())
	}
	if db.createIn.Enabled {
		t.Fatal("expected enabled=false when explicitly set")
	}
}

func TestTasksCreateComplexConfigRoundtrip(t *testing.T) {
	db := &fakeTasksDB{}
	h := newTasksTestHandler(db, "backup_database")
	cfgJSON := `{"output_dir":"/var/backups","retain_count":7,"nested":{"a":[1,2,3]}}`
	body := []byte(`{"name":"n","task_type":"backup_database","cron_expr":"0 3 * * *","config":` + cfgJSON + `}`)
	rec := httptest.NewRecorder()
	h.Create(rec, httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(body)))
	if rec.Code != http.StatusCreated {
		t.Fatalf("status: %d body=%s", rec.Code, rec.Body.String())
	}
	// Bytes must be preserved verbatim — no re-encoding that would reorder keys.
	if string(db.createIn.Config) != cfgJSON {
		t.Fatalf("config roundtrip: got %s want %s", db.createIn.Config, cfgJSON)
	}
}

// ── Update edge cases ───────────────────────────────────────────────────

func TestTasksUpdateEmptyBodyPreservesEverything(t *testing.T) {
	id := uuid.New()
	existing := gen.ScheduledTask{
		ID:       id,
		Name:     "keep",
		TaskType: "scan_library",
		Config:   []byte(`{"library_id":"all"}`),
		CronExpr: "0 0 * * *",
		Enabled:  true,
	}
	db := &fakeTasksDB{getOut: existing, updateOut: existing}
	h := newTasksTestHandler(db, "scan_library")
	rec := httptest.NewRecorder()
	h.Update(rec, reqWithID(http.MethodPatch, "/", id.String(), []byte(`{}`)))
	if rec.Code != http.StatusOK {
		t.Fatalf("status: %d body=%s", rec.Code, rec.Body.String())
	}
	if db.updateIn.Name != "keep" || db.updateIn.TaskType != "scan_library" ||
		string(db.updateIn.Config) != `{"library_id":"all"}` ||
		db.updateIn.CronExpr != "0 0 * * *" || !db.updateIn.Enabled {
		t.Fatalf("empty body should preserve fields, got %+v", db.updateIn)
	}
}
