package i18n

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"testing"
)

// The catalogs, checked as the web app checks its own (web/src/lib/i18n):
// the same area files in every language, only keys English has, English's
// placeholders, and only the plural forms the language uses. Missing
// translations are allowed (they fall back to English) and only logged.

var placeholderRE = regexp.MustCompile(`\{(\w+)\}`)
var pluralRE = regexp.MustCompile(`_(zero|one|two|few|many|other)$`)

var pluralForms = map[Lang][]string{
	English:           {"one", "other"},
	Spanish:           {"one", "many", "other"},
	SimplifiedChinese: {"other"},
}

func TestCatalogFilesMatch(t *testing.T) {
	want, err := areaFiles(localeFS, English)
	if err != nil {
		t.Fatal(err)
	}
	for _, l := range Supported {
		got, err := areaFiles(localeFS, l)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Join(got, ",") != strings.Join(want, ",") {
			t.Errorf("%s has area files %v, English has %v", l, got, want)
		}
	}
	if _, err := loadCatalogs(localeFS); err != nil {
		t.Fatal(err)
	}
}

func TestCatalogsAgreeWithEnglish(t *testing.T) {
	en := catalogs()[English]
	for k := range en {
		if m := pluralRE.FindStringSubmatch(k); m != nil && !contains(pluralForms[English], m[1]) {
			t.Errorf("en: %s: English plural forms are _one and _other", k)
		}
	}
	for _, l := range Supported[1:] {
		cat := catalogs()[l]
		var missing []string
		for k, msg := range cat {
			src, ok := en[k]
			if m := pluralRE.FindStringSubmatch(k); m != nil {
				base := k[:len(k)-len(m[0])]
				if !contains(pluralForms[l], m[1]) {
					t.Errorf("%s: %s: %s doesn't use the _%s form", l, k, l, m[1])
				}
				if !ok {
					src, ok = en[base+"_other"]
				}
			}
			if !ok {
				t.Errorf("%s: %s: English has no such key", l, k)
				continue
			}
			if a, b := placeholders(src), placeholders(msg); a != b {
				t.Errorf("%s: %s: placeholders %s, English has %s", l, k, b, a)
			}
		}
		for k := range en {
			if _, ok := cat[k]; ok {
				continue
			}
			if m := pluralRE.FindStringSubmatch(k); m != nil {
				base := k[:len(k)-len(m[0])]
				if !contains(pluralForms[l], m[1]) {
					continue // a form this language doesn't use
				}
				if _, ok := cat[base+"_other"]; ok && m[1] != "other" {
					continue
				}
			}
			missing = append(missing, k)
		}
		if len(missing) > 0 {
			sort.Strings(missing)
			t.Logf("%s: %d untranslated: %s", l, len(missing), strings.Join(missing, ", "))
		}
	}
}

// Every key the server's code names must be in the English catalog: a typo
// would otherwise show the key itself to the user. Keys are written as
// string literals ("error.…", "email.…", "notify.…", "reason.…").
func TestKeysUsedInCodeExist(t *testing.T) {
	keyRE := regexp.MustCompile(`"((?:error|email|notify|reason)\.[A-Za-z0-9_.]+)"`)
	root := filepath.Join("..")
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == "testdata" || d.Name() == "gen" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
			return nil
		}
		src, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		for _, m := range keyRE.FindAllStringSubmatch(string(src), -1) {
			if !Has(m[1]) {
				t.Errorf("%s: %q is not in locales/en", p, m[1])
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func placeholders(msg string) string {
	var names []string
	for _, m := range placeholderRE.FindAllStringSubmatch(msg, -1) {
		names = append(names, m[1])
	}
	sort.Strings(names)
	return strings.Join(names, ",")
}

func contains(list []string, s string) bool { return slices.Contains(list, s) }
