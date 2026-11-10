// Package i18n holds the server's own text in the languages the apps ship
// (English, Spanish, Simplified Chinese): the API errors a person can see,
// emails, in-app notifications and the Now Playing transcode reasons
// (docs/i18n.md, stage 5).
//
// The catalogs follow the web app's rules: flat JSON in locales/<language>/,
// one file per area, {placeholder} values, and plural forms as key_one /
// key_other (Spanish may add key_many; Chinese has only key_other). English
// is the source; a key a translation lacks falls back to English, then to
// the key itself, so text is never blank.
//
// Which language:
//   - A request: its Accept-Language header. The web and TV apps send the
//     language they show, so an error reads like the screen around it. A
//     client that sends none gets English.
//   - Mail and notifications: the recipient's users.ui_language, else the
//     fallback the caller passes (the language of the request that caused
//     it, or English).
package i18n

import (
	"maps"
	"net/http"
	"sort"
	"strconv"
	"strings"
)

// Lang is a shipped interface language, as the BCP 47 tag the apps and
// users.ui_language use.
type Lang string

const (
	English           Lang = "en"
	Spanish           Lang = "es"
	SimplifiedChinese Lang = "zh-Hans"
)

// Supported lists the shipped languages. It mirrors the users.ui_language
// CHECK (migration 00063) and the apps' LOCALES.
var Supported = []Lang{English, Spanish, SimplifiedChinese}

// Parse returns the shipped language with exactly this tag ("en", "es",
// "zh-Hans": the users.ui_language values).
func Parse(tag string) (Lang, bool) {
	for _, l := range Supported {
		if string(l) == tag {
			return l, true
		}
	}
	return "", false
}

// ForUser is the language a user chose (users.ui_language), else fallback.
func ForUser(uiLanguage *string, fallback Lang) Lang {
	if uiLanguage != nil {
		if l, ok := Parse(*uiLanguage); ok {
			return l
		}
	}
	return fallback
}

// FromRequest is the language r asks for in Accept-Language, else English.
func FromRequest(r *http.Request) Lang {
	if r == nil {
		return English
	}
	return MatchAcceptLanguage(r.Header.Get("Accept-Language"))
}

// MatchAcceptLanguage picks the best shipped language from an
// Accept-Language header, by the web app's rules (matchLocale in
// web/src/lib/i18n): es-* is Spanish, en-* English, and zh, zh-CN, zh-SG,
// zh-MY and zh-Hans* Simplified Chinese. Traditional Chinese (zh-TW, zh-HK,
// zh-Hant) isn't shipped, so the next tag decides. No match is English.
func MatchAcceptLanguage(header string) Lang {
	type tagQ struct {
		tag string
		q   float64
		at  int
	}
	var tags []tagQ
	for i, part := range strings.Split(header, ",") {
		fields := strings.Split(strings.TrimSpace(part), ";")
		tag := strings.TrimSpace(fields[0])
		if tag == "" || tag == "*" {
			continue
		}
		q := 1.0
		for _, f := range fields[1:] {
			f = strings.TrimSpace(f)
			if v, ok := strings.CutPrefix(f, "q="); ok {
				if n, err := strconv.ParseFloat(v, 64); err == nil {
					q = n
				}
			}
		}
		if q <= 0 {
			continue
		}
		tags = append(tags, tagQ{tag, q, i})
	}
	sort.SliceStable(tags, func(a, b int) bool { return tags[a].q > tags[b].q })
	for _, t := range tags {
		if l, ok := matchTag(t.tag); ok {
			return l
		}
	}
	return English
}

func matchTag(raw string) (Lang, bool) {
	parts := strings.Split(strings.ToLower(strings.ReplaceAll(raw, "_", "-")), "-")
	switch parts[0] {
	case "en":
		return English, true
	case "es":
		return Spanish, true
	case "zh":
		if len(parts) == 1 {
			return SimplifiedChinese, true
		}
		switch parts[1] {
		case "hans", "cn", "sg", "my":
			return SimplifiedChinese, true
		}
	}
	return "", false
}

