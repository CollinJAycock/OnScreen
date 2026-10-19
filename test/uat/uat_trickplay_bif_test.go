// uat_trickplay_bif_test.go — the Roku BIF routes, /trickplay/{id}/index.bif
// and its /api/v1/items/{id}/trickplay/index.bif alias. Through the real
// router: the static index.bif segment must win over {file}, and the gates
// must be the sprites' gates — RequiredAllowQueryToken (the Roku Video node
// sends a purpose=asset ?token=), the library ACL and the rating ceiling.
package uat

import (
	"bytes"
	"image"
	"image/jpeg"
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
	"github.com/onscreen/onscreen/internal/domain/media"
	"github.com/onscreen/onscreen/internal/trickplay"
)

func newTrickplayBIFFixture(t *testing.T) *trickplayAliasFixture {
	t.Helper()
	dir := t.TempDir()
	spec := trickplay.Spec{IntervalSec: 10, ThumbWidth: 64, ThumbHeight: 36, GridCols: 2, GridRows: 2}
	vtt, err := spec.WriteVTT(30, []string{"sprite_000.jpg"})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "index.vtt"), []byte(vtt), 0o644); err != nil {
		t.Fatal(err)
	}
	var sprite bytes.Buffer
	if err := jpeg.Encode(&sprite, image.NewGray(image.Rect(0, 0, 128, 72)), nil); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "sprite_000.jpg"), sprite.Bytes(), 0o644); err != nil {
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

func TestTrickplayBIF_Gates(t *testing.T) {
	f := newTrickplayBIFFixture(t)
	c := cappedClaims()
	bearer := f.accessToken(t, c)
	asset := f.assetToken(t, c)

	paths := func(id uuid.UUID) []string {
		return []string{
			"/trickplay/" + id.String() + "/index.bif",
			"/api/v1/items/" + id.String() + "/trickplay/index.bif",
		}
	}
	for _, p := range []struct {
		name          string
		id            uuid.UUID
		bearer, query string
		want          int
	}{
		{"anonymous", f.okItem, "", "", http.StatusUnauthorized},
		{"general token in URL", f.okItem, "", bearer, http.StatusUnauthorized},
		{"no library grant", f.otherLibItem, "", asset, http.StatusNotFound},
		{"over the rating ceiling", f.rItem, "", asset, http.StatusNotFound},
		{"unrated under a ceiling", f.unratedID, bearer, "", http.StatusNotFound},
		{"granted item (asset token)", f.okItem, "", asset, http.StatusOK},
		{"granted item (bearer)", f.okItem, bearer, "", http.StatusOK},
	} {
		for _, path := range paths(p.id) {
			if p.query != "" {
				path += "?token=" + url.QueryEscape(p.query)
			}
			resp := f.ts.do("GET", path, p.bearer, nil)
			if resp.StatusCode != p.want {
				t.Errorf("%s: GET %s = %d, want %d", p.name, strings.SplitN(path, "?", 2)[0], resp.StatusCode, p.want)
			}
			if p.want == http.StatusOK {
				body := readAllBody(t, resp)
				if !strings.HasPrefix(body, "\x89BIF\r\n\x1a\n") {
					t.Errorf("%s: body is not a BIF: %q", p.name, body[:min(len(body), 16)])
				}
				if cc := resp.Header.Get("Cache-Control"); !strings.HasPrefix(cc, "private") {
					t.Errorf("%s: Cache-Control = %q, want private", p.name, cc)
				}
				continue
			}
			resp.Body.Close()
		}
	}
}
