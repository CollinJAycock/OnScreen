package media

import (
	"testing"

	"github.com/google/uuid"
)

func stackFile(item uuid.UUID, path string, part int, edition string, durMS int64) File {
	f := File{ID: uuid.New(), MediaItemID: item, FilePath: path}
	if part > 0 {
		p := part
		f.PartIndex = &p
	}
	if edition != "" {
		e := edition
		f.Edition = &e
	}
	if durMS > 0 {
		d := durMS
		f.DurationMS = &d
	}
	return f
}

func TestStack(t *testing.T) {
	item := uuid.New()
	cd1 := stackFile(item, "/m/Movie (2001)/Movie (2001) cd1.mkv", 1, "", 3_000_000)
	cd2 := stackFile(item, "/m/Movie (2001)/Movie (2001) cd2.mkv", 2, "", 2_000_000)
	single := stackFile(item, "/m/Movie (2001)/Movie (2001) - 4K.mkv", 0, "4K", 5_000_000)

	t.Run("two parts in order whatever the input order", func(t *testing.T) {
		got := Stack([]File{single, cd2, cd1}, cd2)
		if len(got) != 2 || got[0].ID != cd1.ID || got[1].ID != cd2.ID {
			t.Fatalf("Stack = %v, want cd1, cd2", ids(got))
		}
		if d := StackDurationMS(got); d == nil || *d != 5_000_000 {
			t.Fatalf("StackDurationMS = %v, want 5000000", d)
		}
	})
	t.Run("a lone part plays as a normal file", func(t *testing.T) {
		if got := Stack([]File{cd1, single}, cd1); got != nil {
			t.Fatalf("Stack = %v, want nil", ids(got))
		}
	})
	t.Run("a file without a part number is never stacked", func(t *testing.T) {
		if got := Stack([]File{cd1, cd2, single}, single); got != nil {
			t.Fatalf("Stack = %v, want nil", ids(got))
		}
	})
	t.Run("parts of different editions are separate", func(t *testing.T) {
		dc1 := stackFile(item, "/m/Movie (2001)/Movie (2001) {edition-Director's Cut} cd1.mkv", 1, "Director's Cut", 1)
		dc2 := stackFile(item, "/m/Movie (2001)/Movie (2001) {edition-Director's Cut} cd2.mkv", 2, "director's cut", 1)
		files := []File{cd1, cd2, dc1, dc2}
		if got := Stack(files, dc1); len(got) != 2 || got[0].ID != dc1.ID || got[1].ID != dc2.ID {
			t.Fatalf("director's cut stack = %v", ids(got))
		}
		if got := Stack(files, cd2); len(got) != 2 || got[0].ID != cd1.ID {
			t.Fatalf("plain stack = %v", ids(got))
		}
	})
	t.Run("other folder, other extension or other item don't join", func(t *testing.T) {
		otherDir := stackFile(item, "/m/Other/Movie (2001) cd2.mkv", 2, "", 1)
		otherExt := stackFile(item, "/m/Movie (2001)/Movie (2001) cd2.avi", 2, "", 1)
		otherItem := stackFile(uuid.New(), "/m/Movie (2001)/Movie (2001) cd2.mkv", 2, "", 1)
		for _, g := range []File{otherDir, otherExt, otherItem} {
			if got := Stack([]File{cd1, g}, cd1); got != nil {
				t.Fatalf("Stack with %s = %v, want nil", g.FilePath, ids(got))
			}
		}
	})
	t.Run("two files claiming one part make no stack", func(t *testing.T) {
		dup := stackFile(item, "/m/Movie (2001)/Movie (2001) cd01.mkv", 1, "", 1)
		if got := Stack([]File{cd1, cd2, dup}, cd2); got != nil {
			t.Fatalf("Stack = %v, want nil", ids(got))
		}
	})
	t.Run("windows paths group by folder too", func(t *testing.T) {
		a := stackFile(item, `C:\m\Movie (2001)\Movie (2001) pt1.mkv`, 1, "", 1)
		b := stackFile(item, `C:\m\Movie (2001)\Movie (2001) pt2.mkv`, 2, "", 1)
		if got := Stack([]File{a, b}, b); len(got) != 2 {
			t.Fatalf("Stack = %v, want 2 parts", ids(got))
		}
	})
	t.Run("unknown part duration gives no total", func(t *testing.T) {
		noDur := stackFile(item, "/m/Movie (2001)/Movie (2001) cd3.mkv", 3, "", 0)
		if d := StackDurationMS([]File{cd1, cd2, noDur}); d != nil {
			t.Fatalf("StackDurationMS = %d, want nil", *d)
		}
	})
}

func TestOrderFiles(t *testing.T) {
	item := uuid.New()
	cd1 := stackFile(item, "/m/M (2001)/M (2001) cd1.mkv", 1, "", 1)
	cd2 := stackFile(item, "/m/M (2001)/M (2001) cd2.mkv", 2, "", 1)
	uhd := stackFile(item, "/m/M (2001)/M (2001) - 4K.mkv", 0, "4K", 1)
	lone := stackFile(item, "/m/M (2001)/M (2001) - 720p cd1.mkv", 1, "720p", 1)

	// Quality order from the database: 4K, then cd2 (higher bitrate), the
	// lone 720p "cd1", then cd1.
	got := OrderFiles([]File{uhd, cd2, lone, cd1})
	want := []File{uhd, cd1, cd2, lone}
	if len(got) != len(want) {
		t.Fatalf("OrderFiles = %v", ids(got))
	}
	for i := range want {
		if got[i].ID != want[i].ID {
			t.Fatalf("OrderFiles[%d] = %s, want %s (all: %v)", i, got[i].FilePath, want[i].FilePath, ids(got))
		}
	}
}

func ids(fs []File) []string {
	out := make([]string, len(fs))
	for i, f := range fs {
		out[i] = f.FilePath
	}
	return out
}
