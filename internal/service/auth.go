package service

import (
	"context"
	"errors"
	"fmt"
	"net/mail"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/parabeacon/server/internal/model"
	"github.com/parabeacon/server/internal/repository"
	"github.com/parabeacon/server/pkg/utils"
	"gorm.io/gorm"
)

var (
	ErrConflict            = errors.New("username or email already exists")
	ErrInvalidCredentials = errors.New("invalid email or password")
	ErrInvalidInput        = errors.New("invalid registration input")
	ErrNotFound            = errors.New("user not found")
	usernamePattern        = regexp.MustCompile(`^[a-zA-Z0-9_]{3,32}$`)
)

const MinPasswordBytes = 12

type AuthService struct {
	users  repository.UserRepository
	tokens *utils.TokenManager
}

type AuthResult struct {
	User      *model.User
	Token     string
	ExpiresAt time.Time
}

func NewAuthService(users repository.UserRepository, tokens *utils.TokenManager) *AuthService {
	return &AuthService{users: users, tokens: tokens}
}

func (s *AuthService) Register(ctx context.Context, username, email, password string) (*model.User, error) {
	username = strings.ToLower(strings.TrimSpace(username))
	email = strings.ToLower(strings.TrimSpace(email))
	if !usernamePattern.MatchString(username) {
		return nil, fmt.Errorf("%w: username must be 3-32 letters, digits, or underscores", ErrInvalidInput)
	}
	parsedEmail, err := mail.ParseAddress(email)
	if err != nil || parsedEmail.Address != email || len(email) > 254 {
		return nil, fmt.Errorf("%w: invalid email address", ErrInvalidInput)
	}
	if len(password) < MinPasswordBytes || len(password) > utils.MaxPasswordBytes {
		return nil, fmt.Errorf("%w: password length must be between %d and %d bytes", ErrInvalidInput, MinPasswordBytes, utils.MaxPasswordBytes)
	}

	hash, err := utils.HashPassword(password)
	if err != nil {
		return nil, err
	}
	user := &model.User{Username: username, Email: email, PasswordHash: hash}
	if err := s.users.Create(ctx, user); err != nil {
		if errors.Is(err, gorm.ErrDuplicatedKey) {
			return nil, ErrConflict
		}
		return nil, err
	}
	return user, nil
}

func (s *AuthService) Login(ctx context.Context, email, password string) (*AuthResult, error) {
	user, err := s.users.FindByEmail(ctx, strings.ToLower(strings.TrimSpace(email)))
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrInvalidCredentials
		}
		return nil, err
	}
	if err := utils.VerifyPassword(user.PasswordHash, password); err != nil {
		return nil, ErrInvalidCredentials
	}
	token, expiresAt, err := s.tokens.Issue(user.ID)
	if err != nil {
		return nil, err
	}
	return &AuthResult{User: user, Token: token, ExpiresAt: expiresAt}, nil
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
