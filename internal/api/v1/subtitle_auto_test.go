package v1

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/onscreen/onscreen/internal/db/gen"
	"github.com/onscreen/onscreen/internal/domain/settings"
)

type saSettings struct {
	cfg   settings.SubtitleAutoDownloadConfig
	saves int
}

func (s *saSettings) SubtitleAutoDownload(context.Context) settings.SubtitleAutoDownloadConfig {
	return s.cfg
}

func (s *saSettings) SetSubtitleAutoDownload(_ context.Context, cfg settings.SubtitleAutoDownloadConfig) error {
	switch cfg.HearingImpaired {
	case "prefer", "avoid", "any":
	default:
		return settings.ErrInvalidSetting
	}
	s.cfg = cfg
	s.saves++
	return nil
}

type saLibDB struct {
	rows map[uuid.UUID]gen.LibrarySubtitleAutoDownload
}

func (d *saLibDB) GetLibrarySubtitleAutoDownload(_ context.Context, id uuid.UUID) (gen.LibrarySubtitleAutoDownload, error) {
	r, ok := d.rows[id]
	if !ok {
		return gen.LibrarySubtitleAutoDownload{}, pgx.ErrNoRows
	}
	return r, nil
}

func (d *saLibDB) UpsertLibrarySubtitleAutoDownload(_ context.Context, arg gen.UpsertLibrarySubtitleAutoDownloadParams) (gen.LibrarySubtitleAutoDownload, error) {
	r := gen.LibrarySubtitleAutoDownload{LibraryID: arg.LibraryID, Enabled: arg.Enabled, Languages: arg.Languages}
	d.rows[arg.LibraryID] = r
	return r, nil
}

func (d *saLibDB) DeleteLibrarySubtitleAutoDownload(_ context.Context, id uuid.UUID) error {
	delete(d.rows, id)
	return nil
}

func saDo(fn http.HandlerFunc, method, id, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, "/", strings.NewReader(body))
	if id != "" {
		r = withChiParam(r, "id", id)
	}
	w := httptest.NewRecorder()
	fn(w, r)
	return w
}

func TestSubtitleAuto_Settings(t *testing.T) {
	st := &saSettings{cfg: settings.SubtitleAutoDownloadConfig{Languages: "en", PreferHashMatch: true, HearingImpaired: "any"}}
	h := NewSubtitleAutoHandler(st, &saLibDB{rows: map[uuid.UUID]gen.LibrarySubtitleAutoDownload{}},
		func() bool { return false }, slog.New(slog.NewTextHandler(io.Discard, nil)))

	w := saDo(h.GetSettings, http.MethodGet, "", "")
	var env struct {
		Data subtitleAutoSettingsJSON `json:"data"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &env)
	if env.Data.Enabled || !env.Data.OnlyMissing || env.Data.ProviderConfigured || env.Data.Languages != "en" {
		t.Fatalf("defaults: %+v", env.Data)
	}

	w = saDo(h.UpdateSettings, http.MethodPut, "", `{"enabled":true,"languages":"EN, spa ,pt-BR","hearing_impaired":"avoid"}`)
	if w.Code != http.StatusOK || st.cfg.Languages != "en,es,pt-br" || !st.cfg.Enabled || st.cfg.HearingImpaired != "avoid" || !st.cfg.PreferHashMatch {
		t.Fatalf("update: %d %+v", w.Code, st.cfg)
	}

	for _, body := range []string{
		`{"hearing_impaired":"sometimes"}`,
		`{"languages":"../../etc"}`,
		`{"enabled":true,"languages":""}`,
		`nope`,
	} {
		saves := st.saves
		if w := saDo(h.UpdateSettings, http.MethodPut, "", body); w.Code != http.StatusBadRequest && w.Code != http.StatusUnprocessableEntity {
			t.Errorf("%q: status %d", body, w.Code)
		}
		if st.saves != saves {
			t.Errorf("%q was saved", body)
		}
	}
}

func TestSubtitleAuto_LibraryOverride(t *testing.T) {
	db := &saLibDB{rows: map[uuid.UUID]gen.LibrarySubtitleAutoDownload{}}
	h := NewSubtitleAutoHandler(&saSettings{}, db, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	lib := uuid.New()

	w := saDo(h.GetLibrary, http.MethodGet, lib.String(), "")
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"enabled":null`) {
		t.Fatalf("no override: %s", w.Body.String())
	}
	w = saDo(h.UpdateLibrary, http.MethodPut, lib.String(), `{"enabled":false}`)
	if w.Code != http.StatusOK || db.rows[lib].Enabled == nil || *db.rows[lib].Enabled || db.rows[lib].Languages != nil {
		t.Fatalf("disable: %d %+v", w.Code, db.rows[lib])
	}
	w = saDo(h.UpdateLibrary, http.MethodPut, lib.String(), `{"enabled":true,"languages":"ger,fr"}`)
	if w.Code != http.StatusOK || *db.rows[lib].Languages != "de,fr" {
		t.Fatalf("languages: %d %+v", w.Code, db.rows[lib])
	}
	if w := saDo(h.UpdateLibrary, http.MethodPut, lib.String(), `{"languages":"!!"}`); w.Code != http.StatusUnprocessableEntity && w.Code != http.StatusBadRequest {
		t.Fatalf("bad languages: %d", w.Code)
	}
	// Both null: back to following the server.
	w = saDo(h.UpdateLibrary, http.MethodPut, lib.String(), `{"enabled":null,"languages":null}`)
	if _, ok := db.rows[lib]; w.Code != http.StatusOK || ok {
		t.Fatalf("clear: %d %v", w.Code, ok)
	}
}
