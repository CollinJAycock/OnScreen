package main

import (
	"context"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onscreen/onscreen/internal/db/gen"
	dbmigrations "github.com/onscreen/onscreen/internal/db/migrations"
	"github.com/onscreen/onscreen/internal/domain/settings"
)

// features.progress_without_duration follows the applied schema: before
// migration 00035 the rollup clears the stored duration on a duration-less
// event, so advertising the flag then would invite the wipe it exists to stop.
func TestCapabilities_ProgressWithoutDurationFollowsSchema(t *testing.T) {
	p := &capabilitiesProvider{}
	if p.progressWithoutDuration() {
		t.Fatal("advertised before any migration check succeeded")
	}
	steps := []struct {
		applied int64
		want    bool
	}{
		{34, false},
		{35, true}, // `goose up` against a running server, seen by a probe
		{36, true},
		{34, false}, // rolled back
	}
	for _, s := range steps {
		p.setSchemaVersion(s.applied)
		if got := p.progressWithoutDuration(); got != s.want {
			t.Errorf("applied=%d: got %v, want %v", s.applied, got, s.want)
		}
	}
}

// The gate names the migration that makes duration-less events keep the
// stored duration; renumbering or renaming it must move the gate too.
func TestCapabilities_ProgressGateNamesItsMigration(t *testing.T) {
	name := fmt.Sprintf("%05d_watch_progress_keep_duration.sql", progressKeepsDurationVersion)
	if _, err := fs.Stat(dbmigrations.FS, name); err != nil {
		t.Fatalf("gate migration %s not embedded: %v", name, err)
	}
}

// followSchemaVersion re-runs the migration check on its own, so a `goose up`
// against a running server lifts the flag when nothing polls /health/ready,
// and it returns once the server's context is done.
func TestFollowSchemaVersion_RefreshesUntilCancelled(t *testing.T) {
	p := &capabilitiesProvider{}
	p.setSchemaVersion(34)
	var dbVersion atomic.Int64
	dbVersion.Store(34)
	// What main.go's migrationStatusFn does on a successful check.
	status := func() (expected, applied, pending int64, ok bool) {
		v := dbVersion.Load()
		p.setSchemaVersion(v)
		return 35, v, 35 - v, true
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() {
		followSchemaVersion(ctx, time.Millisecond, status)
		close(done)
	}()

	dbVersion.Store(35) // `goose up`
	deadline := time.Now().Add(5 * time.Second)
	for !p.progressWithoutDuration() {
		if time.Now().After(deadline) {
			t.Fatal("flag still off after the schema moved: no refresh tick re-read it")
		}
		time.Sleep(time.Millisecond)
	}

	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("followSchemaVersion did not return after its context was cancelled")
	}
}

