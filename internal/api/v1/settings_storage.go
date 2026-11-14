package v1

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/onscreen/onscreen/internal/api/middleware"
	"github.com/onscreen/onscreen/internal/api/respond"
	"github.com/onscreen/onscreen/internal/audit"
	"github.com/onscreen/onscreen/internal/domain/settings"
	"github.com/onscreen/onscreen/internal/i18n"
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

// StorageLibraryRoots lists the root folders of every library. A storage path
// mapping's From has to be one of them, inside one, or a folder above one; see
// validatePathMappings. Satisfied by cmd/server's adapter over the library
// service.
type StorageLibraryRoots interface {
	LibraryRootPaths(ctx context.Context) ([]string, error)
}

// SetStorageLibraryRoots wires the library roots storage path mappings are
// checked against. Without it no mapping can be saved (fail closed). Returns
// the handler for chaining.
func (h *SettingsHandler) SetStorageLibraryRoots(lr StorageLibraryRoots) *SettingsHandler {
	h.storageRoots = lr
	return h
}

// pathMappingError is a storage path mapping refused on save: the catalog key
// naming the rule it broke, and the values its message shows.
type pathMappingError struct {
	key    string
	params i18n.Params
}

func (e *pathMappingError) Error() string { return "storage path mapping refused: " + e.key }

// errMappingPathDotDot / errMappingPathRelative are cleanMappingPath's two
// refusals.
var (
	errMappingPathRelative = errors.New("path is not absolute")
	errMappingPathDotDot   = errors.New("path contains a .. element")
)

