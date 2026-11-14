package respond

import (
	"context"
	"errors"
	"log/slog"
	"net/http"

	"github.com/jackc/pgx/v5"
)

// ServerError answers a request that failed on the server's side and logs
// why, so no 500 goes out without a trace (InternalError takes no error and
// can't). msg names the operation ("list collections"); args are extra slog
// attributes.
//
//   - A client that went away (its request context is done, or the error is
//     a broken pipe / reset) is normal behaviour, not a failure: it is logged
//     at debug and nothing is written — there is nobody to read it, and a 5xx
//     would only add noise to the access log and metrics.
//   - A write that reached a read-only standby (SQLSTATE 25006, see
//     IsReadOnlyError) answers 503 with Retry-After, so the client retries
//     once the pool has re-homed to the primary, instead of an opaque 500.
//   - Anything else is logged at ERROR and answered 500.
func ServerError(w http.ResponseWriter, r *http.Request, logger *slog.Logger, err error, msg string, args ...any) {
	if logger == nil {
		logger = slog.Default()
	}
	ctx := r.Context()
	attrs := append(args[:len(args):len(args)], "err", err)
	switch {
	case clientGone(r, err):
		logger.DebugContext(ctx, msg+": client gone", attrs...)
	case IsReadOnlyError(err):
		logger.WarnContext(ctx, msg+": write reached a read-only database", attrs...)
		w.Header().Set("Retry-After", "5")
		Fail(w, r, http.StatusServiceUnavailable, "READ_ONLY_DATABASE", "error.unavailable", nil)
	default:
		logger.ErrorContext(ctx, msg, attrs...)
		InternalError(w, r)
	}
}

// NotFoundOrServerError answers 404 when err means the thing asked for
// doesn't exist (pgx.ErrNoRows, or one of the domain's own not-found
// sentinels), and otherwise hands err to ServerError. It replaces the
// "any error → 404" pattern, which turned a database outage into a stream of
// not-founds nobody could trace.
func NotFoundOrServerError(w http.ResponseWriter, r *http.Request, logger *slog.Logger, err error, msg string, notFound ...error) {
	if IsNotFound(err, notFound...) {
		NotFound(w, r)
		return
	}
	ServerError(w, r, logger, err, msg)
}

// IsNotFound reports whether err is pgx.ErrNoRows or wraps one of notFound.
func IsNotFound(err error, notFound ...error) bool {
	if errors.Is(err, pgx.ErrNoRows) {
		return true
	}
	for _, nf := range notFound {
		if errors.Is(err, nf) {
			return true
		}
	}
	return false
}

// clientGone is IsClientGone, minus the case of a server-side deadline: a
// query that ran out its own timeout while the client is still waiting is a
// real failure, so it only counts when the request itself is done.
func clientGone(r *http.Request, err error) bool {
	if r.Context().Err() != nil {
		return true
	}
	return IsClientGone(err) && !errors.Is(err, context.DeadlineExceeded)
}
