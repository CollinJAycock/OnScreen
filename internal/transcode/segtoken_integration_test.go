package transcode

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/testvalkey"
)

func TestIntegration_SegmentToken_IssueValidateRevoke(t *testing.T) {
	v := testvalkey.New(t)
	mgr := NewSegmentTokenManager(v)
	ctx := context.Background()

	sessionID := NewSessionID()
	userID := uuid.New()

	token, err := mgr.Issue(ctx, sessionID, userID)
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	if token == "" {
		t.Fatal("expected non-empty token")
	}

	gotSession, gotUser, err := mgr.Validate(ctx, token)
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if gotSession != sessionID {
		t.Errorf("want sessionID %s, got %s", sessionID, gotSession)
	}
	if gotUser != userID {
		t.Errorf("want userID %s, got %s", userID, gotUser)
	}

	if err := mgr.Revoke(ctx, token); err != nil {
		t.Fatalf("Revoke: %v", err)
	}

	_, _, err = mgr.Validate(ctx, token)
	if err == nil {
		t.Error("expected error after Revoke, got nil")
	}
}

func TestIntegration_SegmentToken_InvalidToken(t *testing.T) {
	v := testvalkey.New(t)
	mgr := NewSegmentTokenManager(v)
	ctx := context.Background()

	_, _, err := mgr.Validate(ctx, "does-not-exist")
	if err == nil {
		t.Error("expected error for unknown token, got nil")
	}
}

func TestIntegration_SegmentToken_IssueMultiple(t *testing.T) {
	v := testvalkey.New(t)
	mgr := NewSegmentTokenManager(v)
	ctx := context.Background()

	sessionID := NewSessionID()
	userID := uuid.New()

	tokens := make(map[string]bool)
	for i := 0; i < 5; i++ {
		token, err := mgr.Issue(ctx, sessionID, userID)
		if err != nil {
			t.Fatalf("Issue %d: %v", i, err)
		}
		if tokens[token] {
			t.Errorf("duplicate token issued: %s", token)
		}
		tokens[token] = true
	}
}
