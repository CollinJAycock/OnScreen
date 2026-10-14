package scheduler

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/faces"
)

type fakeFaceStore struct {
	queue    []FacePhoto
	saved    map[uuid.UUID][]faces.Box
	failed   []uuid.UUID
	gotLimit int32
	gotLib   *uuid.UUID
}

func (f *fakeFaceStore) ListPhotosForFaceScan(_ context.Context, lib *uuid.UUID, limit int32) ([]FacePhoto, error) {
	f.gotLimit, f.gotLib = limit, lib
	if int(limit) < len(f.queue) {
		return f.queue[:limit], nil
	}
	return f.queue, nil
}

func (f *fakeFaceStore) CountPhotosForFaceScan(context.Context, *uuid.UUID) (int64, error) {
	return int64(len(f.queue) - len(f.saved) - len(f.failed)), nil
}

func (f *fakeFaceStore) SaveDetectedFaces(_ context.Context, p FacePhoto, boxes []faces.Box) error {
	if f.saved == nil {
		f.saved = map[uuid.UUID][]faces.Box{}
	}
	f.saved[p.ItemID] = boxes
	return nil
}

func (f *fakeFaceStore) RecordFaceScanFailure(_ context.Context, p FacePhoto) error {
	f.failed = append(f.failed, p.ItemID)
	return nil
}

func TestFaceDetection_DrainsQueueAndRecordsFailures(t *testing.T) {
	a, b, c := uuid.New(), uuid.New(), uuid.New()
	store := &fakeFaceStore{queue: []FacePhoto{{ItemID: a}, {ItemID: b}, {ItemID: c}}}
	detect := func(_ context.Context, id uuid.UUID) ([]faces.Box, error) {
		switch id {
		case a:
			return []faces.Box{{X: 0.1, Y: 0.1, W: 0.2, H: 0.2, Score: 9}}, nil
		case b:
			return nil, errors.New("decode failed")
		}
		return nil, nil
	}
	h := NewFaceDetectionHandler(store, detect, slog.New(slog.NewTextHandler(io.Discard, nil)))
	out, err := h.Run(context.Background(), json.RawMessage(`{"limit":10}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(store.saved[a]) != 1 || len(store.failed) != 1 || store.failed[0] != b {
		t.Fatalf("saved %v failed %v", store.saved, store.failed)
	}
	if _, ok := store.saved[c]; !ok {
		t.Fatal("a photo with no faces must still be recorded as scanned")
	}
	if !strings.Contains(out, "photos=2 faces=1 failed=1") {
		t.Fatalf("summary %q", out)
	}
	if store.gotLimit != 10 {
		t.Fatalf("limit %d", store.gotLimit)
	}
}

func TestFaceDetection_StopsAtTimeBudget(t *testing.T) {
	store := &fakeFaceStore{queue: []FacePhoto{{ItemID: uuid.New()}, {ItemID: uuid.New()}}}
	h := NewFaceDetectionHandler(store, func(context.Context, uuid.UUID) ([]faces.Box, error) { return nil, nil },
		slog.New(slog.NewTextHandler(io.Discard, nil)))
	start := time.Now()
	calls := 0
	h.now = func() time.Time {
		calls++
		// The first call sets the deadline; every later one is past it.
		if calls == 1 {
			return start
		}
		return start.Add(time.Hour)
	}
	if _, err := h.Run(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	if len(store.saved) != 0 {
		t.Fatalf("ran past the budget: %v", store.saved)
	}
}

func TestFaceDetection_BadConfig(t *testing.T) {
	h := NewFaceDetectionHandler(&fakeFaceStore{}, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if _, err := h.Run(context.Background(), json.RawMessage(`{"library_id":"nope"}`)); err == nil {
		t.Fatal("accepted a bad library_id")
	}
}
