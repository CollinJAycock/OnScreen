package api

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"

	v1 "github.com/onscreen/onscreen/internal/api/v1"
)

type aclStub struct{}

func (aclStub) CanAccessLibrary(_ context.Context, _, _ uuid.UUID, _ bool) (bool, error) {
	return true, nil
}
func (aclStub) AllowedLibraryIDs(_ context.Context, _ uuid.UUID, _ bool) (map[uuid.UUID]struct{}, error) {
	return nil, nil
}

func TestValidateLibraryAccess(t *testing.T) {
	// A wired handler passes.
	wired := Handlers{Favorites: v1.NewFavoritesHandler(nil, nil).WithLibraryAccess(aclStub{})}
	if err := wired.ValidateLibraryAccess(); err != nil {
		t.Errorf("wired handler should pass, got: %v", err)
	}

	// A handler built without the checker fails, and the error names it.
	unwired := Handlers{Favorites: v1.NewFavoritesHandler(nil, nil)}
	err := unwired.ValidateLibraryAccess()
	if err == nil {
		t.Fatal("unwired favorites handler must fail validation")
	}
	if !strings.Contains(err.Error(), "favorites") {
		t.Errorf("error should name the unwired handler, got: %v", err)
	}

	// Nil fields (disabled features) are skipped — empty Handlers passes.
	if err := (Handlers{}).ValidateLibraryAccess(); err != nil {
		t.Errorf("empty Handlers should pass (all features disabled), got: %v", err)
	}
}

// TestValidateLibraryAccess_CoversEveryFailOpenHandler pins the assertion's
// COVERAGE, not just its mechanism.
//
// The guarantee this function exists to provide is "no content handler can
// boot with a nil ACL checker, because a nil checker serves every library".
// That guarantee silently did not hold for five handlers — lyrics, people,
// subtitles, transcode and trickplay all take a .WithLibraryAccess checker
// and all fail open without one, but none were listed here, so the assertion
// looked comprehensive while leaving the transcode start path (any user can
// stream from any library) unguarded.
//
// Each subtest builds ONLY that handler, unwired, and requires the validator
// to name it. If someone adds a new fail-open handler and forgets the check(),
// this test won't catch it — but it does stop the five known ones from
// regressing, and the table makes the omission visible to a reviewer.
func TestValidateLibraryAccess_CoversEveryFailOpenHandler(t *testing.T) {
	cases := []struct {
		name string
		h    Handlers
	}{
		{"lyrics", Handlers{Lyrics: v1.NewLyricsHandler(nil, nil, nil, nil)}},
		{"people", Handlers{People: v1.NewPeopleHandler(nil, nil, nil)}},
		{"subtitles", Handlers{Subtitles: v1.NewSubtitleHandler(nil, nil, nil)}},
		{"trickplay", Handlers{Trickplay: v1.NewTrickplayHandler(nil, nil, nil)}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.h.ValidateLibraryAccess()
			if err == nil {
				t.Fatalf("unwired %s handler must fail validation — a nil checker serves every library", tc.name)
			}
			if !strings.Contains(err.Error(), tc.name) {
				t.Errorf("error should name %q, got: %v", tc.name, err)
			}
		})
	}
}
