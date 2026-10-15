package fanarttv

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/metadata"
)

// fakeFanart serves fanart.tv v3 replies by path (without the /v3/ prefix);
// unknown paths 404 the way fanart.tv does. hits counts requests per path.
func fakeFanart(t *testing.T, bodies map[string]string) (*Client, map[string]*int32) {
	t.Helper()
	hits := map[string]*int32{}
	for p := range bodies {
		hits[p] = new(int32)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("api_key") != "key" {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"status":"error","error message":"Invalid API key"}`))
			return
		}
		path := strings.TrimPrefix(r.URL.Path, "/v3/")
		body, ok := bodies[path]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"status":"error","error message":"Not found"}`))
			return
		}
		atomic.AddInt32(hits[path], 1)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return New("key").WithBaseURL(srv.URL+"/v3/", srv.Client()), hits
}

const movieBody = `{
  "name": "Alien", "tmdb_id": "348", "imdb_id": "tt0078748",
  "hdmovielogo": [
    {"id": "1", "url": "https://assets.fanart.tv/fanart/movies/348/hdmovielogo/alien-de.png", "lang": "de", "likes": "9"},
    {"id": "2", "url": "https://assets.fanart.tv/fanart/movies/348/hdmovielogo/alien-en-low.png", "lang": "en", "likes": "1"},
    {"id": "3", "url": "https://assets.fanart.tv/fanart/movies/348/hdmovielogo/alien-en.png", "lang": "en", "likes": "4"}
  ],
  "moviebanner": [
    {"id": "4", "url": "https://assets.fanart.tv/fanart/movies/348/moviebanner/alien-00.jpg", "lang": "00", "likes": "7"},
    {"id": "5", "url": "https://assets.fanart.tv/fanart/movies/348/moviebanner/alien-fr.jpg", "lang": "fr", "likes": "8"}
  ],
  "moviedisc": [
    {"id": "6", "url": "https://assets.fanart.tv/fanart/movies/348/moviedisc/alien.png", "lang": "en", "likes": "2", "disc": "1", "disc_type": "bluray"}
  ]
}`

func TestMovieArtPicksByLanguageThenLikes(t *testing.T) {
	c, _ := fakeFanart(t, map[string]string{"movies/348": movieBody})

	art, err := c.MovieArt(context.Background(), 348, "", "en")
	if err != nil {
		t.Fatalf("MovieArt: %v", err)
	}
	if !strings.HasSuffix(art.LogoURL, "alien-en.png") {
		t.Errorf("logo = %q, want the most liked English logo", art.LogoURL)
	}
	// No English banner: language-neutral beats a more liked French one.
	if !strings.HasSuffix(art.BannerURL, "alien-00.jpg") {
		t.Errorf("banner = %q, want the language-neutral banner", art.BannerURL)
	}
	if !strings.HasSuffix(art.DiscURL, "alien.png") {
		t.Errorf("disc = %q", art.DiscURL)
	}

	// The library's own language comes first, even with fewer likes.
	art, err = c.MovieArt(context.Background(), 348, "", "de-DE")
	if err != nil || !strings.HasSuffix(art.LogoURL, "alien-de.png") {
		t.Errorf("German library logo = %q, %v", art.LogoURL, err)
	}
	art, _ = c.MovieArt(context.Background(), 348, "", "fr")
	if !strings.HasSuffix(art.BannerURL, "alien-fr.jpg") {
		t.Errorf("French library banner = %q", art.BannerURL)
	}
}

func TestMovieArtFallsBackToIMDbAndSDLogo(t *testing.T) {
	c, _ := fakeFanart(t, map[string]string{
		"movies/tt0000001": `{"movielogo":[{"id":"1","url":"https://assets.fanart.tv/sd.png","lang":"en","likes":"0"}]}`,
	})
	art, err := c.MovieArt(context.Background(), 0, "tt0000001", "en")
	if err != nil || art.LogoURL != "https://assets.fanart.tv/sd.png" {
		t.Errorf("IMDb lookup / SD logo = %+v, %v", art, err)
	}
	// No usable id at all: no request, no art.
	art, err = c.MovieArt(context.Background(), 0, "", "en")
	if err != nil || art != (metadata.ExtraArt{}) {
		t.Errorf("no id = %+v, %v", art, err)
	}
}

