package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/parabeacon/server/internal/model"
	"github.com/parabeacon/server/pkg/utils"
)

type memoryRefreshTokens struct {
	byHash map[string]*model.RefreshToken
}

func newMemoryRefreshTokens() *memoryRefreshTokens {
	return &memoryRefreshTokens{byHash: make(map[string]*model.RefreshToken)}
}

func (r *memoryRefreshTokens) Create(_ context.Context, token *model.RefreshToken) error {
	r.byHash[token.TokenHash] = token
	return nil
}

func (r *memoryRefreshTokens) Rotate(_ context.Context, tokenHash string, now time.Time, replacement *model.RefreshToken) (uuid.UUID, error) {
	current, ok := r.byHash[tokenHash]
	if !ok || current.RevokedAt != nil || !current.ExpiresAt.After(now) {
		return uuid.Nil, ErrInvalidRefresh
	}
	revokedAt := now
	current.RevokedAt = &revokedAt
	replacement.UserID = current.UserID
	r.byHash[replacement.TokenHash] = replacement
	return current.UserID, nil
}

func (r *memoryRefreshTokens) Revoke(_ context.Context, tokenHash string, now time.Time) error {
	if current, ok := r.byHash[tokenHash]; ok && current.RevokedAt == nil {
		current.RevokedAt = &now
	}
	return nil
}

func TestAuthServiceRotatesRefreshTokens(t *testing.T) {
	if got, want := RefreshTokenLifetime, 60*24*time.Hour; got != want {
		t.Fatalf("RefreshTokenLifetime = %v, want %v", got, want)
	}
	ctx := context.Background()
	users := newMemoryUsers()
	refreshTokens := newMemoryRefreshTokens()
	tokens := utils.NewTokenManager("0123456789abcdef0123456789abcdef", "test-issuer", time.Minute)
	auth := NewAuthService(users, tokens, refreshTokens)
	user, err := auth.Register(ctx, "refresh_pilot", "", "unique-passphrase")
	if err != nil {
		t.Fatalf("Register() error = %v", err)
	}

	loginStartedAt := time.Now().UTC()
	login, err := auth.Login(ctx, "refresh_pilot", "unique-passphrase")
	if err != nil {
		t.Fatalf("Login() error = %v", err)
	}
	loginFinishedAt := time.Now().UTC()
	if login.RefreshToken == "" || !login.RefreshExpiresAt.After(loginFinishedAt) {
		t.Fatal("login did not issue a valid refresh token")
	}
	assertRefreshExpiry(t, login.RefreshExpiresAt, loginStartedAt, loginFinishedAt)

	refreshStartedAt := time.Now().UTC()
	rotated, err := auth.Refresh(ctx, login.RefreshToken)
	if err != nil {
		t.Fatalf("Refresh() error = %v", err)
	}
	refreshFinishedAt := time.Now().UTC()
	assertRefreshExpiry(t, rotated.RefreshExpiresAt, refreshStartedAt, refreshFinishedAt)
	if rotated.RefreshToken == "" || rotated.RefreshToken == login.RefreshToken {
		t.Fatal("refresh did not rotate the opaque token")
	}
	claims, err := tokens.Parse(rotated.Token)
	if err != nil || claims.Subject != user.ID.String() {
		t.Fatalf("rotated access token subject = %v, error = %v", claims, err)
	}
	if _, err := auth.Refresh(ctx, login.RefreshToken); !errors.Is(err, ErrInvalidRefresh) {
		t.Fatalf("replayed refresh token error = %v, want ErrInvalidRefresh", err)
	}
	if err := auth.Logout(ctx, rotated.RefreshToken); err != nil {
		t.Fatalf("Logout() error = %v", err)
	}
	if _, err := auth.Refresh(ctx, rotated.RefreshToken); !errors.Is(err, ErrInvalidRefresh) {
		t.Fatalf("refresh after logout error = %v, want ErrInvalidRefresh", err)
	}
}

func TestAuthServiceRejectsRefreshTokenAfterIdleExpiry(t *testing.T) {
	ctx := context.Background()
	users := newMemoryUsers()
	refreshTokens := newMemoryRefreshTokens()
	tokens := utils.NewTokenManager("0123456789abcdef0123456789abcdef", "test-issuer", time.Minute)
	auth := NewAuthService(users, tokens, refreshTokens)
	if _, err := auth.Register(ctx, "idle_pilot", "", "unique-passphrase"); err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	login, err := auth.Login(ctx, "idle_pilot", "unique-passphrase")
	if err != nil {
		t.Fatalf("Login() error = %v", err)
	}

	storedToken := refreshTokens.byHash[hashRefreshToken(login.RefreshToken)]
	storedToken.ExpiresAt = time.Now().UTC().Add(-time.Second)
	if _, err := auth.Refresh(ctx, login.RefreshToken); !errors.Is(err, ErrInvalidRefresh) {
		t.Fatalf("Refresh() after idle expiry error = %v, want ErrInvalidRefresh", err)
	}
}

func assertRefreshExpiry(t *testing.T, expiresAt, startedAt, finishedAt time.Time) {
	t.Helper()
	earliest := startedAt.Add(RefreshTokenLifetime)
	latest := finishedAt.Add(RefreshTokenLifetime)
	if expiresAt.Before(earliest) || expiresAt.After(latest) {
		t.Fatalf("refresh expiry = %v, want between %v and %v", expiresAt, earliest, latest)
	}
}
