package v1

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/api/middleware"
	"github.com/onscreen/onscreen/internal/auth"
	"github.com/onscreen/onscreen/internal/db/gen"
	"github.com/onscreen/onscreen/internal/domain/library"
	"github.com/onscreen/onscreen/internal/podcast"
)

// fakePodcasts records what the handler asks of the service and answers
// with err when set.
type fakePodcasts struct {
	err        error
	subscribed podcast.SubscribeParams
	feed       gen.PodcastFeed
	settings   [2]any
	unsub      *bool
}

func (f *fakePodcasts) Subscribe(_ context.Context, p podcast.SubscribeParams) (gen.PodcastFeed, error) {
	f.subscribed = p
	if f.err != nil {
		return gen.PodcastFeed{}, f.err
	}
	return gen.PodcastFeed{ID: uuid.New(), LibraryID: p.LibraryID, FeedUrl: p.FeedURL, Title: "Show",
		AutoDownload: p.AutoDownload, KeepLatest: int32(p.KeepLatest)}, nil
}
func (f *fakePodcasts) Feeds(context.Context, *uuid.UUID) ([]gen.ListPodcastFeedsRow, error) {
	return []gen.ListPodcastFeedsRow{{ID: f.feed.ID, Title: f.feed.Title, EpisodeCount: 7, DownloadedCount: 2}}, f.err
}
func (f *fakePodcasts) Feed(_ context.Context, id uuid.UUID) (gen.PodcastFeed, error) {
	if f.err != nil {
		return gen.PodcastFeed{}, f.err
	}
	if id != f.feed.ID {
		return gen.PodcastFeed{}, podcast.ErrFeedNotFound
	}
	return f.feed, nil
}
func (f *fakePodcasts) Episodes(context.Context, uuid.UUID, int32, int32) ([]gen.PodcastFeedEpisode, int64, error) {
	return []gen.PodcastFeedEpisode{{ID: uuid.New(), Title: "Ep", Status: "available"}}, 1, f.err
}
func (f *fakePodcasts) UpdateSettings(_ context.Context, id uuid.UUID, auto bool, keep int) (gen.PodcastFeed, error) {
	f.settings = [2]any{auto, keep}
	out := f.feed
	out.AutoDownload, out.KeepLatest = auto, int32(keep)
	return out, f.err
}
func (f *fakePodcasts) Refresh(context.Context, uuid.UUID) (podcast.RefreshResult, error) {
	return podcast.RefreshResult{NewEpisodes: 2}, f.err
}
func (f *fakePodcasts) Unsubscribe(_ context.Context, _ uuid.UUID, deleteFiles bool) error {
	f.unsub = &deleteFiles
	return f.err
}
func (f *fakePodcasts) Download(context.Context, uuid.UUID) (gen.PodcastFeedEpisode, error) {
	return gen.PodcastFeedEpisode{ID: uuid.New(), Status: "downloading"}, f.err
}
func (f *fakePodcasts) DeleteDownload(context.Context, uuid.UUID) error { return f.err }

func podcastRequest(method, target, body string, id string) *http.Request {
	r := httptest.NewRequest(method, target, strings.NewReader(body))
	r = r.WithContext(middleware.WithClaims(r.Context(), &auth.Claims{UserID: uuid.New(), IsAdmin: true}))
	if id != "" {
		rctx := chi.NewRouteContext()
		rctx.URLParams.Add("id", id)
		r = r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, rctx))
	}
	return r
}

func newPodcastHandler(f *fakePodcasts) *PodcastHandler {
	return NewPodcastHandler(f, slog.New(slog.NewTextHandler(io.Discard, nil)))
}

func TestPodcastCreate_Defaults(t *testing.T) {
	f := &fakePodcasts{}
	h := newPodcastHandler(f)
	lib := uuid.New()
	w := httptest.NewRecorder()
	h.Create(w, podcastRequest(http.MethodPost, "/", `{"library_id":"`+lib.String()+`","feed_url":"https://a/feed.xml"}`, ""))
	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, body %s", w.Code, w.Body)
	}
	p := f.subscribed
	if p.LibraryID != lib || p.FeedURL != "https://a/feed.xml" || !p.AutoDownload || p.KeepLatest != 0 || p.DownloadLatest != 1 || p.CreatedBy == nil {
		t.Errorf("subscribe params = %+v; want auto-download on, keep all, download 1, created_by set", p)
	}
}

func TestPodcastCreate_Overrides(t *testing.T) {
	f := &fakePodcasts{}
	w := httptest.NewRecorder()
	newPodcastHandler(f).Create(w, podcastRequest(http.MethodPost, "/",
		`{"library_id":"`+uuid.NewString()+`","feed_url":"https://a/f","auto_download":false,"keep_latest":5,"download_latest":0}`, ""))
	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d", w.Code)
	}
	if p := f.subscribed; p.AutoDownload || p.KeepLatest != 5 || p.DownloadLatest != 0 {
		t.Errorf("subscribe params = %+v", p)
	}
}

func TestPodcastCreate_Validation(t *testing.T) {
	for name, c := range map[string]struct {
		body string
		code int
	}{
		"bad json":    {`{`, http.StatusBadRequest},
		"no library":  {`{"feed_url":"https://a/f"}`, http.StatusUnprocessableEntity},
		"no feed url": {`{"library_id":"` + uuid.NewString() + `"}`, http.StatusUnprocessableEntity},
	} {
		t.Run(name, func(t *testing.T) {
			w := httptest.NewRecorder()
			newPodcastHandler(&fakePodcasts{}).Create(w, podcastRequest(http.MethodPost, "/", c.body, ""))
			if w.Code != c.code {
				t.Errorf("status = %d, want %d", w.Code, c.code)
			}
		})
	}
}

