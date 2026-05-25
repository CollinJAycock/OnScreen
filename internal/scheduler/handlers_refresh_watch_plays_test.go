package scheduler

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
)

type fakeRefresher struct {
	gotSQL string
	err    error
}

func (f *fakeRefresher) Exec(_ context.Context, sql string, _ ...any) (pgconn.CommandTag, error) {
	f.gotSQL = sql
	return pgconn.CommandTag{}, f.err
}

func TestRefreshWatchPlaysHandler_RunsConcurrentRefresh(t *testing.T) {
	f := &fakeRefresher{}
	h := NewRefreshWatchPlaysHandler(f, nil)

	out, err := h.Run(context.Background(), nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if out == "" {
		t.Error("expected a non-empty output summary")
	}
	if f.gotSQL != "REFRESH MATERIALIZED VIEW CONCURRENTLY public.watch_plays" {
		t.Errorf("unexpected SQL: %q", f.gotSQL)
	}
}

func TestRefreshWatchPlaysHandler_PropagatesError(t *testing.T) {
	f := &fakeRefresher{err: errors.New("boom")}
	h := NewRefreshWatchPlaysHandler(f, nil)

	if _, err := h.Run(context.Background(), nil); err == nil {
		t.Fatal("expected error to propagate")
	}
}