// cleanMappingPath normalises one side of a path mapping: it must be absolute
// in POSIX form (/mnt/media) or Windows form (D:\media, \\nas\share) — a
// replicated database can carry the other OS's paths — and must not contain a
// ".." element. Empty and "." elements are dropped and the trailing separator
// removed; a Windows path is rewritten with backslashes, the separator the
// scanner records there. isRoot reports a filesystem root ("/", "D:\").
func cleanMappingPath(p string) (cleaned string, isRoot bool, err error) {
	p = strings.TrimSpace(p)
	var prefix, sep string
	switch {
	case strings.HasPrefix(p, `\\`):
		prefix, sep, p = `\\`, `\`, p[2:]
	case len(p) >= 3 && p[1] == ':' && (p[2] == '\\' || p[2] == '/') &&
		((p[0] >= 'A' && p[0] <= 'Z') || (p[0] >= 'a' && p[0] <= 'z')):
		prefix, sep, p = p[:2]+`\`, `\`, p[3:]
	case strings.HasPrefix(p, "/"):
		prefix, sep, p = "/", "/", p[1:]
	default:
		return "", false, errMappingPathRelative
	}
	var elems []string
	for _, e := range strings.FieldsFunc(p, func(r rune) bool { return r == '/' || r == '\\' }) {
		switch e {
		case ".":
			continue
		case "..":
			return "", false, errMappingPathDotDot
		}
		elems = append(elems, e)
	}
	if prefix == `\\` && len(elems) < 2 {
		// A UNC path needs at least \\server\share.
		return "", false, errMappingPathRelative
	}
	return prefix + strings.Join(elems, sep), len(elems) == 0, nil
}

// pathAtOrUnder reports whether child is parent or lies inside it, matching
// whole path elements: /media/a is not under /media/ab.
func pathAtOrUnder(child, parent string) bool {
	if child == parent {
		return true
	}
	if !strings.HasPrefix(child, parent) {
		return false
	}
	if strings.HasSuffix(parent, "/") || strings.HasSuffix(parent, `\`) {
		return true // parent is a root
	}
	c := child[len(parent)]
	return c == '/' || c == '\\'
}

// validatePathMappings checks the multi-site path mappings before they are
// saved and returns them normalised (see cleanMappingPath).
//
// A mapping rewrites the paths the database records before the server opens,
// writes or deletes the file — for every library, with or without object
// storage. Unvalidated, it was a literal string prefix an admin session could
// point anywhere: map one item's file path to the server's .env and stream
// that item to read SECRET_KEY and the database password, a secret that
// elsewhere needs password + TOTP step-up to reveal; podcast downloads and
// deletes reach writes and deletes through the same rewrite. So:
//
//   - both sides absolute, without "..", folder-to-folder: a side that exists
//     on this server must be a directory, and mediastore.Local only rewrites
//     a path strictly inside From, matching whole path elements, so a file
//     path can't be mapped onto another file;
//   - From must be a library root, a folder inside one, or a folder above one
//     (the primary site's mount above its library folders, as in the DR
//     runbook) — never the filesystem root. Library roots come from the
//     replicated database, so they are the primary site's paths, which is
//     exactly what From names.
func (h *SettingsHandler) validatePathMappings(ctx context.Context, in map[string]string) (map[string]string, error) {
	if len(in) == 0 {
		return in, nil
	}
	var roots []string
	if h.storageRoots != nil {
		raw, err := h.storageRoots.LibraryRootPaths(ctx)
		if err != nil {
			return nil, err
		}
		for _, r := range raw {
			if c, _, err := cleanMappingPath(r); err == nil {
				roots = append(roots, c)
			}
		}
	}
	out := make(map[string]string, len(in))
	for rawFrom, rawTo := range in {
		params := i18n.Params{"from": rawFrom, "to": rawTo}
		from, fromRoot, ferr := cleanMappingPath(rawFrom)
		to, _, terr := cleanMappingPath(rawTo)
		switch {
		case errors.Is(ferr, errMappingPathDotDot), errors.Is(terr, errMappingPathDotDot):
			return nil, &pathMappingError{"error.storage.mappingDotDot", params}
		case ferr != nil, terr != nil:
			return nil, &pathMappingError{"error.storage.mappingNotAbsolute", params}
		case fromRoot:
			return nil, &pathMappingError{"error.storage.mappingFromRoot", params}
		}
		related := false
		for _, root := range roots {
			if pathAtOrUnder(from, root) || pathAtOrUnder(root, from) {
				related = true
				break
			}
		}
		if !related {
			return nil, &pathMappingError{"error.storage.mappingOutsideLibraries", params}
		}
		for _, side := range []string{from, to} {
			// A side that exists on this server must be a folder. One this
			// server can't see — the other site's mount, an unreadable path —
			// isn't refused: the mapping is often saved at the site that
			// doesn't have it.
			if fi, err := os.Stat(side); err == nil && !fi.IsDir() {
				params["path"] = side
				return nil, &pathMappingError{"error.storage.mappingNotFolder", params}
			}
		}
		out[from] = to
	}
	return out, nil
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

	mappings, err := h.validatePathMappings(ctx, cfg.PathMappings)
	if err != nil {
		var pmErr *pathMappingError
		if errors.As(err, &pmErr) {
			// 422 with its own code so the storage page can show it next to
			// the mappings; nothing is written.
			respond.Fail(w, r, http.StatusUnprocessableEntity, "PATH_MAPPING_INVALID", pmErr.key, pmErr.params)
			return
		}
		h.logger.ErrorContext(ctx, "validate storage path mappings", "err", err)
		respond.InternalError(w, r)
		return
	}
	cfg.PathMappings = mappings

	if err := h.svc.SetStorage(ctx, cfg); err != nil {
		h.logger.ErrorContext(ctx, "update storage config", "err", err)
		respond.InternalError(w, r)
		return
	}

	if h.storeProvider != nil {
		store, err := BuildMediaStore(cfg)
		if err != nil {
			h.logger.ErrorContext(ctx, "build media store after update", "err", err)
			respond.BadRequestT(w, r, "error.storage.initFailed", i18n.Params{"detail": err.Error()})
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
