// Read/modify the worker_fleet config in server_settings so a load test can push
// past the default per-node session caps to find the hardware wall. Restore after.
//
//	go run ./test/fleetcap -show
//	go run ./test/fleetcap -workers 64   # set every remote worker slot's max_sessions
//	go run ./test/fleetcap -workers 16   # restore
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"github.com/jackc/pgx/v5"
)

func main() {
	show := flag.Bool("show", false, "print current worker_fleet JSON")
	workers := flag.Int("workers", 0, "set every remote worker slot's max_sessions to N")
	embedded := flag.Int("embedded", 0, "set embedded_max_sessions to N (needs server restart to take effect)")
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

	var raw string
	if err := conn.QueryRow(ctx, "SELECT value FROM server_settings WHERE key='worker_fleet'").Scan(&raw); err != nil {
		fmt.Fprintf(os.Stderr, "read worker_fleet: %v\n", err)
		os.Exit(1)
	}
	if *show {
		fmt.Println(raw)
		return
	}

	var m map[string]any
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		fmt.Fprintf(os.Stderr, "unmarshal: %v\n", err)
		os.Exit(1)
	}
	if *embedded > 0 {
		m["embedded_max_sessions"] = *embedded
	}
	if *workers > 0 {
		if ws, ok := m["workers"].([]any); ok {
			for _, w := range ws {
				if wm, ok := w.(map[string]any); ok {
					wm["max_sessions"] = *workers
				}
			}
		}
	}
	b, _ := json.Marshal(m)
	if _, err := conn.Exec(ctx, "UPDATE server_settings SET value=$1, updated_at=now() WHERE key='worker_fleet'", string(b)); err != nil {
		fmt.Fprintf(os.Stderr, "update: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("updated worker_fleet:")
	fmt.Println(string(b))
}
