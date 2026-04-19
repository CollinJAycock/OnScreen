// Package migrations embeds the goose SQL migration files so the running
// binary can compare the highest version it expects against what the DB has
// applied. Goose itself reads the same files from /migrations at runtime;
// this embed exists purely for the readiness check, not for execution.
package migrations

import "embed"

//go:embed *.sql
var FS embed.FS
