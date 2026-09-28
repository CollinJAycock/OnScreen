package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"testing"
)

// TestLibraryWatchersStartAfterEnqueuerWiring guards a startup data race.
// run() builds the scanEnqueuer early and back-fills several of its fields
// (trickplay, newContent, requestsSvc, …) further down once their services
// exist. fs watchers call TriggerDirectoryScan, whose scan goroutine reads
// those fields without a lock — so a watcher started before the last
// back-fill raced it. run() is not unit-runnable, so this pins the order in
// the source: every `libEnqueuer.<field> = …` must precede the first
// `libEnqueuer.watchLibrary(…)`.
func TestLibraryWatchersStartAfterEnqueuerWiring(t *testing.T) {
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

	isEnqueuer := func(e ast.Expr) (string, bool) {
		sel, ok := e.(*ast.SelectorExpr)
		if !ok {
			return "", false
		}
		id, ok := sel.X.(*ast.Ident)
		if !ok || id.Name != "libEnqueuer" {
			return "", false
		}
		return sel.Sel.Name, true
	}

	var firstWatch token.Pos
	type assign struct {
		field string
		pos   token.Pos
	}
	var assigns []assign
	ast.Inspect(run.Body, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.AssignStmt:
			for _, lhs := range n.Lhs {
				if field, ok := isEnqueuer(lhs); ok {
					assigns = append(assigns, assign{field, n.Pos()})
				}
			}
		case *ast.CallExpr:
			if name, ok := isEnqueuer(n.Fun); ok && name == "watchLibrary" {
				if firstWatch == token.NoPos || n.Pos() < firstWatch {
					firstWatch = n.Pos()
				}
			}
		}
		return true
	})
	if firstWatch == token.NoPos {
		t.Fatal("libEnqueuer.watchLibrary call not found in run()")
	}
	if len(assigns) == 0 {
		t.Fatal("no libEnqueuer field assignments found in run() — test is stale")
	}
	for _, a := range assigns {
		if a.pos > firstWatch {
			t.Errorf("libEnqueuer.%s is assigned at %s, after the library watchers start at %s — "+
				"move the watcher loop below it (watcher scan goroutines read it unsynchronized)",
				a.field, fset.Position(a.pos), fset.Position(firstWatch))
		}
	}
}
