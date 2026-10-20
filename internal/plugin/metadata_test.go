package plugin

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
)

func newMetadataFixture(t *testing.T, stubs ...*toolStub) (*MetadataDispatcher, *mockRegistryDB) {
	t.Helper()
	db := newMockRegistryDB()
	reg := NewRegistry(db)
	for i, s := range stubs {
		if _, err := reg.Create(context.Background(), CreateInput{
			Name: "meta-" + string(rune('a'+i)), Role: RoleMetadata,
			EndpointURL: s.endpoint(), Enabled: true,
			AllowedHosts: []string{"image.example.org"},
		}); err != nil {
			t.Fatalf("create plugin: %v", err)
		}
	}
	d := NewMetadataDispatcher(reg, discardLogger(), nil)
	t.Cleanup(d.Close)
	return d, db
}

func TestMetadataDispatcher_StructuredResult(t *testing.T) {
	allowPrivateIPs(t)
	stub := newToolStub(t, EnrichToolName, func(map[string]any) (*mcp.CallToolResult, error) {
		return mcp.NewToolResultStructuredOnly(map[string]any{
			"mode":                    "override",
			"summary":                 "  A replicant hunter.  ",
			"year":                    2017,
			"rating":                  8.1,
			"genres":                  []string{"Sci-Fi", "sci-fi", "Drama"},
			"originally_available_at": "2017-10-06",
			"poster_url":              "https://image.example.org/p/br.jpg",
			"provider_ids":            map[string]any{"tmdb": 335984, "mal": 0},
		}), nil
	})
	d, _ := newMetadataFixture(t, stub)

	idx := 3
	got := d.Enrich(context.Background(), MetadataRequest{
		ItemID: "item-1", ItemType: "movie", Title: "Blade Runner 2049",
		Index: &idx, FileName: "Blade.Runner.2049.mkv", LockedFields: []string{"title"},
	})
	if len(got) != 1 {
		t.Fatalf("expected 1 result, got %d", len(got))
	}
	r := got[0]
	if r.Mode != MetadataModeOverride {
		t.Errorf("mode = %q", r.Mode)
	}
	if r.Summary == nil || *r.Summary != "A replicant hunter." {
		t.Errorf("summary not trimmed/kept: %v", r.Summary)
	}
	if r.Year == nil || *r.Year != 2017 || r.Rating == nil || *r.Rating != 8.1 {
		t.Errorf("year/rating: %v %v", r.Year, r.Rating)
	}
	if strings.Join(r.Genres, ",") != "Sci-Fi,Drama" {
		t.Errorf("genres not de-duplicated: %v", r.Genres)
	}
	if r.OriginallyAvailableAt == nil || r.OriginallyAvailableAt.Format("2006-01-02") != "2017-10-06" {
		t.Errorf("release date: %v", r.OriginallyAvailableAt)
	}
	if r.PosterURL != "https://image.example.org/p/br.jpg" {
		t.Errorf("allowlisted poster dropped: %q", r.PosterURL)
	}
	if r.TMDBID == nil || *r.TMDBID != 335984 || r.MALID != nil {
		t.Errorf("provider ids: tmdb=%v mal=%v", r.TMDBID, r.MALID)
	}

	var sent MetadataRequest
	stub.args(t, &sent)
	if sent.ItemType != "movie" || sent.FileName != "Blade.Runner.2049.mkv" ||
		sent.Index == nil || *sent.Index != 3 || len(sent.LockedFields) != 1 || sent.CorrelationID == "" {
		t.Errorf("request not passed through: %+v", sent)
	}
}

