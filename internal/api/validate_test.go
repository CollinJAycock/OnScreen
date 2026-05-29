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
