package main

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"testing"

	v1 "github.com/onscreen/onscreen/internal/api/v1"
	"github.com/onscreen/onscreen/internal/scanner"
	"github.com/onscreen/onscreen/internal/userstats"
)

// roConsumer says what a gen.New(roPool) handed to one call site can reach.
type roConsumer struct {
	// iface: the interface the site receives; its methods are checked.
	iface reflect.Type
	// adapter: the cmd/server adapter type wrapping the queries; with iface
	// or methods, each method is resolved to the gen queries the adapter's
	// method calls, otherwise every query any adapter method calls is
	// checked.
	adapter string
	// methods: gen (or, with adapter, adapter) methods called directly.
	methods []string
	// roCallsIn: domain package directories whose `.ro.<M>(` calls are
	// the adapter methods this replica-backed adapter serves.
	roCallsIn []string
}

func ifaceOf[T any]() reflect.Type { return reflect.TypeOf((*T)(nil)).Elem() }

// roConsumers maps every place run() hands out a read-replica query surface
// to what that surface can do. A real replica refuses writes (the
// notification mark-read UPDATEs ran on it until 2026-11), so each entry is
// checked to reach only queries that read. The key is the callee and
// argument index, or Type.field for a composite literal.
var roConsumers = map[string]roConsumer{
	"libraryAdapter.q":                {adapter: "libraryAdapter", roCallsIn: []string{"internal/domain/library"}},
	"mediaAdapter.q":                  {adapter: "mediaAdapter", roCallsIn: []string{"internal/domain/media"}},
	"watchEventAdapter.q":             {adapter: "watchEventAdapter", roCallsIn: []string{"internal/domain/watchevent"}},
	"WithWatchState/0":                {iface: ifaceOf[v1.LibraryWatchDB]()},
	"WithParentTitles/0":              {iface: ifaceOf[v1.LibraryParentDB]()},
	"userLibraryAccessAdapter.q":      {adapter: "userLibraryAccessAdapter"},
	"NewAuditHandler/0":               {iface: reflect.TypeOf(v1.NewAuditHandler).In(0)},
	"NewAnalyticsHandler/0":           {iface: reflect.TypeOf(v1.NewAnalyticsHandler).In(0)},
	"NewHubHandler/0":                 {iface: ifaceOf[v1.HubDB]()},
	"WithEpisodePoster/0":             {iface: ifaceOf[v1.EpisodePosterDB]()},
	"WithWatchRows/0":                 {iface: ifaceOf[v1.HubWatchDB]()},
	"WithCollections/0":               {iface: ifaceOf[v1.HubCollectionsDB]()},
	"NewSearchHandler/0":              {iface: ifaceOf[v1.SearchDB]()},
	"NewHistoryHandler/0":             {iface: ifaceOf[v1.HistoryDB]()},
	"streamCapsAdapter.q":             {adapter: "streamCapsAdapter"},
	"NewNativeSessionsHandler/2":      {iface: reflect.TypeOf(v1.NewNativeSessionsHandler).In(2)},
	"setConfigProbes/0":               {iface: ifaceOf[capabilitiesConfigDB]()},
	"favoritesChecker.q":              {adapter: "favoritesChecker"},
	"commercialMarkerLister.q":        {adapter: "commercialMarkerLister"},
	"WithFranchise/0":                 {iface: ifaceOf[v1.ItemFranchiseDB]()},
	"WithManualCollections/0":         {iface: ifaceOf[v1.ItemManualCollectionsDB]()},
	"WithExternalRatings/0":           {iface: ifaceOf[v1.ItemExternalRatingsDB]()},
	"WithLabels/0":                    {iface: ifaceOf[v1.ItemLabelReader]()},
	"userstats.New/0":                 {iface: ifaceOf[userstats.Querier]()},
	"NewPlaybackHandler/0":            {iface: ifaceOf[v1.PlaybackDB]()},
	"WithUsers/0":                     {iface: reflect.TypeOf((*v1.NativeSessionsHandler).WithUsers).In(1)},
	"NewDiscoverHandler/0":            {iface: ifaceOf[v1.DiscoverDB]()},
	"WithSeasons/1":                   {iface: ifaceOf[v1.DiscoverSeasonDB]()},
	"NewUpcomingHandler/0":            {iface: ifaceOf[v1.UpcomingDB]()},
	"ocrExistsChecker.q":              {adapter: "ocrExistsChecker"},
	"staticPopularity.q":              {adapter: "staticPopularity"},
	"NewMusicHandler/0":               {iface: ifaceOf[v1.MusicDB]()},
	"NewOPDSHandler/0":                {iface: ifaceOf[v1.OPDSDB]()},
	"GetArtworkContentRating":         {methods: []string{"GetArtworkContentRating"}},
	"ArtworkLabelsPermitted":          {methods: []string{"ArtworkLabelsPermitted"}},
	"Handlers.LabelGate":              {iface: ifaceOf[v1.ItemLabelGate]()},
	"scanner.LibraryAnimeChecker":     {iface: ifaceOf[scanner.LibraryAnimeChecker](), adapter: "libraryAdapter"},
	"scanner.LibraryEpisodeOrderer":   {iface: ifaceOf[scanner.LibraryEpisodeOrderer](), adapter: "libraryAdapter"},
	"NewDirectPlayGate/0:streamCaps":  {adapter: "streamCapsAdapter"},
	"WithUserCaps/0:streamCaps":       {adapter: "streamCapsAdapter"},
	"WithStreamCaps/0:streamCaps":     {adapter: "streamCapsAdapter"},
	"NewStreamLimit/0:streamCaps":     {adapter: "streamCapsAdapter"},
	"WithLibraryAccess/0:userLibrary": {adapter: "userLibraryAccessAdapter"},
}