// Service errors map to the status codes the web app shows.
func TestPodcastCreate_ErrorMapping(t *testing.T) {
	for _, c := range []struct {
		err  error
		code int
		key  string
	}{
		{podcast.ErrAlreadySubscribed, http.StatusConflict, "ALREADY_SUBSCRIBED"},
		{podcast.ErrNotPodcastLibrary, http.StatusUnprocessableEntity, ""},
		{podcast.ErrNoAudioEpisodes, http.StatusUnprocessableEntity, ""},
		{&podcast.FetchError{Msg: "the feed URL answered HTTP 404"}, http.StatusBadGateway, "FEED_UNAVAILABLE"},
		{library.ErrNotFound, http.StatusNotFound, ""},
		{io.ErrUnexpectedEOF, http.StatusInternalServerError, ""},
	} {
		w := httptest.NewRecorder()
		newPodcastHandler(&fakePodcasts{err: c.err}).Create(w, podcastRequest(http.MethodPost, "/",
			`{"library_id":"`+uuid.NewString()+`","feed_url":"https://a/f"}`, ""))
		if w.Code != c.code {
			t.Errorf("%v → %d, want %d", c.err, w.Code, c.code)
		}
		if c.key != "" && !strings.Contains(w.Body.String(), c.key) {
			t.Errorf("%v body %s lacks %s", c.err, w.Body, c.key)
		}
	}
}

// PATCH changes only what it's given.
func TestPodcastUpdate_Partial(t *testing.T) {
	f := &fakePodcasts{feed: gen.PodcastFeed{ID: uuid.New(), AutoDownload: true, KeepLatest: 3}}
	w := httptest.NewRecorder()
	newPodcastHandler(f).Update(w, podcastRequest(http.MethodPatch, "/", `{"keep_latest":10}`, f.feed.ID.String()))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", w.Code, w.Body)
	}
	if f.settings != [2]any{true, 10} {
		t.Errorf("settings = %v, want auto-download kept on, keep 10", f.settings)
	}
}

func TestPodcastDelete_DeleteFilesFlag(t *testing.T) {
	for query, want := range map[string]bool{"": false, "?delete_files=true": true} {
		f := &fakePodcasts{feed: gen.PodcastFeed{ID: uuid.New()}}
		w := httptest.NewRecorder()
		newPodcastHandler(f).Delete(w, podcastRequest(http.MethodDelete, "/"+query, "", f.feed.ID.String()))
		if w.Code != http.StatusNoContent || f.unsub == nil || *f.unsub != want {
			t.Errorf("%q: status %d, delete_files %v; want 204, %v", query, w.Code, f.unsub, want)
		}
	}
}

func TestPodcastGet_NotFound(t *testing.T) {
	w := httptest.NewRecorder()
	newPodcastHandler(&fakePodcasts{}).Get(w, podcastRequest(http.MethodGet, "/", "", uuid.NewString()))
	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", w.Code)
	}
	w = httptest.NewRecorder()
	newPodcastHandler(&fakePodcasts{}).Get(w, podcastRequest(http.MethodGet, "/", "", "nope"))
	if w.Code != http.StatusBadRequest {
		t.Errorf("bad id status = %d, want 400", w.Code)
	}
}

func TestPodcastList_Counts(t *testing.T) {
	f := &fakePodcasts{feed: gen.PodcastFeed{ID: uuid.New(), Title: "Show"}}
	w := httptest.NewRecorder()
	newPodcastHandler(f).List(w, podcastRequest(http.MethodGet, "/?library_id="+uuid.NewString(), "", ""))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	var body struct {
		Data []podcastFeedDTO `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Data) != 1 || body.Data[0].EpisodeCount == nil || *body.Data[0].EpisodeCount != 7 || *body.Data[0].DownloadedCount != 2 {
		t.Errorf("list = %+v", body.Data)
	}
	w = httptest.NewRecorder()
	newPodcastHandler(f).List(w, podcastRequest(http.MethodGet, "/?library_id=x", "", ""))
	if w.Code != http.StatusBadRequest {
		t.Errorf("bad library_id status = %d, want 400", w.Code)
	}
}

func TestPodcastEpisodeDownload(t *testing.T) {
	w := httptest.NewRecorder()
	newPodcastHandler(&fakePodcasts{}).DownloadEpisode(w, podcastRequest(http.MethodPost, "/", "", uuid.NewString()))
	if w.Code != http.StatusAccepted || !strings.Contains(w.Body.String(), `"downloading"`) {
		t.Errorf("status = %d, body %s; want 202 downloading", w.Code, w.Body)
	}
	w = httptest.NewRecorder()
	newPodcastHandler(&fakePodcasts{err: podcast.ErrNotDownloadable}).DownloadEpisode(w, podcastRequest(http.MethodPost, "/", "", uuid.NewString()))
	if w.Code != http.StatusConflict {
		t.Errorf("already downloading → %d, want 409", w.Code)
	}
	w = httptest.NewRecorder()
	newPodcastHandler(&fakePodcasts{err: podcast.ErrNotDownloaded}).DeleteEpisodeDownload(w, podcastRequest(http.MethodDelete, "/", "", uuid.NewString()))
	if w.Code != http.StatusConflict {
		t.Errorf("delete of a not-downloaded episode → %d, want 409", w.Code)
	}
}
