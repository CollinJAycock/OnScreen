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
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/onscreen/onscreen/internal/api/middleware"
	"github.com/onscreen/onscreen/internal/api/respond"
	"github.com/onscreen/onscreen/internal/audit"
	"github.com/onscreen/onscreen/internal/db/gen"
	"github.com/onscreen/onscreen/internal/domain/settings"
	"github.com/onscreen/onscreen/internal/subtitles"
)

// SubtitleAutoSettings reads and writes the server-wide automatic subtitle
// download setting. Satisfied by *settings.Service.
type SubtitleAutoSettings interface {
	SubtitleAutoDownload(ctx context.Context) settings.SubtitleAutoDownloadConfig
	SetSubtitleAutoDownload(ctx context.Context, cfg settings.SubtitleAutoDownloadConfig) error
}

// SubtitleAutoLibraryDB is the per-library override store. Satisfied by
// *gen.Queries.
type SubtitleAutoLibraryDB interface {
	GetLibrarySubtitleAutoDownload(ctx context.Context, libraryID uuid.UUID) (gen.LibrarySubtitleAutoDownload, error)
	UpsertLibrarySubtitleAutoDownload(ctx context.Context, arg gen.UpsertLibrarySubtitleAutoDownloadParams) (gen.LibrarySubtitleAutoDownload, error)
	DeleteLibrarySubtitleAutoDownload(ctx context.Context, libraryID uuid.UUID) error
}

// SubtitleAutoHandler serves the admin settings of the automatic subtitle
// download (the subtitle_auto_download task):
//
//	GET/PUT /api/v1/settings/subtitle-auto-download       server-wide
//	GET/PUT /api/v1/libraries/{id}/subtitle-auto-download per-library override
type SubtitleAutoHandler struct {
	settings SubtitleAutoSettings
	db       SubtitleAutoLibraryDB
	// providerConfigured reports whether OpenSubtitles is set up; the task
	// does nothing without it and the page says so.
	providerConfigured func() bool
	audit              *audit.Logger
	logger             *slog.Logger
}

// NewSubtitleAutoHandler creates a SubtitleAutoHandler.
func NewSubtitleAutoHandler(s SubtitleAutoSettings, db SubtitleAutoLibraryDB, providerConfigured func() bool, logger *slog.Logger) *SubtitleAutoHandler {
	return &SubtitleAutoHandler{settings: s, db: db, providerConfigured: providerConfigured, logger: logger}
}

// WithAudit attaches an audit logger. Returns the handler for chaining.
func (h *SubtitleAutoHandler) WithAudit(a *audit.Logger) *SubtitleAutoHandler {
	h.audit = a
	return h
}

// subtitleAutoSettingsJSON is the server-wide setting. only_missing is always
// true (a language the file already has is never downloaded again) and is
// reported so clients needn't hard-code it. provider_configured is read-only.
type subtitleAutoSettingsJSON struct {
	Enabled            bool   `json:"enabled"`
	Languages          string `json:"languages"`
	OnlyMissing        bool   `json:"only_missing"`
	PreferHashMatch    bool   `json:"prefer_hash_match"`
	HearingImpaired    string `json:"hearing_impaired"`
	ProviderConfigured bool   `json:"provider_configured"`
}

func (h *SubtitleAutoHandler) settingsJSON(cfg settings.SubtitleAutoDownloadConfig) subtitleAutoSettingsJSON {
	return subtitleAutoSettingsJSON{
		Enabled:            cfg.Enabled,
		Languages:          cfg.Languages,
		OnlyMissing:        true,
		PreferHashMatch:    cfg.PreferHashMatch,
		HearingImpaired:    cfg.HearingImpaired,
		ProviderConfigured: h.providerConfigured != nil && h.providerConfigured(),
	}
}

// GetSettings handles GET /api/v1/settings/subtitle-auto-download.
func (h *SubtitleAutoHandler) GetSettings(w http.ResponseWriter, r *http.Request) {
	respond.Success(w, r, h.settingsJSON(h.settings.SubtitleAutoDownload(r.Context())))
}

// updateSubtitleAutoRequest: every field optional; an omitted one keeps its
// stored value.
type updateSubtitleAutoRequest struct {
	Enabled         *bool   `json:"enabled"`
	Languages       *string `json:"languages"`
	PreferHashMatch *bool   `json:"prefer_hash_match"`
	HearingImpaired *string `json:"hearing_impaired"`
}

