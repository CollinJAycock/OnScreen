package v1

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/api/middleware"
	"github.com/onscreen/onscreen/internal/auth"
	"github.com/onscreen/onscreen/internal/domain/media"
	"github.com/onscreen/onscreen/internal/domain/settings"
	"github.com/onscreen/onscreen/internal/livetv"
)

// ── DVR settings ─────────────────────────────────────────────────────────────

type fakeDVRSettings struct{ cfg settings.DVRConfig }

func (f *fakeDVRSettings) DVR(context.Context) settings.DVRConfig { return f.cfg.WithDefaults() }
func (f *fakeDVRSettings) SetDVR(_ context.Context, c settings.DVRConfig) error {
	c = c.WithDefaults()
	if err := c.Validate(); err != nil {
		return err
	}
	f.cfg = c
	return nil
}

func TestSettingsDVR_GetDefaultsAndEnvReadOnly(t *testing.T) {
	store := &fakeDVRSettings{}
	h := NewSettingsHandler(nil, slog.Default()).SetDVRSettings(store, DVREnvInfo{
		ScriptPath: "/opt/onscreen/post.sh",
	})
	rec := httptest.NewRecorder()
	h.GetDVR(rec, httptest.NewRequest("GET", "/api/v1/settings/dvr", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	var body struct{ Data dvrSettingsDTO }
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	d := body.Data
	if d.Remux || d.Transcode || d.DeleteOriginal || d.Commercials != "off" || d.RemuxContainer != "mkv" {
		t.Errorf("defaults = %+v, want everything off, MKV", d)
	}
	if !d.ScriptConfigured || d.ScriptPath != "/opt/onscreen/post.sh" || d.ComskipConfigured || d.CommercialDetector != "builtin" {
		t.Errorf("env info = %+v", d)
	}
}

func TestSettingsDVR_PutValidatesAndIgnoresExecutablePaths(t *testing.T) {
	store := &fakeDVRSettings{}
	h := NewSettingsHandler(nil, slog.Default()).SetDVRSettings(store, DVREnvInfo{})

	put := func(body string) int {
		rec := httptest.NewRecorder()
		h.UpdateDVR(rec, httptest.NewRequest("PUT", "/api/v1/settings/dvr", strings.NewReader(body)))
		return rec.Code
	}
	if code := put(`{"commercials":"cut"}`); code != http.StatusBadRequest {
		t.Errorf("cut without remux: %d, want 400", code)
	}
	if code := put(`{"remux":true,"remux_container":"avi"}`); code != http.StatusBadRequest {
		t.Errorf("bad container: %d, want 400", code)
	}
	// script_path is read-only: sending it must not configure anything.
	if code := put(`{"remux":true,"commercials":"cut","script_path":"/tmp/evil.sh"}`); code != http.StatusNoContent {
		t.Fatalf("valid put: %d", code)
	}
	if !store.cfg.Remux || store.cfg.Commercials != "cut" {
		t.Errorf("saved = %+v", store.cfg)
	}
	rec := httptest.NewRecorder()
	h.GetDVR(rec, httptest.NewRequest("GET", "/api/v1/settings/dvr", nil))
	if strings.Contains(rec.Body.String(), "evil") {
		t.Error("a script path sent over the API was stored")
	}
}

// ── recording post-process actions ───────────────────────────────────────────

type fakeDVRForPost struct {
	LiveTVDVRService // only GetRecording is used
	recs             map[uuid.UUID]livetv.Recording
}

func (f *fakeDVRForPost) GetRecording(_ context.Context, id uuid.UUID) (livetv.Recording, error) {
	r, ok := f.recs[id]
	if !ok {
		return livetv.Recording{}, errors.New("not found")
	}
	return r, nil
}

type fakePostProcessor struct {
	err   error
	calls []livetv.PostProcessJobKind
}

func (f *fakePostProcessor) Enqueue(_ context.Context, _ uuid.UUID, kind livetv.PostProcessJobKind) error {
	f.calls = append(f.calls, kind)
	return f.err
}

func TestLiveTV_DetectCommercials(t *testing.T) {
	id := uuid.New()
	dvr := &fakeDVRForPost{recs: map[uuid.UUID]livetv.Recording{id: {ID: id}}}
	pp := &fakePostProcessor{}
	h := NewLiveTVHandler(newMockLiveTVService(), slog.Default())
	h.WithDVR(dvr).WithPostProcess(pp)

	call := func(recID string, handler func(http.ResponseWriter, *http.Request)) int {
		req := httptest.NewRequest("POST", "/api/v1/tv/recordings/"+recID+"/detect-commercials", nil)
		req = withChiParam(req, "id", recID)
		req = req.WithContext(middleware.WithClaims(req.Context(), &auth.Claims{UserID: uuid.New(), IsAdmin: true}))
		rec := httptest.NewRecorder()
		handler(rec, req)
		return rec.Code
	}

	if code := call(id.String(), h.DetectCommercials); code != http.StatusAccepted {
		t.Fatalf("detect: %d, want 202", code)
	}
	if len(pp.calls) != 1 || pp.calls[0] != livetv.PostProcessDetect {
		t.Errorf("enqueue calls = %v", pp.calls)
	}
	if code := call(id.String(), h.ReprocessRecording); code != http.StatusAccepted || pp.calls[1] != livetv.PostProcessFull {
		t.Errorf("reprocess: %d, calls %v", code, pp.calls)
	}
	if code := call(uuid.NewString(), h.DetectCommercials); code != http.StatusNotFound {
		t.Errorf("unknown recording: %d, want 404", code)
	}
	pp.err = livetv.ErrPostProcessBusy
	if code := call(id.String(), h.DetectCommercials); code != http.StatusConflict {
		t.Errorf("busy: %d, want 409", code)
	}
	pp.err = livetv.ErrPostProcessIneligible
	if code := call(id.String(), h.DetectCommercials); code != http.StatusBadRequest {
		t.Errorf("ineligible: %d, want 400", code)
	}

	// Without a post-processor the endpoint is unavailable, not a panic.
	h2 := NewLiveTVHandler(newMockLiveTVService(), slog.Default())
	h2.WithDVR(dvr)
	if code := call(id.String(), h2.DetectCommercials); code != http.StatusServiceUnavailable {
		t.Errorf("unwired: %d, want 503", code)
	}
}

// ── commercials on the item detail ───────────────────────────────────────────

type fakeCommercials map[uuid.UUID][]CommercialMarkerJSON

func (f fakeCommercials) ListCommercialsByItem(context.Context, uuid.UUID) (map[uuid.UUID][]CommercialMarkerJSON, error) {
	return f, nil
}

func TestItemGet_FileCommercials(t *testing.T) {
	id, withAds, plain := uuid.New(), uuid.New(), uuid.New()
	ms := &mockItemMedia{
		item: &media.Item{ID: id, Title: "Evening News", Type: "movie"},
		files: []media.File{
			{ID: withAds, Status: "active", FilePath: "/dvr/a.mkv"},
			{ID: plain, Status: "active", FilePath: "/dvr/b.mkv"},
		},
	}
	h := newItemHandler(ms).WithCommercials(fakeCommercials{
		withAds: {{StartMS: 600500, EndMS: 690000, Source: "heuristic"}},
	})
	rec := httptest.NewRecorder()
	h.Get(rec, withChiParam(httptest.NewRequest("GET", "/api/v1/items/"+id.String(), nil), "id", id.String()))
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	var resp struct {
		Data struct {
			Markers []MarkerJSON `json:"markers"`
			Files   []struct {
				ID          string                 `json:"id"`
				Commercials []CommercialMarkerJSON `json:"commercials"`
			} `json:"files"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Data.Markers) != 0 {
		t.Error("commercials leaked into markers, which old clients label Skip intro")
	}
	for _, f := range resp.Data.Files {
		switch f.ID {
		case withAds.String():
			if len(f.Commercials) != 1 || f.Commercials[0].StartMS != 600500 || f.Commercials[0].Source != "heuristic" {
				t.Errorf("file with ads: %+v", f.Commercials)
			}
		case plain.String():
			if f.Commercials != nil {
				t.Errorf("file without ads carries commercials: %+v", f.Commercials)
			}
		}
	}
	if !strings.Contains(rec.Body.String(), `"commercials":[{"start_ms":600500,"end_ms":690000,"source":"heuristic"}]`) {
		t.Errorf("wire shape: %s", rec.Body.String())
	}
}