func TestMetadataDispatcher_TextJSONAndEmptyResults(t *testing.T) {
	allowPrivateIPs(t)
	text := newToolStub(t, EnrichToolName, func(map[string]any) (*mcp.CallToolResult, error) {
		return mcp.NewToolResultText(`{"tagline":"More human than human"}`), nil
	})
	empty := newToolStub(t, EnrichToolName, func(map[string]any) (*mcp.CallToolResult, error) {
		return mcp.NewToolResultText(`{}`), nil
	})
	garbage := newToolStub(t, EnrichToolName, func(map[string]any) (*mcp.CallToolResult, error) {
		return mcp.NewToolResultText(`not json`), nil
	})
	d, _ := newMetadataFixture(t, text, empty, garbage)

	got := d.Enrich(context.Background(), MetadataRequest{ItemType: "movie", Title: "x"})
	if len(got) != 1 {
		t.Fatalf("expected only the text plugin's result, got %d", len(got))
	}
	if got[0].Tagline == nil || *got[0].Tagline != "More human than human" || got[0].Mode != MetadataModeFill {
		t.Errorf("text result: %+v", got[0])
	}
	if empty.calls.Load() != 1 || garbage.calls.Load() != 1 {
		t.Error("every enabled plugin should have been called")
	}
}

func TestMetadataDispatcher_ResultsInNameOrder(t *testing.T) {
	allowPrivateIPs(t)
	mk := func(title string) *toolStub {
		return newToolStub(t, EnrichToolName, func(map[string]any) (*mcp.CallToolResult, error) {
			return mcp.NewToolResultStructuredOnly(map[string]any{"title": title}), nil
		})
	}
	d, _ := newMetadataFixture(t, mk("first"), mk("second"), mk("third"))
	got := d.Enrich(context.Background(), MetadataRequest{ItemType: "movie", Title: "x"})
	var order []string
	for _, r := range got {
		order = append(order, *r.Title)
	}
	if strings.Join(order, ",") != "first,second,third" {
		t.Errorf("results not in plugin-name order: %v", order)
	}
}

func TestMetadataDispatcher_BreakerSkipsFailingPlugin(t *testing.T) {
	allowPrivateIPs(t)
	stub := newToolStub(t, EnrichToolName, func(map[string]any) (*mcp.CallToolResult, error) {
		return nil, errors.New("upstream down")
	})
	d, _ := newMetadataFixture(t, stub)
	ctx := context.Background()

	for i := 0; i < breakerThreshold; i++ {
		if got := d.Enrich(ctx, MetadataRequest{ItemType: "movie", Title: "x"}); len(got) != 0 {
			t.Fatalf("failing plugin produced a result")
		}
	}
	// A JSON-RPC error gets the client's one reconnect-and-retry, so each
	// item may cost two calls; what matters is that calls stop.
	tripped := stub.calls.Load()
	if tripped < breakerThreshold {
		t.Fatalf("expected at least %d calls before the breaker opened, got %d", breakerThreshold, tripped)
	}
	for i := 0; i < 2; i++ {
		d.Enrich(ctx, MetadataRequest{ItemType: "movie", Title: "x"})
	}
	if n := stub.calls.Load(); n != tripped {
		t.Fatalf("open breaker still let %d calls through", n-tripped)
	}

	// Past the cooldown the next item is the probe.
	for _, e := range d.pool.entries {
		e.breaker.now = func() time.Time { return time.Now().Add(breakerCooldown + time.Second) }
	}
	d.Enrich(ctx, MetadataRequest{ItemType: "movie", Title: "x"})
	if n := stub.calls.Load(); n == tripped {
		t.Error("expected a probe call after the cooldown")
	}
}

func TestMetadataDispatcher_TimeoutCountsAsFailure(t *testing.T) {
	allowPrivateIPs(t)
	release := make(chan struct{})
	stub := newToolStub(t, EnrichToolName, func(map[string]any) (*mcp.CallToolResult, error) {
		<-release
		return mcp.NewToolResultText(`{"title":"late"}`), nil
	})
	d, _ := newMetadataFixture(t, stub)
	// Registered last so it runs first: the stub server's Close waits for
	// the blocked handler.
	t.Cleanup(func() { close(release) })
	d.timeout = 150 * time.Millisecond

	start := time.Now()
	if got := d.Enrich(context.Background(), MetadataRequest{ItemType: "movie", Title: "x"}); len(got) != 0 {
		t.Fatal("timed-out plugin produced a result")
	}
	if el := time.Since(start); el > 5*time.Second {
		t.Errorf("Enrich waited %v for a hung plugin", el)
	}
	for _, e := range d.pool.entries {
		if fails, _ := e.breaker.state(); fails != 1 {
			t.Errorf("timeout should count as one failure, got %d", fails)
		}
	}
}

