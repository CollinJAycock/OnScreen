package v1

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/api/middleware"
	"github.com/onscreen/onscreen/internal/auth"
	"github.com/onscreen/onscreen/internal/db/gen"
	"github.com/onscreen/onscreen/internal/domain/media"
	"github.com/onscreen/onscreen/internal/i18n"
	"github.com/onscreen/onscreen/internal/transcode"
)

// reasonsIn writes each message in English, Spanish and Chinese.
func reasonsIn(ms ...transcode.ReasonMessage) [3][]string {
	return [3][]string{
		transcode.MessageTexts(ms, i18n.English),
		transcode.MessageTexts(ms, i18n.Spanish),
		transcode.MessageTexts(ms, i18n.SimplifiedChinese),
	}
}

// The reasons only Start knows, and the receiver lines, in each language;
// English is the text they always had.
func TestStartReasonMessages_Languages(t *testing.T) {
	br := int64(25_000_000)
	file := &media.File{VideoCodec: strPtr("h264"), AudioCodec: strPtr("aac"), Container: strPtr("mkv"), Bitrate: &br}
	caps := transcode.ParseCapabilities("videoDecoder=h264,audioDecoder=aac")
	start := func(decision string, reqH, srcH int, c streamCap, forced bool) []transcode.ReasonMessage {
		return startReasonMessages(file, caps, decision, reqH, srcH, c, forced)
	}
	cases := []struct {
		name string
		msgs []transcode.ReasonMessage
		want [3]string
	}{
		{"user cap", start("transcode", 0, 1080, streamCap{Kbps: 8000}, true), [3]string{
			"Over the user's bitrate cap (25 Mbps > 8 Mbps)",
			"Supera el límite de bitrate del usuario (25 Mbps > 8 Mbps)",
			"超出用户的码率上限（25 Mbps > 8 Mbps）"}},
		{"internet limit", start("transcode", 0, 1080, streamCap{Kbps: 4000, Remote: true, RemoteBinding: true}, false), [3]string{
			"Over the internet streaming limit (25 Mbps > 4 Mbps)",
			"Supera el límite de streaming por internet (25 Mbps > 4 Mbps)",
			"超出互联网串流限制（25 Mbps > 4 Mbps）"}},
		{"quality setting", start("transcode", 0, 1080, streamCap{Kbps: 6000, QualityBinding: true}, false), [3]string{
			"Over the viewer's quality setting (25 Mbps > 6 Mbps)",
			"Supera el ajuste de calidad del espectador (25 Mbps > 6 Mbps)",
			"超出观看者的画质设置（25 Mbps > 6 Mbps）"}},
		{"quality pick, digits not grouped", start("transcode", 1440, 2160, streamCap{}, false), [3]string{
			"Quality set to 1440p by the viewer",
			"El espectador eligió la calidad 1440p",
			"观看者将画质设为 1440p"}},
		{"remux fallback", start("remux", 0, 1080, streamCap{}, false), [3]string{
			"Container adapted for streaming (requested by client)",
			"Contenedor adaptado para streaming (a petición del cliente)",
			"已为串流调整容器（应客户端请求）"}},
		{"fallback", start("transcode", 0, 1080, streamCap{}, false), [3]string{
			"Requested by client", "A petición del cliente", "应客户端请求"}},
		{"passthrough", []transcode.ReasonMessage{passthroughReason("eac3")}, [3]string{
			"E-AC-3 audio passed through to the receiver",
			"Audio E-AC-3 en paso directo al receptor",
			"E-AC-3 音频直通至功放"}},
		{"receiver encode", []transcode.ReasonMessage{receiverEncodeReason("truehd", "eac3")}, [3]string{
			"TrueHD audio converted to E-AC-3 for the receiver",
			"Audio TrueHD convertido a E-AC-3 para el receptor",
			"TrueHD 音频已为功放转换为 E-AC-3"}},
		{"receiver encode, unknown source", []transcode.ReasonMessage{receiverEncodeReason("", "ac3")}, [3]string{
			"Audio converted to AC-3 for the receiver",
			"Audio convertido a AC-3 para el receptor",
			"音频已为功放转换为 AC-3"}},
	}
	for _, tc := range cases {
		got := reasonsIn(tc.msgs...)
		for i := range got {
			if !reflect.DeepEqual(got[i], []string{tc.want[i]}) {
				t.Errorf("%s [%s]: got %q, want %q", tc.name, i18n.Supported[i], got[i], tc.want[i])
			}
		}
	}
	// startReasons stays the English of the same messages.
	if got := startReasons(file, caps, "transcode", 1440, 2160, streamCap{}, false); !reflect.DeepEqual(got, []string{"Quality set to 1440p by the viewer"}) {
		t.Errorf("startReasons: %q", got)
	}
}

