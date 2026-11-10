package i18n

import (
	"net/http/httptest"
	"testing"
)

func TestMatchAcceptLanguage(t *testing.T) {
	cases := []struct {
		header string
		want   Lang
	}{
		{"", English},
		{"es", Spanish},
		{"es-MX,es;q=0.9,en;q=0.8", Spanish},
		{"zh-Hans", SimplifiedChinese},
		{"zh-CN", SimplifiedChinese},
		{"zh", SimplifiedChinese},
		{"zh-Hans-CN", SimplifiedChinese},
		{"zh_SG", SimplifiedChinese},
		// Traditional Chinese isn't shipped: the next tag decides.
		{"zh-TW,es;q=0.5", Spanish},
		{"zh-Hant-HK", English},
		{"fr-FR,de;q=0.9", English},
		// q-values order the tags, not their position.
		{"en;q=0.3, es;q=0.9", Spanish},
		{"es;q=0, en", English},
		{"*", English},
	}
	for _, c := range cases {
		if got := MatchAcceptLanguage(c.header); got != c.want {
			t.Errorf("MatchAcceptLanguage(%q) = %q, want %q", c.header, got, c.want)
		}
	}
}

func TestFromRequestAndForUser(t *testing.T) {
	r := httptest.NewRequest("GET", "/", nil)
	if got := FromRequest(r); got != English {
		t.Errorf("no header: %q", got)
	}
	r.Header.Set("Accept-Language", "zh-Hans")
	if got := FromRequest(r); got != SimplifiedChinese {
		t.Errorf("zh-Hans header: %q", got)
	}
	if got := FromRequest(nil); got != English {
		t.Errorf("nil request: %q", got)
	}
	es, bad := "es", "fr"
	if got := ForUser(&es, English); got != Spanish {
		t.Errorf("ForUser(es) = %q", got)
	}
	if got := ForUser(&bad, SimplifiedChinese); got != SimplifiedChinese {
		t.Errorf("ForUser(fr) = %q, want the fallback", got)
	}
	if got := ForUser(nil, Spanish); got != Spanish {
		t.Errorf("ForUser(nil) = %q, want the fallback", got)
	}
}

func TestTranslateWithFallbacks(t *testing.T) {
	if got := T(English, "error.notFound", nil); got != "resource not found" {
		t.Errorf("en: %q", got)
	}
	if got := T(Spanish, "error.notFound", nil); got == "resource not found" || got == "" {
		t.Errorf("es: %q, want the Spanish text", got)
	}
	// A key no catalog has shows as itself, never blank.
	if got := T(Spanish, "error.noSuchKey", nil); got != "error.noSuchKey" {
		t.Errorf("unknown key: %q", got)
	}
}

func TestInterpolateAndPlurals(t *testing.T) {
	c := map[Lang]map[string]string{
		English:           {"x.files_one": "{count} file in {lib}", "x.files_other": "{count} files in {lib}", "x.raw": "{a} {missing} {}"},
		Spanish:           {"x.files_one": "{count} archivo en {lib}", "x.files_other": "{count} archivos en {lib}"},
		SimplifiedChinese: {"x.files_other": "{lib} 中有 {count} 个文件"},
	}
	restore := loaded
	loadOnce.Do(func() {})
	loaded = c
	defer func() { loaded = restore }()

	if got := TN(English, "x.files", 1, Params{"lib": "Movies"}); got != "1 file in Movies" {
		t.Errorf("en one: %q", got)
	}
	if got := TN(English, "x.files", 1234, Params{"lib": "Movies"}); got != "1,234 files in Movies" {
		t.Errorf("en other: %q", got)
	}
	if got := TN(Spanish, "x.files", 1234, Params{"lib": "Películas"}); got != "1234 archivos en Películas" {
		t.Errorf("es other (no grouping under five digits): %q", got)
	}
	if got := TN(Spanish, "x.files", 2_000_000, Params{"lib": "P"}); got != "2.000.000 archivos en P" {
		t.Errorf("es many falls back to other: %q", got)
	}
	if got := TN(SimplifiedChinese, "x.files", 1, Params{"lib": "电影"}); got != "电影 中有 1 个文件" {
		t.Errorf("zh: %q", got)
	}
	// A placeholder without a value stays as written.
	if got := T(English, "x.raw", Params{"a": "A"}); got != "A {missing} {}" {
		t.Errorf("raw: %q", got)
	}
}

func TestFormatInt(t *testing.T) {
	cases := []struct {
		l    Lang
		n    int64
		want string
	}{
		{English, 999, "999"}, {English, 1234, "1,234"}, {English, -1234567, "-1,234,567"},
		{Spanish, 1234, "1234"}, {Spanish, 12345, "12.345"}, {Spanish, 1234567, "1.234.567"},
		{SimplifiedChinese, 1234, "1,234"},
	}
	for _, c := range cases {
		if got := FormatInt(c.l, c.n); got != c.want {
			t.Errorf("FormatInt(%s, %d) = %q, want %q", c.l, c.n, got, c.want)
		}
	}
}
