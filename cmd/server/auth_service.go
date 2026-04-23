package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"golang.org/x/crypto/bcrypt"

	v1 "github.com/onscreen/onscreen/internal/api/v1"
	"github.com/onscreen/onscreen/internal/auth"
	"github.com/onscreen/onscreen/internal/db/gen"
)

// authQuerier is the DB subset needed by authService.
type authQuerier interface {
	CountUsers(ctx context.Context) (int64, error)
	GetUser(ctx context.Context, id uuid.UUID) (gen.User, error)
	GetUserByUsername(ctx context.Context, username string) (gen.User, error)
	CreateUser(ctx context.Context, arg gen.CreateUserParams) (gen.User, error)
	CreateFirstAdmin(ctx context.Context, arg gen.CreateFirstAdminParams) (gen.User, error)
	CreateSession(ctx context.Context, arg gen.CreateSessionParams) (gen.Session, error)
	GetSessionByTokenHash(ctx context.Context, tokenHash string) (gen.Session, error)
	RotateSession(ctx context.Context, arg gen.RotateSessionParams) (gen.Session, error)
	DeleteSession(ctx context.Context, id uuid.UUID) error
	TouchSession(ctx context.Context, id uuid.UUID) error
}

type authService struct {
	db     authQuerier
	tokens *auth.TokenMaker
	logger *slog.Logger
}

func (s *authService) UserCount(ctx context.Context) (int64, error) {
	return s.db.CountUsers(ctx)
}

// CreateFirstAdmin atomically creates the first user as admin only if
// the users table is empty. Returns v1.ErrNotFirstUser when another
// goroutine (or operator) has already completed setup — the loser of
// the race gets a clean error instead of a spurious unique-constraint
// conflict or a silent "second admin created" incident.
func (s *authService) CreateFirstAdmin(ctx context.Context, username, email, password string) (*v1.UserInfo, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), 12)
	if err != nil {
		return nil, fmt.Errorf("hash password: %w", err)
	}
	hashStr := string(hash)
	var emailPtr *string
	if email != "" {
		emailPtr = &email
	}
	user, err := s.db.CreateFirstAdmin(ctx, gen.CreateFirstAdminParams{
		Username:     username,
		Email:        emailPtr,
		PasswordHash: &hashStr,
	})
	if err != nil {
		// pgx returns ErrNoRows when the WHERE NOT EXISTS clause filters
		// out the insert — that's our "setup already done" signal.
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, v1.ErrNotFirstUser
		}
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return nil, v1.ErrUserExists
		}
		return nil, fmt.Errorf("create first admin: %w", err)
	}
	return &v1.UserInfo{ID: user.ID, Username: user.Username, IsAdmin: user.IsAdmin}, nil
}

func (s *authService) CreateUser(ctx context.Context, username, email, password string, isAdmin bool) (*v1.UserInfo, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), 12)
	if err != nil {
		return nil, fmt.Errorf("hash password: %w", err)
	}
	hashStr := string(hash)
	var emailPtr *string
	if email != "" {
		emailPtr = &email
	}
	user, err := s.db.CreateUser(ctx, gen.CreateUserParams{
		Username:     username,
		Email:        emailPtr,
		PasswordHash: &hashStr,
		IsAdmin:      isAdmin,
	})
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return nil, v1.ErrUserExists
		}
		return nil, fmt.Errorf("create user: %w", err)
	}
	return &v1.UserInfo{ID: user.ID, Username: user.Username, IsAdmin: user.IsAdmin}, nil
}

func (s *authService) LoginLocal(ctx context.Context, username, password string) (*v1.TokenPair, error) {
	user, err := s.db.GetUserByUsername(ctx, username)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("invalid credentials")
		}
		return nil, fmt.Errorf("login: %w", err)
	}
	if user.PasswordHash == nil {
		return nil, fmt.Errorf("invalid credentials")
	}
	if err := bcrypt.CompareHashAndPassword([]byte(*user.PasswordHash), []byte(password)); err != nil {
		return nil, fmt.Errorf("invalid credentials")
	}
	return s.issueTokenPair(ctx, user)
}

func (s *authService) Refresh(ctx context.Context, refreshToken string) (*v1.TokenPair, error) {
	hash := auth.HashToken(refreshToken)
	session, err := s.db.GetSessionByTokenHash(ctx, hash)
	if err != nil {
		return nil, fmt.Errorf("refresh: session not found or expired")
	}
	user, err := s.db.GetUser(ctx, session.UserID)
	if err != nil {
		return nil, fmt.Errorf("refresh: get user: %w", err)
	}

	raw, newHash, err := auth.IssueRefreshToken()
	if err != nil {
		return nil, fmt.Errorf("refresh: issue token: %w", err)
	}
	expiry := time.Now().Add(auth.RefreshTokenTTL)
	if _, err = s.db.RotateSession(ctx, gen.RotateSessionParams{
		ID:        session.ID,
		TokenHash: newHash,
		ExpiresAt: pgtype.Timestamptz{Time: expiry, Valid: true},
	}); err != nil {
		return nil, fmt.Errorf("refresh: rotate session: %w", err)
	}

	refreshClaims := auth.Claims{
		UserID:       user.ID,
		Username:     user.Username,
		IsAdmin:      user.IsAdmin,
		SessionEpoch: user.SessionEpoch,
	}
	if user.MaxContentRating != nil {
		refreshClaims.MaxContentRating = *user.MaxContentRating
	}
	accessToken, err := s.tokens.IssueAccessToken(refreshClaims)
	if err != nil {
		return nil, fmt.Errorf("refresh: issue access token: %w", err)
	}
	return &v1.TokenPair{
		AccessToken:  accessToken,
		RefreshToken: raw,
		ExpiresAt:    expiry,
		UserID:       user.ID,
		Username:     user.Username,
		IsAdmin:      user.IsAdmin,
	}, nil
}

func (s *authService) Logout(ctx context.Context, refreshToken string) error {
	hash := auth.HashToken(refreshToken)
	session, err := s.db.GetSessionByTokenHash(ctx, hash)
	if err != nil {
		return nil // already gone
	}
	return s.db.DeleteSession(ctx, session.ID)
}

// issueTokenPair creates an access + refresh token and persists the session.
func (s *authService) issueTokenPair(ctx context.Context, user gen.User) (*v1.TokenPair, error) {
	claims := auth.Claims{
		UserID:       user.ID,
		Username:     user.Username,
		IsAdmin:      user.IsAdmin,
		SessionEpoch: user.SessionEpoch,
	}
	if user.MaxContentRating != nil {
		claims.MaxContentRating = *user.MaxContentRating
	}
	accessToken, err := s.tokens.IssueAccessToken(claims)
	if err != nil {
		return nil, fmt.Errorf("issue access token: %w", err)
	}

	raw, hash, err := auth.IssueRefreshToken()
	if err != nil {
		return nil, fmt.Errorf("issue refresh token: %w", err)
	}

	expiry := time.Now().Add(auth.RefreshTokenTTL)
	if _, err = s.db.CreateSession(ctx, gen.CreateSessionParams{
		UserID:    user.ID,
		TokenHash: hash,
		ExpiresAt: pgtype.Timestamptz{Time: expiry, Valid: true},
	}); err != nil {
		return nil, fmt.Errorf("create session: %w", err)
	}

	return &v1.TokenPair{
		AccessToken:  accessToken,
		RefreshToken: raw,
		ExpiresAt:    expiry,
		UserID:       user.ID,
		Username:     user.Username,
		IsAdmin:      user.IsAdmin,
	}, nil
}
