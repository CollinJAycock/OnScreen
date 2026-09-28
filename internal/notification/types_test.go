package notification

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// declaredTypeConsts parses types.go and returns every Type* constant name
// with its string value, so the tests below can't drift from the source.
func declaredTypeConsts(t *testing.T) map[string]string {
	t.Helper()
	f, err := parser.ParseFile(token.NewFileSet(), "types.go", nil, 0)
	if err != nil {
		t.Fatalf("parse types.go: %v", err)
	}
	out := map[string]string{}
	for _, decl := range f.Decls {
		gd, ok := decl.(*ast.GenDecl)
		if !ok || gd.Tok != token.CONST {
			continue
		}
		for _, spec := range gd.Specs {
			vs := spec.(*ast.ValueSpec)
			for i, name := range vs.Names {
				if !strings.HasPrefix(name.Name, "Type") || i >= len(vs.Values) {
					continue
				}
				lit, ok := vs.Values[i].(*ast.BasicLit)
				if !ok || lit.Kind != token.STRING {
					t.Fatalf("%s must be a string literal constant", name.Name)
				}
				v, _ := strconv.Unquote(lit.Value)
				out[name.Name] = v
			}
		}
	}
	if len(out) == 0 {
		t.Fatal("no Type* constants found in types.go")
	}
	return out
}

// Every declared type must satisfy the DB format check, or every insert of it
// fails (the bug migration 00021 fixed), and must be registered as known.
func TestTypes_MatchDBFormatAndAreKnown(t *testing.T) {
	consts := declaredTypeConsts(t)
	for name, v := range consts {
		if !TypePattern.MatchString(v) {
			t.Errorf("%s = %q does not match the notifications_type_check pattern %s", name, v, TypePattern)
		}
		if !IsKnownType(v) {
			t.Errorf("%s = %q is not in knownTypes", name, v)
		}
	}
	if len(KnownTypes()) != len(consts) {
		t.Errorf("knownTypes has %d entries, types.go declares %d constants", len(KnownTypes()), len(consts))
	}
	// The request workflow's notices — the ones the old CHECK silently dropped.
	for _, v := range []string{"request_created", "request_pending", "request_approved", "request_declined", "request_available", "request_failed"} {
		if !IsKnownType(v) {
			t.Errorf("%q must be a declared type", v)
		}
	}
}

// The Go pattern and the migration's CHECK must be the same expression.
func TestTypes_PatternMatchesMigration(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "db", "migrations", "00021_notification_types.sql"))
	if err != nil {
		t.Fatalf("read migration: %v", err)
	}
	up := strings.SplitN(string(raw), "-- +goose Down", 2)[0]
	if !strings.Contains(up, "type ~ '"+TypePattern.String()+"'") {
		t.Errorf("migration 00021 Up does not check `type ~ '%s'`", TypePattern)
	}
	for _, bad := range []string{"", "Request", "request pending", "1st", "request-pending", strings.Repeat("a", 65)} {
		if TypePattern.MatchString(bad) {
			t.Errorf("pattern accepts %q", bad)
		}
	}
}

// TestNotifyCallSitesUseKnownTypes walks the server source and fails when a
// Notify / NotifyAdmins / NotifyAllUsers call passes a string literal as the
// type, or names a constant that isn't declared. A literal is how the request
// notices drifted from the DB constraint unnoticed; constants keep every type
// in one audited list.
func TestNotifyCallSitesUseKnownTypes(t *testing.T) {
	consts := declaredTypeConsts(t)
	// typ argument position and arity for each fan-out method.
	methods := map[string]struct{ idx, arity int }{
		"Notify":         {2, 6}, // (ctx, userID, typ, title, body, itemID)
		"NotifyAdmins":   {1, 5}, // (ctx, typ, title, body, itemID)
		"NotifyAllUsers": {1, 5},
	}
	root := filepath.Join("..", "..")
	checked := 0
	for _, dir := range []string{"internal", "cmd"} {
		err := filepath.WalkDir(filepath.Join(root, dir), func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				switch d.Name() {
				case "testdata", "node_modules", "vendor", "gen":
					return filepath.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			fset := token.NewFileSet()
			f, perr := parser.ParseFile(fset, path, nil, 0)
			if perr != nil {
				// A file mid-edit elsewhere in the tree isn't this test's concern;
				// the build catches it.
				t.Logf("skip %s: %v", path, perr)
				return nil
			}
			ast.Inspect(f, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				sel, ok := call.Fun.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				m, ok := methods[sel.Sel.Name]
				if !ok || len(call.Args) != m.arity {
					return true
				}
				checked++
				pos := fset.Position(call.Pos())
				switch arg := call.Args[m.idx].(type) {
				case *ast.BasicLit:
					t.Errorf("%s: %s called with literal type %s — use a notification.Type* constant", pos, sel.Sel.Name, arg.Value)
				case *ast.SelectorExpr:
					if pkg, ok := arg.X.(*ast.Ident); ok && pkg.Name == "notification" {
						if _, ok := consts[arg.Sel.Name]; !ok {
							t.Errorf("%s: notification.%s is not a declared type constant", pos, arg.Sel.Name)
						}
					}
				case *ast.Ident:
					if strings.HasPrefix(arg.Name, "Type") {
						if _, ok := consts[arg.Name]; !ok {
							t.Errorf("%s: %s is not a declared type constant", pos, arg.Name)
						}
					}
				}
				return true
			})
			return nil
		})
		if err != nil {
			t.Fatalf("walk %s: %v", dir, err)
		}
	}
	// Sanity: the walk actually found the request workflow's call sites.
	if checked < 5 {
		t.Errorf("only %d Notify call sites checked — is the walk rooted correctly?", checked)
	}
}
