package omdb

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func fakeOMDb(t *testing.T, bodies map[string]string) *Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("apikey") != "key" {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"Response":"False","Error":"Invalid API key!"}`))
			return
		}
		body, ok := bodies[r.URL.Query().Get("i")]
		if !ok {
			body = `{"Response":"False","Error":"Incorrect IMDb ID."}`
		}
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return New("key").WithBaseURL(srv.URL+"/", srv.Client())
}

func TestGet(t *testing.T) {
	c := fakeOMDb(t, map[string]string{
		"tt0111161": `{"Title":"The Shawshank Redemption","imdbRating":"9.3","imdbVotes":"3,012,345","Metascore":"82",
			"Ratings":[{"Source":"Internet Movie Database","Value":"9.3/10"},{"Source":"Rotten Tomatoes","Value":"89%"},
			{"Source":"Metacritic","Value":"82/100"}],"Response":"True"}`,
		"tt0903747": `{"Title":"Breaking Bad","imdbRating":"9.5","imdbVotes":"2,200,000","Metascore":"N/A",
			"Ratings":[{"Source":"Internet Movie Database","Value":"9.5/10"}],"Response":"True"}`,
		"tt9999999": `{"Title":"Obscure","imdbRating":"N/A","imdbVotes":"N/A","Metascore":"N/A","Ratings":[],"Response":"True"}`,
		"tt0000001": `{"Response":"False","Error":"Request limit reached!"}`,
	})
	r, err := c.Get(context.Background(), "tt0111161")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if *r.IMDb != 9.3 || *r.IMDbVotes != 3012345 || *r.RottenTomatoes != 89 || *r.Metacritic != 82 {
		t.Errorf("Shawshank = %v %v %v %v", *r.IMDb, *r.IMDbVotes, *r.RottenTomatoes, *r.Metacritic)
	}
	r, err = c.Get(context.Background(), "tt0903747")
	if err != nil || *r.IMDb != 9.5 || r.RottenTomatoes != nil || r.Metacritic != nil {
		t.Errorf("Breaking Bad = %+v %v (a show: IMDb only)", r, err)
	}
	r, err = c.Get(context.Background(), "tt9999999")
	if err != nil || r != (Ratings{}) {
		t.Errorf("all N/A = %+v %v", r, err)
	}
	if _, err := c.Get(context.Background(), "tt7654321"); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown ID: %v", err)
	}
	if _, err := c.Get(context.Background(), "tt0000001"); !errors.Is(err, ErrLimitReached) {
		t.Errorf("limit: %v", err)
	}
}

func TestGet_BadKey(t *testing.T) {
	c := fakeOMDb(t, nil)
	c.apiKey = "wrong"
	if _, err := c.Get(context.Background(), "tt0111161"); !errors.Is(err, ErrInvalidKey) {
		t.Errorf("bad key: %v", err)
	}
}

func TestGet_TransportErrorHidesKey(t *testing.T) {
	c := New("secret-key").WithBaseURL("http://127.0.0.1:1/", http.DefaultClient)
	_, err := c.Get(context.Background(), "tt0111161")
	if err == nil || strings.Contains(err.Error(), "secret-key") {
		t.Errorf("error = %v (must not include the key)", err)
	}
}

func TestPercent(t *testing.T) {
	for in, want := range map[string]int16{"0": 0, "100": 100, " 42 ": 42} {
		if got := percent(in); got == nil || *got != want {
			t.Errorf("percent(%q) = %v", in, got)
		}
	}
	for _, bad := range []string{"N/A", "", "101", "-1", "7.5"} {
		if percent(bad) != nil {
			t.Errorf("percent(%q) accepted", bad)
		}
	}
}

// OMDb's error messages, as it sends them (checked against the live API for
// the key errors).
func TestClassify(t *testing.T) {
	cases := map[string]error{
		"No API key provided.":   ErrInvalidKey,
		"Invalid API key!":       ErrInvalidKey,
		"Movie not found!":       ErrNotFound,
		"Incorrect IMDb ID.":     ErrNotFound,
		"Request limit reached!": ErrLimitReached,
	}
	for msg, want := range cases {
		if got := classify(msg, http.StatusOK); !errors.Is(got, want) {
			t.Errorf("classify(%q) = %v, want %v", msg, got, want)
		}
	}
	if got := classify("Something else", http.StatusOK); got == nil || errors.Is(got, ErrNotFound) {
		t.Errorf("unknown message = %v", got)
	}
}
