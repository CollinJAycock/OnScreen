package v1

import (
	"context"
	"encoding/json"
	"net/http"
	"sort"
	"time"

	"github.com/onscreen/onscreen/internal/api/middleware"
	"github.com/onscreen/onscreen/internal/api/respond"
	"github.com/onscreen/onscreen/internal/audit"
	"github.com/onscreen/onscreen/internal/domain/settings"
	"github.com/onscreen/onscreen/internal/mediastore"
)

// secretMask is the sentinel the API returns in place of a stored secret and
// accepts back unchanged to mean "keep the existing secret" — same convention as
// the SMTP/OIDC settings.
const secretMask = "****"

// storageSettingDTO mirrors settings.StorageConfig with the secret key masked on
// read. Backend selects the implementation: "" / "local" → local filesystem,
// "s3" → S3-compatible object storage.
type storageSettingDTO struct {
	Enabled      bool              `json:"enabled"`
	Backend      string            `json:"backend"`
	Endpoint     string            `json:"endpoint"`
	Region       string            `json:"region"`
	Bucket       string            `json:"bucket"`
	AccessKey    string            `json:"access_key"`
	SecretKey    string            `json:"secret_key"` // secretMask if set, "" if empty
	UseSSL       bool              `json:"use_ssl"`
	MediaRoot    string            `json:"media_root"`
	PathPrefix   string            `json:"path_prefix"`
	CDNBaseURL   string            `json:"cdn_base_url"`
	PathMappings map[string]string `json:"path_mappings"`
}

func toStorageDTO(cfg settings.StorageConfig) storageSettingDTO {
	sk := ""
	if cfg.SecretKey != "" {
		sk = secretMask
	}
	return storageSettingDTO{
		Enabled:      cfg.Enabled,
		Backend:      cfg.Backend,
		Endpoint:     cfg.Endpoint,
		Region:       cfg.Region,
		Bucket:       cfg.Bucket,
		AccessKey:    cfg.AccessKey,
		SecretKey:    sk,
		UseSSL:       cfg.UseSSL,
		MediaRoot:    cfg.MediaRoot,
		PathPrefix:   cfg.PathPrefix,
		CDNBaseURL:   cfg.CDNBaseURL,
		PathMappings: cfg.PathMappings,
	}
}

// storageFromDTO merges a DTO onto the existing config, preserving the stored
// secret when the client echoes back the mask (the field was left untouched in
// the form).
func storageFromDTO(dto storageSettingDTO, cur settings.StorageConfig) settings.StorageConfig {
	cfg := settings.StorageConfig{
		Enabled:      dto.Enabled,
		Backend:      dto.Backend,
		Endpoint:     dto.Endpoint,
		Region:       dto.Region,
		Bucket:       dto.Bucket,
		AccessKey:    dto.AccessKey,
		UseSSL:       dto.UseSSL,
		MediaRoot:    dto.MediaRoot,
		PathPrefix:   dto.PathPrefix,
		CDNBaseURL:   dto.CDNBaseURL,
		PathMappings: dto.PathMappings,
	}
	if dto.SecretKey == secretMask {
		cfg.SecretKey = cur.SecretKey
	} else {
		cfg.SecretKey = dto.SecretKey
	}
	return cfg
}

// BuildMediaStore turns persisted storage config into a Store. A disabled config
// or a non-s3 backend yields mediastore.Local (the default), so single-node
// installs that never enable object storage get the unchanged local path. Used
// both at startup and when storage settings change.
func BuildMediaStore(cfg settings.StorageConfig) (mediastore.Store, error) {
	if !cfg.Enabled || cfg.Backend != "s3" {
		// Local backend, optionally with multi-site path remapping (DR secondary).
		return mediastore.Local{Remap: pathMappingsToRemap(cfg.PathMappings)}, nil
	}
	return mediastore.NewS3(mediastore.S3Config{
		Endpoint:   cfg.Endpoint,
		Region:     cfg.Region,
		Bucket:     cfg.Bucket,
		AccessKey:  cfg.AccessKey,
		SecretKey:  cfg.SecretKey,
		UseSSL:     cfg.UseSSL,
		MediaRoot:  cfg.MediaRoot,
		PathPrefix: cfg.PathPrefix,
		CDNBaseURL: cfg.CDNBaseURL,
	})
}

