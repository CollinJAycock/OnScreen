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
	chimiddleware "github.com/go-chi/chi/v5/middleware"

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
	Library     *v1.LibraryHandler
	Webhook     *v1.WebhookHandler
	Auth        *v1.AuthHandler
	User        *v1.UserHandler
	FS          *v1.FSHandler
	Settings    *v1.SettingsHandler
	Analytics       *v1.AnalyticsHandler
	NativeSessions  *v1.NativeSessionsHandler
	Hub             *v1.HubHandler
	Search          *v1.SearchHandler
	History         *v1.HistoryHandler
	Items           *v1.ItemHandler
	NativeTranscode *v1.NativeTranscodeHandler
	Collections     *v1.CollectionHandler
	Arr             *v1.ArrHandler           // incoming arr app notifications
	GoogleAuth      *v1.GoogleOAuthHandler  // nil when SSO not configured
	GitHubAuth      *v1.GitHubOAuthHandler  // nil when SSO not configured
	DiscordAuth     *v1.DiscordOAuthHandler // nil when SSO not configured
	Audit           *v1.AuditHandler
	Email           *v1.EmailHandler
	PasswordReset   *v1.PasswordResetHandler
	Invite          *v1.InviteHandler
	Notifications   *v1.NotificationHandler
	StreamTracker   *streaming.Tracker
	Artwork       *artwork.Manager
	ArtworkRoots  func() []string // returns all library scan_paths for artwork serving
	MediaPath     string          // deprecated — only used for /media/files/* fallback
	Logger        *slog.Logger
	Metrics     *observability.Metrics
	Auth_mw     *middleware.Authenticator
	RateLimiter *valkey.RateLimiter
}

// NewRouter builds the full Chi router.
func NewRouter(h *Handlers) http.Handler {
	r := chi.NewRouter()

	// Global middleware (applied to all routes).
	r.Use(chimiddleware.RealIP)
	r.Use(middleware.RequestID)
	r.Use(middleware.SecurityHeaders)
	r.Use(middleware.Recover(h.Logger))
	r.Use(middleware.Logger(h.Logger))

	// ── Health endpoints ──────────────────────────────────────────────────────
	// These are on the main port for simplicity; the metrics port serves /metrics.
	r.Get("/health/live", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	})
	// /health/ready is registered by the server after checking deps.

	// ── Artwork file server ───────────────────────────────────────────────────
	// Serves poster/fanart images from the media path. No auth required so that
	// <img> tags load without credentials. Path traversal is blocked by
	// http.FileServer's own path cleaning.
	// ── Media stream by file UUID ──────────────────────────────────────────────
	// Serves a media file by its DB UUID. Used by the native web/Android client.
	// No auth required — UUID is opaque; browser video elements cannot send tokens.
	if h.Items != nil {
		r.Get("/media/stream/{id}", h.Items.StreamFile)
		r.Get("/media/subtitles/{fileId}/{streamIndex}", h.Items.ServeSubtitle)
	}

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
			if clean == "." || strings.HasPrefix(clean, "..") {
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

		// ── Direct-play file server ───────────────────────────────────────────
		// Serves raw media files for direct play. Tries each library root.
		mediaFileHandler := http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			rel := strings.TrimPrefix(req.URL.Path, "/media/files/")
			clean := filepath.Clean(rel)
			if clean == "." || strings.HasPrefix(clean, "..") {
				http.Error(w, "forbidden", http.StatusForbidden)
				return
			}
			for _, root := range h.ArtworkRoots() {
				abs := filepath.Join(root, clean)
				if _, err := os.Stat(abs); err == nil {
					http.ServeFile(w, req, abs)
					return
				}
			}
			http.NotFound(w, req)
		})
		var wrappedMedia http.Handler = mediaFileHandler
		if h.StreamTracker != nil {
			// Use first root for tracker middleware context.
			roots := h.ArtworkRoots()
			mediaRoot := ""
			if len(roots) > 0 {
				mediaRoot = roots[0]
			}
			wrappedMedia = h.StreamTracker.Middleware("/media/files", mediaRoot, mediaFileHandler)
		}
		r.Get("/media/files/*", wrappedMedia.ServeHTTP)
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
			if h.GoogleAuth != nil {
				r.Get("/auth/google/enabled", h.GoogleAuth.Enabled)
			} else {
				r.Get("/auth/google/enabled", v1.GoogleDisabledHandler())
			}
			if h.GitHubAuth != nil {
				r.Get("/auth/github/enabled", h.GitHubAuth.Enabled)
			} else {
				r.Get("/auth/github/enabled", v1.GitHubDisabledHandler())
			}
			if h.DiscordAuth != nil {
				r.Get("/auth/discord/enabled", h.DiscordAuth.Enabled)
			} else {
				r.Get("/auth/discord/enabled", v1.DiscordDisabledHandler())
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

			// OAuth2 SSO flows (redirects + callbacks).
			if h.GoogleAuth != nil {
				r.Get("/auth/google", h.GoogleAuth.Redirect)
				r.Get("/auth/google/callback", h.GoogleAuth.Callback)
			}
			if h.GitHubAuth != nil {
				r.Get("/auth/github", h.GitHubAuth.Redirect)
				r.Get("/auth/github/callback", h.GitHubAuth.Callback)
			}
			if h.DiscordAuth != nil {
				r.Get("/auth/discord", h.DiscordAuth.Redirect)
				r.Get("/auth/discord/callback", h.DiscordAuth.Callback)
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

			// Library mutations — admin only.
			r.Group(func(r chi.Router) {
				r.Use(h.Auth_mw.AdminRequired)
				r.Post("/libraries", h.Library.Create)
				r.Patch("/libraries/{id}", h.Library.Update)
				r.Delete("/libraries/{id}", h.Library.Delete)
				r.Post("/libraries/{id}/scan", h.Library.Refresh)
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

			// Individual media items — player, children, progress, on-demand enrich.
			if h.Items != nil {
				r.Get("/items/{id}", h.Items.Get)
				r.Get("/items/{id}/children", h.Items.Children)
				r.Put("/items/{id}/progress", h.Items.Progress)
				r.Post("/items/{id}/enrich", h.Items.Enrich)
				r.Get("/items/{id}/match/search", h.Items.SearchMatch)
				r.Post("/items/{id}/match", h.Items.ApplyMatch)
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
