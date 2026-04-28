// Package api wires the HTTP router for the OnScreen API server.
package api

import (
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/riandyrn/otelchi"

	"github.com/onscreen/onscreen/internal/api/middleware"
	v1 "github.com/onscreen/onscreen/internal/api/v1"
	"github.com/onscreen/onscreen/internal/artwork"
	"github.com/onscreen/onscreen/internal/observability"
	"github.com/onscreen/onscreen/internal/streaming"
	"github.com/onscreen/onscreen/internal/valkey"
	"github.com/onscreen/onscreen/internal/webui"
)

// ArtworkRoot pairs a library's UUID with its scan paths so the artwork
// handler can check per-library ACL before serving. An authenticated
// user without access to the library that owns the resolved file gets
// a 404 (obfuscates existence vs. distinguishing "file not found" from
// "not allowed to see this file").
type ArtworkRoot struct {
	LibraryID uuid.UUID
	Paths     []string
}

// Handlers groups all handler dependencies.
type Handlers struct {
	Library         *v1.LibraryHandler
	Webhook         *v1.WebhookHandler
	Auth            *v1.AuthHandler
	User            *v1.UserHandler
	FS              *v1.FSHandler
	Settings        *v1.SettingsHandler
	Analytics       *v1.AnalyticsHandler
	NativeSessions  *v1.NativeSessionsHandler
	Hub             *v1.HubHandler
	Search          *v1.SearchHandler
	History         *v1.HistoryHandler
	Items           *v1.ItemHandler
	Photos          *v1.PhotosHandler
	Books           *v1.BookHandler
	Trickplay       *v1.TrickplayHandler
	NativeTranscode *v1.NativeTranscodeHandler
	Collections     *v1.CollectionHandler
	Playlists       *v1.PlaylistHandler
	PhotoAlbums     *v1.PhotoAlbumHandler
	LiveTV          *v1.LiveTVHandler
	Lyrics          *v1.LyricsHandler
	Subtitles       *v1.SubtitleHandler
	Arr             *v1.ArrHandler  // incoming arr app notifications
	OIDCAuth        *v1.OIDCHandler // settings-driven, always non-nil
	SAMLAuth        *v1.SAMLHandler // settings-driven, always non-nil
	LDAPAuth        *v1.LDAPHandler // settings-driven, always non-nil
	Audit           *v1.AuditHandler
	Email           *v1.EmailHandler
	PasswordReset   *v1.PasswordResetHandler
	Invite          *v1.InviteHandler
	Notifications   *v1.NotificationHandler
	Favorites       *v1.FavoritesHandler
	Maintenance     *v1.MaintenanceHandler
	Backup          *v1.BackupHandler
	Tasks           *v1.TasksHandler
	People          *v1.PeopleHandler
	Plugins         *v1.PluginHandler
	Pair            *v1.PairHandler
	Logs            *v1.LogsHandler
	Capabilities    *v1.CapabilitiesHandler
	ArrServices     *v1.ArrServicesHandler // outbound arr instance CRUD (admin)
	Requests        *v1.RequestHandler     // user + admin request workflow
	Discover        *v1.DiscoverHandler    // TMDB-backed search for the request UI
	StreamTracker   *streaming.Tracker
	Artwork         *artwork.Manager
	ArtworkRoots    func() []ArtworkRoot             // per-library scan_paths for ACL-aware artwork serving
	LibraryAccess   v1.LibraryAccessChecker          // ACL for artwork; nil = bypass (dev setups)
	Logger          *slog.Logger
	Metrics         *observability.Metrics
	Auth_mw         *middleware.Authenticator
	// Impersonate is the lookup the view-as middleware uses to swap an
	// admin's claims for a target user's on read-only requests. Nil
	// disables the feature (the middleware degrades to a pass-through
	// since it short-circuits on missing param anyway, but skipping
	// the Use() call avoids the dead lookup wiring on dev setups).
	Impersonate middleware.ImpersonationLookup
	RateLimiter *valkey.RateLimiter
	// CORSAllowedOrigins enables cross-origin API access (TV app, third-party
	// native clients). Empty disables CORS — same-origin only.
	CORSAllowedOrigins []string
}

