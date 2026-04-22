package v1

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/onscreen/onscreen/internal/api/middleware"
	"github.com/onscreen/onscreen/internal/api/respond"
	"github.com/onscreen/onscreen/internal/arr"
	"github.com/onscreen/onscreen/internal/audit"
	"github.com/onscreen/onscreen/internal/db/gen"
)

// ArrServicesDB is the slice of generated queries the arr-services admin
// handler needs. Defined as an interface so tests can substitute a fake.
type ArrServicesDB interface {
	ListArrServices(ctx context.Context) ([]gen.ArrService, error)
	GetArrService(ctx context.Context, id uuid.UUID) (gen.ArrService, error)
	CreateArrService(ctx context.Context, arg gen.CreateArrServiceParams) (gen.ArrService, error)
	UpdateArrService(ctx context.Context, arg gen.UpdateArrServiceParams) (gen.ArrService, error)
	DeleteArrService(ctx context.Context, id uuid.UUID) error
	SetArrServiceDefault(ctx context.Context, id uuid.UUID) error
	ClearArrServiceDefault(ctx context.Context, kind string) error
}

// ArrServicesHandler exposes admin-only CRUD for arr_services and a probe
// endpoint that fans out to a Radarr/Sonarr instance to fetch the lists
// needed to populate default-selection dropdowns.
type ArrServicesHandler struct {
	db        ArrServicesDB
	arrClient func(baseURL, apiKey string) *arr.Client
	logger    *slog.Logger
	audit     *audit.Logger
}

// NewArrServicesHandler builds the handler. arrClient is overridable for
// tests; in production callers pass arr.New.
func NewArrServicesHandler(db ArrServicesDB, logger *slog.Logger) *ArrServicesHandler {
	return &ArrServicesHandler{
		db:        db,
		arrClient: arr.New,
		logger:    logger,
	}
}

// WithAudit attaches an audit logger.
func (h *ArrServicesHandler) WithAudit(a *audit.Logger) *ArrServicesHandler {
	h.audit = a
	return h
}

// SetArrClientFactory overrides the arr.Client constructor. Used by tests
// to inject an httptest-backed transport.
func (h *ArrServicesHandler) SetArrClientFactory(f func(baseURL, apiKey string) *arr.Client) {
	if f != nil {
		h.arrClient = f
	}
}

// ── DTOs ──────────────────────────────────────────────────────────────────

// arrServiceDTO is the JSON shape returned to admin clients. The api_key
// is intentionally omitted from list/get responses to avoid leaking the
// credential into logs / browser dev tools — admins re-enter it on edit.
type arrServiceDTO struct {
	ID                      uuid.UUID `json:"id"`
	Name                    string    `json:"name"`
	Kind                    string    `json:"kind"`
	BaseURL                 string    `json:"base_url"`
	APIKeySet               bool      `json:"api_key_set"`
	DefaultQualityProfileID *int32    `json:"default_quality_profile_id,omitempty"`
	DefaultRootFolder       *string   `json:"default_root_folder,omitempty"`
	DefaultTags             []int32   `json:"default_tags"`
	MinimumAvailability     *string   `json:"minimum_availability,omitempty"`
	SeriesType              *string   `json:"series_type,omitempty"`
	SeasonFolder            *bool     `json:"season_folder,omitempty"`
	LanguageProfileID       *int32    `json:"language_profile_id,omitempty"`
	IsDefault               bool      `json:"is_default"`
	Enabled                 bool      `json:"enabled"`
	CreatedAt               string    `json:"created_at"`
	UpdatedAt               string    `json:"updated_at"`
}

func toArrServiceDTO(s gen.ArrService) arrServiceDTO {
	tags, _ := decodeArrTagIDs(s.DefaultTags)
	if tags == nil {
		tags = []int32{}
	}
	return arrServiceDTO{
		ID:                      s.ID,
		Name:                    s.Name,
		Kind:                    s.Kind,
		BaseURL:                 s.BaseUrl,
		APIKeySet:               s.ApiKey != "",
		DefaultQualityProfileID: s.DefaultQualityProfileID,
		DefaultRootFolder:       s.DefaultRootFolder,
		DefaultTags:             tags,
		MinimumAvailability:     s.MinimumAvailability,
		SeriesType:              s.SeriesType,
		SeasonFolder:            s.SeasonFolder,
		LanguageProfileID:       s.LanguageProfileID,
		IsDefault:               s.IsDefault,
		Enabled:                 s.Enabled,
		CreatedAt:               s.CreatedAt.Time.UTC().Format("2006-01-02T15:04:05Z"),
		UpdatedAt:               s.UpdatedAt.Time.UTC().Format("2006-01-02T15:04:05Z"),
	}
}