// TestReadReplicaConsumersOnlyRead finds every gen.New(roPool) in run() and
// checks the surface it is handed reaches only read queries. A new consumer
// fails until it is added to roConsumers — at which point its interface is
// checked too, so a write can't be routed to the replica silently.
func TestReadReplicaConsumersOnlyRead(t *testing.T) {
	writes := writeQueries(t)
	adapters := adapterQueryCalls(t)

	keys := roPoolSites(t)
	if len(keys) == 0 {
		t.Fatal("found no gen.New(roPool) in run(); the test has lost track of main.go")
	}
	for _, k := range keys {
		c, ok := roConsumers[k.key]
		if !ok {
			t.Errorf("%s: gen.New(roPool) handed to %q, which roConsumers doesn't list. Add it with the interface it receives (it must only read: the replica refuses writes).", k.pos, k.key)
			continue
		}
		for _, q := range c.queries(t, adapters) {
			if w, isWrite := writes[q]; isWrite {
				t.Errorf("%s: %q reaches %s (%s), which writes — wire it to rwPool or drop the write from its interface", k.pos, k.key, q, w)
			}
		}
	}
}

func (c roConsumer) queries(t *testing.T, adapters map[string]map[string][]string) []string {
	t.Helper()
	var names []string
	if c.iface != nil {
		for i := 0; i < c.iface.NumMethod(); i++ {
			names = append(names, c.iface.Method(i).Name)
		}
	}
	names = append(names, c.methods...)
	for _, dir := range c.roCallsIn {
		names = append(names, roCalls(t, dir)...)
	}
	if c.adapter == "" {
		return names
	}
	methods, ok := adapters[c.adapter]
	if !ok {
		t.Fatalf("adapter %s not found in cmd/server", c.adapter)
	}
	var out []string
	if len(names) == 0 {
		for _, qs := range methods {
			out = append(out, qs...)
		}
		return out
	}
	for _, m := range names {
		out = append(out, methods[m]...)
	}
	return out
}

var (
	queryNameRe = regexp.MustCompile(`(?m)^-- name: (\w+) :\w+\s*$`)
	sqlComment  = regexp.MustCompile(`--[^\n]*`)
	writeVerbRe = regexp.MustCompile(`(?i)\b(INSERT|UPDATE|DELETE|REFRESH|TRUNCATE)\b|nextval\(|pg_advisory`)
)

