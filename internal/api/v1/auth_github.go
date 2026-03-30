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
	"golang.org/x/oauth2/github"

	"github.com/onscreen/onscreen/internal/api/respond"
	"github.com/onscreen/onscreen/internal/db/gen"
)

// GitHubOAuthDB defines the database queries needed for GitHub SSO.
type GitHubOAuthDB interface {
	GetUserByGitHubID(ctx context.Context, githubID *string) (gen.User, error)
	GetUserByEmail(ctx context.Context, email *string) (gen.User, error)
	LinkGitHubAccount(ctx context.Context, arg gen.LinkGitHubAccountParams) error
	CreateGitHubUser(ctx context.Context, arg gen.CreateGitHubUserParams) (gen.User, error)
	CountUsers(ctx context.Context) (int64, error)
}

// GitHubOAuthHandler handles GitHub OAuth2 SSO.
type GitHubOAuthHandler struct {
	oauth2Cfg *oauth2.Config
	svc       *githubAuthService
	logger    *slog.Logger
}

// NewGitHubOAuthHandler creates a GitHubOAuthHandler.
func NewGitHubOAuthHandler(clientID, clientSecret, baseURL string, db GitHubOAuthDB, issueTokens IssueTokenPairFn, logger *slog.Logger) *GitHubOAuthHandler {
	return &GitHubOAuthHandler{
		oauth2Cfg: &oauth2.Config{
			ClientID:     clientID,
			ClientSecret: clientSecret,
			Endpoint:     github.Endpoint,
			RedirectURL:  baseURL + "/api/v1/auth/github/callback",
			Scopes:       []string{"read:user", "user:email"},
		},
		svc: &githubAuthService{
			db:          db,
			issueTokens: issueTokens,
			logger:      logger,
		},
		logger: logger,
	}
}

const githubStateCookie = "onscreen_github_state"

// Redirect initiates the GitHub OAuth2 flow.
func (h *GitHubOAuthHandler) Redirect(w http.ResponseWriter, r *http.Request) {
	state, err := randomState()
	if err != nil {
		respond.InternalError(w, r)
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     githubStateCookie,
		Value:    state,
		Path:     "/api/v1/auth/github",
		HttpOnly: true,
		Secure:   isSecure(r),
		SameSite: http.SameSiteLaxMode,
		MaxAge:   300,
	})
	url := h.oauth2Cfg.AuthCodeURL(state)
	http.Redirect(w, r, url, http.StatusTemporaryRedirect)
}

// Callback handles the GitHub OAuth2 redirect.
func (h *GitHubOAuthHandler) Callback(w http.ResponseWriter, r *http.Request) {
	stateCookie, err := r.Cookie(githubStateCookie)
	if err != nil || stateCookie.Value == "" {
		respond.BadRequest(w, r, "missing OAuth state")
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name: githubStateCookie, Value: "", Path: "/api/v1/auth/github",
		HttpOnly: true, MaxAge: -1,
	})
	if r.URL.Query().Get("state") != stateCookie.Value {
		respond.BadRequest(w, r, "invalid OAuth state")
		return
	}
	if errParam := r.URL.Query().Get("error"); errParam != "" {
		h.logger.Warn("github oauth error", "error", errParam)
		http.Redirect(w, r, "/login?error=github_denied", http.StatusTemporaryRedirect)
		return
	}

	code := r.URL.Query().Get("code")
	if code == "" {
		respond.BadRequest(w, r, "missing authorization code")
		return
	}
	token, err := h.oauth2Cfg.Exchange(r.Context(), code)
	if err != nil {
		h.logger.Error("github oauth exchange failed", "err", err)
		http.Redirect(w, r, "/login?error=github_failed", http.StatusTemporaryRedirect)
		return
	}

	// Fetch user info from GitHub API.
	ghUser, err := fetchGitHubUser(r.Context(), token.AccessToken)
	if err != nil {
		h.logger.Error("github oauth: fetch user", "err", err)
		http.Redirect(w, r, "/login?error=github_failed", http.StatusTemporaryRedirect)
		return
	}

	pair, err := h.svc.loginOrCreate(r.Context(), ghUser)
	if err != nil {
		h.logger.Error("github oauth: login/create", "err", err)
		http.Redirect(w, r, "/login?error=github_failed", http.StatusTemporaryRedirect)
		return
	}

	setAuthCookies(w, r, pair)
	http.Redirect(w, r, "/?github_auth=1", http.StatusTemporaryRedirect)
}

