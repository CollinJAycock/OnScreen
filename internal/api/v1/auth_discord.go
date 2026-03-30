package v1

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/jackc/pgx/v5"
	"golang.org/x/oauth2"

	"github.com/onscreen/onscreen/internal/api/respond"
	"github.com/onscreen/onscreen/internal/db/gen"
)

// Discord OAuth2 endpoint.
var discordEndpoint = oauth2.Endpoint{
	AuthURL:  "https://discord.com/api/oauth2/authorize",
	TokenURL: "https://discord.com/api/oauth2/token",
}

// DiscordOAuthDB defines the database queries needed for Discord SSO.
type DiscordOAuthDB interface {
	GetUserByDiscordID(ctx context.Context, discordID *string) (gen.User, error)
	GetUserByEmail(ctx context.Context, email *string) (gen.User, error)
	LinkDiscordAccount(ctx context.Context, arg gen.LinkDiscordAccountParams) error
	CreateDiscordUser(ctx context.Context, arg gen.CreateDiscordUserParams) (gen.User, error)
	CountUsers(ctx context.Context) (int64, error)
}

// DiscordOAuthHandler handles Discord OAuth2 SSO.
type DiscordOAuthHandler struct {
	oauth2Cfg *oauth2.Config
	svc       *discordAuthService
	logger    *slog.Logger
}

// NewDiscordOAuthHandler creates a DiscordOAuthHandler.
func NewDiscordOAuthHandler(clientID, clientSecret, baseURL string, db DiscordOAuthDB, issueTokens IssueTokenPairFn, logger *slog.Logger) *DiscordOAuthHandler {
	return &DiscordOAuthHandler{
		oauth2Cfg: &oauth2.Config{
			ClientID:     clientID,
			ClientSecret: clientSecret,
			Endpoint:     discordEndpoint,
			RedirectURL:  baseURL + "/api/v1/auth/discord/callback",
			Scopes:       []string{"identify", "email"},
		},
		svc: &discordAuthService{
			db:          db,
			issueTokens: issueTokens,
			logger:      logger,
		},
		logger: logger,
	}
}

const discordStateCookie = "onscreen_discord_state"

// Redirect initiates the Discord OAuth2 flow.
func (h *DiscordOAuthHandler) Redirect(w http.ResponseWriter, r *http.Request) {
	state, err := randomState()
	if err != nil {
		respond.InternalError(w, r)
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     discordStateCookie,
		Value:    state,
		Path:     "/api/v1/auth/discord",
		HttpOnly: true,
		Secure:   isSecure(r),
		SameSite: http.SameSiteLaxMode,
		MaxAge:   300,
	})
	url := h.oauth2Cfg.AuthCodeURL(state)
	http.Redirect(w, r, url, http.StatusTemporaryRedirect)
}

// Callback handles the Discord OAuth2 redirect.
func (h *DiscordOAuthHandler) Callback(w http.ResponseWriter, r *http.Request) {
	stateCookie, err := r.Cookie(discordStateCookie)
	if err != nil || stateCookie.Value == "" {
		respond.BadRequest(w, r, "missing OAuth state")
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name: discordStateCookie, Value: "", Path: "/api/v1/auth/discord",
		HttpOnly: true, MaxAge: -1,
	})
	if r.URL.Query().Get("state") != stateCookie.Value {
		respond.BadRequest(w, r, "invalid OAuth state")
		return
	}
	if errParam := r.URL.Query().Get("error"); errParam != "" {
		h.logger.Warn("discord oauth error", "error", errParam)
		http.Redirect(w, r, "/login?error=discord_denied", http.StatusTemporaryRedirect)
		return
	}

	code := r.URL.Query().Get("code")
	if code == "" {
		respond.BadRequest(w, r, "missing authorization code")
		return
	}
	token, err := h.oauth2Cfg.Exchange(r.Context(), code)
	if err != nil {
		h.logger.Error("discord oauth exchange failed", "err", err)
		http.Redirect(w, r, "/login?error=discord_failed", http.StatusTemporaryRedirect)
		return
	}

	dcUser, err := fetchDiscordUser(r.Context(), token.AccessToken)
	if err != nil {
		h.logger.Error("discord oauth: fetch user", "err", err)
		http.Redirect(w, r, "/login?error=discord_failed", http.StatusTemporaryRedirect)
		return
	}

	pair, err := h.svc.loginOrCreate(r.Context(), dcUser)
	if err != nil {
		h.logger.Error("discord oauth: login/create", "err", err)
		http.Redirect(w, r, "/login?error=discord_failed", http.StatusTemporaryRedirect)
		return
	}

	setAuthCookies(w, r, pair)
	http.Redirect(w, r, "/?discord_auth=1", http.StatusTemporaryRedirect)
}

// Enabled returns whether Discord SSO is configured.
func (h *DiscordOAuthHandler) Enabled(w http.ResponseWriter, r *http.Request) {
	respond.Success(w, r, map[string]interface{}{"enabled": true})
}

// DiscordDisabledHandler returns a handler that reports Discord SSO as disabled.
func DiscordDisabledHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		respond.Success(w, r, map[string]interface{}{"enabled": false})
	}
}

// ── Discord user info ────────────────────────────────────────────────────────

type discordUserInfo struct {
	ID            string `json:"id"`
	Username      string `json:"username"`
	GlobalName    string `json:"global_name"`
	Email         string `json:"email"`
	Verified      bool   `json:"verified"`
}

func fetchDiscordUser(ctx context.Context, accessToken string) (*discordUserInfo, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", "https://discord.com/api/v10/users/@me", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("discord api: status %d", resp.StatusCode)
	}
	var u discordUserInfo
	if err := json.NewDecoder(resp.Body).Decode(&u); err != nil {
		return nil, err
	}
	return &u, nil
}

// ── Discord auth service ─────────────────────────────────────────────────────

type discordAuthService struct {
	db          DiscordOAuthDB
	issueTokens IssueTokenPairFn
	logger      *slog.Logger
}

func (s *discordAuthService) loginOrCreate(ctx context.Context, dc *discordUserInfo) (*TokenPair, error) {
	// 1. Fast path: already linked.
	user, err := s.db.GetUserByDiscordID(ctx, &dc.ID)
	if err == nil {
		return s.issueTokens(ctx, user)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("get user by discord_id: %w", err)
	}

	// 2. Try email link (only if verified).
	if dc.Email != "" && dc.Verified {
		user, err = s.db.GetUserByEmail(ctx, &dc.Email)
		if err == nil {
			if linkErr := s.db.LinkDiscordAccount(ctx, gen.LinkDiscordAccountParams{
				ID:        user.ID,
				DiscordID: &dc.ID,
				Email:     &dc.Email,
			}); linkErr != nil {
				s.logger.Warn("failed to link discord account", "user_id", user.ID, "err", linkErr)
			}
			return s.issueTokens(ctx, user)
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("get user by email: %w", err)
		}
	}

	// 3. Create new user.
	name := dc.GlobalName
	if name == "" {
		name = dc.Username
	}
	username := deriveUsername(name, dc.Email)
	count, err := s.db.CountUsers(ctx)
	if err != nil {
		return nil, fmt.Errorf("count users: %w", err)
	}
	user, err = s.db.CreateDiscordUser(ctx, gen.CreateDiscordUserParams{
		Username:  username,
		Email:     &dc.Email,
		DiscordID: &dc.ID,
		IsAdmin:   count == 0,
	})
	if err != nil {
		return nil, fmt.Errorf("create discord user: %w", err)
	}
	return s.issueTokens(ctx, user)
}
