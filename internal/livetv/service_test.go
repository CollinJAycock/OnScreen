package livetv

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
)

// newService builds a Service backed by a fresh mockQuerier and an
// empty driver registry. Returned for tests that need both handles.
func newService(t *testing.T) (*Service, *mockQuerier) {
	t.Helper()
	q := newMockQuerier()
	return NewService(q, NewRegistry(), slog.Default()), q
}

// ── Mock Querier ─────────────────────────────────────────────────────────────

type mockQuerier struct {
	mu sync.Mutex

	tuners      map[uuid.UUID]TunerDevice
	channels    map[uuid.UUID]Channel
	upsertCount int
	touchCount  int
	updateCount int

	createTunerErr   error
	getTunerErr      error
	getChannelErr    error
	upsertChannelErr error

	guideRows []EPGProgram

	epgSources       []EPGSource
	unmapped         []Channel
	epgIDSets        []epgIDSet
	upsertedPrograms []UpsertEPGProgramParams
	knownEPGIDs      []string
	sortOrderSets    []sortOrderSet
}

type sortOrderSet struct {
	ID        uuid.UUID
	SortOrder int32
}

func newMockQuerier() *mockQuerier {
	return &mockQuerier{
		tuners:   make(map[uuid.UUID]TunerDevice),
		channels: make(map[uuid.UUID]Channel),
	}
}

func (m *mockQuerier) CreateTunerDevice(_ context.Context, p CreateTunerDeviceParams) (TunerDevice, error) {
	if m.createTunerErr != nil {
		return TunerDevice{}, m.createTunerErr
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	t := TunerDevice{
		ID: uuid.New(), Type: p.Type, Name: p.Name,
		Config: p.Config, TuneCount: p.TuneCount, Enabled: true,
	}
	m.tuners[t.ID] = t
	return t, nil
}

func (m *mockQuerier) GetTunerDevice(_ context.Context, id uuid.UUID) (TunerDevice, error) {
	if m.getTunerErr != nil {
		return TunerDevice{}, m.getTunerErr
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	t, ok := m.tuners[id]
	if !ok {
		return TunerDevice{}, errors.New("not found")
	}
	return t, nil
}

func (m *mockQuerier) ListTunerDevices(_ context.Context) ([]TunerDevice, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]TunerDevice, 0, len(m.tuners))
	for _, t := range m.tuners {
		out = append(out, t)
	}
	return out, nil
}

func (m *mockQuerier) UpdateTunerDevice(_ context.Context, p UpdateTunerDeviceParams) (TunerDevice, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.updateCount++
	t := m.tuners[p.ID]
	t.Name, t.Config, t.TuneCount = p.Name, p.Config, p.TuneCount
	m.tuners[p.ID] = t
	return t, nil
}

func (m *mockQuerier) SetTunerEnabled(_ context.Context, id uuid.UUID, enabled bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	t := m.tuners[id]
	t.Enabled = enabled
	m.tuners[id] = t
	return nil
}

func (m *mockQuerier) TouchTunerLastSeen(_ context.Context, _ uuid.UUID) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.touchCount++
	return nil
}

func (m *mockQuerier) DeleteTunerDevice(_ context.Context, id uuid.UUID) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.tuners, id)
	return nil
}

func (m *mockQuerier) UpsertChannel(_ context.Context, p UpsertChannelParams) (Channel, error) {
	if m.upsertChannelErr != nil {
		return Channel{}, m.upsertChannelErr
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.upsertCount++
	c := Channel{
		ID: uuid.New(), TunerID: p.TunerID, Number: p.Number,
		Callsign: p.Callsign, Name: p.Name, LogoURL: p.LogoURL, Enabled: true,
	}
	m.channels[c.ID] = c
	return c, nil
}

func (m *mockQuerier) GetChannel(_ context.Context, id uuid.UUID) (Channel, error) {
	if m.getChannelErr != nil {
		return Channel{}, m.getChannelErr
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	c, ok := m.channels[id]
	if !ok {
		return Channel{}, errors.New("not found")
	}
	return c, nil
}

func (m *mockQuerier) ListChannels(_ context.Context, _ *bool) ([]ChannelWithTuner, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]ChannelWithTuner, 0, len(m.channels))
	for _, c := range m.channels {
		out = append(out, ChannelWithTuner{Channel: c})
	}
	return out, nil
}

