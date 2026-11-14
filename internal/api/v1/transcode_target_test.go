package v1

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/api/middleware"
	"github.com/onscreen/onscreen/internal/auth"
	"github.com/onscreen/onscreen/internal/domain/media"
	"github.com/onscreen/onscreen/internal/domain/watchlimit"
)

func decisionAs(t *testing.T, h *NativeTranscodeHandler, itemID uuid.UUID, claims *auth.Claims, remote string) *httptest.ResponseRecorder {
	t.Helper()
	b, _ := json.Marshal(transcodeStartRequest{})
	req := withChiParam(httptest.NewRequest("POST", "/", bytes.NewReader(b)), "id", itemID.String())
	req.RemoteAddr = remote
	req.Header.Set("X-Client-Capabilities", "videoDecoder=h264,audioDecoder=aac")
	req = req.WithContext(middleware.WithClaims(req.Context(), claims))
	rec := httptest.NewRecorder()
	h.Decision(rec, req)
	return rec
}

// The playback decision runs exactly Start's gates: it used to skip the
// parental watch limit and the admin stop, so it told a profile outside its
// allowed hours (or a client an admin had just stopped) to direct-play, and
// Start or the stream then refused it.
func TestDecision_AppliesStartsGates(t *testing.T) {
	itemID, user := uuid.New(), uuid.New()
	file := media.File{ID: uuid.New(), MediaItemID: itemID, FilePath: "/media/x.mkv",
		VideoCodec: strPtr("h264"), AudioCodec: strPtr("aac"), ResolutionW: ip(1920), ResolutionH: ip(1080)}

	zero := 0
	cases := []struct {
		name  string
		setup func(h *NativeTranscodeHandler)
		code  string
	}{
		{"watch limit", func(h *NativeTranscodeHandler) {
			h.WithWatchLimit(&fakeWatchLimit{policy: watchlimit.Policy{DailyLimitMinutes: &zero}})
		}, "PARENTAL_LIMIT"},
		{"admin stop", func(h *NativeTranscodeHandler) {
			guard := NewPlaybackStopGuard(nil, slog.Default())
			guard.Block(context.Background(), user, itemID, "192.0.2.50", "")
			h.WithPlaybackStops(guard)
		}, "PLAYBACK_STOPPED"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h, _ := newStartHandler(t, file)
			tc.setup(h)

			dec := decisionAs(t, h, itemID, &auth.Claims{UserID: user}, "192.0.2.50:1")
			start := startAs(t, h, itemID, user, "192.0.2.50:1", "videoDecoder=h264,audioDecoder=aac", transcodeStartRequest{})
			for name, rec := range map[string]*httptest.ResponseRecorder{"decision": dec, "start": start} {
				if rec.Code != http.StatusForbidden {
					t.Fatalf("%s: got %d, want 403 (%s)", name, rec.Code, rec.Body.String())
				}
				if code, _ := errorCode(t, rec); code != tc.code {
					t.Errorf("%s: code %q, want %q", name, code, tc.code)
				}
			}
		})
	}

	// Unrestricted: the decision answers normally.
	h, _ := newStartHandler(t, file)
	h.WithWatchLimit(&fakeWatchLimit{})
	if rec := decisionAs(t, h, itemID, &auth.Claims{UserID: user}, "192.0.2.50:1"); rec.Code != http.StatusOK {
		t.Fatalf("unrestricted decision: %d %s", rec.Code, rec.Body.String())
	}
}

// The rating/label denial stays 403 on Start and the decision (the Android
// apps show their "outside your content rating limit" message for it); a
// library the caller can't see is 404 on both.
func TestDecisionAndStart_RestrictedItemAnswers(t *testing.T) {
	itemID, user := uuid.New(), uuid.New()
	file := media.File{ID: uuid.New(), MediaItemID: itemID, FilePath: "/media/x.mkv",
		VideoCodec: strPtr("h264"), AudioCodec: strPtr("aac")}
	rRated := "R"

	h, _ := newStartHandler(t, file)
	h.media.(*mockTranscodeMedia).item.ContentRating = &rRated
	capped := &auth.Claims{UserID: user, MaxContentRating: "PG-13"}
	if rec := decisionAs(t, h, itemID, capped, "192.0.2.9:1"); rec.Code != http.StatusForbidden {
		t.Errorf("decision over the ceiling: %d, want 403", rec.Code)
	}

	h.WithLibraryAccess(denyLibraries{})
	if rec := decisionAs(t, h, itemID, &auth.Claims{UserID: user}, "192.0.2.9:1"); rec.Code != http.StatusNotFound {
		t.Errorf("decision, library hidden: %d, want 404", rec.Code)
	}
	if rec := startAs(t, h, itemID, user, "192.0.2.9:1", "", transcodeStartRequest{}); rec.Code != http.StatusNotFound {
		t.Errorf("start, library hidden: %d, want 404", rec.Code)
	}
}
