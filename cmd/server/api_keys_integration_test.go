//go:build integration

// Personal API keys end to end against a real Postgres: the middleware
// resolves a key through apiKeyAdapter on every request, so the owner's label
// restriction applies live, deleting the key or the user stops it on the next
// request, an expired key is refused, and last_used_at moves.
//
// Run with: go test -tags=integration ./cmd/server/...
package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onscreen/onscreen/internal/api/middleware"
	"github.com/onscreen/onscreen/internal/auth"
	"github.com/onscreen/onscreen/internal/db/gen"
	"github.com/onscreen/onscreen/internal/testdb"
)

func TestAPIKeys_Integration(t *testing.T) {
	pool := testdb.New(t)
	q := gen.New(pool)
	ctx := context.Background()

	tm, err := auth.NewTokenMaker(auth.DeriveKey32("test-secret-key-that-is-32-bytes!"))
	if err != nil {
		t.Fatal(err)
	}
	keys := &apiKeyAdapter{q: q}
	a := middleware.NewAuthenticator(tm).
		WithEpochReader(&sessionEpochAdapter{q: q}).
		WithAPIKeys(keys)

	user, err := q.CreateUser(ctx, gen.CreateUserParams{Username: "script-owner"})
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	mint := func(name string, expires pgtype.Timestamptz) (string, gen.CreateAPIKeyRow) {
		raw, hash, prefix, err := auth.GenerateAPIKey()
		if err != nil {
			t.Fatal(err)
		}
		row, err := q.CreateAPIKey(ctx, gen.CreateAPIKeyParams{
			UserID: user.ID, Name: name, KeyHash: hash, KeyPrefix: prefix, Scope: auth.APIKeyScopeFull, ExpiresAt: expires,
		})
		if err != nil {
			t.Fatalf("CreateAPIKey: %v", err)
		}
		return raw, row
	}
	call := func(raw string) (int, *auth.Claims) {
		var got *auth.Claims
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		r.Header.Set("Authorization", "Bearer "+raw)
		w := httptest.NewRecorder()
		a.Required(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			got = middleware.ClaimsFromContext(r.Context())
		})).ServeHTTP(w, r)
		return w.Code, got
	}

	raw, row := mint("Home Assistant", pgtype.Timestamptz{})
	code, c := call(raw)
	if code != http.StatusOK || c == nil || c.UserID != user.ID || c.APIKeyID != row.ID || len(c.BlockedLabels) != 0 {
		t.Fatalf("first call: %d %+v", code, c)
	}

	// An admin restricts the user: the key's next request carries it, no
	// re-issue needed.
	if err := q.UpdateUserLabelRestrictions(ctx, gen.UpdateUserLabelRestrictionsParams{
		ID: user.ID, BlockedLabels: []string{"horror"}, AllowedLabels: []string{},
	}); err != nil {
		t.Fatalf("UpdateUserLabelRestrictions: %v", err)
	}
	if err := q.BumpSessionEpoch(ctx, user.ID); err != nil {
		t.Fatal(err)
	}
	code, c = call(raw)
	if code != http.StatusOK || len(c.BlockedLabels) != 1 || c.BlockedLabels[0] != "horror" {
		t.Fatalf("restriction not applied live: %d %+v", code, c)
	}
	// Derived credentials follow the live epoch.
	if !a.SessionStillValid(ctx, c) {
		t.Fatal("fresh key claims fail the re-check")
	}

	// last_used_at is written (asynchronously, at most every few minutes).
	deadline := time.Now().Add(3 * time.Second)
	for {
		list, err := q.ListAPIKeysForUser(ctx, user.ID)
		if err != nil {
			t.Fatal(err)
		}
		if list[0].LastUsedAt.Valid {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("last_used_at never written")
		}
		time.Sleep(50 * time.Millisecond)
	}

	// Expired keys are refused.
	expired, _ := mint("old", pgtype.Timestamptz{Time: time.Now().Add(-time.Minute), Valid: true})
	if code, _ := call(expired); code != http.StatusUnauthorized {
		t.Fatalf("expired key: %d", code)
	}

	// Deleting the key takes effect on the very next request, and ends the
	// credentials minted under it.
	stream, _ := tm.IssueStreamToken(*c, row.ID)
	streamClaims, _ := tm.ValidateAccessToken(stream)
	if _, err := q.DeleteAPIKeyForUser(ctx, gen.DeleteAPIKeyForUserParams{ID: row.ID, UserID: user.ID}); err != nil {
		t.Fatalf("DeleteAPIKeyForUser: %v", err)
	}
	if code, _ := call(raw); code != http.StatusUnauthorized {
		t.Fatalf("deleted key: %d", code)
	}
	if a.SessionStillValid(ctx, streamClaims) {
		t.Fatal("stream token outlived its key")
	}

	// Deleting the user deletes their keys.
	other, _ := mint("other", pgtype.Timestamptz{})
	if code, _ := call(other); code != http.StatusOK {
		t.Fatalf("second key: %d", code)
	}
	if err := q.DeleteUser(ctx, user.ID); err != nil {
		t.Fatalf("DeleteUser: %v", err)
	}
	if code, _ := call(other); code != http.StatusUnauthorized {
		t.Fatalf("deleted user's key: %d", code)
	}
}
