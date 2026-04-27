// Package respond provides helpers for writing consistent JSON API responses.
// All OnScreen native API responses follow the envelope format:
//
//	Success:    { "data": {...} }
//	List:       { "data": [...], "meta": { "total": N, "cursor": "..." } }
//	Error:      { "error": { "code": "NOT_FOUND", "message": "...", "request_id": "..." } }
package respond

import (
	"encoding/json"
	"net/http"
	"strconv"

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

// Error writes a JSON error response.
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

// NotFound writes a 404 error response.
func NotFound(w http.ResponseWriter, r *http.Request) {
	Error(w, r, http.StatusNotFound, "NOT_FOUND", "resource not found")
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
	Error(w, r, http.StatusUnauthorized, "UNAUTHORIZED", "authentication required")
}

// Forbidden writes a 403 error response.
func Forbidden(w http.ResponseWriter, r *http.Request) {
	Error(w, r, http.StatusForbidden, "FORBIDDEN", "insufficient permissions")
}

// InternalError writes a 500 error response. The internal error is not exposed
// to the client; it should be logged by the caller.
func InternalError(w http.ResponseWriter, r *http.Request) {
	Error(w, r, http.StatusInternalServerError, "INTERNAL", "an unexpected error occurred")
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
