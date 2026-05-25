package scheduler

import (
	"context"
	"errors"
	"testing"
)

type fakeStaticRunner struct {
	summary string
	err     error
	calls   int
}

func (f *fakeStaticRunner) RunOnce(context.Context) (string, error) {
	f.calls++
	return f.summary, f.err
}

func TestNewStaticABRPreencodeHandler_RunsAndReturnsSummary(t *testing.T) {
	r := &fakeStaticRunner{summary: "static-abr: 3 candidates, 1 planned, 1 encoded"}
	h := NewStaticABRPreencodeHandler(r)
	got, err := h(context.Background(), nil)
	if err != nil {
		t.Fatalf("handler: %v", err)
	}
	if got != r.summary {
		t.Errorf("summary = %q, want %q", got, r.summary)
	}
	if r.calls != 1 {
		t.Errorf("RunOnce called %d times, want 1", r.calls)
	}
}

func TestNewStaticABRPreencodeHandler_PropagatesError(t *testing.T) {
	h := NewStaticABRPreencodeHandler(&fakeStaticRunner{err: errors.New("boom")})
	if _, err := h(context.Background(), nil); err == nil {
		t.Error("expected the runner's error to propagate")
	}
}
