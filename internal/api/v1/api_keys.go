package v1

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onscreen/onscreen/internal/api/middleware"
	"github.com/onscreen/onscreen/internal/api/respond"
	"github.com/onscreen/onscreen/internal/audit"
	"github.com/onscreen/onscreen/internal/auth"
	"github.com/onscreen/onscreen/internal/db/gen"
)

// APIKeysDB is the api_keys subset the key management endpoints need.
// Satisfied by *gen.Queries — on the PRIMARY pool, so a deletion is visible
// to the auth middleware's next key lookup immediately.
type APIKeysDB interface {
	CreateAPIKey(ctx context.Context, arg gen.CreateAPIKeyParams) (gen.CreateAPIKeyRow, error)
	CountAPIKeysForUser(ctx context.Context, userID uuid.UUID) (int64, error)
	ListAPIKeysForUser(ctx context.Context, userID uuid.UUID) ([]gen.ListAPIKeysForUserRow, error)
	ListAllAPIKeys(ctx context.Context) ([]gen.ListAllAPIKeysRow, error)
	DeleteAPIKeyForUser(ctx context.Context, arg gen.DeleteAPIKeyForUserParams) (gen.DeleteAPIKeyForUserRow, error)
	DeleteAPIKey(ctx context.Context, id uuid.UUID) (gen.DeleteAPIKeyRow, error)
}

// maxAPIKeysPerUser caps how many keys one account can hold. Generous for
// any real set of integrations; it stops a runaway script filling the table.
const maxAPIKeysPerUser = 50

// maxAPIKeyNameLen bounds the label a user gives a key.
const maxAPIKeyNameLen = 100

// APIKeyHandler manages personal API keys: GET/POST /users/me/api-keys,
// DELETE /users/me/api-keys/{id}, and the admin GET /admin/api-keys and
// DELETE /admin/api-keys/{id}.
//
// None of these routes accept an API key as the caller's credential (the
// router mounts middleware.RefuseAPIKey on them): a leaked key must not be
// able to mint further keys, or delete the owner's other keys to cover its
// tracks.
type APIKeyHandler struct {
	db     APIKeysDB
	audit  *audit.Logger
	logger *slog.Logger
	now    func() time.Time
}

// NewAPIKeyHandler creates an APIKeyHandler.
func NewAPIKeyHandler(db APIKeysDB, logger *slog.Logger) *APIKeyHandler {
	return &APIKeyHandler{db: db, logger: logger, now: time.Now}
}

// WithAudit attaches an audit logger. Returns the handler for chaining.
func (h *APIKeyHandler) WithAudit(a *audit.Logger) *APIKeyHandler {
	h.audit = a
	return h
}

// apiKeyJSON is one key in a list. The key itself is never returned after
// creation; prefix is its first characters, enough to tell keys apart.
type apiKeyJSON struct {
	ID         string     `json:"id"`
	Name       string     `json:"name"`
	Prefix     string     `json:"prefix"`
	Scope      string     `json:"scope"`
	CreatedAt  time.Time  `json:"created_at"`
	LastUsedAt *time.Time `json:"last_used_at"`
	ExpiresAt  *time.Time `json:"expires_at"`
	Expired    bool       `json:"expired"`
	// Owner, on the admin list only.
	UserID   string `json:"user_id,omitempty"`
	Username string `json:"username,omitempty"`
}

// createdAPIKeyJSON is the creation response: the key's row plus the key
// itself, shown this once.
type createdAPIKeyJSON struct {
	apiKeyJSON
	Key string `json:"key"`
}

func tsPtr(t pgtype.Timestamptz) *time.Time {
	if !t.Valid {
		return nil
	}
	v := t.Time
	return &v
}

func (h *APIKeyHandler) toJSON(id uuid.UUID, name, prefix, scope string, created, lastUsed, expires pgtype.Timestamptz) apiKeyJSON {
	out := apiKeyJSON{
		ID:         id.String(),
		Name:       name,
		Prefix:     prefix,
		Scope:      scope,
		CreatedAt:  created.Time,
		LastUsedAt: tsPtr(lastUsed),
		ExpiresAt:  tsPtr(expires),
	}
	out.Expired = expires.Valid && !expires.Time.After(h.now())
	return out
}

