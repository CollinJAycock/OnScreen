// Package respond provides helpers for writing consistent JSON API responses.
// All OnScreen native API responses follow the envelope format:
//
//	Success:    { "data": {...} }
//	List:       { "data": [...], "meta": { "total": N, "cursor": "..." } }
//	Error:      { "error": { "code": "NOT_FOUND", "message": "...", "request_id": "..." } }
package respond

import (
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"strconv"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/onscreen/onscreen/internal/i18n"
	"github.com/onscreen/onscreen/internal/observability"
)

// Success writes a 200 JSON response with the data envelope.
func Success(w http.ResponseWriter, r *http.Request, data any) {
	JSON(w, r, http.StatusOK, envelope{"data": data})
}

// Created writes a 201 JSON response with the data envelope.
func Created(w http.ResponseWriter, r *http.Request, data any) {
	JSON(w, r, http.StatusCreated, envelope{"data": data})
}

// Accepted writes a 202 JSON response with the data envelope. Used by
// async endpoints (job-queued OCR, etc.) to return a job descriptor
// immediately while the actual work runs in a server-lifetime goroutine.
func Accepted(w http.ResponseWriter, r *http.Request, data any) {
	JSON(w, r, http.StatusAccepted, envelope{"data": data})
}

// NoContent writes a 204 response with no body.
func NoContent(w http.ResponseWriter) {
	w.WriteHeader(http.StatusNoContent)
}

// List writes a 200 JSON response with data array and pagination meta.
func List(w http.ResponseWriter, r *http.Request, data any, total int64, cursor string) {
	JSON(w, r, http.StatusOK, envelope{
		"data": data,
		"meta": map[string]any{
			"total":  total,
			"cursor": cursor,
		},
	})
}

// Error writes a JSON error response. message is sent as written (English):
// use it for errors only a developer meets ("invalid item id"). An error a
// person can see in an app goes through [Fail], which translates it.
func Error(w http.ResponseWriter, r *http.Request, status int, code, message string) {
	reqID := observability.RequestIDFromContext(r.Context())
	JSON(w, r, status, envelope{
		"error": map[string]string{
			"code":       code,
			"message":    message,
			"request_id": reqID,
		},
	})
}

// Fail writes a JSON error response whose message is the catalog's text for
// key (internal/i18n, locales/*/errors.json) in the request's language
// (Accept-Language; the web and TV apps send the language they show). code
// stays the stable, untranslated field clients branch on; the English text
// is what Error would have sent.
func Fail(w http.ResponseWriter, r *http.Request, status int, code, key string, p i18n.Params) {
	w.Header().Add("Vary", "Accept-Language")
	Error(w, r, status, code, i18n.T(i18n.FromRequest(r), key, p))
}

// FailN is [Fail] for a plural message: the form of key for count (key_one /
// key_other, i18n.TN) in the request's language, with {count} and p filled in.
func FailN(w http.ResponseWriter, r *http.Request, status int, code, key string, count int, p i18n.Params) {
	w.Header().Add("Vary", "Accept-Language")
	Error(w, r, status, code, i18n.TN(i18n.FromRequest(r), key, count, p))
}

// BadRequestT is [BadRequest] with a translated message (see [Fail]).
func BadRequestT(w http.ResponseWriter, r *http.Request, key string, p i18n.Params) {
	Fail(w, r, http.StatusBadRequest, "BAD_REQUEST", key, p)
}

// ValidationErrorT is [ValidationError] with a translated message (see [Fail]).
func ValidationErrorT(w http.ResponseWriter, r *http.Request, key string, p i18n.Params) {
	Fail(w, r, http.StatusUnprocessableEntity, "VALIDATION", key, p)
}

// NotFound writes a 404 error response.
func NotFound(w http.ResponseWriter, r *http.Request) {
	Fail(w, r, http.StatusNotFound, "NOT_FOUND", "error.notFound", nil)
}

// BadRequest writes a 400 error response.
func BadRequest(w http.ResponseWriter, r *http.Request, message string) {
	Error(w, r, http.StatusBadRequest, "BAD_REQUEST", message)
}

// ValidationError writes a 422 error response.
func ValidationError(w http.ResponseWriter, r *http.Request, message string) {
	Error(w, r, http.StatusUnprocessableEntity, "VALIDATION", message)
}

