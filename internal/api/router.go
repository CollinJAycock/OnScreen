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

	"github.com/onscreen/onscreen/internal/api/middleware"
	v1 "github.com/onscreen/onscreen/internal/api/v1"
	"github.com/onscreen/onscreen/internal/artwork"
	"github.com/onscreen/onscreen/internal/observability"
	"github.com/onscreen/onscreen/internal/streaming"
	"github.com/onscreen/onscreen/internal/valkey"
	"github.com/onscreen/onscreen/internal/webui"
)

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
	Trickplay       *v1.TrickplayHandler
	NativeTranscode *v1.NativeTranscodeHandler
	Collections     *v1.CollectionHandler
	Playlists       *v1.PlaylistHandler
	Subtitles       *v1.SubtitleHandler
	Arr             *v1.ArrHandler  // incoming arr app notifications
	OIDCAuth        *v1.OIDCHandler // settings-driven, always non-nil
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
	StreamTracker   *streaming.Tracker
	Artwork         *artwork.Manager
	ArtworkRoots    func() []string // returns all library scan_paths for artwork serving
	Logger          *slog.Logger
	Metrics         *observability.Metrics
	Auth_mw         *middleware.Authenticator
	RateLimiter     *valkey.RateLimiter
	// CORSAllowedOrigins enables cross-origin API access (TV app, third-party
	// native clients). Empty disables CORS — same-origin only.
	CORSAllowedOrigins []string
}

// NewRouter builds the full Chi router.
func NewRouter(h *Handlers) http.Handler {
	r := chi.NewRouter()

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
	// and external VTT subtitles. Auth is required: browser <video> and <track>
	// elements send same-origin cookies, so the cookie auth path in Auth_mw
	// covers these even though they cannot attach Bearer headers. Each handler
	// additionally enforces per-library ACL and the parent item's content rating.
	r.Group(func(r chi.Router) {
		r.Use(h.Auth_mw.Required)
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
		r.Get("/api/v1/transcode/sessions/{sid}/seg/{name}", h.NativeTranscode.Segment)
	}

	// ── Artwork file server ──────────────────────────────────────────────────
	// Serves poster/fanart images from library scan_paths. No auth required so
	// that <img> tags load without credentials. Tries each known scan_path root
	// until the file is found.
	if h.ArtworkRoots != nil {
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

			for _, root := range h.ArtworkRoots() {
				abs := filepath.Join(root, clean)
				if _, err := os.Stat(abs); err == nil {
					// Serve resized variant if dimensions requested.
					if (wParam > 0 || hParam > 0) && h.Artwork != nil {
						w.Header().Set("Content-Type", "image/jpeg")
						w.Header().Set("Cache-Control", "public, max-age=604800, immutable")
						if err := h.Artwork.Resize(req.Context(), w, abs, wParam, hParam); err != nil {
							h.Logger.Error("artwork resize failed", "path", abs, "error", err)
						}
						return
					}
					w.Header().Set("Cache-Control", "public, max-age=86400, must-revalidate")
					http.ServeFile(w, req, abs)
					return
				}
			}
			http.NotFound(w, req)
		})
	}

	// ── Trickplay file server ────────────────────────────────────────────────
	// Serves sprite_NNN.jpg + index.vtt from the trickplay cache dir. No auth
	// so <video><track> loads without credentials, matching /artwork/*. The
	// handler whitelists filenames to block path traversal.
	if h.Trickplay != nil {
		r.Get("/trickplay/{id}/{file}", h.Trickplay.ServeFile)
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
			if h.LDAPAuth != nil {
				r.Get("/auth/ldap/enabled", h.LDAPAuth.Enabled)
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

			// OIDC SSO flow (settings-driven).
			if h.OIDCAuth != nil {
				r.Get("/auth/oidc", h.OIDCAuth.Redirect)
				r.Get("/auth/oidc/callback", h.OIDCAuth.Callback)
			}
			if h.LDAPAuth != nil {
				r.Post("/auth/ldap/login", h.LDAPAuth.Login)
			}
		})

		// Authenticated API — require valid token, rate limit by session.
		r.Group(func(r chi.Router) {
			r.Use(h.Auth_mw.Required)
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
				r.Get("/users/switchable", h.User.ListSwitchable)
				r.Post("/auth/pin-switch", h.User.PINSwitch)
			}

			// Notifications.
			if h.Notifications != nil {
				r.Get("/notifications", h.Notifications.List)
				r.Get("/notifications/unread-count", h.Notifications.UnreadCount)
				r.Post("/notifications/{id}/read", h.Notifications.MarkRead)
				r.Post("/notifications/read-all", h.Notifications.MarkAllRead)
				r.Get("/notifications/stream", h.Notifications.Stream)
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

			// Backup / restore — admin only.
			if h.Backup != nil {
				r.Group(func(r chi.Router) {
					r.Use(h.Auth_mw.AdminRequired)
					r.Get("/admin/backup", h.Backup.Download)
					r.Post("/admin/restore", h.Backup.Restore)
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
				r.Delete("/items/{id}/subtitles/{subId}", h.Subtitles.Delete)
			}

			// Favorites — per-user.
			if h.Favorites != nil {
				r.Get("/favorites", h.Favorites.List)
				r.Post("/items/{id}/favorite", h.Favorites.Add)
				r.Delete("/items/{id}/favorite", h.Favorites.Remove)
			}

			// Native HLS transcode.
			if h.NativeTranscode != nil {
				r.Post("/items/{id}/transcode", h.NativeTranscode.Start)
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
