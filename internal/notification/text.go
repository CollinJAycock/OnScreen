package notification

import "github.com/onscreen/onscreen/internal/i18n"

// Text writes a notice's title and body in one language. Each recipient's
// copy is stored on its own, so the service calls Text once per recipient in
// their users.ui_language (English when they chose none); a notice to every
// admin reads in each admin's language. The outbound notification agents
// (Discord, ntfy, …) get it once in English: they are shared ops channels
// with no single reader, and notifyagents parses the English wording.
//
// Build it from the notify.* catalog (internal/i18n/locales/*/notify.json),
// with Keys or with i18n.T / i18n.TN. Titles, usernames, library names and
// other user content go in as placeholder values and are never translated.
type Text func(l i18n.Lang) (title, body string)

// Keys is a notice whose title and body are catalog keys, each with its own
// placeholder values. An empty bodyKey leaves the body empty.
func Keys(titleKey string, titleParams i18n.Params, bodyKey string, bodyParams i18n.Params) Text {
	return func(l i18n.Lang) (string, string) {
		var body string
		if bodyKey != "" {
			body = i18n.T(l, bodyKey, bodyParams)
		}
		return i18n.T(l, titleKey, titleParams), body
	}
}