// NewRouter builds the full Chi router.
func NewRouter(h *Handlers) http.Handler {
	r := chi.NewRouter()

	// Outermost middleware: wraps every request in a server span. Placed first
	// so the span covers all other middleware (auth, rate limit, etc.).
	// WithChiRoutes resolves the chi route template at span-time, so span names
	// like "GET /items/{id}" aggregate correctly instead of exploding by UUID.
	// When no tracer provider is registered, spans are no-op (near-zero cost).
	r.Use(otelchi.Middleware("onscreen", otelchi.WithChiRoutes(r)))

	// Global middleware (applied to all routes). TrustedRealIP rewrites
	// RemoteAddr from X-Forwarded-* only when the immediate peer is private,
	// preventing public clients from spoofing audit-log / rate-limit IPs.
	r.Use(middleware.TrustedRealIP)
	r.Use(middleware.RequestID)
	r.Use(middleware.SecurityHeaders)
	r.Use(middleware.CORS(h.CORSAllowedOrigins))
	r.Use(middleware.Recover(h.Logger))
	r.Use(middleware.Logger(h.Logger))

	// ── Health endpoints ──────────────────────────────────────────────────────
	// These are on the main port for simplicity; the metrics port serves /metrics.
	r.Get("/health/live", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	})
	// /health/ready is registered by the server after checking deps.

	// ── Media stream + subtitle endpoints ─────────────────────────────────────
	// Serves a media file by its DB UUID for direct play, plus extracted/embedded
	// and external VTT subtitles. Browser <video>/<track> can't attach
	// Authorization headers, so the auth flow accepts three carriers:
	// Bearer (programmatic clients), cookie (browser same-origin), or
	// `?token=<paseto>` query (native cross-origin where neither cookie
	// nor Bearer header is reachable from a media element).
	// RequiredAllowQueryToken's per-route documentation calls out the
	// log/Referer leak trade-off — asset routes only.
	r.Group(func(r chi.Router) {
		r.Use(h.Auth_mw.RequiredAllowQueryToken)
		if h.Items != nil {
			r.Get("/media/stream/{id}", h.Items.StreamFile)
			r.Get("/media/subtitles/{fileId}/{streamIndex}", h.Items.ServeSubtitle)
		}
		if h.Subtitles != nil {
			r.Get("/media/external-subtitles/{subId}", h.Subtitles.Serve)
		}
	})

	// Native HLS playlist + segment endpoints — auth via segment token in query param,
	// not Bearer header, because HLS.js cannot attach arbitrary headers to segment fetches.
	if h.NativeTranscode != nil {
		r.Get("/api/v1/transcode/sessions/{sid}/playlist.m3u8", h.NativeTranscode.Playlist)
		// DASH manifest for the same session, sharing the segment ladder.
		// fMP4 sessions only — non-fMP4 sessions return 415 with a hint
		// to fall back to playlist.m3u8.
		r.Get("/api/v1/transcode/sessions/{sid}/manifest.mpd", h.NativeTranscode.ManifestMPD)
		r.Get("/api/v1/transcode/sessions/{sid}/seg/{name}", h.NativeTranscode.Segment)
	}

	// ── Artwork file server ──────────────────────────────────────────────────
	// Serves poster/fanart images from library scan_paths. Requires auth
	// (cookie for browsers, Bearer for programmatic, ?token= query for
	// the Tauri native client where <img> can't carry an Authorization
	// header) and checks the caller's library ACL against whichever
	// library owns the resolved file. This was previously
	// unauthenticated — any user with a valid URL could read artwork
	// from any library, breaking content-rating confidentiality on
	// mixed-access deployments (kids vs adult library).
	if h.ArtworkRoots != nil {
		r.Group(func(r chi.Router) {
			r.Use(h.Auth_mw.RequiredAllowQueryToken)
			r.Get("/artwork/*", func(w http.ResponseWriter, req *http.Request) {
				rel := strings.TrimPrefix(req.URL.Path, "/artwork/")
				clean := filepath.Clean(rel)
				if clean == "." || strings.HasPrefix(clean, "..") || filepath.IsAbs(clean) {
					http.Error(w, "forbidden", http.StatusForbidden)
					return
				}

				// Parse optional resize query params (?w=300&h=450).
				wParam, _ := strconv.Atoi(req.URL.Query().Get("w"))
				hParam, _ := strconv.Atoi(req.URL.Query().Get("h"))
				if wParam > 1920 {
					wParam = 1920
				}
				if hParam > 1920 {
					hParam = 1920
				}

				claims := middleware.ClaimsFromContext(req.Context())
				for _, root := range h.ArtworkRoots() {
					for _, path := range root.Paths {
						abs := filepath.Join(path, clean)
						if _, err := os.Stat(abs); err != nil {
							continue
						}
						// File exists; check whether the caller can see
						// the owning library. Fail-closed: 404 (not 403)
						// so the absence of access is indistinguishable
						// from a missing file.
						if h.LibraryAccess != nil && claims != nil {
							ok, err := h.LibraryAccess.CanAccessLibrary(req.Context(), claims.UserID, root.LibraryID, claims.IsAdmin)
							if err != nil || !ok {
								http.NotFound(w, req)
								return
							}
						}
						// Serve resized variant if dimensions requested.
						if (wParam > 0 || hParam > 0) && h.Artwork != nil {
							w.Header().Set("Content-Type", "image/jpeg")
							w.Header().Set("Cache-Control", "private, max-age=604800, immutable")
							if err := h.Artwork.Resize(req.Context(), w, abs, wParam, hParam); err != nil {
								h.Logger.Error("artwork resize failed", "path", abs, "error", err)
							}
							return
						}
						w.Header().Set("Cache-Control", "private, max-age=86400, must-revalidate")
						http.ServeFile(w, req, abs)
						return
					}
				}
				http.NotFound(w, req)
			})
		})
	}

	// ── Trickplay file server ────────────────────────────────────────────────
	// Serves sprite_NNN.jpg + index.vtt from the trickplay cache dir. Auth
	// required + per-library ACL enforced inside the handler — sprites can
	// leak adult-library thumbnails into a kids-restricted session otherwise.
	// Filenames are regex-whitelisted (index.vtt | sprite_NNN.jpg) to block
	// path traversal.
	//
	// Uses RequiredAllowQueryToken because sprites are loaded via CSS
	// background-image — no Authorization header support — and the Tauri
	// cross-origin client carries auth as `?token=<paseto>`.
	if h.Trickplay != nil {
		r.Group(func(r chi.Router) {
			r.Use(h.Auth_mw.RequiredAllowQueryToken)
			r.Get("/trickplay/{id}/{file}", h.Trickplay.ServeFile)
		})
	}

	// ── Arr notification webhook (API-key auth, outside user auth) ───────────
	if h.Arr != nil {
		r.Route("/api/v1/arr", func(r chi.Router) {
			r.Use(middleware.MaxBytesBody(1 << 20))
			r.Post("/webhook", h.Arr.Webhook)
		})
	}

	// ── OnScreen native API (/api/v1/) ────────────────────────────────────────
	r.Route("/api/v1", func(r chi.Router) {
		// Limit request bodies to 1 MB to prevent memory exhaustion.
		r.Use(middleware.MaxBytesBody(1 << 20))
		// Public status endpoints — no rate limit (read-only, cheap).
		r.Group(func(r chi.Router) {
			r.Get("/setup/status", h.Auth.SetupStatus)
			r.Get("/email/enabled", h.Email.Enabled)
			r.Get("/auth/forgot-password/enabled", h.PasswordReset.Enabled)
			if h.OIDCAuth != nil {
				r.Get("/auth/oidc/enabled", h.OIDCAuth.Enabled)
			}
			if h.SAMLAuth != nil {
				r.Get("/auth/saml/enabled", h.SAMLAuth.Enabled)
				// SP metadata is public so the IdP admin can register
				// us — XML, no auth required.
				r.Get("/auth/saml/metadata", h.SAMLAuth.Metadata)
			}
			if h.LDAPAuth != nil {
				r.Get("/auth/ldap/enabled", h.LDAPAuth.Enabled)
			}
			if h.Capabilities != nil {
				r.Get("/system/capabilities", h.Capabilities.Get)
			}
		})

		// Auth endpoints — rate limited by IP.
		// Optional auth is applied so /auth/register can read admin claims
		// when an admin creates a new user after initial setup.
		r.Group(func(r chi.Router) {
			r.Use(h.Auth_mw.Optional)
			r.Use(middleware.RateLimit(h.RateLimiter, middleware.AuthLimit,
				middleware.IPKey("ratelimit:auth")))

			r.Post("/auth/login", h.Auth.Login)
			r.Post("/auth/refresh", h.Auth.Refresh)
			r.Post("/auth/logout", h.Auth.Logout)
			r.Post("/auth/register", h.Auth.Register)
			r.Post("/auth/forgot-password", h.PasswordReset.ForgotPassword)
			r.Post("/auth/reset-password", h.PasswordReset.ResetPassword)
			if h.Invite != nil {
				r.Post("/invites/accept", h.Invite.Accept)
			}

			// Native client device pairing — no user auth (the device_token
			// itself serves as a one-shot credential for poll, and the PIN
			// is rate-limited at the IP layer to deter brute force).
			if h.Pair != nil {
				r.Post("/auth/pair/code", h.Pair.CreateCode)
				r.Get("/auth/pair/poll", h.Pair.Poll)
			}

			// OIDC SSO flow (settings-driven).
			if h.OIDCAuth != nil {
				r.Get("/auth/oidc", h.OIDCAuth.Redirect)
				r.Get("/auth/oidc/callback", h.OIDCAuth.Callback)
			}
			// SAML 2.0 SP-initiated SSO (settings-driven). Login redirects
			// to the IdP; ACS receives the signed POST-back. Both are
			// rate-limited under the same IP bucket as the rest of /auth.
			if h.SAMLAuth != nil {
				r.Get("/auth/saml", h.SAMLAuth.Login)
				r.Post("/auth/saml/acs", h.SAMLAuth.ACS)
			}
			if h.LDAPAuth != nil {
				r.Post("/auth/ldap/login", h.LDAPAuth.Login)
			}
		})

		// SSE notification stream — sibling to the authenticated API
		// group below so it gets RequiredAllowQueryToken instead of
		// Required. EventSource can't carry an Authorization header
		// and cookies don't survive cross-origin from the Tauri
		// webview, so a `?token=<paseto>` query param is the only
		// usable carrier. Same trade-off as artwork: log/Referer
		// leaks scoped to the stream endpoint, regular API still
		// requires Bearer/cookie.
		if h.Notifications != nil {
			r.Group(func(r chi.Router) {
				r.Use(h.Auth_mw.RequiredAllowQueryToken)
				r.Get("/notifications/stream", h.Notifications.Stream)
			})
		}

		// Authenticated API — require valid token, rate limit by session.
		r.Group(func(r chi.Router) {
			r.Use(h.Auth_mw.Required)
			// view_as runs after Required so claims are populated, and
			// before any handler so the substitution is invisible to
			// downstream code (handlers see exactly what the target
			// user would see).
			if h.Impersonate != nil {
				r.Use(h.Auth_mw.ViewAs(h.Impersonate))
			}
			r.Use(middleware.RateLimit(h.RateLimiter, middleware.SessionLimit,
				middleware.SessionKey("ratelimit:session")))

			// Libraries — reads are open to all authenticated users.
			r.Get("/libraries", h.Library.List)
			r.Get("/libraries/{id}", h.Library.Get)
			r.Get("/libraries/{id}/items", h.Library.Items)
			r.Get("/libraries/{id}/genres", h.Library.Genres)
			r.Get("/libraries/{id}/years", h.Library.Years)

			// Library mutations — admin only.
			r.Group(func(r chi.Router) {
				r.Use(h.Auth_mw.AdminRequired)
				r.Post("/libraries", h.Library.Create)
				r.Patch("/libraries/{id}", h.Library.Update)
				r.Delete("/libraries/{id}", h.Library.Delete)
				r.Post("/libraries/{id}/scan", h.Library.Refresh)
				r.Post("/libraries/{id}/detect-intros", h.Library.DetectIntros)
			})

			// Webhooks — admin only.
			r.Group(func(r chi.Router) {
				r.Use(h.Auth_mw.AdminRequired)
				r.Get("/webhooks", h.Webhook.List)
				r.Post("/webhooks", h.Webhook.Create)
				r.Patch("/webhooks/{id}", h.Webhook.Update)
				r.Delete("/webhooks/{id}", h.Webhook.Delete)
				r.Post("/webhooks/{id}/test", h.Webhook.Test)
			})

			// User profile — PIN management + user switching + preferences.
			if h.User != nil {
				r.Put("/users/me/pin", h.User.SetPIN)
				r.Delete("/users/me/pin", h.User.ClearPIN)
				r.Get("/users/me/preferences", h.User.GetPreferences)
				r.Put("/users/me/preferences", h.User.SetPreferences)
				r.Put("/users/me/quality-profile", h.User.SetQualityProfile)
				r.Get("/users/switchable", h.User.ListSwitchable)
				r.Post("/auth/pin-switch", h.User.PINSwitch)
			}

			// Native client device pairing — claim binds a PIN to the
			// browser-authenticated user, authorising the waiting device.
			if h.Pair != nil {
				r.Post("/auth/pair/claim", h.Pair.Claim)
			}

			// Notifications.
			if h.Notifications != nil {
				r.Get("/notifications", h.Notifications.List)
				r.Get("/notifications/unread-count", h.Notifications.UnreadCount)
				r.Post("/notifications/{id}/read", h.Notifications.MarkRead)
				r.Post("/notifications/read-all", h.Notifications.MarkAllRead)
				// SSE stream is wired in a separate top-level group
				// below — chi stacks middleware, so a sub-group here
				// would run Required first and 401 before our query-
				// token variant got a chance.
			}

			// User admin management — admin only.
			if h.User != nil {
				r.Group(func(r chi.Router) {
					r.Use(h.Auth_mw.AdminRequired)
					r.Get("/users", h.User.ListUsers)
					r.Delete("/users/{id}", h.User.DeleteUser)
					r.Patch("/users/{id}", h.User.SetAdmin)
					r.Put("/users/{id}/password", h.User.ResetPassword)
					r.Put("/users/{id}/content-rating", h.User.SetContentRating)
					r.Get("/users/{id}/libraries", h.User.GetUserLibraries)
					r.Put("/users/{id}/libraries", h.User.SetUserLibraries)
				})
			}

			// Filesystem browser — admin only (exposes server directory structure).
			if h.FS != nil {
				r.Group(func(r chi.Router) {
					r.Use(h.Auth_mw.AdminRequired)
					r.Get("/fs/browse", h.FS.Browse)
				})
			}

			// Server-wide settings — admin only.
			if h.Settings != nil {
				r.Group(func(r chi.Router) {
					r.Use(h.Auth_mw.AdminRequired)
					r.Get("/settings", h.Settings.Get)
					r.Patch("/settings", h.Settings.Update)
					r.Get("/settings/encoders", h.Settings.GetEncoders)
					r.Get("/settings/workers", h.Settings.GetWorkers)
					r.Get("/settings/fleet", h.Settings.GetFleet)
					r.Put("/settings/fleet", h.Settings.UpdateFleet)
					r.Get("/settings/transcode-config", h.Settings.GetTranscodeConfig)
					r.Put("/settings/transcode-config", h.Settings.UpdateTranscodeConfig)
				})
			}

			// Maintenance — admin only one-shot operations (artwork backfill, etc.).
			if h.Maintenance != nil {
				r.Group(func(r chi.Router) {
					r.Use(h.Auth_mw.AdminRequired)
					r.Post("/maintenance/refresh-missing-art", h.Maintenance.RefreshMissingArt)
					r.Post("/maintenance/dedupe-shows", h.Maintenance.DedupeShows)
					r.Post("/maintenance/dedupe-movies", h.Maintenance.DedupeMovies)
				})
			}

			// Plugins — admin only. Outbound MCP plugin registrations.
			if h.Plugins != nil {
				r.Group(func(r chi.Router) {
					r.Use(h.Auth_mw.AdminRequired)
					r.Get("/admin/plugins", h.Plugins.List)
					r.Post("/admin/plugins", h.Plugins.Create)
					r.Patch("/admin/plugins/{id}", h.Plugins.Update)
					r.Delete("/admin/plugins/{id}", h.Plugins.Delete)
					r.Post("/admin/plugins/{id}/test", h.Plugins.Test)
				})
			}

			// Backup / restore — admin only.
			if h.Backup != nil {
				r.Group(func(r chi.Router) {
					r.Use(h.Auth_mw.AdminRequired)
					r.Get("/admin/backup", h.Backup.Download)
					r.Post("/admin/restore", h.Backup.Restore)
				})
			}

			// Server logs — admin only. Reads from the in-process
			// ring buffer attached to slog at boot.
			if h.Logs != nil {
				r.Group(func(r chi.Router) {
					r.Use(h.Auth_mw.AdminRequired)
					r.Get("/admin/logs", h.Logs.List)
				})
			}

			// Scheduled tasks — admin only.
			if h.Tasks != nil {
				r.Group(func(r chi.Router) {
					r.Use(h.Auth_mw.AdminRequired)
					r.Get("/admin/tasks", h.Tasks.List)
					r.Post("/admin/tasks", h.Tasks.Create)
					r.Get("/admin/tasks/types", h.Tasks.ListTypes)
					r.Patch("/admin/tasks/{id}", h.Tasks.Update)
					r.Delete("/admin/tasks/{id}", h.Tasks.Delete)
					r.Post("/admin/tasks/{id}/run", h.Tasks.RunNow)
					r.Get("/admin/tasks/{id}/runs", h.Tasks.Runs)
				})
			}

			// Email test — admin only.
			if h.Email != nil {
				r.Group(func(r chi.Router) {
					r.Use(h.Auth_mw.AdminRequired)
					r.Post("/email/test", h.Email.SendTest)
				})
			}

			// Invites — admin only.
			if h.Invite != nil {
				r.Group(func(r chi.Router) {
					r.Use(h.Auth_mw.AdminRequired)
					r.Post("/invites", h.Invite.Create)
					r.Get("/invites", h.Invite.List)
					r.Delete("/invites/{id}", h.Invite.Delete)
				})
			}

			// Analytics — admin only.
			if h.Analytics != nil {
				r.Group(func(r chi.Router) {
					r.Use(h.Auth_mw.AdminRequired)
					r.Get("/analytics", h.Analytics.Get)
				})
			}

			// Audit log — admin only.
			if h.Audit != nil {
				r.Group(func(r chi.Router) {
					r.Use(h.Auth_mw.AdminRequired)
					r.Get("/audit", h.Audit.List)
				})
			}

			// Arr-services admin CRUD + probe — admin only.
			if h.ArrServices != nil {
				r.Group(func(r chi.Router) {
					r.Use(h.Auth_mw.AdminRequired)
					r.Get("/admin/arr-services", h.ArrServices.List)
					r.Post("/admin/arr-services", h.ArrServices.Create)
					r.Post("/admin/arr-services/probe", h.ArrServices.Probe)
					r.Get("/admin/arr-services/{id}", h.ArrServices.Get)
					r.Patch("/admin/arr-services/{id}", h.ArrServices.Update)
					r.Delete("/admin/arr-services/{id}", h.ArrServices.Delete)
					r.Post("/admin/arr-services/{id}/set-default", h.ArrServices.SetDefault)
				})
			}

			// Discover — TMDB-backed search powering the Request UI.
			// Per-session cap on top of SessionLimit since each call hits TMDB.
			if h.Discover != nil {
				r.With(middleware.RateLimit(h.RateLimiter, middleware.DiscoverLimit,
					middleware.SessionKey("ratelimit:discover"))).
					Get("/discover/search", h.Discover.Search)
			}

			// Media requests — user-facing workflow + admin queue actions.
			if h.Requests != nil {
				r.Get("/requests", h.Requests.List)
				r.Post("/requests", h.Requests.Create)
				r.Get("/requests/{id}", h.Requests.Get)
				r.Post("/requests/{id}/cancel", h.Requests.Cancel)
				r.Group(func(r chi.Router) {
					r.Use(h.Auth_mw.AdminRequired)
					r.Post("/admin/requests/{id}/approve", h.Requests.Approve)
					r.Post("/admin/requests/{id}/decline", h.Requests.Decline)
					r.Delete("/admin/requests/{id}", h.Requests.Delete)
				})
			}

			// Home page hub — continue watching + recently added.
			if h.Hub != nil {
				r.Get("/hub", h.Hub.Get)
			}

			// Search — full-text search across media items.
			if h.Search != nil {
				r.Get("/search", h.Search.Search)
			}

			// Watch history — per-user playback history.
			if h.History != nil {
				r.Get("/history", h.History.List)
			}

			// Active sessions.
			if h.NativeSessions != nil {
				r.Get("/sessions", h.NativeSessions.List)
			}

			// Managed profiles — any authenticated user can manage their own.
			if h.User != nil {
				r.Get("/profiles", h.User.ListProfiles)
				r.Post("/profiles", h.User.CreateProfile)
				r.Patch("/profiles/{id}", h.User.UpdateProfile)
				r.Delete("/profiles/{id}", h.User.DeleteProfile)
				r.Put("/profiles/{id}/library-inherit", h.User.SetProfileLibraryInherit)
			}

			// Collections & playlists.
			if h.Collections != nil {
				r.Get("/collections", h.Collections.List)
				r.Get("/collections/{id}", h.Collections.Get)
				r.Post("/collections", h.Collections.Create)
				r.Patch("/collections/{id}", h.Collections.Update)
				r.Delete("/collections/{id}", h.Collections.Delete)
				r.Get("/collections/{id}/items", h.Collections.Items)
				r.Post("/collections/{id}/items", h.Collections.AddItem)
				r.Delete("/collections/{id}/items/{itemId}", h.Collections.RemoveItem)
			}

			// User playlists — ownership-checked, per-user.
			if h.Playlists != nil {
				r.Get("/playlists", h.Playlists.List)
				r.Post("/playlists", h.Playlists.Create)
				r.Patch("/playlists/{id}", h.Playlists.Update)
				r.Delete("/playlists/{id}", h.Playlists.Delete)
				r.Get("/playlists/{id}/items", h.Playlists.Items)
				r.Post("/playlists/{id}/items", h.Playlists.AddItem)
				r.Delete("/playlists/{id}/items/{itemId}", h.Playlists.RemoveItem)
				r.Put("/playlists/{id}/items/order", h.Playlists.Reorder)
			}

			// Photo albums — ownership-checked, per-user. Items must be type='photo'.
			if h.PhotoAlbums != nil {
				r.Get("/photo-albums", h.PhotoAlbums.List)
				r.Post("/photo-albums", h.PhotoAlbums.Create)
				r.Patch("/photo-albums/{id}", h.PhotoAlbums.Update)
				r.Delete("/photo-albums/{id}", h.PhotoAlbums.Delete)
				r.Get("/photo-albums/{id}/items", h.PhotoAlbums.Items)
				r.Post("/photo-albums/{id}/items", h.PhotoAlbums.AddItem)
				r.Delete("/photo-albums/{id}/items/{itemId}", h.PhotoAlbums.RemoveItem)
			}

			// Live TV — channels list + now/next available to any
			// authenticated user; tuner CRUD is admin-only and lives
			// in the admin block below.
			if h.LiveTV != nil {
				r.Get("/tv/channels", h.LiveTV.ListChannels)
				r.Get("/tv/channels/now-next", h.LiveTV.NowAndNext)
				r.Get("/tv/channels/{id}/stream.m3u8", h.LiveTV.StreamPlaylist)
				r.Get("/tv/channels/{id}/segments/{name}", h.LiveTV.StreamSegment)
				r.Get("/tv/guide", h.LiveTV.Guide)
				// DVR endpoints are user-scoped (not admin).
				r.Post("/tv/schedules", h.LiveTV.CreateSchedule)
				r.Get("/tv/schedules", h.LiveTV.ListSchedules)
				r.Delete("/tv/schedules/{id}", h.LiveTV.DeleteSchedule)
				r.Get("/tv/recordings", h.LiveTV.ListRecordings)
				r.Delete("/tv/recordings/{id}", h.LiveTV.CancelRecording)
				r.Group(func(r chi.Router) {
					r.Use(h.Auth_mw.AdminRequired)
					r.Get("/tv/tuners", h.LiveTV.ListTuners)
					r.Post("/tv/tuners", h.LiveTV.CreateTuner)
					r.Post("/tv/tuners/discover", h.LiveTV.DiscoverTuners)
					r.Get("/tv/tuners/{id}", h.LiveTV.GetTuner)
					r.Patch("/tv/tuners/{id}", h.LiveTV.UpdateTuner)
					r.Delete("/tv/tuners/{id}", h.LiveTV.DeleteTuner)
					r.Post("/tv/tuners/{id}/rescan", h.LiveTV.RescanTuner)
					r.Patch("/tv/channels/{id}", h.LiveTV.SetChannelEnabled)
					r.Patch("/tv/channels/{id}/epg-id", h.LiveTV.SetChannelEPGID)
					r.Put("/tv/channels/order", h.LiveTV.ReorderChannels)
					r.Get("/tv/channels/unmapped", h.LiveTV.ListUnmappedChannels)
					r.Get("/tv/epg-ids", h.LiveTV.ListEPGIDs)
					r.Get("/tv/epg-sources", h.LiveTV.ListEPGSources)
					r.Post("/tv/epg-sources", h.LiveTV.CreateEPGSource)
					r.Delete("/tv/epg-sources/{id}", h.LiveTV.DeleteEPGSource)
					r.Post("/tv/epg-sources/{id}/refresh", h.LiveTV.RefreshEPGSource)
				})
			}

			// People (cast/crew) — lazy TMDB fetch on first /credits view.
			if h.People != nil {
				r.Get("/items/{id}/credits", h.People.Credits)
				r.Get("/people/{id}", h.People.GetPerson)
				r.Get("/people/{id}/filmography", h.People.Filmography)
				r.Get("/people", h.People.Search)
			}

			// Individual media items — player, children, progress, on-demand enrich.
			if h.Items != nil {
				r.Get("/items/{id}", h.Items.Get)
				r.Get("/items/{id}/children", h.Items.Children)
				r.Put("/items/{id}/progress", h.Items.Progress)
				r.Post("/items/{id}/enrich", h.Items.Enrich)
				r.Get("/items/{id}/match/search", h.Items.SearchMatch)
				r.Post("/items/{id}/match", h.Items.ApplyMatch)
				r.Get("/items/{id}/markers", h.Items.ListMarkers)
				r.Put("/items/{id}/markers/{kind}", h.Items.UpsertMarker)
				r.Delete("/items/{id}/markers/{kind}", h.Items.DeleteMarker)
				r.Get("/items/{id}/exif", h.Items.GetEXIF)
			}

			// Photo browse + on-demand resize. Image endpoint shares the
			// /items/{id} prefix because it is item-scoped; List/Timeline
			// live under /photos because they aggregate across an entire
			// library.
			if h.Lyrics != nil {
				r.Get("/items/{id}/lyrics", h.Lyrics.Get)
			}

			if h.Photos != nil {
				r.Get("/photos", h.Photos.List)
				r.Get("/photos/timeline", h.Photos.Timeline)
				r.Get("/photos/map", h.Photos.Map)
				r.Get("/photos/search", h.Photos.Search)
				r.Get("/items/{id}/image", h.Photos.Image)

				if h.Books != nil {
					r.Get("/items/{id}/book/page/{n}", h.Books.Page)
				}
			}

			// Trickplay (seekbar thumbnail previews).
			if h.Trickplay != nil {
				r.Get("/items/{id}/trickplay", h.Trickplay.Status)
				r.Post("/items/{id}/trickplay", h.Trickplay.Generate)
			}

			// External subtitle search and download (OpenSubtitles, etc.).
			if h.Subtitles != nil {
				r.Get("/items/{id}/subtitles/search", h.Subtitles.Search)
				r.Post("/items/{id}/subtitles/download", h.Subtitles.Download)
				r.Post("/items/{id}/subtitles/ocr", h.Subtitles.OCR)
				r.Get("/items/{id}/subtitles/ocr/{jobId}", h.Subtitles.OCRStatus)
				r.Delete("/items/{id}/subtitles/{subId}", h.Subtitles.Delete)
			}

			// Favorites — per-user.
			if h.Favorites != nil {
				r.Get("/favorites", h.Favorites.List)
				r.Post("/items/{id}/favorite", h.Favorites.Add)
				r.Delete("/items/{id}/favorite", h.Favorites.Remove)
			}

			// Native HLS transcode.
			// Start spins up an ffmpeg job — limited per session to keep a
			// runaway player from DoS-ing the host. Stop is a cheap teardown
			// and only protected by SessionLimit.
			if h.NativeTranscode != nil {
				r.With(middleware.RateLimit(h.RateLimiter, middleware.TranscodeStartLimit,
					middleware.SessionKey("ratelimit:transcode_start"))).
					Post("/items/{id}/transcode", h.NativeTranscode.Start)
				r.Delete("/transcode/sessions/{sid}", h.NativeTranscode.Stop)
			}
		})
	})

	// ── SvelteKit SPA ─────────────────────────────────────────────────────────
	// Serves the embedded frontend. Uses http.ServeContent (not FileServer) to
	// avoid redirect loops caused by FileServer's directory canonicalisation.
	uiFS := webui.FS()
	serveUI := func(w http.ResponseWriter, req *http.Request, name string) {
		f, err := uiFS.Open(name)
		if err != nil {
			http.NotFound(w, req)
			return
		}
		defer f.Close()
		info, err := f.Stat()
		if err != nil {
			http.NotFound(w, req)
			return
		}
		http.ServeContent(w, req, name, info.ModTime(), f.(io.ReadSeeker))
	}
	r.NotFound(func(w http.ResponseWriter, req *http.Request) {
		target := strings.TrimPrefix(req.URL.Path, "/")
		if target != "" {
			if info, err := fs.Stat(uiFS, target); err == nil && !info.IsDir() {
				serveUI(w, req, target)
				return
			}
		}
		serveUI(w, req, "index.html")
	})

	return r
}