// run() is not unit-runnable, so this pins in the source that the applied
// schema version reaches the provider from the boot migration check, the
// readiness probe's check, and a ticker re-running that check. Without the
// boot feed the flag is off until the first refresh; without the other two a
// `goose up` against a running server leaves it off until a restart (the
// probe alone isn't enough: the shipped compose healthchecks poll
// /health/live). Each feed must pass st.Applied from a check that succeeded
// — a feed on the error path would store a zero-value version.
func TestCapabilities_SchemaVersionWiring(t *testing.T) {
	f, err := parser.ParseFile(token.NewFileSet(), "main.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	var run *ast.FuncDecl
	for _, d := range f.Decls {
		if fd, ok := d.(*ast.FuncDecl); ok && fd.Recv == nil && fd.Name.Name == "run" {
			run = fd
		}
	}
	if run == nil {
		t.Fatal("func run not found in main.go")
	}

	// isFeed: capsProvider.setSchemaVersion(st.Applied).
	isFeed := func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok || len(call.Args) != 1 {
			return false
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || sel.Sel.Name != "setSchemaVersion" || !isIdent(sel.X, "capsProvider") {
			return false
		}
		arg, ok := call.Args[0].(*ast.SelectorExpr)
		return ok && arg.Sel.Name == "Applied" && isIdent(arg.X, "st")
	}
	contains := func(root ast.Node, match func(ast.Node) bool) bool {
		if root == nil {
			return false
		}
		found := false
		ast.Inspect(root, func(n ast.Node) bool {
			if match(n) {
				found = true
			}
			return !found
		})
		return found
	}
	// isTicker: followSchemaVersion(gCtx, schemaVersionRefreshInterval,
	// migrationStatusFn) — the probe's own check, stopped by the server's
	// shutdown context.
	isTicker := func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok || !isIdent(call.Fun, "followSchemaVersion") || len(call.Args) != 3 {
			return false
		}
		return isIdent(call.Args[0], "gCtx") &&
			isIdent(call.Args[1], "schemaVersionRefreshInterval") &&
			isIdent(call.Args[2], "migrationStatusFn")
	}

	var inProbe, atBoot, ticked bool
	for _, stmt := range run.Body.List {
		if as, ok := stmt.(*ast.AssignStmt); ok && len(as.Lhs) == 1 && len(as.Rhs) == 1 && isIdent(as.Lhs[0], "migrationStatusFn") {
			if lit, ok := as.Rhs[0].(*ast.FuncLit); ok {
				// A top-level statement after the `if err != nil` return,
				// i.e. only once the check succeeded.
				checked := false
				for _, s := range lit.Body.List {
					if ifs, ok := s.(*ast.IfStmt); ok && errCheck(ifs.Cond) == token.NEQ {
						checked = true
						continue
					}
					if es, ok := s.(*ast.ExprStmt); ok && checked && isFeed(es.X) {
						inProbe = true
					}
				}
			}
			continue
		}
		if ifs, ok := stmt.(*ast.IfStmt); ok && strings.Contains(selectorNames(ifs.Init), "CheckMigrations") {
			var success ast.Node
			if c := errCheck(ifs.Cond); c == token.NEQ && ifs.Else != nil {
				success = ifs.Else
			} else if c == token.EQL {
				success = ifs.Body
			}
			atBoot = atBoot || contains(success, isFeed)
			continue
		}
		// g.Go(func() error { followSchemaVersion(...) ... })
		if es, ok := stmt.(*ast.ExprStmt); ok {
			if call, ok := es.X.(*ast.CallExpr); ok && len(call.Args) == 1 {
				if sel, ok := call.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "Go" && isIdent(sel.X, "g") {
					if lit, ok := call.Args[0].(*ast.FuncLit); ok {
						ticked = ticked || contains(lit, isTicker)
					}
				}
			}
		}
	}
	if !inProbe {
		t.Error("migrationStatusFn (the /health/ready check) does not call capsProvider.setSchemaVersion(st.Applied) after its error check")
	}
	if !atBoot {
		t.Error("the boot CheckMigrations does not call capsProvider.setSchemaVersion(st.Applied) on its success branch")
	}
	if !ticked {
		t.Error("run does not start followSchemaVersion(gCtx, schemaVersionRefreshInterval, migrationStatusFn) in the errgroup")
	}
}

func isIdent(e ast.Expr, name string) bool {
	id, ok := e.(*ast.Ident)
	return ok && id.Name == name
}

// errCheck is token.NEQ for `err != nil`, token.EQL for `err == nil`, and
// token.ILLEGAL for any other condition.
func errCheck(cond ast.Expr) token.Token {
	be, ok := cond.(*ast.BinaryExpr)
	if !ok || !isIdent(be.X, "err") || !isIdent(be.Y, "nil") {
		return token.ILLEGAL
	}
	if be.Op == token.NEQ || be.Op == token.EQL {
		return be.Op
	}
	return token.ILLEGAL
}

// selectorNames lists the selector names (method and package-member calls)
// under n, enough to recognise the boot migration check by its init.
func selectorNames(n ast.Node) string {
	if n == nil {
		return ""
	}
	var b strings.Builder
	ast.Inspect(n, func(n ast.Node) bool {
		if sel, ok := n.(*ast.SelectorExpr); ok {
			b.WriteString(sel.Sel.Name)
			b.WriteByte(' ')
		}
		return true
	})
	return b.String()
}

// ── configuration flags ─────────────────────────────────────────────────────

type fakeConfigDB struct {
	arr    []gen.ArrService
	tuners []gen.TunerDevice
	err    error
}

func (f *fakeConfigDB) ListArrServices(context.Context) ([]gen.ArrService, error) {
	return f.arr, f.err
}

func (f *fakeConfigDB) ListTunerDevices(context.Context) ([]gen.TunerDevice, error) {
	return f.tuners, f.err
}

// features.upcoming: only an enabled Radarr or Sonarr feeds the calendar.
func TestHasCalendarArrService(t *testing.T) {
	cases := []struct {
		name string
		arr  []gen.ArrService
		want bool
	}{
		{"none", nil, false},
		{"disabled radarr", []gen.ArrService{{Kind: "radarr", Enabled: false}}, false},
		{"lidarr only", []gen.ArrService{{Kind: "lidarr", Enabled: true}}, false},
		{"enabled sonarr", []gen.ArrService{{Kind: "lidarr", Enabled: true}, {Kind: "sonarr", Enabled: true}}, true},
		{"enabled radarr", []gen.ArrService{{Kind: "radarr", Enabled: true}}, true},
	}
	for _, c := range cases {
		got, err := hasCalendarArrService(context.Background(), &fakeConfigDB{arr: c.arr})
		if err != nil || got != c.want {
			t.Errorf("%s: got %v, %v; want %v", c.name, got, err, c.want)
		}
	}
	if _, err := hasCalendarArrService(context.Background(), &fakeConfigDB{err: errors.New("db down")}); err == nil {
		t.Error("a failed query must surface as an error, not as false")
	}
}