// UpdateSettings handles PUT /api/v1/settings/subtitle-auto-download.
func (h *SubtitleAutoHandler) UpdateSettings(w http.ResponseWriter, r *http.Request) {
	var body updateSubtitleAutoRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		respond.BadRequest(w, r, "invalid request body")
		return
	}
	ctx := r.Context()
	cfg := h.settings.SubtitleAutoDownload(ctx)
	if body.Enabled != nil {
		cfg.Enabled = *body.Enabled
	}
	if body.Languages != nil {
		langs := subtitles.ParseLanguageList(*body.Languages)
		if strings.TrimSpace(*body.Languages) != "" && len(langs) == 0 {
			respond.ValidationError(w, r, "languages must be a comma-separated list of language codes, e.g. en,es")
			return
		}
		cfg.Languages = strings.Join(langs, ",")
	}
	if body.PreferHashMatch != nil {
		cfg.PreferHashMatch = *body.PreferHashMatch
	}
	if body.HearingImpaired != nil {
		cfg.HearingImpaired = *body.HearingImpaired
	}
	if cfg.Enabled && cfg.Languages == "" {
		respond.ValidationError(w, r, "choose at least one language to download subtitles in")
		return
	}
	if err := h.settings.SetSubtitleAutoDownload(ctx, cfg); err != nil {
		if errors.Is(err, settings.ErrInvalidSetting) {
			respond.ValidationError(w, r, `hearing_impaired must be "prefer", "avoid" or "any"`)
			return
		}
		h.logger.ErrorContext(ctx, "save subtitle auto-download settings", "err", err)
		respond.InternalError(w, r)
		return
	}
	if h.audit != nil {
		if claims := middleware.ClaimsFromContext(ctx); claims != nil {
			h.audit.Log(ctx, &claims.UserID, audit.ActionSettingsUpdate, "", map[string]any{
				"subtitle_auto_download": map[string]any{
					"enabled":           cfg.Enabled,
					"languages":         cfg.Languages,
					"prefer_hash_match": cfg.PreferHashMatch,
					"hearing_impaired":  cfg.HearingImpaired,
				},
			}, audit.ClientIP(r))
		}
	}
	respond.Success(w, r, h.settingsJSON(cfg))
}

// librarySubtitleAutoJSON is one library's override. null = follow the
// server-wide setting.
type librarySubtitleAutoJSON struct {
	LibraryID string  `json:"library_id"`
	Enabled   *bool   `json:"enabled"`
	Languages *string `json:"languages"`
}

// GetLibrary handles GET /api/v1/libraries/{id}/subtitle-auto-download.
func (h *SubtitleAutoHandler) GetLibrary(w http.ResponseWriter, r *http.Request) {
	id, err := parseUUID(r, "id")
	if err != nil {
		respond.BadRequest(w, r, "invalid library id")
		return
	}
	row, err := h.db.GetLibrarySubtitleAutoDownload(r.Context(), id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			respond.Success(w, r, librarySubtitleAutoJSON{LibraryID: id.String()})
			return
		}
		h.logger.ErrorContext(r.Context(), "get library subtitle auto-download", "library_id", id, "err", err)
		respond.InternalError(w, r)
		return
	}
	respond.Success(w, r, librarySubtitleAutoJSON{LibraryID: id.String(), Enabled: row.Enabled, Languages: row.Languages})
}

// UpdateLibrary handles PUT /api/v1/libraries/{id}/subtitle-auto-download.
// Body {enabled: bool|null, languages: string|null}; both null removes the
// override.
func (h *SubtitleAutoHandler) UpdateLibrary(w http.ResponseWriter, r *http.Request) {
	id, err := parseUUID(r, "id")
	if err != nil {
		respond.BadRequest(w, r, "invalid library id")
		return
	}
	var body struct {
		Enabled   *bool   `json:"enabled"`
		Languages *string `json:"languages"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		respond.BadRequest(w, r, "invalid request body")
		return
	}
	if body.Languages != nil {
		langs := subtitles.ParseLanguageList(*body.Languages)
		if len(langs) == 0 {
			if strings.TrimSpace(*body.Languages) != "" {
				respond.ValidationError(w, r, "languages must be a comma-separated list of language codes, e.g. en,es")
				return
			}
			body.Languages = nil // "" = follow the server's languages
		} else {
			joined := strings.Join(langs, ",")
			body.Languages = &joined
		}
	}
	ctx := r.Context()
	out := librarySubtitleAutoJSON{LibraryID: id.String(), Enabled: body.Enabled, Languages: body.Languages}
	if body.Enabled == nil && body.Languages == nil {
		if err := h.db.DeleteLibrarySubtitleAutoDownload(ctx, id); err != nil {
			h.logger.ErrorContext(ctx, "clear library subtitle auto-download", "library_id", id, "err", err)
			respond.InternalError(w, r)
			return
		}
	} else if _, err := h.db.UpsertLibrarySubtitleAutoDownload(ctx, gen.UpsertLibrarySubtitleAutoDownloadParams{
		LibraryID: id, Enabled: body.Enabled, Languages: body.Languages,
	}); err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23503" { // no such library
			respond.NotFound(w, r)
			return
		}
		h.logger.ErrorContext(ctx, "save library subtitle auto-download", "library_id", id, "err", err)
		respond.InternalError(w, r)
		return
	}
	if h.audit != nil {
		if claims := middleware.ClaimsFromContext(ctx); claims != nil {
			h.audit.Log(ctx, &claims.UserID, audit.ActionSettingsUpdate, id.String(), map[string]any{
				"library_subtitle_auto_download": map[string]any{
					"enabled":   body.Enabled,
					"languages": body.Languages,
				},
			}, audit.ClientIP(r))
		}
	}
	respond.Success(w, r, out)
}
