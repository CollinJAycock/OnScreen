package notifyagents

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

var goldenTime = time.Date(2026, 9, 28, 18, 30, 0, 0, time.UTC)

func goldenMessage() Message {
	return Message{
		Event:    EventRequestAvailable,
		Title:    "Now available: Dune",
		Body:     "\"Dune\" is ready to watch.\nRequested by alice.",
		URL:      "https://media.example.com/watch/0192a3b4-0000-7000-8000-000000000001",
		Severity: SeveritySuccess,
		Time:     goldenTime,
	}
}

func TestBuildDiscord_Golden(t *testing.T) {
	out, err := buildDiscord("https://discord.com/api/webhooks/123/abc-DEF_9", goldenMessage())
	if err != nil {
		t.Fatal(err)
	}
	want := `{"username":"OnScreen","embeds":[{"title":"Now available: Dune","description":"\"Dune\" is ready to watch.\nRequested by alice.","url":"https://media.example.com/watch/0192a3b4-0000-7000-8000-000000000001","color":3066993,"timestamp":"2026-09-28T18:30:00Z","footer":{"text":"OnScreen"}}],"allowed_mentions":{"parse":[]}}`
	if string(out.body) != want {
		t.Errorf("discord body\n got: %s\nwant: %s", out.body, want)
	}
	if out.method != "POST" || out.url != "https://discord.com/api/webhooks/123/abc-DEF_9" ||
		out.header.Get("Content-Type") != "application/json" {
		t.Errorf("discord request = %s %s %v", out.method, out.url, out.header)
	}
}

// TestBuildDiscord_MentionsDisabledAndLimits: user-controlled text (a request
// title "@everyone") can't ping the channel, and oversize fields are cut to
// Discord's limits instead of being rejected.
func TestBuildDiscord_MentionsDisabledAndLimits(t *testing.T) {
	m := Message{
		Event: EventRequestPending, Title: "@everyone " + strings.Repeat("t", 300),
		Body: strings.Repeat("b", 5000), Severity: SeverityError,
	}
	out, err := buildDiscord("https://discord.com/api/webhooks/1/x", m)
	if err != nil {
		t.Fatal(err)
	}
	var p discordPayload
	if err := json.Unmarshal(out.body, &p); err != nil {
		t.Fatal(err)
	}
	if p.AllowedMentions.Parse == nil || len(p.AllowedMentions.Parse) != 0 {
		t.Errorf("allowed_mentions.parse = %v, want []", p.AllowedMentions.Parse)
	}
	if n := utf8.RuneCountInString(p.Embeds[0].Title); n != 256 {
		t.Errorf("title length = %d, want 256", n)
	}
	if n := utf8.RuneCountInString(p.Embeds[0].Description); n != 4096 {
		t.Errorf("description length = %d, want 4096", n)
	}
	if p.Embeds[0].Color != 0xE74C3C || p.Embeds[0].URL != "" || p.Embeds[0].Timestamp != "" {
		t.Errorf("embed = %+v", p.Embeds[0])
	}
}

func TestBuildTelegram_GoldenAndEscaping(t *testing.T) {
	m := goldenMessage()
	m.Title = `Now available: Tom & Jerry <"Uncut">`
	m.Body = `"Tom & Jerry" <b>is</b> ready.`
	m.URL = `https://media.example.com/watch/1?a=1&b="2"`
	out, err := buildTelegram("https://api.telegram.org", "123456:SECRET-token_value1234567890", "-1001234", m)
	if err != nil {
		t.Fatal(err)
	}
	if out.url != "https://api.telegram.org/bot123456:SECRET-token_value1234567890/sendMessage" {
		t.Errorf("url = %q", out.url)
	}
	var p struct {
		ChatID    string `json:"chat_id"`
		Text      string `json:"text"`
		ParseMode string `json:"parse_mode"`
		NoPreview bool   `json:"disable_web_page_preview"`
	}
	if err := json.Unmarshal(out.body, &p); err != nil {
		t.Fatal(err)
	}
	want := "<b>Now available: Tom &amp; Jerry &lt;&quot;Uncut&quot;&gt;</b>\n\n" +
		"&quot;Tom &amp; Jerry&quot; &lt;b&gt;is&lt;/b&gt; ready.\n\n" +
		`<a href="https://media.example.com/watch/1?a=1&amp;b=&quot;2&quot;">Open in OnScreen</a>`
	if p.Text != want {
		t.Errorf("text\n got: %q\nwant: %q", p.Text, want)
	}
	if p.ChatID != "-1001234" || p.ParseMode != "HTML" || !p.NoPreview {
		t.Errorf("payload = %+v", p)
	}
}