// ListMine handles GET /api/v1/users/me/api-keys.
func (h *APIKeyHandler) ListMine(w http.ResponseWriter, r *http.Request) {
	claims := middleware.ClaimsFromContext(r.Context())
	if claims == nil {
		respond.Unauthorized(w, r)
		return
	}
	rows, err := h.db.ListAPIKeysForUser(r.Context(), claims.UserID)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "list api keys", "user_id", claims.UserID, "err", err)
		respond.InternalError(w, r)
		return
	}
	out := make([]apiKeyJSON, 0, len(rows))
	for _, k := range rows {
		out = append(out, h.toJSON(k.ID, k.Name, k.KeyPrefix, k.Scope, k.CreatedAt, k.LastUsedAt, k.ExpiresAt))
	}
	respond.Success(w, r, out)
}

// createAPIKeyRequest is the POST body. scope defaults to full; expires_at
// (RFC 3339) is optional and must be in the future.
type createAPIKeyRequest struct {
	Name      string     `json:"name"`
	Scope     string     `json:"scope"`
	ExpiresAt *time.Time `json:"expires_at"`
}

// CreateMine handles POST /api/v1/users/me/api-keys. The response carries the
// key itself; it is never retrievable again.
func (h *APIKeyHandler) CreateMine(w http.ResponseWriter, r *http.Request) {
	claims := middleware.ClaimsFromContext(r.Context())
	if claims == nil {
		respond.Unauthorized(w, r)
		return
	}
	var body createAPIKeyRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		respond.BadRequest(w, r, "invalid request body")
		return
	}
	body.Name = strings.TrimSpace(body.Name)
	if body.Name == "" {
		respond.ValidationError(w, r, "name is required")
		return
	}
	if utf8.RuneCountInString(body.Name) > maxAPIKeyNameLen {
		respond.ValidationError(w, r, "name is too long")
		return
	}
	if body.Scope == "" {
		body.Scope = auth.APIKeyScopeFull
	}
	if !auth.ValidAPIKeyScope(body.Scope) {
		respond.ValidationError(w, r, `scope must be "full" or "read_only"`)
		return
	}
	var expires pgtype.Timestamptz
	if body.ExpiresAt != nil {
		if !body.ExpiresAt.After(h.now()) {
			respond.ValidationError(w, r, "expires_at must be in the future")
			return
		}
		expires = pgtype.Timestamptz{Time: *body.ExpiresAt, Valid: true}
	}
	ctx := r.Context()
	n, err := h.db.CountAPIKeysForUser(ctx, claims.UserID)
	if err != nil {
		h.logger.ErrorContext(ctx, "count api keys", "user_id", claims.UserID, "err", err)
		respond.InternalError(w, r)
		return
	}
	if n >= maxAPIKeysPerUser {
		respond.Error(w, r, http.StatusConflict, "API_KEY_LIMIT",
			"you have too many API keys; delete one you no longer use first")
		return
	}
	raw, hash, prefix, err := auth.GenerateAPIKey()
	if err != nil {
		h.logger.ErrorContext(ctx, "generate api key", "err", err)
		respond.InternalError(w, r)
		return
	}
	row, err := h.db.CreateAPIKey(ctx, gen.CreateAPIKeyParams{
		UserID:    claims.UserID,
		Name:      body.Name,
		KeyHash:   hash,
		KeyPrefix: prefix,
		Scope:     body.Scope,
		ExpiresAt: expires,
	})
	if err != nil {
		h.logger.ErrorContext(ctx, "create api key", "user_id", claims.UserID, "err", err)
		respond.InternalError(w, r)
		return
	}
	if h.audit != nil {
		detail := map[string]any{
			"key_id": row.ID.String(),
			"name":   truncate(row.Name, 200),
			"prefix": row.KeyPrefix,
			"scope":  row.Scope,
		}
		if expires.Valid {
			detail["expires_at"] = expires.Time.UTC().Format(time.RFC3339)
		}
		h.audit.Log(ctx, &claims.UserID, audit.ActionAPIKeyCreate, claims.UserID.String(), detail, audit.ClientIP(r))
	}
	respond.Created(w, r, createdAPIKeyJSON{
		apiKeyJSON: h.toJSON(row.ID, row.Name, row.KeyPrefix, row.Scope, row.CreatedAt, row.LastUsedAt, row.ExpiresAt),
		Key:        raw,
	})
}