func TestNotFoundIsEmptyAndCached(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(http.StatusNotFound)
	}))
	t.Cleanup(srv.Close)
	c := New("key").WithBaseURL(srv.URL+"/v3/", srv.Client())

	for range 3 {
		art, err := c.ShowArt(context.Background(), 81189, "en")
		if err != nil || art != (metadata.ExtraArt{}) {
			t.Fatalf("404 = %+v, %v; want empty, nil", art, err)
		}
	}
	if n := atomic.LoadInt32(&calls); n != 1 {
		t.Errorf("fanart.tv asked %d times for a show it doesn't have; want 1 (cached)", n)
	}

	// Past the TTL it asks again.
	c.now = func() time.Time { return time.Now().Add(cacheTTL + time.Minute) }
	_, _ = c.ShowArt(context.Background(), 81189, "en")
	if n := atomic.LoadInt32(&calls); n != 2 {
		t.Errorf("after the TTL: %d requests, want 2", n)
	}
}

func TestShowArtistAlbumArt(t *testing.T) {
	artist := uuid.MustParse("b10bbbfc-cf9e-42e0-be17-e2c3e1d2600d")
	rg := uuid.MustParse("72d15666-99a7-321e-b1f3-a3f8c09dff9f")
	c, hits := fakeFanart(t, map[string]string{
		"tv/81189": `{"hdtvlogo":[{"id":"1","url":"https://assets.fanart.tv/bb-logo.png","lang":"en","likes":"3"}],
			"tvbanner":[{"id":"2","url":"https://assets.fanart.tv/bb-banner.jpg","lang":"en","likes":"1"}]}`,
		"music/" + artist.String(): `{"hdmusiclogo":[{"id":"3","url":"https://assets.fanart.tv/beatles-logo.png","likes":"5"}],
			"musicbanner":[{"id":"4","url":"https://assets.fanart.tv/beatles-banner.jpg","likes":"2"}]}`,
		"music/albums/" + rg.String(): `{"albums":{"` + rg.String() + `":{"cdart":[
			{"id":"5","url":"https://assets.fanart.tv/abbey-cd1.png","likes":"1","disc":"1","size":"1000"},
			{"id":"6","url":"https://assets.fanart.tv/abbey-cd2.png","likes":"6","disc":"1","size":"1000"}]}}}`,
	})
	ctx := context.Background()

	show, err := c.ShowArt(ctx, 81189, "en")
	if err != nil || show.LogoURL != "https://assets.fanart.tv/bb-logo.png" || show.BannerURL != "https://assets.fanart.tv/bb-banner.jpg" || show.DiscURL != "" {
		t.Errorf("show = %+v, %v", show, err)
	}
	a, err := c.ArtistArt(ctx, artist, "en")
	if err != nil || a.LogoURL != "https://assets.fanart.tv/beatles-logo.png" || a.BannerURL != "https://assets.fanart.tv/beatles-banner.jpg" {
		t.Errorf("artist = %+v, %v", a, err)
	}
	al, err := c.AlbumArt(ctx, rg, "en")
	if err != nil || al.DiscURL != "https://assets.fanart.tv/abbey-cd2.png" {
		t.Errorf("album = %+v, %v; want the most liked cdart", al, err)
	}
	// Repeat lookups are served from the cache.
	_, _ = c.ShowArt(ctx, 81189, "en")
	if n := atomic.LoadInt32(hits["tv/81189"]); n != 1 {
		t.Errorf("tv/81189 fetched %d times, want 1", n)
	}
	if a, _ := c.ArtistArt(ctx, uuid.Nil, "en"); a != (metadata.ExtraArt{}) {
		t.Errorf("nil MBID = %+v", a)
	}
}

func TestInvalidKey(t *testing.T) {
	c, _ := fakeFanart(t, map[string]string{"tv/1": `{}`})
	c.apiKey = "wrong"
	if _, err := c.ShowArt(context.Background(), 1, "en"); !errors.Is(err, ErrInvalidKey) {
		t.Errorf("err = %v, want ErrInvalidKey", err)
	}
}

func TestPickSkipsNonHTTPURLs(t *testing.T) {
	got := pick([]image{
		{URL: "javascript:alert(1)", Lang: "en", Likes: "99"},
		{URL: "https://assets.fanart.tv/ok.png", Lang: "en", Likes: "1"},
	}, "en")
	if got != "https://assets.fanart.tv/ok.png" {
		t.Errorf("pick = %q", got)
	}
	if pick(nil, "en") != "" {
		t.Error("empty list should pick nothing")
	}
}
