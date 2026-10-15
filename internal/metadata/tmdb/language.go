package tmdb

import (
	"context"
	"strings"

	"github.com/onscreen/onscreen/internal/metadata"
)

// defaultRegions turns a bare ISO 639-1 code into the TMDB locale its
// speakers usually mean. TMDB keys translations by language-region; a bare
// "zh" or "pt" still answers, but picks whichever region's translation it
// finds first. Codes not listed are sent as given.
var defaultRegions = map[string]string{
	"en": "en-US",
	"fr": "fr-FR",
	"de": "de-DE",
	"es": "es-ES",
	"it": "it-IT",
	"pt": "pt-BR",
	"ja": "ja-JP",
	"ko": "ko-KR",
	"zh": "zh-CN",
	"ru": "ru-RU",
	"nl": "nl-NL",
	"sv": "sv-SE",
	"pl": "pl-PL",
}

// lang is the language a details request asks TMDB for: the library's
// (metadata.WithLanguage, set by the scanner) when there is one, else the
// client's. Searches don't use it — they always run in the client's
// language, so auto-match sees the same candidate titles whatever the
// library language (TMDB searches every translation either way).
func (c *Client) lang(ctx context.Context) string {
	l := metadata.LanguageFrom(ctx)
	if l == "" {
		return c.language
	}
	if r, ok := defaultRegions[strings.ToLower(l)]; ok {
		return r
	}
	return l
}

// localized reports whether ctx asks for a language other than the
// client's, i.e. whether a missing translation should fall back.
func (c *Client) localized(ctx context.Context) bool {
	return !strings.EqualFold(c.lang(ctx), c.language)
}

// fallbackCtx is ctx asking for the client's own language. TMDB answers an
// untranslated overview with "" rather than the original text, so a
// localized details call whose overview came back empty is repeated in this
// context and the gaps filled from it.
func (c *Client) fallbackCtx(ctx context.Context) context.Context {
	return metadata.WithLanguage(ctx, c.language)
}
