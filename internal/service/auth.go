package service

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net/mail"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/parabeacon/server/internal/model"
	"github.com/parabeacon/server/internal/repository"
	"github.com/parabeacon/server/pkg/utils"
	"gorm.io/gorm"
)

var (
	ErrConflict           = errors.New("username or email already exists")
	ErrInvalidCredentials = errors.New("invalid email or password")
	ErrInvalidInput       = errors.New("invalid registration input")
	ErrNotFound           = errors.New("user not found")
	ErrInvalidRefresh     = repository.ErrInvalidRefreshToken
	usernamePattern       = regexp.MustCompile(`^[a-zA-Z0-9_]{3,32}$`)
)

const MinPasswordCharacters = 6

type AuthService struct {
	users         repository.UserRepository
	tokens        *utils.TokenManager
	refreshTokens repository.RefreshTokenRepository
}

type AuthResult struct {
	User             *model.User
	Token            string
	ExpiresAt        time.Time
	RefreshToken     string
	RefreshExpiresAt *time.Time
}

func NewAuthService(users repository.UserRepository, tokens *utils.TokenManager, refreshTokens ...repository.RefreshTokenRepository) *AuthService {
	service := &AuthService{users: users, tokens: tokens}
	if len(refreshTokens) > 0 {
		service.refreshTokens = refreshTokens[0]
	}
	return service
}

func (s *AuthService) Register(ctx context.Context, username, email, password string) (*model.User, error) {
	username = strings.ToLower(strings.TrimSpace(username))
	email = strings.ToLower(strings.TrimSpace(email))
	if !usernamePattern.MatchString(username) {
		return nil, fmt.Errorf("%w: username must be 3-32 letters, digits, or underscores", ErrInvalidInput)
	}
	if email != "" {
		parsedEmail, err := mail.ParseAddress(email)
		if err != nil || parsedEmail.Address != email || len(email) > 254 {
			return nil, fmt.Errorf("%w: invalid email address", ErrInvalidInput)
		}
	}
	if utf8.RuneCountInString(password) < MinPasswordCharacters || len(password) > utils.MaxPasswordBytes {
		return nil, fmt.Errorf("%w: password must contain at least %d characters and no more than %d bytes", ErrInvalidInput, MinPasswordCharacters, utils.MaxPasswordBytes)
	}

	hash, err := utils.HashPassword(password)
	if err != nil {
		return nil, err
	}
	user := &model.User{Username: username, PasswordHash: hash}
	if email != "" {
		user.Email = &email
	}
	if err := s.users.Create(ctx, user); err != nil {
		if errors.Is(err, gorm.ErrDuplicatedKey) {
			return nil, ErrConflict
		}
		return nil, err
	}
	return user, nil
}

func (s *AuthService) Login(ctx context.Context, identifier, password string) (*AuthResult, error) {
	identifier = strings.ToLower(strings.TrimSpace(identifier))
	user, err := s.users.FindByLogin(ctx, identifier)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrInvalidCredentials
		}
		return nil, err
	}
	if err := utils.VerifyPassword(user.PasswordHash, password); err != nil {
		return nil, ErrInvalidCredentials
	}
	if s.refreshTokens == nil {
		token, expiresAt, err := s.tokens.Issue(user.ID)
		if err != nil {
			return nil, err
		}
		return &AuthResult{User: user, Token: token, ExpiresAt: expiresAt}, nil
	}

	refreshToken, tokenHash, err := createRefreshToken()
	if err != nil {
		return nil, err
	}
	sessionID := uuid.New()
	token, expiresAt, err := s.tokens.Issue(user.ID, sessionID)
	if err != nil {
		return nil, err
	}
	if err := s.refreshTokens.CreateSession(ctx, &model.RefreshToken{
		ID:        sessionID,
		UserID:    user.ID,
		TokenHash: tokenHash,
	}); err != nil {
		return nil, err
	}
	return &AuthResult{
		User:         user,
		Token:        token,
		ExpiresAt:    expiresAt,
		RefreshToken: refreshToken,
	}, nil
}

func (s *AuthService) Refresh(ctx context.Context, rawToken string) (*AuthResult, error) {
	if s.refreshTokens == nil || len(rawToken) < 32 || len(rawToken) > 128 {
		return nil, ErrInvalidRefresh
	}
	newToken, newHash, err := createRefreshToken()
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	replacement := &model.RefreshToken{
		ID:        uuid.New(),
		TokenHash: newHash,
	}
	userID, err := s.refreshTokens.Rotate(ctx, hashRefreshToken(rawToken), now, replacement)
	if err != nil {
		if errors.Is(err, repository.ErrInvalidRefreshToken) {
			return nil, ErrInvalidRefresh
		}
		return nil, err
	}
	accessToken, expiresAt, err := s.tokens.Issue(userID, replacement.ID)
	if err != nil {
		return nil, err
	}
	return &AuthResult{
		Token:        accessToken,
		ExpiresAt:    expiresAt,
		RefreshToken: newToken,
	}, nil
}

func createRefreshToken() (string, string, error) {
	value := make([]byte, 32)
	if _, err := rand.Read(value); err != nil {
		return "", "", fmt.Errorf("generate refresh token: %w", err)
	}
	raw := base64.RawURLEncoding.EncodeToString(value)
	return raw, hashRefreshToken(raw), nil
}

func hashRefreshToken(raw string) string {
	digest := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(digest[:])
}

func (s *AuthService) Logout(ctx context.Context, rawRefreshToken string) error {
	if s.refreshTokens == nil || len(rawRefreshToken) < 32 || len(rawRefreshToken) > 128 {
		return nil
	}
	return s.refreshTokens.Revoke(ctx, hashRefreshToken(rawRefreshToken), time.Now().UTC())
}

func (s *AuthService) Profile(ctx context.Context, userID uuid.UUID) (*model.User, error) {
	user, err := s.users.FindByID(ctx, userID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return user, nil
}
