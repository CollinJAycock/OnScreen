package api

import (
	"net/http"
	"os"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
)

// TestDumpRoutes writes every route NewRouter registers, one "METHOD /path"
// per line, to the file named by ROUTES_OUT (skipped otherwise). It is the
// input to scripts/api-audit (docs/v3-api-audit.md).
//
// Most routes are registered only when their handler is set, so every
// pointer-to-struct and func field of Handlers is filled with a zero value
// first: chi.Walk then sees all of them. Nothing is served; the handlers are
// never called.
func TestDumpRoutes(t *testing.T) {
	out := os.Getenv("ROUTES_OUT")
	if out == "" {
		t.Skip("set ROUTES_OUT to write the route list")
	}
	var h *Handlers
	newTestRouterWith(t, t.TempDir(), func(hs *Handlers) { h = hs })

	for _, f := range reflect.ValueOf(h).Elem().Fields() {
		if !f.CanSet() || !f.IsZero() {
			continue
		}
		switch {
		case f.Kind() == reflect.Pointer && f.Type().Elem().Kind() == reflect.Struct:
			f.Set(reflect.New(f.Type().Elem()))
		case f.Kind() == reflect.Func:
			ft := f.Type()
			f.Set(reflect.MakeFunc(ft, func([]reflect.Value) []reflect.Value {
				res := make([]reflect.Value, ft.NumOut())
				for j := range res {
					res[j] = reflect.Zero(ft.Out(j))
				}
				return res
			}))
		}
	}

	routes, ok := NewRouter(h).(chi.Routes)
	if !ok {
		t.Fatal("NewRouter no longer returns a chi.Routes")
	}
	var lines []string
	err := chi.Walk(routes, func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		lines = append(lines, method+" "+strings.ReplaceAll(route, "/*/", "/"))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(lines)
	if err := os.WriteFile(out, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Logf("%d routes written to %s", len(lines), out)
}
