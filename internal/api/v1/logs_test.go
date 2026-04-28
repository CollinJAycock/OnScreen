package v1

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/onscreen/onscreen/internal/observability"
)

// noopInner is a slog.Handler that emits nothing — tests don't care about
// stdout, they only inspect what the LogRingBuffer captured.
type noopInner struct{ enabled slog.Level }

func (n *noopInner) Enabled(_ context.Context, l slog.Level) bool { return l >= n.enabled }
func (n *noopInner) Handle(_ context.Context, _ slog.Record) error { return nil }
func (n *noopInner) WithAttrs(_ []slog.Attr) slog.Handler          { return n }
func (n *noopInner) WithGroup(_ string) slog.Handler               { return n }

// newSeededHandler returns a LogsHandler whose buffer has been pre-populated
// with a balanced mix of levels so per-level filtering tests have something
// to filter against.
func newSeededHandler(t *testing.T) *LogsHandler {
	t.Helper()
	buf := observability.NewLogRingBuffer(&noopInner{enabled: slog.LevelDebug}, 50)
	logger := slog.New(buf)
	logger.Debug("dbg-msg")
	logger.Info("inf-msg")
	logger.Warn("wrn-msg")
	logger.Error("err-msg")
	return NewLogsHandler(buf)
}

// decodeEntries unmarshals the API envelope { "data": [...] } into the
// public LogEntry shape so test bodies can compare on the same struct
// the API actually serializes.
func decodeEntries(t *testing.T, body []byte) []observability.LogEntry {
	t.Helper()
	var resp struct {
		Data []observability.LogEntry `json:"data"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("unmarshal: %v\nbody=%s", err, body)
	}
	return resp.Data
}

func TestLogs_List_DefaultsToInfo(t *testing.T) {
	h := newSeededHandler(t)
	rec := httptest.NewRecorder()
	h.List(rec, httptest.NewRequest(http.MethodGet, "/api/v1/admin/logs", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200", rec.Code)
	}
	got := decodeEntries(t, rec.Body.Bytes())
	// Default level=info → drops the debug entry.
	if len(got) != 3 {
		t.Fatalf("got %d entries, want 3 (info+warn+error)", len(got))
	}
	for _, e := range got {
		if e.Level == "DEBUG" {
			t.Errorf("debug entry leaked: %+v", e)
		}
	}
}

func TestLogs_List_LevelFilter(t *testing.T) {
	cases := []struct {
		level     string
		wantCount int
	}{
		{"debug", 4},
		{"info", 3},
		{"warn", 2},
		{"error", 1},
		// "warning" alias kept for users who type it out.
		{"warning", 2},
		// Unrecognised values fall back to info.
		{"bogus", 3},
	}
	for _, tc := range cases {
		t.Run(tc.level, func(t *testing.T) {
			h := newSeededHandler(t)
			rec := httptest.NewRecorder()
			h.List(rec, httptest.NewRequest(http.MethodGet, "/api/v1/admin/logs?level="+tc.level, nil))
			got := decodeEntries(t, rec.Body.Bytes())
			if len(got) != tc.wantCount {
				t.Errorf("level=%s: got %d, want %d", tc.level, len(got), tc.wantCount)
			}
		})
	}
}

func TestLogs_List_LimitTakesNewest(t *testing.T) {
	// Fresh buffer with 5 distinguishable info messages.
	buf := observability.NewLogRingBuffer(&noopInner{enabled: slog.LevelDebug}, 50)
	logger := slog.New(buf)
	logger.Info("m1")
	logger.Info("m2")
	logger.Info("m3")
	logger.Info("m4")
	logger.Info("m5")

	h := NewLogsHandler(buf)
	rec := httptest.NewRecorder()
	h.List(rec, httptest.NewRequest(http.MethodGet, "/api/v1/admin/logs?limit=2", nil))

	got := decodeEntries(t, rec.Body.Bytes())
	if len(got) != 2 {
		t.Fatalf("got %d entries, want 2", len(got))
	}
	// limit takes the *tail* — newest entries.
	if got[0].Message != "m4" || got[1].Message != "m5" {
		t.Errorf("limit returned %v, %v; want m4, m5", got[0].Message, got[1].Message)
	}
}

func TestLogs_List_LimitClampedToMax(t *testing.T) {
	h := newSeededHandler(t)
	rec := httptest.NewRecorder()
	// 99999 should clamp to the documented max (2000) without erroring.
	h.List(rec, httptest.NewRequest(http.MethodGet, "/api/v1/admin/logs?limit=99999", nil))
	if rec.Code != http.StatusOK {
		t.Errorf("status: got %d, want 200 (limit should clamp, not reject)", rec.Code)
	}
}

func TestLogs_List_NilBufferReturns503(t *testing.T) {
	h := NewLogsHandler(nil)
	rec := httptest.NewRecorder()
	h.List(rec, httptest.NewRequest(http.MethodGet, "/api/v1/admin/logs", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("nil buffer: got %d, want 503", rec.Code)
	}
}

// noopInner needs context — pulled inline so the file compiles standalone.
// (slog.Handler.Enabled / Handle take ctx as first arg.)
//
// keep this declaration last so test bodies above read first.
var _ slog.Handler = (*noopInner)(nil)
