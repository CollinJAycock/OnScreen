// Seed throwaway load-test users by cloning testUser's password hash so they all
// share the LoadTest123! password. Used to drive the multi-user transcode load
// test past the per-user 5-stream cap. Run with -delete to remove them after.
//
//	go run ./test/seedusers -n 12
//	go run ./test/seedusers -delete
package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"github.com/jackc/pgx/v5"
)

func main() {
	n := flag.Int("n", 12, "number of users to create (testload0..testload{n-1})")
	prefix := flag.String("prefix", "testload", "username prefix")
	clone := flag.String("clone", "testUser", "existing user whose password_hash to clone")
	del := flag.Bool("delete", false, "delete all users matching prefix instead of creating")
	flag.Parse()

	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		dsn = "postgres://onscreen:onscreen@localhost:5432/onscreen?sslmode=disable"
	}
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		fmt.Fprintf(os.Stderr, "connect: %v\n", err)
		os.Exit(1)
	}
	defer conn.Close(ctx)

	if *del {
		// Sessions live in Valkey, not Postgres; watch/pref rows reference users
		// via ON DELETE CASCADE, so a plain delete is enough.
		ct, err := conn.Exec(ctx, "DELETE FROM users WHERE username LIKE $1", *prefix+"%")
		if err != nil {
			fmt.Fprintf(os.Stderr, "delete: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("deleted %d users matching %s%%\n", ct.RowsAffected(), *prefix)
		return
	}

	var hash *string
	if err := conn.QueryRow(ctx, "SELECT password_hash FROM users WHERE username=$1", *clone).Scan(&hash); err != nil {
		fmt.Fprintf(os.Stderr, "clone user %q not found: %v\n", *clone, err)
		os.Exit(1)
	}
	if hash == nil {
		fmt.Fprintf(os.Stderr, "clone user %q has no password_hash (SSO-only?)\n", *clone)
		os.Exit(1)
	}

	created := 0
	for i := 0; i < *n; i++ {
		u := fmt.Sprintf("%s%d", *prefix, i)
		ct, err := conn.Exec(ctx,
			"INSERT INTO users (username, password_hash, is_admin) VALUES ($1,$2,false) ON CONFLICT (username) DO NOTHING",
			u, *hash)
		if err != nil {
			fmt.Fprintf(os.Stderr, "insert %s: %v\n", u, err)
			os.Exit(1)
		}
		if ct.RowsAffected() > 0 {
			created++
		}
	}
	fmt.Printf("created %d new users (%s0..%s%d), password = clone of %q\n", created, *prefix, *prefix, *n-1, *clone)
}
