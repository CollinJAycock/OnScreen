package ratings

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/onscreen/onscreen/internal/db/gen"
)

// fakeQ is an in-memory Querier for exercising the service without a DB.
type fakeQ struct {
	store     map[[2]uuid.UUID]float64
	upserts   int
	deletes   int
	commAvg   float64
	commCount int64
}

func newFakeQ() *fakeQ { return &fakeQ{store: map[[2]uuid.UUID]float64{}} }

func k(u, m uuid.UUID) [2]uuid.UUID { return [2]uuid.UUID{u, m} }

func (f *fakeQ) UpsertUserRating(_ context.Context, arg gen.UpsertUserRatingParams) error {
	f.upserts++
	f.store[k(arg.UserID, arg.MediaItemID)] = arg.Score
	return nil
}

func (f *fakeQ) GetUserRating(_ context.Context, arg gen.GetUserRatingParams) (float64, error) {
	if s, ok := f.store[k(arg.UserID, arg.MediaItemID)]; ok {
		return s, nil
	}
	return 0, pgx.ErrNoRows
}

func (f *fakeQ) DeleteUserRating(_ context.Context, arg gen.DeleteUserRatingParams) error {
	f.deletes++
	delete(f.store, k(arg.UserID, arg.MediaItemID))
	return nil
}

func (f *fakeQ) GetCommunityRating(_ context.Context, _ uuid.UUID) (gen.GetCommunityRatingRow, error) {
	return gen.GetCommunityRatingRow{Average: f.commAvg, Count: f.commCount}, nil
}

func TestService_SetGetClear(t *testing.T) {
	s := New(newFakeQ())
	u, m := uuid.New(), uuid.New()
	ctx := context.Background()

	if _, err := s.Get(ctx, u, m); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get unrated: want ErrNotFound, got %v", err)
	}
	if err := s.Set(ctx, u, m, 8); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if got, err := s.Get(ctx, u, m); err != nil || got != 8 {
		t.Fatalf("Get after set: got %v, %v; want 8, nil", got, err)
	}
	if err := s.Clear(ctx, u, m); err != nil {
		t.Fatalf("Clear: %v", err)
	}
	if _, err := s.Get(ctx, u, m); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get after clear: want ErrNotFound, got %v", err)
	}
}

func TestService_SetRejectsOutOfRange(t *testing.T) {
	q := newFakeQ()
	s := New(q)
	ctx := context.Background()
	for _, bad := range []float64{-0.5, 10.5, 100} {
		if err := s.Set(ctx, uuid.New(), uuid.New(), bad); err == nil {
			t.Errorf("Set(%v): want range error, got nil", bad)
		}
	}
	if q.upserts != 0 {
		t.Errorf("rejected scores must not upsert; got %d upserts", q.upserts)
	}
	for _, ok := range []float64{0, 10, 5.5} { // boundaries + half-step allowed
		if err := s.Set(ctx, uuid.New(), uuid.New(), ok); err != nil {
			t.Errorf("Set(%v): want nil, got %v", ok, err)
		}
	}
}

func TestService_CommunityAverage(t *testing.T) {
	q := newFakeQ()
	q.commAvg, q.commCount = 7.5, 4
	avg, count, err := New(q).CommunityAverage(context.Background(), uuid.New())
	if err != nil || avg != 7.5 || count != 4 {
		t.Fatalf("CommunityAverage: got %v, %v, %v; want 7.5, 4, nil", avg, count, err)
	}
}
