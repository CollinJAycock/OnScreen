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
