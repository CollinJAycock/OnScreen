package i18n

import (
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strings"
	"sync"
)

// The catalogs: locales/<language>/<area>.json, merged per language. A key
// lives in exactly one area file, and every language has the same files
// (catalog_test.go checks both, as the web app's catalog test does).
//
//	errors.json     the API errors a person can see       error.
//	email.json      password reset, invite, SMTP test     email.
//	notify.json     in-app notifications                  notify.
//	transcode.json  Now Playing transcode reasons         reason.
//
//go:embed locales
var localeFS embed.FS

var (
	loadOnce sync.Once
	loaded   map[Lang]map[string]string
)

func catalogs() map[Lang]map[string]string {
	loadOnce.Do(func() {
		c, err := loadCatalogs(localeFS)
		if err != nil {
			// The catalogs are embedded at build time and checked by the
			// tests, so this is a broken build, not a runtime condition.
			panic(err)
		}
		loaded = c
	})
	return loaded
}

// loadCatalogs reads every locales/<language>/*.json file into one map per
// language, refusing a key defined twice.
func loadCatalogs(fsys fs.FS) (map[Lang]map[string]string, error) {
	out := map[Lang]map[string]string{}
	for _, l := range Supported {
		cat := map[string]string{}
		files, err := areaFiles(fsys, l)
		if err != nil {
			return nil, err
		}
		for _, name := range files {
			raw, err := fs.ReadFile(fsys, path.Join("locales", string(l), name))
			if err != nil {
				return nil, err
			}
			var area map[string]string
			if err := json.Unmarshal(raw, &area); err != nil {
				return nil, fmt.Errorf("i18n: locales/%s/%s: %w", l, name, err)
			}
			for k, v := range area {
				if _, dup := cat[k]; dup {
					return nil, fmt.Errorf("i18n: %s: key %q is in two area files", l, k)
				}
				cat[k] = v
			}
		}
		out[l] = cat
	}
	return out, nil
}

// areaFiles lists a language's catalog files, sorted.
func areaFiles(fsys fs.FS, l Lang) ([]string, error) {
	entries, err := fs.ReadDir(fsys, path.Join("locales", string(l)))
	if err != nil {
		return nil, fmt.Errorf("i18n: locales/%s: %w", l, err)
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".json") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	return names, nil
}