// ── Handlers ──────────────────────────────────────────────────────────────

// List handles GET /api/v1/admin/arr-services.
func (h *ArrServicesHandler) List(w http.ResponseWriter, r *http.Request) {
	rows, err := h.db.ListArrServices(r.Context())
	if err != nil {
		h.logger.ErrorContext(r.Context(), "list arr services", "err", err)
		respond.InternalError(w, r)
		return
	}
	out := make([]arrServiceDTO, 0, len(rows))
	for _, s := range rows {
		out = append(out, toArrServiceDTO(s))
	}
	respond.List(w, r, out, int64(len(out)), "")
}

// Get handles GET /api/v1/admin/arr-services/{id}.
func (h *ArrServicesHandler) Get(w http.ResponseWriter, r *http.Request) {
	id, err := parseUUID(r, "id")
	if err != nil {
		respond.BadRequest(w, r, "invalid arr service id")
		return
	}
	s, err := h.db.GetArrService(r.Context(), id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			respond.NotFound(w, r)
			return
		}
		h.logger.ErrorContext(r.Context(), "get arr service", "id", id, "err", err)
		respond.InternalError(w, r)
		return
	}
	respond.Success(w, r, toArrServiceDTO(s))
}

// arrServiceCreateBody is the payload for POST /api/v1/admin/arr-services.
type arrServiceCreateBody struct {
	Name                    string  `json:"name"`
	Kind                    string  `json:"kind"`
	BaseURL                 string  `json:"base_url"`
	APIKey                  string  `json:"api_key"`
	DefaultQualityProfileID *int32  `json:"default_quality_profile_id"`
	DefaultRootFolder       *string `json:"default_root_folder"`
	DefaultTags             []int32 `json:"default_tags"`
	MinimumAvailability     *string `json:"minimum_availability"`
	SeriesType              *string `json:"series_type"`
	SeasonFolder            *bool   `json:"season_folder"`
	LanguageProfileID       *int32  `json:"language_profile_id"`
	IsDefault               bool    `json:"is_default"`
	Enabled                 *bool   `json:"enabled"`
}

// Create handles POST /api/v1/admin/arr-services. The kind/base_url/api_key
// triple is required; everything else is optional and may be filled later
// via the probe endpoint + Update.
func (h *ArrServicesHandler) Create(w http.ResponseWriter, r *http.Request) {
	var body arrServiceCreateBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		respond.BadRequest(w, r, "invalid request body")
		return
	}
	body.Name = strings.TrimSpace(body.Name)
	body.Kind = strings.TrimSpace(strings.ToLower(body.Kind))
	body.BaseURL = strings.TrimRight(strings.TrimSpace(body.BaseURL), "/")
	body.APIKey = strings.TrimSpace(body.APIKey)
	if body.Name == "" {
		respond.ValidationError(w, r, "name is required")
		return
	}
	if body.Kind != "radarr" && body.Kind != "sonarr" {
		respond.ValidationError(w, r, "kind must be 'radarr' or 'sonarr'")
		return
	}
	if body.BaseURL == "" || body.APIKey == "" {
		respond.ValidationError(w, r, "base_url and api_key are required")
		return
	}

	enabled := true
	if body.Enabled != nil {
		enabled = *body.Enabled
	}

	tagsJSON, _ := encodeArrTagIDs(body.DefaultTags)

	if body.IsDefault {
		if err := h.db.ClearArrServiceDefault(r.Context(), body.Kind); err != nil {
			h.logger.ErrorContext(r.Context(), "clear default arr service", "err", err)
			respond.InternalError(w, r)
			return
		}
	}

	created, err := h.db.CreateArrService(r.Context(), gen.CreateArrServiceParams{
		Name:                    body.Name,
		Kind:                    body.Kind,
		BaseUrl:                 body.BaseURL,
		ApiKey:                  body.APIKey,
		DefaultQualityProfileID: body.DefaultQualityProfileID,
		DefaultRootFolder:       body.DefaultRootFolder,
		DefaultTags:             tagsJSON,
		MinimumAvailability:     body.MinimumAvailability,
		SeriesType:              body.SeriesType,
		SeasonFolder:            body.SeasonFolder,
		LanguageProfileID:       body.LanguageProfileID,
		IsDefault:               body.IsDefault,
		Enabled:                 enabled,
	})
	if err != nil {
		h.logger.ErrorContext(r.Context(), "create arr service", "err", err)
		respond.ValidationError(w, r, err.Error())
		return
	}

	h.auditEvent(r, audit.ActionArrServiceCreate, created.ID.String(), map[string]any{
		"name":       created.Name,
		"kind":       created.Kind,
		"base_url":   created.BaseUrl,
		"is_default": created.IsDefault,
		"enabled":    created.Enabled,
	})
	respond.Created(w, r, toArrServiceDTO(created))
}