// writeQueries returns the sqlc query names whose SQL writes, mapped to their
// file.
func writeQueries(t *testing.T) map[string]string {
	t.Helper()
	files, err := filepath.Glob("../../internal/db/queries/*.sql")
	if err != nil || len(files) == 0 {
		t.Fatalf("no query files: %v", err)
	}
	out := map[string]string{}
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		s := string(b)
		locs := queryNameRe.FindAllStringSubmatchIndex(s, -1)
		for i, loc := range locs {
			end := len(s)
			if i+1 < len(locs) {
				end = locs[i+1][0]
			}
			body := sqlComment.ReplaceAllString(s[loc[1]:end], "")
			if writeVerbRe.MatchString(body) {
				out[s[loc[2]:loc[3]]] = filepath.Base(f)
			}
		}
	}
	return out
}

// adapterQueryCalls maps each cmd/server type to, per method, the gen queries
// called on its q field.
func adapterQueryCalls(t *testing.T) map[string]map[string][]string {
	t.Helper()
	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, ".", func(fi os.FileInfo) bool { return !strings.HasSuffix(fi.Name(), "_test.go") }, 0)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]map[string][]string{}
	for _, pkg := range pkgs {
		for _, f := range pkg.Files {
			for _, d := range f.Decls {
				fd, ok := d.(*ast.FuncDecl)
				if !ok || fd.Recv == nil || len(fd.Recv.List) != 1 || fd.Body == nil {
					continue
				}
				recvType := fd.Recv.List[0].Type
				if st, ok := recvType.(*ast.StarExpr); ok {
					recvType = st.X
				}
				id, ok := recvType.(*ast.Ident)
				if !ok {
					continue
				}
				var recvName string
				if len(fd.Recv.List[0].Names) == 1 {
					recvName = fd.Recv.List[0].Names[0].Name
				}
				var calls []string
				ast.Inspect(fd.Body, func(n ast.Node) bool {
					sel, ok := n.(*ast.SelectorExpr)
					if !ok {
						return true
					}
					inner, ok := sel.X.(*ast.SelectorExpr)
					if !ok || inner.Sel.Name != "q" {
						return true
					}
					if x, ok := inner.X.(*ast.Ident); ok && x.Name == recvName {
						calls = append(calls, sel.Sel.Name)
					}
					return true
				})
				if out[id.Name] == nil {
					out[id.Name] = map[string][]string{}
				}
				out[id.Name][fd.Name.Name] = append(out[id.Name][fd.Name.Name], calls...)
			}
		}
	}
	return out
}

// roCalls lists the methods called as `<x>.ro.<M>(` in a package directory.
func roCalls(t *testing.T, dir string) []string {
	t.Helper()
	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, filepath.Join("..", "..", dir), func(fi os.FileInfo) bool { return !strings.HasSuffix(fi.Name(), "_test.go") }, 0)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, pkg := range pkgs {
		for _, f := range pkg.Files {
			ast.Inspect(f, func(n ast.Node) bool {
				sel, ok := n.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				if inner, ok := sel.X.(*ast.SelectorExpr); ok && inner.Sel.Name == "ro" {
					seen[sel.Sel.Name] = true
				}
				return true
			})
		}
	}
	if len(seen) == 0 {
		t.Fatalf("no .ro.<method> calls found in %s", dir)
	}
	out := make([]string, 0, len(seen))
	for m := range seen {
		out = append(out, m)
	}
	sort.Strings(out)
	return out
}

type roSite struct {
	key string
	pos token.Position
}

func isRoPoolNew(e ast.Expr) bool {
	call, ok := e.(*ast.CallExpr)
	if !ok || len(call.Args) != 1 {
		return false
	}
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != "New" {
		return false
	}
	if x, ok := sel.X.(*ast.Ident); !ok || x.Name != "gen" {
		return false
	}
	arg, ok := call.Args[0].(*ast.Ident)
	return ok && arg.Name == "roPool"
}