// pathMappingsToRemap converts the from→to map to ordered PathMappings, longest
// prefix first so the most specific mapping wins (a map has no inherent order).
func pathMappingsToRemap(m map[string]string) []mediastore.PathMapping {
	if len(m) == 0 {
		return nil
	}
	out := make([]mediastore.PathMapping, 0, len(m))
	for from, to := range m {
		out = append(out, mediastore.PathMapping{From: from, To: to})
	}
	sort.Slice(out, func(i, j int) bool { return len(out[i].From) > len(out[j].From) })
	return out
}

// GetStorage handles GET /settings/storage.
func (h *SettingsHandler) GetStorage(w http.ResponseWriter, r *http.Request) {
	respond.JSON(w, r, http.StatusOK, toStorageDTO(h.svc.Storage(r.Context())))
}

// UpdateStorage handles PUT /settings/storage. It persists the config, then
// rebuilds and hot-swaps the live backend so the change applies without a
// restart. A config that saves but fails to initialise (bad endpoint/creds) is
// reported as a 400 — the row is still written so the admin can correct it.
func (h *SettingsHandler) UpdateStorage(w http.ResponseWriter, r *http.Request) {
	var dto storageSettingDTO
	if err := json.NewDecoder(r.Body).Decode(&dto); err != nil {
		respond.BadRequest(w, r, "invalid request body")
		return
	}
	ctx := r.Context()
	cfg := storageFromDTO(dto, h.svc.Storage(ctx))

	if err := h.svc.SetStorage(ctx, cfg); err != nil {
		h.logger.ErrorContext(ctx, "update storage config", "err", err)
		respond.InternalError(w, r)
		return
	}

	if h.storeProvider != nil {
		store, err := BuildMediaStore(cfg)
		if err != nil {
			h.logger.ErrorContext(ctx, "build media store after update", "err", err)
			respond.BadRequest(w, r, "storage settings saved, but the backend failed to initialise: "+err.Error())
			return
		}
		h.storeProvider.Set(store)
	}

	if h.audit != nil {
		if claims := middleware.ClaimsFromContext(ctx); claims != nil {
			h.audit.Log(ctx, &claims.UserID, audit.ActionSettingsUpdate, "", map[string]any{
				"storage": "changed",
			}, audit.ClientIP(r))
		}
	}
	respond.NoContent(w)
}

type storageTestResult struct {
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
}

// TestStorage handles POST /settings/storage/test. It builds an ephemeral
// backend from the posted config (using the stored secret when the mask is
// echoed back) and pings it, without persisting. Returns 200 with {ok,error} so
// the UI can render the outcome inline rather than treating a failed credential
// check as a request error.
func (h *SettingsHandler) TestStorage(w http.ResponseWriter, r *http.Request) {
	var dto storageSettingDTO
	if err := json.NewDecoder(r.Body).Decode(&dto); err != nil {
		respond.BadRequest(w, r, "invalid request body")
		return
	}
	ctx := r.Context()
	cfg := storageFromDTO(dto, h.svc.Storage(ctx))

	store, err := BuildMediaStore(cfg)
	if err != nil {
		respond.JSON(w, r, http.StatusOK, storageTestResult{Error: err.Error()})
		return
	}

	// Only a remote backend has something to verify; Local is always reachable.
	pinger, ok := store.(interface {
		Ping(context.Context) error
	})
	if !ok {
		respond.JSON(w, r, http.StatusOK, storageTestResult{OK: true})
		return
	}

	pctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := pinger.Ping(pctx); err != nil {
		respond.JSON(w, r, http.StatusOK, storageTestResult{Error: err.Error()})
		return
	}
	respond.JSON(w, r, http.StatusOK, storageTestResult{OK: true})
}
