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

func (r *memoryRefreshTokens) CreateSession(_ context.Context, token *model.RefreshToken) error {
	if token.ID == uuid.Nil {
		token.ID = uuid.New()
	}
	now := time.Now().UTC()
	for _, current := range r.byHash {
		if current.UserID == token.UserID && current.RevokedAt == nil {
			current.RevokedAt = &now
		}
	}
	r.byHash[token.TokenHash] = token
	return nil
}

func (r *memoryRefreshTokens) Rotate(_ context.Context, tokenHash string, now time.Time, replacement *model.RefreshToken) (uuid.UUID, error) {
	current, ok := r.byHash[tokenHash]
	if !ok || current.RevokedAt != nil || (current.ExpiresAt != nil && !current.ExpiresAt.After(now)) {
		return uuid.Nil, ErrInvalidRefresh
	}
	revokedAt := now
	current.RevokedAt = &revokedAt
	replacement.UserID = current.UserID
	if replacement.ID == uuid.Nil {
		replacement.ID = uuid.New()
	}
	r.byHash[replacement.TokenHash] = replacement
	return current.UserID, nil
}

func (r *memoryRefreshTokens) Revoke(_ context.Context, tokenHash string, now time.Time) error {
	if current, ok := r.byHash[tokenHash]; ok && current.RevokedAt == nil {
		current.RevokedAt = &now
	}
	return nil
}

func (r *memoryRefreshTokens) IsActiveSession(_ context.Context, sessionID, userID uuid.UUID, now time.Time) (bool, error) {
	for _, token := range r.byHash {
		if token.ID == sessionID && token.UserID == userID && token.RevokedAt == nil &&
			(token.ExpiresAt == nil || token.ExpiresAt.After(now)) {
			return true, nil
		}
	}
	return false, nil
}

func TestAuthServiceRotatesPersistentRefreshTokens(t *testing.T) {
	ctx := context.Background()
	users := newMemoryUsers()
	refreshTokens := newMemoryRefreshTokens()
	tokens := utils.NewTokenManager("0123456789abcdef0123456789abcdef", "test-issuer", time.Minute)
	auth := NewAuthService(users, tokens, refreshTokens)
	user, err := auth.Register(ctx, "refresh_pilot", "", "unique-passphrase")
	if err != nil {
		t.Fatalf("Register() error = %v", err)
	}

	login, err := auth.Login(ctx, "refresh_pilot", "unique-passphrase")
	if err != nil {
		t.Fatalf("Login() error = %v", err)
	}
	if login.RefreshToken == "" || login.RefreshExpiresAt != nil {
		t.Fatal("login did not issue a persistent refresh token without expiry")
	}
	loginClaims, err := tokens.Parse(login.Token)
	if err != nil || loginClaims.Subject != user.ID.String() || loginClaims.SessionID == "" {
		t.Fatalf("login access-token claims = %#v, error = %v", loginClaims, err)
	}
	loginSessionID, err := uuid.Parse(loginClaims.SessionID)
	if err != nil {
		t.Fatalf("parse login session ID: %v", err)
	}
	if active, err := refreshTokens.IsActiveSession(ctx, loginSessionID, user.ID, time.Now().UTC()); err != nil || !active {
		t.Fatalf("login session active = %v, error = %v", active, err)
	}

	rotated, err := auth.Refresh(ctx, login.RefreshToken)
	if err != nil {
		t.Fatalf("Refresh() error = %v", err)
	}
	if rotated.RefreshExpiresAt != nil || rotated.RefreshToken == "" || rotated.RefreshToken == login.RefreshToken {
		t.Fatal("refresh did not rotate the persistent refresh token")
	}
	rotatedClaims, err := tokens.Parse(rotated.Token)
	if err != nil || rotatedClaims.Subject != user.ID.String() || rotatedClaims.SessionID == "" {
		t.Fatalf("rotated access-token claims = %#v, error = %v", rotatedClaims, err)
	}
	rotatedSessionID, err := uuid.Parse(rotatedClaims.SessionID)
	if err != nil {
		t.Fatalf("parse rotated session ID: %v", err)
	}
	if active, err := refreshTokens.IsActiveSession(ctx, loginSessionID, user.ID, time.Now().UTC()); err != nil || active {
		t.Fatalf("previous session active = %v, error = %v; want inactive", active, err)
	}
	if active, err := refreshTokens.IsActiveSession(ctx, rotatedSessionID, user.ID, time.Now().UTC()); err != nil || !active {
		t.Fatalf("rotated session active = %v, error = %v", active, err)
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

func TestSecondLoginInvalidatesPreviousSession(t *testing.T) {
	ctx := context.Background()
	users := newMemoryUsers()
	refreshTokens := newMemoryRefreshTokens()
	tokens := utils.NewTokenManager("0123456789abcdef0123456789abcdef", "test-issuer", time.Minute)
	auth := NewAuthService(users, tokens, refreshTokens)
	user, err := auth.Register(ctx, "single_pilot", "", "unique-passphrase")
	if err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	first, err := auth.Login(ctx, "single_pilot", "unique-passphrase")
	if err != nil {
		t.Fatalf("first Login() error = %v", err)
	}
	firstClaims, err := tokens.Parse(first.Token)
	if err != nil {
		t.Fatalf("parse first access token: %v", err)
	}
	firstSessionID, err := uuid.Parse(firstClaims.SessionID)
	if err != nil {
		t.Fatalf("parse first session ID: %v", err)
	}

	second, err := auth.Login(ctx, "single_pilot", "unique-passphrase")
	if err != nil {
		t.Fatalf("second Login() error = %v", err)
	}
	secondClaims, err := tokens.Parse(second.Token)
	if err != nil {
		t.Fatalf("parse second access token: %v", err)
	}
	secondSessionID, err := uuid.Parse(secondClaims.SessionID)
	if err != nil {
		t.Fatalf("parse second session ID: %v", err)
	}
	if active, err := refreshTokens.IsActiveSession(ctx, firstSessionID, user.ID, time.Now().UTC()); err != nil || active {
		t.Fatalf("first session active after second login = %v, error = %v; want inactive", active, err)
	}
	if active, err := refreshTokens.IsActiveSession(ctx, secondSessionID, user.ID, time.Now().UTC()); err != nil || !active {
		t.Fatalf("second session active = %v, error = %v", active, err)
	}
	if _, err := auth.Refresh(ctx, first.RefreshToken); !errors.Is(err, ErrInvalidRefresh) {
		t.Fatalf("refresh from previous client error = %v, want ErrInvalidRefresh", err)
	}
}

func TestAuthServiceRejectsExpiredRefreshToken(t *testing.T) {
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
	expiredAt := time.Now().UTC().Add(-time.Second)
	storedToken.ExpiresAt = &expiredAt
	if _, err := auth.Refresh(ctx, login.RefreshToken); !errors.Is(err, ErrInvalidRefresh) {
		t.Fatalf("Refresh() after expiry error = %v, want ErrInvalidRefresh", err)
	}
}