// arrServiceUpdateBody is the payload for PATCH /api/v1/admin/arr-services/{id}.
// Every field is optional; nil pointers are no-ops. To clear a value, send
// an explicit zero (empty string / 0). API key is only updated if non-empty
// — a missing api_key in the JSON keeps the current credential.
type arrServiceUpdateBody struct {
	Name                    *string  `json:"name"`
	BaseURL                 *string  `json:"base_url"`
	APIKey                  *string  `json:"api_key"`
	DefaultQualityProfileID *int32   `json:"default_quality_profile_id"`
	DefaultRootFolder       *string  `json:"default_root_folder"`
	DefaultTags             *[]int32 `json:"default_tags"`
	MinimumAvailability     *string  `json:"minimum_availability"`
	SeriesType              *string  `json:"series_type"`
	SeasonFolder            *bool    `json:"season_folder"`
	LanguageProfileID       *int32   `json:"language_profile_id"`
	Enabled                 *bool    `json:"enabled"`
}

// Update handles PATCH /api/v1/admin/arr-services/{id}. is_default is not
// settable here — admins use the dedicated /set-default endpoint so the
// "exactly one default per kind" invariant stays atomic.
func (h *ArrServicesHandler) Update(w http.ResponseWriter, r *http.Request) {
	id, err := parseUUID(r, "id")
	if err != nil {
		respond.BadRequest(w, r, "invalid arr service id")
		return
	}
	existing, err := h.db.GetArrService(r.Context(), id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			respond.NotFound(w, r)
			return
		}
		h.logger.ErrorContext(r.Context(), "get arr service", "id", id, "err", err)
		respond.InternalError(w, r)
		return
	}
	var body arrServiceUpdateBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		respond.BadRequest(w, r, "invalid request body")
		return
	}

	params := gen.UpdateArrServiceParams{
		ID:                      existing.ID,
		Name:                    existing.Name,
		BaseUrl:                 existing.BaseUrl,
		ApiKey:                  existing.ApiKey,
		DefaultQualityProfileID: existing.DefaultQualityProfileID,
		DefaultRootFolder:       existing.DefaultRootFolder,
		DefaultTags:             existing.DefaultTags,
		MinimumAvailability:     existing.MinimumAvailability,
		SeriesType:              existing.SeriesType,
		SeasonFolder:            existing.SeasonFolder,
		LanguageProfileID:       existing.LanguageProfileID,
		IsDefault:               existing.IsDefault,
		Enabled:                 existing.Enabled,
	}
	if body.Name != nil {
		trimmed := strings.TrimSpace(*body.Name)
		if trimmed == "" {
			respond.ValidationError(w, r, "name cannot be empty")
			return
		}
		params.Name = trimmed
	}
	if body.BaseURL != nil {
		trimmed := strings.TrimRight(strings.TrimSpace(*body.BaseURL), "/")
		if trimmed == "" {
			respond.ValidationError(w, r, "base_url cannot be empty")
			return
		}
		params.BaseUrl = trimmed
	}
	if body.APIKey != nil && strings.TrimSpace(*body.APIKey) != "" {
		params.ApiKey = strings.TrimSpace(*body.APIKey)
	}
	if body.DefaultQualityProfileID != nil {
		params.DefaultQualityProfileID = body.DefaultQualityProfileID
	}
	if body.DefaultRootFolder != nil {
		params.DefaultRootFolder = body.DefaultRootFolder
	}
	if body.DefaultTags != nil {
		tagsJSON, _ := encodeArrTagIDs(*body.DefaultTags)
		params.DefaultTags = tagsJSON
	}
	if body.MinimumAvailability != nil {
		params.MinimumAvailability = body.MinimumAvailability
	}
	if body.SeriesType != nil {
		params.SeriesType = body.SeriesType
	}
	if body.SeasonFolder != nil {
		params.SeasonFolder = body.SeasonFolder
	}
	if body.LanguageProfileID != nil {
		params.LanguageProfileID = body.LanguageProfileID
	}
	if body.Enabled != nil {
		params.Enabled = *body.Enabled
	}

	updated, err := h.db.UpdateArrService(r.Context(), params)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "update arr service", "id", id, "err", err)
		respond.ValidationError(w, r, err.Error())
		return
	}

	h.auditEvent(r, audit.ActionArrServiceUpdate, updated.ID.String(), map[string]any{
		"name":    updated.Name,
		"kind":    updated.Kind,
		"enabled": updated.Enabled,
	})
	respond.Success(w, r, toArrServiceDTO(updated))
}

