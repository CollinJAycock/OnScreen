package observability

import (
	"context"
	"errors"
	"testing"
	"testing/fstest"
)

type stubVQ struct {
	v   int64
	err error
}

func (s stubVQ) MaxAppliedVersion(_ context.Context) (int64, error) { return s.v, s.err }

func TestCheckMigrations_caughtUp(t *testing.T) {
	fs := fstest.MapFS{
		"00001_init.sql":     {Data: []byte("-- +goose Up")},
		"00002_things.sql":   {Data: []byte("-- +goose Up")},
		"00003_more.sql":     {Data: []byte("-- +goose Up")},
		"README.md":          {Data: []byte("not a migration")},
		"99_unparseable.sql": {Data: []byte("missing leading zeros, no underscore version-prefix issue")},
	}
	st, err := CheckMigrations(context.Background(), stubVQ{v: 3}, fs)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if st.Expected != 99 {
		t.Errorf("expected=%d, want 99 (highest parseable prefix)", st.Expected)
	}
	if st.Applied != 3 {
		t.Errorf("applied=%d, want 3", st.Applied)
	}
	if st.Pending != 96 {
		t.Errorf("pending=%d, want 96", st.Pending)
	}
}

func TestCheckMigrations_pending(t *testing.T) {
	fs := fstest.MapFS{
		"00001_a.sql": {},
		"00026_b.sql": {},
	}
	st, _ := CheckMigrations(context.Background(), stubVQ{v: 24}, fs)
	if st.Pending != 2 {
		t.Errorf("pending=%d, want 2", st.Pending)
	}
}

func TestCheckMigrations_dbAheadOfCode(t *testing.T) {
	// Operator manually applied a newer migration, or rolling back code —
	// don't report negative pending; treat as caught up.
	fs := fstest.MapFS{"00001_a.sql": {}}
	st, _ := CheckMigrations(context.Background(), stubVQ{v: 5}, fs)
	if st.Pending != 0 {
		t.Errorf("pending=%d on db-ahead-of-code, want 0", st.Pending)
	}
}

func TestCheckMigrations_querierError(t *testing.T) {
	fs := fstest.MapFS{"00001_a.sql": {}}
	_, err := CheckMigrations(context.Background(), stubVQ{err: errors.New("boom")}, fs)
	if err == nil {
		t.Fatal("want error from failing version querier")
	}
}