func (m *mockQuerier) ListChannelsByTuner(_ context.Context, _ uuid.UUID) ([]Channel, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Channel, 0, len(m.channels))
	for _, c := range m.channels {
		out = append(out, c)
	}
	return out, nil
}

func (m *mockQuerier) SetChannelEnabled(_ context.Context, _ uuid.UUID, _ bool) error {
	return nil
}

func (m *mockQuerier) SetChannelSortOrder(_ context.Context, id uuid.UUID, sortOrder int32) error {
	m.sortOrderSets = append(m.sortOrderSets, sortOrderSet{ID: id, SortOrder: sortOrder})
	return nil
}

func (m *mockQuerier) GetNowAndNextForChannels(_ context.Context) ([]NowNextEntry, error) {
	return nil, nil
}

func (m *mockQuerier) ListEPGProgramsInWindow(_ context.Context, _, _ time.Time) ([]EPGProgram, error) {
	return m.guideRows, nil
}

// EPG sources + ingestion — minimal stubs; tests that need behavior here
// will set the corresponding result/err fields.
func (m *mockQuerier) ListEPGSources(_ context.Context) ([]EPGSource, error) {
	return m.epgSources, nil
}
func (m *mockQuerier) GetEPGSource(_ context.Context, id uuid.UUID) (EPGSource, error) {
	for _, s := range m.epgSources {
		if s.ID == id {
			return s, nil
		}
	}
	return EPGSource{}, errors.New("not found")
}
func (m *mockQuerier) CreateEPGSource(_ context.Context, p CreateEPGSourceParams) (EPGSource, error) {
	src := EPGSource{ID: uuid.New(), Type: p.Type, Name: p.Name, Config: p.Config, RefreshIntervalMin: p.RefreshIntervalMin, Enabled: true}
	m.epgSources = append(m.epgSources, src)
	return src, nil
}
func (m *mockQuerier) DeleteEPGSource(_ context.Context, id uuid.UUID) error {
	out := m.epgSources[:0]
	for _, s := range m.epgSources {
		if s.ID != id {
			out = append(out, s)
		}
	}
	m.epgSources = out
	return nil
}
func (m *mockQuerier) SetEPGSourceEnabled(_ context.Context, _ uuid.UUID, _ bool) error { return nil }
func (m *mockQuerier) RecordEPGPull(_ context.Context, _ uuid.UUID, _ *string) error    { return nil }
func (m *mockQuerier) ListUnmappedChannels(_ context.Context) ([]Channel, error) {
	return m.unmapped, nil
}
func (m *mockQuerier) SetChannelEPGID(_ context.Context, id uuid.UUID, epg *string) error {
	m.epgIDSets = append(m.epgIDSets, epgIDSet{ID: id, EPGID: epg})
	return nil
}
func (m *mockQuerier) GetChannelByEPGID(_ context.Context, epg string) (Channel, error) {
	for _, ch := range m.channels {
		if ch.EPGChannelID != nil && *ch.EPGChannelID == epg {
			return ch, nil
		}
	}
	return Channel{}, errors.New("not found")
}
func (m *mockQuerier) UpsertEPGProgram(_ context.Context, p UpsertEPGProgramParams) error {
	m.upsertedPrograms = append(m.upsertedPrograms, p)
	return nil
}
func (m *mockQuerier) TrimOldEPGPrograms(_ context.Context) error { return nil }
func (m *mockQuerier) ListAllKnownEPGChannelIDs(_ context.Context) ([]string, error) {
	return m.knownEPGIDs, nil
}

type epgIDSet struct {
	ID    uuid.UUID
	EPGID *string
}

// ── Fake Driver ──────────────────────────────────────────────────────────────