// SetDefault handles POST /api/v1/admin/arr-services/{id}/set-default.
// Two SQL calls: clear the existing default for this kind, then set this
// row's flag. The unique partial index would reject any concurrent attempt
// to set a second default, so the brief "no default" window is the only
// failure mode — and any in-flight Approve will retry on the next click.
func (h *ArrServicesHandler) SetDefault(w http.ResponseWriter, r *http.Request) {
	id, err := parseUUID(r, "id")
	if err != nil {
		respond.BadRequest(w, r, "invalid arr service id")
		return
	}
	existing, err := h.db.GetArrService(r.Context(), id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			respond.NotFound(w, r)
			return
		}
		h.logger.ErrorContext(r.Context(), "get arr service", "id", id, "err", err)
		respond.InternalError(w, r)
		return
	}
	if err := h.db.ClearArrServiceDefault(r.Context(), existing.Kind); err != nil {
		h.logger.ErrorContext(r.Context(), "clear default arr service", "kind", existing.Kind, "err", err)
		respond.InternalError(w, r)
		return
	}
	if err := h.db.SetArrServiceDefault(r.Context(), id); err != nil {
		h.logger.ErrorContext(r.Context(), "set default arr service", "id", id, "err", err)
		respond.InternalError(w, r)
		return
	}
	updated, err := h.db.GetArrService(r.Context(), id)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "get arr service after set default", "id", id, "err", err)
		respond.InternalError(w, r)
		return
	}
	h.auditEvent(r, audit.ActionArrServiceSetDefault, id.String(), map[string]any{
		"kind": existing.Kind,
		"name": existing.Name,
	})
	respond.Success(w, r, toArrServiceDTO(updated))
}

// Delete handles DELETE /api/v1/admin/arr-services/{id}. The ON DELETE
// SET NULL on media_requests means in-flight requests survive the deletion
// — they just lose their service binding and become orphaned (admin will
// need to re-route them).
func (h *ArrServicesHandler) Delete(w http.ResponseWriter, r *http.Request) {
	id, err := parseUUID(r, "id")
	if err != nil {
		respond.BadRequest(w, r, "invalid arr service id")
		return
	}
	existing, getErr := h.db.GetArrService(r.Context(), id)
	if err := h.db.DeleteArrService(r.Context(), id); err != nil {
		h.logger.ErrorContext(r.Context(), "delete arr service", "id", id, "err", err)
		respond.InternalError(w, r)
		return
	}
	if getErr == nil {
		h.auditEvent(r, audit.ActionArrServiceDelete, id.String(), map[string]any{
			"name":     existing.Name,
			"kind":     existing.Kind,
			"base_url": existing.BaseUrl,
		})
	}
	respond.NoContent(w)
}

// arrProbeBody is the payload for POST /api/v1/admin/arr-services/probe.
// Used by the admin "Test connection" + "populate dropdowns" flow before
// the row is saved. If api_key is omitted but service_id is supplied, the
// stored credential is reused (admins editing a saved row don't have to
// re-enter the key).
type arrProbeBody struct {
	Kind      string     `json:"kind"`
	BaseURL   string     `json:"base_url"`
	APIKey    string     `json:"api_key"`
	ServiceID *uuid.UUID `json:"service_id"`
}

// arrProbeResponse bundles everything the admin UI needs to populate the
// service-config form: connection metadata + lists for the four dropdowns.
// Each list is always present (empty array when the upstream returned 0
// items or 404'd, e.g. Sonarr v4 dropping languageProfile).
type arrProbeResponse struct {
	Status           string                `json:"status"`
	Version          string                `json:"version,omitempty"`
	AppName          string                `json:"app_name,omitempty"`
	QualityProfiles  []arr.QualityProfile  `json:"quality_profiles"`
	RootFolders      []arr.RootFolder      `json:"root_folders"`
	Tags             []arr.Tag             `json:"tags"`
	LanguageProfiles []arr.LanguageProfile `json:"language_profiles"`
}

