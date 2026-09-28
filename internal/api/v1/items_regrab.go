package v1

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/onscreen/onscreen/internal/api/middleware"
	"github.com/onscreen/onscreen/internal/api/respond"
	"github.com/onscreen/onscreen/internal/arr"
	"github.com/onscreen/onscreen/internal/arrcrypt"
	"github.com/onscreen/onscreen/internal/audit"
	"github.com/onscreen/onscreen/internal/db/gen"
)

// regrabTimeout bounds one instance's lookup + blocklist + search round trip.
const regrabTimeout = 20 * time.Second

type regrabRequest struct {
	// Blocklist marks the release the *arr app last grabbed for the item as
	// failed before searching, so the search can't pick it again.
	Blocklist bool `json:"blocklist"`
}

// RegrabResponse is POST /admin/items/{id}/regrab's reply.
type RegrabResponse struct {
	ServiceID   string `json:"service_id"`
	ServiceName string `json:"service_name"`
	ServiceKind string `json:"service_kind"`
	arr.RegrabResult
}

// errRegrabUnsupported: the item type has no *arr counterpart.
var errRegrabUnsupported = errors.New("re-grab is only available for movies, shows, seasons and episodes")

// regrabNotManaged is a 409 NOT_MANAGED explanation.
type regrabNotManaged struct{ msg string }

func (e regrabNotManaged) Error() string { return e.msg }

// Regrab handles POST /api/v1/admin/items/{id}/regrab {"blocklist": bool}.
//
// The owning instance is found by asking each enabled instance of the right
// kind (Radarr for a movie, Sonarr for a show / season / episode — the default
// instance first) whether it manages the title; the first that does gets the
// search. Answers 409 NOT_MANAGED when none does, 502 ARR_UNAVAILABLE when an
// instance couldn't be reached or refused the command.
func (h *IssueHandler) Regrab(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if !issuesRequireAdmin(w, r) {
		return
	}
	claims := middleware.ClaimsFromContext(ctx)
	itemID, err := parseUUID(r, "id")
	if err != nil {
		respond.BadRequest(w, r, "invalid item id")
		return
	}
	var body regrabRequest
	if !decodeIssueBody(w, r, &body) {
		return
	}
	item, err := h.db.GetMediaItem(ctx, itemID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			respond.NotFound(w, r)
			return
		}
		h.logger.ErrorContext(ctx, "regrab: get item", "item_id", itemID, "err", err)
		respond.InternalError(w, r)
		return
	}

	kind, target, err := h.regrabTarget(ctx, item)
	if err != nil {
		var nm regrabNotManaged
		switch {
		case errors.Is(err, errRegrabUnsupported):
			respond.ValidationError(w, r, err.Error())
		case errors.As(err, &nm):
			respond.Error(w, r, http.StatusConflict, "NOT_MANAGED", nm.msg)
		default:
			h.logger.ErrorContext(ctx, "regrab: resolve target", "item_id", itemID, "err", err)
			respond.InternalError(w, r)
		}
		return
	}

	svcs, err := h.db.ListEnabledArrServicesByKind(ctx, kind)
	if err != nil {
		h.logger.ErrorContext(ctx, "regrab: list arr services", "kind", kind, "err", err)
		respond.InternalError(w, r)
		return
	}
	appName := "Radarr"
	if kind == "sonarr" {
		appName = "Sonarr"
	}
	if len(svcs) == 0 {
		respond.Error(w, r, http.StatusConflict, "NOT_MANAGED", "no enabled "+appName+" instance is configured")
		return
	}

	var lookupFailed bool
	for _, svc := range svcs {
		res, err := h.regrabOn(ctx, svc, target, body.Blocklist)
		switch {
		case err == nil:
			if h.audit != nil {
				actor := claims.UserID
				h.audit.Log(ctx, &actor, audit.ActionItemRegrab, "item:"+item.ID.String(), map[string]any{
					"service_id":  svc.ID.String(),
					"service":     svc.Name,
					"command":     res.Command,
					"arr_id":      res.ArrID,
					"blocklist":   body.Blocklist,
					"blocklisted": res.Blocklisted,
				}, audit.ClientIP(r))
			}
			respond.Success(w, r, RegrabResponse{
				ServiceID:    svc.ID.String(),
				ServiceName:  svc.Name,
				ServiceKind:  svc.Kind,
				RegrabResult: *res,
			})
			return
		case errors.Is(err, arr.ErrNotManaged):
			continue
		case errors.Is(err, arr.ErrRegrabAction):
			// This instance manages the title; don't send a second search
			// to another one.
			h.logger.WarnContext(ctx, "regrab: arr command failed",
				"item_id", item.ID, "service", svc.Name, "service_id", svc.ID, "err", err)
			respond.Error(w, r, http.StatusBadGateway, "ARR_UNAVAILABLE",
				svc.Name+" manages this title but the re-grab failed; check the server log")
			return
		default:
			// Upstream detail can carry the base URL: server log only.
			h.logger.WarnContext(ctx, "regrab: arr lookup failed",
				"item_id", item.ID, "service", svc.Name, "service_id", svc.ID, "err", err)
			lookupFailed = true
		}
	}
	if lookupFailed {
		respond.Error(w, r, http.StatusBadGateway, "ARR_UNAVAILABLE",
			"could not reach every "+appName+" instance; check the server log")
		return
	}
	respond.Error(w, r, http.StatusConflict, "NOT_MANAGED", "no "+appName+" instance manages this title")
}