func TestMetadataDispatcher_MissingToolDoesNotTripBreaker(t *testing.T) {
	allowPrivateIPs(t)
	stub := newToolStub(t, "", nil) // advertises nothing
	d, _ := newMetadataFixture(t, stub)
	for i := 0; i < breakerThreshold+1; i++ {
		d.Enrich(context.Background(), MetadataRequest{ItemType: "movie", Title: "x"})
	}
	for _, e := range d.pool.entries {
		if fails, until := e.breaker.state(); fails != 0 || !until.IsZero() {
			t.Errorf("tool-not-advertised tripped the breaker: fails=%d until=%v", fails, until)
		}
	}
}

func TestMetadataDispatcher_NoPluginsInactive(t *testing.T) {
	d := NewMetadataDispatcher(NewRegistry(newMockRegistryDB()), discardLogger(), nil)
	defer d.Close()
	if d.Active(context.Background()) {
		t.Error("Active with no plugins")
	}
	if got := d.Enrich(context.Background(), MetadataRequest{}); got != nil {
		t.Errorf("Enrich with no plugins = %v", got)
	}
	var nilD *MetadataDispatcher
	if nilD.Active(context.Background()) || nilD.Enrich(context.Background(), MetadataRequest{}) != nil {
		t.Error("nil dispatcher should be inert")
	}
}

func TestMetadataWire_ToResultValidation(t *testing.T) {
	allowed, _ := newAllowedHostSet("https://plugin.example.com/mcp", []string{"img.example.com"})
	p := Plugin{Name: "p"}
	s := func(v string) *string { return &v }
	i := func(v int) *int { return &v }
	f := func(v float64) *float64 { return &v }

	w := metadataWire{
		Mode:                  "bogus",
		Title:                 s(strings.Repeat("x", maxTitleLen+1)),
		Summary:               s("   "),
		Year:                  i(99999),
		Rating:                f(11),
		OriginallyAvailableAt: s("06/10/2017"),
		PosterURL:             s("https://evil.example.net/p.jpg"),
		Genres:                make([]string, maxGenres+1),
	}
	w.ProviderIDs = &struct {
		TMDB    *int `json:"tmdb"`
		TVDB    *int `json:"tvdb"`
		AniList *int `json:"anilist"`
		MAL     *int `json:"mal"`
	}{TMDB: i(-5)}
	r, dropped := w.toResult(p, allowed)
	if !r.Empty() {
		t.Errorf("every field should have been rejected: %+v", r)
	}
	if r.Mode != MetadataModeFill {
		t.Errorf("unknown mode should fall back to fill, got %q", r.Mode)
	}
	want := []string{"mode", "title", "year", "rating", "genres", "originally_available_at", "poster_url", "provider_ids.tmdb"}
	if strings.Join(dropped, ",") != strings.Join(want, ",") {
		t.Errorf("dropped = %v, want %v", dropped, want)
	}

	cases := map[string]bool{
		"https://img.example.com/a.jpg":       true,
		"http://plugin.example.com/a.jpg":     true, // endpoint host is always allowed
		"https://IMG.example.com/a.jpg":       true,
		"ftp://img.example.com/a.jpg":         false,
		"https://user:pw@img.example.com/a":   false,
		"/relative.jpg":                       false,
		"https://img.example.com.evil.net/a":  false,
		"https://169.254.169.254/latest/meta": false,
	}
	for raw, ok := range cases {
		if _, got := validPosterURL(raw, allowed); got != ok {
			t.Errorf("validPosterURL(%q) = %v, want %v", raw, got, ok)
		}
	}
}
