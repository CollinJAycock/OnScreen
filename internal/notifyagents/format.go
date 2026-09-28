package notifyagents

import (
	"bytes"
	"encoding/json"
	"fmt"
	"mime"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/onscreen/onscreen/internal/email"
)

// Message is one rendered event, ready for any kind's formatter.
type Message struct {
	Event string
	Title string
	Body  string
	// URL is an absolute http(s) "Open in OnScreen" link, or "" when the
	// server has no public base URL configured.
	URL      string
	Severity Severity
	Time     time.Time

	// dedupe tells apart events that render the same text (two private-
	// library titles, a second fleet outage) so the dedupe window doesn't
	// swallow the later one. "" = the text alone identifies the event.
	dedupe string
}

// outbound is an HTTP request a formatter produced. url may embed the
// secret (Discord webhook URL, Telegram bot path) — never log it.
type outbound struct {
	method string
	url    string
	header http.Header
	body   []byte
}

const userAgent = "OnScreen-Notify/1"

// Discord embed colors per severity.
var discordColors = map[Severity]int{
	SeverityInfo:    0x7C6AF7, // OnScreen accent
	SeveritySuccess: 0x2ECC71,
	SeverityWarning: 0xF1C40F,
	SeverityError:   0xE74C3C,
}

// truncateRunes cuts s to at most n runes, marking the cut with "…".
func truncateRunes(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)
	return string(r[:n-1]) + "…"
}

// singleLine folds CR/LF and runs of whitespace — for titles and HTTP header
// values, where a newline would be header injection.
func singleLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

func jsonBody(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimRight(buf.Bytes(), "\n"), nil
}

// ── Discord ──────────────────────────────────────────────────────────────────

type discordEmbed struct {
	Title       string `json:"title"`
	Description string `json:"description,omitempty"`
	URL         string `json:"url,omitempty"`
	Color       int    `json:"color"`
	Timestamp   string `json:"timestamp,omitempty"`
	Footer      struct {
		Text string `json:"text"`
	} `json:"footer"`
}

type discordPayload struct {
	Username string         `json:"username"`
	Embeds   []discordEmbed `json:"embeds"`
	// AllowedMentions with an empty parse list: a request title or username
	// containing "@everyone" or a role mention must not ping the channel.
	AllowedMentions struct {
		Parse []string `json:"parse"`
	} `json:"allowed_mentions"`
}

// buildDiscord renders m as a Discord webhook execution with one embed.
// Limits: title 256, description 4096 characters.
func buildDiscord(webhookURL string, m Message) (*outbound, error) {
	e := discordEmbed{
		Title:       truncateRunes(singleLine(m.Title), 256),
		Description: truncateRunes(m.Body, 4096),
		URL:         m.URL,
		Color:       discordColors[m.Severity],
	}
	if !m.Time.IsZero() {
		e.Timestamp = m.Time.UTC().Format(time.RFC3339)
	}
	e.Footer.Text = "OnScreen"
	p := discordPayload{Username: "OnScreen", Embeds: []discordEmbed{e}}
	p.AllowedMentions.Parse = []string{}
	body, err := jsonBody(p)
	if err != nil {
		return nil, fmt.Errorf("discord payload: %w", err)
	}
	h := http.Header{}
	h.Set("Content-Type", "application/json")
	h.Set("User-Agent", userAgent)
	return &outbound{method: http.MethodPost, url: webhookURL, header: h, body: body}, nil
}

// ── Telegram ─────────────────────────────────────────────────────────────────

// telegramEscape escapes text for Telegram's HTML parse mode, which accepts
// exactly these entities (&lt; &gt; &amp; &quot;).
func telegramEscape(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;").Replace(s)
}

// telegramMaxText is sendMessage's limit, counted after entity parsing.
const telegramMaxText = 4096

// buildTelegram renders m as a sendMessage call in HTML parse mode.
func buildTelegram(apiBase, token, chatID string, m Message) (*outbound, error) {
	title := singleLine(m.Title)
	link := ""
	if m.URL != "" {
		link = "Open in OnScreen"
	}
	// Budget the visible (post-parse) text: title, two blank-line
	// separators, the body and the link label.
	budget := telegramMaxText - utf8.RuneCountInString(title) - utf8.RuneCountInString(link) - 4
	if budget < 0 {
		budget = 0
	}
	body := m.Body
	if budget == 0 {
		body = ""
	} else {
		body = truncateRunes(body, budget)
	}
	var b strings.Builder
	b.WriteString("<b>" + telegramEscape(title) + "</b>")
	if body != "" {
		b.WriteString("\n\n" + telegramEscape(body))
	}
	if link != "" {
		b.WriteString("\n\n<a href=\"" + telegramEscape(m.URL) + "\">" + link + "</a>")
	}
	payload := struct {
		ChatID                string `json:"chat_id"`
		Text                  string `json:"text"`
		ParseMode             string `json:"parse_mode"`
		DisableWebPagePreview bool   `json:"disable_web_page_preview"`
	}{ChatID: chatID, Text: b.String(), ParseMode: "HTML", DisableWebPagePreview: true}
	raw, err := jsonBody(payload)
	if err != nil {
		return nil, fmt.Errorf("telegram payload: %w", err)
	}
	h := http.Header{}
	h.Set("Content-Type", "application/json")
	h.Set("User-Agent", userAgent)
	return &outbound{
		method: http.MethodPost,
		url:    strings.TrimRight(apiBase, "/") + "/bot" + token + "/sendMessage",
		header: h,
		body:   raw,
	}, nil
}