// Enabled returns whether GitHub SSO is configured.
func (h *GitHubOAuthHandler) Enabled(w http.ResponseWriter, r *http.Request) {
	respond.Success(w, r, map[string]interface{}{"enabled": true})
}

// GitHubDisabledHandler returns a handler that reports GitHub SSO as disabled.
func GitHubDisabledHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		respond.Success(w, r, map[string]interface{}{"enabled": false})
	}
}

// ── GitHub user info ─────────────────────────────────────────────────────────

type githubUserInfo struct {
	ID    int64  `json:"id"`
	Login string `json:"login"`
	Name  string `json:"name"`
	Email string `json:"email"`
}

func fetchGitHubUser(ctx context.Context, accessToken string) (*githubUserInfo, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", "https://api.github.com/user", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("github api: status %d", resp.StatusCode)
	}
	var u githubUserInfo
	if err := json.NewDecoder(resp.Body).Decode(&u); err != nil {
		return nil, err
	}
	// If email is not public, fetch from /user/emails.
	if u.Email == "" {
		u.Email, _ = fetchGitHubPrimaryEmail(ctx, accessToken)
	}
	return &u, nil
}

func fetchGitHubPrimaryEmail(ctx context.Context, accessToken string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", "https://api.github.com/user/emails", nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	var emails []struct {
		Email    string `json:"email"`
		Primary  bool   `json:"primary"`
		Verified bool   `json:"verified"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&emails); err != nil {
		return "", err
	}
	for _, e := range emails {
		if e.Primary && e.Verified {
			return e.Email, nil
		}
	}
	return "", fmt.Errorf("no verified primary email")
}

// ── GitHub auth service ──────────────────────────────────────────────────────

type githubAuthService struct {
	db          GitHubOAuthDB
	issueTokens IssueTokenPairFn
	logger      *slog.Logger
}

func (s *githubAuthService) loginOrCreate(ctx context.Context, gh *githubUserInfo) (*TokenPair, error) {
	ghID := fmt.Sprintf("%d", gh.ID)

	// 1. Fast path: already linked.
	user, err := s.db.GetUserByGitHubID(ctx, &ghID)
	if err == nil {
		return s.issueTokens(ctx, user)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("get user by github_id: %w", err)
	}

	// 2. Try email link.
	if gh.Email != "" {
		user, err = s.db.GetUserByEmail(ctx, &gh.Email)
		if err == nil {
			if linkErr := s.db.LinkGitHubAccount(ctx, gen.LinkGitHubAccountParams{
				ID:       user.ID,
				GithubID: &ghID,
				Email:    &gh.Email,
			}); linkErr != nil {
				s.logger.Warn("failed to link github account", "user_id", user.ID, "err", linkErr)
			}
			return s.issueTokens(ctx, user)
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("get user by email: %w", err)
		}
	}

	// 3. Create new user.
	name := gh.Name
	if name == "" {
		name = gh.Login
	}
	username := deriveUsername(name, gh.Email)
	count, err := s.db.CountUsers(ctx)
	if err != nil {
		return nil, fmt.Errorf("count users: %w", err)
	}
	user, err = s.db.CreateGitHubUser(ctx, gen.CreateGitHubUserParams{
		Username: username,
		Email:    &gh.Email,
		GithubID: &ghID,
		IsAdmin:  count == 0,
	})
	if err != nil {
		return nil, fmt.Errorf("create github user: %w", err)
	}
	return s.issueTokens(ctx, user)
}