// Unauthorized writes a 401 error response.
func Unauthorized(w http.ResponseWriter, r *http.Request) {
	Fail(w, r, http.StatusUnauthorized, "UNAUTHORIZED", "error.unauthorized", nil)
}

// Forbidden writes a 403 error response.
func Forbidden(w http.ResponseWriter, r *http.Request) {
	Fail(w, r, http.StatusForbidden, "FORBIDDEN", "error.forbidden", nil)
}

// ServiceUnavailable writes a 503 — the node can't serve the request right now
// (e.g. a write reached a read-only standby during a failover; a dependency is
// down). Distinct from a 500 so clients/proxies know to retry, possibly against
// another site. Used by /auth/refresh when the token could not be checked, so a
// DB blip doesn't read as "signed out"; the read-only-standby mapping is not
// wired yet — see [IsReadOnlyError].
func ServiceUnavailable(w http.ResponseWriter, r *http.Request, message string) {
	if message == "" {
		Fail(w, r, http.StatusServiceUnavailable, "service_unavailable", "error.unavailable", nil)
		return
	}
	Error(w, r, http.StatusServiceUnavailable, "service_unavailable", message)
}

// IsReadOnlyError reports whether err is a PostgreSQL "read-only transaction"
// error (SQLSTATE 25006) — a write that reached a read-only standby.
//
// NOTHING CALLS THIS YET. It is the intended building block for the HA story:
// a handler whose write fails this way maps it to [ServiceUnavailable] so the
// client retries against the writable primary. Until a caller exists, a write
// to a standby surfaces as an opaque 500, so don't read the presence of this
// helper as evidence the behaviour is in place. Wiring it means routing write
// failures through a shared "write error" responder rather than the bare
// InternalError calls scattered across the handlers today.
func IsReadOnlyError(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "25006"
}

func InternalError(w http.ResponseWriter, r *http.Request) {
	Fail(w, r, http.StatusInternalServerError, "INTERNAL", "error.internal", nil)
}

// Pagination is the parsed (limit, offset) pair for a list request.
type Pagination struct {
	Limit  int32
	Offset int32
}

// ParsePagination reads `limit` and `offset` query params, applying defaults
// and clamping `limit` to maxLimit. Negative or non-numeric values silently
// fall back to the defaults — handlers don't need to special-case them. Pass
// maxLimit=0 to use the package default of 200.
func ParsePagination(r *http.Request, defaultLimit, maxLimit int) Pagination {
	if defaultLimit <= 0 {
		defaultLimit = 50
	}
	if maxLimit <= 0 {
		maxLimit = 200
	}
	q := r.URL.Query()
	limit := int32(defaultLimit)
	if raw := q.Get("limit"); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 {
			if n > maxLimit {
				n = maxLimit
			}
			limit = int32(n)
		}
	}
	var offset int32
	if raw := q.Get("offset"); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n >= 0 {
			// Clamp before narrowing. On 64-bit, Atoi accepts values far past
			// int32, and the conversion wraps: ?offset=2147483648 became
			// -2147483648, which reaches Postgres as a negative OFFSET and
			// errors the whole query into a 500. Clamping keeps an absurd
			// offset behaving like what it is — a page past the end.
			if n > math.MaxInt32 {
				n = math.MaxInt32
			}
			offset = int32(n)
		}
	}
	return Pagination{Limit: limit, Offset: offset}
}

// ParseLimit reads the `limit` query param, applying defaults and clamping to
// maxLimit. Negative or non-numeric values silently fall back to the default.
// Pass maxLimit=0 to use the package default of 200. Use this for endpoints
// that page by something other than offset (e.g. cron-task run history).
func ParseLimit(r *http.Request, defaultLimit, maxLimit int) int32 {
	if defaultLimit <= 0 {
		defaultLimit = 50
	}
	if maxLimit <= 0 {
		maxLimit = 200
	}
	limit := int32(defaultLimit)
	if raw := r.URL.Query().Get("limit"); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 {
			if n > maxLimit {
				n = maxLimit
			}
			limit = int32(n)
		}
	}
	return limit
}

// JSON writes any value as JSON with the given HTTP status code.
func JSON(w http.ResponseWriter, _ *http.Request, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		// Nothing we can do — response already started.
		_ = err
	}
}

type envelope map[string]any
