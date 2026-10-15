package metadata

import (
	"context"
	"strings"
)

type languageKey struct{}

// WithLanguage marks ctx with a library's metadata language (an ISO 639-1
// code such as "fr", or a region-qualified one such as "pt-BR"). The scanner
// sets it before enriching an item, and agents that can localise read it with
// LanguageFrom. An empty lang leaves ctx unchanged.
func WithLanguage(ctx context.Context, lang string) context.Context {
	lang = strings.TrimSpace(lang)
	if lang == "" {
		return ctx
	}
	return context.WithValue(ctx, languageKey{}, lang)
}

// LanguageFrom returns the metadata language WithLanguage set, or "" when
// none was set (the agent then uses its own default).
func LanguageFrom(ctx context.Context) string {
	lang, _ := ctx.Value(languageKey{}).(string)
	return lang
}
