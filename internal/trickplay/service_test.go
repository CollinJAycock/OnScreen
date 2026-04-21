package trickplay

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/db/gen"
)

type fakeStatusReader struct {
	row    gen.TrickplayStatus
	exists bool
	err    error
}

func (f fakeStatusReader) Status(_ context.Context, _ uuid.UUID) (gen.TrickplayStatus, bool, error) {
	return f.row, f.exists, f.err
}

func newServiceWithReader(reader statusReader) *Service {
	g := New("/var/cache/tp", &fakeStore{}, fakeLookup{}, silentLogger())
	return &Service{gen: g, store: reader}
}

func TestServiceStatusReturnsZeroWhenNoRow(t *testing.T) {
	svc := newServiceWithReader(fakeStatusReader{exists: false})
	spec, status, count, exists, err := svc.Status(context.Background(), uuid.New())
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if exists {
		t.Error("exists should be false")
	}
	if status != "" || count != 0 || spec != (Spec{}) {
		t.Errorf("non-zero return for absent row: spec=%+v status=%q count=%d", spec, status, count)
	}
}

func TestServiceStatusPropagatesError(t *testing.T) {
	svc := newServiceWithReader(fakeStatusReader{err: errors.New("db down")})
	_, _, _, exists, err := svc.Status(context.Background(), uuid.New())
	if err == nil {
		t.Fatal("expected error")
	}
	if exists {
		t.Error("exists should be false on error")
	}
}

func TestServiceStatusMapsRowFields(t *testing.T) {
	svc := newServiceWithReader(fakeStatusReader{
		exists: true,
		row: gen.TrickplayStatus{
			Status:      "done",
			SpriteCount: 7,
			IntervalSec: 10,
			ThumbWidth:  320,
			ThumbHeight: 180,
			GridCols:    10,
			GridRows:    10,
		},
	})
	spec, status, count, exists, err := svc.Status(context.Background(), uuid.New())
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if !exists {
		t.Fatal("exists should be true")
	}
	if status != "done" || count != 7 {
		t.Errorf("status=%q count=%d, want done/7", status, count)
	}
	want := Spec{IntervalSec: 10, ThumbWidth: 320, ThumbHeight: 180, GridCols: 10, GridRows: 10}
	if spec != want {
		t.Errorf("spec = %+v, want %+v", spec, want)
	}
}

func TestServiceItemDirDelegatesToGenerator(t *testing.T) {
	svc := newServiceWithReader(fakeStatusReader{})
	id := uuid.New()
	if got, want := svc.ItemDir(id), filepath.Join("/var/cache/tp", id.String()); got != want {
		t.Errorf("ItemDir = %q, want %q", got, want)
	}
}

func TestServiceGenerateDelegatesToGenerator(t *testing.T) {
	store := &fakeStore{}
	g := New(t.TempDir(), store, fakeLookup{path: "", duration: 0}, silentLogger())
	svc := &Service{gen: g, store: fakeStatusReader{}}
	// PrimaryFile returns empty path → ErrNoFile, but the call is still routed.
	err := svc.Generate(context.Background(), uuid.New())
	if !errors.Is(err, ErrNoFile) {
		t.Errorf("Generate should propagate ErrNoFile from generator: got %v", err)
	}
}

func TestNewServiceWiresUpStore(t *testing.T) {
	g := New("/tmp", &fakeStore{}, fakeLookup{}, silentLogger())
	store := &PgStore{} // pool is nil but NewService doesn't dereference it
	svc := NewService(g, store)
	if svc == nil || svc.gen != g || svc.store != store {
		t.Errorf("NewService didn't wire fields: %+v", svc)
	}
}