func calleeName(fun ast.Expr) string {
	switch f := fun.(type) {
	case *ast.Ident:
		return f.Name
	case *ast.SelectorExpr:
		if x, ok := f.X.(*ast.Ident); ok && (x.Name == "userstats") {
			return x.Name + "." + f.Sel.Name
		}
		return f.Sel.Name
	}
	return fmt.Sprintf("%T", fun)
}

// roPoolSites returns a key for every gen.New(roPool) in run(): the callee
// and argument index it is passed to, the Type.field of the composite literal
// it initialises, the method called on it directly, or — for the adapter
// variables roQ — each interface it is returned as.
func roPoolSites(t *testing.T) []roSite {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "main.go", nil, 0)
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

	var sites []roSite
	add := func(key string, n ast.Node) {
		sites = append(sites, roSite{key: key, pos: fset.Position(n.Pos())})
	}
	// Composite literals whose field is gen.New(roPool), by variable name
	// when assigned (roQ := &libraryAdapter{…}), so later uses can be keyed.
	roVars := map[string]string{} // variable -> adapter type
	litKey := func(lit *ast.CompositeLit) (string, bool) {
		for _, el := range lit.Elts {
			kv, ok := el.(*ast.KeyValueExpr)
			if !ok || !isRoPoolNew(kv.Value) {
				continue
			}
			typ := "?"
			switch tt := lit.Type.(type) {
			case *ast.Ident:
				typ = tt.Name
			case *ast.SelectorExpr:
				typ = tt.Sel.Name
			}
			field := "?"
			if k, ok := kv.Key.(*ast.Ident); ok {
				field = k.Name
			}
			return typ + "." + field, true
		}
		return "", false
	}
	ast.Inspect(run.Body, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.AssignStmt:
			for i, rhs := range n.Rhs {
				if u, ok := rhs.(*ast.UnaryExpr); ok {
					rhs = u.X
				}
				if lit, ok := rhs.(*ast.CompositeLit); ok {
					if key, ok := litKey(lit); ok && i < len(n.Lhs) {
						if id, ok := n.Lhs[i].(*ast.Ident); ok {
							roVars[id.Name] = strings.SplitN(key, ".", 2)[0]
						}
					}
				}
			}
		case *ast.CompositeLit:
			if key, ok := litKey(n); ok {
				add(key, n)
			}
		case *ast.CallExpr:
			// gen.New(roPool).Method(...)
			if sel, ok := n.Fun.(*ast.SelectorExpr); ok && isRoPoolNew(sel.X) {
				add(sel.Sel.Name, n)
				return true
			}
			for i, arg := range n.Args {
				if isRoPoolNew(arg) {
					add(fmt.Sprintf("%s/%d", calleeName(n.Fun), i), n)
					continue
				}
				// A replica-backed adapter literal passed straight in.
				if u, ok := arg.(*ast.UnaryExpr); ok {
					arg = u.X
				}
				if lit, ok := arg.(*ast.CompositeLit); ok {
					if key, ok := litKey(lit); ok {
						suffix := map[string]string{
							"streamCapsAdapter":        "streamCaps",
							"userLibraryAccessAdapter": "userLibrary",
						}[strings.SplitN(key, ".", 2)[0]]
						if suffix != "" {
							add(fmt.Sprintf("%s/%d:%s", calleeName(n.Fun), i, suffix), n)
						}
					}
				}
			}
		case *ast.FuncLit:
			// func() scanner.LibraryAnimeChecker { return roQ }
			if n.Type.Results == nil || len(n.Type.Results.List) != 1 {
				return true
			}
			for _, st := range n.Body.List {
				ret, ok := st.(*ast.ReturnStmt)
				if !ok || len(ret.Results) != 1 {
					continue
				}
				if id, ok := ret.Results[0].(*ast.Ident); ok && roVars[id.Name] != "" {
					if rt, ok := n.Type.Results.List[0].Type.(*ast.SelectorExpr); ok {
						if pkg, ok := rt.X.(*ast.Ident); ok {
							add(pkg.Name+"."+rt.Sel.Name, n)
						}
					}
				}
			}
		}
		return true
	})
	return sites
}