type fakeDriver struct {
	channels   []DiscoveredChannel
	tuneCount  int
	streamBody string
	openErr    error
}

func (f *fakeDriver) Type() TunerType { return "fake" }
func (f *fakeDriver) TuneCount() int  { return f.tuneCount }
func (f *fakeDriver) Discover(_ context.Context) ([]DiscoveredChannel, error) {
	return f.channels, nil
}
func (f *fakeDriver) Probe(_ context.Context) error { return nil }
func (f *fakeDriver) OpenStream(_ context.Context, _ string) (Stream, error) {
	if f.openErr != nil {
		return nil, f.openErr
	}
	return io.NopCloser(strings.NewReader(f.streamBody)), nil
}

func newServiceWithFakeDriver(t *testing.T, drv *fakeDriver) (*Service, *mockQuerier) {
	t.Helper()
	q := newMockQuerier()
	r := NewRegistry()
	r.Register("fake", func(_ string, _ []byte) (Driver, error) { return drv, nil })
	return NewService(q, r, slog.Default()), q
}

// ── Tests ────────────────────────────────────────────────────────────────────

func TestService_CreateTuner_DiscoversAndPopulatesChannels(t *testing.T) {
	drv := &fakeDriver{
		channels: []DiscoveredChannel{
			{Number: "5.1", Name: "WCBS"},
			{Number: "7.1", Name: "WABC"},
		},
		tuneCount: 4,
	}
	svc, q := newServiceWithFakeDriver(t, drv)

	row, err := svc.CreateTuner(context.Background(), CreateTunerDeviceParams{
		Type: "fake", Name: "box",
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if row.ID == uuid.Nil {
		t.Error("expected ID set")
	}
	if q.upsertCount != 2 {
		t.Errorf("channel upserts: got %d, want 2", q.upsertCount)
	}
	if q.touchCount != 1 {
		t.Errorf("last_seen touch: got %d, want 1", q.touchCount)
	}
}

func TestService_CreateTuner_PersistsEvenIfDiscoverFails(t *testing.T) {
	// Driver factory returns a driver whose Discover errors. We still want
	// the row created so the user can retry from the settings UI.
	drv := &fakeDriver{} // empty channels = "discover succeeds with 0"
	svc, q := newServiceWithFakeDriver(t, drv)
	q.upsertChannelErr = errors.New("disk full")

	// 0 channels means upsert never runs; force a discover with at least
	// one channel so the upsert error path is exercised.
	drv.channels = []DiscoveredChannel{{Number: "5.1", Name: "WCBS"}}

	row, err := svc.CreateTuner(context.Background(), CreateTunerDeviceParams{
		Type: "fake", Name: "box",
	})
	// Discover failure is logged but doesn't fail CreateTuner.
	if err != nil {
		t.Fatalf("create should not error on discover failure: %v", err)
	}
	if row.ID == uuid.Nil {
		t.Error("row should still be created")
	}
}

func TestService_RescanTuner_RunsDiscover(t *testing.T) {
	drv := &fakeDriver{
		channels: []DiscoveredChannel{{Number: "5.1", Name: "WCBS"}},
	}
	svc, q := newServiceWithFakeDriver(t, drv)
	row, _ := svc.CreateTuner(context.Background(), CreateTunerDeviceParams{
		Type: "fake", Name: "box",
	})
	q.upsertCount = 0 // reset; we want to count just the rescan

	drv.channels = append(drv.channels, DiscoveredChannel{Number: "7.1", Name: "WABC"})
	n, err := svc.RescanTuner(context.Background(), row.ID)
	if err != nil {
		t.Fatalf("rescan: %v", err)
	}
	if n != 2 {
		t.Errorf("count: got %d, want 2", n)
	}
	if q.upsertCount != 2 {
		t.Errorf("upserts: got %d, want 2", q.upsertCount)
	}
}

func TestService_DiscoverUpdatesTuneCountWhenChanged(t *testing.T) {
	drv := &fakeDriver{tuneCount: 5}
	svc, _ := newServiceWithFakeDriver(t, drv)

	row, err := svc.CreateTuner(context.Background(), CreateTunerDeviceParams{
		Type: "fake", Name: "box", TuneCount: 0,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	// One UpdateTunerDevice call should have fired (count went 0 → 5).
	stored, _ := svc.GetTuner(context.Background(), row.ID)
	if stored.TuneCount != 5 {
		t.Errorf("tune_count: got %d, want 5", stored.TuneCount)
	}
}

func TestService_OpenChannelStream_OK(t *testing.T) {
	drv := &fakeDriver{
		channels:   []DiscoveredChannel{{Number: "5.1", Name: "WCBS"}},
		streamBody: "TS-PAYLOAD",
	}
	svc, q := newServiceWithFakeDriver(t, drv)
	svc.CreateTuner(context.Background(), CreateTunerDeviceParams{Type: "fake", Name: "box"})

	// Pull the upserted channel ID out of the mock.
	var chID uuid.UUID
	for id := range q.channels {
		chID = id
	}

	stream, err := svc.OpenChannelStream(context.Background(), chID)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer stream.Close()
	body, _ := io.ReadAll(stream)
	if string(body) != "TS-PAYLOAD" {
		t.Errorf("body: got %q", body)
	}
}

func TestService_OpenChannelStream_UnknownChannel(t *testing.T) {
	drv := &fakeDriver{}
	svc, _ := newServiceWithFakeDriver(t, drv)
	_, err := svc.OpenChannelStream(context.Background(), uuid.New())
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown channel: got %v, want ErrNotFound", err)
	}
}

func TestService_OpenChannelStream_DisabledTunerIs404(t *testing.T) {
	drv := &fakeDriver{
		channels: []DiscoveredChannel{{Number: "5.1", Name: "WCBS"}},
	}
	svc, q := newServiceWithFakeDriver(t, drv)
	row, _ := svc.CreateTuner(context.Background(), CreateTunerDeviceParams{Type: "fake", Name: "box"})

	if err := svc.SetTunerEnabled(context.Background(), row.ID, false); err != nil {
		t.Fatalf("disable: %v", err)
	}
	var chID uuid.UUID
	for id := range q.channels {
		chID = id
	}
	_, err := svc.OpenChannelStream(context.Background(), chID)
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("disabled tuner should 404; got %v", err)
	}
}

func TestService_OpenChannelStream_BusyBubbles(t *testing.T) {
	drv := &fakeDriver{
		channels: []DiscoveredChannel{{Number: "5.1", Name: "WCBS"}},
		openErr:  ErrAllTunersBusy,
	}
	svc, q := newServiceWithFakeDriver(t, drv)
	svc.CreateTuner(context.Background(), CreateTunerDeviceParams{Type: "fake", Name: "box"})
	var chID uuid.UUID
	for id := range q.channels {
		chID = id
	}
	_, err := svc.OpenChannelStream(context.Background(), chID)
	if !errors.Is(err, ErrAllTunersBusy) {
		t.Errorf("got %v, want ErrAllTunersBusy", err)
	}
}

func TestService_DriverIsCachedAcrossCalls(t *testing.T) {
	// Build a Service whose registry hands out a *new* driver every call
	// so we can prove the first one was reused.
	q := newMockQuerier()
	r := NewRegistry()
	var built int
	r.Register("fake", func(_ string, _ []byte) (Driver, error) {
		built++
		return &fakeDriver{
			channels: []DiscoveredChannel{{Number: "5.1", Name: "WCBS"}},
		}, nil
	})
	svc := NewService(q, r, slog.Default())

	row, _ := svc.CreateTuner(context.Background(), CreateTunerDeviceParams{Type: "fake", Name: "box"})
	beforeRescan := built
	svc.RescanTuner(context.Background(), row.ID)
	if built != beforeRescan {
		t.Errorf("driver should be cached; build count went %d -> %d", beforeRescan, built)
	}
}

func TestService_UpdateTuner_InvalidatesDriverCache(t *testing.T) {
	q := newMockQuerier()
	r := NewRegistry()
	var built int
	r.Register("fake", func(_ string, _ []byte) (Driver, error) {
		built++
		return &fakeDriver{
			channels: []DiscoveredChannel{{Number: "5.1", Name: "WCBS"}},
		}, nil
	})
	svc := NewService(q, r, slog.Default())

	row, _ := svc.CreateTuner(context.Background(), CreateTunerDeviceParams{Type: "fake", Name: "box"})
	beforeUpdate := built
	svc.UpdateTuner(context.Background(), UpdateTunerDeviceParams{ID: row.ID, Name: "renamed"})
	svc.RescanTuner(context.Background(), row.ID)
	if built != beforeUpdate+1 {
		t.Errorf("driver should be rebuilt after Update; got %d builds, want %d",
			built, beforeUpdate+1)
	}
}

func TestService_DeleteTuner_InvalidatesDriver(t *testing.T) {
	q := newMockQuerier()
	r := NewRegistry()
	var built int
	r.Register("fake", func(_ string, _ []byte) (Driver, error) {
		built++
		return &fakeDriver{}, nil
	})
	svc := NewService(q, r, slog.Default())
	// Force a build so the cache has an entry.
	row := TunerDevice{ID: uuid.New(), Type: "fake"}
	q.tuners[row.ID] = row
	svc.driverFor(row)
	if len(svc.drivers) != 1 {
		t.Fatalf("expected cached driver")
	}
	svc.DeleteTuner(context.Background(), row.ID)
	if len(svc.drivers) != 0 {
		t.Errorf("driver cache should be empty after delete")
	}
}

// ── EPG ingest ───────────────────────────────────────────────────────────────

const epgTestXMLTV = `<?xml version="1.0" encoding="UTF-8"?>
<tv>
  <channel id="WCBS.5.1.us">
    <display-name>WCBS-DT</display-name>
    <lcn>5.1</lcn>
  </channel>
  <channel id="WABC.7.1.us">
    <display-name>WABC-DT</display-name>
    <lcn>7.1</lcn>
  </channel>
  <programme start="20260423180000 +0000" stop="20260423190000 +0000" channel="WCBS.5.1.us">
    <title>60 Minutes</title>
  </programme>
  <programme start="20260423180000 +0000" stop="20260423190000 +0000" channel="WABC.7.1.us">
    <title>World News</title>
  </programme>
  <programme start="20260423180000 +0000" stop="20260423190000 +0000" channel="UNKNOWN.99.us">
    <title>Should be dropped</title>
  </programme>
</tv>`

func TestRefreshEPGSource_AutoMatchesAndIngests(t *testing.T) {
	svc, q := newService(t)

	// Two channels, neither yet mapped — auto-match by lcn → number.
	wcbsID := uuid.New()
	wabcID := uuid.New()
	q.channels[wcbsID] = Channel{ID: wcbsID, Number: "5.1", Name: "WCBS"}
	q.channels[wabcID] = Channel{ID: wabcID, Number: "7.1", Name: "WABC"}
	q.unmapped = []Channel{q.channels[wcbsID], q.channels[wabcID]}

	dir := t.TempDir()
	path := filepath.Join(dir, "g.xml")
	if err := os.WriteFile(path, []byte(epgTestXMLTV), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	cfg, _ := json.Marshal(XMLTVSourceConfig{Source: path})

	// Seed an EPG source row.
	src, err := svc.CreateEPGSource(context.Background(), CreateEPGSourceParams{
		Type: EPGSourceTypeXMLTVFile, Name: "test", Config: cfg,
	})
	if err != nil {
		t.Fatalf("create source: %v", err)
	}

	// Simulate the auto-match having taken effect by populating the
	// channels' EPGChannelID — the mock's GetChannelByEPGID looks at it.
	wcbsEPG := "WCBS.5.1.us"
	wabcEPG := "WABC.7.1.us"
	q.channels[wcbsID] = Channel{ID: wcbsID, Number: "5.1", Name: "WCBS", EPGChannelID: &wcbsEPG}
	q.channels[wabcID] = Channel{ID: wabcID, Number: "7.1", Name: "WABC", EPGChannelID: &wabcEPG}

	res, err := svc.RefreshEPGSource(context.Background(), src.ID)
	if err != nil {
		t.Fatalf("refresh: %v", err)
	}
	// 2 mapped channels matched programs; 1 unknown channel program dropped.
	if res.ProgramsIngested != 2 {
		t.Errorf("ingested: got %d, want 2", res.ProgramsIngested)
	}
	if len(q.upsertedPrograms) != 2 {
		t.Errorf("upserted programs: got %d, want 2", len(q.upsertedPrograms))
	}
	// auto-match should have written 2 SetChannelEPGID calls (one per
	// initially-unmapped channel; both matched on lcn).
	if len(q.epgIDSets) != 2 {
		t.Errorf("auto-match writes: got %d, want 2", len(q.epgIDSets))
	}
}

func TestRefreshEPGSource_RejectsNonXMLTVType(t *testing.T) {
	svc, q := newService(t)
	src := EPGSource{ID: uuid.New(), Type: "schedules_direct"}
	q.epgSources = []EPGSource{src}
	_, err := svc.RefreshEPGSource(context.Background(), src.ID)
	if err == nil {
		t.Error("expected error on unsupported source type")
	}
}

func TestRefreshEPGSource_BadConfigReturnsError(t *testing.T) {
	svc, q := newService(t)
	src := EPGSource{ID: uuid.New(), Type: EPGSourceTypeXMLTVFile, Config: []byte("not json")}
	q.epgSources = []EPGSource{src}
	_, err := svc.RefreshEPGSource(context.Background(), src.ID)
	if err == nil {
		t.Error("expected error on bad config")
	}
}

func TestRefreshEPGSource_EmptySourceURL(t *testing.T) {
	svc, q := newService(t)
	cfg, _ := json.Marshal(XMLTVSourceConfig{Source: ""})
	src := EPGSource{ID: uuid.New(), Type: EPGSourceTypeXMLTVFile, Config: cfg}
	q.epgSources = []EPGSource{src}
	_, err := svc.RefreshEPGSource(context.Background(), src.ID)
	if err == nil {
		t.Error("expected error on empty source")
	}
}

func TestAutoMatchChannels_MatchesByCallsign(t *testing.T) {
	svc, q := newService(t)
	cs := "WCBS"
	chID := uuid.New()
	q.unmapped = []Channel{{ID: chID, Number: "5.1", Name: "Channel 5", Callsign: &cs}}
	xchans := []XMLTVChannel{
		{ID: "WCBS.5.1.us", DisplayNames: []string{"WCBS-DT"}},
	}
	matched, err := svc.autoMatchChannels(context.Background(), xchans)
	if err != nil {
		t.Fatalf("auto-match: %v", err)
	}
	if matched != 1 {
		t.Errorf("matched: got %d, want 1", matched)
	}
	if len(q.epgIDSets) != 1 || *q.epgIDSets[0].EPGID != "WCBS.5.1.us" {
		t.Errorf("expected callsign substring match; got %v", q.epgIDSets)
	}
}

func TestAutoMatchChannels_LCNTakesPrecedenceOverDisplayName(t *testing.T) {
	svc, q := newService(t)
	chID := uuid.New()
	q.unmapped = []Channel{{ID: chID, Number: "5.1", Name: "WCBS"}}
	xchans := []XMLTVChannel{
		// First entry has display-name match, second has lcn match.
		// LCN should win since it's tried first.
		{ID: "by-name", DisplayNames: []string{"WCBS"}},
		{ID: "by-lcn", LCN: "5.1"},
	}
	_, _ = svc.autoMatchChannels(context.Background(), xchans)
	if len(q.epgIDSets) != 1 || *q.epgIDSets[0].EPGID != "by-lcn" {
		t.Errorf("expected lcn match to win; got %v", q.epgIDSets)
	}
}
