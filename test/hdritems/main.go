// List HDR movie item IDs (comma-separated on stdout) so the load test can drive
// HDR-only content and saturate the GPU tonemap path. Titles go to stderr.
//
//	go run ./test/hdritems
package main

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/jackc/pgx/v5"
)

func main() {
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

	rows, err := conn.Query(ctx, `
		SELECT DISTINCT mi.id::text, mi.title, mf.hdr_type, COALESCE(mf.resolution_h,0)
		FROM media_items mi
		JOIN media_files mf ON mf.media_item_id = mi.id
		WHERE mi.type = 'movie' AND mf.hdr_type IS NOT NULL AND mf.hdr_type <> '' AND mf.status = 'active'
		ORDER BY mi.title`)
	if err != nil {
		fmt.Fprintf(os.Stderr, "query: %v\n", err)
		os.Exit(1)
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id, title, hdr string
		var h int
		if err := rows.Scan(&id, &title, &hdr, &h); err != nil {
			continue
		}
		ids = append(ids, id)
		fmt.Fprintf(os.Stderr, "  %-40s %s %dp\n", title, hdr, h)
	}
	fmt.Fprintf(os.Stderr, "=== %d HDR movies ===\n", len(ids))
	fmt.Println(strings.Join(ids, ","))
}
