package scheduler

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/onscreen/onscreen/internal/franchise"
)

type stubFranchise struct {
	got franchise.MaintainOptions
	res franchise.MaintainResult
	err error
}

func (s *stubFranchise) Maintain(_ context.Context, opts franchise.MaintainOptions) (franchise.MaintainResult, error) {
	s.got = opts
	return s.res, s.err
}

func TestFranchiseCollectionsHandler_PassesConfig(t *testing.T) {
	stub := &stubFranchise{res: franchise.MaintainResult{Checked: 3, Synced: 2}}
	h := NewFranchiseCollectionsHandler(stub, slog.Default())
	out, err := h.Run(context.Background(), json.RawMessage(`{"backfill_limit":50,"refresh_limit":5,"fetch_budget":10}`))
	if err != nil {
		t.Fatal(err)
	}
	if stub.got.BackfillLimit != 50 || stub.got.RefreshLimit != 5 || stub.got.FetchBudget != 10 {
		t.Fatalf("options = %+v", stub.got)
	}
	if !strings.Contains(out, "checked=3") || !strings.Contains(out, "synced=2") {
		t.Fatalf("summary = %q", out)
	}
}

func TestFranchiseCollectionsHandler_DefaultsOnEmptyConfig(t *testing.T) {
	stub := &stubFranchise{}
	h := NewFranchiseCollectionsHandler(stub, slog.Default())
	for _, raw := range []json.RawMessage{nil, json.RawMessage(`null`), json.RawMessage(`{}`)} {
		if _, err := h.Run(context.Background(), raw); err != nil {
			t.Fatalf("%s: %v", raw, err)
		}
		if stub.got != (franchise.MaintainOptions{}) {
			t.Fatalf("%s: zero options expected (service applies defaults), got %+v", raw, stub.got)
		}
	}
}

func TestFranchiseCollectionsHandler_Errors(t *testing.T) {
	h := NewFranchiseCollectionsHandler(&stubFranchise{}, slog.Default())
	if _, err := h.Run(context.Background(), json.RawMessage(`{bad`)); err == nil {
		t.Fatal("bad config must error")
	}
	h = NewFranchiseCollectionsHandler(&stubFranchise{err: errors.New("db down")}, slog.Default())
	if _, err := h.Run(context.Background(), nil); err == nil {
		t.Fatal("service error must propagate")
	}
}