// DeleteMine handles DELETE /api/v1/users/me/api-keys/{id}. The key stops
// working on its next request.
func (h *APIKeyHandler) DeleteMine(w http.ResponseWriter, r *http.Request) {
	claims := middleware.ClaimsFromContext(r.Context())
	if claims == nil {
		respond.Unauthorized(w, r)
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		respond.BadRequest(w, r, "invalid api key id")
		return
	}
	ctx := r.Context()
	// Scoped by owner in SQL: another user's key id matches no row (404).
	row, err := h.db.DeleteAPIKeyForUser(ctx, gen.DeleteAPIKeyForUserParams{ID: id, UserID: claims.UserID})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			respond.NotFound(w, r)
			return
		}
		h.logger.ErrorContext(ctx, "delete api key", "user_id", claims.UserID, "key_id", id, "err", err)
		respond.InternalError(w, r)
		return
	}
	h.logDelete(r, claims.UserID, row.UserID, row.ID, row.Name, row.KeyPrefix, false)
	respond.NoContent(w)
}

// AdminList handles GET /api/v1/admin/api-keys: every user's keys.
func (h *APIKeyHandler) AdminList(w http.ResponseWriter, r *http.Request) {
	claims := middleware.ClaimsFromContext(r.Context())
	if claims == nil || !claims.IsAdmin {
		respond.Forbidden(w, r)
		return
	}
	rows, err := h.db.ListAllAPIKeys(r.Context())
	if err != nil {
		h.logger.ErrorContext(r.Context(), "admin list api keys", "err", err)
		respond.InternalError(w, r)
		return
	}
	out := make([]apiKeyJSON, 0, len(rows))
	for _, k := range rows {
		j := h.toJSON(k.ID, k.Name, k.KeyPrefix, k.Scope, k.CreatedAt, k.LastUsedAt, k.ExpiresAt)
		j.UserID = k.UserID.String()
		j.Username = k.Username
		out = append(out, j)
	}
	respond.Success(w, r, out)
}

// AdminDelete handles DELETE /api/v1/admin/api-keys/{id}: revoke any user's
// key.
func (h *APIKeyHandler) AdminDelete(w http.ResponseWriter, r *http.Request) {
	claims := middleware.ClaimsFromContext(r.Context())
	if claims == nil || !claims.IsAdmin {
		respond.Forbidden(w, r)
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		respond.BadRequest(w, r, "invalid api key id")
		return
	}
	ctx := r.Context()
	row, err := h.db.DeleteAPIKey(ctx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			respond.NotFound(w, r)
			return
		}
		h.logger.ErrorContext(ctx, "admin delete api key", "key_id", id, "err", err)
		respond.InternalError(w, r)
		return
	}
	h.logDelete(r, claims.UserID, row.UserID, row.ID, row.Name, row.KeyPrefix, true)
	respond.NoContent(w)
}

// logDelete writes the api_key.delete audit record: actor, key owner as the
// target, and which key.
func (h *APIKeyHandler) logDelete(r *http.Request, actor, owner, keyID uuid.UUID, name, prefix string, byAdmin bool) {
	if h.audit == nil {
		return
	}
	h.audit.Log(r.Context(), &actor, audit.ActionAPIKeyDelete, owner.String(), map[string]any{
		"key_id":   keyID.String(),
		"name":     truncate(name, 200),
		"prefix":   prefix,
		"by_admin": byAdmin,
	}, audit.ClientIP(r))
}
