// uat_trickplay_alias_test.go — the /api/v1/items/{id}/trickplay/{file}
// alias the Tizen and webOS clients fetch seek-bar thumbnails from. It must
// serve the same files as the canonical /trickplay/{id}/{file} route under
// exactly the same gates: RequiredAllowQueryToken (Bearer for the VTT, a
// purpose=asset ?token= for sprites), the item's library ACL and the
// caller's content-rating ceiling.
package uat

import (
	"bufio"
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/api"
	v1 "github.com/onscreen/onscreen/internal/api/v1"
	"github.com/onscreen/onscreen/internal/auth"
	"github.com/onscreen/onscreen/internal/domain/media"
	"github.com/onscreen/onscreen/internal/trickplay"
)

// aliasTrickplaySvc serves every item's sprites out of one directory.
type aliasTrickplaySvc struct{ dir string }

func (s aliasTrickplaySvc) Status(context.Context, uuid.UUID) (trickplay.Spec, string, int, bool, error) {
	return trickplay.Spec{}, "done", 1, true, nil
}
func (s aliasTrickplaySvc) Generate(context.Context, uuid.UUID) error { return nil }
func (s aliasTrickplaySvc) ItemDir(uuid.UUID) string                  { return s.dir }

// aliasTrickplayMedia resolves the fixture items.
type aliasTrickplayMedia struct{ items map[uuid.UUID]*media.Item }

func (m aliasTrickplayMedia) GetItem(_ context.Context, id uuid.UUID) (*media.Item, error) {
	if it, ok := m.items[id]; ok {
		return it, nil
	}
	return nil, media.ErrNotFound
}

// aliasLibAccess grants non-admins exactly one library.
type aliasLibAccess struct{ granted uuid.UUID }

func (a aliasLibAccess) CanAccessLibrary(_ context.Context, _, libraryID uuid.UUID, isAdmin bool) (bool, error) {
	return isAdmin || libraryID == a.granted, nil
}
func (a aliasLibAccess) AllowedLibraryIDs(_ context.Context, _ uuid.UUID, isAdmin bool) (map[uuid.UUID]struct{}, error) {
	if isAdmin {
		return nil, nil
	}
	return map[uuid.UUID]struct{}{a.granted: {}}, nil
}

type trickplayAliasFixture struct {
	ts                                     *testServer
	okItem, otherLibItem, rItem, unratedID uuid.UUID
}

func newTrickplayAliasFixture(t *testing.T) *trickplayAliasFixture {
	t.Helper()
	dir := t.TempDir()
	spec := trickplay.Spec{IntervalSec: 10, ThumbWidth: 320, ThumbHeight: 180, GridCols: 10, GridRows: 10}
	vtt, err := spec.WriteVTT(30, []string{"sprite_000.jpg"})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "index.vtt"), []byte(vtt), 0o644); err != nil {
		t.Fatal(err)
	}
	// JPEG SOI/EOI markers — enough for a byte-for-byte comparison.
	if err := os.WriteFile(filepath.Join(dir, "sprite_000.jpg"), []byte{0xFF, 0xD8, 0xFF, 0xD9}, 0o644); err != nil {
		t.Fatal(err)
	}

	granted, other := uuid.New(), uuid.New()
	pg, r := "PG", "R"
	f := &trickplayAliasFixture{okItem: uuid.New(), otherLibItem: uuid.New(), rItem: uuid.New(), unratedID: uuid.New()}
	items := map[uuid.UUID]*media.Item{
		f.okItem:       {ID: f.okItem, LibraryID: granted, Type: "movie", ContentRating: &pg},
		f.otherLibItem: {ID: f.otherLibItem, LibraryID: other, Type: "movie", ContentRating: &pg},
		f.rItem:        {ID: f.rItem, LibraryID: granted, Type: "movie", ContentRating: &r},
		f.unratedID:    {ID: f.unratedID, LibraryID: granted, Type: "movie"},
	}
	f.ts = newExtrasServer(t, func(h *api.Handlers) {
		h.Trickplay = v1.NewTrickplayHandler(aliasTrickplaySvc{dir: dir}, aliasTrickplayMedia{items: items}, slog.Default()).
			WithLibraryAccess(aliasLibAccess{granted: granted})
	})
	return f
}

// cappedClaims is a PG-13 managed profile with a grant on one library.
func cappedClaims() auth.Claims {
	return auth.Claims{UserID: uuid.New(), Username: "kid", MaxContentRating: "PG-13"}
}

func (f *trickplayAliasFixture) accessToken(t *testing.T, c auth.Claims) string {
	t.Helper()
	tok, err := f.ts.tm.IssueAccessToken(c)
	if err != nil {
		t.Fatal(err)
	}
	return tok
}

func (f *trickplayAliasFixture) assetToken(t *testing.T, c auth.Claims) string {
	t.Helper()
	tok, err := f.ts.tm.IssueAssetToken(c)
	if err != nil {
		t.Fatal(err)
	}
	return tok
}

