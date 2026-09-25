package dbtools

import (
	"net/url"
	"os"
	"strings"
)

// CommandDSN returns the arguments and environment for invoking a libpq client
// tool (pg_dump, pg_restore) against dsn WITHOUT putting the password on the
// command line.
//
// A password inside argv is visible to every local user via the process table
// (ps, /proc/<pid>/cmdline, Windows process listings) for the life of the
// command, and it leaks into any error message that echoes the command. For a
// URL-form DSN (postgres://user:pass@host/db) the password is removed from the
// DSN and supplied through PGPASSWORD in the child's environment instead, where
// only the process owner can read it. Key/value DSNs and URLs without a
// password are returned unchanged with the parent environment.
func CommandDSN(dsn string) (cleanDSN string, env []string) {
	env = os.Environ()
	if !strings.HasPrefix(dsn, "postgres://") && !strings.HasPrefix(dsn, "postgresql://") {
		return dsn, env
	}
	u, err := url.Parse(dsn)
	if err != nil || u.User == nil {
		return dsn, env
	}
	pw, hasPW := u.User.Password()
	if !hasPW {
		return dsn, env
	}
	u.User = url.User(u.User.Username())
	return u.String(), append(env, "PGPASSWORD="+pw)
}
