package podcast

import (
	"errors"
	"net/url"
	"strings"
	"testing"
	"time"
)

const sampleFeed = `<?xml version="1.0" encoding="UTF-8"?>
<rss version="2.0"
     xmlns:itunes="http://www.itunes.com/dtds/podcast-1.0.dtd"
     xmlns:content="http://purl.org/rss/1.0/modules/content/">
  <channel>
    <title>Night &amp; Day Radio</title>
    <link>https://example.com/show</link>
    <language>en-us</language>
    <description><![CDATA[<p>A show about <b>things</b>.</p><p>Second paragraph.</p>]]></description>
    <itunes:author>Jane Host</itunes:author>
    <itunes:image href="https://cdn.example.com/art.jpg"/>
    <image><url>https://cdn.example.com/rss-art.jpg</url></image>
    <item>
      <title>Episode 12: The Return</title>
      <itunes:title>The Return</itunes:title>
      <guid isPermaLink="false">ep-12</guid>
      <pubDate>Mon, 12 Oct 2026 07:00:00 +0000</pubDate>
      <description>Short summary.</description>
      <content:encoded><![CDATA[<p>Full notes with <a href="https://x">a link</a>.</p><script>alert(1)</script>]]></content:encoded>
      <enclosure url="https://cdn.example.com/ep12.mp3?tracking=1" length="31457280" type="audio/mpeg"/>
      <itunes:duration>1:02:03</itunes:duration>
      <itunes:season>2</itunes:season>
      <itunes:episode>12</itunes:episode>
      <itunes:image href="/ep12.jpg"/>
    </item>
    <item>
      <title>Bonus: no guid</title>
      <pubDate>Fri, 9 Oct 2026 18:30:00 GMT</pubDate>
      <enclosure url="ep11.m4a" type="audio/x-m4a"/>
      <itunes:duration>754</itunes:duration>
    </item>
    <item>
      <title>A video episode</title>
      <guid>vid-1</guid>
      <enclosure url="https://cdn.example.com/v.mp4" type="video/mp4"/>
    </item>
    <item>
      <title>An item with no enclosure</title>
      <guid>blog-1</guid>
    </item>
    <item>
      <title>Repeat of ep 12</title>
      <guid isPermaLink="false">ep-12</guid>
      <enclosure url="https://cdn.example.com/ep12-repeat.mp3" type="audio/mpeg"/>
    </item>
  </channel>
</rss>`

func mustURL(t *testing.T, s string) *url.URL {
	t.Helper()
	u, err := url.Parse(s)
	if err != nil {
		t.Fatal(err)
	}
	return u
}

func TestParse_Channel(t *testing.T) {
	f, err := Parse(strings.NewReader(sampleFeed), mustURL(t, "https://feeds.example.com/night.xml"))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if f.Title != "Night & Day Radio" {
		t.Errorf("Title = %q", f.Title)
	}
	if f.Author != "Jane Host" {
		t.Errorf("Author = %q", f.Author)
	}
	if f.Description != "A show about things.\n\nSecond paragraph." {
		t.Errorf("Description = %q", f.Description)
	}
	// itunes:image wins over the RSS <image>.
	if f.ImageURL != "https://cdn.example.com/art.jpg" {
		t.Errorf("ImageURL = %q", f.ImageURL)
	}
	if f.SiteURL != "https://example.com/show" || f.Language != "en-us" {
		t.Errorf("SiteURL/Language = %q/%q", f.SiteURL, f.Language)
	}
}