// regrabOn runs the re-grab against one instance.
func (h *IssueHandler) regrabOn(ctx context.Context, svc gen.ArrService, target arr.RegrabTarget, blocklist bool) (*arr.RegrabResult, error) {
	apiKey, err := arrcrypt.Open(h.enc, svc.ID, svc.ApiKey)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, regrabTimeout)
	defer cancel()
	return h.arrClient(svc.BaseUrl, apiKey).Regrab(ctx, target, blocklist)
}

// regrabTarget maps an item to the *arr kind and search target. Episodes and
// seasons take the show's provider ids and their own season / episode numbers
// from the hierarchy.
func (h *IssueHandler) regrabTarget(ctx context.Context, item gen.GetMediaItemRow) (string, arr.RegrabTarget, error) {
	ids := func(it gen.GetMediaItemRow) (tmdb, tvdb int) {
		if it.TmdbID != nil {
			tmdb = int(*it.TmdbID)
		}
		if it.TvdbID != nil {
			tvdb = int(*it.TvdbID)
		}
		return tmdb, tvdb
	}
	parentOf := func(it gen.GetMediaItemRow) (gen.GetMediaItemRow, error) {
		if !it.ParentID.Valid {
			return gen.GetMediaItemRow{}, regrabNotManaged{msg: it.Type + " has no parent show"}
		}
		p, err := h.db.GetMediaItem(ctx, uuid.UUID(it.ParentID.Bytes))
		if errors.Is(err, pgx.ErrNoRows) {
			return gen.GetMediaItemRow{}, regrabNotManaged{msg: it.Type + " has no parent show"}
		}
		return p, err
	}

	switch item.Type {
	case "movie":
		tmdb, _ := ids(item)
		if tmdb == 0 {
			return "", arr.RegrabTarget{}, regrabNotManaged{msg: "movie has no TMDB id; fix its match first"}
		}
		return "radarr", arr.RegrabTarget{Scope: arr.RegrabMovie, TMDBID: tmdb}, nil
	case "show":
		tmdb, tvdb := ids(item)
		if tmdb == 0 && tvdb == 0 {
			return "", arr.RegrabTarget{}, regrabNotManaged{msg: "show has no TVDB or TMDB id; fix its match first"}
		}
		return "sonarr", arr.RegrabTarget{Scope: arr.RegrabSeries, TMDBID: tmdb, TVDBID: tvdb}, nil
	case "season":
		if item.Index == nil {
			return "", arr.RegrabTarget{}, regrabNotManaged{msg: "season has no season number"}
		}
		show, err := parentOf(item)
		if err != nil {
			return "", arr.RegrabTarget{}, err
		}
		tmdb, tvdb := ids(show)
		if tmdb == 0 && tvdb == 0 {
			return "", arr.RegrabTarget{}, regrabNotManaged{msg: "show has no TVDB or TMDB id; fix its match first"}
		}
		return "sonarr", arr.RegrabTarget{Scope: arr.RegrabSeason, TMDBID: tmdb, TVDBID: tvdb, SeasonNumber: int(*item.Index)}, nil
	case "episode":
		if item.Index == nil {
			return "", arr.RegrabTarget{}, regrabNotManaged{msg: "episode has no episode number"}
		}
		season, err := parentOf(item)
		if err != nil {
			return "", arr.RegrabTarget{}, err
		}
		if season.Type != "season" || season.Index == nil {
			return "", arr.RegrabTarget{}, regrabNotManaged{msg: "episode has no season number"}
		}
		show, err := parentOf(season)
		if err != nil {
			return "", arr.RegrabTarget{}, err
		}
		tmdb, tvdb := ids(show)
		if tmdb == 0 && tvdb == 0 {
			return "", arr.RegrabTarget{}, regrabNotManaged{msg: "show has no TVDB or TMDB id; fix its match first"}
		}
		return "sonarr", arr.RegrabTarget{
			Scope: arr.RegrabEpisode, TMDBID: tmdb, TVDBID: tvdb,
			SeasonNumber: int(*season.Index), EpisodeNumber: int(*item.Index),
		}, nil
	default:
		return "", arr.RegrabTarget{}, errRegrabUnsupported
	}
}
