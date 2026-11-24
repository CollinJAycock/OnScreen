package v1

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/db/gen"
)

// subtitle_mode (migration 00066) passes through the quality-profile PUT when
// sent; an app that predates it sends only forced_subtitles_only, and the
// UPDATE's own rule (tested against Postgres) decides the mode.
func TestSetQualityProfile_SubtitleMode(t *testing.T) {
	cases := []struct {
		body       string
		status     int
		wantMode   *string
		wantForced bool
	}{
		{`{"subtitle_mode":"foreign"}`, http.StatusNoContent, strPtr("foreign"), false},
		{`{"subtitle_mode":"always","forced_subtitles_only":true}`, http.StatusNoContent, strPtr("always"), true},
		{`{"subtitle_mode":"forced"}`, http.StatusNoContent, strPtr("forced"), false},
		{`{"subtitle_mode":""}`, http.StatusNoContent, nil, false},
		{`{"forced_subtitles_only":true}`, http.StatusNoContent, nil, true},
		{`{"subtitle_mode":"sometimes"}`, http.StatusBadRequest, nil, false},
	}
	for _, tc := range cases {
		db := &mockUserDB{}
		h := NewUserHandler(&mockUserService{}).WithDB(db)
		req := adminAuthedRequest(httptest.NewRequest("PUT", "/api/v1/users/me/quality-profile", strings.NewReader(tc.body)), uuid.New())
		rec := httptest.NewRecorder()
		h.SetQualityProfile(rec, req)
		if rec.Code != tc.status {
			t.Errorf("%s: status %d, want %d (%s)", tc.body, rec.Code, tc.status, rec.Body)
			continue
		}
		if tc.status != http.StatusNoContent {
			if db.lastQuality != nil {
				t.Errorf("%s: refused body still wrote the profile", tc.body)
			}
			continue
		}
		got := db.lastQuality
		if got == nil {
			t.Fatalf("%s: no write", tc.body)
		}
		if (got.SubtitleMode == nil) != (tc.wantMode == nil) || (got.SubtitleMode != nil && *got.SubtitleMode != *tc.wantMode) {
			t.Errorf("%s: SubtitleMode %v, want %v", tc.body, derefMode(got.SubtitleMode), derefMode(tc.wantMode))
		}
		if got.ForcedSubtitlesOnly != tc.wantForced {
			t.Errorf("%s: ForcedSubtitlesOnly %v, want %v", tc.body, got.ForcedSubtitlesOnly, tc.wantForced)
		}
	}
}

// GET derives forced_subtitles_only from the mode, so an app that only knows
// the bool treats "foreign" as forced-only.
func TestGetPreferences_SubtitleMode(t *testing.T) {
	for mode, wantForced := range map[string]bool{"foreign": true, "forced": true, "always": false} {
		db := &mockUserDB{prefsRow: gen.GetUserPreferencesRow{SubtitleMode: mode, ForcedSubtitlesOnly: !wantForced}}
		h := NewUserHandler(&mockUserService{}).WithDB(db)
		rec := httptest.NewRecorder()
		h.GetPreferences(rec, adminAuthedRequest(httptest.NewRequest("GET", "/api/v1/users/me/preferences", nil), uuid.New()))
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: status %d (%s)", mode, rec.Code, rec.Body)
		}
		var env struct {
			Data struct {
				SubtitleMode        string `json:"subtitle_mode"`
				ForcedSubtitlesOnly bool   `json:"forced_subtitles_only"`
			} `json:"data"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
			t.Fatal(err)
		}
		if env.Data.SubtitleMode != mode || env.Data.ForcedSubtitlesOnly != wantForced {
			t.Errorf("%s: got mode %q forced %v, want %q %v", mode, env.Data.SubtitleMode, env.Data.ForcedSubtitlesOnly, mode, wantForced)
		}
	}
}

func derefMode(s *string) string {
	if s == nil {
		return "<nil>"
	}
	return *s
}