func TestParse_Episodes(t *testing.T) {
	f, err := Parse(strings.NewReader(sampleFeed), mustURL(t, "https://feeds.example.com/night.xml"))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	// Video and enclosure-less items are skipped; the repeated guid is
	// dropped (the first one is kept).
	if len(f.Episodes) != 2 || f.Skipped != 2 {
		t.Fatalf("episodes=%d skipped=%d, want 2 and 2", len(f.Episodes), f.Skipped)
	}

	ep := f.Episodes[0]
	// The plain <title> wins over <itunes:title>.
	if ep.Title != "Episode 12: The Return" {
		t.Errorf("Title = %q", ep.Title)
	}
	if ep.GUID != "ep-12" {
		t.Errorf("GUID = %q", ep.GUID)
	}
	// content:encoded is preferred, as plain text, without the script.
	if ep.Description != "Full notes with a link." {
		t.Errorf("Description = %q", ep.Description)
	}
	want := time.Date(2026, 10, 12, 7, 0, 0, 0, time.UTC)
	if ep.PublishedAt == nil || !ep.PublishedAt.Equal(want) {
		t.Errorf("PublishedAt = %v, want %v", ep.PublishedAt, want)
	}
	if ep.DurationMS == nil || *ep.DurationMS != 3723000 {
		t.Errorf("DurationMS = %v, want 3723000", ep.DurationMS)
	}
	if ep.Season == nil || *ep.Season != 2 || ep.Episode == nil || *ep.Episode != 12 {
		t.Errorf("Season/Episode = %v/%v", ep.Season, ep.Episode)
	}
	if ep.EnclosureLength == nil || *ep.EnclosureLength != 31457280 {
		t.Errorf("EnclosureLength = %v", ep.EnclosureLength)
	}
	if ep.ImageURL != "https://feeds.example.com/ep12.jpg" {
		t.Errorf("ImageURL = %q (relative href resolves against the feed URL)", ep.ImageURL)
	}

	bonus := f.Episodes[1]
	// No guid: the enclosure URL stands in, resolved against the feed URL.
	if bonus.GUID != "https://feeds.example.com/ep11.m4a" || bonus.EnclosureURL != bonus.GUID {
		t.Errorf("GUID = %q, EnclosureURL = %q", bonus.GUID, bonus.EnclosureURL)
	}
	if bonus.DurationMS == nil || *bonus.DurationMS != 754000 {
		t.Errorf("DurationMS = %v", bonus.DurationMS)
	}
	if bonus.PublishedAt == nil || bonus.PublishedAt.Day() != 9 {
		t.Errorf("PublishedAt = %v", bonus.PublishedAt)
	}
}

func TestParse_NotAFeed(t *testing.T) {
	for name, doc := range map[string]string{
		"html page": `<!DOCTYPE html><html><head><title>Podcast</title></head><body>hi</body></html>`,
		"atom":      `<?xml version="1.0"?><feed xmlns="http://www.w3.org/2005/Atom"><title>x</title></feed>`,
		"garbage":   `{"not": "xml"}`,
		"no title":  `<rss><channel><item><enclosure url="https://a/b.mp3" type="audio/mpeg"/></item></channel></rss>`,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := Parse(strings.NewReader(doc), nil); !errors.Is(err, ErrNotAFeed) {
				t.Errorf("Parse = %v, want ErrNotAFeed", err)
			}
		})
	}
}

// Older feeds spell the iTunes namespace in mixed case.
func TestParse_ITunesNamespaceAnyCase(t *testing.T) {
	doc := `<rss xmlns:itunes="http://www.itunes.com/DTDs/Podcast-1.0.dtd"><channel><title>T</title>
<itunes:image href="https://a/art.png"/>
<item><title>E</title><enclosure url="https://a/e.mp3" type="audio/mpeg"/><itunes:duration>90</itunes:duration></item>
</channel></rss>`
	f, err := Parse(strings.NewReader(doc), nil)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if f.ImageURL != "https://a/art.png" {
		t.Errorf("ImageURL = %q", f.ImageURL)
	}
	if d := f.Episodes[0].DurationMS; d == nil || *d != 90000 {
		t.Errorf("DurationMS = %v", d)
	}
}

func TestParse_Latin1(t *testing.T) {
	doc := "<?xml version=\"1.0\" encoding=\"ISO-8859-1\"?><rss><channel><title>Caf\xe9 Talk</title>" +
		"<item><title>\xc9pisode</title><enclosure url=\"https://a/e.mp3\" type=\"audio/mpeg\"/></item></channel></rss>"
	f, err := Parse(strings.NewReader(doc), nil)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if f.Title != "Café Talk" || f.Episodes[0].Title != "Épisode" {
		t.Errorf("Title = %q, episode = %q", f.Title, f.Episodes[0].Title)
	}
}

