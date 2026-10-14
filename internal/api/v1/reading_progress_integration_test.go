//go:build integration

package v1

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/auth"
	"github.com/onscreen/onscreen/internal/db/gen"
	"github.com/onscreen/onscreen/internal/testdb"
)

// Reading progress against real Postgres: newest-by-client-time wins in SQL,
// the row is per user, and finishing a book writes the watched mark the
// library's read badge comes from (user_watch_state).
func TestReadingProgress_Integration(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	q := gen.New(pool)
	h := NewReadingProgressHandler(q, rpAccess{}, slog.New(slog.NewTextHandler(io.Discard, nil)))

	user := func() uuid.UUID {
		u, err := q.CreateUser(ctx, gen.CreateUserParams{Username: "r-" + uuid.NewString()[:8]})
		if err != nil {
			t.Fatalf("create user: %v", err)
		}
		return u.ID
	}
	alice, bob := user(), user()
	lib, err := q.CreateLibrary(ctx, gen.CreateLibraryParams{
		Name: "Books-" + uuid.NewString()[:8], Type: "book", ScanPaths: []string{"/tmp/books"},
		Agent: "tmdb", Language: "en", ScanInterval: time.Hour, MetadataRefreshInterval: 24 * time.Hour,
	})
	if err != nil {
		t.Fatalf("create library: %v", err)
	}
	row, err := q.CreateMediaItem(ctx, gen.CreateMediaItemParams{
		LibraryID: lib.ID, Type: "book", Title: "Novel", SortTitle: "novel", Genres: []string{}, Tags: []string{},
	})
	if err != nil {
		t.Fatalf("create book: %v", err)
	}
	book := row.ID

	put := func(as uuid.UUID, body string) readingProgressJSON {
		t.Helper()
		w := rpDo(h.Put, &auth.Claims{UserID: as}, http.MethodPut, book, body)
		if w.Code != http.StatusOK {
			t.Fatalf("put %s: %d %s", body, w.Code, w.Body.String())
		}
		var env rpEnvelope
		if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil || env.Data == nil {
			t.Fatalf("decode: %v %s", err, w.Body.String())
		}
		return *env.Data
	}

	at := func(ago time.Duration) string { return time.Now().Add(-ago).UTC().Format(time.RFC3339) }
	p := put(alice, `{"locator":"epubcfi(/6/4!/4/2)","progression":0.25,"device_name":"Kindle-ish","updated_at":"`+at(2*time.Hour)+`"}`)
	if !*p.Applied {
		t.Fatal("first save not applied")
	}
	p = put(alice, `{"locator":"epubcfi(/6/2!/4/2)","progression":0.1,"device_name":"Phone","updated_at":"`+at(3*time.Hour)+`"}`)
	if *p.Applied || p.DeviceName != "Kindle-ish" || p.Progression != 0.25 {
		t.Fatalf("older save rewound the position: %+v", p)
	}
	p = put(alice, `{"locator":"epubcfi(/6/8!/4/2)","progression":0.5,"device_name":"Phone","updated_at":"`+at(time.Hour)+`"}`)
	if !*p.Applied || p.DeviceName != "Phone" {
		t.Fatalf("newer save refused: %+v", p)
	}
	if _, err := q.GetReadingProgress(ctx, gen.GetReadingProgressParams{UserID: bob, MediaID: book}); err == nil {
		t.Fatal("bob has alice's position")
	}

	status := func(as uuid.UUID) string {
		var s string
		err := pool.QueryRow(ctx, `SELECT COALESCE((SELECT status FROM user_watch_state WHERE user_id=$1 AND media_id=$2), 'unwatched')`, as, book).Scan(&s)
		if err != nil {
			t.Fatal(err)
		}
		return s
	}
	if status(alice) != "unwatched" {
		t.Fatal("half-read book already read")
	}
	put(alice, `{"locator":"epubcfi(/6/40!/4/2)","progression":0.99}`)
	if status(alice) != "watched" {
		t.Fatalf("finished book not marked read: %s", status(alice))
	}
	// Saving again at the end doesn't fail on the existing mark.
	put(alice, `{"locator":"epubcfi(/6/41!/4/2)","progression":1}`)
	if status(bob) != "unwatched" {
		t.Fatal("bob's book marked read")
	}

	// Deleting the book removes the positions (FK cascade).
	if _, err := pool.Exec(ctx, `DELETE FROM media_items WHERE id = $1`, book); err != nil {
		t.Fatal(err)
	}
	var n int
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM reading_progress WHERE media_id = $1`, book).Scan(&n)
	if n != 0 {
		t.Fatalf("%d positions survived the book", n)
	}
}