// features.live_tv_configured: only an enabled tuner's channels are listed.
func TestHasEnabledTuner(t *testing.T) {
	cases := []struct {
		name   string
		tuners []gen.TunerDevice
		want   bool
	}{
		{"none", nil, false},
		{"disabled only", []gen.TunerDevice{{Enabled: false}}, false},
		{"one enabled", []gen.TunerDevice{{Enabled: false}, {Enabled: true}}, true},
	}
	for _, c := range cases {
		got, err := hasEnabledTuner(context.Background(), &fakeConfigDB{tuners: c.tuners})
		if err != nil || got != c.want {
			t.Errorf("%s: got %v, %v; want %v", c.name, got, err, c.want)
		}
	}
}

// The capabilities endpoint is anonymous: a database-backed flag is probed
// at most once per ttl, a failed probe keeps the last answer and isn't
// retried within the ttl, and a nil flag (not wired) reads false.
func TestCachedFlag_TTLAndFailure(t *testing.T) {
	now := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	var calls int
	val, fail := true, false
	f := &cachedFlag{
		probe: func(context.Context) (bool, error) {
			calls++
			if fail {
				return false, errors.New("db down")
			}
			return val, nil
		},
		ttl: configFlagTTL,
		now: func() time.Time { return now },
	}
	ctx := context.Background()

	if !f.get(ctx) || calls != 1 {
		t.Fatalf("first read: calls = %d", calls)
	}
	val = false
	now = now.Add(configFlagTTL - time.Second)
	if !f.get(ctx) || calls != 1 {
		t.Errorf("within ttl: probed again (calls = %d) or lost the cached true", calls)
	}
	now = now.Add(2 * time.Second)
	if f.get(ctx) || calls != 2 {
		t.Errorf("after ttl: want a fresh probe reading false (calls = %d)", calls)
	}

	val = true
	now = now.Add(configFlagTTL)
	if !f.get(ctx) {
		t.Fatal("expected a fresh true")
	}
	fail = true
	now = now.Add(configFlagTTL)
	if !f.get(ctx) {
		t.Error("failed probe dropped the last good answer")
	}
	before := calls
	f.get(ctx)
	if calls != before {
		t.Error("failed probe retried within the ttl")
	}

	var unwired *cachedFlag
	if unwired.get(ctx) {
		t.Error("nil flag must read false")
	}
}

// features.requests / people_credits follow agentFn's key resolution, so a
// key stored in Settings (the usual way) counts — not only TMDB_API_KEY.
func TestTMDBConfigured(t *testing.T) {
	cases := []struct {
		stored, env string
		bundled     bool
		want        bool
	}{
		{"", "", false, false},
		{"stored", "", false, true},
		{"", "env", false, true},
		{"", "", true, true},
	}
	for _, c := range cases {
		if got := tmdbConfigured(c.stored, c.env, c.bundled); got != c.want {
			t.Errorf("tmdbConfigured(%q, %q, %v) = %v, want %v", c.stored, c.env, c.bundled, got, c.want)
		}
	}
}

// features.subtitles_external is false exactly when the subtitle provider
// would answer "not configured": it needs the operator's opt-in and a key.
func TestSubtitlesExternalConfigured(t *testing.T) {
	cases := []struct {
		cfg     settings.OpenSubtitlesConfig
		bundled bool
		want    bool
	}{
		{settings.OpenSubtitlesConfig{}, false, false},
		{settings.OpenSubtitlesConfig{APIKey: "k"}, false, false}, // key, not enabled
		{settings.OpenSubtitlesConfig{Enabled: true}, false, false},
		{settings.OpenSubtitlesConfig{Enabled: true}, true, true}, // bundled key
		{settings.OpenSubtitlesConfig{Enabled: true, APIKey: "k"}, false, true},
	}
	for _, c := range cases {
		if got := subtitlesExternalConfigured(c.cfg, c.bundled); got != c.want {
			t.Errorf("subtitlesExternalConfigured(%+v, %v) = %v, want %v", c.cfg, c.bundled, got, c.want)
		}
	}
}

// run() is not unit-runnable: pin that main.go wires the configuration
// flags, or features.upcoming and live_tv_configured would always be false.
func TestCapabilities_ConfigProbesWired(t *testing.T) {
	f, err := parser.ParseFile(token.NewFileSet(), "main.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	ast.Inspect(f, func(n ast.Node) bool {
		if call, ok := n.(*ast.CallExpr); ok {
			if sel, ok := call.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "setConfigProbes" && isIdent(sel.X, "capsProvider") {
				found = true
			}
		}
		return !found
	})
	if !found {
		t.Error("main.go never calls capsProvider.setConfigProbes")
	}
}
