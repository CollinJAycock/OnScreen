package main

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"

	v1 "github.com/onscreen/onscreen/internal/api/v1"
	"github.com/onscreen/onscreen/internal/db/gen"
	"github.com/onscreen/onscreen/internal/valkey"
)

// pairStore adapts *valkey.Client to v1.PairStore, translating the redis
// "key not found" sentinel into v1.ErrPairNotFound.
type pairStore struct{ v *valkey.Client }

func (s *pairStore) Set(ctx context.Context, key, value string, ttl time.Duration) error {
	return s.v.Set(ctx, key, value, ttl)
}

func (s *pairStore) Get(ctx context.Context, key string) (string, error) {
	out, err := s.v.Get(ctx, key)
	if errors.Is(err, valkey.ErrNotFound) {
		return "", v1.ErrPairNotFound
	}
	return out, err
}

func (s *pairStore) Del(ctx context.Context, keys ...string) error {
	return s.v.Del(ctx, keys...)
}

// pairTokenIssuer wraps authService so the pair handler can mint tokens for a
// given user id without depending on the full auth surface.
func pairTokenIssuer(svc *authService, q authQuerier) v1.PairTokenIssuer {
	return func(ctx context.Context, userID uuid.UUID) (*v1.TokenPair, error) {
		var user gen.User
		user, err := q.GetUser(ctx, userID)
		if err != nil {
			return nil, err
		}
		return svc.issueTokenPair(ctx, user)
	}
}
