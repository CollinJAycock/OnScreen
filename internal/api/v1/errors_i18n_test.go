package v1

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/api/middleware"
	"github.com/onscreen/onscreen/internal/auth"
)

// API errors a person can see come back in the language the app asks for
// (Accept-Language), with the code unchanged; with no header they are the
// English text they always were. The drafts are checked by the catalog test;
// this checks the wiring, on a few representative errors: a fixed sentence,
// one with placeholders, a plural, and a validation helper's typed error.
func TestErrorsFollowAcceptLanguage(t *testing.T) {
	switched := func(lang string) *http.Request {
		req := httptest.NewRequest("DELETE", "/", nil)
		if lang != "" {
			req.Header.Set("Accept-Language", lang)
		}
		return req.WithContext(middleware.WithClaims(req.Context(), &auth.Claims{UserID: uuid.New(), Switched: true}))
	}
	plain := func(lang string) *http.Request {
		req := httptest.NewRequest("POST", "/", nil)
		if lang != "" {
			req.Header.Set("Accept-Language", lang)
		}
		return req
	}

	cases := []struct {
		name  string
		write func(w http.ResponseWriter, r *http.Request)
		req   func(lang string) *http.Request
		code  string
		want  map[string]string // Accept-Language -> message ("" = no header)
	}{
		{
			name: "fixed sentence",
			write: func(w http.ResponseWriter, r *http.Request) {
				blockSwitchedSession(w, r, "error.pinSession.deleteAccount")
			},
			req:  switched,
			code: "PIN_SESSION_FORBIDDEN",
			want: map[string]string{
				"":               "sign in with your account password to delete your account",
				"es-ES,es;q=0.9": "inicia sesión con la contraseña de tu cuenta para eliminarla",
				"zh-CN":          "请使用账户密码登录后再删除账户",
			},
		},
		{
			name:  "placeholder: the admin's note stays as written",
			write: func(w http.ResponseWriter, r *http.Request) { writePlaybackStopped(w, r, "Server rebooting") },
			req:   plain,
			code:  "PLAYBACK_STOPPED",
			want: map[string]string{
				"":   "Playback was stopped by the server admin: Server rebooting",
				"es": "El administrador del servidor detuvo la reproducción: Server rebooting",
				"zh": "服务器管理员已停止播放：Server rebooting",
			},
		},
		{
			name:  "plural, one",
			write: func(w http.ResponseWriter, r *http.Request) { refuseStreamLimit(w, r, 1, 1) },
			req:   plain,
			code:  "TOO_MANY_SESSIONS",
			want: map[string]string{
				"":   "Your account can play one stream at a time, and another device is already playing. Stop it to watch here.",
				"es": "Tu cuenta puede tener una sola reproducción a la vez y ya hay otro dispositivo reproduciendo. Detenla para verlo aquí.",
				"zh": "你的账户一次最多可播放 1 个流，其他设备上已有 1 个正在播放。请停止其中一个，再在这里观看。",
			},
		},
		{
			name:  "plural, other",
			write: func(w http.ResponseWriter, r *http.Request) { refuseStreamLimit(w, r, 3, 3) },
			req:   plain,
			code:  "TOO_MANY_SESSIONS",
			want: map[string]string{
				"":   "Your account can play 3 streams at a time, and 3 are already playing on other devices. Stop one to watch here.",
				"es": "Tu cuenta puede tener 3 reproducciones a la vez y ya hay 3 en otros dispositivos. Detén una para verlo aquí.",
			},
		},
		{
			name:  "validation helper's typed error",
			write: func(w http.ResponseWriter, r *http.Request) { badRequestErr(w, r, ValidatePassword("short")) },
			req:   plain,
			code:  "BAD_REQUEST",
			want: map[string]string{
				"":      "password must be at least 12 characters",
				"es":    "la contraseña debe tener al menos 12 caracteres",
				"zh-CN": "密码至少需要 12 个字符",
				// Traditional Chinese isn't shipped: the next tag decides.
				"zh-TW,es;q=0.5": "la contraseña debe tener al menos 12 caracteres",
			},
		},
	}
	for _, c := range cases {
		for lang, want := range c.want {
			t.Run(c.name+"/"+lang, func(t *testing.T) {
				rec := httptest.NewRecorder()
				c.write(rec, c.req(lang))
				var body struct {
					Error struct {
						Code    string `json:"code"`
						Message string `json:"message"`
					} `json:"error"`
				}
				if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
					t.Fatalf("body %q: %v", rec.Body.String(), err)
				}
				if body.Error.Code != c.code {
					t.Errorf("code = %q, want %q", body.Error.Code, c.code)
				}
				if body.Error.Message != want {
					t.Errorf("message = %q, want %q", body.Error.Message, want)
				}
				if got := rec.Header().Get("Vary"); got != "Accept-Language" {
					t.Errorf("Vary = %q, want Accept-Language", got)
				}
			})
		}
	}
}

// A userError reads as its English text wherever err.Error() is used (logs,
// tests), whatever language the response is in.
func TestUserErrorIsEnglish(t *testing.T) {
	if got := ValidatePassword("short").Error(); got != "password must be at least 12 characters" {
		t.Errorf("Error() = %q", got)
	}
	long := strings.Repeat("a", issueNoteMaxRunes+1)
	if _, err := cleanIssueNote(&long); err == nil || err.Error() != "note must be at most 1000 characters" {
		t.Errorf("cleanIssueNote: %v", err)
	}
}