// ── ntfy ─────────────────────────────────────────────────────────────────────

// ntfyTags are the emoji shortcodes ntfy shows before the title.
var ntfyTags = map[string]string{
	EventRequestPending:         "inbox_tray",
	EventRequestApproved:        "white_check_mark",
	EventRequestAvailable:       "tv",
	EventRequestSeasonAvailable: "tv",
	EventRequestFailed:          "x",
	EventIssueReported:          "warning",
	EventNewContent:             "new",
	EventTaskFailed:             "rotating_light",
	EventBackupFailed:           "floppy_disk,rotating_light",
	EventWorkerFleetDown:        "rotating_light",
	EventTest:                   "test_tube",
}

// ntfyPriority maps severity to ntfy's 1..5 scale (3 = default).
func ntfyPriority(s Severity) string {
	if s == SeverityError || s == SeverityWarning {
		return "4"
	}
	return "3"
}

// headerValue makes s safe for an HTTP header: one line, RFC 2047-encoded
// when it isn't plain ASCII (ntfy decodes encoded words in Title / Tags).
func headerValue(s string) string {
	return mime.BEncoding.Encode("utf-8", singleLine(s))
}

// buildNtfy renders m as an ntfy publish: the body is the message, metadata
// rides in the Title / Tags / Priority / Click headers.
func buildNtfy(serverURL, topic, token string, m Message) (*outbound, error) {
	h := http.Header{}
	h.Set("Content-Type", "text/plain; charset=utf-8")
	h.Set("User-Agent", userAgent)
	h.Set("Title", headerValue(truncateRunes(singleLine(m.Title), 250)))
	if tags := ntfyTags[m.Event]; tags != "" {
		h.Set("Tags", tags)
	}
	h.Set("Priority", ntfyPriority(m.Severity))
	if m.URL != "" {
		h.Set("Click", m.URL)
	}
	if token != "" {
		h.Set("Authorization", "Bearer "+token)
	}
	body := m.Body
	if body == "" {
		body = singleLine(m.Title)
	}
	return &outbound{
		method: http.MethodPost,
		url:    strings.TrimRight(serverURL, "/") + "/" + url.PathEscape(topic),
		header: h,
		body:   []byte(truncateRunes(body, 4000)),
	}, nil
}

// ── Gotify ───────────────────────────────────────────────────────────────────

// gotifyPriority maps severity to Gotify's 0..10 scale (>= 8 is high).
func gotifyPriority(s Severity) int {
	switch s {
	case SeverityError:
		return 8
	case SeverityWarning:
		return 6
	case SeverityInfo, SeveritySuccess:
		return 4
	}
	return 4
}

// buildGotify renders m as a POST /message. The app token rides in the
// X-Gotify-Key header, not the query string, so it can't surface in a proxy
// access log or a url.Error.
func buildGotify(serverURL, token string, m Message) (*outbound, error) {
	extras := map[string]any{
		"client::display": map[string]string{"contentType": "text/plain"},
	}
	if m.URL != "" {
		extras["client::notification"] = map[string]any{"click": map[string]string{"url": m.URL}}
	}
	payload := struct {
		Title    string         `json:"title"`
		Message  string         `json:"message"`
		Priority int            `json:"priority"`
		Extras   map[string]any `json:"extras"`
	}{
		Title:    truncateRunes(singleLine(m.Title), 250),
		Message:  m.Body,
		Priority: gotifyPriority(m.Severity),
		Extras:   extras,
	}
	if payload.Message == "" {
		payload.Message = payload.Title
	}
	raw, err := jsonBody(payload)
	if err != nil {
		return nil, fmt.Errorf("gotify payload: %w", err)
	}
	h := http.Header{}
	h.Set("Content-Type", "application/json")
	h.Set("User-Agent", userAgent)
	h.Set("X-Gotify-Key", token)
	return &outbound{
		method: http.MethodPost,
		url:    strings.TrimRight(serverURL, "/") + "/message",
		header: h,
		body:   raw,
	}, nil
}

// ── Email ────────────────────────────────────────────────────────────────────

// buildEmail renders m as a subject plus text and HTML bodies.
func buildEmail(m Message) (subject, textBody, htmlBody string) {
	subject = "[OnScreen] " + truncateRunes(singleLine(m.Title), 150)
	textBody, htmlBody = email.NotificationEmail(singleLine(m.Title), m.Body, m.URL)
	return subject, textBody, htmlBody
}