// Start stores the reasons as messages with their English; a Spanish or
// Chinese reader gets them in their language.
func TestStart_ReasonsStoredForAnyReader(t *testing.T) {
	itemID, user := uuid.New(), uuid.New()
	file := media.File{ID: uuid.New(), MediaItemID: itemID, FilePath: "/media/x.mkv",
		VideoCodec: strPtr("hevc"), AudioCodec: strPtr("aac"), ResolutionW: ip(3840), ResolutionH: ip(2160)}
	h, store := newStartHandler(t, file)
	rec := startAs(t, h, itemID, user, "203.0.113.4:9", "videoDecoder=h264,audioDecoder=aac", transcodeStartRequest{Height: 720})
	if rec.Code != http.StatusOK {
		t.Fatalf("start: %d %s", rec.Code, rec.Body.String())
	}
	s := onlySession(t, store)
	want := [3][]string{
		{"HEVC not supported by client", "3840×2160 exceeds client max 1920×1080", "Quality set to 720p by the viewer"},
		{"HEVC no compatible con el cliente", "3840×2160 supera el máx. del cliente: 1920×1080", "El espectador eligió la calidad 720p"},
		{"客户端不支持 HEVC", "3840×2160 超出客户端上限（1920×1080）", "观看者将画质设为 720p"},
	}
	if !reflect.DeepEqual(s.TranscodeReasons, want[0]) {
		t.Errorf("stored English: %q", s.TranscodeReasons)
	}
	for i, l := range i18n.Supported {
		if got := s.ReasonsIn(l); !reflect.DeepEqual(got, want[i]) {
			t.Errorf("%s:\n got  %q\n want %q", l, got, want[i])
		}
	}
}

// A burn-in replaces the "Requested by client" fallback, in every language.
func TestStart_BurnInReason(t *testing.T) {
	itemID, user := uuid.New(), uuid.New()
	file := burnFile(itemID)
	h, store := newStartHandler(t, file)
	offsets := &fakeOffsets{}
	h.WithBurnInOffsets(offsets, offsets)
	registerWorker(t, store, allWorkerFeatures)
	rec := startAs(t, h, itemID, user, "192.0.2.10:1", "videoDecoder=h264,audioDecoder=aac",
		transcodeStartRequest{BurnSubtitleStreamIndex: ip(3)})
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	s := onlySession(t, store)
	if !reflect.DeepEqual(s.TranscodeReasons, []string{"Burning in image subtitles"}) {
		t.Errorf("English: %q", s.TranscodeReasons)
	}
	if got := s.ReasonsIn(i18n.SimplifiedChinese); !reflect.DeepEqual(got, []string{"正在烧录图像字幕"}) {
		t.Errorf("zh-Hans: %q", got)
	}
}

func listSessionsIn(t *testing.T, hs *stopHarness, claims *auth.Claims, acceptLanguage string) ([]activeSession, http.Header) {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/sessions", nil)
	if acceptLanguage != "" {
		req.Header.Set("Accept-Language", acceptLanguage)
	}
	req = req.WithContext(middleware.WithClaims(req.Context(), claims))
	hs.h.List(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("list status %d: %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Data []activeSession `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v (%s)", err, rec.Body.String())
	}
	return body.Data, rec.Header()
}

// GET /sessions writes transcode_reasons in the reader's language. A session
// stored before translations (only English strings) shows them as they are.
func TestSessions_List_ReasonsInReaderLanguage(t *testing.T) {
	hs := newStopHarness(t)
	user := uuid.New()
	hs.users[user] = gen.User{ID: user, Username: "sam"}
	now := time.Now()
	add := func(title string, set func(*transcode.Session)) {
		item := hs.addItem(title)
		fileID := uuid.New()
		hs.files[fileID] = media.File{ID: fileID, MediaItemID: item, VideoCodec: strPtr("hevc"), AudioCodec: strPtr("truehd")}
		sess := transcode.Session{
			ID: transcode.NewSessionID(), UserID: user, MediaItemID: item, FileID: fileID,
			Decision: "transcode", FilePath: "/media/" + title + ".mkv", CreatedAt: now, LastActivityAt: now,
		}
		set(&sess)
		if err := hs.store.Create(context.Background(), sess); err != nil {
			t.Fatal(err)
		}
	}
	add("New", func(s *transcode.Session) {
		s.SetTranscodeReasons([]transcode.ReasonMessage{
			transcode.NewReasonMessage("reason.videoCodec", "codec", "HEVC"),
			transcode.NewReasonMessage("reason.audioCodec", "codec", "TrueHD"),
		})
	})
	add("Legacy", func(s *transcode.Session) {
		s.TranscodeReasons = []string{"HEVC not supported by client"}
	})

	byTitle := func(cards []activeSession) map[string][]string {
		m := map[string][]string{}
		for _, c := range cards {
			m[c.Title] = c.TranscodeReasons
		}
		return m
	}
	want := map[string]map[string][]string{
		"": {
			"New":    {"HEVC not supported by client", "TrueHD audio not supported by client"},
			"Legacy": {"HEVC not supported by client"},
		},
		"es-MX,es;q=0.9": {
			"New":    {"HEVC no compatible con el cliente", "Audio TrueHD no compatible con el cliente"},
			"Legacy": {"HEVC not supported by client"},
		},
		"zh-CN": {
			"New":    {"客户端不支持 HEVC", "客户端不支持 TrueHD 音频"},
			"Legacy": {"HEVC not supported by client"},
		},
	}
	for lang, w := range want {
		cards, hdr := listSessionsIn(t, hs, stopAdminClaims(), lang)
		if got := byTitle(cards); !reflect.DeepEqual(got, w) {
			t.Errorf("Accept-Language %q:\n got  %q\n want %q", lang, got, w)
		}
		if !slices.Contains(hdr.Values("Vary"), "Accept-Language") {
			t.Errorf("Accept-Language %q: Vary = %q", lang, hdr.Values("Vary"))
		}
	}
}