// Probe handles POST /api/v1/admin/arr-services/probe. Calls Ping +
// QualityProfiles + RootFolders + Tags (+ LanguageProfiles for sonarr) and
// returns them in one round-trip so the form can render dropdowns without
// chaining four GETs.
func (h *ArrServicesHandler) Probe(w http.ResponseWriter, r *http.Request) {
	var body arrProbeBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		respond.BadRequest(w, r, "invalid request body")
		return
	}
	body.Kind = strings.TrimSpace(strings.ToLower(body.Kind))
	body.BaseURL = strings.TrimRight(strings.TrimSpace(body.BaseURL), "/")
	body.APIKey = strings.TrimSpace(body.APIKey)

	// Editing a saved row: pull base_url + api_key from storage so the form
	// doesn't have to round-trip the existing credential.
	if body.ServiceID != nil {
		existing, err := h.db.GetArrService(r.Context(), *body.ServiceID)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				respond.NotFound(w, r)
				return
			}
			h.logger.ErrorContext(r.Context(), "probe: get arr service", "id", *body.ServiceID, "err", err)
			respond.InternalError(w, r)
			return
		}
		if body.BaseURL == "" {
			body.BaseURL = existing.BaseUrl
		}
		if body.APIKey == "" {
			body.APIKey = existing.ApiKey
		}
		if body.Kind == "" {
			body.Kind = existing.Kind
		}
	}

	if body.Kind != "radarr" && body.Kind != "sonarr" {
		respond.ValidationError(w, r, "kind must be 'radarr' or 'sonarr'")
		return
	}
	if body.BaseURL == "" || body.APIKey == "" {
		respond.ValidationError(w, r, "base_url and api_key are required")
		return
	}

	client := h.arrClient(body.BaseURL, body.APIKey)

	status, err := client.Ping(r.Context())
	if err != nil {
		// Surface the upstream failure mode in the error message — admins
		// need to know whether they got the URL wrong vs the API key wrong.
		switch {
		case errors.Is(err, arr.ErrUnauthorized):
			respond.ValidationError(w, r, "arr instance rejected the api key")
		case errors.Is(err, arr.ErrNotFound):
			respond.ValidationError(w, r, "arr instance returned 404 — base_url is probably wrong")
		default:
			respond.ValidationError(w, r, "could not reach arr instance: "+err.Error())
		}
		return
	}

	out := arrProbeResponse{
		Status:           "ok",
		Version:          status.Version,
		AppName:          status.AppName,
		QualityProfiles:  []arr.QualityProfile{},
		RootFolders:      []arr.RootFolder{},
		Tags:             []arr.Tag{},
		LanguageProfiles: []arr.LanguageProfile{},
	}

	if profiles, err := client.QualityProfiles(r.Context()); err == nil {
		out.QualityProfiles = profiles
	} else {
		h.logger.WarnContext(r.Context(), "probe: quality profiles", "err", err)
	}
	if folders, err := client.RootFolders(r.Context()); err == nil {
		out.RootFolders = folders
	} else {
		h.logger.WarnContext(r.Context(), "probe: root folders", "err", err)
	}
	if tags, err := client.Tags(r.Context()); err == nil {
		out.Tags = tags
	} else {
		h.logger.WarnContext(r.Context(), "probe: tags", "err", err)
	}
	if body.Kind == "sonarr" {
		if langs, err := client.LanguageProfiles(r.Context()); err == nil {
			out.LanguageProfiles = langs
		} else {
			h.logger.WarnContext(r.Context(), "probe: language profiles", "err", err)
		}
	}

	respond.Success(w, r, out)
}

// ── helpers ───────────────────────────────────────────────────────────────

func (h *ArrServicesHandler) auditEvent(r *http.Request, action, target string, detail map[string]any) {
	if h.audit == nil {
		return
	}
	claims := middleware.ClaimsFromContext(r.Context())
	if claims == nil {
		return
	}
	actor := claims.UserID
	h.audit.Log(r.Context(), &actor, action, target, detail, audit.ClientIP(r))
}

func encodeArrTagIDs(ids []int32) ([]byte, error) {
	if ids == nil {
		ids = []int32{}
	}
	return json.Marshal(ids)
}

func decodeArrTagIDs(raw []byte) ([]int32, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	var out []int32
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	return out, nil
}