func readAllBody(t *testing.T, resp *http.Response) string {
	t.Helper()
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// firstCueImage returns the image reference of the VTT's first cue, with the
// #xywh fragment stripped (what the Tizen/webOS parser hands to
// trickplaySpriteUrl).
func firstCueImage(t *testing.T, vtt string) string {
	t.Helper()
	sc := bufio.NewScanner(strings.NewReader(vtt))
	for sc.Scan() {
		if strings.Contains(sc.Text(), "-->") && sc.Scan() {
			ref, _, _ := strings.Cut(sc.Text(), "#")
			return ref
		}
	}
	t.Fatalf("no cue in VTT: %q", vtt)
	return ""
}

// TestTrickplayAlias_TizenWebOSFlow replays the TV clients' fetches: the VTT
// with a Bearer header, then the sprite its first cue names — resolved
// relative to the VTT URL — with the asset token in ?token= and no header.
func TestTrickplayAlias_TizenWebOSFlow(t *testing.T) {
	f := newTrickplayAliasFixture(t)
	c := cappedClaims()
	vttPath := "/api/v1/items/" + f.okItem.String() + "/trickplay/index.vtt"

	resp := f.ts.do("GET", vttPath, f.accessToken(t, c), nil)
	assertStatus(t, resp, http.StatusOK)
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/vtt") {
		t.Errorf("VTT content-type = %q", ct)
	}
	if cc := resp.Header.Get("Cache-Control"); !strings.HasPrefix(cc, "private") {
		t.Errorf("Cache-Control = %q, want private (per-request ACL)", cc)
	}
	aliasVTT := readAllBody(t, resp)

	// Byte-identical to the canonical route.
	canon := f.ts.do("GET", "/trickplay/"+f.okItem.String()+"/index.vtt", f.accessToken(t, c), nil)
	assertStatus(t, canon, http.StatusOK)
	if got := readAllBody(t, canon); got != aliasVTT {
		t.Fatalf("alias VTT differs from canonical:\n%s\n---\n%s", aliasVTT, got)
	}

	// The cue's sprite name is relative: it must land on the alias's sibling.
	ref := firstCueImage(t, aliasVTT)
	base, err := url.Parse(f.ts.url(vttPath))
	if err != nil {
		t.Fatal(err)
	}
	spriteURL := base.ResolveReference(&url.URL{Path: ref})
	wantPath := "/api/v1/items/" + f.okItem.String() + "/trickplay/sprite_000.jpg"
	if spriteURL.Path != wantPath {
		t.Fatalf("sprite resolves to %s, want %s", spriteURL.Path, wantPath)
	}
	q := spriteURL.Query()
	q.Set("token", f.assetToken(t, c))
	spriteURL.RawQuery = q.Encode()
	sresp, err := f.ts.client.Get(spriteURL.String())
	if err != nil {
		t.Fatal(err)
	}
	assertStatus(t, sresp, http.StatusOK)
	if ct := sresp.Header.Get("Content-Type"); ct != "image/jpeg" {
		t.Errorf("sprite content-type = %q", ct)
	}
	if body := readAllBody(t, sresp); body != "\xFF\xD8\xFF\xD9" {
		t.Errorf("sprite body = %q", body)
	}
}

// TestTrickplayAlias_Gates: the alias refuses exactly what the canonical
// route refuses — no credentials, a general access token in the URL, an item
// in a library without a grant, and items above (or without) a rating when
// the caller has a ceiling — with the same status on both paths.
func TestTrickplayAlias_Gates(t *testing.T) {
	f := newTrickplayAliasFixture(t)
	c := cappedClaims()
	bearer := f.accessToken(t, c)
	asset := f.assetToken(t, c)

	paths := func(id uuid.UUID, file string) []string {
		return []string{
			"/api/v1/items/" + id.String() + "/trickplay/" + file,
			"/trickplay/" + id.String() + "/" + file,
		}
	}
	type probe struct {
		name   string
		id     uuid.UUID
		file   string
		bearer string
		query  string
		want   int
	}
	for _, p := range []probe{
		{"anonymous", f.okItem, "index.vtt", "", "", http.StatusUnauthorized},
		{"general token in URL", f.okItem, "sprite_000.jpg", "", bearer, http.StatusUnauthorized},
		{"no library grant (bearer)", f.otherLibItem, "index.vtt", bearer, "", http.StatusNotFound},
		{"no library grant (asset token)", f.otherLibItem, "sprite_000.jpg", "", asset, http.StatusNotFound},
		{"over the rating ceiling", f.rItem, "index.vtt", bearer, "", http.StatusNotFound},
		{"unrated under a ceiling", f.unratedID, "sprite_000.jpg", "", asset, http.StatusNotFound},
		{"filename outside the whitelist", f.okItem, "sprite_0.jpg", bearer, "", http.StatusNotFound},
		{"granted item (asset token)", f.okItem, "sprite_000.jpg", "", asset, http.StatusOK},
	} {
		for _, path := range paths(p.id, p.file) {
			if p.query != "" {
				path += "?token=" + url.QueryEscape(p.query)
			}
			resp := f.ts.do("GET", path, p.bearer, nil)
			if resp.StatusCode != p.want {
				t.Errorf("%s: GET %s = %d, want %d", p.name, strings.SplitN(path, "?", 2)[0], resp.StatusCode, p.want)
			}
			resp.Body.Close()
		}
	}

	// An uncapped admin sees the R item through the alias, as through the
	// canonical route: the gate is the ceiling, not the rating itself.
	admin := f.accessToken(t, auth.Claims{UserID: uuid.New(), Username: "admin", IsAdmin: true})
	resp := f.ts.do("GET", "/api/v1/items/"+f.rItem.String()+"/trickplay/index.vtt", admin, nil)
	assertStatus(t, resp, http.StatusOK)
	resp.Body.Close()
}