// TestBuildTelegram_TruncatesBeforeEscaping: the 4096 limit counts characters
// after entity parsing, so the raw body is cut — never an escape sequence.
func TestBuildTelegram_TruncatesBeforeEscaping(t *testing.T) {
	m := Message{Event: EventNewContent, Title: "T", Body: strings.Repeat("&", 5000)}
	out, err := buildTelegram("https://api.telegram.org", "1:x", "1", m)
	if err != nil {
		t.Fatal(err)
	}
	var p struct {
		Text string `json:"text"`
	}
	_ = json.Unmarshal(out.body, &p)
	visible := strings.ReplaceAll(p.Text, "&amp;", "&")
	visible = strings.NewReplacer("<b>", "", "</b>", "").Replace(visible)
	if n := utf8.RuneCountInString(visible); n > telegramMaxText {
		t.Errorf("visible length = %d, want <= %d", n, telegramMaxText)
	}
	if strings.HasSuffix(strings.TrimSuffix(p.Text, "…"), "&am") {
		t.Error("an entity was cut in half")
	}
}

func TestBuildNtfy_Golden(t *testing.T) {
	out, err := buildNtfy("https://ntfy.example.com/", "onscreen-house", "tk_secret", goldenMessage())
	if err != nil {
		t.Fatal(err)
	}
	if out.url != "https://ntfy.example.com/onscreen-house" || out.method != "POST" {
		t.Errorf("request = %s %s", out.method, out.url)
	}
	for h, want := range map[string]string{
		"Title":         "Now available: Dune",
		"Tags":          "tv",
		"Priority":      "3",
		"Click":         "https://media.example.com/watch/0192a3b4-0000-7000-8000-000000000001",
		"Authorization": "Bearer tk_secret",
		"Content-Type":  "text/plain; charset=utf-8",
	} {
		if got := out.header.Get(h); got != want {
			t.Errorf("header %s = %q, want %q", h, got, want)
		}
	}
	if string(out.body) != "\"Dune\" is ready to watch.\nRequested by alice." {
		t.Errorf("body = %q", out.body)
	}
}

// TestBuildNtfy_HeaderSafety: a non-ASCII title is RFC 2047-encoded and a
// newline can't inject a header; no token → no Authorization header.
func TestBuildNtfy_HeaderSafety(t *testing.T) {
	m := Message{Event: EventTaskFailed, Title: "Amélie\r\nX-Evil: 1", Body: "", Severity: SeverityError}
	out, err := buildNtfy("http://10.0.0.5:8080", "t", "", m)
	if err != nil {
		t.Fatal(err)
	}
	title := out.header.Get("Title")
	if strings.ContainsAny(title, "\r\n") || !strings.HasPrefix(title, "=?utf-8?b?") {
		t.Errorf("Title header = %q", title)
	}
	if out.header.Get("Authorization") != "" {
		t.Error("Authorization set without a token")
	}
	if out.header.Get("Priority") != "4" || out.header.Get("Tags") != "rotating_light" {
		t.Errorf("priority/tags = %q/%q", out.header.Get("Priority"), out.header.Get("Tags"))
	}
	if string(out.body) != "Amélie X-Evil: 1" {
		t.Errorf("empty body should fall back to the title, got %q", out.body)
	}
}

func TestBuildGotify_Golden(t *testing.T) {
	out, err := buildGotify("https://gotify.example.com", "AppTok3n", goldenMessage())
	if err != nil {
		t.Fatal(err)
	}
	if out.url != "https://gotify.example.com/message" {
		t.Errorf("url = %q (token must not be in the URL)", out.url)
	}
	if out.header.Get("X-Gotify-Key") != "AppTok3n" {
		t.Errorf("X-Gotify-Key = %q", out.header.Get("X-Gotify-Key"))
	}
	want := `{"title":"Now available: Dune","message":"\"Dune\" is ready to watch.\nRequested by alice.","priority":4,"extras":{"client::display":{"contentType":"text/plain"},"client::notification":{"click":{"url":"https://media.example.com/watch/0192a3b4-0000-7000-8000-000000000001"}}}}`
	if string(out.body) != want {
		t.Errorf("gotify body\n got: %s\nwant: %s", out.body, want)
	}
}

func TestBuildGotify_ErrorPriorityNoLink(t *testing.T) {
	out, _ := buildGotify("https://g.example.com", "t", Message{Event: EventBackupFailed, Title: "Backup failed", Severity: SeverityError})
	var p struct {
		Message  string         `json:"message"`
		Priority int            `json:"priority"`
		Extras   map[string]any `json:"extras"`
	}
	_ = json.Unmarshal(out.body, &p)
	if p.Priority != 8 || p.Message != "Backup failed" {
		t.Errorf("payload = %+v", p)
	}
	if _, ok := p.Extras["client::notification"]; ok {
		t.Error("click extra set without a link")
	}
}

func TestBuildEmail(t *testing.T) {
	m := goldenMessage()
	m.Body = "<script>x</script>\nline two"
	subject, text, html := buildEmail(m)
	if subject != "[OnScreen] Now available: Dune" {
		t.Errorf("subject = %q", subject)
	}
	if !strings.Contains(text, "<script>x</script>\nline two") || !strings.Contains(text, "Open in OnScreen: "+m.URL) {
		t.Errorf("text = %q", text)
	}
	if strings.Contains(html, "<script>") || !strings.Contains(html, "&lt;script&gt;x&lt;/script&gt;<br>line two") {
		t.Errorf("html body not escaped / line breaks lost: %s", html)
	}
	if !strings.Contains(html, `href="`+m.URL+`"`) {
		t.Error("html missing the link button")
	}
}
