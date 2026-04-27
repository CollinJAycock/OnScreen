package library

import (
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// ── mocks ─────────────────────────────────────────────────────────────────────

type mockQuerier struct {
	libs      map[uuid.UUID]Library
	access    map[uuid.UUID][]uuid.UUID
	listErr   error
	createErr error
	updateErr error
	deleteErr error
	count     int64
	countErr  error
	due       []Library
	dueMeta   []Library
}

func newMockQuerier() *mockQuerier {
	return &mockQuerier{
		libs:   make(map[uuid.UUID]Library),
		access: make(map[uuid.UUID][]uuid.UUID),
	}
}

func (m *mockQuerier) GetLibrary(_ context.Context, id uuid.UUID) (Library, error) {
	if lib, ok := m.libs[id]; ok {
		return lib, nil
	}
	return Library{}, pgx.ErrNoRows
}
func (m *mockQuerier) ListLibraries(_ context.Context) ([]Library, error) {
	if m.listErr != nil {
		return nil, m.listErr
	}
	out := make([]Library, 0, len(m.libs))
	for _, l := range m.libs {
		out = append(out, l)
	}
	return out, nil
}
func (m *mockQuerier) CreateLibrary(_ context.Context, p CreateLibraryParams) (Library, error) {
	if m.createErr != nil {
		return Library{}, m.createErr
	}
	lib := Library{
		ID:        uuid.New(),
		Name:      p.Name,
		Type:      p.Type,
		Paths:     p.Paths,
		Agent:     p.Agent,
		Lang:      p.Lang,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	m.libs[lib.ID] = lib
	return lib, nil
}
func (m *mockQuerier) UpdateLibrary(_ context.Context, p UpdateLibraryParams) (Library, error) {
	if m.updateErr != nil {
		return Library{}, m.updateErr
	}
	lib, ok := m.libs[p.ID]
	if !ok {
		return Library{}, pgx.ErrNoRows
	}
	lib.Name = p.Name
	m.libs[p.ID] = lib
	return lib, nil
}
func (m *mockQuerier) SoftDeleteLibrary(_ context.Context, id uuid.UUID) error {
	if m.deleteErr != nil {
		return m.deleteErr
	}
	if _, ok := m.libs[id]; !ok {
		return pgx.ErrNoRows
	}
	delete(m.libs, id)
	return nil
}
func (m *mockQuerier) SoftDeleteMediaItemsByLibrary(_ context.Context, _ uuid.UUID) error { return nil }
func (m *mockQuerier) GrantAutoLibrariesToUser(_ context.Context, _ uuid.UUID) error      { return nil }
func (m *mockQuerier) RefreshHubRecentlyAdded(_ context.Context) error                    { return nil }
func (m *mockQuerier) MarkLibraryScanCompleted(_ context.Context, _ uuid.UUID) error      { return nil }
func (m *mockQuerier) MarkLibraryMetadataRefreshed(_ context.Context, _ uuid.UUID) error  { return nil }
func (m *mockQuerier) ListLibrariesDueForScan(_ context.Context) ([]Library, error) {
	return m.due, nil
}
func (m *mockQuerier) ListLibrariesDueForMetadataRefresh(_ context.Context) ([]Library, error) {
	return m.dueMeta, nil
}
func (m *mockQuerier) CountLibraries(_ context.Context) (int64, error) {
	return m.count, m.countErr
}
func (m *mockQuerier) ListLibraryAccessByUser(_ context.Context, userID uuid.UUID) ([]uuid.UUID, error) {
	return m.access[userID], nil
}
func (m *mockQuerier) ListAllowedLibraryIDsForUser(_ context.Context, userID uuid.UUID) ([]uuid.UUID, error) {
	ids := m.access[userID]
	out := make([]uuid.UUID, 0, len(ids))
	for _, id := range ids {
		if _, ok := m.libs[id]; ok && m.libs[id].DeletedAt == nil {
			out = append(out, id)
		}
	}
	return out, nil
}
func (m *mockQuerier) HasLibraryAccess(_ context.Context, userID, libraryID uuid.UUID) (bool, error) {
	for _, id := range m.access[userID] {
		if id == libraryID {
			return true, nil
		}
	}
	return false, nil
}
func (m *mockQuerier) GrantLibraryAccess(_ context.Context, userID, libraryID uuid.UUID) error {
	if m.access == nil {
		m.access = make(map[uuid.UUID][]uuid.UUID)
	}
	for _, id := range m.access[userID] {
		if id == libraryID {
			return nil
		}
	}
	m.access[userID] = append(m.access[userID], libraryID)
	return nil
}
func (m *mockQuerier) RevokeAllLibraryAccessForUser(_ context.Context, userID uuid.UUID) error {
	delete(m.access, userID)
	return nil
}

type mockEnqueuer struct {
	called bool
	err    error
}

func (e *mockEnqueuer) EnqueueScan(_ context.Context, _ uuid.UUID) error {
	e.called = true
	return e.err
}

func newService(t *testing.T) (*Service, *mockQuerier, *mockEnqueuer) {
	t.Helper()
	q := newMockQuerier()
	enq := &mockEnqueuer{}
	svc := NewService(q, q, enq, slog.Default())
	return svc, q, enq
}

// ── tests ─────────────────────────────────────────────────────────────────────

func TestGet_Found(t *testing.T) {
	svc, q, _ := newService(t)
	id := uuid.New()
	q.libs[id] = Library{ID: id, Name: "Movies", Type: "movie"}

	lib, err := svc.Get(context.Background(), id)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if lib.Name != "Movies" {
		t.Errorf("want name Movies, got %s", lib.Name)
	}
}

func TestGet_NotFound(t *testing.T) {
	svc, _, _ := newService(t)
	_, err := svc.Get(context.Background(), uuid.New())
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("want ErrNotFound, got %v", err)
	}
}

func TestList_Empty(t *testing.T) {
	svc, _, _ := newService(t)
	libs, err := svc.List(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(libs) != 0 {
		t.Errorf("want 0 libs, got %d", len(libs))
	}
}

func TestList_ReturnsAll(t *testing.T) {
	svc, q, _ := newService(t)
	q.libs[uuid.New()] = Library{ID: uuid.New(), Name: "A"}
	q.libs[uuid.New()] = Library{ID: uuid.New(), Name: "B"}

	libs, err := svc.List(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(libs) != 2 {
		t.Errorf("want 2 libs, got %d", len(libs))
	}
}

func TestList_Error(t *testing.T) {
	svc, q, _ := newService(t)
	q.listErr = errors.New("db down")
	_, err := svc.List(context.Background())
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestCreate_Success(t *testing.T) {
	svc, _, enq := newService(t)
	lib, err := svc.Create(context.Background(), CreateLibraryParams{
		Name:  "Movies",
		Type:  "movie",
		Paths: []string{"/media/movies"},
		Agent: "tmdb",
		Lang:  "en",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if lib.Name != "Movies" {
		t.Errorf("want name Movies, got %s", lib.Name)
	}
	if !enq.called {
		t.Error("expected EnqueueScan to be called")
	}
}

func TestCreate_ValidatesNameRequired(t *testing.T) {
	svc, _, _ := newService(t)
	_, err := svc.Create(context.Background(), CreateLibraryParams{
		Type:  "movie",
		Paths: []string{"/media"},
	})
	var ve *ValidationError
	if !errors.As(err, &ve) {
		t.Fatalf("expected ValidationError, got %v", err)
	}
	if ve.Field != "name" {
		t.Errorf("want field=name, got %s", ve.Field)
	}
}

func TestCreate_ValidatesTypeInvalid(t *testing.T) {
	svc, _, _ := newService(t)
	_, err := svc.Create(context.Background(), CreateLibraryParams{
		Name:  "Test",
		Type:  "bogus",
		Paths: []string{"/media"},
	})
	var ve *ValidationError
	if !errors.As(err, &ve) {
		t.Fatalf("expected ValidationError, got %v", err)
	}
	if ve.Field != "type" {
		t.Errorf("want field=type, got %s", ve.Field)
	}
}

func TestCreate_ValidatesPathsRequired(t *testing.T) {
	svc, _, _ := newService(t)
	_, err := svc.Create(context.Background(), CreateLibraryParams{
		Name:  "Test",
		Type:  "movie",
		Paths: nil,
	})
	var ve *ValidationError
	if !errors.As(err, &ve) {
		t.Fatalf("expected ValidationError, got %v", err)
	}
	if ve.Field != "scan_paths" {
		t.Errorf("want field=scan_paths, got %s", ve.Field)
	}
}

func TestCreate_DBError(t *testing.T) {
	svc, q, _ := newService(t)
	q.createErr = errors.New("constraint violation")
	_, err := svc.Create(context.Background(), CreateLibraryParams{
		Name:  "Movies",
		Type:  "movie",
		Paths: []string{"/media"},
	})
	if err == nil {
		t.Fatal("expected error from DB")
	}
}

func TestCreate_EnqueueErrorNonFatal(t *testing.T) {
	svc, _, enq := newService(t)
	enq.err = errors.New("queue full")
	// Should still return the library even if enqueue fails.
	lib, err := svc.Create(context.Background(), CreateLibraryParams{
		Name:  "Shows",
		Type:  "show",
		Paths: []string{"/media/shows"},
	})
	if err != nil {
		t.Fatalf("expected nil error despite enqueue failure, got %v", err)
	}
	if lib.Name != "Shows" {
		t.Errorf("want name Shows, got %s", lib.Name)
	}
}

func TestUpdate_Success(t *testing.T) {
	svc, q, _ := newService(t)
	id := uuid.New()
	q.libs[id] = Library{ID: id, Name: "Old Name", Type: "movie"}

	lib, err := svc.Update(context.Background(), UpdateLibraryParams{
		ID:   id,
		Name: "New Name",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if lib.Name != "New Name" {
		t.Errorf("want name New Name, got %s", lib.Name)
	}
}

func TestUpdate_NotFound(t *testing.T) {
	svc, _, _ := newService(t)
	_, err := svc.Update(context.Background(), UpdateLibraryParams{ID: uuid.New(), Name: "X"})
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("want ErrNotFound, got %v", err)
	}
}

func TestDelete_Success(t *testing.T) {
	svc, q, _ := newService(t)
	id := uuid.New()
	q.libs[id] = Library{ID: id, Name: "Movies"}

	if err := svc.Delete(context.Background(), id); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := q.libs[id]; ok {
		t.Error("expected library to be removed from map")
	}
}

func TestDelete_NotFound(t *testing.T) {
	svc, _, _ := newService(t)
	err := svc.Delete(context.Background(), uuid.New())
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("want ErrNotFound, got %v", err)
	}
}

func TestEnqueueScan_LibraryNotFound(t *testing.T) {
	svc, _, _ := newService(t)
	err := svc.EnqueueScan(context.Background(), uuid.New())
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("want ErrNotFound, got %v", err)
	}
}

func TestEnqueueScan_EnqueueError(t *testing.T) {
	svc, q, enq := newService(t)
	id := uuid.New()
	q.libs[id] = Library{ID: id, Name: "Movies"}
	enq.err = errors.New("queue error")

	err := svc.EnqueueScan(context.Background(), id)
	if err == nil {
		t.Fatal("expected error from enqueue")
	}
}

func TestEnqueueScan_Success(t *testing.T) {
	svc, q, enq := newService(t)
	id := uuid.New()
	q.libs[id] = Library{ID: id, Name: "Movies"}

	if err := svc.EnqueueScan(context.Background(), id); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !enq.called {
		t.Error("expected EnqueueScan to be called")
	}
}

func TestIsSetupRequired_True(t *testing.T) {
	svc, q, _ := newService(t)
	q.count = 0
	required, err := svc.IsSetupRequired(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !required {
		t.Error("want true when no libraries")
	}
}

func TestIsSetupRequired_False(t *testing.T) {
	svc, q, _ := newService(t)
	q.count = 2
	required, err := svc.IsSetupRequired(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if required {
		t.Error("want false when libraries exist")
	}
}

func TestIsSetupRequired_CountError(t *testing.T) {
	svc, q, _ := newService(t)
	q.countErr = errors.New("db down")
	_, err := svc.IsSetupRequired(context.Background())
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestValidationError_String(t *testing.T) {
	ve := &ValidationError{Field: "name", Message: "required"}
	want := "validation: name: required"
	if got := ve.Error(); got != want {
		t.Errorf("want %q, got %q", want, got)
	}
}

func TestMapNotFound_NilPassthrough(t *testing.T) {
	if got := mapNotFound(nil); got != nil {
		t.Errorf("want nil, got %v", got)
	}
}

func TestMapNotFound_OtherErrorPassthrough(t *testing.T) {
	other := errors.New("connection refused")
	got := mapNotFound(other)
	if got != other {
		t.Errorf("want original error, got %v", got)
	}
}

func TestMapNotFound_NoRowsBecomesErrNotFound(t *testing.T) {
	err := pgx.ErrNoRows
	got := mapNotFound(err)
	if !errors.Is(got, ErrNotFound) {
		t.Errorf("want ErrNotFound, got %v", got)
	}
}
