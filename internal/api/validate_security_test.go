package api

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	v1 "github.com/onscreen/onscreen/internal/api/v1"
	"github.com/onscreen/onscreen/internal/domain/watchlimit"
)

// Every handler whose security checks come through optional setters fails
// validation, naming itself and the missing dependency, when built without
// them; wired, it passes.
func TestValidateSecurityDeps(t *testing.T) {
	cases := []struct {
		name string
		h    Handlers
		want []string
	}{
		{"items", Handlers{Items: v1.NewItemHandler(nil, nil, nil, nil, nil, nil, nil, nil, nil)},
			[]string{"watch limit", "admin stop", "stream limit", "downloads toggle"}},
		{"transcode", Handlers{NativeTranscode: v1.NewNativeTranscodeHandler(nil, nil, nil, nil, nil)},
			[]string{"watch limit", "admin stop", "stream limit"}},
		{"downloads", Handlers{Downloads: v1.NewDownloadsHandler(nil, nil, nil)},
			[]string{"downloads toggle", "watch limit", "admin stop"}},
		{"backup", Handlers{Backup: v1.NewBackupHandler("", 0, nil, nil)}, []string{"re-authentication"}},
		{"opds", Handlers{OPDS: v1.NewOPDSHandler(nil, nil, nil)}, []string{"downloads toggle"}},
		{"livetv", Handlers{LiveTV: v1.NewLiveTVHandler(nil, nil)}, []string{"watch limit"}},
		{"watch_status", Handlers{WatchStatus: v1.NewWatchStatusHandler(nil, nil)}, []string{"item gate"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.h.ValidateSecurityDeps()
			if err == nil {
				t.Fatalf("unwired %s handler must fail validation", tc.name)
			}
			for _, w := range append([]string{tc.name + ":"}, tc.want...) {
				if !strings.Contains(err.Error(), w) {
					t.Errorf("error should mention %q, got: %v", w, err)
				}
			}
		})
	}

	wired := Handlers{
		NativeTranscode: v1.NewNativeTranscodeHandler(nil, nil, nil, nil, nil).
			WithWatchLimit(noLimits{}).
			WithPlaybackStops(v1.NewPlaybackStopGuard(nil, nil)).
			WithStreamLimit(v1.NewStreamLimit(nil, nil, nil)),
	}
	if err := wired.ValidateSecurityDeps(); err != nil {
		t.Errorf("wired transcode handler should pass, got: %v", err)
	}
	// Every feature off: nothing to check.
	if err := (Handlers{}).ValidateSecurityDeps(); err != nil {
		t.Errorf("empty Handlers should pass, got: %v", err)
	}
}

type noLimits struct{}

func (noLimits) GetPolicy(context.Context, uuid.UUID) (watchlimit.Policy, error) {
	return watchlimit.Policy{}, nil
}
func (noLimits) TodayUsageSeconds(context.Context, uuid.UUID, time.Time) (int, error) {
	return 0, nil
}
func (noLimits) AddTick(context.Context, uuid.UUID, time.Time, time.Time) (int, error) {
	return 0, nil
}