// Params fills a message's {placeholders}. Integers are written in the
// language's style ("1,234" / "12.345").
type Params map[string]any

// T is the text for key in l, with its placeholders filled from p.
func T(l Lang, key string, p Params) string {
	msg, ok := lookup(l, key)
	if !ok {
		return key
	}
	return interpolate(l, msg, p)
}

// TN is the plural form of key for count in l (key_one, key_many or
// key_other, as the language's rules pick), with {count} and p filled in.
func TN(l Lang, key string, count int, p Params) string {
	form := pluralForm(l, count)
	msg, ok := lookupExact(l, key+"_"+form)
	if !ok {
		msg, ok = lookupExact(l, key+"_other")
	}
	if !ok {
		msg, ok = lookupExact(English, key+"_"+pluralForm(English, count))
	}
	if !ok {
		msg, ok = lookupExact(English, key+"_other")
	}
	if !ok {
		return key
	}
	all := Params{"count": count}
	maps.Copy(all, p)
	return interpolate(l, msg, all)
}

// Has reports whether the English catalog has key (or its plural forms).
func Has(key string) bool {
	if _, ok := lookupExact(English, key); ok {
		return true
	}
	_, ok := lookupExact(English, key+"_other")
	return ok
}

func lookup(l Lang, key string) (string, bool) {
	if msg, ok := lookupExact(l, key); ok {
		return msg, true
	}
	return lookupExact(English, key)
}

func lookupExact(l Lang, key string) (string, bool) {
	msg, ok := catalogs()[l][key]
	return msg, ok
}

// pluralForm follows CLDR for the shipped languages: English one/other,
// Spanish one/many/other (many: a non-zero multiple of a million, which the
// catalogs may leave to _other), Chinese other only.
func pluralForm(l Lang, n int) string {
	switch l {
	case SimplifiedChinese:
		return "other"
	case Spanish:
		if n == 1 {
			return "one"
		}
		if n != 0 && n%1_000_000 == 0 {
			return "many"
		}
		return "other"
	default:
		if n == 1 {
			return "one"
		}
		return "other"
	}
}

func interpolate(l Lang, msg string, p Params) string {
	if len(p) == 0 || !strings.Contains(msg, "{") {
		return msg
	}
	var b strings.Builder
	for i := 0; i < len(msg); i++ {
		if msg[i] == '{' {
			if end := strings.IndexByte(msg[i:], '}'); end > 1 {
				name := msg[i+1 : i+end]
				if v, ok := p[name]; ok && isWord(name) {
					b.WriteString(format(l, v))
					i += end
					continue
				}
			}
		}
		b.WriteByte(msg[i])
	}
	return b.String()
}

func isWord(s string) bool {
	for _, c := range s {
		if !(c == '_' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9') {
			return false
		}
	}
	return s != ""
}

func format(l Lang, v any) string {
	switch n := v.(type) {
	case int:
		return FormatInt(l, int64(n))
	case int32:
		return FormatInt(l, int64(n))
	case int64:
		return FormatInt(l, n)
	case string:
		return n
	case float64:
		return strconv.FormatFloat(n, 'f', -1, 64)
	case interface{ String() string }:
		return n.String()
	}
	return ""
}

// FormatInt writes n with the language's digit grouping, as the apps'
// Intl.NumberFormat does: "1,234" in English and Chinese; Spanish groups
// only from five digits ("1234", "12.345").
func FormatInt(l Lang, n int64) string {
	s := strconv.FormatInt(n, 10)
	neg := strings.HasPrefix(s, "-")
	if neg {
		s = s[1:]
	}
	sep, minDigits := ",", 4
	if l == Spanish {
		sep, minDigits = ".", 5
	}
	if len(s) >= minDigits {
		var b strings.Builder
		lead := len(s) % 3
		if lead > 0 {
			b.WriteString(s[:lead])
		}
		for i := lead; i < len(s); i += 3 {
			if b.Len() > 0 {
				b.WriteString(sep)
			}
			b.WriteString(s[i : i+3])
		}
		s = b.String()
	}
	if neg {
		return "-" + s
	}
	return s
}
