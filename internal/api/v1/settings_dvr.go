package v1

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/onscreen/onscreen/internal/api/middleware"
	"github.com/onscreen/onscreen/internal/api/respond"
	"github.com/onscreen/onscreen/internal/audit"
	"github.com/onscreen/onscreen/internal/domain/settings"
)

// DVRSettingsStore reads and writes the DVR post-processing settings.
// *settings.Service satisfies it.
type DVRSettingsStore interface {
	DVR(ctx context.Context) settings.DVRConfig
	SetDVR(ctx context.Context, cfg settings.DVRConfig) error
}

// DVREnvInfo is the environment-only half of DVR post-processing, reported
// read-only. These are executable paths: they can't be set through the API.
type DVREnvInfo struct {
	ScriptPath  string // ONSCREEN_DVR_POSTPROCESS_SCRIPT
	ComskipPath string // ONSCREEN_COMSKIP_PATH
	ComskipINI  string // ONSCREEN_COMSKIP_INI
}

// SetDVRSettings enables GET/PUT /settings/dvr. Returns the handler for
// chaining.
func (h *SettingsHandler) SetDVRSettings(store DVRSettingsStore, env DVREnvInfo) *SettingsHandler {
	h.dvr = store
	h.dvrEnv = env
	return h
}

// dvrSettingsDTO is the GET shape. The *_configured / script_path /
// commercial_detector fields are read-only (ignored on PUT).
type dvrSettingsDTO struct {
	Remux            bool   `json:"remux"`
	RemuxContainer   string `json:"remux_container"`
	Transcode        bool   `json:"transcode"`
	TranscodeCodec   string `json:"transcode_codec"`
	TranscodeQuality string `json:"transcode_quality"`
	DeleteOriginal   bool   `json:"delete_original"`
	Commercials      string `json:"commercials"`

	ScriptConfigured   bool   `json:"script_configured"`
	ScriptPath         string `json:"script_path,omitempty"`
	ComskipConfigured  bool   `json:"comskip_configured"`
	CommercialDetector string `json:"commercial_detector"` // "comskip" | "builtin"
}

// GetDVR handles GET /settings/dvr.
func (h *SettingsHandler) GetDVR(w http.ResponseWriter, r *http.Request) {
	if h.dvr == nil {
		respond.NotFound(w, r)
		return
	}
	cfg := h.dvr.DVR(r.Context())
	detector := "builtin"
	if h.dvrEnv.ComskipPath != "" {
		detector = "comskip"
	}
	respond.Success(w, r, dvrSettingsDTO{
		Remux:              cfg.Remux,
		RemuxContainer:     cfg.RemuxContainer,
		Transcode:          cfg.Transcode,
		TranscodeCodec:     cfg.TranscodeCodec,
		TranscodeQuality:   cfg.TranscodeQuality,
		DeleteOriginal:     cfg.DeleteOriginal,
		Commercials:        cfg.Commercials,
		ScriptConfigured:   h.dvrEnv.ScriptPath != "",
		ScriptPath:         h.dvrEnv.ScriptPath,
		ComskipConfigured:  h.dvrEnv.ComskipPath != "",
		CommercialDetector: detector,
	})
}

// UpdateDVR handles PUT /settings/dvr. Takes effect for the next recording
// that finishes — no restart. Executable paths are not accepted here.
func (h *SettingsHandler) UpdateDVR(w http.ResponseWriter, r *http.Request) {
	if h.dvr == nil {
		respond.NotFound(w, r)
		return
	}
	var dto dvrSettingsDTO
	if err := json.NewDecoder(r.Body).Decode(&dto); err != nil {
		respond.BadRequest(w, r, "invalid request body")
		return
	}
	cfg := settings.DVRConfig{
		Remux:            dto.Remux,
		RemuxContainer:   dto.RemuxContainer,
		Transcode:        dto.Transcode,
		TranscodeCodec:   dto.TranscodeCodec,
		TranscodeQuality: dto.TranscodeQuality,
		DeleteOriginal:   dto.DeleteOriginal,
		Commercials:      dto.Commercials,
	}
	ctx := r.Context()
	if err := h.dvr.SetDVR(ctx, cfg); err != nil {
		if errors.Is(err, settings.ErrInvalidDVRConfig) {
			respond.BadRequest(w, r, err.Error())
			return
		}
		h.logger.ErrorContext(ctx, "update dvr config", "err", err)
		respond.InternalError(w, r)
		return
	}
	if h.audit != nil {
		if claims := middleware.ClaimsFromContext(ctx); claims != nil {
			saved := cfg.WithDefaults()
			h.audit.Log(ctx, &claims.UserID, audit.ActionSettingsUpdate, "", map[string]any{
				"dvr_remux":           saved.Remux,
				"dvr_transcode":       saved.Transcode,
				"dvr_delete_original": saved.DeleteOriginal,
				"dvr_commercials":     saved.Commercials,
			}, audit.ClientIP(r))
		}
	}
	respond.NoContent(w)
}