// An enclosure without a MIME type is accepted by its audio extension; a
// non-http(s) enclosure is not.
func TestParse_EnclosureByExtension(t *testing.T) {
	doc := `<rss><channel><title>T</title>
<item><title>A</title><enclosure url="https://a/one.opus"/></item>
<item><title>B</title><enclosure url="https://a/two.bin" type="application/octet-stream"/></item>
<item><title>C</title><enclosure url="ftp://a/three.mp3" type="audio/mpeg"/></item>
</channel></rss>`
	f, err := Parse(strings.NewReader(doc), nil)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(f.Episodes) != 1 || f.Episodes[0].Title != "A" || f.Skipped != 2 {
		t.Errorf("episodes = %+v, skipped = %d; want only A", f.Episodes, f.Skipped)
	}
}

func TestParseDuration(t *testing.T) {
	for in, want := range map[string]int64{
		"3723":    3723000,
		"3723.5":  3723500,
		"62:03":   3723000,
		"1:02:03": 3723000,
		" 0:45 ":  45000,
	} {
		if got, ok := parseDuration(in); !ok || got != want {
			t.Errorf("parseDuration(%q) = %d, %v; want %d", in, got, ok, want)
		}
	}
	for _, in := range []string{"", "abc", "1:2:3:4", "-5", "0", "1.5:00"} {
		if got, ok := parseDuration(in); ok {
			t.Errorf("parseDuration(%q) = %d, want rejected", in, got)
		}
	}
}

func TestParseDate(t *testing.T) {
	want := time.Date(2026, 10, 12, 7, 0, 0, 0, time.UTC)
	for _, in := range []string{
		"Mon, 12 Oct 2026 07:00:00 +0000",
		"Mon, 12 Oct 2026 07:00:00 GMT",
		"Mon, 12 Oct 2026 03:00:00 -0400",
		"12 Oct 2026 07:00:00 +0000",
		"Mon, 12 Oct 2026 07:00 +0000",
		"Mon,  12 Oct 2026   07:00:00 UT",
		"2026-10-12T07:00:00Z",
	} {
		got, ok := parseDate(in)
		if !ok || !got.Equal(want) {
			t.Errorf("parseDate(%q) = %v, %v; want %v", in, got, ok, want)
		}
	}
	if _, ok := parseDate("next tuesday"); ok {
		t.Error("parseDate accepted nonsense")
	}
}

func TestEpisodeExt(t *testing.T) {
	for _, c := range []struct{ url, mime, want string }{
		{"https://a/e.MP3?x=1", "audio/mpeg", ".mp3"},
		{"https://a/e.m4a", "", ".m4a"},
		{"https://a/e.oga", "", ".ogg"},
		{"https://a/stream", "audio/x-m4a", ".m4a"},
		{"https://a/stream", "audio/ogg; codecs=opus", ".ogg"},
		{"https://a/stream", "", ".mp3"},
	} {
		if got := EpisodeExt(c.url, c.mime); got != c.want {
			t.Errorf("EpisodeExt(%q, %q) = %q, want %q", c.url, c.mime, got, c.want)
		}
	}
}

func TestPlainText(t *testing.T) {
	for in, want := range map[string]string{
		"plain   text":                              "plain text",
		"Tom &amp; Jerry":                           "Tom & Jerry",
		"<p>One</p><p>Two</p>":                      "One\n\nTwo",
		"a<br>b<br/><br/>c":                         "a\nb\n\nc",
		"<style>p{}</style>Shown<script>x</script>": "Shown",
	} {
		if got := plainText(in); got != want {
			t.Errorf("plainText(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestClip_KeepsRunesWhole(t *testing.T) {
	s := strings.Repeat("é", 10) // 20 bytes
	got := clip(s, 5)
	if got != "éé" {
		t.Errorf("clip = %q, want two whole runes", got)
	}
}
