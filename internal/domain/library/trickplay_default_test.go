package library

import (
	"context"
	"log/slog"
	"testing"
)

// captureQuerier records the params Create hands to the querier.
type captureQuerier struct {
	*mockQuerier
	got *CreateLibraryParams
}

func (c *captureQuerier) CreateLibrary(ctx context.Context, p CreateLibraryParams) (Library, error) {
	c.got = &p
	return c.mockQuerier.CreateLibrary(ctx, p)
}

func TestDefaultTrickplayEnabled(t *testing.T) {
	for typ, want := range map[string]bool{
		"movie": true, "show": true, "home_video": true, "anime": true, "cartoons": true,
		"music": false, "photo": false, "dvr": false, "audiobook": false,
		"book": false, "podcast": false, "manga": false, "": false,
	} {
		if got := DefaultTrickplayEnabled(typ); got != want {
			t.Errorf("DefaultTrickplayEnabled(%q) = %v, want %v", typ, got, want)
		}
	}
}

func TestCreate_TrickplayDefaultsByTypeUnlessSet(t *testing.T) {
	off := false
	on := true
	cases := []struct {
		name string
		typ  string
		in   *bool
		want bool
	}{
		{"video library defaults on", "movie", nil, true},
		{"anime defaults on", "anime", nil, true},
		{"music defaults off", "music", nil, false},
		{"explicit off on a video library", "show", &off, false},
		{"explicit on for photos", "photo", &on, true},
	}
	for _, tc := range cases {
		q := &captureQuerier{mockQuerier: newMockQuerier()}
		svc := NewService(q, q, &mockEnqueuer{}, slog.Default())
		_, err := svc.Create(context.Background(), CreateLibraryParams{
			Name: "L", Type: tc.typ, Paths: []string{"/m"}, TrickplayEnabled: tc.in,
		})
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if q.got == nil || q.got.TrickplayEnabled == nil {
			t.Fatalf("%s: querier got no resolved TrickplayEnabled", tc.name)
		}
		if *q.got.TrickplayEnabled != tc.want {
			t.Errorf("%s: TrickplayEnabled = %v, want %v", tc.name, *q.got.TrickplayEnabled, tc.want)
		}
	}
}
